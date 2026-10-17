//go:build linux

package softmedia

import (
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/14752222/Gox/gfx"
)

// errStopped 是"窗口已关, 不再接受挂载" (Attach 可能和关闭竞态)。
var errStopped = errors.New("softmedia: 窗口已关闭")

// resolvePath 把 src 解析成文件路径。
//
// 与 gfx 的 resolveAssetPath 同规则 (相对路径按**进程工作目录**解释 ——
// testdata/video_demo.js 的文件头就是这么跟用户承诺的), 软媒体自己带一份:
// 反向依赖内核私有函数不可能, 而两条规则必须一致, 否则同一个相对路径
// 在封面 (gfx 解) 与视频帧 (softmedia 解) 上会指向不同文件。
func resolvePath(src string) string {
	if src == "" {
		return ""
	}
	if filepath.IsAbs(src) {
		return src
	}
	wd, err := os.Getwd()
	if err != nil || wd == "" {
		return filepath.Clean(src)
	}
	return filepath.Join(wd, src)
}

// Wrap 是 softmedia 的呈现包装器 (经 gfx.SetPresentWrapper 安装)。
//
// 它做三件事:
//  1. 替换「像素怎么上屏」: ShowRegions 时把各视频播放器的当前帧合成进
//     *image.RGBA, 再把那些矩形并进脏区一起 blit;
//  2. 实现 nativeVideoHost / nativeAudioHost 两个可选能力 —— 内核的
//     videoHostOf / audioHostOf 对包装后的 Surface 断言命中, `<video>` /
//     `<audio>` 于是走"真的播"而不是降级;
//  3. 盯窗口关闭: 用户点 X (EventClose) 与 X 连接猝死 (WaitEvents 返回
//     false) 两条路都汇入内核的 a.close(), 它经 windowCloser 可选接口
//     叫到 WindowClosed —— 不停机的话, 播放器时钟 goroutine 会跟着
//     gfx.Post 的队列一起无限增长 ( Pump 已停, 没人 Drain), 直到进程退出。
//
// 返回 ok=false 表示"这次不包装" (inner 为 nil), 调用方继续用 inner。
func Wrap(inner gfx.Surface) (gfx.Surface, bool) {
	if inner == nil {
		return nil, false
	}
	return newMediaSurface(inner), true
}

// mediaSurface 是 softmedia 的 Surface 包装器。
//
// 内嵌 gfx.Surface: Show / Size / WaitEvents / Events 全部转发 inner
// (事件、尺寸、窗口操作归平台后端, softmedia 只管媒体)。WaitEvents 被
// 显式重写 (盯关闭), ShowRegions 被显式重写 (合成视频帧)。
type mediaSurface struct {
	gfx.Surface // inner

	mu     sync.Mutex
	videos map[uint64]*videoPlayer
	audios map[uint64]*audioPlayer

	// rejected 记住"这个 src 是永久不支持的格式" (.mp4 / .mp3 之类)。
	// 不缓存的话, 内核每帧重试都会再解一次码再失败一次 —— Attach 失败
	// 时 st.id 保持 0, flush 下一帧还会来。只缓存**格式**判定 (不缓存
	// IO 错误: 文件被替换/权限变化后该能恢复)。
	rejected map[string]error

	stopped bool
}

func newMediaSurface(inner gfx.Surface) *mediaSurface {
	return &mediaSurface{
		Surface:  inner,
		videos:   map[uint64]*videoPlayer{},
		audios:   map[uint64]*audioPlayer{},
		rejected: map[string]error{},
	}
}

// WindowClosed 实现 gfx 的 windowCloser 可选接口: 窗口关闭时停机全部
// 播放器 (正常关窗的主路径, 见文件头第 3 条)。幂等。
func (m *mediaSurface) WindowClosed() { m.stopAll() }

// WaitEvents 转发 inner, 但盯住"窗口没了"这一刻: 停机全部播放器。
//
// 这是**第二保险**: 宿主猝死 (X 连接断) 时 WaitEvents 直接返回 false,
// 内核的 a.close() 也会随后到来 (WindowClosed), 但那条路要等 Pump 下一轮
// —— 提前停没有副作用 (stopAll 幂等), 好处是时钟 goroutine 尽早退出。
func (m *mediaSurface) WaitEvents(maxWait time.Duration) bool {
	ok := m.Surface.WaitEvents(maxWait)
	if !ok {
		m.stopAll()
	}
	return ok
}

// WaitEventsWake 实现 gfx 的 waker 可选接口 (与 WaitEvents 同义, 但把跨线程
// Post 的唤醒信号并进等待, 见 gfx.go 的 wakeCh)。
//
// 包装器**必须**把这个能力转发出来: gfx 的 waitEvents 是对最外层 Surface 断
// 言的, 而 mediaSurface 内嵌的是 gfx.Surface 接口 —— inner 的可选方法不会
// 被提升, 不转发的话每个 Linux 应用空闲期都会从"唤醒即醒"退化成有界轮询
// (白烧 CPU)。waker 在 gfx 里未导出, 跨包断言不了那个接口类型; 但 Go 的类
// 型断言是结构化的, 照抄一份方法集即可命中 inner 的动态类型。
func (m *mediaSurface) WaitEventsWake(maxWait time.Duration, wake <-chan struct{}) bool {
	w, ok := m.Surface.(interface {
		WaitEventsWake(maxWait time.Duration, wake <-chan struct{}) bool
	})
	if !ok {
		// inner 没有 waker (如 GPU 面): 走普通等待, 关闭检测照旧
		return m.WaitEvents(maxWait)
	}
	alive := w.WaitEventsWake(maxWait, wake)
	if !alive {
		m.stopAll()
	}
	return alive
}

// stopAll 停掉全部播放器 (窗口关闭 / 测试拆卸)。幂等。
func (m *mediaSurface) stopAll() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	videos := make([]*videoPlayer, 0, len(m.videos))
	for _, p := range m.videos {
		videos = append(videos, p)
	}
	audios := make([]*audioPlayer, 0, len(m.audios))
	for _, p := range m.audios {
		audios = append(audios, p)
	}
	m.videos = map[uint64]*videoPlayer{}
	m.audios = map[uint64]*audioPlayer{}
	m.mu.Unlock()
	for _, p := range videos {
		p.stop()
	}
	for _, p := range audios {
		p.stop()
	}
}

// ===== 呈现: 合成视频帧 =====

// ShowRegions 把各视频播放器的当前帧合成进 img, 再转交 inner。
//
// rects 为 nil 表示整帧 (首帧/resize) —— 那种情况**不并**视频矩形:
// 整帧 blit 已经覆盖了它们, 并进去反而把 nil 变成非 nil, 上屏路径从
// "整帧"降级成"局部", 白付一次区域计算。
func (m *mediaSurface) ShowRegions(img *image.RGBA, rects []image.Rectangle) {
	if len(m.videos) > 0 {
		m.mu.Lock()
		var extra []image.Rectangle
		for _, p := range m.videos {
			if r, ok := p.compositeInto(img); ok {
				extra = append(extra, image.Rect(r.X, r.Y, r.X+r.W, r.Y+r.H))
			}
		}
		m.mu.Unlock()
		if rects != nil && len(extra) > 0 {
			rects = append(rects, extra...)
		}
	}
	m.Surface.ShowRegions(img, rects)
}

// Show 整帧上屏 (视频帧同样合成进去)。
func (m *mediaSurface) Show(img *image.RGBA) {
	if len(m.videos) > 0 {
		m.mu.Lock()
		for _, p := range m.videos {
			p.compositeInto(img)
		}
		m.mu.Unlock()
	}
	m.Surface.Show(img)
}

// ===== 可选能力: nativeVideoHost =====

// VideoInlineSupported 恒真: softmedia 在, 视频内联播放就在 (能解算能解,
// 不能解算的格式走 Attach 失败 → 内核的 attach-failed 降级)。
func (m *mediaSurface) VideoInlineSupported() bool { return true }

// VideoSurfaceAttach 挂一块视频表面。id 由内核分配 (进程内唯一), 这里只是
// 拿它当不透明键存进 map —— 不再自己从 1 编号 (两个窗口会撞车, 见内核侧
// nativeVideoHost 的注释)。
func (m *mediaSurface) VideoSurfaceAttach(id uint64, r gfx.Rect, src string, opts gfx.VideoSurfaceOptions) error {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return errStopped
	}
	if err, bad := m.rejected[src]; bad {
		m.mu.Unlock()
		return err
	}
	if _, dup := m.videos[id]; dup {
		m.mu.Unlock()
		return fmt.Errorf("softmedia: 视频句柄 %d 重复挂载", id)
	}
	m.mu.Unlock()

	frames, delays, err := decodeVideo(resolvePath(src))
	if err != nil {
		if isFormatError(err) {
			// 永久性格式问题: 记下来, 别让内核每帧重试都再解一次
			m.mu.Lock()
			m.rejected[src] = err
			m.mu.Unlock()
		}
		return err
	}

	p := newVideoPlayer(r, src, opts, frames, delays)

	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return errStopped
	}
	p.id = id // 事件回填要用 (start 之前定好, 之后只读)
	m.videos[id] = p
	m.mu.Unlock()

	p.start()
	return nil
}

func (m *mediaSurface) VideoSurfaceRect(id uint64, r gfx.Rect) error {
	m.mu.Lock()
	p := m.videos[id]
	m.mu.Unlock()
	if p == nil {
		return nil // 已摘: 静默 (内核的 release 与这里的竞态是正常形状)
	}
	p.setRect(r)
	return nil
}

func (m *mediaSurface) VideoSurfaceCommand(id uint64, cmd gfx.VideoCommand, value float64) error {
	m.mu.Lock()
	p := m.videos[id]
	m.mu.Unlock()
	if p == nil {
		return nil
	}
	p.command(cmd, value)
	return nil
}

func (m *mediaSurface) VideoSurfaceDetach(id uint64) error {
	m.mu.Lock()
	p := m.videos[id]
	delete(m.videos, id)
	m.mu.Unlock()
	if p != nil {
		p.stop()
	}
	return nil
}

// ===== 可选能力: nativeAudioHost =====

// AudioInlineSupported 恒真 (理由同 VideoInlineSupported)。
func (m *mediaSurface) AudioInlineSupported() bool { return true }

// AudioAttach 挂一个音频源 (id 由内核分配, 理由同 VideoSurfaceAttach)。
func (m *mediaSurface) AudioAttach(id uint64, src string, opts gfx.AudioPlayOptions) error {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return errStopped
	}
	if err, bad := m.rejected[src]; bad {
		m.mu.Unlock()
		return err
	}
	if _, dup := m.audios[id]; dup {
		m.mu.Unlock()
		return fmt.Errorf("softmedia: 音频句柄 %d 重复挂载", id)
	}
	m.mu.Unlock()

	clip, err := decodeWAV(resolvePath(src))
	if err != nil {
		if isFormatError(err) {
			m.mu.Lock()
			m.rejected[src] = err
			m.mu.Unlock()
		}
		return err
	}

	p := newAudioPlayer(src, opts, clip)

	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return errStopped
	}
	p.id = id
	m.audios[id] = p
	m.mu.Unlock()

	p.start()
	return nil
}

func (m *mediaSurface) AudioCommand(id uint64, cmd gfx.AudioCommand, value float64) error {
	m.mu.Lock()
	p := m.audios[id]
	m.mu.Unlock()
	if p == nil {
		return nil
	}
	p.command(cmd, value)
	return nil
}

func (m *mediaSurface) AudioDetach(id uint64) error {
	m.mu.Lock()
	p := m.audios[id]
	delete(m.audios, id)
	m.mu.Unlock()
	if p != nil {
		p.stop()
	}
	return nil
}
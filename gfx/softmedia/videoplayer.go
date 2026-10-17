//go:build linux

package softmedia

import (
	"image"
	"sync"
	"time"

	"github.com/14752222/Gox/gfx"
)

// videoPlayer 是一个 `<video>` 节点的播放器: 解码好的全帧序列 + 一条时钟
// goroutine。
//
// 时钟与上屏的分工 (整个软媒体后端的核心形状):
//   - 时钟 goroutine 只做"到点换帧 + gfx.InvalidateRect(盒子)";
//   - 帧合成发生在渲染线程的 ShowRegions 里 (mediaSurface.ShowRegions);
//   - 事件 (ready/play/pause/ended/timeupdate) 经 gfx.Post 投回 GUI 线程
//     再 ResolveVideoEvent —— 时钟 goroutine 永远不碰 VM。
type videoPlayer struct {
	id     uint64
	src    string
	frames []*image.RGBA
	delays []time.Duration // 每帧停留时长 (与 frames 等长)
	dur    time.Duration   // 一轮总时长

	mu      sync.Mutex
	rect    gfx.Rect
	fit     string
	cur     int
	playing bool
	loop    bool
	muted   bool
	volume  float64
	seekSec float64 // >= 0 = 有待处理的跳转
	stopped bool

	wake  chan struct{} // 命令/seek 的唤醒 (buffered 1)
	stopC chan struct{} // 停机
	once  sync.Once

	lastEvt time.Time // 上次 timeupdate 的时刻 (节流用)
}

func newVideoPlayer(r gfx.Rect, src string, opts gfx.VideoSurfaceOptions, frames []*image.RGBA, delays []time.Duration) *videoPlayer {
	var dur time.Duration
	for _, d := range delays {
		dur += d
	}
	return &videoPlayer{
		id:     0, // 由 mediaSurface 在登记后回填 (事件要用)
		src:    src,
		frames: frames,
		delays: delays,
		dur:    dur,
		rect:   r,
		fit:    opts.Fit,
		loop:   opts.Loop,
		muted:  opts.Muted,
		volume: opts.Volume,
		seekSec: -1,
		wake:   make(chan struct{}, 1),
		stopC:  make(chan struct{}),
	}
}

func (p *videoPlayer) start() {
	go p.run()
}

func (p *videoPlayer) stop() {
	p.once.Do(func() {
		p.mu.Lock()
		p.stopped = true
		p.mu.Unlock()
		close(p.stopC)
	})
}

// setRect 跟随布局移动/缩放。旧盒子也标一次脏: 那块区域的旧帧要擦掉。
func (p *videoPlayer) setRect(r gfx.Rect) {
	p.mu.Lock()
	old := p.rect
	p.rect = r
	p.mu.Unlock()
	if old != r {
		gfx.InvalidateRect(old)
		gfx.InvalidateRect(r)
	}
}

func (p *videoPlayer) command(cmd gfx.VideoCommand, value float64) {
	switch cmd {
	case gfx.VideoCmdPlay:
		p.mu.Lock()
		p.playing = true
		p.mu.Unlock()
		p.post(gfx.VideoEvent{Kind: "play", CurrentTime: p.currentTime(), Duration: p.duration()})
		p.kick()
	case gfx.VideoCmdPause:
		p.mu.Lock()
		p.playing = false
		p.mu.Unlock()
		p.post(gfx.VideoEvent{Kind: "pause", CurrentTime: p.currentTime(), Duration: p.duration()})
		p.kick()
	case gfx.VideoCmdSeek:
		p.mu.Lock()
		p.seekSec = value
		box := p.rect
		p.mu.Unlock()
		// 跳转 (哪怕暂停中) 也要让新帧画出来: 标脏那一块
		gfx.InvalidateRect(box)
		p.kick()
	case gfx.VideoCmdVolume:
		p.mu.Lock()
		p.volume = value
		p.mu.Unlock()
	case gfx.VideoCmdMute:
		p.mu.Lock()
		p.muted = value > 0.5
		p.mu.Unlock()
	case gfx.VideoCmdLoop:
		p.mu.Lock()
		p.loop = value > 0.5
		p.mu.Unlock()
	}
}

// kick 唤醒时钟 (非阻塞: 缓冲 1, 满说明上一轮还没消费, 不缺这一次)。
func (p *videoPlayer) kick() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// run 是时钟 goroutine 的主循环。
func (p *videoPlayer) run() {
	p.post(gfx.VideoEvent{Kind: "ready", Duration: p.duration()})
	for {
		p.mu.Lock()
		if p.stopped {
			p.mu.Unlock()
			return
		}
		// seek 在播放判断**之前**应用: 暂停中跳转也必须生效 (脚本的典型
		// 用法是"先 seek 到某处, 再 play")。
		if p.seekSec >= 0 {
			p.applySeekLocked()
		}
		if !p.playing {
			p.mu.Unlock()
			// 暂停: 睡到命令或停机
			select {
			case <-p.wake:
			case <-p.stopC:
				return
			}
			continue
		}
		delay := p.delays[p.cur]
		p.mu.Unlock()

		select {
		case <-time.After(delay):
		case <-p.wake:
			continue // 命令到了: 回锁重新评估 (可能暂停/跳转)
		case <-p.stopC:
			return
		}

		p.mu.Lock()
		if p.stopped {
			p.mu.Unlock()
			return
		}
		p.cur++
		ended := false
		if p.cur >= len(p.frames) {
			if p.loop {
				p.cur = 0
			} else {
				p.cur = len(p.frames) - 1
				p.playing = false
				ended = true
			}
		}
		box := p.rect
		p.mu.Unlock()

		if ended {
			// 停在最后一帧, 派发 ended (经 Post 回 GUI 线程)
			p.post(gfx.VideoEvent{Kind: "ended", CurrentTime: p.duration(), Duration: p.duration()})
			continue
		}
		gfx.InvalidateRect(box)
		p.maybeTimeUpdate()
	}
}

// applySeekLocked 应用跳转 (调用方持 mu)。
//
// 这里**只能**读裸字段 (p.dur), 不能调 p.duration() —— 后者自己要拿
// p.mu, 而调用方 (run) 已经持着, 非可重入的 sync.Mutex 会当场自死锁。
func (p *videoPlayer) applySeekLocked() {
	target := p.seekSec
	p.seekSec = -1
	if target < 0 {
		target = 0
	}
	dur := p.dur.Seconds()
	var acc time.Duration
	for i, d := range p.delays {
		if time.Duration(target*float64(time.Second)) < acc+d {
			p.cur = i
			p.post(gfx.VideoEvent{Kind: "timeupdate", CurrentTime: target, Duration: dur})
			return
		}
		acc += d
	}
	p.cur = len(p.frames) - 1
	p.post(gfx.VideoEvent{Kind: "timeupdate", CurrentTime: dur, Duration: dur})
}

// maybeTimeUpdate 节流派发 timeupdate (浏览器同款 ~4/s: 每帧都派等于把
// 脚本回调按帧率call, 30fps 的视频就是每秒 30 次 JS 调用)。
func (p *videoPlayer) maybeTimeUpdate() {
	now := time.Now()
	p.mu.Lock()
	if now.Sub(p.lastEvt) < 250*time.Millisecond {
		p.mu.Unlock()
		return
	}
	p.lastEvt = now
	cur, dur := p.currentTimeLocked(), p.dur
	p.mu.Unlock()
	p.post(gfx.VideoEvent{Kind: "timeupdate", CurrentTime: cur, Duration: dur.Seconds()})
}

func (p *videoPlayer) currentTime() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.currentTimeLocked()
}

func (p *videoPlayer) currentTimeLocked() float64 {
	var acc time.Duration
	for i := 0; i < p.cur && i < len(p.delays); i++ {
		acc += p.delays[i]
	}
	return acc.Seconds()
}

func (p *videoPlayer) duration() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dur.Seconds()
}

// post 经 GUI 线程回填事件 (时钟 goroutine 与 ShowRegions 都走这里;
// 直接调 ResolveVideoEvent 会在时钟线程碰 VM, 那是别的线程的)。
// post 把事件投回 GUI 线程。
//
// 自检放在**任务执行时**而不是入队时, 原因是一场事故: gfx.Post 的队列是
// 全局广播的, 任务不带目标窗口 (见 gfx.Post 的注释), 而 Pump 在**没有窗口
// 时直接返回 false、不 drain**。于是关窗那一刻队列里压着的最后几个
// timeupdate 会原封不动留到下一个窗口的泵里才被执行 —— 那时旧窗口的脚本
// 早已销毁, 回调打过去只会拿到 undefined (实测表现为下一个脚本里冒出
// "Cannot read properties of undefined (reading 'push')")。所以闭包里先问
// 一句"我还活着吗", 停了就丢弃: 已关闭窗口的迟到事件没有任何接收方。
func (p *videoPlayer) post(ev gfx.VideoEvent) {
	id := p.id
	gfx.Post(func() {
		if !p.alive() {
			return
		}
		gfx.ResolveVideoEvent(id, ev)
	})
}

// alive 报告播放器还没停 (停了 = 关窗/卸载, 事件不该再往外发)。
func (p *videoPlayer) alive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.stopped
}

// compositeInto 把当前帧按 fit 模式合成进 img, 返回需要上屏的矩形
// (节点盒: 帧 + contain 留边都算上, 留边由 drawNode 刚画的封面/底色提供)。
//
// 在渲染线程调用 (ShowRegions 内), 只读当前帧索引, 不阻塞时钟。
func (p *videoPlayer) compositeInto(img *image.RGBA) (gfx.Rect, bool) {
	p.mu.Lock()
	box := p.rect
	frame := p.frames[p.cur]
	fit := p.fit
	p.mu.Unlock()

	if box.W <= 0 || box.H <= 0 || frame == nil {
		return gfx.Rect{}, false
	}
	dst, ok := fitRect(box, frame.Bounds().Dx(), frame.Bounds().Dy(), fit)
	if !ok {
		return box, true
	}
	blitScaled(img, dst, frame)
	return box, true
}

// ===== 几何: fit 矩形 =====

// fitRect 按 fit 模式算"源图应该画到哪个矩形"。
//
// 与 gfx.videoFitRect 同逻辑 (那边是包内私有, 且软媒体不该反向依赖内核
// 内部)。两份都短到不会漂移, 各自的测试各钉各的几何值。
func fitRect(box gfx.Rect, sw, sh int, mode string) (gfx.Rect, bool) {
	if box.W <= 0 || box.H <= 0 || sw <= 0 || sh <= 0 {
		return gfx.Rect{}, false
	}
	switch mode {
	case "fill":
		return box, true
	case "cover":
		if int64(box.W)*int64(sh) >= int64(box.H)*int64(sw) {
			h := int(int64(box.W) * int64(sh) / int64(sw))
			return gfx.Rect{X: box.X, Y: box.Y + (box.H-h)/2, W: box.W, H: h}, h > 0
		}
		w := int(int64(box.H) * int64(sw) / int64(sh))
		return gfx.Rect{X: box.X + (box.W-w)/2, Y: box.Y, W: w, H: box.H}, w > 0
	default: // contain
		if int64(box.W)*int64(sh) <= int64(box.H)*int64(sw) {
			h := int(int64(box.W) * int64(sh) / int64(sw))
			return gfx.Rect{X: box.X, Y: box.Y + (box.H-h)/2, W: box.W, H: h}, h > 0
		}
		w := int(int64(box.H) * int64(sw) / int64(sh))
		return gfx.Rect{X: box.X + (box.W-w)/2, Y: box.Y, W: w, H: box.H}, w > 0
	}
}

// blitScaled 最近邻缩放把 src 画进 dst 的 r (自动裁剪到 img 边界)。
func blitScaled(img *image.RGBA, r gfx.Rect, src *image.RGBA) {
	// 裁剪到画布
	if r.X < 0 {
		r.W += r.X
		r.X = 0
	}
	if r.Y < 0 {
		r.H += r.Y
		r.Y = 0
	}
	if r.W <= 0 || r.H <= 0 {
		return
	}
	b := img.Bounds()
	if r.X+r.W > b.Dx() {
		r.W = b.Dx() - r.X
	}
	if r.Y+r.H > b.Dy() {
		r.H = b.Dy() - r.Y
	}
	if r.W <= 0 || r.H <= 0 {
		return
	}
	sw, sh := src.Bounds().Dx(), src.Bounds().Dy()
	for y := 0; y < r.H; y++ {
		sy := y * sh / r.H
		if sy >= sh {
			sy = sh - 1
		}
		row := img.Pix[img.PixOffset(r.X, r.Y+y):]
		for x := 0; x < r.W; x++ {
			sx := x * sw / r.W
			if sx >= sw {
				sx = sw - 1
			}
			c := src.RGBAAt(sx, sy)
			i := x * 4
			row[i+0] = c.R
			row[i+1] = c.G
			row[i+2] = c.B
			row[i+3] = c.A
		}
	}
}
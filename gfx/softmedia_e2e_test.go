//go:build linux

// softmedia 端到端: 真 VM + 真渲染路径 + 软媒体包装器。
//
// 与 gfx/softmedia 包内测试的分工: 那边钉播放器/编解码/包装器的**单元**
// 行为 (假 inner, 直接调方法); 这边走完整链路 —— EvalVM 起脚本 → render()
// 建窗 (SetPresentWrapper 在此刻套上 softmedia.Wrap) → Pump 驱动重绘与事件
// 回填 → 帧缓冲经包装面落到假 Surface 上。三件事只有这边能验:
//
//  1. `<video>` 的解码帧**真的进了帧缓冲** (包装面 ShowRegions 收到的 img 里
//     有 GIF 的红方块, 且随时间移动);
//  2. 宿主事件 (ready/play/ended/timeupdate) 经 gfx.Post 回到 GUI 线程、
//     ResolveVideoEvent/ResolveAudioEvent 落到脚本回调上;
//  3. 窗口关闭 (EventClose → a.close() → windowCloser.WindowClosed) 经
//     包装器停机全部播放器。
//
// 必须在**外部测试包** gfx_test: softmedia 导入 gfx, 包内测试反着 import
// 会成环 (同 bind_smoke_test.go 的纪律)。代价是包内 fakeSurface 用不了,
// 所以这里自带一个最小假 Surface —— 它**实现 waker** (三个真桌面后端都有),
// 于是 softmedia 的 WaitEventsWake 转发也一并被覆盖。
//
// 不需要 DISPLAY: 假工厂顶掉平台后端, 无头环境照跑。

package gfx_test

import (
	"image"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/14752222/Gox/gfx"
	"github.com/14752222/Gox/gfx/softmedia"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== 最小假 Surface (外部包用不了包内 fakeSurface) =====

// e2eSurface 记录上屏调用 (img 是 softmedia 合成视频帧**之后**的帧缓冲),
// 并实现 waker 可选接口 (WaitEventsWake 把 wake 并进等待)。
type e2eSurface struct {
	mu      sync.Mutex
	events  chan gfx.Event // takeEvent 消费 (只有 processEvents 读)
	arrived chan struct{}  // 等待唤醒消费 (WaitEvents/WaitEventsWake 读)
	w, h    int

	showCalls int
	wakeCalls int
	waitCalls int

	// lastImg 最近一次上屏的帧缓冲引用 (redraw 复用同一张, 不拷贝).
	lastImg *image.RGBA
	// redSamples 是每次上屏时帧缓冲里 GIF 红方块的外包框 (采样用).
	redSamples []redSample
}

type redSample struct {
	at    time.Time
	count int
	minX  int
	maxX  int
	minY  int
	maxY  int
}

func newE2ESurface(w, h int) *e2eSurface {
	// 双通道 (同内部 fakeSurface 与 x11): 事件只进 events 给 takeEvent,
	// arrived 只作唤醒信号给等待方 —— 等待方**不能**直接 select events,
	// 那会把事件从 takeEvent 眼皮底下偷走 (EventClose 永远到不了
	// processEvents, 窗口关不掉)。
	return &e2eSurface{
		events:  make(chan gfx.Event, 16),
		arrived: make(chan struct{}, 16),
		w:       w, h: h,
	}
}

func (s *e2eSurface) Show(img *image.RGBA) {
	s.mu.Lock()
	s.showCalls++
	s.lastImg = img
	s.mu.Unlock()
}

func (s *e2eSurface) ShowRegions(img *image.RGBA, rects []image.Rectangle) {
	s.mu.Lock()
	s.showCalls++
	s.lastImg = img
	s.redSamples = append(s.redSamples, sampleRed(img))
	s.mu.Unlock()
}

func (s *e2eSurface) Size() (int, int) { return s.w, s.h }

func (s *e2eSurface) Events() <-chan gfx.Event { return s.events }

func (s *e2eSurface) WaitEvents(maxWait time.Duration) bool {
	s.mu.Lock()
	s.waitCalls++
	s.mu.Unlock()
	if maxWait <= 0 {
		maxWait = 50 * time.Millisecond // 无限期在本假面上改成有界, 防挂死
	}
	t := time.NewTimer(maxWait)
	defer t.Stop()
	select {
	case <-s.arrived:
		return true
	case <-t.C:
		return true
	}
}

// WaitEventsWake 实现 gfx 的 waker 可选接口 (与 WaitEvents 同义 + wake)。
func (s *e2eSurface) WaitEventsWake(maxWait time.Duration, wake <-chan struct{}) bool {
	s.mu.Lock()
	s.wakeCalls++
	s.mu.Unlock()
	if maxWait <= 0 {
		maxWait = 50 * time.Millisecond
	}
	t := time.NewTimer(maxWait)
	defer t.Stop()
	select {
	case <-s.arrived:
		return true
	case <-wake:
		return true
	case <-t.C:
		return true
	}
}

// pushClose 推一个 EventClose —— x11 的真实关窗路径 (WM_DELETE_WINDOW →
// trySend(EventClose)), WaitEvents 并不因此返回 false。内核的 processEvents
// 看到它 → a.close() → windowCloser.WindowClosed → softmedia 停机。
func (s *e2eSurface) pushClose() {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case s.events <- gfx.Event{Kind: gfx.EventClose}:
	default:
	}
	select {
	case s.arrived <- struct{}{}:
	default:
	}
}

func (s *e2eSurface) snapshot() (shows, wakes, waits int, samples []redSample) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.showCalls, s.wakeCalls, s.waitCalls, append([]redSample(nil), s.redSamples...)
}

type e2eFactory struct{ s *e2eSurface }

func (f *e2eFactory) Create(gfx.WindowConfig) (gfx.Surface, error) { return f.s, nil }

// ===== 帧缓冲采样: GIF 红方块 (#C0392B, 无损调色板, 逐字节相等) =====

var gifRed = [4]byte{0xC0, 0x39, 0x2B, 0xFF}

func sampleRed(img *image.RGBA) redSample {
	var rs redSample
	rs.minX, rs.minY = 1<<30, 1<<30
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			i := img.PixOffset(x, y)
			if img.Pix[i] == gifRed[0] && img.Pix[i+1] == gifRed[1] &&
				img.Pix[i+2] == gifRed[2] && img.Pix[i+3] == gifRed[3] {
				rs.count++
				if x < rs.minX {
					rs.minX = x
				}
				if x > rs.maxX {
					rs.maxX = x
				}
				if y < rs.minY {
					rs.minY = y
				}
				if y > rs.maxY {
					rs.maxY = y
				}
			}
		}
	}
	return rs
}

// ===== 脚本 =====

const mediaVideoScript = `
import { h, render } from "gx/gfx";
import { canIUse } from "gx/device";

globalThis.g_events = [];

render(
  h("window", { title: "media e2e", width: 400, height: 300 },
    h("column", { padding: 10 },
      h("video", {
        src: "../testdata/media_demo.gif",
        width: 64,
        height: 48,
        playing: true,
        onReady: () => globalThis.g_events.push("ready"),
        onPlay: () => globalThis.g_events.push("play"),
        onPause: () => globalThis.g_events.push("pause"),
        onEnded: () => globalThis.g_events.push("ended"),
        onTimeUpdate: (e) => globalThis.g_events.push("t:" + e.currentTime + "/" + e.duration),
        onError: (e) => globalThis.g_events.push("err:" + e.code),
      }))));

globalThis.g_canuse = canIUse("video");
`

const mediaAudioScript = `
import { h, render } from "gx/gfx";
import { canIUse } from "gx/device";

globalThis.g_events = [];

render(
  h("window", { title: "media e2e", width: 400, height: 300 },
    h("column", { padding: 10 },
      h("audio", {
        src: "../testdata/media_demo.wav",
        playing: true,
        onReady: () => globalThis.g_events.push("ready"),
        onPlay: () => globalThis.g_events.push("play"),
        onPause: () => globalThis.g_events.push("pause"),
        onEnded: () => globalThis.g_events.push("ended"),
        onTimeUpdate: (e) => globalThis.g_events.push("t:" + e.currentTime + "/" + e.duration),
        onError: (e) => globalThis.g_events.push("err:" + e.code),
      }))));

globalThis.g_canuse = canIUse("audio");
`

// mediaRejectScript 用一个**不存在但扩展名明确**的文件: 格式判定发生在读盘
// 之前 (softmedia 的 decodeVideo 先看扩展名), 所以它走 errUnsupportedFormat
// → rejected 缓存 → 内核 attach-failed 降级。
const mediaRejectScript = `
import { h, render } from "gx/gfx";
import { canIUse } from "gx/device";

globalThis.g_events = [];

render(
  h("window", { title: "media e2e", width: 400, height: 300 },
    h("column", { padding: 10 },
      h("video", {
        src: "../testdata/video_demo.mp4",
        width: 64,
        height: 48,
        playing: true,
        onReady: () => globalThis.g_events.push("ready"),
        onPlay: () => globalThis.g_events.push("play"),
        onEnded: () => globalThis.g_events.push("ended"),
        onError: (e) => globalThis.g_events.push("err:" + e.code),
      }))));

globalThis.g_canuse = canIUse("video");
`

const mediaCloseScript = `
import { h, render } from "gx/gfx";

render(
  h("window", { title: "media e2e", width: 400, height: 300 },
    h("column", { padding: 10 },
      h("video", {
        src: "../testdata/media_demo.gif",
        width: 64,
        height: 48,
        playing: true,
        loop: true,
        onPlay: () => globalThis.g_played = (globalThis.g_played || 0) + 1,
      }))));
`

// ===== 驱动设施 =====

// bootMediaVM 起真 VM 并把假 Surface 交出去 (与包内 bootAudioVM 同形状)。
func bootMediaVM(t *testing.T, src string, s *e2eSurface) *vm.VM {
	t.Helper()
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	gfx.SetDefaultFactory(&e2eFactory{s})
	t.Cleanup(func() { gfx.SetDefaultFactory(nil) })

	v, err := vm.EvalVM(src)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	return v
}

// pumpFor 泵 dur 时长 (每轮最多 40ms), 到点后关窗让循环退出。
func pumpFor(t *testing.T, v *vm.VM, s *e2eSurface, dur time.Duration) {
	t.Helper()
	deadline := time.Now().Add(dur)
	pushed := false
	pump := func(time.Duration) bool {
		if !pushed && !time.Now().Before(deadline) {
			pushed = true
			s.pushClose()
		}
		return gfx.Pump(40 * time.Millisecond)
	}
	if err := v.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
}

// installSoftMedia 把 softmedia.Wrap 装进呈现包装器链, 并**捕获**包装面
// (关窗停机断言要对它说话)。返回捕获到的 Surface。
func installSoftMedia(t *testing.T) *gfx.Surface {
	t.Helper()
	var captured gfx.Surface
	gfx.SetPresentWrapper(func(inner gfx.Surface) (gfx.Surface, bool) {
		s, ok := softmedia.Wrap(inner)
		captured = s
		return s, ok
	})
	t.Cleanup(func() { gfx.SetPresentWrapper(nil) })
	return &captured
}

func jsStringsE2E(t *testing.T, v *vm.VM, name string) []string {
	t.Helper()
	val, ok := v.Globals().Get(name)
	if !ok {
		t.Fatalf("全局 %s 缺失", name)
	}
	arr, ok := val.(*object.Array)
	if !ok {
		t.Fatalf("全局 %s 不是数组, 实际 %s", name, val.Type())
	}
	out := make([]string, 0, len(arr.Elements))
	for i, el := range arr.Elements {
		s, ok := el.(*object.String)
		if !ok {
			t.Fatalf("全局 %s[%d] 不是字符串, 实际 %s", name, i, el.Type())
		}
		out = append(out, s.Value)
	}
	return out
}

func jsBoolE2E(t *testing.T, v *vm.VM, name string) bool {
	t.Helper()
	val, ok := v.Globals().Get(name)
	if !ok {
		t.Fatalf("全局 %s 缺失", name)
	}
	b, ok := val.(*object.Boolean)
	if !ok {
		t.Fatalf("全局 %s 不是布尔, 实际 %s", name, val.Type())
	}
	return b.Value
}

func hasEvent(events []string, want string) bool {
	for _, e := range events {
		if e == want {
			return true
		}
	}
	return false
}

// ===== 用例 =====

// TestSoftMediaE2EVideo: 解码帧进帧缓冲 + 时钟推进 + 事件回填 + canIUse。
func TestSoftMediaE2EVideo(t *testing.T) {
	// 无头环境没有 pulse 服务: 把 PULSE_SERVER 指到一个不存在的地址,
	// libpulse 直接快速失败走 null sink (否则可能尝试 autospawn)。
	// 视频路径不碰音频, 设它只为整个文件的口径一致。
	t.Setenv("PULSE_SERVER", "/nonexistent-gox-e2e")

	media := installSoftMedia(t)
	s := newE2ESurface(400, 300)
	v := bootMediaVM(t, mediaVideoScript, s)

	// 视频 10 帧 x 100ms = 1s; 泵 2.5s 覆盖 ended + 余量。
	pumpFor(t, v, s, 2500*time.Millisecond)

	// 1. 包装面确实被装上了 (render() 建窗时套的)
	if *media == nil {
		t.Fatal("softmedia.Wrap 没有被装进呈现链 (captured 为 nil)")
	}

	// 2. 帧缓冲里出现过红方块, 且位置随时间移动 (时钟在推进帧)
	shows, wakes, waits, samples := s.snapshot()
	if shows < 10 {
		t.Fatalf("2.5s 至少该泵出 10 次上屏, 实际 %d", shows)
	}
	if wakes == 0 {
		t.Fatal("假面实现了 waker, 等待却从没走过 WaitEventsWake —— softmedia 的转发没生效")
	}
	if waits != 0 {
		t.Fatalf("走了 waker 路径就不该再碰普通 WaitEvents (wakes=%d waits=%d)", wakes, waits)
	}
	var first, later redSample
	firstSeen, laterSeen := false, false
	for _, rs := range samples {
		if rs.count >= 50 && !firstSeen {
			first, firstSeen = rs, true
			continue
		}
		if firstSeen && rs.count >= 50 && rs.minX != first.minX {
			later, laterSeen = rs, true
			break
		}
	}
	if !firstSeen {
		t.Fatal("帧缓冲里从没出现过 GIF 红方块 (>=50 像素) —— 视频帧没合成进 img")
	}
	if !laterSeen {
		t.Fatal("红方块位置从头到尾没变 —— 时钟没推进帧 (或合成的是静止图)")
	}
	t.Logf("红方块: 首见 x[%d,%d] y[%d,%d] (%d px), 后见 x[%d,%d] (%d px)",
		first.minX, first.maxX, first.minY, first.maxY, first.count,
		later.minX, later.maxX, later.count)

	// 3. 事件回填: ready → play → ended (timeupdate 节流后也会有)
	events := jsStringsE2E(t, v, "g_events")
	for _, want := range []string{"ready", "play", "ended"} {
		if !hasEvent(events, want) {
			t.Fatalf("脚本该收到 %q, 实际事件序列 %v", want, events)
		}
	}
	if len(events) > 0 && events[len(events)-1] != "ended" {
		t.Fatalf("ended 该是最后一个事件, 实际 %v", events)
	}

	// 4. canIUse 照实: softmedia 在, 视频内联就在
	if !jsBoolE2E(t, v, "g_canuse") {
		t.Fatal("有 softmedia 时 canIUse(\"video\") 应为 true")
	}
}

// TestSoftMediaE2EDemoScript 跑一遍 S10 的演示脚本 (testdata/media_demo.js):
// 它是"用户真正会写的那种脚本" —— 两个视频 + 一个音频 + 一个边界框 + 两块
// 受控按钮。断言它**真的播起来** (不是降级), 且边界框照实报错。
func TestSoftMediaE2EDemoScript(t *testing.T) {
	t.Setenv("PULSE_SERVER", "/nonexistent-gox-e2e")
	installSoftMedia(t)

	// 脚本里的 src 按进程工作目录解释 (同 docs 的承诺), 演示脚本要在仓库根跑
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(".."); err != nil {
		t.Fatalf("切换到仓库根目录: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("恢复 cwd: %v", err)
		}
	})

	srcBytes, err := os.ReadFile(filepath.Join("testdata", "media_demo.js"))
	if err != nil {
		t.Fatalf("读取演示脚本: %v", err)
	}
	s := newE2ESurface(340, 560)
	v := bootMediaVM(t, string(srcBytes), s)
	pumpFor(t, v, s, 1500*time.Millisecond)

	// 1. 两个视频容器与音频都播起来了 (不是降级占位)
	events := jsStringsE2E(t, v, "g_mediaEvents")
	for _, want := range []string{"gif:ready", "gif:play", "mjpeg:ready", "mjpeg:t", "wav:ready", "wav:play", "wav:ended"} {
		if !hasEvent(events, want) {
			t.Fatalf("演示脚本该收到 %q, 实际事件 %v", want, events)
		}
	}

	// 2. 边界框: h264 该 attach-failed, 且不假装在播
	if !hasEvent(events, "mp4:err:attach-failed") {
		t.Fatalf("mp4 框该报 attach-failed, 实际事件 %v", events)
	}
	if hasEvent(events, "mp4:ready") || hasEvent(events, "mp4:play") {
		t.Fatalf("mp4 框不该有播放事件, 实际 %v", events)
	}

	// 3. 上屏次数: 两个视频在播, 重绘不该只有首帧一次
	shows, _, _, _ := s.snapshot()
	if shows < 10 {
		t.Fatalf("1.5s 内两个视频在播, 上屏该有十几次, 实际 %d", shows)
	}
}

// 但 ready/play/ended 的时间轴必须是真的 (null sink 也照真走时)。
func TestSoftMediaE2EAudio(t *testing.T) {
	t.Setenv("PULSE_SERVER", "/nonexistent-gox-e2e")

	installSoftMedia(t)
	s := newE2ESurface(400, 300)
	v := bootMediaVM(t, mediaAudioScript, s)

	pumpFor(t, v, s, 1500*time.Millisecond)

	events := jsStringsE2E(t, v, "g_events")
	for _, want := range []string{"ready", "play", "ended"} {
		if !hasEvent(events, want) {
			t.Fatalf("脚本该收到 %q, 实际事件序列 %v", want, events)
		}
	}
	if len(events) > 0 && events[len(events)-1] != "ended" {
		t.Fatalf("ended 该是最后一个事件, 实际 %v", events)
	}
	if !jsBoolE2E(t, v, "g_canuse") {
		t.Fatal("有 softmedia 时 canIUse(\"audio\") 应为 true")
	}
}

// TestSoftMediaE2ERejectsUnsupportedFormat: 解不动的格式 → 内核
// attach-failed 降级 + onError, 而 canIUse 仍说 true (能力在, 这个文件不在
// 它的覆盖范围内)。绝不假装在播。
func TestSoftMediaE2ERejectsUnsupportedFormat(t *testing.T) {
	t.Setenv("PULSE_SERVER", "/nonexistent-gox-e2e")

	installSoftMedia(t)
	s := newE2ESurface(400, 300)
	v := bootMediaVM(t, mediaRejectScript, s)

	pumpFor(t, v, s, 600*time.Millisecond)

	events := jsStringsE2E(t, v, "g_events")
	if !hasEvent(events, "err:attach-failed") {
		t.Fatalf("该派发 err:attach-failed, 实际 %v", events)
	}
	if hasEvent(events, "play") || hasEvent(events, "ready") {
		t.Fatalf("挂载失败不该有播放事件, 实际 %v", events)
	}
	if !jsBoolE2E(t, v, "g_canuse") {
		t.Fatal("canIUse 该照实回答 true (softmedia 是宿主, 只是不支持这个格式)")
	}
}

// TestSoftMediaE2EWindowCloseStopsPlayers: 内核的 a.close() 不销毁渲染树、
// 不会来 Detach —— 播放器要停在包装器的"窗口没了"钩子上 (WaitEvents 返回
// false), 否则音乐会一路放到进程退出。停机的可观测面: 停机后 mediaSurface
// 拒绝新挂载 (errStopped)。
func TestSoftMediaE2EWindowCloseStopsPlayers(t *testing.T) {
	t.Setenv("PULSE_SERVER", "/nonexistent-gox-e2e")

	media := installSoftMedia(t)
	s := newE2ESurface(400, 300)
	v := bootMediaVM(t, mediaCloseScript, s)

	// 先播一会儿 (loop 视频不会自己结束)
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !gfx.Pump(40 * time.Millisecond) {
			t.Fatal("窗口还活着, Pump 不该返回 false")
		}
	}
	shows, _, _, _ := s.snapshot()
	if shows < 3 {
		t.Fatalf("400ms 至少该泵出 3 次上屏 (视频在播), 实际 %d", shows)
	}

	// 关窗 (x11 的真实路径: EventClose 事件, 不是 WaitEvents 返回 false):
	// 内核 processEvents → a.close() → windowCloser.WindowClosed → 停机
	s.pushClose()
	if gfx.Pump(100 * time.Millisecond) {
		t.Fatal("关窗后 Pump 该返回 false")
	}
	if err := v.RunTimersWithPump(func(time.Duration) bool { return false }); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}

	// 停机的可观测面: 包装面拒绝新挂载
	if *media == nil {
		t.Fatal("包装面没被捕获")
	}
	// 形状与内核的 nativeVideoHost 一致 (它是未导出的, 外部包只能照抄形状)。
	// id 由内核分配, 这里手编一个 —— 关窗后的拒绝发生在读盘之前, 用不到它。
	vh, ok := (*media).(interface {
		VideoSurfaceAttach(id uint64, r gfx.Rect, src string, opts gfx.VideoSurfaceOptions) error
	})
	if !ok {
		t.Fatal("捕获到的包装面不满足 nativeVideoHost 形状")
	}
	err := vh.VideoSurfaceAttach(999, gfx.Rect{W: 64, H: 48}, "../testdata/media_demo.gif", gfx.VideoSurfaceOptions{})
	if err == nil || !strings.Contains(err.Error(), "窗口已关闭") {
		t.Fatalf("关窗后该拒绝新挂载 (errStopped), 实际 %v", err)
	}
}

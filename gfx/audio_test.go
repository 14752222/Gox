package gfx

import (
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== `<audio>` 组件 (S9): 契约 + 降级 =====
//
// 与 video_test.go 同一套结构: 三条互不重叠的路径 (没有宿主 → 降级;
// 有宿主 → 挂载/指令去重/释放; 有接口但自报不支持 → 同降级), 加事件回填
// 与逐像素绘制断言。音频没有矩形, 所以比 video 少一整块"跟随布局"的用例,
// 但多一条"src 清零即摘源" (没有盒子退化那种中间态)。

// =====================================================================
// 假平台音频层: nativeAudioHost 的测试替身
// =====================================================================

type fakeAudioAttach struct {
	ID   uint64
	Src  string
	Opts AudioPlayOptions
	Err  error // 非空 = 这次挂载失败了 (失败也记一笔: 要断言"每帧还在重试")
}

type fakeAudioCommand struct {
	ID    uint64
	Cmd   AudioCommand
	Value float64
}

type fakeAudioHost struct {
	mu        sync.Mutex
	supported bool   // AudioInlineSupported 的返回值
	attachErr error  // 非空时 AudioAttach 失败

	attachs  []fakeAudioAttach
	commands []fakeAudioCommand
	detaches []uint64
	live     map[uint64]string // 还挂着的源 (Detach 后移除) —— 用来抓"泄漏"
}

func newFakeAudioHost() *fakeAudioHost {
	return &fakeAudioHost{supported: true, live: map[uint64]string{}}
}

func (f *fakeAudioHost) AudioInlineSupported() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.supported
}

// AudioAttach: id 由内核分配 (句柄全局唯一, 后端不自己编号)。
func (f *fakeAudioHost) AudioAttach(id uint64, src string, opts AudioPlayOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.attachErr != nil {
		// 失败的尝试也记一笔 (但不进 live): 用例要断言"宿主还能恢复,
		// 所以内核每帧都在重试", 只记成功的调用就看不出这件事。
		f.attachs = append(f.attachs, fakeAudioAttach{ID: id, Src: src, Opts: opts, Err: f.attachErr})
		return f.attachErr
	}
	f.attachs = append(f.attachs, fakeAudioAttach{ID: id, Src: src, Opts: opts})
	f.live[id] = src
	return nil
}

func (f *fakeAudioHost) AudioCommand(id uint64, cmd AudioCommand, value float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, fakeAudioCommand{ID: id, Cmd: cmd, Value: value})
	return nil
}

func (f *fakeAudioHost) AudioDetach(id uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.detaches = append(f.detaches, id)
	delete(f.live, id)
	return nil
}

// ---- 读回 (全部返回副本) ----

func (f *fakeAudioHost) attachCalls() []fakeAudioAttach {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeAudioAttach(nil), f.attachs...)
}

func (f *fakeAudioHost) commandCalls() []fakeAudioCommand {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeAudioCommand(nil), f.commands...)
}

func (f *fakeAudioHost) detachCalls() []uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint64(nil), f.detaches...)
}

func (f *fakeAudioHost) liveIDs() []uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]uint64, 0, len(f.live))
	for id := range f.live {
		out = append(out, id)
	}
	return out
}

// lastID 返回最近一次**成功**挂载的句柄。
//
// 句柄现在由内核分配 (后端不再自己编号), 所以从挂载记录里读回来 —— 失败的
// 尝试也记在 attachs 里, 取最近一条成功的才是"当前挂着的那个"。
func (f *fakeAudioHost) lastID() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.attachs) - 1; i >= 0; i-- {
		if f.attachs[i].Err == nil {
			return f.attachs[i].ID
		}
	}
	return 0
}

// commandNames 返回从第 from 条起的指令名序列 (同 fakeVideoHost.commandNames:
// 断言"这一轮到底发了什么"比逐条比对 ID/Value 更贴近"重发会不会打断播放")。
func (f *fakeAudioHost) commandNames(from int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.commands))
	for _, c := range f.commands[minInt(from, len(f.commands)):] {
		out = append(out, string(c.Cmd))
	}
	return out
}

// ---- fakeSurface 侧的能力转发 (字段定义在 helpers_test.go) ----

func (f *fakeSurface) AudioInlineSupported() bool {
	if f.audio == nil {
		return false
	}
	return f.audio.AudioInlineSupported()
}

func (f *fakeSurface) AudioAttach(id uint64, src string, opts AudioPlayOptions) error {
	if f.audio == nil {
		return errors.New("test: 未注入假音频宿主")
	}
	return f.audio.AudioAttach(id, src, opts)
}

func (f *fakeSurface) AudioCommand(id uint64, cmd AudioCommand, value float64) error {
	if f.audio == nil {
		return errors.New("test: 未注入假音频宿主")
	}
	return f.audio.AudioCommand(id, cmd, value)
}

func (f *fakeSurface) AudioDetach(id uint64) error {
	if f.audio == nil {
		return errors.New("test: 未注入假音频宿主")
	}
	return f.audio.AudioDetach(id)
}

// =====================================================================
// 设施: 挂载 / 状态读取
// =====================================================================

func startAudioTest(t *testing.T) {
	t.Helper()
	resetAudioStateForTest()
	t.Cleanup(resetAudioStateForTest)
}

// mountAudioApp 挂一个纯 Go 的 app, 且**先**把假音频宿主注入好 (理由同
// mountVideoApp: 首帧就带宿主才是真后端的形状)。
func mountAudioApp(t *testing.T, root *GuiNode, w, h int) (*fakeSurface, *app, *fakeAudioHost) {
	t.Helper()
	return mountAudioAppHost(t, root, w, h, newFakeAudioHost())
}

func mountAudioAppHost(t *testing.T, root *GuiNode, w, h int, ah *fakeAudioHost) (*fakeSurface, *app, *fakeAudioHost) {
	t.Helper()
	fake := newFakeSurface()
	fake.audio = ah
	a := mountTestAppWith(t, root, fake, w, h)
	return fake, a, ah
}

// bootAudioVM 起一个真 VM 并把假 Surface 交出去 (同 bootVideoVM)。
func bootAudioVM(t *testing.T, src string, fake *fakeSurface) *vm.VM {
	t.Helper()
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(src)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	return v
}

// audioStateOf 读节点的宿主侧状态快照 (没有条目返回 false)。
func audioStateOf(n *GuiNode) (audioState, bool) {
	audioMu.Lock()
	defer audioMu.Unlock()
	st, ok := audioStates[n]
	if !ok {
		return audioState{}, false
	}
	return *st, true
}

// audioSurfaceID 取节点当前挂着的平台音频句柄 (0 = 没挂上)。
func audioSurfaceID(n *GuiNode) uint64 {
	st, _ := audioStateOf(n)
	return st.id
}

// audioReported 报告该节点是否已经因"没有平台音频层 / 挂载失败"报过一次。
func audioReported(n *GuiNode) bool {
	st, ok := audioStateOf(n)
	return ok && st.reported
}

// mkAudioNode 造一个 audio 节点 (src 是字符串属性, mkNode 只收数值)。
func mkAudioNode(src string, props map[string]float64) *GuiNode {
	return withStr(mkNode("audio", props), "src", src)
}

// =====================================================================
// 纯函数: 固有尺寸
// =====================================================================

// TestAudioIntrinsicSize 没尺寸要落 300×36 兜底 —— **必须非 0**,
// 0 尺寸子树会被 drawNode 整支跳过 (症状是"这里什么都没有")。
func TestAudioIntrinsicSize(t *testing.T) {
	plain := mkNode("audio", nil)
	if w, h := plain.intrinsicSize(); w != audioDefaultW || h != audioDefaultH {
		t.Fatalf("无尺寸应为 %dx%d, 实际 %dx%d", audioDefaultW, audioDefaultH, w, h)
	}

	// 显式尺寸优先; 只给一轴时另一轴补缺省
	nw := withStr(mkNode("audio", map[string]float64{"width": 40}), "src", "a.wav")
	if w, h := nw.intrinsicSize(); w != 40 || h != audioDefaultH {
		t.Fatalf("显式宽 40 + 缺省高, 实际 %dx%d", w, h)
	}
	nh := withStr(mkNode("audio", map[string]float64{"height": 50}), "src", "a.wav")
	if w, h := nh.intrinsicSize(); w != audioDefaultW || h != 50 {
		t.Fatalf("缺省宽 + 显式高 50, 实际 %dx%d", w, h)
	}
}

// =====================================================================
// 降级: 没有平台音频层
// =====================================================================

// TestAudioNoHostCapability 三种"没有能力"要分清:
// 没挂窗口 / 后端不实现 nativeAudioHost / 后端实现了但自己说不支持。
func TestAudioNoHostCapability(t *testing.T) {
	startAudioTest(t)

	// 1. 裸后端 (连接口都没实现)
	if _, ok := any(&bareSurface{}).(nativeAudioHost); ok {
		t.Fatalf("bareSurface 不该实现 nativeAudioHost")
	}

	// 2. 挂了窗口, 但 fakeSurface 没注入音频宿主 = 这个后端没有平台音频层
	root := mkNode("column", nil)
	mountChildren(root, mkAudioNode("a.wav", map[string]float64{"width": 80, "height": 36}))
	fake, a := mountTestApp(t, root, 200, 150)
	if AudioInlineSupported() {
		t.Fatalf("没注入宿主时 AudioInlineSupported() 应为 false")
	}
	if h, ok := audioHostOf(a); ok || h != nil {
		t.Fatalf("没注入宿主时 audioHostOf 该判为不可用")
	}

	// 3. 后端实现了接口, 但自己报告"不支持"
	ah := newFakeAudioHost()
	ah.supported = false
	fake.audio = ah
	if AudioInlineSupported() {
		t.Fatalf("宿主自报不支持时 AudioInlineSupported() 应为 false")
	}
}

// TestAudioDowngradeWarnsOnceReportsPerNode 降级的两条硬要求 (与 video 对称):
// 告警**按进程**只出一次, onError **按节点**各报一次。
func TestAudioDowngradeWarnsOnceReportsPerNode(t *testing.T) {
	startAudioTest(t)

	var warned []string
	oldWarn := warnAudioNoHost
	warnAudioNoHost = func(src string) { warned = append(warned, src) }
	defer func() { warnAudioNoHost = oldWarn }()

	root := mkNode("column", nil)
	a1 := mkAudioNode("a.wav", map[string]float64{"width": 80, "height": 36})
	a2 := mkAudioNode("b.wav", map[string]float64{"width": 80, "height": 36})
	// 没写 src 的节点连"想播放"都不算: 不打扰 (还没准备好而已)
	quiet := mkNode("audio", map[string]float64{"width": 80, "height": 36})
	mountChildren(root, a1, a2, quiet)

	_, a := mountTestApp(t, root, 200, 150) // 没注入宿主 ⇒ 首帧即降级
	a.redraw()
	a.redraw()

	if len(warned) != 1 {
		t.Fatalf("进程级告警应只出一次, 实际 %d 次: %v", len(warned), warned)
	}
	if !audioReported(a1) || !audioReported(a2) {
		t.Fatalf("写了 src 的节点都该各报一次 onError (a1=%v a2=%v)",
			audioReported(a1), audioReported(a2))
	}
	if audioReported(quiet) {
		t.Fatalf("没写 src 的节点不该收到 onError")
	}
	if id := audioSurfaceID(a1); id != 0 {
		t.Fatalf("降级路径不该挂音频源, 实际 id=%d", id)
	}
}

// TestAudioAttachFailureReportedOnce 宿主挂载报错时: 每帧**继续重试**
// (宿主可能刚好那一刻拿不到设备), 但告警与 onError 各只出一次。
func TestAudioAttachFailureReportedOnce(t *testing.T) {
	startAudioTest(t)

	var warned []string
	oldWarn := warnAudioAttach
	warnAudioAttach = func(src string, err error) { warned = append(warned, src) }
	defer func() { warnAudioAttach = oldWarn }()

	root := mkNode("column", nil)
	n := mkAudioNode("a.wav", map[string]float64{"width": 80, "height": 36})
	mountChildren(root, n)

	// 宿主必须在**首帧之前**就报"挂不上" —— 事后才改 attachErr 的话首帧已经
	// 挂成功了, 这条路径根本不会走到。
	ah := newFakeAudioHost()
	ah.attachErr = errors.New("test: 没有声卡")
	_, a, ah := mountAudioAppHost(t, root, 200, 150, ah)

	a.redraw()
	a.redraw()

	if len(warned) != 1 {
		t.Fatalf("挂载失败应只告警一次, 实际 %d 次: %v", len(warned), warned)
	}
	// 失败也仍然每帧重试 (宿主下一帧可能就好了), 但一次都没成功
	if n := len(ah.attachCalls()); n != 3 { // 首帧 + 上面两次 redraw
		t.Fatalf("挂载失败该每帧重试, 实际只尝试了 %d 次", n)
	}
	if got := ah.liveIDs(); len(got) != 0 {
		t.Fatalf("失败的挂载不该留下活着的源: %v", got)
	}
	if id := audioSurfaceID(n); id != 0 {
		t.Fatalf("挂载失败时句柄应为 0, 实际 %d", id)
	}
	if !audioReported(n) {
		t.Fatalf("挂载失败该对本报一次 onError")
	}
}

// =====================================================================
// 契约: 挂载 / 指令去重 / 释放
// =====================================================================

// TestAudioLifecycle 走完整的一条命: 首帧挂上 → 受控属性变化只发变化的那条
// 指令 → 值没变一条不发 → src 清零摘源 → 写回来重新挂 → 节点离树摘掉。
func TestAudioLifecycle(t *testing.T) {
	startAudioTest(t)

	root := mkNode("column", nil)
	n := mkAudioNode("song.wav", map[string]float64{"width": 80, "height": 36})
	withBool(n, "playing", true)
	withBool(n, "controls", true)
	mountChildren(root, n)

	_, a, ah := mountAudioApp(t, root, 400, 300)

	att := ah.attachCalls()
	if len(att) != 1 {
		t.Fatalf("首帧应挂一次音频源, 实际 %d 次", len(att))
	}
	if att[0].Src != "song.wav" {
		t.Fatalf("挂载的 src = %q, want %q", att[0].Src, "song.wav")
	}
	if !att[0].Opts.Controls {
		t.Fatalf("controls 该传给宿主")
	}
	if att[0].Opts.Volume != 1 || att[0].Opts.Muted {
		t.Fatalf("缺省音量 1 且不静音, 实际 volume=%v muted=%v", att[0].Opts.Volume, att[0].Opts.Muted)
	}
	id := ah.lastID()
	if sid := audioSurfaceID(n); sid != id {
		t.Fatalf("节点记录的句柄 = %d, 宿主分配的是 %d", sid, id)
	}
	// 挂载是 force 全量: play / mute / loop / volume 四条都要下发
	if got := strings.Join(ah.commandNames(0), ","); got != "play,mute,loop,volume" {
		t.Fatalf("挂载时的指令序列 = %q, want %q", got, "play,mute,loop,volume")
	}

	// 受控属性变化: 只发变化的那一条 (重发 play/pause 会打断正在进行的播放)
	from := len(ah.commandCalls())
	withBool(n, "playing", false)
	a.redraw()
	if got := strings.Join(ah.commandNames(from), ","); got != "pause" {
		t.Fatalf("只改 playing 时该只发 pause, 实际 %q", got)
	}

	// 值没变: 一条都不该发
	from = len(ah.commandCalls())
	a.redraw()
	if got := ah.commandNames(from); len(got) != 0 {
		t.Fatalf("无变化时不该下发任何指令, 实际 %v", got)
	}

	// src 被脚本清掉 → 摘源 (没有"盒子退化"那种中间态, src 清零就是全部)
	withStr(n, "src", "")
	a.redraw()
	if len(ah.detachCalls()) != 1 {
		t.Fatalf("src 清零后该摘掉音频源, detach=%v", ah.detachCalls())
	}
	if _, ok := audioStateOf(n); ok {
		t.Fatalf("摘掉后状态表该清干净")
	}
	// 再写回来 → 重新挂上 (不是"一次性"的)
	withStr(n, "src", "song.wav")
	a.redraw()
	if n := len(ah.attachCalls()); n != 2 {
		t.Fatalf("src 写回来该重新挂载, 实际累计 %d 次", n)
	}

	// 节点整支从树上消失 (each 重建 / 条件分支翻面) → 源必须摘掉
	root.Children = nil
	a.redraw()
	if got := ah.detachCalls(); len(got) != 2 || got[1] != ah.lastID() {
		t.Fatalf("离开树的源应被摘掉, detach=%v", ah.detachCalls())
	}
	if live := ah.liveIDs(); len(live) != 0 {
		t.Fatalf("摘掉后不该还有活着的源: %v", live)
	}
}

// TestAudioDisposeReleasesSource 节点被 dispose 时**立刻**释放音频源,
// 不等下一次 flush —— 子树销毁后可能再也没有重绘 (没有脏区就不进 redraw),
// 音乐会一直放到窗口关闭。
func TestAudioDisposeReleasesSource(t *testing.T) {
	startAudioTest(t)

	root := mkNode("column", nil)
	n := mkAudioNode("song.wav", map[string]float64{"width": 80, "height": 36})
	mountChildren(root, n)

	_, _, ah := mountAudioApp(t, root, 400, 300)
	id := ah.lastID()
	if id == 0 {
		t.Fatalf("首帧该挂上音频源")
	}

	disposeNode(n)

	if got := ah.detachCalls(); len(got) != 1 || got[0] != id {
		t.Fatalf("dispose 应立刻摘掉音频源, detach=%v (id=%d)", got, id)
	}
	if _, ok := audioStateOf(n); ok {
		t.Fatalf("dispose 后状态表该清干净")
	}
}

// =====================================================================
// 宿主 → 脚本: 事件回填 (真 VM)
// =====================================================================

// audioEventScript 与 videoEventScript 同款: 把每个回调记进 globalThis.g_events。
const audioEventScript = `
import { h, render } from "gx/gfx";
import { canIUse } from "gx/device";

globalThis.g_events = [];

render(
  h("window", { title: "audio", width: 400, height: 300 },
    h("column", { padding: 10 },
      h("audio", {
        src: "song.wav",
        width: 300,
        height: 36,
        onReady: () => globalThis.g_events.push("ready"),
        onPlay: () => globalThis.g_events.push("play"),
        onPause: () => globalThis.g_events.push("pause"),
        onEnded: () => globalThis.g_events.push("ended"),
        onTimeUpdate: (e) => globalThis.g_events.push("t:" + e.currentTime + "/" + e.duration),
        onError: (e) => globalThis.g_events.push("err:" + e.code),
      }))));

globalThis.g_canuse = canIUse("audio");
`

// TestAudioEventsReachScript 有宿主时: ResolveAudioEvent 必须把事件落到脚本的
// 对应回调上, 句柄对不上/为 0 的静默丢弃。
func TestAudioEventsReachScript(t *testing.T) {
	startAudioTest(t)

	fake := newFakeSurface()
	ah := newFakeAudioHost()
	fake.audio = ah
	v := bootAudioVM(t, audioEventScript, fake)

	runPumpSteps(t, v, fake, []func(){
		func() {}, // 首帧: 把音频源挂上
		func() {
			att := ah.attachCalls()
			if len(att) != 1 {
				t.Fatalf("首帧应挂一次音频源, 实际 %d 次", len(att))
			}
			if att[0].Src != "song.wav" {
				t.Fatalf("挂载的 src = %q", att[0].Src)
			}
			id := ah.lastID()
			ResolveAudioEvent(id, AudioEvent{Kind: "ready"})
			ResolveAudioEvent(id, AudioEvent{Kind: "play"})
			ResolveAudioEvent(id, AudioEvent{Kind: "timeupdate", CurrentTime: 2.5, Duration: 30})
			ResolveAudioEvent(id, AudioEvent{Kind: "ended"})
			// 句柄对不上 / 句柄为 0: 事件在路上而界面已经不要它了, 静默丢弃
			ResolveAudioEvent(id+999, AudioEvent{Kind: "play"})
			ResolveAudioEvent(0, AudioEvent{Kind: "play"})
		},
		func() {
			want := "ready,play,t:2.5/30,ended"
			if got := strings.Join(jsStrings(t, v, "g_events"), ","); got != want {
				t.Fatalf("脚本收到的事件 = %q, want %q", got, want)
			}
			if ok, isBool := globalBool(t, v, "g_canuse"); !isBool || !ok {
				t.Fatalf("有宿主时 canIUse(\"audio\") 应为 true, 实际 %v/%v", ok, isBool)
			}
		},
	})
}

// TestAudioHostErrorReachesScript 宿主上报 error: 没给 code 时要落一个明确
// 的兜底码, 而不是空串 (脚本按 code 分支处理)。
func TestAudioHostErrorReachesScript(t *testing.T) {
	startAudioTest(t)

	fake := newFakeSurface()
	ah := newFakeAudioHost()
	fake.audio = ah
	v := bootAudioVM(t, audioEventScript, fake)

	runPumpSteps(t, v, fake, []func(){
		func() {},
		func() {
			id := ah.lastID()
			ResolveAudioEvent(id, AudioEvent{Kind: "error", Code: "decode-failed", Message: "坏文件"})
			ResolveAudioEvent(id, AudioEvent{Kind: "error", Message: "宿主没说原因"})
		},
		func() {
			want := "err:decode-failed,err:playback-failed"
			if got := strings.Join(jsStrings(t, v, "g_events"), ","); got != want {
				t.Fatalf("脚本收到的事件 = %q, want %q", got, want)
			}
		},
	})
}

// TestAudioDowngradeReachesScript 没有平台音频层时, 脚本必须收到
// `onError {code:"unsupported"}` (与 video 同一条理由: 无法假装在播)。
// 同时 canIUse 要照实回答 false, 让脚本能"先问再选路"。
func TestAudioDowngradeReachesScript(t *testing.T) {
	cases := []struct {
		name    string
		prepare func(fake *fakeSurface)
	}{
		{"后端不实现 nativeAudioHost", func(fake *fakeSurface) {}},
		{"后端实现了但自报不支持", func(fake *fakeSurface) {
			ah := newFakeAudioHost()
			ah.supported = false
			fake.audio = ah
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			startAudioTest(t)

			fake := newFakeSurface()
			c.prepare(fake)
			v := bootAudioVM(t, audioEventScript, fake)

			runPumpSteps(t, v, fake, []func(){
				func() {},
				func() {
					want := "err:unsupported"
					if got := strings.Join(jsStrings(t, v, "g_events"), ","); got != want {
						t.Fatalf("降级事件 = %q, want %q", got, want)
					}
					if ok, isBool := globalBool(t, v, "g_canuse"); !isBool || ok {
						t.Fatalf("无能力时 canIUse(\"audio\") 应为 false, 实际 %v/%v", ok, isBool)
					}
				},
			})
		})
	}
}

// =====================================================================
// 绘制: 占位 / 用户 background / 禁用罩 / 退化盒
// =====================================================================

// TestAudioPaintPlaceholder 画浅色底 + 声波柱 —— 柱不能顶到盒子边缘
// (那样就成了一块纯色), 也不能小到看不见。
func TestAudioPaintPlaceholder(t *testing.T) {
	n := mkAudioNode("song.wav", map[string]float64{"width": 200, "height": 36})
	n.Box = Rect{X: 0, Y: 0, W: 200, H: 36}

	img := image.NewRGBA(image.Rect(0, 0, 200, 36))
	paintAudio(img, n, false)

	if got := img.RGBAAt(2, 2); got != colorAudioBackdrop {
		t.Fatalf("应铺浅色占位底, 左上角实际 %v, want %v", got, colorAudioBackdrop)
	}
	// 声波柱画在盒子中间: 左右两端不该有它
	if c := countColor(img, Rect{X: 0, Y: 0, W: 20, H: 36}, colorAudioGlyph); c != 0 {
		t.Fatalf("声波柱不该顶到盒子左边缘 (实际 %d 个像素)", c)
	}
	if c := countColor(img, Rect{X: 80, Y: 4, W: 40, H: 28}, colorAudioGlyph); c == 0 {
		t.Fatalf("盒子中间该有声波柱")
	}
}

// TestAudioPaintKeepsUserBackground 用户显式写了 background 时, 底色归用户:
// 占位底不许盖上去 (同 video: 补画了装饰, 但不能反过来盖掉用户的东西)。
func TestAudioPaintKeepsUserBackground(t *testing.T) {
	bg := mustColor(t, "#123456")
	n := mkAudioNode("song.wav", map[string]float64{"width": 60, "height": 36})
	withStr(n, "background", "#123456")
	n.Box = Rect{X: 0, Y: 0, W: 60, H: 36}
	img := image.NewRGBA(image.Rect(0, 0, 60, 36))
	paintAudio(img, n, false)
	assertPx(t, img, 1, 1, bg, "该露出用户的 background")
	if c := countColor(img, n.Box, colorAudioBackdrop); c != 0 {
		t.Fatalf("不该铺占位底 (实际 %d 个像素)", c)
	}
}

// TestAudioDisabledVeil 禁用罩要真的改变像素, 且罩下仍能看见内容 (半透明)。
func TestAudioDisabledVeil(t *testing.T) {
	n := mkAudioNode("song.wav", map[string]float64{"width": 40, "height": 36})
	n.Box = Rect{X: 0, Y: 0, W: 40, H: 36}

	plain := image.NewRGBA(image.Rect(0, 0, 40, 36))
	paintAudio(plain, n, false)
	veiled := image.NewRGBA(image.Rect(0, 0, 40, 36))
	paintAudio(veiled, n, true)

	if veiled.RGBAAt(0, 0) == plain.RGBAAt(0, 0) {
		t.Fatalf("禁用罩没改变像素: %v", veiled.RGBAAt(0, 0))
	}
	if veiled.RGBAAt(0, 0).R == 0 {
		t.Fatalf("禁用罩该是半透明的, 底下内容不该被抹平: %v", veiled.RGBAAt(0, 0))
	}
}

// TestAudioDegenerateBoxNoPanic 0 尺寸盒不该 panic (与 video 同一条:
// 窗口被拖到 0 尺寸、或还没布局时的中间态都会走到这)。
func TestAudioDegenerateBoxNoPanic(t *testing.T) {
	n := mkAudioNode("song.wav", nil)
	n.Box = Rect{}
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	paintAudio(img, n, false) // 不 panic 即通过

	n.Box = Rect{X: 0, Y: 0, W: 10, H: 0}
	paintAudio(img, n, false)
}

// =====================================================================
// 演示脚本
// =====================================================================

// TestAudioDemoScript 跑一遍 S9 的演示脚本 (占位 / 用户底色 / 降级计数一屏)。
//
// cwd 处理同 TestVideoDemoScript: 脚本按"从仓库根目录运行"编写
// (README 惯例 ./gox testdata/audio_demo.js), 测试进程的 cwd 是 gfx/。
func TestAudioDemoScript(t *testing.T) {
	startAudioTest(t)

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

	srcBytes, err := os.ReadFile(filepath.Join("testdata", "audio_demo.js"))
	if err != nil {
		t.Fatalf("读取演示脚本: %v", err)
	}
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(string(srcBytes))
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	appMu.Lock()
	root := activeApp.root
	appMu.Unlock()

	round := 0
	pump := func(maxWait time.Duration) bool {
		round++
		fake.push(Event{Kind: EventMouseLeave})
		if round > 1 {
			fake.push(Event{Kind: EventClose})
		}
		return Pump(maxWait)
	}
	if err := v.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if Active() {
		t.Fatalf("关闭窗口后应用未退出")
	}

	// ===== 降级: 没有平台音频层时, 两个框各报一次 unsupported =====
	want := "plain:unsupported,bg:unsupported"
	if got := strings.Join(jsStrings(t, v, "g_audioErrors"), ","); got != want {
		t.Fatalf("onError 序列 = %q, want %q", got, want)
	}
	// 计数写回 signal 之后, 底部那行提示该变成 2
	if !textContainsAny(root, "播不了的条: 2") {
		t.Fatalf("底部提示没跟上计数 (期望含 \"播不了的条: 2\")")
	}
	if !textContainsAny(root, "canIUse(\"audio\") = false") {
		t.Fatalf("底部该照实显示 canIUse(\"audio\") = false")
	}

	// ===== 几何: 显式尺寸原样生效 =====
	auds := findAll(root, "audio")
	if len(auds) != 2 {
		t.Fatalf("演示脚本应有 2 个 audio, 实际 %d", len(auds))
	}
	for i, wantBox := range []Rect{
		{X: 12, Y: 0, W: 300, H: 36},
		{X: 12, Y: 0, W: 300, H: 36},
	} {
		if b := auds[i].Box; b.W != wantBox.W || b.H != wantBox.H {
			t.Fatalf("第 %d 个 audio 盒子 = %dx%d, want %dx%d", i, b.W, b.H, wantBox.W, wantBox.H)
		}
	}
}

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

// ===== `<video>` 组件 (S8): 契约 + 降级 =====
//
// 这一组用例围绕"契约的每一句话都要能被证伪"来写, 覆盖三条互不重叠的路径:
//
//	1. 后端**没有** nativeVideoHost            → 降级: 一次进程级告警 + 每节点一次 onError
//	2. 后端**有** nativeVideoHost              → 挂载 / 跟随布局 / 指令只在变化时下发 / 释放
//	3. 后端有接口但 VideoInlineSupported=false → 与 1 同样的降级 (真后端里"能不能"是运行期才知道的)
//
// 加上两段纯函数 (fit 几何 / 归一化) 与一段逐像素断言 (封面 / contain 留边 /
// 用户 background 不被盖掉 / 禁用罩)。平台侧的"真解码真播放"不在范围里 ——
// 那正是 nativeVideoHost 交给宿主的部分。

// =====================================================================
// 假平台视频层: nativeVideoHost 的测试替身
// =====================================================================
//
// 与 fakeDialogHost 同一套路 —— 可选能力接口的附带好处: "平台播视频"这件事
// 在测试里退化成一笔可断言的数据 (挂载过几次 / 落在哪个矩形 / 发过哪些指令),
// 不需要真窗口、真解码器、真视频文件。

type fakeVideoAttach struct {
	Rect Rect
	Src  string
	Opts VideoSurfaceOptions
	Err  error // 非空 = 这次挂载失败了 (失败也记一笔: 要断言"每帧还在重试")
}

type fakeVideoRect struct {
	ID   uint64
	Rect Rect
}

type fakeVideoCommand struct {
	ID    uint64
	Cmd   VideoCommand
	Value float64
}

type fakeVideoHost struct {
	mu        sync.Mutex
	supported bool   // VideoInlineSupported 的返回值
	nextID    uint64 // 句柄分配器 (从 1 起, 0 保留给"还没挂")
	attachErr error  // 非空时 VideoSurfaceAttach 失败

	attachs  []fakeVideoAttach
	rects    []fakeVideoRect
	commands []fakeVideoCommand
	detaches []uint64
	live     map[uint64]string // 还挂着的表面 (Detach 后移除) —— 用来抓"泄漏"
}

func newFakeVideoHost() *fakeVideoHost {
	return &fakeVideoHost{supported: true, live: map[uint64]string{}}
}

func (f *fakeVideoHost) VideoInlineSupported() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.supported
}

func (f *fakeVideoHost) VideoSurfaceAttach(r Rect, src string, opts VideoSurfaceOptions) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.attachErr != nil {
		// 失败的尝试也记一笔 (但不进 live): 用例要断言"宿主还能恢复,
		// 所以内核每帧都在重试", 只记成功的调用就看不出这件事。
		f.attachs = append(f.attachs, fakeVideoAttach{Rect: r, Src: src, Opts: opts, Err: f.attachErr})
		return 0, f.attachErr
	}
	f.nextID++
	f.attachs = append(f.attachs, fakeVideoAttach{Rect: r, Src: src, Opts: opts})
	f.live[f.nextID] = src
	return f.nextID, nil
}

func (f *fakeVideoHost) VideoSurfaceRect(id uint64, r Rect) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rects = append(f.rects, fakeVideoRect{ID: id, Rect: r})
	return nil
}

func (f *fakeVideoHost) VideoSurfaceCommand(id uint64, cmd VideoCommand, value float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, fakeVideoCommand{ID: id, Cmd: cmd, Value: value})
	return nil
}

func (f *fakeVideoHost) VideoSurfaceDetach(id uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.detaches = append(f.detaches, id)
	delete(f.live, id)
	return nil
}

// ---- 读回 (全部返回副本: 断言拿到的切片不该被后续调用改写) ----

func (f *fakeVideoHost) attachCalls() []fakeVideoAttach {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeVideoAttach(nil), f.attachs...)
}

func (f *fakeVideoHost) rectCalls() []fakeVideoRect {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeVideoRect(nil), f.rects...)
}

func (f *fakeVideoHost) commandCalls() []fakeVideoCommand {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeVideoCommand(nil), f.commands...)
}

func (f *fakeVideoHost) detachCalls() []uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]uint64(nil), f.detaches...)
}

func (f *fakeVideoHost) liveIDs() []uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]uint64, 0, len(f.live))
	for id := range f.live {
		out = append(out, id)
	}
	return out
}

func (f *fakeVideoHost) lastID() uint64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nextID
}

// commandNames 返回从第 from 条起的指令名序列 (用它断言"这一轮到底发了什么",
// 比逐条比对 ID/Value 更贴近"重发会不会打断播放"这个问题)。
func (f *fakeVideoHost) commandNames(from int) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.commands))
	for _, c := range f.commands[minInt(from, len(f.commands)):] {
		out = append(out, string(c.Cmd))
	}
	return out
}

// ---- fakeSurface 侧的能力转发 (字段定义在 helpers_test.go) ----
//
// 没注入假宿主时就**不满足** nativeVideoHost 的语义 —— 返回 false 让
// videoHostOf 判为"没有这块能力" (真后端里 X11 / cocoa 现在就是这状态),
// 而不是靠类型断言失败。裸接口缺失那条路由 bareSurface 覆盖。

func (f *fakeSurface) VideoInlineSupported() bool {
	if f.video == nil {
		return false
	}
	return f.video.VideoInlineSupported()
}

func (f *fakeSurface) VideoSurfaceAttach(r Rect, src string, opts VideoSurfaceOptions) (uint64, error) {
	if f.video == nil {
		return 0, errors.New("test: 未注入假视频宿主")
	}
	return f.video.VideoSurfaceAttach(r, src, opts)
}

func (f *fakeSurface) VideoSurfaceRect(id uint64, r Rect) error {
	if f.video == nil {
		return errors.New("test: 未注入假视频宿主")
	}
	return f.video.VideoSurfaceRect(id, r)
}

func (f *fakeSurface) VideoSurfaceCommand(id uint64, cmd VideoCommand, value float64) error {
	if f.video == nil {
		return errors.New("test: 未注入假视频宿主")
	}
	return f.video.VideoSurfaceCommand(id, cmd, value)
}

func (f *fakeSurface) VideoSurfaceDetach(id uint64) error {
	if f.video == nil {
		return errors.New("test: 未注入假视频宿主")
	}
	return f.video.VideoSurfaceDetach(id)
}

// =====================================================================
// 设施: 挂载 / 状态读取
// =====================================================================

// startVideoTest 清空视频侧的包级状态 (跨用例串味的唯一来源: videoStates
// 与 videoWarned 都是包级变量), 并在用例结束时再清一次。
func startVideoTest(t *testing.T) {
	t.Helper()
	resetVideoStateForTest()
	t.Cleanup(resetVideoStateForTest)
}

// mountVideoApp 挂一个纯 Go 的 app, 且**先**把假视频宿主注入好 ——
// 首帧就带宿主才是真后端的形状。不这么做的话第一帧会先走一遍降级路径,
// 把节点的 reported 置真, 后面就再也看不到 attach 失败的那次告警了。
func mountVideoApp(t *testing.T, root *GuiNode, w, h int) (*fakeSurface, *app, *fakeVideoHost) {
	t.Helper()
	return mountVideoAppHost(t, root, w, h, newFakeVideoHost())
}

// mountVideoAppHost 同上, 但用调用方给的宿主 —— 需要预设 attachErr / supported
// 的用例走这条 (预设必须发生在首帧之前, 事后改宿主已经来不及了)。
func mountVideoAppHost(t *testing.T, root *GuiNode, w, h int, vh *fakeVideoHost) (*fakeSurface, *app, *fakeVideoHost) {
	t.Helper()
	fake := newFakeSurface()
	fake.video = vh
	a := mountTestAppWith(t, root, fake, w, h)
	return fake, a, vh
}

// bootVideoVM 起一个真 VM 并把假 Surface 交出去 —— 由调用方决定"首帧之前
// 宿主在不在", 因为这正是要验的自变量 (fake.video 必须在脚本执行前设好)。
func bootVideoVM(t *testing.T, src string, fake *fakeSurface) *vm.VM {
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

// videoStateOf 读节点的宿主侧状态快照 (没有条目返回 false)。
// 返回副本而不是指针: 断言读到的值不该被下一帧的 flush 改写。
func videoStateOf(n *GuiNode) (videoState, bool) {
	videoMu.Lock()
	defer videoMu.Unlock()
	st, ok := videoStates[n]
	if !ok {
		return videoState{}, false
	}
	return *st, true
}

// videoSurfaceID 取节点当前挂着的平台表面句柄 (0 = 没挂上)。
func videoSurfaceID(n *GuiNode) uint64 {
	st, _ := videoStateOf(n)
	return st.id
}

// videoReported 报告该节点是否已经因"没有平台视频层 / 挂载失败"报过一次。
func videoReported(n *GuiNode) bool {
	st, ok := videoStateOf(n)
	return ok && st.reported
}

// jsStrings 取一个脚本全局数组里的字符串项。
func jsStrings(t *testing.T, v *vm.VM, name string) []string {
	t.Helper()
	arr := jsArray(t, v, name)
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

// =====================================================================
// 纯函数: fit 几何
// =====================================================================

// TestVideoFitRect contain 是缺省, cover 允许返回**比盒更大**的矩形
// (多出来的部分由 blitNearest 的脏区裁剪吃掉) —— 这两条都不是"显然"的,
// 所以逐条钉住几何值。
func TestVideoFitRect(t *testing.T) {
	box := Rect{X: 10, Y: 20, W: 100, H: 100}
	cases := []struct {
		name   string
		sw, sh int
		mode   string
		want   Rect
	}{
		{"contain 宽图: 上下留黑", 200, 100, "contain", Rect{X: 10, Y: 45, W: 100, H: 50}},
		{"contain 高图: 左右留黑", 100, 200, "contain", Rect{X: 35, Y: 20, W: 50, H: 100}},
		{"cover 宽图: 长边溢出左右", 200, 100, "cover", Rect{X: -40, Y: 20, W: 200, H: 100}},
		{"cover 高图: 长边溢出上下", 100, 200, "cover", Rect{X: 10, Y: -30, W: 100, H: 200}},
		{"fill: 铺满整盒", 200, 100, "fill", box},
		{"方图进方盒: 恰好占满", 100, 100, "contain", box},
	}
	for _, c := range cases {
		got, ok := videoFitRect(box, c.sw, c.sh, c.mode)
		if !ok {
			t.Fatalf("%s: 不该退化, 却返回 ok=false", c.name)
		}
		if got != c.want {
			t.Fatalf("%s: %v, want %v", c.name, got, c.want)
		}
	}

	// 退化的输入必须返回 ok=false (调用方据此跳过绘制, 而不是画一条 0 宽的线)
	for _, c := range []struct {
		name   string
		box    Rect
		sw, sh int
	}{
		{"盒宽为 0", Rect{0, 0, 0, 50}, 10, 10},
		{"盒高为 0", Rect{0, 0, 50, 0}, 10, 10},
		{"源宽为 0", Rect{0, 0, 50, 50}, 0, 10},
		{"源高为 0", Rect{0, 0, 50, 50}, 10, 0},
	} {
		if _, ok := videoFitRect(c.box, c.sw, c.sh, "contain"); ok {
			t.Fatalf("%s: 应返回 ok=false", c.name)
		}
	}
}

// TestVideoFitMode fit 词表: 不认识的**不猜**, 一律落回 contain。
func TestVideoFitMode(t *testing.T) {
	cases := map[string]string{
		"contain": "contain",
		"cover":   "cover",
		"fill":    "fill",
		"stretch": "fill", // 别名
		"CONTAIN": "contain",
		"bogus":   "contain",
	}
	for in, want := range cases {
		n := mkNode("video", nil)
		if in != "" {
			withStr(n, "fit", in)
		}
		if got := videoFit(n); got != want {
			t.Fatalf("videoFit(fit=%q) = %q, want %q", in, got, want)
		}
	}
}

// =====================================================================
// 纯函数: 固有尺寸
// =====================================================================

// TestVideoIntrinsicSize 没封面没尺寸要落 16:9 兜底 —— **必须非 0**,
// 0 尺寸子树会被 drawNode 整支跳过 (症状是"这里什么都没有"而不是"这里该有视频")。
func TestVideoIntrinsicSize(t *testing.T) {
	plain := mkNode("video", nil)
	if w, h := plain.intrinsicSize(); w != videoDefaultW || h != videoDefaultH {
		t.Fatalf("无封面无尺寸应为 %dx%d, 实际 %dx%d", videoDefaultW, videoDefaultH, w, h)
	}

	poster := writeTestImage(t, "poster.png") // 2x2 四象限图
	withPoster := withStr(mkNode("video", nil), "poster", poster)
	if w, h := withPoster.intrinsicSize(); w != 2 || h != 2 {
		t.Fatalf("有封面应取封面自然尺寸 2x2, 实际 %dx%d", w, h)
	}

	// 显式尺寸优先, 只给一轴时另一轴补封面的自然值
	nw := withStr(mkNode("video", map[string]float64{"width": 40}), "poster", poster)
	if w, h := nw.intrinsicSize(); w != 40 || h != 2 {
		t.Fatalf("显式宽 40 + 封面自然高 2, 实际 %dx%d", w, h)
	}

	// 封面路径坏掉 → 回到 16:9 兜底 (不是 0, 也不 panic)
	broken := withStr(mkNode("video", nil), "poster", filepath.Join(t.TempDir(), "nope.png"))
	if w, h := broken.intrinsicSize(); w != videoDefaultW || h != videoDefaultH {
		t.Fatalf("封面加载失败应落兜底 %dx%d, 实际 %dx%d", videoDefaultW, videoDefaultH, w, h)
	}
}

// =====================================================================
// 降级: 没有平台视频层
// =====================================================================

// TestVideoNoHostCapability 三种"没有能力"要分清:
// 没挂窗口 / 后端不实现 nativeVideoHost / 后端实现了但自己说不支持。
func TestVideoNoHostCapability(t *testing.T) {
	startVideoTest(t)

	// 1. 裸后端 (连接口都没实现)
	if _, ok := any(&bareSurface{}).(nativeVideoHost); ok {
		t.Fatalf("bareSurface 不该实现 nativeVideoHost")
	}

	// 2. 挂了窗口, 但 fakeSurface 没注入视频宿主 = 这个后端没有平台视频层
	root := mkNode("column", nil)
	mountChildren(root, withStr(mkNode("video", map[string]float64{"width": 80, "height": 45}), "src", "a.mp4"))
	fake, a := mountTestApp(t, root, 200, 150)
	if VideoInlineSupported() {
		t.Fatalf("没注入宿主时 VideoInlineSupported() 应为 false")
	}
	if h, ok := videoHostOf(a); ok || h != nil {
		t.Fatalf("没注入宿主时 videoHostOf 该判为不可用")
	}

	// 3. 后端实现了接口, 但自己报告"不支持"
	vh := newFakeVideoHost()
	vh.supported = false
	fake.video = vh
	if VideoInlineSupported() {
		t.Fatalf("宿主自报不支持时 VideoInlineSupported() 应为 false")
	}
}

// TestVideoDowngradeWarnsOnceReportsPerNode 降级的两条硬要求:
// 告警**按进程**只出一次 (否则界面里 10 个视频框会把 stderr 刷爆),
// onError **按节点**各报一次 (脚本要靠它把每个框都换成封面/提示)。
func TestVideoDowngradeWarnsOnceReportsPerNode(t *testing.T) {
	startVideoTest(t)

	var warned []string
	oldWarn := warnVideoNoHost
	warnVideoNoHost = func(src string) { warned = append(warned, src) }
	defer func() { warnVideoNoHost = oldWarn }()

	root := mkNode("column", nil)
	v1 := withStr(mkNode("video", map[string]float64{"width": 80, "height": 45}), "src", "a.mp4")
	v2 := withStr(mkNode("video", map[string]float64{"width": 80, "height": 45}), "src", "b.mp4")
	// 没写 src 的盒子连"想播放"都不算: 不打扰 (还没准备好而已)
	quiet := mkNode("video", map[string]float64{"width": 80, "height": 45})
	mountChildren(root, v1, v2, quiet)

	_, a := mountTestApp(t, root, 200, 150) // 没注入宿主 ⇒ 首帧即降级
	a.redraw()
	a.redraw()

	if len(warned) != 1 {
		t.Fatalf("进程级告警应只出一次, 实际 %d 次: %v", len(warned), warned)
	}
	if !videoReported(v1) || !videoReported(v2) {
		t.Fatalf("写了 src 的节点都该各报一次 onError (v1=%v v2=%v)",
			videoReported(v1), videoReported(v2))
	}
	if videoReported(quiet) {
		t.Fatalf("没写 src 的节点不该收到 onError")
	}
	if id := videoSurfaceID(v1); id != 0 {
		t.Fatalf("降级路径不该挂表面, 实际 id=%d", id)
	}
}

// TestVideoAttachFailureReportedOnce 宿主挂载报错时: 每帧**继续重试**
// (宿主可能刚好那一刻拿不到资源), 但告警与 onError 各只出一次。
func TestVideoAttachFailureReportedOnce(t *testing.T) {
	startVideoTest(t)

	var warned []string
	oldWarn := warnVideoAttach
	warnVideoAttach = func(src string, err error) { warned = append(warned, src) }
	defer func() { warnVideoAttach = oldWarn }()

	root := mkNode("column", nil)
	v := withStr(mkNode("video", map[string]float64{"width": 80, "height": 45}), "src", "a.mp4")
	mountChildren(root, v)

	// 宿主必须在**首帧之前**就报"挂不上" —— 事后才改 attachErr 的话首帧已经
	// 挂成功了, 这条路径根本不会走到。
	vh := newFakeVideoHost()
	vh.attachErr = errors.New("test: 没有解码器")
	_, a, vh := mountVideoAppHost(t, root, 200, 150, vh)

	a.redraw()
	a.redraw()

	if len(warned) != 1 {
		t.Fatalf("挂载失败应只告警一次, 实际 %d 次: %v", len(warned), warned)
	}
	// 失败也仍然每帧重试 (宿主下一帧可能就好了), 但一次都没成功
	if n := len(vh.attachCalls()); n != 3 { // 首帧 + 上面两次 redraw
		t.Fatalf("挂载失败该每帧重试, 实际只尝试了 %d 次", n)
	}
	if got := vh.liveIDs(); len(got) != 0 {
		t.Fatalf("失败的挂载不该留下活着的表面: %v", got)
	}
	if id := videoSurfaceID(v); id != 0 {
		t.Fatalf("挂载失败时句柄应为 0, 实际 %d", id)
	}
	if !videoReported(v) {
		t.Fatalf("挂载失败该对本报一次 onError")
	}
}

// =====================================================================
// 契约: 挂载 / 跟随布局 / 指令去重 / 释放
// =====================================================================

// mkVideoNode 造一个 video 节点 (src 是字符串属性, mkNode 只收数值)。
func mkVideoNode(src string, props map[string]float64) *GuiNode {
	return withStr(mkNode("video", props), "src", src)
}

// TestVideoSurfaceLifecycle 走完整的一条命: 首帧挂上 → 受控属性变化只发变化
// 的那条指令 → 布局变了补一次位置 → 节点离开树立刻摘掉表面。
func TestVideoSurfaceLifecycle(t *testing.T) {
	startVideoTest(t)

	root := mkNode("column", nil)
	// 显式尺寸: 位置变化的断言要有确定效果, 就不能让盒子交给"父容器拉伸"
	// 这条隐式规则 (拉伸与否取决于标签, 改窗口大小不一定改盒子)
	v := mkVideoNode("movie.mp4", map[string]float64{"width": 80, "height": 45})
	withBool(v, "playing", true)
	withBool(v, "controls", true)
	mountChildren(root, v)

	fake, a, vh := mountVideoApp(t, root, 400, 300)

	att := vh.attachCalls()
	if len(att) != 1 {
		t.Fatalf("首帧应挂一次平台表面, 实际 %d 次", len(att))
	}
	if att[0].Src != "movie.mp4" {
		t.Fatalf("挂载的 src = %q, want %q", att[0].Src, "movie.mp4")
	}
	if att[0].Rect != v.Box {
		t.Fatalf("挂载矩形应等于节点盒 %v, 实际 %v", v.Box, att[0].Rect)
	}
	if att[0].Rect.W <= 0 || att[0].Rect.H <= 0 {
		t.Fatalf("挂载矩形不该退化: %v", att[0].Rect)
	}
	if !att[0].Opts.Controls {
		t.Fatalf("controls 该传给宿主")
	}
	if att[0].Opts.Volume != 1 || att[0].Opts.Muted {
		t.Fatalf("缺省音量 1 且不静音, 实际 volume=%v muted=%v", att[0].Opts.Volume, att[0].Opts.Muted)
	}
	id := vh.lastID()
	if sid := videoSurfaceID(v); sid != id {
		t.Fatalf("节点记录的句柄 = %d, 宿主分配的是 %d", sid, id)
	}
	// 挂载是 force 全量: play / mute / loop / volume 四条都要下发
	if got := strings.Join(vh.commandNames(0), ","); got != "play,mute,loop,volume" {
		t.Fatalf("挂载时的指令序列 = %q, want %q", got, "play,mute,loop,volume")
	}
	if got := len(vh.rectCalls()); got != 0 {
		t.Fatalf("位置刚随挂载给过, 不该再单独发一次 rect, 实际 %d 次", got)
	}

	// 受控属性变化: 只发变化的那一条 (重发 play/pause 会打断正在进行的播放)
	from := len(vh.commandCalls())
	withBool(v, "playing", false)
	a.redraw()
	if got := strings.Join(vh.commandNames(from), ","); got != "pause" {
		t.Fatalf("只改 playing 时该只发 pause, 实际 %q", got)
	}

	// 值没变: 一条都不该发
	from = len(vh.commandCalls())
	a.redraw()
	if got := vh.commandNames(from); len(got) != 0 {
		t.Fatalf("无变化时不该下发任何指令, 实际 %v", got)
	}

	// 盒子变了 → 补一次位置, 且不重发受控指令
	from = len(vh.commandCalls())
	rectFrom := len(vh.rectCalls())
	fake.w, fake.h = 200, 120
	withNum(v, "width", 120)
	a.redraw()
	if v.Box.W != 120 {
		t.Fatalf("显式宽 120 该生效, 实际盒宽 %d", v.Box.W)
	}
	if got := len(vh.rectCalls()); got != rectFrom+1 {
		t.Fatalf("盒子变化该补一次 VideoSurfaceRect, 实际 %d 次", got-rectFrom)
	}
	if r := vh.rectCalls()[rectFrom]; r.ID != id || r.Rect != v.Box {
		t.Fatalf("rect 调用 = id %d %v, want id %d %v", r.ID, r.Rect, id, v.Box)
	}
	if got := vh.commandNames(from); len(got) != 0 {
		t.Fatalf("只是位置变了不该重发受控指令, 实际 %v", got)
	}

	// 节点整支从树上消失 (each 重建 / 条件分支翻面) → 表面必须摘掉
	root.Children = nil
	a.redraw()
	if got := vh.detachCalls(); len(got) != 1 || got[0] != id {
		t.Fatalf("离开树的表面应被摘掉, detach=%v (id=%d)", got, id)
	}
	if live := vh.liveIDs(); len(live) != 0 {
		t.Fatalf("摘掉后不该还有活着的表面: %v", live)
	}
	if _, ok := videoStateOf(v); ok {
		t.Fatalf("离开树后状态表里不该还留着条目")
	}
}

// TestVideoDisposeReleasesSurface 节点被 dispose 时**立刻**释放表面, 不等下一次
// flush —— 子树销毁后可能再也没有重绘 (没有脏区就不进 redraw), 那块表面会一直
// 浮在窗口上关不掉。
func TestVideoDisposeReleasesSurface(t *testing.T) {
	startVideoTest(t)

	root := mkNode("column", nil)
	v := mkVideoNode("movie.mp4", map[string]float64{"width": 80, "height": 45})
	mountChildren(root, v)

	_, _, vh := mountVideoApp(t, root, 400, 300)
	id := vh.lastID()
	if id == 0 {
		t.Fatalf("首帧该挂上表面")
	}

	disposeNode(v)

	if got := vh.detachCalls(); len(got) != 1 || got[0] != id {
		t.Fatalf("dispose 应立刻摘掉表面, detach=%v (id=%d)", got, id)
	}
	if _, ok := videoStateOf(v); ok {
		t.Fatalf("dispose 后状态表该清干净")
	}
}

// TestVideoNoSrcOrZeroBoxReleases 没写 src 或盒子退化的节点不该挂表面;
// **已经挂上的**要摘掉 —— 否则屏幕上会留下一块"界面里已经没有对应元素"的画面。
func TestVideoNoSrcOrZeroBoxReleases(t *testing.T) {
	startVideoTest(t)

	root := mkNode("column", nil)
	v := mkVideoNode("movie.mp4", map[string]float64{"width": 80, "height": 45})
	mountChildren(root, v)

	_, a, vh := mountVideoApp(t, root, 400, 300)
	if vh.lastID() == 0 {
		t.Fatalf("首帧该挂上表面")
	}

	// src 被脚本清掉 (源换成空串 / 还没加载出来) → 表面摘掉
	withStr(v, "src", "")
	a.redraw()
	if len(vh.detachCalls()) != 1 {
		t.Fatalf("src 清零后该摘掉表面, detach=%v", vh.detachCalls())
	}
	if _, ok := videoStateOf(v); ok {
		t.Fatalf("摘掉后状态表该清干净")
	}
	// 再写回来 → 重新挂上 (不是"一次性"的)
	withStr(v, "src", "movie.mp4")
	a.redraw()
	if n := len(vh.attachCalls()); n != 2 {
		t.Fatalf("src 写回来该重新挂载, 实际累计 %d 次", n)
	}

	// 盒子退化同理: 摘掉, 不留浮层。
	// **不能用"把窗口拖到 0 尺寸"来造这个状态** —— redraw 在 w/h<=0 时直接
	// 返回, 根本走不到 flush。真会出现的形状是"窗口正常、这个节点没有盒子"
	// (被隐藏的祖先 / 条件分支里整支跳过), 所以直接把这个状态摆出来再跑 flush。
	v.Box = Rect{}
	flushVideoSurfaces(a)
	if n := len(vh.detachCalls()); n != 2 {
		t.Fatalf("盒子退化该摘掉表面, detach=%v", vh.detachCalls())
	}
	if _, ok := videoStateOf(v); ok {
		t.Fatalf("盒子退化后状态表该清干净")
	}
}

// =====================================================================
// 宿主 → 脚本: 事件回填 (真 VM)
// =====================================================================

// videoEventScript 是一段最小可用的 `<video>` 脚本: 把每个回调都记进
// globalThis.g_events, 便于 Go 侧断言"哪个事件到了、带什么参数、顺序对不对"。
//
// canIUse("video") 放在 render **之后**: 它问的是"当前窗口有没有这块能力",
// 窗口还没挂上时问是不算数的 (会拿到上一次会话的窗口)。
const videoEventScript = `
import { h, render } from "gx/gfx";
import { canIUse } from "gx/device";

globalThis.g_events = [];

render(
  h("window", { title: "video", width: 400, height: 300 },
    h("column", { padding: 10 },
      h("video", {
        src: "movie.mp4",
        width: 200,
        height: 112,
        onReady: () => globalThis.g_events.push("ready"),
        onPlay: () => globalThis.g_events.push("play"),
        onPause: () => globalThis.g_events.push("pause"),
        onEnded: () => globalThis.g_events.push("ended"),
        onTimeUpdate: (e) => globalThis.g_events.push("t:" + e.currentTime + "/" + e.duration),
        onError: (e) => globalThis.g_events.push("err:" + e.code),
      }))));

globalThis.g_canuse = canIUse("video");
`

// TestVideoEventsReachScript 有宿主时: ResolveVideoEvent 必须把事件落到脚本的
// 对应回调上, 句柄对不上/为 0 的静默丢弃。
func TestVideoEventsReachScript(t *testing.T) {
	startVideoTest(t)

	fake := newFakeSurface()
	vh := newFakeVideoHost()
	fake.video = vh
	v := bootVideoVM(t, videoEventScript, fake)

	runPumpSteps(t, v, fake, []func(){
		func() {}, // 首帧: 把平台表面挂上
		func() {
			att := vh.attachCalls()
			if len(att) != 1 {
				t.Fatalf("首帧应挂一次表面, 实际 %d 次", len(att))
			}
			if att[0].Src != "movie.mp4" {
				t.Fatalf("挂载的 src = %q", att[0].Src)
			}
			id := vh.lastID()
			ResolveVideoEvent(id, VideoEvent{Kind: "ready"})
			ResolveVideoEvent(id, VideoEvent{Kind: "play"})
			ResolveVideoEvent(id, VideoEvent{Kind: "timeupdate", CurrentTime: 1.5, Duration: 12})
			ResolveVideoEvent(id, VideoEvent{Kind: "ended"})
			// 句柄对不上 / 句柄为 0: 事件在路上而界面已经不要它了, 静默丢弃
			ResolveVideoEvent(id+999, VideoEvent{Kind: "play"})
			ResolveVideoEvent(0, VideoEvent{Kind: "play"})
		},
		func() {
			want := "ready,play,t:1.5/12,ended"
			if got := strings.Join(jsStrings(t, v, "g_events"), ","); got != want {
				t.Fatalf("脚本收到的事件 = %q, want %q", got, want)
			}
			if ok, isBool := globalBool(t, v, "g_canuse"); !isBool || !ok {
				t.Fatalf("有宿主时 canIUse(\"video\") 应为 true, 实际 %v/%v", ok, isBool)
			}
		},
	})
}

// TestVideoHostErrorReachesScript 宿主上报 error: 没给 code 时要落一个明确
// 的兜底码, 而不是空串 (脚本按 code 分支处理)。
func TestVideoHostErrorReachesScript(t *testing.T) {
	startVideoTest(t)

	fake := newFakeSurface()
	vh := newFakeVideoHost()
	fake.video = vh
	v := bootVideoVM(t, videoEventScript, fake)

	runPumpSteps(t, v, fake, []func(){
		func() {},
		func() {
			id := vh.lastID()
			ResolveVideoEvent(id, VideoEvent{Kind: "error", Code: "decode-failed", Message: "坏文件"})
			ResolveVideoEvent(id, VideoEvent{Kind: "error", Message: "宿主没说原因"})
		},
		func() {
			want := "err:decode-failed,err:playback-failed"
			if got := strings.Join(jsStrings(t, v, "g_events"), ","); got != want {
				t.Fatalf("脚本收到的事件 = %q, want %q", got, want)
			}
		},
	})
}

// TestVideoDowngradeReachesScript 没有平台视频层时, 脚本必须收到
// `onError {code:"unsupported"}` —— 因为**无法假装在播**才是这条降级的全部要点。
// 同时 canIUse 要照实回答 false, 让脚本能"先问再选路"(退回系统播放器)。
func TestVideoDowngradeReachesScript(t *testing.T) {
	cases := []struct {
		name string
		// prepare 决定 fake 的能力状态: nil = 后端没有这块能力
		prepare func(fake *fakeSurface)
	}{
		{"后端不实现 nativeVideoHost", func(fake *fakeSurface) {}},
		{"后端实现了但自报不支持", func(fake *fakeSurface) {
			vh := newFakeVideoHost()
			vh.supported = false
			fake.video = vh
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			startVideoTest(t)

			fake := newFakeSurface()
			c.prepare(fake)
			v := bootVideoVM(t, videoEventScript, fake)

			runPumpSteps(t, v, fake, []func(){
				func() {},
				func() {
					want := "err:unsupported"
					if got := strings.Join(jsStrings(t, v, "g_events"), ","); got != want {
						t.Fatalf("降级事件 = %q, want %q", got, want)
					}
					if ok, isBool := globalBool(t, v, "g_canuse"); !isBool || ok {
						t.Fatalf("无能力时 canIUse(\"video\") 应为 false, 实际 %v/%v", ok, isBool)
					}
				},
			})
		})
	}
}

// =====================================================================
// 绘制: 封面 / contain 留边 / 用户 background / 禁用罩
// =====================================================================

// TestVideoPaintPlaceholder 没封面时画深色底 + 播放三角 —— 三角不能占满整盒
// (那样就成了一块纯色), 也不能小到看不见。
func TestVideoPaintPlaceholder(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.png")
	n := withStr(mkVideoNode("movie.mp4", map[string]float64{"width": 200, "height": 100}), "poster", missing)
	n.Box = Rect{X: 0, Y: 0, W: 200, H: 100}

	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	paintVideo(img, n, false)

	if got := img.RGBAAt(2, 2); got != colorVideoBackdrop {
		t.Fatalf("没封面时应铺深色底, 左上角实际 %v, want %v", got, colorVideoBackdrop)
	}
	// 三角画在盒子中间: 左右两端的竖条区不该有它
	if c := countColor(img, Rect{X: 0, Y: 0, W: 8, H: 100}, colorVideoGlyph); c != 0 {
		t.Fatalf("播放三角不该顶到盒子左边缘 (实际 %d 个像素)", c)
	}
	if c := countColor(img, Rect{X: 80, Y: 30, W: 40, H: 40}, colorVideoGlyph); c == 0 {
		t.Fatalf("盒子中间该有播放三角")
	}
}

// TestVideoPaintPosterContain 封面按 contain 落位: 留边铺黑, 画面上四角
// 分别对应源图的四角 (最近邻采样, 用四角断言能同时抓住"缩放比反了"与
// "行列对调"两种错位)。
func TestVideoPaintPosterContain(t *testing.T) {
	poster := writeTestImage(t, "poster.png")
	src := mustLoadImage(t, poster).img

	// 源 1:1 进 2:1 的盒子 ⇒ 按高缩放, 左右各留 25px
	n := withStr(mkVideoNode("movie.mp4", map[string]float64{"width": 100, "height": 50}), "poster", poster)
	n.Box = Rect{X: 0, Y: 0, W: 100, H: 50}

	img := image.NewRGBA(image.Rect(0, 0, 100, 50))
	paintVideo(img, n, false)

	assertPx(t, img, 0, 0, colorVideoLetter, "contain 左留边")
	assertPx(t, img, 99, 49, colorVideoLetter, "contain 右留边")
	assertPx(t, img, 25, 0, src.RGBAAt(0, 0), "封面左上")
	assertPx(t, img, 74, 0, src.RGBAAt(1, 0), "封面右上")
	assertPx(t, img, 25, 49, src.RGBAAt(0, 1), "封面左下")
	assertPx(t, img, 74, 49, src.RGBAAt(1, 1), "封面右下")
}

// TestVideoPaintKeepsUserBackground 用户显式写了 background 时, 底色归用户:
// 深色底与 contain 留边都不许盖上去 —— 这是"被 case 截走的标签要在自己分支里
// 补画装饰"那条纪律的另一半 (补画了, 但不能反过来盖掉用户的东西)。
func TestVideoPaintKeepsUserBackground(t *testing.T) {
	bg := mustColor(t, "#123456")

	// 1. 没封面: 不铺深色底
	missing := filepath.Join(t.TempDir(), "nope.png")
	n := withStr(mkVideoNode("movie.mp4", map[string]float64{"width": 60, "height": 40}), "poster", missing)
	withStr(n, "background", "#123456")
	n.Box = Rect{X: 0, Y: 0, W: 60, H: 40}
	img := image.NewRGBA(image.Rect(0, 0, 60, 40))
	paintVideo(img, n, false)
	assertPx(t, img, 1, 1, bg, "没封面时该露出用户的 background")
	if c := countColor(img, n.Box, colorVideoBackdrop); c != 0 {
		t.Fatalf("不该铺深色底 (实际 %d 个像素)", c)
	}

	// 2. 有封面 + contain: 留边也不铺黑
	poster := writeTestImage(t, "poster.png")
	n2 := withStr(mkVideoNode("movie.mp4", map[string]float64{"width": 100, "height": 50}), "poster", poster)
	withStr(n2, "background", "#123456")
	n2.Box = Rect{X: 0, Y: 0, W: 100, H: 50}
	img2 := image.NewRGBA(image.Rect(0, 0, 100, 50))
	paintVideo(img2, n2, false)
	assertPx(t, img2, 0, 0, bg, "contain 留边该露出用户的 background")
	if c := countColor(img2, n2.Box, colorVideoLetter); c != 0 {
		t.Fatalf("写了 background 就不该再铺黑边 (实际 %d 个像素)", c)
	}
}

// TestVideoDisabledVeil 禁用罩要真的改变像素 (否则"禁用了"看不出来),
// 且罩下仍能看见内容 (半透明, 不是刷成一块灰)。
func TestVideoDisabledVeil(t *testing.T) {
	poster := writeTestImage(t, "poster.png")
	n := withStr(mkVideoNode("movie.mp4", map[string]float64{"width": 40, "height": 40}), "poster", poster)
	n.Box = Rect{X: 0, Y: 0, W: 40, H: 40}

	plain := image.NewRGBA(image.Rect(0, 0, 40, 40))
	paintVideo(plain, n, false)
	veiled := image.NewRGBA(image.Rect(0, 0, 40, 40))
	paintVideo(veiled, n, true)

	if veiled.RGBAAt(0, 0) == plain.RGBAAt(0, 0) {
		t.Fatalf("禁用罩没改变像素: %v", veiled.RGBAAt(0, 0))
	}
	// 半透明: 底下那层红色还在 (罩层是灰, 不会把 R 压到 0)
	if veiled.RGBAAt(0, 0).R == 0 {
		t.Fatalf("禁用罩该是半透明的, 底下内容不该被抹平: %v", veiled.RGBAAt(0, 0))
	}
}

// TestVideoDegenerateBoxNoPanic 0 尺寸盒不该 panic (窗口被拖到 0 尺寸、
// 或还没布局时的中间态都会走到这)。
func TestVideoDegenerateBoxNoPanic(t *testing.T) {
	n := mkVideoNode("movie.mp4", nil)
	n.Box = Rect{}
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	paintVideo(img, n, false) // 不 panic 即通过

	// 只有一轴为 0 也一样
	n.Box = Rect{X: 0, Y: 0, W: 10, H: 0}
	paintVideo(img, n, false)
}

// =====================================================================
// 演示脚本
// =====================================================================

// TestVideoDemoScript 跑一遍 S8 的演示脚本 —— 它是 `<video>` 的真实 JSX 用法
// (封面 / 占位 / fit + 用户 background 三种形态一屏)。
//
// 与 image_demo 同一情形: 脚本里的 `poster` 是**相对文件路径**, 按"从仓库根
// 目录运行"编写 (README 的惯例是 `./gox testdata/video_demo.js`), 而测试进程
// 的 cwd 是 `gfx/` ⇒ 临时切到仓库根再跑 (gfx 包内没有并行用例, 切换是安全的;
// 断言全部在恢复 cwd 之前完成)。
func TestVideoDemoScript(t *testing.T) {
	startVideoTest(t)

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

	srcBytes, err := os.ReadFile(filepath.Join("testdata", "video_demo.js"))
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
		// 每轮补一个无害唤醒事件: 队列为空时事件泵会按 WaitEvents 的语义长阻塞。
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

	// ===== 降级: 没有平台视频层时, 三个框各报一次 unsupported =====
	want := "cover:unsupported,plain:unsupported,coverbg:unsupported"
	if got := strings.Join(jsStrings(t, v, "g_videoErrors"), ","); got != want {
		t.Fatalf("onError 序列 = %q, want %q", got, want)
	}
	// 计数写回 signal 之后, 底部那行提示该变成 3
	if !textContainsAny(root, "播不了的框: 3") {
		t.Fatalf("底部提示没跟上计数 (期望含 \"播不了的框: 3\")")
	}
	if !textContainsAny(root, "canIUse(\"video\") = false") {
		t.Fatalf("底部该照实显示 canIUse(\"video\") = false")
	}

	// ===== 几何: 显式尺寸原样生效 =====
	vids := findAll(root, "video")
	if len(vids) != 3 {
		t.Fatalf("演示脚本应有 3 个 video, 实际 %d", len(vids))
	}
	for i, wantBox := range []Rect{
		{X: 12, Y: 0, W: 316, H: 178},
		{X: 12, Y: 0, W: 316, H: 178},
		{X: 12, Y: 0, W: 316, H: 100},
	} {
		if b := vids[i].Box; b.W != wantBox.W || b.H != wantBox.H {
			t.Fatalf("第 %d 个 video 盒子 = %dx%d, want %dx%d", i, b.W, b.H, wantBox.W, wantBox.H)
		}
	}

	// ===== 像素: 自己画一帧 (画布给足高度, 免得被窗口高度裁掉) =====
	const cw, ch = 340, 720
	img := image.NewRGBA(image.Rect(0, 0, cw, ch))
	FillRect(img, Rect{0, 0, cw, ch}, pxWhite)
	Layout(root, cw, ch)
	Draw(img, root)

	srcImg := mustLoadImage(t, filepath.Join("testdata", "image_demo.png")).img

	// 1. 封面 contain: 源是 1:1、盒是 16:9 ⇒ 左右留黑, 封面居中
	b0 := vids[0].Box
	assertPx(t, img, b0.X+2, b0.Y+2, colorVideoLetter, "contain 左留边")
	assertPx(t, img, b0.X+b0.W-3, b0.Y+b0.H-3, colorVideoLetter, "contain 右留边")
	assertPx(t, img, b0.X+(b0.W-b0.H)/2+1, b0.Y+1, srcImg.RGBAAt(0, 0), "封面左上")

	// 2. 没封面: 深色底 + 居中播放三角
	b1 := vids[1].Box
	assertPx(t, img, b1.X+2, b1.Y+2, colorVideoBackdrop, "无封面铺深色底")
	if c := countColor(img, b1, colorVideoGlyph); c == 0 {
		t.Fatalf("无封面该画播放三角")
	}

	// 3. cover + 显式 background: 封面裁满整盒 ⇒ 既看不到留边也看不到底色
	b2 := vids[2].Box
	if c := countColor(img, b2, colorVideoLetter); c != 0 {
		t.Fatalf("cover 不该留边 (实际 %d 个像素)", c)
	}
	if c := countColor(img, b2, colorVideoBackdrop); c != 0 {
		t.Fatalf("cover 已铺满, 不该露出深色底 (实际 %d 个像素)", c)
	}
	if c := countColor(img, b2, mustColor(t, "#123456")); c != 0 {
		t.Fatalf("封面铺满时用户的 background 该被盖住 (实际 %d 个像素)", c)
	}
}

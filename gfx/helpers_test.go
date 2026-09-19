package gfx

// ===== 共享测试设施 (全包唯一的定义点) =====
//
// 此前这些 helper 散落在 p0/p1/p2b/p2c/pipeline/view/resource/window/
// clipboard/dialog/menu/slider 各测试文件里, 多处近似重复 (7 份 VM 引导、
// 9 份事件泵驱动、10+ 个全局取值器)。现在统一收敛到这里; 组件专属的
// builder (mkSelect/mkInput/mkScroll...) 仍留在各自的组件测试文件里。
//
// 迁移纪律: 定义从这里搬走时只做搬移, 不改语义; 断言口径的注释随定义走。

import (
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// =====================================================================
// 假 Surface 家族: 无真窗口的测试后端
// =====================================================================

// fakeSurface 是 Surface 的测试替身: 无真窗口, 事件由测试注入。
type fakeSurface struct {
	events  chan Event
	arrived chan struct{} // WaitEvents 的唤醒信号 (不消费 events)
	mu      sync.Mutex
	img     *image.RGBA
	w, h    int
	shown   int
	regions []image.Rectangle // 最近一次 ShowRegions 的区域 (nil=整帧)
	title   string            // 最近一次 SetTitle 的值 (窗口 API 断言用)

	// dialog 非空时, fakeSurface 就同时满足 nativeDialogHost 可选接口 (P3-4)。
	// 字段放在这里而不是各测试文件里加包装类型, 是为了让"注入假原生框"
	// 与其它可选接口 (剪贴板等) 用同一种写法。
	dialog *fakeDialogHost
}

// SetTitle / ResizeClient 实现 windowController 可选接口: 记录标题;
// 改尺寸并像真后端一样投递 EventResize (resize → onResize 全链可测)。
func (f *fakeSurface) SetTitle(title string) {
	f.mu.Lock()
	f.title = title
	f.mu.Unlock()
}

func (f *fakeSurface) ResizeClient(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	f.mu.Lock()
	f.w, f.h = w, h
	f.mu.Unlock()
	f.push(Event{Kind: EventResize, W: w, H: h})
}

// ShowMessage 转发给注入的假原生框 (未注入时返回错误 = "后端不支持")。
func (f *fakeSurface) ShowMessage(kind NativeDialogKind, title, message string) (bool, error) {
	if f.dialog == nil {
		return false, errors.New("test: 未注入假对话框")
	}
	return f.dialog.ShowMessage(kind, title, message)
}

// ShowOpenFile 同上。
func (f *fakeSurface) ShowOpenFile(opts NativeFileOptions) (string, bool, error) {
	if f.dialog == nil {
		return "", false, errors.New("test: 未注入假对话框")
	}
	return f.dialog.ShowOpenFile(opts)
}

// ReadClipboardText / WriteClipboardText 是假 Surface 的 clipboardHost 实现
// (测试专用; 真后端在 gfx/win32)。刻意**不碰系统剪贴板**: CI 上没有剪贴板
// 所有者, 在开发机上跑测试改掉用户正在用的剪贴板更是不可接受。
func (f *fakeSurface) ReadClipboardText() (string, error) {
	clipMu.Lock()
	defer clipMu.Unlock()
	if clipFail {
		return "", errors.New("clipboard busy")
	}
	return clipText, nil
}

func (f *fakeSurface) WriteClipboardText(s string) error {
	clipMu.Lock()
	defer clipMu.Unlock()
	if clipFail {
		return errors.New("clipboard busy")
	}
	clipText = s
	return nil
}

func newFakeSurface() *fakeSurface {
	return &fakeSurface{events: make(chan Event, 16), arrived: make(chan struct{}, 16), w: 400, h: 300}
}

func (f *fakeSurface) Show(img *image.RGBA) {
	f.ShowRegions(img, nil)
}

func (f *fakeSurface) ShowRegions(img *image.RGBA, rects []image.Rectangle) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *image.NewRGBA(img.Bounds())
	copy(cp.Pix, img.Pix)
	f.img = &cp
	f.shown++
	f.regions = rects
}

func (f *fakeSurface) Size() (int, int) { return f.w, f.h }

func (f *fakeSurface) WaitEvents(maxWait time.Duration) bool {
	if maxWait <= 0 {
		maxWait = 10 * time.Second
	}
	select {
	case <-f.arrived:
		return true
	case <-time.After(maxWait):
		return true
	}
}

func (f *fakeSurface) Events() <-chan Event { return f.events }

// push 注入一个窗口事件并唤醒 WaitEvents。
func (f *fakeSurface) push(ev Event) {
	f.events <- ev
	f.arrived <- struct{}{}
}

func shots(f *fakeSurface) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shown
}

// shotsImage 取最近一次上屏的帧 (假 Surface 的记录)。
func shotsImage(f *fakeSurface) *image.RGBA {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.img
}

type fakeFactory struct{ s *fakeSurface }

func (f *fakeFactory) Create(cfg WindowConfig) (Surface, error) { return f.s, nil }

// 内存剪贴板的状态 (fakeSurface 的 clipboardHost 实现背后)。
var (
	clipMu   sync.Mutex
	clipText string
	// clipFail 置真时模拟"剪贴板被别的进程占用"。
	clipFail bool
)

// resetClipboard 把内存剪贴板复位 (每个用例开头调, 用例之间不许串味)。
func resetClipboard() {
	clipMu.Lock()
	clipText = ""
	clipFail = false
	clipMu.Unlock()
}

// fakeDialogHost 是 nativeDialogHost 的测试替身: 记录调用参数 + 返回预设结果。
// **绝不在单测里真的弹系统对话框**: 无人值守的环境下 MessageBoxW 会一直挂住。
type fakeDialogHost struct {
	mu sync.Mutex
	// 收到的调用
	messages []fakeMessageCall
	opens    []NativeFileOptions
	// 预设的应答
	confirmAnswer  bool
	messageErr     error
	openPath       string
	openOk         bool
	openErr        error
	showMessageNil bool // true 时让 ShowMessage 走"后端不支持"分支之外的另一条
}

type fakeMessageCall struct {
	Kind    NativeDialogKind
	Title   string
	Message string
}

func (f *fakeDialogHost) ShowMessage(kind NativeDialogKind, title, message string) (bool, error) {
	f.mu.Lock()
	f.messages = append(f.messages, fakeMessageCall{Kind: kind, Title: title, Message: message})
	f.mu.Unlock()
	if f.messageErr != nil {
		return false, f.messageErr
	}
	return f.confirmAnswer, nil
}

func (f *fakeDialogHost) ShowOpenFile(opts NativeFileOptions) (string, bool, error) {
	f.mu.Lock()
	f.opens = append(f.opens, opts)
	f.mu.Unlock()
	if f.openErr != nil {
		return "", false, f.openErr
	}
	return f.openPath, f.openOk, nil
}

func (f *fakeDialogHost) messageCalls() []fakeMessageCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeMessageCall(nil), f.messages...)
}

func (f *fakeDialogHost) openCalls() []NativeFileOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]NativeFileOptions(nil), f.opens...)
}

// bareSurface 是一个**只实现 Surface 接口**的最小后端 (刻意不实现任何可选
// 能力接口), 用来验"能力缺失时的降级"。
type bareSurface struct{}

func (b *bareSurface) Show(*image.RGBA)                           {}
func (b *bareSurface) ShowRegions(*image.RGBA, []image.Rectangle) {}
func (b *bareSurface) Size() (int, int)                           { return 200, 150 }
func (b *bareSurface) WaitEvents(time.Duration) bool              { return true }
func (b *bareSurface) Events() <-chan Event                       { return nil }

// =====================================================================
// VM 引导: 假 Surface + 真 VM 的统一入口
// =====================================================================

// evalUI 挂载一段脚本并返回 (VM, 假 Surface)。cleanup 里清空注册表
// (多窗口注册表是包级状态, 见 render.go 的测试隔离纪律)。
func evalUI(t *testing.T, src string) (*vm.VM, *fakeSurface) {
	t.Helper()
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })
	v, err := vm.EvalVM(src)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	return v, fake
}

// uiRoot 取当前窗口的元素树根。
func uiRoot(t *testing.T) *GuiNode {
	t.Helper()
	appMu.Lock()
	defer appMu.Unlock()
	if activeApp == nil || activeApp.root == nil {
		t.Fatalf("没有已挂载的元素树")
	}
	return activeApp.root
}

// evalUIRoot 挂载脚本并返回 (VM, 根节点, app) —— 需要 root/app 的用例用这个。
func evalUIRoot(t *testing.T, src string) (*vm.VM, *GuiNode, *app) {
	t.Helper()
	v, _ := evalUI(t, src)
	appMu.Lock()
	a := activeApp
	appMu.Unlock()
	if a == nil {
		t.Fatalf("脚本未挂载窗口")
	}
	return v, a.root, a
}

// =====================================================================
// 事件泵驱动
// =====================================================================

// runDemoSteps 读入演示脚本并挂载, 然后按 step 逐轮注入事件驱动事件循环。
// 每轮之间会真正重绘, 因此"先点开 → 再点遮罩"这类依赖新布局的序列才测得到
// (一次性把所有事件塞进去只会用同一份旧布局处理全部点击)。
func runDemoSteps(t *testing.T, name string, steps []func(root *GuiNode, fake *fakeSurface)) {
	t.Helper()
	// 演示脚本会注册定时器 (progress_demo 的 setInterval 等), 而调度器是
	// 进程级单例: 不清掉的话, 上一个测试遗留的定时器会在本测试的事件循环里
	// 触发, 报出莫名其妙的 "xxx is not defined"。
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	src, err := os.ReadFile(filepath.Join("..", "testdata", name))
	if err != nil {
		t.Fatalf("读取脚本: %v", err)
	}
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(string(src))
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	appMu.Lock()
	root := activeApp.root
	appMu.Unlock()

	round := 0
	pump := func(maxWait time.Duration) bool {
		round++
		// 每轮先补一个"无害唤醒"事件: 只做断言的步骤自己不产生事件, 而事件泵
		// 在队列为空时会按 WaitEvents 的语义长时间阻塞 (假 Surface 在
		// maxWait<=0 时是 10 秒), 白等一场还会把以秒计的定时器提前耗掉。
		// EventMouseLeave 的语义就是"没有悬停、没有按压", 重复投递是幂等的。
		fake.push(Event{Kind: EventMouseLeave})
		if round-1 < len(steps) {
			steps[round-1](root, fake)
		} else {
			fake.push(Event{Kind: EventClose})
		}
		return Pump(maxWait)
	}
	if err := v.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if Active() {
		t.Fatalf("关闭后应用未退出")
	}
}

// runPumpSteps 把 Go 侧的驱动/断言步骤逐个塞进泵轮次 (与 runDemoSteps
// 同构, 但步骤不带 root/fake 参数 —— 这里驱动的是脚本全局函数)。
func runPumpSteps(t *testing.T, v *vm.VM, fake *fakeSurface, steps []func()) {
	t.Helper()
	round := 0
	pump := func(maxWait time.Duration) bool {
		round++
		// 无害唤醒: 只做断言的步骤自己不产生事件 (语义同 runDemoSteps)
		fake.push(Event{Kind: EventMouseLeave})
		if round-1 < len(steps) {
			steps[round-1]()
		} else {
			fake.push(Event{Kind: EventClose})
		}
		return Pump(maxWait)
	}
	if err := v.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
}

// =====================================================================
// 全局取值 / 调用 (脚本把断言值挂到 globalThis, Go 侧读回)
// =====================================================================

// globalVal 读一个全局变量的原始值。
func globalVal(t *testing.T, v *vm.VM, name string) object.Value {
	t.Helper()
	val, ok := v.Globals().Get(name)
	if !ok {
		t.Fatalf("全局变量 %s 不存在", name)
	}
	return val
}

// globalBool / globalStr / globalNum 读脚本全局里的布尔 / 字符串 / 数字。
func globalBool(t *testing.T, v *vm.VM, name string) (bool, bool) {
	t.Helper()
	val, ok := v.Globals().Get(name)
	if !ok {
		t.Fatalf("全局 %s 缺失", name)
	}
	b, ok := val.(*object.Boolean)
	return b.Value, ok
}

func globalStr(t *testing.T, v *vm.VM, name string) (string, bool) {
	t.Helper()
	val, ok := v.Globals().Get(name)
	if !ok {
		t.Fatalf("全局 %s 缺失", name)
	}
	s, ok := val.(*object.String)
	return s.Value, ok
}

func globalNum(t *testing.T, v *vm.VM, name string) (float64, bool) {
	t.Helper()
	val, ok := v.Globals().Get(name)
	if !ok {
		t.Fatalf("全局 %s 缺失", name)
	}
	n, ok := val.(*object.Number)
	if !ok {
		t.Fatalf("全局 %s 不是数字: %T", name, val)
	}
	return n.Value, true
}

// jsArray 取全局数组; jsArrayLen / jsNumAt 是常用读法 (测试里用来数回调次数)。
func jsArray(t *testing.T, v *vm.VM, name string) *object.Array {
	t.Helper()
	val, ok := v.Globals().Get(name)
	if !ok {
		t.Fatalf("全局 %s 缺失", name)
	}
	arr, ok := val.(*object.Array)
	if !ok {
		t.Fatalf("全局 %s 不是数组, 实际 %s", name, val.Type())
	}
	return arr
}

func jsArrayLen(t *testing.T, v *vm.VM, name string) int {
	t.Helper()
	return len(jsArray(t, v, name).Elements)
}

func jsNumAt(t *testing.T, v *vm.VM, name string, i int) float64 {
	t.Helper()
	arr := jsArray(t, v, name)
	if i >= len(arr.Elements) || i < 0 {
		t.Fatalf("全局 %s 只有 %d 个元素, 取不到第 %d 个", name, len(arr.Elements), i)
	}
	n, ok := arr.Elements[i].(*object.Number)
	if !ok {
		t.Fatalf("全局 %s[%d] 不是数字, 实际 %s", name, i, arr.Elements[i].Type())
	}
	return n.Value
}

// assertGlobal 按 Inspect 文本断言一个全局的值。
func assertGlobal(t *testing.T, v *vm.VM, name, want string) {
	t.Helper()
	val, _ := v.Globals().Get(name)
	if val == nil || val.Inspect() != want {
		got := "<nil>"
		if val != nil {
			got = val.Inspect()
		}
		t.Fatalf("%s = %s, want %q", name, got, want)
	}
}

// callGlobalFn 调用挂在 globalThis 上的函数并取返回值。必须在泵轮次内调用
// (主脚本执行结束后 currentVM 为 nil, 回调桥静默变空操作 —— 见 condrender
// 测试文件头的那条纪律)。
func callGlobalFn(t *testing.T, v *vm.VM, name string, args ...object.Value) object.Value {
	t.Helper()
	fn, ok := v.Globals().Get(name)
	if !ok || !object.IsCallable(fn) {
		t.Fatalf("global %s 不是函数", name)
	}
	return object.CallFunction(fn, nil, args...)
}

// callGlobalInspect 调全局函数并返回结果的 Inspect 文本。
func callGlobalInspect(t *testing.T, v *vm.VM, name string, args ...object.Value) string {
	t.Helper()
	res := callGlobalFn(t, v, name, args...)
	if err := takeCallbackErr(); err != nil {
		t.Fatalf("调用 %s 抛错: %v", name, err)
	}
	if res == nil {
		return "<nil>"
	}
	return res.Inspect()
}

// =====================================================================
// 节点构造 / 树遍历 / 像素断言 / 调色板
// =====================================================================

// mkNode 造一个带数值属性的测试节点。
func mkNode(tag string, props map[string]float64) *GuiNode {
	n := &GuiNode{Tag: tag, Props: map[string]object.Value{}}
	for k, v := range props {
		n.Props[k] = object.NewNumber(v)
	}
	return n
}

// withBool / withStr / withNum 给节点补一个布尔 / 字符串 / 数值属性
// (mkNode 只写数值属性, 挂载后再改的场景用这些)。
func withBool(n *GuiNode, name string, v bool) *GuiNode {
	n.Props[name] = object.NewBoolean(v)
	return n
}

func withStr(n *GuiNode, name, v string) *GuiNode {
	n.Props[name] = object.NewString(v)
	return n
}

func withNum(n *GuiNode, name string, v float64) *GuiNode {
	n.Props[name] = object.NewNumber(v)
	return n
}

// mkButton 造一个带文本子节点的按钮 (标签文本是命中测试回溯的常见场景)。
func mkButton(label string) *GuiNode {
	b := &GuiNode{Tag: "button", Props: map[string]object.Value{}}
	b.Children = []*GuiNode{{Tag: "#text", Text: label, Props: map[string]object.Value{}}}
	b.Children[0].Parent = b
	return b
}

// withClick 给节点挂一个空的 onClick (让 HitTest 能命中它)。
func withClick(n *GuiNode) *GuiNode {
	n.Props["onClick"] = object.NewBuiltin("click", func(args ...object.Value) object.Value {
		return object.UndefinedSingleton
	})
	return n
}

// renderTree 白底布局并整帧绘制, 返回可直接断言的像素面。
func renderTree(root *GuiNode, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	FillRect(img, Rect{0, 0, w, h}, pxWhite)
	Layout(root, w, h)
	Draw(img, root)
	return img
}

// findFirst 深度优先找第一个指定标签的节点。
func findFirst(root *GuiNode, tag string) *GuiNode {
	if root == nil {
		return nil
	}
	if root.Tag == tag {
		return root
	}
	for _, c := range root.Children {
		if n := findFirst(c, tag); n != nil {
			return n
		}
	}
	return nil
}

// countTag 统计指定标签的节点数。
func countTag(root *GuiNode, tag string) int {
	if root == nil {
		return 0
	}
	n := 0
	if root.Tag == tag {
		n++
	}
	for _, c := range root.Children {
		n += countTag(c, tag)
	}
	return n
}

// findFirstWhere 深度优先找第一个满足谓词的节点 (按属性特征定位比按下标稳)。
func findFirstWhere(root *GuiNode, pred func(*GuiNode) bool) *GuiNode {
	if root == nil {
		return nil
	}
	if pred(root) {
		return root
	}
	for _, c := range root.Children {
		if n := findFirstWhere(c, pred); n != nil {
			return n
		}
	}
	return nil
}

// findAll 收集指定标签的全部节点 (声明序)。
func findAll(root *GuiNode, tag string) []*GuiNode {
	var out []*GuiNode
	var walk func(n *GuiNode)
	walk = func(n *GuiNode) {
		if n.Tag == tag {
			out = append(out, n)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return out
}

// allNodes 收集子树全部节点 (含自身)。
func allNodes(root *GuiNode) []*GuiNode {
	var out []*GuiNode
	var walk func(n *GuiNode)
	walk = func(n *GuiNode) {
		out = append(out, n)
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return out
}

// textStartingWith 返回第一个以此前缀开头的文本节点内容。
func textStartingWith(root *GuiNode, prefix string) string {
	for _, n := range findAll(root, "#text") {
		if strings.HasPrefix(n.Text, prefix) {
			return n.Text
		}
	}
	return ""
}

// textContainsAny 报告树上有没有文本片段包含 substr。
func textContainsAny(root *GuiNode, substr string) bool {
	for _, n := range findAll(root, "#text") {
		if strings.Contains(n.Text, substr) {
			return true
		}
	}
	return false
}

func assertPx(t *testing.T, img *image.RGBA, x, y int, want color.RGBA, what string) {
	t.Helper()
	if got := img.RGBAAt(x, y); got != want {
		t.Fatalf("%s: pixel(%d,%d) = %v, want %v", what, x, y, got, want)
	}
}

// countColor 统计矩形内等于某颜色的像素数。
func countColor(img *image.RGBA, r Rect, c color.RGBA) int {
	n := 0
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			if img.RGBAAt(x, y) == c {
				n++
			}
		}
	}
	return n
}

// hasInkIn 报告矩形内有没有"非底色"的像素 (字体渲染受平台影响, 断言具体
// 字形不现实, 但"这一带画了东西"是稳定的)。
func hasInkIn(img *image.RGBA, r Rect) bool {
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			if c := img.RGBAAt(x, y); c != pxFieldFace && c != pxWhite {
				return true
			}
		}
	}
	return false
}

// =====================================================================
// 纯 Go 层 app 组装 (无 VM): 事件分发 / 脏标记断言用
// =====================================================================

// mountTestApp 组装一个挂到假 Surface 上的 app 并出首帧 (不起 VM)。
// 事件分发与脏标记的断言需要"一次 pump 一次观察", 走 VM 事件循环做不到。
//
// 注意: 没有 VM 时 object.CallFunction 是空操作 (回调桥要求 currentVM),
// 所以这里只断言节点状态 / 脏标记 / 像素; 回调参数与调用顺序走全链路测试。
func mountTestApp(t *testing.T, root *GuiNode, w, h int) (*fakeSurface, *app) {
	t.Helper()
	fake := newFakeSurface()
	fake.w, fake.h = w, h
	a := &app{
		surface:    fake,
		root:       root,
		fullDirty:  true,
		dirtyNodes: map[*GuiNode]struct{}{},
	}
	// 登记进注册表 (不只是 activeApp) —— Pump 现在按注册表遍历窗口,
	// 只写 activeApp 的替身会在 Pump 里被完全看不见。
	registerApp(a)
	t.Cleanup(func() {
		// 摘除 + 清 activeApp: 注册表是**包级**状态, 留着会让下一个用例的
		// Pump 去等一个再也不会来事件的假 Surface (表现为整个测试挂住)。
		unregisterApp(a)
		appMu.Lock()
		if activeApp == a {
			activeApp = nil
		}
		appMu.Unlock()
	})
	a.redraw()
	return fake, a
}

// pushAndPump 注入一个事件并跑一轮 pump (处理事件 + 按需重绘)。
func pushAndPump(t *testing.T, fake *fakeSurface, a *app, ev Event) {
	t.Helper()
	fake.push(ev)
	if !a.pump(time.Millisecond) {
		t.Fatalf("pump 意外结束 (窗口被关闭)")
	}
}

// needDraw 读当前脏标记。
func needDraw(a *app) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.needDraw
}

// =====================================================================
// 组件缺省色 (与 raster.go 的色板保持一致, 便于逐像素断言)
// =====================================================================

var (
	pxWhite      = color.RGBA{R: 255, G: 255, B: 255, A: 255}
	pxAccent     = color.RGBA{R: 0x27, G: 0xAE, B: 0x60, A: 255}
	pxTrack      = color.RGBA{R: 0xD0, G: 0xD0, B: 0xD0, A: 255}
	pxSwitchOff  = color.RGBA{R: 0xC8, G: 0xC8, B: 0xC8, A: 255}
	pxFieldEdge  = color.RGBA{R: 0x55, G: 0x55, B: 0x55, A: 255}
	pxBtnFace    = color.RGBA{R: 0xE8, G: 0xE8, B: 0xE8, A: 255}
	pxBtnEdge    = color.RGBA{R: 0x99, G: 0x99, B: 0x99, A: 255}
	pxBtnFaceOff = color.RGBA{R: 211, G: 211, B: 211, A: 255} // 禁用态 (与 190 取平均)
	pxRed        = color.RGBA{R: 0xC0, G: 0x39, B: 0x2B, A: 255}

	// 交互反馈色 (与 raster.go 的偏移量对齐)
	pxBtnHover = color.RGBA{R: 244, G: 244, B: 244, A: 255} // #e8e8e8 +12
	pxBtnPress = color.RGBA{R: 208, G: 208, B: 208, A: 255} // #e8e8e8 -24
	pxRing     = color.RGBA{R: 0x1A, G: 0x5F, B: 0xB4, A: 255}

	// 字段 / 弹层系 (直连源码色板)
	pxFieldFace    = colorFieldFace
	pxInputEdge    = colorInputEdge
	pxOptionActive = colorOptionActive
	pxPopupFace    = colorPopupFace
	pxPopupEdge    = colorPopupEdge
	pxToastAccent  = colorAccent

	// 输入框系
	pxCaret       = colorText
	pxPlaceholder = colorPlaceholder
	pxFieldHover  = colorFieldHover
)

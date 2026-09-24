package mobile

import (
	"fmt"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/14752222/Gox/gfx"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// 本包全部是纯 Go, 因此这些用例在开发机 (Windows) 上就能跑 —— gfx/android 带 cgo
// 只能在 android 目标下编译, 那里的胶水没有单测能力 (真机验证见 M1 验收)。

// drainEvents 取出目前排队的全部事件。
func drainEvents(s *Surface) []gfx.Event {
	var out []gfx.Event
	for {
		select {
		case ev := <-s.Events():
			out = append(out, ev)
		default:
			return out
		}
	}
}

// TestSurfaceSizeAndUpload 尺寸上报与整帧/脏区上传。
func TestSurfaceSizeAndUpload(t *testing.T) {
	var gotImgs int
	var gotRects []image.Rectangle
	s := New(Config{
		Width: 400, Height: 300, Density: 2,
		Uploader: func(img *image.RGBA, rects []image.Rectangle) {
			gotImgs++
			gotRects = rects
		},
	})
	if w, h := s.Size(); w != 400 || h != 300 {
		t.Fatalf("Size = %d,%d want 400,300", w, h)
	}
	if d := s.Density(); d != 2 {
		t.Fatalf("Density = %v want 2", d)
	}
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	s.Show(img)
	if gotImgs != 1 || gotRects != nil {
		t.Fatalf("Show 应整帧上传: imgs=%d rects=%v", gotImgs, gotRects)
	}
	s.ShowRegions(img, []image.Rectangle{{Min: image.Point{X: 1, Y: 2}, Max: image.Point{X: 3, Y: 4}}})
	if gotImgs != 2 || len(gotRects) != 1 {
		t.Fatalf("ShowRegions 应带脏区上传: imgs=%d rects=%v", gotImgs, gotRects)
	}

	// 未注入上传钩子时上屏是空操作, 不能 panic (桌面/无宿主场景)
	bare := New(Config{Width: 10, Height: 10})
	bare.Show(img)
	bare.ShowRegions(img, nil)

	// Close 之后不再上传 (宿主已析构, 再碰它的 buffer 会崩)
	s.Close()
	s.Show(img)
	if gotImgs != 2 {
		t.Fatalf("Close 后不该再上传: imgs=%d", gotImgs)
	}
	// 密度缺省为 1 (宿主没报 density 时不能变成 0 缩放)
	if d := New(Config{}).Density(); d != 1 {
		t.Fatalf("缺省 Density 应为 1, got %v", d)
	}
}

// TestTouchMapping 触摸 → 指针事件的映射规则 (含 Cancel 的收尾语义)。
func TestTouchMapping(t *testing.T) {
	s := New(Config{Width: 200, Height: 200})

	// 按下: 先补一次 MouseMove (让 hover 态正确), 再 Down
	s.Touch(TouchDown, 10, 20)
	evs := drainEvents(s)
	if len(evs) != 2 || evs[0].Kind != gfx.EventMouseMove || evs[1].Kind != gfx.EventMouseDown {
		t.Fatalf("Down 应产生 [MouseMove, MouseDown], got %+v", evs)
	}
	if evs[1].X != 10 || evs[1].Y != 20 {
		t.Fatalf("坐标应原样透传, got %d,%d", evs[1].X, evs[1].Y)
	}

	// 移动 → 一条 MouseMove
	s.Touch(TouchMove, 30, 40)
	if evs := drainEvents(s); len(evs) != 1 || evs[0].Kind != gfx.EventMouseMove || evs[0].X != 30 {
		t.Fatalf("Move 应产生一条 MouseMove, got %+v", evs)
	}

	// 抬起 → MouseUp (点击由内核在 Up 上派发)
	s.Touch(TouchUp, 30, 41)
	if evs := drainEvents(s); len(evs) != 1 || evs[0].Kind != gfx.EventMouseUp || evs[0].Y != 41 {
		t.Fatalf("Up 应产生一条 MouseUp, got %+v", evs)
	}

	// Cancel: 按下过 → 用最后坐标补一条 MouseUp (否则按压态永久残留)
	s.Touch(TouchDown, 50, 60)
	drainEvents(s)
	s.Touch(TouchCancel, 0, 0)
	evs = drainEvents(s)
	if len(evs) != 1 || evs[0].Kind != gfx.EventMouseUp || evs[0].X != 50 || evs[0].Y != 60 {
		t.Fatalf("Cancel 应用最后坐标补 MouseUp, got %+v", evs)
	}

	// 没按下就 Cancel (例如第二根手指) → 不能凭空造出点击
	s.Touch(TouchCancel, 0, 0)
	if evs := drainEvents(s); len(evs) != 0 {
		t.Fatalf("未按下时 Cancel 不该产生事件, got %+v", evs)
	}
}

// TestWaitEventsWakeups WaitEvents 的唤醒与收尾语义 (事件泵全靠它)。
func TestWaitEventsWakeups(t *testing.T) {
	s := New(Config{Width: 100, Height: 100})

	// Tick 唤醒 (宿主每帧调一次 → rAF 落在 vsync 上)
	s.Tick()
	if !s.WaitEvents(5 * time.Second) {
		t.Fatalf("Tick 后 WaitEvents 应立即返回 true")
	}

	// 新事件唤醒
	s.Post(gfx.Event{Kind: gfx.EventKeyDown, Key: "Enter"})
	if !s.WaitEvents(5 * time.Second) {
		t.Fatalf("有事件时 WaitEvents 应立即返回 true")
	}
	if evs := drainEvents(s); len(evs) != 1 || evs[0].Key != "Enter" {
		t.Fatalf("Post 的事件应可取到, got %+v", evs)
	}

	// 超时返回 true (窗口还活着, 只是这一轮没事做)
	start := time.Now()
	if !s.WaitEvents(20 * time.Millisecond) {
		t.Fatalf("超时不该结束会话")
	}
	if elapsed := time.Since(start); elapsed < 10*time.Millisecond {
		t.Fatalf("应真的等了一会儿, 只等了 %v", elapsed)
	}

	// Close → 立刻返回 false (泵退出), 且幂等
	s.Close()
	if s.WaitEvents(5 * time.Second) {
		t.Fatalf("Close 后 WaitEvents 应返回 false")
	}
	s.Close()
	if s.WaitEvents(0) {
		t.Fatalf("重复 Close 后仍应返回 false")
	}
	// 关闭后 Post 不进队列 (也不 panic)
	s.Post(gfx.Event{Kind: gfx.EventClose})
	if evs := drainEvents(s); len(evs) != 0 {
		t.Fatalf("关闭后不该再收事件, got %+v", evs)
	}
}

// TestResizePostsEventOnlyOnChange 尺寸变化才投 EventResize (重复投递会白重排)。
func TestResizePostsEventOnlyOnChange(t *testing.T) {
	s := New(Config{Width: 400, Height: 800, Density: 3})
	s.Resize(400, 800, 3) // 没变
	if evs := drainEvents(s); len(evs) != 0 {
		t.Fatalf("尺寸未变不该产生事件, got %+v", evs)
	}
	s.Resize(800, 400, 3) // 旋转
	evs := drainEvents(s)
	if len(evs) != 1 || evs[0].Kind != gfx.EventResize || evs[0].W != 800 || evs[0].H != 400 {
		t.Fatalf("旋转应产生 EventResize(800,400), got %+v", evs)
	}
	if w, h := s.Size(); w != 800 || h != 400 {
		t.Fatalf("Size 应更新, got %d,%d", w, h)
	}
	s.Resize(0, 0, 0) // 非法尺寸忽略
	if w, h := s.Size(); w != 800 || h != 400 {
		t.Fatalf("非法尺寸应被忽略, got %d,%d", w, h)
	}
}

// TestDisplaysAndCaption gx/screen 看得见的那部分: density + 窗口归属 + 指针捕获。
func TestDisplaysAndCaption(t *testing.T) {
	s := New(Config{Width: 1080, Height: 2400, Density: 2.75, ID: "phone", Name: "Pixel"})
	list := s.Displays()
	if len(list) != 1 {
		t.Fatalf("移动端应只上报一块屏, got %d", len(list))
	}
	d := list[0]
	if d.ID != "phone" || d.Scale != 2.75 || d.W != 1080 || d.H != 2400 || !d.Primary {
		t.Fatalf("Display 字段不对: %+v", d)
	}
	if d.WorkW != 1080 || d.WorkH != 2400 {
		t.Fatalf("工作区应等于整屏 (安全区由宿主另报), got %dx%d", d.WorkW, d.WorkH)
	}
	if id, ok := s.DisplayOf(s); !ok || id != "phone" {
		t.Fatalf("DisplayOf(自己) 应命中 phone, got %q %v", id, ok)
	}
	if _, ok := s.DisplayOf(New(Config{})); ok {
		t.Fatalf("别的 Surface 不该被认领")
	}
	// capturer: 移动端触摸天然被 View 捕获, 不实现的话拖动会在手指移出元素时被放弃
	if _, ok := interface{}(s).(interface{ CapturePointer() }); !ok {
		t.Fatalf("Surface 应实现 capturer 可选接口")
	}
	s.CapturePointer()
	s.ReleasePointer()
}

// TestFactoryBindsSurface Factory 不创建窗口, 只交回宿主已建好的表面。
func TestFactoryBindsSurface(t *testing.T) {
	s := New(Config{Width: 300, Height: 200})
	f := NewFactory(s)
	got, err := f.Create(gfx.WindowConfig{Title: "T", Width: 300, Height: 200})
	if err != nil || got != gfx.Surface(s) {
		t.Fatalf("Create 应返回绑定的 Surface: got=%v err=%v", got, err)
	}
	s.Close()
	if _, err := f.Create(gfx.WindowConfig{}); err == nil {
		t.Fatalf("表面关闭后 Create 应报错")
	}
	if _, err := NewFactory(nil).Create(gfx.WindowConfig{}); err == nil {
		t.Fatalf("未绑定表面时 Create 应报错")
	}
}

// TestTouchDrivesButtonClick 端到端: 一段真脚本 + 触摸点击 + 定时器/渲染全链路。
//
// 这就是 M1 验收标准 ("counter_demo 在真机上能点、数字递增") 在开发机上的等价物 ——
// 真机额外要验的只有"JNI 胶水与上屏通道", 交互/布局/点击派发这条链在这里已经是真的。
func TestTouchDrivesButtonClick(t *testing.T) {
	var uploads int
	s := New(Config{
		Width: 400, Height: 300, Density: 2,
		Uploader: func(img *image.RGBA, rects []image.Rectangle) { uploads++ },
	})
	s.Register()
	// 注册表是包级状态, 必须拆干净, 否则下一个用例的 Pump 会去等这个已经关掉的表面
	defer gfx.SetDefaultFactory(nil)

	v, err := vm.EvalVM(`
		import { h, render } from "gx/gfx";
		let clicks = 0;
		const ui = h("column", {gap: 0, padding: 0},
			h("button", {width: 200, height: 60, onClick: () => { clicks = clicks + 1; }}, "加一"));
		render(ui, {title: "mobile", width: 400, height: 300});
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	uiVal, ok := v.Globals().Get("ui")
	if !ok {
		t.Fatalf("脚本里应能看到 ui")
	}
	ui, ok := uiVal.(*gfx.GuiNode)
	if !ok || len(ui.Children) == 0 {
		t.Fatalf("ui 结构不对: %T %v", uiVal, uiVal)
	}
	btn := ui.Children[0]
	if btn.Box.W == 0 || btn.Box.H == 0 {
		t.Fatalf("按钮应先完成布局, got %+v", btn.Box)
	}
	cx, cy := btn.Box.X+btn.Box.W/2, btn.Box.Y+btn.Box.H/2

	round := 0
	pump := func(maxWait time.Duration) bool {
		round++
		switch round {
		case 1:
			// 一根手指点下去再抬起来
			s.Touch(TouchDown, cx, cy)
			s.Touch(TouchUp, cx, cy)
		case 2:
			// 点完之后留一轮给重绘, 再关窗
			s.Post(gfx.Event{Kind: gfx.EventClose})
		}
		return gfx.Pump(maxWait)
	}
	if err := v.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}

	cv, _ := v.Globals().Get("clicks")
	num, _ := cv.(*object.Number)
	if num == nil || num.Value != 1 {
		t.Fatalf("点一次按钮 clicks 应为 1, got %v", cv)
	}
	if uploads == 0 {
		t.Fatalf("触摸点击这一轮应至少上屏一帧")
	}
}

// collectText 递归收集整棵树里的文本节点内容。
func collectText(n *gfx.GuiNode) []string {
	if n == nil {
		return nil
	}
	var out []string
	if n.Tag == "#text" && n.Text != "" {
		out = append(out, n.Text)
	}
	for _, c := range n.Children {
		out = append(out, collectText(c)...)
	}
	return out
}

func hasText(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestAndroidAssetScriptClickDrivesCounter 直接跑**要打进 APK 的那份 asset 脚本**。
//
// 它与 TestTouchDrivesButtonClick 的区别在于脚本来源: 这里读的是
// app/android/app/src/main/assets/app.js —— 真机上唯一会被执行的脚本。安卓侧没法单测,
// 所以把"asset 能不能跑、点得动、界面会不会更新"这件事拉到开发机上来守:
// 谁把 asset 改坏了 (JSX 写错、用了相对 import、引用了不存在的模块), 这里当场红。
func TestAndroidAssetScriptClickDrivesCounter(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..", "app", "android", "app", "src", "main", "assets", "app.js"))
	if err != nil {
		t.Fatalf("读取 asset 脚本: %v", err)
	}

	s := New(Config{Width: 400, Height: 600, Density: 2})
	s.Register()
	defer gfx.SetDefaultFactory(nil)

	v, err := vm.EvalVM(string(src))
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	// 断言全部攒到泵外再做: t.Fatalf 走 runtime.Goexit, 在 pump 回调里调用会把
	// RunTimersWithPump 连同已挂载的窗口一起丢在半路 —— 报告出来的现象是
	// "测试卡住" 而不是 "断言失败"。所以泵内只记录, 泵后统一判。
	//
	// 失败路径也要走一次关窗: 挂着不放的窗口会留在包级注册表里, 后面的用例
	// 调 gfx.Pump 时会去等一个已经不存在的宿主。
	var (
		before  []string // 点击前的界面文本
		after   []string // 点击并重绘后的界面文本
		problem string   // 泵内发现的第一个问题
	)
	closeAndPump := func(maxWait time.Duration) bool {
		s.Post(gfx.Event{Kind: gfx.EventClose})
		return gfx.Pump(maxWait)
	}
	round := 0
	pump := func(maxWait time.Duration) bool {
		round++
		switch round {
		case 1:
			// 首帧: 让 render 挂上的树走完布局 + 首绘 —— 坐标要到这之后才算得出来。
			s.Tick()
			return gfx.Pump(maxWait)
		case 2:
			root := gfx.ActiveRoot()
			if root == nil {
				problem = "首帧之后仍拿不到窗口根节点"
				return closeAndPump(maxWait)
			}
			before = collectText(root)
			btn := lastButton(root)
			if btn == nil {
				problem = fmt.Sprintf("asset 里没找到 button 节点, 树上文本: %v", before)
				return closeAndPump(maxWait)
			}
			if btn.Box.W == 0 || btn.Box.H == 0 {
				problem = fmt.Sprintf("button 未完成布局 (Box=%+v)", btn.Box)
				return closeAndPump(maxWait)
			}
			// 脚本里只有一个 button: 把它的中心当一根手指的落点。
			cx, cy := btn.Box.X+btn.Box.W/2, btn.Box.Y+btn.Box.H/2
			s.Touch(TouchDown, cx, cy)
			s.Touch(TouchUp, cx, cy)
			return gfx.Pump(maxWait)
		case 3:
			// 点击已在上一轮 Pump 里派发完 (setCount → 信号 → effect → 文本节点),
			// 这一轮读到的就是更新后的树; 读完立刻关窗, 让泵自己收敛退出。
			after = collectText(gfx.ActiveRoot())
			return closeAndPump(maxWait)
		default:
			problem = fmt.Sprintf("关窗后事件泵未收敛 (已到第 %d 轮)", round)
			return false
		}
	}
	if err := v.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if problem != "" {
		t.Fatal(problem)
	}
	// 先确认点击前是 0: 否则"点击后是 1"可能是脚本一上来就写着 1 (断言空转),
	// 那样这条回归等于什么都没守。
	if !hasText(before, "触摸链路已通: 计数 0") {
		t.Fatalf("点击前界面应显示计数 0, 实际: %v", before)
	}
	if !hasText(after, "触摸链路已通: 计数 1") {
		t.Fatalf("点一次按钮后界面应显示计数 1; 点击前=%v 点击后=%v", before, after)
	}
}

// lastButton 深度优先找最后一个 button 节点 (asset 只放了一个)。
func lastButton(n *gfx.GuiNode) *gfx.GuiNode {
	if n == nil {
		return nil
	}
	var found *gfx.GuiNode
	if n.Tag == "button" {
		found = n
	}
	for _, c := range n.Children {
		if b := lastButton(c); b != nil {
			found = b
		}
	}
	return found
}

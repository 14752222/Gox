//go:build darwin

// v1.1 三项增强的真机验证: IME (NSTextInputClient 转发链)、原生对话框
// (runModal + 定时点击/取消)、多屏枚举。
//
// 与 cocoa_e2e_test.go 同一纪律: 场景跑在 TestMain (主线程), Test 函数只
// 断言包级结果。场景由 cocoa_e2e_test.go 的 TestMain 调 runV1Scenarios。
//
// 验证技巧说明:
//   - IME: 不需要真实输入法 —— 直接向 view 发送被"转发"的协议选择器
//     (insertText:replacementRange: 等), 消息会走 methodSignatureForSelector:
//     → forwardInvocation: 的完整链路 (这正是被测对象), 并验证
//     EventIMECommit 进入事件通道、结构体返回值 (selectedRange/markedRect)
//     经 setReturnValue 正确回传。
//   - 对话框: nativeDialogHost 是阻塞式 runModal, 自动化靠
//     performSelector:withObject:afterDelay:inModes: 在
//     NSModalPanelRunLoopMode 里定时点击按钮/触发取消, 让 runModal 自己
//     返回 —— 用户点击的那条路径 (响应码 → bool/(path,ok)) 原样跑到。
//   - 多屏: 直接断言枚举结果 (单屏机器也成立: 恰好一块主屏)。
//
// 注意: 测试会短暂弹出窗口/模态框 (每个对话框约 0.4s 自动关闭)。
package cocoa

import (
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"github.com/14752222/Gox/gfx"
	"github.com/ebitengine/purego/objc"
)

var (
	selPerformSelectorDelay = objc.RegisterName("performSelector:withObject:afterDelay:inModes:")
	selPerformClick         = objc.RegisterName("performClick:")
	selCancelAction         = objc.RegisterName("cancel:")
	selNSMutableInitCap     = objc.RegisterName("initWithCapacity:")
)

// nsModalPanelModeStr 是 NSModalPanelRunLoopMode 的字符串值 (延迟执行必须
// 挂在这个 mode: runModal 期间只有它被运行)。
const nsModalPanelModeStr = "NSModalPanelRunLoopMode"

// v1Fail 非空 = 场景执行失败 (TestMain 里记录)。
var v1Fail string

// TestCocoaIMEForwarding 断言 IME 转发链路: setMarkedText/unmarkText 状态、
// insertText → EventIMECommit、结构体返回值 (selectedRange/markedRect)。
func TestCocoaIMEForwarding(t *testing.T) {
	if v1Fail != "" {
		t.Fatalf("v1 场景失败: %s", v1Fail)
	}
	if !v1MarkedOn {
		t.Error("setMarkedText 后 hasMarkedText 应为 true (组合态未建立)")
	}
	if v1MarkedOff {
		t.Error("unmarkText 后 hasMarkedText 应为 false")
	}
	if v1IMECommit != "你好" {
		t.Errorf("EventIMECommit.Text = %q, want \"你好\"", v1IMECommit)
	}
	if v1SelRange.Location != 0 || v1SelRange.Length != 0 {
		t.Errorf("selectedRange = %+v, want 零区间 (setReturnValue 回传失真)", v1SelRange)
	}
	if !v1RectOK {
		t.Error("markedRect 回传的窗口矩形为空 (候选窗将无处锚定)")
	}
}

// TestCocoaDisplays 断言多屏枚举与 DisplayOf。
func TestCocoaDisplays(t *testing.T) {
	if v1Fail != "" {
		t.Fatalf("v1 场景失败: %s", v1Fail)
	}
	if !v1DisplaysOK {
		t.Error("Displays 枚举异常 (非空/恰一主屏/几何与缩放合理)")
	}
	if !v1DisplayOf {
		t.Error("DisplayOf(窗口) 未命中枚举表中的 ID")
	}
}

// TestCocoaDialogs 断言对话框响应码映射与取消语义。
func TestCocoaDialogs(t *testing.T) {
	if v1Fail != "" {
		t.Fatalf("v1 场景失败: %s", v1Fail)
	}
	if !v1AlertOK {
		t.Error("点击\"确定\"后 ShowMessage 应返回 true")
	}
	if v1AlertCancel {
		t.Error("点击\"取消\"后 ShowMessage 应返回 false")
	}
	if !v1PanelCancel {
		t.Error("文件面板取消应返回 (\"\", false)")
	}
	if !v1URLPath {
		t.Error("NSURL → path 映射错误 (openFile OK 路径的组成部分)")
	}
}

// ===== 场景结果 (Test 函数断言用) =====

var (
	// IME: 转发链 + 事件通道
	v1IMECommit string // insertText 转发后收到的事件文本
	v1MarkedOn  bool   // setMarkedText 后 hasMarkedText
	v1MarkedOff bool   // unmarkText 后 hasMarkedText (应为 false)
	v1SelRange  nsRange
	v1RectOK    bool // markedRect 结构体回传非零

	// 多屏
	v1DisplaysOK bool // 枚举非空、有主屏、几何/缩放合理
	v1DisplayOf  bool // DisplayOf(窗口) 命中枚举表中的 ID

	// 对话框
	v1AlertOK     bool // 定时点击"确定" → true
	v1AlertCancel bool // 定时点击"取消" → false
	v1PanelCancel bool // 定时 cancel: → ("", false)
	v1URLPath     bool // NSURL → path 映射 (openFile OK 路径的组成部分)
)

// runV1Scenarios 在主线程上执行全部 v1.1 场景。必须在主 goroutine 调用。
func runV1Scenarios() error {
	s0, err := newSurface(gfx.WindowConfig{Title: "cocoa-v1", Width: 400, Height: 300})
	if err != nil {
		return err
	}
	s := s0.(*surface)
	defer func() {
		s.win.Send(selOrderOut, objc.ID(0))
		unregSurface(s.view, s.delegate)
	}()

	scenarioIME(s)
	scenarioDisplays(s)
	if err := scenarioDialogs(); err != nil {
		return err
	}
	return nil
}

// ===== 场景 1: IME =====

func scenarioIME(s *surface) {
	// 组合开始: setMarkedText("nihao") —— 走 methodSignatureForSelector →
	// forwardInvocation → getArgument (指针 + 16 字节结构体)
	objc.Send[uintptr](s.view, selSetMarkedText, nsString("nihao"), nsRange{Location: 0, Length: 5}, nsRange{})
	v1MarkedOn = objc.Send[bool](s.view, selHasMarkedText)

	// 组合串经 NSSize 无关路径读取 (NSString 参数还能以 NSAttributedString
	// 形态到达 —— insertText 的归一在 imeStringOf, 这里顺手验 NSString)
	objc.Send[uintptr](s.view, selUnmarkText)
	v1MarkedOff = objc.Send[bool](s.view, selHasMarkedText)

	// 提交: insertText:replacementRange: → forwardInvocation →
	// EventIMECommit 进事件通道
	objc.Send[uintptr](s.view, selInsertTextRepl, nsString("你好"), nsRange{})
	select {
	case ev := <-s.Events():
		if ev.Kind == gfx.EventIMECommit {
			v1IMECommit = ev.Text
		}
	case <-time.After(2 * time.Second):
	}

	// 结构体返回值: selectedRange (NSRange) / markedRect (NSRect, HFA)
	// 都经 setReturnValue 回填 —— 回传错了这里直接读到零/垃圾
	v1SelRange = objc.Send[nsRange](s.view, selSelectedRange)
	rect := objc.Send[nsRect](s.view, selMarkedRect)
	v1RectOK = rect.Size.Width > 0 && rect.Size.Height > 0
}

// ===== 场景 2: 多屏 =====

func scenarioDisplays(s *surface) {
	f := &factory{}
	ds := f.Displays()
	ok := len(ds) > 0
	primaries := 0
	ids := map[string]bool{}
	for _, d := range ds {
		if d.W <= 0 || d.H <= 0 || d.WorkW <= 0 || d.WorkH <= 0 || d.Scale < 1 {
			ok = false
		}
		if d.Primary {
			primaries++
		}
		ids[d.ID] = true
	}
	v1DisplaysOK = ok && primaries == 1

	id, hit := f.DisplayOf(s)
	v1DisplayOf = hit && id != "" && ids[id]
}

// ===== 场景 3: 对话框 =====

func scenarioDialogs() error {
	// alert 确认路径: 定时点击第一个按钮 ("确定") → runModal 返回 1000
	alert := newAlert(gfx.DialogConfirm, "cocoa-v1", "确认路径")
	okBtn := objc.Send[objc.ID](alert, selButtons).Send(selObjectAtIndex, uintptr(0))
	schedulePerform(okBtn, selPerformClick, 0.4)
	v1AlertOK = alertConfirm(alert)

	// alert 取消路径: 点击第二个按钮 ("取消") → 非 1000 → false
	alert2 := newAlert(gfx.DialogConfirm, "cocoa-v1", "取消路径")
	cancelBtn := objc.Send[objc.ID](alert2, selButtons).Send(selObjectAtIndex, uintptr(1))
	schedulePerform(cancelBtn, selPerformClick, 0.4)
	v1AlertCancel = alertConfirm(alert2)

	// 文件面板取消路径: cancel: (Esc 等价动作) → 非 OK → ("", false)
	panel := newOpenPanel(gfx.NativeFileOptions{Title: "cocoa-v1"})
	schedulePerform(panel, selCancelAction, 0.4)
	path, ok := runFilePanel(panel)
	v1PanelCancel = !ok && path == ""

	// openFile OK 路径的映射段: NSURL.fileURLWithPath → path
	// (runModal 的 OK 分支需要真实文件存在才能点击, 映射段单独验证)
	dir, err := os.MkdirTemp("", "gox-cocoa-v1")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	want := filepath.Join(dir, "a.txt")
	url := objc.ID(objc.GetClass("NSURL")).Send(selFileURLWithPath, nsString(want))
	v1URLPath = url != 0 && nsToGo(url.Send(selPath)) == want
	return nil
}

// schedulePerform 在 NSModalPanelRunLoopMode 里安排一次延迟调用 —— runModal
// 期间只有该 mode 在跑, 缺省 mode 的定时器永远不会触发。
func schedulePerform(target objc.ID, sel objc.SEL, delay float64) {
	modes := objc.ID(objc.GetClass("NSMutableArray")).Send(selAlloc)
	modes = modes.Send(selNSMutableInitCap, uintptr(1))
	modes.Send(selAddObject, nsString(nsModalPanelModeStr))
	target.Send(selPerformSelectorDelay, sel, objc.ID(0), float64(delay), modes)
}

// keep unsafe import (getArgument 路径在 ime.go, 测试文件里暂无直接使用)
var _ = unsafe.Pointer(nil)

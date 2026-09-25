//go:build darwin

// 输入法 (IME) —— NSTextInputClient 协议的 cocoa 实现 (P2-7 的 macOS 补全)。
//
// ## gfx 内核的契约 (gfx/ime.go)
//
// 内核只认两条: ① 焦点进出编辑框时调 SetIMEEnabled (可选 imeController);
// ② 输入法提交整批文本时后端投一条 EventIMECommit{Text}。插到哪儿、怎么改
// 受控值全在内核 —— 本文件只负责把 AppKit 的输入法结果翻译成这条事件。
//
// ## 为什么走"消息转发"而不是直接注册 IMP
//
// NSTextInputClient 的核心方法一半带 NSRange/NSRect 结构体参数或结构体
// 返回值 (insertText:replacementRange: / setMarkedText:selectedRange:… /
// markedRect / selectedRange)。IMP 回调侧的结构体 ABI 打包是 purego 未
// 覆盖的区域 (见 cocoa.go 文件头决策 1: 事件方法只收 NSEvent 指针), 所以
// 这些方法**不能**用 objc.RegisterClass 直接挂 Go IMP。
//
// 解法是 ObjC 的标准消息转发: 注册三个"纯指针参数"的挂钩方法 ——
// methodSignatureForSelector: / forwardInvocation: / respondsToSelector:。
//
//  1. respondsToSelector: 对 IME 选择器返回 YES (AppKit 会先探测客户端
//     能力, 不回答 YES 输入法直接不工作);
//  2. methodSignatureForSelector: 对 IME 选择器用
//     NSMethodSignature.signatureWithObjCTypes: 现场拼签名 (结构体用类型
//     编码描述, 与 ABI 无关);
//  3. forwardInvocation: 里经 NSInvocation.getArgument:atIndex:/setReturnValue:
//     取参数/回填返回值 —— 这两个方法只收指针+整数, 完全落在 IMP 纪律内。
//     参数在寄存器还是栈上、HFA 返回值怎么放回浮点寄存器, 全由运行时的
//     转发机制处理, 我们不碰 ABI。
//
// ## keyDown 的分流规则 (英文直入照旧)
//
//   - 焦点不在编辑框上 (SetIMEEnabled(false)) → 照旧直入, 零行为变化;
//   - Ctrl/Alt 组合 (快捷键) → 照旧直入;
//   - 当前输入源是纯键盘布局 (TIS 的 InputSourceID 前缀 com.apple.keylayout.)
//     → 照旧直入 —— 英文用户感知不到本文件的存在;
//   - 正在组合 (hasMarkedText) → 全部键交给输入法 (Enter 提交原串、Esc
//     取消, 内核此时不应收到按键);
//   - 其余可打印键且当前输入源是输入法 → 交 NSTextInputContext.handleEvent:
//     组合过程走 setMarkedText (候选窗由系统绘制), 选定后 insertText: →
//     EventIMECommit。
//
// 组合过程 (拼音未上屏那段) 不投任何内核事件 —— 与 win32/移动端同一口径
// (gfx/ime.go: v1 只做结果提交, 候选窗由系统绘制)。
package cocoa

import (
	"strings"
	"structs"
	"sync"
	"unsafe"

	"github.com/14752222/Gox/gfx"
	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

var (
	selMethodSignature = objc.RegisterName("methodSignatureForSelector:")
	selForwardInv      = objc.RegisterName("forwardInvocation:")
	selResponds        = objc.RegisterName("respondsToSelector:")
	selSignatureTypes  = objc.RegisterName("signatureWithObjCTypes:")
	selGetArgument     = objc.RegisterName("getArgument:atIndex:")
	selSetReturnValue  = objc.RegisterName("setReturnValue:")
	selInvocationSel   = objc.RegisterName("selector")
	selIsKindOfClass   = objc.RegisterName("isKindOfClass:")
	selAttrString      = objc.RegisterName("string") // NSAttributedString.string
	selInitWithClient  = objc.RegisterName("initWithClient:")
	selHandleEvent     = objc.RegisterName("handleEvent:")
	selConvertRect     = objc.RegisterName("convertRect:toView:")

	// NSTextInputClient 的选择器 (名 → 表项)
	selInsertTextRepl = objc.RegisterName("insertText:replacementRange:")
	selSetMarkedText  = objc.RegisterName("setMarkedText:selectedRange:replacementRange:")
	selUnmarkText     = objc.RegisterName("unmarkText")
	selHasMarkedText  = objc.RegisterName("hasMarkedText")
	selValidAttrs     = objc.RegisterName("validAttributesForMarkedText")
	selAttrSubstr     = objc.RegisterName("attributedSubstringForProposedRange:actualRange:")
	selMarkedRect     = objc.RegisterName("markedRect")
	selSelectedRange  = objc.RegisterName("selectedRange")
	selDoCommand      = objc.RegisterName("doCommandBySelector:")
)

// nsRange 与 NSRange 对齐 (两个整数, 16 字节 —— 非浮点 HFA, 但回调侧同样
// 不直接打包, 一律经 NSInvocation 取用)。
type nsRange struct {
	_        structs.HostLayout
	Location uint64
	Length   uint64
}

// imeSignatures 是"本类以转发方式实现"的选择器 → 类型编码表。
//
// 编码约定: @=对象, :=SEL, v=void, c=BOOL(macOS 是 signed char),
// NSRange={_NSRange=QQ} (2×unsigned long long, 8 字节/域, arm64 与
// NSUInteger 同宽), NSRect={_NSRect={_NSPoint=dd}{_NSSize=dd}}。
// NSMethodSignature 只用编码算尺寸与布局, 这些串与 Apple 头文件的
// 编码一致。
func imeSignatures(sel objc.SEL) (string, bool) {
	switch sel {
	case selInsertTextRepl:
		return "v@:@@{_NSRange=QQ}", true
	case selSetMarkedText:
		return "v@:@{_NSRange=QQ}{_NSRange=QQ}", true
	case selUnmarkText:
		return "v@:", true
	case selHasMarkedText:
		return "c@:", true
	case selValidAttrs:
		return "@@:", true
	case selAttrSubstr:
		return "@@:{_NSRange=QQ}^{_NSRange=QQ}", true
	case selMarkedRect:
		return "{_NSRect={_NSPoint=dd}{_NSSize=dd}}@:", true
	case selSelectedRange:
		return "{_NSRange=QQ}@:", true
	case selDoCommand:
		return "v@::", true
	}
	return "", false
}

// ===== IMP: 转发三挂钩 (全部只收指针/SEL, 符合注册纪律) =====

// impRespondsToSelector 让 AppKit 的能力探测看见 IME 方法。没有这一环,
// NSTextInputContext 会认为客户端不认得 insertText:replacementRange: 等选择
// 器, 输入法整条链路静默失效 (不会报错, 就是打不出字)。
func impRespondsToSelector(self objc.ID, cmd objc.SEL, sel objc.SEL) uintptr {
	if _, ok := imeSignatures(sel); ok {
		return 1
	}
	return uintptr(objc.SendSuper[objc.ID](self, selResponds, sel))
}

// impMethodSignatureForSelector 为 IME 选择器现场拼签名; 其余交给 NSView
// (框架内部还会对别的选择器做转发探测, 不能一概返回 nil)。
func impMethodSignatureForSelector(self objc.ID, cmd objc.SEL, sel objc.SEL) uintptr {
	if enc, ok := imeSignatures(sel); ok {
		// signatureWithObjCTypes: 收的是 **C 字符串** (const char *), 不是
		// NSString —— 传对象会把对象头当编码解析, 直接抛异常。
		b := append([]byte(enc), 0)
		sig := objc.ID(objc.GetClass("NSMethodSignature")).Send(selSignatureTypes, unsafe.Pointer(&b[0]))
		return uintptr(sig)
	}
	return uintptr(objc.SendSuper[objc.ID](self, selMethodSignature, sel))
}

// impForwardInvocation 是全部 IME 协议方法的真正入口。
func impForwardInvocation(self objc.ID, cmd objc.SEL, inv objc.ID) uintptr {
	s := surfaceOf(self)
	sel := objc.Send[objc.SEL](inv, selInvocationSel)
	switch sel {
	case selInsertTextRepl:
		if s == nil {
			return 0
		}
		var str objc.ID
		var rng nsRange
		objc.Send[uintptr](inv, selGetArgument, unsafe.Pointer(&str), uintptr(2))
		objc.Send[uintptr](inv, selGetArgument, unsafe.Pointer(&rng), uintptr(3))
		_ = rng // v1 忽略 replacementRange: 提交永远落在内核维护的光标处
		s.imeCommitText(imeStringOf(str))
	case selSetMarkedText:
		if s == nil {
			return 0
		}
		var str objc.ID
		var sel2, rng nsRange
		objc.Send[uintptr](inv, selGetArgument, unsafe.Pointer(&str), uintptr(2))
		objc.Send[uintptr](inv, selGetArgument, unsafe.Pointer(&sel2), uintptr(3))
		objc.Send[uintptr](inv, selGetArgument, unsafe.Pointer(&rng), uintptr(4))
		_ = sel2 // v1 不内联绘制组合串, 选中范围/替换范围都不消费
		s.imeMarked = imeStringOf(str)
	case selUnmarkText:
		if s == nil {
			return 0
		}
		s.imeMarked = ""
	case selHasMarkedText:
		marked := uint8(0)
		if s != nil && s.imeMarked != "" {
			marked = 1
		}
		objc.Send[uintptr](inv, selSetReturnValue, unsafe.Pointer(&marked))
	case selSelectedRange:
		// v1 不知道内核光标位置, 回零区间: 输入法据此不会去改已有文本
		// (最坏情形是"替换选区"式操作失效, 组合/提交不受影响)。
		r := nsRange{}
		objc.Send[uintptr](inv, selSetReturnValue, unsafe.Pointer(&r))
	case selMarkedRect:
		// 候选窗锚点: 用整个 view 的可见区换算成窗口坐标。精确到光标位置
		// 需要内核把编辑框矩形回传给后端, v1 不做 —— 候选窗位置可能偏,
		// 功能不受影响。
		var r nsRect
		if s != nil {
			b := objc.Send[nsRect](self, selBounds)
			r = objc.Send[nsRect](self, selConvertRect, b, objc.ID(0))
		}
		objc.Send[uintptr](inv, selSetReturnValue, unsafe.Pointer(&r))
	case selValidAttrs:
		// 无内联组合串 → 无属性可言, 返回 nil (缺省零返回, 不用 set)。
	case selAttrSubstr:
		// 不提供区间取词 (输入法的"重选候选"高级功能), 缺省 nil 即"没有"。
	case selDoCommand:
		// 框架命令 (cancelOperation:/insertTab: …) 一律忽略: 组合取消由
		// 输入法自己经 setMarkedText:@"" 表达, 不需要我们响应。
	}
	return 0
}

// imeStringOf 归一 insertText/setMarkedText 的文本参数: 可能是 NSString,
// 也可能是 NSAttributedString (带样式的提交), 后者取其纯文本。
func imeStringOf(v objc.ID) string {
	if v == 0 {
		return ""
	}
	if objc.Send[bool](v, selIsKindOfClass, objc.ID(objc.GetClass("NSAttributedString"))) {
		v = v.Send(selAttrString)
		if v == 0 {
			return ""
		}
	}
	return nsToGo(v)
}

// ===== surface 侧接线 =====

// imeCommitText 把输入法提交的一批字符投给内核 (gfx/ime.go 消费)。
func (s *surface) imeCommitText(text string) {
	if text == "" || s.isClosed() {
		return
	}
	s.trySend(gfx.Event{Kind: gfx.EventIMECommit, Text: text})
}

// imeContext 惰性创建 NSTextInputContext (客户端就是本 view; 它经上面的
// 转发挂钩"认得"NSTextInputClient 的全部方法)。必须在 GUI 线程调用。
func (s *surface) imeContext() objc.ID {
	if s.inputCtx == 0 {
		ctx := objc.ID(objc.GetClass("NSTextInputContext")).Send(selAlloc)
		s.inputCtx = ctx.Send(selInitWithClient, s.view)
	}
	return s.inputCtx
}

// SetIMEEnabled 实现 gfx 的可选 imeController 接口: 焦点落在 input/textarea
// 上才开输入法 (render.go 的 setFocus 会调)。缺省关闭 —— 在编辑框获焦之前,
// 中文输入法的按键也不该被拦截, 与"英文直入照旧"互为表里。
func (s *surface) SetIMEEnabled(on bool) {
	s.imeEnabled = on
	if !on {
		s.imeMarked = ""
	}
}

// routeIMEKeyDown 判定 keyDown 是否交给输入法, 是则送入 handleEvent 并
// 返回 true (调用方不再投内核按键事件)。必须在 GUI 线程调用。
func (s *surface) routeIMEKeyDown(view, ev objc.ID) bool {
	if !s.imeEnabled {
		return false
	}
	// Ctrl/Alt 组合是快捷键, 不过输入法 (与 win32 的 ImmProcessKey 口径一致;
	// Cmd 不参与字符翻译, 不拦)
	flags := objc.Send[uint64](ev, selModifierFlags)
	if flags&(nsModifierControl|nsModifierAlternate) != 0 {
		return false
	}
	// 正在组合: 全部按键交给输入法 (Enter 提交原串 / Esc 取消都由它处理,
	// 内核此时不该收到任何键)
	if s.imeMarked != "" {
		if ctx := s.imeContext(); ctx != 0 {
			ctx.Send(selHandleEvent, ev)
			return true
		}
		return false
	}
	// 可打印键且当前输入源是输入法: 交给输入法决定 (纯键盘布局 → 直入)
	if r := firstCharacter(ev); r > 0x20 && r != 0x7f && isIMEActive() {
		if ctx := s.imeContext(); ctx != 0 {
			ctx.Send(selHandleEvent, ev)
			return true
		}
	}
	return false
}

// firstCharacter 取 event.characters 的首字符 (非法/空 → 0)。
func firstCharacter(ev objc.ID) rune {
	str := ev.Send(selCharacters)
	if str == 0 {
		return 0
	}
	ch := nsToGo(str)
	for _, r := range ch {
		return r
	}
	return 0
}

// ===== 输入源检测 (纯键盘布局 vs 输入法) =====
//
// Carbon 的 TIS (Text Input Source) API: InputSourceID 形如
// "com.apple.keylayout.ABC" (纯布局) 或 "com.apple.inputmethod.SCIM.*"
// (简体拼音)。前缀判别是启发式, 但覆盖了"用户在 ABC 和拼音间切换"这个
// 唯一的真实场景 —— 没有它, ABC 布局下直入的英文也会被拦一道。
//
// TISCopyCurrentKeyboardInputSource 返回 +1 引用, 用完 CFRelease;
// kTISPropertyInputSourceID 是 CFStringRef 全局变量, Dlsym 拿到的是
// "变量的地址", 该地址本身又正是 TISGetInputSourceProperty 要的
// CFStringRef* 参数。

var (
	tisOnce         sync.Once
	tisCopy         func() uintptr
	tisGetProperty  func(src, key uintptr) uintptr
	tisRelease      func(x uintptr)
	tisIDKeyAddress uintptr
	tisOk           bool
)

func initTIS() {
	tisOk = tisLoad()
}

func tisLoad() (ok bool) {
	// RegisterLibFunc 找不到符号时会 panic: Carbon 在所有 macOS 上都在,
	// 正常不会走到这里, 但 TIS 缺席不该带崩整个 GUI —— 按加载失败处理。
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	handle, err := purego.Dlopen("/System/Library/Frameworks/Carbon.framework/Carbon", purego.RTLD_GLOBAL|purego.RTLD_LAZY)
	if err != nil {
		return false
	}
	purego.RegisterLibFunc(&tisCopy, handle, "TISCopyCurrentKeyboardInputSource")
	purego.RegisterLibFunc(&tisGetProperty, handle, "TISGetInputSourceProperty")
	// CFRelease 在 CoreFoundation (随 Cocoa 已加载), 从默认搜索路径取
	purego.RegisterLibFunc(&tisRelease, purego.RTLD_DEFAULT, "CFRelease")
	addr, err := purego.Dlsym(handle, "kTISPropertyInputSourceID")
	if err != nil {
		return false
	}
	tisIDKeyAddress = addr
	return true
}

// isIMEActive 报告当前输入源是否为输入法 (而非纯键盘布局)。
// 检测失败一律返回 false (输入法路径不生效 = 退回英文直入, 宁可少不可错)。
func isIMEActive() bool {
	tisOnce.Do(initTIS)
	if !tisOk || tisCopy == nil || tisGetProperty == nil || tisIDKeyAddress == 0 {
		return false
	}
	src := tisCopy()
	if src == 0 {
		return false
	}
	if tisRelease != nil {
		defer tisRelease(src)
	}
	cfID := tisGetProperty(src, tisIDKeyAddress)
	if cfID == 0 {
		return false
	}
	// CFStringRef 与 NSString toll-free bridged, 直接按对象发消息
	id := nsToGo(objc.ID(cfID))
	return id != "" && !strings.HasPrefix(id, "com.apple.keylayout.")
}

package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== P3-3: 剪贴板 =====
//
// 验证分两层, 与本项目其它任务一致:
//   - Go 层 (mountTestApp + 假 Surface 的内存剪贴板) 验**壳**: 读写往返、
//     失败回落 (空串 / false)、模块函数的参数与返回值类型。
//   - 全链路 (真 VM + 真脚本 import) 验**接线**: `gx/gfx` 真的导出了这两个
//     函数, 且脚本拿到的返回值类型正确。
//
// 刻意**不碰系统剪贴板**: CI 上没有剪贴板所有者, 而在开发机上跑测试改掉
// 用户正在用的剪贴板更是不可接受。假 Surface 的 clipboardHost 实现与
// globalBool/globalStr 取值器都在 helpers_test.go。

// TestClipboardRoundTrip Go 层: 写入 → 读回, 中文与 emoji 都要原样回来。
func TestClipboardRoundTrip(t *testing.T) {
	resetClipboard()
	t.Cleanup(resetClipboard)
	_, _ = mountTestApp(t, mkNode("column", nil), 300, 120)

	if got := readClipboardText(); got != "" {
		t.Fatalf("初始剪贴板应为空, got %q", got)
	}
	const want = "你好, Gox 😀"
	if !writeClipboardText(want) {
		t.Fatalf("writeClipboardText 返回 false")
	}
	if got := readClipboardText(); got != want {
		t.Fatalf("读回 = %q, want %q", got, want)
	}
	// 空串也要能写 (清空剪贴板是合法操作), 且读回来还是空串
	if !writeClipboardText("") {
		t.Fatalf("写入空串返回 false")
	}
	if got := readClipboardText(); got != "" {
		t.Fatalf("写入空串后读回 %q", got)
	}
}

// TestClipboardFailureFallsBackSoftly 拿不到剪贴板时**静默降级**:
// 读返回空串、写返回 false, 不抛异常、不中断脚本。
func TestClipboardFailureFallsBackSoftly(t *testing.T) {
	resetClipboard()
	t.Cleanup(resetClipboard)
	_, _ = mountTestApp(t, mkNode("column", nil), 300, 120)

	clipMu.Lock()
	clipFail = true
	clipMu.Unlock()

	if got := readClipboardText(); got != "" {
		t.Fatalf("失败时应回落空串, got %q", got)
	}
	if writeClipboardText("x") {
		t.Fatalf("失败时 write 应返回 false")
	}
}

// TestClipboardModuleExports 模块函数本身: 参数缺失不 panic, 返回值类型正确。
func TestClipboardModuleExports(t *testing.T) {
	resetClipboard()
	t.Cleanup(resetClipboard)
	_, _ = mountTestApp(t, mkNode("column", nil), 300, 120)

	// 无参: 写返回 false (不 panic)。比 Value 而不是比指针 ——
	// object.NewBoolean 每次都新分配, 只有字面量 true/false 才是单例。
	if b, ok := jsClipboardWriteText().(*object.Boolean); !ok || b.Value {
		t.Fatalf("无参 clipboardWriteText = %v, want false", b)
	}
	if b, ok := jsClipboardWriteText(object.NewString("abc")).(*object.Boolean); !ok || !b.Value {
		t.Fatalf("clipboardWriteText(\"abc\") = %v, want true", b)
	}
	got := jsClipboardReadText()
	s, ok := got.(*object.String)
	if !ok {
		t.Fatalf("clipboardReadText 返回 %s, want string", got.Type())
	}
	if s.Value != "abc" {
		t.Fatalf("读回 %q, want abc", s.Value)
	}
}

// TestClipboardFullChain 全链路: 脚本 import 后写入再读回, 且返回值类型正确。
func TestClipboardFullChain(t *testing.T) {
	resetClipboard()
	t.Cleanup(resetClipboard)
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })
	v, err := vm.EvalVM(`
		import { h, render, clipboardReadText, clipboardWriteText } from "gx/gfx";
		globalThis.copyOk = false;
		globalThis.pasted = "";
		globalThis.noArg = "?";
		globalThis.copy = () => { globalThis.copyOk = clipboardWriteText("from-gox"); };
		globalThis.paste = () => { globalThis.pasted = clipboardReadText(); };
		globalThis.bad = () => { globalThis.noArg = clipboardWriteText(); };
		render(h("column", null, h("text", null, "clipboard")),
		       { title: "clip", width: 300, height: 120 });
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	driveSteps(t, v,
		func() { callGlobal(t, v, "copy") },
		func() { callGlobal(t, v, "paste") },
		func() { callGlobal(t, v, "bad") },
	)

	if b, ok := globalBool(t, v, "copyOk"); !ok || !b {
		t.Fatalf("copyOk = %v (ok=%v), want true", b, ok)
	}
	if s, ok := globalStr(t, v, "pasted"); !ok || s != "from-gox" {
		t.Fatalf("pasted = %q (ok=%v), want %q", s, ok, "from-gox")
	}
	if b, ok := globalBool(t, v, "noArg"); !ok || b {
		t.Fatalf("noArg = %v (ok=%v), want false", b, ok)
	}
	// 真后端收到的一定是脚本给的那串文本
	clipMu.Lock()
	got := clipText
	clipMu.Unlock()
	if got != "from-gox" {
		t.Fatalf("后端收到的文本 = %q, want %q", got, "from-gox")
	}
}

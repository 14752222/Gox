package stdlib

import (
	"errors"
	"io"
	"os"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== 回调桥「两槽同步消费」契约回归 (看板 rr1O8P) =====
//
// 回调桥在 JS 抛出时会写两个槽:
//   - 错误槽: object.TakeCallbackError()  —— Go 侧 error (只有字符串)
//   - 值槽:   object.TakeCallbackErrorValue() —— `throw x` 的 x 本身
//
// 但桥**只在错误类型是 *vm.ThrowError / *vm.jsThrow 时才写值槽**
// (见 vm.setCallbackErrorValueFromThrow), 其余错误只写错误槽 ⇒ **值槽会
// 原封不动地保留上一次的抛出值**。
//
// 因此消费方必须**两槽一起消费**。**只消费错误槽**的位点会把过期值留在
// 原地, 直到之后某个取两槽的位点 (stdlib/async.go、promise.go、eval.go、
// solid.go 等) 把它当成"本次抛出的值"读走 —— 症状是**静默错值**: 不报错,
// 只是用户 catch 到的是上一次的值。
//
// 本文件钉住 stdlib 侧 8 处 reportUncaught 位点
// (fs.go:622/638、http.go:206/427/457、update_module.go:137/184/208)
// 与 Reflect.construct 位点的两槽同步契约。

// resetCallbackSlots 清空两槽, 隔离用例间的包级全局状态。
func resetCallbackSlots() {
	object.TakeCallbackError()
	object.TakeCallbackErrorValue()
}

// captureStderr 捕获 fn 执行期间写往 os.Stderr 的文本。
// reportUncaught 是直接 fmt.Fprintf(os.Stderr, ...) 的, 只能靠换 fd 抓。
// 报告文本很短 (远小于 pipe 缓冲), 同步读回不会阻塞。
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	fn()
	os.Stderr = orig
	if err := w.Close(); err != nil {
		t.Fatalf("关闭写端: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("读回 stderr: %v", err)
	}
	r.Close()
	return string(out)
}

// 8 处 reportUncaught 位点必须**同时消费值槽**。
// 修复前它们只消费错误槽, 值槽的原值会一直挂着。
func TestReportUncaughtConsumesValueSlot(t *testing.T) {
	resetCallbackSlots()
	t.Cleanup(resetCallbackSlots)

	object.SetCallbackError(errors.New("Error: PHASE1"))
	object.SetCallbackErrorValue(object.NewString("POISON"))

	out := captureStderr(t, func() {
		reportUncaught(object.TakeCallbackError())
	})
	if out == "" {
		t.Fatal("reportUncaught 没有向 stderr 报告任何内容")
	}
	if v := object.TakeCallbackErrorValue(); v != object.UndefinedSingleton {
		t.Fatalf("值槽未被消费, 残留 %s —— 之后任一取两槽的位点会把它当成真实抛出值 (静默错值)",
			v.Inspect())
	}
}

// 复现 rr1O8P 的**真实危害链路**, 而不只是"槽没清"这件事本身:
// 阶段1 的抛出值经「只写错误槽」的桥错误泄漏到阶段2 的消费点。
//
// 这是修复前会失败、修复后通过的判据用例 —— 只清错误槽时, 阶段2 读到的
// 是阶段1 的 "PHASE1", 而它本该读到 undefined (本次没有原值)。
func TestStaleValueSlotDoesNotLeakIntoNextBridgeError(t *testing.T) {
	resetCallbackSlots()
	t.Cleanup(resetCallbackSlots)

	// 阶段1: JS 回调抛 "PHASE1", 由 reportUncaught 报告并消费 (8 处位点同形)。
	object.SetCallbackError(errors.New("Error: PHASE1"))
	object.SetCallbackErrorValue(object.NewString("PHASE1"))
	captureStderr(t, func() {
		reportUncaught(object.TakeCallbackError())
	})

	// 阶段2: 一次**非 ThrowError** 的桥失败 —— 桥只写错误槽, 值槽不刷新。
	object.SetCallbackError(errors.New("real bridge failure"))

	// 按 stdlib/async.go、promise.go 的取两槽口径消费。
	cbErr := object.TakeCallbackError()
	if cbErr == nil {
		t.Fatal("阶段2 应当报告错误")
	}
	if thrown := object.TakeCallbackErrorValue(); thrown != object.UndefinedSingleton {
		t.Fatalf("阶段2 读到了阶段1 的过期值 %s (应为 undefined) —— reportUncaught 漏消费值槽",
			thrown.Inspect())
	}
}

// reportUncaught 的文本必须优先用**原始抛出值**渲染: 非 Error 抛出值
// (字符串/数字) 只有值槽里才有, 一律走 cbErr.Error() 会拿到 Go 侧包装文本。
func TestUncaughtMessagePrefersOriginalThrownValue(t *testing.T) {
	t.Cleanup(resetCallbackSlots)

	cases := []struct {
		name    string
		thrown  object.Value // nil 表示值槽为空
		cbErr   error
		wantOut string
	}{
		{
			name:    "字符串抛出值不带引号",
			thrown:  object.NewString("boom"),
			cbErr:   errors.New("Error: boom"),
			wantOut: "Uncaught boom\n",
		},
		{
			name:    "Error 对象保留类型前缀",
			thrown:  object.NewErrorWithName("TypeError", "bad"),
			cbErr:   errors.New("TypeError: bad"),
			wantOut: "Uncaught TypeError: bad\n",
		},
		{
			name:    "数字抛出值",
			thrown:  object.NewNumber(42),
			cbErr:   errors.New("42"),
			wantOut: "Uncaught 42\n",
		},
		{
			name:    "值槽为空时回退 Go 错误文本",
			thrown:  nil,
			cbErr:   errors.New("plain go failure"),
			wantOut: "Uncaught plain go failure\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetCallbackSlots()
			if tc.thrown != nil {
				object.SetCallbackErrorValue(tc.thrown)
			}
			object.SetCallbackError(tc.cbErr)

			got := captureStderr(t, func() {
				reportUncaught(object.TakeCallbackError())
			})
			if got != tc.wantOut {
				t.Fatalf("stderr = %q, 期望 %q", got, tc.wantOut)
			}
		})
	}
}

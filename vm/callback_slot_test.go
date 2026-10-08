package vm

// ===== VM 侧回调桥「两槽同步消费」契约回归 (看板 rZNsX9) =====
//
// 与 stdlib/callback_slot_test.go (rr1O8P) 是同一机制的两个半边: 那边钉的是
// stdlib 的 8 处 reportUncaught 位点, 这边钉的是 **VM 主循环** —— ~25 处
// checkCallbackErr 调用点与 callToString, 它们都经由 vm.checkCallbackErr /
// vm.callToString 这两个汇聚点, 故只需钉住这两个函数。
//
// 回调桥写两个槽:
//   - 错误槽: object.TakeCallbackError()       —— Go error (只有字符串)
//   - 值槽:   object.TakeCallbackErrorValue()  —— `throw x` 的 x 本身
//
// 桥**只在错误类型是 *ThrowError / *jsThrow 时才写值槽**
// (见 setCallbackErrorValueFromThrow), 其余错误只写错误槽 ⇒ 值槽会原封不动
// 保留上一次的抛出值。而 object.CallFunction 每次进入只重置**错误槽**、**不
// 重置值槽** —— 残留值能跨任意多次后续桥调用存活。于是下一次「只写错误槽」
// 的桥失败被任一两槽消费者 (stdlib/{async,promise,eval,solid}.go) 读走时,
// 用户拿到的是**上一次的抛出值**: 不报错, 只是值错 (静默错值)。

import (
	"errors"
	"testing"

	"github.com/14752222/Gox/object"
)

// resetBridgeSlots 清空两槽, 隔离用例间的包级全局状态。
func resetBridgeSlots() {
	object.TakeCallbackError()
	object.TakeCallbackErrorValue()
}

// checkCallbackErr 必须**两槽同步消费**: 取到错误的同时清掉值槽。
func TestCheckCallbackErrConsumesValueSlot(t *testing.T) {
	resetBridgeSlots()
	t.Cleanup(resetBridgeSlots)

	// 模拟「本次桥失败只写错误槽」—— 值槽里是上一次抛出的 PHASE1。
	object.SetCallbackError(errors.New("TypeError: phase2"))
	object.SetCallbackErrorValue(object.NewString("PHASE1"))

	vm := &VM{}
	err := vm.checkCallbackErr()
	if err == nil {
		t.Fatal("checkCallbackErr 应返回布置的错误")
	}
	if v := object.TakeCallbackErrorValue(); v != object.UndefinedSingleton {
		t.Fatalf("值槽未被消费, 残留 %s —— 之后任一两槽消费者会把它当成真实抛出值 (静默错值)",
			v.Inspect())
	}
}

// 反向契约: 没有桥错误时**不得**抢先清空值槽 —— 值槽里的东西不由本次负责,
// 硬清会把尚未被真正消费方读走的值丢掉。
func TestCheckCallbackErrKeepsValueSlotWhenNoError(t *testing.T) {
	resetBridgeSlots()
	t.Cleanup(resetBridgeSlots)

	object.SetCallbackErrorValue(object.NewString("PENDING"))
	vm := &VM{}
	if err := vm.checkCallbackErr(); err != nil {
		t.Fatalf("无桥错误时 checkCallbackErr 应返回 nil, got %v", err)
	}
	if v := object.TakeCallbackErrorValue(); v == object.UndefinedSingleton {
		t.Fatal("无桥错误时不该动值槽 —— 抢清会丢掉尚未被消费的值")
	}
}

// VM 主循环真实路径: getter 抛非 Error 值 ⇒ OP_GET_PROP 后的
// checkCallbackErr 必须把两槽一起收走。
//
// 这是 rr1O8P 第一条判据的 VM 版 —— rr1O8P 验收要求「必须走到 VM 主循环而
// 不只是 stdlib 层」, 本例与下面两条都经由完整管线 (lexer→parser→compiler→VM)。
func TestVMMainLoopClearsBridgeValueSlot(t *testing.T) {
	resetBridgeSlots()
	t.Cleanup(resetBridgeSlots)

	evalJS(t, `try { ({ get boom() { throw "PHASE1"; } }).boom; } catch (e) { }`)

	if v := object.TakeCallbackErrorValue(); v != object.UndefinedSingleton {
		t.Fatalf("VM 主循环消费掉错误槽后值槽仍残留 %s", v.Inspect())
	}
}

// callToString 的错误出口同样要消费值槽: 它既不重抛也不上报, 是典型的
// "错误被吞掉、值却留下" 的位点。
//
// 路径: 顶层 throw 的值在渲染未捕获错误文本时走 EvalVM → uncaughtError →
// thrownDisplayString → vm.callToString → 用户 toString 抛错 ⇒ 桥写两槽,
// callToString 只取错误槽。
//
// 必须用 Eval (而非裸 vm.Run): uncaughtError 只在 Eval* 入口调用, 而
// thrownDisplayString 挂在它下面。
func TestCallToStringConsumesValueSlot(t *testing.T) {
	resetBridgeSlots()
	t.Cleanup(resetBridgeSlots)

	_, err := Eval(`
		var o = { toString: function () { throw "TOSTRING-PHASE1"; } };
		throw o;
	`)
	if err == nil {
		t.Fatal("顶层 throw 对象应产生未捕获错误")
	}
	if v := object.TakeCallbackErrorValue(); v != object.UndefinedSingleton {
		t.Fatalf("callToString 未消费值槽, 残留 %s —— 之后任一两槽消费者会把它当成真实抛出值",
			v.Inspect())
	}
}

// 完整危害链路 (修复前 FAIL): 相位1 的抛出值经「只写错误槽」的桥失败泄漏出去。
//
// 与 rr1O8P 的手法一致 —— 「只写错误槽」这一步在 object 层布置, 因为能天然
// 触发它的 JS 路径取决于引擎内部状态 (callFunction 的非 ThrowError 出口),
// 无法从 JS 侧确定性地复现; 其余三步 (相位1 的真实抛出、主循环消费、判据)
// 全部走真实路径。
func TestStaleValueSlotDoesNotLeakIntoNextVMBridgeError(t *testing.T) {
	resetBridgeSlots()
	t.Cleanup(resetBridgeSlots)

	// 相位1: 真实的 getter 抛出非 Error 值 —— 两槽同时写入, 主循环随后消费。
	evalJS(t, `try { ({ get boom() { throw "PHASE1"; } }).boom; } catch (e) { }`)

	// 相位2: 只写错误槽的桥失败 (模拟), 由主循环的下一次 checkCallbackErr 消费。
	// 用不带 getter 的裸属性读, 避免 CallFunction 在入口重置错误槽。
	object.SetCallbackError(errors.New("TypeError: phase2-only-error-slot"))
	if _, err := testEvalCatch(t, `({ plain: 1 }).plain;`); err == nil {
		t.Fatal("相位2 的桥错误应被重抛")
	}

	// 判据: 主循环消费完这一轮桥信号后, 两槽必须都是空的。
	// 修复前这里读到的是相位1 的 "PHASE1" —— 它会在之后任一两槽消费者
	// (promise/async/eval/solid) 那里被当成"本次抛出的值"。
	if v := object.TakeCallbackErrorValue(); v != object.UndefinedSingleton {
		t.Fatalf("相位2 的桥失败后值槽残留 %s (应为 undefined) —— 静默错值", v.Inspect())
	}
}

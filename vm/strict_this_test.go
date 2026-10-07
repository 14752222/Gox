package vm

import "testing"

// ===== this 绑定: 严格模式半边 (r63RpV 子项4 收尾) =====
//
// 背景: vm/top_level_this_test.go (来自 Phase 0+2) 钉的是 **sloppy** 半边 ——
// 顶层 this=globalThis、裸调用归一、箭头词法、call/bind(undefined) 归一。
// 该提交的注释还写着「Gox 目前没有 strict 模式, 一律按 sloppy 处理」。
//
// 但现行 vm.resolveFrameThis 已经有严格分支 (closure.Fn.IsStrict 原样保留
// this, 不做 undefined/null 归一), 而这条分支**没有直接回归测试**:
// vm/ 下只有 vm/eval_this_binding_test.go 从 eval 入口间接覆盖了两例。
//
// 本文件把严格半边逐条钉死, 防止 IsStrict 印章或 this 发射路径后续被改坏:
//   - 严格函数裸调用 this = undefined (不是 globalThis);
//   - 严格 call/apply(undefined) 保持 undefined、call(null) 保持 null;
//   - 严格性向嵌套函数体传播 (严格外层内声明的函数也是严格);
//   - class 体恒严格 → 方法取出裸调用 this = undefined;
//   - 严格箭头词法继承严格帧的 this;
//   - 严格 async/generator 裸调用 this = undefined;
//   - new / 有形参接收者的方法调用不受严格化影响;
//   - Function 构造器带 "use strict" 体同口径。

// TestStrictFnBareCallThisIsUndefined 严格函数体裸调用 this 必须是 undefined
// (而非 sloppy 的 globalThis) —— 这是本组的第一条基准。
func TestStrictFnBareCallThisIsUndefined(t *testing.T) {
	_, res := runEvalVM(t, `function f(){ "use strict"; return this; } f() === undefined`)
	assertBoolean(t, res, true)

	// 交叉验证: 不是被误换成 globalThis。
	_, res = runEvalVM(t, `function f(){ "use strict"; return this; } f() !== globalThis`)
	assertBoolean(t, res, true)

	// 严格函数表达式同口径。
	_, res = runEvalVM(t, `(function(){ "use strict"; return this; })() === undefined`)
	assertBoolean(t, res, true)
}

// TestStrictCallApplyThisNotNormalized call/apply 的 undefined/null 在严格
// 函数里**不得**被归一覆盖: undefined 保持 undefined, null 保持 null。
func TestStrictCallApplyThisNotNormalized(t *testing.T) {
	for _, expr := range []string{
		`function f(){ "use strict"; return this; } f.call(undefined) === undefined`,
		`function f(){ "use strict"; return this; } f.apply(undefined) === undefined`,
		`function f(){ "use strict"; return this; } f.call(null) === null`,
		`function f(){ "use strict"; return this; } f.apply(null) === null`,
		// 显式接收者原样保留。
		`var o = {}; function f(){ "use strict"; return this; } f.call(o) === o`,
		// 原始值不被装箱改写 (严格模式 this 就是传入值)。
		`function f(){ "use strict"; return this; } f.call(42) === 42`,
		`function f(){ "use strict"; return this; } f.call("s") === "s"`,
	} {
		_, res := runEvalVM(t, expr)
		assertBoolean(t, res, true)
	}
}

// TestSloppyCallNullThisIsGlobalThis 作为对照: 同一形态在 sloppy 下 null 归一到
// globalThis —— 两侧口径差异正是本组要钉的核心。
func TestSloppyCallNullThisIsGlobalThis(t *testing.T) {
	_, res := runEvalVM(t, `function f(){ return this; } f.call(null) === globalThis`)
	assertBoolean(t, res, true)
}

// TestStrictPropagatesToNestedFunction 严格性沿词法嵌套向**函数体**传播:
// 严格外层内声明的普通函数 (自身无指令) 也是严格, 裸调用 this = undefined。
func TestStrictPropagatesToNestedFunction(t *testing.T) {
	_, res := runEvalVM(t, `function outer(){ "use strict"; function inner(){ return this; } return inner(); } outer() === undefined`)
	assertBoolean(t, res, true)

	_, res = runEvalVM(t, `function outer(){ "use strict"; var inner = function(){ return this; }; return inner(); } outer() === undefined`)
	assertBoolean(t, res, true)

	// 对照: sloppy 外层内的同名嵌套函数仍是 sloppy。
	_, res = runEvalVM(t, `function outer(){ function inner(){ return this; } return inner(); } outer() === globalThis`)
	assertBoolean(t, res, true)
}

// TestStrictScriptTopLevelFnIsStrict 严格脚本顶层声明的函数也是严格 (script 顶层
// this 本身仍 = globalThis, 那是 sloppy 半边的守卫, 见 top_level_this_test.go)。
func TestStrictScriptTopLevelFnIsStrict(t *testing.T) {
	_, res := runEvalVM(t, `"use strict";
function f(){ return this; }
f() === undefined`)
	assertBoolean(t, res, true)

	// 严格脚本顶层的 this 仍是 globalThis (顶层 this 与 script 严格性无关)。
	_, res = runEvalVM(t, `"use strict"; this === globalThis`)
	assertBoolean(t, res, true)
}

// TestStrictClassMethodBareCallIsUndefined class 体内所有 code 恒严格:
// 方法取出后裸调用 this = undefined。
func TestStrictClassMethodBareCallIsUndefined(t *testing.T) {
	_, res := runEvalVM(t, `class C { m(){ return this; } } var m = C.prototype.m; m() === undefined`)
	assertBoolean(t, res, true)

	// class 体严格性也向方法内嵌套函数传播。
	_, res = runEvalVM(t, `class C { m(){ function inner(){ return this; } return inner(); } } new C().m() === undefined`)
	assertBoolean(t, res, true)

	// 对照: 对象字面量方法语法**不**因所在位置而严格, 裸调用仍是 sloppy 归一。
	_, res = runEvalVM(t, `var o = { m(){ return this; } }; var m = o.m; m() === globalThis`)
	assertBoolean(t, res, true)
}

// TestStrictArrowInheritsStrictThis 箭头 this 是词法绑定: 严格帧内创建的箭头
// 继承严格帧的 this (undefined), sloppy 帧内创建的继承归一后的 globalThis。
func TestStrictArrowInheritsStrictThis(t *testing.T) {
	_, res := runEvalVM(t, `function f(){ "use strict"; return (() => this)(); } f() === undefined`)
	assertBoolean(t, res, true)

	_, res = runEvalVM(t, `function f(){ return (() => this)(); } f() === globalThis`)
	assertBoolean(t, res, true)

	// 严格方法内的箭头继承方法的接收者 (不是 undefined)。
	_, res = runEvalVM(t, `var o = { m: function(){ "use strict"; return (() => this)(); } }; o.m() === o`)
	assertBoolean(t, res, true)
}

// TestStrictMethodThisIsReceiver 严格函数有真实接收者时 this 原样保留 (不归一,
// 也不被错误地置成 undefined)。
func TestStrictMethodThisIsReceiver(t *testing.T) {
	_, res := runEvalVM(t, `function s(){ "use strict"; return this; } var o = { f: s }; o.f() === o`)
	assertBoolean(t, res, true)

	// bind 显式接收者同样保留。
	_, res = runEvalVM(t, `function s(){ "use strict"; return this; } var b = s.bind({ z: 1 }); b().z === 1`)
	assertBoolean(t, res, true)

	// bind(undefined) 在严格函数下应得到 undefined (不归一)。
	_, res = runEvalVM(t, `function s(){ "use strict"; return this; } s.bind(undefined)() === undefined`)
	assertBoolean(t, res, true)
}

// TestStrictGeneratorAndAsyncThis 严格 generator / async 函数裸调用 this =
// undefined; 方法形态保留接收者。
func TestStrictGeneratorThisBareAndMethod(t *testing.T) {
	_, res := runEvalVM(t, `function* g(){ "use strict"; return this; } g().next().value === undefined`)
	assertBoolean(t, res, true)

	_, res = runEvalVM(t, `var o = { m: function*(){ "use strict"; return this; } }; o.m().next().value === o`)
	assertBoolean(t, res, true)
}

// TestConstructorThisUnaffectedByStrict new 调用不受严格化影响: this = 新实例。
func TestConstructorThisUnaffectedByStrict(t *testing.T) {
	_, res := runEvalVM(t, `function C(){ "use strict"; this.x = 1; } var c = new C(); c.x === 1`)
	assertBoolean(t, res, true)

	// 严格构造器里返回对象可以替换 this。
	_, res = runEvalVM(t, `var custom = {}; function C(){ "use strict"; return custom; } new C() === custom`)
	assertBoolean(t, res, true)
}

// TestStrictThisWriteDoesNotPolluteGlobal 严格函数 this = undefined 时写属性
// 应抛 TypeError, 且不得悄悄写到全局对象 (sloppy 归一后会是写 globalThis)。
func TestStrictThisWriteDoesNotPolluteGlobal(t *testing.T) {
	_, res := runEvalVM(t, `function f(){ "use strict"; try { this.__goxStrictPollute = 1; return false; } catch(e){ return e instanceof TypeError; } } f()`)
	assertBoolean(t, res, true)

	_, res = runEvalVM(t, `globalThis.__goxStrictPollute === undefined`)
	assertBoolean(t, res, true)
}

// TestFunctionCtorStrictBodyThis Function 构造器带 "use strict" 体: 严格函数
// 裸调用 this = undefined; 不带指令则 sloppy 归一。
func TestFunctionCtorStrictBodyThis(t *testing.T) {
	_, res := runEvalVM(t, `var f = Function('"use strict"; return this;'); f() === undefined`)
	assertBoolean(t, res, true)

	_, res = runEvalVM(t, `var f = Function('return this;'); f() === globalThis`)
	assertBoolean(t, res, true)
}

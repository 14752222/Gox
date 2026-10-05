package vm

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== 未声明全局标识符的读取/赋值语义 (rYVgne) =====
//
// 规范口径 (ES2023 §8.1 / §13.3, Node 22 实测为准):
//   - 标识符引用未声明 ⇒ ReferenceError；
//   - typeof 未声明名 ⇒ "undefined"，**不报错**；
//   - 赋值: sloppy 下建全局属性；strict 下抛 ReferenceError；
//   - 读取「全局对象的属性」(globalThis.x) 与「标识符」是两条路径，
//     globalThis.x 本就该是 undefined。
//
// 本组用例锁定标识符路径不误伤内建全局与宿主注入名，并防回退。
//
// 已知边界 (本运行时尚未建模严格模式——无 "use strict" 语言模式)：
// strict 下赋值未声明名应抛 ReferenceError，Gox 目前按 sloppy 口径建全局属性。
// 该缺口需先有 strict 模式跟踪 (parser/compiler 层)，不在本组范围，故不钉测试。

// --- A. 未声明全局标识符的“读取”必须抛 ReferenceError ---

func TestUndeclaredGlobalReadThrows(t *testing.T) {
	// 直接标识符读取
	assertJSThrows(t, `__gox_undeclared_read__`, "ReferenceError")
	// 作为调用目标
	assertJSThrows(t, `__gox_undeclared_fn__()`, "ReferenceError")
	// 参与运算
	assertJSThrows(t, `__gox_undeclared_read__ + 1`, "ReferenceError")
	// 在函数体内
	assertJSThrows(t, `(function(){ return __gox_undeclared_infn__; })()`, "ReferenceError")
	// eval 内的读取同样必须抛出 (曾静默吞成 undefined)
	assertJSThrows(t, `eval("__gox_undeclared_eval__")`, "ReferenceError")
	assertJSThrows(t, `eval("__gox_undeclared_eval_fn__()")`, "ReferenceError")
}

// eval 中显式 throw 必须向外传播 (曾整体被吞)。
func TestEvalThrowPropagates(t *testing.T) {
	assertJSThrows(t, `eval("throw new Error('boom')")`, "Error")
	assertJSThrows(t, `eval("throw new TypeError('t')")`, "TypeError")
	// eval 内部自己 catch 掉的异常不应外泄 (用单个表达式源，避免多语句完成值限制)。
	assertJS(t, `eval("(function(){ try { __gox_caught__ } catch (e) { return e.name; } })()")`, "ReferenceError")
}

// --- B. typeof 未声明名 ⇒ "undefined"，不报错 ---

func TestTypeofUndeclaredIsUndefined(t *testing.T) {
	assertJS(t, `typeof __gox_undeclared_typeof__`, "undefined")
	assertJS(t, `typeof __gox_undeclared_typeof__ === "undefined"`, "true")
	// 函数内同理
	assertJS(t, `(function(){ return typeof __gox_undeclared_typeof_infn__; })()`, "undefined")
	// eval 内同理
	assertJS(t, `eval("typeof __gox_undeclared_typeof_eval__")`, "undefined")
}

// --- C. 赋值: sloppy 建全局属性 (Gox 无 strict 模式，此处锁 sloppy 口径) ---

func TestUndeclaredGlobalAssignSloppy(t *testing.T) {
	// 赋值未声明名后，该名字成为可读的全局绑定
	got := evalWithStdlib(t, `(function(){ __gox_sloppy_assign__ = 7; return __gox_sloppy_assign__; })()`)
	assertNumber(t, got, 7)

	// 全局对象上可见 (标识符赋值 → 全局环境 → globalThis 视图)
	assertJS(t, `(function(){ __gox_sloppy_assign2__ = 9; return globalThis.__gox_sloppy_assign2__; })()`, "9")
}

// 读取「全局对象属性」与「标识符」是两条路径：globalThis.__x__ 本就是 undefined。
func TestGlobalObjectPropertyReadIsUndefined(t *testing.T) {
	assertJS(t, `globalThis.__gox_absent_prop__`, "undefined")
	assertJS(t, `typeof globalThis.__gox_absent_prop__`, "undefined")
	assertJS(t, `("__gox_absent_prop__" in globalThis)`, "false")
}

// --- D. 内建全局 / 宿主注入名不得误伤 ---

func TestUndeclaredFixDoesNotAffectBuiltins(t *testing.T) {
	// 内建全局
	assertJS(t, `typeof Math`, "object")
	assertJS(t, `typeof Object`, "function")
	assertJS(t, `typeof JSON`, "object")
	assertJS(t, `typeof Array`, "function")
	assertJS(t, `typeof undefined`, "undefined")
	assertJS(t, `typeof NaN`, "number")
	// 宿主注入名
	assertJS(t, `typeof globalThis`, "object")
	assertJS(t, `typeof console`, "object")
	assertJS(t, `typeof setTimeout`, "function")
	assertJS(t, `typeof eval`, "function")
}

// %AsyncGeneratorFunction% 是内建 intrinsic，不是全局对象属性
// (Node: typeof AsyncGeneratorFunction === "undefined")。
// 曾误注册为全局 ⇒ 读它不抛 ReferenceError。
func TestAsyncGeneratorFunctionIsNotAGlobal(t *testing.T) {
	assertJS(t, `typeof AsyncGeneratorFunction`, "undefined")
	assertJS(t, `("AsyncGeneratorFunction" in globalThis)`, "false")
	assertJSThrows(t, `AsyncGeneratorFunction`, "ReferenceError")
	// 只应经原型链暴露
	assertJS(t, `Object.getPrototypeOf(async function*(){}).constructor.name`, "AsyncGeneratorFunction")
	assertJS(t, `typeof Object.getPrototypeOf(async function*(){}).constructor`, "function")
}

// 确保未声明名报错不会把 undefined/NaN/Infinity 这类“合法全局”当未声明。
func TestDeclaredGlobalConstantsStillReadable(t *testing.T) {
	_ = object.UndefinedSingleton
	assertJS(t, `undefined`, "undefined")
	assertJS(t, `String(NaN)`, "NaN")
	assertJS(t, `String(Infinity)`, "Infinity")
}

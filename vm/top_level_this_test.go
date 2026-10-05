package vm

import (
	"path/filepath"
	"testing"
)

// ===== this 绑定: 顶层 + 裸调用 sloppy 归一 (r63RpV, strict Phase 0+2) =====
//
// 规范 (sloppy 段):
//   - script 顶层 this = globalThis; module 顶层 this = undefined;
//   - 非箭头函数被"无接收者调用"(裸调用 / call(undefined) / call(null) /
//     apply(undefined)) 时 this = globalThis;
//   - 有真实接收者的方法调用、构造调用 (this=新实例)、super (this=当前实例)
//     的 this 不变;
//   - 箭头函数 this 是创建时所在帧的词法绑定 (顶层箭头因此拿到 globalThis)。
//
// 落点:
//   - vm.Frame.This 承载每帧生效的 this, 由 callClosure 用
//     vm.resolveFrameThis 归一并写入 (箭头取词法, 非箭头 sloppy 归一);
//   - vm.createClosure 只在箭头时按词法捕获所在帧的 this, 非箭头留 nil;
//   - OP_THIS 主帧按 script(globalThis)/module(undefined) 区分。
//
// **明确的非目标** (留待后续): eval 包装函数的 this (stdlib/eval.go 的
// `&object.Closure{Fn, Env}`, 本轮禁碰 —— 其 this 归一目前按 sloppy 生效)。

// TestScriptTopLevelThisIsGlobalThis script 顶层 this 必须是 globalThis。
func TestScriptTopLevelThisIsGlobalThis(t *testing.T) {
	_, res := runEvalVM(t, `this === globalThis`)
	assertBoolean(t, res, true)

	_, res = runEvalVM(t, `typeof this`)
	assertString(t, res, "object")
}

// TestScriptTopLevelThisViaFile 文件入口与字符串入口同口径 (EvalFileVM
// 与 EvalVM 都走 stdlib globals 装配的 globalThis 绑定)。
func TestScriptTopLevelThisViaFile(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"entry.js": `this === globalThis`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("EvalFileVM: %v", err)
	}
	assertBoolean(t, vm.LastPopped(), true)
}

// TestModuleTopLevelThisIsUndefined module 顶层 this 必须保持 undefined ——
// 这是本轮的**守卫**: 不能把顶层 this 一刀切成 globalThis。
func TestModuleTopLevelThisIsUndefined(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export const topThis = this;`,
		"entry.js": `import { topThis } from "./m.js";
topThis === undefined`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("EvalFileVM: %v", err)
	}
	assertBoolean(t, vm.LastPopped(), true)
}

// TestModuleTopLevelThisIsNotGlobalThis 进一步钉住 module 顶层 this
// **不是** globalThis (若被误接成 globalThis, 上面 === undefined 会失败,
// 这条再从另一侧交叉验证)。
func TestModuleTopLevelThisIsNotGlobalThis(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export const typeOf = typeof this;`,
		"entry.js": `import { typeOf } from "./m.js";
typeOf`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("EvalFileVM: %v", err)
	}
	assertString(t, vm.LastPopped(), "undefined")
}

// TestEntryScriptTopLevelThisWithImports 入口脚本自己有 import 时, 顶层 this
// 仍应是 globalThis (模块加载不应污染入口 VM 的 this 语义); 同时被导入模块
// 的顶层 this 仍是 undefined。
func TestEntryScriptTopLevelThisWithImports(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export const topThis = this;`,
		"entry.js": `import { topThis } from "./m.js";
(this === globalThis) && (topThis === undefined)`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("EvalFileVM: %v", err)
	}
	assertBoolean(t, vm.LastPopped(), true)
}

// ===== 裸调用 / 方法调用 / 构造 / 箭头 (Phase 2) =====

// TestBareCallThisIsGlobalThis 裸调用的非箭头函数 this = globalThis。
func TestBareCallThisIsGlobalThis(t *testing.T) {
	_, res := runEvalVM(t, `function f(){ return this; } f() === globalThis`)
	assertBoolean(t, res, true)
}

// TestMethodCallThisIsReceiver 有真实接收者的方法调用 this = 接收者 (不变)。
func TestMethodCallThisIsReceiver(t *testing.T) {
	_, res := runEvalVM(t, `var o = { m: function(){ return this; } }; o.m() === o`)
	assertBoolean(t, res, true)
}

// TestCallUndefinedThisIsGlobalThis 显式 call/apply(undefined|null) 同裸调用口径。
func TestCallUndefinedThisIsGlobalThis(t *testing.T) {
	for _, expr := range []string{
		`function f(){ return this; } f.call(undefined) === globalThis`,
		`function f(){ return this; } f.call(null) === globalThis`,
		`function f(){ return this; } f.apply(undefined) === globalThis`,
		`function f(){ return this; } f.apply(null) === globalThis`,
	} {
		_, res := runEvalVM(t, expr)
		assertBoolean(t, res, true)
	}
}

// TestBareCallDoesNotInheritCreationThis 裸调用不继承"创建处方法"的 this ——
// 这是 Phase 0 单独落地时最易错的一点 (旧实现把创建帧的 this 烘进闭包)。
func TestBareCallDoesNotInheritCreationThis(t *testing.T) {
	_, res := runEvalVM(t, `var o = { m: function(){ function inner(){ return this; } return inner(); } }; o.m() === globalThis`)
	assertBoolean(t, res, true)
}

// TestConstructorThisIsInstance 构造调用的 this = 新实例 (不变)。
func TestConstructorThisIsInstance(t *testing.T) {
	_, res := runEvalVM(t, `function C(){ this.x = 1; } var c = new C(); c.x === 1`)
	assertBoolean(t, res, true)
}

// TestArrowThisIsLexical 箭头 this 词法: 方法内箭头拿到方法的 this。
func TestArrowThisIsLexical(t *testing.T) {
	_, res := runEvalVM(t, `var o = { m: function(){ return () => this; } }; o.m()() === o`)
	assertBoolean(t, res, true)
}

// TestTopLevelArrowThisIsGlobalThis 顶层箭头词法捕获顶层 this = globalThis。
func TestTopLevelArrowThisIsGlobalThis(t *testing.T) {
	_, res := runEvalVM(t, `var a = () => this; a() === globalThis`)
	assertBoolean(t, res, true)

	// 对象属性箭头不因方法调用而重绑 —— 仍是顶层词法 this。
	_, res = runEvalVM(t, `var o = { a: () => this }; o.a() === globalThis`)
	assertBoolean(t, res, true)
}

// TestArrowInBareFnCapturesNormalizedThis 裸调用函数内创建的箭头, 捕获的是
// 归一后的 this (globalThis)。
func TestArrowInBareFnCapturesNormalizedThis(t *testing.T) {
	_, res := runEvalVM(t, `function g(){ return () => this; } g()() === globalThis`)
	assertBoolean(t, res, true)
}

// TestArrowInMethodCapturesMethodThis 方法内创建的箭头捕获方法的 this;
// 裸调用方法里再建的箭头则捕获归一后的 globalThis。
func TestArrowInMethodCapturesMethodThis(t *testing.T) {
	_, res := runEvalVM(t, `var o = { m: function(){ return () => this; } }; o.m()() === o`)
	assertBoolean(t, res, true)
}

// TestBindUndefinedThisIsGlobalThis bind(undefined) 的目标按 sloppy 归一口径。
func TestBindUndefinedThisIsGlobalThis(t *testing.T) {
	_, res := runEvalVM(t, `function f(){ return this; } f.bind(undefined)() === globalThis`)
	assertBoolean(t, res, true)
}

// TestArrayCallbackThisIsGlobalThis Array.prototype.map 不传 thisArg 时回调
// 的 this = globalThis (sloppy)。
func TestArrayCallbackThisIsGlobalThis(t *testing.T) {
	_, res := runEvalVM(t, `[1].map(function(){ return this; })[0] === globalThis`)
	assertBoolean(t, res, true)
}

// TestGeneratorThisBareAndMethod 生成器: 裸调用 -> globalThis; 方法调用 -> 接收者。
func TestGeneratorThisBareAndMethod(t *testing.T) {
	_, res := runEvalVM(t, `function* g(){ return this; } g().next().value === globalThis`)
	assertBoolean(t, res, true)

	_, res = runEvalVM(t, `var o = { m: function*(){ return this; } }; o.m().next().value === o`)
	assertBoolean(t, res, true)
}

// TestSuperThisPropagation super() 保留当前实例的 this (派生类构造器)。
func TestSuperThisPropagation(t *testing.T) {
	_, res := runEvalVM(t, `
class B { constructor(){ this.b = 1; } }
class D extends B { constructor(){ super(); this.d = this.b; } }
var d = new D();
d.d === 1 && d.b === 1`)
	assertBoolean(t, res, true)
}

package vm

import (
	"path/filepath"
	"testing"
)

// ===== 模块命名空间的活绑定 (r8tHPv) =====
//
// 规范 11.4.6 把模块命名空间定义成 exotic object: 它的 [[Get]] **每次**都回源
// 模块的绑定槽取值, 所以导入方在模块求值之后修改导出绑定, 命名空间上读到的
// 必须是新值。
//
// Gox 的命名空间是普通 *object.Object, 此前 buildNamespace 在物化时逐名
// resolve 后 obj.SetProperty(name, v) —— 活绑定被固化成**快照**:
//
//	var x = 1; export { x }; ... x = 2;   →   imported.x 永远是 1
//
// 于是 test262 `language/expressions/dynamic-import/usage/
// *gtbndng-indirect-update*` 一族 36 条全挂 (报 Expected SameValue(«1», «2»))。
//
// 现状: 活导出 (顶层词法声明 / 具名再导出) 落成读取器, 每次读取回源模块导出槽;
// 静态导出 (内置模块塞进 Named 的值、匿名 default) 仍是数据属性。本文件钉住
// 这条分界, 以及"读取器化"没有把枚举 / 描述符 / 再导出链带偏。

// TestNamespaceLiveBindingReflectsLaterUpdate 同一个命名空间对象在导出绑定
// 被改之后读到**新值** —— 这是"活绑定"与"物化快照"的分水岭。
//
// 注意与既有 TestExportLocalLiveBinding 的区别: 后者靠"改值前后各 import 一次
// 命名空间"绕过了快照, 这里从头到尾只用**一个** ns 对象。
func TestNamespaceLiveBindingReflectsLaterUpdate(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export let a = 1;
export function bump() { a = 2; }`,
		"entry.js": `import * as ns from "./m.js";
import { bump } from "./m.js";
var first = ns.a;
bump();
first + "/" + ns.a`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("命名空间活绑定: %v", err)
	}
	assertString(t, v, "1/2")
}

// TestNamespaceLiveBindingDynamicImport 动态 import 拿到的是同一个命名空间对象
// (test262 gtbndng-indirect-update 的核心形态): 模块求值完之后从外部改绑定,
// 命名空间必须跟着变。
func TestNamespaceLiveBindingDynamicImport(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export var x = 1;
export function bump() { x = 2; }`,
		"entry.js": `import * as ns from "./m.js";
import { bump } from "./m.js";
var first = ns.x;
bump();
first + "/" + ns.x`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("动态 import 命名空间活绑定: %v", err)
	}
	assertString(t, v, "1/2")
}

// TestNamespaceDefaultLiveBinding 具名默认导出是**活绑定**
// (规范 16.2.3.7: LocalExportEntry {ExportName:"default", LocalName:"fn"}):
// `export default function fn(){ fn = 2; }` 里模块内把 fn 改掉之后, 导入方读
// ns.default 必须是新值 (不是原来的函数)。
func TestNamespaceDefaultLiveBinding(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export default function fn() { fn = 2; return 1; }`,
		"entry.js": `import * as ns from "./m.js";
var called = ns.default();
called + "/" + ns.default`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("default 活绑定: %v", err)
	}
	assertString(t, v, "1/2")
}

// TestNamespaceDefaultClassLiveBinding 具名默认**类**同样是活绑定。
func TestNamespaceDefaultClassLiveBinding(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export default class C { }
C = "rebound";`,
		"entry.js": `import * as ns from "./m.js";
typeof ns.default + "/" + ns.default`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("具名默认类活绑定: %v", err)
	}
	assertString(t, v, "string/rebound")
}

// TestNamespaceAnonymousDefaultStaysDataProperty 匿名默认导出没有可供引用的
// 局部名 (规范的 localName 是不可书写的 "*default*"), 只能取值导出 —— 描述符
// 上仍是**数据属性** (有 value、没有 get), 不能被误装成读取器。
func TestNamespaceAnonymousDefaultStaysDataProperty(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export default function () { return "anon"; }`,
		"entry.js": `import * as ns from "./m.js";
var d = Object.getOwnPropertyDescriptor(ns, "default");
typeof d.value + "/" + typeof d.get + "/" + ns.default()`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("匿名 default 描述符: %v", err)
	}
	assertString(t, v, "function/undefined/anon")
}

// TestNamespaceLiveExportDescriptorHasGetter 对照上一条: 活导出的描述符带
// getter (读取器形态), 且不可配置 (规范命名空间属性 configurable:false)。
func TestNamespaceLiveExportDescriptorHasGetter(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export let a = 1;`,
		"entry.js": `import * as ns from "./m.js";
var d = Object.getOwnPropertyDescriptor(ns, "a");
typeof d.get + "/" + d.configurable + "/" + d.enumerable`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("活导出描述符: %v", err)
	}
	assertString(t, v, "function/false/true")
}

// TestNamespaceLiveBindingThroughReexport 再导出链上的活绑定: 源模块改了值,
// 中间模块的命名空间对象 (同一个) 也要读到新值。
func TestNamespaceLiveBindingThroughReexport(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"src.js": `export let a = 1;
export function bump() { a = 2; }`,
		"mid.js": `export { a } from "./src.js";`,
		"entry.js": `import * as ns from "./mid.js";
import { bump } from "./src.js";
var first = ns.a;
bump();
first + "/" + ns.a`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("再导出链活绑定: %v", err)
	}
	assertString(t, v, "1/2")
}

// TestNamespaceLiveBindingThroughStar 星号再导出的活绑定同上。
func TestNamespaceLiveBindingThroughStar(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"src.js": `export let a = 1;
export function bump() { a = 2; }`,
		"mid.js": `export * from "./src.js";`,
		"entry.js": `import * as ns from "./mid.js";
import { bump } from "./src.js";
var first = ns.a;
bump();
first + "/" + ns.a`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("星号再导出活绑定: %v", err)
	}
	assertString(t, v, "1/2")
}

// TestNamespaceLiveExportStillEnumerable 读取器化不能把枚举/展开带偏:
// Object.keys / for-in / 对象展开在活命名空间上照旧工作。
func TestNamespaceLiveExportStillEnumerable(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export let a = 1;
export const b = "B";`,
		"entry.js": `import * as ns from "./m.js";
var keys = Object.keys(ns).join(",");
var spread = Object.keys({...ns}).join(",");
var forin = []; for (var k in ns) forin.push(k);
keys + "|" + spread + "|" + forin.join(",")`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("活命名空间枚举: %v", err)
	}
	assertString(t, v, "a,b|a,b|a,b")
}

// TestNamespaceReexportNamespaceStaysDataProperty `export * as ns from "m"`
// 转出的是另一个命名空间对象: 保持物化语义 (数据属性), 不能被装成读取器 ——
// 否则每次读 ns.inner 都会新建一个对象, 破坏 ns.inner === ns.inner 的同一性。
func TestNamespaceReexportNamespaceStaysDataProperty(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"src.js": `export const x = 5;`,
		"mid.js": `export * as inner from "./src.js";`,
		"entry.js": `import * as ns from "./mid.js";
var d = Object.getOwnPropertyDescriptor(ns, "inner");
(typeof d.get) + "/" + ns.inner.x + "/" + (ns.inner === ns.inner)`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("命名空间再导出描述符: %v", err)
	}
	assertString(t, v, "undefined/5/true")
}

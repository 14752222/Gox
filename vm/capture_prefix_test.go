package vm

// 捕获前缀必须覆盖**嵌套闭包**引用的外层槽 (rtWB7N)。
//
// 症状: "工厂返回 + 解构 + JSX, 且闭包引用外层绑定" 的组合抛
// `ReferenceError: Cannot access lexical declaration before initialization`
// —— 一个本该是 7 的值读成了 TDZ 的 nil 槽。
//
// 真因不在解构、不在 signal、也不在跨模块 import: 三者单独出现都好。
// 是中间层函数的 CapturePrefixLen 只算了**它自己**引用的外层槽, 没算它
// 体内定义的嵌套闭包要用的那些。嵌套闭包是在中间层的帧里创建的, 中间层
// 没捕获到的槽位它就取不到, 只能退到读中间层帧自己的 Locals —— 那里对应
// 的是中间层的局部 (尚未初始化), 于是读到 nil ⇒ TDZ 误报。
//
// 修复在 compiler.computeCapturePrefixLen: 并入本函数体内定义的嵌套函数的
// 前缀 (clamp 到本函数 baseSlot)。

import (
	"path/filepath"
	"testing"
)

// TestNestedClosureCapturesFactoryLocal 最简形态: 工厂里的 const 被两层之内
// 的回调闭包引用, 中间层自己不引用它。
//
//	make()            ← n 在 slot 3
//	  W()             ← 自己只引用 slot 0, 不引用 n
//	    (ctx) => n()  ← 要 slot 3
//
// 中间层 W 的前缀若不并入最内层的需求, onDraw 就读到 W 帧的 nil 槽 ⇒ TDZ。
func TestNestedClosureCapturesFactoryLocal(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"mod.js": `
function callWith(fn) { return fn(1); }
export function make() {
  const n = () => 7;
  const W = () => callWith((ctx) => n());
  return { W };
}`,
		"entry.js": `
import { make } from "./mod.js";
make().W()`,
	})
	v, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("不该抛 TDZ: %v", err)
	}
	assertNumber(t, v.LastPopped(), 7)
}

// TestNestedClosureCapturesFactoryLocalDeeper 三层嵌套: 捕获需求要一路冒泡到
// 最外层 (每层都只引用自己那一层的东西)。
func TestNestedClosureCapturesFactoryLocalDeeper(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"mod.js": `
function callWith(fn) { return fn(1); }
export function make() {
  const n = () => 7;
  const mid = () => () => callWith((ctx) => n());
  return { mid };
}`,
		"entry.js": `
import { make } from "./mod.js";
make().mid()()`,
	})
	v, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("不该抛 TDZ: %v", err)
	}
	assertNumber(t, v.LastPopped(), 7)
}

// TestNestedClosureSeesLaterWrite 捕获的是 binding cell 而不是快照: 工厂返回
// 之后外层再改这个绑定, 嵌套闭包必须看到新值 (这是"修 TDZ 时顺手把它改成
// 读快照"最容易踩的反向风险)。
func TestNestedClosureSeesLaterWrite(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"mod.js": `
function callWith(fn) { return fn(1); }
export function make() {
  let n = 1;
  const bump = () => { n = n + 1; };
  const read = () => callWith((ctx) => n);
  return { bump, read };
}`,
		"entry.js": `
import { make } from "./mod.js";
var m = make();
var before = m.read();
m.bump();
var after = m.read();
before * 10 + after`,
	})
	v, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("不该抛 TDZ: %v", err)
	}
	// before=1, after=2 ⇒ 12
	assertNumber(t, v.LastPopped(), 12)
}

// TestNestedClosureSiblingSharesCell 兄弟闭包共享同一颗 cell: 一个写、另一个
// 读, 中间层同样不引用这个绑定。
func TestNestedClosureSiblingSharesCell(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"mod.js": `
function callWith(fn) { return fn(1); }
export function make() {
  let n = 0;
  const inc = () => { n = n + 5; };
  const get = () => callWith((ctx) => n);
  return { inc, get };
}`,
		"entry.js": `
import { make } from "./mod.js";
var m = make();
m.inc();
m.inc();
m.get()`,
	})
	v, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("不该抛 TDZ: %v", err)
	}
	assertNumber(t, v.LastPopped(), 10)
}

// TestNestedClosureCapturesDestructuredLocal 解构声明出来的绑定同样要能被
// 嵌套闭包捕获 (原始 bug 报告里的形态: 工厂 + 解构 + 回调)。
func TestNestedClosureCapturesDestructuredLocal(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"mod.js": `
function callWith(fn) { return fn(1); }
export function make() {
  const [a, b] = [3, 4];
  const W = () => callWith((ctx) => a + b);
  return { W };
}`,
		"entry.js": `
import { make } from "./mod.js";
make().W()`,
	})
	v, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("不该抛 TDZ: %v", err)
	}
	assertNumber(t, v.LastPopped(), 7)
}

// TestNestedClosureCapturesImportedBinding 闭包引用的是跨模块 import 绑定
// (原始 bug 报告里"闭包引用跨模块 import 绑定"那一半)。
func TestNestedClosureCapturesImportedBinding(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"dep.js": `export function bump() { return 7; }`,
		"mod.js": `
import { bump } from "./dep.js";
function callWith(fn) { return fn(1); }
export function make() {
  const W = () => callWith((ctx) => bump());
  return { W };
}`,
		"entry.js": `
import { make } from "./mod.js";
make().W()`,
	})
	v, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("不该抛 TDZ: %v", err)
	}
	assertNumber(t, v.LastPopped(), 7)
}

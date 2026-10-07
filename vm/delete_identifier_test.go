package vm

// 本文件钉住 sloppy 下 `delete 标识符` 的规范语义 (bench: 远端 main 已修
// delete this.x / 全局访问器读写 / ToPropertyKey 时机三处, 但 `delete 标识符`
// 仍编译成「求值标识符 + POP + TRUE」—— 既不删隐式全局属性, 未定义名还会
// 先抛一次 ReferenceError)。
//
// 规范 (UnaryExpression: delete + 环境记录 DeleteBinding) 按引用基分三类:
//   ① 顶层 var / 函数声明 / 隐式赋值全局 / 未绑定名 —— 基是全局对象,
//      走 globalThis 自有属性删除 (var/函数声明不可配置 → false;
//      隐式/未绑定 → true);
//   ② 顶层 let/const/class 与模块顶层绑定 —— 基是词法环境, 不可删 → false;
//   ③ 内层局部绑定 (局部变量/形参) —— 同为词法环境 → false。
//
// 编译器发射 OP_DELETE_GLOBAL (名字操作数) 落 ①, ②③ 直接压 false。

import "testing"

// TestDeleteIdentifierGlobalVarReturnsFalse 顶层 var 是 globalThis 的
// 不可配置自有属性: delete 返回 false 且绑定保留。
func TestDeleteIdentifierGlobalVarReturnsFalse(t *testing.T) {
	got := evalWithStdlib(t, `
var gv = 1;
var d = delete gv;
d + "," + ("gv" in this) + "," + gv
`)
	assertString(t, got, "false,true,1")
}

// TestDeleteIdentifierImplicitGlobalDeleted 隐式赋值建的全局是可配置
// 自有属性: delete 返回 true 且绑定真的消失。
func TestDeleteIdentifierImplicitGlobalDeleted(t *testing.T) {
	got := evalWithStdlib(t, `
imp = 5;
var d = delete imp;
d + "," + (typeof imp)
`)
	assertString(t, got, "true,undefined")
}

// TestDeleteIdentifierLexicalAndLocalReturnFalse 词法绑定不可删:
// 顶层 let、顶层函数声明、内层局部变量一律 false 且绑定保留。
func TestDeleteIdentifierLexicalAndLocalReturnFalse(t *testing.T) {
	got := evalWithStdlib(t, `
var out = [];
let lex = 1;
out.push(delete lex);
function fnD() { return 1; }
out.push(delete fnD);
out.push(delete console);
(function () {
  var localV = 1;
  out.push(delete localV);
})();
out.join(",") + "|" + lex + "|" + fnD + "|" + (typeof localV)
`)
	assertString(t, got, "false,false,true,false|1|[Function: fnD]|undefined")
}

// TestDeleteGlobalVarFromInnerFunction 内层函数里引用顶层 var: 引用基
// 仍是全局环境 → 与顶层 delete var 同解 (false)。
func TestDeleteGlobalVarFromInnerFunction(t *testing.T) {
	got := evalWithStdlib(t, `
var gv = 1;
var fromInner = (function () { return delete gv; })();
fromInner + "," + ("gv" in this) + "," + gv
`)
	assertString(t, got, "false,true,1")
}

// TestDeleteUnboundIdentifierReturnsTrue 未绑定名: sloppy 下直接 true,
// 且不求值该名 (不抛 ReferenceError)。
func TestDeleteUnboundIdentifierReturnsTrue(t *testing.T) {
	got := evalWithStdlib(t, `
var out = [];
out.push(delete neverDeclaredName);
out.push(typeof neverDeclaredName);
out.join(",")
`)
	assertString(t, got, "true,undefined")
}

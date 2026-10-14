package vm

import (
	"testing"
)

// ===== for...in 的解构头部与无声明赋值形态 (本轮新增) =====
//
// 改造前 parser 显式拒绝 `for (const [k, v] in obj)`
// ("destructuring binding in for...in is not supported"), 连**无声明**的
// `for (x in obj)` / `for ([a, b] in o)` 也不支持 —— for-in 头部只认单个标识符。
//
// 三条语义要点 (均已用 node v22 逐字节对照):
//  1. for...in 每轮产出的是**键字符串**, 解构/绑定的就是这个字符串本身
//     (不是 obj[k]) —— 所以 `for (const [a] in {aa:1})` 的 a 是 "a"。
//  2. 声明形态的 let/const 是每轮新建绑定 (闭包各捕获自己的);
//     var 与无声明赋值形态共享同一绑定 (闭包都读到最后一次写入的值)。
//  3. var 解构名要提升到函数作用域层 (循环外可见、声明前读取得 undefined)。

// TestForInDestructure_KeysAreStrings 钉住"解构的是键字符串本身"这条最易错的语义。
//
// 若误把 obj[k] 当作解构目标, 第 3 例的首字符会是 "9" 而不是 "a"。
func TestForInDestructure_KeysAreStrings(t *testing.T) {
	assertString(t, evalJS(t, `
		var out = [];
		for (const [a, b] in { xy: 1, zw: 2 }) out.push(a + "-" + b);
		out.join(",");
	`), "x-y,z-w")

	// 拿到的是**键**串的长度: "aa" → 2, "bbb" → 3, 不是 obj.length (undefined)。
	assertString(t, evalJS(t, `
		var out = [];
		for (const { length } in { aa: 1, bbb: 2 }) out.push(length);
		out.join(",");
	`), "2,3")

	assertString(t, evalJS(t, `
		var out = [];
		for (const [c] in { abc: 999 }) out.push(c);
		out.join("");
	`), "a")
}

// TestForInDestructure_LetConstPerIteration 声明形态的 let/const 每轮新建绑定。
func TestForInDestructure_LetConstPerIteration(t *testing.T) {
	assertString(t, evalJS(t, `
		var fns = [];
		for (let [z] in { x1: 1, y2: 2 }) fns.push(function () { return z; });
		fns[0]() + "," + fns[1]();
	`), "x,y")

	assertString(t, evalJS(t, `
		var fns = [];
		for (const [z] in { x1: 1, y2: 2 }) fns.push(function () { return z; });
		fns[0]() + "," + fns[1]();
	`), "x,y")

	// 简单绑定的同组对照: let k 同样是每轮新建。
	assertString(t, evalJS(t, `
		var fns = [];
		for (let k in { x1: 1, y2: 2 }) fns.push(function () { return k; });
		fns[0]() + "," + fns[1]();
	`), "x1,y2")
}

// TestForInDestructure_VarSharesSingleBinding var 形态跨轮共享同一绑定
// (与上面的 let/const 形成对照 —— 这正是 OP_ITER_BOUNDARY 只克隆块内新槽位
// 的语义边界)。
func TestForInDestructure_VarSharesSingleBinding(t *testing.T) {
	assertString(t, evalJS(t, `
		var fns = [];
		for (var [z] in { x1: 1, y2: 2 }) fns.push(function () { return z; });
		fns[0]() + "," + fns[1]();
	`), "y,y")
}

// TestForInDestructure_VarHoisted var 解构名提升到函数作用域层。
func TestForInDestructure_VarHoisted(t *testing.T) {
	// 循环外可见。
	assertString(t, evalJS(t, `
		for (var [p] in { aa: 1, bb: 2 }) {}
		p;
	`), "b")

	// 声明前读取得 undefined (提升), 而不是 ReferenceError。
	assertString(t, evalJS(t, `
		var before = q;
		for (var [q] in { aa: 1 }) {}
		String(before);
	`), "undefined")

	// 函数体作用域内同样提升。
	assertString(t, evalJS(t, `
		function f() {
		  var before = r;
		  for (var [r] in { zz: 1 }) {}
		  return String(before) + "/" + r;
		}
		f();
	`), "undefined/z")
}

// TestForInAssignmentTarget 无声明赋值形态: 写外部已有绑定, 不声明新名。
func TestForInAssignmentTarget(t *testing.T) {
	assertString(t, evalJS(t, `
		var x;
		for (x in { a: 1, b: 2 }) {}
		x;
	`), "b")

	assertString(t, evalJS(t, `
		var box = {};
		for (box.k in { m: 1, n: 2 }) {}
		box.k;
	`), "n")

	// 数组解构赋值: 每轮是赋值, 写外部已有绑定。
	assertString(t, evalJS(t, `
		var p, q;
		for ([p, q] in { xy: 1, zw: 2 }) {}
		p + "," + q;
	`), "z,w")

	// 对象解构赋值 (带重命名属性): 键 "bbb" 的长度是 3。
	assertNumber(t, evalJS(t, `
		var oa;
		for ({ length: oa } in { aa: 1, bbb: 2 }) {}
		oa;
	`), 3)

	// 解构赋值跨轮共享外部绑定 (闭包都读到最后一次写入的值)。
	assertString(t, evalJS(t, `
		var fns = [];
		var gg;
		for ([gg] in { x1: 1, y2: 2 }) fns.push(function () { return gg; });
		fns[0]() + "," + fns[1]();
	`), "y,y")
}

// TestForInSimpleBinding_StillWorks 简单绑定形态的回归护栏
// (本轮重构不得破坏既有行为)。
func TestForInSimpleBinding_StillWorks(t *testing.T) {
	assertString(t, evalJS(t, `
		var out = [];
		for (const k in { a: 1, b: 2 }) out.push(k);
		out.join(",");
	`), "a,b")

	assertString(t, evalJS(t, `
		for (var k in { a: 1, b: 2 }) {}
		k;
	`), "b")

	assertNumber(t, evalJS(t, `
		var n = 0;
		for (const k in {}) n++;
		n;
	`), 0)
}

// TestForInDestructure_BreakContinue break / continue 在解构头部下仍正确。
func TestForInDestructure_BreakContinue(t *testing.T) {
	assertString(t, evalJS(t, `
		var out = [];
		for (const [a] in { x1: 1, y2: 2, z3: 3 }) {
		  if (a === "y") continue;
		  out.push(a);
		}
		out.join(",");
	`), "x,z")

	assertString(t, evalJS(t, `
		var out = [];
		for (const [a] in { x1: 1, y2: 2, z3: 3 }) {
		  if (a === "y") break;
		  out.push(a);
		}
		out.join(",");
	`), "x")
}

// TestForInDestructure_NestedPattern 嵌套模式与默认值。
func TestForInDestructure_NestedPattern(t *testing.T) {
	// 键 "ab" 解构: a="a", b="b"。
	assertString(t, evalJS(t, `
		var out = [];
		for (const [a, [b]] in { ab: 1 }) out.push(a + b);
		out.join(",");
	`), "ab")

	// 数组模式对超出长度的位置取到 undefined; 默认值在 undefined 时生效。
	assertString(t, evalJS(t, `
		var out = [];
		for (const [a, b = "Z"] in { x: 1 }) out.push(a + b);
		out.join(",");
	`), "xZ")
}

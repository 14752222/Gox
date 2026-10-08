package vm

import (
	"strings"
	"testing"
)

// ===== var 解构的绑定落点必须在**函数作用域层** (r4McL4) =====
//
// 与 let/const 解构 (绑定落当前块, 每轮迭代新建) 的根本分野: var 解构的绑定
// 落函数作用域层 (FuncLayer), 于是有三条语义:
//   - 块/循环里声明的 var 在块外可见;
//   - 各轮迭代共享**同一个**绑定 (闭包都看到最后一次写入的值);
//   - 提升 (声明前读取得 undefined, 不是 ReferenceError)。
//
// 修复前: 绑定被按 let 口径登记进块作用域, 块退出即消失 —— 块外读该名字
// 退化成 OP_LOAD_GLOBAL, 运行期抛 ReferenceError (node v22 全程有值)。

// TestVarDestructureScope_BlockVisible: 块/循环体里声明的 var 解构在块外可读。
func TestVarDestructureScope_BlockVisible(t *testing.T) {
	assertNumber(t, evalJS(t, `{ var [r] = [9]; } r;`), 9)

	assertNumber(t, evalJS(t, `
		function f(){ { var [q] = [7]; } return q; }
		f();
	`), 7)

	// 经典 for 的 init 位置声明解构: 循环体不执行也不影响绑定可见性。
	assertNumber(t, evalJS(t, `
		function f(){ for (var [t] = [5]; false; ) {} return t; }
		f();
	`), 5)

	// 循环体内再声明一个 var 解构, 循环外同样可读。
	assertNumber(t, evalJS(t, `
		function f(){ for (var [c] of [[3]]) { var [d] = [4]; } return c * 10 + d; }
		f();
	`), 34)
}

// TestVarDestructureScope_ForOfVisibleAfterLoop: for-of 的 var 解构绑定
// 在循环外可读; 嵌套模式同样。
func TestVarDestructureScope_ForOfVisibleAfterLoop(t *testing.T) {
	assertNumber(t, evalJS(t, `
		function f(){ for (var [p] of [[1],[2],[3]]) {} return p; }
		f();
	`), 3)

	assertNumber(t, evalJS(t, `
		function f(){ for (var {q} of [{q:7}]) {} return q; }
		f();
	`), 7)

	assertNumber(t, evalJS(t, `
		function f(){ for (var [a,[b]] of [[1,[2]]]) {} return a * 10 + b; }
		f();
	`), 12)
}

// TestVarDestructureScope_Hoisted: 声明前读取得 undefined (提升语义),
// 而不是 ReferenceError。
func TestVarDestructureScope_Hoisted(t *testing.T) {
	assertNumber(t, evalJS(t, `
		function f(){ var before = typeof p; var [p] = [1]; return before === "undefined" ? 1 : 0; }
		f();
	`), 1)
}

// TestVarDestructureScope_SharedSlotAcrossIterations: 各轮迭代共享同一绑定
// —— 闭包捕获到的是同一个槽位, 最后都读到最后一次写入的值。
// (对照: let [i] of … 每轮新绑定, 见 TestLetDestructureScope_PerIteration。)
func TestVarDestructureScope_SharedSlotAcrossIterations(t *testing.T) {
	assertNumber(t, evalJS(t, `
		function f(){
			var fns = [];
			for (var [i] of [[1],[2],[3]]) { fns.push(function(){ return i; }); }
			return fns[0]() * 100 + fns[1]() * 10 + fns[2]();
		}
		f();
	`), 333)
}

// TestVarDestructureScope_RestAndDefault: rest 名与默认值同样按 var 登记。
func TestVarDestructureScope_RestAndDefault(t *testing.T) {
	assertNumber(t, evalJS(t, `
		function f(){ var {x, ...r} = {x:1,y:2,z:3}; return x + r.y + r.z; }
		f();
	`), 6)

	assertNumber(t, evalJS(t, `
		function f(){ var [d = 5] = []; return d; }
		f();
	`), 5)
}

// TestVarDestructureScope_RedenclareAllowed: var/var 同名 (含同名解构)
// 复用同一绑定, 不报错。
func TestVarDestructureScope_RedenclareAllowed(t *testing.T) {
	assertNumber(t, evalJS(t, `
		function f(){ var [a] = [1]; var a = 2; return a; }
		f();
	`), 2)

	assertNumber(t, evalJS(t, `
		function f(){ var [b] = [1]; var [b] = [2]; return b; }
		f();
	`), 2)

	// 简单参数与 var 解构同名 → 复用参数绑定
	assertNumber(t, evalJS(t, `
		function f(x){ var [x] = [3]; return x; }
		f(9);
	`), 3)
}

// TestVarDestructureScope_LexicalConflict: var 解构与 let/const 同层同名是
// 编译期 SyntaxError (两个方向都要拦)。
func TestVarDestructureScope_LexicalConflict(t *testing.T) {
	for _, src := range []string{
		`function f(){ let q = 1; var [q] = [2]; return q; } f();`,
		`function f(){ var [a] = [1]; let a; return a; } f();`,
	} {
		if _, err := EvalVM(src); err == nil {
			t.Fatalf("应报 SyntaxError: %s", src)
		} else if !strings.Contains(err.Error(), "already been declared") {
			t.Errorf("错误文案应指明重复声明, got: %v", err)
		}
	}
}

// TestLetDestructureScope_PerIteration: 对照组 —— let/const 解构仍必须落
// **块作用域**, 每轮迭代是新的绑定 (闭包各看各的)。
func TestLetDestructureScope_PerIteration(t *testing.T) {
	assertNumber(t, evalJS(t, `
		function f(){
			var fns = [];
			for (let [i] of [[1],[2],[3]]) { fns.push(function(){ return i; }); }
			return fns[0]() * 100 + fns[1]() * 10 + fns[2]();
		}
		f();
	`), 123)

	assertNumber(t, evalJS(t, `
		function f(){ { let [z] = [9]; } return typeof z; }
		f() === "undefined" ? 1 : 0;
	`), 1)
}

// TestForOfPatternAssignTargetStillAssigns: 回归护栏 —— `for ([a, b] of xs)`
// 是**赋值**形态 (VarDecl 为 nil), 每轮写入外部已有绑定。修复 var 分流时
// 若把 nil 误判成声明口径, 这里会退回 a=1/b=2 (得 102 而非 908)。
func TestForOfPatternAssignTargetStillAssigns(t *testing.T) {
	assertNumber(t, evalJS(t, `
		let a = 1, b = 2;
		for ([a, b] of [[9, 8]]) {}
		a * 100 + b;
	`), 908)
}

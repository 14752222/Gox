package vm

import (
	"strings"
	"testing"
)

// ===== 无声明关键字的 for-of / for-await 赋值目标形态 (rODXmB 增量) =====
//
// for (x of xs) / for (obj.k of xs) / for ([a, b] of xs) / for ({a} of xs)
// 与 for await 的对应形态。每轮迭代是**赋值**而非声明: 写外部已有绑定
// (node 22 实测口径), 解构目标走 compilePatternBind(isDecl=false)。

// TestForOfLHSAssignment: 同步 for-of 赋值目标的求值结果。
func TestForOfLHSAssignment(t *testing.T) {
	// 标识符目标: 累加外部变量
	assertNumber(t, testEval(t, `
		let s = 0;
		for (x of [1, 2, 3]) { s = s + x; }
		s;
	`), 6)

	// 未声明的标识符目标: 隐式全局 (与 x = v 的 sloppy 口径一致)
	assertNumber(t, testEval(t, `
		var acc = 0;
		for (imp of [4, 5]) { acc = acc + imp; }
		acc + imp;
	`), 14)

	// 成员表达式目标: 每轮写入对象属性
	assertNumber(t, testEval(t, `
		let o = { k: 0 };
		for (o.k of [10, 20]) { }
		o.k;
	`), 20)

	// 索引表达式目标
	assertNumber(t, testEval(t, `
		let arr = [0, 0];
		let i = 0;
		for (arr[i] of [7]) {}
		i = i + 1;
		for (arr[i] of [8]) {}
		arr[0] + arr[1];
	`), 15)

	// 数组解构目标: 每轮写入外部已声明的 a/b (node: 9 8 那条口径)
	assertNumber(t, testEval(t, `
		let a = 1, b = 2;
		for ([a, b] of [[9, 8]]) {}
		a * 100 + b;
	`), 908)

	// 对象解构目标
	assertNumber(t, testEval(t, `
		let p = 0;
		for ({p} of [{p: 3}, {p: 4}]) {}
		p;
	`), 4)
}

func TestForOfLHSWithContinueBreak(t *testing.T) {
	assertNumber(t, testEval(t, `
		let s = 0;
		for (x of [1, 2, 3, 4, 5]) {
			if (x > 3) { break; }
			if (x === 2) { continue; }
			s = s + x;
		}
		s;
	`), 4)
}

// TestForAwaitOfLHSForms: for await 的赋值目标形态在异步迭代下求值正确。
func TestForAwaitOfLHSForms(t *testing.T) {
	got := evalForAwait(t, `
		async function main() {
			let sum = 0;
			for await (x of [1, 2, 3]) { sum = sum + x; }
			__out.push("sum:" + sum);

			let o = { k: 0 };
			for await (o.k of [40]) {}
			__out.push("k:" + o.k);

			let a = 1, b = 2;
			for await ([a, b] of [[7, 8]]) {}
			__out.push("ab:" + a + b);
		}
		main();
	`)
	want := "sum:6\nk:40\nab:78\n"
	if got != want {
		t.Errorf("for await LHS 形态\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestForOfLHSClosurePerIterationIsAssignment: 赋值目标形态下所有轮次写
// **同一个**外部绑定 (与 for (let …) 的每轮新绑定相反) —— 闭包最后看到的
// 都是最后一次写入的值 3。
func TestForOfLHSClosurePerIterationIsAssignment(t *testing.T) {
	assertNumber(t, testEval(t, `
		let fns = [];
		let v = 0;
		for (v of [1, 2, 3]) {
			fns.push(() => v);
		}
		fns.length + fns[0]() * 10 + fns[2]() * 100;
	`), 333)
}

// TestForOfLHSErrors: 编译期拒绝不可赋值目标 / 非 async 上下文的 for await。
func TestForOfLHSErrors(t *testing.T) {
	if _, err := EvalVM(`for (1 of [1]) {}`); err == nil {
		t.Fatal("for (1 of …) 应报编译错")
	} else if !strings.Contains(err.Error(), "invalid left-hand side") {
		t.Errorf("错误文案应指明赋值目标非法, got: %v", err)
	}
	if _, err := EvalVM(`for (f() of [1]) {}`); err == nil {
		t.Fatal("for (f() of …) 应报编译错")
	}
	if _, err := EvalVM(`for await (x of [1]) {}`); err == nil {
		t.Fatal("顶层 for await 应报编译错")
	}
}

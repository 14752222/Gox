package vm

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== 定时器 + Promise 集成回归测试 =====
// 修复前: currentVM 只在主脚本执行期间有效，RunTimers 期间为 nil，
// 定时器回调中 resolve 的 Promise 的 .then 回调经 object.CallFunction
// 桥调用时被静默丢弃 (返回 undefined)。
// 修复: RunTimersUntil 执行期间注册 currentVM。

// TestTimerPromiseThen: setTimeout 回调中 resolve → .then 回调应执行
func TestTimerPromiseThen(t *testing.T) {
	resetTimers(t)
	vm, _ := runEvalVM(t, `
		let got = "";
		let p = new Promise(function(resolve) {
			setTimeout(function() { resolve("done"); }, 5);
		});
		p.then(function(v) { got = v; });
		got;
	`)
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers: %v", err)
	}
	val, ok := vm.Globals().Get("got")
	if !ok {
		t.Fatalf("global %q not found", "got")
	}
	s, ok := val.(*object.String)
	if !ok {
		t.Fatalf("expected String, got %T", val)
	}
	if s.Value != "done" {
		t.Fatalf("expected then callback to set got=\"done\", got %q", s.Value)
	}
}

// TestAsyncAwaitWithTimer: async/await 等待定时器驱动的 Promise 应完成
func TestAsyncAwaitWithTimer(t *testing.T) {
	resetTimers(t)
	vm, _ := runEvalVM(t, `
		let phase = "start";
		async function main() {
			await delay(5);
			phase = "awaited";
		}
		main();
		phase;
	`)
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers: %v", err)
	}
	val, _ := vm.Globals().Get("phase")
	s, ok := val.(*object.String)
	if !ok {
		t.Fatalf("expected String, got %T", val)
	}
	if s.Value != "awaited" {
		t.Fatalf("expected phase=\"awaited\", got %q", s.Value)
	}
}

// TestPromiseChainAcrossTimers: 跨定时器的 Promise 链应完整执行
func TestPromiseChainAcrossTimers(t *testing.T) {
	resetTimers(t)
	vm, _ := runEvalVM(t, `
		let result = 0;
		delay(5, 1)
			.then(function(v) { result = v + 1; return result; })
			.then(function(v) { result = v * 10; });
		result;
	`)
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers: %v", err)
	}
	num := getGlobalNumber(t, vm, "result")
	if num != 20 {
		t.Fatalf("expected chained result 20, got %v", num)
	}
}

// TestAsyncArrowFunction 验证 async 箭头函数与 async function **完全同路**。
//
// 编译器把两者都送进 compileAsyncFunctionSelf 那条 "wrapper + 内层 generator"
// 路径 (await 编译成 yield), 唯一的区别是 FunctionMetadata.IsArrow —— 它决定
// 调用时要不要重绑 this。所以这里既覆盖各种绑定形状, 也用 this 做一次对照。
//
// 有意避开的写法 (踩过, 与本事无关):
//   - 函数体 throw / rest 参数: async function 自己也不对
//     (`__spawn(<立刻 throw 的 generator>)` 返回 undefined 而不是 rejected
//     Promise; `async function (...xs)` 的 xs.length 恒为 1)。
//   - 「先把 await 的返回值赋给成员表达式 (out[i] = await f()), 且先后混用
//     立即完成与真正挂起的 await」: 会让 VM 在定时器回调里 panic
//     (index out of range [-1])。最小复现见提交说明, 这里是纯 async function
//     就能触发的既有问题 —— 放进来只会让这个用例在别的问题上变红。
func TestAsyncArrowFunction(t *testing.T) {
	vm, _ := runEvalVM(t, `
		let f1 = async () => 1;
		let f2 = async (a, b) => a + b;
		let f3 = async (x) => { let y = await Promise.resolve(x); return y * 2; };
		let f4 = async x => x + 1;
		let f5 = async (a, b,) => a * b;
		let f6 = async (a, b = 5) => a + b;
		let f7 = async ([a, b]) => a + b;
		let f8 = async ({ n }) => n;
		let f9 = async () => { let inner = async (n) => n * 3; return await inner(4); };
		let obj = {
			x: 42,
			plain: function () { let g = () => this.x; return g(); },
			asyn: function () { let g = async () => this.x; return g(); },
		};
		let result = "(未完成)";
		async function main() {
			// 结果一律先落到局部变量: 一条一条对成员表达式赋值是另一个坑 (见上)
			let a = await f1();
			let b = await f2(1, 2);
			let c = await f3(3);
			let d = await f4(1);
			let e = await f5(2, 3);
			let g = await f6(1);
			let h = await f7([1, 2]);
			let i = await f8({ n: 7 });
			let j = await f9();
			let k = typeof f1().then;
			let l = obj.plain();
			let m = await obj.asyn();
			result = [a, b, c, d, e, g, h, i, j, k, l, m].join(",");
		}
		main();
	`)
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers: %v", err)
	}
	assertGlobalString(t, vm, "result",
		// f1 | f2 | f3 | f4 | f5 | f6 | f7 | f8 | f9 | 返回 Promise | this 词法(普通/async)
		"1,3,6,2,6,6,3,7,12,function,42,42")
}

// TestAsyncArrowFunctionSuspends 让 async 箭头真正挂起一次 (定时器驱动的
// Promise), 确认它走的是与 async function 同一条 "yield → 等 promise → 续跑"
// 的路径, 而不只是快速路径。
//
// 形状刻意保持最小且单一: 两条 await 都是"会挂起"的那种, 结果落局部变量 ——
// 一旦掺入立即完成的 await, 就会撞上上面记的那个既有 panic。
func TestAsyncArrowFunctionSuspends(t *testing.T) {
	resetTimers(t)
	vm, _ := runEvalVM(t, `
		let f = async (x) => { let y = await delay(1, x); return y * 2; };
		let result = "(未完成)";
		async function main() {
			let a = await f(3);
			let b = await f(4);
			result = a + "," + b;
		}
		main();
	`)
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers: %v", err)
	}
	assertGlobalString(t, vm, "result", "6,8")
}

// assertGlobalString 读一个全局字符串并断言其值。
func assertGlobalString(t *testing.T, vm *VM, name, want string) {
	t.Helper()
	val, ok := vm.Globals().Get(name)
	if !ok {
		t.Fatalf("global %q not found", name)
	}
	s, ok := val.(*object.String)
	if !ok {
		t.Fatalf("global %q is %T, want String (值: %s)", name, val, val.Inspect())
	}
	if s.Value != want {
		t.Fatalf("global %q = %q, want %q", name, s.Value, want)
	}
}

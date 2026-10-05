package vm

import (
	"strings"
	"testing"
)

// ===== for await...of（rODXmB, 2026-10-05）=====
//
// for await (binding of iterable) 在 async 函数 (内层 generator) 里执行:
// GET_ASYNC_ITERATOR 取迭代器, 循环头 ASYNC_ITER_NEXT + OP_YIELD 等待
// Promise, resolve 值取 .done/.value。详见 compiler.compileForAwaitOfStatement。
//
// 已知既有边界 (绕开而非修复, 与 for-await 语法无关, 见 async_throw_test.go):
//   - 循环体内含 await 的 async 函数, 其 return 值会丢 (纯 for-of + await
//     也复现);
//   - 成员表达式赋值 + 「先立即后挂起」的 await 混用会 panic。
// 因此断言一律从 __out 数组收集 (EvalVM + RunTimers 驱动 Promise 链),
// 不依赖 async 函数的 return 值 / .then 链。

// evalForAwait: 跑一段含 for await 的脚本, RunTimers 驱动 Promise 后收 __out。
func evalForAwait(t *testing.T, src string) string {
	t.Helper()
	vm, err := EvalVM("let __out=[];\n" + src)
	if err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers: %v", err)
	}
	return outLines(t, vm)
}

func TestForAwaitOfAsyncIterator(t *testing.T) {
	got := evalForAwait(t, `
		async function main() {
			const ag = {
				[Symbol.asyncIterator]() {
					let i = 0;
					return {
						next() {
							i = i + 1;
							if (i > 3) return Promise.resolve({ done: true, value: undefined });
							return Promise.resolve({ done: false, value: i * 10 });
						}
					};
				}
			};
			let sum = 0;
			for await (const v of ag) { sum = sum + v; }
			__out.push("sum:" + sum);
		}
		main();
	`)
	want := "sum:60\n"
	if got != want {
		t.Errorf("异步迭代器求和\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestForAwaitOfSyncIterable(t *testing.T) {
	// 规范: for await 也消费同步可迭代 (数组走 Symbol.iterator,
	// 每步的值原样穿过 OP_YIELD —— __spawn 对非 Promise 直接回传)。
	got := evalForAwait(t, `
		async function main() {
			let log = "";
			for await (const ch of ["a", "b", "c"]) { log = log + ch; }
			__out.push("sync:" + log);
		}
		main();
	`)
	want := "sync:abc\n"
	if got != want {
		t.Errorf("同步数组消费\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestForAwaitOfBreakContinue(t *testing.T) {
	got := evalForAwait(t, `
		async function main() {
			const ag = {
				[Symbol.asyncIterator]() {
					let i = 0;
					return {
						next() {
							i = i + 1;
							if (i > 5) return Promise.resolve({ done: true, value: undefined });
							return Promise.resolve({ done: false, value: i });
						}
					};
				}
			};
			let log = "";
			for await (const v of ag) {
				if (v === 2) continue;
				if (v === 4) break;
				log = log + v;
			}
			__out.push("bc:" + log);
		}
		main();
	`)
	want := "bc:13\n" // 1, (2 continue), 3, (4 break)
	if got != want {
		t.Errorf("break/continue\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestForAwaitOfDestructuring(t *testing.T) {
	got := evalForAwait(t, `
		async function main() {
			const pairs = [[1, 2], [3, 4]];
			let log = "";
			for await (const [a, b] of pairs) { log = log + (a + b); }
			__out.push("dstr:" + log);
		}
		main();
	`)
	want := "dstr:37\n" // (1+2)(3+4)
	if got != want {
		t.Errorf("解构绑定\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestForAwaitOfRejection(t *testing.T) {
	// next() 的 Promise reject: 沿 OP_YIELD 恢复链路以 GeneratorThrow 抛回
	// for-await 所在的 try/catch。
	got := evalForAwait(t, `
		async function main() {
			const ag = {
				[Symbol.asyncIterator]() {
					let i = 0;
					return {
						next() {
							i = i + 1;
							if (i === 3) return Promise.reject(new Error("boom@" + i));
							return Promise.resolve({ done: i > 4, value: i });
						}
					};
				}
			};
			let log = "";
			try {
				for await (const v of ag) { log = log + v; }
			} catch (e) {
				log = log + "|caught:" + e.message;
			}
			__out.push("rej:" + log);
		}
		main();
	`)
	want := "rej:12|caught:boom@3\n"
	if got != want {
		t.Errorf("reject 传播\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestForAwaitOfEmptyAndNonIterable(t *testing.T) {
	got := evalForAwait(t, `
		async function main() {
			const empty = {
				[Symbol.asyncIterator]() {
					return { next() { return Promise.resolve({ done: true, value: undefined }); } };
				}
			};
			let count = 0;
			for await (const v of empty) { count = count + 1; }
			__out.push("empty:" + count);
		}
		main();
	`)
	want := "empty:0\n"
	if got != want {
		t.Errorf("空迭代器\nwant:\n%s\ngot:\n%s", want, got)
	}

	// 非可迭代对象: TypeError, async 调用的 Promise reject 由 .catch 收。
	got2 := evalForAwait(t, `
		async function main() {
			for await (const v of 42) { }
		}
		main().catch(e => __out.push("err:" + e.name + ":" + e.message));
	`)
	if !strings.Contains(got2, "TypeError") {
		t.Errorf("非可迭代对象应报 TypeError, got: %q", got2)
	}
}

func TestForAwaitOfOutsideAsyncIsError(t *testing.T) {
	// for await 只在 async 函数体内合法 (编译期 SyntaxError)。
	if _, err := EvalVM(`for await (const x of [1]) {}`); err == nil {
		t.Fatal("顶层 for await 应报编译错")
	} else if !strings.Contains(err.Error(), "only allowed inside an async function") {
		t.Errorf("错误文案应指明仅 async 内合法, got: %v", err)
	}
	if _, err := EvalVM(`function f() { for await (const x of [1]) {} }`); err == nil {
		t.Fatal("普通函数内 for await 应报编译错")
	}
	// 对照: 传统 for 的 init 里出现 await 表达式仍按普通表达式解析 (不误伤)。
	if _, err := EvalVM(`async function f(p) { for (let i = await p; i < 3; i++) {} }`); err != nil {
		t.Errorf("传统 for 头部的 await 不应报错, got: %v", err)
	}
}

// TestForAwaitOfRealAsyncGenerator: for await 的迭代源是**真正的 async generator**
// (而非手写的 [Symbol.asyncIterator] 对象), 走 OP_GET_ASYNC_ITERATOR 的
// AsyncGenerator 直通分支 + OP_ASYNC_ITER_NEXT 的 AsyncGenerator 分支。
func TestForAwaitOfRealAsyncGenerator(t *testing.T) {
	got := evalForAwait(t, `
		async function* g() {
			yield 1;
			yield 2;
			yield 3;
		}
		async function main() {
			let sum = 0;
			for await (const v of g()) { sum = sum + v; }
			__out.push("agsum:" + sum);
		}
		main();
	`)
	want := "agsum:6\n"
	if got != want {
		t.Errorf("for await 消费异步生成器\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestForAwaitOfRealAsyncGeneratorTryFinally 覆盖 async generator 体内 try/finally
// 与「挂起期操作数栈中间值」的组合: `100 + (yield 1)` 挂起时栈上留有 100
// (恢复要经 rebuildGenFrame 的 SavedStack 还原), finally 在生成器正常完成时
// 展开。这个组合正是 rebuildGenFrame 帧基修复 (帧基须取在压入 SavedStack 之前)
// 针对的形状 —— 帧基取错会让 SavedStack 落到帧基之下, try 条目绝对 stackBase
// 错位。
func TestForAwaitOfRealAsyncGeneratorTryFinally(t *testing.T) {
	got := evalForAwait(t, `
		async function* k() {
			try {
				const x = 100 + (yield 1);  // 挂起时生成器操作数栈上有 100
				yield x;
			} finally {
				__out.push("fin");
			}
		}
		async function main() {
			const it = k();
			const r1 = await it.next();               // {value:1}
			const r2 = await it.next(r1.value + 4);   // 100 + 5 = 105
			await it.next();                          // 驱动到完成 → 展开 finally
			__out.push("r2:" + r2.value);
		}
		main();
	`)
	want := "fin\nr2:105\n"
	if got != want {
		t.Errorf("带 try/finally 的异步生成器 + 挂起期中间值\nwant:\n%s\ngot:\n%s", want, got)
	}
}

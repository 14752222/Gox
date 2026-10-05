package vm

import (
	"strings"
	"testing"
)

// ===== for-await 的 AsyncIteratorClose 与中断传播 (rODXmB P3) =====
//
// break / return / body 抛异常穿出 for-await 体时调用迭代器的 return() 并
// await 其结果; 无 return 方法则跳过。优先级口径 (node 22 实测钉死):
//   - done 正常出口 / continue: 不调 return();
//   - body 抛异常: return() 被调, 但 close 序列自身的异常被吞 —— 原异常胜出;
//   - break / return 路径: close 序列的异常照常传播;
//   - next()/value 的 await 拒绝: 不触发 close。
//
// 每步的 value 也要 await (CreateAsyncFromSyncIterator 语义):
// for await (x of [p, 2]) 的 p 是 Promise 时解包。

// mkIterSrc 生成 JS 源码: 一个 3 步的 async 迭代器, return 方法记日志到
// logVar 并调用 retFnExpr (函数表达式, 自带 this/参数取舍), 传 "null" 表示
// 没有 return 方法。
func mkIterSrc(logVar, retFnExpr string) string {
	ret := ""
	if retFnExpr != "null" {
		ret = ", return() { " + logVar + ".push('ret'); return (" + retFnExpr + ")(); }"
	}
	return "(function(){ let i = 0; return { next() { i = i + 1; return Promise.resolve({ done: i > 3, value: i }); }" +
		ret + " }; })()"
}

func TestForAwaitCloseOnBreak(t *testing.T) {
	got := evalForAwait(t, `
		async function main() {
			const log = [];
			const it = `+mkIterSrc("log", "function(){ return Promise.resolve({}); }")+`;
			for await (const x of it) { break; }
			__out.push("break:" + log.length);
		}
		main();
	`)
	want := "break:1\n"
	if got != want {
		t.Errorf("break 应调 return()\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestForAwaitNoCloseOnDoneAndContinue(t *testing.T) {
	got := evalForAwait(t, `
		async function main() {
			const log = [];
			const it1 = `+mkIterSrc("log", "function(){ return Promise.resolve({}); }")+`;
			for await (const x of it1) {}
			const it2 = `+mkIterSrc("log", "function(){ return Promise.resolve({}); }")+`;
			for await (const x of it2) { continue; }
			__out.push("close:" + log.length);
		}
		main();
	`)
	want := "close:0\n"
	if got != want {
		t.Errorf("done/continue 不调 return()\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestForAwaitCloseOnThrowOriginalWins(t *testing.T) {
	// body 抛异常: return() 被调, 原异常胜出 —— close 里 return() 正常完成
	got := evalForAwait(t, `
		async function main() {
			const log = [];
			const it = `+mkIterSrc("log", "function(){ log.push('ret-ok'); return Promise.resolve({}); }")+`;
			try {
				for await (const x of it) { throw new Error("body-err"); }
			} catch (e) {
				__out.push("caught:" + e.message + " log:" + log.join(","));
			}
		}
		main();
	`)
	want := "caught:body-err log:ret,ret-ok\n"
	if got != want {
		t.Errorf("throw 路径 close + 原异常\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestForAwaitCloseErrorPriority(t *testing.T) {
	// (a) body 抛 + return() 也抛: 原异常胜出 (close 异常被吞)
	got := evalForAwait(t, `
		async function main() {
			const __x1 = [];
			const it1 = `+mkIterSrc("__x1", "function(){ throw new Error('ret-err'); }")+`;
			try {
				for await (const x of it1) { throw new Error("body-err"); }
			} catch (e) {
				__out.push("a:" + e.message);
			}
			// (b) break 路径 return() 抛: close 异常照常传播
			const __x2 = [];
			const it2 = `+mkIterSrc("__x2", "function(){ throw new Error('ret-err2'); }")+`;
			try {
				for await (const x of it2) { break; }
			} catch (e) {
				__out.push("b:" + e.message);
			}
		}
		main();
	`)
	want := "a:body-err\nb:ret-err2\n"
	if got != want {
		t.Errorf("close 异常优先级\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestForAwaitCloseAwaited(t *testing.T) {
	// return() 返回 Promise: await 它之后再继续 (用副作用顺序验证)
	got := evalForAwait(t, `
		async function main() {
			const log = [];
			const it = {
				next() { return Promise.resolve({ done: false, value: 1 }); },
				return() { return Promise.resolve({}).then(() => { log.push("returned"); return {}; }); }
			};
			for await (const x of it) { break; }
			log.push("after");
			__out.push("order:" + log.join(","));
		}
		main();
	`)
	want := "order:returned,after\n"
	if got != want {
		t.Errorf("return() 结果应被 await\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestForAwaitNextRejectNoClose(t *testing.T) {
	got := evalForAwait(t, `
		async function main() {
			const log = [];
			const it = {
				next() { return Promise.reject(new Error("next-err")); },
				return() { log.push("ret"); return Promise.resolve({}); }
			};
			try { for await (const x of it) {} } catch (e) {
				__out.push("ret?" + log.length + " err:" + e.message);
			}
		}
		main();
	`)
	want := "ret?0 err:next-err\n"
	if got != want {
		t.Errorf("next 拒绝不触发 close\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestForAwaitSyncValuesAwaited(t *testing.T) {
	// 同步可迭代的 value 是 Promise 时解包 (CreateAsyncFromSyncIterator)
	got := evalForAwait(t, `
		async function main() {
			const got = [];
			for await (const x of [Promise.resolve(1), 2, Promise.resolve(3)]) { got.push(x); }
			__out.push("vals:" + got.join(","));
		}
		main();
	`)
	want := "vals:1,2,3\n"
	if got != want {
		t.Errorf("同步 value 解包\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestForAwaitNestedLabeledClose(t *testing.T) {
	// break outer: 内外两层迭代器都 close, 由内向外
	got := evalForAwait(t, `
		async function main() {
			const log = [];
			function mk(name) {
				return { next() { return Promise.resolve({ done: false, value: name }); },
					return() { log.push("close:" + name); return Promise.resolve({}); } };
			}
			outer: for await (const o of mk("O")) {
				for await (const i of mk("I")) { break outer; }
			}
			__out.push("nested:" + log.join(","));
		}
		main();
	`)
	want := "nested:close:I,close:O\n"
	if got != want {
		t.Errorf("嵌套标签 break 由内向外 close\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestForAwaitCloseOnReturn(t *testing.T) {
	got := evalForAwait(t, `
		async function main() {
			const log = [];
			const it = `+mkIterSrc("log", "function(){ log.push('ret-ok'); return Promise.resolve({}); }")+`;
			async function f() {
				for await (const x of it) { return "R"; }
			}
			const v = await f();
			__out.push("return:" + log.join(",") + " v:" + v);
		}
		main();
	`)
	want := "return:ret,ret-ok v:R\n"
	if got != want {
		t.Errorf("return 语句触发 close\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestForAwaitNoReturnMethodBreak(t *testing.T) {
	// 迭代器没有 return 方法: break 照常结束, 不报错
	got := evalForAwait(t, `
		async function main() {
			const it = { next() { return Promise.resolve({ done: false, value: 1 }); } };
			for await (const x of it) { break; }
			__out.push("ok");
		}
		main();
	`)
	if !strings.Contains(got, "ok") {
		t.Errorf("无 return 方法时 break 应正常结束, got:\n%s", got)
	}
}

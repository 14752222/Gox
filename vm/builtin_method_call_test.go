package vm

import (
	"testing"
)

// ===== 内建方法的调用语义 (看板 rYFTlt) =====
//
// 两条互相独立的缺陷, 同一个症状 (TypeError: xxx is not a function):
//
//  1. **OP_CALL 不认 *BuiltinMethod**。内建方法一旦脱离方法调用形态
//     (`const f = obj.m; f()`) 就落到 default 分支报 "is not a function",
//     而同一个值 `typeof` 明明是 "function" —— 极难归因。
//
//  2. **计算成员调用 `obj[k]()` 不绑接收者**。编译器只对 `obj.m()` (非计算
//     成员) 发射 OP_CALL_METHOD, `obj[k]()` 一律退化成裸调用, 于是
//     `arr[Symbol.iterator]()` 拿不到 this。
//
// 修第 1 条时踩到的坑写在这里防复发: 裸调用传给内建方法的 this 必须是
// **undefined**, 不能按 sloppy 归一成 globalThis —— 内建函数是 strict 的。
// 归一成 globalThis 会让 ToObject 成功, 于是 `Object.prototype.valueOf()`
// 该抛的 TypeError 不抛 (Node 实测它抛)。

// TestComputedMemberCallBindsReceiver 计算成员调用必须绑定接收者。
//
// `obj[k]()` 与 `obj.k()` 在规范里是同形的: 被调都是 Reference, 都有 base。
// 修复前 obj[k]() 退化成裸调用, this 丢失。
func TestComputedMemberCallBindsReceiver(t *testing.T) {
	evalJS(t, `
		const obj = { v: 42, get() { return this.v; } };
		const key = "get";
		if (obj[key]() !== 42) { throw new Error("obj[key]() 的 this 不是 obj"); }

		// 字符串键与 Symbol 键两条路都要绑
		const sym = Symbol("s");
		const o2 = { v: 7, [sym]() { return this.v; } };
		if (o2[sym]() !== 7) { throw new Error("o2[sym]() 的 this 不是 o2"); }

		// 取出来单独调用 (裸调用): this 归位成 globalThis, this.v 是 undefined
		const detached = obj[key];
		if (detached() !== undefined) { throw new Error("detached() 的 this 不该是 obj"); }
		"ok";
	`)
}

// TestComputedMemberCallOnBuiltinProto 内建原型上的 Symbol 键成员也要能调。
//
// 用 %AsyncIteratorPrototype%[@@asyncIterator] 而不是 Array.prototype
// [@@iterator]: 后者依赖「读取侧补齐」(rYFTlt 阶段 1), 尚未落地;
// 本文件只钉住**调用侧**——读得到就该调得动。
func TestComputedMemberCallOnBuiltinProto(t *testing.T) {
	evalJS(t, `
		async function* gen() {}
		const AIP = Object.getPrototypeOf(Object.getPrototypeOf(gen.prototype));
		const iter = Object.create(AIP);
		// 计算成员调用: this 必须是 iter, 所以返回值 === iter
		if (iter[Symbol.asyncIterator]() !== iter) {
			throw new Error("iter[Symbol.asyncIterator]() 没有把 iter 当 this");
		}
		"ok";
	`)
}

// TestBuiltinMethodBareCallIsCallable 内建方法脱离接收者后**仍可调用**。
// 修复前这里直接 TypeError: xxx is not a function。
func TestBuiltinMethodBareCallIsCallable(t *testing.T) {
	evalJS(t, `
		const v = Object.prototype.toString;
		if (typeof v !== "function") { throw new Error("typeof 不是 function"); }
		// 裸调用: this = undefined ⇒ "[object Undefined]"
		if (v() !== "[object Undefined]") { throw new Error("Object.prototype.toString() -> " + v()); }
		"ok";
	`)
}

// TestBuiltinMethodBareCallHasUndefinedThis 内建函数的 this 是 undefined。
//
// 这是修复过程中最容易做错的一处: 若按 sloppy 把 this 归一成 globalThis,
// ToObject 就成功了, 该抛的 TypeError 全部消失。Node v22 实测口径:
//   - Object.prototype.valueOf()        → TypeError
//   - Object.prototype.toLocaleString() → TypeError
//   - Object.prototype.toString()       → "[object Undefined]" (不抛)
//   - Array.prototype.concat()          → TypeError
func TestBuiltinMethodBareCallHasUndefinedThis(t *testing.T) {
	evalJS(t, `
		function throwsTypeError(fn, label) {
			let threw = false;
			try { fn(); } catch (e) {
				if (!(e instanceof TypeError)) { throw new Error(label + " 抛的不是 TypeError: " + e); }
				threw = true;
			}
			if (!threw) { throw new Error(label + " 没有抛 TypeError"); }
		}
		const valueOf = Object.prototype.valueOf;
		const toLocaleString = Object.prototype.toLocaleString;
		const concat = Array.prototype.concat;
		throwsTypeError(valueOf, "Object.prototype.valueOf()");
		throwsTypeError(toLocaleString, "Object.prototype.toLocaleString()");
		throwsTypeError(concat, "Array.prototype.concat()");

		// 显式给 globalThis 时反而不抛 (ToObject(globalThis) 成功)
		if (typeof Object.prototype.valueOf.call(globalThis) !== "object") {
			throw new Error("valueOf.call(globalThis) 应返回 globalThis");
		}
		"ok";
	`)
}

// TestComputedMemberCallArgOrder 计算成员调用的求值序: obj → key → 实参。
//
// 旧路径 (裸调用) 是先算实参再算 obj[k]; 规范是 obj → key → 实参。
// 这是修复顺带校正的一条, 用副作用顺序把它钉住。
func TestComputedMemberCallArgOrder(t *testing.T) {
	evalJS(t, `
		const log = [];
		const obj = { m(x) { return "m" + x; } };
		function key() { log.push("key"); return "m"; }
		function arg() { log.push("arg"); return 1; }
		const base = { get self() { log.push("obj"); return obj; } };
		const r = base["self"][key()](arg());
		if (r !== "m1") { throw new Error("返回值不对: " + r); }
		if (log.join(",") !== "obj,key,arg") { throw new Error("求值序: " + log.join(",")); }
		"ok";
	`)
}

// TestDetachedBuiltinMethodStillCallable 内建方法被取出后仍可调用 (含 spread)。
//
// 这是 rYFTlt 标题里 "arr[Symbol.iterator]() 直接 TypeError" 的调用侧那一半:
// 症状是 `typeof` 明明是 "function", 一调就说 "is not a function"。
func TestDetachedBuiltinMethodStillCallable(t *testing.T) {
	v := evalJS(t, `
		const m = Array.prototype.join;
		const args = ["-"];
		// 裸调用 (spread 形态): OP_CALL_SPREAD 此前同样不认 *BuiltinMethod
		m.call(["a","b"], "-") + "|" + typeof m;
	`)
	assertString(t, v, "a-b|function")
}

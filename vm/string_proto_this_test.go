package vm

import (
	"testing"
)

// ===== String.prototype.toString/valueOf 的 this 校验 (看板 rnm4C5) =====
//
// 这是 built-ins 套件整轮跑批被崩掉的那条。症状与根因:
//
//	runtime: goroutine stack exceeds 1000000000-byte limit
//	fatal error: stack overflow
//	  object/conversion.go:50  (ToString → CallFunction)
//	  stdlib/string_methods.go:590 (旧版 String.prototype.toString)
//
// 根因是一条**跨语言边界的循环**, 因此 Go 侧的递归防御完全失效:
//
//	ToString(obj)                      // object/conversion.go:49
//	  → CallFunction(obj.toString)     // 跨进 JS 侧
//	    → String.prototype.toString()  // 旧实现: toStr(this)
//	      → ToString(obj)              // 跨回 Go 侧, depth **从 0 重新起算**
//
// object.ToString 里的 maxToStringDepth=8 拦不住它: 每绕一圈 JS 边界,
// depth 都重新从 0 数起。这是 "Go 侧 depth 防御" 这种局部手段的固有盲区 ——
// 防御跨语言循环必须在**每一侧各自的入口**都做类型校验, 而不是指望一侧的
// 计数器能穿透另一侧。
//
// 规范同样站在校验这边: ThisStringValue 要求 this 是 String 或 String 对象,
// 其余一律 TypeError —— 与 Number.prototype.toString 的写法同源。
//
// 顺带一提: 全仓库只有 toString/valueOf 这两处漏了校验 (其余 20+ 个 String
// 方法早就是 thisTypeError)。漏的原因很典型 —— 它俩"返回值本来就是字符串",
// 看起来"不需要校验类型也能出结果", 于是退化成了 toStr(this) 兜底。
// 凡是"看起来不需要校验"的地方, 往往正是校验最该在的地方。

// TestStringProtoSelfToStringMustNotRecurse **String.prototype 自己**必须能 toString。
//
// 这是整条崩溃链的入口, 也是最容易被"修复过猛"带偏的一处, 所以单独成一个用例:
//
//   - 按规范: String.prototype 是个 [[StringData]]="" 的 String 对象,
//     所以 String.prototype.toString() === "" (Node v22 实测)。
//   - 按直觉: 它长得像个普通对象 (Gox 里确实是 *object.Object), 于是"顺手"
//     给它抛个 TypeError —— 错, 且这一抛会让 built-ins 里一整族
//     this-value 用例从"崩"变成"红", 数量不减反增。
//
// 负向说明: 若有人把实现改回 toStr(this), 本用例**不会**优雅地红 —— 它是
// fatal error: stack overflow, 整个测试进程直接被打挂。这仍然算真红: 一个
// 只会优雅报红的闸门, 恰恰掩盖了它真正要防的那种崩溃形态。
func TestStringProtoSelfToStringMustNotRecurse(t *testing.T) {
	evalJS(t, `
		// 这一行就是 built-ins 整轮跑批被崩掉的那一发
		if (String.prototype.toString() !== "") {
			throw new Error("String.prototype.toString() 应为 \"\", 得到 " + JSON.stringify(String.prototype.toString()));
		}
		if (String.prototype.valueOf() !== "") {
			throw new Error("String.prototype.valueOf() 应为 \"\", 得到 " + JSON.stringify(String.prototype.valueOf()));
		}
		if (String.prototype.concat("a") !== "a") {
			throw new Error("String.prototype.concat(\"a\") 应为 \"a\"");
		}

		// 显式 .call 进去也要一样 (test262 的 this-value 用例多半是这种形态)
		if (String.prototype.toString.call(String.prototype) !== "") {
			throw new Error("toString.call(String.prototype) 应为 \"\"");
		}
		"ok";
	`)
}

// TestStringProtoToStringRequiresStringThis 非 String 的 this 必须抛 TypeError。
func TestStringProtoToStringRequiresStringThis(t *testing.T) {
	evalJS(t, `
		function mustThrow(fn, label) {
			let threw = false;
			try { fn(); } catch (e) {
				if (!(e instanceof TypeError)) {
					throw new Error(label + " 抛的不是 TypeError: " + e);
				}
				threw = true;
			}
			if (!threw) { throw new Error(label + " 没有抛 TypeError"); }
		}

		// 普通对象: 崩在原型方法上的那一族
		mustThrow(() => String.prototype.toString.call({}), "toString.call({})");
		mustThrow(() => String.prototype.valueOf.call({}), "valueOf.call({})");

		// 自带 toString 的对象: 旧实现正是从这里绕进 Go 侧的循环
		mustThrow(() => String.prototype.toString.call({ toString() { return "x"; } }),
			"toString.call({toString})");
		mustThrow(() => String.prototype.valueOf.call({ toString() { return "x"; } }),
			"valueOf.call({toString})");

		// 非 String 的原始值也不行 (ThisStringValue 不做强制转换)
		mustThrow(() => String.prototype.toString.call(42), "toString.call(42)");
		mustThrow(() => String.prototype.valueOf.call(null), "valueOf.call(null)");
		mustThrow(() => String.prototype.toString.call(undefined), "toString.call(undefined)");

		// 只有原型、没有 [[StringData]] 的"类 String 对象": 也必须抛。
		// 这条容易和 String.prototype 本身混淆 —— 它俩在 Gox 里都是
		// *object.Object, 但规范上一个是 String 对象、一个不是。
		const sLike = Object.create(String.prototype);
		mustThrow(() => String.prototype.toString.call(sLike), "toString.call(Object.create(String.prototype))");
		mustThrow(() => sLike.toString(), "Object.create(String.prototype).toString()");

		// 裸调用: 内建函数是 strict 的, this 为 undefined
		const bare = String.prototype.toString;
		mustThrow(bare, "String.prototype.toString() 裸调用");
		"ok";
	`)
}

// TestStringProtoToStringAcceptsStringThis 合法 this 仍要正常工作。
//
// 上半条的对称面: 加了校验不能把正常的路一起堵死。三条合法形态都要覆盖 ——
// 原始字符串、String 包装对象、以及通过 .call 显式传入的包装对象。
func TestStringProtoToStringAcceptsStringThis(t *testing.T) {
	evalJS(t, `
		// 原始字符串
		if ("ab".toString() !== "ab") { throw new Error("\"ab\".toString() 错了"); }
		if ("ab".valueOf() !== "ab") { throw new Error("\"ab\".valueOf() 错了"); }
		if (typeof "ab".valueOf() !== "string") {
			throw new Error("valueOf 返回的必须是原始字符串, 不是包装对象");
		}

		// String 包装对象
		const boxed = new String("cd");
		if (boxed.toString() !== "cd") { throw new Error("new String().toString() 错了"); }
		if (boxed.valueOf() !== "cd") { throw new Error("new String().valueOf() 错了"); }
		if (String.prototype.toString.call(boxed) !== "cd") {
			throw new Error("toString.call(包装对象) 错了");
		}

		// 空串与含代理对的串
		if ("".toString() !== "") { throw new Error("空串 toString 错了"); }
		if ("😀".toString() !== "😀") { throw new Error("代理对 toString 错了"); }
		"ok";
	`)
}

// TestStringProtoConcatIsCoercibleNotThisStringValue
//
// concat 走 RequireObjectCoercible + ToString, **不是** ThisStringValue。
// 这两个是**不同的抽象操作**, 混用会直接丢用例 —— 我第一次修这里时就混了,
// 结果 ES5 老用例 S15.5.4.6_A4_T1 从过变挂 (靠用例级 diff 才发现, 读代码
// 看不出来)。所以三条形态各钉一条:
//
//	concat.call(42, "x")                      === "42x"   (ToString 强制转换)
//	({toString(){return "one"}}).concat("two", x) === "onetwoundefined"  (ES5)
//	concat.call(null / undefined)             → TypeError (RequireObjectCoercible)
func TestStringProtoConcatIsCoercibleNotThisStringValue(t *testing.T) {
	evalJS(t, `
		// ① 非 String 的原始值: ToString 强制转换, 不是 TypeError
		if (String.prototype.concat.call(42, "x") !== "42x") {
			throw new Error("concat.call(42) 应强制转换为 \"42x\"");
		}

		// ② ES5 老用例的口径: 自定义 toString 的对象也要先 ToString 再拼
		const inst = { toString: function () { return "one"; } };
		inst.concat = String.prototype.concat;
		let x;
		if (inst.concat("two", x) !== "onetwoundefined") {
			throw new Error("ES5 concat 口径错了, 得到 " + inst.concat("two", x));
		}

		// ③ 只有 null/undefined 抛 TypeError
		function mustThrow(fn, label) {
			let threw = false;
			try { fn(); } catch (e) {
				if (!(e instanceof TypeError)) { throw new Error(label + " 抛的不是 TypeError: " + e); }
				threw = true;
			}
			if (!threw) { throw new Error(label + " 没有抛 TypeError"); }
		}
		const concat = String.prototype.concat;
		mustThrow(() => concat.call(undefined, ""), "concat.call(undefined)");
		mustThrow(() => concat.call(null, ""), "concat.call(null)");
		"ok";
	`)
}

// TestToStringGuardStillStopsUserRecursion 用户自定义递归仍由 Go 侧 depth 兜住。
//
// 这条钉住的是"不要因为这次修了 String 就以为递归问题全解决了": object.ToString
// 的 maxToStringDepth 只对**同一侧**的递归有效 (返回值等于自身、或对象链成环)。
// 它必须继续有效 —— 不能因为修了跨边界的那一族就把它误删。
func TestToStringGuardStillStopsUserRecursion(t *testing.T) {
	evalJS(t, `
		// 返回自身: 触发 r != v 的短路
		const self = { toString() { return this; } };
		if (String(self) !== "[object Object]") {
			throw new Error("返回自身的 toString 应短路为 [object Object], 得到 " + String(self));
		}

		// 两个对象互指成环: 必须靠 depth 计数停住, 不能栈溢出
		const a = {};
		const b = { toString() { return a; } };
		a.toString = () => b;
		// 只要能返回就说明 depth 防御生效 (溢出的话进程已经没了)
		const r = String(a);
		if (typeof r !== "string") { throw new Error("String(a) 应返回字符串"); }
		"ok";
	`)
}

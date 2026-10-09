package vm

import "testing"

// 本文件锁定 computed 求值抛错的 Rx 语义 (看板 rvE6lH, 选定方案 a)。
//
// 契约: computed(fn) 的 `.value` ≡ 调用 fn()。fn() 抛什么, 读 `.value` 就抛什么,
// 且抛出的是**原始抛出值** (throw x 的 x 本身, 不降级成字符串、不包装成 Error)。
//
// 另有三条边界, 各有用例:
//   1. 失败态要缓存 —— 不重复执行 fn (副作用不能翻倍);
//   2. 失败态仍要布线依赖 —— 依赖变了要能重算, 不能锁死在旧错误里;
//   3. 失败不往监听链扩散 —— 一次求值失败不该打断整条 effect / 渲染链路。

// TestComputedValueThrowsOriginalValue: `.value` 抛出原始抛出值, 且值保真。
func TestComputedValueThrowsOriginalValue(t *testing.T) {
	// throw 一个字符串: 抛出来的就该是这个字符串本身, 不是 "Error: boom"
	assertJS(t, `(function(){
		let c = computed(function(){ throw "boom" });
		try { c.value; return "no-throw" } catch (e) { return typeof e + ":" + e }
	})()`, "string:boom")

	// throw 一个数字
	assertJS(t, `(function(){
		let c = computed(function(){ throw 42 });
		try { c.value; return "no-throw" } catch (e) { return typeof e + ":" + e }
	})()`, "number:42")

	// throw 一个 Error: 抛出的必须是**同一个对象** (规范要求 catch 侧 === 抛出侧)
	assertJS(t, `(function(){
		let err = new TypeError("bad");
		let c = computed(function(){ throw err });
		try { c.value; return "no-throw" } catch (e) { return e === err }
	})()`, "true")

	// throw 一个普通对象: 同样要求同一性, 且不被降级成字符串
	assertJS(t, `(function(){
		let marker = { tag: "M" };
		let c = computed(function(){ throw marker });
		try { c.value; return "no-throw" } catch (e) { return (e === marker) + ":" + e.tag }
	})()`, "true:M")
}

// TestComputedErrorProperty: `.error` 是不抛的读法, 给 GUI 一个"渲染错误态"的出口。
func TestComputedErrorProperty(t *testing.T) {
	assertJS(t, `(function(){
		let c = computed(function(){ throw "boom" });
		return String(c.error)
	})()`, "boom")

	// 求值成功时 .error 是 undefined
	assertJS(t, `(function(){
		let c = computed(function(){ return 7 });
		return String(c.error) + "/" + String(c.value)
	})()`, "undefined/7")

	// .error 保真: 拿到的是原始对象本身
	assertJS(t, `(function(){
		let marker = { tag: "M" };
		let c = computed(function(){ throw marker });
		return c.error === marker
	})()`, "true")
}

// TestComputedFailureIsCached: 失败态要缓存, fn 不能被反复执行。
//
// 修复前 valid 一直为 false, 每次读 `.value` 都重跑一遍 fn —— 有副作用的
// 求值函数会被执行 N 次 (读 N 次就跑 N 次)。
func TestComputedFailureIsCached(t *testing.T) {
	assertJS(t, `(function(){
		let runs = 0;
		let c = computed(function(){ runs++; throw "E" });
		for (let i = 0; i < 5; i++) { try { c.value } catch (e) {} }
		return runs
	})()`, "1")

	// 成功态本来就有缓存, 这里一并锁住, 防止改坏
	assertJS(t, `(function(){
		let runs = 0;
		let c = computed(function(){ runs++; return runs });
		c.value; c.value; c.value;
		return runs + ":" + c.value
	})()`, "1:1")
}

// TestComputedFailureRecoversOnDependencyChange: 失败态仍要布线依赖。
//
// 依赖变到一个能算出值的新状态后, 必须能重算出来 —— 否则 computed 会永久
// 锁死在上一次的错误里。
func TestComputedFailureRecoversOnDependencyChange(t *testing.T) {
	assertJS(t, `(function(){
		let n = obs(1);
		let c = computed(function(){
			if (n.value < 0) { throw "neg" }
			return "ok" + n.value
		});
		n.value = -5;                       // 进入错误态
		let first;
		try { c.value; first = "no-throw" } catch (e) { first = e }
		n.value = 9;                        // 依赖变好
		return first + " -> " + c.value + " -> " + String(c.error)
	})()`, "neg -> ok9 -> undefined")
}

// TestComputedFailureDoesNotNotifyListeners: 失败不往监听链扩散。
//
// 这是 rvE6lH 明确要求确认的一点: 抛错的 computed 会不会把 effect 整条链路断?
// 答案是不会 —— 进入错误态时不通知监听者, 只有恢复到正常值才通知。
func TestComputedFailureDoesNotNotifyListeners(t *testing.T) {
	assertJS(t, `(function(){
		let src = obs(0);
		let c = computed(function(){
			if (src.value < 0) { throw "neg" }
			return src.value
		});
		let log = [];
		c.listen(function(v){ log.push("v" + v) });
		// listen 有"立即以当前值回调一次"的 BehaviorSubject 语义, 所以订阅
		// 完成的那一刻 log 里已经有了 v0 —— 要减掉这个基线再看增量。
		let atSubscribe = log.length;
		src.value = -1;                     // 进入错误态: 不通知
		let afterErr = log.length - atSubscribe;
		src.value = 2;                      // 恢复: 通知
		return afterErr + ":" + log.join(",")
	})()`, "0:v0,v2")

	// 初始就处于错误态时, listen 也不该以 undefined 回调
	assertJS(t, `(function(){
		let c = computed(function(){ throw "E" });
		let hits = 0;
		c.listen(function(){ hits++ });
		return hits
	})()`, "0")
}

// TestComputedCallSyntaxThrows: GetX 的 `c()` 直接调用语法同样要抛。
func TestComputedCallSyntaxThrows(t *testing.T) {
	assertJS(t, `(function(){
		let c = computed(function(){ throw "boom" });
		try { c(); return "no-throw" } catch (e) { return e }
	})()`, "boom")

	// 求值成功时 c() 照旧返回计算值, 不能因为加了重抛逻辑而误伤
	assertJS(t, `(function(){
		let c = computed(function(){ return 21 });
		return c() * 2
	})()`, "42")
}

// TestComputedThrowDoesNotPolluteCallbackErrSlot: 读值抛错不得污染后续调用点。
//
// 这是 rr1O8P 那一类"静默错值"的防线: 错误信号必须在读取点被消费掉,
// 不能残留到之后某个不相干的内建调用才被读走。
func TestComputedThrowDoesNotPolluteCallbackErrSlot(t *testing.T) {
	assertJS(t, `(function(){
		let c = computed(function(){ throw "boom" });
		let caught = "none";
		try { c.value } catch (e) { caught = e }
		// 紧随其后的一次不相干内建调用: 若错误槽有残留, 这里会莫名失败
		let after = JSON.parse('{"a":1}').a;
		// 以及一次回调桥调用: 残留信号会被当成"回调抛错"
		let mapped = [1,2].map(function(x){ return x * 2 }).join(",");
		return caught + "|" + after + "|" + mapped
	})()`, "boom|1|2,4")

	// 未被 catch 的读取也不能把信号留给后面的语句
	assertJS(t, `(function(){
		let c = computed(function(){ throw "boom" });
		let out = [];
		for (let i = 0; i < 3; i++) {
			try { c.value; out.push("no") } catch (e) { out.push(e) }
		}
		return out.join(",")
	})()`, "boom,boom,boom")
}

// TestComputedNestedThrowPropagates: 嵌套 computed 的错误要向外传播。
//
// 内层抛错 ⇒ 外层读取 `.value` 时在内层读取点抛出 ⇒ 外层 fn 抛出 ⇒
// 外层也进入错误态, 读外层的 `.value` 同样抛。
func TestComputedNestedThrowPropagates(t *testing.T) {
	assertJS(t, `(function(){
		let inner = computed(function(){ throw "inner-boom" });
		let outer = computed(function(){ return "outer(" + inner.value + ")" });
		try { outer.value; return "no-throw" } catch (e) { return e }
	})()`, "inner-boom")

	// 内层恢复后外层也跟着恢复
	assertJS(t, `(function(){
		let n = obs(0);
		let inner = computed(function(){ if (n.value === 0) { throw "E" } return "i" + n.value });
		let outer = computed(function(){ return "o(" + inner.value + ")" });
		let first;
		try { outer.value } catch (e) { first = e }
		n.value = 3;
		return first + " -> " + outer.value
	})()`, "E -> o(i3)")
}

// TestComputedRefreshClearsFailure: refresh 要能把失败态清掉并重算。
func TestComputedRefreshClearsFailure(t *testing.T) {
	assertJS(t, `(function(){
		let n = obs(0);
		let c = computed(function(){ if (n.value === 0) { throw "E" } return "ok" + n.value });
		try { c.value } catch (e) { }
		n.value = 5;
		c.refresh();
		return c.value + "/" + String(c.error)
	})()`, "ok5/undefined")
}

// TestComputedInspectDoesNotThrow: console.log / 字符串化不能因为抛错态而炸,
// 也不能在诊断路径上往回调桥写错误信号。
func TestComputedInspectDoesNotThrow(t *testing.T) {
	assertJS(t, `(function(){
		let c = computed(function(){ throw "boom" });
		let s = String(c);
		// 抛错态的 computed 字符串化应可读出错误, 且不影响后续求值
		let after = JSON.parse('{"k":2}').k;
		return (s.indexOf("boom") >= 0) + ":" + after
	})()`, "true:2")
}

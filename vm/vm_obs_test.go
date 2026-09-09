package vm

import "testing"

// 本文件锁定 Dart GetX 风格响应式 API 的语义。
// 每个用例对应 obs/computed/ever/once 的一个行为契约。

func TestObsBasic(t *testing.T) {
	assertNumber(t, evalJS(t, `let c = obs(0); c.value = 5; c.value`), 5)
	// GetX: count() 直接调用返回当前值
	assertNumber(t, evalJS(t, `let c = obs(41); c() + 1`), 42)
	assertString(t, evalJS(t, `let s = obs("hi"); s.value`), "hi")
	assertJS(t, `typeof obs(1)`, "object")
	// .obs 幂等: 对已是 Rx 的单元原样返回
	assertJS(t, `(function(){ let r = obs(5); return obs(r) === r })()`, "true")
}

func TestObsListen(t *testing.T) {
	// listen 立即以当前值回调一次 (BehaviorSubject 语义), 之后每次变化回调
	assertJS(t, `(function(){ let c = obs(0); let log = [];
		c.listen(function(v){ log.push(v) });
		c.value = 1; c.value = 2; return log.join(",") })()`, "0,1,2")

	// 值相等时不通知 (GetX 核心语义)
	assertNumber(t, evalJS(t, `let c = obs(0); let n = 0;
		c.listen(function(){ n++ });
		c.value = 0;
		n`), 1)

	// 回调收到 (newValue, oldValue)
	assertJS(t, `(function(){ let c = obs(1); let pair = "";
		c.listen(function(nv, ov){ pair = nv + ">" + ov });
		c.value = 2; return pair })()`, "2>1")

	// close() 取消全部订阅
	assertNumber(t, evalJS(t, `let c = obs(1); let n = 0;
		c.listen(function(){ n++ });
		c.close(); c.value = 2;
		n`), 1)

	// refresh() 强制通知
	assertNumber(t, evalJS(t, `let c = obs(1); let n = 0;
		c.listen(function(){ n++ });
		c.refresh(); c.refresh();
		n`), 3)

	// 订阅对象 close() 单独取消
	assertNumber(t, evalJS(t, `let c = obs(0); let n = 0;
		let sub = c.listen(function(){ n++ });
		sub.close();
		c.value = 9;
		n`), 1)
}

func TestComputed(t *testing.T) {
	// 惰性求值
	assertNumber(t, evalJS(t, `let a = obs(2); let b = obs(3);
		let sum = computed(function(){ return a.value + b.value });
		sum.value`), 5)

	// 依赖变化自动重算
	assertNumber(t, evalJS(t, `let a = obs(2); let b = obs(3);
		let sum = computed(function(){ return a.value + b.value });
		sum.listen(function(){});
		a.value = 10;
		sum.value`), 13)

	// 变化通知: 立即值 + 重算值
	assertJS(t, `(function(){ let a = obs(2); let b = obs(3);
		let sum = computed(function(){ return a.value + b.value });
		let log = [];
		sum.listen(function(v){ log.push(v) });
		a.value = 10; return log.join(",") })()`, "5,13")

	// 重算结果不变时不通知
	assertNumber(t, evalJS(t, `let a = obs(2);
		let f = computed(function(){ return a.value * 0 });
		let n = 0;
		f.listen(function(){ n++ });
		a.value = 9;
		n`), 1)

	// 嵌套 computed: 上层依赖下层
	assertNumber(t, evalJS(t, `let a = obs(1);
		let d = computed(function(){ return a.value * 2 });
		let e = computed(function(){ return d.value + 1 });
		e.value`), 3)
	assertNumber(t, evalJS(t, `let a = obs(1);
		let d = computed(function(){ return a.value * 2 });
		let e = computed(function(){ return d.value + 1 });
		a.value = 5;
		e.value`), 11)
}

func TestRxList(t *testing.T) {
	// push 触发通知
	assertJS(t, `(function(){ let l = obs([1,2]); let n = 0;
		l.listen(function(){ n++ });
		l.push(3); return n + ":" + l.length })()`, "2:3")

	// 索引读写
	assertNumber(t, evalJS(t, `let l = obs([1,2]); l[0] = 9; l[0]`), 9)
	assertJS(t, `(function(){ let l = obs([1,2]); l[0] = 9; return l.join(",") })()`, "9,2")

	// 索引写触发通知
	assertNumber(t, evalJS(t, `let l = obs([1,2]); let n = 0;
		l.listen(function(){ n++ });
		l[1] = 8;
		n`), 2) // listen 立即 1 次 + 索引写 1 次

	// remove (GetX 语义)
	assertJS(t, `(function(){ let l = obs([1,2,3]); l.remove(2); return l.join(",") })()`, "1,3")

	// 其余数组方法透传
	assertJS(t, `(function(){ let l = obs([3,1,2]); return l.sort().join(",") })()`, "1,2,3")
}

func TestRxMap(t *testing.T) {
	assertNumber(t, evalJS(t, `let m = obs(new Map()); m.set("a", 1); m.value.get("a")`), 1)
	// set/delete 均触发通知
	assertNumber(t, evalJS(t, `let m = obs(new Map()); let n = 0;
		m.listen(function(){ n++ });
		m.set("a", 1); m.delete("a");
		n`), 3) // listen 立即 1 次 + set + delete
}

func TestRxWorkers(t *testing.T) {
	// ever: 每次变化回调
	assertJS(t, `(function(){ let c = obs(0); let log = [];
		ever(c, function(v){ log.push(v) });
		c.value = 1; c.value = 2; return log.join(",") })()`, "0,1,2")

	// once: 仅首次变化回调
	assertJS(t, `(function(){ let c = obs(0); let log = [];
		once(c, function(v){ log.push(v) });
		c.value = 1; c.value = 2; return log.join(",") })()`, "0")

	// worker 参数错误
	assertJS(t, `(function(){ try { ever(1, function(){}); return "no-throw" } catch (e) { return e.name } })()`, "TypeError")
}

func TestRxTypeError(t *testing.T) {
	// computed 非函数参数
	assertJS(t, `(function(){ try { computed(1); return "no-throw" } catch (e) { return e.name } })()`, "TypeError")
}

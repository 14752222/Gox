package vm

import (
	"testing"
)

// ===== 内建对象的 well-known Symbol 成员 (看板 rYFTlt 阶段一) =====
//
// 此前 Gox 里只有少数显式注册过的 Symbol 成员可读 (Math/JSON/Symbol.prototype
// 的 @@toStringTag、Date.prototype 的 @@toPrimitive), 而原型上的 @@iterator、
// 构造器上的 @@species、Symbol.prototype 的 @@toPrimitive、Function.prototype
// 的 @@hasInstance 全部读不到 —— `arr[Symbol.iterator]()` 直接 TypeError。
//
// 本文件按 rYFTlt 单面「现象」表逐条断言, 并锁定三处**断链修复**:
//   - *object.String 此前没有 GetProto, 原型链遍历在字符串处断掉;
//   - *Map / *Set 的 GetProto 在实例级 proto 为 nil 时不回退全局原型
//     (而它们的 GetProperty 会回退 —— 两处口径不一致);
//   - vm.getIndex 的 Symbol 键读取按类型穷举, 漏掉的类型静默回落 default。
//
// 基准口径: Node v22 (探针已在沙箱内对照验证)。

// TestWellKnownSymbolIteratorMembers 单面现象表逐项: 都必须是 function。
func TestWellKnownSymbolIteratorMembers(t *testing.T) {
	sink := evalWithProbe(t, `
		__probe(typeof [][Symbol.iterator]);
		__probe(typeof "abc"[Symbol.iterator]);
		__probe(typeof new Map()[Symbol.iterator]);
		__probe(typeof new Set()[Symbol.iterator]);
		__probe(typeof Array[Symbol.species]);
		__probe(typeof Promise[Symbol.species]);
		__probe(typeof Map[Symbol.species]);
		__probe(typeof Set[Symbol.species]);
		__probe(typeof Symbol.prototype[Symbol.toPrimitive]);
		__probe(typeof Function.prototype[Symbol.hasInstance]);
	`)
	for i, want := range []string{
		"function", "function", "function", "function", "function",
		"function", "function", "function", "function", "function",
	} {
		if got := sink.args[i].Inspect(); got != want {
			t.Errorf("观测值 #%d: 期望 %s, 实得 %s", i, want, got)
		}
	}
}

// TestWellKnownSymbolIteratorIdentity 规范里 @@iterator 与既有方法是**同一个
// 函数对象**: Array/Set 取 values, Map 取 entries。test262 有显式断言。
func TestWellKnownSymbolIteratorIdentity(t *testing.T) {
	sink := evalWithProbe(t, `
		__probe(Array.prototype[Symbol.iterator] === Array.prototype.values);
		__probe(Set.prototype[Symbol.iterator] === Set.prototype.values);
		__probe(Map.prototype[Symbol.iterator] === Map.prototype.entries);
	`)
	for i, want := range []string{"true", "true", "true"} {
		if got := sink.args[i].Inspect(); got != want {
			t.Errorf("同一性 #%d: 期望 %s, 实得 %s", i, want, got)
		}
	}
}

// TestWellKnownSymbolIteratorCallable 读到之后必须**调得动**, 且结果正确。
// 计算成员调用 (obj[k]()) 的接收者绑定由阶段二保证, 这里两条路都验:
// 直接调用与显式 .call。
func TestWellKnownSymbolIteratorCallable(t *testing.T) {
	sink := evalWithProbe(t, `
		__probe([1,2][Symbol.iterator]().next().value);
		__probe("abc"[Symbol.iterator]().next().value);
		__probe(Array.prototype[Symbol.iterator].call([7,8]).next().value);
		__probe("xy"[Symbol.iterator].call("ab").next().value);
	`)
	for i, want := range []string{"1", "a", "7", "a"} {
		if got := sink.args[i].Inspect(); got != want {
			t.Errorf("调用结果 #%d: 期望 %s, 实得 %s", i, want, got)
		}
	}
}

// TestWellKnownSymbolSpeciesIsAccessor @@species 规范形态是**访问器**。
// 写成数据属性的话, 子类读 @@species 会拿到父类 (应为子类本身), 子类化
// 全错 —— 这里用 descriptor 形状把这条钉住。
func TestWellKnownSymbolSpeciesIsAccessor(t *testing.T) {
	sink := evalWithProbe(t, `
		const d = Object.getOwnPropertyDescriptor(Array, Symbol.species);
		__probe(typeof d.get);
		__probe(d.set);
		__probe(d.enumerable);
		__probe(d.configurable);
		__probe(Array[Symbol.species] === Array);
		// getter 的 this 必须是**接收者**: 子类/子对象读 @@species 得到自己。
		const sub = Object.create(Array);
		__probe(sub[Symbol.species] === sub);
	`)
	for i, want := range []string{"function", "undefined", "false", "true", "true", "true"} {
		if got := sink.args[i].Inspect(); got != want {
			t.Errorf("@@species #%d: 期望 %s, 实得 %s", i, want, got)
		}
	}
}

// TestSymbolPrototypeToPrimitive 规范 20.4.3.5: 原样返回 this。
func TestSymbolPrototypeToPrimitive(t *testing.T) {
	sink := evalWithProbe(t, `
		const s = Symbol("x");
		__probe(Symbol.prototype[Symbol.toPrimitive].call(s) === s);
		__probe(typeof Symbol.prototype[Symbol.toPrimitive]);
	`)
	for i, want := range []string{"true", "function"} {
		if got := sink.args[i].Inspect(); got != want {
			t.Errorf("@@toPrimitive #%d: 期望 %s, 实得 %s", i, want, got)
		}
	}
}

// TestFunctionPrototypeHasInstance 规范 20.2.3.8: OrdinaryHasInstance(this, V)。
// 原型链命中 / 未命中 / 左值为原始值三条路。
func TestFunctionPrototypeHasInstance(t *testing.T) {
	sink := evalWithProbe(t, `
		const HI = Function.prototype[Symbol.hasInstance];
		__probe(HI.call(Array, []));
		__probe(HI.call(Array, {}));
		__probe(HI.call(Array, 0));
		__probe(HI.call(Map, new Map()));
		__probe(HI.call(Array, "s"));
	`)
	for i, want := range []string{"true", "false", "false", "true", "false"} {
		if got := sink.args[i].Inspect(); got != want {
			t.Errorf("@@hasInstance #%d: 期望 %s, 实得 %s", i, want, got)
		}
	}
}

// TestSymbolIndexWalksProtoChainOnAllTypes Symbol 键读取必须在**所有**对象
// 类型上沿原型链 —— 此前 vm.getIndex 按类型穷举 (*Array / *TypedArray /
// *Object / 函数类各一份), 漏掉 *String / *Map / *Set。
//
// 这三类的断链各有独立成因, 一并钉住:
//   - *String 根本没有 GetProto 方法;
//   - *Map / *Set 的 GetProto 在实例级 proto 为 nil 时不回退全局原型。
func TestSymbolIndexWalksProtoChainOnAllTypes(t *testing.T) {
	sink := evalWithProbe(t, `
		__probe(typeof "abc"[Symbol.iterator]);
		__probe(typeof new Map()[Symbol.iterator]);
		__probe(typeof new Set()[Symbol.iterator]);
		__probe(typeof [][Symbol.iterator]);
		__probe(typeof new Date()[Symbol.toPrimitive]);
	`)
	for i, want := range []string{"function", "function", "function", "function", "function"} {
		if got := sink.args[i].Inspect(); got != want {
			t.Errorf("原型链 #%d: 期望 %s, 实得 %s", i, want, got)
		}
	}
}

// TestSpreadStillWorksAfterIteratorAssembly 装配 @@iterator 之后, 数组展开 /
// 调用展开不得倒退。
//
// 这条同时守着一个**既有** bug: resolveSymbolIterator 对 JS 层迭代器已经包
// 好 *runtime.Iterator, 而 OP_ARRAY_SPREAD 把它当"待迭代值"再丢回
// runtime.GetIterable —— 后者只认 JS 值, 于是恒报 "is not iterable"。此前
// 内建原型上没有 @@iterator, 数组展开永远走下面的快路径, 这条死代码从未
// 被执行; 装配 @@iterator 后它第一次被走到。
func TestSpreadStillWorksAfterIteratorAssembly(t *testing.T) {
	sink := evalWithProbe(t, `
		__probe([...[1,2],3].join(","));
		__probe([1,...[2,3]].join(","));
		__probe([..."ab"].join(","));
		__probe([...new Set([1,2])].join(","));
		function g() { return [...arguments].join(","); }
		__probe(g(1,2,3));
		function s(x, y) { return "" + x + y; }
		__probe(s(...[7,8]));
		__probe(Math.max(...[1,5,3]));
	`)
	for i, want := range []string{"1,2,3", "1,2,3", "a,b", "1,2", "1,2,3", "78", "5"} {
		if got := sink.args[i].Inspect(); got != want {
			t.Errorf("展开 #%d: 期望 %s, 实得 %s", i, want, got)
		}
	}
}

// TestForOfAndDestructuringNotRegressed 单面点名的「最大回归风险面」:
// for-of / 解构走 VM 内部通道, 必须与新增的 @@iterator 路径结论一致。
func TestForOfAndDestructuringNotRegressed(t *testing.T) {
	sink := evalWithProbe(t, `
		let a = ""; for (const x of [1,2,3]) a += x;            __probe(a);
		let b = ""; for (const c of "abc") b += c;               __probe(b);
		let d = ""; for (const kv of new Map([["a",1]])) d += kv[0]; __probe(d);
		let e = ""; for (const v of new Set([1,2])) e += v;      __probe(e);
		const [p, q] = [10, 20];                                 __probe(p + "," + q);
		const [r, ...rest] = [1,2,3,4];                          __probe(rest.length);
		let f = ""; for (const k in {a:1,b:2}) f += k;           __probe(f);
		                                                          __probe(Object.keys([1,2]).join(","));
		                                                          __probe(JSON.stringify({a:1}));
	`)
	for i, want := range []string{"123", "abc", "a", "12", "10,20", "3", "ab", "0,1", "{\"a\":1}"} {
		if got := sink.args[i].Inspect(); got != want {
			t.Errorf("回归 #%d: 期望 %s, 实得 %s", i, want, got)
		}
	}
}

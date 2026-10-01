package vm

import "testing"

// TestIndexPropEquivalence 守住不变量: obj["k"] 恒等于 obj.k。
//
// 背景 (2026-10-01 实测): vm.getIndex 是**按类型穷举**的, 只认
// Array / String / TypedArray / RxIndexed / *Object —— 而 GlobalObject、Map、
// Set、Closure、Error 等"自成一类"的值 (它们实现了 Value 接口, 但不是 *Object)
// 会一路落到函数末尾返回 undefined。与之相对, OP_GET_PROP 走的是通用
// Value.GetProperty, 对这些类型全部正常。于是同一个属性, 两种写法结果不同:
//
//	globalThis.z   为 42    而 globalThis["z"]   为 undefined
//	map.size       为 1     而 map["size"]       为 undefined
//	fn.name        为 named 而 fn["name"]        为 undefined
//
// 连带受害者是 ++ / -- / +=: compileIncDec 对成员表达式走
// compileMemberRef + OP_DUP2/OP_GET_INDEX, 也就是 GET_INDEX 路径, 因此
// `globalThis.n++` 读到 undefined, ToNumber 之后算出 NaN —— 而完全同形的
// `obj.n++` (普通对象) 一切正常, 是本工程最难排查的一类静默失效。
//
// 修复: vm.getIndex 增加 default 兜底分支, 与 setIndex 的 default 对称。
func TestIndexPropEquivalence(t *testing.T) {
	numCases := []struct {
		name string
		src  string
		want float64
	}{
		{"globalThis 点号等于方括号", `globalThis.z = 42; globalThis["z"]`, 42},
		{"Map size", `const m = new Map(); m.set("k", 7); m["size"]`, 1},
		{"Set size", `const s = new Set([1, 2, 3]); s["size"]`, 3},
		{"globalThis 别名后缀自增", `globalThis.a = 0; const g = globalThis; g.a++; g.a`, 1},
		{"globalThis 后缀自增", `globalThis.n = 0; globalThis.n++; globalThis.n`, 1},
		{"globalThis 前缀自增", `globalThis.n = 0; ++globalThis.n`, 1},
		{"globalThis 后缀自减", `globalThis.n = 5; globalThis.n--; globalThis.n`, 4},
		{"globalThis 复合加赋值", `globalThis.n = 1; globalThis.n += 2; globalThis.n`, 3},
		{"globalThis 方括号复合赋值", `globalThis.n = 1; globalThis["n"] += 2; globalThis.n`, 3},
	}
	for _, tc := range numCases {
		t.Run(tc.name, func(t *testing.T) {
			assertNumber(t, evalJS(t, tc.src), tc.want)
		})
	}

	strCases := []struct {
		name string
		src  string
		want string
	}{
		{"Error message 索引读", `const e = new Error("boom"); e["message"]`, "boom"},
		{"闭包 name 索引读", `const f = function named() {}; f["name"]`, "named"},
	}
	for _, tc := range strCases {
		t.Run(tc.name, func(t *testing.T) {
			assertString(t, evalJS(t, tc.src), tc.want)
		})
	}
}

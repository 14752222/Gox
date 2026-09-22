package gfx

import "testing"

// ===== 嵌套解构 (2026-09-22 修) 的端到端回归 =====
//
// 当时报的症状就是 createResource 那条最自然的写法:
//
//	const [data, {refetch}] = createResource(f);   // parser 直接报错, 只能拆成两步
//
// 这里把"解构语法 × 响应式资源"的组合走一遍真 VM: 嵌套位置上的绑定必须真的接上
// (data 能取到值、解构出来的 refetch / state 是可调用的真函数)。

func TestNestedDestructureOfCreateResource(t *testing.T) {
	v, _ := evalUI(t, `
		import { createResource } from "gx/solid";
		const [data, {refetch, state}] = createResource(() => ({ n: 7 }));
		globalThis.g_n = data().n;
		globalThis.g_state = state();
		globalThis.g_refetch = typeof refetch;
	`)
	assertGlobal(t, v, "g_n", "7")
	assertGlobal(t, v, "g_state", "ready")
	assertGlobal(t, v, "g_refetch", "function")
}

// TestNestedDestructuringEndToEnd 语言层: 数组套数组 / 数组套对象 / 对象套对象
// 以及嵌套默认值, 全部走真编译器 + VM 求值。
func TestNestedDestructuringEndToEnd(t *testing.T) {
	v, _ := evalUI(t, `
		const [a, [b, c]] = [1, [2, 3]];
		const {x, y: {z}} = {x: 4, y: {z: 5}};
		const [m, {n = 9}] = [6, {}];
		globalThis.g_sum = a + b + c + x + z + m + n;
	`)
	assertGlobal(t, v, "g_sum", "30")
}

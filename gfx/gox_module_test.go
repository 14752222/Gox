package gfx

import "testing"

// ===== 聚合模块 "gox" (gx/* 导出并集) =====
//
// 断言口径与 view_test 相同: 真 VM + 假 Surface + 事件泵全链路,
// 证明一行 gox 导入拿到的 h/render/createSignal/Switch 与细分模块导入
// 行为完全一致 (导出表是同一份对象, 不是复制)。
//
// 2026-09-20: 列表与条件改成了元素级指令 (each / show), 不再需要 import ——
// 聚合模块里自然也没有 For / Show 了。

// TestGoxUmbrellaOneImportCoversGfxSolidView 一行 gox 导入同时覆盖
// gx/solid + gx/gfx (+ gx/view 的 Switch/Match), 信号驱动列表渲染正常工作
// (each 是 h() 层的指令, 与导入哪个模块无关)。
func TestGoxUmbrellaOneImportCoversGfxSolidView(t *testing.T) {
	v, fake := evalUI(t, `
		import { h, render, createSignal } from "gox";
		const [rows, setRows] = createSignal(["a", "b"]);
		render(
			<window title="gox" width={300} height={200}>
				<column gap={2}>
					<text note="head">{() => "n=" + rows().length}</text>
					<view each={rows}>{(it) => <text note={it}>{it}</text>}</view>
				</column>
			</window>
		);
		globalThis.append = () => setRows(rows().concat(["c"]));
	`)

	runPumpSteps(t, v, fake, []func(){
		func() { viewAssertLayout(t, uiRoot(t), "head|a|b", "n=2|a|b") },
		func() { callGlobalFn(t, v, "append") },
		func() { viewAssertLayout(t, uiRoot(t), "head|a|b|c", "n=3|a|b|c") },
	})
}

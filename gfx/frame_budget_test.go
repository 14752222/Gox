package gfx

import (
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== T06: UI 帧预算 (60fps 口径的落闸点) =====
//
// DoD 要求"UI 场景 60fps 不掉帧"。这里给出可复现的落闸口径:
// 代表性节点规模 (6×8 卡片栅格, ~200 节点) 下, 布局 + 全树软件光栅化
// 的**平均**单帧耗时必须显著低于 16.7ms。
//
// 说明 (口径诚实性):
//   - 这是无 VSync 的软件渲染管线耗时, 不含 OS 合成/上屏 (假 Surface);
//   - 闸门卡 8ms (约 2 倍余量): 足以拦住"数量级劣化"的回归, 又不会被
//     共享 CI runner 的抖动误杀。实测见 scripts/bench-compare.sh 的存档。
//   - 超预算时的第一嫌疑: raster/drawNode 的新绘制分支、布局回退到 O(n²)。

func TestFrameBudgetRepresentativeTree(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过帧预算测量")
	}
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { h, render, createSignal } from "gox";
		const [tick] = createSignal(0);
		const card = (i) => (
			<column gap={4} padding={8} background="#ffffff">
				<text font={13}>{"卡片 " + i}</text>
				<progress value={() => (i % 97) / 97} />
				<row gap={6}>
					<button padding={4} onClick={() => {}}>详情</button>
					<button padding={4} onClick={() => {}}>忽略</button>
					<checkbox checked={tick} onClick={() => {}} />
				</row>
			</column>
		);
		const rows = [];
		for (let r = 0; r < 8; r++) {
			const cells = [];
			for (let c = 0; c < 6; c++) { cells.push(card(r * 6 + c)); }
			rows.push(<row gap={8}>{cells}</row>);
		}
		render(
			<window title="frame-budget" width={960} height={640}>
				<column gap={8} padding={10} background="#eef1f5">{rows}</column>
			</window>
		);
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	_ = v
	root := uiRoot(t)

	// 预热 (字体缓存/首次路径), 不计入统计
	const W, H = 960, 640
	for i := 0; i < 5; i++ {
		Layout(root, W, H)
		drawNode(fake.img, root)
	}

	const frames = 200
	start := time.Now()
	for i := 0; i < frames; i++ {
		Layout(root, W, H)
		drawNode(fake.img, root)
	}
	elapsed := time.Since(start)
	avg := elapsed / frames

	nodes := countNodes(root)
	t.Logf("节点 %d, %d 帧共 %v, 平均单帧 %v", nodes, frames, elapsed, avg)
	if avg > 8*time.Millisecond {
		t.Fatalf("平均单帧 %v 超过 8ms 预算 (节点 %d) —— 查 raster/layout 回归", avg, nodes)
	}
}

// countNodes 统计子树规模 (给日志与失败信息用)。
func countNodes(root *GuiNode) int {
	n := 1
	for _, c := range root.Children {
		n += countNodes(c)
	}
	return n
}

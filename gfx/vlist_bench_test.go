package gfx

import (
	"fmt"
	"image"
	"strings"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== 长列表基准 (虚拟化 vs 全量) =====
//
// 这些基准回答一个具体问题: **行数增长时, 首帧与滚动要各付多少**。
// 两个量分别对应两条真实路径:
//
//	首帧   —— 从零建树到上屏一次 (用户打开页面的等待)
//	滚动帧 —— 滚动若干像素后重排+重绘一帧 (用户拖动时的流畅度)
//
// 口径: 用真实的内核路径 (JS 建树 + Layout + Draw), 但**不建平台窗口** ——
// 那部分是后端的事, 与本优化无关。断言只看"随 N 的增长趋势", 不看绝对值
// (绝对值随机器变化, 趋势才是可移植的结论)。
//
// 跑法:
//   go test ./gfx -run '^$' -bench 'BenchmarkVlist' -benchtime 1x -v
//   go test ./gfx -run '^$' -bench 'BenchmarkVlist' -benchmem
//
// ## 为什么必须走 EvalVM 而不是直接拼 GuiNode (2026-10-01 实测踩到)
//
// 第一版基准用纯 Go 拼树 (直接调 JSBuiltinH + object.CallFunction 跑渲染函数)。
// 它**跑不出任何行**: `object.CallFunction` 在 vm 包没被导入时直接返回
// undefined (回调桥没注册, 见 object/callback.go), 于是每一行的渲染函数都
// "调用了但返回空" —— 树上是 N 个空 slot, 一个 row 节点都没有。症状是数字
// 漂亮得离谱 (十万行首帧 110ms) 而 `物化=100000` 全是空壳, 谁也没发现。
// 同一类坑还有 gx/solid 未注册 (见下面 benchEnsureSolid 的说明)。
//
// 结论: **基准必须跑在"真实宿主同款"的环境里**, 而不是"能编译就行"的等价物 ——
// 否则量到的是一棵空树的耗时。走 EvalVM 同时解决了 VM 与内置模块两件事。

// benchRowH 是基准里每行的固定高 (与 vlist_demo.js 的 ROW_H 同量级)。
const benchRowH = 28

// benchSource 生成一段建列表的脚本 (与 vlist_demo.js 同形, 便于结论互相对照)。
//
// 导出 `__root` 与 `__rowCalls`: 前者给基准取根节点, 后者是**渲染函数被调用
// 次数** —— "首帧建了多少行"不能从树上数 (虚拟化会把多余的销毁, 树上只剩窗口
// 内的十几行), 只能数调用次数。
func benchSource(n int, vlistOn bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "import { h, render } from \"gx/gfx\";\n")
	fmt.Fprintf(&b, "const N = %d, ROW_H = %d;\n", n, benchRowH)
	b.WriteString("const rows = [];\n")
	b.WriteString("for (let i = 0; i < N; i++) rows.push({ id: i, title: \"第 \" + i + \" 行 · 基准\" });\n")
	b.WriteString("const stats = { rowCalls: 0 };\n")
	b.WriteString("globalThis.__stats = stats;\n")
	if vlistOn {
		fmt.Fprintf(&b, "const scrollProps = { vlist: true, itemHeight: ROW_H, width: 420, height: 300 };\n")
	} else {
		fmt.Fprintf(&b, "const scrollProps = { width: 420, height: 300 };\n")
	}
	b.WriteString("globalThis.__root = render(\n")
	b.WriteString("  h(\"column\", {},\n")
	b.WriteString("    h(\"scroll\", scrollProps,\n")
	b.WriteString("      h(\"view\", { each: rows, key: \"id\" },\n")
	b.WriteString("        (r, i) => { stats.rowCalls++; return h(\"row\", { height: ROW_H, padding: 6 }, h(\"text\", {}, i + \"\")); }\n")
	b.WriteString("      )\n")
	b.WriteString("    )\n")
	b.WriteString("  ),\n")
	b.WriteString("  { title: \"vlist bench\", width: 470, height: 400 }\n")
	b.WriteString(");\n")
	return b.String()
}

// benchEnv 是一次基准运行的现场: 一个活着的 VM + 它的根节点。
type benchEnv struct {
	v    *vm.VM
	root *GuiNode
}

// benchSetup 起一个基准现场: 装假 Surface, 跑脚本建树。
//
// 假 Surface 是必需的: `<scroll>` 的窗口现在会真的被建出来, 没有后端就会
// 走平台分支失败。它也让窗口注册表有真实的键 (见 helpers_test.go)。
func benchSetup(b *testing.B, n int, vlistOn bool) *benchEnv {
	b.Helper()
	object.GlobalScheduler().ClearAll()
	b.Cleanup(func() { object.GlobalScheduler().ClearAll() })
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	b.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(benchSource(n, vlistOn))
	if err != nil {
		b.Fatalf("EvalVM: %v", err)
	}
	appMu.Lock()
	a := activeApp
	appMu.Unlock()
	if a == nil || a.root == nil {
		b.Fatalf("脚本没有建立窗口 (activeApp/root 为空)")
	}
	return &benchEnv{v: v, root: a.root}
}

// benchRowCallsOf 读脚本里的渲染调用计数。
//
// 计数放在一个普通对象 `stats` 上而不是 `globalThis.__rowCalls++`:
// **`globalThis` 上属性的自增在本机会算出 NaN** (2026-10-01 实测, 一个真实的
// 语言层 bug, 已单独记进看板), 拿它当计数器会让基准悄悄失去意义。
func benchRowCallsOf(env *benchEnv) int {
	g := env.v.Globals()
	v, ok := g.Get("__stats")
	if !ok {
		return -1
	}
	obj, ok := v.(*object.Object)
	if !ok {
		return -1
	}
	desc, ok := obj.Properties["rowCalls"]
	if !ok || desc.Value == nil {
		return -1
	}
	num, ok := desc.Value.(*object.Number)
	if !ok {
		return -1
	}
	return int(num.Value)
}

// benchDraw 走真实的光栅入口 (Draw 内部即逐节点 drawNode), 供基准计量。
func benchDraw(root *GuiNode, w, h int) {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	Draw(img, root)
}

// benchFindScroll 在树里找第一个 scroll 容器 (基准用来驱动滚动)。
func benchFindScroll(root *GuiNode) *GuiNode {
	return findFirstWhere(root, func(n *GuiNode) bool { return n.Tag == "scroll" })
}

// benchFirstFrame 量"建树 (JS 求值) + 首帧布局 + 一次全量绘制"的耗时。
//
// 同时报告**渲染函数被调用了多少次** —— 那才是建树成本的真相。
func benchFirstFrame(b *testing.B, n int, vlistOn bool) time.Duration {
	b.Helper()
	start := time.Now()
	env := benchSetup(b, n, vlistOn)
	root := env.root
	// 首帧成本 = 脚本求值 (已含建树) + 第一次布局/绘制。把两者分开报:
	// "首帧建了多少行"是虚拟化唯一没省掉的那块, 必须单独可见。
	buildCalls := benchRowCallsOf(env)
	evalEl := time.Since(start)
	start = time.Now()
	Layout(root, 600, 600)
	benchDraw(root, 600, 600)
	paintEl := time.Since(start)
	el := evalEl + paintEl
	a := activeApp
	b.Logf("首帧 %d 行 vlist=%v: 渲染调用=%d 求值=%v 布局+绘制=%v 合计=%v | %s",
		n, vlistOn, buildCalls, evalEl.Round(time.Millisecond),
		paintEl.Round(time.Millisecond), el.Round(time.Millisecond), a.vlistStatsOf(root))
	return el
}

// BenchmarkVlistFirstFrame 首帧耗时 vs 行数。
//
// 期望结论: 全量随 N 线性增长; 虚拟化基本持平 (只跟视口有关)。
func BenchmarkVlistFirstFrame(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		for _, on := range []bool{false, true} {
			label := "full"
			if on {
				label = "vlist"
			}
			b.Run(fmt.Sprintf("%s/%d", label, n), func(b *testing.B) {
				benchFirstFrame(b, n, on)
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					benchFirstFrame(b, n, on)
				}
			})
		}
	}
}

// BenchmarkVlistScroll 滚动一帧的耗时 (布局 + 绘制)。
//
// 期望结论: 全量随 N 线性增长; 虚拟化只跟"窗口换行次数"有关, 与 N 无关。
//
// **单次运行内滚动**: 拆成"每次 b.Run 都重建环境"会把建树成本混进来,
// 那是首帧基准的事; 这里只量滚动本身。
func BenchmarkVlistScroll(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		for _, on := range []bool{false, true} {
			label := "full"
			if on {
				label = "vlist"
			}
			b.Run(fmt.Sprintf("%s/%d", label, n), func(b *testing.B) {
				env := benchSetup(b, n, on)
				root := env.root
				Layout(root, 600, 600)
				sc := benchFindScroll(root)
				if sc == nil {
					b.Fatal("没找到 scroll")
				}
				// 每次滚一屏, 到顶了回 0 (保证每次都真的重排重绘)。
				const step = 300
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					sc.scrollBy(0, step)
					Layout(root, 600, 600)
					benchDraw(root, 600, 600)
					if sc.offsetY >= sc.scrollMaxOffset() {
						sc.offsetY = 0
					}
				}
			})
		}
	}
}

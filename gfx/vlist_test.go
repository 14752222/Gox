package gfx

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== 虚拟化长列表 (vlist) 测试 =====
//
// 分两层:
//  1. 纯几何 (vlistCompute / vlistSpacers): 边界用表驱动钉死, 不依赖运行时;
//  2. 端到端: 挂一棵 <scroll vlist><view each={N 行}> 的树, 断言**物化节点数
//     远小于 N**、滚动后窗口平移、内容总高恒等于 N*itemH (滚动条不变)。
//
// 第 2 层是关键: 它同时钉住"真的虚拟化了"和"滚动条没被搞坏"两件事 ——
// 只虚拟化不对齐总高的话, 滚动条会变得巨长/巨短, 那是虚拟化最常见的副作用。

// vlistCountRows 数一个子树里的**行** (each 每项的 slot 宿主都带 key 标记)。
// 用"带 height prop 的子节点数"更直接: 测试脚本里每行都显式写 height。
func vlistCountRows(n *GuiNode) int {
	count := 0
	var walk func(x *GuiNode)
	walk = func(x *GuiNode) {
		if x == nil {
			return
		}
		if _, ok := x.Props["height"]; ok && x.Tag == "row" {
			count++
		}
		for _, c := range x.Children {
			walk(c)
		}
	}
	walk(n)
	return count
}

// TestVlistCompute 纯几何: 可见区间 + 缓冲 + clamp + 病态兜底。
func TestVlistCompute(t *testing.T) {
	cases := []struct {
		name                  string
		offsetY, viewH, itemH int
		buffer, total         int
		wantFirst, wantLast   int
	}{
		{"顶部首屏", 0, 10, 10, 2, 100, 0, 3},            // 可见 1 行 + 下缓冲 2
		{"滚到中段", 500, 100, 10, 2, 1000, 48, 62},      // 可见 50..59 + 上下各 2
		{"滚到底部", 9000, 1000, 10, 2, 1000, 898, 1000}, // last 被 total 截断
		{"小列表不足一屏", 0, 500, 10, 2, 3, 0, 3},          // 全物化
		{"buffer=0", 100, 100, 10, 0, 1000, 10, 20},  // 无缓冲: 恰好可见区
		{"空数据", 0, 100, 10, 2, 0, 0, 0},              // 空列表 (退化路径)
		{"itemH=0 防呆", 0, 100, 0, 2, 100, 0, 0},      // 非正 itemH ⇒ 空窗口
		{"负 offset 收口", -50, 100, 10, 1, 100, 0, 11}, // offset<0 当 0
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := vlistCompute(c.offsetY, c.viewH, c.itemH, c.buffer, c.total)
			if w.first != c.wantFirst || w.last != c.wantLast {
				t.Fatalf("窗口 = [%d, %d), want [%d, %d)",
					w.first, w.last, c.wantFirst, c.wantLast)
			}
			if c.total > 0 && c.itemH > 0 && w.contentH != c.total*c.itemH {
				t.Fatalf("contentH = %d, want %d (必须恒为 N*itemH, 否则滚动条长度会错)",
					w.contentH, c.total*c.itemH)
			}
		})
	}
}

// TestVlistSpacers 垫片高度: 上下之和 + 物化行高 = 内容总高。
func TestVlistSpacers(t *testing.T) {
	w := vlistCompute(500, 100, 10, 2, 1000) // [48, 62)
	top, bottom := vlistSpacers(w, 10)
	if top != 480 {
		t.Fatalf("top 垫片 = %d, want 480 (first*itemH)", top)
	}
	if bottom != 9380 {
		t.Fatalf("bottom 垫片 = %d, want 9380 ((total-last)*itemH)", bottom)
	}
	materialized := (w.last - w.first) * 10
	if top+materialized+bottom != w.contentH {
		t.Fatalf("垫片 %d + 物化 %d + 垫片 %d = %d, want contentH %d",
			top, materialized, bottom, top+materialized+bottom, w.contentH)
	}
}

// TestVlistSameWindow 窗口没换行 ⇒ 不该重建 (热路径判据)。
//
// 判据是**行区间**而不是像素位移: 位移跨过行边界时窗口是真的变了 (可见末行
// 多出一行), 那时重建才对。所以这里用"完全不跨界"的位移。
func TestVlistSameWindow(t *testing.T) {
	// 起点选在**行内部** (105 落在第 5 行内): 小位移不会跨边界。
	a := vlistCompute(105, 200, 20, 2, 1000) // 可见 5..15, 窗口 [3, 18)
	if a.first != 3 || a.last != 18 {
		t.Fatalf("前置条件: a = [%d, %d), want [3, 18)", a.first, a.last)
	}
	// 105 → 108: 底边都在第 15 行内 (305..308 < 320), 可见末行不变
	b := vlistCompute(108, 200, 20, 2, 1000)
	if !vlistSameWindow(a, b) {
		t.Fatalf("挪 3px 未跨行: 窗口 %v → %v 不该变 (变了就会每像素重建所有行)", a, b)
	}
	// 挪过一整行: 窗口必须变 (否则滚下去内容不更新)
	c := vlistCompute(105+20*8, 200, 20, 2, 1000)
	if vlistSameWindow(a, c) {
		t.Fatalf("挪 8 行后窗口应该变 (a=%v c=%v)", a, c)
	}
}

// ===== 端到端: 十万行只物化可见区间 =====

// TestVlistDemoMaterializesWindow 是虚拟化的核心断言:
// 十万行只物化十几行, 内容总高仍是 N*itemH (滚动条不变), 滚动后窗口平移。
func TestVlistDemoMaterializesWindow(t *testing.T) {
	const N = 100000
	const rowH = 28

	runDemoSteps(t, "vlist_demo.js", []func(*GuiNode, *fakeSurface){
		func(root *GuiNode, fake *fakeSurface) {
			sc := findFirstWhere(root, func(n *GuiNode) bool {
				if n.Tag != "scroll" {
					return false
				}
				v, ok := n.Props["vlist"]
				return ok && v != nil && objectIsTruthy(v)
			})
			if sc == nil {
				t.Fatalf("没找到带 vlist 的 scroll")
			}
			// ① 内容总高必须是全量的 N*itemH —— 滚动条长度/行程全靠它
			if want := N * rowH; sc.contentH != want {
				t.Fatalf("contentH = %d, want %d (虚拟化不能改变内容总高)", sc.contentH, want)
			}
			// ② 物化行数: 视口 ~300px / 28 ≈ 11 行, 加 buffer 与垫片后
			//    也应该是十几行 —— 绝不能是十万。
			rows := countTag(root, "row")
			if rows > 40 {
				t.Fatalf("物化了 %d 行, 远超可见区间 (视口 300/28 ≈ 11 行) —— 虚拟化没生效", rows)
			}
			if rows < 8 {
				t.Fatalf("只物化了 %d 行, 少于可见区 (视口 300/28 ≈ 11 行) —— 会露出空白", rows)
			}
			t.Logf("十万行 → 物化 %d 行", rows)
		},
		// 滚到第 50000 行的位置后: 窗口必须整体平移 (内容跟着变)
		func(root *GuiNode, fake *fakeSurface) {
			sc := findFirstWhere(root, func(n *GuiNode) bool { return n.Tag == "scroll" })
			if sc == nil {
				t.Fatalf("没找到 scroll")
			}
			sc.scrollBy(0, 50000*rowH)
			// 手工跑一次布局 (正常路径由 Pump 里的重绘驱动, 这里直接驱动更确定)
			Layout(root, 600, 600)
			rows := countTag(root, "row")
			if rows > 40 {
				t.Fatalf("滚动后物化了 %d 行 —— 窗口没被限制住", rows)
			}
			if rows == 0 {
				t.Fatalf("滚动后一行都没物化 —— 滚下去会是空白")
			}
			t.Logf("滚到第 50000 行 → 物化 %d 行", rows)
		},
	})
}

// objectIsTruthy 判断 prop 值是否为真 (vlist 开关可能是布尔或数字)。
func objectIsTruthy(v object.Value) bool {
	switch x := v.(type) {
	case *object.Boolean:
		return x.Value
	case *object.Number:
		return x.Value != 0
	case *object.Null, *object.Undefined:
		return false
	}
	return true
}

// TestVlistFirstBuildIsBounded 钉住**首帧建树**也是有界的。
//
// 为什么单列一条: 上面那条端到端断言 (物化行数) 只能证明"最终树是对的" ——
// 而虚拟化写歪了最常见的样子恰恰是"先把 N 行全建出来、再销毁多余的": 树上最终
// 只有十几行, 断言通过, 但首帧已经付了 N 行的代价, 用户开页面照样卡
// (2026-10-01 实测: 十万行首帧 2s, 物化显示 13 行)。
//
// 所以这里数的是**渲染函数被调用了多少次** —— 它无法被后续销毁掩盖。
// 期望: 首帧只建 vlistPendingRows (64) 行上下, 与 N 无关。
func TestVlistFirstBuildIsBounded(t *testing.T) {
	const N = 20000

	src, err := os.ReadFile(filepath.Join("..", "testdata", "vlist_bounds.js"))
	if err != nil {
		t.Fatalf("读取脚本: %v", err)
	}
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(string(src))
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	appMu.Lock()
	root := activeApp.root
	appMu.Unlock()
	if root == nil {
		t.Fatal("脚本没有建立窗口")
	}

	// ① 首帧 (还没布局): 建树阶段就该只物化待物化区间
	before := readCounter(t, v, "rowCalls")
	if before > 200 {
		t.Fatalf("首帧建树调用了渲染函数 %d 次 (总量 %d) —— 首帧退化成全量了, "+
			"虚拟化只省了每帧布局、没省掉最贵的那块", before, N)
	}
	if before == 0 {
		t.Fatal("首帧一行都没建 —— 列表会空白")
	}

	// ② 布局后仍是可见区间 (精确窗口接管)
	Layout(root, 600, 600)
	afterLayout := countTag(root, "row")
	if afterLayout > 40 {
		t.Fatalf("布局后物化了 %d 行, 远超可见区间 —— 窗口没被限制住", afterLayout)
	}
	if afterLayout < 8 {
		t.Fatalf("布局后只物化了 %d 行, 少于可见区 —— 会露出空白", afterLayout)
	}
	t.Logf("%d 行: 首帧渲染调用 %d 次, 布局后物化 %d 行", N, before, afterLayout)
}

// readCounter 读脚本里 stats.<name> 的计数。
//
// 用普通对象而不是 `globalThis.x++`: 后者在本机算出 NaN (实测, 已记看板)。
func readCounter(t *testing.T, v *vm.VM, name string) int {
	t.Helper()
	val, ok := v.Globals().Get("__stats")
	if !ok {
		t.Fatalf("脚本没有导出 __stats")
	}
	obj, ok := val.(*object.Object)
	if !ok {
		t.Fatalf("__stats 不是对象, 是 %T", val)
	}
	desc, ok := obj.Properties[name]
	if !ok || desc.Value == nil {
		t.Fatalf("__stats.%s 不存在", name)
	}
	num, ok := desc.Value.(*object.Number)
	if !ok {
		t.Fatalf("__stats.%s 不是数字, 是 %T", name, desc.Value)
	}
	return int(num.Value)
}

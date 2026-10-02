package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== M4 断点系统测试 =====
//
// 分三层:
//   - 纯函数: breakpointNameAt / breakpointThreshold 的阈值边界 (599/600/
//     839/840/1199/1200) —— 这是最容易写成"差一"的地方;
//   - 模块函数: above/below/between/matchBreakpoint 的取值语义;
//   - 表管理: setBreakpoints 生效 / resetBreakpoints 复位 / 非法输入不清表。

// mountSizedWindow 挂一个指定客户区尺寸的假窗口 (断点按窗口宽度算, 所以尺寸
// 必须可控)。断点表是包级状态, 用例之间用 resetBreakpoints 隔离。
func mountSizedWindow(t *testing.T, w, h int) *Window {
	t.Helper()
	resetBreakpointsForTest()
	t.Cleanup(resetBreakpointsForTest)

	fs := newFakeSurface()
	fs.w, fs.h = w, h
	SetDefaultFactory(&fakeFactory{fs})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	root := &GuiNode{Tag: "column", Props: map[string]object.Value{}}
	win, err := Mount(root, WindowConfig{Title: "bp", Width: w, Height: h})
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if got := int(windowWidthDP(win)); got != w {
		t.Fatalf("窗口宽度 dp = %d, want %d (假屏 scale=1)", got, w)
	}
	return win
}

// TestBreakpointThresholdBoundaries 钉住默认表在 599/600/839/840/1199/1200
// 六个边界上的归属 —— 断点的"差一"只会让某一段宽度静默用了隔壁档的布局。
func TestBreakpointThresholdBoundaries(t *testing.T) {
	table := defaultBreakpointTable
	cases := []struct {
		dp   float64
		want string
	}{
		{0, "sm"}, {1, "sm"}, {599, "sm"},
		{600, "md"}, {700, "md"}, {839, "md"},
		{840, "lg"}, {1000, "lg"}, {1199, "lg"},
		{1200, "xl"}, {4000, "xl"},
	}
	for _, c := range cases {
		if got := breakpointNameAt(c.dp, table); got != c.want {
			t.Errorf("dp=%.0f → %q, want %q", c.dp, got, c.want)
		}
	}
}

// TestBreakpointBelowAllThresholds 验证"低于所有下界"归到最小档 (自定义表可能
// 把最小档抬到 0 以上)。
func TestBreakpointBelowAllThresholds(t *testing.T) {
	table := []breakpointEntry{{"tiny", 100}, {"big", 900}}
	if got := breakpointNameAt(50, table); got != "tiny" {
		t.Fatalf("低于所有阈值的 dp 应归到最小档, 实际 %q", got)
	}
}

// TestBreakpointAboveBelowBetween 验证三个区间判定在窗口宽度上的行为。
func TestBreakpointAboveBelowBetween(t *testing.T) {
	win := mountSizedWindow(t, 700, 400) // md 档

	if !breakpointAbove("md", win) {
		t.Errorf("700dp 应在 md 之上")
	}
	if breakpointAbove("lg", win) {
		t.Errorf("700dp 不应在 lg 之上")
	}
	if !breakpointBelow("lg", win) {
		t.Errorf("700dp 应在 lg 之下")
	}
	if breakpointBelow("md", win) {
		t.Errorf("700dp 不应在 md 之下 (>= 才算)")
	}
	if !breakpointBetween("md", "lg", win) {
		t.Errorf("700dp 应落在 [md, lg) 区间")
	}
	if breakpointBetween("sm", "md", win) {
		t.Errorf("700dp 不应落在 [sm, md) 区间")
	}
	// 参数反序也成立 (内部交换)
	if !breakpointBetween("lg", "md", win) {
		t.Errorf("between 参数反序应等价")
	}
	// 未知档名 → false (不猜)
	if breakpointAbove("nope", win) || breakpointBelow("nope", win) || breakpointBetween("md", "nope", win) {
		t.Errorf("未知档名应一律 false")
	}
}

// TestBreakpointAtExactThreshold 验证"从上往下走"到精确阈值时的翻转点。
func TestBreakpointAtExactThreshold(t *testing.T) {
	// 839 → md; 840 → lg (下界语义, 与默认表 lg:840 一致)
	if got := breakpointNameAt(839, defaultBreakpointTable); got != "md" {
		t.Fatalf("839 → %q, want md", got)
	}
	win := mountSizedWindow(t, 840, 400)
	if got := currentBreakpointName(win); got != "lg" {
		t.Fatalf("840dp 窗口应命中 lg, 实际 %q", got)
	}
	if !breakpointAbove("lg", win) {
		t.Fatalf("840dp 应恰好在 lg 之上 (>= 下界)")
	}
}

// TestMatchBreakpointValue 验证 matchBreakpoint 返回命中档的值。
func TestMatchBreakpointValue(t *testing.T) {
	win := mountSizedWindow(t, 700, 400) // md

	// 表里有 sm/md/lg/xl 的值: 700dp 命中 md
	o := object.NewObject()
	o.SetProperty("sm", object.NewString("narrow"))
	o.SetProperty("md", object.NewString("mid"))
	o.SetProperty("lg", object.NewString("wide"))
	if got := breakpointMatch(o, win); object.ToString(got) != "mid" {
		t.Fatalf("700dp 应命中 md, 实际 %q", object.ToString(got))
	}

	// 只提供 sm: 700dp 高于 sm, 仍应命中 sm (下界 <= dp 中最大)
	only := object.NewObject()
	only.SetProperty("sm", object.NewString("base"))
	if got := breakpointMatch(only, win); object.ToString(got) != "base" {
		t.Fatalf("只给 sm 时应回落到 sm, 实际 %q", object.ToString(got))
	}

	// 只提供 lg (600 窗口在 lg 之下): 无命中 → undefined
	narrow := mountSizedWindow(t, 600, 400)
	lgOnly := object.NewObject()
	lgOnly.SetProperty("lg", object.NewString("wide"))
	if got := breakpointMatch(lgOnly, narrow); got != object.UndefinedSingleton {
		t.Fatalf("无命中档应返回 undefined, 实际 %v", got)
	}
}

// TestSetBreakpointsAndReset 验证 setBreakpoints 生效、resetBreakpoints 复位,
// 以及非法输入不清空当前表。
func TestSetBreakpointsAndReset(t *testing.T) {
	resetBreakpointsForTest()
	t.Cleanup(resetBreakpointsForTest)

	// 默认表
	if got := breakpointTableSnapshot(); len(got) != 4 || got[0].name != "sm" {
		t.Fatalf("默认表异常: %+v", got)
	}

	// 覆盖: 两档 + 自定义阈值
	o := object.NewObject()
	o.SetProperty("phone", object.NewNumber(0))
	o.SetProperty("desk", object.NewNumber(500))
	jsSetBreakpoints(o)

	got := breakpointTableSnapshot()
	if len(got) != 2 || got[0].name != "phone" || got[1].name != "desk" || got[1].dp != 500 {
		t.Fatalf("setBreakpoints 未生效: %+v", got)
	}
	// 覆盖后 700dp → desk
	if name := breakpointNameAt(700, got); name != "desk" {
		t.Fatalf("自定义表 700dp 应命中 desk, 实际 %q", name)
	}

	// 非法输入 (全非数字): 保持原表, 不清空
	bad := object.NewObject()
	bad.SetProperty("a", object.NewString("x"))
	jsSetBreakpoints(bad)
	if got := breakpointTableSnapshot(); len(got) != 2 || got[0].name != "phone" {
		t.Fatalf("非法输入不该清空断点表: %+v", got)
	}

	// 复位
	jsResetBreakpoints()
	if got := breakpointTableSnapshot(); len(got) != 4 || got[3].name != "xl" {
		t.Fatalf("resetBreakpoints 未复位: %+v", got)
	}
}

// TestSetBreakpointsSortsByThreshold 验证乱序输入按 dp 排序 (求值依赖顺序)。
func TestSetBreakpointsSortsByThreshold(t *testing.T) {
	resetBreakpointsForTest()
	t.Cleanup(resetBreakpointsForTest)

	o := object.NewObject()
	o.SetProperty("xl", object.NewNumber(1200))
	o.SetProperty("sm", object.NewNumber(0))
	o.SetProperty("lg", object.NewNumber(840))
	o.SetProperty("md", object.NewNumber(600))
	jsSetBreakpoints(o)

	got := breakpointTableSnapshot()
	want := []string{"sm", "md", "lg", "xl"}
	for i, n := range want {
		if got[i].name != n {
			t.Fatalf("排序错误: %+v", got)
		}
	}
}

// TestBreakpointConsistentWithSizeClass 说明性断言: 默认断点的阈值数值与
// deriveSizeClasses 的 600/840 完全一致 (不是两套阈值)。
func TestBreakpointConsistentWithSizeClass(t *testing.T) {
	md, _ := breakpointThreshold("md", defaultBreakpointTable)
	lg, _ := breakpointThreshold("lg", defaultBreakpointTable)
	if md != 600 || lg != 840 {
		t.Fatalf("默认断点阈值应与尺寸类一致 (600/840), 实际 md=%.0f lg=%.0f", md, lg)
	}
	// 尺寸类的 600 / 840 分界 (通过 deriveSizeClasses 间接确认)
	if wc, _ := deriveSizeClasses(600, 400, 1); wc != SizeMedium {
		t.Fatalf("600px@1x 尺寸类应为 medium, 实际 %q", wc)
	}
	if wc, _ := deriveSizeClasses(840, 400, 1); wc != SizeMedium {
		t.Fatalf("840px@1x 尺寸类应为 medium, 实际 %q", wc)
	}
}

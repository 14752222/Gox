package gfx

import (
	"strings"
	"testing"

	"github.com/14752222/Gox/vm"
)

// TestReportKeyboardHeightReachesUseKeyboardHeight 回归 (2026-10-02, Android 模拟器实测):
// 宿主 ReportKeyboardHeight(nil, 883) 之后, 脚本侧 useKeyboardHeight() 必须读到
// 883 —— 真机上 nativeSetKeyboard 日志打了 883, 画面上 "键盘高(px)" 却停在 0。
// 本用例把"上报 → 存储 → 版本信号 → 订阅 effect"整条链钉在内核层:
//   - 若这里读不到 883, 是内核上报/存储/通知的 bug;
//   - 若这里能读到而真机不行, 是宿主上报时机或线程的问题。
//
// **上报与断言都必须在泵轮次内做** (foldjs_test.go / slider_test.go 的同一条
// 纪律): EvalVM 返回后 currentVM 已恢复为 nil, 泵外调 ReportViewport 的话,
// notifyViewportChanged → solid effect 重跑里的脚本闭包会被回调桥静默丢弃
// (vm.go 回调桥的 currentVM==nil 卫语句), 测出假阴性。
func TestReportKeyboardHeightReachesUseKeyboardHeight(t *testing.T) {
	resetViewportStateForTest()
	t.Cleanup(resetViewportStateForTest)

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { useKeyboardHeight, useInsets } from "gx/viewport";
		import { h, render } from "gx/gfx";
		render(
			h("window", { title: "t" },
				h("column", null,
					h("text", null, () => "kb=" + useKeyboardHeight()),
					h("text", null, () => "bottom=" + useInsets().bottom),
				)
			)
		);
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	_ = v
	appMu.Lock()
	a := activeApp
	root := a.root
	appMu.Unlock()

	var kbText, insText *GuiNode
	var walk func(n *GuiNode)
	walk = func(n *GuiNode) {
		if n.Tag == "#text" {
			if strings.HasPrefix(n.Text, "kb=") && kbText == nil {
				kbText = n
			}
			if strings.HasPrefix(n.Text, "bottom=") && insText == nil {
				insText = n
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	if kbText == nil || insText == nil {
		t.Fatalf("没找到 kb=/bottom= 文本节点")
	}
	if !strings.Contains(kbText.Text, "kb=0") {
		t.Fatalf("初始 kb 文本 = %q, want kb=0", kbText.Text)
	}

	var steps []func()
	// 第 ① 段: upsert 本身 —— 安全区与键盘分两次报, 两者都要在。
	// notifyViewportChanged 是同步通知, 但必须在泵轮次内调 (见文件头纪律)。
	steps = append(steps, func() {
		ReportInsets(nil, Insets{Bottom: 63})
		ReportKeyboardHeight(nil, 883)
		if got := viewportResolved(nil).Keyboard; got != 883 {
			t.Errorf("viewportResolved(nil).Keyboard = %d, want 883 (存储层丢了)", got)
		}
	})
	// 下一轮断言文本 —— 上报那一轮里 effect 已同步重跑, 这里只留一个轮次
	// 余量, 与"断言在泵内"的既有纪律对齐。
	steps = append(steps, func() {
		if !strings.Contains(kbText.Text, "kb=883") {
			t.Errorf("键盘上报后 kb 文本 = %q, want kb=883 (版本信号没通知到订阅者?)", kbText.Text)
		}
		if !strings.Contains(insText.Text, "bottom=63") {
			t.Errorf("insets 上报后 bottom 文本 = %q, want bottom=63", insText.Text)
		}
	})

	// 第 ② 段 (2026-10-02 真机回归): 宿主的真实顺序是"**先报键盘**, 键盘弹起后
	// 系统还会再分发一次 insets", 而且 insets 一直在动 (导航栏/手势条)。
	// 这一趟里安全区通道**不得**碰键盘高度 —— 早先这里用 ReportViewport
	// (patchAll) 把 883 清成了 0, 界面上"键盘高(px)"于是永远是 0。
	// 旧用例为什么没抓住: 它只覆盖了上面那段"先 insets 后键盘", 恰好绕开了这个
	// 组合 (见 gfx.ReportInsets 的注释)。
	steps = append(steps, func() {
		ReportKeyboardHeight(nil, 883)
		ReportInsets(nil, Insets{Bottom: 63})
		got := viewportResolved(nil)
		if got.Keyboard != 883 {
			t.Errorf("insets 再分发后 Keyboard = %d, want 883 (安全区通道清掉了键盘高度)", got.Keyboard)
		}
		if got.Insets.Bottom != 63 {
			t.Errorf("insets 再分发后 Insets.Bottom = %d, want 63", got.Insets.Bottom)
		}
	})
	steps = append(steps, func() {
		if !strings.Contains(kbText.Text, "kb=883") {
			t.Errorf("insets 再分发后 kb 文本 = %q, want kb=883 (键盘高度被安全区上报清掉了)", kbText.Text)
		}
		if !strings.Contains(insText.Text, "bottom=63") {
			t.Errorf("insets 再分发后 bottom 文本 = %q, want bottom=63", insText.Text)
		}
	})
	runPumpSteps(t, v, fake, steps)
}

// TestReportSizeClassesKeepsInsetsAndKeyboard 回归 (2026-10-02, Android 模拟器实测):
// 折叠宿主报完姿态与保留区之后还要补一份尺寸类, 那份补报**不得**动安全区与键盘。
//
// 真机顺序 (MainActivity.onSurfaceSize): nativeInit → reportInsets(b=63) →
// displayFold.start() → WindowLayoutTracker 回调 → gfx/mobile.ReportDisplayFold
// → 补报尺寸类。早先那里走 gfx.ReportViewport (patchAll), 于是 Insets 被清成 0,
// 页面上 `useInsets().bottom` 恒为 0, 直到下一次 insets 分发 (弹一次键盘) 才恢复。
// 与 TestReportKeyboardHeightReachesUseKeyboardHeight 是同一枚硬币的两面。
func TestReportSizeClassesKeepsInsetsAndKeyboard(t *testing.T) {
	resetViewportStateForTest()
	t.Cleanup(resetViewportStateForTest)

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { useKeyboardHeight, useInsets, useViewport } from "gx/viewport";
		import { h, render } from "gx/gfx";
		render(
			h("window", { title: "t" },
				h("column", null,
					h("text", null, () => "kb=" + useKeyboardHeight()),
					h("text", null, () => "bottom=" + useInsets().bottom),
					h("text", null, () => "wc=" + useViewport().widthClass),
				)
			)
		);
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	_ = v
	appMu.Lock()
	a := activeApp
	root := a.root
	appMu.Unlock()

	var kbText, insText, wcText *GuiNode
	var walk func(n *GuiNode)
	walk = func(n *GuiNode) {
		if n.Tag == "#text" {
			switch {
			case strings.HasPrefix(n.Text, "kb=") && kbText == nil:
				kbText = n
			case strings.HasPrefix(n.Text, "bottom=") && insText == nil:
				insText = n
			case strings.HasPrefix(n.Text, "wc=") && wcText == nil:
				wcText = n
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	if kbText == nil || insText == nil || wcText == nil {
		t.Fatalf("没找到 kb=/bottom=/wc= 文本节点")
	}

	var steps []func()
	// 第 ① 段: 宿主 nativeInit 之后的两条补报 (安全区 + 键盘)。
	steps = append(steps, func() {
		ReportInsets(nil, Insets{Bottom: 63})
		ReportKeyboardHeight(nil, 883)
	})
	// 第 ② 段: 折叠回调补报尺寸类 —— 前两样都不许被它清掉。
	steps = append(steps, func() {
		ReportSizeClasses(nil, SizeMedium, SizeRegular)
		got := viewportResolved(nil)
		if got.Insets.Bottom != 63 {
			t.Errorf("补报尺寸类后 Insets.Bottom = %d, want 63 (尺寸类通道清掉了安全区)", got.Insets.Bottom)
		}
		if got.Keyboard != 883 {
			t.Errorf("补报尺寸类后 Keyboard = %d, want 883 (尺寸类通道清掉了键盘高度)", got.Keyboard)
		}
		if got.WidthClass != SizeMedium || got.HeightClass != SizeRegular {
			t.Errorf("补报尺寸类后 WidthClass=%q HeightClass=%q, want %q/%q",
				got.WidthClass, got.HeightClass, SizeMedium, SizeRegular)
		}
	})
	steps = append(steps, func() {
		if !strings.Contains(insText.Text, "bottom=63") {
			t.Errorf("补报尺寸类后 bottom 文本 = %q, want bottom=63", insText.Text)
		}
		if !strings.Contains(kbText.Text, "kb=883") {
			t.Errorf("补报尺寸类后 kb 文本 = %q, want kb=883", kbText.Text)
		}
		if !strings.Contains(wcText.Text, "wc="+SizeMedium) {
			t.Errorf("补报尺寸类后 wc 文本 = %q, want wc=%s", wcText.Text, SizeMedium)
		}
	})
	runPumpSteps(t, v, fake, steps)
}

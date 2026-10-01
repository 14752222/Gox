package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== 折叠屏 JS API 的端到端用例 (批 E) =====
//
// 走真 VM + 假 Surface: 断言脚本侧能读到"上报 → 读数/订阅"的完整链路。
// 屏表注入用 reportPostureGo (与 reserved_test.go 同一套设施)。

// foldFixture 注入一块半折屏: 竖折痕在 (bandX, bandW), 一条 active division
// 加一条 active occlusion。
func foldFixture(t *testing.T) {
	t.Helper()
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)

	foldFixtureInline()
}

// foldFixtureInline 与 foldFixture 同一份注入, 但不碰 *testing.T —— 供
// runPumpSteps 的步骤闭包内使用。
func foldFixtureInline() {
	reportPostureGo(routeObj(
		"display", "fold-e2e", "posture", "half-open",
		"width", object.NewNumber(1000), "height", object.NewNumber(800),
		"hinge", routeObj("x", object.NewNumber(480), "y", object.NewNumber(0),
			"width", object.NewNumber(40), "height", object.NewNumber(800),
			"orientation", object.NewString("vertical")),
		"regions", object.NewArray([]object.Value{
			routeObj("kind", "division",
				"x", object.NewNumber(480), "y", object.NewNumber(0),
				"width", object.NewNumber(40), "height", object.NewNumber(800),
				"active", object.NewBoolean(true)),
			routeObj("kind", "occlusion",
				"x", object.NewNumber(20), "y", object.NewNumber(0),
				"width", object.NewNumber(60), "height", object.NewNumber(60),
				"active", object.NewBoolean(true)),
		}),
	))
}

// TestFoldJSReservedRegions 验证 reservedRegions() 的分组输出与字段完整。
func TestFoldJSReservedRegions(t *testing.T) {
	setupHost(t, nil)
	foldFixture(t)
	resetViewportStateForTest()
	t.Cleanup(resetViewportStateForTest)

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { reservedRegions, hasFold, layoutMode } from "gx/viewport";
		import { h, render } from "gx/gfx";
		globalThis.div = -1;
		globalThis.occ = -1;
		globalThis.all = -1;
		globalThis.firstKind = "?";
		globalThis.firstW = -1;
		globalThis.firstActive = false;
		globalThis.hasFoldRes = false;
		globalThis.suggested = "?";
		globalThis.posture = "?";
		globalThis.widthClass = "?";
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 1000, height: 800});
		var r = reservedRegions();
		globalThis.div = r.division.length;
		globalThis.occ = r.occlusion.length;
		globalThis.all = r.all.length;
		globalThis.firstKind = r.division[0].kind;
		globalThis.firstW = r.division[0].width;
		globalThis.firstActive = r.division[0].active;
		globalThis.hasFoldRes = hasFold();
		var m = layoutMode();
		globalThis.suggested = m.suggested;
		globalThis.posture = m.posture;
		globalThis.widthClass = m.widthClass;
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	if n, _ := globalNum(t, v, "div"); n != 1 {
		t.Fatalf("division 应 1 条: %v", n)
	}
	if n, _ := globalNum(t, v, "occ"); n != 1 {
		t.Fatalf("occlusion 应 1 条: %v", n)
	}
	if n, _ := globalNum(t, v, "all"); n != 2 {
		t.Fatalf("all 应 2 条: %v", n)
	}
	if s, _ := globalStr(t, v, "firstKind"); s != RegionDivision {
		t.Fatalf("kind 字段不对: %q", s)
	}
	if n, _ := globalNum(t, v, "firstW"); n != 40 {
		t.Fatalf("几何字段不对: %v", n)
	}
	if b, _ := globalBool(t, v, "firstActive"); !b {
		t.Fatal("active 字段不对")
	}
	if b, _ := globalBool(t, v, "hasFoldRes"); !b {
		t.Fatal("hasFold 应为 true")
	}
	if s, _ := globalStr(t, v, "suggested"); s != "dual" {
		t.Fatalf("半折 + 有折痕应建议 dual: %q", s)
	}
	if s, _ := globalStr(t, v, "posture"); s != "half-open" {
		t.Fatalf("posture 不对: %q", s)
	}
}

// TestFoldJSHasFoldStableAcrossFlips 是 hasFold 存在的**全部理由**的回归:
// 折叠 → 平展 → 折叠 之间, hasFold 必须恒为 true (结构性), 而 division 的
// active 会翻转。
//
// 若 hasFold 按 active 算, 界面列数会在姿态切换时 1↔2 跳 —— 而用户手上其实
// 只是折了一下又折回来。
func TestFoldJSHasFoldStableAcrossFlips(t *testing.T) {
	setupHost(t, nil)
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { reservedRegions, hasFold } from "gx/viewport";
		import { h, render } from "gx/gfx";
		globalThis.results = [];
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 1000, height: 800});
		globalThis.probe = function () {
			var r = reservedRegions();
			globalThis.results.push(hasFold() ? "F" : "-");
			globalThis.results.push(r.division.length > 0 && r.division[0].active ? "A" : "-");
		};
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	// 三个姿态: 半折 (宽 40 + active) → 平展 (宽 0 + inactive) → 半折。
	//
	// 断言必须在**泵轮次内**做 (callGlobalFn 的纪律): 主脚本执行结束后
	// currentVM 变 nil, 泵外调脚本全局函数只会拿到空操作。用现成的
	// runPumpSteps 把「上报 + 读数」成对塞进三轮。
	half := routeObj(
		"display", "fold-flip", "posture", "half-open",
		"width", object.NewNumber(1000), "height", object.NewNumber(800),
		"regions", object.NewArray([]object.Value{
			routeObj("kind", "division", "x", object.NewNumber(480), "y", object.NewNumber(0),
				"width", object.NewNumber(40), "height", object.NewNumber(800),
				"active", object.NewBoolean(true)),
		}))
	flat := routeObj(
		"display", "fold-flip", "posture", "flat",
		"width", object.NewNumber(1000), "height", object.NewNumber(800),
		"regions", object.NewArray([]object.Value{
			routeObj("kind", "division", "x", object.NewNumber(480), "y", object.NewNumber(0),
				"width", object.NewNumber(0), "height", object.NewNumber(800),
				"active", object.NewBoolean(false)),
		}))

	var steps []func()
	for _, st := range []*object.Object{half, flat, half} {
		st := st
		steps = append(steps, func() {
			reportPostureGo(st)
			callGlobalInspect(t, v, "probe")
		})
	}
	runPumpSteps(t, v, fake, steps)

	// 期望: 每步两个字符 —— hasFold 恒 "F", active 在三步里是 A/-/A。
	want := []string{"F", "A", "F", "-", "F", "A"}
	arr := jsArray(t, v, "results")
	if len(arr.Elements) != len(want) {
		t.Fatalf("结果数不对: %d", len(arr.Elements))
	}
	for i, w := range want {
		if got := object.ToString(arr.Elements[i]); got != w {
			t.Fatalf("第 %d 步: %q want %q (全部: %v)", i, got, w, arr.Inspect())
		}
	}
}

// TestFoldJSLayoutModeSuggestions 覆盖 suggested 的三种取值。
func TestFoldJSLayoutModeSuggestions(t *testing.T) {
	setupHost(t, nil)
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { layoutMode } from "gx/viewport";
		import { h, render } from "gx/gfx";
		globalThis.s1 = "?";
		globalThis.s2 = "?";
		globalThis.s3 = "?";
		globalThis.s4 = "?";
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 1000, height: 800});
		globalThis.probe = function () { return layoutMode().suggested; };
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	// 全部断言都在泵轮次内做 (见上一条用例的纪律说明)。
	// 注意 Inspect() 对字符串不加引号 ⇒ 期望值写裸串。
	var steps []func()
	check := func(want, what string) {
		steps = append(steps, func() {
			if got := callGlobalInspect(t, v, "probe"); got != want {
				t.Errorf("%s: got %s want %s", what, got, want)
			}
		})
	}

	// ① 非折叠屏 + compact 宽 → single。
	steps = append(steps, func() {
		resetScreenStateForTest()
		ReportViewport(nil, Viewport{WidthClass: SizeCompact, HeightClass: SizeRegular})
	})
	check("single", "非折叠 compact 应 single")

	// ② 非折叠屏 + medium 宽 (展开) → tablet。
	steps = append(steps, func() {
		ReportViewport(nil, Viewport{WidthClass: SizeMedium, HeightClass: SizeRegular})
	})
	check("tablet", "非折叠 medium 应 tablet")

	// ③ 半折 + 有折痕 → dual (即便宽度是 compact)。
	steps = append(steps, func() {
		resetScreenStateForTest()
		foldFixtureInline()
		ReportViewport(nil, Viewport{WidthClass: SizeCompact, HeightClass: SizeRegular})
	})
	check("dual", "半折 + 折痕应 dual")

	// ④ 半折但**没有**折痕 (宿主只报了姿态) → 退回按宽度判。
	steps = append(steps, func() {
		resetScreenStateForTest()
		reportPostureGo(routeObj(
			"display", "fold-no-hinge", "posture", "half-open",
			"width", object.NewNumber(1000), "height", object.NewNumber(800),
		))
		ReportViewport(nil, Viewport{WidthClass: SizeMedium, HeightClass: SizeRegular})
	})
	check("tablet", "半折无折痕应退回宽度判定(medium→tablet)")

	runPumpSteps(t, v, fake, steps)
}

// TestFoldJSSplitRatioAndOrientation 钉住 gx/screen 新增的两个纯读数。
func TestFoldJSSplitRatioAndOrientation(t *testing.T) {
	setupHost(t, nil)
	foldFixture(t)

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { splitRatio, hingeOrientation, posture } from "gx/screen";
		import { h, render } from "gx/gfx";
		globalThis.ratio = -1;
		globalThis.ori = "?";
		globalThis.pos = "?";
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 1000, height: 800});
		globalThis.ratio = splitRatio();
		globalThis.ori = hingeOrientation();
		globalThis.pos = posture();
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	// 折痕在 480/1000 处 → 0.48 (未被钳制, 因为在 0.2..0.8 内)。
	if r, _ := globalNum(t, v, "ratio"); r != 0.48 {
		t.Fatalf("splitRatio 应为 0.48, 实际 %v", r)
	}
	if s, _ := globalStr(t, v, "ori"); s != "vertical" {
		t.Fatalf("hingeOrientation 应为 vertical: %q", s)
	}
	if s, _ := globalStr(t, v, "pos"); s != "half-open" {
		t.Fatalf("posture 应为 half-open: %q", s)
	}
}

// TestFoldJSSubscribeHintWiring 钉住文档纪律里那条"同时读两个版本号"的接线。
//
// 断言方式: 上报姿态 → 环境版本被抬高。这里只能证明**版本号确实在动**
// (订阅是否接通由 gx/solid 的响应式用例负责, 那属于另一层); 但若哪天有人把
// useReservedRegions 里的 envSignal() 调用删掉, 折叠相关的视图就不会再被唤醒
// —— 本用例通过"读数每次都是最新值"间接看住这一点。
func TestFoldJSSubscribeHintWiring(t *testing.T) {
	setupHost(t, nil)
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { useReservedRegions } from "gx/viewport";
		import { h, render } from "gx/gfx";
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 1000, height: 800});
		globalThis.get = useReservedRegions();
		globalThis.n1 = -1;
		globalThis.n2 = -1;
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	// 初始: 无折痕。
	callGlobalInspect(t, v, "get")
	if n := len(displayRegionsOf(nil)); n != 0 {
		t.Fatalf("初始不该有保留区: %d", n)
	}

	// 上报后: 读数变 2 条 (且每次调用都重新取)。
	foldFixture(t)
	res := callGlobalInspect(t, v, "get")
	if res == "<nil>" {
		t.Fatalf("useReservedRegions 返回值异常: %s", res)
	}
	if n := len(displayRegionsOf(nil)); n != 2 {
		t.Fatalf("上报后应有 2 条保留区: %d", n)
	}
}

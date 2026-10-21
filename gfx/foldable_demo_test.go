package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== foldable_demo (testdata/foldable_demo.js) 的形态矩阵回归 =====
//
// demo 顶栏 4 个形态按钮各对应一次 reportPosture 载荷 (模拟宿主
// ReportDisplayFold)。这里用**同一段脚本里的 4 次上报**回放那份矩阵,
// 断言每档的 posture / suggested / 保留区分组读数与 demo 注释宣称的一致 ——
// demo 脚本若与内核读数语义脱节, 这组测试先红。
//
// 为什么四档放一个 VM 里跑: 每次 EvalVM 都是全新全局环境, 跨 VM 读不到上一档
// 的上报状态, 而 VM 没有对外"再执行一段源码"的入口。四档连做也正好对应
// 桌面上连点 4 个按钮的时序 (X11 桌面手测见 testdata/foldable_demo.js 头注释)。

// demoSnap 是一档形态的快照 (与 demo 徽章/指示条读取的字段同源)。
type demoSnap struct {
	Posture   string
	Suggested string
	Div       int
	Occ       int
}

// TestFoldableDemoDeviceMatrix 回放 demo 的 4 档形态按钮载荷。
func TestFoldableDemoDeviceMatrix(t *testing.T) {
	setupHost(t, nil)
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)
	resetViewportStateForTest()
	t.Cleanup(resetViewportStateForTest)

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	// 脚本与 foldable_demo.js 的 setDevice 一一对应: 每档 = 一次
	// reportPosture (posture + 尺寸 + 折痕 + 保留区) 后立刻取读数。
	v, err := vm.EvalVM(`
		import { reservedRegions, layoutMode } from "gx/viewport";
		import { h, render } from "gx/gfx";
		import { reportPosture } from "gx/screen";

		globalThis.snap = [];
		globalThis.w = render(h("rect", {width: 10, height: 10}),
			{title: "T", width: 1280, height: 800});

		globalThis.setDevice = function (next) {
			if (next === "folded") {
				reportPosture({display: "fold-demo", posture: "folded", width: 480, height: 800,
					hinge: {x: 0, y: 396, width: 480, height: 8, orientation: "horizontal"},
					regions: [{kind: "division", x: 0, y: 396, width: 0, height: 8, active: false}]});
			} else if (next === "flat") {
				reportPosture({display: "fold-demo", posture: "flat", width: 600, height: 800,
					hinge: {x: 300, y: 0, width: 0, height: 800, orientation: "vertical"},
					regions: [{kind: "division", x: 300, y: 0, width: 0, height: 800, active: false}]});
			} else if (next === "half") {
				reportPosture({display: "fold-demo", posture: "half-open", width: 1000, height: 800,
					hinge: {x: 480, y: 0, width: 40, height: 800, orientation: "vertical"},
					regions: [{kind: "division", x: 480, y: 0, width: 40, height: 800, active: true},
					          {kind: "occlusion", x: 20, y: 20, width: 60, height: 60, active: true}]});
			} else {
				// 平板档与 demo 一样要同时把**窗口**放大 —— widthClass 取自窗口
				// 尺寸 (假 Surface 默认 400x300 = compact), 只报显示器宽度不会
				// 让 suggested 变成 tablet。
				reportPosture({display: "fold-demo", posture: "flat", width: 1280, height: 800,
					regions: []});
				globalThis.w.resize(1280, 800);
			}
			var m = layoutMode(), r = reservedRegions();
			globalThis.snap.push({posture: m.posture, suggested: m.suggested,
				div: r.division.length, occ: r.occlusion.length});
		};

		setDevice("half");
		setDevice("folded");
		setDevice("flat");
		setDevice("tablet");
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	got := readDemoSnaps(t, v)
	want := []demoSnap{
		{"half-open", "dual", 1, 1}, // 半折: 1000 宽 + active 竖折痕 → 双栏避让
		{"folded", "single", 1, 0},  // 折叠态: 480 宽, 折痕 inactive → 单栏
		{"flat", "single", 1, 0},    // 展开: 600 宽, 折痕 inactive → 单栏
		{"flat", "tablet", 0, 0},    // 平板: 1280 宽, 无折痕 → 平板布局
	}
	if len(got) != len(want) {
		t.Fatalf("快照数 = %d, want %d: %+v", len(got), len(want), got)
	}
	names := []string{"半折", "折叠态", "展开", "平板"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s 档读数 = %+v, want %+v", names[i], got[i], want[i])
		}
	}
}

// readDemoSnaps 把脚本里的 snap 数组读成 Go 结构。
func readDemoSnaps(t *testing.T, v *vm.VM) []demoSnap {
	t.Helper()
	val, ok := v.Globals().Get("snap")
	if !ok {
		t.Fatal("脚本没有 snap 全局")
	}
	arr, ok := val.(*object.Array)
	if !ok {
		t.Fatalf("snap 不是数组: %T", val)
	}
	out := make([]demoSnap, 0, len(arr.Elements))
	for _, e := range arr.Elements {
		o, ok := e.(*object.Object)
		if !ok {
			t.Fatalf("快照项不是对象: %T", e)
		}
		var s demoSnap
		if x, ok := o.GetProperty("posture"); ok {
			if str, ok := x.(*object.String); ok {
				s.Posture = str.Value
			}
		}
		if x, ok := o.GetProperty("suggested"); ok {
			if str, ok := x.(*object.String); ok {
				s.Suggested = str.Value
			}
		}
		if x, ok := o.GetProperty("div"); ok {
			if n, ok := x.(*object.Number); ok {
				s.Div = int(n.Value)
			}
		}
		if x, ok := o.GetProperty("occ"); ok {
			if n, ok := x.(*object.Number); ok {
				s.Occ = int(n.Value)
			}
		}
		out = append(out, s)
	}
	return out
}

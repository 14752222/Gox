package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== gx/dev (devtools 方案 A): 快照 schema + 基础设施 1-3 =====

// TestDevSnapshotShape 锁住快照的结构 —— 字段名是 API (agent_doc/gui-devtools-options.md
// §3.1: "快照的结构有专门用例锁住")。
func TestDevSnapshotShape(t *testing.T) {
	resetDevState()
	t.Cleanup(resetDevState)
	v, _ := evalUI(t, `
		import { devSnapshot } from "gx/dev";
		globalThis.g_snap = devSnapshot();
	`)
	val, _ := v.Globals().Get("g_snap")
	snap, ok := val.(*object.Object)
	if !ok {
		t.Fatalf("devSnapshot 返回 %s, want object", val.Inspect())
	}
	// 顶层五节
	for _, section := range []string{"frame", "imageCache", "glyphCache", "tree", "solid", "warnings"} {
		if _, ok := snap.GetProperty(section); !ok {
			t.Fatalf("快照缺 %s 节", section)
		}
	}
	assertNumProps(t, snap, "frame", []string{"count", "full", "partial", "fullRatio"})
	assertNumProps(t, snap, "imageCache", []string{"size", "cap", "hits", "misses", "evicts"})
	assertNumProps(t, snap, "glyphCache", []string{"size", "cap", "hits", "misses", "evicts"})
	assertNumProps(t, snap, "tree", []string{"windows", "nodes", "depth"})
	assertNumProps(t, snap, "solid", []string{"effects"})

	// warnings 是数组
	w, _ := snap.GetProperty("warnings")
	if _, ok := w.(*object.Array); !ok {
		t.Fatalf("warnings 应为数组, 得到 %s", w.Inspect())
	}
	// 缓存上限是常量事实 (imageCacheCap / glyph 1024), 顺带锁住
	if got := numProp(t, snap, "imageCache", "cap"); got != float64(imageCacheCap) {
		t.Fatalf("imageCache.cap = %v, want %d", got, imageCacheCap)
	}
	if got := numProp(t, snap, "glyphCache", "cap"); got != 1024 {
		t.Fatalf("glyphCache.cap = %v, want 1024", got)
	}
}

// TestDevWarnRingRecordsUnknownTag 警告环形缓冲 (基础设施 3): 未知标签警告
// 在 stderr 之外还留了副本, devSnapshot 能读到 (去重: 同标签只记一条)。
func TestDevWarnRingRecordsUnknownTag(t *testing.T) {
	resetDevState()
	t.Cleanup(resetDevState)
	v, _ := evalUI(t, `
		import { h, render } from "gx/gfx";
		import { devSnapshot } from "gx/dev";
		render(h("column", null, h("zz-not-a-tag", null), h("zz-not-a-tag", null)));
		globalThis.g_warns = devSnapshot().warnings;
	`)
	val, _ := v.Globals().Get("g_warns")
	arr, ok := val.(*object.Array)
	if !ok || len(arr.Elements) != 1 {
		t.Fatalf("同标签警告应只记 1 条, warnings = %v", val.Inspect())
	}
	entry, ok := arr.Elements[0].(*object.Object)
	if !ok {
		t.Fatalf("警告条目应是对象: %v", arr.Elements[0].Inspect())
	}
	if txt, _ := entry.GetProperty("text"); txt.Inspect() == "" {
		t.Fatalf("警告条目缺 text")
	}
	if at, _ := entry.GetProperty("at"); at.Inspect() == "" {
		t.Fatalf("警告条目缺 at (时间戳)")
	}
}

// TestDevFrameCounters 帧埋点 (基础设施 2): 首帧与 resize 走整帧路径,
// signal 驱动的属性更新走局部路径。
func TestDevFrameCounters(t *testing.T) {
	resetDevState()
	t.Cleanup(resetDevState)
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(`
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		const [n, setN] = createSignal(0);
		render(h("column", {gap: 6},
			h("rect", {width: () => 40 + n() * 2, height: 8, background: "#3355aa"}),
			h("text", {font: 14}, () => "n=" + n())));
		globalThis.bump = () => setN(n() + 1);
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	count, full, partial := devFrameSnapshot()
	if count < 1 || full < 1 {
		t.Fatalf("首帧应记为整帧: count=%d full=%d", count, full)
	}
	before := count

	runPumpSteps(t, v, fake, []func(){
		func() {
			// resize → 整帧 +1
			fake.w, fake.h = 500, 400
			fake.push(Event{Kind: EventResize, W: 500, H: 400})
		},
		func() { callGlobalFn(t, v, "bump") }, // signal → 局部 +1
		func() {
			count, full, partial = devFrameSnapshot()
			if count-before < 2 || full < 2 || partial < 1 {
				t.Fatalf("帧计数不完整: 两轮后 count=%d(+%d) full=%d partial=%d",
					count, count-before, full, partial)
			}
		},
	})
}

// ===== 断言辅助 =====

func assertNumProps(t *testing.T, snap *object.Object, section string, names []string) {
	t.Helper()
	sec, ok := snap.GetProperty(section)
	if !ok {
		t.Fatalf("快照缺 %s 节", section)
	}
	o, ok := sec.(*object.Object)
	if !ok {
		t.Fatalf("%s 应为对象", section)
	}
	for _, name := range names {
		v, ok := o.GetProperty(name)
		if !ok {
			t.Fatalf("%s.%s 缺失", section, name)
		}
		if _, ok := v.(*object.Number); !ok {
			t.Fatalf("%s.%s 应为数字, 得到 %s", section, name, v.Inspect())
		}
	}
}

func numProp(t *testing.T, snap *object.Object, section, name string) float64 {
	t.Helper()
	sec, _ := snap.GetProperty(section)
	o := sec.(*object.Object)
	v, _ := o.GetProperty(name)
	n, _ := v.(*object.Number)
	return n.Value
}

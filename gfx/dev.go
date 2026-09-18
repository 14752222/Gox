package gfx

import (
	"sync"

	"github.com/14752222/Gox/object"
)

// gx/dev: 开发期只读快照 (devtools 方案 A, 2026-09-19 拍板落地)。
//
//	import { devSnapshot } from "gx/dev";
//	const snap = devSnapshot();
//	// snap.frame       → { count, full, partial, fullRatio }
//	// snap.imageCache  → { size, cap, hits, misses, evicts }
//	// snap.glyphCache  → { size, cap, hits, misses, evicts }
//	// snap.tree        → { windows, nodes, depth }
//	// snap.solid       → { effects }   (gx/solid 的 devStats; 未注册时 effects=-1)
//	// snap.warnings    → [{ at, text }]  最近 N 条内核警告
//
// 设计要点 (gui-devtools-options.md §3.1):
//   - **快照是纯对象**: 一次调用返回整棵 JSON 形状数据, 构建在调用线程内联
//     完成 (脚本线程就是 GUI 线程, 与剪贴板"同步 API"同一论证)。
//   - **拉取式刷新**: 不推送, 面板自己 setInterval 拉取 (示例 1s), 观察者
//     效应控制在明处。**别用 requestAnimationFrame** (会和真实渲染抢帧)。
//   - **字段名是 API**: 结构有专门用例锁住 (TestDevSnapshotShape)。
//   - 生产零成本: 不 import 就是纯开销为零的死代码路径 (模块惰性构建)。
//   - 快照与 machine/gx 历史解耦: 只含静态树与统计 (拍板 2026-09-19)。

func init() {
	object.RegisterBuiltinModule("gx/dev", func() map[string]object.Value {
		return map[string]object.Value{
			"devSnapshot": object.NewBuiltin("devSnapshot", jsDevSnapshot),
		}
	})
}

// ===== 帧埋点 (devtools 基础设施 2) =====
//
// 只做单调计数 (整帧/局部分布), 不测 layout/draw 耗时: "为什么卡"先从帧数
// 与整帧率就能答一半, 耗时滑动平均等有真实需求再加 (避免为不存在的需求
// 预建架构)。只在 redraw 真正上屏的三个出口计数, "醒来但没重绘"不计。

var devFrames struct {
	mu      sync.Mutex
	count   int
	full    int
	partial int
}

// devFrameTick 记一帧 (full=true 表示整帧路径, 含 85% 面积退化)。
func devFrameTick(full bool) {
	devFrames.mu.Lock()
	devFrames.count++
	if full {
		devFrames.full++
	} else {
		devFrames.partial++
	}
	devFrames.mu.Unlock()
}

func devFrameSnapshot() (count, full, partial int) {
	devFrames.mu.Lock()
	defer devFrames.mu.Unlock()
	return devFrames.count, devFrames.full, devFrames.partial
}

// jsDevSnapshot 组装快照对象。
func jsDevSnapshot(args ...object.Value) object.Value {
	snap := object.NewObject()

	// 帧
	count, full, partial := devFrameSnapshot()
	frame := object.NewObject()
	frame.SetProperty("count", object.NewNumber(float64(count)))
	frame.SetProperty("full", object.NewNumber(float64(full)))
	frame.SetProperty("partial", object.NewNumber(float64(partial)))
	ratio := 0.0
	if count > 0 {
		ratio = float64(full) / float64(count)
	}
	frame.SetProperty("fullRatio", object.NewNumber(ratio))
	snap.SetProperty("frame", frame)

	// 缓存 (基础设施 1)
	snap.SetProperty("imageCache", cacheStatsObject(imageCache.stats()))
	snap.SetProperty("glyphCache", cacheStatsObject(glyphLRU.stats()))

	// 树统计: 全部窗口聚合 (nodes 含 #text; depth 取各窗口最大)
	windows, nodes, depth := treeStats()
	tree := object.NewObject()
	tree.SetProperty("windows", object.NewNumber(float64(windows)))
	tree.SetProperty("nodes", object.NewNumber(float64(nodes)))
	tree.SetProperty("depth", object.NewNumber(float64(depth)))
	snap.SetProperty("tree", tree)

	// solid: 经 gx/solid 的 devStats 取 (字段名不稳定, 不算公共承诺);
	// solid 未注册 (纯 Go 单测无 VM) 时给 -1 表示"不可用"。
	effects := -1.0
	if exports, ok := object.LookupBuiltinModule("gx/solid"); ok {
		if fn, ok := exports["devStats"]; ok && object.IsCallable(fn) {
			if res := object.CallFunction(fn, nil); res != nil {
				if o, ok := res.(*object.Object); ok {
					if v, ok := o.GetProperty("effects"); ok {
						if n, ok := v.(*object.Number); ok {
							effects = n.Value
						}
					}
				}
			}
		}
	}
	solid := object.NewObject()
	solid.SetProperty("effects", object.NewNumber(effects))
	snap.SetProperty("solid", solid)

	// 警告环形缓冲 (基础设施 3)
	entries := warnSnapshot()
	warnObjs := make([]object.Value, 0, len(entries))
	for _, w := range entries {
		wo := object.NewObject()
		wo.SetProperty("at", object.NewString(w.At.Format("15:04:05")))
		wo.SetProperty("text", object.NewString(w.Text))
		warnObjs = append(warnObjs, wo)
	}
	snap.SetProperty("warnings", object.NewArray(warnObjs))

	return snap
}

// cacheStatsObject 把缓存统计组装成快照子对象。
func cacheStatsObject(size, cap, hits, misses, evicts int) *object.Object {
	o := object.NewObject()
	o.SetProperty("size", object.NewNumber(float64(size)))
	o.SetProperty("cap", object.NewNumber(float64(cap)))
	o.SetProperty("hits", object.NewNumber(float64(hits)))
	o.SetProperty("misses", object.NewNumber(float64(misses)))
	o.SetProperty("evicts", object.NewNumber(float64(evicts)))
	return o
}

// treeStats 聚合全部存活窗口的树规模。调用发生在 GUI 线程 (devSnapshot 的
// 内联构建), 与 layout/draw 同一线程, 遍历无需额外锁。
func treeStats() (windows, nodes, depth int) {
	for _, a := range appsSnapshot() {
		windows++
		n, d := walkStats(a.rootNode(), 1)
		nodes += n
		if d > depth {
			depth = d
		}
	}
	return
}

func walkStats(n *GuiNode, depth int) (nodes, maxDepth int) {
	if n == nil {
		return 0, 0
	}
	nodes = 1
	maxDepth = depth
	for _, c := range n.Children {
		cn, cd := walkStats(c, depth+1)
		nodes += cn
		if cd > maxDepth {
			maxDepth = cd
		}
	}
	return
}

// resetDevState 清空帧计数与警告缓冲 (仅测试用; 缓存计数随实例生命周期,
// 不在此重置)。
func resetDevState() {
	devFrames.mu.Lock()
	devFrames.count, devFrames.full, devFrames.partial = 0, 0, 0
	devFrames.mu.Unlock()
	resetWarnRing()
}

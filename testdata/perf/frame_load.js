// perf/ 帧率与 GC 毛刺基准的压测脚本 (2026-10-02, M9 基准设施)。
//
// 形态: 一个"高频更新的长列表" —— ROWS 行, 每行一个独立 value signal。
// 每个 Go 侧"帧"调用一次 globalThis.__tick(): 更新 UPDATES_PER_FRAME 个
// 行的 value, 触发响应式 effect 标脏 → 真实 Layout + 脏区 Draw + 上屏。
//
// 为什么用"每行一个 signal"而不是改一个全局数字:
//   - 每行独立 signal 才是真实列表的更新形态 (只有变化的行重排/重绘);
//   - 一个全局 signal 会让整个列表每帧全量重建, 量到的是"最坏情况"而非
//     "高频更新"的常态。
//
// 为什么数值走 __ROWS__ / __UPDATES__ 占位符:
//   Go 侧 (perf/frame_gc_test.go) 读取本文件后替换成具体数字 —— 这样脚本是
//   一个**真实可读的文件** (check-imports.py 会扫它), 同时行数/更新量可被
//   基准在轻量/重压两档之间调参, 不用复制两份脚本。
import { h, render, createSignal } from "gox";

const ROWS = __ROWS__;
const UPDATES_PER_FRAME = __UPDATES__;

// 造 ROWS 行数据 + 每行一个 value signal。
// createSignal 返回 [getter, setter] 数组, 只取 getter (它自带 .set 方法)。
const rows = [];
for (let i = 0; i < ROWS; i++) rows.push({ id: i, label: "第 " + i + " 行" });
const cells = rows.map(() => { const [c] = createSignal(0); return c; });

// 帧计数本身也走 signal (列表顶部那行文本响应它), 保证每帧至少有 1 个更新。
const [tick, setTick] = createSignal(0);
let cursor = 0;

// Go 侧每帧调一次。返回帧号 (供调试; 基准不依赖返回值)。
globalThis.__tick = function () {
  const t = tick() + 1;
  setTick(t);
  // 轮转更新 UPDATES_PER_FRAME 行, 模拟"列表持续局部变化"。
  for (let k = 0; k < UPDATES_PER_FRAME; k++) {
    const idx = cursor % ROWS;
    cursor++;
    const c = cells[idx];
    c.set((c() + 1) % 100000);
  }
  return t;
};

// 建立真实元素树 (走 JSBuiltinH → 真节点); 窗口尺寸 640×480。
globalThis.__root = render(
  h("column", { gap: 2, padding: 8, background: "#ffffff" },
    h("text", { font: 12, color: "#333333" }, () => "frame " + tick()),
    h("view", { each: rows, key: "id" },
      (r, i) => h("row", { height: 16, padding: 2 },
        h("text", { font: 12, color: "#222222" }, () => r.label + " · " + cells[i]())
      )
    )
  ),
  { title: "perf-frame", width: 640, height: 480 }
);

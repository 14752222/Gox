// memdrift 的长跑负载（T3-3）。
//
// 形态：**一个窗口挂到底**，每轮 __churn() 整批换掉列表数据（key 全变 →
// 旧行卸载、新行挂载），再 __tick() 若干次驱动行内 signal 更新，Go 侧每帧
// 泵一次真实 Layout + 脏区 Draw + 上屏。
//
// 为什么不每轮新建窗口：常驻应用的窗口数量是常数，反复开关窗口是"热重载"
// 的形态，不是"挂一夜"的形态；而且窗口开关路径另有句柄泄漏（见
// docs/performance.md §11 已知盲区），混进来会让这条闸门测不到它本来要测的
// 东西 —— 帧循环与组件挂载的稳态漂移。窗口只在测试末尾关一次。
//
// 行数与更新量的占位符由 Go 侧替换（与 testdata/perf/frame_load.js 同款做法），
// 这样同一份脚本能按机器快慢调档，而不用维护多份副本。
import { h, render, createSignal } from "gox";

const ROWS = __ROWS__;
const UPDATES = __UPDATES__;

const [items, setItems] = createSignal([]);
const [tick, setTick] = createSignal(0);

// cells 与 items 同步重建：每行一个独立 signal，更新只脏化对应的行。
let cells = [];
let gen = 0;
let cursor = 0;

function build(g) {
  const rows = [];
  cells = [];
  for (let i = 0; i < ROWS; i++) {
    rows.push({ id: g * 1000003 + i, label: "第 " + i + " 行" });
    const [get] = createSignal(0);
    cells.push(get);
  }
  return rows;
}

// __churn：换一整批行。key 全部不同 → For 引擎销毁旧节点、挂载新节点，
// 这是"组件挂载"压力的来源。
globalThis.__churn = function () {
  gen++;
  setItems(build(gen));
  return gen;
};

// __tick：更新 UPDATES 个行内 signal（轮转），并推进表头计数。
globalThis.__tick = function () {
  setTick(tick() + 1);
  for (let k = 0; k < UPDATES; k++) {
    const idx = cursor % ROWS;
    cursor++;
    const c = cells[idx];
    if (c) c.set((c() + 1) % 100000);
  }
  return tick();
};

// __close：显式关窗（走 Window.Close 的 Post 路径，会摘掉窗口句柄登记）。
globalThis.__close = function () {
  __root.close();
};

globalThis.__root = render(
  h("column", { gap: 2, padding: 8, background: "#ffffff" },
    h("text", { font: 12, color: "#333333" }, () => "gen " + gen + " · frame " + tick()),
    h("view", { each: items, key: "id" },
      (r, i) => h("row", { height: 16, padding: 2 },
        h("text", { font: 12, color: "#222222" }, () => r.label + " · " + cells[i]())
      )
    )
  ),
  { title: "memdrift", width: 640, height: 480 }
);

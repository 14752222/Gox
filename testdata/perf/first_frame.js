// perf/ 冷启动到首帧基准 (T2-5) 的最小 GUI 脚本。
//
// 与 scripts/bench/coldstart_gui.js 的区别: 本脚本是**进程内口径** ——
// Go 侧 (coldstart_test.go) 自己驱动事件泵、用 perfSurface 记录上屏时刻,
// 脚本只负责"求值时建一个最小窗口", 不做首帧探测 (探测逻辑在 Go 侧,
// 直接读 perfSurface 的时间戳, 比轮询 devSnapshot 更精确、零偏置)。
//
// 窗口刻意最小 (一个 3 行的 column): 让"首帧"尽量只含解释器初始化、
// 模块装载、窗口挂载、首次 Layout/Draw/上屏, 不混入应用自身的建树成本。
import { h, render } from "gox";

globalThis.__root = render(
  h("column", { gap: 4, padding: 8 },
    h("text", { font: 14, color: "#222222" }, "coldstart"),
    h("text", { font: 12, color: "#555555" }, "first-frame probe"),
    h("text", { font: 12, color: "#888888" }, "gox")
  ),
  { title: "coldstart", width: 320, height: 200 }
);

// 冷启动到首帧口径的最小 GUI 脚本 (T2-5, 2026-10-28)。
//
// 口径: 进程 exec → 解释器初始化 + 本脚本求值 (建窗口) → **首个 gfx 帧真正上屏**
// → JS 侧探测到首帧 → 打一行标记并关窗退出。外层 harness (scripts/coldstart.sh)
// 用外部 wall time 计时, 因此测得的是"单文件分发用户双击 exe 到看见第一帧"的
// 进程级代理量 (含 ≤2ms 的探测/退出开销, 见 scripts/coldstart.sh 的口径说明)。
//
// 为什么用 gx/dev 的 devSnapshot().frame.count 而不是 requestAnimationFrame:
//   rAF 在内核里只是 16ms 的 setTimeout (gfx/render.go 的 jsRAF), 与真实上屏
//   无关, 拿它当"首帧"会把测量偏置成 16ms; devSnapshot().frame 只在 redraw
//   真正上屏的出口计数 (gfx/dev.go), 是脚本侧唯一与"像素真的出了"对齐的信号。
//   探测用 1ms interval 轮询, 首帧后 ≤1ms 内被发现。
//
// 窗口刻意最小 (一个 3 行的 column), 让"首帧"尽量只含引擎与渲染初始化,
// 不混入应用本身的建树成本 —— 那是应用代码的事, 不是运行时的。
import { h, render } from "gx/gfx";
import { devSnapshot } from "gx/dev";

const root = render(
  h("column", { gap: 4, padding: 8 },
    h("text", { font: 14, color: "#222222" }, "coldstart"),
    h("text", { font: 12, color: "#555555" }, "first-frame probe"),
    h("text", { font: 12, color: "#888888" }, "gox")
  ),
  { title: "coldstart", width: 320, height: 200 }
);

const poll = setInterval(() => {
  if (devSnapshot().frame.count > 0) {
    clearInterval(poll);
    console.log("FIRST_FRAME");
    root.close();
  }
}, 1);

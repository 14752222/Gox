// ============================================================================
// §2 内置模块的导入及使用方法
//
// 运行:  gox testdata/tutorial_modules.js
//
// gx/storage 的演示会真的落盘。想把数据根目录挪走(推荐, 免得污染真实的用户
// 配置目录), 先设环境变量 GOX_STORAGE_DIR:
//   GOX_STORAGE_DIR=./tutorial-storage gox testdata/tutorial_modules.js
//
// 两条互不重叠的注入路径:
//   A. **全局对象**  fs / path / http / fetch / process / stats / console /
//      obs / computed / ever / once / 各种定时器 —— 不用 import, 直接可用 (见 §1)
//   B. **ES 模块**  gx/* 与聚合入口 gox —— 必须写 import (§2 本文)
// ============================================================================

// ---- 2.1 import 的四种形态 (一律写在文件顶部) ------------------------------
import { h, render, createSignal, createMemo, createEffect } from "gox"; // ① 聚合入口: 所有 gx/* 导出的并集
import { untrack } from "gx/solid"; // ② 细分模块: 按需导入(库代码推荐这种)
import * as solid from "gx/solid"; // ③ 命名空间导入: 拿到整个导出对象
import * as gfx from "gx/gfx";
import * as view from "gx/view";
import * as router from "gx/router";
import * as screen from "gx/screen";
import * as dialog from "gx/dialog";
import * as storage from "gx/storage";
import * as dev from "gx/dev";
import describe, { VERSION, shout } from "./tutorial_util.js"; // ④ 相对路径: 本地模块(默认导出 + 命名导出)

console.log("== 2.1 导入形态 ==");
console.log("聚合入口  gox        -> h:", typeof h, "render:", typeof render, "createSignal:", typeof createSignal);
console.log("细分模块  gx/solid   -> untrack:", typeof untrack, "| 命名空间导出数:", Object.keys(solid).length);
console.log("命名空间  gx/router  -> 导出:", Object.keys(router).join(", "));
console.log("同一份实现?          createSignal === solid.createSignal →", createSignal === solid.createSignal);
console.log("本地模块  ./xxx.js   -> VERSION:", VERSION, "| shout:", shout("ok"), "| default:", describe("util"));

// ---- 2.2 各内置模块的导出清单 (只 import 用到的名字即可) -------------------
console.log("\n== 2.2 各内置模块的导出 ==");

// for-of 的绑定也可以直接解构 (`for (const [a, b] of pairs)`, 2026-09-24 起支持);
// 这里顺手用它把每项拆成 [模块名, 模块对象]。
const mods = [
  ["gx/solid", solid],
  ["gx/gfx", gfx],
  ["gx/view", view],
  ["gx/router", router],
  ["gx/screen", screen],
  ["gx/dialog", dialog],
  ["gx/storage", storage],
  ["gx/dev", dev],
];
for (const [name, mod] of mods) {
  console.log(name, "->", Object.keys(mod).sort().join(", "));
}

// ---- 2.3 gx/solid: 无界面也能用的信号 -------------------------------------
// 信号与渲染无关: 不挂窗口照样能建、能算、能订阅 —— 这也是"逻辑与界面分离"的落点。
console.log("\n== 2.3 gx/solid 无界面用法 ==");
const [count, setCount] = createSignal(2);
const doubled = createMemo(() => count() * 2); // 派生值: 惰性, 依赖变化只标脏

const seen = [];
createEffect(() => {
  seen.push(count()); // 立即执行一次, 之后依赖变化再跑
});
setCount(3); // 新值 → 通知
setCount(3); // 与当前值相同 (===) → 不通知
setCount((c) => c + 1); // setter 也接受"旧值 → 新值"的函数
console.log("effect 收到的值序列:", JSON.stringify(seen));
console.log("memo 惰性求值: count =", count(), "doubled =", doubled());

// untrack: 只想读一次、不想建立依赖时用它 (路由视图就靠它避免"页面里读 signal 就整页重建")
let reads = 0;
createEffect(() => {
  untrack(() => reads++);
  count();
});
setCount(10);
console.log("untrack 内的读取不建立依赖 → reads =", reads);

// 实测口径: 同一个 effect 里既读 signal 又读它的 memo —— 该 effect 每轮只跑一次。
// 它同时经两条路订阅了同一次变更 (直接订阅 signal + 经 memo 的 cell), 引擎按"一趟
// 通知"去重。(2026-09-24 之前这里是每轮两次, 计数类断言会多一倍。)
const [n, setN] = createSignal(1);
const nm = createMemo(() => n() * 10);
const both = [];
createEffect(() => both.push(n() + "|" + nm()));
setN(2);
console.log("同时读 signal 与 memo →", JSON.stringify(both), "(每轮一次)");

// ---- 2.4 gx/storage: 应用级持久化 -----------------------------------------
console.log("\n== 2.4 gx/storage 持久化 ==");
storage.setAppName("gox-tutorial-demo"); // 决定数据落在 <UserConfigDir>/Gox/<应用名>
storage.setStorage("theme", "dark"); // 值可以是任意可 JSON 序列化的数据
console.log("数据目录:", storage.appDataDir());
console.log("getStorage('theme') =", storage.getStorage("theme"));
// 读不存在的键返回 **undefined** —— getStorage 只认第一个参数(第二参数会被忽略),
// 想要默认值自己兜: 用空值合并 ?? 或 `=== undefined ? 默认值 : 读到的值`
console.log("getStorage('缺失') ?? 'light' =", storage.getStorage("缺失") ?? "light");
console.log("getStorageInfo().keys =", storage.getStorageInfo().keys.join(","));
storage.removeStorage("theme"); // 演示收尾: 把本应用名下的键清掉

// ---- 2.5 其它模块先看导出, 具体用法见对应示例 -----------------------------
console.log("\n== 2.5 其它模块 ==");
console.log("gx/screen.platform()      =", screen.platform()); // win32 / x11 / cocoa / headless
console.log("gx/dev.devSnapshot() 的键 =", Object.keys(dev.devSnapshot()).join(", "));
console.log("gx/dialog 的导出          =", Object.keys(dialog).sort().join(", "), "(需窗口, 见 §1 GUI 示例)");

console.log("\n用法约定: 库代码用细分模块(gx/xxx), 应用代码用聚合入口 gox —— 一行拿全常用 API。");

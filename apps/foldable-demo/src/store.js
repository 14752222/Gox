// 应用状态：折叠态上报 + 读数快照 + 事件日志。
//
// 桌面/无头宿主没有折叠姿态查询 API（Windows 没有 WinRT、X11 没有对应扩展），
// 框架的立场是**提供通道不猜姿态** —— 没人上报就恒为 flat。所以这个演示自己
// 扮演"宿主"：按钮点一下 = 往内核上报一次姿态，脚本侧读数随之变化。
// 真机上的上报者是平台壳工程（Android 的 DisplayManager / iOS 的 UITraitCollection），
// 脚本写法完全一样。
import { createSignal } from "gox";
import {
  posture,
  hinge,
  regions,
  splitRatio,
  hingeOrientation,
  reportPosture,
  resetDisplays,
} from "gx/screen";
import {
  reservedRegions,
  hasFold,
  layoutMode,
  widthClass,
  onViewportChange,
} from "gx/viewport";
import { logText, postureText } from "./lib/fold.js";

// ===== 形态：三种姿态 + 一个"非折叠"基线 =====
//
// 每个形态就是一次 reportPosture：屏幕尺寸、是否折叠设备、姿态、折痕。
// 竖折（竖屏左右折）折痕是竖条，横折（Tabletop）折痕是横条。
export const FORMS = [
  {
    key: "flat",
    label: "8 形态一键切换",
    name: "平展 1440×900",
    opts: {
      foldable: true,
      width: 1440,
      height: 900,
      posture: "flat",
      hinge: { x: 700, y: 0, w: 40, h: 900, orientation: "vertical" },
    },
  },
  {
    key: "half-open",
    name: "半折 竖 1440×900",
    opts: {
      foldable: true,
      width: 1440,
      height: 900,
      posture: "half-open",
      hinge: { x: 700, y: 0, w: 40, h: 900, orientation: "vertical" },
    },
  },
  {
    key: "folded",
    name: "完全折起 1440×900",
    opts: {
      foldable: true,
      width: 1440,
      height: 900,
      posture: "folded",
      hinge: { x: 700, y: 0, w: 40, h: 900, orientation: "vertical" },
    },
  },
  {
    key: "tabletop",
    name: "半折 横 900×1440",
    opts: {
      foldable: true,
      width: 900,
      height: 1440,
      posture: "half-open",
      hinge: { x: 0, y: 700, w: 900, h: 40, orientation: "horizontal" },
    },
  },
  {
    key: "plain",
    name: "非折叠 1440×900",
    opts: { foldable: false, width: 1440, height: 900, posture: "flat" },
  },
];

// ===== signal =====
const formP = createSignal("half-open");
const logP = createSignal([]);
const snapP = createSignal("");

export const form = formP[0];
export const log = logP[0];
export const snap = snapP[0];

function pushLog(line) {
  logP[1](log().concat([line]));
}

let win = null;

// 记录窗口句柄（main.js 在 render 之后调）—— 所有读数都按窗口取。
export function bindWindow(w) {
  win = w;
  applyForm("half-open");
}

function report(opts) {
  // 省略 display ⇒ 作用于当前窗口所在的那块屏。首次上报必须带
  // width / height（+ foldable），否则"窗口所在显示器"解析不到，
  // 之后的姿态读了也是空的 —— 这是最容易踩的一条。
  reportPosture(Object.assign({}, opts));
}

// applyForm 切到某个形态：上报姿态 + 记一条日志 + 立刻刷一次快照。
export function applyForm(key) {
  const f = FORMS.find((x) => x.key === key);
  if (!f) return;
  resetDisplays(); // 先清掉上一次的覆盖，避免形态叠加
  report(f.opts);
  formP[1](key);
  pushLog("形态 → " + f.name);
  refreshSnap();
}

// refreshSnap 把全部读数取一次拼成多行文本。
//
// 注意这里全是**纯读数**（posture() / hinge() / layoutMode() …）—— 它们不会
// 建立订阅，读一次就是"此刻的值"。要跟着环境自动更新，用的是下面 onViewportChange
// 注册的回调 + 把读数写进 signal。
export function refreshSnap() {
  const p = posture(win);
  const h = hinge(win);
  const r = reservedRegions(win);
  const m = layoutMode(win);
  const lines = [
    "posture()          = " + p + "   （" + postureText(p) + "）",
    "hingeOrientation() = " + hingeOrientation(win),
    "splitRatio()       = " + splitRatio(win).toFixed(3) + "   （钳 [0.2, 0.8]，无折痕退化 0.5）",
    "hasFold()          = " + hasFold(win) + "   （结构性：有折痕的机器恒 true）",
    "widthClass()       = " + widthClass(win) + "   （compact<600 / medium 600–840 / expanded>840）",
    "layoutMode().suggested = " + m.suggested + "   （只给建议，框架不改布局）",
    "reservedRegions()  = division " + r.division.length + " 段 / occlusion " + r.occlusion.length + " 处",
    "regions()          = " + regions(win).length + " 段",
  ];
  snapP[1](lines.join("\n"));
}

// 环境一变（上报姿态 / 转屏 / 窗口挪屏）就刷快照 —— 这是"响应式"的接线方式。
onViewportChange(() => refreshSnap());

export { refreshSnap as refresh, logText };

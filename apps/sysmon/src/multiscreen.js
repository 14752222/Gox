// 多屏模式：每块显示器一个独立窗口；单屏环境下"降级"为在屏幕外再开一个窗口，
// 用来验证多窗口生命周期（创建 / 显示 / 关闭）。
//
// 定位走窗口句柄的 moveTo（render 的窗口配置不解析 x/y/display，见 docs/multi-window.md）：
//   - 多显示器：把副窗挪到该屏坐标（虚拟桌面坐标，best-effort）；
//   - 单屏降级：挪到"工作区右缘之外"，作为"这是第二块屏"的占位演示。
import { h, render } from "gox";
import { screens } from "gx/screen";
import { colors, space, font } from "./theme.js";
import { refreshTopology, samples } from "./store.js";
import { Chart } from "./components/chart.js";

let opened = [];

const SecondaryBody = (p) => {
  // —— 别名到激活作用域（跨模块组件的响应式闭包引用模块级导入绑定会失败，见 card.js 文件头）——
  const S = space;
  const F = font;
  const C = colors;
  const sm = samples;
  return (
    <column gap={S.sm} padding={S.md} background={C.bg}>
      <text font={F.md} color={C.text}>{p.title}</text>
      <text font={F.xs} color={C.muted}>{p.note}</text>
      <Chart width={p.cw} height={150} />
      <text font={F.xs} color={C.muted}>{() => "共享采样 · 样本 " + sm()}</text>
    </column>
  );
};

// 关闭全部副窗。
export function closeScreenWindows() {
  for (let i = 0; i < opened.length; i = i + 1) {
    try { opened[i].close(); } catch (e) { /* 已关闭 */ }
  }
  opened = [];
  refreshTopology();
}

// 打开（或先关再开）副窗。返回本次打开的窗口数。
export function openScreenWindows() {
  if (opened.length > 0) {
    closeScreenWindows();
    return 0;
  }

  let list = [];
  try { list = screens(); } catch (e) { list = []; }

  const targets = [];
  if (list.length > 1) {
    for (let i = 1; i < list.length; i = i + 1) {
      targets.push({ d: list[i], sim: false });
    }
  } else if (list.length === 1) {
    targets.push({ d: list[0], sim: true });
  }

  for (let i = 0; i < targets.length; i = i + 1) {
    const t = targets[i];
    const title = "系统资源监视器 · " + (t.sim ? "模拟副屏" : t.d.name);
    const note = t.sim
      ? "单屏降级：此窗口在屏幕外坐标，用于验证多窗口生命周期（非真实跨屏）"
      : "显示器 " + t.d.name + " " + t.d.width + "×" + t.d.height;
    const handle = render(
      <window title={title} width={480} height={360}>
        <SecondaryBody title={title} note={note} cw={440} />
      </window>
    );
    try {
      if (t.sim) handle.moveTo(t.d.workWidth + 40, 40);
      else handle.moveTo(t.d.x, t.d.y);
    } catch (e) { /* 后端不支持移动时忽略 */ }
    opened.push(handle);
  }

  refreshTopology();
  return opened.length;
}

// 当前打开的副窗数量（UI 展示用）。
export function secondaryCount() {
  return opened.length;
}

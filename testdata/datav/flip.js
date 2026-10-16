// ===== gx-datav 数字组件: 翻牌器 + 数字动效 =====
//
// FlipBoard  —— 机械翻牌器。每位数字是一列 0-9 的竖直带, 值变化时该位
//               从旧数字滚到新数字 (连续多圈, 像真翻牌)。大屏 KPI 的
//               标配, 比直接换文字贵一点但"值在动"的感觉很强。
// CountUp    —— 数字缓动。值变化时从旧值 easeOutCubic 到新值, 中途每帧
//               都格式化显示。适合"总量/汇总"类指标。
//
// 公共约定:
//   - value 可以是数字或函数 (signal getter), 变化即触发动画;
//   - 动画复用 animate() (gx/gfx), 帧驱动是内核那根 16ms 心跳, 静止
//     时零开销;
//   - **副作用与绘制分离**: 值监听放 createEffect (组件构建时一次),
//     onDraw 只读信号 + 落笔。onDraw 在依赖收集与真实绘制时都会被调,
//     在里面 createSignal / 启动动画会导致每次重绘重复建状态。
//
// 动画进度的依赖链: effect 改 cur[i] 信号 → canvas onDraw 里读过它 →
// 自动重绘。effect 自己不读 cur (只用普通变量做镜像), 否则每帧通知都
// 会把 effect 拖起来重跑, 动画变死循环。

import { h, animate } from "gx/gfx";
import { createSignal, createEffect } from "gx/solid";
import { palette, fmtInt, textWidth } from "./theme.js";
import { alpha, centerText } from "./draw.js";

// ===== FlipBoard 翻牌器 =====
//
// props:
//   value    数字 (或 signal getter)
//   digits   位数 (缺省按 value 位数, 最少 2)
//   size     单字宽 (缺省 34), 牌高 = size * 1.4
//   gap      位间距 (缺省 6)
//   color    数字色 (缺省主文本色)
//   accent   翻定时的落位色 (缺省强调色)
//   bg       牌底 (缺省半透明深蓝)
export function FlipBoard(props) {
  const size = props.size || 34;
  const gap = props.gap === undefined ? 6 : props.gap;
  const color = props.color || palette.text;
  const accent = props.accent || palette.accent;
  const bg = props.bg || "rgba(10, 24, 56, 0.8)";
  const fontSize = Math.round(size * 0.72);
  const cellH = size * 1.4;

  const getValue = typeof props.value === "function" ? props.value : () => props.value;
  const n = Math.max(2, props.digits || String(Math.round(Number(getValue()) || 0)).length);

  // 每位的当前偏移 (格, 连续值) 做成信号供 onDraw 订阅; 另存一份普通
  // 镜像供 effect 计算 from (effect 读信号会把自己绕进去)。
  // createSignal 返回 [getter, setter] 二元组 —— getter 供 onDraw 读,
  // setter 供 effect 写, 分开存 (二元组本身不是函数, 不能直接调)。
  const curGet = [];
  const curSet = [];
  const curVals = [];
  const targetVals = [];
  const anims = [];
  for (let i = 0; i < n; i++) {
    const sig = createSignal(0);
    curGet.push(sig[0]);
    curSet.push(sig[1]);
    curVals.push(0);
    targetVals.push(0);
  }
  let lastStr = null;

  createEffect(() => {
    const v = Math.max(0, Math.round(Number(getValue()) || 0));
    const s = String(v).padStart(n, "0").slice(-n);
    if (lastStr === null) {
      // 首帧: 直接落位, 不播入场动画 (与 transition 的首值语义一致)
      lastStr = s;
      for (let i = 0; i < n; i++) {
        curVals[i] = Number(s.charAt(i));
        targetVals[i] = Number(s.charAt(i));
        curSet[i](curVals[i]);
      }
      return;
    }
    if (s === lastStr) return;
    for (let i = 0; i < n; i++) {
      if (s.charAt(i) === lastStr.charAt(i)) continue;
      const from = curVals[i];
      // 只向前滚: 目标不大于当前就绕圈展开 (模 10), 至少滚 1.5 圈 ——
      // 短距翻牌看不出"翻", 像直接换字。
      let dest = Number(s.charAt(i));
      while (dest <= from) dest += 10;
      if (dest - from < 15) dest += 10;
      targetVals[i] = dest;
      if (anims[i]) anims[i]();
      anims[i] = animate(from, dest, 650, (val) => {
        curVals[i] = val;
        curSet[i](val);
      });
    }
    lastStr = s;
  });

  const width = n * size + (n - 1) * gap + 8;
  const height = cellH + 8;

  return h("canvas", {
    width: width,
    height: height,
    onDraw: (ctx) => {
      // 底: 每位一张牌
      for (let i = 0; i < n; i++) {
        const x = 4 + i * (size + gap);
        ctx.fillRect(x, 0, size, cellH, bg);
        ctx.line(x, cellH / 2, x + size, cellH / 2, alpha(color, 0.12)); // 折痕
        ctx.strokeRect(x + 0.5, 0.5, size - 1, cellH - 1, alpha(color, 0.18));
      }
      // 数字带: 每位的 0-9 循环带按偏移 translate。
      // 带间距 = 整整一个牌高 (cellH) —— 一位一格: 静止态只有当前数字
      // 落在窗里 (相邻数字中心差一个 cellH, 恰在窗外); 翻动中带滑动,
      // 旧数字上半部滑出、新数字下半部滑入, 就是机械翻牌的那一下。
      // (曾用 fontSize*1.15 当日间距, 小于牌高 → 一格窗挤 1.7 个数字,
      //  静止态上下都露半截, 像没对齐的骰子。)
      for (let i = 0; i < n; i++) {
        const x = 4 + i * (size + gap);
        const off = curGet[i]() % 10; // 读信号 → 该位翻动时自动重绘
        for (let rep = -1; rep <= 10; rep++) {
          const d = ((rep % 10) + 10) % 10;
          const cy = cellH / 2 + (rep - off) * cellH;
          if (cy < -fontSize || cy > cellH + fontSize) continue;
          // 落在牌格中线上的那一位用强调色 (翻定的瞬间被看见)
          const settled = Math.abs(rep - off) < 0.5;
          centerText(ctx, String(d), x + size / 2, cy - fontSize / 2, fontSize,
            settled ? accent : color);
        }
        // 翻动中: 牌格上下缘加渐变遮罩, 遮住循环带的接缝
        if (Math.abs(curVals[i] - targetVals[i]) > 0.01) {
          ctx.fillRect(x, 0, size, Math.round(cellH * 0.16), alpha(bg, 0.92));
          ctx.fillRect(x, cellH - Math.round(cellH * 0.16), size, Math.round(cellH * 0.16), alpha(bg, 0.92));
        }
      }
    },
  });
}

// ===== CountUp 数字动效 =====
//
// props:
//   value      目标值 (数字或 signal getter)
//   duration   缓动时长 ms (缺省 800)
//   decimals   小数位 (缺省 0)
//   prefix     前缀串 (如 "¥")
//   suffix     后缀串 (如 "%" / "万")
//   fontSize   字号 (缺省 26)
//   color      颜色 (缺省主文本色)
//   comma      千分位 (缺省 true)
//   width      画布宽 (缺省按文本估算)
export function CountUp(props) {
  const duration = props.duration || 800;
  const decimals = props.decimals || 0;
  const prefix = props.prefix || "";
  const suffix = props.suffix || "";
  const fontSize = props.fontSize || 26;
  const color = props.color || palette.text;
  const comma = props.comma !== false;

  const getValue = typeof props.value === "function" ? props.value : () => props.value;

  const fmt = (v) => {
    const neg = v < 0;
    const abs = Math.abs(v);
    const ip = Math.floor(abs);
    const s = comma ? fmtInt(ip) : String(ip);
    const frac = decimals > 0
      ? "." + String(Math.round((abs - ip) * Math.pow(10, decimals))).padStart(decimals, "0")
      : "";
    return (neg ? "-" : "") + prefix + s + frac + suffix;
  };

  const [shown, setShown] = createSignal(fmt(Number(getValue()) || 0));
  let shownVal = Number(getValue()) || 0; // 普通镜像, effect 用它算 from
  let cancel = null;

  createEffect(() => {
    const nv = Number(getValue()) || 0;
    if (cancel) cancel();
    if (shownVal === nv) { setShown(fmt(nv)); return; }
    cancel = animate(shownVal, nv, duration, (v) => {
      shownVal = v;
      setShown(fmt(v));
    }, () => {
      shownVal = nv;
      setShown(fmt(nv));
    });
  });

  return h("canvas", {
    width: props.width || textWidth(fmt(Number(getValue()) || 0), fontSize) + 8,
    height: Math.round(fontSize * 1.5),
    onDraw: (ctx) => {
      centerText(ctx, shown(), ctx.width / 2, (ctx.height - fontSize) / 2, fontSize, color);
    },
  });
}

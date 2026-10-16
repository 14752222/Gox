// ===== gx-datav KPI 指标卡 =====
//
// 一屏 KPI 的原子单位: 标题 + 大数字 (缓动) + 单位 + 环比 (涨跌箭
// 头 + 三档色) + 迷你趋势线。单卡自足, 也可以 <row> 一排铺开 ——
// 大屏顶栏下面那一溜基本都是它。
//
// props (label/value/delta/trend 均可传 signal getter, 与全库一致):
//   label     指标名
//   value     数值
//   unit      单位 (缺省 "")
//   delta     环比变化 (数字; 正=涨, 负=跌, undefined=不显示)
//   deltaText 环比文案 (缺省 "较昨日"; 传 "" 不显示前缀)
//   trend     趋势数组 (给 Sparkline; 不给则不画线)
//   decimals  小数位 (缺省 0)
//   comma     千分位 (缺省 true)
//   color     大数字色 (缺省主文本色)
//   width     画布宽 (缺省 200)
//   height    画布高 (缺省 92)
//   animMs    缓动 ms (缺省 800)
import { h, animate } from "gx/gfx";
import { createSignal, createEffect } from "gx/solid";
import { palette, fmtInt, textWidth } from "./theme.js";
import { alpha } from "./draw.js";
import { Sparkline } from "./sparkline.js";

function val(v) { return typeof v === "function" ? v() : v; }

export function KpiCard(props) {
  const width = props.width || 200;
  const height = props.height || 92;
  const decimals = props.decimals || 0;
  const comma = props.comma !== false;
  const color = props.color || palette.text;
  const unit = props.unit || "";
  const animMs = props.animMs || 800;

  const getValue = typeof props.value === "function" ? props.value : () => props.value;

  // 数字缓动: effect 只依赖目标, 当前值走闭包镜像 (motion.js 同款思路,
  // 这里内联是因为还要顺带格式化, 不值得为一行包装抽文件)
  const init = Number(getValue()) || 0;
  let cur = init;
  let cancel = null;
  const [shown, setShown] = createSignal(fmt(init, decimals, comma));
  createEffect(() => {
    const nv = Number(getValue()) || 0;
    if (cancel) cancel();
    if (cur === nv) { setShown(fmt(nv, decimals, comma)); return; }
    cancel = animate(cur, nv, animMs, (v) => { cur = v; setShown(fmt(v, decimals, comma)); },
      () => { cur = nv; setShown(fmt(nv, decimals, comma)); });
  });

  // 环比派生值: label/delta 都可能是 getter, 每帧现算 (deltaText 同理)
  const deltaInfo = () => {
    const d = val(props.delta);
    const has = d !== undefined && d !== null;
    const up = has && Number(d) >= 0;
    return {
      has: has,
      up: up,
      text: has ? (up ? "▲ " : "▼ ") + (Math.round(Math.abs(Number(d)) * 10) / 10) + "%" : "",
      color: !has ? palette.sub : up ? palette.ok : palette.bad,
    };
  };

  return h("column", { width: width, height: height, gap: 4 },
    // 标题行: 左标题 + 右环比
    h("canvas", {
      width: width, height: 16,
      onDraw: (ctx) => {
        const fs = 12;
        ctx.drawText(String(val(props.label) || ""), 0, 2, fs, palette.sub);
        const di = deltaInfo();
        if (di.has) {
          const dfs = 11;
          const dw = textWidth(di.text, dfs);
          // 右对齐坐标用 ctx.width 而不是 width prop: 卡宽可能是百分比
          // 字符串 ("24%"), 字符串减数字得 NaN, 环比文本会整条消失
          // (2026-10-15 实测)。
          ctx.drawText(di.text, ctx.width - dw, 3, dfs, di.color);
          const prefix = props.deltaText === undefined ? "较昨日" : props.deltaText;
          if (prefix) {
            const pfs = 10;
            const pw = textWidth(String(prefix), pfs);
            ctx.drawText(String(prefix), ctx.width - dw - 6 - pw, 4, pfs, alpha(palette.sub, 0.8));
          }
        }
      },
    }),
    // 大数字行 (+ 右侧趋势线)
    h("row", { width: width, alignItems: "end", gap: 8 },
      // 宽度交给 flexGrow: 卡宽是百分比时算不出 "宽-96" 这个数
      // (NaN 画布会把这行整体带进 0 尺寸, 大数字和趋势线一起消失),
      // 行内只有一个可伸长元素, flexGrow=1 即"占满减趋势线的剩余"。
      h("canvas", {
        flexGrow: 1, height: 44,
        onDraw: (ctx) => {
          const fs = 30;
          const txt = shown();
          const tw = textWidth(txt, fs);
          ctx.drawText(txt, 0, 6, fs, color);
          if (unit) {
            ctx.drawText(String(unit), tw + 4, 6 + fs - 13, 12, palette.sub);
          }
        },
      }),
      props.trend
        ? Sparkline({
            width: 84, height: 36, data: props.trend,
            color: () => deltaInfo().color,
          })
        : null
    ),
    // 底部细线 (卡的"桌沿") —— 色随环比涨跌
    h("canvas", {
      width: width, height: 2,
      onDraw: (ctx) => {
        // 同理: 横跨卡的整宽取 ctx.width (width prop 可能是百分比字符串)
        ctx.fillRect(0, 0, ctx.width, 2, alpha(deltaInfo().color, 0.5));
      },
    })
  );
}

// fmt(v, decimals, comma): 千分位 + 小数位。模块级纯函数 (decimals/comma
// 是组件 prop, 由调用方传进来 —— 闭包够不到模块级)。
function fmt(v, decimals, comma) {
  const neg = v < 0;
  const abs = Math.abs(v);
  const ip = Math.floor(abs);
  const s = comma ? fmtInt(ip) : String(ip);
  const frac = decimals > 0
    ? "." + String(Math.round((abs - ip) * Math.pow(10, decimals))).padStart(decimals, "0")
    : "";
  return (neg ? "-" : "") + s + frac;
}

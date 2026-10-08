// 实时折线图：canvas 自绘（只用 7 个原语里的 fillRect / line / fillCircle / drawText）。
//
// 关键点：onDraw 里读 signal → 该 effect 订阅它 → 值变即自动重绘。
// 这里读的是 store 的 revision()（每采样一次 +1）与 selected()，于是每采一次
// 折线就重画一帧。
//
// ⚠️ 为什么把颜色 / 函数 / 常量在组件体里**再别名一遍**（col / bp / ff / …）？
//   这是绕开一个本仓库实测的 VM 缺陷（b381cbc，见 README「已知限制」）：
//   跨模块 import 的组件，其 `onDraw` 回调若**直接引用模块顶层的导入绑定**，
//   会在依赖收集阶段抛 `ReferenceError: Cannot access lexical declaration before
//   initialization`（TDZ）。把绑定取到**组件函数激活作用域**的局部变量后，
//   onDraw 闭包引用的是局部绑定，即可正常绘制。
//   最小复现已归档：D:/code/Gox/.workbuddy/tmp/（_c4/_t4 报错，_c6/_t6 通过）。
//   改这个文件时**别把这些别名删掉**。
import { h } from "gox";
import { colors } from "../theme.js";
import { channelById } from "../lib/channels.js";
import { buildPoints } from "../lib/polyline.js";
import { frameFor } from "../lib/ticks.js";
import { classify } from "../lib/threshold.js";
import { mapY } from "../lib/scale.js";
import { selected, revision, historyFor } from "../store.js";

export const Chart = (p) => {
  // —— 别名到激活作用域（见文件头说明，勿删）——
  const col = colors;
  const sel = selected;
  const rev = revision;
  const hist = historyFor;
  const cById = channelById;
  const bp = buildPoints;
  const ff = frameFor;
  const cls = classify;
  const mY = mapY;

  // 绘图区内边距（左侧留数值标签、底部留时间刻度）。
  const PADL = 46;
  const PADR = 14;
  const PADT = 24;
  const PADB = 20;

  const fmt1 = (v) => (typeof v !== "number" || v !== v ? "—" : String(Math.round(v * 10) / 10));

  return (
    <canvas
      width={p.width}
      height={p.height}
      onDraw={(ctx) => {
        // 订阅：每采一次 rev 变一次 → 本 effect 重跑 → 重绘
        rev();
        const ch = cById(sel());
        const values = hist(ch.id);
        const W = ctx.width;
        const H = ctx.height;

        ctx.fillRect(0, 0, W, H, col.chartBg);

        const minSpan = ch.kind === "gauge" ? 25 : 2;
        const fr = ff(values, { minSpan: minSpan });

        // 横向网格 + 左轴数值标签
        for (let i = 0; i < fr.valueTicks.length; i = i + 1) {
          const v = fr.valueTicks[i];
          const y = mY(v, fr.lo, fr.hi, H, PADT, PADB);
          ctx.line(PADL, y, W - PADR, y, col.grid);
          ctx.drawText(fmt1(v), 6, y - 5, 10, col.axis);
        }

        // 阈值横线（warn 琥珀 / danger 红）——只在落在当前量程内时画
        const drawT = (t, color) => {
          if (typeof t !== "number") return;
          if (t < fr.lo || t > fr.hi) return;
          const y = mY(t, fr.lo, fr.hi, H, PADT, PADB);
          ctx.line(PADL, y, W - PADR, y, color);
        };
        drawT(ch.warn, col.warn);
        drawT(ch.danger, col.danger);

        // 折线本体：逐段画（ctx.line 无路径对象）
        const pts = bp(values, {
          width: W,
          height: H,
          lo: fr.lo,
          hi: fr.hi,
          padLeft: PADL,
          padRight: PADR,
          padTop: PADT,
          padBottom: PADB,
        });
        for (let i = 1; i < pts.length; i = i + 1) {
          ctx.line(pts[i - 1].x, pts[i - 1].y, pts[i].x, pts[i].y, col.accent);
        }

        // 末点：按当前档位着色（阈值告警在这里最直观）
        if (pts.length > 0) {
          const lastIdx = values.length - 1;
          const lvl = cls(values[lastIdx], ch.warn, ch.danger);
          const c = lvl === "danger" ? col.danger : lvl === "warn" ? col.warn : col.ok;
          const lp = pts[pts.length - 1];
          ctx.fillCircle(lp.x, lp.y, 3, c);
        }

        // 标题行 + 当前读数
        ctx.drawText(ch.name + "  (" + ch.unit + ")", PADL, 6, 11, col.muted);
        const cur = values.length > 0 ? values[values.length - 1] : null;
        ctx.drawText(cur === null ? "无样本" : fmt1(cur), W - 84, 6, 12, col.text);

        // 时间轴刻度（采样序号 —— 运行时无墙钟，见 README）
        const it = fr.indexTicks;
        for (let k = 0; k < it.length; k = k + 1) {
          const idx = it[k];
          const x = Math.round(
            PADL + (values.length <= 1 ? 0.5 : idx / (values.length - 1)) * (W - PADL - PADR)
          );
          ctx.line(x, H - PADB, x, H - PADB + 3, col.grid);
          ctx.drawText("#" + idx, x - 8, H - PADB + 5, 9, col.axis);
        }
      }}
    />
  );
};

// ===== gx-datav 主题与公共工具 =====
//
// 数据大屏的视觉语言与桌面应用相反: 桌面要"轻、亮、贴近系统", 大屏要
// "沉、暗、把数据推到最前"。本文件收三样东西:
//   1. 色板 —— 深空底 + 高饱和强调色, 全部常量, 不运行时解析;
//   2. 数学工具 —— clamp / lerp / 缓动 / 角度换算, 图表组件天天用;
//   3. 文本度量 —— 内核 canvas 没有把 MeasureText 暴露给脚本 (只有 Go 侧
//      内部用), 而图表要居中、要避让、要截断, 没宽度算不了。这里按
//      runeAdvanceFace 的口径做估算: CJK 全宽 ≈ 字号, ASCII ≈ 0.58 字号,
//      空格 0.3 —— 用于布局估算, 不用于像素级对齐 (那种场景请用 Go 侧
//      组件)。
//
// 颜色语义约定 (全库统一):
//   bg      大屏最底层底色
//   panel   面板填充 (半透明, 叠在 bg 上略有层次)
//   line    网格 / 轴线 (低亮)
//   text    主文本 / sub 次文本
//   accent  主强调色 (青) —— 进度、当前值、高亮
//   ok/warn/bad  三档状态色
//   series  图表分类色板 (按索引循环取)

// ---- 色板 ----
export const palette = {
  bg: "#070d1f",
  bgDeep: "#040814",
  panel: "rgba(13, 32, 74, 0.55)",
  panelSolid: "#0d1f3f",
  panelEdge: "rgba(64, 128, 255, 0.28)",
  line: "rgba(90, 130, 200, 0.18)",
  text: "#dbe8ff",
  sub: "#7e93bd",
  accent: "#22e6ff",
  accentDim: "rgba(34, 230, 255, 0.25)",
  ok: "#2ee6a8",
  warn: "#ffc94d",
  bad: "#ff5c7a",
  purple: "#9d7bff",
  orange: "#ff9f5a",
  pink: "#ff7ab8",
};

// 图表分类色板: 顺序即优先级, 主色打头。深底上按这个顺序取不会撞。
export const seriesColors = [
  palette.accent,   // 青
  palette.ok,       // 绿
  palette.warn,     // 黄
  palette.bad,      // 红
  palette.purple,   // 紫
  palette.orange,   // 橙
  palette.pink,     // 粉
  "#5aa9ff",        // 蓝
];

// 缺省字号阶梯: 大屏观看距离远, 正文不小于 12, 主数字 28 起。
export const fontSizes = {
  micro: 11,
  small: 12,
  body: 14,
  title: 16,
  big: 22,
  hero: 30,
};

// ---- 图表刻度 ----

// niceCeil(v): 向上取到 1/2/5 × 10^k 的"整数感"刻度。Y 轴上限、散点轴域
// 都用它 —— 曾在线/柱/散点/组合四个文件里各复制一份, 提取到此一处
// (图表文件从 theme.js 导入, 不再各自为政)。
export function niceCeil(v) {
  const p = Math.pow(10, Math.floor(Math.log10(Math.max(v, 1e-9))));
  const n = v / p;
  const m = n <= 1 ? 1 : n <= 2 ? 2 : n <= 5 ? 5 : 10;
  return m * p;
}

// ---- 数学工具 ----

export function clamp(v, lo, hi) { return v < lo ? lo : v > hi ? hi : v; }

export function lerp(a, b, t) { return a + (b - a) * t; }

// 先快后慢的出场缓动 —— 大屏数字/进度入场用它与线性观感差异明显,
// 而成本一样是一次乘法。
export function easeOutCubic(t) {
  const x = clamp(t, 0, 1);
  return 1 - Math.pow(1 - x, 3);
}

export function easeOutBack(t) {
  const x = clamp(t, 0, 1);
  const c = 1.70158;
  return 1 + (c + 1) * Math.pow(x - 1, 3) + c * Math.pow(x - 1, 2);
}

// 弧度/度互换: canvas 的 arc 是弧度, 仪表盘刻度习惯用度, 两边都写全。
export const DEG = Math.PI / 180;
export function deg2rad(d) { return d * DEG; }
export function rad2deg(r) { return r / DEG; }

// 极坐标 → 直角 (canvas 的 y 向下, 所以 0° 指右、90° 指下)。
export function polar(cx, cy, r, rad) {
  return { x: cx + r * Math.cos(rad), y: cy + r * Math.sin(rad) };
}

// ---- 数字格式化 ----

// 固定宽度补零 (int 版): pad2(7) → "07"。
export function pad2(n) {
  const v = Math.floor(Math.abs(n));
  return (v < 10 ? "0" : "") + v;
}

// 千分位: fmtInt(1234567) → "1,234,567"。大屏大数字一眼看不出量级,
// 分隔符是最便宜的方案 (Intl 运行时没有, 自己拼)。
export function fmtInt(n) {
  const neg = n < 0;
  const s = String(Math.floor(Math.abs(n)));
  let out = "";
  for (let i = 0; i < s.length; i++) {
    if (i > 0 && (s.length - i) % 3 === 0) out += ",";
    out += s.charAt(i);
  }
  return (neg ? "-" : "") + out;
}

// 大数字缩写: fmtCompact(12345) → "1.2w"。万以下原样, 万/亿分档。
export function fmtCompact(n) {
  const abs = Math.abs(n);
  if (abs >= 1e8) return (n / 1e8).toFixed(1).replace(/\.0$/, "") + "亿";
  if (abs >= 1e4) return (n / 1e4).toFixed(1).replace(/\.0$/, "") + "万";
  return fmtInt(n);
}

// 保留 n 位小数并去掉尾零: trimFloat(3.10) → "3.1"。
export function trimFloat(v, n) {
  const d = n === undefined ? 1 : n;
  return Number(v.toFixed(d)).toString();
}

// ---- 文本度量 (估算) ----

// 单字符宽度系数: 与 gfx/runeAdvanceFace 对齐 —— 取不到字形时退半个字号,
// CJK 取全宽, ASCII 字母数字偏窄。入参是 UTF-16 码元 (number), 不是字符:
// 本运行时的 for...of 目前按 UTF-8 字节迭代 (实测 item.length 恒 1), 逐
// 字符度量必须走索引 + charCodeAt, 否则中文宽度会按字节数虚高近 3 倍。
// 代价: U+FFFF 以上的代理对 (emoji) 按两个半宽算 —— 大屏文本用不到。
function charFactor(c) {
  if (c === 32) return 0.3;
  if (c >= 0x1100 && (c <= 0x115f || c === 0x2329 || c === 0x232a ||
      (c >= 0x2e80 && c <= 0xa4cf) || (c >= 0xac00 && c <= 0xd7a3) ||
      (c >= 0xf900 && c <= 0xfaff) || (c >= 0xfe30 && c <= 0xfe6f) ||
      (c >= 0xff00 && c <= 0xff60) || (c >= 0xffe0 && c <= 0xffe6))) {
    return 1.0;
  }
  return 0.58;
}

// textWidth(text, size): 估算字符串像素宽。全库的"文本居中/避让/截断"
// 都走它 —— 一处系数, 全库一致; 真要精确, 请反馈给内核加 measureText。
export function textWidth(text, size) {
  const s = String(text);
  let w = 0;
  for (let i = 0; i < s.length; i++) w += charFactor(s.charCodeAt(i)) * size;
  return Math.ceil(w);
}

// truncateTo(text, size, maxWidth): 超宽截断并补 ".."。
export function truncateTo(text, size, maxWidth) {
  const s = String(text);
  if (textWidth(s, size) <= maxWidth) return s;
  let out = "";
  for (let i = 0; i < s.length; i++) {
    if (textWidth(out + s[i] + "..", size) > maxWidth) break;
    out += s.charAt(i);
  }
  return out + "..";
}

// ---- 数据流工具 ----

// walkSeries(values, amp): 生成围绕均值的平滑随机游走 —— demo 的模拟
// 数据源。确定性种子可选 (同一 seed 每次相同, 截图测试要可复现)。
export function makeRng(seed) {
  let s = (seed === undefined ? 123456789 : seed) >>> 0;
  return function rng() {
    // xorshift32: 周期 2^32-1, 无外部依赖, 够 demo 用
    s ^= s << 13; s >>>= 0;
    s ^= s >>> 17;
    s ^= s << 5; s >>>= 0;
    return s / 4294967296;
  };
}

// seriesRandomWalk(n, start, amp, seed, lo, hi): 长度 n 的随机游走序列。
export function seriesRandomWalk(n, start, amp, seed, lo, hi) {
  const rng = makeRng(seed);
  const out = [];
  let v = start;
  for (let i = 0; i < n; i++) {
    v += (rng() - 0.5) * 2 * amp;
    if (lo !== undefined && v < lo) v = lo;
    if (hi !== undefined && v > hi) v = hi;
    out.push(Math.round(v * 100) / 100);
  }
  return out;
}

// normalize(values, min, max): 线性映射到 [0,1], 供条形/进度复用。
export function normalize(v, min, max) {
  if (max === min) return 0;
  return clamp((v - min) / (max - min), 0, 1);
}

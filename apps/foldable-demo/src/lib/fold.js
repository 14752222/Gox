// 纯逻辑：折叠屏读数的文本格式化与形态判定。不 import "gox"。
//
// 这一层刻意不碰任何 API —— 它把"读数 → 字符串 / 布尔"这件事单独拿出来，
// 好让布局组件只管排版，也让 `lib/fold.js` 可以被纯逻辑测试直接覆盖。

// postureText 姿态 → 中文标签（姿态字符串来自 gx/screen 的 posture()）。
export function postureText(p) {
  if (p === "half-open") return "半折（两段都看得见）";
  if (p === "folded") return "完全折起（只看得到一段）";
  if (p === "flat") return "平展（一整块屏）";
  return "未知（没人上报）";
}

// hingeText 折痕 → 一行摘要。无折痕时明确说"无"，不要静默成 0。
export function hingeText(h) {
  if (!h) return "无折痕";
  return (
    "折痕 " + h.orientation + "  (" + h.x + "," + h.y + ") " + h.width + "×" + h.height
  );
}

// regionsText 保留区 → 一行摘要。division 是折痕那条带，occlusion 是挖孔一类遮挡。
export function regionsText(r) {
  if (!r) return "division 0 段 / occlusion 0 处";
  const d = (r.division || []).length;
  const o = (r.occlusion || []).length;
  return "division " + d + " 段 / occlusion " + o + " 处";
}

// suggestedText layoutMode().suggested → 中文标签。框架只给建议，不改布局。
export function suggestedText(s) {
  if (s === "dual") return "建议双栏";
  if (s === "tablet") return "建议平板档";
  if (s === "single") return "建议单栏";
  return "—";
}

// panes 由每个面板的宽度算"几栏"。**只信宽度，不信姿态** ——
// 上下折（Pocket 系列）展开后宽度可能仍 < 600dp，用姿态推必然翻车。
export function panes(width) {
  return width >= 600 ? 2 : 1;
}

// logText 日志数组 → 单行（最多留最近 3 条，避免撑破面板）。
export function logText(lines) {
  if (!lines || lines.length === 0) return "(还没有事件)";
  return lines.slice(-3).join("   |   ");
}

// 文本度量 —— 图表刻度留白与引用块竖条高度都要先知道"这段字有多宽"。
//
// ## 为什么是估算而不是测量
//
// ctx（见 `gfx/canvas.go`）只暴露绘制原语：**没有任何测量接口**。
// `ctx.drawText` 在 Go 侧是有返回值的（实际绘制宽度），但 builtin 把它丢了，
// 脚本侧拿不到。而 `<text>` 节点的尺寸是布局阶段由 Go 侧量出来的，脚本也读不到。
//
// 于是只能估。口径统一放在这里（而不是散在 chart.js / markdown.js 里），
// 将来内核若补了 `ctx.measureText`，**只改这一个文件**就够了。
//
// ## 估算口径
//
//   - 单个字符：CJK 及全角标点（Unicode > U+2E80，即 CJK 部首起始）按 1em；
//     其余（拉丁字母、数字、半角标点）按 0.6em —— 这是无衬线正体的典型平均字宽。
//   - 段宽 = 各字符宽之和。
//
// 误差来源：比例字体的字宽本就不齐（"i" 比 "W" 窄得多），所以 Latin 文本会
// 偏宽几个像素。对"刻度标签留白""竖条高度"这种用途够用；不要拿它去做
// 逐像素对齐的排版。

export function textWidth(s, size) {
  const t = s === undefined || s === null ? "" : String(s);
  let w = 0;
  for (let i = 0; i < t.length; i++) {
    const code = t.charCodeAt(i);
    w = w + (code > 0x2e80 ? size : size * 0.6);
  }
  return Math.round(w);
}

// estimateLines 估算一段文本在给定像素宽下折成几行。
//
// 只用于"这块内容大概多高"这类**不要求精确**的场合（引用块竖条）。
// 差一两行不影响可读性，所以不做逐词断行的模拟。
export function estimateLines(text, font, width) {
  const w = textWidth(text, font);
  if (w <= 0) return 1;
  if (width <= 0) return 1;
  const n = Math.ceil(w / width);
  return n < 1 ? 1 : n;
}

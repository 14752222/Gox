// 设计令牌 —— 颜色 / 间距 / 字号 / Markdown 排版层级集中在这里。
//
// 与 json-toolbox 同一条约定：**每个应用自带一份**，不共享（应用要能整个目录
// 拷走独立运行，依赖 ../_shared 会断）。等第二个应用复用同一份代码时再抽公共
// 目录 —— 过早抽象会把接口锁死在只有第一个使用者见过的形状上。
//
// gfx 的颜色支持命名色与 #rgb / #rgba / #rrggbb / #rrggbbaa / rgb() / rgba()，
// 带 alpha 的会与下方内容做真正的混合。

export const colors = {
  bg: "#f2f4f8",
  panel: "#ffffff",
  sidebar: "#e9edf4",
  border: "#d5dbe6",
  text: "#1c2430",
  muted: "#6b7686",
  accent: "#2f6fed",
  accentFg: "#ffffff",
  // Markdown 专用
  codeBg: "#eef1f6",
  codeText: "#a13d5c",
  quoteBar: "#c9d2e0",
  quoteText: "#55607a",
  link: "#1a5fb4",
  headText: "#141b26",
  hr: "#c3cad6",
};

export const space = { xs: 4, sm: 8, md: 12, lg: 18 };

export const font = { xs: 10, sm: 11, md: 13, lg: 16, xl: 20 };

// markdownStyle 是 renderMarkdown 的排版表。
//
// 为什么单独抽出来而不是散在渲染函数里：Markdown 的"长什么样"是设计决定，
// 换应用换主题时只该改这一处；渲染函数只负责"什么结构"。
export const markdownStyle = {
  width: 520,
  body: { font: font.md, color: colors.text, lineHeight: 20 },
  head: [
    { font: font.xl, color: colors.headText },
    { font: font.lg, color: colors.headText },
    { font: 15, color: colors.headText },
    { font: font.md, color: colors.headText },
    { font: font.sm, color: colors.muted },
    { font: font.xs, color: colors.muted },
  ],
  code: {
    font: font.sm,
    color: colors.codeText,
    fontFamily: "monospace",
    background: colors.codeBg,
    padding: 8,
  },
  quote: { font: font.md, color: colors.quoteText, bar: colors.quoteBar },
  link: colors.link,
  hr: colors.hr,
  listGap: 2,
  blockGap: 10,
};

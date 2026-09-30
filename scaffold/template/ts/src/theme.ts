// 设计令牌 —— 颜色 / 间距 / 字号集中在这里（TypeScript 版）。
//
// 组件只引用令牌、不写魔法值，于是"整体换肤"是改这一个文件的事。
// gfx 的颜色支持命名色与 #rgb / #rgba / #rrggbb / #rrggbbaa / rgb() / rgba()，
// 带 alpha 的颜色会与下方内容做真正的混合。
//
// as const：把每个字段收成字面量类型，令牌被改错时 IDE 当场画红线。
export const colors = {
  bg: "#ffffff",
  panel: "#f5f7fa",
  border: "#dfe4ec",
  text: "#1f2430",
  muted: "#7b8494",
  accent: "#2f6fed",
  accentFg: "#ffffff",
  ok: "#27ae60",
  warn: "#e67e22",
  danger: "#c0392b",
} as const;

export const space = { xs: 4, sm: 8, md: 12, lg: 18 } as const;

export const font = { sm: 11, md: 13, lg: 17, xl: 22 } as const;

// 令牌的类型推导出来给别处复用（比如自定义组件的 props 引用颜色键名）。
export type ColorToken = keyof typeof colors;
export type SpaceToken = keyof typeof space;
export type FontToken = keyof typeof font;

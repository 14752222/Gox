// 设计令牌 —— 颜色 / 间距 / 字号集中在这里。
//
// 组件只引用令牌、不写魔法值，于是"整体换肤"是改这一个文件的事。
// gfx 的颜色支持命名色与 #rgb / #rgba / #rrggbb / #rrggbbaa / rgb() / rgba()，
// 带 alpha 的颜色会与下方内容做真正的混合。
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
};

export const space = { xs: 4, sm: 8, md: 12, lg: 18 };

export const font = { sm: 11, md: 13, lg: 17, xl: 22 };

// 设计令牌 —— 颜色 / 间距 / 字号集中在这里。
//
// 应用只引用令牌、不写魔法值。监视器默认深色（长时间盯屏更舒服，也让折线更醒目）。
// gfx 颜色支持命名色与 #rgb / #rgba / #rrggbb / #rrggbbaa / rgb() / rgba()。
export const colors = {
  bg: "#12151c",
  panel: "#1b2029",
  panelAlt: "#232936",
  border: "#2c3444",
  text: "#e6eaf2",
  muted: "#8b95a7",

  accent: "#4f8cff",
  ok: "#3fbf6a",
  warn: "#e0a53a",
  danger: "#e5484d",

  // 画布专用
  chartBg: "#0e1218",
  grid: "#242c3a",
  axis: "#55607a",
  threshold: "#4a3547",
};

export const space = { xs: 4, sm: 6, md: 10, lg: 16 };

export const font = { xs: 10, sm: 11, md: 13, lg: 15, xl: 22 };

// 阈值档位 → 颜色（UI 层唯一的一处映射，threshold.js 只出档位字符串）
export const levelColor = (level) =>
  level === "danger" ? colors.danger : level === "warn" ? colors.warn : colors.ok;

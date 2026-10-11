// 纯逻辑：设置项默认值与校验。不 import "gox"。

export const DEFAULTS = {
  dark: false,
  accent: "#2f6fed",
  fontSize: 13,
  autoSave: true,
  notifications: true,
  sound: false,
  quality: "high",
  reminderDate: "",
};

// merge 把存储里读出的（可能缺字段/类型错的）值并到默认值上。
export function merge(saved) {
  const out = {};
  for (const k in DEFAULTS) out[k] = DEFAULTS[k];
  if (saved && typeof saved === "object") {
    for (const k in DEFAULTS) {
      const v = saved[k];
      if (typeof v === typeof DEFAULTS[k]) out[k] = v;
    }
  }
  return out;
}

// clampFontSize 字号限制在 10..20。
export function clampFontSize(n) {
  if (n < 10) return 10;
  if (n > 20) return 20;
  return Math.round(n);
}

export const QUALITY_OPTIONS = [
  { value: "low", label: "低（省电）" },
  { value: "medium", label: "中" },
  { value: "high", label: "高（推荐）" },
];

// 纯逻辑：播放模式 / 时间格式化 / 频谱样值。不 import "gox"。

// nextIndex 按模式算下一首下标；到尾返回 -1（顺序模式停播）。
export function nextIndex(cur, len, mode) {
  if (len === 0) return -1;
  if (mode === "one") return cur;
  const n = cur + 1;
  if (n < len) return n;
  return mode === "all" ? 0 : -1;
}

// prevIndex 上一首；到头回绕到末尾。
export function prevIndex(cur, len) {
  if (len === 0) return -1;
  return cur - 1 >= 0 ? cur - 1 : len - 1;
}

// fmtTime 秒 → "m:ss"。
export function fmtTime(sec) {
  const s = Math.floor(sec);
  const m = Math.floor(s / 60);
  const r = s % 60;
  return m + ":" + (r < 10 ? "0" : "") + r;
}

export const MODES = ["seq", "all", "one"];
export const MODE_LABEL = { seq: "顺序", all: "循环", one: "单曲" };

export function nextMode(mode) {
  const i = MODES.indexOf(mode);
  return MODES[(i + 1) % MODES.length];
}

// spectrum 频谱样值：progress 驱动的伪随机柱高（确定性，避免闪烁无序）。
export function spectrum(seed, bars, playing) {
  const out = [];
  for (let i = 0; i < bars; i++) {
    const ph = seed * 0.7 + i * 1.3;
    const v = playing
      ? 0.25 + 0.75 * Math.abs(Math.sin(ph) * Math.cos(ph * 0.37 + i))
      : 0.08;
    out.push(v);
  }
  return out;
}

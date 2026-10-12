// 纯逻辑：定长环形缓冲 + 折线坐标。不 import "gox"。

// makeRing(cap) → { push(v), values() }，超出容量丢最旧。
export function makeRing(cap) {
  let buf = [];
  return {
    push(v) {
      buf.push(v);
      if (buf.length > cap) buf = buf.slice(buf.length - cap);
    },
    values() {
      return buf;
    },
  };
}

// linePoints(values, w, h, max) → 折线点 [{x, y}]，y 轴向下。
export function linePoints(values, w, h, max) {
  const n = values.length;
  if (n === 0) return [];
  const step = n > 1 ? w / (n - 1) : 0;
  const pts = [];
  for (let i = 0; i < n; i++) {
    const v = values[i] < 0 ? 0 : values[i] > max ? max : values[i];
    pts.push({ x: i * step, y: h - (v / max) * (h - 4) - 2 });
  }
  return pts;
}

// nextWalk 随机游走采样（桌面演示数据源），0..100 之间。
export function nextWalk(prev, jitter) {
  let v = prev + (Math.random() - 0.5) * 2 * jitter;
  if (v < 2) v = 2;
  if (v > 98) v = 98;
  return v;
}

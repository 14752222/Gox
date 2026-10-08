// 指标通道定义 + 采样规则（纯逻辑）。
//
// 每个通道描述「从 gx/dev.devSnapshot() 的哪一处读、怎么变成一条序列、告警阈值」。
// 通道分两类：
//   counter —— 单调累加计数器（帧数 / 缓存命中数…）。每样本取**增量**（速率）。
//   gauge   —— 瞬时比例（整帧比例 / 缓存占用率…）。每样本直接取值。
//
// 阈值语义是「越大越危险」，与 threshold.js 一致。
//
// ⚠️ 数据源说明：Gox 的运行时**没有**暴露 OS 级 CPU / 内存 / 磁盘读数
// （stats 只是数学模块，见 README）。本监视器监视的是它**确实暴露**的运行时资源：
// 渲染帧、图像/字形缓存、GUI 树、响应式 effect、内核告警 —— 全部来自 gx/dev。

export const CHANNELS = [
  {
    id: "frames",
    name: "帧增量",
    unit: "帧/样本",
    kind: "counter",
    warn: 12,
    danger: 30,
    // snap.frame.count —— 累计上屏帧数
    read: function (s) { return s.frame.count; },
  },
  {
    id: "full",
    name: "整帧增量",
    unit: "帧/样本",
    kind: "counter",
    warn: 4,
    danger: 10,
    read: function (s) { return s.frame.full; },
  },
  {
    id: "glyphHit",
    name: "字形缓存命中",
    unit: "次/样本",
    kind: "counter",
    warn: 400,
    danger: 1200,
    read: function (s) { return s.glyphCache.hits; },
  },
  {
    id: "glyphUse",
    name: "字形缓存占用",
    unit: "%",
    kind: "gauge",
    warn: 60,
    danger: 85,
    read: function (s) {
      const c = s.glyphCache;
      return c.cap > 0 ? (c.size / c.cap) * 100 : 0;
    },
  },
  {
    id: "imgUse",
    name: "图像缓存占用",
    unit: "%",
    kind: "gauge",
    warn: 60,
    danger: 85,
    read: function (s) {
      const c = s.imageCache;
      return c.cap > 0 ? (c.size / c.cap) * 100 : 0;
    },
  },
  {
    id: "fullRatio",
    name: "整帧比例",
    unit: "%",
    kind: "gauge",
    warn: 40,
    danger: 70,
    read: function (s) { return s.frame.fullRatio * 100; },
  },
];

const BY_ID = {};
for (let i = 0; i < CHANNELS.length; i = i + 1) {
  BY_ID[CHANNELS[i].id] = CHANNELS[i];
}

export function channelById(id) {
  return BY_ID[id] ? BY_ID[id] : CHANNELS[0];
}

// sampleChannel(ch, prevSnap, curSnap) → 该通道本样本的数值
//
// prevSnap 为 null（首个样本）时：counter 取 0（没有"上一次"可减），gauge 正常取值。
export function sampleChannel(ch, prevSnap, curSnap) {
  const cur = ch.read(curSnap);
  if (ch.kind === "counter") {
    if (!prevSnap) return 0;
    const before = ch.read(prevSnap);
    const d = cur - before;
    return d > 0 ? d : 0; // 计数器被重置时也回落成 0，不出现负速率
  }
  return cur;
}

// sampleAll(prevSnap, curSnap) → { id: number }
export function sampleAll(prevSnap, curSnap) {
  const out = {};
  for (let i = 0; i < CHANNELS.length; i = i + 1) {
    const ch = CHANNELS[i];
    out[ch.id] = sampleChannel(ch, prevSnap, curSnap);
  }
  return out;
}

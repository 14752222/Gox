// 纯逻辑：样例天气数据 + 曲线点计算。不 import "gox"。
// 数据形状对齐常见天气 API：current + hourly[24] + daily[7]。

export const CITIES = [
  {
    name: "深圳",
    current: { temp: 28, desc: "多云", humidity: 74, wind: "东南风 3 级" },
    hourly: [26, 26, 25, 25, 25, 26, 27, 28, 29, 30, 31, 32, 32, 33, 33, 32, 31, 30, 29, 29, 28, 28, 27, 27],
    daily: [
      { day: "今天", low: 25, high: 33, desc: "多云" },
      { day: "明天", low: 26, high: 34, desc: "晴" },
      { day: "周三", low: 26, high: 33, desc: "晴" },
      { day: "周四", low: 25, high: 31, desc: "阵雨" },
      { day: "周五", low: 24, high: 30, desc: "阵雨" },
      { day: "周六", low: 25, high: 31, desc: "多云" },
      { day: "周日", low: 26, high: 32, desc: "多云" },
    ],
  },
  {
    name: "北京",
    current: { temp: 12, desc: "晴", humidity: 30, wind: "北风 4 级" },
    hourly: [6, 5, 4, 4, 3, 3, 4, 6, 8, 10, 12, 13, 14, 15, 15, 14, 13, 11, 10, 9, 8, 7, 7, 6],
    daily: [
      { day: "今天", low: 3, high: 15, desc: "晴" },
      { day: "明天", low: 4, high: 16, desc: "晴" },
      { day: "周三", low: 5, high: 17, desc: "多云" },
      { day: "周四", low: 6, high: 16, desc: "多云" },
      { day: "周五", low: 4, high: 13, desc: "阴" },
      { day: "周六", low: 3, high: 12, desc: "晴" },
      { day: "周日", low: 2, high: 11, desc: "晴" },
    ],
  },
  {
    name: "哈尔滨",
    current: { temp: -4, desc: "小雪", humidity: 68, wind: "西北风 5 级" },
    hourly: [-8, -9, -10, -10, -11, -11, -10, -9, -7, -5, -4, -3, -2, -2, -3, -4, -5, -6, -7, -7, -8, -8, -8, -8],
    daily: [
      { day: "今天", low: -11, high: -2, desc: "小雪" },
      { day: "明天", low: -13, high: -5, desc: "晴" },
      { day: "周三", low: -15, high: -7, desc: "晴" },
      { day: "周四", low: -14, high: -6, desc: "多云" },
      { day: "周五", low: -12, high: -4, desc: "小雪" },
      { day: "周六", low: -10, high: -3, desc: "多云" },
      { day: "周日", low: -9, high: -2, desc: "晴" },
    ],
  },
];

export function cityByName(name) {
  for (let i = 0; i < CITIES.length; i++) {
    if (CITIES[i].name === name) return CITIES[i];
  }
  return CITIES[0];
}

export function cityNames() {
  return CITIES.map((c) => c.name);
}

// tempPoints 把逐时温度映射成画布点 [{x, y}]（含 8px 边距）。
export function tempPoints(temps, w, h) {
  let lo = temps[0];
  let hi = temps[0];
  for (let i = 1; i < temps.length; i++) {
    if (temps[i] < lo) lo = temps[i];
    if (temps[i] > hi) hi = temps[i];
  }
  const span = hi - lo || 1;
  const step = w / (temps.length - 1);
  return temps.map((t, i) => ({
    x: i * step,
    y: 8 + (1 - (t - lo) / span) * (h - 16),
  }));
}

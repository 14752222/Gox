// 环形缓冲：固定容量，push 覆盖最旧，toArray 按「旧 → 新」给出一份副本。
//
// 纯逻辑，不 import 任何宿主模块 —— 可在探针脚本里脱离 UI 单独跑。
// 监视器的历史序列都用它承载（每个指标一条），容量决定了折线图上可见的点数。
export function createRing(capacity) {
  const cap = capacity > 0 ? Math.floor(capacity) : 1;
  // 预分配 + 双指针：push 是 O(1)，不搬数组。
  const buf = new Array(cap);
  let len = 0; // 当前有效元素个数（<= cap）
  let head = 0; // 下一个写入位置

  return {
    capacity: cap,

    push(v) {
      buf[head] = v;
      head = (head + 1) % cap;
      if (len < cap) len = len + 1;
    },

    // 有效元素个数（不会超过容量）。
    size() {
      return len;
    },

    // 是否已填满（折线图右端对齐时用得到）。
    full() {
      return len >= cap;
    },

    // 旧 → 新。返回新数组，外部改不动内部状态。
    toArray() {
      const out = [];
      const start = (head - len + cap) % cap;
      for (let i = 0; i < len; i = i + 1) {
        out.push(buf[(start + i) % cap]);
      }
      return out;
    },

    // 最新一个（空 → undefined）。
    last() {
      return len > 0 ? buf[(head - 1 + cap) % cap] : undefined;
    },

    clear() {
      len = 0;
      head = 0;
    },
  };
}

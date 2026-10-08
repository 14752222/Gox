// 数值坐标映射：把「一串采样值」映射到画布像素坐标，含 y 轴范围自适应。
//
// 纯逻辑，可脱离 UI 单测。所有函数对边界输入（空数组 / 全等值 / NaN / 超量程）
// 都有确定行为，绝不返回 NaN —— 那会让 canvas 的 line 画不出来且不报错。

// isFinite 的本地版：只认有限数（排除 NaN / ±Infinity）。
function finite(v) {
  return typeof v === "number" && v === v && v !== Infinity && v !== -Infinity;
}

// niceBounds(values, opts) → { lo, hi, span }
//
// 自适应 y 轴范围：
//   - 空数组 / 无有效数 → [0, minSpan]
//   - 全等值或范围过窄（< minSpan）→ 以该值居中撑开 minSpan
//   - 否则 → 真实 [min,max] 上下各留 pad 比例
//   - 数据全为非负时，lo 不会被拉到 0 以下（速率/占用率都非负，负轴没意义）
export function niceBounds(values, opts) {
  const minSpan = opts && finite(opts.minSpan) && opts.minSpan > 0 ? opts.minSpan : 1;
  const pad = opts && finite(opts.pad) ? opts.pad : 0.12;

  let lo = Infinity;
  let hi = -Infinity;
  let sawNegative = false;
  for (let i = 0; i < values.length; i = i + 1) {
    const v = values[i];
    if (!finite(v)) continue;
    if (v < lo) lo = v;
    if (v > hi) hi = v;
    if (v < 0) sawNegative = true;
  }

  if (lo === Infinity) {
    // 空数据：给一个稳定的初始量程，别让首帧画布抖。
    return { lo: 0, hi: minSpan, span: minSpan };
  }
  if (hi - lo < minSpan) {
    const mid = (lo + hi) / 2;
    let a = mid - minSpan / 2;
    let b = mid + minSpan / 2;
    if (!sawNegative && a < 0) {
      a = 0;
      b = minSpan;
    }
    return { lo: a, hi: b, span: b - a };
  }
  const span = hi - lo;
  let a = lo - span * pad;
  let b = hi + span * pad;
  if (!sawNegative && a < 0) a = 0;
  return { lo: a, hi: b, span: b - a };
}

// mapY(v, lo, hi, height, padTop, padBottom) → 像素 y
//
// 结果**钳在 [padTop, height-padBottom]**：超量程的点贴在上下边缘而不是跑到
// 画布外（canvas 虽有裁剪，但贴边更符合「量程被顶满」的直觉）。
export function mapY(v, lo, hi, height, padTop, padBottom) {
  const top = padTop;
  const bottom = height - padBottom;
  const span = hi - lo;
  let y;
  if (!finite(v) || !finite(span) || span === 0) {
    y = bottom;
  } else {
    y = bottom - ((v - lo) / span) * (bottom - top);
  }
  return clampRound(y, top, bottom);
}

// mapX(i, n, width, padLeft, padRight) → 像素 x
//
// n<=1 时把唯一点放在绘图区水平中点（避免除零，也避免点贴左缘）。
export function mapX(i, n, width, padLeft, padRight) {
  const left = padLeft;
  const right = width - padRight;
  if (n <= 1) {
    return Math.round((left + right) / 2);
  }
  return Math.round(left + (i / (n - 1)) * (right - left));
}

function clampRound(v, min, max) {
  if (!finite(v)) return min;
  let x = Math.round(v);
  if (x < min) x = min;
  if (x > max) x = max;
  return x;
}

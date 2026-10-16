// ===== gx-datav 值缓动公共逻辑 =====
//
// 环形进度、仪表盘、排行榜条形都要同一个模式: "目标值变化 → 当前值
// easeOutCubic 追上去, 途中每帧通知重绘"。抽成 makeAnimValue, 各组件
// 不再各自写一遍 effect + 镜像 + cancel。
//
// 为什么必须用普通变量做镜像: createEffect 执行期间读到的 getter 全
// 会成为依赖 —— effect 里若读了动画信号自己, 每帧 setV 都会把 effect
// 拖起来重跑, 动画还没开始就变成了每帧重启的死循环。所以 effect 只
// 依赖"目标表达式", 当前值走闭包变量。
//
// makeAnimValue(get, duration) → { signal, value }
//   get      () => 目标值 (数字, 内部可读 signal)
//   signal   动画中的当前值 (createSignal getter, 交给 onDraw 订阅)
//   value    同上的普通读法 (给非响应式上下文用)

import { createSignal, createEffect } from "gx/solid";
import { animate } from "gx/gfx";

export function makeAnimValue(get, duration) {
  const init = Number(get()) || 0;
  const [signal, setSignal] = createSignal(init);
  let cur = init;
  let cancel = null;

  createEffect(() => {
    const nv = Number(get()) || 0;
    if (cancel) cancel();
    if (cur === nv) return;
    const from = cur;
    cancel = animate(from, nv, duration, (x) => {
      cur = x;
      setSignal(x);
    }, () => {
      cur = nv;
      setSignal(nv);
    });
  });

  return { signal, value: () => cur };
}

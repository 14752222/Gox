// 示例应用 2: 服务仪表盘 (T15 交付之二)。
// 运行: ./gox testdata/apps/dashboard.js
//
// 展示的能力:
//   1. grid 布局: 等宽卡片栅格 (columns={3});
//   2. canvas 自绘 + 响应式重绘: mini 折线图 —— onDraw 里读 signal,
//      数据变化自动重绘 (这是引擎的"响应式 canvas"惯用法);
//   3. setInterval 定时刷新模拟"活数据": 每 800ms 推进一轮指标;
//   4. 进度条与状态徽章: progress / 彩色 rect + text 拼的 badge;
//   5. 暂停/恢复: show 指令切换"实时/已暂停"徽标, 定时器用 clearInterval 停掉。
//
// 数据是本地模拟的 (没联网), 指标走势用确定性伪随机 —— 每次启动长一样,
// 方便截图与对比。
import { h, render, createSignal } from "gox";

// ===== 模拟数据源 (确定性伪随机: 同一种子同一序列) =====

let seed = 42;
const nextRand = () => {
  // xorshift32, 够用且确定性
  seed ^= seed << 13; seed ^= seed >>> 17; seed ^= seed << 5;
  return (seed >>> 0) / 4294967295;
};

const mkSeries = () => {
  const arr = [];
  for (let i = 0; i < 24; i++) arr.push(0.35 + nextRand() * 0.4);
  return arr;
};

// ===== 状态 =====

const [paused, setPaused] = createSignal(false);

const mkMetric = (name, unit) => {
  const s = createSignal(mkSeries());
  return {
    name,
    unit,
    series: s[0],
    push: (v) => s[1]((arr) => arr.slice(1).concat([v])),
  };
};

const cpu = mkMetric("CPU", "%");
const mem = mkMetric("MEM", "MB");
const rps = mkMetric("REQ/s", "");

const stepAll = () => {
  const drift = () => (nextRand() - 0.5) * 0.12;
  const step1 = (m, lo, hi) => {
    const arr = m.series();
    const v = Math.min(hi, Math.max(lo, arr[arr.length - 1] + drift()));
    m.push(v);
  };
  step1(cpu, 0.05, 0.95);
  step1(mem, 0.2, 0.9);
  step1(rps, 0.1, 0.9);
};

// 定时器句柄留在 signal 里, 暂停/恢复切换时用
let timerId = 0;
let logSeq = 0;
const pushLog = (text) => {
  logSeq = logSeq + 1;
  setLog(log().slice(-4).concat([{ id: logSeq, text }]));
};
const [log, setLog] = createSignal([{ id: 0, text: "服务已启动, 每 800ms 采样一次" }]);

timerId = setInterval(() => {
  stepAll();
  const c = cpu.series();
  const last = c[c.length - 1];
  if (last > 0.85) {
    pushLog(`警告: CPU 高位 ${Math.round(last * 100)}%`);
  }
}, 800);

const togglePause = () => {
  const to = !paused();
  setPaused(to);
  if (to) {
    clearInterval(timerId);
    pushLog("采样已暂停");
  } else {
    timerId = setInterval(stepAll, 800);
    pushLog("采样已恢复");
  }
};

// ===== 界面 =====

// mini 折线图: 把 0..1 的序列画成 24 点折线 (onDraw 读 signal → 自动重绘)
const Spark = (p) => (
  <canvas width={p.width} height={40}
    onDraw={(ctx) => {
      const arr = p.metric.series();
      ctx.fillRect(0, 0, ctx.width, ctx.height, "#f4f6fa");
      const n = arr.length;
      const dx = ctx.width / (n - 1);
      for (let i = 1; i < n; i++) {
        const x1 = Math.round((i - 1) * dx);
        const y1 = Math.round(ctx.height - 4 - arr[i - 1] * (ctx.height - 8));
        const x2 = Math.round(i * dx);
        const y2 = Math.round(ctx.height - 4 - arr[i] * (ctx.height - 8));
        ctx.line(x1, y1, x2, y2, "#3355aa");
      }
    }} />
);

const pct = (m) => {
  const arr = m.series();
  return Math.round(arr[arr.length - 1] * 100);
};

const Card = (p) => (
  <column gap={6} padding={10} background="#ffffff">
    <row gap={6}>
      <text font={13} color="#68707c">{p.metric.name}</text>
      <rect width={8} height={8} background={() => (pct(p.metric) > 80 ? "#d84a3a" : "#3f9d55")} />
    </row>
    <text font={22}>{() => pct(p.metric) + (p.metric.unit ? " " + p.metric.unit : "")}</text>
    <Spark metric={p.metric} width={150} height={40} />
  </column>
);

render(
  <window title="服务仪表盘 — Gox 示例应用" width={560} height={380}>
    <column gap={10} padding={14} background="#eef1f5">
      <row gap={8}>
        <text font={17}>服务仪表盘</text>
        <rect width={10} height={10} background={() => (paused() ? "#d9a13a" : "#3f9d55")} />
        <text font={12} color="#68707c">{() => (paused() ? "已暂停" : "实时")}</text>
        <button padding={4} onClick={togglePause}>{() => (paused() ? "恢复" : "暂停")}</button>
      </row>

      <grid columns={3} gap={10}>
        <Card metric={cpu} />
        <Card metric={mem} />
        <Card metric={rps} />
      </grid>

      <column gap={4} padding={10} background="#ffffff">
        <text font={13} color="#68707c">事件日志</text>
        <view each={log} key="id">
          {(line) => <text font={12} color="#1c2430">{() => "· " + line.text}</text>}
        </view>
      </column>
    </column>
  </window>
);

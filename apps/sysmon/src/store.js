// 应用状态与采样循环。
//
// 跨组件共享状态的机制就是"把 signal 建在模块作用域"—— 没有额外的 store 框架。
//
// 采样源是 gx/dev.devSnapshot()（Gox 运行时**真实暴露**的资源读数：渲染帧、
// 图像/字形缓存、GUI 树、响应式 effect、内核告警）。Gox 没有 OS 级 CPU/内存
// 读数，stats 只是数学模块 —— 详见 README「数据源」。
//
// ⚠️ signal 一律用**索引取值**（`const p = createSignal(v); const set = p[1];`），
// 不要写 `const [x, setX] = createSignal(v)`：模块作用域下该形态的 setter 在被
// 同模块函数引用时会报 ReferenceError（见 apps-notes.md）。改这段别改回解构。
import { createSignal } from "gox";
import { devSnapshot } from "gx/dev";
import { screens, windows } from "gx/screen";
import { CHANNELS, channelById, sampleAll } from "./lib/channels.js";
import { createRing } from "./lib/ring.js";
import { classify } from "./lib/threshold.js";

// 历史点数（折线图可见窗口）与采样周期。
export const HISTORY_CAPACITY = 120;
export const SAMPLE_MS = 500;

// 每个通道一条环形缓冲。
const rings = {};
for (let i = 0; i < CHANNELS.length; i = i + 1) {
  rings[CHANNELS[i].id] = createRing(HISTORY_CAPACITY);
}

const pSnap = createSignal(null);
const snap = pSnap[0];
const setSnap = pSnap[1];

// revision 每采一次 +1：它是画布/统计的**响应式触发器**（环形缓冲本身是普通对象，
// 不是 signal，靠这条计数让 onDraw 重跑）。
const pRevision = createSignal(0);
const revision = pRevision[0];
const setRevision = pRevision[1];

// 默认通道选「字形缓存占用」：它会随绘制真实增长，折线肉眼可见地在动；
// 「帧增量」每样本恒为 1（本应用每 tick 自绘一帧），真实但是条平线。
const pSelected = createSignal("glyphUse");
const selected = pSelected[0];
const setSelected = pSelected[1];

const pPaused = createSignal(false);
const paused = pPaused[0];
const setPaused = pPaused[1];

const pSamples = createSignal(0);
const samples = pSamples[0];
const setSamples = pSamples[1];

const pScreenList = createSignal([]);
const screenList = pScreenList[0];
const setScreenList = pScreenList[1];

const pWindowList = createSignal([]);
const windowList = pWindowList[0];
const setWindowList = pWindowList[1];

const pAlerts = createSignal([]);
const alerts = pAlerts[0];
const setAlerts = pAlerts[1];

let prevSnap = null;
let timerId = 0;

// 一次采样：读快照 → 算各通道样本 → 入环 → 抬 revision。
export function pullOnce() {
  const cur = devSnapshot();
  const s = sampleAll(prevSnap, cur);
  for (let i = 0; i < CHANNELS.length; i = i + 1) {
    const id = CHANNELS[i].id;
    rings[id].push(s[id]);
  }
  prevSnap = cur;
  setSnap(cur);
  setSamples(samples() + 1);
  setRevision(revision() + 1);
  if (cur.warnings && cur.warnings.length > 0) {
    const recent = cur.warnings.slice(-3);
    const withIds = [];
    for (let i = 0; i < recent.length; i = i + 1) {
      withIds.push({ id: i, at: recent[i].at, text: recent[i].text });
    }
    setAlerts(withIds);
  }
}

export function startSampling() {
  if (timerId !== 0) return;
  pullOnce(); // 立刻采一次，别让首帧空着
  timerId = setInterval(pullOnce, SAMPLE_MS);
}

export function stopSampling() {
  if (timerId !== 0) {
    clearInterval(timerId);
    timerId = 0;
  }
}

// 暂停/继续。
export function togglePause() {
  const to = !paused();
  setPaused(to);
  if (to) stopSampling();
  else startSampling();
}

// 清空历史（不清快照计数基线 —— 继续采样时 counter 增量从下一样本起算）。
export function clearHistory() {
  for (let i = 0; i < CHANNELS.length; i = i + 1) rings[CHANNELS[i].id].clear();
  setRevision(revision() + 1);
}

// 重采样基线（清空后下一帧不出现一次巨大增量）。
export function rebaseBaseline() {
  prevSnap = null;
}

export function selectChannel(id) {
  setSelected(channelById(id).id);
}

export function refreshTopology() {
  try { setScreenList(screens()); } catch (e) { setScreenList([]); }
  try { setWindowList(windows()); } catch (e) { setWindowList([]); }
}

// 通道历史（副本，旧 → 新）。
export function historyFor(id) {
  const r = rings[id];
  return r ? r.toArray() : [];
}

// 通道最新一个样本值（空 → undefined）。
export function latestFor(id) {
  const r = rings[id];
  return r ? r.last() : undefined;
}

// 用真实 stats 模块算滚动统计。空历史 → null（stats.describe 对空数组抛 RangeError）。
export function statsFor(id) {
  const values = historyFor(id);
  if (values.length === 0) return null;
  return stats.describe(values);
}

// 某通道当前告警档位。
export function levelFor(id) {
  const ch = channelById(id);
  const v = latestFor(id);
  return classify(v === undefined ? NaN : v, ch.warn, ch.danger);
}

export {
  CHANNELS,
  snap,
  revision,
  selected,
  paused,
  samples,
  screenList,
  windowList,
  alerts,
  setScreenList,
  setWindowList,
};

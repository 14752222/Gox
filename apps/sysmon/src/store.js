// 应用状态：采样环形缓冲 + 设备订阅。signal 用索引取值（引擎纪律）。
import { createSignal } from "gox";
import { deviceInfo, useBattery, useNetwork } from "gx/device";
import { makeRing, nextWalk } from "./lib/ring.js";

const CAP = 60; // 60 个采样点 = 一分钟窗口

const cpuRing = makeRing(CAP);
const memRing = makeRing(CAP);

const cpuP = createSignal([]);
const memP = createSignal([]);
const tickP = createSignal(0);

const cpu = cpuP[0];
const setCpu = cpuP[1];
const mem = memP[0];
const setMem = memP[1];
const tick = tickP[0];

// 设备信息（一次性读）+ 电池/网络订阅（宿主不支持时给空对象的降级由 gx/device 保证）。
const info = deviceInfo() || {};
const battery = useBattery();
const network = useNetwork();

let lastCpu = 30;
let lastMem = 45;

// 每秒一个采样点。桌面无真传感器：随机游走充当数据源，
// 数据形状与真上报一致（0..100 百分比）。
setInterval(() => {
  lastCpu = nextWalk(lastCpu, 9);
  lastMem = nextWalk(lastMem, 5);
  cpuRing.push(lastCpu);
  memRing.push(lastMem);
  setCpu(cpuRing.values().slice());
  setMem(memRing.values().slice());
  tickP[1](tick() + 1);
}, 1000);

// infoLine 设备信息摘要。
const infoLine = () => {
  const parts = [];
  if (info.platform) parts.push(info.platform);
  if (info.model) parts.push(info.model);
  if (info.osVersion) parts.push(info.osVersion);
  return parts.length > 0 ? parts.join(" · ") : "桌面宿主（无真设备信息）";
}

// batteryText / batteryLevel：useBattery 返回取值函数，形状 {level, charging}。
const batteryText = () => {
  const b = battery() || {};
  const lv = typeof b.level === "number" ? Math.round(b.level * 100) + "%" : "--";
  return (b.charging ? "⚡ 充电中 " : "") + lv;
}

const batteryOk = () => {
  const b = battery() || {};
  return typeof b.level === "number" && b.level > 0.2;
}

// networkText：{type, online}。
const networkText = () => {
  const n = network() || {};
  if (n.online === false) return "离线";
  return n.type ? String(n.type) : "在线";
}

const networkOk = () => {
  const n = network() || {};
  return n.online !== false;
}

export { cpu, mem, infoLine, batteryText, batteryOk, networkText, networkOk };

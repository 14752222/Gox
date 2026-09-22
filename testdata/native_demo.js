// gx/device · gx/geo · gx/media 演示: 统一应用能力 API。
//
// 运行: go run . testdata/native_demo.js
//
// 现象: 一个设备信息面板 —— 顶部显示平台/系统/机型/设备 ID (device.info),
//   电池卡片显示电量与充电状态 (battery, 响应式: 宿主 ReportBattery 时自动刷新),
//   网络卡片显示连接类型 (network, 同样响应式), 定位卡片显示最近一次定位
//   (getLocation / watchLocation 两种取法), 底部一行能力检测 (canIUse)。
//
// 本脚本覆盖三种调用形态, 这是统一应用 API 层的设计核心:
//
//   1. 拉取型 (device.info) —— 同步可得, 直接读返回值;
//   2. Promise 型 (getLocation) —— 要等宿主 (可能弹权限框), await 拿结果,
//      失败走 catch (errCode 是跨平台统一词汇);
//   3. 流式 (watchLocation) —— 宿主持续推送, 每次回调都带新位置;
//
//   以及**响应式取值函数** (useBattery / useNetwork): 放进渲染路径的函数子节点,
//   宿主一 Report, 那一处自动重算 —— 应用不需要写任何订阅/取消订阅代码。
//
// 桌面宿主 (win32) 与移动宿主都实现同一个 NativeHost 契约, 所以这份脚本
// 在 Windows 桌面与 Android/iOS 上跑的是**同一份** —— 差异只在宿主报了
// 什么 (桌面没有定位 → getLocation 会 reject, canIUse("location") 为 false)。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";
import {
  deviceInfo,
  battery,
  useBattery,
  network,
  useNetwork,
  isOnline,
  canIUse,
  capabilities,
} from "gx/device";
import { getLocation, watchLocation, clearAllWatches, lastLocation } from "gx/geo";

const [locText, setLocText] = createSignal("(not requested)");
const [watchText, setWatchText] = createSignal("(not started)");
const [capText, setCapText] = createSignal("");

function fmtLoc(loc) {
  if (!loc) return "(no fix)";
  return `${loc.latitude.toFixed(4)}, ${loc.longitude.toFixed(4)}${loc.mocked ? " [mocked]" : ""}`;
}

// 拉取型: device.info 同步可得
const dev = deviceInfo();

// Promise 型: getLocation 要等宿主, 失败走 catch (errCode 统一词汇)
function onLocate() {
  setLocText("locating...");
  getLocation({ highAccuracy: true })
    .then((loc) => setLocText(fmtLoc(loc)))
    .catch((e) => setLocText(`error: ${e.errCode}`));
}

// 流式: watchLocation 持续推送
function onWatch() {
  setWatchText("watching...");
  watchLocation((loc) => setWatchText(fmtLoc(loc)));
}
function onStopWatch() {
  clearAllWatches();
  setWatchText("(stopped)");
}

// 能力检测: 宿主报了什么、内核有什么, 一目了然
function onCheck() {
  const list = capabilities().join(", ");
  setCapText(
    `location: ${canIUse("location")} | camera: ${canIUse("camera")} | battery: ${canIUse("battery")}\n${list}`
  );
}

render(
  <window title="Native API demo" width={420} height={460}>
    <column gap={8} padding={16}>
      <text font={18}>Device & platform</text>
      <text>{() => `platform: ${dev.platform} | os: ${dev.os} ${dev.osVersion}`}</text>
      <text>{() => `model: ${dev.model} | arch: ${dev.arch}`}</text>
      <text>{() => `deviceId: ${dev.deviceId.slice(0, 24)}...`}</text>

      <text font={18}>Battery (reactive)</text>
      {/* 响应式取值: 宿主一 Report, 这处自动重算, 应用零订阅代码 */}
      <text>
        {() => {
          const b = useBattery();
          return b.supported
            ? `level: ${b.levelPercent}% | ${b.charging ? "charging" : "on battery"}`
            : "no battery (desktop)";
        }}
      </text>
      <text>{() => `battery() snapshot: ${battery().levelPercent}%`}</text>

      <text font={18}>Network (reactive)</text>
      <text>
        {() => {
          const n = useNetwork();
          return `connected: ${n.connected} | type: ${n.type}${n.connected ? "" : " (offline)"}`;
        }}
      </text>
      <text>{() => `isOnline: ${isOnline()}`}</text>

      <text font={18}>Location</text>
      <row gap={8}>
        <button onClick={onLocate}>Locate once</button>
        <button onClick={onWatch}>Watch</button>
        <button onClick={onStopWatch}>Stop watch</button>
      </row>
      <text>{() => `one-shot: ${locText()}`}</text>
      <text>{() => `watch: ${watchText()}`}</text>
      <text>{() => `lastLocation: ${lastLocation() ? fmtLoc(lastLocation()) : "(none)"}`}</text>

      <button onClick={onCheck}>Check capabilities</button>
      <text font={12}>{() => capText()}</text>
    </column>
  </window>
);

// 设备能力 API 方案 A 演示: gx/storage 应用级 kv 持久化 (2026-09-19 拍板落地)。
// 运行: go run . testdata/storage_demo.js
// 现象:
//   1. 点击加一/切换主题, 关掉窗口再跑一次 —— 计数和主题都还在:
//      数据落在 %APPDATA%/Gox/storage-demo/storage.json (拍板的应用目录约定),
//      换脚本就是换应用, 互不串数据。
//   2. "清除并退出" 按钮: clearStorage 后退出, 下次启动回到初始状态。
//
// 要点:
//   - setAppName 要在任何 storage 调用之前 (缺省从脚本文件名推, 但显式写
//     明是兼容承诺 —— 目录名一旦发布就是应用的身份证)。
//   - 值经 JSON 序列化, 对象/数组/嵌套都能存; getStorage 不存在的键返回
//     undefined 而不是抛错 (写"不存在就初始化"不需要 try/catch)。
//   - 全同步 + 写穿透: 每次 set 立即落盘 (临时文件 + rename 原子替换),
//     没有"退出前要 flush"这种隐藏约定。
import { createSignal } from "gx/solid";
import { setAppName, setStorage, getStorage, clearStorage } from "gx/storage";
import { h, render } from "gx/gfx";

setAppName("storage-demo");

const [count, setCount] = createSignal(getStorage("count") || 0);
const [dark, setDark] = createSignal(getStorage("theme") === "dark");

const save = () => {
  setStorage("count", count());
  setStorage("theme", dark() ? "dark" : "light");
};

const face = () => (dark() ? "#242b33" : "#ffffff");
const ink = () => (dark() ? "#dfe6ee" : "#1c2430");

render(
  <window title="gx/storage demo" width={420} height={280}>
    <column gap={12} padding={18} background={face}>
      <text font={17} color={ink}>{() => `count = ${count()} (persisted)`}</text>
      <text font={13} color={ink}>{() => `theme = ${dark() ? "dark" : "light"} (persisted)`}</text>

      <row gap={10}>
        <button onClick={() => { setCount(count() + 1); save(); }}>+1 &amp; save</button>
        <button onClick={() => { setDark(!dark()); save(); }}>toggle theme</button>
      </row>

      <text font={12} color={ink}>
        Close the window and run this demo again - state survives via storage.json.
      </text>
      <button onClick={() => { clearStorage(); }}>clear storage (quit to see reset)</button>
    </column>
  </window>
);

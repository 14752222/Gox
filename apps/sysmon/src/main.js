// 入口：建主窗口 + 挂根组件 + 启动采样循环。
//
// 运行：/f/tmp/gox-b10.exe apps/sysmon/src/main.js
// 多屏模式自动化触发：/f/tmp/gox-b10.exe apps/sysmon/src/main.js --multiscreen
import { h, render } from "gox";
import { App } from "./app.js";
import { startSampling, refreshTopology } from "./store.js";
import { openScreenWindows } from "./multiscreen.js";

render(
  <window title="系统资源监视器" width={1060} height={760}>
    <App />
  </window>
);

// 窗口建好后再读拓扑（windows() 需要至少一个窗口）。
refreshTopology();
startSampling();

// --multiscreen：启动即展开多屏/副窗（供自动化验证多窗口生命周期）。
const argv = process.argv || [];
let wantMulti = false;
for (let i = 0; i < argv.length; i = i + 1) {
  if (argv[i] === "--multiscreen") wantMulti = true;
}
if (wantMulti) openScreenWindows();

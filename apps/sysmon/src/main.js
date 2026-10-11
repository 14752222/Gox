// 入口：建窗口 + 挂根组件。
import { h, render } from "gox";
import { App } from "./app.js";

render(
  <window title="系统监控" width={760} height={560}>
    <App />
  </window>
);

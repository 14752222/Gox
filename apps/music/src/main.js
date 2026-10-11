// 入口：建窗口 + 挂根组件。
import { h, render } from "gox";
import { App } from "./app.js";

render(
  <window title="音乐" width={720} height={560}>
    <App />
  </window>
);

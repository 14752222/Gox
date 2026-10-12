// 入口：建窗口 + 挂根组件。
import { h, render } from "gox";
import { App } from "./app.js";

render(
  <window title="设置" width={640} height={600}>
    <App />
  </window>
);

// 入口：移动端尺寸窗口。
import { h, render } from "gox";
import { App } from "./app.js";

render(
  <window title="计算器" width={390} height={720}>
    <App />
  </window>
);

// 入口：移动端尺寸窗口（390×720，竖屏）。
import { h, render } from "gox";
import { App } from "./app.js";

render(
  <window title="记事" width={390} height={720}>
    <App />
  </window>
);

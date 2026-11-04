// 入口：建窗口 + 挂根组件。
//
// 运行：./gox apps/devtools/src/main.js
// 建议配合 GOX_DEV=1（内核诊断警告也会进 devSnapshot().warnings / console 留存）。
import { h, render } from "gox";
import { App } from "./app.js";

render(
  <window title="Gox DevTools" width={1024} height={760}>
    <App />
  </window>
);

// 入口：建窗口 + 挂根组件。
//
// 运行：go build -o F:/tmp/gox-current.exe ./cmd/gox && F:/tmp/gox-current.exe apps/json-toolbox/src/main.js
import { h, render } from "gox";
import { App } from "./app.js";

render(
  <window title="JSON 工具箱" width={960} height={700}>
    <App />
  </window>
);

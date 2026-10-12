// 入口：建窗口 + 挂根组件。
//
// 运行：
//
//	go build -o /tmp/gox-current ./cmd/gox
//	/tmp/gox-current apps/files/src/main.js
import { h, render } from "gox";
import { App } from "./app.js";

render(
  <window title="文件" width={860} height={600}>
    <App />
  </window>
);

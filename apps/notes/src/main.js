// 入口：建窗口 + 挂根组件。
//
// 运行：
//
//	go build -o /tmp/gox-current ./cmd/gox
//	/tmp/gox-current apps/notes/src/main.js
//
// 无 X server 的 Linux 上用 `xvfb-run -a` 包一层（GUI 后端要一个 display）。
import { h, render } from "gox";
import { App } from "./app.js";

render(
  <window title="笔记 —— Markdown + 图表" width={820} height={640}>
    <App />
  </window>
);

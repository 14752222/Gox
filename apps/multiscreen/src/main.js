// 入口：建 home / road 两个窗口（句柄在 render 后才拿得到，用 let 占位）。
import { h, render } from "gox";
import { Panel } from "./app.js";

let homeHandle = null;
let roadHandle = null;

homeHandle = render(
  <window title="multiscreen · home" width={480} height={420}>
    <Panel title="home 窗口" handleRef={() => homeHandle} peer={() => roadHandle} />
  </window>
);

roadHandle = render(
  <window title="multiscreen · road" width={480} height={420}>
    <Panel title="road 窗口" handleRef={() => roadHandle} peer={() => homeHandle} />
  </window>
);

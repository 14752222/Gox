// 入口：一个窗口，四个折叠屏场景。
//
// 窗口开到 900×760（够放下"半折竖屏"的宽度档），但**列数由窗口真实宽度决定** ——
// 把窗口拖窄到 600 以下，S1 会自动从双栏变单栏，这份代码一个字都不用改。
import { h, render } from "gox";
import { App } from "./app.js";
import { bindWindow } from "./store.js";

const win = render(
  <window title="foldable-demo · 折叠屏适配" width={900} height={760}>
    <App />
  </window>
);

// 窗口句柄要 render 之后才拿得到，读数一律按窗口取（多窗口时各读各的）。
bindWindow(win);

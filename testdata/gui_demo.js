// P2 集成演示: 自研软件渲染器 + signals 响应式。
// 运行: go run . testdata/gui_demo.js
// 现象: 弹出 400x300 窗口, 点击绿色块, 红色条变宽 (count*20 像素)。
import { createSignal } from "gx/solid";
import { h, window, render } from "gx/gfx";

const [count, setCount] = createSignal(0);

render(
  <column gap={10} padding={16}>
    <rect width={() => count() * 20} height={24} background="#c0392b"/>
    <rect width={200} height={32} background="#27ae60" onClick={() => setCount(c => c + 1)}/>
  </column>,
  window({ title: "Gox GUI", width: 400, height: 300 })
);

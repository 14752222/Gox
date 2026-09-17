// P1 集成演示: JSX 语法降级 + gx/solid 响应式。
// 运行: go run . testdata/jsx_demo.js
import { createSignal, createEffect } from "gx/solid";

// debug 版 h: 构建纯数据节点树 (P2 会换成真正的渲染元素树)
function h(tag, props, ...children) { return { tag, props, children }; }

const [count, setCount] = createSignal(0);

const ui =
  <column gap={8}>
    <text font={20}>{() => `count: ${count()}`}</text>
    <button onClick={() => setCount(c => c + 1)}>加一</button>
  </column>;

console.log(JSON.stringify(ui, null, 2));

createEffect(() => console.log("count is", count()));
setCount(5);

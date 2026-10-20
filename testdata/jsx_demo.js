// P1 集成演示: JSX 语法降级 + gx/solid 响应式。
// 运行: ./gox testdata/jsx_demo.js
import { createSignal, createEffect } from "gx/solid";

// debug 版 h: 构建纯数据节点树 (P2 起可换成真正的渲染元素树)
function h(tag, props, ...children) { return { tag, props, children }; }

const [count, setCount] = createSignal(0);

// 插值子节点自动响应式: {count()} 在解析期被包成 () => count(), signal 变了文本跟着变,
// 不必再手写 {() => ...} (显式函数子节点也仍然支持)。
// 注意注释要写在 JSX **外面或者说用 {/* */}**: 子节点区的 `//` 不是注释, 会被当成文本。
const ui =
  <column gap={8}>
    <text font={20}>计数：{ count()}</text>
    <button onClick={() => setCount(c => c + 1)}>加一</button>
  </column>;

console.log(JSON.stringify(ui, null, 2));

createEffect(() => console.log("count is", count()));
setCount(5);

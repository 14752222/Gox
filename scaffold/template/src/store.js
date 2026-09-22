// 应用状态。
//
// 跨组件共享状态的**全部机制**就是"把 signal 建在模块作用域"—— 没有额外的
// store 框架。组件读 signal 即建立依赖，写 signal 即触发重渲染。
//
// 派生值（"还有几条没做完"这种）**不用单独存一份**，写成读 signal 的普通函数即可：
// 第二份状态一定会和第一份对不上。
//
// 注：当前页签/页面这类**导航状态**不在这里 —— 它归 `gx/router` 的路由表管
// （见 app.js），不是一个手写的 signal。
import { createSignal } from "gox";

const [todos, setTodos] = createSignal([
  { id: "t1", title: "npm run dev 看一眼窗口", done: true },
  { id: "t2", title: "读 src/components/todo-list.js", done: false },
  { id: "t3", title: "把 theme.js 换成自己的配色", done: false },
]);
const [draft, setDraft] = createSignal("");
const [note, setNote] = createSignal("就绪");

// 解构出来的 signal 用 export {} 一起导出（export const [a, b] = ... 这种写法不走）。
export { todos, setTodos, draft, setDraft, note, setNote };

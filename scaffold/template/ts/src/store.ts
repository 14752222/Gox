// 应用状态（TypeScript 版）。
//
// 跨组件共享状态的**全部机制**就是"把 signal 建在模块作用域"—— 没有额外的
// store 框架。组件读 signal 即建立依赖，写 signal 即触发重渲染。
//
// 派生值（"还有几条没做完"这种）**不用单独存一份**，写成读 signal 的普通函数即可：
// 第二份状态一定会和第一份对不上。
//
// 注：当前页签/页面这类**导航状态**不在这里 —— 它归 `gx/router` 的路由表管
// （见 app.tsx），不是一个手写的 signal。
import { createSignal } from "gox";

// 一条待办。interface 放这里（store），组件从这里 import —— 数据形态只有一份。
export interface Todo {
  id: string;
  title: string;
  done: boolean;
}

const [todos, setTodos] = createSignal<Todo[]>([
  { id: "t1", title: "npm run dev 看一眼窗口", done: true },
  { id: "t2", title: "读 src/components/todo-list.tsx", done: false },
  { id: "t3", title: "把 theme.ts 换成自己的配色", done: false },
]);
const [draft, setDraft] = createSignal<string>("");
const [note, setNote] = createSignal<string>("就绪");

// 解构出来的 signal 用 export {} 一起导出（export const [a, b] = ... 这种写法不走）。
export { todos, setTodos, draft, setDraft, note, setNote };

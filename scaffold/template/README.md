# __PROJECT_TITLE__

用 Gox 写的桌面 GUI 应用 —— JSX + 信号（signal），产物是单个静态可执行文件。

> 本工程由 `gox create` 生成（Gox 自带的脚手架，等价于前端的 `npm create vite`）。
> 目录布局是脚手架的**默认输出**，一路加功能即可，不需要重新组织。

## 运行

```bash
npm install        # 只为拿到 goxjs 命令（Gox 运行时本身零依赖）
npm run dev        # 等价于 goxjs src/main.js
```

已经全局装过 Gox 的话，也可以直接跑二进制的原始形态：

```bash
goxjs src/main.js
```

## 目录

```
.
├── package.json          # 元信息 + 两个脚本（dev / start）
├── src/
│   ├── main.js           # 入口：建窗口 + 挂根组件
│   ├── app.js            # 根组件：路由表 + 页签（RouterLink）+ 页面出口（RouterView）
│   ├── store.js          # 应用状态：signal 建在模块作用域，组件共享它
│   ├── theme.js          # 设计令牌：颜色 / 间距 / 字号
│   └── components/
│       ├── counter.js    # 局部状态 + 受控滑块
│       ├── todo-list.js  # 列表（each 指令 + keyed 复用）+ 输入绑定
│       └── status-bar.js # 派生值 + Switch / Match 多分支 + useRoute 读本页路由
└── .gitignore
```

## 这套模板里演示了什么

| 特性 | 在哪 | 要点 |
|---|---|---|
| 信号与响应式文本 | `counter.js` | `createSignal` → 文本子节点传函数 `{() => ...}` |
| 响应式 prop | `counter.js` | `value={() => ...}` / `background={() => ...}`，**传函数才会更新** |
| 列表 | `todo-list.js` | `<view each={todos} key="id">` —— keyed 复用，行内状态保留 |
| 双向绑定 | `todo-list.js` | `<input model={draft} />` 一条指令接好读写（等价 `v-model`） |
| 页面路由 | `app.js` | `createRouter` + `<RouterLink>` + `<RouterView>` —— 页签就是路径 |
| 高亮当前页 | `app.js` | `RouterLink` 的 `activeBackground`：命中当前路由自动换底色 |
| 条件显隐 | `app.js` | `<view show={() => here() !== "/"}>` —— keep-alive，隐藏不销毁 |
| 读本页路由 | `status-bar.js` | `useRoute()` 写在页面体里（多窗口下别在窗口根读 `currentRoute()`） |
| 多分支 | `status-bar.js` | `<Switch>` + `<Match when={...}>` |
| 设计令牌 | `theme.js` | 组件只引用令牌，改一处整体换肤 |
| 跨组件共享状态 | `store.js` | 信号提到模块作用域就是全部机制，没有额外框架 |

三条路由记录都带了 `keepAlive: true`（与 `show` 的 keep-alive 同义）：在计数器页签调过的
值、在输入框里打的字，切走再切回来都还在。想要"每次进入都全新构建"，删掉那一条的
`keepAlive` 即可。

## 五条最容易踩的坑

1. **用了 JSX 就要 import `h`**：JSX 在 parser 层被降级成 `h("column", {...}, ...)` 调用，
   所以**每个用了 JSX 的文件**都要有 `h` 在作用域里（`import { h, render } from "gox"`）。
   只 import `render` 也能编译，但挂载时会当场 `ReferenceError: h is not defined`。
2. **响应式的东西一律传函数**：`value` / `disabled` / `background` / `each` / `show` / `when` 收到的是
   **取值函数**。写成快照（`disabled={count() === 0}`、`each={todos()}`）只有第一帧是对的 —— 之后
   信号再变也不会重渲染。内核会就非法形态打一条警告（去重），行为降级但不静默。
3. **路由记录的 `component` 要写组件函数本身**：`{ path: "/todos", component: TodoList }` ✓。
   写成 `component: TodoList()` 是**当场调用** —— 等于在定义路由表那一刻就把所有页面的组件体
   全部跑了一遍（局部 signal 提前建好、懒构建失效）。路由器要的是"给我一个函数"，
   进入页面时它自己会调。
4. **条件渲染不要指望"先销毁再挂回来"**：静态子树被 `dispose` 后响应式接线就断了，同一个元素
   对象重新挂回去只是"看着一样但不再响应式"的死树。要么在函数体里新建元素
   （`{() => cond() ? <A/> : null}`），要么用 keep-alive（`show` 指令的隐显，或路由记录的
   `keepAlive: true`）。
5. **`view` 只有一个子节点时，写它身上的 `padding` 等于没写**：`view` 是布局透明的容器，
   单子时子节点直接占满它的盒子 —— padding 既不进固有尺寸也不缩子节点（不报错，只是没效果）。
   页签的内边距因此写在 `RouterLink` **里面**的 `<row padding={5}>` 上（见 `app.js`）。

## 下一步

- 完整 API：`docs/gui-guide.md`（内置元素、布局、事件、宿主能力的权威参考）
- **路由**：本模板用的就是内置模块 `gx/router`（页签 = 路径）。参数路由（`/detail/:id`）、
  守卫、历史栈、懒加载、多窗口与折叠双栏这些进阶能力，手册见 `docs/gui-router.md`。
- 用户态模式：`docs/gui-patterns.md`（状态、主题、屏幕适配等惯用法）
- 持久化：`gx/storage` 的 `setAppName` / `setStorage` / `getStorage`
- 系统能力：`gx/dialog` 的 `alert` / `confirm` / `openFile`（async，用 `async function`，
  运行时**不支持** `async () => {}`）
- 打包成单文件可执行程序：仓库里的 `packager`（`jsbuild <入口.js> --gui -o app.exe`）

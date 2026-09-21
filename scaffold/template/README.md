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
│   ├── app.js            # 根组件：页签 + 面板组合
│   ├── store.js          # 应用状态：signal 建在模块作用域，组件共享它
│   ├── theme.js          # 设计令牌：颜色 / 间距 / 字号
│   └── components/
│       ├── counter.js    # 局部状态 + 受控滑块
│       ├── todo-list.js  # 列表（each 指令 + keyed 复用）+ 输入绑定
│       └── status-bar.js # 派生值 + Switch / Match 多状态
└── .gitignore
```

## 这套模板里演示了什么

| 特性 | 在哪 | 要点 |
|---|---|---|
| 信号与响应式文本 | `counter.js` | `createSignal` → 文本子节点传函数 `{() => ...}` |
| 响应式 prop | `counter.js` | `value={() => ...}` / `background={() => ...}`，**传函数才会更新** |
| 列表 | `todo-list.js` | `<view each={todos} key="id">` —— keyed 复用，行内状态保留 |
| 双向绑定 | `todo-list.js` | `<input model={draft} />` 一条指令接好读写（等价 `v-model`） |
| 条件显隐 | `app.js` | `<view show={() => tab() === 0}>` —— keep-alive，隐藏不销毁 |
| 多分支 | `status-bar.js` | `<Switch>` + `<Match when={...}>` |
| 设计令牌 | `theme.js` | 组件只引用令牌，改一处整体换肤 |
| 跨组件共享状态 | `store.js` | 信号提到模块作用域就是全部机制，没有额外框架 |

## 四条最容易踩的坑

1. **用了 JSX 就要 import `h`**：JSX 在 parser 层被降级成 `h("column", {...}, ...)` 调用，
   所以**每个用了 JSX 的文件**都要有 `h` 在作用域里（`import { h, render } from "gox"`）。
   只 import `render` 也能编译，但挂载时会当场 `ReferenceError: h is not defined`。
2. **响应式的东西一律传函数**：`value` / `disabled` / `background` / `each` / `show` 收到的是
   **取值函数**。写成快照（`show={tab() === 0}`、`each={todos()}`）只有第一帧是对的 —— 之后
   信号再变也不会重渲染。内核会就非法形态打一条警告（去重），行为降级但不静默。
3. **`each` / `show` 的 children 位置要用 `{Comp}`**：JSX 里大写标签是**当场调用**
   （`<Comp/>` 被降级成 `Comp(...)`），写在指令容器的 children 位置就等于"脚本求值期就跑了一遍
   组件体"。要 `<view show={open}>{Panel}</view>`，不要 `<view show={open}><Panel/></view>`。
4. **条件渲染不要指望"先销毁再挂回来"**：静态子树被 `dispose` 后响应式接线就断了，同一个元素
   对象重新挂回去只是"看着一样但不再响应式"的死树。要么在函数体里新建元素
   （`{() => cond() ? <A/> : null}`），要么用 `show` 的 keep-alive。

## 下一步

- 完整 API：`docs/gui-guide.md`（内置元素、布局、事件、宿主能力的权威参考）
- 用户态模式：`docs/gui-patterns.md`（路由、主题、状态等惯用法）
- 持久化：`gx/storage` 的 `setAppName` / `setStorage` / `getStorage`
- 系统能力：`gx/dialog` 的 `alert` / `confirm` / `openFile`（async，用 `async function`，
  运行时**不支持** `async () => {}`）
- 打包成单文件可执行程序：仓库里的 `packager`（`jsbuild <入口.js> --gui -o app.exe`）

# __PROJECT_TITLE__

用 Gox 写的桌面 GUI 应用 —— TSX + 类型 + 信号（signal），产物是单个静态可执行文件。

> 本工程由 `gox create --ts` 生成（Gox 自带的脚手架，等价于前端的 `npm create vite --template ts`）。
> 目录布局是脚手架的**默认输出**，一路加功能即可，不需要重新组织。

## 运行

```bash
npm install        # 只为拿到 goxjs 命令（Gox 运行时本身零依赖）
npm run dev        # 等价于 goxjs src/main.tsx
```

已经全局装过 Gox 的话，也可以直接跑二进制的原始形态：

```bash
goxjs src/main.tsx
```

## TypeScript 是怎么跑起来的

**没有 node 端构建步骤，也没有"编译产物"这个概念**：`.tsx` / `.ts` 文件在 gox
加载时自动完成类型剥离（内嵌 esbuild 转译，JSX 原样保留给引擎自己的降级管线），
然后直接进引擎。也就是说：

- 源码即运行码 —— 改完保存就是新的，没有 watch-compile 链路要伺候；
- 类型只存在于"类型世界"：IDE 检查（`src/gox.d.ts` + `tsconfig.json` 已配好）
  与引擎运行互不干扰，类型标注错了 IDE 画红线，运行时零成本；
- `import` 写真实后缀（`./app.tsx` / `../theme.ts`）—— 与 TS 官方 ESM 风格一致。

## 目录

```
.
├── package.json          # 元信息 + 两个脚本（dev / start）
├── tsconfig.json         # IDE / tsc 检查配置（引擎不读它）
├── src/
│   ├── gox.d.ts          # gox API 与 JSX 元素的类型声明（宽松索引签名）
│   ├── main.tsx          # 入口：建窗口 + 挂根组件
│   ├── app.tsx           # 根组件：路由表 + 页签（RouterLink）+ 页面出口（RouterView）
│   ├── store.ts          # 应用状态：signal 建在模块作用域，组件共享它（含 Todo interface）
│   ├── theme.ts          # 设计令牌：颜色 / 间距 / 字号（as const，键名即类型）
│   └── components/
│       ├── counter.tsx    # 局部状态 + 受控滑块
│       ├── todo-list.tsx  # 列表（each 指令 + keyed 复用）+ 输入绑定
│       └── status-bar.tsx # 派生值 + Switch / Match 多分支 + useRoute 读本页路由
└── .gitignore
```

## 这套模板里演示了什么

| 特性 | 在哪 | 要点 |
|---|---|---|
| 类型与运行时分离 | 全部 | 注解/接口在加载时剥离，IDE 检查 + 运行零成本并行 |
| signal 泛型 | `counter.tsx` | `createSignal<number>(0)` —— setter 回调参数自动推断 |
| 字面量联合类型 | `status-bar.tsx` | `type Phase = "ready" \| "loading" \| "error"`，拼错状态名当场红线 |
| 数据形态单点声明 | `store.ts` | `interface Todo` 放 store，组件 import 共用 |
| 令牌 as const | `theme.ts` | 颜色键名收成字面量类型，`ColorToken` 等导出复用 |
| 列表 | `todo-list.tsx` | `<view each={todos} key="id">` —— keyed 复用，行内状态保留 |
| 双向绑定 | `todo-list.tsx` | `<input model={draft} />` 一条指令接好读写（等价 `v-model`） |
| 页面路由 | `app.tsx` | `createRouter` + `<RouterLink>` + `<RouterView>` —— 页签就是路径 |
| 高亮当前页 | `app.tsx` | `RouterLink` 的 `activeBackground`：命中当前路由自动换底色 |
| 条件显隐 | `app.tsx` | `<view show={() => here() !== "/"}>` —— keep-alive，隐藏不销毁 |
| 读本页路由 | `status-bar.tsx` | `useRoute()` 写在页面体里（多窗口下别在窗口根读 `currentRoute()`） |
| 设计令牌 | `theme.ts` | 组件只引用令牌，改一处整体换肤 |
| 跨组件共享状态 | `store.ts` | 信号提到模块作用域就是全部机制，没有额外框架 |

三条路由记录都带了 `keepAlive: true`（与 `show` 的 keep-alive 同义）：在计数器页签调过的
值、在输入框里打的字，切走再切回来都还在。想要"每次进入都全新构建"，删掉那一条的
`keepAlive` 即可。

## 五条最容易踩的坑

1. **JSX 的渲染工厂 `h` 会自动补上**：JSX 在 parser 层被降级成 `h("column", {...}, ...)`
   调用，所以文件里要有一个 `h`。**文件里没绑定时编译器会自动补一条
   `import { h } from "gx/gfx"`** —— 这条对 .tsx 同样生效（esbuild 剥掉未使用的
   `h` 导入也无所谓，编译器会补回来）。显式写 `import { h, render } from "gox"`
   仍然推荐；自己定义/导入的 `h` 不会被顶掉。
2. **响应式的东西一律传函数**：`value` / `disabled` / `background` / `each` / `show` / `when` 收到的是
   **取值函数**。写成快照（`disabled={count() === 0}`、`each={todos()}`）只有第一帧是对的 —— 之后
   信号再变也不会重渲染。内核会就非法形态打一条警告（去重），行为降级但不静默。
   **子节点也一样**：`共 {todos().length} 条` 是快照（在 `h()` 之前就求值完了），要写成
   ``{() => "共 " + todos().length + " 条"}`` —— 而且**这条没有警告**，只能靠纪律。
3. **路由记录的 `component` 要写组件函数本身**：`{ path: "/todos", component: TodoList }` ✓。
   写成 `component: TodoList()` 是**当场调用** —— 等于在定义路由表那一刻就把所有页面的组件体
   全部跑了一遍（局部 signal 提前建好、懒构建失效）。路由器要的是"给我一个函数"，
   进入页面时它自己会调。
4. **`import` 写真实后缀**：`./app.tsx` / `../theme.ts` / `./store.ts`。写 `./app.js`
   也能解析（会回落到 `app.tsx`），但别依赖 —— 真实后缀与 TS 官方 ESM 风格一致，
   IDE 跳转最顺。
5. **`view` 只有一个子节点时，写它身上的 `padding` 等于没写**：`view` 是布局透明的容器，
   单子时子节点直接占满它的盒子 —— padding 既不进固有尺寸也不缩子节点（不报错，只是没效果）。
   页签的内边距因此写在 `RouterLink` **里面**的 `<row padding={5}>` 上（见 `app.tsx`）。

## 下一步

- 完整 API：`docs/gui-guide.md`（内置元素、布局、事件、宿主能力的权威参考）
- **路由**：本模板用的就是内置模块 `gx/router`（页签 = 路径）。参数路由（`/detail/:id`）、
  守卫、历史栈、懒加载、多窗口与折叠双栏这些进阶能力，手册见 `docs/gui-router.md`。
- 用户态模式：`docs/gui-patterns.md`（状态、主题、屏幕适配等惯用法）
- 持久化：`gx/storage` 的 `setAppName` / `setStorage` / `getStorage`
- 系统能力：`gx/dialog` 的 `alert` / `confirm` / `openFile` / `saveFile`（async，用 `async function`
  或 async 箭头 `async () => {}` 都行）
- 打包成单文件可执行程序：`gox build windows|macos|android|ios`

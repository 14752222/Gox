# @goxjs/goxjs

> Gox JavaScript Runtime — 用 Go 从零实现的 JavaScript 运行时：词法分析 → 语法分析 → 字节码编译 → 栈式虚拟机执行。单二进制、零外部依赖。

本包把 Gox 的各平台二进制分发到 npm，安装后直接获得 `goxjs` 命令。

## 安装

```bash
npm i -g @goxjs/goxjs
```

## 使用

**REPL：**

```bash
goxjs
```

```
Gox REPL (ES6 subset, no var)
Type :exit to quit, :help for help

> let x = 10
> [1, 2, 3].map(v => v * 2)
  [2, 4, 6]
```

**运行脚本：**

```bash
goxjs app.js
```

**免安装直接跑：**

```bash
npx goxjs app.js
```

支持 ES6+ 语言子集（`let`/`const`、class、`async`/`await`、解构、模板字符串、ES 模块），内置 `Array` / `Map` / `Set` / `Promise` / `BigInt` / `Temporal` 等标准对象，以及 `fs` / `http` / `fetch` / `path` / `process` 等 Node 风格宿主 API。

## 声明式界面（GUI）

`gx/gfx` 是纯 Go 自绘的窗口层，界面用 JSX 写。绑定、列表、条件都是**元素级指令**（写在元素上，不需要 import）：

```jsx
import { h, render, createSignal, Switch, Match } from "gox";

const [name, setName]   = createSignal("");
const [rows, setRows]   = createSignal([{ id: "a", title: "Alpha" }]);
const [open, setOpen]   = createSignal(true);
const [phase, setPhase] = createSignal("loading");

render(
  <window title="app" width={420} height={260}>
    <column gap={8} padding={12}>
      <input model={name} />                                                  {/* 双向绑定 */}
      <view each={rows} key="id">{(row) => <text>{row.title}</text>}</view>   {/* 列表: keyed 复用 */}
      <view show={open} fallback={<text>已隐藏</text>}>可见时显示这里</view>  {/* 条件: 保活显隐 */}
      <Switch fallback={<text>未知状态</text>}>                               {/* 多分支 */}
        <Match when={() => phase() === "loading"}><text>加载中…</text></Match>
      </Switch>
    </column>
  </window>
);
```

- `model` —— 受控组件的双向绑定（等价 Vue 的 `v-model`）：`input` / `textarea` / `slider` / `select` 读写 `value`，`checkbox` / `switch` 读写 `checked`，`radio` 选中时把 `value` 属性写进 model；也可传 `[get, set]` 二元组接自定义来源。
- `each` —— 列表循环：**同 key + 同行引用 + 同下标**的行原样复用（行内输入框、滚动位置、局部 signal 都留着），只重建真正变了的行；支持 `stable` / `fallback` / `key="id"`。
- `show` —— 条件显隐，**保活**：隐藏只把子树摘出布局流，再显示瞬间切回（不是销毁重建）。
- `view` —— 布局透明的容器（Fragment）；指令写在哪个元素上就重复/显隐哪个元素。

> **0.3.0 起是破坏性变更**：`<For>` / `<Show>` 组件已移除 ——
> `<For each={x} key={k}>…</For>` → `<view each={x} key={k}>…</view>`，
> `<Show when={x} fallback={f}>…</Show>` → `<view show={x} fallback={f}>…</view>`。
> `gx/view` 现在只导出 `Switch` / `Match`。

GUI 示例（仓库 `testdata/` 下有 35+ 个可直接运行的演示）：

```bash
goxjs testdata/view_demo2.js      # 列表复用 / 条件保活 / 多分支
goxjs testdata/model_demo.js      # 八类受控组件的双向绑定
goxjs testdata/router_demo.js     # 路由表 / 参数匹配 / 三级守卫 / 懒加载
```

## 路由与屏幕（`gx/router` / `gx/screen`）

**0.4.0 新增模块**：路由不用再自己手写 signal 切页了 ——

```jsx
import { h, render } from "gx/gfx";
import { createRouter, RouterView, RouterLink } from "gx/router";

function HomePage()        { return <text font={15}>home</text>; }
function ListPage()        { return <text font={15}>list</text>; }
function DetailPage(props) { return <text font={15}>{"detail id=" + props.param.id}</text>; }

const router = createRouter({
  routes: [
    { path: "/",           name: "home",   component: HomePage },
    { path: "/list",       name: "list",   component: ListPage, keepAlive: true },
    { path: "/detail/:id", name: "detail", component: DetailPage },
    { path: "*",           name: "nf",     component: HomePage },
  ],
  initial: "/",
});

render(
  <window title="app" width={480} height={360}>
    <column gap={8} padding={12}>
      <text font={12}>{() => "route: " + router.currentRoute().path}</text>
      <row gap={10}>
        <RouterLink to="/list"><text font={13}>列表</text></RouterLink>
        <RouterLink to={{ name: "detail", params: { id: 7 } }}><text font={13}>详情</text></RouterLink>
      </row>
      <RouterView />
    </column>
  </window>
);
```

开箱就有：`:param` / `:param?` / `*` 匹配（优先级不依赖声明序）、三级守卫
（`beforeEach` / `beforeEnter` / `beforeRouteLeave` …，可返回 Promise 做异步）、`push` 返回
Promise（`{ok:false, reason}` 表达被拦截）、`Alt+←` / `Alt+→` 后退前进、`lazy(() => import(…))`
懒加载、**两档状态保留**（路由记录 `keepAlive: true` 保住整棵子树；`useRouteState()` 存住值）、
多窗口独立导航栈与 `sync()` 同步组（mirror / share / follow）。

`gx/screen` 提供显示器枚举、窗口所在屏、折叠姿态上报；**半折时 `RouterView` 自动变双栏**
（左栏历史的上一条，通常是列表）。

> 三处与浏览器路由的刻意差异：**没有 URL，也没有 history 模式**（桌面应用没有地址栏，"历史"
> 是内存里的一个栈）；`to` 一律按**绝对路径**解析；正则路径约束 / `alias` / 相对路径不做，
> 参数校验写在 `beforeEnter`。Windows / X11 没有折叠姿态查询 API —— 框架只提供 `reportPosture()`
> 通道而不猜姿态，没人上报就恒为平展屏。
>
> 完整手册：[`docs/gui-router.md`](https://github.com/14752222/Gox/blob/main/docs/gui-router.md)。

完整文档与语言示例见 [GitHub 仓库](https://github.com/14752222/Gox)。

## License

[Apache License 2.0](https://github.com/14752222/Gox/blob/main/LICENSE)

# Gox 导航壳：TabBar / SideNav / AppShell（PC / 移动自适应）

> **定位**：用户态组件库（纯 JS，零内核改动），落点 `testdata/ui/`。
> 同一套声明式 API：移动端渲染为**底部 TabBar**、桌面/宽屏渲染为**侧边导航（SideNav）**，
> 安全区、软键盘、返回键、宽度断点等平台差异由 `AppShell` 统一分派。
> **示例**：`testdata/tabbar_demo.js`（GUI 演示）、`testdata/tabbar_logic_test.js`（纯逻辑）、
> `testdata/tabbar_smoke_test.js`（headless 挂载冒烟）。
> **前置阅读**：[gui-router.md](gui-router.md)（tab 根页 `keepAlive` + 返回键组合写法见 §13）、
> [gui-guide.md](gui-guide.md) §9.6（宿主能力层）。

---

## 1. 快速上手

```js
import { h, render } from "gx/gfx";
import { createRouter, RouterView } from "gx/router";
import { AppShell, createTabs } from "./ui/shell.js";
import { icons } from "./ui/icons.js";

const tabs = createTabs([
  { path: "/home",     title: "首页", icon: icons.home },
  { path: "/discover", title: "发现", icon: icons.chat, badge: () => unread() },
  { path: "/mine",     title: "我的", icon: icons.user },
  { path: "/settings", title: "设置", icon: icons.gear, footer: true }, // 桌面侧栏沉底
]);

const router = createRouter({
  routes: [
    { path: "/home",     name: "home",     component: HomePage,     keepAlive: true },
    { path: "/discover", name: "discover", component: DiscoverPage, keepAlive: true },
    { path: "/mine",     name: "mine",     component: MinePage,     keepAlive: true },
    { path: "/detail/:id", name: "detail", component: DetailPage },   // tab 内详情页才 push
    { path: "*", name: "nf", component: HomePage },
  ],
  initial: "/home",
});

render(
  <window title="demo" width={960} height={640}>
    <AppShell tabs={tabs} router={router} breakpoint={720} content={() => <RouterView />} />
  </window>
);
```

- 业务只声明 tabs；分派、安全区、软键盘、返回键、快捷键全部由 AppShell 承担。
- **tab 根页之间用 `router.replace`**（AppShell 的 `select` 内置），防止来回切 tab 把
  路由栈压到无限深（返回键要点 N 次才出去）；**tab 内详情页才 `push`**。
- tab 根页开 `keepAlive: true`：来回切滚动位置、输入草稿、页面内 signal 原样保留。
- `content` 传**函数**（`() => <RouterView />`）：形态切换（窄 ↔ 宽）时每次求值都
  新建内容元素。直接传 JSX 元素会在切换时复用同一节点对象 —— 引擎里一个节点
  只有一个 `Parent`，会出问题。

## 2. 分派规则（单一函数，可测试）

```
isWide = 窗口内容宽(逻辑像素) ≥ breakpoint(缺省 720)
宽 → row { SideNav | 内容区 }        窄 → column { 内容区 | TabBar }
```

- **宽度是逻辑像素**：`onResize` 给的是**物理像素**，AppShell 先除以
  `useWindowInfo().scale`（gx/screen）再比较。高 DPI 设备直接拿物理像素比断点
  必然错判档位。
- **初始档位**用 `useWindowInfo().width`（与 `onResize` 同口径），首帧即正确，
  不存在"第一帧错、resize 后才对"的窗口期。
- 断点可配（`breakpoint` prop）。常见参考：手机竖屏 360–430（TabBar）、
  iPad 竖屏 768（想让它落 SideNav 就把断点配到 ≤768）、桌面窗口随意。
- 移动宿主分屏/折叠屏展开后的窗口宽，走宿主上报的同一口径，不引入第二套
  档位词汇（`gx/viewport` 的 `widthClass` 可在业务侧自行叠加判断）。
- 纯函数 `resolveNavMode({width, breakpoint})` 与 `toLogical(px, scale)`
  从 `ui/shell.js` 导出，`testdata/tabbar_logic_test.js` 有断言。

## 3. PC 与移动端差异矩阵

| 维度 | 移动端（Android/iOS 宿主） | 桌面端（Windows/Linux） | 处理方式 |
|---|---|---|---|
| 形态 | 底部 TabBar（拇指热区），5 项以内，图标+短标签 | 侧边栏 SideNav（可折叠），图标+长标签，项数不限 | 按平台+宽度断点自动二选一，API 一套 |
| 底部安全区 | 手势条/虚拟导航栏占位（`insets.bottom` ≠ 0） | 恒为 0 | TabBar 内 `paddingBottom = useInsets().bottom`，桌面自动为 0 |
| 软键盘 | 弹起时底栏被盖/顶起，必须隐藏 | 不存在 | `useViewport().keyboard > 0` → `show={false}` 隐藏底栏（keep-alive），收起恢复 |
| 返回键 | Android 返回键先走路由栈，栈空才退出 | 无此概念（关窗口语义） | Shell 挂 `onBackPress`：`router.index() > 0` → `back()` 返回 true；已在 tab 根 → 切回首个 tab；再按放行退出 |
| 悬停/按压 | 无悬停，只有按压 | 悬停提亮自动有，另有选中项左侧指示条 | 组件内置选中态；桌面多画指示条 |
| 键盘快捷键 | 无 | `Ctrl+1..9` 切 tab | AppShell 根节点 `onKeyDown`（见 §6 坑位） |
| 窗口可拉伸 | 固定屏/分屏 | 拖拽 resize 触发 `onResize` | 统一走"逻辑宽断点"分派，折叠屏展开自动切 SideNav |
| 徽标 badge | 常用（未读数） | 也常用 | absolute 叠加 rect+text，`>99` 显示 `99+`，两形态通用 |
| 多窗口 | 单窗口 | 多窗口各自导航栈 | active 信号是 AppShell 实例局部状态（每窗口各跑一遍组件函数，天然不共享）；路由栈隔离沿用 router scope |

## 4. API 参考

### 4.1 `createTabs(defs)`

```js
const tabs = createTabs([{ path, title, icon, badge?, footer? }, ...])
```

| 字段 | 说明 |
|---|---|
| `path` | **必填**，与 `gx/router` 路由表里的路径一致；同时作为 each 的 key |
| `title` | 标签文案（缺省用 path） |
| `icon` | 自绘函数（canvas）**或** image 路径字符串，见 §5 |
| `badge` | `() => number`，signal 驱动；`>0` 显示，`>99` 显示 `99+` |
| `footer` | `true` 时桌面 SideNav 里沉到 `<spacer>` 之后（"设置"类低频项）；移动端仍在底栏 |

返回 `{ tabs, main, footer, indexOf(path) }`；`main/footer` 是按 `footer` 拆好的一份。
AppShell 也接受裸数组（内部归一化）。

### 4.2 `<AppShell>`

| prop | 缺省 | 说明 |
|---|---|---|
| `tabs` | — | `createTabs` 的返回值（或裸数组） |
| `router` | — | `gx/router` 实例；给了才做 replace 联动与返回键 back |
| `breakpoint` | `720` | 宽度断点（逻辑像素） |
| `content` | — | **传函数**，返回内容区元素（通常是 `<RouterView />`） |
| `win` | — | 窗口句柄或 scope 名；多窗口需要精确指认路由作用域时传 |
| `collapsible` | `true` | 桌面形态是否显示折叠开关 |

AppShell 自己持有的实例状态：`active`（当前 tab 下标）、`collapsed`（折叠态，
持久化到 `gx/storage` 键 `gox.sidenav.collapsed`）、`resized`（最近一次窗口尺寸）。

### 4.3 `<TabBar>` / `<SideNav>`（哑组件，可单独复用）

```
<TabBar items active onChange iconSize barHeight activeColor idleColor background borderColor />
<SideNav items footerItems active onChange collapsed onToggleCollapse showToggle
         width collapsedWidth itemHeight accentColor activeBackground />
```

- `items` 项结构同 createTabs 的产出（至少 `key/index/title`，可选 `icon/badge`）。
- TabBar 的 `active` 是**取值函数**（signal getter）；`onChange(i, item)` 下标优先。
- SideNav 的 `collapsed` 是可选取值函数；不传则恒展开。折叠记忆由调用方持久化
  （AppShell 已用 gx/storage 实现），组件本身保持无副作用。
- 底部安全区 / 键盘隐藏在 TabBar+AppShell 组合里自动生效；单独用 TabBar 时
  安全区 padding 仍生效（组件内部读 `useInsets`），键盘隐藏需自备 `show`。

### 4.4 返回键语义（AppShell 内置，仅移动平台注册）

```js
onBackPress(() => {
  if (router.index() > 0) { router.back(); return true; }  // tab 内详情页: 退栈
  if (active() !== 0)     { select(0);      return true; }  // 已在 tab 根: 回首页 tab
  return false;                                            // 首个 tab 根: 放行, 宿主退出
})
```

- `onBackPress` 是全框架唯一"回调返回值有意义"的 API：返回真值 = 已消费，
  宿主不关界面。写错的症状是"按返回直接退 App、路由栈还在"。
- `gx/router` **没有** `canBack()`；能否后退用 `router.index(scope) > 0` 判断
  （`index()` 返回当前栈指针，`history()` 返回整栈）。

## 5. 图标方案（A/B 并存）

`icon` 字段同时接受两种形态，`ui/icons.js` 的 `IconView` 负责分派：

| 形态 | 写法 | 走向 |
|---|---|---|
| 方案 A：canvas 自绘 | `{ icon: icons.home }` | `<canvas onDraw>` 直接落笔，零文件依赖，三平台观感一致 |
| 方案 B：image 资源 | `{ icon: "assets/home.png" }` | `<image src>`，相对**进程工作目录**解析，走 gox.json assets 打包链路 |

内置自绘图标集（24×24 逻辑坐标、按 ctx.width 等比缩放，任意尺寸可用）：
`home / search / user / gear / bell / chat / folder / calendar / heart / plus /
check / arrow-left` 共 12 个。自绘用直线+矩形+圆拼装（内核 canvas 无抗锯齿/路径/变换），
像素风但零依赖；要精致图标就走方案 B 的 image 路径。

## 6. 坑位清单（每一条都对应一次真实的踩法）

1. **`onResize` 是物理像素**。断点比较前必须除以 DPI（`useWindowInfo().scale`），
   否则高 DPI 下 960 逻辑宽的窗口会被当成 1920，永远分派到 SideNav。
2. **键盘 insets 与底部 insets 勿重复相加**。内核 `safeAreaStyle` 已明确
   "相加会顶出一截"（Android 键盘 insets 通常已含导航栏高度）。本组件库的做法是
   键盘弹起直接隐藏底栏，二者根本不会同时生效；自拼 padding 时记得取大不取和。
3. **`useXxx()` 本身就是取值函数**（gx/viewport、gx/screen 同）：调用一次 =
   订阅一次 + 读一次快照。要在响应式读取点**现调现读**
   （`height={() => barHeight + useInsets().bottom}`），把返回值存下来再调用
   拿到的是快照对象，`insets()` 会直接 TypeError。
4. **`show` 是 keep-alive**：隐藏底栏时 tab 状态保留是好事，但 badge 的定时器
   要挂 `onCleanup`，否则后台空转（本组件库的 badge 由外部 signal 驱动，无定时器）。
5. **`onBackPress` 返回值语义**是全框架唯一的"回调返回值有意义"（见 §4.4），
   必须真机验证。
6. **快捷键没有免焦点的用户态通道**。`onKeyDown` 沿"焦点节点 → 祖先链"派发，
   窗口刚打开还没点击过任何节点时可能收不到。要事件泵级（免焦点）匹配，
   在窗口里加一条 `<menubar>` + `<menuitem shortcut="Ctrl+1">`（代价是 26px
   菜单栏高度）；普通应用建议接受"先点一下窗口"的默认行为。
7. **tab 间切换必须 `replace`**。`push` 会让栈无限深：切 20 次 tab 后，
   Android 返回键要按 20 次才能退出。详情页才 push。
8. **`content` 传函数不传元素**。形态切换会重建内容子树；传 JSX 元素会在
   切换时把同一个节点对象挂到新父节点下（一个节点只有一个 `Parent`）。
9. **text 没有 bold**。TabBar 的"选中加粗"用字号 +1 近似（12 vs 11）；
   要真正的粗体得换字体文件（内核 font 是字号继承，无 weight 轴）。
10. **badge 的绘制出盒**。徽标挂在 24×24 图标容器上、超出容器边界，所以挂了
    `escapeClipping`（默认子树裁剪会把它裁成残月）。它因此会在根层级最后绘制，
    别再给它叠更大的 `zIndex` 期待改变相对顺序。

## 7. 真机验收清单（桌面无法覆盖的部分）

- [ ] Android/iOS 手势条机型：底栏 `paddingBottom = insets.bottom` 不被手势条压住
- [ ] 软键盘弹起：底栏隐藏；收起：底栏恢复、tab 状态原样（keep-alive）
- [ ] Android 返回键：详情页 → 退栈；tab 根 → 回首个 tab；首个 tab 根 → 退出
- [ ] 分屏/折叠屏展开：宿主上报新窗口宽后自动切 SideNav（`widthTier` 修正）
- [ ] 高 DPI 机型：断点分派与逻辑宽一致（验证 §6.1）
- [ ] 多窗口（桌面）：各窗口独立 active 与导航栈，互不串扰

## 8. 相关文档

| 文档 | 内容 |
|---|---|
| [gui-router.md](gui-router.md) §13 | tab 场景：keepAlive + replace + 返回键的组合写法 |
| [gui-patterns.md](gui-patterns.md) §11 | 导航壳惯用法（本章的用户态视角摘要） |
| [gui-guide.md](gui-guide.md) §9.6 | 宿主能力层（insets / 键盘 / 返回键的底层语义） |

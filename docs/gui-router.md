# Gox GUI 路由系统 (`gx/router`) 使用手册

> **模块**：`gx/router`（路由）与 `gx/screen`（屏幕信息 / 折叠姿态），均在 `gfx` 包内实现
> （`router.go` / `router_match.go` / `router_view.go` / `screen.go`）。
> **状态**：2026-09-21 落地。渲染内核（布局 / 绘制 / 命中 / 脏矩形）**未改动**，
> 只增加了"窗口号"这一个小身份（见 §8.1）。
> **示例**：`testdata/router_demo.js`（核心）、`testdata/router_page_detail.js`（懒加载模块）、
> `testdata/router_window_demo.js`（多窗口 + 多屏 + 折叠）。
> **前置阅读**：`agent_doc/gui-routing-options.md`（选型对比，本文是它的落地结果）、
> `docs/gui-patterns.md` §1-§2（无路由时代的用户态写法，仍适用于 3 页以内的小工具）。

---

## 1. 它是什么 / 与 Vue3 Router 的对应关系

路由是**模块层能力**：页面注册与匹配、历史栈、守卫、懒加载都在 `gx/router` 里，
页面切换仍然走内核既有的"函数子节点 + keep-alive 分支"机制。所以：

- 没有新的渲染概念（没有 `hidden` prop、没有新标签、没有新布局语义）；
- 出错时的排查路径与手写切页完全一样；
- 内核里其余"路由"字样是**事件/任务派发**的意思（`gfx.Post` 的任务目标），与本模块无关。

| Vue3 Router | `gx/router` | 说明 |
|---|---|---|
| `createRouter({routes,…})` | `createRouter({routes,…})` / `createRouter(routes, opts)` | 两种形态都收 |
| `router.push/replace/back/forward/go` | 同名同义 | 都返回 **Promise**（守卫可异步） |
| `router.currentRoute` (ref) | `router.currentRoute()` | 取值函数（signal 语义），不是属性 |
| `router.resolve(to)` | `router.resolve(to)` | 纯解析，不导航 |
| `<RouterView>` / `<RouterLink>` | 同名同义 | 大写标签 = 当场调用，无需注册 |
| `useRoute()` / `useRouter()` | 同名同义 | |
| `beforeEach` / `afterEach` | 同名同义 | 返回注销函数 |
| `beforeEnter`（路由记录上） | 同名同义 | |
| `beforeRouteEnter/Update/Leave` | 同名同义 | 写在**页面模块的命名导出**上 |
| `props` 路由参数传递 | `props` 支持对象或 `true` | 另外每个页面默认收到平铺的 `param` |
| `keep-alive` | 路由记录 `keepAlive: true` | 语义是"离开时摘出而不销毁" |

### 四处刻意的差异（都写进文档，不藏着）

1. **没有 URL，也没有 history 模式。** 桌面应用没有地址栏，"历史"就是内存里的一个栈
   （memory 语义）。要做 deep-link 时用 `process.argv` 解析初始路径后传给 `initial`。
2. **`to` 一律按绝对路径解析**（不支持 `"./sub"`）。没有"当前 URL 作为基准"的语境，
   相对路径只会引入歧义。
3. **懒加载推荐显式写 `lazy(() => import(…))`。** 直接写 `() => import(…)` 也能跑，
   但**首次**进入时组件级守卫（`beforeRouteEnter` 等）拿不到 —— 理由见 §5。
4. **多了「作用域」（scope）概念**：每个窗口一套独立导航栈，还能把若干作用域绑成同步组。
   浏览器世界里没这个需求（一个标签页一个 document）。

---

## 2. 快速上手

```js
import { h, render } from "gx/gfx";
import { createRouter, RouterView, RouterLink, useRoute } from "gx/router";

function HomePage()   { return <text font={15}>home</text>; }
function ListPage()   { const r = useRoute(); return <text font={15}>{() => "list " + r().path}</text>; }
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
  <window title="demo" width={480} height={360}>
    <column gap={8} padding={12}>
      {/* 顶部实时显示当前路由: 读取就是订阅 */}
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

跑起来就能用的东西：`Alt+←` / `Alt+→` 后退/前进、`router.push` 的 Promise 结果、
`*` 兜底、`:id` 参数、`keepAlive` 的列表页回来时状态原样。
（`go run . testdata/router_demo.js` 是这个例子的完整版。）

---

## 3. 路由表

```js
{ path, name, component, children, meta, redirect, props, beforeEnter,
  keepAlive, dualPane }
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `path` | string | **必填**。见下面的路径语法 |
| `name` | string | 命名路由：`push({name:"detail", params:{id:1}})`、`resolve`。重名时先声明者胜（会出声） |
| `component` | 函数 / `lazy(… )` | 页面组件；不填则这条记录只是分组或重定向 |
| `children` | 数组 | 嵌套路由。子路径不以 `/` 开头时拼在父路径后 |
| `meta` | 对象 | 随路由传递的任意数据（`route().meta`） |
| `redirect` | 字符串 / 对象 / 函数 | 进入这条记录时改去别处（有 8 次上限，防死循环） |
| `props` | 对象 / `true` | 合并进页面 props；`true` = 把路径参数平铺成 props |
| `beforeEnter` | 函数 | 路由级守卫（见 §4） |
| `keepAlive` | bool | 离开时不销毁子树（见 §7） |
| `dualPane` | bool | 折叠态下能否作为一栏（缺省 true，见 §9.3） |

### 3.1 路径语法（v1 支持的全部）

| 写法 | 匹配 | 参数 |
|---|---|---|
| `/items` | 静态段 | — |
| `/items/:id` | 一段非空路径 | `params.id` |
| `/items/:id?` | **只能出现在末尾**；缺省时该段整段消失 | `params.id` 为 `undefined` |
| `/files/*` | 吞掉剩余全部路径 | `params.pathMatch` |
| `/raw/*rest` | 同上，参数名自定 | `params.rest` |
| `/` | 根 | — |
| `*` | 兜底（写在最后） | `params.pathMatch` |

**匹配优先级不依赖声明序**：静态段 > `:param` > `:param?` > 通配。所以
`[{path:"*"}, {path:"/home"}]` 里 `/home` 照样命中它自己那条 —— 这点与直觉一致，
也是刻意与"按声明序取第一个"的做法分道扬镳的地方。

**不做**（有意的边界）：正则约束 `:id(\\d+)`、重复参数、`alias`、大小写不敏感匹配。
需要参数校验就写在 `beforeEnter` 里。

### 3.2 嵌套路由

```js
{ path: "/users", component: UsersLayout, beforeEnter: requireLogin,
  children: [
    { path: "",    component: UserList },   // /users
    { path: ":id", component: UserDetail }, // /users/42
  ] }
```

```js
function UsersLayout() {
  return (
    <column>
      <text>users layout</text>
      <RouterView />   {/* 子路由在这里出现 */}
    </column>
  );
}
```

每一层 `RouterView` 显示 `route().matched` 链上对应的一层（顶层看 `matched[0]`）。
链上超出范围的层渲染为空。

---

## 4. 守卫

三级守卫，执行顺序与 vue-router 一致：

```
1. beforeRouteLeave   当前路由各层，由内向外      —— 离开拦截
2. beforeEach         全局，注册序
3. beforeEnter        目标链上"新出现"的记录，由外向内
4. beforeRouteUpdate  同一条记录但参数变了
5. beforeRouteEnter   目标链各层，由外向内
   ↓ 全部通过
   提交（改栈）→ afterEach → 同步组镜像
```

**时机**：守卫全部通过之前**不动历史栈**。所以"被拦截"的导航不会留下半截状态，
`back()` 被拦下时指针也不会乱。

### 4.1 返回值语义

| 返回 | 结果 |
|---|---|
| `undefined` / `true` / 其它普通值 | 放行 |
| `false` | **中止**（栈不动，`push` 的 Promise 以 `{ok:false, reason:"aborted"}` 解决） |
| 字符串 `"/login"` | 重定向（重新走一遍守卫链，上限 8 次） |
| `{path}` / `{name, params}` | 同上 |
| 抛错 / 返回 Error | 失败：`onError` 收到错误，Promise 被 reject |
| Promise | **异步守卫**：结算后按上面的规则解释其值 |

```js
router.beforeEach((to, from) => {
  if (to.path === "/settings" && !loggedIn()) return "/login";  // 重定向
  if (dirty() && !confirmLeave()) return false;                 // 中止
});

// 异步: 检查远端是否有未提交内容
router.beforeEach(async (to) => {
  return await checkRemote(to.path) ? true : false;
});
```

### 4.2 组件级守卫写在哪

```
(a) 懒加载模块的命名导出      export function beforeRouteLeave(to, from) {}
(b) 组件函数对象上的属性      Home.beforeRouteLeave = (to, from) => {}
(c) 路由记录自身的字段        { path, beforeEnter }
```

`(a)` 与 `(b)` 只是"组件怎么给的"不同（懒加载拿到模块命名空间，非懒加载拿到函数对象），
脚本侧看不到差别。优先级：`(a)/(b)` 优先于同名记录字段。

---

## 5. 懒加载

```js
import { lazy } from "gx/router";

{ path: "/detail/:id", name: "detail", component: lazy(() => import("./pages/detail.js")) }
```

`pages/detail.js`：

```js
import { h } from "gx/gfx";
import { useRoute, useRouteState } from "gx/router";

// 组件级守卫: 命名导出即可, 与 vue-router 的 <script> 导出同一心智
export function beforeRouteEnter(to, from) { console.log("enter", to.path); }

// 页面组件: default 导出
export default function DetailPage(props) {
  const route = useRoute();
  return <text>{() => "detail " + props.param.id + " @ " + route().path}</text>;
}
```

### 5.1 为什么推荐 `lazy()`

导航的守卫链要在**提交之前**拿到组件级守卫，而它只能从"已加载模块的命名导出"里读。
自动识别（`component: () => import(…)` 不包 `lazy`）要等视图第一次调用组件才知道
"这是个返回 Promise 的加载器"，那时守卫链已经跑完 —— 于是那一次进入会**漏掉
`beforeRouteEnter`**。

`lazy()` 把"这是懒的"提前到编译期，导航就会先等模块加载完再收守卫。
不写 `lazy()` 依然可用（首次进入漏组件级守卫，会在 `gx/dev` 的警告缓冲里出声提示）。

### 5.2 加载中与失败

```jsx
<RouterView
  loading={<text font={12} color="#a70">加载中…</text>}
  error={<column gap={4}><text font={12}>加载失败</text><button onClick={() => router.rebuild()}>重试</button></column>}
/>
```

- 加载期间挂 `loading` 分支，失败挂 `error` 分支；两者都不保活（重进即重建，便于重试）。
- **加载失败不拦导航**：用户已经点了"去那一页"，把他留在旧页面更没有出路。
- 加载结果认三种形态：模块命名空间（取 `.default`）、`() => ({default: Comp})`、
  直接是组件。没有 `default` 会明确报错（而不是挂出一个空白页）。

---

## 6. 历史栈与键绑定

- 每个作用域**一套**栈（见 §8）。`push` 会截断"前进"部分（与浏览器一致），`replace` 只换当前项。
- 默认绑定 `Alt+←` / `Alt+→`。实现方式是**包装**窗口根节点上脚本自己的
  `onKeyDown`（两层都跑），不是覆盖。
- 关掉它：`createRouter({ routes, backKeys: false })`。

```js
const r = await router.push("/detail/7");
// r = { ok: true, route: {...} }  |  { ok:false, reason:"aborted"|"exhausted", detail }

router.history();     // [{path, fullPath, name}, …]
router.index();       // 当前指针
router.scopes();      // [{scope, depth, index, path, busy}, …] 多窗口调试用
```

---

## 7. 页面状态保留（两档）

这是路由最容易踩的一档事，也是本引擎与浏览器路由最大的实现差异所在：
**销毁后的静态子树无法复活**（`disposeNode` 会摘掉响应式接线，再挂回去只是一棵不再更新的死树）。
所以"切页即销毁"是**不可逆**的，状态保留必须显式选一档：

| 档位 | 写法 | 保住什么 | 适合 |
|---|---|---|---|
| **子树保活** | 路由记录 `keepAlive: true` | 子树原样：滚动位置、输入焦点、草稿、页面内局部 signal | 经常来回切的页（列表/详情） |
| **状态袋** | `useRouteState()` | 显式存进去的值。子树可以被销毁重建 | 随姿态/换屏会重建的页 |

```js
function ListPage() {
  const st = useRouteState();               // 挂在**历史栈项**上
  const n = (st.get("renders", 0) || 0) + 1;
  st.set("renders", n);
  return <column>
    <input width={180} />
    <text font={12}>{() => "renders=" + n}</text>
  </column>;
}
```

- `keepAlive: true` 的页面：离开只从 `Children` 摘出去，回来原样挂回，`renders` 停在 1。
- 不保活的页面：离开即销毁，回到**同一条**栈项时页面体重跑，但 `st.get` 仍读得回
  之前写的值（`testdata/router_window_demo.js` 的 `note fresh / note kept` 就是这个对照）。
- 保活页面会常驻内存（节点 + effect）。深栈应用请只给真正需要的页开 `keepAlive`。

---

## 8. 多窗口

### 8.1 作用域模型（理解多窗口的全部关键）

**一个作用域 = 一套独立导航栈。** 作用域有两种来源：

| 来源 | 名字 | 谁在用 |
|---|---|---|
| 窗口自动 | `"win:1"` / `"win:2"` …（`Mount` 时分配，永不复用） | `<RouterView />` 不写 `scope` 时，按"视图挂在哪个窗口上"自动认领 |
| 脚本显式 | `<RouterView scope="preview" />` | 一个窗口里想放两套独立导航（如主区 + 侧栏预览），或跨窗口共用一个栈 |

窗口句柄上能读到自己的作用域名：`wa.scope()`（还有 `wa.id()`）。

```js
const wa = render(<window title="A" width={420} height={300}><RouterView /></window>);
const wb = render(<window title="B" width={420} height={300}><RouterView /></window>);

router.push("/list", wa);      // 只动 A 的栈 —— 独立导航栈是结构保证
router.back(wa);
```

**根级 UI 读取当前路由**时会落到"最近用到的会话"（`router.currentRoute()`），
所以每个窗口要显示自己的路径时，请在页面体里用 `useRoute()`（它绑定的是那个页面
所属的会话），而不是在窗口根上读 `currentRoute()`。

### 8.2 跨窗口状态同步

```js
router.sync([wa, wb], { mode: "mirror" });   // 路径同步，页面状态各自独立（默认）
router.sync(["win:1", "preview"], { mode: "share" });   // 路径 + **状态袋**都共享
router.sync([wa, wb], { mode: "follow", source: "win:1" }); // 单向：只跟随 source
router.unsync();                              // 清掉全部同步组
```

- `mirror`：任一方导航，其余成员跳到同一条路由，**各窗口的 `route.state` 各自独立**。
- `share`：干脆共用同一条栈项 —— 两个窗口渲染同一个页面实例的数据，`route.state` 是同一份。
- 组内不会来回弹（镜像期间标记 `applying`，不再向外传播）。
- 成员写错（比如把 `scope="preview"` 的窗口用句柄来指认）会在 `gx/dev` 里出声一次。

---

## 9. 多屏与折叠屏

### 9.1 `gx/screen` API

| 导出 | 说明 |
|---|---|
| `screens()` | 全部显示器（数组）；`useScreens()` 是它的响应式版 |
| `screen(id?)` | 按 ID 取一块屏 |
| `screenOf(win?)` | **窗口所在**的显示器（省略参数 = 最近挂载的窗口） |
| `useScreen(win?)` | 上面这个的响应式取值函数 |
| `windowInfo(win?)` | `{width,height,scale,screenWidth,screenHeight,workWidth,workHeight,screenId,platform}` |
| `useWindowInfo(win?)` | 同上，响应式 |
| `posture(win?)` | 折叠姿态（**字符串**）：`"flat"` / `"half-open"` / `"folded"` / `"unknown"` |
| `usePosture(win?)` | 上面这个的**响应式**版 —— 返回的是取值函数，要 `const r = usePosture(); r()` 才是值；只要"此刻的值"就用 `posture(win?)`。别把它直接当字符串比（`usePosture() === "half-open"` 永远为 false，且不报错） |
| `hinge(win?)` | 折痕矩形 `{x,y,width,height,orientation}`，无则 `null`。**注意是 `width/height`**：直接回填给 `reportPosture` 也认（两种拼法都行，见 §9.2） |
| `regions(win?)` | 折叠分段面板（`{id,x,y,width,height}`） |
| `platform()` | `"win32"` / `"x11"` / `"cocoa"` / `"headless"` |
| `reportPosture(opts)` | **宿主/模拟器上报**姿态（见 §9.2） |
| `resetDisplays()` | 撤销全部上报，交还给后端枚举 |
| `onDisplayChange(fn)` | 显示器/姿态变化通知，返回注销函数（另导出 `offDisplayChange(fn)`，可显式注销） |
| `primaryScreen()` | 主屏（等价于 `screens()` 里 `primary` 为真的那块） |

字段名口径（一次定好）：**窗口**尺寸是 `width/height`（与 `render` 配置、`onResize` 同词），
**屏幕**尺寸是 `screenWidth/screenHeight` —— 两组名字不同，因为它们回答的是不同的问题。

显示器对象字段：`id/name/x/y/width/height/workX/workY/workWidth/workHeight/scale/primary/foldable/posture/hinge/regions`。
`id` 用设备名（形如 `\\.\DISPLAY1`）而不是序号 —— 序号会随插拔变化，设备名稳定。

### 9.2 姿态从哪来（这一条必须诚实）

**Windows / X11 上没有可用的折叠姿态查询 API**（WinRT 的 `Windows.Devices.Sensors`
不在纯 syscall + 零 cgo 的可达范围内）。框架的立场是：**提供姿态通道，而不是猜姿态** ——
猜错会让界面在没有折痕的屏上分栏。

```
移动宿主 (Android / 鸿蒙 / iOS)  → 读系统 API → 调 reportPosture 上报
桌面折叠屏模拟器 / 开发者工具     → 同一个 reportPosture
真·桌面 Windows                  → 没人上报 ⇒ 姿态恒 "flat" ⇒ 行为与今天完全一致
```

```js
import { reportPosture, screenOf, resetDisplays } from "gx/screen";

// 按"窗口所在那块屏"上报 (后端负责枚举与归属, 宿主只负责姿态)
reportPosture({
  display: screenOf(win).id,
  foldable: true,
  width: 1600, height: 1000,
  posture: "half-open",
  hinge: { x: 700, y: 0, w: 24, h: 1000, orientation: "vertical" },
});
```

规则：
- `posture` 词表按 Android Jetpack WindowManager 归一化（不认识的词 → `"unknown"`，不猜）。
- **折痕不随姿态清除**：宿主常见写法是"只改姿态"，所以 `{posture:"flat"}` 之后
  折痕/尺寸都记得，再折回来比例不变。
- 一旦上报过，那张表就是权威；`resetDisplays()` 交还给后端枚举。
- **尺寸两种拼法都认**（2026-09-22 起）：`hinge` / `regions` 里写 `w`/`h` 或
  `width`/`height` 都行（同时给时短名优先）。这条是为了让"反手回填"能直接用：
  `hinge()` / `regions()` 的输出用 `width/height`，而这里历史上只读 `w/h` ——
  于是 `reportPosture({ hinge: hinge() })` 会把折痕宽度**静默**读成 0（只影响分栏
  比例，什么都不报，症状是"折痕宽度读出来是 0"）。

**双栏不生效时的三步排查**（顺序别换，一步排除一类原因）：

```js
import { posture, screenOf } from "gx/screen";
posture(win);        // ① 姿态是不是 "half-open"? 是 "flat" ⇒ 没人上报 (§9.2)
screenOf(win).foldable; // ② 这块屏是不是折叠屏? 上报姿态时不带 foldable 也会自动置 true
                     // ③ 都不是 ⇒ 看路由记录: dualPane:false / 页签没进双栏 (§9.3)
```

### 9.3 折叠态下的路由：双栏

半折（`half-open`）时，`RouterView` 会自动变成**双栏**：左栏放**上一条**（通常是列表），
右栏放**当前页**（通常是详情）。这不是"两个 RouterView"，而是同一个视图在同一块屏上
按姿态摆两栏：

```
平展 (flat)                   半折 (half-open)
┌────────────────┐            ┌──────────┬─┬──────────────┐
│     详情页      │     →      │  列表页   │┊│    详情页     │
└────────────────┘            └──────────┴─┴──────────────┘
                              左栏 = 历史的上一条 (display 的折痕左侧)
```

- 两栏宽度用 `flexGrow` 按折痕比例分配 —— **不需要知道窗口像素宽**（布局期才知道真值）。
- `dualPane: false` 的页面不参与双栏（例如引导页、全屏编辑器）。
- 只想关掉这个行为：`createRouter({ routes, foldable: false })`。
- **按屏隔离**：姿态是每块屏的属性，折 A 屏不影响 B 屏上的窗口（用例里就是这么钉的）。
- **路由重建与状态保持**：姿态变化会让视图重算、页面子树按需重建（左栏被挤走的那条
  若开了 `keepAlive` 就只是摘出去，没开则销毁）；而**导航栈、参数、`route.state`
  与页面树无关，因此原样保持**。想让"被重建的页"仍然记住东西，就用 `useRouteState()`。

### 9.4 窗口换屏

后端侧：`gfx/win32/display.go` 用 `MonitorFromWindow` 判定"窗口在哪块屏上"
（横跨两块屏、负坐标副屏这些情形用坐标自己算都会出错），并用 `GetDpiForMonitor`
给出**每屏**缩放；`WM_DISPLAYCHANGE` / `WM_DPICHANGED` 经 `gfx.Post` 转投，
由 `gx/screen` 抬高环境版本 → 订阅者（含路由视图）当场重算。

脚本侧：`screenOf(win)` / `useWindowInfo(win)` 就是这条链路的出口。
窗口实际被拖动后想强制全量重排，可以调 `router.rebuild()`（抬高版本信号 + 通知所有视图）。

---

## 10. 边界（v1 不做的事）

| 不做 | 替代 |
|---|---|
| 正则路径约束 `:id(\\d+)` | 在 `beforeEnter` 里校验 |
| `alias` / 相对路径 `"./sub"` | 用命名的绝对路径 |
| 完整 `beforeResolve` / `isReady()` | 用 `beforeEach` + `push` 返回的 Promise |
| `scrollBehavior` | `keepAlive` 保住滚动状态；或自己写 `useRouteState` |
| 切页转场动画 | 给页面根元素挂 `transition`（P3-2），或按 `route` 变化自己驱动 |
| 长列表虚拟化 | 列表页自己分页 |
| 命名视图（一个路由多个具名 RouterView） | 一个窗口里放多个 `<RouterView scope="x">` |
| 完整 media query / 逻辑像素层 | 见 `agent_doc/gui-responsive-screen-options.md`（B/D 方案仍未做） |

---

## 11. 排障：症状 → 原因

| 症状 | 十有八九是 |
|---|---|
| 页面一片空白，控制台什么都没有 | 页面组件抛错了（stderr 与 `gx/dev` 的警告缓冲里会有 `gx/router 页面组件抛错`）；或路由记录的 `component` 不是函数 |
| 懒加载页第一次进去空、第二次正常 | `beforeRouteEnter` 没生效 → 改用 `lazy(() => import(…))`（§5.1） |
| 顶部显示的路由永远停在初始值 | 在窗口根上读了 `router.currentRoute()` 却期望"本窗口" —— 用 `useRoute()` 写在页面体里 |
| 折叠了但没变双栏 | 姿态没上报（Windows 上没人报就是 `flat`）—— 先 `posture(win)` 确认读到的是 `"half-open"`；或路由记录 `dualPane:false`；或 `foldable:false`（排查三步见 §9.2） |
| `hinge()` 的宽度是 0，回填后折痕像丢了 | 回填进了 `reportPosture` 的 `hinge`：`hinge()` 输出 `width/height`、入参老写法只读 `w/h` ⇒ 静默读成 0。**已修**：两种拼法都认，`reportPosture({hinge: hinge()})` 可以直接写 |
| `usePosture()` 比较结果总是不对 | 它返回取值函数（响应式），不是字符串 —— `usePosture()()` 才是值；只要当前值用 `posture(win)` |
| 分栏比例怪异（左栏极窄/极宽） | `hinge.x/y` 传成了"折痕长度"而不是"折痕在屏上的位置"（比例按显示器长度算，已钳到 0.2~0.8） |
| `sync([wa, wb])` 没同步 | 那个窗口的 RouterView 写了显式 `scope=`，句柄代表的是自动作用域 `win:N` —— 用作用域名指认，`gx/dev` 里有一条提示 |
| 页面里改个 signal 就把整页重建了 | 页面体直接读了 signal（`const n = count()`）。改成 `{() => count()}` 或放进函数 prop；路由已用 `untrack` 屏蔽，但组件体内的直接读取仍会成立 |
| 回来时草稿/滚动没了 | 页面没开 `keepAlive`，而且状态没进 `useRouteState()` |
| `Alt+←` 被应用自己的快捷键抢了 | 本模块**包装**根节点 `onKeyDown` 两层都跑；要独占就关掉 `backKeys` |

---

## 12. 测试与示例

| 文件 | 覆盖 |
|---|---|
| `gfx/router_match_test.go` | 匹配层纯函数：优先级、可选段、通配、嵌套拼接、编译容错 |
| `gfx/router_test.go` | `testdata/router_demo.js` 端到端：懒加载（含模块命名导出守卫）、三级守卫、历史、keepAlive、状态袋、`*` 兜底 |
| `gfx/router_window_test.go` | `testdata/router_window_demo.js`：独立导航栈、mirror 同步、按屏姿态隔离、双栏、状态袋跨子树重建 |
| `gfx/screen_test.go` | 折痕比例、姿态词表、上报 upsert、平台名、句柄内省口 |
| `gfx/win32/display_test.go` | **真机**枚举：几何/工作区/缩放合理、恰好一个主屏、win32 不自带折叠姿态 |

> 三个演示脚本**不在** `TestExampleScriptsMount` 的清单里：那个用例的 cwd 是 `gfx/`，
> 而 `router_demo.js` 的懒加载写的是 `import("./router_page_detail.js")`（相对脚本自身
> 目录）。与 `image_demo.js` / `storage_demo.js` 同一情形，由上面的专职用例覆盖
> （它们额外把模块基准路径设成 `testdata/`，等价于用户从仓库根目录 `go run . testdata/router_demo.js`）。

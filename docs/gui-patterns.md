# Gox GUI 用户态模式手册（gui-patterns）

> 「怎么用已有的内核能力组织出常见应用形态」的模式层文档。每个模式都满足三个标准：
> **零内核改动**、可整段复制、有 testdata demo + 全链路测试兜底。
>
> 来源：2026-09-18 选型拍板（`agent_doc/undecided-and-unimplemented.md` §一）——
> 路由 A「用户态 signal 模式（模式文档 + testdata demo）」与屏幕适配 A
> 「onResize + useWindowSize」的落地交付物；2026-09-19 增补状态 B
> createResource、onMount/onCleanup、devtools A（gx/dev）、样式 F（用户态
> 设计套件）四章，以及 §9 视图（`gx/view` 的声明式循环与条件，同日内核
> 模块化的第一批：模式 → 模块的升级触发见文末 §10）。
>
> 章节对应 demo：§1–§2 → `testdata/routing_demo.js`（`gfx/routing_test.go`）；
> §3 → `testdata/resize_demo.js`（`gfx/resize_test.go`）；§4 → `testdata/resource_demo.js`；
> §7 → `testdata/dev_panel_demo.js`；§8 → `testdata/kit_demo.js`；§9 →
> `testdata/view_demo.js`（`gfx/view_test.go`，后四个在
> `TestExampleScriptsMount` 挂载，交互断言见 `gfx/resource_test.go` /
> `gfx/dev_test.go` / `gfx/view_test.go`）。

---

## 1. 路由：一个 signal 切页

> **2026-09-21 起有模块版了**：页数多、要参数路由 / 守卫 / 历史栈 / 懒加载时，
> 直接用内置模块 **[`gx/router`](gui-router.md)**（`createRouter` + `<RouterView>` +
> `<RouterLink>` + `useRoute`）。本节的模式在 **3 页以内的小工具**里依然是最省事的
> 写法（零 import、概念为零），且 §2 的守卫思路与 `gx/router` 的守卫完全同源 ——
> 从本节迁到模块是"把同一件事换成声明"，不是换心智。

**问题**：多页应用（设置页 / 主列表 / 详情）在 Gox 里怎么组织。

**模式**：路由状态就是一个普通 signal，页面表是普通对象，当前页靠函数子节点条件渲染。

```js
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

const [route, setRoute] = createSignal("home");

const pages = {
  home:    () => (<column gap={8}><text font={15}>Home</text></column>),
  editor:  () => (<column gap={8}><input width={260} /></column>),
};

render(
  <window title="app" width={420} height={300}>
    <column gap={10} padding={14}>
      {() => pages[route()]()}          {/* 条件渲染的统一入口: 函数子节点 */}
      <row gap={8}>
        <button onClick={() => setRoute("home")}>home</button>
        <button onClick={() => setRoute("editor")}>editor</button>
      </row>
    </column>
  </window>
);
```

三条语义（拍板 2026-09-18，写进兼容承诺）：

- **切页即卸载**：`{() => pages[route()]()}` 求值成什么就渲染什么，旧页整棵
  子树被替换（信号订阅一并释放）。v1 不保留旧页状态；要保留就把状态**提到
  页面组件外面**（demo 里 `draft`/`saved` 都在组件外，回编辑页草稿还在）。
- **函数子节点是唯一的条件渲染入口**：返回元素 / 数组 / 标量 / `null`/`false`
  （渲染为空）都合法；别用 `if` 在 `render()` 外面算一次——那不是响应式的。
- **页面表是普通对象**：键就是路由名。想要参数化路由（`detail/42`），把 route
  存成对象 `{name: "detail", id: 42}`，`pages[route().name]` 分派即可——
  不需要内核懂「路径」这个概念。

## 2. 路由：守卫（未保存拦截）与转场

**未保存拦截**是模式层的普通函数，不是内核 hook：

```js
const [pending, setPending] = createSignal(null);   // 被拦下的目标页
const dirty = createMemo(() => draft() !== saved());

function go(next) {
  if (route() === "editor" && next !== "editor" && dirty()) {
    setPending(next);                                // 只改 signal: 确认条是响应式子树
    return;
  }
  setRoute(next);
}
```

确认条本身也是条件渲染：`{() => pending() ? <ConfirmBar/> : null}`。
「留下」`setPending(null)`；「放弃」要么直接 `setRoute(目标)`（绕过守卫），要么
**先清脏标记再 `go(目标)`**——放弃的语义就是清稿，忘了清会被自己的守卫
再拦一次（`routing_demo.js` 踩过）。完整可跑的实现见 `testdata/routing_demo.js`
（`gfx/routing_test.go` 走通了输入变脏 → 拦截 → 留下 → 再拦 → 放行 的全流程）。

**两个易踩坑**（都在上面的测试里踩实过）：

- 受控 `input` 的 `value` 要传**函数**：`value={() => draft()}`。写成
  `value={draft()}` 是一次性求值，prop 永不更新——每个按键都基于旧值计算，
  现象是"只剩最后一个字符"。
- 交互测试里**每个按键单独一轮 pump**：受控写回（signal → prop）要等下一轮
  才落回节点，同轮连推两个按键同样只剩最后一个字符。

**切页转场**用 P3-2 已落地的 transition/animate 表达，无前置。两个层次：

```js
// ① 页内属性变化（展开面板、侧栏让位）: 声明式, 挂 transition prop 即生效
<column transition={{width: 200}} width={() => wide() ? 200 : 0}>...</column>

// ② 切页入场（新页从 0 淡入）: 声明式 transition 首次赋值不动画（与 CSS
//    一致）, 入场要命令式 animate 把值灌进 signal:
const [fade, setFade] = createSignal(1);
function go(next) {
  if (/* 守卫 */) { setPending(next); return; }
  setRoute(next);
  setFade(0);
  animate(0, 1, 180, setFade);            // 每帧写 signal, 页面根挂 opacity={fade()}
}
```

页面根元素挂 `opacity={fade()}`，切页即卸载的语义不受影响（旧页直接消失，
新页淡入）。`value`/`padding`/`gap`/`margin`/`flexGrow` 刻意排除在可过渡
属性之外（与受控写回打架），见 `agent_doc/gui-component-status.md` §20。

## 3. 屏幕适配：useWindowSize 与断点

**问题**：脚本怎么知道窗口多大（拖窗口边缘时自适应布局）。

**内核依赖**（2026-09-19 落地）：`EventResize` 在标脏整帧之外，还会把
`onResize({width, height})` 派发给**布局根**——resize 是窗口级事件，与焦点
在哪无关，所以从根节点链上找处理器（挂非根节点不触发，`gfx/resize_test.go`
有负向用例）。载荷字段名与设备 API 的 `getSystemInfo` 统一为 `width/height`。

**useWindowSize 就是三行**（`testdata/resize_demo.js` 可直接跑）：

```js
const [win, setWin] = createSignal({ width: 520, height: 360 }); // 初始值用窗口配置
const wide = createMemo(() => win().width >= 480);               // 断点是普通 memo

render(
  <window title="app" width={520} height={360}>
    <column onResize={(e) => setWin({ width: e.width, height: e.height })}>
      {() => (wide() ? <Sidebar/> : <text font={12}>(narrow)</text>)}
      <rect height={10} width={() => Math.max(60, win().width - 300)} />
    </column>
  </window>
);
```

语义要点：

- **断点定义在脚本里**：768 还是 480 是业务决策，不烧进内核；一个断点一个
  memo，布局切换全部骑在既有响应式管线上（prop 是函数 → 依赖 signal →
  自动标脏重排）。
- **尺寸语义 v1 是物理像素**（拍板）：高 DPI 下拖出来的数字就是帧缓冲像素数。
  逻辑像素 / density 留给屏幕适配方案 B 统一引入，届时的破坏性变更一次做完。
- **防抖刻意不进内核**：拖动时后端连发 resize，signal 每次都更新是正确语义
  （布局本来就每帧跑）；嫌重自己在 onInput 模式里 `setTimeout` 合并。
- 响应式分支里**别把 signal 读取藏进条件后半段**（canvas 依赖收集同款坑）：
  `{() => wide() ? ... : ...}` 两边都读 `wide` 本身，天然安全。

## 4. 状态：createResource（异步取数三件套）

**问题**：取数 → 展示页面的 loading / error / data 三件套与竞态防御，
每页手写一遍太疼（状态管理方案 B 收编的就是这块）。

**模块依赖**（2026-09-19 落地，`gx/solid` 新增）：

```js
import { createResource } from "gx/solid";

const [data, res] = createResource(fetchItems);   // fetcher 返回 Promise
// data()        → undefined (pending) | 值 | 上一次的值 (refreshing / error)
// res.state()   → "pending" | "ready" | "refreshing" | "error"
// res.error()   → 错误值 (仅 error 态有值)
// res.refetch() → 重取 (保留旧值显示, 即 refreshing)

<text font={14}>{() => res.state() === "pending" ? "加载中…" : String(data())}</text>
<button onClick={() => res.refetch()} disabled={() => res.state() === "pending"}>刷新</button>
```

与 Solid 的刻意差异（v1 减法，**属公共 API 承诺**）：

- `const [data, { refetch }] = ...` 嵌套解构**引擎不支持**（数组解构里不能嵌
  对象模式），controls 作为第二个元素取出（上例的 `res`）。
- `state`/`error` 不挂在 `data` 函数上（函数值不带属性），是 controls 里的
  signal getter。
- **error 态的 `data()` 不抛**（v1 无 ErrorBoundary，抛了会冒泡进任意 effect，
  炸得没有上下文）：返回上一次的值（从未成功过则 undefined），错误只从
  `res.error()` 读。
- fetcher 返回非 Promise（同步值）按「立即可用」处理；不做 source signal
  自动重取（Solid 二参形态），联动用 `createEffect` 手动串。
- **latest-wins**：快速连续 `refetch` 时旧响应后到即丢弃（结构性消灭竞态），
  `resource_demo.js` 可当场验证。

`testdata/resource_demo.js` 覆盖 pending → ready、refetch 保旧值、fail 后
`data()` 不抛不丢、恢复四个场景。

## 5. 状态：枚举 signal 状态机（流程状态）

**问题**：向导 / 审批流这类**流程**状态（loading→ok/fail→retry）怎么管。
拍板结论：v1 不做 `gx/machine`，用一张迁移表 + 单入口 `send`（状态管理
方案 A 的模式），第一个真实多状态流程出现再升级。

```js
const transitions = {
  idle:    { LOAD: "loading" },
  loading: { OK: "success", FAIL: "error", CANCEL: "idle" },
  error:   { RETRY: "loading", RESET: "idle" },
  success: { RESET: "idle" },
};
const [phase, setPhase] = createSignal("idle");

function send(event) {
  const next = transitions[phase()]?.[event];
  if (!next) throw new Error(`illegal transition: ${phase()} --${event}-->`);
  setPhase(next);
}

// UI 全部派生, 不存第二份状态
const face = createMemo(() =>
  phase() === "loading" ? "加载中…" : phase() === "error" ? "失败" : "完成");
```

纪律（写错的表现比写对更常见）：

- `phase` 存字符串（`===` 可用）；带载荷就存 `{name, data}` 且**每次迁移新建
  对象**，不做原地修改。
- 所有迁移过 `send`：非法迁移直接抛错（宁可炸也别"静默卡住"）；绕过 `send`
  直接 `setPhase` 编译期拦不住，靠 review。
- 竞态防御已被 §4 的 createResource 收走 —— 这张表只管"流程走到哪"，
  别再用它管"数据怎么来"。

## 6. 生命周期：onMount / onCleanup

**问题**：组件要在"我挂上/我被换掉"时做事（起定时器、注册回调、清理）。
`gx/solid` 新增（2026-09-19，与 createResource 同批）：

```js
import { onMount, onCleanup } from "gx/solid";

const Timer = (p) => {
  const [tick, setTick] = createSignal(0);
  const id = setInterval(() => setTick(tick() + 1), 1000);
  onCleanup(() => clearInterval(id));      // 本代子树被替换/销毁时执行
  onMount(() => console.log("Timer mounted"));
  return <text font={14}>{() => `t=${tick()}`}</text>;
};

render(<window title="t" width={200} height={100}>
  <column>{() => show() ? <Timer/> : null}</column>   {/* 切走时 clearInterval 自动跑 */}
</window>);
```

语义与边界（v1，文档即承诺）：

- 登记到**当前正在构建的响应式子树**（条件/列表渲染的求值期）：切页/换代
  时 `onCleanup` **逆序**执行，新子树挂上后 `onMount` 立即执行（顺序：
  旧代 cleanup → 拆树 → 挂新树 → 新代 mount，`gfx/resource_test.go`
  有序列断言）。
- **顶层脚本直接调用是 no-op**（打一次警告）：初始静态树存活于整个窗口生命
  期，没有"被换掉"的时刻；需要"窗口关闭时清理"的场景 v1 不覆盖。
- 列表渲染同一代多个组件的登记**整批执行**，粒度是"代"不是"组件实例"；
  换用 `gx/view` 的 `For` 时，每一行各自构成一个"代"（行宿主 = 一棵子树），
  于是删掉一行只跑那一行的 `onCleanup`（§9）。

## 7. devtools：gx/dev 快照与自绘面板

**问题**：开发期想看帧统计 / 缓存命中 / 树规模 / 内核警告。
模块 `gx/dev`（2026-09-19 落地）只导出一个只读函数：

```js
import { devSnapshot } from "gx/dev";
const snap = devSnapshot();
// snap.frame      → { count, full, partial, fullRatio }   帧埋点 (只计真实上屏)
// snap.imageCache → { size, cap, hits, misses, evicts }   图片缓存 (cap=16)
// snap.glyphCache → { ... }                               字形缓存 (cap=1024)
// snap.tree       → { windows, nodes, depth }             全部窗口聚合
// snap.solid      → { effects }                           存活 effect 数 (泄漏排查)
// snap.warnings   → [{ at, text }]                        最近 64 条内核警告
```

用法三条纪律（拍板 2026-09-18）：

- **拉取式**：面板自己 `setInterval(() => setSnap(devSnapshot()), 1000)`；
  **别用 requestAnimationFrame**（和真实渲染抢帧）。`testdata/dev_panel_demo.js`
  是可复制的模板（帧 / 缓存 / 树 / warnings 四区）。
- **字段名是 API**：结构由 `TestDevSnapshotShape` 锁住；`solid.effects` 与
  警告文本格式不算承诺。
- **应用树坏掉时面板一起坏**（方案 A 的已知边界）：需要"卡死现场可看"再
  评估 HTTP 旁路（方案 C）；脏矩形可视化是内核帧埋点的后补项。

## 8. 样式：用户态设计套件（令牌 / 变体 / 主题）

**问题**：颜色与间距的重复（同一种按钮蓝抄 20 遍）。拍板 F+C+B 起步：
零内核改动，令牌是普通对象，变体是工厂函数参数，主题切换 = 换一组令牌。
`testdata/kit_demo.js` 是完整模板，骨架：

```js
const light = { surface: "#ffffff", ink: "#1c2430", accent: "#3355aa", padX: 14, gap: 10 };
const dark  = { surface: "#242b33", ink: "#dfe6ee", accent: "#6c8fd9", padX: 14, gap: 10 };
const [themeName, setThemeName] = createSignal("light");
const t = () => (themeName() === "dark" ? dark : light);

const Btn = (p, ...kids) => {
  const [hover, setHover] = createSignal(false);           // 悬停近似 (方案 F)
  return (
    <button
      padding={p.size === "sm" ? 6 : 10}
      color={() => (p.variant === "primary" ? "#fff" : t().ink)}
      background={() => p.variant === "primary" ? t().accent : t().surface}
      onMouseMove={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
    >{kids}</button>
  );
};
// 注意: 组件子节点是**变参** —— <Btn>x</Btn> 降级成 Btn(props, x), 不是 p.children
const Card = (p, ...kids) => <column background={() => t().surface} padding={() => t().padX}>{kids}</column>;
```

**能力边界必须诚实**（❌ 清单，内核把值写死）：焦点虚线框颜色 / 滚动条与
滑块色 / select 箭头 / progress 轨道色 / checkbox 未选中底色 / modal 遮罩 /
switch 滑块 / disabled 降饱和 / 光标闪烁周期——套件在这些地方会"露出底"，
属预期。悬停是 `onMouseMove` + signal 的**近似**（事件粒度是"移动"不是
"进入/离开"）。交互态覆盖 button/input/select/menu 核心件；第二主题成为
硬需求时再评估样式表选择器（方案 D）。

> 2026-09-19 更新：原 ❌ 清单中的**圆角 / 阴影 / 边框宽度**已由内核装饰
> props 解锁（`radius` / `shadow={{x,y,blur,color}}` / `borderWidth` /
> `borderStyle`，另 `background` 支持 `linear-gradient(...)`，见 status §29）——
> 套件的 Btn/Card 可以直接用它们收口观感（kit_demo 的 Decor 卡是示例），
> 其余 ❌ 项维持。

## 9. 视图：元素级指令 each / show（+ gx/view 的 Switch / Match）

**问题**：列表与条件渲染此前只有两种写法 —— 函数子节点里 map 一遍
（`{() => rows().map((r, i) => <Row r={r} />)}`：列表一变整表拆掉重建，行内的
输入焦点、滚动位置、局部 signal 全丢），或三元/短路表达式堆在 JSX 里（不可读、
也没有"哪几行该重建"的概念）。

**两条指令**（2026-09-20 从 `<For>` / `<Show>` 组件改成元素级指令，语义未变）：
写在元素上，`gx/view` 只需要 import `Switch` / `Match`：

```js
import { Switch, Match } from "gx/view";

<view each={rows} key="id" fallback={<text>暂无数据</text>}>
  {(row, i) => <RowCard row={row} i={i} />}
</view>

<view show={open} fallback={<text>已隐藏</text>}>
  <column gap={4}><input model={draft} /></column>      {/* 双向绑定见 gui-model-binding.md */}
</view>

<Switch fallback={<text>未知状态</text>}>
  <Match when={() => phase() === "loading"}><progress value={0.5} /></Match>
  <Match when={() => phase() === "ready"}><text>就绪</text></Match>
</Switch>
```

`view` 是布局透明的容器（Fragment）：`<view each={rows}>` 与旧的 `<For>` 逐像素一致；
换成别的标签就是"每一项一个盒子"（`<row each={rows}>`）。指令写在哪个元素上，
就重复/显隐哪个元素。

三个短写法（`each={rows}` / `show={open}` / `key="id"`）与展开式语义完全一致：
signal 本身就是取值函数，所以不用再包一层箭头；`key="id"` 就是 `key={(r) => r.id}`。
需要派生/过滤时再写函数：`each={() => rows().filter(ok)}`。

`each` 也接受数字：`each={() => 5}` → 0..4（Vue 的 `v-for="n in 5"`）。

**四条必须记住的语义**（完整理由见 `gfx/view.go` 与 `gfx/directive.go` 文件头）：

1. **`each` / `show` 收取值函数**，而 **signal 本身就是最简的那个取值函数**。
   JSX 属性在调用当场求值：`each={rows()}` / `show={open()}` 只是一张快照，之后 signal
   再变不会重渲染 —— 与"受控 input 的 value 必须传函数"是同一条纪律。传数组/数字字面量
   是合法的**静态**列表（渲染一次）。写错现在**会出声**：非法形态（`each` 收到
   字符串/对象、`show` 收到字符串、尤其是 `show={open()}` 这种忘了括号的静态布尔、
   `key` 收到数字、`stable` 传函数）各打一条去重警告 —— 降级行为不变，只是不再
   静默；警告进 stderr 与 `gx/dev` 的缓冲（§7）。
2. **复用按「key + 引用同一性 + 下标」判定**。同 key、同行引用、同下标 ⇒ 原样
   复用（节点指针不变，行内状态保留）；只就地重渲染真正变了的行；旧 key 消失
   则 dispose（onCleanup 执行）。`rows()` 每次 map 出新对象 ⇒ 每行都被判为变了
   （与 Solid 的 For 同口径，比较用 `===`）。**重排/中间删除会移动后续行的下标，
   那些行会就地重渲染** —— 因为本引擎的 JSX 内容是求值一次的静态值，序号必须
   跟着位置更新；行不显示位置时加 `stable` 把下标从判定里摘出去（代价：下标参数
   停在挂载时的值）。
3. **`key` 也可以直接给字段名**：`key="id"`。取不到该字段（项不是对象 / 没有这个
   字段）时退化为按位置匹配，与不给 key 同义；给了别的类型会警告并同样退化。
4. **Show / Switch 是 keep-alive 显隐，不是 v-if**。隐藏 = 摘出布局流（布局、
   绘制、命中都看不见它），子树**保持挂载** —— 里面的输入框内容、滚动位置、
   局部 signal 全留着，再显示瞬间切回。原因不是舍不得销毁：本引擎 dispose 过的
   静态子树**无法复活**（reactiveProps 已断），v-if 会得到一棵"看着一样但不再
   响应式"的死树。要 v-if（每次显示都全新构建）就用函数子节点：
   `{() => cond() ? <column><input .../></column> : null}`。

**布局**：三者的宿主是 slot（内核的透明占位），放进 column 竖排、放进 row 横排，
gap 缺省跟随父容器 —— 不凭空多一层盒子。已知边界：宿主是 slot，因此**不参与
容器级 wrap**（`row wrap` 只认常规流子节点）。

demo `testdata/view_demo.js`（每行自带输入框，是"复用是否真的发生"的照妖镜：
shuffle / drop last 之后文字跟着行走）；改写版 `testdata/view_demo2.js` 把"复用还是
重建"做成界面上的 gen / builds 计数。交互断言 `gfx/view_test.go` 13 例；短写法
（裸 signal、`key="id"`）与误用警告见 `gfx/view_guard_test.go`。

## 10. 何时从「模式」升级为「模块 / 内核」

模式层的成本是每个应用抄一遍；升级触发（拍板 2026-09-18，抄自各 options 文档）：

| 模式 | 升级动作 | 触发条件 |
|---|---|---|
| §1 路由 | ✅ **已固化**：`gx/router`（2026-09-21，见 [gui-router.md](gui-router.md)） | 触发条件已满足 |
| §1 切页即卸载 | ✅ **已在模块层解决**：路由记录 `keepAlive` + `useRouteState()`；内核 `hidden` prop **不做**（理由见 gui-routing-options 的落地记录） | 触发条件已满足 |
| §3 useWindowSize | 并入 `gx/device` 的 `getSystemInfo` | 移动端 M1 或第二个平台能力出现 |
| §3 断点 | 声明式断点（样式体系的一部分） | 样式体系 F+C+B 落地之后（此前单独做会发明第二套样式通道） |
| §4 createResource | source signal 自动重取（Solid 二参形态） | 出现 ≥3 个"手动 createEffect 串 refetch"的应用 |
| §5 枚举状态机 | `gx/machine`（XState 子集） | 第一个真实多状态流程（状态 >5 或迁移 >10 条） |
| §6 生命周期 | 窗口级卸载钩子 / 组件实例粒度 | 窗口关闭清理成为真实需求 / 内核引入 diff+key |
| §7 gx/dev | 脏矩形可视化（内核帧埋点后补） / HTTP 旁路（方案 C） | 排查"局部重绘不生效" / 应用卡死时要能看 |
| §8 设计套件 | 语义令牌进内核（方案 B/C 内核侧）+ 装饰栈（方案 E） | 第二主题硬需求 / 圆角阴影等光栅能力落地后 |
| §9 视图 | 行级 keyed「移动」动画 / 虚拟化长列表（内核侧需 diff 之外的动画与测量设施） | 需要拖动排序动画 / 列表规模上千 |

> §9 已经不在"模式"这一档：它连同 `gx/view` 一起交付（模式 → 模块的升级在
> 同一天完成，触发条件是"每个应用都在手写 map + 三元"）。表中保留它是因为
> 再往前一步（动画 / 虚拟化）仍要动内核。

在那之前，本手册的写法就是**官方推荐用法**：内核 API 面不增长，模式演进
（加参数路由、加嵌套路由）只是改示例，不是改兼容承诺。

## 11. 导航壳：AppShell（PC / 移动自适应的官方组装）

> **2026-09-25 起**：这一章从"每个应用抄一遍的模式"升级成了**现成的用户态组件库**
> （`testdata/ui/` 下的 TabBar / SideNav / AppShell，纯 JS 零内核改动），
> 完整 API、差异矩阵与坑位清单见 [gui-tabbar.md](gui-tabbar.md)；
> 与路由的组合写法见 [gui-router.md](gui-router.md) §13。本节保留组装思路，
> 供"不想引组件库"或要自己改造的场景。

**问题**：§3 的断点布局解决"窗口多大摆什么"，但一套代码同时跑移动端（要底部
TabBar、安全区、软键盘、返回键）与桌面端（要侧边栏、折叠、快捷键）时，
差异逻辑散落在每个应用里抄来抄去。

**模式**：差异全部可以被"平台 + 宽度断点 + viewport 上报"三件事解释 ——
做一个 Shell 承担全部分派，业务只声明 tabs：

- **分派**：逻辑宽 ≥ 断点（缺省 720）→ SideNav；否则 → TabBar。
  注意 `onResize` 是**物理像素**，比较前除以 `useWindowInfo().scale`（§3 的
  "尺寸语义 v1 是物理像素"在这里必须处理，不是可选优化）。
- **安全区 / 键盘**（§9.6 宿主能力层）：底栏 `paddingBottom = useInsets().bottom`
  （桌面恒 0 无需分支）；`useViewport().keyboard > 0` → `show={false}` 隐藏底栏
  （keep-alive，恢复时状态原样）。键盘 insets 与底部 insets **取大不取和**
  （内核 `safeAreaStyle` 注释明确相加会顶出一截）；底栏隐藏时二者不同时生效。
- **返回键**（Android）：`onBackPress` 返回真值 = 已消费。栈里有上一页
  → `router.back()`；已在 tab 根 → 切回首个 tab；再按放行退出。
- **tab 与路由**：tab 根页之间 `router.replace`（防栈无限深）+ `keepAlive`；
  tab 内详情页才 `push`。组合写法见 gui-router.md §13。
- **桌面增强**：`Ctrl+1..9` 挂在壳的根 `onKeyDown`（键盘事件沿祖先链派发，
  根是所有节点的祖先；要免焦点用 `menuitem shortcut` 的事件泵表）；折叠态
  记忆到 `gx/storage`；低频项用 `<spacer>` 沉底。
- **多窗口**：active 信号在 Shell 组件函数体内创建 —— 每窗口各跑一遍组件函数，
  天然独立；路由栈隔离沿用 router 的 scope 概念。

demo：`testdata/tabbar_demo.js`（拖窗口宽度过断点，形态自动切换，业务零改动）。

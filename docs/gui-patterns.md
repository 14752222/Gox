# Gox GUI 用户态模式手册（gui-patterns）

> 「怎么用已有的内核能力组织出常见应用形态」的模式层文档。每个模式都满足三个标准：
> **零内核改动**、可整段复制、有 testdata demo + 全链路测试兜底。
>
> 来源：2026-09-18 选型拍板（`undecided-and-unimplemented.md` §一）——
> 路由 A「用户态 signal 模式（模式文档 + testdata demo）」与屏幕适配 A
> 「onResize + useWindowSize」的落地交付物。
>
> 章节对应 demo：§1–§2 → `testdata/routing_demo.js`（`gfx/routing_test.go`）；
> §3 → `testdata/resize_demo.js`（`gfx/resize_test.go`）。

---

## 1. 路由：一个 signal 切页

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
属性之外（与受控写回打架），见 `docs/gui-component-status.md` §20。

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

## 4. 何时从「模式」升级为「模块 / 内核」

模式层的成本是每个应用抄一遍；升级触发（拍板 2026-09-18，抄自各 options 文档）：

| 模式 | 升级动作 | 触发条件 |
|---|---|---|
| §1 路由 | 固化为 `gx/router` 模块 | ≥3 个应用重复抄同一路由模式 |
| §1 切页即卸载 | 内核 `hidden` prop（唯一动内核项） | 真实应用抱怨切页丢状态 |
| §3 useWindowSize | 并入 `gx/device` 的 `getSystemInfo` | 移动端 M1 或第二个平台能力出现 |
| §3 断点 | 声明式断点（样式体系的一部分） | 样式体系 F+C+B 落地之后（此前单独做会发明第二套样式通道） |

在那之前，本手册的写法就是**官方推荐用法**：内核 API 面不增长，模式演进
（加参数路由、加嵌套路由）只是改示例，不是改兼容承诺。

# 多窗口 · 多屏 · 自适应断点 · 应用接续

本文覆盖 M4（自适应断点布局 + 多窗口/多屏 API v1）与 M8（多屏协同 v1：跨屏窗口 + 应用接续）。

- 断点系统：`gx/viewport` 的 `breakpoint` / `useBreakpoint` / `above` / `below` / `between` / `matchBreakpoint` / `setBreakpoints`
- 窗口列表：`gx/screen` 的 `windows()` / `window(id)`
- 窗口几何：句柄 `moveTo` / `center` / `position` / `bounds` / `display`
- 跨屏事件：`gx/screen` 的 `onWindowDisplayChange` / `useWindowDisplay`
- 应用接续：`gx/router` 的 `router.handoff` / `router.continuity`

---

## 1. 坐标口径（唯一一处定义）

脚本侧读到的所有窗口位置都是**工作区相对、设备像素**：

    外框左上角 相对 其所在显示器工作区左上角 的偏移

| API | 读/写 | 口径 |
|---|---|---|
| `windowInfo().x / .y` | 读 | 工作区相对，设备像素 |
| `w.position()` | 读 | 同上，`{x, y}` |
| `w.bounds()` | 读 | `{x, y, width, height, displayId, scale}`，x/y 同上 |
| `w.moveTo(x, y)` | 写 | 同上，相对**当前所在屏**的工作区 |
| `windows()[i].x / .y` | 读 | 同上 |
| `w.display()` | 读 | 显示器 id |

**为什么是"工作区相对"而不是"虚拟桌面绝对"**：脚本真正想表达的是"相对这块屏我还要挪多少"（居中、贴边、留边距）。绝对坐标随屏的排列/插拔而变，工作区相对量不会 —— 同一段"居中"代码在任何多屏拼接下都成立。

**为什么不是"屏幕相对"而是"工作区相对"**：工作区 = 排除任务栏/Dock/菜单栏之后的可用区。`w.center()` 在可见区里居中，不会被任务栏挡住。

### 单位：脚本恒设备像素，后端各用各的

各平台原生窗口坐标的单位不同：

| 后端 | 位置单位 | `Display.PosInPoints` |
|---|---|---|
| win32 | 设备像素 | `false` |
| x11 | 设备像素 | `false` |
| cocoa | **点**（AppKit 全局坐标，左下原点） | `true` |

`Display.PosInPoints` 把这个差别标出来，`gfx/window_move.go` 的 `toDevicePx` / `fromDevicePx` 在"设备像素 ↔ 后端位置单位"之间换算一次。**这是最容易静默出错的一步**：Retina（scale=2）上少了它，`moveTo`/`position` 会差一倍，窗口挪到错误位置却不报任何错。

> 跨屏拖动由用户/系统完成（OS 原生行为），不经过这套 API；后端只在窗口换屏时经 `PostWindowDisplayCheck` 通知内核。

---

## 2. 自适应断点（`gx/viewport`）

断点是**命名阈值**：宽度越过某个阈值就换一档。默认表：

    sm: 0    md: 600    lg: 840    xl: 1200      （单位 dp）

```js
import { useBreakpoint, matchBreakpoint, breakpoints, setBreakpoints } from "gx/viewport";

const bp = useBreakpoint();          // () => "sm" | "md" | "lg" | "xl"

// matchBreakpoint 返回命中档的值; 它自身**不订阅**, 要放进读 bp() 的函数里才会重算
const layout = () => {
  bp();                              // ← 建立订阅 (见 gui-guide §8.1 的订阅纪律)
  return matchBreakpoint({
    sm: { cols: 1, side: false },
    md: { cols: 1, side: true },
    lg: { cols: 2, side: true },
    xl: { cols: 2, side: true, info: true },
  });
};

h("text", null, () => `breakpoint = ${bp()} / cols = ${layout().cols}`);
```

| API | 返回 | 说明 |
|---|---|---|
| `breakpoints()` | `{sm:0, md:600, lg:840, xl:1200}` | 当前表（name → dp） |
| `breakpoint(win?)` | `"sm"` | 当前命中档，不订阅 |
| `useBreakpoint(win?)` | `() => string` | 取值函数 + 订阅，窗口宽度变化时重算 |
| `above(name, win?)` | `boolean` | 宽度 `>= name` 的下界 |
| `below(name, win?)` | `boolean` | 宽度 `< name` 的下界 |
| `between(a, b, win?)` | `boolean` | 半开区间 `[a, b)`，参数顺序无关 |
| `matchBreakpoint(table, win?)` | 命中的值 / `undefined` | 在给定档里取"下界 ≤ 宽度中最大"的那档的值 |
| `setBreakpoints(table)` | — | 整表替换（名字非空、数值 ≥0、至少一档；非法项静默跳过，全非法则保持原表并告警） |
| `resetBreakpoints()` | — | 复位到默认表 |

**断点 vs 尺寸类**：尺寸类（`widthClass()` 的 `compact`/`medium`/`expanded`，阈值 600/840dp）是内核对"物理宽度档"的**分类**；断点是**命名阈值**，默认表恰好复用同一组数字。要业务自定义阈值用 `setBreakpoints`，不影响尺寸类。

**断点按窗口宽度算，不是屏幕宽度**：多窗口/分屏下每个窗口各算各的（宽度取客户区宽度 ÷ 所在显示器缩放）。

示例：[testdata/breakpoint_demo.js](../testdata/breakpoint_demo.js)（同一份代码在 sm/md/lg/xl 四个尺寸下自动换形态，按钮直接 `resize` 到四档便于观察）。

---

## 3. 窗口列表（`gx/screen`）

```js
import { windows, window } from "gx/screen";

windows();        // [{ id, title, scope, x, y, width, height, scale, displayId, active, focused }, ...]
window(3);        // 单个窗口的条目; 不存在 → null
```

| 字段 | 含义 |
|---|---|
| `id` | 窗口号（句柄 `id()`，永不复用） |
| `title` | 当前标题（最近一次 `setTitle` 的值） |
| `scope` | 路由作用域名 `"win:<id>"` |
| `x` / `y` | 工作区相对、设备像素（同 §1） |
| `width` / `height` | 客户区尺寸（设备像素） |
| `scale` | 所在显示器缩放 |
| `displayId` | 所在显示器 id |
| `active` | 是否内核"最近挂载/最近活跃"的窗口 |
| `focused` | 是否拥有系统键盘焦点（后端不支持时退化为 `active`） |

> `active` 与 `focused` 刻意都保留：多窗口下"我最近开的是哪个"与"用户此刻在敲哪个"是两件事。

---

## 4. 窗口几何（句柄）

`render()` 返回的句柄上新增：

| 方法 | 说明 |
|---|---|
| `w.moveTo(x, y)` | 把外框左上角移到当前屏工作区的 `(x, y)`；缺参/非数字抛 TypeError |
| `w.center()` | 在当前屏工作区居中 |
| `w.position()` | `{x, y}`（工作区相对，设备像素） |
| `w.bounds()` | `{x, y, width, height, displayId, scale}` |
| `w.display()` | 所在显示器 id |

```js
const w = render(<window title="geo" width={320} height={200}>…</window>);
w.moveTo(40, 60);
console.log(w.position(), w.bounds(), w.display());
```

`moveTo` 是**当前屏**的工作区相对坐标：把窗口拖到副屏后，`moveTo(0,0)` 是"副屏工作区左上角"。要跨屏，`moveTo` 到超出当前屏工作区的坐标（会落到相邻屏），或由用户拖动。

### 初始按屏放置（Go 侧）

Go 嵌入侧可用 `WindowConfig` 在创建时指定位置与目标屏：

```go
win, _ := gfx.Mount(root, gfx.WindowConfig{
    Title: "B", Width: 800, Height: 600,
    Display: "DISPLAY2",   // 目标显示器 id; 省略 = 平台默认
    X: 100, Y: 80,         // 相对该屏工作区; 省略且给了 Display = 该屏居中
})
```

规则（`gfx.ResolveWindowPlacement`）：

- `Display` 空 + `X/Y` 未指定 → 平台自选（Windows 级联 / macOS 居中）
- `Display` 给定 + `X/Y` 未指定 → 该屏工作区居中
- `X/Y` 指定（`Display` 空则用主屏）→ 该屏工作区 + `(X, Y)`
- `X`/`Y` 任一 `< DefaultWindowPos(-100000)` 或全零 → 视为未指定

三个后端（win32 `CreateWindowExW` / x11 `CreateWindow` / cocoa `setFrameOrigin`）都接了这套解析。

> **v1 边界**：`render()` 的第二个参数目前只解析 `title`/`width`/`height`，不解析 `x`/`y`/`display`。JS 侧按屏放置请用 `w.moveTo`（跨屏时见上）。

---

## 5. 跨屏事件

```js
import { onWindowDisplayChange, useWindowDisplay } from "gx/screen";

const off = onWindowDisplayChange(({ windowId, fromDisplay, toDisplay }) => {
  console.log(`win ${windowId}: ${fromDisplay} -> ${toDisplay}`);
});
off();   // 注销

const latest = useWindowDisplay();   // () => 最近一次事件 / null (可订阅)
```

- 后端在窗口换屏（win32 `WM_DPICHANGED` / cocoa `NSWindowDidChangeScreenNotification` / x11 `ConfigureNotify`）时只 `PostWindowDisplayCheck`，**由内核**用 `lastWindowDisplay` 表比对"变没变"并填 `windowId`。这样"上次在哪块屏"只有一份状态，三个后端不会各存各的。
- 首次比对不产生事件（不报"从空串换屏"）；同屏重复比对不重复派发。
- 后端不支持 `DisplayOf` 时恒报主屏 → 永远不派发（安静降级）。

---

## 6. 应用接续（`gx/router`）

`router.handoff(from, to, opts?)` 把 `from` 的**整条导航栈（含每一项的 `route.state`）搬到** `to`：

```js
import { createRouter, RouterView, useRouter } from "gx/router";

const router = createRouter({ routes: [ /* … */ ], initial: "/" });

const wa = render(<window title="home">{() => RouterView()}</window>);
const wb = render(<window title="road">{() => RouterView()}</window>);

await router.handoff(wa, wb);                          // 搬迁: 源复位回首页
await router.handoff(wa, wb, { keepSource: true });    // 复制: 源不动, 目标拿克隆
```

| 模式 | 语义 |
|---|---|
| 默认（搬迁） | 目标拿到源的整条栈（**复用栈项指针**，`route.state` 随指针走）；源复位到栈底/首页 |
| `{ keepSource: true }` | 源完全不动；目标拿到**克隆**（路径/参数/query 复制，状态袋浅拷贝，此后各自独立） |

返回 Promise，resolve 值：`{ ok, kind:"handoff", from, to, count, keptSource, route }`。
非法 `from`/`to` → TypeError；空源 → resolve `{reason:"empty-source"}`；同作用域 → `{reason:"same-scope"}`。

### 与 `sync` 的区别

    router.sync([a, b], {mode:"mirror"})  路径同步, 两条栈各自继续存在
    router.sync([a, b], {mode:"share"})   路径 + 状态袋共享
    router.sync([a, b], {mode:"follow"})  单向跟随
    router.handoff(a, b)                  **搬迁**: a 的栈移到 b, a 回到首页, 此后无关

差别落在"源窗口之后会怎样"：mirror/follow 之后源仍在原页面（它在继续导航）；handoff 之后源被腾空 —— 这才是"接续"的物理动作。

### `router.continuity()`：接续前体检

```js
router.continuity();
// [{ scope, depth, index, path, stack: [path…], stateKeys: [key…] }, …]
```

只读内省：哪个作用域持有哪条栈、深度多少、当前在哪一页、状态袋里有哪些键。**不返回状态值**，只给键名（内省接口不该让脚本顺手拿到别人的私有状态 —— 真需要状态用 `useRouteState`）。

示例：[testdata/multiscreen_demo.js](../testdata/multiscreen_demo.js)（两个窗口 + 跨屏事件 + `handoff` 搬迁/复制按钮）。

---

## 7. 平台支持矩阵

| 能力 | win32 | cocoa | x11 |
|---|---|---|---|
| `moveTo`（`windowMover`） | ✓ `SetWindowPos` | ✓ `setFrameOrigin` | ✓ `ConfigureWindow` |
| `position`/`bounds`（`windowBoundsProvider`） | ✓ `GetWindowRect` | ✓ `frame` | ✓ `TranslateCoordinates`→根坐标 |
| `focused`（`windowActiveProvider`） | ✓ `GetForegroundWindow` | ✓ `isKeyWindow` | —（退化为 `active`） |
| 显示器枚举（`displayProvider`） | ✓ `EnumDisplayMonitors` | ✓ `NSScreen.screens` | ✓ RandR 1.5 monitors → 退化单屏 |
| 换屏事件投递点 | `WM_DPICHANGED` | `NSWindowDidChangeScreenNotification` | `ConfigureNotify` |
| 位置单位 | 设备像素 | 点（`PosInPoints`） | 设备像素 |

移动端（android/ios/harmony）沿用同一套可选接口；缺能力的方法静默降级（`position` 返回 `(0,0)`、`moveTo` no-op、`focused` 退化为 `active`）。

---

## 8. v1 边界（明确不做 / 未接）

- **跨设备接续（手机 → 桌面）不做**：需要设备配对与传输通道，超出本里程碑。`handoff` 演示的是**同机跨窗口**接续，语义与跨设备完全一致（搬迁 + 源复位），差的只是一层传输层；`continuity()` 就是给未来的接续协调器做只读体检用的。
- `render()` 配置不解析 `x`/`y`/`display`（Go 侧 `WindowConfig` 才支持）；JS 用 `moveTo`。
- x11 工作区 = 显示器区域（未读 `_NET_WORKAREA`，拿不到任务栏让出的可用区）；x11 `scale` 恒 1（X11 无标准 DPI 查询）。
- cocoa 混合 DPI 多屏的绝对坐标按各屏点坐标近似（macOS 全局坐标本就是点）。
- 断点表是**进程级全局**（`setBreakpoints` 影响所有窗口）；要按窗口自定义请用 `matchBreakpoint` 在脚本里分派。

---

## 相关文档

- [gui-guide.md](gui-guide.md) §8.4 自适应断点 / §9.5 多窗口
- [gui-router.md](gui-router.md) 路由与导航栈
- [gui-patterns.md](gui-patterns.md) 响应式布局模式
- [mobile-adaptation.md](mobile-adaptation.md) 移动端适配

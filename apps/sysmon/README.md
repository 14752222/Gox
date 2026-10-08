# 系统资源监视器（sysmon）

Gox 实用应用 **#5**（一期）：实时折线 + 数值卡片，监视 **Gox 运行时真实暴露的资源**
—— 渲染帧、图像/字形缓存、GUI 树、响应式 effect、内核告警。支持多屏多窗口、
阈值告警变色、暂停/继续。

## 它监视的是什么（先说实话）

Gox 的运行时**没有** OS 级 CPU / 内存 / 磁盘读数 —— 本应用探针实测（b381cbc）：

- `stats` 只是**数学模块**，可用键只有 `["sum", "describe"]`：
  - `stats.sum(arr)` → 数；`stats.sum([])` → `0`
  - `stats.describe(arr)` → `{count, min, max, mean}`；**空数组抛 RangeError**
- 运行时**没有 `Date`、没有 `performance`**（`typeof Date === "undefined"`）——
  拿不到墙钟毫秒，所以本应用的"时间轴"是**采样序号**（第 N 个样本），不是钟点。
- 真实的资源读数来自 **`gx/dev.devSnapshot()`**（逐字段实测）：

```
frame:      { count, full, partial, fullRatio }        // 上屏帧计数（整帧/局部）
imageCache: { size, cap:16,   hits, misses, evicts }    // 图像缓存
glyphCache: { size, cap:1024, hits, misses, evicts }    // 字形缓存
tree:       { windows, nodes, depth }                   // 全部窗口的 GUI 树规模
solid:      { effects }                                 // gx/solid 活跃 effect 数
warnings:   [ { at:"15:04:05", text } ]                 // 内核告警环形缓冲
```

所以指标通道是这 6 条（全部 `gx/dev` 真实数据，阈值越大越危险）：

| 通道 | 取法 | 阈值 warn/danger |
|---|---|---|
| 帧增量 | Δ`frame.count` / 样本 | 12 / 30 |
| 整帧增量 | Δ`frame.full` / 样本 | 4 / 10 |
| 字形缓存命中 | Δ`glyphCache.hits` / 样本 | 400 / 1200 |
| 字形缓存占用 | `glyphCache.size / cap × 100` | 60 / 85 |
| 图像缓存占用 | `imageCache.size / cap × 100` | 60 / 85 |
| 整帧比例 | `frame.fullRatio × 100` | 40 / 70 |

`stats` 在本应用里的真实用途：每张卡片下方的滚动统计（均值/低/高/样本数）
就是 `stats.describe(history)` 算的。

侧栏还展示（也都是实测读数）：

- `gx/screen.screens()` —— 显示器拓扑。本机实测：1 块
  `{id:"\\\\.\\DISPLAY1", x:0, y:0, width:3440, height:1440, workWidth:3440,
  workHeight:1392, scale:1, primary:true, foldable:false, posture:"flat", hinge:null, regions:[]}`
- `gx/screen.windows()` —— 本进程窗口列表（id/标题/位置/所在显示器/active）
- `gx/device.battery()` —— 台式机实测 `{supported:true, level:-1, levelPercent:-1,
  charging:true, chargingType:"ac", temperature:-1}`（电量 -1 = 未知，故显示「未知/供电中」）
- `process.platform` / `process.pid`

## 运行

```bash
# 仓库内直接跑（基线 b381cbc 构建的二进制）
F:/tmp/gox-b10.exe apps/sysmon/src/main.js

# 启动即展开多屏/副窗（自动化验证用）
F:/tmp/gox-b10.exe apps/sysmon/src/main.js --multiscreen

# 或走 npm（需 npm i -g @goxjs/goxjs）
cd apps/sysmon && npm run dev
```

## 打包成单文件

```bash
F:/tmp/gox-packager.exe apps/sysmon/src/main.js --gui --windowed --name sysmon --version 0.1.0 -o sysmon.exe
```

实测：`OK: F:\tmp\sysmon.exe (15 JS files embedded)`，产物可直接运行。

## 多屏模式

- 工具栏「多屏模式」按钮：对 `gx/screen.screens()` 里**每块显示器**开一个独立窗口
  （`render(<window>)` 一次一窗，`handle.moveTo(d.x, d.y)` 定位到该屏坐标），
  各窗口共享同一份采样（store 是模块作用域单例），副窗标题为
  `系统资源监视器 · <显示器名>`。再点一次 = 全部关闭。
- `--multiscreen` 命令行参数：启动即展开（供自动化验证）。
- **单屏降级**（本机只有一块屏时的实测行为）：没有第二块屏可放时，退而在
  **屏幕外坐标**（`workWidth + 40`）开一个「模拟副屏」窗口，验证多窗口生命周期。
  实测：副窗创建于 `(3480, 40)`（屏幕宽 3440），可见、正常渲染共享曲线、可单独关闭，
  关闭后主窗继续工作。**这不是真实跨屏** —— 跨屏布局（`moveTo` 落到相邻屏、
  `onWindowDisplayChange` 换屏事件）在本机未验到。
- 窗口句柄能力实测可用：`moveTo / center / position / bounds / setTitle / close / setLevel`
  （level 取值 `""/normal/top/bottom`）/ `setFullscreen`。

## 压到的框架能力

- `<canvas>` 自绘实时折线：只用 7 原语里的 `fillRect / line / fillCircle / drawText`；
  **`line` 线宽固定 1px、颜色是第 5 个参数**（`ctx.line(x1,y1,x2,y2,color)`），无路径对象，
  折线靠应用自己切段。`onDraw` 里读 signal（本应用读 `revision`，每采样 +1）即自动重绘。
- y 轴量程自适应 + 整齐刻度（1/2/5×10^k）、阈值横线、末点告警着色 —— 全在 `src/lib/` 纯逻辑。
- 环形缓冲历史（120 点）、`setInterval` 500ms 心跳、`clearInterval` 暂停/恢复。
- `select` 受控下拉、`grid` 卡片栅格、`view each` + `key`、`spacer flexGrow`、
  容器级 `onClick`（点卡片切通道）。
- 多窗口 `render` + 句柄 `moveTo/close`；`gx/dev` / `gx/screen` / `gx/device` 三个宿主模块。

## 目录结构

```
apps/sysmon/
  README.md  package.json  gox.json      # appId com.gox.apps.sysmon
  src/
    main.js        # 入口：render + 启动采样 + --multiscreen
    app.js         # 根组件（页头/工具栏/折线图/卡片/侧栏布局）
    store.js       # 模块作用域 signal + 采样循环 + stats.describe
    theme.js       # 设计令牌（深色）
    multiscreen.js # 多屏/副窗生命周期
    lib/           # 纯逻辑（不 import gox，探针可单跑）
      ring.js      #   环形缓冲
      scale.js     #   量程自适应 + 坐标映射（越界钳边、空数据/全等值/NaN 都有确定行为）
      polyline.js  #   点集/线段生成
      threshold.js #   ok/warn/danger 分档
      ticks.js     #   数值轴整齐刻度 + 时间轴（采样序号）刻度
      channels.js  #   指标通道定义与采样规则
    components/
      chart.js     # canvas 折线图
      card.js      # 数值卡片（含 stats 统计行）
      toolbar.js   # 通道选择/暂停/清空/多屏/刷新
      sidebar.js   # 显示器/窗口/运行时资源/宿主/告警
```

## 已知限制与暴露的缺陷（写这个应用撞出来的）

1. **`<canvas>` 没有 `arc` / 路径 / 变换 / getImageData**（原语只有 fillRect/strokeRect/
   fillCircle/strokeCircle/line/drawText/clear 七个）⇒ 饼图、环形图做不了，
   本应用只能折线 + 数值卡片；`line` 也没有线宽维度（要粗线得叠多条）。
2. **运行时无 `Date` / `performance`** ⇒ 拿不到墙钟与毫秒级耗时，"时间轴"只能是采样序号，
   也测不了真实的心跳抖动（这是压测目标里没验成的一项）。
3. **`stats` 是数学模块不是系统指标源**（见上），应用名里的"系统资源"实际是
   "Gox 运行时资源" —— 已按 README 口径如实呈现。
4. **【VM 缺陷】跨模块组件的 `onDraw` 引用模块顶层导入绑定会 TDZ**（b381cbc 实测）：
   最小复现 —— 组件 A 定义 `<canvas onDraw={(ctx)=>{…bump()…}}/>`（`bump` 是 A 里
   import 的普通函数），由另一文件 `render(<A/>)`，挂载即抛
   `ReferenceError: Cannot access lexical declaration before initialization`；
   onDraw 只用字面量/局部 const 则正常。**规避**：把导入绑定在组件函数体内别名成
   局部变量（本应用 `chart.js` 顶部那一排 `const col = colors; …` 就是干这个的），
   或全部经 props 传入。最小复现已归档：`D:/code/Gox/.workbuddy/tmp/sysmon-repro/`
   （`bug_run.js` 报 TDZ、`ok_run.js` 用别名规避后正常）。
5. **【VM 缺陷】同一根因波及响应式闭包**：函数子节点/函数 prop 直接引用模块顶层
   导入绑定时，**初始渲染正常、重渲染时失败**（文本整行被吞成空白）—— 与
   apps-notes.md 里 status-bar 那条同源，本应用在卡片数值/侧栏读数上再次撞到。
   规避同上（`card.js` / `sidebar.js` / `toolbar.js` / `app.js` 的别名块）。
6. **【口径】组件标签的 children 是位置参数**：parser 层降级为 `Comp(props, ...children)`，
   不会挂到 `props.children` ⇒ 「接收 children 的面板组件」写法不成立，本应用
   侧栏因此改成内联展开（`sidebar.js` 的 `Head` 局部辅助 + 直接铺内容）。
7. **单屏降级验证**：多窗口生命周期已验证（创建/显示/共享采样/单独关闭），
   但**真实跨屏**（副屏坐标、`moveTo` 跨屏、`onWindowDisplayChange`）本机只有一块
   3440×1440 显示器，未验到。
8. **默认通道选了「字形缓存占用」**：因为「帧增量」在本应用里每样本恒为 1
   （每 tick 自绘一帧）——真实但是条平线。这是数据源性质，不是 bug。
9. 按纪律应在 `gfx/apps_smoke_test.go` 用例表加一行冒烟，但本次任务**禁止改动引擎
   目录**，故未加 —— 需由 lead 补。

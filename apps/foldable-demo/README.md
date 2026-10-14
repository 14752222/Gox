# foldable-demo —— 折叠屏适配演示（四场景）

一个窗口里并排四个场景，每个回答一个折叠屏适配的具体问题：

- **S1 单双栏自适应** —— 列数**只由宽度决定**（`widthClass()` / `useWindowInfo()`），
  不信姿态。上下折展开后宽度可能仍 < 600dp，拿 `posture()` 推布局必然翻车
  （官方时序里尺寸变化还早于姿态变化，会落后一帧）。
- **S2 折痕避让** —— `avoidReserved` 是 **opt-in**：内核元素默认不避让（老界面
  不会莫名位移）。折痕带只是"分隔"，是否让开由应用决定。`<scroll>` 上不适用
  （会收缩滚动可用区、与滚动条语义打架，内核忽略并告警）。
- **S3 Tabletop** —— 横折（`hingeOrientation() === "horizontal"`）→ 上下分屏，
  上段当"显示屏"、下段当"操作台"。`layoutMode().suggested` 只给建议，框架永不动你的布局。
- **S4 跨屏拖拽** —— 窗口在两个物理屏之间移动时，`screenOf()` / `windowInfo().screenId`
  的变化。折痕坐标是**显示器坐标**（设备像素）。

## 运行

```bash
go build -o /tmp/gox-current ./cmd/gox
/tmp/gox-current apps/foldable-demo/src/main.js
```

把窗口拖窄到 600 以下，S1 会自动从双栏变单栏 —— 这份代码一个字都不用改。

## 压到的能力

| 能力 | API |
|---|---|
| 姿态读数 | `gx/screen` 的 `posture()` / `hinge()` / `hingeOrientation()` / `splitRatio()` / `regions()` |
| 姿态订阅 | `gx/screen` 的 `usePosture()` / `useWindowInfo()` / `screenOf()` |
| 姿态上报 | `gx/screen` 的 `reportPosture(opts)` / `resetDisplays()` |
| 布局建议 | `gx/viewport` 的 `layoutMode()` / `widthClass()` / `hasFold()` / `reservedRegions()` |
| 断点订阅 | `gx/viewport` 的 `useBreakpoint()` / `onViewportChange()` |
| 折痕避让 | 元素属性 `avoidReserved="division"`（opt-in） |

## 结构

```
src/
  main.js        入口：一个窗口 900×760，四个场景
  app.js         根组件 + 四个场景组件
  store.js       signal：形态切换 / 读数快照 / 事件日志
  theme.js       设计令牌（含折痕可视化色）
  lib/fold.js    纯逻辑：读数文案格式化 + 列数判定
```

## 四条纪律（本演示要传达的核心）

1. **布局只信宽度，不信姿态** —— `posture()` 变之前尺寸可能已经变了。
2. **订阅型读数先取再分支，别塞进三元** —— 两段式 `useXxx()()`：外层拿 getter，
   内层再调才订阅。首帧没走到的分支不建立订阅，effect 零依赖、永不重跑，界面就像"死"了一样。
3. **折痕/保留区坐标是显示器坐标** —— 全屏时等于窗口坐标，分屏/自由窗口下
   要减去窗口在屏上的原点。
4. **没人上报就恒为平展** —— 桌面/无头宿主没有折叠姿态查询 API（Windows 没有
   WinRT、X11 没有对应扩展）。框架的立场是**提供通道不猜姿态**；真机由平台壳工程
   上报（Android 的 `DisplayManager` / iOS 的 `UITraitCollection`），脚本写法完全一样。

## 已知限制

- 本演示自己扮演"宿主"：点形态按钮 = 往内核上报一次姿态。真机上这步由平台壳
  工程做，脚本侧读数写法不变。
- 首次上报必须带 `width` / `height`（+ `foldable`），否则"窗口所在显示器"解析不到，
  之后的姿态读了也是空的 —— 这是最容易踩的一条。
- 应用退出时应调 `resetDisplays()` 把屏表交还后端；测试里对应
  `ResetDisplaysForTest()`。

# 如何在 Gox 里做折叠屏适配

> 看板单 rKRbyq（S5/N5）。配套可跑代码：
> [`testdata/foldable_demo.js`](../testdata/foldable_demo.js)（八形态切换）、
> [`testdata/adaptive_list_demo.js`](../testdata/adaptive_list_demo.js)（单双栏自适应，README 首屏示例）。
> 回归测试：`gfx/foldable_demo_test.go` / `gfx/lifecycle_fold_test.go` / `gfx/adaptive_list_test.go`。

## 0. 先把那个坑写在最前面

Gox 的折叠屏 API 里，**纯读数不订阅，订阅型读数才订阅**：

```js
import { hasFold, useLayoutMode, useReservedRegions } from "gx/viewport";
import { posture } from "gx/screen";   // posture 在 gx/screen，别 import 错模块

// ❌ 错：首帧 hasFold() 为 false ⇒ 右侧的 useReservedRegions()() **一次都没执行**
//    ⇒ 这个 effect 零依赖 ⇒ 之后设备折起来，这一支永不重跑，而且没有任何警告。
const regions = hasFold() ? useReservedRegions()() : null;

// ✅ 对：订阅型读数无条件先读一次，再决定显示什么
const rr = useReservedRegions();           // () => {division:[...], occlusion:[...]}
const regions = rr();                       // 每次重算都会走到这里
```

`hasFold()` / `posture()` / `breakpoint()` 是纯读数 —— 它们给你**此刻的值**，但不登记
依赖；`useXxx()` 返回的取值函数（两段式 `useLayoutMode()()`）才是订阅。把纯读数写进三元
的第一操作数或 `&&` 左侧，短路掉的那一支就成了「首帧定死」的死代码。

这条被钉成了可执行的回归：`gfx/adaptive_list_test.go` 断言 demo 里的纯读数快照在拖宽后
**仍停在首帧的值**，而订阅型读数已经变了。你写的代码如果将来踩上这个坑，测试比你先知道。

## 1. 姿态不驱动布局 —— 用尺寸，别用 foldStatus

这是**设计取舍**，不是功能缺失。三个理由：

1. **平台官方不推荐**：华为《多设备适配屏幕差异》明说不推荐用折叠状态监听来实现
   响应式布局；Android 的 Material 窗口尺寸类同理（断点判尺寸，不判铰链）。
2. **用 foldStatus 推布局会判错**：上下折（Pocket / nova Flip 类）展开后宽度仍
   **< 600dp** —— 折着时是竖条手机，展开还是竖条手机。用「折叠 = 小屏、展开 = 大屏」
   的直觉推布局，会给出错误的「大屏」判断。这正是 `foldable_demo.js` 里 M-F2 那档
   要演示的东西：展开后 `layoutMode().suggested` **不变**。
3. **Gox 内核照此建模**：`deriveSizeClasses`（gfx/viewport.go:426）只做**宽三档**
   （600/840dp）**高两档**（480dp），姿态信息只参与 `layoutMode().suggested`
   的「该长成什么形态」提示，不直接改布局。

所以正确的姿势是：

```js
import { useBreakpoint, matchBreakpoint } from "gx/viewport";

const bp = useBreakpoint();
const layout = () => {
  bp();   // 无条件读 = 订阅（见 §0）
  return matchBreakpoint({
    sm: { cols: 1, side: false },   // <600dp  单栏
    md: { cols: 2, side: true },    // >=600dp 双栏
    lg: { cols: 2, side: true },
    xl: { cols: 2, side: true },
  });
};
```

布局分支由断点名声明，而不是把同一个宽度判断散落在 N 处 if 里 —— 加一档只改表。

## 2. 铰链角度：可以用来做交互特效，不能用来做布局

`DisplayHinge`（gfx/screen.go:44）**刻意没有 Angle 字段**。iOS 的 hinge API 与 Android 的
FoldingFeature 都把铰链角度划给「交互与特效」：Android 甚至不对外暴露精确角度。三端口径
一致 —— 角度是玩具，尺寸是骨架。将来若真要加，字段注释里必须写死「仅交互、不布局」，
否则一定会有人在布局引擎里读它。

## 3. 折痕避让：division 与 occlusion 是两个不同的坑

`reservedRegions()` 返回两类保留区，避让策略完全不同：

| 类型 | 是什么 | active 的条件 | 避让策略 |
|---|---|---|---|
| `division` | 折痕把大屏切成两半的那条带 | **只有设备实际折着时**才有宽度（平放时宽度 0、inactive） | 半折时把内容分到两侧（`layoutMode().suggested === "dual"` 的物理依据） |
| `occlusion` | 盖在内容上方的遮挡区（屏下摄像头） | 与折不折无关 | **永远**避开：别把可点击内容放那里 |

**单位坑**（demo 里最容易错）：`reservedRegions()` 的 x/y/width/height 是**设备像素**；
`safeAreaStyle()` 的产出与脚本显式写的尺寸属性是 **dp**（内核每帧乘 `Display.Scale`，
见 gfx/density.go）。不要把前者的数字直接喂给布局属性 —— 先除 `deviceInfo().pixelRatio`
（gx/device，这是脚本侧读密度系数的入口，没有独立的 `pixelRatio()` 函数）。

```js
// ❌ 设备像素当 dp 用：高分屏上避让量偏大
<column paddingLeft={regions()[0].width}>

// ✅ 先换算
<column paddingLeft={regions()[0].width / deviceInfo().pixelRatio}>
```

## 4. Tabletop（桌面/帐篷态）怎么做

Tabletop 是折叠屏**独有**的交互形态：半折放桌上，下半屏变成触控板/控制区。做法上它就是
「横折痕 + division 避让」的组合拳：

```js
const lm = useLayoutMode();
const rr = useReservedRegions();

const body = () => {
  const m = lm();          // 订阅
  const regions = rr();    // 订阅
  const div = regions.division.find((d) => d.active && d.orientation === "horizontal");
  if (m.posture === "half-open" && div) {
    // 上半屏内容 / 下半屏控制区，界线就是折痕（设备像素 → dp 记得除 deviceInfo().pixelRatio）
    return { split: "horizontal" };
  }
  return { split: "none" };
};
```

`foldable_demo.js` 的 Tabletop 档就是这么演示的（横折痕、上下分栏）。

> 注：Tabletop 归 posture 枚举还是 layoutMode().suggested 还是 demo 层组合，是
> 看板单 rc2XuQ 的待拍板项。在拍板前，上面这种「posture + 横折痕 division」的
> demo 层组合写法是稳定可用的。

## 5. 权限与能力：拒绝与不支持都要能看

折叠屏 demo 里还有两条非形态的分支，做适配时别忘了：

- **权限拒绝**：`reportPermission("camera", "denied")` 后 UI 要降级（给个入口去设置），
  不是空白也不是崩。三端口径见 [mobile-adaptation.md §12](mobile-adaptation.md)
  （`gfx/permission_matrix_test.go` 把六态穷举钉成了断言）。
- **能力 unsupported**：用 `canIUse("foldable")` 事前判断；探测要用**方法名**
  （`canIUse("permission.request")` 才反映宿主，`canIUse("permission")` 对内置模块恒 true）。

## 6. 三端验证范围（诚实口径）

能力矩阵按**实际验证面**写（看板单 rSm7zW，README 同口径）：

| 平台 | 验证范围 |
|---|---|
| **Android** | CI 的 pixel_fold 模拟器（API 34+）折叠态切换矩阵 + 截屏；真机 `scripts/mobile-device-accept.sh --rounds 20`（§4 生命周期循环：崩溃/ANR 计数 + 内存差值） |
| **iOS** | 模拟器机型矩阵（iPhone 16 / 16 Pro Max / iPad）× 竖横屏截屏；**模拟器没有折叠屏**（`reservedRegions` 是 iOS 27.1+，只报 flat，已知边界非 bug）；真机验收待做 |
| **鸿蒙** | 交叉编译 + 宿主契约测试 + assembleHap 构建通过；**模拟器验收待做**（HAP 签名要在 DevEco GUI 里配华为账号，命令行替不了） |

不写「已支持鸿蒙」，只写到哪验到哪。

## 7. 一页检查清单

- [ ] 布局分支只认断点名（600/840dp），不认姿态
- [ ] 订阅型读数（`useXxx()()）`在函数体**无条件**先读一次
- [ ] 纯读数（`hasFold()` / `posture()`）不写进三元/&& 的短路位置
- [ ] 折痕避让前把设备像素除 `deviceInfo().pixelRatio` 换成 dp
- [ ] `occlusion` 永远避让，`division` 只在折着时避让
- [ ] 权限拒绝与能力 unsupported 都有可见的降级 UI
- [ ] 桌面先跑 `gox testdata/foldable_demo.js` 过一遍八形态，再上真机

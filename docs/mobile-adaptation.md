# 移动端适配规范：触控目标 / 软键盘 / 安全区 / 手势态 / 断点（T23）

> 状态：v1 规范已定稿（2026-09-27）。S3 组件库一期（表单类 15 个）起按本规范实现；
> S4 二期与后续组件沿用。与 T10 无障碍规范的对齐见 §8。

## 0. 为什么规范先行

S3/S4 将交付约 30 个组件。若等 P1「迈向移动 v0.5.0」再回头补移动端行为，
等于整库返工：触控目标、安全区、键盘避让都是**结构级**约束（padding、最小
尺寸、事件模型），事后补比先定难一个量级。本文把标准钉在组件动工之前。

范围声明：桌面端行为不受本规范限制（鼠标精度高、无安全区）；同一组件在移动
断点下必须满足本文，桌面断点沿用桌面交互惯例。

## 1. 触控目标

| 项 | 标准 | 依据 |
|---|---|---|
| 最小可点尺寸 | **44×44pt（iOS）/ 48×48dp（Android）**，取两者较大 → 组件实现用 **48dp** 为最低档 | Apple HIG / Material |
| 可点区 < 48dp 时 | 视觉尺寸可以小（如图标按钮 24dp），但**命中区必须扩到 48dp**（内边距或 hit-test 扩张） |  |
| 相邻可点目标间距 | ≥ 8dp | 防误触 |
| 文本输入框高度 | ≥ 44dp | 含 placeholder 可读性 |

落地方式：token 档位 `touchTarget = 48`（见 §7）。表单类组件（button / input /
select / switch / checkbox / radio / slider / …）的默认尺寸档与最小档都必须 ≥
touchTarget；组件内部不得出现小于 48dp 且无命中扩张的可点元素。

## 2. 安全区

数据链路已通：宿主上报（iOS `gox_set_insets` / Android `nativeSetInsets`）→
`gfx.Post` → `ReportViewport` → 脚本侧 `gx/viewport` 的 `useInsets()` /
`safeAreaStyle()`。

规则：

1. **组件库不写死安全区数值**。所有贴边组件（TabBar、TopBar、全屏容器、弹层
   贴边场景）必须消费 `useInsets()` 做响应式 padding，方向对应：
   顶部组件吃 `top`，底部组件吃 `bottom`，侧滑/侧边栏吃 `left/right`。
2. 组件的 padding token 是**内容边距**，不包含安全区；安全区由容器层叠加。
   两者不得混写在一个常量里。
3. 桌面端 insets 恒为 0，同一份代码在桌面自然退化为普通 padding —— 不需要
   条件分支，这正是响应式消费的价值。

参考实现：`app/android/app/src/main/assets/app.js`（M1 验收脚本）里 column
对 `paddingTop/Bottom/Left/Right` 的响应式写法。

## 3. 软键盘

链路：宿主 IME（iOS 隐藏 UITextField / Android BaseInputConnection，结果提交
制）→ `ReportViewport` → `gx/viewport` 的 `keyboardVisible` /
`useKeyboardHeight()`。

规则：

1. **输入类组件获得焦点时必须保证自身可见**：容器层用 `useKeyboardHeight()`
   抬升或压缩内容区；组件自身负责 `scrollIntoView` 语义（滚到最近可见滚动容器）。
2. 键盘弹出时，**底部贴边组件（TabBar 等）应让位或隐藏**，不得浮在键盘上。
3. 输入框 `onInput` 触发的受控 value 必须接 `createSignal`（普通 let 无依赖，
   响应式 prop 停在初值）—— 这是项目已踩过的坑，组件库内部实现同样适用。
4. IME 结果提交制是 v1 边界：组合输入（拼音中间态）不逐键上报，确认后才提交。
   组件不得假设"每个按键都会触发 onInput"。

## 4. 手势交互态

| 手势 | 桌面等价物 | 移动端标准 |
|---|---|---|
| 按压（press） | hover + click | 可点组件必须有**按压态**视觉反馈（透明度/明度变化），触发延迟 ≤ 100ms |
| 长按 | 右键（onContextMenu） | v1 边界：内核尚无 `onLongPress`（见 §9 差距清单）。S3 前组件不得自造长按；右键语义在移动端暂不映射 |
| 滑动（swipe/scroll） | 滚轮 | 滚动容器统一走 scroll 组件；v1 不做组件级滑动手势（与 PageController 边界一致） |
| 返回 | 无 | 页面级返回走 `gx/app` 的 `onBackPress` / `reportBackPress`，组件不自行拦截系统返回 |

原则：**hover 态在移动断点自动映射为按压态**（同一视觉 token），组件实现里
hover 样式与 active 样式引用同一档 token，禁止两套独立配色。

## 5. 移动端默认值与断点

断点直接消费 `gx/viewport` 现成 API：`widthClass()`、`isCompactWidth()`、
`isMediumWidth()`、`isTabletLayout()`、`isSplit()`、`multiWindow`。组件库
**不自定义断点常量**，避免与 viewport 层两套标准。

| class | 宽度 | 组件行为基准 |
|---|---|---|
| compact（手机竖屏） | < 600dp | 单列布局；弹层改全屏/底部贴边；Table→List 降级 |
| medium（平板竖屏/折叠） | 600 – 840dp | 双列可选；弹层维持居中 |
| expanded（平板横屏/桌面） | > 840dp | 桌面惯例 |

- `widthClass()` 返回三档字符串 `"compact"` / `"medium"` / `"expanded"`，
  阈值 600 / 840dp（2026-10-01 由两档扩为三档 —— 折叠屏展开态正落 medium，
  两档口径下无法被规范表达）。
- `isMediumWidth()` 是 medium **单档**判定；`isTabletLayout()` 保持历史语义
  **medium 或 expanded**（>= 600dp 即真），老代码不用改。
- 高度仍是两档（阈值 480dp），不参与本表。

默认值：

- 字号：内核 `defaultFontSize()` = 16dp × 显示器 Scale，组件不显式写 font 时
  物理尺寸自动正常；组件默认字号档见 §7。
- 颜色/主题：沿用 S3 设计 token（T07 暗色模式体系），移动端不另立色板。
- 方向：竖屏为一等公民；横屏 compact 宽度同样落入 compact 档，行为一致。

## 6. 弹层与浮层（移动端特化）

- dialog / menu-popup / toast 在 compact 断点：dialog 底部贴边（含
  `useInsets().bottom`），menu-popup 全屏遮罩 + 列表，toast 顶部贴边避开键盘。
- 遮罩点击关闭在移动端必须可用（桌面端可依赖 Esc，移动端没有 Esc）。

## 7. 设计 token 档位（移动端）

单位一律 dp（脚本侧用 `pixelRatio` 换算物理像素，见 gx/device）。

```
// 触控
touchTarget      = 48        // 最小命中区（§1）
touchGap         = 8         // 相邻可点目标最小间距
inputHeight      = 44        // 输入框最小高度

// 间距（4 的倍数档，安全区除外）
spacing          = 4 | 8 | 12 | 16 | 20 | 24 | 32

// 圆角
radius           = 4 | 8 | 12 | 999(pill)

// 字号（内容字号，全局默认 16 由内核保证）
font             = 12 | 14 | 16 | 20 | 24

// 图标（可点的图标按钮命中区按 touchTarget 扩张）
iconSize         = 16 | 20 | 24
```

落地路径：本表是 S3 表单组件实现时的**唯一档位来源**；组件 props 的默认值、
内部 padding、最小尺寸约束都必须取自本表（允许 props 覆盖，不允许内部硬编码
表外数值）。token 常量随组件库（gox-npm 子模块）发布，本仓库文档为规范源。

## 8. 与无障碍规范（T10）的对齐

| 项 | 本规范 | T10 | 关系 |
|---|---|---|---|
| 触控目标 | 48dp 命中区 | WCAG 2.5.5 (AAA) 44×44px | 本规范更严，以 48dp 为准 |
| 键盘导航 | 移动端软键盘属 IME 链路 | Tab 焦点顺序、focus 可见性 | 互补；焦点环样式两规范共用 token |
| 色彩对比 | 不另行规定 | 文本对比 ≥ 4.5:1 | 以 T10 为准 |
| 读屏（VoiceOver/TalkBack） | v1 不做 | v1 不做 | 双方 v1 边界一致，P1 再立项 |

冲突裁决原则：移动端体验项以本规范为准，桌面/通用可达性项以 T10 为准；
两规范都不覆盖的场景在组件评审会上定，定完回填对应文档。

## 9. 现状与落地差距（v1 边界，防止被当 bug）

| 项 | 现状 | 差距 |
|---|---|---|
| 安全区 | 宿主上报链路已通，`useInsets()` 可用 | 无 |
| 键盘高度 | `keyboardHeight` / `useKeyboardHeight()` 已注册 | 各宿主实测待 T24 回归矩阵覆盖 |
| 断点 | `widthClass` / `isCompactWidth` / `isMediumWidth` / `isTabletLayout` 已注册（三档，600/840） | 无 |
| 折叠屏 | 内核数据模型 + `reportPosture` 通道已通；`gx/viewport` 有 `reservedRegions()` / `hasFold()` / `layoutMode()`；Android / iOS 宿主已接上报 | 鸿蒙宿主待接（HF2）；折叠态**接续**（页面栈/滚动位置）v1 不做 |
| 长按手势 | 内核无 `onLongPress` | S3 前如组件评审要求长按，先在 gfx 内核立项 |
| 逻辑像素 | 换算靠脚本侧 `pixelRatio`（gx/device） | 内核收编 dp 单位是 P1 候选项 |
| IME | 结果提交制 | 组合输入逐键上报属 P1 |

## 10. 验收（对齐任务验收标准）

组件评审时逐条核对：

1. 组件可点元素在移动断点下命中区 ≥ touchTarget（§1/§7）。
2. 贴边组件消费 `useInsets()`，桌面端退化为普通 padding（§2）。
3. 输入类组件在键盘弹出后自身可见（§3）。
4. hover 与按压态共用 token（§4）。
5. 断点行为按 §5 表执行，组件内无自定义断点常量。
6. Given 同一组件，When 在桌面/移动断点渲染，Then 交互态与触控目标均符合本规范。

移动断点冒烟统一用 iOS 模拟器 + Android 模拟器（构建链路见
`.github/workflows/mobile-smoke.yml`，T22）。

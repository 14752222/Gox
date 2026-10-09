# 移动端真机回归矩阵与 Release 前 Checklist（T24）

> 目的：v0.1.0 起每次 release 前执行一轮 iOS + Android 双平台真机回归并留档，
> 让移动端问题在发版前暴露，而不是等用户反馈。
> 关联文档：`docs/platform-config.md`（构建与验收步骤）、
> `docs/mobile-adaptation.md`（T23 适配规范，回归项的判定依据）、
> `docs/mobile-perf-baseline.md`（真机性能基线：指标口径与空表，基线数字待采集）、
> `.github/workflows/mobile-smoke.yml`（T22，CI 构建冒烟——真机回归的前置）。
>
> **一次只插一次设备**：接上设备后按 §1→§6 顺序一次跑完（看板单 rpr9zf）。为此
> §1~§3 需要的真机数据（密度 / 触控与多指手势 / 帧率·内存·冷启动）由
> `scripts/mobile-device-accept.sh` 一条命令取完，见 §4 末尾。

## 1. 触发时机

| 时机 | 是否执行 | 范围 |
|---|---|---|
| v0.1.0 及之后每次 release | 必须全量 | §3 全部条目 + §5 记录留档 |
| 移动端内核/gfx 大改（字体、事件、viewport） | 必须全量 | 同上 |
| 移动端壳工程或权限声明变更 | 触发相关项 | 至少 §3.1/3.5 |
| 纯桌面/引擎内部改动 | 不执行 | CI 冒烟（T22）已兜底 |

## 2. 固定测试机矩阵

> 原则：iOS + Android 各至少一台**长期固定**的测试机；刘海/打孔屏必选一台
> （安全区是高危区），Android 必须支持手势导航。发版回归只跑矩阵内机型，
> 临时借机另行备注。

| # | 平台 | 机型 | 系统版本 | 备注 |
|---|---|---|---|---|
| M1 | iOS | _（待定，占位）_ | _待定_ | 需刘海/灵动岛屏（验证顶部安全区） |
| M2 | Android | _（待定，占位）_ | _待定_ | 需支持手势导航（验证底部 insets 与返回） |
| M-F1 | Android | _（待定，占位）_ | _待定_ | **左右折**（Mate X / Z Fold 类）：跨 600dp 断点 |
| M-F2 | Android | _（待定，占位）_ | _待定_ | **上下折**（Pocket / nova Flip 类）：展开不跨档，验证"不误判" |
| M-F3 | iOS | _（待定，占位）_ | _iOS 27.1+_ | **折叠机型**（iPhone Duo 类）：`reservedRegions` 需 iOS 27.1+，老系统走降级分支 |

维护约定：换机时更新本表并在 git 提交信息注明；系统大版本升级后需重跑一轮
全量回归再继续作为矩阵机型。

> 折叠机型**可以缺位**：`gfx/mobile` 的折叠逻辑是纯 Go（Windows 可单测），
> 上报通道也能在桌面注入 —— 矩阵里没有折叠机时，§3.9 的桌面可验证部分照跑，
> 机型相关的项标豁免。

## 3. 回归 Checklist

执行方式：每项通过打 ✅，失败记 ❌ + 一行现象 + issue 链接。**任何一项 ❌
即阻断 release**（明确豁免的 v1 边界除外，见 §6）。

其中**数据类**条目（屏幕密度、触控/多指证据、帧率、内存、冷启动）不要靠人肉眼
估 —— 交给 `scripts/mobile-device-accept.sh` 采集，结果落在
`bench-results/device-accept-<日期>-<机型>.json`，口径与空表见
`docs/mobile-perf-baseline.md`。本节其余条目（功能/交互/权限/生命周期）仍需人工
按下面逐条打勾。

### 3.1 安装与启动
- [ ] 全新安装（先卸载）后首次启动不崩溃，3 秒内出画面
- [ ] 覆盖安装（旧版本 → 新版本）后启动正常，本地存储（gx/storage）数据保留
- [ ] 图标/启动屏/显示名/版本号与 gox.json 声明一致

### 3.2 渲染与基本交互
- [ ] 默认示例（Counter）按钮点击、文本更新正常
- [ ] 列表滚动流畅、无花屏/黑块（软光栅链路）
- [ ] 图标（canvas 自绘 / image 资源两种）显示正常
- [ ] 暗色模式（若已启用 T07 token）跟随系统切换

### 3.3 导航与安全区
- [ ] TabBar 切换正常，图标/文字不被状态栏、Home 指示条遮挡（useInsets 生效）
- [ ] Android 手势返回 / iOS 侧滑返回触发 `onBackPress` 链路正确
- [ ] 页面栈返回后状态保持符合预期（不重复初始化）

### 3.4 权限申请
- [ ] 首次触发权限弹窗文案与 gox.json 声明一致（iOS NS*UsageDescription /
      Android 运行时权限，见 gox sync 注入）
- [ ] 拒绝权限后功能给出引导（openAppSettings 链路）
- [ ] 未声明的权限不触发系统弹窗（App Store 审核红线）
- [ ] **基线权限在清单里**（`INTERNET` + `ACCESS_NETWORK_STATE`，`config/`
      的 `AndroidBootstrap` 铺底）：`aapt2 dump permissions <apk>` 核对。
      缺失的症状是**全新安装启动即崩**（宿主 `registerNetworkCallback` 抛
      `SecurityException`），2026-10-02 实测踩过。模板与手写壳三处镜像，
      改动任一处触发本项。

### 3.5 前后台与生命周期
- [ ] 切后台再回前台，画面恢复、事件继续响应（onAppStateChange）
- [ ] 切后台期间来电话/通知横幅，回前台无渲染残留
- [ ] 低内存警告（onMemoryWarning）不崩溃

### 3.6 分享与媒体
- [ ] share 拉起系统分享面板（gx/app share）
- [ ] 拍照（takePhoto）与相册选图（chooseImage 多选）：回填**顺序与所选顺序一致**
      （历史验收项 r7I1Oh：PHPicker 保序回归点）
- [ ] previewImage 多文件预览可翻页、可关闭（含 iPad popover 形态，iOS）
- [ ] 拍摄/所选图片在引擎内渲染方向、缩放正确（EXIF 方向）

### 3.7 软键盘
- [ ] 点击输入框弹出软键盘，输入框不被键盘遮挡（useKeyboardHeight 链路）
- [ ] 中文输入（结果提交制）：确认后 onInput/受控 value 正确；拼音中间态不误提交
- [ ] 收起键盘（手势/返回）后页面恢复，底部 insets 恢复
- [ ] 键盘弹出时底部 TabBar 让位或隐藏（T23 §3.2）

### 3.8 分屏与形态
- [ ] Android 分屏 / iPad Split View 下宽度断点（widthClass）切换后布局正确
- [ ] 旋转横竖屏：安全区、布局、键盘链路均正常

### 3.9 折叠屏
断点现在是**三档**：`compact`（<600dp）/ `medium`（600–840dp）/ `expanded`（>840dp）。

| 机型形态 | 代码 | 断言 |
|---|---|---|
| 左右折（Mate X / Galaxy Z Fold 类） | M-F1 | 折叠态 `widthClass()="compact"`；展开态 `"medium"` 或 `"expanded"`（大折叠），布局随之重排，**第一帧就对**（不落后一帧） |
| 上下折（Pocket / nova Flip 类） | M-F2 | 展开后 `widthClass()` **仍可能 `"compact"`** —— 布局**不应**变化；`layoutMode().suggested` 也不应变成 `"tablet"` |
| 半折 / 悬停态 | M-F3 | `posture()="half-open"`；`hinge()` 非 `null`；`splitRatio()` 在 `[0.2, 0.8]`；`hingeOrientation()` 与折向一致（左右折 `"vertical"` / 上下折 `"horizontal"`） |
| 折展往返（≥3 次） | M-F4 | 折回去再折回来，**分栏比例不变**（折痕不随 `flat` 清除的回归点）；`hasFold()` 全程稳定为 `true` |
| 内屏 / 外屏切换 | M-F5 | 不崩、布局重排正确；`useWindowInfo()` / `useInsets()` 都跟着变 |

逐条清单：

- [ ] `posture()` 在四种姿态间切换（`flat` / `half-open` / `folded` / `unknown`）不崩溃
- [ ] `hasFold()` 是**结构性**信号：折/展往返期间恒为 `true`（不 flip-flop）
- [ ] `reservedRegions()` 三个键（`division` / `occlusion` / `all`）恒存在（空时是空数组）
- [ ] `avoidReserved="division"` 的元素在折痕侧留出避让带；**不声明则行为与旧版完全一致**（不避让）
- [ ] `<scroll avoidReserved=…>` 被忽略并打一条一次性告警（设计如此，不是 bug）
- [ ] 内外屏密度不同（如 2x / 3x）时，字体与触控目标物理尺寸正确（`Display.Scale` 链路）
- [ ] 折叠**接续**不在 v1 范围内：折展后页面栈/滚动位置丢失**不算缺陷**（见 §9 差距表）

> 桌面即可验证的部分（无需真机）：`posture` / `hinge` / `reservedRegions` / `layoutMode` /
> `splitRatio` 全部可用宿主上报通道注入（`reportPosture`，或 Go 侧
> `gfx/mobile.ReportDisplayFold`），断言脚本见 `gfx/foldjs_test.go`。

## 4. 真机安装与构建

构建命令与签名配置见 `docs/platform-config.md`（Phase 5 验收清单）与仓库内
壳工程 README（`app/android/README.md`、`app/ios/`）。速查：

```
gox build android <项目>            # 出 debug APK，adb install 安装
gox build ios <项目> --device       # 真机包（需签名证书）
gox build ios <项目> --simulator    # 模拟器（xcrun simctl install booted ...）
```

CI 已出每日冒烟产物（T22 工作流 artifact：gox-android-debug-apk /
gox-ios-simulator-app），可用于快速装机，但 **release 回归必须用 release 流水线
的正式包**（含签名链路验证，见 T17 文档，落地后互链于此）。

### 4.1 真机数据一键采集（Android，插一次设备跑完）

装好包后，**一次插设备**把 §1~§3 需要的真机数据全取走 —— 别验到一半才发现漏采：

```bash
bash scripts/mobile-device-accept.sh --package <appId>            # 全量采集 + 落 JSON + 打印摘要
bash scripts/mobile-device-accept.sh --dry-run                    # 没设备也能跑：只列命令，退出 0
bash scripts/mobile-device-accept.sh --device <serial>            # 多台在线时指定
```

- 前置自检：adb 不在 PATH 报「先装 Android platform-tools」并退出 2；设备
  offline/unauthorized 给对应处理提示并退出 3；非 arm64 ABI 会警告（模拟器数据
  **不能**当真机基线）。
- 采不到的项写 `null` + 原因，并汇总「本次未完成项」（退出 1）—— 补齐方式见
  `docs/mobile-perf-baseline.md` §5。**不要**用桌面数字或上一轮旧值填空。
- 采集完把数字回填 `docs/mobile-perf-baseline.md` 的空表（写清机型/系统/日期），
  存档 JSON 随 §5 的执行记录一起提交。
- iOS / 鸿蒙的等价采集方法**待补**（分别依赖 Xcode/Instruments 与 hdc + 签名材料）。

## 5. 执行记录

每轮回归在 release PR/issue 中留档，格式（另附本轮
`bench-results/device-accept-<日期>-<机型>.json` 存档，见 §4.1）：

| 日期 | 构建 commit | 包类型 | 机型（矩阵编号） | 结果 | 问题 |
|---|---|---|---|---|---|
| 2026-10-02 | _工作区（未打 tag）_ | debug（模拟器 x86_64 / API 34） | 模拟器，非矩阵机（M2 真机待补） | §3.1/3.2/3.3/3.4/3.7 模拟器可测项通过（详见 `app/android/README.md` M1/M2 实测记录）；中文拼音组合、真机项豁免 | 基线权限缺失启动崩（已修）；lockCanvas 单矩形脏区丢失（已修）；CursorAnchorInfo 缺 matrix 崩溃（已修） |
| 2026-__-__ | _commit_ | release | M1 | __ 项通过 / __ 项豁免 | #issue |
| 2026-__-__ | _commit_ | release | M2 | __ 项通过 / __ 项豁免 | #issue |

v0.1.0 发布前的首轮回归记录在此追加（发布阻塞项，完成后勾选）：

- [ ] v0.1.0 双平台全量回归完成，记录见上表

## 6. v1 已知边界（回归时的明确豁免项）

以下为当前架构的**有意边界**，不作为回归失败项；其中任一项立项修复后，
需同步把对应 check 升级为阻断项：

| 边界 | 出处 |
|---|---|
| IME 结果提交制：拼音组合中间态不上报，确认后提交 | T23 §3.4 / §9 |
| 逻辑像素换算靠脚本侧 pixelRatio，内核未收编 dp 单位 | T23 §9 |
| 无组件级长按/滑动手势（onLongPress 未实现） | T23 §4/§9 |
| 读屏（VoiceOver/TalkBack）v1 不做 | T23 §8 |

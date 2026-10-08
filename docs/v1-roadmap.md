# Gox v1.0.0 路线图（v0.9.0 → v1.0.0）

> 生成：2026-10-01 ｜ 基线：工作区版本号 **0.9.0**（`npm/package.json` 与 `cmd/gox/main.go` 一致；
> 最新提交 `efe8cc6 release: v0.8.0`；**修订（2026-10-04）**：v0.8.0 / v0.9.0 的 tag 与 GitHub Release **均已发布**（远端实测），本行原写的「尚未打 tag」已作废）
> 依据：17 篇 `docs/` + README + `app/NATIVE-HOST.md` + `agent_doc/undecided-and-unimplemented.md`（2026-09-18 快照；`agent_doc/` 过程文档已迁往项目共享资产盘、仅协作者可见，仓库里不留副本）
> + 代码 Grep 交叉验证。

## 判定口径

- **P0 = 阻断 v1.0.0**：语义错误或平台空白，用户会直接踩到。
- **P1 = 应该在 v1.0.0 收口**：不是新功能，而是把已承诺能力补齐 / 把"已知限制"消解。
- **P2 = 可延后到 v1.x**：文档已明说"v1 不做"，属设计边界，不算欠账。
- **不计入差距**：已拍板"不做"的项，只需在 v1 发布说明里复述清楚。

---

## 一、P0 —— 语言语义

| # | 项 | 现状证据 | 影响面 |
|---|---|---|---|
| P0-1 | ~~**`await` 被拒 Promise + `try/catch` 不生效**~~ **已于 2026-10-03 修复（`04cbdeb`）** | **原口径描述是错的**。真实症状不是「rejected 后生成器无法恢复」，而是 **generator 帧异常终止时未回收**：异常退出时 `runLoop` 是带着错误返回的、只有正常路径才 `popFrame` ⇒ generator 帧留在栈上压住外层 async wrapper 帧，wrapper 执行 `OP_RETURN` 时在错误栈基上取值，返回 `undefined` 而非 `__spawn` 的 promise ⇒ 调用方拿到非 Promise、`try/catch` 抓不到。修法 `unwindGenFrame`（只弹到 generator 自己那帧，绝不动外层）+ `genThrow` 必须在 `handleThrow` **之前**取帧。回归 `vm/async_throw_test.go` 6 例，变异验证过 | 影响面比「await 语义」更广：**任何从 generator 抛出的异常都泄漏帧**（含同步 `function*`）。同批还修掉 try/finally 三条路径全坏（`3148491`，见 §十一） |
| P0-2 | ~~**私有字段 / 私有方法 `#name`**~~ **已于 2026-10-03 落地** | 原口径（`PrivateName\|#name` 于 `parser/ compiler/ vm/` 零命中）已失效：lexer 新增 `PRIVATE_NAME` token，ast 新增 `PrivateIdentifier` 与 `IsPrivate` 标记，compiler 用 `\x00<类前缀>:<裸名>` 混编码键落地（`Object.keys`/`JSON.stringify`/for-in/`obj["#x"]` 全部摸不到，子类与同名类互不串槽）。测试 `vm/class_private_test.go` 9 例 + `parser/class_private_test.go` 4 例全绿（含 `#x in obj`、私有访问器、静态私有、私有与公有同名不冲突）—— 与原口径同为 2026-10-02 复测，**已不再零命中** | 现代 class 代码的基础设施（React 风格组件、库封装都依赖）。**test262 复测已完成（2026-10-04，本地浅克隆套件）**：该特性净 **+30 例**（7844/23726 = **33.06%**；私有字段之前的基线 7814 = 32.93%）。期间暴露并修掉一个**编译器 panic**（`delete obj.#x` 整进程 abort，`97b3c15`），另揭出 213 例规范早错缺口单独立项（`rpEXH2`）。`super(...args)` spread 与隐式构造函数缺口 **已于 2026-10-05 修复（`847992a`，见 §十一 H）** |
| P0-3 | ~~**`for await...of`**~~ **已于 2026-10-05 落地（rODXmB）** | 原口径（零命中）已失效：parser 识别 `for await (`（复用 for-of 头部解析 + `Await` 标志，AST `String()` 反映该形态）；compiler 新增 `compileForAwaitOfStatement`（`for await` 仅 async 函数体内合法，编译期 SyntaxError；绑定复用同步 for-of 的三形状：简单/解构/var）；vm 新增 `OP_GET_ASYNC_ITERATOR`（优先 `[Symbol.asyncIterator]()`，其次同步可迭代形状——generator/Iterator/带 next 对象/数组经 `GetIterable` 适配，规范允许 for await 消费同步可迭代）与 `OP_ASYNC_ITER_NEXT`（调 `next()` 驱动一步，结果经 `OP_YIELD` 交 `__spawn` 等 Promise，resolve 值取 `.done/.value`；reject 沿 GeneratorThrow 抛回 for-await 的 try/catch）。回归 `vm/for_await_test.go` 7 例（含 break/continue/解构/reject/空迭代器/类外非法） | 异步迭代是 async 生态的标准写法；曾试探后完整回退（runner 里保留回退记录），本次按「异步步进 = OP_YIELD 交 __spawn」的现有机制复用落地，未动 async 基础设施。**边界**：AsyncGenerator 声明语法 `async function*` 仍未实现（下附行），返回 Promise 的 `{next}` 对象迭代器已覆盖 |

> 附带：AsyncGenerator 目前"可编译执行但 `.next()` 协议不符"（T04 报告）。`async function*` 声明语法仍未实现（P0-3 落地时确认：parser 不识别该组合）；P0-3 的 for await 已覆盖「`{next}` 对象返回 Promise」与「同步可迭代被 for await 消费」两种主流形状，async generator 产出真异步流待声明语法落地后天然接通。

## 二、P0 —— 平台空白

| # | 项 | 现状证据 |
|---|---|---|
| P0-4 | ~~**鸿蒙（ArkTS）全线 unsupported**~~ **已于 2026-10-02 大幅收口** | 原口径基于 `app/NATIVE-HOST.md` 全 ⬜ 与 stub 宿主。现状：HF1/HF2 已落地 —— NAPI 通道层（`GoxDispatch` 序号分发 + `napi_module_register`）+ 装配层 + 交叉编译脚本（`scripts/build-harmony.sh`，OHOS clang + sysroot）+ ArkTS 壳工程（折叠上报链路含 `display.on('foldStatusChange')`），壳工程已通过命令行 `assembleHap` 构建并核对产物（`4af646c`）；契约测试 `fold_contract_test.go` 5 条鸿蒙静态契约全绿。**剩余缺口收敛为「模拟器未验收（HAP 未签名）」—— 验收口径：模拟器即可（2026-10-02 拍板，不依赖真机折叠屏；折叠上报 HF2 逻辑已由桌面资产测试覆盖，模拟器验 HF1）**，不再是平台空白 |
| P0-5 | **Android 侧代码本机从未编译验证** | `app/NATIVE-HOST.md`：Android libgox **未编译（无 NDK）**、Kotlin **未编译（无 gradle/SDK）**。虽然 M1 已在模拟器 x86_64/API 34 六项验收全通，但"能跑"与"能在这里重建"是两件事 —— v1 前应在有 NDK 的环境重跑 `bash scripts/build-android.sh`（看板跟踪：`rpr9zf`） |
| P0-6 | **真机（arm64）验证缺失** | `agent_doc/mobile-port-plan.md` §九：M1 验证跑在模拟器上，`input tap` 是注入事件；真机触摸、多指、性能均未验（看板跟踪：`rpr9zf`） |

## 三、P1 —— 交互能力补齐

| # | 项 | 现状证据 | 备注 |
|---|---|---|---|
| P1-1 | **Tab 键焦点遍历** | `docs/gui-guide.md` §3 末明文"尚未实现（需要 focusable 注册表）" | 桌面应用可访问性的地基；键盘用户完全无法操作 |
| P1-2 | **Linux/X11：IME 不支持** | `gfx/ime.go:15` + `gfx/x11/x11.go:12` 两处 `TODO(P2-7)`，需走 XIM 协议 | 中文/日文用户无法在 Linux 下输入 |
| P1-3 | **Linux/X11：剪贴板不支持** | `docs/gui-guide.md` §2 平台矩阵 | |
| P1-4 | **Linux/X11 未经实机验证** | `undecided-and-unimplemented.md` §三："代码已实现但未经 Linux 实机验证" | **注意**：`f7898a6` 已加 CI Linux xvfb 无头闸门 —— 需要确认这是否等价于"实机验证"，若只是无头冒烟则仍未达标 |
| P1-5 | **`gx/dialog` 缺 `saveFile`** | `docs/gui-guide.md` §2：macOS `NSSavePanel` **已接后端但脚本侧无入口** | 后端能力已在，纯补契约 + 导出；性价比最高的一条 |
| P1-6 | ~~**显示器插拔不派发 `onDisplayChange`（cocoa）**~~ **已于 2026-10-02 修掉** | 原 `docs/gui-guide.md` §2：win32 有、cocoa 待补 | 已接 NSApplicationDidChangeScreenParametersNotification + NSWindowDidChangeScreenNotification（见 §九） |
| P1-7 | **组件级长按/滑动手势（`onLongPress`）** | `docs/mobile-adaptation.md` §9 + `mobile-regression-checklist.md` §6 v1 边界 | 移动端标注"v1 不做"，但与上面两条不同：这是**触摸应用的日常操作**，建议在 v1 一并收 |
| P1-8 | **内核把一批样式值写死** | `docs/gui-patterns.md` §8 ❌ 清单：焦点虚线框色 / 滚动条与滑块色 / select 箭头 / progress 轨道色 / checkbox 未选中底色 / modal 遮罩 / switch 滑块 / disabled 降饱和 / 光标闪烁周期 | 0.9.0 已落 32 项颜色 token（`9611d73`），但这份 ❌ 清单是**另一批**；v1 前应收编成 token，否则主题切换有视觉残留 |

## 四、P1 —— 验收与发布流程

| # | 项 | 现状证据 |
|---|---|---|
| P1-9 | **移动端回归测试机矩阵仍是"待定，占位"** | `docs/mobile-regression-checklist.md` §2；该文档 §3 写着"任何一项 ❌ 即阻断 release" |
| P1-10 | **「v0.1.0 双平台全量回归完成」未勾选** | 同上 §5 |
| P1-11 | **`gui-tabbar.md` §7 真机验收清单全部未勾选** | Android/iOS 手势条、软键盘、返回键、分屏折叠、高 DPI、多窗口 |
| P1-12 | **iOS 真机链路** | `docs/platform-config.md`：模拟器已验收，剩 `gox build ios --device` 签名安装 + 桌面图标遮罩目视 |
| P1-13 | **桌面签名/公证未进自动化** | `docs/desktop-distribution.md`：Windows SmartScreen、macOS Gatekeeper（codesign + notarytool + stapler） |
| P1-14 | **NATIVE-HOST 遗留真机项 5 条** | `app/NATIVE-HOST.md`：Android 返回键 ANR 风险、相机 API 兼容、iOS onMain、权限态、battery chargingType |

## 五、P1 —— 文档缺陷（实打实，已定位）

| # | 项 | 证据 |
|---|---|---|
| P1-15 | ~~**官网首页"已知缺口"文案过期**~~ **已于 2026-10-01 修掉** | `website/index.md`（zh + en）原文写着「虚拟化长列表、表格、tooltip、图标、富文本等尚未封装」—— 表格/树/tooltip 早在 0.9.0 落地（`be8e476`、`971a596`），虚拟化长列表也于 2026-10-01 以 `<scroll vlist>` 落地。已同步修正首页、`components/limits.md`、`components/patterns.md`、`components/layout.md` 的中英两面 |
| P1-16 | ~~**v0.8.0 / v0.9.0 未打 tag**~~ **已于 2026-10-04 复核为已兑付** | 原口径（`git tag -l` 最新只到 v0.7.0）已被推翻：远端 tags 实测 **v0.9.0 / v0.8.0 都在**，且两个 Release 均已发布（`draft: false`，各带 darwin amd64 / arm64 / universal 等 asset）。tag 与包版本已对齐 |
| P1-17 | ~~**工作区有未提交改动**~~ **已过时（2026-10-04 复核）** | 原 `git status` 列的五个 M 状态已清（2026-10-02 起工作区即干净，残留的只是 autocrlf 归一化假象）。**注意**：工作区常态会有 `bench-results/perf-*.json` 之类的并行会话产物，属预期，不算「未提交改动」缺陷 |

## 六、P2 —— 文档已明说"v1 不做"（可延后，但要复述）

| 项 | 依据 |
|---|---|
| 读屏（VoiceOver / TalkBack） | `mobile-adaptation.md` §9、`mobile-regression-checklist.md` §6：**P1 再立项** |
| 内核收编 dp 单位 | `mobile-adaptation.md` §9：现靠脚本侧 `pixelRatio`，内核收编是 **P1 候选** |
| IME 结果提交制（组合中间态不逐键上报） | 同上，设计选择 |
| ~~`textarea` 软换行 / 文本选区复制 / 横向滚动~~ | ✅ **已兑付（2026-10-02，见 §十）**：横向滚动随 `59f4bdb` 落地，软换行 + 选区/复制/剪切/粘贴本轮补齐 |
| GUI 常驻脚本热更新 | `docs/dev-workflow.md` 顶部 TODO：泵循环阻塞监听，需先在 gfx 上游讨论 |
| 复杂 shaping / 富文本 / ~~粗斜体字族~~ | `undecided-and-unimplemented.md` §四：文本域缺口。**粗斜体/字体族/行高/字距已于 2026-10-02 落地（见 §十）**；复杂 shaping（连字、双向、断行断词）与富文本仍不做 |
| `transform` / 路径对象 / 变换矩阵 / 贝塞尔 / 裁剪栈 | 同上 §四：绘制域缺口 |
| 双击/三击、通用 drag & drop、文件拖放、系统右键菜单抑制 | 同上 §四：事件域缺口 |
| `alignSelf` | 同上 §四：布局域缺口 |
| ~~窗口位置与层级、模态子窗口、光标形状、窗口尺寸约束/全屏~~ | ✅ **已兑付（2026-10-02，见 §十）**：位置/层级/约束/全屏/激活下到三后端，模态与光标形状解析留在 gfx 层 |
| devtools 方案 D（独立窗口）、routing 方案 D（多窗口即路由） | 同上 §五：前置已解除暂缓，需要时再评估 |

## 七、明确"不做"（v1 发布说明里复述即可，不是欠账）

- 内核**不做视频解码**（`docs/video-decision.md`：无解码器，软件光栅化撑不起逐帧解码）。
  **注意口径已于 2026-10-01 修订（S8）**：`<video>` **标签与宿主契约已落地**
  （`knownTags` 里有 `video`，属性/事件/受控语义齐全），播放交给后端可选实现的
  `nativeVideoHost`（平台视频层）；桌面三后端目前都没接 ⇒ 降级为封面/占位 +
  一次 `onError({code:"unsupported"})`。**解码仍不做**，这条继续成立。
- `gx/update` **不做差分更新 / 无回滚 UI / 不做进程热替换**（`docs/auto-update.md`，v1.2 候选）。
- **BigInt / SharedArrayBuffer / 真 `String.normalize`** 刻意不实现（`undecided-and-unimplemented.md` §三）。
- `grid` 不做轨道语法 / colSpan；内置图标只 15 个（`gui-guide.md` §4）。
- 无 `Date`（用 `Temporal`）；零 cgo ⇒ `-race` 不可用。
- macOS GUI 后端占位（两条路线都撞零 cgo 硬约束）。

---

## 建议的执行顺序

1. ~~**先做"零成本兑付"**~~ **四项已全部闭环（2026-10-04 复核）**：P1-5（`saveFile`）、P1-15（官网文案）、P1-16（tag 经复核本就已打）、P1-17（工作区已清）—— 见 §八 / §五。
2. ~~**再攻 P0-1**~~ **已闭环（`04cbdeb`，2026-10-03）**：原判的「`await` + try/catch 语义级 bug」实为 generator 帧异常终止未回收，已修（见 §一 P0-1）。
3. **~~P0-2~~ → P0-3**：P0-2（私有字段 / 私有方法 `#name`）**已于 2026-10-03 落地**（见 §一）；剩余 P0-3（`for await...of`，T04 报告列 ~1500 例）按原顺序继续；另 P0-2 落地时揭出的 **213 例早错缺口**（`rpEXH2`）建议单独排一轮 —— 纯增益（约 +0.9pp），与 `for await` 无耦合。
4. **并行推进平台验收**：P0-5/P0-6（Android 交叉编译重跑 + 真机）、P1-9~P1-14 的清单画勾。
5. ~~**鸿蒙 P0-4 单独立项**~~ **已兑付大半（2026-10-02）**：壳工程、NAPI 通道与折叠上报链路已落地并通过命令行构建；剩余「DevEco 自动签名 + 模拟器 HF1 验收」（口径：模拟器即可，HF2 折叠上报逻辑已由桌面资产测试覆盖）无需再单独立项，按清单画勾即可。

## 八、已完成（2026-10-01 收尾）

| 项 | 结果 |
|---|---|
| P1-5 `gx/dialog` 缺 `saveFile` | ✅ 已修（2026-10-01 第二轮）：`nativeDialogHost` 补 `ShowSaveFile` 契约，win32 走 `GetSaveFileNameW` + `OFN_OVERWRITEPROMPT`，cocoa 预置能力接上；脚本侧 `saveFile(options?) → Promise<string\|null>`，中英文档 13 处同步（主仓 `d213263`、官网子模块 `4299f18`） |
| P1-15 官网文案过期 | ✅ 已修：zh + en 全站扫了一遍，元素数 25→41（当时）/**41→42**（新增 `<video>` 后）、模块 14→16、`var` 支持口径、limits / demo 清单同步；`vitepress build` 通过 |
| 视频线口径 | ✅ 已修订：`<video>` 标签 + `nativeVideoHost` 契约落地（S8），解码仍不做（见 §七） |
| 文档自身缺陷 | ✅ 顺手修：`docs/gui-guide.md` §13 说"过程文档在仓库 `agent_doc/` 下"已过期（该目录 2026-09-27 已移出仓库）；**52 个** `testdata/**/*.js` 头部写的 `go run . testdata/x.js` 已失效（CLI 早已搬进 `cmd/gox`）⇒ 改为 `./gox testdata/x.js`，另同步 `docs/gui-router.md` / `docs/gui-model-binding.md` 与三处测试注释 |
| P0-4 鸿蒙平台空白 | ✅ 大幅收口（2026-10-02）：HF1/HF2 落地（`a6c185b` + `4af646c`）—— NAPI 通道 + 装配层 + 交叉编译脚本 + ArkTS 壳工程 + 折叠上报链路，`assembleHap` 命令行构建通过且产物核对含 `libs/arm64-v8a/libgox.so`；剩模拟器验收（需签名；口径：模拟器即可，2026-10-02 拍板，HF2 逻辑已由桌面测试覆盖） |

> P1-16（补 tag）与 P1-17（工作区改动）**已于 2026-10-04 复核闭环**：tag / Release 实测均已发布，工作区自 2026-10-02 起即干净。

## 九、已完成（2026-10-02 iOS/Mac 收尾）

| 项 | 结果 |
|---|---|
| P1-6 显示器插拔派发 `onDisplayChange`（cocoa） | ✅ 已修：`GoxGfxScreenObserver` 监听 `NSApplicationDidChangeScreenParametersNotification`（插拔/分辨率/排列）+ `NSWindowDidChangeScreenNotification`（窗口跨屏）→ `gfx.Post(NotifyDisplaysChanged)`，与 win32 的 WndProc 纪律同构；契约测试 `TestCocoaDisplayChangeNotify`（真窗口 + 真实通知投递） |
| M6 iOS 攻坚（模拟器链路部分） | ✅ 修复壳工程红链：`GoxDisplayFold.swift`（2026-10-01 折叠上报）加进来后从未编译过 —— XcodeGen 工程未重新 generate + 引用的 iOS 27.1 API 不在本机 SDK。现改为运行时动态派发（selector 不存在则整条 27.1 分支跳过，不崩不猜姿态），模拟器 Debug/Release 构建全绿，模拟器冒烟通过（Counter demo 渲染 + 触摸链路） |
| M6 TestFlight 分发链路 | ✅ `scripts/build-ios.sh` 新增 `--release`（Release 配置）与 `--archive`（真机 Release + `xcodebuild archive` + `exportArchive` 出 .ipa，`GOX_EXPORT_METHOD` 可换分发方式），上传命令在产物后给出提示。真机签名验收仍需 Apple 开发者账号环境（见 P0-6 类真机项） |

## 十、已完成（2026-10-02 gfx 窗口管理 + 文本绘制）

对应 `agent_doc/undecided-and-unimplemented.md` §四 的「窗口/系统域缺口」与「文本域缺口」两项，也是富文本组件长期被阻率的根因。

### 10.1 窗口管理（`rlUxHu`）

| 项 | 结果 |
|---|---|
| 窗口位置 | `WindowConfig` 新增 `X/Y/HasPos`（零值 = 不干预，`HasPos` 独立标志而非 `*int`）；`Window.MoveTo/Center/Bounds/outerSize`；新增 `EventMove`（X/Y 为窗口外框屏幕坐标） |
| 窗口层级 | `WindowConfig.Level`（只认 `top`/`bottom`/`normal`，`windowLevel()` 归一）+ `Window.SetLevel/Level` |
| 尺寸约束 | `MinWidth/MinHeight/MaxWidth/MaxHeight` + `NoResize` → `Window.SetSizeConstraints/SetResizable/IsResizable`；win32 走 `WM_GETMINMAXINFO`，x11 写 `WM_NORMAL_HINTS`（18×CARD32），cocoa 走 `setMinSize:`/`setMaxSize:` |
| 全屏 | `WindowConfig.Fullscreen` + `SetFullscreen/IsFullscreen`；三后端分别走 `SetWindowPos`+样式位 / EWMH `_NET_WM_STATE_FULLSCREEN` / `setFrame:`+`NSWindowCollectionBehaviorFullScreenPrimary` |
| 模态子窗口 | 新建 `gfx/modal.go`：`Modal` + `ModalParent *Window`，父窗口句柄挂子窗口；事件入口按 `modalBlocksEvent` 屏蔽（保留 `Close`/`Resize`/`Move`/`MouseLeave`），父窗口关闭连带关子窗口（经 `Post`）。模态状态在 `app` 上（`modalParent`/`modalChild`），故**假 Surface 也能完整测** |
| 光标形状 | 新建 `gfx/cursor.go`：CSS 值域 + `none`，别名表（`hand`/`ibeam`/`busy`/`move-x`…），`SetCursor` 覆盖 + 节点 `cursor` prop 沿父链继承 + 组件默认表（`input`/`textarea`/`search`→`text`，`button`/`menu`/`tab`/`list-item`/`tree-row`…→`pointer`）；同形状跳过平台调用；`setCursor(null)` 清覆盖 |
| 激活 | `Window.Activate()`（win32 `SetForegroundWindow`、cocoa `makeKeyAndOrderFront:`、x11 降级 no-op） |
| 脚本 API | `Window` 句柄补 `bounds()/position()/moveTo()/center()/level()/setLevel()/setConstraints()/setResizable()/isResizable()/setFullscreen()/isFullscreen()/activate()/setCursor()/isModal()/isBlocked()/modalParent()/modalChild()` —— **全部做成方法**，避免属性快照漂移 |

分层原则：几何/层级/约束/全屏/激活下到后端（可选能力接口 `windowManager`/`cursorHost`/`boundsProvider`，type assertion 落空即静默降级；**x11 无 `SetCursor`**），跨窗口语义的模态与依赖节点树的光标形状解析留在 `gfx` 层。

### 10.2 文本绘制能力（`ru628k`）

| 项 | 结果 |
|---|---|
| 软换行 | `textarea` 缺省开启（`wrap` prop 可关）。抽出 **`wrapRuneSpans`** 作为分段算法唯一实现（`gfx/textstyle.go`），`wrapTextStyled` 与编辑框视觉行都由它派生 —— 杜绝「光标画的位置和字不在同一格」。软换行只影响四类动作（↑↓ / Home/End / 点击定位 / 光标绘制与滚动跟随），插入/退格/左右移动仍在逻辑行做 |
| 字体族 | 新建 `gfx/fontset.go`：字体族索引（`NameIDFamily=1` 与 `NameIDTypographicFamily=16` 双登记），惰性构建；**无族名 + 无样式走 `baseFaceFor` 快路径，绝不建索引**（避免 ~300ms 一次性开销） |
| 粗体/斜体 | 新建 `gfx/textstyle.go`：`TextStyle{Size,Family,Bold,Italic,LineH,LetterSp}` + `resolveTextStyle`（沿父链继承，**每根轴独立记「定没定」**，保证 `fontWeight="normal"` 能关掉继承的粗体）。真实变体优先，无变体时合成：粗 = 右移 1px 再压，斜 = 绕基线剪切 `(baseY-iy)*21/100` |
| 行高 / 字距 | `lineHeight` / `letterSpacing` prop → `lineHeightStyled`/`runeAdvanceStyled`/`runeWidthStyled`/`MeasureTextStyled`/`ellipsizeStyled`/`DrawTextStyled`，`text`/`input`/`textarea` 三类节点统一走这套度量 |
| 文本选区 | 新建 `gfx/selection.go` + `gfx/textedit.go`：`selAnchorLine/selAnchorCol/selActive` 落在节点上；`beginTextDrag`/`textDragTarget`（`app.textDrag` 与 `dragTarget` 分开，语义不同）；`handleMouseMove` 最前面「编辑框拖选优先」；`handleMouseDown` 走 `textareaInChain`/`inputInChain` 起选 |
| 复制 / 剪切 / 粘贴 | `handleFieldClipboard` 统一处理 `Ctrl/Cmd+A/C/X/V`（macOS 侧 `gfx/cocoa` 把 Cmd 与 Ctrl 归一，新增 `nsModifierCommand`）；复用既有 `clipboardHost`，无宿主时静默降级 |
| 编辑内核 | `taApplyKeyEx` 抽成**纯函数**（插入/退格/删除/方向/Home/End/Enter/Ctrl+A），`input` 与 `textarea` 共用；`input` 的 `search` 回车提交单独接 |
| 选区渲染 | `selection` 主题 token（亮 `#9cc4ecb0` / 暗 `#2e547ad0`，走 `setToken`/`themeTokens`/`syncThemeVars` 全链）；`paintTextarea`/`paintField` 把**选区高亮画在文字之下**，跨行选区中间行铺到行尾 |

### 10.3 cocoa 缩放比 (backingScaleFactor) 的保鲜

由本轮新增的窗口几何真机测试抓出（`gfx/cocoa/window_e2e_test.go`）。cocoa 是三后端里**唯一缓存了「点 ↔ 设备像素」比值**的后端 —— win32 开了 Per-Monitor V2 DPI 感知、坐标本就是物理像素，x11 恒为像素，都没有可过期的缓存。M4 合流后**位置**的单位换算已收归 gfx 层（`Display.PosInPoints` / `posScale`），但**客户区尺寸与输入坐标**仍由后端自己乘除 `scale`：

| 受影响的量 | 过期后的表现 |
|---|---|
| `postDevice`（鼠标/滚轮坐标 → 设备像素） | 点不准：命中测试整体偏移，且不报错 |
| `Size()` / `w,h`、`onWindowDidResize` | 客户区尺寸读回错单位 |
| `ResizeClient`、`SetSizeConstraints` | 设备像素 → 点，旧点值整体偏一倍 |
| `allocBackbuffer`、`layer.contentsScale` | Retina 上发虚 |
| `Bounds()`（`EventMove` 的载荷） | 上报的位置是错的单位 |

修法（两处，同一个不变量）：**建窗落点之后对齐一次**（`pinClientSize`：`initWithContentRect:` 只能按主屏猜比值，而窗口最终落在哪块屏要 placement 之后才知道 —— 笔记本 Retina 主屏 + 外接 1080p 是最常见组合，猜错就是整块屏的鼠标坐标差一倍；对齐时把客户区尺寸钉回脚本给的设备像素值），**运行期靠 AppKit 的 `windowDidChangeBackingProperties:` 刷新**（`applyBackingScale`：重算全部派生量 + **重推尺寸约束** + 补报 `EventResize`/`EventMove`）。

> `moveTo` / `center` / `position` / `bounds` 这几条**不在**本项范围内 —— 它们的单位换算在 gfx 层，后端只承接点，本来就没有本地缓存。

### 10.4 验收

- 新增测试：`gfx/window_mgmt_test.go`（33 例）、`gfx/textstyle_test.go`（16 例）、`gfx/softwrap_test.go`（12 例）、`gfx/selection_test.go`；`fakeSurface` 扩到支持 `Bounds`/`windowManager`/光标记录/内存剪贴板。
- cocoa 真机测试：`gfx/cocoa/window_e2e_test.go` —— 建窗落点/`bounds()` 往返/`EventMove` 投递时机、建窗期不投假移动、缩放比翻倍后全部设备像素口径的量随之重算（尺寸/约束/输入坐标换算/补报的两个事件/`ResizeClient` 出方向）。钩子是否真挂上用运行时的 `respondsToSelector:` 问（不是 grep 源码）；约束重推、钩子注册、比值缓存不更新三处均做过**变异验证**（摘掉即红）。
- 构建矩阵全绿：darwin amd64/arm64、windows amd64/386、linux amd64/arm64；`go vet ./...` 干净；`go test ./...` 全通过（含 `gfx/cocoa` 真机场景）。
- 文档：`docs/gui-guide.md`（组件表、§2 平台矩阵新增「窗口管理」行、§6.1、§6.3 扩写 + 新增「文本样式」「选区、复制与剪贴板」两小节、§9.5 窗口管理含 prop 表/句柄方法/平台降级表）、`docs/theme.md`（`selection` token）。

> 仍未做：复杂 shaping（连字 / 双向 / 断行断词）、富文本、`transform` 族绘制能力 —— 见 §六。

## 十一、已完成（2026-10-02 内核修复 + 2026-10-03 语言语义）

| 项 | 结果 |
|---|---|
| Android 冷启动 insets 首报为 0 | ✅ 已修（`be3bfcb`）：折叠入口补报尺寸类以清掉安全区，新增 `ReportSizeClasses`；台账 `rEEXB2` 已闭环 |
| **P0-1** async 函数体 throw 被吞成 undefined | ✅ 已修（`04cbdeb`）：generator 帧异常终止未回收（详见 §一 P0-1）；回归 `vm/async_throw_test.go` 6 例，变异验证过 |
| try / finally 三条路径全坏 | ✅ 已修（`3148491`）：`compileTryStatement` 重写 —— 纯 finally 形状改 `PUSH_TRY 0`（不再伪造只 POP 的假 catch），try 正常 / catch 正常 / 异常三条路径汇入**同一份** finally 体，catch 的 finally 保护改 `PUSH_TRY 0` + `PUSH_FINALLY`（旧写法孤儿）。变异验证打在 `handleThrowInner` 的 `pendingThrow` 设置上，3 例精确复现。**残留**（`rMkA8D`）→ 见下条 |
| **D** return / break / continue 穿 try 体时 finally 不执行（`rMkA8D`） | ✅ 已修（2026-10-04）：编译器新增 **try 镜像** `Compiler.tryScopes []tryScope{hasFinally, finallyBody}`（长度即 try 嵌套深度，与运行时 `tryStack` 逐条对应）+ `emitTryUnwind(targetDepth)` —— return/break/continue 发跳转前，由内向外逐个 `POP_TRY`，遇带 finally 的条目**就地内联编译一份 finally 体**（有意重复编译：脚本无子程序返回原语）。return 值经**隐藏槽** `finallyRetSlot`（惰性分配、全函数复用）跨 finally 传递，避免残留值污染求值栈。`controlContext` 记 `tryScopes` 深度，故带标签 break 跨层也正确。验证：17 条控制转移探针与 Node 逐条一致（含「finally 里 return 覆盖」「finally 里 throw 覆盖」「finally 内再嵌 try」「带 var/let/function/class 声明」）；`go test ./...` 全绿；test262 全量 A/B（固定 `-jobs 6`）**7850 → 7876（净 +26，回归 0）** |
| **E** 挂起异常状态机泄漏（`r8Kq3z`，本轮揭出的既有缺陷） | ✅ 已修：旧实现用 VM 全局 `vm.pendingThrow` 存「finally 里新抛的异常」，finally 里 return/throw 离开后挂起值残留，被后续无关 `END_FINALLY` 误重抛；嵌套 try 时互相覆盖。改为挂到 **tryStack 条目自身**（`tryEntry.inFinally` + `pendingVal`），`handleThrowInner` 遇 `inFinally` 条目即丢弃旧挂起值，`OP_END_FINALLY` 弹条目重抛；generator 挂起/恢复同步 `InFinally`/`PendingVal`。回归 `TestFinallyPendingThrowNoLeak` 5 例 |
| **F** catch 体末尾 `POP_TRY` 未配平（`rC97XG`，本轮揭出的既有缺陷） | ✅ 已修：无 finally 时 catch 体开头并未压保护条目、进入 catch 时原条目已被 `handleThrowInner` 弹出，而 catch 体末尾仍无条件 `POP_TRY` ⇒ **多弹一层外层条目**，导致 `try{ try{}catch(){} throw x }finally{}` 的外层 finally 不执行（基线亦复现）。改为仅 `hasFinally` 时发 `POP_TRY`。回归 `TestCatchDoesNotPopOuterTryEntry` |
| **P0-2** 类私有字段 / 私有方法 `#name` | ✅ 已落地（`7a3f37b`）：详见 §一 P0-2 |
| 私有名引发的编译期 panic | ✅ 已修（`97b3c15`）：`delete obj.#x` / `super.#m()` 会让 `compileDelete` 与 super 方法调用分支的 `Property.(*ast.Identifier)` 撞 nil ⇒ **整进程 abort**（test262 里 158 例被记为 crashed，还连带吃掉了 3 分钟孤儿重派）。规范上两者都是**早错 SyntaxError**，改在解析期拒绝 + 编译器兜底。test262 class 子树 A/B：16.77% → **19.13%**（+160 例），耗时 4m13s → 1m09s；全量回到 **33.06%**（7844/23726） |
| **G** test262 合规率依 `-jobs` 而变（`rjeHji`） | ✅ 已修（2026-10-05，runner `bf95d04` / 远端 `ace29dd`）：真根因**不是**并发倍增（`executeCases` 两处调用点早硬编码 jobs=1），而是**引擎包级可变状态**（`vm.currentVM`、`object.callbackError[Value]`、`solid` 包级开关）在同进程内**跨用例串行泄漏**，叠加**分片数随 `-jobs` 变** ⇒ 每个用例的「同进程邻居」变 ⇒ 判定确定性翻转（非竞态：同 jobs 复跑稳定）。修法只动 runner：分片数钉成常量 `test262ShardCount=128` 与 `-jobs` 解耦，子进程用例走父进程落的临时 manifest 直读。⇒ **结果与 `-jobs` 无关**（j1/2/6/8 一致），A/B 不再需要固定 jobs。**遗留**：引擎包级状态仍在，判定只是「对 jobs 确定」而非每例真隔离 |
| **H** 隐式 constructor 不调父类构造（`rCzckg`）+ 两个连带既有缺陷（`rIIXSR`/`rVI6Eb`） | ✅ 已修（2026-10-05，`847992a` + `59d5f4c`）：`class X extends Y {}` 语义改为等价 `constructor(...args){ super(...args) }` —— 新增 `OP_CALL_METHOD_SPREAD`（`0x69`，栈 `[fn, this, argsArray]`），VM 把 `OP_CALL_METHOD` 抽成薄壳与新 opcode 共用 `invokeWithThis`；顺带支持手写 `super(...args)`（此前报 `unsupported expression type` 内部错）。**落地时探针揭出两个既有缺陷**：① `rIIXSR` —— `OP_NEW` 重建闭包时漏结转 `CapturedLocals` ⇒ 凡「构造函数引用 enclosing 作用域绑定」的类经 `new` 实例化都报 TDZ（module 模式 / 嵌套函数作用域中招，script 顶层类因 super 编成 `LOAD_GLOBAL` 而侥幸绕开；**不限于 super**）；② `rVI6Eb` —— `OP_NEW` 的 `callClosure`/`runFrom` 错误直接 `return`，未经 `handleThrow` ⇒ 外层 `try{ new X() }catch` 抓不到，且构造帧不回收。回归 `vm/class_ctor_forward_test.go`（13 例，期望值全取自 Node）+ `vm/new_captured_locals_test.go`（6 例含真 TDZ 守卫）+ `vm/new_throw_catch_test.go`（5 例）。test262 全量 A/B（`-jobs 6`）**7876 → 8015（+139，回归 0）**，修复面集中在 class/elements(116)、super(16)、subclass、Promise regular-subclassing |
| **I** for-await 剩余主体：迭代器驱动的解构 + 早退 IteratorClose（`rKWmkc`） | ✅ 已落地（2026-10-05，`fe77ad1`）：新 opcode `OP_ITER_STEP`（`0xA8`）/`OP_ITER_CLOSE`（`0xA9`）；`compilePatternBind` 重写为 `compileArrayPatternBind`（GetIterator → `PUSH_TRY`/`PUSH_FINALLY` → 逐步 `ITER_STEP` → 收尾 `ITER_CLOSE`，异常路径 swallow-try + `END_FINALLY` 重抛）；plain-object 迭代器认可；parser 补数组/对象模式 elision（洞）、成员目标 `x.y`/`x[k]`、空模式 `[]`/`{}`。Node 22 探针 15 点逐项在 gox 上复现（含 **7.4.6 step 9**「`return()` 返回值非 Object ⇒ TypeError」与 **7.4.2**「`next()` 返回非 Object ⇒ TypeError」——后者让 `yield*` 一处旧假通过转真抛）。test262 全量（`-jobs 4`）**9964 → 11673（+1709，回归 0）**；`-filter dstr` 3154→4816、`for-await-of` 596→913。剩余另立三单：`rGXXZ6`（形参位解构 ~2184）/`r9JAuo`（对象模式族 ~1160）/`rvdPPH`（member-target 求值序 38） |
| **J** 属性描述符建模第一期（`rmdv40` 缺口 2） | ✅ 已落地（2026-10-05，`fe77ad1`）：`PropertyDescriptor` 扩为 `{Value,Writable,Enumerable,Configurable}` + `DataProperty`/`Object.EnumerableKeys`；枚举类 API（`Object.keys/values/entries`、for-in、`JSON.stringify`、`Object.assign`、`propertyIsEnumerable`）全面尊重可枚举性；`defineProperty` 新建缺省全 false、更新**保留原值**；`freeze/seal/preventExtensions` 落地。`built-ins/Object` 定向 606→**763（+157）**；全量 +4、正例回归 0。揭出**内建属性全变可枚举**（`Object.keys(Math)` 44 vs Node 0）⇒ `rwqcke`；22 条定向回归归因为「无装箱原始值 + 内建命名空间无 `Object.prototype` 原型链」⇒ `rm16Za` |
| **K** 读取未声明全局标识符 / eval 吞异常（`rYVgne`） | ✅ 已落地（2026-10-05，`fe77ad1`）：看板原口径被探针**推翻** —— `OP_LOAD_GLOBAL` 早就有 `!found → throwNamedError("ReferenceError")`（`vm/vm.go:1061`，`08dd599` 引入），点名的 `AsyncFunction/is-not-a-global.js` 早已通过；真凶是 `runGlobalEval` 消费 callbackError 后直接 `return result` ⇒ `eval("__x__")` / `eval("throw …")` 静默吞成 `undefined`。另 `AsyncGeneratorFunction` 被 `env.Declare` 误注册为全局属性（Node 下 `undefined`）。test262 全量 9964 → **9979（+15）**；唯一回归（`eval-block-with-statment-regexp-literal-flags`）是 eval 不再吞异常后揭出的 parser 缺陷 ⇒ `r9HBA8` |
| **L** rpEXH2 剩余早错补齐（`rpEXH2`） | ✅ 已落地（2026-10-05，`fe77ad1`）：新增集中校验层 `parser/class_grammar_early_errors.go`（static 成员名 `prototype`、特殊方法名 `constructor`、重复 constructor、HasDirectSuper、**字段 ASI**、对象字面量 ILLEGAL 键），另有「解析已报错则跳过早错遍历」的防御。**顺带修掉裸字段分支的双重 `nextToken`**（`class C { x }` 与多裸字段换行此前根本解析不对）。test262 全量 **+73**、`class/elements` 833→**903**、正例回归 0。剩余另立 `rO13zU`（heritage 箭头+裸计算字段）/`rdyGNY`（initializer 内 direct-eval 早错）/`r63RpV`（sloppy-strict 语义一批） |

## 十二、已完成（2026-10-06 合流批次一：with / this 语义 / 块级早错）

由 4 条并行工作流（每流一个 worktree + 分支 + agent）`cherry-pick` 进 `merge/batch1`，随后**串行全量 A/B**：
`language` **14371 / 23726 = 60.57%**（基线 `c8eb426` 11765 = 49.59%，**+2606**）；`go test ./...` 全绿。

| 项 | 结果 |
|---|---|
| sloppy `with` 语句（`rlXn83`） | ✅ 已落地：lexer/AST/parser/VM 全链 + `OP_WITH_ENTER` 对象环境记录（`withRefOf`/`withResolve`/unscopables）+ `with(a,b,obj)` 逗号序列。定向 `statements/with` **6/181 → 154/181（85.08%）** |
| direct eval 继承调用者 this + 完成值（`r63RpV` 部分） | ✅ 已落地：新增 `OP_EVAL_MARK_INIT` / `callerEvalContext()` / `SetDirectEvalThis` 跨层桥；eval 包装处理末尾分号与严格指令。真根因是**两个叠加缺陷**（完成值 `return (this;)` + eval 闭包 this 不继承）—— 且合流前 script 顶层 this 恰好也是 undefined，**bug 掩盖 bug**。全量 **+214**（GAIN 216 / LOST 2 遮羞布） |
| 块级早错 + 位置早错（`rUZN3k`） | ✅ 已落地：`parser/block_early_errors.go` —— lexical∩var 重声明（含 annex B 函数声明豁免）/ 块内 import-export 位置早错 / 同行缺分号（`checkSameLineASI`）；lexer 认 `\r`/`\u2028`/`\u2029` 行终止符 + `}`/`)` 后 `/` 的正则-除法判定（`lexer/brace_context.go`）；语句位 `{` 恒为 BlockStatement（`r9HBA8` 重做）。`block-scope` 111→146、`module-code` 130→216、`asi` 109→120 |
| typed-nil 语句崩溃（本轮揭出的既有缺陷） | ✅ 已修：`parseClassDeclaration` 等失败时返回 `(*ast.X)(nil)`，装箱进 `ast.Statement` 后 `stmt != nil` **为真** ⇒ 被入列、被块级早错扫描解引用 ⇒ **nil panic**（基线因无该扫描而侥幸）。新增 `isNilStmt`（reflect）统一 5 处语句入列守卫 |
| npm 移动端平台子包（`r8DoFS` / `rgnRC4`） | ✅ 已落地：子包定义上移主仓 `packaging/npm-mobile/`（单一真源）+ `scripts/gen-npm-mobile-pkgs.py`（`--check` 同步断言）+ CI 断言 + gox-npm 子模块指针 |

**本轮新增跟踪单**：`rNR2Zk`（module 用例未走模块入口）/ `r9fWvc`（`new import()` 早错缺失）/ `r23xdR`（runner 的 `Contains(err,NegType)` 会匹配到被回显的源码行 ⇒ 侥幸判过）/ `rmFazR`（`using` 声明零实现）。

## 十三、已完成（2026-10-06 合流批次二：函数名推断 / 类静态成员 / bind / toString 格式 / 顶层 await）

由 5 条并行工作流（每流一个 worktree + 分支 + agent，基线 `0e3c395` = 批次一的合流树）各自 `cherry-pick` 进 `merge/batch2`；
`go build ./...` + `go vet` 干净，随后**串行全量 A/B**：
`language` **16261 / 23726 = 68.54%**（批次一后的基线 14367，**+1894**）；CI 徽章独立复核 **68.5%**（`docs/test262-compliance.json` @ `a776100`，与本地逐位吻合）。

| 项 | 结果 |
|---|---|
| 函数名推断 `SetFunctionName`（`r0XJbW`） | ✅ 已落地：变量声明 / 属性赋值 / 对象字面量键与简写方法 / class 字段统一走 SetFunctionName；匿名形态（`(function(){})`、实参、数组元素）保持 `name === ""` 不瞎猜；`name` 为 `{writable:false, enumerable:false, configurable:true}`。定向 `language -filter fn-name` **165/1054 → 805/1054（+640，零回归）** |
| 类静态成员（`rkfPth` + `rWVt9D`） | ✅ 已落地：静态公有字段初始化器（按定义顺序、类定义求值时执行）+ 静态私有槽挂到构造器、brand check 用构造器。探针：`class C { static s=1; static #p=5; static #m(){return 9} static get g(){return this.#m()+this.#p} }` → `s=1 g=14`（基线同探针 `TypeError`）。定向 `-filter private` **1549/4148 → 2283/4148（+734）** |
| `Function.prototype.call/apply/bind` 的 this 绑定（`rFvpFz`） | ✅ 已落地：改为**运行时 this 绑定**。真根因**不是**看板原口径的「bind 返回值丢失」，而是 `call/apply/bind` 组合链上的 this 绑定失效 ⇒ 通用工具 `Function.prototype.call.bind(Object.prototype.hasOwnProperty)` 拿不到结果，**整族挡住 `obj-ptrn-rest-*`**。定向 `-filter obj-ptrn-rest` **0/276 → 240/276**；broader 集 **5845/9347 → 6213/9347** |
| `Object.prototype.toString` 格式 + `Symbol.toStringTag`（`r1qCg2`） | ✅ 已落地：Gox 原输出 `[Object]`/`[Array]`/`[Null]`（**丢了 `object ` 前缀、标签首字母大写**）不符规范 ⇒ 改为 `[object Object]` 族；命名空间与内建补自身 `Symbol.toStringTag`。定向 `built-ins -filter Object/prototype/toString` **1/41 → 25/41** |
| 顶层 await（`rZU4qA`，部分） | ✅ 解析层 + 同步结算求值层落地：模块顶层 `+Await`（含 `for await...of`）、形参区显式 `~Await`（early-does-not-propagate）、编译器 `fnDepth`/`HasTopLevelAwait()` 把顶层 await 编成主单元 `OP_YIELD`、VM 新增 `RunCompiledAsync`（`Closure==nil` 生成器驱动，主帧仍 `frame#0`）。`module-code` **216/617 → 220/617（GAIN 7 / LOST 3）**。**3 条 LOST 属「遮羞布被揭」**：那三例**自身没有顶层 await**（靠 import 的 fixture），base 上是 runner 对 module+async「无错即放行」的**假通过**（连跑 3 次 100% 稳定），开 TLA 后模块真被执行才暴露缺 async 模块图求值 ⇒ 另立 `rEMAhe` |

**本轮新增跟踪单**：`rEMAhe`（async 模块图求值顺序 `ExecuteAsyncModule`/`AsyncModuleExecutionFulfilled` + pending Promise 的 TLA）/ `rJ56bZ`（**ES2022 `static { }` 类静态初始化块零实现**，探针直接 `unexpected token in class body: LBRACE`）/ `rXrGfu`（**CI 红**：`ci.yml` 的 `ubuntu-latest` 在 `go test ./... (linux + xvfb)` 失败，check-run 注解只有 `Process completed with exit code 1.`，须补 `::error::` 证据）。

**本轮补充证据（修正口径）**：`rm16Za` 的「无 `Object.prototype` 原型链」**不止限于内建命名空间**——`Object.getPrototypeOf({}) === Object.prototype` 为 **false**、`({}).hasOwnProperty` 为 `undefined`（而方法本身在 `Object.prototype` 上存在）⇒ **所有 `o.hasOwnProperty(...)` / `o.isPrototypeOf(...)` / `o.propertyIsEnumerable(...)` 的隐式调用形式全族受影响**，建议升 P1。

## 十四、已完成（2026-10-06 合流批次三：对象原型链 / 形参解构与早错 / 类静态块 / 模块早错 / new.target）

由 5 条并行工作流（每流一个 worktree + 分支 + agent，基线 `7a016b6` = 批次二的合流树）各自 `cherry-pick` 进 `merge/batch3`；
`go build ./...` + `go vet` 干净，`go test`（parser / compiler / object / stdlib / bytecode / vm / runtime / config）全绿，随后**串行全量 A/B**：
`language` **16640 / 23726 = 70.13%**（同机同口径基线 `7a016b6` 实测 **16263**；**GAIN 384 / LOST 7**，净 **+377**）。

> **基线口径说明**：本次实测基线 **16263**，与批次二记录的 16261 差 **2** —— 即 `-jobs 4` 下仍有 ±2 的用例判定漂移。分片固化（`test262ShardCount=128`）只保证「结果对 `-jobs` 确定」，**不是每例真隔离**（与 `rjeHji` 的遗留说明一致：引擎包级状态仍在，相隔 ≤128 条的邻居仍可互相影响）。

| 项 | 结果 |
|---|---|
| 对象字面量 `[[Prototype]]` + 内建原型链（`rm16Za`） | ✅ 新增 `object.NewPlainObject()`，`OP_NEW_OBJECT` 走它 ⇒ `Object.getPrototypeOf({}) === Object.prototype`；`stdlib.linkBuiltinPrototypes()` 把内建命名空间 / 原始值包装器接上各自原型。定向 `-filter Object/prototype` **49/248 → 106/248**；全量 **GAIN 52 / LOST 0** |
| 形参位置解构 + 形参早错 + 默认值 TDZ（`rGXXZ6` / `r9JAuo`） | ✅ `function f([a,b])` / 方法形参解构；非简单形参 + `"use strict"` 指令 ⇒ SyntaxError；箭头形参唯一名；形参默认值 TDZ。定向合并集 **386/594 → 519/594（+133）**；全量 **GAIN 181 / LOST 0** |
| ES2022 类静态初始化块 `static { }`（`rJ56bZ`） | ✅ `ast.IsStaticBlock` + `parser/class_static_block.go` + 编译器按**定义顺序**在类定义求值时执行。定向 `statements/class/static-init` **11/27 → 23/27（+12）** |
| 模块早错 + `new import()` 早错（`rTI1PN` / `r9fWvc`） | ✅ 新增 `parser/module_early_errors.go`（重复导出名 / 未声明导出 / 顶层 return）；`new import(...)` 判 SyntaxError；宿主经 `vm.EvalFileVMModuleEarlyErrors` **单独**开启模块早错而不改运行语义。定向 `module-code` **226/617 → 258/617**、`dynamic-import` **512/1010 → 574/1010**；全量 **GAIN 102 / LOST 0** |
| 元属性 `new.target`（`rvi5ZG`） | ✅ `ast.MetaProperty` + 新字节码/常量 + 词法合法性上下文（非箭头函数体内合法、箭头**词法透明**、script/module/eval 顶层非法）+ eval/Function 编译桥整单元禁止。定向 `-filter new\.target` **7/22 → 17/22（+10）**；全量 **GAIN 26 / LOST 0** |
| 合流冲突：`vm` 编译入口被两条路线各自重命名 | ✅ 合并为**一个 4 参 `compileSourceOpts(src, moduleMode, moduleEE, forbidNewTarget)`**（取代 `compileSourceEE` / `compileSourceOpts` 两份并行实现），三个调用点同步更新 |

**7 条 LOST 全部归因（同一家族，遮羞布被揭）**：`static-init-await-reference.js`、`static-init-await-binding-invalid.js`（variable / let / const 各一）、`static-init-invalid-await.js`、`identifier-shorthand-static-init-await-invalid.js`、`obj-ptrn-elem-id-static-init-await-invalid.js`。
base 上这些负例「通过」是因为 `static {` **整段解析失败**（实测 base 输出 `unexpected token in class body: LBRACE`），并非真的报了规范要求的早错；批次三让 `static {}` 能解析后，**缺的早错（`ClassStaticBlock` 内 `await` 作标识符引用/绑定名恒为 SyntaxError）**才暴露出来。⇒ 另立 `r5T1AR`。

**本轮新增跟踪单**：
- `r5T1AR` —— 类静态块内 `await` 早错缺失（上条 7 例的正面修复；**不能靠 strict 判定**：`await` 在 sloppy 下是普通标识符，必须按「类静态块上下文」单独标记，且不跨 function / 嵌套静态块边界）。
- `rNAtZs` —— `new.target` 收尾 5 例（箭头函数**运行时**词法继承需 `object.Closure` 槽 / `Reflect.construct(Target, args, newTarget)` 的 newTarget 桥 / 派生构造器里 `new.target` 应为最初被 `new` 的那个构造器）。
- `r81aQt` —— `parser`：名为 `async` 的 async 方法解析失败（`({async async(){}})` / `class C { async async(){} }`）。
## 十五、已完成（2026-10-06 合流批次四：using 声明 / parser 收尾 / new.target 收尾 / async 模块图 / 解构求值时机）

由 5 条并行工作流（每流一个 worktree + 分支 + agent，基线 `365c8a4` = 批次三的合流树）各自 `cherry-pick` 进 `merge/batch4`；
`go build ./...` + `go vet` 干净，`go test`（parser / compiler / object / stdlib / bytecode / vm / runtime / config）全绿，**5 个 cherry-pick 零冲突**；随后**串行全量 A/B**：
`language` **16809 / 23726 = 70.85%**（同批实测基线 `365c8a4` = **16640**；**GAIN 185 / LOST 16**，净 **+169**）。

| 项 | 结果 |
|---|---|
| ES2023 `using` / `await using`（`rmFazR`） | ✅ 已落地（此前**零实现**，靠静默误解析侥幸通过）：lexer/parser 上下文关键字消歧 + AST/编译期「作用域退出逆序释放」+ `Symbol.dispose` / `Symbol.asyncDispose` well-known symbol。定向 `statements/(await-)?using` **55/178 → 60/178（GAIN 5 / LOST 0）**；`await using` 的异步释放仍是该套件主要缺口（整体 33.7%） |
| parser 收尾三件（`r5T1AR` / `r81aQt` / `r9HBA8`） | ✅ **类静态块内 `await` 早错**（正是批次三那 7 条 LOST 的正面修复）+ `({async async(){}})` / `class C { async async(){} }` 命名方法 + eval 前导块。定向 `-filter static-init` **52/69 → 60/69（GAIN 8 / LOST 0）** |
| `new.target` 收尾（`rNAtZs`） | ✅ `object.Closure` 新增 `NewTarget` 字段（**三个闭包重建点全部结转**，与方法调用只改 `invokeWithThis` 同一纪律）+ `Reflect.construct` 的 newTarget 桥 + 直接 eval 继承（`SetDirectEvalNewTarget` + `CompileSourceAllowingNewTarget`，**仅**「非箭头函数体内的直接 eval」放行）。定向 `-filter new\.target` **17/22 → 21/22（GAIN 4 / LOST 0）** |
| async 模块图求值顺序（`rEMAhe`） | ✅ 根因是 `OP_IMPORT` **同步内联**求值 + TLA 后 promise 回调中途重入 ⇒ 依赖求值顺序错。新增 `moduleRecord` 状态机：静态依赖 leaf-to-root 门控、TLA 挂起-恢复、rejection 沿依赖边传播且依赖方本体不运行、dynamic import 等模块完成；循环判定用说明符可达性区分真环与回调重入的假父子。**批次二 TLA 的 3 条 LOST 全部转正**；定向 `top-level-await|module-code` **259/622 → 262/622** |
| 解构成员目标求值时机（`rvdPPH`） | ✅ `OP_GET_INDEX`/`OP_SET_INDEX` 触发 getter/setter 抛错时**立即** `checkCallbackErr + rethrowBridgeError`（原先错误残留到后续无关内建调用才被消费，那时外层 `POP_TRY` 已出栈 ⇒ 不被 catch）；`OP_ITER_STEP` 带迭代器隐藏槽号，`next()` abrupt 时先清槽（等价 `iteratorRecord.[[done]]=true`，规范 7.4.6）⇒ 异常路径的 `IteratorClose` 跳过 `return()`。定向 `dstr` **8243/8783 → 8366/8783（GAIN 123 / LOST 0）** |

**LOST 16 全部归因（两族，都是遮羞布被揭，非回归）**：
1. **15 条 —— 复合赋值 / `++`·`--` 的成员左值把 `ToPropertyKey` 调了两次**（`compound-assignment/S11.13.2_A7.{1..11}_T4`、`pre/postfix-increment/decrement` 的 `_A6_T3`）。实测**基线同样调两次**（探针：`var c=0; var prop={toString(){c++;return "k";}}; var base={}; base[prop] *= 2;` → 两个二进制都得 `c===2`），只是基线把错误**残留到测试判定之后**才浮现 ⇒ 侥幸判过；路线 E 的「立即重抛」纠正了错误浮现时机，于是暴露。⇒ 另立 **`r8TaL3`** 做正面修复（成员引用只求值一次）。
2. **1 条 —— `for (using x of []) { var x; }`**：规范要求 `ForDeclaration` 的 BoundNames ∩ `Statement` 的 VarDeclaredNames ⇒ SyntaxError；**Gox 连 `let` / `const` 版本都没做**（lead 探针实测三条全无报错），基线是靠 `using` 整段解析失败侥幸判过。⇒ 另立 **`rrFSyI`**（整个 for 头部重声明早错族）。

**本轮流程教训（写进纪律）**：5 条路线里有 2 条的 agent 进程**在 `git commit` 之前结束**，代码留在工作区未提交。
lead 按既有恢复套路从 worktree 的 `git status` / `git diff` 捞回，**先独立复核**（`go build` + 各包测试 + 定向 A/B 复现它声称的数字）再**代为提交**（提交消息里写明「由路线 agent 完成、lead 代为提交」及复核结论）。
⇒ 派工单里「干完 `git commit`」这一条必须写死，且 lead 收工前必须逐 worktree 检查 `git log` **与** `git status`（只看 log 会漏掉未提交的整条路线）。

**并行会话的动向**：本批推送时远端 main 已被另一会话推进 5 个提交（`f5642b2` / `2a17dd4` / `09c2f64` / `4a5f939` / `c5665b0`，均围绕 `rXrGfu` 的 CI 红归因，含 `fix(gfx): 测试清空了字体扫描结果, 害 Linux 整屏中文不渲染`）⇒ `merge/batch4` 已 `rebase` 到其上再推（`c5665b0..7a426b9`）。

## 十六、已完成（2026-10-06 合流批次五：globalThis 自有属性 / for 头部早错 / ToPropertyKey 一次 / yield 终结符 / using 位置 / 符号键访问器）

由并行工作流各自 `cherry-pick` 进 `merge/batch5`，基线 `7a426b9` = 批次四合流树，合流尖 `f1a46fb`。全量 A/B 数字**待 lead 回填**（徽章 `docs/test262-compliance.json` 截至本台账仍为批次四口径 70.8%）。逐路线台账（一特性一行：提交 sha / 定向 test262 数字 / LOST 归因字母）：

| 项 | 结果 |
|---|---|
| globalThis 自有属性查询（global-obj 路线，`f1a46fb`） | ✅ 已落地：`object.GlobalBindingInfo/GlobalBindingProvider` 接口暴露全局环境绑定元信息；GlobalObject 补 [[Prototype]] + OwnKeys/HasOwn/OwnDescriptor/EnumerableOwnKeys/DeleteOwn/DefineGlobal；`OP_DECLARE_VAR` 供顶层 var 提升期建自有属性绑定。定向 A/B（filter `dynamic-import\|await-using\|async-generator\|async-function\|optional-chaining\|import-defer\|global-code\|unary-expr\|new\.target\|top-level-await\|for-await-of\|source-phase-import\|async-arrow`，4200 例）base 2905 → 2939，**GAIN 34 / LOST 0**（归因 A） |
| for/for-in/for-of 头部词法声明与循环体 var 重声明早错（for-head 路线，`88c70e5`） | ✅ 已落地：`checkForHeadRedeclaration` 唯一 FOR 分派点收口，覆盖 for/for-in/for-of/for await-of/using 头部；循环体 var 名经 `blockVarDeclaredNames` 收集（函数边界自然切断）。定向 A/B：`for-of\|for-in` **GAIN 5 / LOST 0**、`statements/for/` **GAIN 2 / LOST 0**（归因 A） |
| 成员左值键只求值一次 ToPropertyKey + delete 键归一（tpk 路线，`d97e8dc`） | ✅ 已落地：新增 `OP_TO_PROPERTY_KEY`，复合赋值/++/-- 的 GET/SET_INDEX 共用同一份已转换键（正面修复批次四那 15 条 LOST 的 `r8TaL3`）；`OP_DELETE` 键按 ToPropertyKey 归一。定向 A/B（`compound-assignment\|increment\|decrement`）base 383 → 398，**GAIN 15 / LOST 0**（归因 A） |
| yield 空表达式终结符补全 + async GetIterator 抛错传播（asyncgen-tail 路线，`5adc99d`） | ✅ 已落地：`parseYieldExpression` 空 yield 补 RBRACKET/COMMA/COLA/COLON 终结符 + ASI 换行判定（换行判定位置由 lead 修正到消费 `*` 之前）；`resolveAsyncSymbolIterator` 按 GetIterator(hint=async) 语义三处 nullish/不可调用/非对象 TypeError。定向 A/B（`async-generator` 1040 例）base 837 → 848，**GAIN 14 / LOST 3** ⇒ 3 条 LOST 归因 **B**（`yield` 作标签标识符早错缺失、基线靠解析意外报错侥幸判过，遮羞布被揭）⇒ 转批次六 `rDc5ui` 修复 |
| using 绑定名/初始化器纳入静态块早错下钻（using 路线之一，`d6d735b`） | ✅ 已落地：static 块内 `using x = ...` 绑定名/初始化器纳入 childNodes 下钻。定向 A/B（`using` 190 例）base 68 → 93，**GAIN 26 / LOST 1** ⇒ LOST 1 归因 **B**（`using` 顶层 eval 早错缺失，靠"using 被全局误拒"侥幸判过）⇒ 转批次六 `rabcWh` 修复 |
| 接通 using/await using 的位置跟踪 usingDeclAllowed（using 路线之二，`a410fb1`） | ✅ 已落地：`parseBlockImpl/parseBlockWithDirectives` 进块置 true（Block/FunctionBody/GeneratorBody/AsyncFunctionBody/ClassStaticBlockBody）；单语句体/标签非块体/switch CaseClause 语句列表置 false（规范第 2 条）；Module 顶层由 ParseProgram 置 true。定向 A/B（`using`，language）base 68/190 → 92/190，**GAIN 26 / LOST 2** ⇒ LOST 2 归因 **B**（static-init await 绑定早错 / eval 顶层 using 早错，同为遮羞布被揭）⇒ 转批次六 `rabcWh` 修复 |
| 符号键访问器存取（propdesc 路线，`39388d0`） | ✅ 已落地：`DefineSymbolAccessor`（落 SymbolProperties 键空间）+ `LookupSymbolPropertyDescriptor`；`OP_SET_GETTER_DYN/SET_SETTER_DYN` 遇 Symbol 键改走符号空间；`GET_INDEX` Symbol 分支展开 getter。修复对象字面量 `get [Symbol.x](){}` 被 toJSString 成字符串键导致 `obj[Symbol.x]`/`getOwnPropertySymbols`/迭代协议全查不到。定向 A/B 数字待 lead 回填（提交信息未附）；LOST 归因 **A**（无记录 LOST） |

## 十七、合流批次六（生成器 yield 标签早错 / eval 顶层 using 早错 / 抛值 ToString / 直接 eval super home / defineProperty 自有属性接口）

进行中/待 lead 合流确认：由 5 条路线 cherry-pick 进 `merge/batch6`（基线 `f1a46fb`，尖 `c6e6ae6`）。**全量 A/B 数字待 lead 回填**。逐路线台账（一特性一行：提交 sha / 定向 test262 数字 / 归因字母）：

| 项 | 结果 |
|---|---|
| 生成器体内 yield 作标签标识符早错（`rDc5ui`，`4fa96f7`） | ✅ 已落地：LabelIdentifier 的 [Yield] 参数约束 —— 生成器体内 `yield: ;` 作标签为 SyntaxError（正是批次五 asyncgen 路线那 3 条 LOST 的正面修复）。定向 A/B（`async-generator\|generator`，1780 例）base 1395 → 1401，**GAIN 6 / LOST 0**（归因 A） |
| 直接 eval 顶层 using/await using 早错（`rabcWh`，`771987b`） | ✅ 已落地：直接 eval 的顶层 `using` / `await using` 早错（批次五 using 路线 LOST 族的正面修复）。定向 A/B 数字待 lead 回填（提交信息未附）；LOST 归因 **A**（无记录 LOST） |
| 未捕获抛值按 ToString 渲染（`r9ttI7`，`2f4eaf2`） | ✅ 已落地：未捕获 non-Error 抛值按 ToString 渲染，不再退化成 Inspect 形式。定向 A/B（`line-terminators\|module-code`）297 → 302，**GAIN 5 / LOST 0**（归因 A） |
| 直接 eval 内 super.x 不再误报 SyntaxError（`roiE5Z`，`b6ea5e3`） | ✅ 已落地：函数/箭头/间接 eval 保持 SyntaxError 的前提下，直接 eval 内 `super.x` 合法。定向 A/B：`eval-code` **GAIN 1 / LOST 0**、`expressions/class/elements` **GAIN 4 / LOST 0**、`statements/class/elements` **GAIN 4 / LOST 0**、`expressions/super` **GAIN 2 / LOST 0**（归因 A） |
| defineProperty/getOwnPropertyDescriptor/hasOwnProperty 统一走自有属性接口（`rlGCky`，`c6e6ae6`） | ✅ 已落地：三族 API 统一走自有属性接口。定向 A/B：`object\|Object` 1143/1707 → 1173/1707（**GAIN 30 / LOST 0**）；更广 `class\|method\|generator\|async\|prototype\|ownKeys\|property` 9476/13153 → 9573/13153（**GAIN 97 / LOST 0**）；`Reflect\|Proxy\|JSON\|arguments` 364/718 → 380/718（**GAIN 16 / LOST 0**）。`go test ./...` 全绿（归因 A） |

**批次二 8 条 LOST 的收尾**：其中 #1–#4（class 内 yield 计算键 / async 形参 await 早错）由 wt/ledger2 工作流（看板 `r4hv9u`）在两个提交 `23a7aa3` / `864a1e3` 修复转正；#5–#7 由批次四 `d62aaf5` 转正；#8 系批次二全量跑判定抖动、引擎无缺口。详见 `docs/batch2-lost-r4hv9u-triage.md`。

## 十八、合流批次七/八（真模块入口语义揭示的三条 LOST + async generator 原型链 + promise 采纳）

**批次七**（基线 `merge/batch6` 尖 `c6e6ae6` → 尖 `5030c7f`，10 引擎 + 8 文档提交）：
`language` 全量 **16969 → 17226 = GAIN 260 / LOST 3**。
**批次八**（基线 `merge/batch7` 尖 `5030c7f` → 尖见 git，8 提交）：
`language` 全量 **17223 → 17430 = GAIN 210 / LOST 3（全为超时抖动，实为 LOST 0）**。
`go build ./...` + `go vet` + `go test -count=1 ./...`（20 包）全绿。

**批次七的 3 条 LOST**（逐条归因见 `docs/batch7-lost-triage.md`）全部是
`9b001f6`（runner 改走**真模块入口** + negative 判据只取首行）**揭掉遮羞布**后
暴露的真缺口，**非引擎回归**；其中两条已在批次八正面修复：

| LOST | 根因 | 归宿 |
|---|---|---|
| `module-code/eval-self-abrupt.js`、`eval-export-dflt-expr-err-eval.js` | `Test262Error` 实例在 Go 侧是 `*object.Object`，**模块入口**未捕获异常渲染走 `ThrowError.Error()` → `Value.Inspect()` 得 `{ message: "" }`，构造器类型名丢失（`message` 为空是正确 JS 语义，缺的是 ToString 渲染）。script 入口走 `vm.uncaughtError` 是对的，两条入口分叉 | 批次八 `47fca04` 修（模块入口改走 `vm.uncaughtError`）⇒ 两条转 GAIN |
| `module-code/instn-local-bndng-const.js` | **模块顶层 const 完全可变**。① `isGlobalScope()` = `depth==0 && !moduleMode`，模块模式恒 false；② 于是 const 走 `OP_STORE_CONST` 存**局部槽**；③ 赋值路径只发 `OP_STORE`，**从不查 `Symbol.IsConst`**（`compiler.go:1662` 注释直言「局部槽位根本不查」） | 批次八 `a2a671d` 修（新增 `OP_STORE_CONST_GUARD`，接到全部局部赋值位置；顺带修正 class 绑定被错标为 const）⇒ 转 GAIN |

**批次八逐路线台账**：

| 项 | 结果 |
|---|---|
| 模块顶层 const 不可变性（`a2a671d`） | ✅ 新增 `OP_STORE_CONST_GUARD = 0x2A`；`emitLocalStore(sym)` 在 IsConst 时改发守卫指令，覆盖 `=`/复合赋值/`++`·`--`/逻辑赋值/解构/for-of 目标；运行期无条件抛 `TypeError: Assignment to constant variable`（可捕获）。顺带修 `compileClassDeclaration` 把 class 绑定错标为 const（规范 CreateMutableBinding，与 let 同类）。定向 A/B（`module-code`，617 例）272 → 274，**GAIN 2 / LOST 0**；新增 13 个单测 |
| 未捕获抛值渲染（`47fca04`） | ✅ 模块入口改走 `vm.uncaughtError`，与 script 入口同口径 ⇒ 首行由 `vm error: { message: "" }` 变 `vm error: Test262Error:`。定向 A/B（`module-code`）273 → 275，**GAIN 2 / LOST 0**；`built-ins/Error`、`language/throw` 无变化；新增 `vm/uncaught_render_test.go` |
| AsyncGeneratorFunction 原型链装配（`a328874`） | ✅ 实例 `[[Prototype]]` = 该函数自己的 `.prototype`（其 `[[Prototype]]` 才是 `%AsyncGeneratorPrototype%`）；AGF/AGFFP/AGP 三者互链 + 属性描述符逐条对齐 Node v22。compiler 加合成自引用槽 `\x00agself` + `FunctionMeta.SelfSlot` |
| AG 实例 `@@toStringTag` 沿原型链可达（`4c3764a`） | ✅ 根因：`AsyncGenerator.GetSymbolProperty` 只向 `g.Proto` 委托**一层**，`*Object.GetSymbolProperty` 只查自身 ⇒ 落在 AGP 上的 `@@toStringTag` 永不可达（`it[Symbol.toStringTag]` 得 undefined、`toString.call(it)` 得 `[object Object]`）。改为沿完整链查找。定向 A/B（`AsyncGenerator` built-ins / `async-generator\|generator` language）均 **GAIN 0 / LOST 0**；新增 3 个回归测试 |
| async/generator 形参求值时机（`c7f23fe`） | ✅ **现状已正确**（探针 vs Node 22 逐行一致：async 调用后即置位 side、async generator 形参求值早于返回对象、生成器体推迟到 next()、抛错三口径）⇒ 只补 3 个钉子，无代码改动 |
| promise 采纳（`649a8b1`） | ✅ 根因：`object.Promise.Resolve` 在「内层 promise 仍 pending」分支把 rejection 回调 `__inner_reject` 注册进 `CatchCallbacks` 时**漏标 `IsCatch`**，`invokePromiseCallbacks` 只对 `IsCatch=true` 走 rejection 分支 ⇒ 回调被静默跳过，外层 promise 永挂。修法一行语义修正。影响面：`p.then(cb)` 中 cb 返回最终 reject 的 promise 时派生 promise hang（上游 `Promise.all` / 模块图动态 import 因此不结算） |

**批次八的一次真实回归及其定位（流程教训）**：`a328874` 越界改写了 `parser/parser.go`
的 `parseYieldExpression` —— 把批次七 `5747a43` 那版整体替换成一个手写版，导致
`function* g(x = yield) {}` 这类本该报 SyntaxError 的用例**静默通过解析**，
全量 A/B 出 **46 条 yield 相关 LOST**（`param-dflt-yield` / `yield-ident-invalid` /
`yield-star-after-newline` 三族）。二分定位到该提交后，`git checkout 5030c7f --
parser/parser.go` 恢复，`dc15392` 记录。
⇒ **纪律**：cherry-pick 前必须对每条做**净 diff 审计**，尤其警惕「改了但提交信息
没提」的文件 —— 本次该提交共改 15 个文件，只有 3 个与「AsyncGeneratorFunction
原型链」这一声称任务相关（`object/ownproperty.go` +171 行 tombstone、
`stdlib/{bigint,eval,function_proto,object_methods}.go` 的 `SetFunctionLength`
重构均属越界，已另立看板单 `rxsCia` 追踪其残留缺口）。

**批次八的 3 条「LOST」**：`meth-static-dflt-ary-ptrn-elem-id-init-throws` /
`grammar-static-private-gen-meth-super` / `async-func-dstr-const-obj-ptrn-empty`
—— 全为 `phase: timeout`，单跑均 100% 通过（两侧 JSON 对照：base `pass=True phase=pass`，
cand `phase=timeout`）。根因是**全量跑时机器过载**（观测到 9–14 个并发 gox 进程，
远超 `-jobs 4`）⇒ **非回归**。定向 filter 重跑 9/9 通过确证。

**批次八新增看板单**：`rLjAX9`（函数 `.length` 未按规范截断，挡 `dflt-params` 32 例）、
`ryPk0S`（数组解构忽略被覆盖的 `Array.prototype[Symbol.iterator]`，挡
`async-generator/dstr` 12 例）、`rxsCia`（`Object.keys(函数)` 不列出动态自有的可枚举
属性）。前两条基线即如此，第三条由 `a328874` 的 tombstone 改动部分触及但未修完。

## 十九、最终合流（批次八独有提交 rebase 到已推进的远端 main）

**背景**：批次八准备合流时，`git fetch` 发现远端 `Gox/main` 已被另一会话推进到
`323703b`，其中① 包含批次五/六/七的**全部内容**（re-commit 过，SHA 不同）；
② 多一个 `47f4d81`（全局访问器绑定与计算成员键三处语义修复），其提交信息记录
「批次七净增 +417 例，但全量 A/B 复核发现 **23 例回归**」——即对方已把批次七合进
main 并修掉了一批我未发现的回归。故**不再整体 merge 批次八**，改为用
`git cherry -v Gox/main merge/batch8` 取出**真正独有**的提交，rebase 到远端 main 上。

**最终集成分支** `merge/final2`（基线 `Gox/main` = `323703b`）：
cherry-pick 8 条 —— `f121d25`（未捕获抛值渲染）、`9dd6dac`（`OP_STORE_CONST_GUARD`
模块顶层 const 不可变）、`fce17cc`/`3c47ba5`/`7bd05d6`（async generator 原型链 /
`@@toStringTag` 沿链可达 / 形参求值时机钉子）、`e98de49`（promise 采纳）、
`aeaf943`（**再次**撤回 `a328874` 对 `parseYieldExpression` 的越界改写 ——
证明该坑会随每次 cherry-pick 复现，必须每次复查）、外加 2 条 docs。

**逐文件净 diff 审计**：改动面精确等于上述 8 条修复 + docs，
**`git diff --name-only Gox/main HEAD -- parser gfx` 为空**（parser 与 gfx 与 main 零 diff）。

**全量 A/B（静默机器上单跑）**：
`language` 全量 **17273 → 17477 = GAIN 204 / LOST 0**（23726 例）。
`go build ./...` + `go vet` + `go test -count=1 ./...`（20 包）全绿。
徽章更新为 **73.7%**（17477/23726）。

**流程纪律（新增两条）**：
1. **同机并行会话会污染基准跑分**：本次跑到一半机器上出现 14 个 gox 进程，
   抓父链发现 `gox-f2.exe -jobs 8` 来自**另一个 CodeBuddy 会话**（其 shell 包装器
   命令行带 `__codebuddy_payload=…`），其二进制 mtime 比本验证启动仅晚 2 分钟。
   12 分片 / 16 核 ⇒ 首轮结果里的超时均为**假 `phase: timeout`**。
   ⇒ 全量 A/B 开跑前必须 `tasklist | grep -i '^gox'` 按**可执行文件名**核查有无
   他人运行（本机 `wmic ... get CommandLine` 取不到命令行，不能作判据）；等机器静默再跑。
2. **cherry-pick 后必须复查越界文件**（本条在本次又一次生效：`aeaf943` 是第二次
   因同一提交撤回同一处 parser 改写）。


## 二十、§十九 勘误：gfx 字体修复的补入（非零 diff）

§十九 称「parser 与 gfx 与 main 零 diff」——**该断言在补入字体修复后不再成立**，特此更正并留档。

发现：做 `git cherry -v Gox/main merge/batch8` 的**完整**差集时，还有第 9 条 `5030c7f`
（`fix(gfx): 默认字体优先选正体面 —— 修 Linux 上"正文整屏变粗/bold 不生效" (rXrGfu)`）。
它在批次八的差集里排最后（是批次七分支的尾部提交，被批次八继承），前一次合流把它**漏掉了**
——`merge/final2` 当时 `gfx/` 目录零改动。这是「只看差集前几屏就动手」的代价。

**不能整文件 cherry-pick**：main 已走过 `7168945`/`c8613e0` 那条线，两侧对同两个文件
各有独有改动 ——

| 文件 | main 独有 | `5030c7f` 独有 |
|---|---|---|
| `gfx/font.go` | `SetBaseAssetsDir` / `bundledFontCandidates` / 随包候选注入 / CJK 静态候选（`NotoSansCJK-VF.otf.ttc`、`wqy-microhei.ttc`、`uming.ttc`） | `regularFaceOf` / `baseFaceRank` / `subIsPlain` / `chooseBaseFont`；`parseFontFile` 改走 `regularFaceOf` |
| `gfx/fontset.go` | `axisFromFont(f, faceIndex, subfamily)` 三参数 + `localizedBoldNames` + 面序号长注释（**比 `5030c7f` 的版本更新**） | `subfamilyOf` / `fontAxisOf` |

故**手工合并**（提交 `9877fff`）：`font.go` 采纳 `5030c7f` 的选面逻辑、保留 main 的随包
字体能力；`fontset.go` **只增量补**两个函数并适配三参数签名；`font_test.go` 追加其 124 行
测试（只增不改）；顺带移除不再使用的 `sfnt` 导入。

**验收**：`go vet ./gfx/` 干净；`go test ./gfx/...` 全绿；`TestBaseFontPrefersRegularFace`
（`5030c7f` 新增）与 `TestDrawTextStyledDiffersPerAxis`（CI 上红过的那个）均通过。
全量 test262 **逐用例**比对补入前后：23726 例 **0 差异**（`17477/23726 = 73.6618%`）。

**诚实结论**：把 `chooseBaseFont` 判据降级回 main 内联版的「只排粗体不分档」后，
`TestBaseFontPrefersRegularFace` **在本机仍绿** —— 因为 Windows 字体库只有 Regular/Bold
两档、没有 DemiLight/Light/Medium，而 `baseFaceRank` 的**分档增量恰恰只在后者上现形**。
故这条修复的正确性**仍以 CI 的 ubuntu runner 为准**，本地只能说「没引入回归」。

**流程纪律（第三条，本次新增）**：`git cherry -v` 的输出**必须读到最后一行**再决定
cherry-pick 清单 —— 差集末尾的提交最容易因「看到前面几条就以为齐了」而漏掉。

## 二十一、批 5 补遗：`delete 标识符` 按引用基三分（c02e051）

**背景**：批 5（§十六）合流时，`delete this.x` / `delete globalThis.x`（引用基是
GlobalObject 的那一支）已随 `47f4d81` 修掉，但 **`delete 标识符` 整支没动** ——
`compileDelete` 对标识符一律编译成「求值 + POP + TRUE」：既不删绑定（隐式赋值
建的全局删不掉），又给未定义名徒增一次 ReferenceError（规范 sloppy 下直接
true），顶层 var / let / 内层局部还一律误报 true。

**修法**（c02e051，5 文件 +128/-1）：按规范 UnaryExpression 的引用基三分 ——
① 顶层 var / 函数声明 / 隐式全局 / 未绑定名 → 新指令 `OP_DELETE_GLOBAL`（0xF3；
0xF1/0xF2 已被 OP_TO_PROPERTY_KEY / OP_GET_PROTO 占用）按名删全局环境绑定；
② 顶层 let/const/class 与模块顶层绑定 → 编译期压 `OP_FALSE`；
③ 内层局部绑定 → 同样 `OP_FALSE`。
**不**内联成 `OP_THIS + OP_DELETE`：函数体内 this 是调用方接收者、module 顶层
this 是 undefined，都会把删除落到错误的基上。

**A/B**（基线 = 合流后的远端 main，验证时 tip = 0e35257）：
- 定向 `expressions/delete` 69 例：30 → 42，**GAIN 12 / LOST 0**；
- 全量 23726 例：17477（73.66%）→ 17498（73.75%），**GAIN 21 / LOST 0**；
- 基线复跑得 17477，与 §十九 记录**逐例吻合** ⇒ 本轮并行跑 go test 未污染基准。
- `go build` / `go vet` / `go test ./...` 全绿；新增 `vm/delete_identifier_test.go`（5 例）钉三类语义。
- 徽章由 CI（test262.yml，compiler/** vm/** 触发）自动回写；73.7488% 与原值同到一位小数
  （73.7%），故无可见变化。

**跨会话 tip 漂移实锤（又一次）**：本条 delta 从 323703b 起共 rebase 三次
（323703b → 0e35257 → 475c2c8 → 231ee25），其中一次推送被拒（non-fast-forward）——
另一会话先推了 docs / CI 提交。**推论**：验证完的 delta 不要放很久再推；推送前
先 `git fetch` 对一次 tip，rebase 次数才少。后两次 rebase 均为 docs/CI-only 提交，Go 源码树
不变，因此 A/B 数字无需重跑。


## 二十二、合流批次九（parser yield 标识符 / 函数 length / ToPropertyKey 时序 / 数组符号键 / 自有属性枚举）

**背景**：批 5 补遗（§二十一）合流后，基线 `b3e2088` = `17498/23726 = 73.7503%`。
从 wb-issues 看板挑出 5 个可独立完成的 parser/VM 缺陷，**并行派 5 路 agent** 修复，
各自在独立 worktree 工作、只 add 自己的文件、**每步 commit**、**不 push**；lead 收工后
统一 cherry-pick 进 `merge/batch9` 并做一次全量验收。

| 路线 | 分支 | 提交数 | 缺陷 | delta |
|---|---|---|---|---|
| yld | `open/yld` | 3 | sloppy 非生成器代码里 `yield` 应作普通标识符（`rmg9Qy`） | 定向 +58 / 全量 +56 |
| fnlen | `open/fnlen` | 2 | 函数 `length` 应按规范 `ExpectedArgumentCount` 截断 | 定向 +32 / 全量 +49 |
| tpk | `open/tpk` | 2 | 成员访问的 `ToPropertyKey` 必须晚于基 null/undefined 检查（`rknvx2`） | 定向 +39 / 全量 +18 |
| dstr | `open/dstr` | 4 | 数组解构须尊重被覆盖的 `@@iterator`（`ryPk0S`） | 定向 +80 / 全量 +80 |
| keys | `open/keys` | 4 | `Object.keys` 等枚举 API 须列函数对象自有可枚举属性（`rxsCia`） | 全量 +18 |

合计 15 条提交，唯一冲突在 `vm/vm.go`：`dstr` 的 `getSymbolIndexedValue` 与 `keys` 的
`markAssignmentEnumerable`/`inStaticInitFrame` **都在文件末尾追加互不相关的新函数**，
保留两侧即解。`go build` / `go vet` / `go test ./...`（20 包）全绿。

**关键实现要点**：

- **yld**：新增 `p.yieldIsIdentifier()`（判据 = 非 `allowYield`、非 strict、非 module）；
  `parseYieldExpression` 前缀返回 `Identifier`；**原有表达式 / 空-yield 逻辑一行未动**
  ——刻意绕开 `dc15392`/`aeaf943` 的 46 例回归雷区（空 yield 在表达式位置解析失败
  属既有缺陷，另立新单）。
- **fnlen**：`FunctionMetadata` 新增 `Length` 字段，与 `NumParameters` **分义保留**
  （后者按实参摆放用，不可动）；4 处解构形参 `HasDefault` 从硬编码 `false` 改
  `param.Default != nil`。
- **tpk**：**编译期零改动**，全在 VM 层；`OP_TO_PROPERTY_KEY` 增就地基数检查
  `PeekAt(1)`（基为 null/undefined 时抛 TypeError 且**不碰键**）——
  未新增操作码，保住 `d61d89d` 的「键只转一次」语义（`S11.13.2_A7.*_T4` 仍全绿）。
- **dstr**：根因**不是**编译期索引快路径，而是**数组/类型化数组没有 Symbol 键属性槽**
  （`arr[Symbol.iterator] = fn` 被 `setIndex` 的 `*Array` 分支静默丢弃）。
  新增 `object/symbol_props.go`（`SymbolPropertyStore` 接口 + 沿原型链查找），
  `object/array.go` / `object/typedarray.go` 补齐槽位；`@@iterator` 存在但不可调用时
  抛 TypeError，不再静默回退索引快路径。
- **keys**：`ownKeys` / `getOwnProperty` 走统一 `OwnPropertyStore` 接口；
  `OP_FOR_IN_INIT` / `OP_OBJECT_SPREAD` 同接口；函数对象自有属性加 `NonEnumProps` 标记。
  agent **自己 A/B 抓出并修掉了自己引入的 1 条 LOST**（`Object.assign` 取源属性未触发
  访问器，`82ddc41` 改走 `GetProperty`）。

**A/B（合流后统一跑，基线 `b3e2088`）**：

- **全量 23726 例：17498（73.7503%）→ 17742（74.7787%），GAIN 244 / LOST 0**；
- **逐用例 diff 三项全 0**：`only_a = 0` / `only_b = 0` / `pass_differs = 0`
  —— 这是「零回归」的最强证据（比总数相等强得多）；
- `-jobs 8` 下报的 21 条 `timeout` 经 `-jobs 2` 单独复验**全部 PASS**，判定为机器争抢假阳性；
- 徽章 `docs/test262-compliance.json` 由 `73.7%` 改为 `74.8%`。

**诚实列出的 3 项不修（另立新单）**：① 空 yield 在表达式位置解析失败
（`[...yield]` / `{...yield}` / `(yield)` / `f(yield)`，基线同样失败）；② for-of 的 `break`
不调 `IteratorClose`（既有 bug）；③ 函数 Symbol 键枚举缺失（`*Closure` 无
`SymbolProperties`）+ 函数自有键顺序（Go map 无插入序）。


## 二十三、合流批次十（空 yield 表达式位置 / for-of IteratorClose / 函数 Symbol 键与键序 / async generator `yield*` 异步委托 / 移动端分发决策 / WASM 可行性探针）

**本轮形状与往批不同**：13 张 `running` 单里 **8 张的实现早已躺在未合入分支上**（单子挂着 running 只是因为交付没合进 `main`）
⇒ 本轮主体是「**审计既有交付 → 在干净基线上重放 → lead 独立复核**」，只有 3 条是从零实现（空 yield / for-of IteratorClose / 函数 Symbol 键）。
派发 9 条并行路线（同一条消息里并列前台 Agent，各占独立 worktree，基线 `Gox/main` = `e17baeb`；远端中途由并行会话推进到 `9bfab1f`，
该提交只加 `apps/` 12 个文件、**不含引擎代码**，故合流基线与 A/B 不受影响）。

| 路线 | 单 | 类型 | 要点 | 定向 A/B（agent 自报，lead 抽查过族用例） |
|---|---|---|---|---|
| b10-yld | `rmiC4k` | 新实现 | 空 `yield` 在**表达式位置**可解析：根因是 `parseYieldExpression` 先 `nextToken()` ⇒ 空-yield 早退时 `curToken` 越位，破坏「返回后 cur 停在表达式末 token」的全局约定；改为一律基于 `peekToken` 判定、空 yield **不消费** token；另显式承接「形参窗口内不得出现 YieldExpression」早错（此前靠终结符越位侥幸触发） | filter `yield` 1109/1395 → 1207（**GAIN 98 / LOST 0**）；`generators` +9、`async-generator` +9、`yield-ident` +8 |
| b10-iterclose | `rseS4J` | 新实现 | for-of 语句 abrupt 完成调 **IteratorClose**：迭代器改存隐藏槽、`OP_ITER_STEP` 按 `.done` 判结束（替掉「值 === undefined/null 即结束」的启发式，连带修掉 for-of 产 `null`/`undefined` 提前退出）；复用 for-await 的 try/finally 基础设施（新增 `closeSync` 区分同步/异步收尾） | `iterator-close` 5/15 → **15/15**（GAIN 10 / LOST 0）；`statements/for-of` GAIN 36 |
| b10-funcprop | `rS4HXt` | 新实现 | `*Closure`/`*BuiltinFunction`/`*BuiltinMethod` 嵌入 `funcSymStore`（Symbol 键不再静默丢弃）；函数自有字符串键改按 **OrdinaryOwnPropertyKeys**（整数升序 → 插入序，用 `propKeyOrder` 取代 map 字典序）；`*BuiltinFunction.prototype` 必须照常列出（否则 `getOwnPropertyNames(Object)` 丢 `prototype`） | 随全量 A/B 一并验收（未单独留定向数字） |
| b10-asyncgen | `r6e5qp` 子项1 | 审计+补完 | async generator `yield*` **异步委托**：新增 `OP_PUSH_RET_TRY`（try 条目上的「return 完成拦截 PC」）使 return 完成可透传；`throw` 缺方法时先 AsyncIteratorClose 再 TypeError；`yield X` 的 await 移到**编译期**（值须 await，但结算不 await）；Async-from-Sync wrapper 的 next/return/throw 三处一律 PromiseResolve 解包 | `yield-star` 666/720 → **719/720**（GAIN 53 / LOST 0）；`generators`/`async-function`/`async-arrow`/`await` 逐条 0/0 |
| b10-strict | `r63RpV` 子项4 | 核实+补钉 | **核实结论：子项2（`x++` 运行期 ReferenceError）与子项4（顶层 sloppy `this`=globalThis）的实现都已在 `main`**（`wt/strict-p0` 的 `84b7f43` 半成品内容已被别的 sha 合入）⇒ 本轮只补**严格半边**回归测试（严格裸调用 this=undefined、严格 call/apply(null)、严格性向嵌套函数传播、class 体恒严格、箭头词法继承、generator、`new` 不受影响），**无引擎代码改动** | 纯测试（0 行为改动） |
| b10-evalsuper | `roiE5Z` | 核实+补边界 | **核实结论：`super.x` 在直接 eval 里不再误报的实现已在 `main`**（`a7ef20e`，与分支 `6532cf2` 同源，diff 为分支超集）⇒ 本轮只补同族**计算式 SuperProperty** `super[expr]`（前缀解析只认 `super(`/`super.` 的纯解析缺口；中缀下标与两条编译路径早已处理 Computed） | `contains-superproperty` GAIN 8 / `super` GAIN 12，**LOST 0** |
| b10-wasm | `rzTQml` | 实测探针 | `GOOS=js GOARCH=wasm` **真编出来并在 Node 里跑通**（`cmd/goxwasm/`）。体积 **strip 26.01 MiB / plain 26.51 MiB**（比桌面宿主 24.57 MiB **还大**：js/wasm 目标少若干原生优化 + stdlib 全量）。**编译层面无阻塞包**；体积层面唯一候选 = `update`（连带 `net/http`）。**新增致命阻塞点：`process.exit()` 会打死实例**（`stdlib/process.go` 的 `os.Exit`）⇒ Playground 必须宿主屏蔽或让 stdlib 在 wasm 下降级为可捕获异常；`setTimeout` 需宿主驱动 `RunTimers` | 不需要 test262 A/B |
| b10-mobdist | `rgnRC4`/`r8DoFS` | 审计+决策 | **推翻「零实现」前提**：`scripts/build-npm-mobile.sh`、`gen-npm-mobile-pkgs.py`、`docs/mobile-distribution-decision.md` 等已在 `main`。本轮补：`docs/mobile-distribution-decision.md` 定稿（三项决策：产物形态 / 分发渠道 / 版本兼容矩阵四层机制）、`.github/workflows/mobile-release.yml`（nightly + release 两档，产物 APK/HAP/壳工程 zip，缺 NDK/签名时优雅跳过且跳过步骤一律 `::error::` 带证据）、`scripts/check-shell-engine-version.py`（壳工程↔引擎版本闸门，`--require` 可升级为硬性）、`docs/npm-release.md`/`desktop-distribution.md`/`release-post--checklist.md` 口径同步 | 不需要 test262 A/B |
| b10-runner2 | `rNR2Zk`/`r23xdR` | 核实+补钉 | **两张单的主体都已由并行会话合入 `main`**：真模块入口 `EvalModuleFileVMWithGlobals`（`isModule` 分支）+ negative 判据只取首行（`errorHead`/`firstLine`，注释里写明正是「回显源码行侥幸判过」）⇒ 本轮只补「模块默认严格」两条回归测试（模块顶层赋值抛 ReferenceError 且不建全局 / 模块内裸调用 this=undefined） | 纯测试（0 行为改动） |
| **lead 修回归** | — | 独立复核产物 | 全量 A/B 抓到**唯一 LOST**：`for-await-of/iterator-close-non-throw-get-method-abrupt.js`（3/3 稳定复现）。逐条 A/B 定位到 **`9c5123a` 一个提交**（asyncgen 路线）：它把 `GetProperty` 触发的回调桥错误一律消费后交给 `errToValue` 走**返回值**通道，而 `throwIfError` **只认 `*object.Error`** ⇒ 抛出值是普通对象时被当成正常返回值，「该 reject」退化成「正常完成」。改法：按抛出值类型分流（`*object.Error` 走返回值通道保持原 GAIN；其余任意 JS 值**原样退回桥信号**——`callbackError` 与 `callbackErrorValue` **两槽一并还原**，只还原值槽等于没还原） | `iterator-close-non-throw` 3/6 → **6/6**（修复前 5/6）；`AsyncFromSyncIteratorPrototype`(built-ins) 16/38 → **28/38**（GAIN 12 保持）；`yield-star` 666/720 → **719/720**（GAIN 53 保持） |

**A/B（lead 独立实测，基线 = `Gox/main` 的 `9bfab1f`；基线二进制由 `git archive 9bfab1f` 自建）**：

- **全量 23726 例：17742（74.7787%）→ 18098（76.2792%），GAIN 356 / LOST 0**；
- 逐用例 diff 三项：`only_a = 0` / `only_b = 0`（专项 `pass_differs` 见上表修复行）；
- **LOST 0**
- `go build ./...` / `go vet ./...` / `go test -count=1 ./...`（20 包）全绿；
- 徽章 `docs/test262-compliance.json`：74.8% → **76.3%**。

**流程实锤（本轮新增，值得记牢）**：

- **`fullab` 共享锁的 stale 清理判据有 bug**：`find "$LOCK" -maxdepth 1 -mmin +12` 只看**目录内条目**，空目录永远匹配不上 ⇒ 遇到隔夜僵锁会**永久自旋**（本轮实测卡 9 分钟，锁是 14 小时前留下的）。正解：`find "$LOCK" -maxdepth 0 -mmin +12`（判目录自身）或直接比目录 mtime。
- **agent 的最终报告会随父回合中断而丢失，但提交正文留了验收数字** ⇒ 回收成果的标准动作是 `git log --format='%h %s%n%b'` 读提交正文 + `git status`（只看 `git log` 会漏掉未提交的半成品）。本轮 9 条里 8 条有产出，**1 条（b10-runner）零提交零改动**，等于白跑一轮，已重开为 b10-runner2。
- **agent 自报的「LOST 0」不能当验收**：asyncgen 路线自报 `for-await` LOST 0，实际引入了一条稳定回归；只有 lead 的全量逐用例 diff 才抓得到。
- **「单子里的因果链」又一次被证伪**：`rNR2Zk`/`r23xdR`/`roiE5Z`/`r63RpV 子项4` 四张单的主体**都已在 main**，看板却仍挂着 running（没有关闭动作）。⇒ 开工前必须先按 patch-id + 内容双核对账。

## 二十四、合流批次十一（解构绑定 prescan 真实名 / 基线身份订正 / 三路废弃）

**本轮形状**：13 张 `running` 单的推进过程中，**绝大多数被并行会话抢在合流前修完并推上 `main`**，本轮真正非冗余的产出只有 1 条（`rLGyHa`）。合并前 `Gox/main` 已被并行会话推进到 `154b8c4`（相对 `b381cbc` +13 提交）。

| 路线 | 单 | 处置 | 要点 | 证据 |
|---|---|---|---|---|
| b11-linkfix | `rLGyHa` | **合入** | `prescanScope` 对**解构声明项**只跳过合成名 `__destructure__`、没有登记模式里的**真实绑定名**。而 `compileStatements` 会先把语句列表里所有函数声明**提升编译**（编译函数体），函数体解析"列表后面才出现的词法绑定"全靠 prescan 预登记 ⇒ `const [x] = …; function f(){ return x; }` 里 `f` 提升编译时 `x` 尚未入作用域 → `emitGlobalLoad` 退化 → 运行期在**非全局作用域**抛 `ReferenceError`（脚本顶层因顶层绑定是全局属性而侥幸可用）。修法：新增 `prescanDeclarator`，用 `ast.PatternBoundNames` 取真实名逐个 `prescanDeclare` | 探针（Node v22 对照）：`const [x]` + 提升函数 `fDestr=1` ✅、`let [a]`/`const {b}` 混用 `a=3 b=5` ✅（修复前均 `ReferenceError`）；per-iteration 语义 0 回归 |
| b11-hostinit | `rMWHi1` | **废弃（冗余）** | gfx `IP_ADAPTER_INFO` 布局错位致启动即 `SIGSEGV`（fault addr 恒 `0x322b`）。修复内容与并行会话已推的 `2bb3bd5` 同源同效 ⇒ 不再重复合入 | — |
| b11-ergslot | `rqAkgl` | **废弃（被取代）** | Promise 回调抛错归属派生 promise。本路线变体净 **GAIN 170 / LOST 8**（8 条 `dynamic-import` + async 嵌套回归，11 轮收口未果）；并行会话以更完整方案修完（`d53ff69` + `a2320a2` 动态/静态 import 透传原始拒绝值 + `a4bf644` + `4c0e116`）⇒ 弃本路线 | — |
| b11-linkfix | `rUm0q6` | **废弃（冗余）** | 本路线只产出"零回归护栏"；`rUm0q6` 本身已由并行会话 `06e25e7` 修复 | — |

**A/B（lead 独立实测，基线 = 合流时点的 `Gox/main` = `154b8c4`，由 `git worktree add --detach` 自建并逐项验证身份）**：

- **全量 23726 例：18126（76.3972%）→ 18126（76.3972%）**；
- 逐用例 diff 三项：`only_a = 0` / `only_b = 0` / `pass_differs = 0` ⇒ **严格零回归**；
- `go build ./...` 干净、`go vet ./...` 干净、`go test ./vm/... ./compiler/... ./parser/...` 全绿；
- 徽章 `docs/test262-compliance.json` = **76.4%**（并行会话 `154b8c4` 已更新，与本次实测逐位吻合，未再改动）。

> 注：`rLGyHa` 的缺陷面**不在 test262 `language` 套件的计分范围内**（该套件无"函数提升 + 解构绑定于非全局作用域"的用例），故全量数字 +0；其价值由 Node v22 对照探针与新增回归测试（`vm/destructure_scope_binding_test.go`）保证。

**基线身份订正（本轮最重要的教训）**：

- 本会话曾据 `D:/tmp/real-base.json`（17929）判定"批次十的 18098 不可复现、台账与徽章需订正"——**该判定错误**。
- 决定性实验（全新 worktree + `git rev-parse HEAD` + `git status --porcelain` + `git diff --stat -- parser/` 四重确认）：纯净 `b381cbc` **通过**三条 `yield` 族争议用例（各 1/1），`b381cbc` + 脏 parser（把 yield 修复整体 revert 的 +45/-208）则**三条全挂**（0/1）；纯净 `b381cbc` 全量 = **18098 / 76.2792%**。
- ⇒ `real-base.json` 的 `17929` **恰是"`b381cbc` + 脏 parser"的数**，被误标为"真 `b381cbc`"。**台账批次十的 `18098（76.2792%）` 与徽章 `76.3%` 本来正确，无需订正**。
- **纪律**：基准二进制的身份必须用 CM 验证（`git worktree add --detach <tmp> <sha>` → 确认 HEAD/工作区/关键目录三重干净 → 才 `go build`），**不能靠文件名、不能靠"上次是谁编的"**。本会话两次在基线身份上翻车，两次都是因为信了一个未验证来源的 JSON。
## 二十五、合流批次十二（`var` 解构绑定落点 = 函数作用域层）

**本轮形状**：批次十一收尾后，13 张 `running` 单里剩下的唯一真缺口是 `r4McL4`（`var` 解构作用域），其余单已由并行会话修完并在前几轮核销。本轮单线程推进这一条。

| 路线 | 单 | 处置 | 要点 | 证据 |
|---|---|---|---|---|
| b12/var-destr | `r4McL4` | **合入** | `var` 解构与 `let` 共用 `compileDestructureAssignment(assign, true)` 一条路径，而 `bindPatternTarget` 在 `isDecl=true` 时统一走**当前块**的 `declareOnce` ⇒ `for (var [p] of …) {}` 的 `p` 落在循环体块作用域，块退出即消失，块外读 `p` 退化成 `OP_LOAD_GLOBAL` → 运行期 `ReferenceError`（同作用域内声明+读取因共享同一块而侥幸可用）。修法：`isDecl bool` → **三态 `bindKind`**（`bindAssign`/`bindLexical`/`bindVar`），`bindVar` 走新增的 `declareFuncLayerVar`/`emitVarAssign`（FuncLayer 登记）；`for`-of/`for await`-of 的 `Pattern` 分支按 `VarDecl` **三态**分流（`nil` → `bindAssign` 赋值形态）；`collectVarBindingsStmt` 补齐解构声明项真实名的提升（否则槽位晚于循环 `sealFrom` 分配，会被 `OP_ITER_BOUNDARY` 当每轮词法绑定克隆）；`ast.PatternBoundNames` 补 `ObjectPattern.RestTarget` | 全量 A/B **+1 GAIN / 0 LOST**（唯一翻转 `statements/for-of/head-var-bound-names-dup.js`）；node v22 对照探针 21 例全对齐；新增 `vm/var_destructure_scope_test.go` 9 例 |

**A/B（lead 独立实测，基线 = 修复前的 `Gox/main` = `438b29a`）**：

- 全量 23726 例：`18126（76.3972%）` → **`18127（76.4014%）`**；
- 逐用例 diff 三项：`only_a = 0` / `only_b = 0` / `pass_differs = 1`（**GAIN 1 / LOST 0**）；
- `go test ./...`（20 包）全绿；徽章 `docs/test262-compliance.json` 仍为 `76.4%`（76.4014% 四舍五入同位，未改动）。

**本轮实锤（流程教训）**：

- **三态分流实现时漏了 `VarDecl == nil`**：`for ([a, b] of xs)` 是**赋值**形态，应落 `bindAssign`，第一版误写成 `bindLexical` ⇒ 既有测试 `TestForOfLHSAssignment`（`a*100+b` 期望 `908`、错则得 `102`）当场拦下。**"改完先跑全量 `go test ./...`"这一步不能省**。
- **裸环境（`testEval`，不装 stdlib）下、函数体内的 `arr.push(...)` 报 `TypeError: undefined is not a function`**（`Array.prototype.push` 由 stdlib 提供）。为排除"是本轮改动引起"，在**干净 `438b29a` worktree** 上跑同一探针 —— 失败完全一致 ⇒ 既有现象、与本轮无关；护栏测试改用完整管线 `evalJS`。
- **"缺陷是否在计分范围内"必须实测、不能照抄**：开工前的记录是"该缺陷不在 test262 `language` 计分范围内"，实测 A/B 拿到 **+1 GAIN**。

## 二十六、合流批次十三（回调桥「两槽同步消费」= `rr1O8P`）

**本轮形状**：批次十二收尾后，`running` 单里挑的是缺陷形态最确定、且**不依赖 Windows/真机**的一条 —— `rr1O8P`。批次十一 `ergslot` 路线已修好 `throwIfError` 只认 `*object.Error` 的那一族，本单是**同一机制的明面**：内建回调桥的**值槽**没人消费。

| 路线 | 单 | 处置 | 要点 | 证据 |
|---|---|---|---|---|
| b13/ergslot2 | `rr1O8P` | **合入** | 回调桥写**两个**槽：错误槽（`TakeCallbackError`，Go `error`）+ 值槽（`TakeCallbackErrorValue`，`throw x` 的 x）。8 处 `reportUncaught` 位点（fs/http/update_module）只消费错误槽 ⇒ 过期原值滞留值槽。修法：8 处**全部汇聚到同一个 `reportUncaught`**，故改一处即全覆盖 —— 内部无条件 `TakeCallbackErrorValue()`（两槽同步）并**优先用原值渲染**文本；顺带同族位点：`reflect_proxy.go`（`Reflect.construct` 丢原值）改用既有 `callbackThrown()` 口径；`object/observable.go`（`Computed.recompute`）只做值槽消费的**卫生修复**，返回值语义**刻意不动**（属 `rvE6lH` 的 Rx 语义决策范围） | A/B 反证：旧实现下 3 例全 FAIL（`阶段2 读到了阶段1 的过期值 PHASE1`）；新增 `stdlib/callback_slot_test.go` 3 例；`go test ./...` 全绿 |

**根因的关键一环（易被忽略，写在这里防复发）**：只消费错误槽之所以有害，是因为桥**并不保证两槽同时被写** —— `vm.setCallbackErrorValueFromThrow` **只在错误类型是 `*vm.ThrowError` / `*vm.jsThrow` 时才写值槽**（`vm/vm.go:422/426`），其余 4 处 `SetCallbackError`（`vm.go:446/460/473/486`）**只写错误槽**。所以「上一次的抛出值」会一直挂着，直到下一次**只写错误槽**的桥失败被某个取两槽的位点（`stdlib/async.go`、`promise.go`、`eval.go`、`solid.go`）读走 ⇒ 用户 `catch` 到**上一次的值**：不报错，只是值错（静默错值）。

**本轮实锤（流程教训）**：

- **「改一处覆盖 8 处」优于「8 处各加一行」**：8 个位点全都是同一个句型且都调 `reportUncaught`，在函数内部消费值槽即全覆盖，不会再出现"改了 7 处漏 1 处"。审计时先看**汇聚点**再改调用点。
- **判据用例必须能复现危害、而不只是断言"槽被清了"**：`TestStaleValueSlotDoesNotLeakIntoNextBridgeError` 走完「阶段1 抛 `PHASE1` → 阶段2 只写错误槽的桥失败 → 取两槽」整条链路，修复前失败信息直接是 `读到了阶段1 的过期值 PHASE1`。只写"槽残留"断言的话，读起来像是卫生问题，看不出危害。
- **test262 A/B 在本沙箱不可执行**：套件不在仓库内（`.gitmodules` 只有 npm/website/logo 三个子模块，`testdata/` 下无 test262）。本单的「全量 language + built-ins 两套 A/B 均 LOST 0」验收**未做**，需在具备套件的环境补跑；已提交的是单元级 A/B 反证。

## 二十七、打包产物转发命令行参数（`r1q3Gy`）

**本轮形状**：`rr1O8P` 收尾后接着挑的是**与缺陷证据无关、能直接端到端兑付**的一条 —— `r1q3Gy`（打包后的单文件 exe 不把 CLI 参数转给内嵌脚本）。它是「_UNIX TOOLBOX KILLER FEATURE_（双击能开、拖文件上去能开）」的直接缺口，且**不依赖 Windows/真机**，可以在沙箱里真打包、真跑、真对照。

| 路线 | 单 | 处置 | 要点 | 证据 |
|---|---|---|---|---|
| j1/argvbridge | `r1q3Gy` | **合入** | 打包产物把脚本嵌进二进制 ⇒ `os.Args` 里没有「脚本路径」这一项，用户参数从**索引 1** 起，而源码跑（`gox app.js a.log`）从**索引 2** 起 ⇒ 同一份脚本两种跑法错位。修法：新增 `stdlib.SetProcessArgv`（宿主在建 VM 前注入 argv，并连带 `object.InvalidateBuiltinModuleCache("process")` 清掉模块导出表缓存 —— 只改包级变量会被缓存挡住）；两个 `appMainGo*` 模板在建 VM 前注入 `[os.Args[0], entry, os.Args[1:]...]`，使 `process.argv` 恒为 `[可执行文件, 脚本路径, ...用户参数]` | 真打包实跑 A/B：修复前 `argvapp_old error.log --tail=20` → `process.argv[2] = "--tail=20"`（错位）；修复后 → `process.argv[2] = "error.log"`，与 `go run ./cmd/gox main.js error.log --tail=20` **逐项一致**。新增 `stdlib/process_argv_test.go` 3 例 + `packager.TestAppTemplatesForwardProcessArgv`；`go test ./...` 全绿 |

**实锤（流程教训）**：

- **模板是字符串 ⇒ 里面不能出现反引号**：`appMainGo` 用 raw string（反引号）定界，注释里写 `` `gox <file> <args>` `` 会**提前闭合字面量**，`go build` 报 `syntax error: unexpected name gox`。补位注释改用双引号后即通过。生成型模板必须配一个语法兜底测试 —— 本轮在 `packager` 测试里用标准库 `go/parser` 解析两个模板，即可抓住这类低级错误（不做类型检查、不解析依赖，纯语法层）。
- **打包熟路的判断必须"真打包、真跑"**：本单的成因（差一位）靠读代码容易想当然写成"打包版完全没有参数"，真跑一次才发现是**错位**而不是**缺失** —— 错位比缺失更隐蔽（脚本能读到东西，只是读错）。
- **宿主注入型 API 必须同时清模块缓存**：`process.argv` 是 `RegisterBuiltinModule` 的**惰性快照**（`builtinModuleCache`），宿主改了全局状态而缓存不清 ⇒ 脚本读到的仍是旧值。测试里**刻意先 `LookupBuiltinModule("process")` 把缓存喂热**再注入，才能顶住这一点（去掉 `InvalidateBuiltinModuleCache` 后该用例立即 FAIL）。
- **本单不适用 test262 A/B**（同 §二十六 的沙箱约束）：不是语言层语义，改的是宿主接线；判据是上面那组打包实跑对照。

## 二十八、合流批次十四（VM 回调桥值槽 `rZNsX9` + `<scroll>` 滚动位置写入口 `r846P0`）

**本轮形状**：`r1q3Gy` 收尾后接着清 `running`，挑的两条都是**确定形态、且在沙箱内可端到端验证**的单 —— 一条是 §二十六 `rr1O8P` 的同机制另一半（改到 VM 侧），一条是 GUI 侧功能缺口。刻意避开依赖 Windows / 真机 / test262 套件的单。

| 路线 | 单 | 处置 | 要点 | 证据 |
|---|---|---|---|---|
| b14/slotvm | `rZNsX9` | **合入** | 回调桥值槽的**VM 侧**版本。`checkCallbackErr()` 是 vm 包全部同形态位点的唯一汇聚点（vm.go 28 处 + async_from_sync.go 3 处 = 31 处），在其内部**仅当真的取到错误时**连带 `TakeCallbackErrorValue()`（两槽同进同出）；`callToString()` 不属于该句型，单独补消费（它走的是 `CallFunction` → 直接判 `TakeCallbackError()`） | A/B 反证：撤掉修复后新增 5 例中 4 例 FAIL（`值槽未被消费, 残留 PHASE1` / `VM 主循环消费掉错误槽后值槽仍残留 PHASE1` / `callToString 未消费值槽, 残留 TOSTRING-PHASE1` / `相位2 的桥失败后值槽残留 PHASE1`）；新增 `vm/callback_slot_test.go` 5 例；`go test ./...` 全绿 |
| b14/scrollto | `r846P0` | **合入** | `<scroll>` 只有读入口（滚轮 / 拖滑块改 offset），脚本没有任何**写**滚动位置的办法 ⇒ 「跳到底部」「跳到第 N 行」「跟随最新一条」全做不出来。补两个受控 prop：`scrollTop` / `scrollLeft`（数字 = 绝对像素、按内容钳位；别名 `top`/`bottom` 与 `start`/`end`，`bottom`/`end` **粘性**：去重键带解出的最大值，内容变长即算新目标）；以及反向通道 `onScroll({offsetX, offsetY})` | A/B 反证：撤掉写入口 ⇒ `TestScrollWritePropEndToEnd`（`scrollTop={200} 后 offsetY = 0, want 200`）与 `TestScrollWriteDemoScript`（`bottom 别名未贴底: offsetY = 0, max = 196`）FAIL；去掉**去重** ⇒ `TestScrollTopDoesNotFightUserScroll`、`TestScrollTopBottomAliasFollowsGrowingContent` FAIL；去掉 `notifyScroll` ⇒ `TestOnScrollFiresOnUserScroll` FAIL。新增 `gfx/scroll_write_test.go` 9 例；`go test ./...` 全绿 |

**两条各自的根因一环（防复发）**：

- **「契约已存在、实现却缺席」是静默失败**：`testdata/vlist_demo.js` 早就写了 `onScroll: (e) => { setScrollTop(e.offsetY); … }`，而内核从未实现 `onScroll` ⇒ 脚本一个错误都不报，只是那个读数恒为 0。**demo / 文档里已经写出来的 API 比没写文档的更危险** —— 它会让人以为是自己脚本写错了。本次顺手把这个坑填上。
- **写入必须去重、还不能顺手派发**：写入口 `applyScrollCommand` 放在 `layoutScroll` 里（内容量出来之后、钳位与 vlist 开窗之前），所以同一帧内写进去的偏移立刻参与钳位和开窗，不会慢一帧；但也正因为每帧都走一遍，**不去重就等于每帧把用户拽回脚本给的值**（表现是"滚不动"）。反过来，**派发 `onScroll` 不能挂在写入路径上** —— 那是在布局过程中回调脚本，属于重入。写 = 搬指针（哑），手势 = 上报（响），两条路必须分开。

**本轮实锤（流程教训）**：

- **`<button label="…">` 在本仓库不成立**：只有 `menu` / `menuitem` 读 `label` prop（`gfx/menu.go`），`<button>` 的文案是**文本子节点**。按 DOM 直觉写出来的按钮不报错，只是个宽 16px 的空白壳；测试侧 `buttonWithText` 找不到它 ⇒ `click(fake, nil)` 直接 nil 解引用。这是本轮唯一一次卡住的坑，代价不低：错误信息是 `InternalError: VM panic: nil pointer`，栈里指向的是**测试辅助函数**而不是根因。写 demo 先照抄一个现有 demo 的写法。
- **VM panic 的第一现场要主动取**：默认只给 `InternalError: VM panic: …` 一行，看不出在哪。`GOX_PANIC_TRACE=1` 会打出 Go 调用栈 + panic 帧附近的字节码窗口（`vm/vm.go` 的 `runProtected`）；上面那个 nil 解引用就是靠它一眼定位到辅助函数里的。
- **端到端 demo 用例的价值在这里兑现**：`scroll_write_test.go` 前 8 例都是 Go 侧直接调布局，**绕过了 gfx/solid signal → h() 建树这一整段**；`TestScrollWriteDemoScript` 跑真脚本，才把「按钮点下去 → signal → prop 名落在节点上 → 布局期读到」整链接通。
- **驱动 GUI demo 的时序口径**：一次点击一轮 pump（受控 prop 要等 signal 写回 + 下一轮 pump 才落回节点）；断言写在**点击的下一轮** —— 本轮第一版写成"先连点 9 次再断言"，结果断言全部打空，必须先 readout 一轮真实的树才知道差在哪。
- **本批次不适用 test262 A/B**（同 §二十六 的沙箱约束）：一条改的是宿主↔VM 的边界接线，一条是 GUI 组件语义，都没有语言层计分项。

## 待确认（信息缺口）

- `agent_doc/undecided-and-unimplemented.md` 是 **2026-09-18 快照**，其 §四 缺口清单中已有多项（cocoa 后端、iOS 后端、X11 修正、M2 IME、滚动条拖拽、tabs、table/tree、tooltip）在 09-18 后落地。本路线图已按 git 历史更正，但**建议回填该台账**，否则后续排期会继续基于过期口径。
- `gui-component-status.md` 是最新组件权威（203KB，最大 §编号为准），本次未逐节通读；组件级剩余缺口应以它为准做最终核对。

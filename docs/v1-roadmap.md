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
| **G** test262 合规率依 `-jobs` 而变（`rjeHji`，待修跟踪单） | ⚠️ 已归因、未修：runner `executeCases` 在同一进程内用信号量并发跑 `jobs` 个用例，父进程又起 `jobs` 个子进程 ⇒ 真实并发 `jobs × jobs`；引擎有包级可变状态（`vm/vm.go:311 currentVM`、`object/callback.go:20/64 callbackError[Value]`、`stdlib/solid.go:317 solidEffectsLive`）⇒ 同进程并发用例互相污染。证据：基线二进制自身连跑两遍即 9 pass→fail / 3 fail→pass；固定 filter 只改 `-jobs`（1→11、2→11、8→6）结果稳定复现。**纪律：A/B 两侧必须同一 `-jobs`（本项目取 6），基线需连跑两遍取稳定集** |
| **H** 隐式 constructor 不调父类构造（`rCzckg`）+ 两个连带既有缺陷（`rIIXSR`/`rVI6Eb`） | ✅ 已修（2026-10-05，`847992a` + `59d5f4c`）：`class X extends Y {}` 语义改为等价 `constructor(...args){ super(...args) }` —— 新增 `OP_CALL_METHOD_SPREAD`（`0x69`，栈 `[fn, this, argsArray]`），VM 把 `OP_CALL_METHOD` 抽成薄壳与新 opcode 共用 `invokeWithThis`；顺带支持手写 `super(...args)`（此前报 `unsupported expression type` 内部错）。**落地时探针揭出两个既有缺陷**：① `rIIXSR` —— `OP_NEW` 重建闭包时漏结转 `CapturedLocals` ⇒ 凡「构造函数引用 enclosing 作用域绑定」的类经 `new` 实例化都报 TDZ（module 模式 / 嵌套函数作用域中招，script 顶层类因 super 编成 `LOAD_GLOBAL` 而侥幸绕开；**不限于 super**）；② `rVI6Eb` —— `OP_NEW` 的 `callClosure`/`runFrom` 错误直接 `return`，未经 `handleThrow` ⇒ 外层 `try{ new X() }catch` 抓不到，且构造帧不回收。回归 `vm/class_ctor_forward_test.go`（13 例，期望值全取自 Node）+ `vm/new_captured_locals_test.go`（6 例含真 TDZ 守卫）+ `vm/new_throw_catch_test.go`（5 例）。test262 全量 A/B（`-jobs 6`）**7876 → 8015（+139，回归 0）**，修复面集中在 class/elements(116)、super(16)、subclass、Promise regular-subclassing |
| **I** for-await 剩余主体：迭代器驱动的解构 + 早退 IteratorClose（`rKWmkc`） | ✅ 已落地（2026-10-05，`fe77ad1`）：新 opcode `OP_ITER_STEP`（`0xA8`）/`OP_ITER_CLOSE`（`0xA9`）；`compilePatternBind` 重写为 `compileArrayPatternBind`（GetIterator → `PUSH_TRY`/`PUSH_FINALLY` → 逐步 `ITER_STEP` → 收尾 `ITER_CLOSE`，异常路径 swallow-try + `END_FINALLY` 重抛）；plain-object 迭代器认可；parser 补数组/对象模式 elision（洞）、成员目标 `x.y`/`x[k]`、空模式 `[]`/`{}`。Node 22 探针 15 点逐项在 gox 上复现（含 **7.4.6 step 9**「`return()` 返回值非 Object ⇒ TypeError」与 **7.4.2**「`next()` 返回非 Object ⇒ TypeError」——后者让 `yield*` 一处旧假通过转真抛）。test262 全量（`-jobs 4`）**9964 → 11673（+1709，回归 0）**；`-filter dstr` 3154→4816、`for-await-of` 596→913。剩余另立三单：`rGXXZ6`（形参位解构 ~2184）/`r9JAuo`（对象模式族 ~1160）/`rvdPPH`（member-target 求值序 38） |
| **J** 属性描述符建模第一期（`rmdv40` 缺口 2） | ✅ 已落地（2026-10-05，`fe77ad1`）：`PropertyDescriptor` 扩为 `{Value,Writable,Enumerable,Configurable}` + `DataProperty`/`Object.EnumerableKeys`；枚举类 API（`Object.keys/values/entries`、for-in、`JSON.stringify`、`Object.assign`、`propertyIsEnumerable`）全面尊重可枚举性；`defineProperty` 新建缺省全 false、更新**保留原值**；`freeze/seal/preventExtensions` 落地。`built-ins/Object` 定向 606→**763（+157）**；全量 +4、正例回归 0。揭出**内建属性全变可枚举**（`Object.keys(Math)` 44 vs Node 0）⇒ `rwqcke`；22 条定向回归归因为「无装箱原始值 + 内建命名空间无 `Object.prototype` 原型链」⇒ `rm16Za` |
| **K** 读取未声明全局标识符 / eval 吞异常（`rYVgne`） | ✅ 已落地（2026-10-05，`fe77ad1`）：看板原口径被探针**推翻** —— `OP_LOAD_GLOBAL` 早就有 `!found → throwNamedError("ReferenceError")`（`vm/vm.go:1061`，`08dd599` 引入），点名的 `AsyncFunction/is-not-a-global.js` 早已通过；真凶是 `runGlobalEval` 消费 callbackError 后直接 `return result` ⇒ `eval("__x__")` / `eval("throw …")` 静默吞成 `undefined`。另 `AsyncGeneratorFunction` 被 `env.Declare` 误注册为全局属性（Node 下 `undefined`）。test262 全量 9964 → **9979（+15）**；唯一回归（`eval-block-with-statment-regexp-literal-flags`）是 eval 不再吞异常后揭出的 parser 缺陷 ⇒ `r9HBA8` |
| **L** rpEXH2 剩余早错补齐（`rpEXH2`） | ✅ 已落地（2026-10-05，`fe77ad1`）：新增集中校验层 `parser/class_grammar_early_errors.go`（static 成员名 `prototype`、特殊方法名 `constructor`、重复 constructor、HasDirectSuper、**字段 ASI**、对象字面量 ILLEGAL 键），另有「解析已报错则跳过早错遍历」的防御。**顺带修掉裸字段分支的双重 `nextToken`**（`class C { x }` 与多裸字段换行此前根本解析不对）。test262 全量 **+73**、`class/elements` 833→**903**、正例回归 0。剩余另立 `rO13zU`（heritage 箭头+裸计算字段）/`rdyGNY`（initializer 内 direct-eval 早错）/`r63RpV`（sloppy-strict 语义一批） |

## 待确认（信息缺口）

- `agent_doc/undecided-and-unimplemented.md` 是 **2026-09-18 快照**，其 §四 缺口清单中已有多项（cocoa 后端、iOS 后端、X11 修正、M2 IME、滚动条拖拽、tabs、table/tree、tooltip）在 09-18 后落地。本路线图已按 git 历史更正，但**建议回填该台账**，否则后续排期会继续基于过期口径。
- `gui-component-status.md` 是最新组件权威（203KB，最大 §编号为准），本次未逐节通读；组件级剩余缺口应以它为准做最终核对。

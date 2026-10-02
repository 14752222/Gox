# Gox v1.0.0 路线图（v0.9.0 → v1.0.0）

> 生成：2026-10-01 ｜ 基线：工作区版本号 **0.9.0**（`npm/package.json` 与 `cmd/gox/main.go` 一致；
> 最新提交 `efe8cc6 release: v0.8.0`，v0.8.0/v0.9.0 **尚未打 tag**）
> 依据：17 篇 `docs/` + README + `app/NATIVE-HOST.md` + `agent_doc/undecided-and-unimplemented.md`（2026-09-18 快照）
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
| P0-1 | **`await` 被拒 Promise + `try/catch` 不生效** | `docs/js-runtime-api-tutorial.md` §5.3 已知限制 1 明文 | async/await 是基于 generator 实现的，`await` 编译为 `yield`，rejected 后生成器无法恢复 ⇒ try/catch 块失效。**这是语义级 bug，不是"限制"**，普通用户第一周就会踩到 |
| P0-2 | **私有字段 / 私有方法 `#name`** | Grep `PrivateName\|#name` 于 `parser/ compiler/ vm/` **零命中**；`test262-t04-report.md` §四列为**高风险最大收益 ~3449 例** | 现代 class 代码的基础设施（React 风格组件、库封装都依赖）。缺失会导致整类源码无法运行 |
| P0-3 | **`for await...of`** | Grep `for await\|ForAwait\|AsyncIterator` **零命中**；T04 报告列 ~1500 例、中高风险 | 异步迭代是 async 生态的标准写法；曾试探后**完整回退**（runner 里应保留回退记录） |

> 附带：AsyncGenerator 目前"可编译执行但 `.next()` 协议不符"（T04 报告）。修 P0-2/P0-3 时需一并处理。

## 二、P0 —— 平台空白

| # | 项 | 现状证据 |
|---|---|---|
| P0-4 | ~~**鸿蒙（ArkTS）全线 unsupported**~~ **已于 2026-10-02 大幅收口** | 原口径基于 `app/NATIVE-HOST.md` 全 ⬜ 与 stub 宿主。现状：HF1/HF2 已落地 —— NAPI 通道层（`GoxDispatch` 序号分发 + `napi_module_register`）+ 装配层 + 交叉编译脚本（`scripts/build-harmony.sh`，OHOS clang + sysroot）+ ArkTS 壳工程（折叠上报链路含 `display.on('foldStatusChange')`），壳工程已通过命令行 `assembleHap` 构建并核对产物（`4af646c`）；契约测试 `fold_contract_test.go` 5 条鸿蒙静态契约全绿。**剩余缺口收敛为「模拟器未验收（HAP 未签名）」—— 验收口径：模拟器即可（2026-10-02 拍板，不依赖真机折叠屏；折叠上报 HF2 逻辑已由桌面资产测试覆盖，模拟器验 HF1）**，不再是平台空白 |
| P0-5 | **Android 侧代码本机从未编译验证** | `app/NATIVE-HOST.md`：Android libgox **未编译（无 NDK）**、Kotlin **未编译（无 gradle/SDK）**。虽然 M1 已在模拟器 x86_64/API 34 六项验收全通，但"能跑"与"能在这里重建"是两件事 —— v1 前应在有 NDK 的环境重跑 `bash scripts/build-android.sh` |
| P0-6 | **真机（arm64）验证缺失** | `agent_doc/mobile-port-plan.md` §九：M1 验证跑在模拟器上，`input tap` 是注入事件；真机触摸、多指、性能均未验 |

## 三、P1 —— 交互能力补齐

| # | 项 | 现状证据 | 备注 |
|---|---|---|---|
| P1-1 | **Tab 键焦点遍历** | `docs/gui-guide.md` §3 末明文"尚未实现（需要 focusable 注册表）" | 桌面应用可访问性的地基；键盘用户完全无法操作 |
| P1-2 | **Linux/X11：IME 不支持** | `gfx/ime.go:15` + `gfx/x11/x11.go:12` 两处 `TODO(P2-7)`，需走 XIM 协议 | 中文/日文用户无法在 Linux 下输入 |
| P1-3 | **Linux/X11：剪贴板不支持** | `docs/gui-guide.md` §2 平台矩阵 | |
| P1-4 | **Linux/X11 未经实机验证** | `undecided-and-unimplemented.md` §三："代码已实现但未经 Linux 实机验证" | **注意**：`f7898a6` 已加 CI Linux xvfb 无头闸门 —— 需要确认这是否等价于"实机验证"，若只是无头冒烟则仍未达标 |
| P1-5 | **`gx/dialog` 缺 `saveFile`** | `docs/gui-guide.md` §2：macOS `NSSavePanel` **已接后端但脚本侧无入口** | 后端能力已在，纯补契约 + 导出；性价比最高的一条 |
| P1-6 | **显示器插拔不派发 `onDisplayChange`（cocoa）** | `docs/gui-guide.md` §2：win32 有、cocoa 待补 | 多屏是项目核心卖点（"目标多屏幕"），macOS 缺这条与定位矛盾 |
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
| P1-16 | **v0.8.0 / v0.9.0 未打 tag** | `git tag -l` 最新只到 v0.7.0，但版本号已是 0.9.0。发版流程要求 tag 与包版本对齐 |
| P1-17 | **工作区有未提交改动** | `git status`：`README.md`、`cmd/gox/main.go`、`npm`（子模块）、`vm/timer_stress_test.go`、`website` 均 M 状态 |

## 六、P2 —— 文档已明说"v1 不做"（可延后，但要复述）

| 项 | 依据 |
|---|---|
| 读屏（VoiceOver / TalkBack） | `mobile-adaptation.md` §9、`mobile-regression-checklist.md` §6：**P1 再立项** |
| 内核收编 dp 单位 | `mobile-adaptation.md` §9：现靠脚本侧 `pixelRatio`，内核收编是 **P1 候选** |
| IME 结果提交制（组合中间态不逐键上报） | 同上，设计选择 |
| `textarea` 软换行 / 文本选区复制 / 横向滚动 | `undecided-and-unimplemented.md` §三：v1 明确不做（**注**：横向滚动已随 `59f4bdb` 落地，此条需复核是否只剩软换行与选区） |
| GUI 常驻脚本热更新 | `docs/dev-workflow.md` 顶部 TODO：泵循环阻塞监听，需先在 gfx 上游讨论 |
| 复杂 shaping / 富文本 / 粗斜体字族 | `undecided-and-unimplemented.md` §四：文本域缺口，v1 不做 |
| `transform` / 路径对象 / 变换矩阵 / 贝塞尔 / 裁剪栈 | 同上 §四：绘制域缺口 |
| 双击/三击、通用 drag & drop、文件拖放、系统右键菜单抑制 | 同上 §四：事件域缺口 |
| `alignSelf` | 同上 §四：布局域缺口 |
| 窗口位置与层级、模态子窗口、光标形状、窗口尺寸约束/全屏 | 同上 §四：窗口/系统域缺口 |
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

1. **先做"零成本兑付"**：P1-5（`saveFile`）、P1-15（官网文案）、P1-16（补 tag）、P1-17（提交工作区）—— 半天内可清完，且直接改善外部可见度。
2. **再攻 P0-1**：`await` + try/catch 是语义级 bug，修它比堆合规率更值。
3. **P0-2 → P0-3**：按 T04 报告既定顺序（私有字段 ~3449 例收益最大）。
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

> P1-16（补 tag）与 P1-17（工作区改动）仍在推进中。

## 待确认（信息缺口）

- `agent_doc/undecided-and-unimplemented.md` 是 **2026-09-18 快照**，其 §四 缺口清单中已有多项（cocoa 后端、iOS 后端、X11 修正、M2 IME、滚动条拖拽、tabs、table/tree、tooltip）在 09-18 后落地。本路线图已按 git 历史更正，但**建议回填该台账**，否则后续排期会继续基于过期口径。
- `gui-component-status.md` 是最新组件权威（203KB，最大 §编号为准），本次未逐节通读；组件级剩余缺口应以它为准做最终核对。

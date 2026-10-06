# 移动端分发策略（M11）—— 决策与落地

> 本文回答 `rgnRC4` / `r8DoFS` 需要的三件事：**产物形态 / 分发渠道 / 版本兼容矩阵**，
> 并给出已落地的发布面布局。文件位置本来更该在 `docs/`（那才是对外文档目录），
> 本次落在 `app/` 是因为该工作流只允许改 `scripts/ | .github/workflows/ | npm/ | app/ | scaffold/`
> —— 合并时若要挪进 `docs/`，正文无需改动。

## 结论速览

- 移动端产物**只发预编译库**（`libgox.so` / `libgox.a` + `libgox.h`），**不发壳工程源码**。
- 发布渠道 = **平台子包** `@goxjs/goxjs-mobile-<platform>-<abi>`，**不塞主包、不用 optionalDependencies**。
- 版本对齐 = 子包 `version` 与主包**同号** + 子包 `manifest.json` 的 `goxVersion` 消费端校验。

---

## 1. 三个决策

### 决策 1：产物形态 —— 只发「预编译库」，不发壳工程源码

移动端不是一个可执行文件，而是 **`.so`/`.a` 预编译库 + 壳工程（Kotlin / Swift / ArkTS）**。
`r8DoFS` 原始描述里最重的顾虑是"进 npm 意味着包里要带一整套壳工程源码，体积与维护成本要重新评估"。

**结论：发布面只带预编译库（`libgox.so` / `libgox.a` + `libgox.h`），不带壳工程源码。**
壳工程源码走仓库内的 `app/{android,harmony,ios}` 骨架与 `gox create` 生成的工程骨架。
这样既给了移动端一条公开的分发路径，又没有把整棵壳工程源码树塞进 npm。

### 决策 2：分发渠道 —— 三选项代价对比，定稿「平台子包」

| 选项 | 桌面用户代价 | 移动端用户代价 | 版本对齐 | 判定 |
|---|---|---|---|---|
| **A. 主包直塞** `mobile/` 进 `@goxjs/goxjs` | **被迫从 ~20MB 顶到 ~72MB**（每个桌面用户都下载，哪怕永远不碰移动端） | 开箱可用 | 天然同号 | ❌ |
| **B. GitHub Releases 独立渠道** | 0 | 要离开 npm 去找渠道、手动下载/校验、自己拼进工程 | 靠人；没有 registry 版本判重 | ❌ |
| **C. 平台子包（定稿）** `@goxjs/goxjs-mobile-<platform>-<abi>` | **0**（主包 ~20MB 不变） | `npm i @goxjs/goxjs-mobile-android-arm64-v8a` 一条命令 | 子包 `version` 与主包同号，`check-registries.py` 卡死 | ✅ |

**为什么定稿 C：**
1. **谁用谁装**：移动端库单平台 20–30MB，桌面用户完全不需要 —— A 把成本强加给最大的人群。
2. **留在 npm 生态**：`npx` / `npm i` / registry 版本判重 / OIDC provenance 全都复用；
   B 要另起一套产物托管与获取说明，且"用户装的是哪个引擎版本"更难说清。
3. **与 M5 不冲突**：M5 定稿的是"`@goxjs/goxjs` = **桌面二进制**分发面"。
   A 会破坏它（往主包塞移动端），C 把它**扩展**成一组按平台拆分的二进制分发面 ——
   移动端子包与主包是同一性质（预编译二进制），只是目标平台不同、且**分开发布**。
4. **发布面清晰**：主包 `files` = `bin/gox.js` + `binaries/`；每个子包 `files` = 自己那一个产物 +
   `manifest.json`。`check-registries.py` 现在**双向**卡：主包出现 `mobile/` 直接红（防 A 回归），
   子包命名/版本/清单自洽也红。

### 决策 3：版本兼容矩阵 —— 「同号 + manifest 校验」

壳工程版本 ↔ 引擎版本错配（新壳配旧引擎，或反之）是 **JNI/NAPI 契约不匹配、运行时才崩**，
编译期拦不住。机制分三层：

| 层 | 动作 | 落在哪 |
|---|---|---|
| 发布约定 | 子包 `version` 必须与主包**同号**（本轮 0.9.0） | `check-registries.py` 检查 5 |
| 产出端 | 每个子包生成 `manifest.json`：`goxVersion` + 产物 `kind`/`sha256`/`size` | `scripts/build-npm-mobile.sh` |
| 消费端 | 取库前校验：`manifest.goxVersion == 引擎版本`；每个产物 `sha256` 与清单一致；子包 `name` 与目录名一致 | `scripts/fetch-mobile-libs.sh` |

消费端命令（壳工程在仓库内时自动读 `cmd/gox/main.go` 的 `const version` 做对齐）：

```bash
npm i @goxjs/goxjs-mobile-android-arm64-v8a    # 按需装
bash scripts/fetch-mobile-libs.sh              # 校验并放进 app/{android,harmony,ios}
bash scripts/fetch-mobile-libs.sh --check-only # 只校验，不落盘（CI 可用）
```

---

## 2. ⚠️ 为何不用 optionalDependencies 自动装

这是最容易被反问的一点：**为什么不把子包写进主包的 `optionalDependencies`，让 npm 自动按平台装？**

因为 **npm 默认就会安装 `optionalDependencies`**（只有显式 `--no-optional` / `--omit=optional`
才跳过）。把它写进去，等于每个桌面用户 `npm i @goxjs/goxjs` 时**照样被塞 20–30MB 的移动端库**——
和"主包直塞"（选项 A）的代价**完全一样**，等于没拆。

有人会说 `optionalDependencies` 的经典用法正是"平台不匹配就跳过"（如 esbuild 的 `@esbuild/*`）。
但那个场景成立的前提是：**每个用户都需要它**（esbuild 在每个平台都要跑），只是平台不同。
移动端库不是这样 —— **桌面用户不是"装了用不上"，而是"根本不该装"**：他们永远在桌面上跑 `gox`，
不会去链 `libgox.so`。"按平台跳过的自动依赖"在这里只会退化成"给所有人多下 20–30MB"。

所以：**平台子包不进 `dependencies` / `optionalDependencies` / `peerDependencies`，
靠移动端消费者显式安装**，再由 `scripts/fetch-mobile-libs.sh` 从 `node_modules` 定位并落进壳工程。

（这也让"用户装的是哪个平台、哪个版本"变得显式可查：`npm ls @goxjs/goxjs-mobile-*` 一清二楚，
而不是藏在 optional 依赖树里。）

---

## 3. 发布面布局

```
@goxjs/goxjs                            主包（M5 面，不变，~20MB）
├── bin/gox.js                          bin 入口（桌面可执行转发）
└── binaries/<os>-<arch>/gox[.exe]      桌面五平台可执行文件

@goxjs/goxjs-mobile-<platform>-<abi>    平台子包（M11 面，本次落地；每个独立发布）
├── package.json                        name/version/files/access（committed 定义）
├── README.md                           npm 自动收录（包页面用）
├── manifest.json                       goxVersion + 产物 kind/sha256/size
└── libgox.so | libgox.a + libgox.h     该平台该 ABI 的预编译库
```

当前定义的 4 个子包：

| 子包 | 产物 |
|---|---|
| `@goxjs/goxjs-mobile-android-arm64-v8a` | `libgox.so` |
| `@goxjs/goxjs-mobile-harmony-arm64-v8a` | `libgox.so` |
| `@goxjs/goxjs-mobile-ios-iphoneos-arm64` | `libgox.a` + `libgox.h` |
| `@goxjs/goxjs-mobile-ios-iphonesimulator-arm64` | `libgox.a` + `libgox.h` |

子包**定义**（`npm/packages/<dirname>/package.json`）是 committed 源文件；**产物目录**
`dist/npm-mobile-pkgs/<dirname>/` 是构建输出（`dist/` 已 gitignore），由
`build-npm-mobile.sh` 把 committed 的 `package.json`/`README.md` 拷进去、再落产物与清单。

---

## 4. 产出矩阵（谁能产出哪些平台）

移动端窗口/输入只存在于宿主侧，绕不开各平台自己的工具链，所以"能编哪些"是**按主机探测**的：

| 平台 | 子包 | 工具链 | 本机(Windows) | 托管 CI |
|---|---|---|---|---|
| android | `goxjs-mobile-android-arm64-v8a` | Android NDK（c-shared） | ✅ 实测 arm64-v8a 22MB | ✅ ubuntu（`release.yml` publish / `mobile-smoke.yml`） |
| harmony | `goxjs-mobile-harmony-arm64-v8a` | DevEco SDK（OHOS clang + sysroot） | ✅ 实测 arm64-v8a 30MB | ❌ 见下 |
| ios | `goxjs-mobile-ios-{iphoneos,iphonesimulator}-arm64` | macOS + Xcode（c-archive） | ❌ 做不到 | ✅ macos（`release.yml` 的 `mobile-ios` 作业） |

### iOS 为什么本机做不到
c-archive 链接要 Xcode 的 clang + iOS SDK，Windows/Linux 上没有。
**需要什么**：一台 macOS（或 macOS runner）。已落成 `mobile-ios` 作业（`runs-on: macos-latest`，
复用 `scripts/build-ios.sh --lib-only`），整包子包以 artifact 交给 `publish` 一起发布。

### harmony 为什么不在托管 CI
OHOS clang + musl sysroot 来自 DevEco SDK，需华为账号/许可且体积大，托管 runner 装不了。
**需要什么**：自建 runner 或本地按需构建（本机已验证可产）。
**建议另立单**：给 harmony 加一条"自建 runner / 手动 `workflow_dispatch`"的产出作业。

---

## 5. 用户获取方式（移动端 vs 桌面端）

| | 桌面端 | 移动端（M11） |
|---|---|---|
| 拿到什么 | 一个 `gox` 可执行文件 | 预编译库（`libgox.so`/`.a`）+ 壳工程骨架 |
| 从哪拿 | `npm i -g @goxjs/goxjs` | `npm i @goxjs/goxjs-mobile-<platform>-<abi>`（按需） |
| 怎么用 | 直接跑 `gox` / `goxjs` | `scripts/fetch-mobile-libs.sh` 把库放进壳工程，再 `gradle` / `hvigor` / `xcodebuild` |
| 本机要不要工具链 | 不要（二进制自足） | **要**：android 要 NDK+JDK、harmony 要 DevEco、ios 要 Xcode——但**不需要 Go** |
| 版本对齐 | 包版本 = 引擎版本即可 | 子包版本与主包同号；`manifest.json` 的 `goxVersion` 消费端校验（见决策 3） |

差异的根因：桌面是**可执行文件**（跑起来就完事），移动端是**要被宿主 App 链接的库**
（还差一层壳工程把它装进 APK/HAP/App）。

---

## 6. 已落地的改动清单

| 文件 | 作用 |
|---|---|
| `npm/packages/goxjs-mobile-*/package.json` + `README.md`（新增） | 4 个平台子包的定义 |
| `scripts/build-npm-mobile.sh`（重写） | 编 android/harmony/ios 的 libgox，staging 成 `dist/npm-mobile-pkgs/<dirname>/`（含生成的 `manifest.json`） |
| `scripts/fetch-mobile-libs.sh`（重写） | 消费端：从已装的平台子包校验 `goxVersion` + 逐产物 `sha256`，再放进三平台壳工程 |
| `scripts/check-registries.py` | 检查4 改为主包 `files` **不含 `mobile/`**；新增检查5「移动端子包自洽」（命名/版本/清单；对存在的 staging 逐产物比对 sha256） |
| `scripts/build-ios.sh` | 新增 `--lib-only`（只编 `libgox.a`，不碰壳工程/xcodebuild） |
| `.github/workflows/release.yml` | `mobile-ios` 作业产出 iOS 子包；`publish` 编 android 子包、下载 ios 子包、校验、逐子包 pack/publish |
| `.github/workflows/mobile-smoke.yml` | `npm-package` 作业：主包 pack **不含 mobile/** 且无大文件回归 + 每个子包 pack 含产物且 sha256 与 manifest 一致 |

---

## 7. 未完成边界

- **壳工程模板「发布」无独立制品**：壳工程源码目前只随 Gox 仓库与 `gox create` 分发；
  `gox create` 的内置骨架已含 `android/` 与 `ios/`，**缺 `harmony/` 骨架**。
  建议另立单：把 `harmony/` 骨架补进 `scaffold/template/`（并同步 `scaffold_test.go` 的 `wantFiles`）。
- **成品 APK / HAP 的公开渠道**：本次只做到"预编译库进子包、iOS 子包由 CI 产出"。
  成品 APK/HAP 的发布（GitHub Releases nightly + release 两档）属父任务 `rgnRC4` 的另一条，**未做**。
- **harmony 子包的 CI 产出**：见 §4，建议另立自建 runner 单。
- **npm 侧一次性配置**：4 个新子包都要在 npmjs.com 上单独配 Trusted Publishing
  （Organization/user=`14752222`、Repository=`Gox`、Workflow filename=`release.yml`），
  否则首次发布以误导性的 404 收场。
- **`docs/npm-release.md` 验收口径待同步**：仍是"5 个平台二进制"，未含子包 —— `docs/` 不在本工作流文件区。

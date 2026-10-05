# 移动端分发策略（M11）—— 决策与落地

> 本文回答 `rgnRC4` / `r8DoFS` 需要的三件事：**产物形态 / 分发渠道 / 版本兼容矩阵**，
> 并给出已落地的发布面布局。文件位置本来更该在 `docs/`（那才是对外文档目录），
> 本次落在 `app/` 是因为该工作流只允许改 `scripts/ | .github/workflows/ | npm/ | app/ | scaffold/`
> —— 合并时若要挪进 `docs/`，正文无需改动。

---

## 1. 三个决策

### 决策 1：产物形态 —— 只发「预编译库」，不发壳工程源码

移动端不是一个可执行文件，而是 **`.so`/`.a` 预编译库 + 壳工程（Kotlin / Swift / ArkTS）**。
`r8DoFS` 原始描述里最重的顾虑是"进 npm 意味着包里要带一整套壳工程源码，体积与维护成本要重新评估"。

**结论：npm 发布面只带预编译库（`libgox.so` / `libgox.a` + `libgox.h`），不带壳工程源码。**
壳工程源码走仓库内的 `app/{android,harmony,ios}` 骨架与 `gox create` 生成的工程骨架。
这样既给了移动端一条公开的分发路径，又没有把整棵壳工程源码树塞进 npm。

### 决策 2：分发渠道 —— 主发布面仍是 `@goxjs/goxjs`，但落在 `mobile/` 子目录

`r8DoFS` 担忧"把移动端塞进同一个包会破坏 M5『npm 包 = 桌面二进制分发面』的约定"。

**结论：不破坏。** M5 的约定是"这个包是**二进制分发面**"（而不是"只许放桌面"）。
移动端产物与桌面 `binaries/` 是同一性质的东西——**预编译二进制**，只是**目标平台不同**：

```
@goxjs/goxjs
├── bin/gox.js                     bin 入口（桌面可执行转发）
├── binaries/<os>-<arch>/gox[.exe] 桌面五平台可执行文件（M5 面）
└── mobile/                        移动端预编译库（M11 面，本次新增）
    ├── manifest.json              版本 + 每个产物的 sha256/size
    ├── android/<abi>/libgox.so
    ├── harmony/<abi>/libgox.so
    └── ios/<sdk>-<arch>/{libgox.a,libgox.h}
```

之所以不新开 `@goxjs/gox-android` 等独立子包：那条路会给"壳工程 ↔ 引擎版本必须成对"
再加一层跨包对齐成本，且移动端库的体积（arm64 单平台 20–30MB）拆包并不能省。
若将来移动端产物需要独立发版节奏，再拆子包——`manifest.json` 的 `platform` 字段已经为
拆分预留了元数据。

### 决策 3：版本兼容矩阵 —— 用 `manifest.json` + 消费端校验挡住错配

壳工程版本 ↔ 引擎版本错配（新壳配旧引擎，或反之）是 **JNI/NAPI 契约不匹配、运行时才崩**，
编译期拦不住。机制分两层：

| 层 | 动作 | 落在哪 |
|---|---|---|
| 产出端 | 每次 staging 重新扫描产物，写 `mobile/manifest.json`，含 `goxVersion` + 每个产物的 `sha256`/`size`/`platform`/`abi`/`kind` | `scripts/build-npm-mobile.sh` |
| 消费端 | 取库前校验：`manifest.goxVersion == 引擎版本`；每个产物 `sha256` 与清单一致；请求的 `platform+abi` 确实在清单里 | `scripts/fetch-mobile-libs.sh` |

消费端命令（壳工程在仓库内时自动读 `cmd/gox/main.go` 的 `const version` 做对齐）：

```bash
bash scripts/fetch-mobile-libs.sh                 # 校验并放进 app/{android,harmony,ios}
bash scripts/fetch-mobile-libs.sh --check-only     # 只校验，不落盘（CI 可用）
```

---

## 2. 产出矩阵（谁能产出哪些平台）

移动端窗口/输入只存在于宿主侧，绕不开各平台自己的工具链，所以"能编哪些"是**按主机探测**的：

| 平台 | 产物 | 工具链 | 本机(Windows) | 托管 CI |
|---|---|---|---|---|
| android | `mobile/android/<abi>/libgox.so` | Android NDK（c-shared） | ✅ 可产（实测 arm64-v8a 22MB） | ✅ ubuntu（`release.yml` publish / `mobile-smoke.yml`） |
| harmony | `mobile/harmony/<abi>/libgox.so` | DevEco SDK（OHOS clang + sysroot） | ✅ 可产（实测 arm64-v8a 30MB） | ❌ 见下 |
| ios | `mobile/ios/<sdk>-<arch>/{libgox.a,libgox.h}` | macOS + Xcode（c-archive） | ❌ 做不到 | ✅ macos（`release.yml` 的 `mobile-ios` 作业） |

### iOS 为什么本机做不到
c-archive 链接要 Xcode 的 clang + iOS SDK，Windows/Linux 上没有。
**需要什么**：一台 macOS（或 macOS runner）。已在 `release.yml` 落成 `mobile-ios` 作业
（`runs-on: macos-latest`，复用 `scripts/build-ios.sh --lib-only`），产物以 artifact 交给
`publish` 合并进 `npm/mobile/ios/`。

### harmony 为什么不在托管 CI
OHOS clang + musl sysroot 来自 DevEco SDK，需华为账号/许可且体积大，托管 runner 装不了。
**需要什么**：自建 runner 或本地按需构建（本机已验证可产）。
**建议另立单**：给 harmony 加一条"自建 runner / 手动 workflow_dispatch"的产出作业。

---

## 3. 用户获取方式（移动端 vs 桌面端）

| | 桌面端 | 移动端（M11） |
|---|---|---|
| 拿到什么 | 一个 `gox` 可执行文件 | `libgox.so` / `libgox.a` 预编译库 + 壳工程骨架 |
| 从哪拿 | `npm i -g @goxjs/goxjs` | `npm i @goxjs/goxjs` → 库里 `node_modules/@goxjs/goxjs/mobile/` |
| 怎么用 | 直接跑 `gox` / `goxjs` | 用 `scripts/fetch-mobile-libs.sh` 把库放进壳工程，再 `gradle` / `hvigor` / `xcodebuild` |
| 本机要不要工具链 | 不要（二进制自足） | **要**：android 要 NDK+JDK、harmony 要 DevEco、ios 要 Xcode——但**不需要 Go** |
| 版本对齐 | 包版本 = 引擎版本即可 | 壳工程 ↔ 引擎由 `manifest.json` 的 `goxVersion` 对齐（见决策 3） |

差异的根因：桌面是**可执行文件**（跑起来就完事），移动端是**要被宿主 App 链接的库**
（还差一层壳工程把它装进 APK/HAP/App）。

---

## 4. 已落地的改动清单

| 文件 | 作用 |
|---|---|
| `scripts/build-npm-mobile.sh`（新增） | 编 android/harmony/ios 的 libgox，staging 进 `npm/mobile/`，扫描生成 `manifest.json` |
| `scripts/fetch-mobile-libs.sh`（新增） | 消费端：从装好的 `@goxjs/goxjs/mobile/` 校验并放进三平台壳工程 |
| `scripts/check-registries.py` | 「npm 包清单自洽」新增：`files` 必须含 `mobile/`（与 `binaries/` 同一条空壳包防线） |
| `scripts/build-ios.sh` | 新增 `--lib-only`（只编 `libgox.a`，不碰壳工程/xcodebuild） |
| `.github/workflows/release.yml` | 新增 `mobile-ios` 作业；`publish` 里编 android、合并 ios、重扫清单、校验、清理 |
| `.github/workflows/mobile-smoke.yml` | 新增 `npm-package` 作业：打包后 `tar -tzf` 核对 `mobile/` 真的进包 |

---

## 5. 未完成边界

- **壳工程模板"发布"未做独立制品**：壳工程源码目前只随 Gox 仓库与 `gox create` 分发；
  `gox create` 的内置骨架已含 `android/` 与 `ios/`，**缺 `harmony/` 骨架**。
  建议另立单：把 `harmony/` 骨架补进 `scaffold/template/`（并同步 `scaffold_test.go` 的 `wantFiles`），
  再评估是否需要一个独立的壳工程模板包/仓库。
- **Android APK / iOS 壳工程 / 鸿蒙 HAP 的"公开渠道成品"**：本次只做到"预编译库进 npm、
  iOS 库由 CI 产出"。成品 APK/HAP 的发布（GitHub Releases nightly + release 两档）属于
  父任务 `rgnRC4` 里"CI 产物发布到 GitHub Releases"那一条，**未做**。
- **`docs/` 侧文档未同步**：`docs/npm-release.md` 的验收口径仍是"5 个平台二进制"，
  未含 `mobile/`；应在下一步补上移动端验收条目（该目录不在本工作流文件区内）。

# 移动端分发策略决策文档（rgnRC4 / r8DoFS）

> **状态：建议已定稿，决策待用户拍板 —— 拍板点集中在 §6。** 本文只出建议，不替用户做决定。
>
> 本文是看板单 **rgnRC4（[M11] 移动端分发策略）** 及其子单 **r8DoFS（npm 移动端产物）** 的
> **权威决策文档**。`app/MOBILE-DISTRIBUTION.md` 是 M11 当时的**落地记录**（写它时该工作流只允许
> 改 `app/`，所以没能落在 `docs/`），两者关系见 §0.2 —— 本文拍板后以本文为准。
>
> 基线：`Gox/main` = `e17baeb`（引擎 `0.9.0`，`cmd/gox/main.go:28`）。
> 凡标「已落地」的都给了文件路径 + 行数或 sha 作为证据（证据怎么取的见 §0.3）。

---

## 0. 先对齐：与 M5 的兼容面结论、文档关系、现状审计

### 0.1 与 M5 的兼容面结论 —— 不冲突，且**不需要修订 M5**

M5 已定稿（`docs/npm-compat.md`）：**`@goxjs/goxjs` = 桌面二进制分发面**（主包只装桌面可执行，~20MB）。

本文三项决策的硬约束是「**不破坏 M5**」。逐条核对后的结论：

| 决策 | 与 M5 的关系 | 是否要修订 M5 |
|---|---|---|
| 决策一 产物形态（库进子包、壳工程源码不进 npm） | 主包内容一个字节都不变 | **不需要** |
| 决策二 分发渠道（平台子包 + Releases 两档） | 是 M5「二进制分发面」的**扩展**：子包与主包同性质（预编译二进制），只是目标平台不同、分开发布 | **不需要** |
| 决策三 版本兼容矩阵 | 与 M5 无关（新增发布纪律） | **不需要** |

已被 `scripts/check-registries.py:523` 用代码卡死：主包 `package.json` 的 `files` 里**一旦出现
`mobile/` 直接报错**（防「又给每个桌面用户塞几十 MB」的回归）。

> **唯一需要修订的是口径文档**，不是 M5 结论：`docs/npm-release.md` 的验收口径原先只写
> 「5 平台二进制」，未含移动端子包 —— 本轮已同步（见 §5）。这就是「若冲突要写明修订哪个、为什么」
> 的答案：**没有方案冲突，只有一处文档口径滞后**。

### 0.2 文档关系（避免两份决策文档打架）

| 文档 | 定位 | 处置 |
|---|---|---|
| `docs/mobile-distribution-decision.md`（本文） | rgnRC4 的**权威决策**（含成品 App 公开渠道，是父任务的另一半） | 拍板后以此为准 |
| `app/MOBILE-DISTRIBUTION.md`（218 行） | M11 的**落地记录**（子包布局、`optionalDependencies` 论证、改动清单） | 内容与本文一致，不冲突；拍板后建议瘦身为「指向本文」的落地记录（该改动不在本工作流文件区，列 §6 待办） |
| `packaging/npm-mobile/README.md`（71 行） | 子包**定义**的单一真源说明 | 保持 |
| `docs/npm-release.md`（158 行） | 桌面发版手册 | 本轮补移动端小节（见 §5） |

### 0.3 现状审计（逐项，含证据；「零实现」的说法**不成立**）

看板单描述写的是「零实现」，但 `main` 上其实已经落了不少。逐项核实结果：

| # | 清单项 | 在 main 上 | 证据 |
|---|---|---|---|
| 1 | `scripts/build-npm-mobile.sh` | ✅ 有 | 396 行；读 `packaging/npm-mobile/` 出 staging 到 `dist/npm-mobile-pkgs/` |
| 2 | `scripts/gen-npm-mobile-pkgs.py` | ✅ 有 | 163 行；`--check` 供 CI 断言「生成物与定义已同步」 |
| 3 | `packaging/npm-mobile/`（单一真源） | ✅ 有 | `README.md` + 4 个 `<dirname>/metadata.json`（各 19–20 行） |
| 4 | `scripts/fetch-mobile-libs.sh`（消费端） | ✅ 有 | 218 行；校验 `manifest.goxVersion` + 逐产物 sha256；有 `--check-only` |
| 5 | `scripts/check-registries.py` 检查4/检查5 | ✅ 有 | 721 行；检查4 `:523` 卡「主包不含 `mobile/`」；检查5 `:534` 卡子包命名/版本/清单/sha256 |
| 6 | `docs/mobile-distribution-decision.md` | 🟡 半成品 | 131 行；三项决策都有**建议**，但①未正面回答「进 npm 是否要带整套壳工程源码」②未把「两者并存」作为定稿选项③缺第 4 层机制；且把成品 App 两档留空。本轮修订 |
| 7 | `docs/mobile-adaptation.md` | ✅ 有（**但同名不同事**） | 191 行，是 **T23 移动端适配规范**（触控/安全区/键盘），与分发无关 |
| 8 | `docs/npm-compat.md` | ✅ 有（**同名不同事**） | 187 行，M5 的 npm 兼容面（语言/Node API/native addon），不是分发 |
| 9 | `docs/npm-release.md` | 🟡 半成品 | 158 行；§1/§5 验收口径仍是「5 平台二进制」，**未含移动端子包**。本轮补 |
| 10 | `docs/desktop-distribution.md` | 🟡 半成品 | 76 行，纯桌面；**无移动端对照**，用户看不出两端获取方式的差异。本轮补 |
| 11 | `.github/workflows/release.yml` | ✅ 有（覆盖 npm 侧） | 545 行；`mobile-ios` 作业（`:518`）产 iOS 子包，`publish` 编 android 子包（`:228`）并逐子包 `npm publish`（`:378`） |
| 12 | `.github/workflows/mobile-smoke.yml` | ✅ 有（但**只产出 CI artifact**） | 310 行；android 出 `app-debug.apk` 归档（`:97`）、iOS 出模拟器 `.app` zip（`:174`）、`npm-package` 作业核对包内容（`:199`）。**都不挂 Release** |
| 13 | `docs/release-post--checklist.md` | ✅ 有 | 81 行；§2（`:28`）**已写明**「v0.7.0 及更早 0 assets、v0.8.0 起才挂产物」 |
| 14 | 未合入分支 `wt/npm-mobile` 的 5 个提交 | ✅ **已全合** | `git -C D:/code/Gox cherry -v Gox/main wt/npm-mobile` 六条全为 `-`（= main 已有等价提交）；对应文件 1/2/3/5/6 均在 `main` 树里（`git ls-tree -r --name-only Gox/main \| grep npm-mobile` 有输出）。**不要重复挑** |
| 15 | 成品 App（APK/HAP/壳工程）的**公开渠道** | ❌ **没有** | 无 `mobile-release.yml`；`release.yml` 只发 npm 子包，`mobile-smoke.yml` 只产 CI artifact。**这是本轮真正补的那块** |

**结论**：r8DoFS / M11 的「npm 移动端产物」这条路**基本已通**（1–5、11–12 齐备），缺的是
**父任务 rgnRC4 的另一半**：成品 App 的公开渠道（15）+ 决策文档定稿（6）+ 消费端口径（9、10）。

---

## 1. 决策一：产物形态 —— 建议「库进 npm 子包 + 成品示例 App 进 GitHub Releases；**壳工程源码不进 npm**」

先把问题摆正：移动端产物**不是一个可执行文件**，而是

```
libgox.so / libgox.a（引擎）  +  壳工程（Kotlin / Swift / ArkTS 源码）
                                   └─ 把引擎装进 APK / HAP / .app 的那一层
```

所以「移动端进 npm」的自然反面是：**发布包里要不要带整套壳工程源码？**

### 1.1 推荐方案（建议 C）

| 产物 | 形态 | 去处 | 面向谁 |
|---|---|---|---|
| **引擎库** | `libgox.so` / `libgox.a` + `libgox.h` + `manifest.json` | **npm 平台子包**（决策二） | 移动端开发者（要集成进自己的 App） |
| **壳工程** | `app/{android,harmony,ios}` 源码骨架 + `gox create` 生成骨架 | **Gox 仓库 + `gox create`**，**不进 npm**、不单独发制品 | 移动端开发者 |
| **成品示例 App** | 打包好的 APK / HAP / iOS 壳工程 zip | **GitHub Releases：nightly + release 两档**（决策二） | 只想「装上看看效果」的试用者 |

**理由**：

1. **壳工程源码是「模板」不是「产物」**。它要跟着仓库分支演进、要 review、要跟
   `gfx/android/libgox/main.go` 的 JNI 符号名（`Java_com_gox_GoxRuntime_*`，见
   `app/android/app/build.gradle.kts:17-19` 注释）逐字符对齐 —— 塞进 npm tarball 等于把
   一份**会立刻过期**的源码快照发出去，用户拿到的是死版本，反而更难用。
2. **体积/维护成本落在最不需要它的人身上**。壳工程源码 + Gradle wrapper + XcodeGen 工程有
   一堆小文件；而 npm 子包的价值恰恰是「一条 `npm i` 精准拿到一个平台的库」。混在一起会让
   「拿库」这件事变重。
3. **与现有分发面一致**：桌面侧 M5 也只发二进制、不发源码；壳工程源码走仓库与脚手架，是同一套思路。

### 1.2 代价（诚实记一笔，别只写优点）

- 移动端用户**做不到「一条命令就能跑」**：他必须自备工具链（Android 要 NDK+JDK、iOS 要 Xcode、
  鸿蒙要 DevEco），再从 `gox create` 铺壳工程 —— 这与桌面 `npm i -g @goxjs/goxjs` 直接跑差一截。
  这是**移动端平台的固有成本**（App 必须被签名、被装进宿主工程），不是本决策引入的；但必须写进
  用户可见文档（§5），否则会被当成「文档不说清」的坑。
- 成品 App 走 Releases ⇒ 需要**额外一条发布作业**（本轮新增，工作量已付）。
- 壳工程不进 npm ⇒ 用户手上的壳工程版本与引擎版本的对应关系**只靠 §3 的声明文件机制保障**，
  没有 registry 的版本判重兜底。

### 1.3 反面选项

| 方案 | 优点 | 缺点 | 判定 |
|---|---|---|---|
| A. 主包直塞 `mobile/`（库+壳工程一起进 `@goxjs/goxjs`） | 一处集中 | 桌面用户被迫从 ~20MB 顶到 ~72MB；**违反 M5**；已被 `check-registries.py:523` 卡死 | ❌ |
| B. 子包带整套壳工程源码 | 用户「开箱即得」一个可编译工程 | tarball 臃肿；源码快照**拿到即过期**（模板随仓库演进）；与 `gox create` 骨架形成两份真源 | ❌ |
| C. 只发库（子包）+ 壳工程走仓库/脚手架 + 成品 App 走 Releases | 分流清晰、复用现有设施、主包不变 | 移动端用户要自备工具链、自己铺壳工程 | ✅ **建议** |

---

## 2. 决策二：分发渠道 —— 建议「**两者并存**」：npm 平台子包（开发者）+ GitHub Releases 两档（试用者）

### 2.1 推荐方案

| 渠道 | 内容 | 面向 | 现状 |
|---|---|---|---|
| **npm 平台子包** `@goxjs/goxjs-mobile-<platform>-<abi>` | 单个平台的预编译库（+ `manifest.json`） | 移动端开发者 | ✅ **已落地**（`release.yml` `:378` 逐子包发布） |
| **GitHub Releases · release 档** | 成品 APK / HAP / iOS 壳工程 zip，挂到 `v<x.y.z>` | 试用者 | ✅ **本轮新增**（`mobile-release.yml`，`release: published` 触发） |
| **GitHub Releases · nightly 档** | 同上，挂到 `nightly-<yyyy-mm-dd>`（prerelease） | 每日尝鲜、发布前验证 | ✅ **本轮新增**（`mobile-release.yml`，每日 cron） |

四个子包（`packaging/npm-mobile/` 为单一真源）：

| 子包 | 产物 |
|---|---|
| `@goxjs/goxjs-mobile-android-arm64-v8a` | `libgox.so` |
| `@goxjs/goxjs-mobile-harmony-arm64-v8a` | `libgox.so` |
| `@goxjs/goxjs-mobile-ios-iphoneos-arm64` | `libgox.a` + `libgox.h` |
| `@goxjs/goxjs-mobile-ios-iphonesimulator-arm64` | `libgox.a` + `libgox.h` |

**理由**：

1. **两类人不是一类人**。要集成进自己 App 的开发者需要「库 + registry 版本判重 + OIDC provenance」；
   想「装上看看」的试用者需要「一个能直接装的包」。**同一个渠道满足不了两拨人**。
2. **两条渠道各自复用已有设施，零新增基础设施**：npm 侧复用 `release.yml` 的 OIDC 发布链；
   Releases 侧本就是桌面二进制已有的托管处（`release.yml` 的 `universal` 作业已在往 Release 挂 assets）。
3. **与 M5 不冲突**（§0.1）。

### 2.2 为什么**不**用 `optionalDependencies` 自动装

**npm 默认就会安装 `optionalDependencies`**（只有 `--no-optional` / `--omit=optional` 才跳过）。
写进主包 = 每个桌面用户照样被塞 20–30MB，**与「主包直塞」代价完全一样，等于没拆**。

esbuild 的 `@esbuild/*` 之所以成立，前提是**每个用户都需要它**（每个平台都要跑 esbuild），
只是平台不同；移动端库不是 —— 桌面用户不是「装了用不上」，而是「**根本不该装**」。
所以子包不进任何 `dependencies` / `optionalDependencies` / `peerDependencies`，由消费者显式安装
（详见 `app/MOBILE-DISTRIBUTION.md` §2 与 `packaging/npm-mobile/README.md`）。

### 2.3 代价

- **两处对外说明要保版本号一致**：Release 说明、npm 包页、官网三处的口径现在还要多一行
  「移动端这次能拿到什么」——落在 `docs/release-post--checklist.md`（见 §5）。
- nightly 档会**每天产生一个 prerelease** ⇒ Release 列表会变长（可接受；必要时后续加「只保留最近 N 个」的清理）。
- 成品 App 目前只能覆盖 **android + ios**；**harmony 恒缺**（§4）。

### 2.4 反面选项

| 方案 | 优点 | 缺点 | 判定 |
|---|---|---|---|
| A. 只发 npm 子包，不发成品 App | 实现最省 | 非开发者没有体验入口；移动端口碑缺示范 | ⚠️ 可作 interim（当前事实状态） |
| B. 只发 Releases，不做 npm 子包 | 渠道单一 | 离开 npm 生态（无版本判重 / 无 provenance）；「用户装的是哪个引擎版本」说不清 | ❌ |
| C. **两者并存**（子包=开发者、Releases 两档=试用者） | 分流清晰、复用现有设施 | 需对齐两处口径 | ✅ **建议** |

---

## 3. 决策三：版本兼容矩阵机制 —— 建议「壳工程声明文件 + 构建时校验 + CI 闸门」，四层防错配

**要防的是什么**：壳工程版本 ↔ 引擎版本错配（新壳配旧引擎，或反之）是 **JNI / NAPI 契约不匹配**，
**编译期拦不住、只在运行时崩**（症状：`UnsatisfiedLinkError`、符号找不到、方法签名对不上）。
这类问题一旦发出去，用户现场根本排不出来，所以必须机制化防。

### 3.1 推荐机制

壳工程里**写死**它面向哪一代引擎，用一个声明文件：

```
app/<platform>/gox-engine.json
{"engineVersionRange": "0.9.x"}      # 绑定到某一代（只比 major.minor）
{"engineVersion": "0.9.0"}           # 或精确绑定
```

三层既有机制 + 本轮新增的第四层：

| 层 | 机制 | 落在哪 | 状态 |
|---|---|---|---|
| 1 发布约定 | 子包 `version` 恒等于主包 `version`（同号） | `check-registries.py` 检查5 | ✅ 已落地 |
| 2 产出端 | 每子包生成 `manifest.json`：`goxVersion` + 每产物 `kind/sha256/size` | `scripts/build-npm-mobile.sh` | ✅ 已落地 |
| 3 消费端 | 取库前校验 `manifest.goxVersion == 引擎版本` + 逐产物 sha256，不符拒绝拷贝 | `scripts/fetch-mobile-libs.sh`（含 `--check-only`） | ✅ 已落地 |
| **4 成对发布闸门** | CI 断言「引擎版本 ↔ 子包定义 ↔ **壳工程版本声明**」三者一致 | `scripts/check-shell-engine-version.py` + `mobile-release.yml` 的 `gate` 作业 | 🟡 **闸门已就位，声明文件未落地**（见 §3.3） |

闸门的具体判据（`scripts/check-shell-engine-version.py`，135 行）：

- 引擎版本真源 = `cmd/gox/main.go` 的 `const version`（与 `check-registries.py` 检查3 同一处）；
- 声明**存在且相符** → 通过；
- 声明**存在但不符** → **退出码 1（硬闸门）** —— 这是发布契约被破坏，必须挡住；
- 声明**缺失** → 打印带证据的 `::error::`（列出 `app/<platform>/` 实际文件清单）+ **退出码 0**。

> 「缺失时不判红」是刻意的：机制还没在三处落地，此时判红只会让人把闸门关掉。所以先
> **响而不红**，并用 `--require` 提供了「机制落地后升级为硬性要求」的开关。

发布链条上的两个落点：

- `mobile-release.yml` 的 `gate` 作业（成品 App 挂 Release 之前）→ 跑上面这个脚本 + 
  `check-registries.py` + `gen-npm-mobile-pkgs.py --check`；
- `release.yml` 的 `publish` 作业（npm 子包发布之前，`:176`）→ 跑 `check-registries.py`。

两档（nightly / release）共用同一份版本真源，`mobile-release.yml` 的 `version` 作业里
对上 release 档还会断言「Release tag 去掉 `v` 后 == 引擎版本」。

### 3.2 兼容矩阵（表格化，发布时自动生成，不手写）

| 引擎版本 | 壳工程 | Android | HarmonyOS | iOS(真机) | iOS(模拟器) | 说明 |
|---|---|---|---|---|---|---|
| `0.9.x` | `app/*` 同 commit | arm64-v8a | arm64-v8a | arm64 | arm64 | 首个公开版；子包与主包同号 |
| `nightly-<date>` | 同 commit | ✅ | ⚠️ 需自建 runner | ✅ | ✅ | nightly 档；可能随时不兼容 |

矩阵维护方式：每行由发布时**从声明文件与 `manifest.json` 自动取数**，不手写，避免漂移。

### 3.3 现状与待办（重要）

**三个壳工程目前都没有 `gox-engine.json`** —— 所以第 4 层**当前形同虚设**。
`mobile-release.yml` 的 `gate` 作业在 CI 上会为每个平台打一条 `::error::`（带 `ls` 证据），
但**不会判红**。要把第 4 层真正激活，需要在 `app/{android,harmony,ios}/` 各加一个
`gox-engine.json`（内容按 §3.1）—— **该改动不在本工作流的文件区**（本轮只允许改
`.github/workflows/`、`scripts/`、`docs/`、`packaging/`），已列 §6 待办。

本地实测（`scripts/check-shell-engine-version.py`）：

```
引擎版本（真源 cmd/gox/main.go）：0.9.0
::error::壳工程 android 没有版本声明文件 app/android/gox-engine.json —— …
::error::壳工程 harmony 没有版本声明文件 app/harmony/gox-engine.json —— …
::error::壳工程 ios 没有版本声明文件 app/ios/gox-engine.json —— …
::error::壳工程 android/harmony/ios 尚无 gox-engine.json，第 4 层闸门对其未生效（待办，不是失败）
exit=0
```

负向自测（临时目录造一个 `{"engineVersion":"0.8.3"}` 声明）→ `exit=1`，并按设计报出不符原因。

### 3.4 反面选项

| 方案 | 防错配强度 | 成本 | 判定 |
|---|---|---|---|
| semver 范围匹配（只在 npm 侧写 `^0.9.0`） | 弱：范围对但契约变仍 runtime 崩 | 低 | ❌ |
| 桌面式 auto-update ignore 表 | 不适用：移动端不随引擎自更新（App 由用户自己签名分发） | — | ❌ |
| 只在文档里写「壳工程要跟引擎同版本」 | 无强制力，必然漂移 | 0 | ❌ |
| **声明文件 + 同号 + manifest 逐产物 sha256 + 发布前 CI 闸门** | 强：校验到字节 + 发布即卡口 | 中 | ✅ **建议** |

---

## 4. 产出矩阵与外部前提（谁能产出哪些，真缺什么）

| 平台 | 渠道产物 | 工具链 | 本机（Windows） | 托管 CI | 当前真实状态 |
|---|---|---|---|---|---|
| android | 子包 `libgox.so` + **APK** | Android NDK（c-shared）+ JDK17 + Gradle 8.9 | ✅ 可 | ✅ `ubuntu-latest`（镜像自带 NDK） | 库：`release.yml` 已发；APK：`mobile-release.yml` 可编，但**只有 debug 签名** |
| harmony | 子包 `libgox.so` + **HAP** | DevEco SDK（OHOS clang + musl sysroot）+ DevEco 自带 node | ✅ 可（`app/harmony/README.md` 实测 `assembleHap` BUILD SUCCESSFUL） | ❌ **编不了** | **恒缺**：`mobile-release.yml` 的 harmony 作业会探测 SDK，探不到就优雅跳过 |
| ios | 子包 `libgox.a` + **壳工程 zip** | macOS + Xcode + XcodeGen | ❌ 做不到 | ✅ `macos-latest` | 库：`release.yml` 的 `mobile-ios` 作业已产；壳工程 zip：`mobile-release.yml` 可产（**不需要签名材料**） |

### 4.1 当前**真缺**的东西（不谎报）

| 缺失项 | 影响 | 现状处置 |
|---|---|---|
| **Android 签名材料**（`app/android/app/keystore.properties`：storeFile/storePassword/keyAlias/keyPassword） | `assembleRelease` 出的 APK **不签名**（`app/android/app/build.gradle.kts:8-9` 明说「没有它 release 不签名」）⇒ 产出的 APK 只能 debug 签名，**仅供试用、不可上架** | `mobile-release.yml` 检测到缺失 → 打 `::error::` 证据 + 降级出 debug APK（诚实标注） |
| **DevEco SDK**（需华为账号/许可，体积大） | **HAP 完全产不出** | 探测不到 → `::error::` 证据 + 优雅跳过；要真出 HAP 必须换成一台装了 DevEco 的**自建 runner**（改 `runs-on` + 设 `DEVECO_SDK_HOME`/`NODE_HOME`） |
| **macOS 签名证书 / 公证** | iOS **真机** `.app` / TestFlight 发不了 | 本作业不碰：只发**壳工程 zip**（source + XcodeGen 生成的 `.xcodeproj`），用户在本地自行签名 |
| **4 个 npm 子包的 Trusted Publishing 配置** | 首次发布 404 | 需人工在 npmjs.com 为每个子包配（org=`14752222`、repo=`Gox`、workflow=`release.yml`） |

### 4.2 诚实声明

本轮**没有真实产出过任何 APK / HAP / 壳工程 zip**：
新增的 `mobile-release.yml` 只在 `schedule` / `release: published` / `workflow_dispatch` 触发，
本工作流**不 push、不真发版**（既无凭证、也按纪律不触发），因此它**尚未被真实运行验证过**。
它的可验证部分是：YAML 语法（已用 PyYAML 解析通过，6 个作业键齐全）、跳过/校验分支的证据纪律、
以及 `scripts/check-shell-engine-version.py` 的正负向本地自测（§3.3）。

---

## 5. 用户可见渠道口径（差异 + 各渠道「实际能拿到什么」+ 历史分界）

> 本节的口径已分别落到已有文档，**不新建过程文档**：
> `docs/desktop-distribution.md`（桌面 vs 移动对照）、`docs/npm-release.md`（发版手册补移动端子包）、
> `docs/release-post--checklist.md`（三平台口径补移动端一行）。

### 5.1 移动端 vs 桌面端：获取方式差在哪

| | 桌面端 | 移动端 |
|---|---|---|
| 拿到什么 | 一个 `gox` 可执行文件 | 预编译库（`libgox.so`/`.a`）+ **要自己铺一层壳工程** |
| 从哪拿 | `npm i -g @goxjs/goxjs` | `npm i @goxjs/goxjs-mobile-<platform>-<abi>`（按需；子包**不在**主包依赖里） |
| 怎么用 | 直接 `gox` / `goxjs` 跑 | `scripts/fetch-mobile-libs.sh` 把库落进壳工程 → `gradle` / `hvigor` / `xcodebuild` |
| 本机要不要工具链 | **不要**（二进制自足、零动态依赖） | **要**：Android 要 NDK+JDK、鸿蒙要 DevEco、iOS 要 Xcode —— 但**不需要 Go** |
| 想「装上看看」 | `npm i -g` 即可，或 Release 下 universal 二进制 | **去 GitHub Releases 下成品 APK / 壳工程 zip**（nightly 或 release 档） |
| 版本对齐 | 包版本 = 引擎版本即可 | 子包与主包同号；`manifest.goxVersion` 消费端校验；壳工程还要过 §3 第 4 层闸门 |

差异的根因：桌面是**可执行文件**（跑起来就完事），移动端是**要被宿主 App 链接的库**
（还差一层壳工程把它装进 APK / HAP / App）。

### 5.2 各渠道**当前实际**能拿到什么（哪些真实可用、哪些只是占位）

| 渠道 | 真实可用的 | 占位 / 尚未可用 |
|---|---|---|
| npm `@goxjs/goxjs`（主包，M5 面） | 桌面五平台二进制 | — |
| npm `@goxjs/goxjs-mobile-android-arm64-v8a` | 定义与发布链就绪 | **能否拿到取决于是否已配好该子包的 Trusted Publishing**（未配则首次发布 404）；实际是否已上架以 `npm view` 为准 |
| npm `@goxjs/goxjs-mobile-ios-*`（2 个） | 同上（`release.yml` 的 `mobile-ios` 产出） | 同上 |
| npm `@goxjs/goxjs-mobile-harmony-arm64-v8a` | 定义已就绪、**本机可产** | **CI 产不出**（托管 runner 没 DevEco）⇒ 该子包目前**只能本地/自建 runner 手工发** |
| GitHub Releases | 桌面五平台二进制 + macOS universal（`release.yml` 的 `universal` 作业） | **移动端成品 APK / HAP / 壳工程 zip：工作流已就位但从未运行过**（§4.2）；harmony HAP 恒缺签名与 SDK |
| `gox create` 脚手架 | android / ios 骨架 | **缺 harmony 骨架**（`app/MOBILE-DISTRIBUTION.md` §7 已记录，建议另立单） |

### 5.3 历史分界（务必写进 Release 说明 / 包页 / 官网）

- **v0.7.0 及更早的 Release 是 0 assets**（空壳，只有 tag 和一段文字）——
  「自动打 tag + 建 Release + 内容校验」是后来才补进 `release.yml` 的。
- **v0.8.0 起才真正挂产物**（5 平台二进制 + OIDC attestation）。
- 所以回溯老版本的用户看到 v0.7.0 及更早「没有下载」**不是事故**：Release 说明模板里带一句
  「v0.8.0 以前的 Release 不含二进制产物，请使用 v0.8.0+」。
- 移动端成品 App 若启用 `mobile-release.yml`，其**首个带移动端产物的 Release** 会在启用之后
  才出现 —— 之前的 Release 只有桌面产物，同样不是事故。这条口径需在启用时补进
  `docs/release-post--checklist.md` §2。

---

## 6. 待用户拍板清单（本单**真正的出口条件**）

> 每条给「要决策什么 / 选项 / 推荐 / 不决策会卡住什么」。**本文不替用户拍板。**

### 6.1 产物形态：壳工程源码要不要进 npm 子包？

- **选项**：① 只发预编译库（推荐）② 子包额外带壳工程源码 ③ 库+壳工程一起塞主包
- **推荐**：①
- **代价**：移动端用户必须自备工具链、自己 `gox create` 铺壳工程（§1.2）
- **不决策会卡住**：子包 `files` 清单与 `metadata.json` 的 `artifacts` 白名单无法定稿，
  `build-npm-mobile.sh` / `check-registries.py` 检查5 的判据也就悬着；后续想加壳工程要改发布面 + 重发子包。

### 6.2 分发渠道：npm 子包 / Releases / 两者并存？

- **选项**：① 两者并存（推荐）② 只发 npm 子包 ③ 只发 Releases
- **推荐**：①（npm 子包已落地，本轮补上 Releases 两档）
- **代价**：两处对外说明要保持版本号口径一致（§2.3）
- **不决策会卡住**：新增的 `mobile-release.yml` 无法定稿其存在合理性；也会影响「谁负责 nightly」的归属。

### 6.3 Releases 两档：nightly 要不要？以什么形态？

- **选项**：① 每日 cron + `nightly-<date>` prerelease（推荐）② 只在 release 档出移动端产物 ③ nightly 走 npm dist-tag（`@next`）
- **推荐**：①（子包发布仍只走 release 档，nightly 不污染 `@next`）
- **代价**：Release 列表每天 +1（§2.3）
- **不决策会卡住**：`mobile-release.yml` 的 `on.schedule` 是否保留；prerelease 命名与清理策略。
  > 现状：工作流**已按 ① 写好**，若选 ②/③ 需改 3 处（`on:`、`version` 作业、`publish` 的 nightly 分支）。

### 6.4 版本兼容矩阵：第 4 层机制要不要落到三个壳工程里？

- **选项**：① 在 `app/{android,harmony,ios}/` 各加 `gox-engine.json` 并让闸门硬起来（推荐）
  ② 保持现状「响而不红」③ 不要第 4 层，只靠同号 + manifest 校验
- **推荐**：①（`--require` 已备好，落地后一行就能升级为硬性要求）
- **代价**：三个壳工程各加一个文件；机制未落地前，壳工程↔引擎错配仍是「运行时才崩」
- **不决策会卡住**：`scripts/check-shell-engine-version.py` 只能停在「证据式跳过」，
  §3 的兼容矩阵无法自动取数。

### 6.5 移动端对外渠道：谁去配 4 个 npm 子包的 Trusted Publishing？谁提供签名/SDK？

- **选项**：① 人工到 npmjs.com 为 4 个子包各配一次 Trusted Publishing（必须，否则首次 404）
  ② 提供 Android 签名材料（keystore.properties 或以 secret 注入）→ 出可上架 APK
  ③ 提供自建 runner + DevEco SDK → 出 HAP
- **推荐**：① 必须做；②③ 按「是否真的要把移动端对外」决定
- **代价**：①②的凭证由用户持有，本工作流**不可能**代持
- **不决策会卡住**：`mobile-release.yml` 永远只能出 debug APK、永远跳过 HAP；
  npm 子包首次发布必然 404。**这是「成品 App 公开渠道」能否真正交付的硬前提。**

### 6.6 `app/MOBILE-DISTRIBUTION.md` 与本文的关系

- **选项**：① 以本文为准，`app/` 那份瘦身为「指向本文 + 保留落地记录」（推荐）② 两份并存互为镜像
- **推荐**：①（避免两份决策文档长期漂移）
- **代价**：一次文档合并（`app/` 不在本工作流文件区，需另安排）
- **不决策会卡住**：后续再改决策时不知道该改哪一份，「改一处忘一处」必然发生。

### 6.7 `docs/npm-release.md` 与 `docs/desktop-distribution.md` 的口径修订是否认可？

- **选项**：① 认可本轮修订（推荐）② 换更合适的位置
- **推荐**：①
- **不决策会卡住**：发版手册的验收口径还是「5 平台二进制」，会把移动端子包当成「不在验收范围内」。

---

## 7. 本轮改动清单（含文件路径，供 review）

| 文件 | 动作 | 要点 |
|---|---|---|
| `.github/workflows/mobile-release.yml` | **新增**（501 行） | nightly（cron）+ release（`release: published`）两档；产物 = Android APK / HarmonyOS HAP / iOS 壳工程 zip；无 NDK / 无签名材料 / 无 DevEco 一律：`echo "::error::<带证据>"` + 优雅跳过；一件产物都没有时不建空 Release；**不触发 push main**（不真发版） |
| `scripts/check-shell-engine-version.py` | **新增**（135 行） | 决策三第 4 层的闸门本体：读 `cmd/gox/main.go` 的 `const version`，比对 `app/<platform>/gox-engine.json`；声明不符 → 退出 1；声明缺失 → `::error::` 证据 + 退出 0（`--require` 可升级） |
| `docs/mobile-distribution-decision.md` | **重写** | 三项决策（推荐 + 理由 + 代价 + 反面选项）、与 M5 不冲突的逐条判定、现状审计表、产出矩阵与真实缺口、用户可见口径、7 条拍板项 |
| `docs/npm-release.md` | 修订 | 补移动端子包小节（验收口径从「5 平台二进制」扩到含 4 个子包）+ 指向 `mobile-release.yml` |
| `docs/desktop-distribution.md` | 修订 | 补「移动端不适用本指南，另见」的对照与交叉链接 |
| `docs/release-post--checklist.md` | 修订 | 三平台口径表补「移动端渠道」一行 + 移动端产物首次出现的分界说明 |

## 8. 未覆盖边界与已知问题（逐条）

1. **第 4 层闸门未激活**：三个壳工程缺 `gox-engine.json`（§3.3），闸门当前只响不红。
2. **`mobile-release.yml` 从未真实运行**：本轮不 push、不触发；YAML 与脚本分支仅本地验证（§4.2）。
3. **harmony 全链路缺口**：CI 产不出 HAP，也产不出 harmony 子包（`release.yml` 明确不含）；
   `gox create` 脚手架还缺 harmony 骨架 —— 三者都需要「自建 runner + DevEco」或另立单。
4. **Android 只有 debug 签名**：无 keystore.properties ⇒ 不可上架（§4.1）。
5. **workspace 内的先天盲区**：`npm/` 是 git submodule，在本 worktree 里是空目录 ⇒
   `gen-npm-mobile-pkgs.py --check` / `check-registries.py` **本地跑会因缺 `npm/package.json` 报
   `FileNotFoundError`**。这是 worktree 事实，不是脚本缺陷（CI `submodules: true` 下正常）。
   本轮**没有**、也不应去初始化或改动 `npm/`。
6. **本机无法验证 Android/iOS 构建**：无 NDK / 无 Xcode（Windows 主机）⇒ APK 与壳工程 zip 的
   构建链路只能靠 CI 或真机验证；本轮未做。
7. **nightly prerelease 无清理策略**：长期会累积。需要时另立「保留最近 N 个」的单。
8. **iOS 壳工程 zip 的可用性**：它包含 `project.yml` 与生成的 `Gox.xcodeproj`，但**不含 `libgox.a`** ——
   用户仍需 `npm i @goxjs/goxjs-mobile-ios-*` + `fetch-mobile-libs.sh` 才能编译。这一点必须在
   §5 的对外口径里说清（否则会被当成「zip 不完整」）。

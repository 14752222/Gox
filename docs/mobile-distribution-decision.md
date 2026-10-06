# 移动端分发策略决策文档（rgnRC4 / r8DoFS）—— 建议方案，⚠️ 待用户拍板

> **本文只出建议，不做决定。** 每项决策附备选对比；拍板点集中在 §6。
> 已落地现状（M11，基线 f1a46fb）：npm 平台子包定义上移主仓 `packaging/npm-mobile/`（单一真源）+
> `scripts/gen-npm-mobile-pkgs.py --check` CI 断言 + `app/MOBILE-DISTRIBUTION.md` 决策记录 +
> gox-npm 子模块指针 `6ee9f9e`。本文把决策记录正式挪进 `docs/`（M11 当时因工作流文件区限制落在 `app/`），
> 并补齐父任务 rgnRC4 的第三条（成品 APK/HAP 公开渠道）。

## 0. 与 M5 结论的关系（先对齐，再决策）

M5 已定稿：**`@goxjs/goxjs` = 桌面二进制分发面**（主包只装桌面可执行，~20MB）。
本文件三项决策全部以「**不破坏 M5**」为硬约束——已由 `scripts/check-registries.py` 检查4 双向卡死
（主包 `files` 出现 `mobile/` 直接报错，防回归）。

---

## 1. 决策一：产物形态 —— 建议「预编译库进 npm + 成品示例 App 进 GitHub Releases 两档」

移动端产物不是可执行文件，而是**要被壳工程链接的库**。建议拆分两种产物、两个去处：

| 产物 | 形态 | 去处 | 面向谁 |
|---|---|---|---|
| 引擎库 | `libgox.so` / `libgox.a` + `libgox.h` + `manifest.json` | npm 平台子包（决策二） | 移动端开发者（要集成进自己的 App） |
| 成品示例 App | 打包好的 APK / HAP（演示壳工程 + 预置 JS 脚本） | **GitHub Releases：nightly + release 两档** | 只想「装上看看效果」的普通用户、试用者 |

**理由**：
1. 库走 npm 是开发者天然路径（`npm i` 一条命令、registry 版本判重、OIDC provenance 全复用）；
2. 成品 APK/HAP 塞进 npm 既没有生态位（npm 用户不用包装 App）又撑大元数据，而 GitHub Releases 本就是桌面二进制已有的托管处，**零新增基础设施**；
3. 两类用户分流：找库的走 registry，试用的走 Releases，互不打扰。

**备选对比**：

| 方案 | 优点 | 缺点 | 判定 |
|---|---|---|---|
| A. 全塞 npm 主包 | 一处集中 | 桌面用户被迫从 ~20MB 顶到 ~72MB；违反 M5 | ❌ |
| B. 只发库，不发成品 App | 实现最省 | 非开发者没有体验入口，移动端口碑缺示范 | ⚠️ 可作 interim |
| C. 库进 npm + 成品 App 进 Releases 两档 | 分流清晰、复用现有设施 | 需加一条 nightly 发布作业（工作量小） | ✅ 建议 |

---

## 2. 决策二：分发渠道 —— 建议「独立 npm 平台子包 `@goxjs/goxjs-mobile-<platform>-<abi>` + GitHub Releases 两档」

### 2.1 npm 侧：平台子包（谁用谁装）

子包清单（M11 已定义，`packaging/npm-mobile/` 为单一真源）：

| 子包 | 产物 |
|---|---|
| `@goxjs/goxjs-mobile-android-arm64-v8a` | `libgox.so` |
| `@goxjs/goxjs-mobile-harmony-arm64-v8a` | `libgox.so` |
| `@goxjs/goxjs-mobile-ios-iphoneos-arm64` | `libgox.a` + `libgox.h` |
| `@goxjs/goxjs-mobile-ios-iphonesimulator-arm64` | `libgox.a` + `libgox.h` |

三选项代价对比：

| 选项 | 桌面用户代价 | 移动端用户代价 | 版本对齐 | 判定 |
|---|---|---|---|---|
| A. 主包直塞 `mobile/` | ~20MB → ~72MB | 开箱可用 | 天然同号 | ❌ |
| B. 纯 GitHub Releases，无 npm 子包 | 0 | 离开 npm、手动下载校验、说不清装的哪个引擎版本 | 靠人 | ❌ |
| **C. 平台子包 + Releases（建议）** | **0**（主包 20MB 不变） | `npm i @goxjs/goxjs-mobile-android-arm64-v8a` 一条命令 | 子包与主包同号 + manifest 校验 | ✅ |

**为何不选 `optionalDependencies` 自动装**：npm **默认就会装** `optionalDependencies`，
写进主包 = 每个桌面用户照样被塞 20–30MB，等于没拆（esbuild 式「按平台跳过」成立的前提是
*每个平台都需要它*；移动端库是桌面用户**根本不该装**）。详见 `app/MOBILE-DISTRIBUTION.md` §2。

### 2.2 GitHub Releases 侧：nightly + release 两档

| 档位 | 触发 | tag 形态 | 内容 | 用途 |
|---|---|---|---|---|
| release（正式） | 推送 `v<x.y.z>` tag（现有 release.yml 已实现） | `v0.9.0` | 桌面五平台二进制 + npm 子包 + 成品 APK/HAP | 对外版本 |
| **nightly（建议新增）** | push 到 main | `nightly-<yyyy-mm-dd>` 或日期 suffix prerelease | 同上产物，标 `prerelease` | 每日尝鲜、发布前验证 |

成品 APK/HAP 的构建产物来自 `app/{android,harmony,ios}` 壳工程 + `scripts/build-{android,harmony,ios}.sh`
（build-npm-mobile.sh 产出库之后接壳打包）。两档共用同一份版本真源（见决策三），只是 tag 前缀不同。

**备选**：nightly 走 npm `dist-tag`（`@next`）——仅适用 npm 可分发的产物，APK/HAP 本身进不了 npm，两档还得落 Releases，
不如一开始就两档都在 Releases。**建议 Releases 双档，npm 侧只发 release 档**（nightly 的 npm 发布留给后续需要时再加 dist-tag）。

---

## 3. 决策三：版本兼容矩阵 —— 建议「表格化矩阵 + CI 断言成对发布」

壳工程 ↔ 引擎版本错配是 **JNI/NAPI 契约不匹配、运行时才崩**，编译期拦不住，必须机制化防。

### 3.1 兼容矩阵（表格化，落 `docs/` 或 `app/MOBILE-DISTRIBUTION.md`）

| 引擎版本 | 壳工程版本 | Android | HarmonyOS | iOS(真机) | iOS(模拟器) | 说明 |
|---|---|---|---|---|---|---|
| 0.9.x | app/* 同 commit | arm64-v8a | arm64-v8a | arm64 | arm64 | 首个公开版；子包与主包同号 |
| nightly-<date> | 同 commit | ✅ | ✅ | ✅ | ✅ | nightly 档；可能随时不兼容 |

矩阵维护方式：每行由发布时**自动生成**（版本真源单一），不手写，避免漂移。

### 3.2 三层防错配机制（M11 已落地两层，本文建议补第三层）

| 层 | 机制 | 状态 |
|---|---|---|
| 发布约定 | 子包 `version` 恒等于主包 `version`（生成器取 `npm/package.json`，不含在 metadata） | ✅ `check-registries.py` 检查5 |
| 产出端 | 每子包生成 `manifest.json`：`goxVersion` + 每产物 `kind/sha256/size` | ✅ `build-npm-mobile.sh` |
| 消费端 | 取库前校验 `manifest.goxVersion == 引擎版本` + 逐产物 sha256，不匹配拒绝拷贝 | ✅ `scripts/fetch-mobile-libs.sh`（含 `--check-only` CI 模式） |
| **成对发布断言（建议新增）** | CI 断言「子包定义 ↔ 生成物 ↔ 壳工程版本引用」三者一致才允许发布 | 🟡 `gen-npm-mobile-pkgs.py --check` 已卡前两者；建议再加一步断言各壳工程（`app/*/`）里的引擎版本引用与 `cmd/gox/main.go` 的 `const version` 一致 |

**CI 断言成对发布具体设计（建议）**：在 `.github/workflows/release.yml` 的 publish 作业前置一步
`version-consistency`：读 `cmd/gox/main.go` 的 `const version` 为唯一真源，断言 ① tag 一致（已实现）、
② 子包同号（已实现）、③ 壳工程 manifest/构建脚本引用的引擎版本一致（新增），④ nightly/release 两档
`GOX_VERSION` 环境变量同源下发。任何一环不齐 → 作业红 → 不发布，从机制上杜绝「发了 A 忘了 B」。

**备选对比**：

| 方案 | 防错配强度 | 成本 | 判定 |
|---|---|---|---|
| semver 范围匹配（`^0.9.0`） | 弱：范围对但契约变仍 runtime 崩 | 低 | ❌ |
| 桌面式 auto-update ignore 表 | 不适用：移动端不随引擎自更新 | — | ❌ |
| manifest 逐产物 sha256 + 同号 + 成对发布断言 | 强：校验到字节 + 发布即卡口 | 中 | ✅ 建议 |

---

## 4. 已落地边界（本轮不做，列出防漏）

- **harmony 子包的 CI 产出**：需华为 DevEco SDK，托管 runner 装不了 → 建议另立单（自建 runner / workflow_dispatch）。
- **npm Trusted Publishing 配置**：4 个新子包需在 npmjs.com 单独配（repo=Gox、workflow=release.yml），否则首次发布 404。
- **`docs/npm-release.md` 验收口径同步**：仍是「5 平台二进制」，未含子包 → 本文拍板后一并更新。
- **成品 APK/HAP nightly 作业**：本文 §2.2 建议项，属 rgnRC4 未做部分，待拍板后实施。

## 5. 待用户拍板清单

1. **产物形态**：是否采纳「库进 npm + 成品示例 APK/HAP 进 GitHub Releases」双轨（§1 方案 C）？
2. **分发渠道**：是否确认独立平台子包命名 `@goxjs/goxjs-mobile-<platform>-<abi>`（而非 `@goxjs/gox-android` 式短名）？
3. **Releases 两档**：是否新增 nightly 档（`nightly-<date>` + prerelease 标记）？npm 侧是否 nightly 只发库不发 dist-tag？
4. **成对发布断言**：是否在 release.yml 增加 `version-consistency` 步骤（含壳工程版本引用一致性）？
5. **本文与 `app/MOBILE-DISTRIBUTION.md` 的关系**：拍板后以本文为准，`app/` 版本改为引用本文？

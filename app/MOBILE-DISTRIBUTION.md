# 移动端分发 —— 落地记录（M11）

> **决策的唯一真源是 [`docs/mobile-distribution-decision.md`](../docs/mobile-distribution-decision.md)。**
> 那份文档回答 `rgnRC4` / `r8DoFS` 的三件事：产物形态 / 分发渠道 / 版本兼容矩阵，
> 以及 §6 的拍板结论（2026-10-10 自动化轮次已按推荐项 ① 执行，最终由维护者复核）。
>
> **本文只保留落地记录**：子包布局、`optionalDependencies` 的论证、改动清单、未完成边界。
> 写它时该工作流只允许改 `app/`，所以决策部分当初落在了这里；两份内容已对齐，
> **今后改决策改 `docs/` 那份，改落地事实改本文** —— 不要两处都写，避免长期漂移。

## 结论速览（详见决策文档）

- 移动端产物**只发预编译库**（`libgox.so` / `libgox.a` + `libgox.h`），**不发壳工程源码**。
- 发布渠道 = npm **平台子包** + GitHub Releases 两档（nightly / release）**并存**。
- 版本对齐 = 子包 `version` 与主包**同号** + `manifest.json` 的 `goxVersion` 消费端校验
  + 壳工程声明文件 `app/<platform>/gox-engine.json`（第 4 层发布闸门，见下）。

### 壳工程版本声明（每个壳工程一份）

`app/{android,harmony,ios}/gox-engine.json`：

```json
{ "engineVersionRange": "0.9.x" }
```

引擎版本真源是 `cmd/gox/main.go` 的 `const version`；`scripts/check-shell-engine-version.py`
比对两者，`mobile-release.yml` 的 `gate` 作业带 `--require` 跑 —— **缺失或不符都判红**。
引擎 minor 版本推进时，三个壳工程的声明必须一起改（否则发布闸门会挡住）。

---

## 1. ⚠️ 为何不用 optionalDependencies 自动装

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

## 2. 发布面布局

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

子包**定义**（单一真源）在主仓 `packaging/npm-mobile/<dirname>/metadata.json`，经
`scripts/gen-npm-mobile-pkgs.py` 生成到 `npm/packages/<dirname>/{package.json,README.md}`；
**产物目录** `dist/npm-mobile-pkgs/<dirname>/` 是构建输出（`dist/` 已 gitignore），由
`build-npm-mobile.sh` 把生成的 `package.json`/`README.md` 拷进去、再落产物与清单。

> 定义为什么放主仓而不是 `npm/`：`npm/` 是独立仓库（`gox-npm`）的 git submodule，
> 定义放进子模块就脱离主仓版本管理、也没法跟 `scripts/`、`release.yml` 一起 review。
> `build-npm-mobile.sh` 也不再硬编码子包表，而是从 `packaging/npm-mobile/` 读
> （加平台只需加一个定义目录，脚本自动认到）。详见 `packaging/npm-mobile/README.md`。

### 消费端用法

```bash
npm i @goxjs/goxjs-mobile-android-arm64-v8a    # 按需装（其余平台同理，见决策文档 §5）
bash scripts/fetch-mobile-libs.sh              # 校验并放进 app/{android,harmony,ios}
bash scripts/fetch-mobile-libs.sh --check-only # 只校验，不落盘（CI 可用）
```

---

## 3. 已落地的改动清单

| 文件 | 作用 |
|---|---|
| `packaging/npm-mobile/<dirname>/metadata.json`（新增，**单一真源**） | 4 个平台子包的元数据定义（主仓可版本管理） |
| `scripts/gen-npm-mobile-pkgs.py`（新增） | 从上面的定义生成 `npm/packages/<dirname>/{package.json,README.md}`；`--check` 供 CI 断言已同步 |
| `npm/packages/goxjs-mobile-*/{package.json,README.md}`（生成物） | 4 个平台子包的 npm 身份与包页面文案 |
| `scripts/build-npm-mobile.sh`（重写） | 编 android/harmony/ios 的 libgox，staging 成 `dist/npm-mobile-pkgs/<dirname>/`（含生成的 `manifest.json`）；子包表从 `packaging/npm-mobile/` 读，不硬编码 |
| `scripts/fetch-mobile-libs.sh`（重写） | 消费端：从已装的平台子包校验 `goxVersion` + 逐产物 `sha256`，再放进三平台壳工程 |
| `scripts/check-registries.py` | 检查4 改为主包 `files` **不含 `mobile/`**；新增检查5「移动端子包自洽」（命名/版本/清单；对存在的 staging 逐产物比对 sha256） |
| `scripts/build-ios.sh` | 新增 `--lib-only`（只编 `libgox.a`，不碰壳工程/xcodebuild） |
| `.github/workflows/release.yml` | `mobile-ios` 作业产出 iOS 子包；`publish` 编 android 子包、下载 ios 子包、校验、逐子包 pack/publish |
| `.github/workflows/mobile-smoke.yml` | `npm-package` 作业：主包 pack **不含 mobile/** 且无大文件回归 + 每个子包 pack 含产物且 sha256 与 manifest 一致 |
| `.github/workflows/mobile-release.yml`（新增） | 成品 App 进 GitHub Releases：nightly（cron）+ release 两档；`gate` 作业跑三个一致性闸门 |
| `scripts/check-shell-engine-version.py`（新增） | 第 4 层闸门：读 `cmd/gox/main.go` 的 `const version` 比对壳工程声明；`--require` 下缺失/不符均退出 1 |
| `app/{android,harmony,ios}/gox-engine.json`（新增） | 壳工程声明它面向哪一代引擎（当前 `{"engineVersionRange": "0.9.x"}`） |

---

## 4. 未完成边界

- **壳工程模板「发布」无独立制品**：壳工程源码目前只随 Gox 仓库与 `gox create` 分发；
  `gox create` 的内置骨架已含 `android/` 与 `ios/`，**缺 `harmony/` 骨架**。
  建议另立单：把 `harmony/` 骨架补进 `scaffold/template/`（并同步 `scaffold_test.go` 的 `wantFiles`）。
- **harmony 全链路缺口**：托管 runner 装不了 DevEco SDK ⇒ CI 产不出 HAP、也产不出 harmony 子包。
  需要「自建 runner + DevEco」或另立单。
- **凭证仍待人工**（决策文档 §6.5）：4 个 npm 子包的 Trusted Publishing 未配（首次发布 404）、
  Android keystore 缺失（只能出 debug APK，不可上架）、DevEco SDK 缺失（HAP 恒缺）。
- **`gox create` 与产品文档**：`docs/npm-release.md` / `docs/desktop-distribution.md` 的移动端
  口径已在 2026-10-10 轮次补齐（决策文档 §5），无需再动。

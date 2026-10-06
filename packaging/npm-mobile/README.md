# 移动端子包定义（M11）

本目录是 Gox 移动端**平台子包**的**单一真源**：

```
packaging/npm-mobile/<dirname>/metadata.json     ← 定义（可版本管理，进主仓）
                        │
                        │  python3 scripts/gen-npm-mobile-pkgs.py
                        ▼
npm/packages/<dirname>/package.json              ← npm 真正认的身份（生成物）
npm/packages/<dirname>/README.md                 ← 包页面文案（生成物）
```

**为什么不直接改 `npm/packages/`？** 因为 `npm/` 是独立仓库（`gox-npm`）的 git submodule，
定义放在子模块里就脱离主仓版本管理、也没法跟 `scripts/`、`release.yml` 一起 review。
所以定义**上移到主仓** `packaging/npm-mobile/`，`npm/packages/` 下的两个文件只当**生成产物**。

## 子包是什么

发布渠道 = **平台子包** `@goxjs/goxjs-mobile-<platform>-<abi>`：

| dirname | 平台 | ABI | 产物 |
|---|---|---|---|
| `goxjs-mobile-android-arm64-v8a` | android | arm64-v8a | `libgox.so` |
| `goxjs-mobile-harmony-arm64-v8a` | harmony | arm64-v8a | `libgox.so` |
| `goxjs-mobile-ios-iphoneos-arm64` | ios | iphoneos-arm64 | `libgox.a` + `libgox.h` |
| `goxjs-mobile-ios-iphonesimulator-arm64` | ios | iphonesimulator-arm64 | `libgox.a` + `libgox.h` |

## 为什么不用 optionalDependencies

**npm 默认就会安装 `optionalDependencies`**（只有 `--no-optional` / `--omit=optional` 才跳过）。
把它写进主包，等于每个桌面用户 `npm i @goxjs/goxjs` 时照样被塞 20–30MB 的移动端库 ——
和「主包直塞」代价完全一样，等于没拆。

`optionalDependencies` 的经典用法（esbuild 的 `@esbuild/*`）成立的前提是**每个用户都需要它**，
只是平台不同。移动端库不是：**桌面用户不是"装了用不上"，而是"根本不该装"**。
所以子包**不进 `dependencies` / `optionalDependencies` / `peerDependencies`**，
由移动端消费者**显式安装**：

```bash
npm i @goxjs/goxjs-mobile-android-arm64-v8a   # 按需，一条命令
```

完整论证见 `app/MOBILE-DISTRIBUTION.md`。

## 字段说明（`metadata.json`）

| 字段 | 用途 |
|---|---|
| `dirname` | 子包目录名，也是 `@goxjs/<dirname>` 的包名后缀 |
| `name` | 完整 npm 包名（`@goxjs/goxjs-mobile-<platform>-<abi>`） |
| `platform` / `abi` | 由它们推出 dirname（`<platform>-<abi>`）；`build-npm-mobile.sh` 的查表就靠这两个 |
| `description` | 包描述（进 `package.json`） |
| `keywords` | 包关键词（进 `package.json`） |
| `artifacts` | 该平台的产物白名单（进 `package.json` 的 `files`，除 `manifest.json` 外） |
| `libName` / `buildmode` | 主产物名与 Go buildmode（文档/校验用） |
| `readme*` | 生成 README 各段落的文案 |

`version` **不在**这里 —— 子包版本恒等于主包版本（同号约定），
生成器直接取 `npm/package.json` 的 `version`。

## 加/改一个子包

1. 在 `packaging/npm-mobile/` 下新建 `<dirname>/metadata.json`（`dirname` 必须 `goxjs-mobile-<platform>-<abi>`）。
2. 跑 `python3 scripts/gen-npm-mobile-pkgs.py` 生成 `npm/packages/<dirname>/{package.json,README.md}`。
3. 在 `npm/packages/` 所在的 gox-npm 子模块里 commit 生成物，并在主仓更新 submodule 指针。
4. `scripts/build-npm-mobile.sh` 会自动认到新子包（它从 `packaging/npm-mobile/` 读表，不硬编码）。
5. `scripts/check-registries.py` 检查5 会校验命名/版本/清单自洽，漏了同步会红。

CI 用 `python3 scripts/gen-npm-mobile-pkgs.py --check` 断言「生成物与定义已同步」。

# Gox 桌面程序分发指南

面向 `jsbuild` 打包产物 (P2–P4) 的各平台分发注意事项。Gox GUI 应用自带
运行时与软件渲染器, 无任何动态库依赖 (Windows/Linux 产物均为静态单文件)。

> **本文不适用移动端 (Android / iOS / HarmonyOS)。** 移动端产物不是一个可执行文件,
> 而是「预编译库 (`libgox.so` / `libgox.a`) + 一层宿主壳工程 (Kotlin / Swift / ArkTS)」——
> 桌面这条「编出一个文件、用户直接跑」的路子在移动端不存在。
>
> | | 桌面 (本文) | 移动端 |
> |---|---|---|
> | 拿到什么 | 一个 `gox` 可执行文件 | 预编译库 + 要自己铺一层壳工程 |
> | 从哪拿 | `npm i -g @goxjs/goxjs` | `npm i @goxjs/goxjs-mobile-<platform>-<abi>` |
> | 怎么用 | 直接跑 `gox` / `goxjs` | 库落进壳工程后 `gradle` / `hvigor` / `xcodebuild` |
> | 本机工具链 | **不需要** (静态单文件) | **需要**: Android NDK+JDK / DevEco / Xcode (但不需要 Go) |
> | 想装上直接看 | Release 下 universal 二进制 | Release 下成品 APK / 壳工程 zip (`mobile-release.yml`) |
>
> 移动端怎么拿、怎么装、版本怎么对齐, 见
> [`docs/mobile-distribution-decision.md`](./mobile-distribution-decision.md);
> 发版口径见 [`docs/npm-release.md`](./npm-release.md) §7。

## 产物命名约定

| 平台 | 命令 | 产物 |
|---|---|---|
| Windows | `jsbuild app.js --gui` | `app.exe` |
| Windows (无控制台) | `jsbuild app.js --gui --windowed` | `app.exe` |
| Linux | `jsbuild app.js --gui --target linux/amd64` | `app-linux-amd64` |
| Linux arm64 | `jsbuild app.js --target linux/arm64` | `app-linux-arm64` |
| macOS | `gox build macos [目录]` | `dist/<name>.app` (darwin/arm64) |
| macOS universal | `gox build macos --arch universal [目录]` | `dist/<name>.app` (fat: amd64+arm64) |
| Release Assets | push 到 main 时 `release.yml` 的 universal 作业 | `gox-darwin-universal` / `gox-darwin-amd64` / `gox-darwin-arm64` |

Release Assets 那三个 darwin 产物是给"官网 / Release 页直接下载"的用户准备的，
npm 包里的五平台二进制走 `npm i -g @goxjs/goxjs` —— 两者并存、互不替代
（架构清单分发按 arch 引用单 arch 产物）。

`--target` 支持的 os/arch: `windows`/`linux`/`darwin` × `amd64`/`arm64`/`386`
(纯 Go 交叉编译, 无需目标机工具链; darwin 另支持 `universal`, 由 packager
编译 amd64+arm64 两次后 `lipo` 合并)。macOS GUI 后端 (cocoa/purego) 已落地,
日常分发直接用 `gox build macos`, 无需手写 jsbuild 命令。

## Windows

- **图标与版本信息**：Go 1.26 支持 `//go:embed` 版本信息吗——不支持,
  标准做法是嵌入 `.syso` 资源文件。在生成的工程目录 (或本仓库根) 放置
  `rsrc_windows_amd64.syso`，可用工具生成：
  - [go-version-info](https://github.com/josephspurrier/goversioninfo)
    (XML 描述 → .syso，含图标/版本/清单)
  - [winres](https://github.com/tc-hib/winres) (CLI 或 Go API)
  - jsbuild 暂不自动生成；把 `.syso` 放进生成的工程再 build 即可
  (后续可为 jsbuild 增加 `--icon`/`--version` 参数自动生成)。
- **DPI 清单**：gfx 后端已调用 SetProcessDpiAwarenessContext
  (Per-Monitor V2 → 逐级回退)，无需清单文件；若嵌入自定义清单请保留
  dpiAware 节点。
- **SmartScreen**：未签名 exe 首次运行会提示"Windows 已保护你的电脑"。
  消除方式：代码签名证书 (OV 证书可消除大部分告警, EV 证书即时消除)。
  个人分发可让用户选"仍要运行"，或提供校验和 (sha256) 供比对。
- **CI 自动签名**：配了 `WINDOWS_PFX_BASE64` 后，`release.yml` 的 publish 作业
  会在 `npm pack` 之前用 osslsigncode 给 `gox.exe` 做 Authenticode 签名
  (含 RFC3161 时间戳)；没配就跳过，详见 [§签名与公证自动化](#签名与公证自动化)。
  自签证书 (`gox cert windows`) 也能走这条链路，但它**消除不了 SmartScreen**
  (信誉是靠证书颁发机构 + 累积下载量堆出来的)，能保证的只有完整性。
- `--windowed` 仅对 Windows 目标生效 (`-H windowsgui` 隐藏控制台)。

## Linux

- 产物为静态 ELF (纯 Go + xgb 走 X11 协议, 无 cgo), 任何发行版可直接运行。
- GUI 需要 X11 (X server 或 Wayland 的 XWayland)。`DISPLAY` 未设置时
  render() 报 "connect X server"。
- 打包格式建议：`.tar.xz` 压缩包 + `sha256sum`；进发行版仓库则按各发行版
  规范 (.deb/.rpm/.AppImage 皆可套壳)。
- **无签名/公证要求**：Linux 没有 Gatekeeper / SmartScreen 这类"首次运行拦截"，
  签名自动化的缺口只落在 Windows 与 macOS，所以本节没有对应的流水线步骤。
  ⚠️ **AppImage / deb 目前仍无流水线** —— 仓库里只有上面的格式清单，
  没有任何脚本或 workflow 产出它们；要发就手工打，或另开一单接流水线。

## macOS

GUI 后端为 cocoa (purego, 无 cgo), 已支持 IME / 原生对话框 / 多屏枚举。

- 构建: `gox build macos [目录]`（缺省 darwin/arm64; `--arch amd64` 出 Intel
  版, `--arch universal` 出 amd64+arm64 fat 版）, 产物为 .app bundle:

  ```
  <name>.app/
    Contents/
      MacOS/<name>           (Mach-O 可执行)
      Info.plist             (CFBundleName/Identifier/Version/MinimumSystemVersion)
      Resources/icon.icns
  ```

- 签名与公证 (Gatekeeper 要求) —— 手工三步（本地分发 / 排查用）：
  1. Apple Developer 账号; `codesign --deep --options runtime --sign "Developer ID Application: ..." <name>.app`
  2. `xcrun notarytool submit <name>.app.zip --apple-id ... --wait`
  3. `xcrun stapler staple <name>.app`
  - 未签名/未公证：用户需右键打开或 `xattr -d com.apple.quarantine`。
  - CI 里这三步由 `release.yml` 自动执行（用 App Store Connect API 密钥代替
    上面的 `--apple-id` 账号密码），见 [§签名与公证自动化](#签名与公证自动化)。
  - ⚠️ **staple 只对 bundle / dmg / pkg 成立**：票据要落在 `.app/Contents/` 里，
    扁平 Mach-O 二进制（本仓库 release 的 `gox` 就是这种）无处安放，硬 staple
    只会得到一句没有任何信息量的 `Error 73`。扁平二进制只能做到"已签名 +
    已公证"，Gatekeeper 首次运行时联网验票（联网可用即等同于放行，只是不能离线）。
- 手动通用二进制 (不经 gox build): jsbuild 的 `--target` 一次只出一个 arch,
  可 `--target darwin/amd64` 与 `--target darwin/arm64` 各打一次, 再
  `lipo -create -output <fat> <amd64> <arm64>` 合并。

## 签名与公证自动化

桌面签名 / 公证已接进 `release.yml`，但它是**可选增强**：配了 secrets 才生效，
没配就整段跳过并 `::notice::` 说明原因，发版不阻塞。

### 哪些产物被签了（与本文上面的清单对表）

| 产物 | 平台 | 谁签 | 缺凭证时 |
|---|---|---|---|
| `npm/binaries/windows-x64/gox.exe` | Windows | publish 作业：osslsigncode Authenticode + RFC3161 时间戳 | 跳过，发出未签名 exe |
| `npm/binaries/darwin-{x64,arm64}/gox` | macOS | sign-macos 作业（macos runner）：codesign → notarytool | 跳过，用 publish 自己编的未签名产物 |
| Release Assets 的 `gox-darwin-*` | macOS | universal 作业：同上（上传前签） | 跳过，照常上传未签名产物 |
| `npm/binaries/linux-*` | Linux | 不需要（Linux 没有 Gatekeeper / SmartScreen） | — |
| `gox build macos` 出的 `.app` | macOS | **未接 CI**：那是用户在本机产出的应用，不在本仓库的发版链路里 | — |
| AppImage / deb | Linux | **无流水线**：本文只有格式清单，没有脚本也没有作业 | — |

### 需要配哪些 secrets

| Secret | 内容 | 缺了的后果 |
|---|---|---|
| `WINDOWS_PFX_BASE64` | base64(.pfx/.p12)，自签 (`gox cert windows`) 或 CA 签发都行 | Windows 不签名（跳过 + notice） |
| `WINDOWS_PFX_PASSWORD` | pfx 密码（无密码可不配） | 同上；配了但与证书不符 → 判红 |
| `MACOS_CERT_P12_BASE64` | base64(Developer ID Application 的 p12) | macOS 整体跳过（npm 产物 + Release Assets） |
| `MACOS_CERT_PASSWORD` | p12 导出密码 | 同上 |
| `APPLE_API_KEY` | App Store Connect API 密钥的 Key ID | 只 codesign、不公证 |
| `APPLE_API_ISSUER` | Issuer ID | 同上 |
| `APPLE_API_KEY_CONTENT` | .p8 的 base64（也接受直接贴 PEM 原文） | 同上 |
| `MACOS_SIGNING_IDENTITY` | 可选，签名身份全称；不配就取 keychain 里第一个 `Developer ID Application` | — |

公证用的 App Store Connect API 密钥与 TestFlight 上传（`scripts/build-ios.sh --archive`）
是**同一套账号凭证**，桌面公证不需要另开账号。

### 跳过条件（逐条）

- 判空在 YAML 侧做：`secrets.X != ''`（GitHub 未配置的 secret 求值为空串，
  只有表达式能区分"没配"和"配了空串"）；布尔结论交给
  `scripts/check-signing-creds.sh`，由它打 `::notice::` 并输出 `enabled=`。
- 缺 `WINDOWS_PFX_BASE64` → Windows 签名步骤不跑（`exit 0`）。
- 缺 `MACOS_CERT_P12_BASE64` → sign-macos 作业一步不跑（连 checkout 都不做，
  也不传 artifact），publish 用它自己编的产物；universal 作业只上传不签名。
- 有证书但 API 密钥缺一项 → 只 codesign，日志写明"未公证，Gatekeeper 仍会拦"。
- **配了凭证却签失败 → 判红**：静默发出未签名包比发不出更糟。Windows 直接挡住
  publish；macOS 通过 `publish.needs` 连带挡住。逃生口：Apple 公证服务长时间
  不可用时，把 `MACOS_CERT_P12_BASE64` 临时置空即退回跳过路径。

### 怎么验证（本地 / CI）

- 跳过逻辑没被改坏：`python3 scripts/check-signing-workflow.py`（纯静态，
  不需要凭证也不需要 runner）；`ci.yml` 的 registries 作业跑同一条。
  它守的是"某步骤用了 secrets 却没有 `if:` 闸门"这类只在无凭证仓库上复现的坏味道。
- 探测脚本单独试：`HAS_WIN_PFX=false bash scripts/check-signing-creds.sh windows`。
- Windows（Linux 上即可验）：
  `sudo apt-get install -y osslsigncode` →
  `GOX_WIN_PFX=<pfx> bash scripts/sign-windows.sh in.exe out.exe`（脚本末尾自带
  `osslsigncode verify`，并把 pfx 里的签发者证书取出来当 `-CAfile`，所以**自签
  证书也能验过** —— 否则会出现「配了自签证书反而发不出包」）。
  Windows 上的权威判据是 `signtool verify /pa gox.exe`；用自签证书时，Windows
  侧要先把该证书装进验证机的 Trusted Root 才能通过信任链校验。
- macOS（需在 mac 上）：
  `MACOS_CERT_P12=<p12> APPLE_API_KEY=<KeyID> APPLE_API_ISSUER=<IssuerID> APPLE_API_KEY_FILE=<AuthKey.p8> bash scripts/sign-macos.sh <产物>`；
  验收 `spctl -a -vv -t exec <产物>` 应出现 `accepted source=Notarized Developer ID`；
  `.app` / `.dmg` 另跑 `xcrun stapler validate <产物>` 看票据。
- 拿到待验产物：`npm pack --pack-destination dist` 后解包看 `package/binaries/`，
  或从 Release 页下载 asset。

## 校验和与更新

- 每个发布产物附 `sha256sum`。
- 简单的自动更新：应用内 fetch 一个 JSON (版本号+下载地址+sha256)，
  提示用户下载替换 (引擎自带 http/fetch 能力即可实现)。

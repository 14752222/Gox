# Gox 桌面程序分发指南

面向 `jsbuild` 打包产物 (P2–P4) 的各平台分发注意事项。Gox GUI 应用自带
运行时与软件渲染器, 无任何动态库依赖 (Windows/Linux 产物均为静态单文件)。

## 产物命名约定

| 平台 | 命令 | 产物 |
|---|---|---|
| Windows | `jsbuild app.js --gui` | `app.exe` |
| Windows (无控制台) | `jsbuild app.js --gui --windowed` | `app.exe` |
| Linux | `jsbuild app.js --gui --target linux/amd64` | `app-linux-amd64` |
| Linux arm64 | `jsbuild app.js --target linux/arm64` | `app-linux-arm64` |
| macOS | `jsbuild app.js --target darwin/universal 尚未支持` (见下) | — |

`--target` 支持的 os/arch: `windows`/`linux`/`darwin` × `amd64`/`arm64`/`386`
(纯 Go 交叉编译, 无需目标机工具链; darwin GUI 后端尚未实现, 见
IDLE_TASK_REPORT_P4)。

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
- `--windowed` 仅对 Windows 目标生效 (`-H windowsgui` 隐藏控制台)。

## Linux

- 产物为静态 ELF (纯 Go + xgb 走 X11 协议, 无 cgo), 任何发行版可直接运行。
- GUI 需要 X11 (X server 或 Wayland 的 XWayland)。`DISPLAY` 未设置时
  render() 报 "connect X server"。
- 打包格式建议：`.tar.xz` 压缩包 + `sha256sum`；进发行版仓库则按各发行版
  规范 (.deb/.rpm/.AppImage 皆可套壳)。

## macOS (待 GUI 后端落地)

- .app bundle 结构：

  ```
  Counter.app/
    Contents/
      MacOS/counter          (Mach-O 可执行)
      Info.plist             (CFBundleName/Identifier/Version/MinimumSystemVersion)
      Resources/icon.icns
  ```

- 签名与公证 (Gatekeeper 要求)：
  1. Apple Developer 账号; `codesign --deep --options runtime --sign "Developer ID Application: ..." Counter.app`
  2. `xcrun notarytool submit Counter.app.zip --apple-id ... --wait`
  3. `xcrun stapler staple Counter.app`
  - 未签名/未公证：用户需右键打开或 `xattr -d com.apple.quarantine`。
- 通用二进制：`lipo` 合并 amd64/arm64 产物 (jsbuild 的 `--target` 一次
  只出一个 arch, 各打一次再合并)。

## 校验和与更新

- 每个发布产物附 `sha256sum`。
- 简单的自动更新：应用内 fetch 一个 JSON (版本号+下载地址+sha256)，
  提示用户下载替换 (引擎自带 http/fetch 能力即可实现)。

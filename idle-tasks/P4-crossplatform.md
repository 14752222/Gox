# 闲时任务 P4：Gox GUI —— Linux/macOS 后端与跨平台打包

## 项目背景

- 仓库：当前工作区（Gox 项目根目录），纯 Go 的 JS 引擎 Gox。
  路线：SolidJS 式 JSX + signals + 自研渲染器。
- P1 signals+JSX；P2 `gfx/win32` + Surface 接口 + 事件循环；P3 字体/布局/`jsbuild --gui`。
- 本任务把窗口层扩展到 Linux(X11) 和 macOS，并打通交叉编译打包。

## 前置检查（不满足则停止并写报告）

`go test ./...` 全绿；`gfx` 中存在 `Surface`/`WindowFactory` 平台抽象；
P3 报告确认字体/布局可用。

## 任务范围

### 1. Linux X11 后端（`gfx/x11/`）

- 允许依赖白名单新增：`github.com/jezek/xgb`（纯 Go 的 X11 协议绑定，无 cgo）。
- 实现 P2 定义的 `Surface`/`WindowFactory`：建窗口、`PutImage` 上屏、
  按钮/按键/关闭事件接入 PostTask 队列、暴露事件等待入口供 pump 使用（`poll` 超时等待）。
- `WindowFactory` 按 `runtime.GOOS` 自动选择后端（构建标签分文件：
  `win32.go`/`x11.go` 加 `//go:build` 约束）。
- 验证方式：从 Windows 交叉编译 `GOOS=linux GOARCH=amd64 go build ./...` 必须通过；
  运行时验证在 Linux/WSL（有 X server，如 VcXsrv）下手动做，报告中注明是否验证过。

### 2. macOS 后端（`gfx/cocoa/`，允许先行降级）

- 目标：纯 Go 经 syscall 调 libobjc（`objc_msgSend`）驱动 NSApplication/NSWindow，
  图层或 NSBitmapImageRep 上屏。`WindowFactory` 不必自动启用，编译期用 `//go:build darwin` 隔离。
- 这是研究型任务，按里程碑推进：① 跑通 objc 消息发送拿到 NSWindow；② 上屏一块纯色；
  ③ 接事件。**每个里程碑都先提交到报告再继续**；任一里程碑超过 5 次尝试未突破，
  停止并在报告中给出可行路径分析（包括"cgo CoreGraphics 作为后备"的对比），不要硬磕。
- 从 Windows 交叉编译 `GOOS=darwin GOARCH=amd64 go build ./...` 通过即可（运行时验证列为待办）。

### 3. jsbuild 跨平台目标

- `packager/main.go` 加 `--target <os>/<arch>`：设置 `GOOS/GOARCH` 交给 `go build`；
  非 Windows 目标默认输出不带 `.exe` 后缀；`--windowed` 的 `-H windowsgui` 仅对 windows 目标生效；
  加 `-trimpath`。
- 验收：在 Windows 上产出 linux/amd64 的 CLI demo 可执行文件，
  并在 WSL 里实际运行成功（GUI exe 在 WSL+X server 下能弹窗）。

### 4. 分发打磨（文档即可，不强制实现）

写 `docs/desktop-distribution.md`：Windows 图标与版本信息（.syso）做法、
SmartScreen/签名说明、macOS .app bundle 结构与签名公证要求、各平台产物命名。
不要求真的签名（无证书）。

## 验收标准

1. `go build ./...` 在 windows/amd64 全绿；`GOOS=linux`、`GOOS=darwin` 交叉编译全绿。
2. `go test ./...` 全绿（新增后端逻辑的可测部分：事件解码、帧缓冲布局等不依赖显示器的单元测试）。
3. `jsbuild --target linux/amd64` 产物在 WSL 运行成功（或报告中说明环境缺失原因）。
4. 依赖白名单外零新增；Gox 核心包零改动。

## 安全边界

同前：无 git 操作、不动无关文件、白名单外不引入依赖
（`go get` 白名单包是唯一允许的网络操作）、macOS 后端严格按里程碑止损。

## 交付物

仓库根目录写 `IDLE_TASK_REPORT_P4.md`：各后端状态矩阵（编译/运行/未验证）、
交叉编译命令与产物清单、macOS 里程碑进展与结论、分发文档位置。

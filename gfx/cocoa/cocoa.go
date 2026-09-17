//go:build darwin

// Package cocoa 是 gfx 的 macOS 窗口后端 —— 当前为**编译级占位实现**
// (Create 返回明确错误), 运行时路径分析与后备方案见 IDLE_TASK_REPORT_P4.md。
//
// 为什么降级 (任务书 P4 允许, 里程碑止损):
//  1. 无 cgo 时 darwin 缺少动态库加载入口: syscall.NewLazyDLL 仅存在于
//     Windows; dlopen/dlsym 本身需要先取得函数地址 —— 纯 Go 做到这一步
//     需要汇编蹦床 (github.com/ebitengine/purego 的方案, 第三方依赖,
//     超出 P4 白名单)。
//  2. 即使有了 objc_msgSend, 无 cgo 也无法生成 C 函数指针 → 不能
//     subclass NSView / 挂 delegate IMP。可绕行 (App 层 nextEvent 截获 +
//     lockFocus + CoreGraphics C API 绘制), 但同样依赖第 1 点。
//  3. 本仓库无 macOS 环境, 交叉编译只能保证"编译通过", 无法逼近任何
//     里程碑的运行时验证。
//
// 候选路线 (按侵入度排序):
//
//	a. 引入 purego (纯 Go, 无 cgo) 提供 dlopen/objc_msgSend → 按上述
//	   绕行设计实现 (预计中等工作量, 仍需真机验证);
//	b. cgo 后备: build tag 隔离的 darwin+cgo 后端, 直接用 CoreGraphics
//	   与 NSView subclass (最标准, 但破坏"零 cgo"特性, 且需要 macOS SDK
//	   的交叉编译工具链, Windows 主机无法产出);
//	c. WebView/WKWebView 壳 (同样需要上述任一底层能力)。
//
// 在真 macOS 环境可用前, 保持本占位实现; render() 在 darwin 上返回
// "cocoa backend not yet implemented (see IDLE_TASK_REPORT_P4)"。
package cocoa

import (
	"errors"

	"github.com/14752222/Gox/gfx"
)

func init() {
	gfx.SetDefaultFactory(&factory{})
}

type factory struct{}

var errNotImplemented = errors.New("cocoa backend not yet implemented (see IDLE_TASK_REPORT_P4)")

func (f *factory) Create(cfg gfx.WindowConfig) (gfx.Surface, error) {
	return nil, errNotImplemented
}

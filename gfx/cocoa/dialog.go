//go:build darwin

// 原生对话框 (nativeDialogHost 的 cocoa 实现): NSAlert / NSOpenPanel / NSSavePanel。
//
// ## 为什么用 runModal 而不是 beginSheetModalForWindow + completionHandler
//
// gfx/dialog.go 的契约是**同步**的: nativeDialogHost 的两个方法都阻塞到
// 用户作答 (JS 侧的 Promise 外观由 resolveDeferred + SetTimeout(0) 营造,
// 见 gfx/dialog.go 文件头)。而这个同步调用发生在**主 GUI 线程**上
// (脚本执行、定时器回调、gfx.Pump 全在同一线程串行) —— 此时 gfx 自己的
// 事件泵正被我们占着, 没有人跑 NSDefaultRunLoopMode。如果改用 sheet 异步
// 回调, completionHandler 永远等不到 run loop 空闲 → 死锁。
//
// runModal 则是 AppKit 官方支持的嵌套 run loop (NSModalPanelRunLoopMode):
// 它自带事件泵, 对话框期间窗口重绘 (Core Animation 事务在 common modes
// 提交)、拖动、其它窗口交互全部照常 —— 与 win32 用 MessageBoxW(OWNER)
// 让 Windows 接管消息泵是同一思路。代价是 gfx.Pump 暂停 (定时器停摆、
// gfx 层的鼠标/键盘事件排队不走), 这正是"模态"的语义, 与 win32 行为一致。
//
// ## 错误语义 (与 gfx/dialog.go 对齐)
//
//   - 取消/关窗**不是错误**: 返回 ok=false + err=nil (用户按 Esc 是正常
//     操作, 报错会逼所有脚本写 try/catch) → JS 侧 openFile 取消 → null。
//   - err 只留给"对话框本身没法弹"的故障 (本实现里 runModal 不会失败,
//     所以实际上恒为 nil, 保留签名与契约一致)。
//
// ## NSSavePanel 的说明
//
// nativeDialogHost 目前只有 openFile 一条文件 API (gfx/dialog.go 没有
// saveFile 的脚本侧入口), 这里把 NSSavePanel 一并实现为 surface 上的
// 预置能力 (ShowSaveFile): 构造/执行/结果映射与 NSOpenPanel 共用同一条
// runFilePanel 路径, 等 gfx 契约补 saveFile 时直接接上即可。
package cocoa

import (
	"strings"

	"github.com/14752222/Gox/gfx"
	"github.com/ebitengine/purego/objc"
)

var (
	selSetMessageText     = objc.RegisterName("setMessageText:")
	selSetInformativeText = objc.RegisterName("setInformativeText:")
	selSetAlertStyle      = objc.RegisterName("setAlertStyle:")
	selAddButton          = objc.RegisterName("addButtonWithTitle:")
	selRunModal           = objc.RegisterName("runModal")
	selButtons            = objc.RegisterName("buttons")
	selOpenPanel          = objc.RegisterName("openPanel")
	selSavePanel          = objc.RegisterName("savePanel")
	selChooseFiles        = objc.RegisterName("setCanChooseFiles:")
	selChooseDirs         = objc.RegisterName("setCanChooseDirectories:")
	selAllowMultiple      = objc.RegisterName("setAllowsMultipleSelection:")
	selSetDirectoryURL    = objc.RegisterName("setDirectoryURL:")
	selSetNameField       = objc.RegisterName("setNameFieldStringValue:")
	selSetAllowedTypes    = objc.RegisterName("setAllowedFileTypes:") // macOS 12 起弃用但仍可用
	selURL                = objc.RegisterName("URL")
	selFileURLWithPath    = objc.RegisterName("fileURLWithPath:")
	selPath               = objc.RegisterName("path")
	selAddObject          = objc.RegisterName("addObject:")
	selMutableArrayInit   = objc.RegisterName("initWithCapacity:")
)

// AppKit 响应码 (NSModalResponse / NSAlertButtonReturn)。
const (
	nsModalResponseOK         = 1    // NSModalResponseOK
	nsAlertFirstButtonReturn  = 1000 // NSAlertFirstButtonReturn
	nsAlertStyleInformational = 1
)

// ShowMessage 弹消息框, 实现 gfx 的 nativeDialogHost。
//
// DialogInfo 只有一个缺省按钮 (AppKit 自带的 OK) → 恒返回 true;
// DialogConfirm 补"确定/取消"两个按钮 → 返回码 1000 = 确定。
func (s *surface) ShowMessage(kind gfx.NativeDialogKind, title, message string) (bool, error) {
	return alertConfirm(newAlert(kind, title, message)), nil
}

// alertConfirm 进模态并判定是否"确定" (拆出来供真机测试注入定时点击)。
func alertConfirm(alert objc.ID) bool {
	return objc.Send[int64](alert, selRunModal) == nsAlertFirstButtonReturn
}

// newAlert 构造 NSAlert (构造与进模态分离, 理由见 alertConfirm)。
func newAlert(kind gfx.NativeDialogKind, title, message string) objc.ID {
	if title == "" {
		title = "Gox"
	}
	alert := objc.ID(objc.GetClass("NSAlert")).Send(selAlloc)
	alert = alert.Send(selInit)
	alert.Send(selSetAlertStyle, uintptr(nsAlertStyleInformational))
	alert.Send(selSetMessageText, nsString(title))
	alert.Send(selSetInformativeText, nsString(message))
	if kind == gfx.DialogConfirm {
		// 第一个按钮 = 缺省 (Return) → 1000; 第二个 (取消) → 1001,
		// Esc 也映射到它。两个返回码都不等于"确定"时自然映射 false。
		alert.Send(selAddButton, nsString("确定"))
		alert.Send(selAddButton, nsString("取消"))
	}
	return alert
}

// ShowOpenFile 弹"打开文件", 实现 gfx 的 nativeDialogHost。
// 取消时 ("", false, nil) —— 见文件头错误语义。
func (s *surface) ShowOpenFile(opts gfx.NativeFileOptions) (string, bool, error) {
	path, ok := runFilePanel(newOpenPanel(opts))
	return path, ok, nil
}

// ShowSaveFile 弹"保存文件"。gfx 契约暂无 saveFile 入口 (见文件头),
// 这是预置的 surface 能力, 语义与 ShowOpenFile 完全一致。
func (s *surface) ShowSaveFile(opts gfx.NativeFileOptions) (string, bool, error) {
	path, ok := runFilePanel(newSavePanel(opts))
	return path, ok, nil
}

// runFilePanel 进模态并把返回码翻译回 gfx 的 (path, ok) 口径。
func runFilePanel(panel objc.ID) (string, bool) {
	if objc.Send[int64](panel, selRunModal) != nsModalResponseOK {
		return "", false
	}
	url := panel.Send(selURL)
	if url == 0 {
		return "", false
	}
	return nsToGo(url.Send(selPath)), true
}

// newOpenPanel / newSavePanel 构造文件面板 (构造与进模态分离, 理由同上)。
func newOpenPanel(opts gfx.NativeFileOptions) objc.ID {
	panel := objc.ID(objc.GetClass("NSOpenPanel")).Send(selOpenPanel)
	panel.Send(selChooseFiles, true)
	panel.Send(selChooseDirs, false)
	panel.Send(selAllowMultiple, false)
	applyFileOptions(panel, opts)
	return panel
}

func newSavePanel(opts gfx.NativeFileOptions) objc.ID {
	panel := objc.ID(objc.GetClass("NSSavePanel")).Send(selSavePanel)
	applyFileOptions(panel, opts)
	return panel
}

// applyFileOptions 把 gfx.NativeFileOptions 翻译到 NSPanel 的公共选项。
func applyFileOptions(panel objc.ID, opts gfx.NativeFileOptions) {
	if opts.Title != "" {
		panel.Send(selSetTitle, nsString(opts.Title))
	}
	if opts.Dir != "" {
		if url := objc.ID(objc.GetClass("NSURL")).Send(selFileURLWithPath, nsString(opts.Dir)); url != 0 {
			panel.Send(selSetDirectoryURL, url)
		}
	}
	if opts.Default != "" {
		panel.Send(selSetNameField, nsString(opts.Default))
	}
	if arr := filterExtensions(opts.Filter); arr != 0 {
		panel.Send(selSetAllowedTypes, arr)
	}
}

// filterExtensions 把过滤规则归一成扩展名数组 (NSArray of NSString)。
//
// macOS 的文件面板不认 Windows 式 "desc|*.txt" 通配串, 只认扩展名列表
// ("txt","md"); "*" (所有文件) 没有对应扩展名, 丢弃即可 —— 只剩全 "*"
// 时返回 nil, 面板退化为不过滤。
func filterExtensions(filters []gfx.NativeFileFilter) objc.ID {
	var exts []string
	for _, f := range filters {
		for _, p := range strings.Split(f.Pattern, ";") {
			p = strings.TrimSpace(p)
			p = strings.TrimPrefix(p, "*.")
			if p == "" || p == "*" || strings.ContainsAny(p, "*?/") {
				continue
			}
			exts = append(exts, p)
		}
	}
	if len(exts) == 0 {
		return 0
	}
	arr := objc.ID(objc.GetClass("NSMutableArray")).Send(selAlloc)
	arr = arr.Send(selMutableArrayInit, uintptr(len(exts)))
	for _, e := range exts {
		arr.Send(selAddObject, nsString(e))
	}
	return arr
}

package gfx

import (
	"errors"
	"os"
	"strings"

	"github.com/14752222/Gox/object"
)

// 原生系统对话框 (P3-4): alert / confirm / 文件选择。
//
// 注册为独立模块 `gx/dialog` (与 `gx/gfx` 分开: 这三个 API 不依赖元素树,
// 只依赖"当前有没有窗口"):
//
//	import { alert, confirm, openFile } from "gx/dialog";
//	await alert("保存成功");
//	if (await confirm("确定删除吗?")) { ... }
//	const path = await openFile({ filter: "文本文件|*.txt", title: "打开" }); // 取消 → null
//
// **为什么是 async (Promise) 而不是像剪贴板那样的同步 API**:
// 剪贴板是真的同步 —— 在脚本线程内联调 `OpenClipboard` 就完事, 没有等待。
// 原生模态对话框不一样: 它要在屏幕上挂住直到用户点按钮, 期间**必须继续泵消息**,
// 否则窗口会假死 (重绘、拖动、其它窗口的输入全部停摆)。
//
// 任务书建议"在独立 goroutine 里弹出 MessageBox, 结果经 channel 回 VM 线程
// resolve Promise"。这里**没有**沿用 goroutine, 原因是最关键的一条:
//
//	Windows 的 MessageBox 若以 hwndOwner (我们的窗口) 为 owner, 会
//	**禁用该窗口**并**归一到调用线程的消息队列**, 直到它返回为止。
//
// 也就是说 owner 版的模态框会自动接管"泵消息"这件事 —— 我们不需要额外线程;
// 而在另一个 goroutine 里调用它, 反而会让窗口的归属与禁用关系变得难以推理
// (跨线程 UI 调用本身就是 Win32 的雷区)。所以最终形态是**同步落地 + 异步外观**:
//
//   - Go 侧 `alertBox`/`confirmBox`/`openFileBox` 是同步函数 (阻塞到用户作答);
//   - JS 侧 `jsAlert`/`jsConfirm`/`jsOpenFile` 返回 Promise, 且**先返回再作答**:
//     把作答动作经 `object.GlobalScheduler().SetTimeout(..., 0)` 投回事件循环,
//     于是 `await` 之后的代码在"本次脚本执行结束、事件循环跑起来"之后才继续。
//
// 这样既保住了 `await` 的书写体验 (任务书要求), 又让单测能注入假对话框
// 直接验 Promise 的 resolve 链路。
//
// **绝不能做的事**: 在 goroutine 里 `p.Resolve(v)`。Promise 的
// `invokePromiseCallbacks` 是**同步**执行的 (见 object/promise.go 的注释),
// 它会在那个 goroutine 里直接跑 JS 回调 —— 而 JS 线程模型要求"脚本执行、
// 消息泵、VM 回调全部在同一个 OS 线程串行"。所以 resolve 必须投回 GUI 线程。
//
// 原生能力经**可选接口** `nativeDialogHost` 由后端提供 (与 `clipboardHost` /
// `capturer` / `imeController` 同一思路): `Surface` 接口不扩, 没实现的后端
// (X11 / 假 Surface) 降级为"把内容打到 stderr 并立刻返回缺省值", 不报错 ——
// 对话框不可用不该让应用崩掉。

var errNoNativeDialog = errors.New("gfx: 当前后端没有原生对话框能力")

// NativeDialogKind 是 alert/confirm 的两种语义。
type NativeDialogKind int

const (
	// DialogInfo 只有一个"确定"按钮 (alert)。
	DialogInfo NativeDialogKind = iota
	// DialogConfirm 是"确定/取消"二选一 (confirm)。
	DialogConfirm
)

// NativeFileFilter 是一条文件类型过滤规则。
//
// Name 是显示给人看的描述 (如 "文本文件"), Pattern 是分号分隔的通配符
// (如 "*.txt;*.md")。两者分开而不是让调用者拼 "文本文件|*.txt", 是为了
// 在 Go 侧统一拼装平台要求的格式 —— Windows 的过滤串是
// `desc\x00pattern\x00 desc2\x00pattern2\x00\x00`, 手拼极易漏掉结尾的双 NUL。
type NativeFileFilter struct {
	Name    string
	Pattern string
}

// NativeFileOptions 是 openFile 的参数 (零值即合理缺省)。
type NativeFileOptions struct {
	Title   string // 对话框标题
	Filter  []NativeFileFilter
	Default string // 缺省文件名 / 初始文件名
	Dir     string // 初始目录
}

// nativeDialogHost 是 Surface 的可选能力: 弹出系统原生对话框。
//
// 三个方法都**阻塞到用户作出选择**。实现只负责"把参数翻译成本平台 API、
// 把结果翻译回来", 不碰元素树、不碰 VM (与 WndProc 的禁律一致)。
type nativeDialogHost interface {
	// ShowMessage 弹出消息框, 返回用户是否选了"确定"
	// (DialogInfo 只有确定键, 所以恒为 true)。
	ShowMessage(kind NativeDialogKind, title, message string) (bool, error)
	// ShowOpenFile 弹出"打开文件"对话框; 用户取消时 ok 为 false
	// (这不是错误 —— 取消是正常操作, 返回 err 会让脚本被迫写 try/catch)。
	ShowOpenFile(opts NativeFileOptions) (path string, ok bool, err error)
}

// dialogBackend 取当前窗口后端里的原生对话框能力。
//
// 与剪贴板同理, 走窗口而不是"进程级": 消息框需要一个 owner 窗口
// (既是模态归属, 也是居中定位的依据), 没有窗口时也无从弹起。
func dialogBackend() (nativeDialogHost, bool) {
	a := currentApp()
	if a == nil {
		return nil, false
	}
	a.mu.Lock()
	s := a.surface
	a.mu.Unlock()
	d, ok := s.(nativeDialogHost)
	return d, ok
}

// ===== 同步落地: Go 侧真正阻塞到用户作答 =====

// alertBox 弹一个只有"确定"的消息框。后端不支持时打到 stderr 并返回。
func alertBox(title, message string) {
	d, ok := dialogBackend()
	if !ok {
		fallbackMessageBox(title, message)
		return
	}
	if _, err := d.ShowMessage(DialogInfo, title, message); err != nil {
		fallbackMessageBox(title, message)
	}
}

// confirmBox 弹"确定/取消", 返回用户是否确定。后端不支持时返回 true
// (取"确定"而不是 false: 静默把用户的确定操作变成取消, 会让脚本什么都不做,
// 这种"看起来没反应"的失败比"多做一次"更难排查)。
func confirmBox(title, message string) bool {
	d, ok := dialogBackend()
	if !ok {
		fallbackMessageBox(title, message)
		return true
	}
	r, err := d.ShowMessage(DialogConfirm, title, message)
	if err != nil {
		fallbackMessageBox(title, message)
		return true
	}
	return r
}

// openFileBox 弹"打开文件"; 取消返回 ("", false)。
func openFileBox(opts NativeFileOptions) (string, bool) {
	d, ok := dialogBackend()
	if !ok {
		fallbackMessageBox(opts.Title, "当前后端不支持文件选择对话框")
		return "", false
	}
	path, ok, err := d.ShowOpenFile(opts)
	if err != nil {
		fallbackMessageBox(opts.Title, err.Error())
		return "", false
	}
	return path, ok
}

// fallbackMessageBox 是没有原生能力时的降级出口: 把内容打到 stderr。
//
// 刻意不走 `println` 之外的任何 UI: 这条路径的意义正是"还没法弹框",
// 唯一能指望的输出通道就是控制台。
func fallbackMessageBox(title, message string) {
	if title == "" {
		title = "Gox"
	}
	os.Stderr.WriteString("[dialog] " + title + ": " + message + "\n")
}

// ===== 异步外观: 返回 Promise, 作答投回事件循环 =====

// resolveDeferred 把"作答"这一步投回事件循环执行。
//
// 为什么不就地 resolve: 见文件头注释 —— 就地 resolve 会让 `await` 之后的
// 代码在**本次脚本执行途中**同步跑起来, 而 Promise 的语义是"微任务",
// 应该等当前这段脚本结束。用 `SetTimeout(..., 0)` 正好是这个语义
// (与 stdlib/http.go 的 `dispatchToLoop` 完全一致)。
func resolveDeferred(fn func()) {
	object.GlobalScheduler().SetTimeout(object.NewBuiltin("__dialog_resolve", func(...object.Value) object.Value {
		fn()
		return object.UndefinedSingleton
	}), 0)
}

// jsAlert 是 `alert(message, title?)` → Promise<void>。
func jsAlert(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.NewTypeError("alert: message required")
	}
	title, message := dialogTitleMessage(args)
	p := object.NewPromise()
	resolveDeferred(func() {
		alertBox(title, message)
		p.Resolve(object.UndefinedSingleton)
	})
	return p
}

// jsConfirm 是 `confirm(message, title?)` → Promise<boolean>。
func jsConfirm(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.NewTypeError("confirm: message required")
	}
	title, message := dialogTitleMessage(args)
	p := object.NewPromise()
	resolveDeferred(func() {
		p.Resolve(object.NewBoolean(confirmBox(title, message)))
	})
	return p
}

// jsOpenFile 是 `openFile(options?)` → Promise<string|null>。
//
// options 可以是对象 `{title, filter, default, dir}`, 也可以省略 (用系统缺省
// 过滤)。`filter` 支持三种形态, 因为它们各有各的自然写法:
//
//	"文本文件|*.txt"                     // 单条, 字符串
//	["文本文件|*.txt", "所有文件|*.*"]     // 多条, 字符串数组
//	[{name: "文本文件", pattern: "*.txt"}] // 结构化 (与其它 API 一致)
func jsOpenFile(args ...object.Value) object.Value {
	opts := parseFileOptions(args)
	p := object.NewPromise()
	resolveDeferred(func() {
		path, ok := openFileBox(opts)
		if !ok {
			// 取消 → null (与浏览器 File System Access API 一致:
			// 用户取消不是错误, 用 null 表达"什么都没选")
			p.Resolve(object.NullSingleton)
			return
		}
		p.Resolve(object.NewString(path))
	})
	return p
}

// dialogTitleMessage 从 (message, title?) 参数里取值。
func dialogTitleMessage(args []object.Value) (title, message string) {
	message = valueText(args[0])
	if len(args) > 1 {
		title = valueText(args[1])
	}
	if title == "" {
		title = "Gox"
	}
	return title, message
}

// parseFileOptions 从 openFile 的参数里解析选项。
//
// 宽容处理: 参数不是对象就整体忽略 (而不是报错)。对话框的参数写错时
// 最坏的后果应当是"过滤没生效", 不该是"整个调用抛异常"。
func parseFileOptions(args []object.Value) NativeFileOptions {
	var opts NativeFileOptions
	if len(args) == 0 {
		return opts
	}
	o, ok := args[0].(*object.Object)
	if !ok {
		return opts
	}
	if v, ok := o.GetProperty("title"); ok {
		opts.Title = valueText(v)
	}
	if v, ok := o.GetProperty("default"); ok {
		opts.Default = valueText(v)
	}
	if v, ok := o.GetProperty("dir"); ok {
		opts.Dir = valueText(v)
	}
	if v, ok := o.GetProperty("filter"); ok {
		opts.Filter = parseFileFilter(v)
	}
	if len(opts.Filter) == 0 && opts.Title == "" {
		opts.Title = "打开文件"
	}
	return opts
}

// parseFileFilter 把 filter 的三种形态归一成 []NativeFileFilter。
func parseFileFilter(v object.Value) []NativeFileFilter {
	switch x := v.(type) {
	case *object.String:
		return []NativeFileFilter{parseFilterString(x.Value)}
	case *object.Array:
		out := make([]NativeFileFilter, 0, len(x.Elements))
		for _, el := range x.Elements {
			switch e := el.(type) {
			case *object.String:
				out = append(out, parseFilterString(e.Value))
			case *object.Object:
				out = append(out, filterFromObject(e))
			}
		}
		return out
	case *object.Object:
		return []NativeFileFilter{filterFromObject(x)}
	}
	return nil
}

// parseFilterString 解析 "描述|通配符" 形式; 没有竖线就把整串当通配符。
func parseFilterString(s string) NativeFileFilter {
	if i := strings.IndexByte(s, '|'); i >= 0 {
		return NativeFileFilter{Name: s[:i], Pattern: s[i+1:]}
	}
	return NativeFileFilter{Name: s, Pattern: s}
}

// filterFromObject 解析 {name, pattern} 结构形态。
func filterFromObject(o *object.Object) NativeFileFilter {
	var f NativeFileFilter
	if v, ok := o.GetProperty("name"); ok {
		f.Name = valueText(v)
	}
	if v, ok := o.GetProperty("pattern"); ok {
		f.Pattern = valueText(v)
	}
	if f.Pattern == "" {
		f.Pattern = "*.*"
	}
	if f.Name == "" {
		f.Name = f.Pattern
	}
	return f
}

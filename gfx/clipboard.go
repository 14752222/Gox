package gfx

import (
	"errors"

	"github.com/14752222/Gox/object"
)

// 剪贴板 (P3-3)。
//
// **同步签名**: `clipboardReadText()` 返回字符串、`clipboardWriteText(text)`
// 返回布尔。与浏览器的 `navigator.clipboard.readText()` (Promise) 不同 ——
// 脚本只在事件循环里跑, 而窗口也在同一个 OS 线程, 直接调原生 API 就是正确的
// 线程, 没必要绕一圈 Promise。
//
// **唯一绝对不能写的形态**是 `Post(fn)` 之后 `<-ch` 同步等结果: 那会堵死
// 唯一会执行 `DrainTasks` 的线程, 而 fn 正排在那个队列里 —— 必然死锁,
// 且无 panic、无输出 (与"一步推多个事件"那个 16 缓冲死锁同类症状)。
//
// 原生能力经**可选接口** `clipboardHost` 由后端提供 (与 `capturer` /
// `imeController` 同思路): `Surface` 接口不扩, 没实现的后端 (X11 / 假 Surface)
// 读返回空串、写返回 false, 不报错 —— 剪贴板不可用不该让应用崩掉。

var errNoClipboard = errors.New("gfx: 当前后端没有剪贴板能力")

// clipboardHost 是 Surface 的可选能力: 读写系统剪贴板里的文本。
type clipboardHost interface {
	ReadClipboardText() (string, error)
	WriteClipboardText(s string) error
}

// clipboardBackend 取当前窗口后端里的剪贴板能力。
//
// 走窗口而不是"进程级"是有意的: Windows 的 `OpenClipboard` 要一个属于本线程
// 的窗口句柄, 没有窗口 (纯 CLI 脚本) 时本来也没有剪贴板可用。
func clipboardBackend() (clipboardHost, bool) {
	a := currentApp()
	if a == nil {
		return nil, false
	}
	a.mu.Lock()
	s := a.surface
	a.mu.Unlock()
	c, ok := s.(clipboardHost)
	return c, ok
}

// readClipboardText 读系统剪贴板里的文本; 拿不到 (无窗口 / 后端不支持 /
// 剪贴板里不是文本 / 被别的进程占着) 一律返回空串, 不 panic、不抛给脚本。
func readClipboardText() string {
	c, ok := clipboardBackend()
	if !ok {
		return ""
	}
	s, err := c.ReadClipboardText()
	if err != nil {
		return ""
	}
	return s
}

// writeClipboardText 写系统剪贴板, 返回是否成功。
func writeClipboardText(s string) bool {
	c, ok := clipboardBackend()
	if !ok {
		return false
	}
	return c.WriteClipboardText(s) == nil
}

// jsClipboardReadText 是 `clipboardReadText()`: 取剪贴板文本 (失败为空串)。
func jsClipboardReadText(args ...object.Value) object.Value {
	return object.NewString(readClipboardText())
}

// jsClipboardWriteText 是 `clipboardWriteText(text)`: 写入剪贴板, 返回布尔。
func jsClipboardWriteText(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.NewBoolean(false)
	}
	return object.NewBoolean(writeClipboardText(valueText(args[0])))
}

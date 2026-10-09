//go:build windows

package win32

// ===== GetAdaptersInfo 的 syscall 封装 =====
//
// 这个文件只放"必须 Win32 才能编译"的那部分 (懒加载 DLL + 调用约定)。布局与解析
// 在 ipadapter.go (**不带** windows 标签, 好让它在 Linux 上也能 `go test` —— 见该
// 文件头的说明)。
//
// 边界纪律在这里收口: queryAdapterInfo 返回的切片**已经按可信长度截断**, 下游
// networkTypeFromBuffer 只认这个长度, 不再回头看 GetAdaptersInfo 的返回值。

import (
	"syscall"
	"unsafe"
)

var (
	iphlpapi            = syscall.NewLazyDLL("iphlpapi.dll")
	procGetAdaptersInfo = iphlpapi.NewProc("GetAdaptersInfo")
)

// errBufferOverflow 是 Win32 ERROR_BUFFER_OVERFLOW (111): 传 nil 缓冲探尺寸时
// GetAdaptersInfo 用这个码告诉你"要多少字节"。用字面量是因为 syscall 包里没有这个
// 常量 (Windows 的 Errno 与 errno 不同源)。
const errBufferOverflow = 111

// queryAdapterInfo 调 GetAdaptersInfo 取记录缓冲, 返回值**已按可信长度截断**。
//
// 两步走的原因: IP_ADAPTER_INFO 是变长链表, 缓冲要多大只能问内核。不"先拍一个够
// 大的缓冲然后信回填 size" —— 回填值必须再与自己的分配长度取小者, 否则它就是
// 别人写进来的一个数。
func queryAdapterInfo() ([]byte, bool) {
	var need uint32
	if r, _, _ := procGetAdaptersInfo.Call(0, uintptr(unsafe.Pointer(&need))); r != errBufferOverflow {
		// 没有网卡 (ERROR_NO_DATA) 或枚举不可用: 诚实报未连接, 不是错误。
		return nil, false
	}
	if need == 0 || need > maxAdapterBufferSize {
		return nil, false // 尺寸荒谬 / 过大: 不按它分配
	}
	buf := make([]byte, need)
	size := need
	if r, _, _ := procGetAdaptersInfo.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size))); r != 0 {
		return nil, false
	}
	n := uintptr(size)
	if n > uintptr(len(buf)) {
		// 回填值比我们分配的还大 —— 不可信, 按自己的缓冲长度截断。
		n = uintptr(len(buf))
	}
	if n < ipAdapterInfoReadSpan {
		// size 健全性校验: 连一条记录的读取区间都覆盖不了 => 没有可解码的数据。
		// 这一步是必需的 —— size 是别人写进来的数, 不能直接用它当长度。
		return nil, false
	}
	return buf[:n], true
}

// hostNetworkType 枚举网卡判断"有没有连上 + 什么类型"。
//
// **不在包 init() 里调** (见 host.go init 的注释与 reportNetworkLazy): 走到这里
// 只有一种可能 —— 宿主能力真的被用到了。CLI / test262 跑测从不 Call 宿主 ⇒ 网卡
// 枚举在这些路径上根本不发生, 于是"枚举崩了"最坏只毁掉这一个能力, 不再毁掉进程
// 启动。
func hostNetworkType() (connected bool, typ string) {
	buf, ok := queryAdapterInfo()
	if !ok {
		return false, "none"
	}
	return networkTypeFromBuffer(buf)
}

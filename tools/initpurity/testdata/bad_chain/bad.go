// 负向样本：init → helper → 平台调用（调用链形态）。
//
// 这是 rMWHi1 的真实形态：平台调用不在 init 里，而是藏在 init 调用的 helper
// 后面。只看 init 函数体本身的工具会漏掉这一类，所以这里专门钉住它。
// 期望：报 1 处，调用链形如 `init → setupDevice → syscall.Getpid()`。
package bad_chain

import "syscall"

func init() {
	setupDevice()
}

func setupDevice() {
	_ = syscall.Getpid()
}

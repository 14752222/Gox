// 负向样本：init 里直接做系统调用。
//
// 对应 rMWHi1 的最朴素形态 —— 平台调用直接写在 init 函数体里。
// 期望：initpurity 报 1 处违规，行号落在本文件的 syscall.Getpid() 那一行。
package bad_direct

import "syscall"

func init() {
	// 进程启动阶段读系统信息：一旦这个调用出问题，不是「这个能力不可用」，
	// 而是「所有 import 了本包的二进制都起不来」。
	_ = syscall.Getpid()
}

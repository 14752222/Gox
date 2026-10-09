// 正向样本：注册表式 init —— 这类写法必须放行，否则闸门没法用。
//
// 两个关键点：
//  1. init 里只登记 map / 注册模块，不碰平台；
//  2. 平台调用出现在**闭包里**（注册给用户的回调），那是用户调用时才执行的，
//     正是平台调用该待的位置 —— 把它判成违规会让整个 stdlib 全红（实测过）。
package good_registry

import "syscall"

var registry = map[string]func() int{}

func init() {
	registry["pid"] = func() int {
		return syscall.Getpid()
	}
	registry["noop"] = func() int { return 0 }
}

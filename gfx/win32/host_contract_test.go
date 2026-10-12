//go:build windows

package win32

// ===== win32 真实宿主的契约测试 =====
//
// 断言全部在平台无关的 gfx/hosttest 里 (套件说明与断言清单见其包注释), 这里
// 只做一件事: 把 win32 真实宿主 (&win32Host{}) 喂给它。
//
// 与本文件同包的 host_test.go 是「返回值形状」单测 (字段存在、类型对); 本文件
// 是「契约语义」测试 (声明口径、声明即可用、内核回填承诺)。两者互补, 别合并。

import (
	"testing"

	"github.com/14752222/Gox/gfx"
	"github.com/14752222/Gox/gfx/hosttest"
	"github.com/14752222/Gox/object"
)

// contractHost 包装真实宿主, 只屏蔽一个**不可在无人值守环境执行的副作用**:
//
// 契约的「声明即可用」子测试会以空参数 Call 每个声明的方法。device.openSettings
// 的空参数在内核口径下等于 kind="app" (host.go hostOpenSettings: kind 缺省 app),
// 真实路径会 ShellExecuteW 弹出系统设置页 —— 那不是测试该做的副作用 (与
// host_test.go「不测成功路径」的纪律同源)。
//
// 以 Pending 应答表示「这是要系统 UI 的调用, 单测不执行」; 该方法的真假不由
// 这条包装担保 —— 映射表的正确性/完整性由 TestSettingURICoversKernelWhitelist
// 以纯函数断言单独钉住, 未实现页的错误口径由 TestHostOpenSettingsUnknownKind
// 钉住。**只许屏蔽副作用, 不许掩盖 unsupported**: 其余方法一律透传。
type contractHost struct{ *win32Host }

func (h contractHost) Call(method string, args object.Value) gfx.NativeCallResult {
	if method == "device.openSettings" {
		return gfx.NativePending()
	}
	return h.win32Host.Call(method, args)
}

// TestNativeHostContract 用平台无关契约套件跑 win32 真实宿主。
//
// 注意 (gfx/hosttest 包注释的口径): 套件的 can-i-use / set-nil 子测试会临时
// 占用进程级宿主槽并在结束后 SetNativeHost(nil) —— 本包其余用例都直接
// &win32Host{} 实例、不依赖单例, 不受影响; init 注册的全局宿主被拆卸不会
// 自动恢复, 介意的用例需自行重新注册。
func TestNativeHostContract(t *testing.T) {
	hosttest.RunNativeHostContract(t, contractHost{&win32Host{}})
}

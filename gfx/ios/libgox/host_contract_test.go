//go:build ios

package main

// ===== iOS 真实宿主的契约测试 =====
//
// 断言全部在平台无关的 gfx/hosttest 里 (套件说明与断言清单见其包注释), 这里
// 只做一件事: 把 iOS 真实宿主 (包级 nativeHost) 喂给它。
//
// 与 gfx/hostbridge/contract_test.go 的分工:
//
//	那一个  桌面可跑。从 Swift 源码解析真实能力清单, 装进桥接宿主后跑同一套
//	        断言 —— 验的是「声明表对不对」, 不需要设备。
//	这一个  真机/模拟器上跑。验的是「运行时对不对」: C 函数指针通道真的通、
//	        Swift 侧真的按协议回包。必须在 gox_set_native_host 之后跑。
//
// 副作用: 契约的「声明即可用」会以空参数 Call 每个声明的方法, 其中
// device.openSettings / camera.takePhoto / gallery.pick / app.share 会拉起系统
// UI —— 所以本测试**不适合无人值守的 CI**。要接进自动化时, 参照
// gfx/win32/host_contract_test.go 的 contractHost 用一个包装屏蔽这几个方法
// (只许屏蔽副作用, 不许用来掩盖 unsupported)。

import (
	"testing"

	"github.com/14752222/Gox/gfx/hosttest"
)

func TestNativeHostContract(t *testing.T) {
	hosttest.RunNativeHostContract(t, nativeHost)
}

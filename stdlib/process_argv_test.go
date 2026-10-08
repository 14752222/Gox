package stdlib

import (
	"os"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== process.argv 的宿主注入 (看板 r1q3Gy) =====
//
// 契约: process.argv 恒为 [可执行文件, 脚本路径, ...用户参数]。
//
// 源码跑 (`gox app.js a.log`) 时 os.Args 天然就是这个形状; 打包后的单文件
// exe 把脚本嵌进了二进制, os.Args = [exe, ...用户参数] —— 用户参数整体前移
// 一位。宿主 (packager 生成的 main.go) 用 SetProcessArgv 补齐脚本路径那一格,
// 两种跑法就统一了: 脚本里写 process.argv[2] 恒等于第一个用户参数。
//
// 本文件钉住这个"补齐"确实生效, 且不污染未注入时的既有行为。

// argvStrings 读出 process 模块导出表里 argv 的全部字符串项。
func argvStrings(t *testing.T) []string {
	t.Helper()
	exports, ok := object.LookupBuiltinModule("process")
	if !ok {
		t.Fatal(`内置模块 "process" 未注册`)
	}
	arr, isArr := exports["argv"].(*object.Array)
	if !isArr {
		t.Fatalf("process.argv 不是数组, 得到 %s", exports["argv"].Inspect())
	}
	out := make([]string, 0, len(arr.Elements))
	for _, el := range arr.Elements {
		s, isStr := el.(*object.String)
		if !isStr {
			t.Fatalf("process.argv[%d] 不是字符串: %s", len(out), el.Inspect())
		}
		out = append(out, s.Value)
	}
	return out
}

// restoreProcessArgv 隔离包级注入状态与模块缓存对其它用例的影响。
func restoreProcessArgv(t *testing.T) {
	t.Helper()
	saved := processArgvOverride
	t.Cleanup(func() {
		processArgvOverride = saved
		object.InvalidateBuiltinModuleCache("process")
	})
}

// 注入必须对 "process" 模块**可见** —— 光改动包级变量还不够, 模块的导出表
// 是被 builtinModuleCache 缓存的快照, 不清缓存脚本读到的仍是旧值。
func TestSetProcessArgvVisibleInProcessModule(t *testing.T) {
	restoreProcessArgv(t)

	// 先把模块导出表建出来 (任何先于注入的访问都会建 —— 这里刻意模拟),
	// 于是注入必须**连带清掉缓存**才能生效。
	if _, ok := object.LookupBuiltinModule("process"); !ok {
		t.Fatal(`内置模块 "process" 未注册`)
	}

	injected := []string{"/apps/log-viewer.exe", "/tmp/jsapp-1/main.js", "a.log", "--verbose"}
	SetProcessArgv(injected)

	got := argvStrings(t)
	if len(got) != len(injected) {
		t.Fatalf("process.argv = %v, 期望恰好 %v", got, injected)
	}
	for i, want := range injected {
		if got[i] != want {
			t.Fatalf("process.argv[%d] = %q, 期望 %q", i, got[i], want)
		}
	}

	// 打包产物补了脚本路径 ⇒ 用户参数恒从索引 2 起, 与源码跑同一口径。
	if got := argvStrings(t); len(got) < 3 || got[2] != "a.log" {
		t.Fatalf("用户参数应从索引 2 起, 实际 argv = %v", got)
	}
}

// 注入是**覆盖**而不是追加/累加: 反复调用不会把上一次的遗留并进来。
func TestSetProcessArgvReplacesPreviousInjection(t *testing.T) {
	restoreProcessArgv(t)

	SetProcessArgv([]string{"exe", "main.js", "first.log"})
	SetProcessArgv([]string{"exe", "main.js", "second.log"})

	got := argvStrings(t)
	if len(got) != 3 || got[2] != "second.log" {
		t.Fatalf("重复注入后 process.argv = %v, 期望 [exe main.js second.log]", got)
	}
}

// 未注入 (宿主从没调 SetProcessArgv) 时维持既有行为: process.argv = os.Args。
// 这条同时是"普通测试进程不会被注入状态带偏"的守卫。
func TestProcessArgvFallsBackToOSArgs(t *testing.T) {
	restoreProcessArgv(t)

	processArgvOverride = nil
	// 显式清缓存: 前面的用例可能已经把 process 模块建好了。
	object.InvalidateBuiltinModuleCache("process")

	got := argvStrings(t)
	if len(got) != len(os.Args) {
		t.Fatalf("未注入时 process.argv 长度 = %d, os.Args 长度 = %d", len(got), len(os.Args))
	}
	for i, want := range os.Args {
		if got[i] != want {
			t.Fatalf("未注入时 process.argv[%d] = %q, 期望 os.Args[%d] = %q", i, got[i], i, want)
		}
	}
}

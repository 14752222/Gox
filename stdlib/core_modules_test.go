package stdlib

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// 本文件钉住 "fs / path / http / process 既是全局对象、又是可 import 的内置模块"
// 这一契约。**为什么不在本包直接跑 `import fs from "fs"` 的 JS**: stdlib 不能
// 反向 import vm（vm → stdlib 的依赖方向），真正的 import 端到端断言在
// vm/vm_npm_test.go（TestBuiltinModuleDefaultImportFS 等）。这里覆盖注册表本身。

func TestCoreBuiltinModulesRegistered(t *testing.T) {
	cases := []struct {
		mod   string
		names []string
	}{
		{"fs", []string{"readFileSync", "writeFileSync", "readBytesSync", "existsSync", "statSync", "readdirSync", "mkdirSync", "rmSync", "renameSync", "copyFileSync"}},
		{"path", []string{"join", "resolve", "dirname", "basename", "extname", "isAbsolute", "sep", "delimiter"}},
		{"http", []string{"createServer", "get", "request"}},
		{"process", []string{"argv", "env", "cwd", "chdir", "exit", "platform", "pid"}},
	}
	for _, tc := range cases {
		exports, ok := object.LookupBuiltinModule(tc.mod)
		if !ok {
			t.Fatalf("内置模块 %q 未注册（import from %q 会失败）", tc.mod, tc.mod)
		}
		for _, name := range tc.names {
			if _, ok := exports[name]; !ok {
				t.Errorf("内置模块 %q 缺少导出 %q", tc.mod, name)
			}
		}
		// default = 模块命名空间对象，支撑 `import fs from "fs"`（Node 互操作）
		if _, ok := exports["default"]; !ok {
			t.Errorf("内置模块 %q 缺少 default —— `import x from %q` 会拿到 undefined", tc.mod, tc.mod)
		}
	}
}

// TestCoreGlobalsReuseModuleExports 断言全局对象与内置模块共用同一批实现
// （不是两份拷贝），且全局对象不暴露 import 专用的 default。
func TestCoreGlobalsReuseModuleExports(t *testing.T) {
	env := SetupGlobals()
	for _, mod := range []string{"fs", "path", "http", "process"} {
		exports, _ := object.LookupBuiltinModule(mod)
		global, ok := env.Get(mod)
		if !ok {
			t.Fatalf("全局 %q 未声明", mod)
		}
		obj, ok := global.(*object.Object)
		if !ok {
			t.Fatalf("全局 %q 不是对象: %T", mod, global)
		}
		// 任取一个导出，确认是同一个函数对象（复用而非各建一份）
		for name, want := range exports {
			if name == "default" {
				continue
			}
			got, found := obj.GetProperty(name)
			if !found {
				t.Errorf("全局 %q 缺少 %q", mod, name)
				continue
			}
			if got != want {
				t.Errorf("全局 %q 的 %q 与内置模块导出不是同一对象（实现被复制了）", mod, name)
			}
			break
		}
		if _, found := obj.GetProperty("default"); found {
			t.Errorf("全局 %q 不应暴露 default（default 只服务 import）", mod)
		}
	}
}

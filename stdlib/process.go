package stdlib

import (
	"os"
	goruntime "runtime"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// setupProcess 注册全局 process 对象 (Node 风格的最小子集)。
//
// 提供脚本定位文件 (cwd)、读取环境变量与退出进程的能力，
// 与 fs / http 模块配合构成可用的脚本运行环境:
//
//	process.argv     — 命令行参数 ([可执行文件, 脚本路径, ...])
//	process.env      — 环境变量快照对象
//	process.cwd()    — 当前工作目录
//	process.chdir()  — 切换工作目录
//	process.exit(n)  — 立即退出进程 (不等待未完成的定时器，与 Node 一致)
//	process.platform — 宿主平台 ("windows" / "linux" / "darwin")
//	process.pid      — 进程 ID
//
// 实现真源见下面 init() 注册的内置模块 "process"：全局 process 与
// `import process from "process"` 共用同一批函数与同一份进程级快照。
//
// v1 边界: process.env 是**进程级快照**（首次构建内置模块时取一次），
// 不是 Node 那样的实时视图 —— 脚本运行中再 setenv 不会反映进来。这与
// "process 是进程单例" 的模型一致，也避免多个 VM 各持一份互相矛盾的快照。
func setupProcess(env *runtime.Environment) {
	exports, ok := object.LookupBuiltinModule("process")
	if !ok {
		panic(`stdlib: builtin module "process" is not registered`)
	}
	p := object.NewObject()
	for _, name := range sortedBuiltinExportNames(exports) {
		if name == "default" {
			continue
		}
		p.SetProperty(name, exports[name])
	}
	env.Declare("process", p, false)
}

func init() {
	object.RegisterBuiltinModule("process", func() map[string]object.Value {
		exports := map[string]object.Value{
			"argv": processArgv(),
			"env":  processEnvSnapshot(),
			"platform": object.NewString(goruntime.GOOS),
			"pid":      object.NewNumber(float64(os.Getpid())),

			"cwd": object.NewBuiltin("cwd", func(...object.Value) object.Value {
				dir, err := os.Getwd()
				if err != nil {
					return object.NewError("process.cwd: " + err.Error())
				}
				return object.NewString(dir)
			}),

			"chdir": object.NewBuiltin("chdir", func(args ...object.Value) object.Value {
				if len(args) < 1 {
					return object.NewTypeError("process.chdir: missing directory argument")
				}
				if err := os.Chdir(toStr(args[0])); err != nil {
					return object.NewError("process.chdir: " + err.Error())
				}
				return object.UndefinedSingleton
			}),

			"exit": object.NewBuiltin("exit", func(args ...object.Value) object.Value {
				code := 0
				if len(args) > 0 {
					code = int(toFloat(args[0]))
				}
				os.Exit(code)
				return object.UndefinedSingleton
			}),
		}

		// Node 互操作（v1 边界）: 默认导出 = 模块命名空间对象。
		exports["default"] = builtinNamespaceObject(exports)
		return exports
	})
}

// processArgv 构造 process.argv 数组（[可执行文件, 脚本路径, ...]）。
func processArgv() *object.Array {
	argv := make([]object.Value, len(os.Args))
	for i, a := range os.Args {
		argv[i] = object.NewString(a)
	}
	return object.NewArray(argv)
}

// processEnvSnapshot 取一份环境变量快照为 JS 对象。
func processEnvSnapshot() *object.Object {
	snap := object.NewObject()
	for _, kv := range os.Environ() {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				snap.SetProperty(kv[:i], object.NewString(kv[i+1:]))
				break
			}
		}
	}
	return snap
}

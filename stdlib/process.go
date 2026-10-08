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

// processArgvOverride 由打包宿主注入的 process.argv 来源 (见 SetProcessArgv)。
// 零值 (nil) 表示"未注入", process.argv 维持取 os.Args 的既有行为。
var processArgvOverride []string

// SetProcessArgv 让宿主显式指定 process.argv 的来源。
//
// 为什么需要它 (r1q3Gy): 打包后的单文件 exe **没有**「脚本路径」这一个宿主
// 参数 —— 脚本已经嵌进二进制了, 所以 os.Args = [exe, ...用户参数]，用户参数
// 从索引 1 开始；而源码跑 (`gox app.js a b`) 时 os.Args =
// [gox, app.js, a, b]，用户参数从索引 2 开始。同一个脚本用两种跑法会读到
// 不同的东西 —— 打包版拿不到参数, 于是「双击/拖拽打开文件」这类桌面基本预期
// 落空 (apps/log-viewer 因此只能退到"打开…"按钮)。
//
// 宿主把自己的 argv 补齐成与 `gox <file> <args>` 同一口径的
// [可执行文件, 脚本路径, ...用户参数] 再注入进来, 两种跑法就统一了 —— 文档
// 只需写一套, 脚本也只需一套写法。
//
// 时序: 必须在**第一次**构造 "process" 模块之前调用 (本函数会一并丢弃该模块的
// 导出表缓存, 之后取用的一定是注入值)。已经在跑的 VM 已持有的 process 对象
// 不受影响 —— 这是刻意的: process 本就是进程级快照语义, 不该 runtime 变更。
func SetProcessArgv(argv []string) {
	processArgvOverride = append([]string(nil), argv...)
	object.InvalidateBuiltinModuleCache("process")
}

// processArgv 构造 process.argv 数组（[可执行文件, 脚本路径, ...]）。
func processArgv() *object.Array {
	src := processArgvOverride
	if len(src) == 0 {
		src = os.Args
	}
	argv := make([]object.Value, len(src))
	for i, a := range src {
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

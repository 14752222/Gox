package stdlib

import (
	"os"
	goruntime "runtime"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// setupProcess 注册 process 全局对象 (Node 风格的最小子集)。
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
func setupProcess(env *runtime.Environment) {
	p := object.NewObject()

	argv := make([]object.Value, len(os.Args))
	for i, a := range os.Args {
		argv[i] = object.NewString(a)
	}
	p.SetProperty("argv", object.NewArray(argv))

	envSnapshot := object.NewObject()
	for _, kv := range os.Environ() {
		for i := 0; i < len(kv); i++ {
			if kv[i] == '=' {
				envSnapshot.SetProperty(kv[:i], object.NewString(kv[i+1:]))
				break
			}
		}
	}
	p.SetProperty("env", envSnapshot)

	p.SetProperty("platform", object.NewString(goruntime.GOOS))
	p.SetProperty("pid", object.NewNumber(float64(os.Getpid())))

	p.SetProperty("cwd", object.NewBuiltin("cwd", func(...object.Value) object.Value {
		dir, err := os.Getwd()
		if err != nil {
			return object.NewError("process.cwd: " + err.Error())
		}
		return object.NewString(dir)
	}))

	p.SetProperty("chdir", object.NewBuiltin("chdir", func(args ...object.Value) object.Value {
		if len(args) < 1 {
			return object.NewTypeError("process.chdir: missing directory argument")
		}
		if err := os.Chdir(toStr(args[0])); err != nil {
			return object.NewError("process.chdir: " + err.Error())
		}
		return object.UndefinedSingleton
	}))

	p.SetProperty("exit", object.NewBuiltin("exit", func(args ...object.Value) object.Value {
		code := 0
		if len(args) > 0 {
			code = int(toFloat(args[0]))
		}
		os.Exit(code)
		return object.UndefinedSingleton
	}))

	env.Declare("process", p, false)
}

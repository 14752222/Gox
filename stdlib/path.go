package stdlib

import (
	"os"
	"path/filepath"
	"strings"

	"js-runtime/object"
	"js-runtime/runtime"
)

// setupPath 注册 path 路径工具模块。
//
// 基于 Go 的 filepath 实现，分隔符跟随宿主系统 (Windows 上是 \)，
// 但输入中的 / 在所有平台上都被接受。语义与 Node 的 path 模块对齐:
// extname 对隐藏文件 (".bashrc") 返回 ""，basename 去除尾部分隔符。
func setupPath(env *runtime.Environment) {
	p := object.NewObject()

	// path.join(...parts) — 拼接并规范化路径
	p.SetProperty("join", object.NewBuiltin("join", func(args ...object.Value) object.Value {
		parts := make([]string, 0, len(args))
		for _, a := range args {
			parts = append(parts, toStr(a))
		}
		return object.NewString(filepath.Join(parts...))
	}))

	// path.resolve(...parts) — 拼接后解析为绝对路径 (相对部分基于当前工作目录)
	p.SetProperty("resolve", object.NewBuiltin("resolve", func(args ...object.Value) object.Value {
		parts := make([]string, 0, len(args))
		for _, a := range args {
			parts = append(parts, toStr(a))
		}
		abs, err := filepath.Abs(filepath.Join(parts...))
		if err != nil {
			return object.NewError("path.resolve: " + err.Error())
		}
		return object.NewString(abs)
	}))

	// path.dirname(p) — 目录部分
	p.SetProperty("dirname", object.NewBuiltin("dirname", func(args ...object.Value) object.Value {
		path, errVal := pathStringArg(args, "dirname")
		if errVal != nil {
			return errVal
		}
		return object.NewString(filepath.Dir(path))
	}))

	// path.basename(p) — 最后一段文件名
	p.SetProperty("basename", object.NewBuiltin("basename", func(args ...object.Value) object.Value {
		path, errVal := pathStringArg(args, "basename")
		if errVal != nil {
			return errVal
		}
		return object.NewString(filepath.Base(path))
	}))

	// path.extname(p) — 扩展名 (含点)。与 Go 的 filepath.Ext 不同:
	// 隐藏文件 (".bashrc") 与无扩展名文件返回 ""，"index." 返回 ""，
	// 与 Node 语义一致。
	p.SetProperty("extname", object.NewBuiltin("extname", func(args ...object.Value) object.Value {
		path, errVal := pathStringArg(args, "extname")
		if errVal != nil {
			return errVal
		}
		base := strings.TrimRight(filepath.Base(path), ".")
		dot := strings.LastIndexByte(base, '.')
		if dot <= 0 {
			// 无点，或点在首字符 (隐藏文件)
			return object.NewString("")
		}
		return object.NewString(base[dot:])
	}))

	// path.isAbsolute(p)
	p.SetProperty("isAbsolute", object.NewBuiltin("isAbsolute", func(args ...object.Value) object.Value {
		path, errVal := pathStringArg(args, "isAbsolute")
		if errVal != nil {
			return errVal
		}
		return object.NewBoolean(filepath.IsAbs(path))
	}))

	// path.sep — 路径分隔符; path.delimiter — PATH 环境变量分隔符
	p.SetProperty("sep", object.NewString(string(filepath.Separator)))
	p.SetProperty("delimiter", object.NewString(string(os.PathListSeparator)))

	env.Declare("path", p, false)
}

// pathStringArg 校验唯一的路径参数是字符串。
func pathStringArg(args []object.Value, op string) (string, object.Value) {
	if len(args) == 0 {
		return "", object.NewTypeError("path.%s: missing path argument", op)
	}
	s, ok := args[0].(*object.String)
	if !ok {
		return "", object.NewTypeError("path.%s: path must be a string, got %s", op, object.TypeOf(args[0]))
	}
	return s.Value, nil
}

package stdlib

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// setupPath 注册全局 path 对象。
//
// 基于 Go 的 filepath 实现，分隔符跟随宿主系统 (Windows 上是 \)，
// 但输入中的 / 在所有平台上都被接受。语义与 Node 的 path 模块对齐:
// extname 对隐藏文件 (".bashrc") 返回 ""，basename 去除尾部分隔符。
//
// 实现真源见下面 init() 注册的内置模块 "path"：全局 path 与
// `import path from "path"` 共用同一批函数对象。
func setupPath(env *runtime.Environment) {
	exports, ok := object.LookupBuiltinModule("path")
	if !ok {
		panic(`stdlib: builtin module "path" is not registered`)
	}
	p := object.NewObject()
	for _, name := range sortedBuiltinExportNames(exports) {
		if name == "default" {
			continue
		}
		p.SetProperty(name, exports[name])
	}
	env.Declare("path", p, false)
}

func init() {
	object.RegisterBuiltinModule("path", func() map[string]object.Value {
		exports := map[string]object.Value{
			// path.join(...parts) — 拼接并规范化路径
			"join": object.NewBuiltin("join", func(args ...object.Value) object.Value {
				parts := make([]string, 0, len(args))
				for _, a := range args {
					parts = append(parts, toStr(a))
				}
				return object.NewString(filepath.Join(parts...))
			}),

			// path.resolve(...parts) — 拼接后解析为绝对路径 (相对部分基于当前工作目录)
			"resolve": object.NewBuiltin("resolve", func(args ...object.Value) object.Value {
				parts := make([]string, 0, len(args))
				for _, a := range args {
					parts = append(parts, toStr(a))
				}
				abs, err := filepath.Abs(filepath.Join(parts...))
				if err != nil {
					return object.NewError("path.resolve: " + err.Error())
				}
				return object.NewString(abs)
			}),

			// path.dirname(p) — 目录部分
			"dirname": object.NewBuiltin("dirname", func(args ...object.Value) object.Value {
				path, errVal := pathStringArg(args, "dirname")
				if errVal != nil {
					return errVal
				}
				return object.NewString(filepath.Dir(path))
			}),

			// path.basename(p) — 最后一段文件名
			"basename": object.NewBuiltin("basename", func(args ...object.Value) object.Value {
				path, errVal := pathStringArg(args, "basename")
				if errVal != nil {
					return errVal
				}
				return object.NewString(filepath.Base(path))
			}),

			// path.extname(p) — 扩展名 (含点)。与 Go 的 filepath.Ext 不同:
			// 隐藏文件 (".bashrc") 与无扩展名文件返回 ""，"index." 返回 ""，
			// 与 Node 语义一致。
			"extname": object.NewBuiltin("extname", func(args ...object.Value) object.Value {
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
			}),

			// path.isAbsolute(p)
			"isAbsolute": object.NewBuiltin("isAbsolute", func(args ...object.Value) object.Value {
				path, errVal := pathStringArg(args, "isAbsolute")
				if errVal != nil {
					return errVal
				}
				return object.NewBoolean(filepath.IsAbs(path))
			}),

			// path.sep — 路径分隔符; path.delimiter — PATH 环境变量分隔符
			"sep":       object.NewString(string(filepath.Separator)),
			"delimiter": object.NewString(string(os.PathListSeparator)),
		}

		// Node 互操作（v1 边界）: 默认导出 = 模块命名空间对象。
		exports["default"] = builtinNamespaceObject(exports)
		return exports
	})
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

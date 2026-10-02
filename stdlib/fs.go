package stdlib

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"sort"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// ===== 核心模块（fs/path/http/process）共用的辅助 =====
//
// 这四个模块既是全局对象（env.Declare），又是可 import 的内置模块。
// 为避免"全局一套实现、import 又一套实现"慢慢漂移，实现的唯一真源是各文件
// 里 init() 注册的内置模块导出表；全局对象只是复用同一批函数、另建一层外壳。

// builtinNamespaceObject 把内置模块的命名导出表包成一个普通对象，用作
// Node 互操作意义上的"默认导出"（`import fs from "fs"`）。
// 必须在把 "default" 写进 exports 之前调用，否则会自引用。
func builtinNamespaceObject(exports map[string]object.Value) *object.Object {
	obj := object.NewObject()
	for name, val := range exports {
		obj.SetProperty(name, val)
	}
	return obj
}

// sortedBuiltinExportNames 返回导出名（字典序）。导出表是 map，迭代顺序随机；
// 构造全局对象时排序一次，保证 Object.keys(fs) 稳定可预期。
func sortedBuiltinExportNames(exports map[string]object.Value) []string {
	names := make([]string, 0, len(exports))
	for name := range exports {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// setupFS 注册全局 fs 对象。
//
// 提供同步与异步两套 API:
//   - 同步: fs.readFileSync / writeFileSync / statSync / readdirSync ...
//     失败时返回 *object.Error，VM 会将其作为 JS 异常抛出。
//   - 异步: fs.readFile / writeFile / appendFile / readdir ...
//     最后一个参数是回调时走 callback(err, data) 风格，否则返回 Promise。
//
// 异步的实现模型: 工作在事件循环主线程上、下一个 tick (0ms 定时器) 执行。
// 注册定时器发生在 fs.readFile 返回之前，因此事件循环在任务完成前不会
// 退出。相比 goroutine 方案牺牲了真实并行 I/O，换来确定性的回调顺序
// (FIFO) 与单线程安全 —— 回调里访问 VM 状态无需加锁。
//
// 实现真源见下面 init() 注册的内置模块 "fs"；这里只负责把同一批函数挂到
// 全局对象上（脚本不开模块也能直接用 fs），因此 `fs.readFileSync(...)` 与
// `import fs from "fs"; fs.readFileSync(...)` 是同一份实现。
func setupFS(env *runtime.Environment) {
	exports, ok := object.LookupBuiltinModule("fs")
	if !ok {
		// 不可达: init() 必先于任何 SetupGlobals 调用执行。显式 panic 好过
		// 静默造出一个空 fs 全局对象（脚本会在很远的地方报
		// "readFileSync is not a function"，根因难查）。
		panic(`stdlib: builtin module "fs" is not registered`)
	}
	fs := object.NewObject()
	for _, name := range sortedBuiltinExportNames(exports) {
		if name == "default" {
			continue // default 只服务 `import fs from "fs"`，不进全局对象
		}
		fs.SetProperty(name, exports[name])
	}
	env.Declare("fs", fs, false)
}

func init() {
	object.RegisterBuiltinModule("fs", func() map[string]object.Value {
		exports := map[string]object.Value{
			// ===== 读 =====

			// fs.readFileSync(path, encoding?)
			// encoding: "utf8"/"utf-8" (默认) → 字符串; "base64"/"hex" → 编码字符串
			"readFileSync": object.NewBuiltin("readFileSync", func(args ...object.Value) object.Value {
				path, errVal := fsPathArg(args, 0, "readFileSync")
				if errVal != nil {
					return errVal
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return fsError("readFileSync", path, err)
				}
				return fsDecodeBytes(string(data), fsEncoding(args, 1))
			}),

			// fs.readFile(path, encoding?, callback?)
			"readFile": object.NewBuiltin("readFile", func(args ...object.Value) object.Value {
				path, errVal := fsPathArg(args, 0, "readFile")
				if errVal != nil {
					return errVal
				}
				enc := fsEncoding(args, 1)
				cb := lastCallback(args)
				return fsSchedule("readFile", cb, func() object.Value {
					data, err := os.ReadFile(path)
					if err != nil {
						return fsError("readFile", path, err)
					}
					return fsDecodeBytes(string(data), enc)
				})
			}),

			// fs.readBytesSync(path) — 二进制读取，返回字节数组 (0-255 的数字)
			"readBytesSync": object.NewBuiltin("readBytesSync", func(args ...object.Value) object.Value {
				path, errVal := fsPathArg(args, 0, "readBytesSync")
				if errVal != nil {
					return errVal
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return fsError("readBytesSync", path, err)
				}
				return fsBytesToArray(data)
			}),

			// ===== 写 =====

			// fs.writeFileSync(path, data, encoding?)
			// data: 字符串 (按 encoding 解码，默认 utf8) 或字节数组
			"writeFileSync": object.NewBuiltin("writeFileSync", func(args ...object.Value) object.Value {
				path, data, errVal := fsWriteArgs(args, "writeFileSync")
				if errVal != nil {
					return errVal
				}
				if err := os.WriteFile(path, data, 0644); err != nil {
					return fsError("writeFileSync", path, err)
				}
				return object.UndefinedSingleton
			}),

			// fs.writeFile(path, data, encoding?, callback?)
			"writeFile": object.NewBuiltin("writeFile", func(args ...object.Value) object.Value {
				path, data, errVal := fsWriteArgs(args, "writeFile")
				if errVal != nil {
					return errVal
				}
				cb := lastCallback(args)
				return fsSchedule("writeFile", cb, func() object.Value {
					if err := os.WriteFile(path, data, 0644); err != nil {
						return fsError("writeFile", path, err)
					}
					return object.UndefinedSingleton
				})
			}),

			// fs.appendFileSync(path, data)
			"appendFileSync": object.NewBuiltin("appendFileSync", func(args ...object.Value) object.Value {
				path, data, errVal := fsWriteArgs(args, "appendFileSync")
				if errVal != nil {
					return errVal
				}
				f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
				if err != nil {
					return fsError("appendFileSync", path, err)
				}
				defer f.Close()
				if _, err := f.Write(data); err != nil {
					return fsError("appendFileSync", path, err)
				}
				return object.UndefinedSingleton
			}),

			// fs.appendFile(path, data, callback?)
			"appendFile": object.NewBuiltin("appendFile", func(args ...object.Value) object.Value {
				path, data, errVal := fsWriteArgs(args, "appendFile")
				if errVal != nil {
					return errVal
				}
				cb := lastCallback(args)
				return fsSchedule("appendFile", cb, func() object.Value {
					f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
					if err != nil {
						return fsError("appendFile", path, err)
					}
					defer f.Close()
					if _, err := f.Write(data); err != nil {
						return fsError("appendFile", path, err)
					}
					return object.UndefinedSingleton
				})
			}),

			// ===== 目录与元信息 =====

			// fs.existsSync(path)
			"existsSync": object.NewBuiltin("existsSync", func(args ...object.Value) object.Value {
				path, errVal := fsPathArg(args, 0, "existsSync")
				if errVal != nil {
					return errVal
				}
				_, err := os.Stat(path)
				return object.NewBoolean(err == nil)
			}),

			// fs.statSync(path) → { size, mtimeMs, isFile, isDirectory }
			"statSync": object.NewBuiltin("statSync", func(args ...object.Value) object.Value {
				path, errVal := fsPathArg(args, 0, "statSync")
				if errVal != nil {
					return errVal
				}
				info, err := os.Stat(path)
				if err != nil {
					return fsError("statSync", path, err)
				}
				return fsStatObject(info)
			}),

			// fs.stat(path, callback?)
			"stat": object.NewBuiltin("stat", func(args ...object.Value) object.Value {
				path, errVal := fsPathArg(args, 0, "stat")
				if errVal != nil {
					return errVal
				}
				cb := lastCallback(args)
				return fsSchedule("stat", cb, func() object.Value {
					info, err := os.Stat(path)
					if err != nil {
						return fsError("stat", path, err)
					}
					return fsStatObject(info)
				})
			}),

			// fs.readdirSync(path) → 文件名数组 (按名称排序)
			"readdirSync": object.NewBuiltin("readdirSync", func(args ...object.Value) object.Value {
				path, errVal := fsPathArg(args, 0, "readdirSync")
				if errVal != nil {
					return errVal
				}
				entries, err := os.ReadDir(path)
				if err != nil {
					return fsError("readdirSync", path, err)
				}
				return fsEntriesToArray(entries)
			}),

			// fs.readdir(path, callback?)
			"readdir": object.NewBuiltin("readdir", func(args ...object.Value) object.Value {
				path, errVal := fsPathArg(args, 0, "readdir")
				if errVal != nil {
					return errVal
				}
				cb := lastCallback(args)
				return fsSchedule("readdir", cb, func() object.Value {
					entries, err := os.ReadDir(path)
					if err != nil {
						return fsError("readdir", path, err)
					}
					return fsEntriesToArray(entries)
				})
			}),

			// fs.mkdirSync(path, options?) — options 为 {recursive:true} 或 true
			"mkdirSync": object.NewBuiltin("mkdirSync", func(args ...object.Value) object.Value {
				path, errVal := fsPathArg(args, 0, "mkdirSync")
				if errVal != nil {
					return errVal
				}
				if err := fsMkdir(path, fsRecursiveArg(args)); err != nil {
					return fsError("mkdirSync", path, err)
				}
				return object.UndefinedSingleton
			}),

			// fs.mkdir(path, options?, callback?)
			"mkdir": object.NewBuiltin("mkdir", func(args ...object.Value) object.Value {
				path, errVal := fsPathArg(args, 0, "mkdir")
				if errVal != nil {
					return errVal
				}
				recursive := fsRecursiveArg(args)
				cb := lastCallback(args)
				return fsSchedule("mkdir", cb, func() object.Value {
					if err := fsMkdir(path, recursive); err != nil {
						return fsError("mkdir", path, err)
					}
					return object.UndefinedSingleton
				})
			}),

			// ===== 删除 / 移动 / 复制 =====

			// fs.unlinkSync(path) — 删除文件
			"unlinkSync": object.NewBuiltin("unlinkSync", func(args ...object.Value) object.Value {
				path, errVal := fsPathArg(args, 0, "unlinkSync")
				if errVal != nil {
					return errVal
				}
				if err := os.Remove(path); err != nil {
					return fsError("unlinkSync", path, err)
				}
				return object.UndefinedSingleton
			}),

			// fs.unlink(path, callback?)
			"unlink": object.NewBuiltin("unlink", func(args ...object.Value) object.Value {
				path, errVal := fsPathArg(args, 0, "unlink")
				if errVal != nil {
					return errVal
				}
				cb := lastCallback(args)
				return fsSchedule("unlink", cb, func() object.Value {
					if err := os.Remove(path); err != nil {
						return fsError("unlink", path, err)
					}
					return object.UndefinedSingleton
				})
			}),

			// fs.rmdirSync(path) — 删除空目录
			"rmdirSync": object.NewBuiltin("rmdirSync", func(args ...object.Value) object.Value {
				path, errVal := fsPathArg(args, 0, "rmdirSync")
				if errVal != nil {
					return errVal
				}
				if err := os.Remove(path); err != nil {
					return fsError("rmdirSync", path, err)
				}
				return object.UndefinedSingleton
			}),

			// fs.rmSync(path, options?) — options: {recursive, force}
			// recursive 时删除目录及其全部内容 (os.RemoveAll)；force 时忽略不存在的路径
			"rmSync": object.NewBuiltin("rmSync", func(args ...object.Value) object.Value {
				path, errVal := fsPathArg(args, 0, "rmSync")
				if errVal != nil {
					return errVal
				}
				recursive, force := fsRmArgs(args)
				if err := fsRm(path, recursive, force); err != nil {
					return fsError("rmSync", path, err)
				}
				return object.UndefinedSingleton
			}),

			// fs.renameSync(oldPath, newPath)
			"renameSync": object.NewBuiltin("renameSync", func(args ...object.Value) object.Value {
				oldPath, errVal := fsPathArg(args, 0, "renameSync")
				if errVal != nil {
					return errVal
				}
				newPath, errVal := fsPathArg(args, 1, "renameSync")
				if errVal != nil {
					return errVal
				}
				if err := os.Rename(oldPath, newPath); err != nil {
					return fsError("renameSync", oldPath, err)
				}
				return object.UndefinedSingleton
			}),

			// fs.copyFileSync(src, dest)
			"copyFileSync": object.NewBuiltin("copyFileSync", func(args ...object.Value) object.Value {
				src, errVal := fsPathArg(args, 0, "copyFileSync")
				if errVal != nil {
					return errVal
				}
				dest, errVal := fsPathArg(args, 1, "copyFileSync")
				if errVal != nil {
					return errVal
				}
				if err := copyFile(src, dest); err != nil {
					return fsError("copyFileSync", src, err)
				}
				return object.UndefinedSingleton
			}),
		}

		// Node 互操作（v1 边界）: 默认导出 = 模块命名空间对象，使
		// `import fs from "fs"` 与 `import * as fs from "fs"` 等价。
		// 注意这不是 CommonJS 的 module.exports 互操作 —— Gox v0 不做 CJS，
		// 这里只是给默认导入一个落点，`require("fs")` 依旧不可用。
		exports["default"] = builtinNamespaceObject(exports)
		return exports
	})
}

// ===== 参数解析辅助 =====

// fsPathArg 取第 index 个参数并转为路径字符串。
// 缺失或非字符串时返回 TypeError。
func fsPathArg(args []object.Value, index int, op string) (string, object.Value) {
	if index >= len(args) {
		return "", object.NewTypeError("fs.%s: missing path argument", op)
	}
	s, ok := args[index].(*object.String)
	if !ok {
		return "", object.NewTypeError("fs.%s: path must be a string, got %s", op, object.TypeOf(args[index]))
	}
	return s.Value, nil
}

// fsEncoding 取指定位置的编码参数 ("utf8"/"base64"/"hex")，默认 utf8。
// 编码参数可能在位置 1 (readFileSync(p)) 或 2 (readFile(p, cb)) ——
// 逐个扫描字符串参数即可，跳过回调所在位置。
func fsEncoding(args []object.Value, index int) string {
	if index < len(args) {
		if s, ok := args[index].(*object.String); ok {
			return fsNormalizeEncoding(s.Value)
		}
	}
	return "utf8"
}

// fsNormalizeEncoding 归一化编码名。未知编码按 utf8 处理。
func fsNormalizeEncoding(name string) string {
	switch name {
	case "utf-8", "UTF-8", "utf8", "UTF8", "":
		return "utf8"
	case "base64", "Base64":
		return "base64"
	case "hex", "HEX":
		return "hex"
	}
	return "utf8"
}

// lastCallback 取参数列表末尾的可调用对象作为回调，没有则返回 nil。
// fs 与 http 模块共用的回调风格 API 约定: 可调用参数出现在参数列表末尾。
func lastCallback(args []object.Value) object.Value {
	if len(args) > 0 && object.IsCallable(args[len(args)-1]) {
		return args[len(args)-1]
	}
	return nil
}

// fsRecursiveArg 解析 mkdir 的 recursive 选项 (布尔或 {recursive:true})。
func fsRecursiveArg(args []object.Value) bool {
	for _, a := range args[1:] {
		switch v := a.(type) {
		case *object.Boolean:
			if v.Value {
				return true
			}
		case *object.Object:
			if rv, found := v.GetProperty("recursive"); found {
				if b, ok := rv.(*object.Boolean); ok && b.Value {
					return true
				}
			}
		}
	}
	return false
}

// fsRmArgs 解析 rmSync 的 {recursive, force} 选项。
func fsRmArgs(args []object.Value) (recursive, force bool) {
	if len(args) < 2 {
		return false, false
	}
	opts, ok := args[1].(*object.Object)
	if !ok {
		return false, false
	}
	if rv, found := opts.GetProperty("recursive"); found {
		if b, ok := rv.(*object.Boolean); ok {
			recursive = b.Value
		}
	}
	if fv, found := opts.GetProperty("force"); found {
		if b, ok := fv.(*object.Boolean); ok {
			force = b.Value
		}
	}
	return recursive, force
}

// fsWriteArgs 解析写操作的公共参数 (path, data, 可选编码)。
// data 为字符串时按 encoding 解码为字节；为数组时逐元素取低 8 位。
func fsWriteArgs(args []object.Value, op string) (string, []byte, object.Value) {
	path, errVal := fsPathArg(args, 0, op)
	if errVal != nil {
		return "", nil, errVal
	}
	if len(args) < 2 {
		return "", nil, object.NewTypeError("fs.%s: missing data argument", op)
	}
	switch v := args[1].(type) {
	case *object.String:
		data, derr := fsEncodeString(v.Value, fsEncoding(args, 2))
		if derr != nil {
			return "", nil, object.NewError(fmt.Sprintf("fs.%s: %v", op, derr))
		}
		return path, data, nil
	case *object.Array:
		return path, fsArrayToBytes(v), nil
	}
	return "", nil, object.NewTypeError("fs.%s: data must be a string or byte array, got %s",
		op, object.TypeOf(args[1]))
}

// ===== 值转换辅助 =====

// fsDecodeBytes 把原始字节按编码转为 JS 值。
func fsDecodeBytes(data, encoding string) object.Value {
	switch encoding {
	case "base64":
		return object.NewString(base64.StdEncoding.EncodeToString([]byte(data)))
	case "hex":
		return object.NewString(hex.EncodeToString([]byte(data)))
	}
	return object.NewString(data)
}

// fsEncodeString 把字符串按编码解码为字节 (写文件用)。
func fsEncodeString(s, encoding string) ([]byte, error) {
	switch encoding {
	case "base64":
		return base64.StdEncoding.DecodeString(s)
	case "hex":
		return hex.DecodeString(s)
	}
	return []byte(s), nil
}

// fsBytesToArray 把 Go 字节切片转为 JS 数字数组。
func fsBytesToArray(data []byte) *object.Array {
	elements := make([]object.Value, len(data))
	for i, b := range data {
		elements[i] = object.NewNumber(float64(b))
	}
	return object.NewArray(elements)
}

// fsArrayToBytes 把 JS 数字数组转为 Go 字节切片 (每元素取低 8 位)。
func fsArrayToBytes(arr *object.Array) []byte {
	data := make([]byte, len(arr.Elements))
	for i, e := range arr.Elements {
		data[i] = byte(int(toFloat(e)))
	}
	return data
}

// fsStatObject 把 os.FileInfo 转为 JS 对象。
func fsStatObject(info os.FileInfo) *object.Object {
	st := object.NewObject()
	st.SetProperty("size", object.NewNumber(float64(info.Size())))
	st.SetProperty("mtimeMs", object.NewNumber(float64(info.ModTime().UnixMilli())))
	st.SetProperty("isFile", object.NewBoolean(info.Mode().IsRegular()))
	st.SetProperty("isDirectory", object.NewBoolean(info.IsDir()))
	return st
}

// fsEntriesToArray 把目录项列表转为文件名数组。
func fsEntriesToArray(entries []os.DirEntry) *object.Array {
	elements := make([]object.Value, len(entries))
	for i, e := range entries {
		elements[i] = object.NewString(e.Name())
	}
	return object.NewArray(elements)
}

// ===== 工作函数 =====

// fsError 构造带操作名与路径的 fs 错误。
func fsError(op, path string, err error) *object.Error {
	return object.NewError(fmt.Sprintf("fs.%s %q: %v", op, path, err))
}

// fsMkdir 按 recursive 选项创建目录。
func fsMkdir(path string, recursive bool) error {
	if recursive {
		return os.MkdirAll(path, 0755)
	}
	return os.Mkdir(path, 0755)
}

// fsRm 按 recursive/force 选项删除路径。
func fsRm(path string, recursive, force bool) error {
	if recursive {
		// RemoveAll 对不存在的路径返回 nil，与 force 语义一致
		return os.RemoveAll(path)
	}
	err := os.Remove(path)
	if err != nil && force && os.IsNotExist(err) {
		return nil
	}
	return err
}

// copyFile 复制文件内容与权限位。
func copyFile(src, dest string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dest, data, info.Mode().Perm())
}

// ===== 异步调度 =====

// fsSchedule 把一个 fs 工作函数安排到下一个 tick 执行。
//
// cb 为 nil 时返回 Promise (resolve 结果 / reject 错误)；
// cb 非 nil 时返回 undefined，完成或失败后以 (err, result) 调用回调，
// err 成功时为 null。工作函数返回 *object.Error 表示失败。
func fsSchedule(op string, cb object.Value, work func() object.Value) object.Value {
	if cb == nil {
		result := object.NewPromise()
		object.GlobalScheduler().SetTimeout(object.NewBuiltin("__fs_"+op+"_task", func(...object.Value) object.Value {
			res := work()
			if errObj, isErr := res.(*object.Error); isErr {
				result.Reject(errObj)
				return object.UndefinedSingleton
			}
			result.Resolve(res)
			// Resolve 会同步驱动 await 恢复链; 恢复链里的未捕获异常
			// 在这里消费掉，避免残留到之后的内建函数调用点被误抛出。
			if cbErr := object.TakeCallbackError(); cbErr != nil {
				reportUncaught(cbErr)
			}
			return object.UndefinedSingleton
		}), 0)
		return result
	}
	object.GlobalScheduler().SetTimeout(object.NewBuiltin("__fs_"+op+"_task", func(...object.Value) object.Value {
		res := work()
		if errObj, isErr := res.(*object.Error); isErr {
			object.CallFunction(cb, nil, errObj, object.UndefinedSingleton)
		} else {
			object.CallFunction(cb, nil, object.NullSingleton, res)
		}
		// 回调抛出的异常必须在这里消费掉 (报告而非传播)，否则会残留到
		// 之后任意一个内建函数调用点被误抛出。
		if cbErr := object.TakeCallbackError(); cbErr != nil {
			reportUncaught(cbErr)
		}
		return object.UndefinedSingleton
	}), 0)
	return object.UndefinedSingleton
}

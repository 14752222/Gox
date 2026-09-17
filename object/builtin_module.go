package object

// 内置模块注册表。
//
// 让宿主以 "gx/xxx" 这类非相对路径名字注册模块, VM 的模块加载在读文件
// 系统之前先查这里。注册方 (stdlib) 与使用方 (vm) 都已依赖 object 包,
// 注册表放在 object 可以避免 stdlib → vm 的循环依赖。
//
// 使用方式 (stdlib 侧):
//
//	object.RegisterBuiltinModule("gx/solid", func() map[string]Value {
//	    return map[string]Value{ "createSignal": ... }
//	})
//
// 模块导出表按名字惰性构建一次, 之后所有 import 复用同一份 (与文件模块
// 的缓存语义一致)。全引擎单线程执行, 注册表无需加锁。

// BuiltinModuleBuilder 惰性构建一个内置模块的命名导出表。
type BuiltinModuleBuilder func() map[string]Value

var (
	builtinModules     = map[string]BuiltinModuleBuilder{}
	builtinModuleCache = map[string]map[string]Value{}
)

// RegisterBuiltinModule 注册内置模块。重复注册以最后一次为准。
func RegisterBuiltinModule(name string, builder BuiltinModuleBuilder) {
	builtinModules[name] = builder
	delete(builtinModuleCache, name)
}

// LookupBuiltinModule 查询内置模块的导出表; 未注册时 ok 为 false。
func LookupBuiltinModule(name string) (exports map[string]Value, ok bool) {
	builder, found := builtinModules[name]
	if !found {
		return nil, false
	}
	if exports, found = builtinModuleCache[name]; !found {
		exports = builder()
		builtinModuleCache[name] = exports
	}
	return exports, true
}

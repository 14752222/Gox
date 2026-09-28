package object

import (
	"sort"
	"sync"
)

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
// 的缓存语义一致)。
//
// 并发注意: 虽然 JS 执行是单线程的, 但多个 **独立的 VM 实例** 可以各自在
// 自己的 goroutine 里运行（test262 runner 的 worker 池、嵌入多 VM 的宿主）,
// 每个 goroutine 都会经 SetupGlobals 触发 RegisterBuiltinModule —— 两个 map
// 必须过锁, 否则并发写直接 fatal（已实测）。导出表缓存 get-or-build 同样
// 在锁内完成。

// BuiltinModuleBuilder 惰性构建一个内置模块的命名导出表。
type BuiltinModuleBuilder func() map[string]Value

var builtinModulesMu sync.Mutex

var (
	builtinModules     = map[string]BuiltinModuleBuilder{}
	builtinModuleCache = map[string]map[string]Value{}
)

// RegisterBuiltinModule 注册内置模块。重复注册以最后一次为准。
func RegisterBuiltinModule(name string, builder BuiltinModuleBuilder) {
	builtinModulesMu.Lock()
	defer builtinModulesMu.Unlock()
	builtinModules[name] = builder
	delete(builtinModuleCache, name)
}

// LookupBuiltinModule 查询内置模块的导出表; 未注册时 ok 为 false。
//
// 锁的粒度: builder() 必须**在锁外**调用 —— 聚合模块 "gox" 的 builder 会
// 递归 Lookup 自己的子模块, 在锁内调用直接自死锁 (已实测)。代价是并发
// 首次访问同一模块时可能重复构建一次, 后写覆盖; builder 幂等, 无害。
func LookupBuiltinModule(name string) (exports map[string]Value, ok bool) {
	builtinModulesMu.Lock()
	builder, found := builtinModules[name]
	if !found {
		builtinModulesMu.Unlock()
		return nil, false
	}
	if exports, found = builtinModuleCache[name]; found {
		builtinModulesMu.Unlock()
		return exports, true
	}
	builtinModulesMu.Unlock()

	exports = builder()

	builtinModulesMu.Lock()
	builtinModuleCache[name] = exports
	builtinModulesMu.Unlock()
	return exports, true
}

// RegisteredBuiltinModules 返回已注册的内置模块名 (按字典序)。
// 供宿主在 "模块未找到" 的报错里列出可用模块。
func RegisteredBuiltinModules() []string {
	builtinModulesMu.Lock()
	defer builtinModulesMu.Unlock()
	names := make([]string, 0, len(builtinModules))
	for name := range builtinModules {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Package stdlib 提供 JavaScript 标准库实现。
//
// 包括:
// - console: log, error, warn, info
// - Math: 数学函数和常量
// - JSON: stringify, parse
// - Object: keys, values, entries, assign 等
// - Array/String 原型方法
// - Error 构造器
// - fs: 文件读写 (同步 + 异步 Promise/callback)
// - path: 路径拼接与解析
// - http: HTTP 服务器 (createServer) 与客户端 (get/request)
// - fetch: 全局 fetch (Promise 风格)
// - process: argv/env/cwd/exit 等宿主信息
//
// SetupGlobals 将所有内建对象注入到全局环境中。
package stdlib

import (
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// SetupGlobals 创建并返回带有所有标准库对象的全局环境。
func SetupGlobals() *runtime.Environment {
	env := runtime.NewEnvironment()

	// ===== 原型对象 =====
	// 必须先于构造器创建: 构造器需要 prototype 属性，
	// 原型需要 constructor 反向引用。
	arrayProto := setupArrayProto()
	object.SetArrayProto(arrayProto)

	stringProto := setupStringProto()
	object.SetStringProto(stringProto)

	numberProto := setupNumberProto()
	object.SetNumberProto(numberProto)

	// ===== console =====
	console := setupConsole()
	env.Declare("console", console, false)

	// ===== Math =====
	mathObj := setupMath()
	env.Declare("Math", mathObj, false)

	// ===== JSON =====
	jsonObj := setupJSON()
	env.Declare("JSON", jsonObj, false)

	// ===== Object =====
	objectObj := setupObjectGlobal()
	// Object.prototype 及其标准方法 (toString/hasOwnProperty/valueOf...)。
	// 在此之前 Object 构造器没有 prototype 属性 —— Object.prototype.toString.call
	// 这类反射写法全部失效。
	setupObjectPrototype(objectObj)
	env.Declare("Object", objectObj, false)

	// ===== Array =====
	arrayObj := setupArrayGlobal()
	arrayObj.SetProperty("prototype", arrayProto)
	arrayProto.SetProperty("constructor", arrayObj)
	env.Declare("Array", arrayObj, false)

	// 注意: String / Number / Boolean 三个构造器统一由 setupGlobalFunctions 注册。
	// 早期版本在这里用 setupStringGlobal/setupNumberGlobal/setupBooleanGlobal
	// 先注册一次，而 Environment.Declare 是覆盖写，导致前者注册的静态成员
	// (Number.MAX_VALUE、Number.parseInt 等) 被后来的空壳对象覆盖丢失。

	// ===== Error 类型 =====
	setupErrorTypes(env)

	// ===== Symbol =====
	setupSymbolFunction(env)

	// ===== BigInt (Temporal 的前置依赖) =====
	setupBigInt(env)

	// ===== Temporal (ES2027) =====
	setupTemporal(env)

	// ===== Map / Set / WeakMap / WeakSet =====
	setupMapSet(env)

	// ===== Promise =====
	setupPromise(env)

	// ===== async/await 运行时辅助 =====
	setupAsync(env)

	// ===== RegExp =====
	setupRegExp(env)

	// ===== setTimeout / setInterval 定时器 =====
	setupTimers(env)

	// ===== stats (教程示例: 数据转换 API) =====
	setupStats(env)

	// ===== setStrictTimeout / setStrictInterval 严格定时器 =====
	setupStrictTimers(env)

	// ===== Reflect / Proxy =====
	setupReflectProxy(env)

	// ===== fs / path / http / process (宿主能力) =====
	setupFS(env)
	setupPath(env)
	setupHTTP(env)
	setupProcess(env)

	// ===== TypedArray / ArrayBuffer / DataView =====
	setupTypedArrays(env)

	// ===== 全局函数 =====
	setupGlobalFunctions(env)

	// ===== parseInt, parseFloat, isNaN, isFinite =====
	setupNumberFunctions(env)

	// ===== 构造器 prototype / constructor 反向引用 =====
	// 构造器缺少 prototype 属性会导致 Array.prototype.map.call(...) 这类
	// 通用调用、以及 Number.prototype / [].constructor 等反射访问全部失效。
	// String/Number 构造器在 setupGlobalFunctions 中创建，故此处从环境取回。
	attachPrototype(env, "String", stringProto)
	attachPrototype(env, "Number", numberProto)

	// ===== ES2025 Iterator / WeakRef / FinalizationRegistry / globalThis =====
	env.Declare("Iterator", setupIteratorGlobal(), false)
	setupWeakRefGlobals(env)
	setupGlobalThis(env)

	// ===== eval / AggregateError / Function.prototype =====
	setupEvalAndMisc(env)

	// ===== 响应式 (Dart GetX 风格 obs/computed/ever/once) =====
	setupObs(env)

	// ===== 响应式 (SolidJS 风格, 内置模块 gx/solid) =====
	setupSolid(env)

	// ===== 应用级 kv 持久化 (内置模块 gx/storage, 设备能力 API 方案 A) =====
	setupStorage(env)

	return env
}

// attachPrototype 为构造器挂上 prototype 属性，并在原型上设置 constructor
// 反向引用。
func attachPrototype(env *runtime.Environment, name string, proto *object.Object) {
	v, ok := env.Get(name)
	if !ok || v == nil {
		return
	}
	v.SetProperty("prototype", proto)
	proto.SetProperty("constructor", v)
}

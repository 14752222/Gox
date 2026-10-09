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
	// %Math%[@@toStringTag] = "Math" —— Math 是普通对象 (builtinTag "Object")，
	// 其 "[object Math]" 品牌完全来自这个自身符号属性。
	setToStringTag(mathObj, "Math")
	env.Declare("Math", mathObj, false)

	// ===== JSON =====
	jsonObj := setupJSON()
	setToStringTag(jsonObj, "JSON")
	env.Declare("JSON", jsonObj, false)

	// ===== Object =====
	objectObj := setupObjectGlobal()
	// Object.prototype 及其标准方法 (toString/hasOwnProperty/valueOf...)。
	// 在此之前 Object 构造器没有 prototype 属性 —— Object.prototype.toString.call
	// 这类反射写法全部失效。
	setupObjectPrototype(objectObj)
	if p, ok := objectObj.GetProperty("prototype"); ok {
		SetObjectPrototypeRef(p)
		// 同时注册到 object 层: 供函数 .prototype 对象设置默认 [[Prototype]]。
		object.SetObjectPrototype(p)
	}
	env.Declare("Object", objectObj, false)

	// Math / JSON / console 在此前创建 (Object.prototype 尚未就绪), 此处回填其
	// [[Prototype]] = %Object.prototype% —— 它们是"普通对象"，规范要求原型链含
	// Object.prototype。
	setNamespaceProto(mathObj)
	setNamespaceProto(jsonObj)
	setNamespaceProto(console)

	// ===== 函数对象原型链 (Function / GeneratorFunction / AsyncFunction) =====
	// 必须在 setupAsync 之前: 后者要把 %AsyncGeneratorFunction.prototype% 链接到
	// %Function.prototype%。本函数同时注册全局 Function 构造器 (原先在
	// setupGlobalFunctions 里创建)。
	setupFunctionIntrinsics(env)

	// ===== Array =====
	arrayObj := setupArrayGlobal()
	arrayObj.SetProperty("prototype", arrayProto)
	arrayProto.SetBuiltinProperty("constructor", arrayObj)
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

	// ===== Date / performance (看板 ryGXAJ: 此前全仓零注册) =====
	setupDate(env)

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

	// ===== 桌面自动更新 (内置模块 gx/update, v1.1: 流式下载/进度/pre 通道) =====
	setupUpdate(env)

	// ===== 摘要与随机数 (内置模块 gx/crypto, 看板 ru3TZK) =====
	setupCrypto(env)

	// ===== 聚合模块 "gox" (gx/* 导出并集, 一行导入) =====
	setupGoxUmbrella(env)

	// ===== 内建原型对象统一接入 %Object.prototype% =====
	// 必须放在最后: 前面各 setup 造出的原型对象此刻均已注册完毕。
	linkBuiltinPrototypes(env)

	// ===== 内建对象的 well-known Symbol 成员 (@@iterator / @@species /
	// @@toPrimitive / @@hasInstance) =====
	// 同样必须放在最后: 要从环境取回构造器与原型, 且 @@iterator 复用既有的
	// values / entries 方法对象。见 wellknown_symbols.go 的说明。
	setupWellKnownSymbolMembers(env)

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
	proto.SetBuiltinProperty("constructor", v)
}

// setNamespaceProto 把内建命名空间对象 (Math / JSON / Reflect / Temporal ...)
// 的 [[Prototype]] 指向 %Object.prototype%。
//
// 规范里这些是"普通对象": 原型链必须含 Object.prototype —— 于是
// Object.getPrototypeOf(Math) === Object.prototype 成立, 且 Object.prototype
// 上的可枚举属性会经由 for-in (Math) 被枚举到。
//
// object.NewObject() 把 [[Prototype]] 默认置为 null (为宿主侧纯数据对象保留),
// 命名空间必须显式回填。调用时机: Object.prototype 装配之后 (GetObjectPrototype
// 已就绪)。
func setNamespaceProto(obj *object.Object) {
	if p := object.GetObjectPrototype(); p != nil {
		obj.Proto = p
	}
}

// linkBuiltinPrototypes 把各内建原型对象的 [[Prototype]] 接入 %Object.prototype%。
//
// 规范里除 %Object.prototype% 自身 ([[Prototype]] = null) 外，内建原型对象都是
// "普通对象"，其原型链必须含 Object.prototype。此前它们多由 object.NewObject()
// 建成、[[Prototype]] = null —— 于是 `[].hasOwnProperty` / `"x".valueOf` /
// `new Map().hasOwnProperty` 这类经原型链的隐式调用全部取不到方法
// (test262 harness/verifyProperty.js 重度依赖 o.hasOwnProperty)。
//
// 只回填"当前为 null"的原型对象 —— 已显式接好者 (Function / Boolean / Symbol /
// %GeneratorPrototype% 等) 的链不受影响。必须在 SetupGlobals 末尾调用: 各原型
// 对象此刻均已注册完毕。
func linkBuiltinPrototypes(env *runtime.Environment) {
	objProto := object.GetObjectPrototype()
	if objProto == nil {
		return
	}
	link := func(p object.Value) { linkProtoTo(p, objProto) }

	// (1) 直接注册在 object 包里的原型对象 (部分未暴露为全局的 .prototype，
	//     例如 RegExp.prototype)。
	for _, p := range []object.Value{
		object.ArrayProto,
		object.StringProto,
		object.NumberProto,
		object.GetBooleanProto(),
		object.GetSymbolProto(),
		object.GetBigIntProto(),
		object.MapProto,
		object.SetProto,
		object.PromiseProto,
		object.RegExpProto,
	} {
		link(p)
	}

	// (2) 全局构造器的 .prototype (TypedArray 子类 / Iterator / WeakRef ...)。
	//     Object 不在列: 其 .prototype 就是 %Object.prototype%，必须保持 null 原型。
	names := []string{
		"Function", "Array", "String", "Number", "Boolean", "Symbol", "BigInt",
		"RegExp", "Map", "Set", "WeakMap", "WeakSet", "Promise",
		"AggregateError", "WeakRef", "FinalizationRegistry", "Iterator",
		"ArrayBuffer", "DataView", "Date",
	}
	for _, k := range object.TAKindList() {
		names = append(names, k.Name)
	}
	for _, n := range names {
		v, ok := env.Get(n)
		if !ok || v == nil {
			continue
		}
		if p, ok := v.GetProperty("prototype"); ok {
			link(p)
		}
	}
}

// linkProtoTo 把原型对象 p 的 [[Prototype]] 指向 target，仅当它当前为 null
// (不覆盖已显式接好的原型链)。
func linkProtoTo(p object.Value, target object.Value) {
	o, ok := p.(*object.Object)
	if !ok || o == nil {
		return
	}
	if o.Proto == nil || o.Proto == object.NullSingleton {
		o.Proto = target
	}
}

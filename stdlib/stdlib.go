// Package stdlib 提供 JavaScript 标准库实现。
//
// 包括:
// - console: log, error, warn, info
// - Math: 数学函数和常量
// - JSON: stringify, parse
// - Object: keys, values, entries, assign 等
// - Array/String 原型方法
// - Error 构造器
//
// SetupGlobals 将所有内建对象注入到全局环境中。
package stdlib

import (
	"js-runtime/object"
	"js-runtime/runtime"
)

// SetupGlobals 创建并返回带有所有标准库对象的全局环境。
func SetupGlobals() *runtime.Environment {
	env := runtime.NewEnvironment()

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
	env.Declare("Object", objectObj, false)

	// ===== Array =====
	arrayObj := setupArrayGlobal()
	env.Declare("Array", arrayObj, false)

	// ===== String =====
	stringObj := setupStringGlobal()
	env.Declare("String", stringObj, false)

	// ===== Number =====
	numberObj := setupNumberGlobal()
	env.Declare("Number", numberObj, false)

	// ===== Boolean =====
	booleanObj := setupBooleanGlobal()
	env.Declare("Boolean", booleanObj, false)

	// ===== Error 类型 =====
	setupErrorTypes(env)

	// ===== Symbol =====
	setupSymbolFunction(env)

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

	// ===== Reflect / Proxy =====
	setupReflectProxy(env)

	// ===== 原型链设置 =====
	// ArrayProto: 所有数组实例的原型
	arrayProto := setupArrayProto()
	object.SetArrayProto(arrayProto)

	// StringProto: 所有字符串实例的原型
	stringProto := setupStringProto()
	object.SetStringProto(stringProto)

	// NumberProto: 所有数字实例的原型
	numberProto := setupNumberProto()
	object.SetNumberProto(numberProto)

	// ===== 全局函数 =====
	setupGlobalFunctions(env)

	// ===== parseInt, parseFloat, isNaN, isFinite =====
	setupNumberFunctions(env)

	return env
}

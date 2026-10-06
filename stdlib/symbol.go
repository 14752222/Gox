package stdlib

import (
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// setupSymbol 设置 Symbol 全局对象。
func setupSymbol(env *runtime.Environment) {
	symbolObj := object.NewObject()

	// Symbol.for(key): 全局 Symbol 注册表
	symbolObj.SetProperty("for", object.NewBuiltin("for", func(args ...object.Value) object.Value {
		key := ""
		if len(args) > 0 {
			key = toStr(args[0])
		}
		return object.GetGlobalSymbol(key)
	}))

	// Symbol.keyFor(sym): 查找全局 Symbol 的 key
	symbolObj.SetProperty("keyFor", object.NewBuiltin("keyFor", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.UndefinedSingleton
		}
		if sym, ok := args[0].(*object.Symbol); ok {
			if key, found := object.KeyForSymbol(sym); found {
				return object.NewString(key)
			}
		}
		return object.UndefinedSingleton
	}))

	// Well-known symbols
	// 必须走 GetGlobalSymbol 注册表 (而非 NewSymbol): 引擎内部
	// (GetIterable / 迭代协议) 与 JS 侧 Symbol.iterator 必须是同一
	// Symbol 实例, 否则 SymbolProperties 按 ID 存储后互查不到。
	symbolObj.SetProperty("iterator", object.GetGlobalSymbol("Symbol.iterator"))
	symbolObj.SetProperty("asyncIterator", object.GetGlobalSymbol("Symbol.asyncIterator"))
	symbolObj.SetProperty("toPrimitive", object.GetGlobalSymbol("Symbol.toPrimitive"))
	symbolObj.SetProperty("toStringTag", object.GetGlobalSymbol("Symbol.toStringTag"))
	symbolObj.SetProperty("hasInstance", object.GetGlobalSymbol("Symbol.hasInstance"))
	symbolObj.SetProperty("species", object.GetGlobalSymbol("Symbol.species"))
	symbolObj.SetProperty("match", object.GetGlobalSymbol("Symbol.match"))
	symbolObj.SetProperty("replace", object.GetGlobalSymbol("Symbol.replace"))
	symbolObj.SetProperty("search", object.GetGlobalSymbol("Symbol.search"))
	symbolObj.SetProperty("split", object.GetGlobalSymbol("Symbol.split"))
	symbolObj.SetProperty("isConcatSpreadable", object.GetGlobalSymbol("Symbol.isConcatSpreadable"))
	symbolObj.SetProperty("unscopables", object.GetGlobalSymbol("Symbol.unscopables"))
	symbolObj.SetProperty("matchAll", object.GetGlobalSymbol("Symbol.matchAll"))

	env.Declare("Symbol", symbolObj, false)
}

// setupSymbolFunction 设置 Symbol() 调用函数。
// 在 JS 中 Symbol() 是函数而非构造器，不能用 new 调用。
// 我们通过 BuiltinFunction 实现，在调用时检查是否是 new 调用。
func setupSymbolFunction(env *runtime.Environment) {
	// Symbol 作为函数调用
	symbolFn := object.NewBuiltin("Symbol", func(args ...object.Value) object.Value {
		desc := ""
		if len(args) > 0 && args[0] != object.UndefinedSingleton {
			desc = toStr(args[0])
		}
		return object.NewSymbol(desc)
	})

	// 复制 Symbol 对象的属性到函数
	symbolObj := object.NewObject()
	symbolObj.SetProperty("for", object.NewBuiltin("for", func(args ...object.Value) object.Value {
		key := ""
		if len(args) > 0 {
			key = toStr(args[0])
		}
		return object.GetGlobalSymbol(key)
	}))
	symbolObj.SetProperty("keyFor", object.NewBuiltin("keyFor", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.UndefinedSingleton
		}
		if sym, ok := args[0].(*object.Symbol); ok {
			if key, found := object.KeyForSymbol(sym); found {
				return object.NewString(key)
			}
		}
		return object.UndefinedSingleton
	}))
	symbolObj.SetProperty("iterator", object.GetGlobalSymbol("Symbol.iterator"))
	symbolObj.SetProperty("asyncIterator", object.GetGlobalSymbol("Symbol.asyncIterator"))
	symbolObj.SetProperty("toPrimitive", object.GetGlobalSymbol("Symbol.toPrimitive"))
	symbolObj.SetProperty("toStringTag", object.GetGlobalSymbol("Symbol.toStringTag"))
	symbolObj.SetProperty("hasInstance", object.GetGlobalSymbol("Symbol.hasInstance"))
	symbolObj.SetProperty("species", object.GetGlobalSymbol("Symbol.species"))
	symbolObj.SetProperty("match", object.GetGlobalSymbol("Symbol.match"))
	symbolObj.SetProperty("replace", object.GetGlobalSymbol("Symbol.replace"))
	symbolObj.SetProperty("search", object.GetGlobalSymbol("Symbol.search"))
	symbolObj.SetProperty("split", object.GetGlobalSymbol("Symbol.split"))
	symbolObj.SetProperty("isConcatSpreadable", object.GetGlobalSymbol("Symbol.isConcatSpreadable"))
	symbolObj.SetProperty("unscopables", object.GetGlobalSymbol("Symbol.unscopables"))
	symbolObj.SetProperty("matchAll", object.GetGlobalSymbol("Symbol.matchAll"))

	// 将 Symbol 对象的属性复制到函数上
	for _, k := range symbolObj.Keys() {
		val, _ := symbolObj.GetProperty(k)
		symbolFn.SetProperty(k, val)
	}

	// ===== %Symbol.prototype% =====
	// Symbol 原始值没有自有属性: 属性访问 (含 @@toStringTag) 一律沿这个
	// 原型对象查找。规范里它携带 @@toStringTag = "Symbol"，因此
	// Object.prototype.toString.call(Symbol('x')) === "[object Symbol]"，
	// 而 `delete Symbol.prototype[Symbol.toStringTag]` 之后回落 "[object Object]"。
	symbolProto := object.NewObjectWithProto(object.GetObjectPrototype())
	symbolProto.SetBuiltinProperty("constructor", symbolFn)
	setToStringTag(symbolProto, "Symbol")
	symbolFn.SetProperty("prototype", symbolProto)
	object.SetSymbolProto(symbolProto)

	env.Declare("Symbol", symbolFn, false)
}

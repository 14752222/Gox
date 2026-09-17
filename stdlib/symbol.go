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
	symbolObj.SetProperty("iterator", object.NewSymbol("Symbol.iterator"))
	symbolObj.SetProperty("asyncIterator", object.NewSymbol("Symbol.asyncIterator"))
	symbolObj.SetProperty("toPrimitive", object.NewSymbol("Symbol.toPrimitive"))
	symbolObj.SetProperty("toStringTag", object.NewSymbol("Symbol.toStringTag"))
	symbolObj.SetProperty("hasInstance", object.NewSymbol("Symbol.hasInstance"))
	symbolObj.SetProperty("species", object.NewSymbol("Symbol.species"))
	symbolObj.SetProperty("match", object.NewSymbol("Symbol.match"))
	symbolObj.SetProperty("replace", object.NewSymbol("Symbol.replace"))
	symbolObj.SetProperty("search", object.NewSymbol("Symbol.search"))
	symbolObj.SetProperty("split", object.NewSymbol("Symbol.split"))
	symbolObj.SetProperty("isConcatSpreadable", object.NewSymbol("Symbol.isConcatSpreadable"))
	symbolObj.SetProperty("unscopables", object.NewSymbol("Symbol.unscopables"))
	symbolObj.SetProperty("matchAll", object.NewSymbol("Symbol.matchAll"))

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
	symbolObj.SetProperty("iterator", object.NewSymbol("Symbol.iterator"))
	symbolObj.SetProperty("asyncIterator", object.NewSymbol("Symbol.asyncIterator"))
	symbolObj.SetProperty("toPrimitive", object.NewSymbol("Symbol.toPrimitive"))
	symbolObj.SetProperty("toStringTag", object.NewSymbol("Symbol.toStringTag"))
	symbolObj.SetProperty("hasInstance", object.NewSymbol("Symbol.hasInstance"))
	symbolObj.SetProperty("species", object.NewSymbol("Symbol.species"))
	symbolObj.SetProperty("match", object.NewSymbol("Symbol.match"))
	symbolObj.SetProperty("replace", object.NewSymbol("Symbol.replace"))
	symbolObj.SetProperty("search", object.NewSymbol("Symbol.search"))
	symbolObj.SetProperty("split", object.NewSymbol("Symbol.split"))
	symbolObj.SetProperty("isConcatSpreadable", object.NewSymbol("Symbol.isConcatSpreadable"))
	symbolObj.SetProperty("unscopables", object.NewSymbol("Symbol.unscopables"))
	symbolObj.SetProperty("matchAll", object.NewSymbol("Symbol.matchAll"))

	// 将 Symbol 对象的属性复制到函数上
	for _, k := range symbolObj.Keys() {
		val, _ := symbolObj.GetProperty(k)
		symbolFn.SetProperty(k, val)
	}

	env.Declare("Symbol", symbolFn, false)
}

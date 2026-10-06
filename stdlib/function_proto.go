package stdlib

import (
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// setupFunctionIntrinsics 装配函数对象的内建原型链。
//
// 需在 Object.prototype 就绪之后、setupAsync 之前调用 —— setupAsync 要用
// %Function.prototype% 去链接 %AsyncGeneratorFunction.prototype%。
//
// 规范口径 (经 Node 22 实测):
//
//	%Function%               .[[Prototype]] = %Function.prototype%
//	                         .prototype     = %Function.prototype%
//	%Function.prototype%     [[Prototype]]  = %Object.prototype% (可调用, 返回 undefined)
//	%GeneratorFunction%      .[[Prototype]] = %Function%          (!)
//	                         .prototype     = %GeneratorFunction.prototype%
//	%GeneratorFunction.prototype%   [[Prototype]] = %Function.prototype%
//	                                .prototype     = %GeneratorPrototype%
//	%AsyncFunction%          .[[Prototype]] = %Function%
//	                         .prototype     = %AsyncFunction.prototype%
//	%AsyncFunction.prototype%       [[Prototype]] = %Function.prototype% (无 .prototype)
//	%AsyncGeneratorFunction% 由 setupAsyncGeneratorIntrinsics 装配, 此处仅链接。
//
// 这些对象通过 object.SetXxxPrototype 注册，供 vm.createClosure 按函数种类
// 选择函数对象的 [[Prototype]]。
func setupFunctionIntrinsics(env *runtime.Environment) {
	objProto := objectPrototype // 由 setupObjectPrototype / SetObjectPrototypeRef 提供
	tagSym := object.GetGlobalSymbol("Symbol.toStringTag")

	// %Function.prototype%: 可调用 (规范: Function.prototype() 返回 undefined)，
	// 自身 [[Prototype]] 是 Object.prototype。
	funcProto := object.NewBuiltin("", func(args ...object.Value) object.Value {
		return object.UndefinedSingleton
	})
	if objProto != nil {
		funcProto.FuncPrototype = objProto
	}
	object.SetFunctionPrototype(funcProto)

	// %Function% 构造器: new Function(...) / Function(...) 动态编译源码。
	funcCtor := object.NewBuiltin("Function", func(args ...object.Value) object.Value {
		return newDynamicFunction(env, args, dynFuncNormal)
	})
	funcCtor.SetProperty("name", object.NewString("Function"))
	funcCtor.SetProperty("length", object.NewNumber(1))
	funcCtor.FuncPrototype = funcProto // %Function%.[[Prototype]] = %Function.prototype%
	funcCtor.SetProperty("prototype", funcProto)
	funcProto.SetProperty("constructor", funcCtor)
	env.Declare("Function", funcCtor, false)

	// %GeneratorFunction% + %GeneratorFunction.prototype% + %GeneratorPrototype%
	// 本身是可构造的 (CreateDynamicFunction kind="generator")。
	genFunc := object.NewBuiltin("GeneratorFunction", func(args ...object.Value) object.Value {
		return newDynamicFunction(env, args, dynFuncGenerator)
	})
	genFuncProto := object.NewObjectWithProto(funcProto) // %GeneratorFunction.prototype%
	genProto := object.NewObjectWithProto(objProto)      // %GeneratorPrototype%
	genFuncProto.SetBuiltinProperty("prototype", genProto)
	genFuncProto.SetBuiltinProperty("constructor", genFunc)
	if tagSym != nil {
		genFuncProto.SetBuiltinSymbolProperty(tagSym, object.NewString("GeneratorFunction"))
	}
	genProto.SetBuiltinProperty("constructor", genFuncProto)
	if tagSym != nil {
		genProto.SetBuiltinSymbolProperty(tagSym, object.NewString("Generator"))
	}
	genFunc.SetProperty("prototype", genFuncProto)
	genFunc.FuncPrototype = funcCtor // %GeneratorFunction%.[[Prototype]] = %Function%
	object.SetGeneratorFunctionPrototype(genFuncProto)
	object.SetGeneratorPrototype(genProto)

	// %AsyncFunction% + %AsyncFunction.prototype% (后者无 .prototype 属性)
	// 本身是可构造的 (CreateDynamicFunction kind="async")。
	asyncFunc := object.NewBuiltin("AsyncFunction", func(args ...object.Value) object.Value {
		return newDynamicFunction(env, args, dynFuncAsync)
	})
	asyncFuncProto := object.NewObjectWithProto(funcProto) // %AsyncFunction.prototype%
	asyncFuncProto.SetBuiltinProperty("constructor", asyncFunc)
	// %AsyncFunction.prototype%[@@toStringTag] = "AsyncFunction":
	// async 函数对象的 "[object AsyncFunction]" 品牌来自这里
	// (普通函数无此标签，回落 builtinTag "Function")。
	if tagSym != nil {
		asyncFuncProto.SetBuiltinSymbolProperty(tagSym, object.NewString("AsyncFunction"))
	}
	asyncFunc.SetProperty("prototype", asyncFuncProto)
	asyncFunc.FuncPrototype = funcCtor // %AsyncFunction%.[[Prototype]] = %Function%
	object.SetAsyncFunctionPrototype(asyncFuncProto)
}

package object

import (
	"strconv"
)

// 本文件实现 Function.prototype 的共享方法 (call/apply/bind/toString)。
//
// 运行时没有独立的 Function.prototype 原型链对象，而是让每种可调用类型
// (Closure/BuiltinFunction/BuiltinMethod) 的 GetProperty 在遇到这几个
// 方法名时返回这里的实现 —— 效果上等价于所有函数都继承自 Function.prototype。
//
// 实现依赖 CallFunc 回调桥 (SetCallFunction 注册)，因此可以调用任意
// 可调用对象且不引入 object → vm 的依赖。

// funcProtoLookup 在可调用类型的 GetProperty 中调用，返回 Function.prototype
// 的共享方法 (call/apply/bind/toString)。
//
// 这些方法**必须**是可感知运行时 this 的 BuiltinMethod，而不能是把查找接收者
// 词法捕获进闭包的无 this BuiltinFunction。原因: GetProperty 只保证普通方法调用
// `f.call(x)` 的 this 恰好等于查找接收者 f，一旦方法被"间接"使用就会错位:
//
//	Function.prototype.call.bind(Object.prototype.hasOwnProperty)
//	f.call.call(other, ...)   /   var c = f.call; c(x)
//
// 词法捕获版本会把 `call` 的目标错认成查找时那个对象 (如上例中的
// Function.prototype)，导致 harness/verifyProperty.js 捕获的
// `__hasOwnProperty` / `__propertyIsEnumerable` 恒返回 undefined，进而令
// language/expressions/*/dstr 下大量 verifyProperty 用例失败。
//
// 参数 recv 保留仅为调用点签名稳定 (Closure/BuiltinFunction/BuiltinMethod 的
// GetProperty 都传自身)，此处不再使用 —— 目标由运行时 this 决定。
func funcProtoLookup(_ Value, name string) (Value, bool) {
	switch name {
	case "call":
		return NewBuiltinMethod("call", func(this Value, args ...Value) Value {
			thisArg := args0OrUndefined(args)
			var rest []Value
			if len(args) > 1 {
				rest = args[1:]
			} else {
				rest = []Value{}
			}
			return CallFunction(this, thisArg, rest...)
		}), true
	case "apply":
		return NewBuiltinMethod("apply", func(this Value, args ...Value) Value {
			thisArg := args0OrUndefined(args)
			callArgs := []Value{}
			if len(args) > 1 {
				if arr, ok := args[1].(*Array); ok {
					callArgs = make([]Value, len(arr.Elements))
					copy(callArgs, arr.Elements)
				} else if args[1] != nil && args[1] != UndefinedSingleton && args[1] != NullSingleton {
					// 非数组且非 null/undefined: 类数组对象取 length + 数字键
					callArgs = arrayLikeArgs(args[1])
				}
			}
			return CallFunction(this, thisArg, callArgs...)
		}), true
	case "bind":
		return NewBuiltinMethod("bind", func(this Value, args ...Value) Value {
			thisArg := args0OrUndefined(args)
			var pre []Value
			if len(args) > 1 {
				pre = args[1:]
			} else {
				pre = []Value{}
			}
			target := this
			// 规范: 绑定函数的 name = "bound " + target.name。
			boundName := "bound "
			if target != nil {
				if s, ok := target.GetProperty("name"); ok {
					if str, ok := s.(*String); ok {
						boundName += str.Value
					}
				}
			}
			bound := NewBuiltin(boundName, func(callArgs ...Value) Value {
				full := make([]Value, 0, len(pre)+len(callArgs))
				full = append(full, pre...)
				full = append(full, callArgs...)
				return CallFunction(target, thisArg, full...)
			})
			// 绑定函数的 length = 原函数 length - 前置参数个数 (下限 0)
			if target != nil {
				if l, ok := target.GetProperty("length"); ok {
					if n, ok := l.(*Number); ok {
						ln := n.Value - float64(len(pre))
						if ln < 0 {
							ln = 0
						}
						bound.SetProperty("length", NewNumber(ln))
					}
				}
			}
			return bound
		}), true
	case "toString":
		return NewBuiltinMethod("toString", func(this Value, args ...Value) Value {
			if this == nil {
				return NewString("undefined")
			}
			return NewString(this.Inspect())
		}), true
	}
	return nil, false
}

// args0OrUndefined 返回 args[0]，args 为空时返回 undefined (thisArg 位置)。
func args0OrUndefined(args []Value) Value {
	if len(args) == 0 {
		return UndefinedSingleton
	}
	return args[0]
}

// arrayLikeArgs 从类数组对象 (有 length 和数字键) 提取参数列表。
func arrayLikeArgs(v Value) []Value {
	lv, ok := v.GetProperty("length")
	if !ok {
		return []Value{}
	}
	n, ok := lv.(*Number)
	if !ok || n.Value < 0 || n.Value != float64(int(n.Value)) {
		return []Value{}
	}
	length := int(n.Value)
	if length > 1<<20 {
		length = 1 << 20 // 防御异常大的 length
	}
	args := make([]Value, length)
	for i := 0; i < length; i++ {
		if ev, found := v.GetProperty(strconv.Itoa(i)); found {
			args[i] = ev
		} else {
			args[i] = UndefinedSingleton
		}
	}
	return args
}

// ===== 函数对象 [[Prototype]] 注册表 =====
//
// 函数对象有两套原型:
//   - .prototype 属性 (实例侧): new fn() 创建对象的 [[Prototype]]，存于
//     Closure.Proto；
//   - [[Prototype]] (函数自身): 按函数种类指向不同的内建原型对象。
//
// 规范口径 (经 Node 实测):
//
//	Object.getPrototypeOf(function(){})          === %Function.prototype%
//	Object.getPrototypeOf(()=>{})                === %Function.prototype%
//	Object.getPrototypeOf(class {})              === %Function.prototype%
//	Object.getPrototypeOf(function*(){})         === %GeneratorFunction.prototype%
//	Object.getPrototypeOf(async function(){})    === %AsyncFunction.prototype%
//	Object.getPrototypeOf(async function*(){})   === %AsyncGeneratorFunction.prototype%
//	Object.getPrototypeOf(内置函数)               === %Function.prototype%
//
// 这些内建原型对象由 stdlib 装配 (见 stdlib/function_proto.go)，通过下面的
// setter 注册；object 层不反向依赖 stdlib。

var (
	functionPrototype               Value
	generatorFunctionPrototype      Value
	asyncFunctionPrototype          Value
	asyncGeneratorFunctionPrototype Value
	generatorPrototype              Value // %GeneratorPrototype% (生成器实例原型)
	objectPrototypeRef              Value // %Object.prototype% (函数 .prototype 的默认原型)
)

// SetObjectPrototype 注册全局 %Object.prototype%。
// 用于给普通函数的 .prototype 对象 (实例原型) 设置默认 [[Prototype]]。
func SetObjectPrototype(v Value) { objectPrototypeRef = v }

// GetObjectPrototype 返回全局 %Object.prototype%。
func GetObjectPrototype() Value { return objectPrototypeRef }

// SetFunctionPrototype 注册全局 %Function.prototype%。
func SetFunctionPrototype(v Value) { functionPrototype = v }

// GetFunctionPrototype 返回全局 %Function.prototype%。
func GetFunctionPrototype() Value { return functionPrototype }

// SetGeneratorFunctionPrototype 注册 %GeneratorFunction.prototype%。
func SetGeneratorFunctionPrototype(v Value) { generatorFunctionPrototype = v }

// GetGeneratorFunctionPrototype 返回 %GeneratorFunction.prototype%。
func GetGeneratorFunctionPrototype() Value { return generatorFunctionPrototype }

// SetAsyncFunctionPrototype 注册 %AsyncFunction.prototype%。
func SetAsyncFunctionPrototype(v Value) { asyncFunctionPrototype = v }

// GetAsyncFunctionPrototype 返回 %AsyncFunction.prototype%。
func GetAsyncFunctionPrototype() Value { return asyncFunctionPrototype }

// SetAsyncGeneratorFunctionPrototype 注册 %AsyncGeneratorFunction.prototype%。
func SetAsyncGeneratorFunctionPrototype(v Value) { asyncGeneratorFunctionPrototype = v }

// GetAsyncGeneratorFunctionPrototype 返回 %AsyncGeneratorFunction.prototype%。
func GetAsyncGeneratorFunctionPrototype() Value { return asyncGeneratorFunctionPrototype }

// SetGeneratorPrototype 注册 %GeneratorPrototype% (生成器实例的 [[Prototype]])。
func SetGeneratorPrototype(v Value) { generatorPrototype = v }

// GetGeneratorPrototype 返回 %GeneratorPrototype%。
func GetGeneratorPrototype() Value { return generatorPrototype }

// FuncPrototypeOf 返回函数对象自身的 [[Prototype]]；非函数对象返回 nil。
// 内建函数默认继承全局 %Function.prototype% (除非显式指定 FuncPrototype，
// 例如 %Function.prototype% 自身继承 Object.prototype)。
func FuncPrototypeOf(v Value) Value {
	switch f := v.(type) {
	case *Closure:
		if f.FuncPrototype != nil {
			return f.FuncPrototype
		}
		return functionPrototype
	case *BuiltinFunction:
		if f.FuncPrototype != nil {
			return f.FuncPrototype
		}
		return functionPrototype
	case *BuiltinMethod:
		if f.FuncPrototype != nil {
			return f.FuncPrototype
		}
		return functionPrototype
	}
	return nil
}

// FuncPrototypeForCompiled 按函数种类返回其函数对象 [[Prototype]]。
// 供 vm.createClosure 与动态函数构造 (CreateDynamicFunction) 共用:
//   - IsAsyncGenerator         → %AsyncGeneratorFunction.prototype%
//   - IsGenerator (非 async)   → %GeneratorFunction.prototype%
//   - IsAsync (非 generator)   → %AsyncFunction.prototype%
//   - 其余 (普通/箭头/方法/类)  → %Function.prototype%
//
// 未注册对应内建原型时回退到 %Function.prototype% (乃至 nil)。
func FuncPrototypeForCompiled(fn *CompiledFunction) Value {
	if fn != nil {
		switch {
		case fn.IsAsyncGenerator:
			if asyncGeneratorFunctionPrototype != nil {
				return asyncGeneratorFunctionPrototype
			}
		case fn.IsGenerator:
			if generatorFunctionPrototype != nil {
				return generatorFunctionPrototype
			}
		case fn.IsAsync:
			if asyncFunctionPrototype != nil {
				return asyncFunctionPrototype
			}
		}
	}
	return functionPrototype
}

// funcProtoLookupChain 沿函数对象的 [[Prototype]] 链查找属性。
// 供 Closure/BuiltinFunction/BuiltinMethod 的 GetProperty 在专有分支之后兜底
// (例如 .constructor 应解析到 Function / GeneratorFunction 等)。
func funcProtoLookupChain(v Value, name string) (Value, bool) {
	p := FuncPrototypeOf(v)
	// 最多 64 层防环 (正常链长 <= 4)。
	for i := 0; i < 64 && p != nil; i++ {
		if val, ok := p.GetProperty(name); ok {
			return val, true
		}
		switch t := p.(type) {
		case *Object:
			p = t.Proto
		case *Array:
			p = t.GetProto()
		default:
			p = nil
		}
	}
	return nil, false
}

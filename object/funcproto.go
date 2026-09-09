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

// funcProtoLookup 在可调用类型的 GetProperty 中调用:
// recv 是持有该方法的函数值 (调用时的 this)。
func funcProtoLookup(recv Value, name string) (Value, bool) {
	switch name {
	case "call":
		return NewBuiltin("call", func(args ...Value) Value {
			thisArg := args[0]
			var rest []Value
			if len(args) > 1 {
				rest = args[1:]
			} else {
				rest = []Value{}
			}
			return CallFunction(recv, thisArg, rest...)
		}), true
	case "apply":
		return NewBuiltin("apply", func(args ...Value) Value {
			thisArg := args[0]
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
			return CallFunction(recv, thisArg, callArgs...)
		}), true
	case "bind":
		return NewBuiltin("bind", func(args ...Value) Value {
			thisArg := args[0]
			var pre []Value
			if len(args) > 1 {
				pre = args[1:]
			} else {
				pre = []Value{}
			}
			boundName := "bound "
			if s, ok := recv.GetProperty("name"); ok {
				if str, ok := s.(*String); ok {
					boundName += str.Value
				}
			} else {
				boundName += ""
			}
			bound := NewBuiltin(boundName, func(callArgs ...Value) Value {
				full := make([]Value, 0, len(pre)+len(callArgs))
				full = append(full, pre...)
				full = append(full, callArgs...)
				return CallFunction(recv, thisArg, full...)
			})
			// 绑定函数的 length = 原函数 length - 前置参数个数 (下限 0)
			if l, ok := recv.GetProperty("length"); ok {
				if n, ok := l.(*Number); ok {
					ln := n.Value - float64(len(pre))
					if ln < 0 {
						ln = 0
					}
					bound.SetProperty("length", NewNumber(ln))
				}
			}
			return bound
		}), true
	case "toString":
		return NewBuiltin("toString", func(args ...Value) Value {
			return NewString(recv.Inspect())
		}), true
	}
	return nil, false
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

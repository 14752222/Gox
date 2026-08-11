package stdlib

import (
	"js-runtime/object"
	"js-runtime/runtime"
)

// setupReflectProxy 设置 Reflect 全局对象和 Proxy 构造器。
//
// Reflect: 提供与 Proxy trap 对应的反射方法，直接操作目标对象。
//   - Reflect.get / set / has / deleteProperty
//   - Reflect.getPrototypeOf / setPrototypeOf
//   - Reflect.ownKeys / isExtensible
//   - Reflect.apply / construct
//
// Proxy: 包装目标对象，通过 handler 上的 trap 拦截操作。
//   这里只负责创建代理。trap 的转发由 vm 包在属性访问/调用时完成。
func setupReflectProxy(env *runtime.Environment) {
	// ===== Proxy =====
	proxyFn := object.NewBuiltin("Proxy", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return object.NewTypeError("Proxy constructor requires 2 arguments")
		}
		target := args[0]
		handler := args[1]
		if target == nil || handler == nil {
			return object.NewTypeError("Cannot create proxy with a non-object as target or handler")
		}
		p, err := object.NewProxy(target, handler)
		if err != nil {
			return err
		}
		return p
	})

	// Proxy.revocable(target, handler): 返回 { proxy, revoke }
	proxyFn.Properties = map[string]object.Value{
		"revocable": object.NewBuiltin("revocable", func(args ...object.Value) object.Value {
			if len(args) < 2 {
				return object.NewTypeError("Proxy.revocable requires 2 arguments")
			}
			p, err := object.NewProxy(args[0], args[1])
			if err != nil {
				return err
			}
			// 简化: revoke 标记代理已撤销，撤销后操作抛 TypeError
			revoke := object.NewBuiltin("revoke", func(args ...object.Value) object.Value {
				p.IsRevoked = true
				return object.UndefinedSingleton
			})
			res := object.NewObject()
			res.SetProperty("proxy", p)
			res.SetProperty("revoke", revoke)
			return res
		}),
	}

	env.Declare("Proxy", proxyFn, false)

	// ===== Reflect =====
	reflectObj := object.NewObject()

	// Reflect.get(target, key, receiver?) — 简化: 直接读取目标属性
	reflectObj.SetProperty("get", object.NewBuiltin("Reflect.get", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return object.UndefinedSingleton
		}
		target := args[0]
		key := toPropKey(args[1])
		// 代理通过 VM 转发 get trap；这里 Reflect 直接读取目标
		if p, ok := target.(*object.Proxy); ok {
			target = p.Target
		}
		val, found := target.GetProperty(key)
		if !found {
			return object.UndefinedSingleton
		}
		return val
	}))

	// Reflect.set(target, key, value, receiver?) — 直接写入目标属性
	reflectObj.SetProperty("set", object.NewBuiltin("Reflect.set", func(args ...object.Value) object.Value {
		if len(args) < 3 {
			return object.NewBoolean(false)
		}
		target := args[0]
		key := toPropKey(args[1])
		val := args[2]
		if p, ok := target.(*object.Proxy); ok {
			target = p.Target
		}
		// 检查是否可扩展 (简化: 直接设置)
		target.SetProperty(key, val)
		return object.TrueSingleton
	}))

	// Reflect.has(target, key) — 检查属性是否存在 (含原型链)
	reflectObj.SetProperty("has", object.NewBuiltin("Reflect.has", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return object.NewBoolean(false)
		}
		target := args[0]
		key := toPropKey(args[1])
		if p, ok := target.(*object.Proxy); ok {
			target = p.Target
		}
		_, found := target.GetProperty(key)
		return object.NewBoolean(found)
	}))

	// Reflect.deleteProperty(target, key) — 删除自有属性
	reflectObj.SetProperty("deleteProperty", object.NewBuiltin("Reflect.deleteProperty", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return object.NewBoolean(false)
		}
		target := args[0]
		key := toPropKey(args[1])
		if p, ok := target.(*object.Proxy); ok {
			target = p.Target
		}
		// 简化: 从对象 Properties 中删除 (如支持)
		if o, ok := target.(*object.Object); ok {
			delete(o.Properties, key)
			return object.TrueSingleton
		}
		return object.TrueSingleton
	}))

	// Reflect.getPrototypeOf(target) — 获取原型
	reflectObj.SetProperty("getPrototypeOf", object.NewBuiltin("Reflect.getPrototypeOf", func(args ...object.Value) object.Value {
		if len(args) < 1 {
			return object.NullSingleton
		}
		target := args[0]
		if p, ok := target.(*object.Proxy); ok {
			target = p.Target
		}
		switch t := target.(type) {
		case *object.Object:
			return t.Proto
		case *object.Array:
			return t.GetProto()
		}
		return object.NullSingleton
	}))

	// Reflect.setPrototypeOf(target, proto) — 设置原型
	reflectObj.SetProperty("setPrototypeOf", object.NewBuiltin("Reflect.setPrototypeOf", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return object.NewBoolean(false)
		}
		target := args[0]
		proto := args[1]
		if p, ok := target.(*object.Proxy); ok {
			target = p.Target
		}
		switch t := target.(type) {
		case *object.Object:
			t.Proto = proto
			return object.TrueSingleton
		case *object.Array:
			t.SetProto(proto)
			return object.TrueSingleton
		}
		return object.NewBoolean(false)
	}))

	// Reflect.ownKeys(target) — 返回自有属性键数组
	reflectObj.SetProperty("ownKeys", object.NewBuiltin("Reflect.ownKeys", func(args ...object.Value) object.Value {
		if len(args) < 1 {
			return object.NewArray(nil)
		}
		target := args[0]
		if p, ok := target.(*object.Proxy); ok {
			target = p.Target
		}
		var keys []object.Value
		switch t := target.(type) {
		case *object.Object:
			for _, k := range t.Keys() {
				keys = append(keys, object.NewString(k))
			}
		case *object.Array:
			for i := range t.Elements {
				keys = append(keys, object.NewString(itoa(i)))
			}
			keys = append(keys, object.NewString("length"))
		default:
			return object.NewArray(nil)
		}
		return object.NewArray(keys)
	}))

	// Reflect.isExtensible(target) — 检查是否可扩展
	reflectObj.SetProperty("isExtensible", object.NewBuiltin("Reflect.isExtensible", func(args ...object.Value) object.Value {
		if len(args) < 1 {
			return object.NewBoolean(false)
		}
		target := args[0]
		if p, ok := target.(*object.Proxy); ok {
			target = p.Target
		}
		if o, ok := target.(*object.Object); ok {
			return object.NewBoolean(o.Extensible)
		}
		return object.TrueSingleton
	}))

	// Reflect.apply(fn, thisArg, argsArray) — 以 thisArg 调用 fn
	reflectObj.SetProperty("apply", object.NewBuiltin("Reflect.apply", func(args ...object.Value) object.Value {
		if len(args) < 1 {
			return object.UndefinedSingleton
		}
		fn := args[0]
		var thisArg object.Value = object.UndefinedSingleton
		if len(args) > 1 {
			thisArg = args[1]
		}
		var callArgs []object.Value
		if len(args) > 2 {
			if arr, ok := args[2].(*object.Array); ok {
				callArgs = arr.Elements
			}
		}
		return object.CallFunction(fn, thisArg, callArgs...)
	}))

	// Reflect.construct(target, argsArray) — 以构造方式调用
	reflectObj.SetProperty("construct", object.NewBuiltin("Reflect.construct", func(args ...object.Value) object.Value {
		if len(args) < 1 {
			return object.UndefinedSingleton
		}
		fn := args[0]
		var ctorArgs []object.Value
		if len(args) > 1 {
			if arr, ok := args[1].(*object.Array); ok {
				ctorArgs = arr.Elements
			}
		}
		// 简化: 调用 VM 的构造逻辑通过回调桥不可行，这里直接调用函数
		// (对普通构造器，可退化为 Reflect.apply 的 this=新对象)
		return object.CallFunction(fn, object.NewObject(), ctorArgs...)
	}))

	env.Declare("Reflect", reflectObj, false)
}

// toPropKey 将属性键值转为字符串。
func toPropKey(v object.Value) string {
	switch k := v.(type) {
	case *object.String:
		return k.Value
	case *object.Number:
		return k.Inspect()
	case *object.Symbol:
		return k.Inspect()
	}
	return v.Inspect()
}

// itoa 整数转字符串 (避免引入 strconv 造成不必要的依赖混乱)。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

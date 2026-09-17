package stdlib

import (
	"strconv"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
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
//
//	这里只负责创建代理。trap 的转发由 vm 包在属性访问/调用时完成。
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

	// Reflect.get(target, key, receiver?) — 读取目标属性
	reflectObj.SetProperty("get", object.NewBuiltin("Reflect.get", func(args ...object.Value) object.Value {
		target, errVal := reflectTargetArg(args, "Reflect.get")
		if errVal != nil {
			return errVal
		}
		if len(args) < 2 {
			return object.NewTypeError("Reflect.get: property key is required")
		}
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

	// Reflect.set(target, key, value, receiver?) — 写入目标属性
	reflectObj.SetProperty("set", object.NewBuiltin("Reflect.set", func(args ...object.Value) object.Value {
		target, errVal := reflectTargetArg(args, "Reflect.set")
		if errVal != nil {
			return errVal
		}
		if len(args) < 3 {
			return object.NewTypeError("Reflect.set: value is required")
		}
		key := toPropKey(args[1])
		val := args[2]
		if p, ok := target.(*object.Proxy); ok {
			target = p.Target
		}
		// 不可写 / 不可扩展时必须返回 false，而不是无条件 true
		if o, ok := target.(*object.Object); ok {
			if desc, exists := o.Properties[key]; exists {
				if !desc.Writable {
					return object.FalseSingleton
				}
			} else if !o.Extensible {
				return object.FalseSingleton
			}
		}
		target.SetProperty(key, val)
		return object.TrueSingleton
	}))

	// Reflect.has(target, key) — 检查属性是否存在 (含原型链)
	reflectObj.SetProperty("has", object.NewBuiltin("Reflect.has", func(args ...object.Value) object.Value {
		target, errVal := reflectTargetArg(args, "Reflect.has")
		if errVal != nil {
			return errVal
		}
		if len(args) < 2 {
			return object.NewTypeError("Reflect.has: property key is required")
		}
		key := toPropKey(args[1])
		if p, ok := target.(*object.Proxy); ok {
			target = p.Target
		}
		_, found := target.GetProperty(key)
		// Array.GetProperty 对任意数字索引都返回 (undefined, true)，
		// 会让 Reflect.has(arr, "999") 恒为 true。需按索引范围修正。
		if found {
			if arr, ok := target.(*object.Array); ok {
				if idx, err := strconv.Atoi(key); err == nil {
					found = idx >= 0 && idx < len(arr.Elements)
				}
			}
		}
		return object.NewBoolean(found)
	}))

	// Reflect.deleteProperty(target, key) — 删除自有属性
	reflectObj.SetProperty("deleteProperty", object.NewBuiltin("Reflect.deleteProperty", func(args ...object.Value) object.Value {
		target, errVal := reflectTargetArg(args, "Reflect.deleteProperty")
		if errVal != nil {
			return errVal
		}
		if len(args) < 2 {
			return object.NewTypeError("Reflect.deleteProperty: property key is required")
		}
		key := toPropKey(args[1])
		if p, ok := target.(*object.Proxy); ok {
			target = p.Target
		}
		// 走 DeleteProperty 而不是直接 delete map: 后者会漏掉
		// Object.InsertOrder 的清理，导致键重新加入后顺序错乱。
		switch t := target.(type) {
		case *object.Object:
			return object.NewBoolean(t.DeleteProperty(key))
		case *object.Array:
			if idx, err := strconv.Atoi(key); err == nil &&
				idx >= 0 && idx < len(t.Elements) {
				t.Elements[idx] = object.UndefinedSingleton
			}
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
				keys = append(keys, object.NewString(strconv.Itoa(i)))
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

	// Reflect.construct(target, argsArray[, newTarget]) — 以构造方式调用
	reflectObj.SetProperty("construct", object.NewBuiltin("Reflect.construct", func(args ...object.Value) object.Value {
		if len(args) < 1 || !object.IsCallable(args[0]) {
			return object.NewTypeError("Reflect.construct: target is not a constructor")
		}
		fn := args[0]

		var ctorArgs []object.Value
		if len(args) > 1 {
			if arr, ok := args[1].(*object.Array); ok {
				ctorArgs = append([]object.Value(nil), arr.Elements...)
			}
		}
		// newTarget 省略时等于 target
		newTarget := fn
		if len(args) > 2 && args[2] != nil {
			newTarget = args[2]
		}

		// 1) 创建新对象并绑定原型到 newTarget.prototype
		// 旧实现直接 new Object()，丢失了原型绑定，且把构造器返回值
		// 原样返回 (构造器不返回对象时应返回新对象)，导致结果为 undefined。
		obj := object.NewObject()
		if proto, found := newTarget.GetProperty("prototype"); found && proto != nil {
			obj.Proto = proto
		}

		// 2) 以新对象为 this 调用构造器
		result := object.CallFunction(fn, obj, ctorArgs...)
		if cbErr := object.TakeCallbackError(); cbErr != nil {
			return object.NewErrorWithName("Error", cbErr.Error())
		}

		// 3) 构造器返回对象时用返回值，否则用新创建的对象
		if result != nil && object.IsObjectLike(result) {
			return result
		}
		return obj
	}))

	env.Declare("Reflect", reflectObj, false)
}

// reflectTargetArg 校验 Reflect API 的 target 参数。
// 规范: target 必须是对象，否则抛 TypeError (旧实现静默返回默认值)。
func reflectTargetArg(args []object.Value, api string) (object.Value, object.Value) {
	if len(args) < 1 || !object.IsObjectLike(args[0]) {
		return nil, object.NewTypeError("%s called on non-object", api)
	}
	return args[0], nil
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

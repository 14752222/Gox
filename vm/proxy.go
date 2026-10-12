package vm

import (
	"fmt"

	"github.com/14752222/Gox/object"
)

func (vm *VM) proxyTrap(proxy *object.Proxy, trapName string, args []object.Value) (object.Value, bool, error) {
	// 已撤销的代理: 所有操作抛 TypeError
	if proxy.IsRevoked {
		return nil, true, fmt.Errorf("TypeError: Cannot perform '%s' on a proxy that has been revoked", trapName)
	}
	handler := proxy.Handler
	if handler == nil {
		return nil, false, nil
	}
	trap := object.GetProxyTrap(handler, trapName)
	if trap == nil {
		return nil, false, nil
	}
	// 调用 trap，this 绑定为 handler
	result, err := vm.callFunction(trap, handler, args)
	if err != nil {
		return nil, true, err
	}
	return result, true, nil
}

// proxyGet 处理对代理的属性读取。
// receiver 是接收者 (通常就是代理本身)。

func (vm *VM) proxyGet(proxy *object.Proxy, key string, receiver object.Value) (object.Value, error) {
	res, handled, err := vm.proxyTrap(proxy, "get", []object.Value{proxy.Target, object.NewString(key), receiver})
	if err != nil {
		return nil, err
	}
	if !handled {
		// 无 trap: 直接从目标读取
		val, found := proxy.Target.GetProperty(key)
		if !found || val == nil {
			return object.UndefinedSingleton, nil
		}
		return val, nil
	}
	if res == nil {
		return object.UndefinedSingleton, nil
	}
	return res, nil
}

// proxySet 处理对代理的属性写入。

func (vm *VM) proxySet(proxy *object.Proxy, key string, val, receiver object.Value) error {
	res, handled, err := vm.proxyTrap(proxy, "set", []object.Value{proxy.Target, object.NewString(key), val, receiver})
	if err != nil {
		return err
	}
	if !handled {
		// 无 trap: 直接写入目标
		proxy.Target.SetProperty(key, val)
		return nil
	}
	// 有 trap: 若 trap 返回 falsy，视为静默失败 (非严格模式)
	_ = res
	return nil
}

// proxyHas 处理对代理的属性存在性检查 (in 操作符)。

func (vm *VM) proxyHas(proxy *object.Proxy, key string) (bool, error) {
	res, handled, err := vm.proxyTrap(proxy, "has", []object.Value{proxy.Target, object.NewString(key)})
	if err != nil {
		return false, err
	}
	if !handled {
		_, found := proxy.Target.GetProperty(key)
		return found, nil
	}
	return object.IsTruthyValue(res), nil
}

// inOperator 实现 `key in obj` 语义。
// obj 为 Proxy 时走 has trap; 否则沿属性/原型链检查 key 是否存在。

func (vm *VM) proxyApply(proxy *object.Proxy, thisArg object.Value, args []object.Value) (object.Value, error) {
	res, handled, err := vm.proxyTrap(proxy, "apply", []object.Value{proxy.Target, thisArg, object.NewArray(args)})
	if err != nil {
		return nil, err
	}
	if !handled {
		// 无 trap: 直接调用目标
		return vm.callFunction(proxy.Target, thisArg, args)
	}
	return res, nil
}

// proxyConstruct 处理对代理 (目标为构造函数) 的 new 调用。

func (vm *VM) proxyConstruct(proxy *object.Proxy, args []object.Value) (object.Value, error) {
	res, handled, err := vm.proxyTrap(proxy, "construct", []object.Value{proxy.Target, object.NewArray(args)})
	if err != nil {
		return nil, err
	}
	if !handled {
		// 无 trap: 直接以 new 方式调用目标
		newObj := object.NewObject()
		result, err := vm.callFunction(proxy.Target, newObj, args)
		if err != nil {
			return nil, err
		}
		// 构造语义: 若构造器返回对象则用之，否则用新对象
		if result != nil && object.IsObjectLike(result) {
			return result, nil
		}
		return newObj, nil
	}
	return res, nil
}

// isCallable 检查值是否可作为函数调用 (含函数目标代理)。

package vm

import (
	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/object"
)

func withRefOf(frame *Frame, operand uint16) *bytecode.WithRef {
	ref, _ := frame.Constants.Get(operand).(*bytecode.WithRef)
	return ref
}

// withResolve 沿 ref.Slots (内层在前) 逐个取 with 对象, 查 ref.Name 属性。
//
// 命中返回 (对象, 值, true); 属性不存在或被 Symbol.unscopables 排除返回
// (nil, nil, false)。属性 getter / unscopables getter 抛出的用户异常以
// error 返回 —— 调用方须走 JS 抛出流程 (rethrowBridgeError)。

func (vm *VM) withResolve(frame *Frame, ref *bytecode.WithRef) (object.Value, object.Value, bool, error) {
	for _, slot := range ref.Slots {
		if slot < 0 || slot >= len(frame.Locals) {
			continue
		}
		obj := frame.Locals[slot]
		if obj == nil {
			continue
		}
		val, found, err := vm.withGetBinding(obj, ref.Name)
		if err != nil {
			return nil, nil, false, err
		}
		if found {
			return obj, val, true, nil
		}
	}
	return nil, nil, false, nil
}

// withGetBinding 判断 with 对象 obj 是否绑定 name 并取其值 (HasBinding + Get)。
// 属性不存在 → (nil, false); 存在但被 @@unscopables 排除 → 按不存在处理。

func (vm *VM) withGetBinding(obj object.Value, name string) (object.Value, bool, error) {
	if p, ok := obj.(*object.Proxy); ok {
		has, err := vm.proxyHas(p, name)
		if err != nil {
			return nil, false, err
		}
		if !has {
			return nil, false, nil
		}
		v, err := vm.proxyGet(p, name, p)
		if err != nil {
			return nil, false, err
		}
		return v, true, nil
	}
	if o, ok := obj.(*object.Object); ok {
		val, found := o.GetProperty(name)
		if err := vm.checkCallbackErr(); err != nil {
			return nil, false, err
		}
		if !found {
			return nil, false, nil
		}
		blocked, err := vm.unscopablesBlocked(o, name)
		if err != nil {
			return nil, false, err
		}
		if blocked {
			return nil, false, nil
		}
		return val, true, nil
	}
	// 其余类型 (原始值包装 / 数组 / 函数对象等) 走通用 GetProperty 通路;
	// 它们没有 @@unscopables 语义, 不做排除判定。
	val, found := obj.GetProperty(name)
	if err := vm.checkCallbackErr(); err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	return val, true, nil
}

// unscopablesBlocked 判断对象 o 的 @@unscopables 是否把 name 排除出 with 作用域。
// 仅在属性已确认存在后调用 (规范: HasBinding 先 HasProperty 再读 @@unscopables)。

func (vm *VM) unscopablesBlocked(o *object.Object, name string) (bool, error) {
	un, found := object.LookupSymbolProperty(o, object.GetGlobalSymbol("Symbol.unscopables"))
	if !found {
		return false, nil
	}
	// LookupSymbolProperty 返回原始访问器, 需显式调用其 getter (可能抛异常)。
	if acc, isAcc := un.(*object.Accessor); isAcc {
		if acc.Getter == nil || !object.IsCallable(acc.Getter) {
			return false, nil
		}
		un = object.CallFunction(acc.Getter, o)
		if err := vm.checkCallbackErr(); err != nil {
			return false, err
		}
	}
	uo, ok := un.(*object.Object)
	if !ok {
		// 非对象 (含 undefined/null/原始值) 不排除任何属性。
		return false, nil
	}
	bval, bfound := uo.GetProperty(name)
	if err := vm.checkCallbackErr(); err != nil {
		return false, err
	}
	if bfound && bval != nil && bval.IsTruthy() {
		return true, nil
	}
	return false, nil
}

// withSet 把 val 写入 with 对象的 name 属性 (SetMutableBinding)。

func (vm *VM) withSet(owner object.Value, name string, val object.Value) error {
	switch o := owner.(type) {
	case *object.Proxy:
		return vm.proxySet(o, name, val, o)
	default:
		owner.SetProperty(name, val)
		return nil
	}
}

// withDelete 删除 with 对象上的 name 属性 (delete 标识符语义)。

func (vm *VM) withDelete(owner object.Value, name string) {
	if o, ok := owner.(*object.Object); ok {
		o.DeleteProperty(name)
	}
	// 其余类型 (含 Proxy) 暂无属性删除通路, 静默跳过 —— delete 结果仍为 true。
}

// storeLocalSlot 向当前帧的局部槽写入值, 并复刻 OP_STORE 的传播语义
// (SharedCells / 闭包捕获 / 子闭包 / ModifiedSlots)。仅供 with 的局部回退路径使用。

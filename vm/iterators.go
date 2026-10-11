package vm

import (
	"errors"

	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

func makeIterStep(val object.Value, done bool) *object.Object {
	if val == nil {
		val = object.UndefinedSingleton
	}
	step := object.NewObject()
	step.SetProperty("value", val)
	step.SetProperty("done", object.NewBoolean(done))
	return step
}

// syncIterStep 同步驱动迭代器一步, 返回 {value, done} 步进对象 (数组解构用)。
// 覆盖三种形状: 生成器 (VM 帧驱动)、runtime.Iterator (数组/字符串/Map/Set
// 等的 Go 适配器)、用户自建 { next } 对象 (调其 next())。
// 返回的 error 交给 rethrowBridgeError / throwJSError 走抛出流程。

func (vm *VM) syncIterStep(iter object.Value) (object.Value, error) {
	switch it := iter.(type) {
	case *object.Generator:
		val, done, err := vm.genResume(it, object.UndefinedSingleton)
		if err != nil {
			return nil, err
		}
		return makeIterStep(val, done), nil
	case *runtime.Iterator:
		val, done := it.Next()
		return makeIterStep(val, done), nil
	case *object.Object:
		nextFn, found := it.GetProperty("next")
		if !found || !object.IsCallable(nextFn) {
			return nil, &jsThrow{Name: "TypeError", Message: "iterator has no callable next()"}
		}
		res, err := vm.callFunction(nextFn, it, nil)
		if err != nil {
			return nil, err
		}
		if err := vm.checkCallbackErr(); err != nil {
			return nil, err
		}
		// 规范 7.4.2 step 2: next() 的返回值必须是 Object, 否则 TypeError。
		if res == nil || !object.IsObjectLike(res) {
			return nil, &jsThrow{Name: "TypeError", Message: "iterator next() must return an object"}
		}
		return res, nil
	}
	return nil, &jsThrow{Name: "TypeError", Message: "value is not an iterator"}
}

// iteratorClose 实现规范 7.4.6 IteratorClose: 取迭代器的 return 方法,
// null/undefined 直接跳过; 非可调用 → TypeError; 否则以迭代器为 this 调用,
// 丢弃返回值。生成器没有暴露 return 属性 (object.Generator.GetProperty 只
// 认 next), 这里直接用内建 return 语义注入 return 完成并展开 finally。
// 返回的 error 交给调用点决定 rethrow (异常是否被 try 吞掉)。

func (vm *VM) iteratorClose(iter object.Value) error {
	if iter == nil || isNullish(iter) {
		return nil
	}
	switch it := iter.(type) {
	case *object.Generator:
		if _, _, err := vm.genReturn(it, object.UndefinedSingleton); err != nil {
			return err
		}
		return vm.checkCallbackErr()
	case *object.Object:
		ret, found := it.GetProperty("return")
		// `return` 是访问器 (getter) 且 getter 抛错 ⇒ GetMethod abrupt
		// (规范 7.4.6 step 4 + GetMethod step 2): 该错误必须**照常传播**,
		// 由调用点按 completion 类型决定去留 (break/return 路径向上抛;
		// body 抛异常路径被吞掉、原异常优先)。getter 经回调桥执行, 错误
		// 滞留在桥信号里, 必须在此消费 —— 否则会残留到之后某个内建调用点,
		// 报错位置串台 (test262 iterator-close-*-get-method-abrupt)。
		if err := vm.checkCallbackErr(); err != nil {
			return err
		}
		if !found || ret == nil || isNullish(ret) {
			return nil
		}
		if !object.IsCallable(ret) {
			return &jsThrow{Name: "TypeError", Message: "iterator return method is not callable"}
		}
		res, err := vm.callFunction(ret, it, nil)
		if err != nil {
			return err
		}
		if err := vm.checkCallbackErr(); err != nil {
			return err
		}
		// 规范 7.4.6 step 9: return() 的返回值必须是 Object, 否则 TypeError
		// (return null / undefined / 数字都算; 数组/函数等对象类通过)。
		if res == nil || !object.IsObjectLike(res) {
			return &jsThrow{Name: "TypeError", Message: "iterator return method must return an object"}
		}
		return nil
	}
	// runtime.Iterator / Array / String 等其它形状没有 return 方法: 跳过。
	return nil
}

// ===== explicit resource management (using / await using) =====

// disposeAdd 实现 AddDisposableResource + CreateDisposableResource
// (sync-dispose hint) 的登记步骤:
//   - v 为 null/undefined: 跳过 (规范 step 1a; async 下等价无操作);
//   - v 非对象: TypeError;
//   - 取释放方法 (sync: @@dispose; async: @@asyncDispose, 缺失回退 @@dispose);
//     方法为 null/undefined 或非可调用: TypeError;
//   - 把值与方法写入两个隐藏局部槽 (方法 getter 只在此时读取一次)。
//
// 返回非 nil 表示要抛出的异常 (交给 rethrowBridgeError 走抛出流程)。

func (vm *VM) disposeAdd(res *bytecode.DisposeResource, v object.Value) error {
	if res == nil {
		return nil
	}
	if isNullish(v) {
		return nil
	}
	if !object.IsObjectLike(v) {
		return &jsThrow{Name: "TypeError", Message: "using value must be an object"}
	}
	method, err := vm.getDisposeMethod(v, res.Async)
	if err != nil {
		return err
	}
	if isNullish(method) {
		return &jsThrow{Name: "TypeError", Message: "using value has no dispose method"}
	}
	if !object.IsCallable(method) {
		return &jsThrow{Name: "TypeError", Message: "using value's dispose method is not callable"}
	}
	vm.setLocalSlot(res.ValueSlot, v)
	vm.setLocalSlot(res.MethodSlot, method)
	return nil
}

// getDisposeMethod 取释放方法: sync 取 @@dispose; async 先取 @@asyncDispose,
// 为 null/undefined 时回退 @@dispose (规范 GetDisposeMethod)。属性读取会触发
// getter, 且只发生一次。

func (vm *VM) getDisposeMethod(v object.Value, async bool) (object.Value, error) {
	if async {
		m, err := vm.getSymbolMember(v, object.SymbolAsyncDispose())
		if err != nil {
			return nil, err
		}
		if !isNullish(m) {
			return m, nil
		}
	}
	return vm.getSymbolMember(v, object.SymbolDispose())
}

// getSymbolMember 以 Symbol 为键读取属性, 展开访问器 getter (this = 原接收者)。
// object.LookupSymbolProperty 只返回描述符里的原始值 (不展开 getter), 而
// `{ get [Symbol.dispose]() {...} }` 必须调用 getter, 故这里自带展开。

func (vm *VM) getSymbolMember(obj object.Value, sym *object.Symbol) (object.Value, error) {
	if sym == nil {
		return object.UndefinedSingleton, nil
	}
	// 沿原型链查 Symbol 键 (支持 *Object 的 SymbolProperties 与
	// *Array/*TypedArray 的 SymbolPropertyStore 段)。
	for cur := obj; cur != nil; {
		var desc object.PropertyDescriptor
		var found bool
		switch o := cur.(type) {
		case *object.Object:
			desc, found = o.GetSymbolPropertyDescriptor(sym)
		case object.SymbolPropertyStore:
			desc, found = o.GetSymbolPropertyDescriptor(sym)
		}
		if found {
			if acc, isAcc := desc.Value.(*object.Accessor); isAcc {
				if acc.Getter != nil && object.IsCallable(acc.Getter) {
					res, err := vm.callFunction(acc.Getter, obj, nil)
					if err != nil {
						return nil, err
					}
					if err := vm.checkCallbackErr(); err != nil {
						return nil, err
					}
					if res == nil {
						res = object.UndefinedSingleton
					}
					return res, nil
				}
				return object.UndefinedSingleton, nil
			}
			if desc.Value == nil {
				return object.UndefinedSingleton, nil
			}
			return desc.Value, nil
		}
		pp, ok := cur.(interface{ GetProto() object.Value })
		if !ok {
			break
		}
		cur = pp.GetProto()
	}
	return object.UndefinedSingleton, nil
}

// setLocalSlot 把值写入当前帧的局部槽, 必要时扩展 Locals (编译器已保证
// 该槽在 NumLocals 内, 这里是防御性兜底)。

func (vm *VM) disposeExit(scope *bytecode.DisposeScope) error {
	if scope == nil || len(scope.Resources) == 0 {
		return nil
	}
	// 判断本帧是否正在跑一个带挂起异常的 finally (从栈顶往下找本帧的 inFinally
	// 条目)。是则以它的 pendingVal 作为「已有完成」, 释放错误与之合成。
	// 只记下标不取指针: 释放方法调用可能触发嵌套调用, 让 tryStack 底层数组
	// 重新分配, 指针会失效。
	pendingIdx := -1
	var pendingErr object.Value
	for i := len(vm.tryStack) - 1; i >= 0; i-- {
		top := vm.tryStack[i]
		if top.inFinally && top.frameIdx == vm.frameIdx {
			pendingIdx = i
			pendingErr = top.pendingVal
			break
		}
	}
	hadPending := pendingIdx >= 0

	for i := len(scope.Resources) - 1; i >= 0; i-- {
		res := scope.Resources[i]
		method := vm.getLocalSlot(res.MethodSlot)
		if isNullish(method) {
			continue // 未登记 (初始化为 null/undefined) 或已释放
		}
		value := vm.getLocalSlot(res.ValueSlot)
		_, err := vm.callFunction(method, value, nil)
		if err == nil {
			if cerr := vm.checkCallbackErr(); cerr != nil {
				err = cerr
			}
		}
		if err == nil {
			continue
		}
		// 释放抛错: 取出 JS 抛出值 (非 JS 级异常原样上抛)。
		var thrown object.Value
		var te *ThrowError
		if errors.As(err, &te) {
			thrown = te.Value
		} else if vr := vm.throwJSErrorValue(err); vr != nil {
			thrown = vr
		} else {
			return err
		}
		if hadPending {
			pendingErr = object.NewSuppressedError(thrown, pendingErr)
		} else {
			pendingErr = thrown
		}
	}

	if !hadPending {
		if pendingErr != nil {
			return &ThrowError{Value: pendingErr}
		}
		return nil
	}
	// 把 (可能合成过的) 错误写回挂起条目, 交给 OP_END_FINALLY 重抛。
	if pendingIdx < len(vm.tryStack) {
		vm.tryStack[pendingIdx].pendingVal = pendingErr
		return nil
	}
	// 条目已不在 (极端情形): 直接抛出。
	if pendingErr != nil {
		return &ThrowError{Value: pendingErr}
	}
	return nil
}

// throwJSErrorValue 把非 *ThrowError 的 Go error (如 *jsThrow) 转成 JS 异常值。
// 不是 JS 语言级异常时返回 nil。

func (vm *VM) resolveSymbolIterator(val object.Value) (object.Value, bool, error) {
	// 只有持有符号键属性的类型才可能实现了 @@iterator: *Object, 以及
	// *Array/*TypedArray (SymbolPropertyStore)。其余值 (原始值 / 无符号键槽的
	// 对象类型) 直接交回 runtime.GetIterable 兜底。
	sym := object.GetGlobalSymbol("Symbol.iterator")
	if sym == nil {
		return nil, false, nil
	}
	if !hasSymbolProps(val) {
		return nil, false, nil
	}
	fn, err := vm.getSymbolMember(val, sym)
	if err != nil {
		return nil, false, err
	}
	// 规范 GetMethod/GetIterator: @@iterator 为 undefined/null (含未定义) →
	// 回退到其它可迭代形状 (返回值 false, 由调用方交 runtime.GetIterable);
	// 存在但不可调用 → TypeError (不得静默回退)。
	if fn == nil || fn == object.UndefinedSingleton || fn == object.NullSingleton {
		return nil, false, nil
	}
	if !object.IsCallable(fn) {
		// @@iterator 存在但不可调用: TypeError。throwNamedError 若被 try/catch
		// 接住返回 nil, 此时需以 errHandledThrow 提示调用点停止兜底。
		if err := vm.throwNamedError("TypeError",
			"%s[Symbol.iterator] is not a function", val.Inspect()); err != nil {
			return nil, false, err
		}
		return nil, false, errHandledThrow
	}
	res, err := vm.callFunction(fn, val, nil)
	if err != nil {
		return nil, false, err
	}
	switch r := res.(type) {
	case *object.Generator:
		return r, true, nil
	case *object.JSIterator:
		return runtime.NewCallbackIterator(r.Next), true, nil
	case *object.Object:
		// 用户自建迭代器对象 ({ next, return }): 只要 next 键存在就认。
		// 由 OP_ITER_STEP / OP_ITER_NEXT 的对象分支驱动 (调 next())。
		// 用 hasNextKey (非触发式) 判定, 不能再 GetProperty——提前访问
		// next getter 会多一次属性访问 (r6e5qp 属性访问顺序)。
		if hasNextKey(r) {
			return r, true, nil
		}
	}
	return nil, false, nil
}

// resolveAsyncSymbolIterator 预解析实现了 [Symbol.asyncIterator] 的对象:
// 在 VM 层调用该方法并适配返回值 (与 resolveSymbolIterator 同构, 只是
// 查的 Symbol 键不同)。返回 (迭代器, true, nil) 表示已解析;
// (nil, false, nil) 表示没有该方法 (交回同步可迭代形状分发)。
//
// 规范 GetMethod/GetIterator(hint=async): @@asyncIterator 一旦存在, 就
// 必须按其值处理 —— 值为 nullish 才回退同步可迭代; 值不可调用, 或调用
// 结果不是对象, 一律 TypeError (不得再退回 [Symbol.iterator]，否则
// Symbol.iterator 的 getter 会被意外触发)。

func (vm *VM) resolveAsyncSymbolIterator(val object.Value) (object.Value, bool, error) {
	o, ok := val.(*object.Object)
	if !ok {
		return nil, false, nil
	}
	sym := object.GetGlobalSymbol("Symbol.asyncIterator")
	if sym == nil {
		return nil, false, nil
	}
	if _, present := object.LookupSymbolPropertyDescriptor(o, sym); !present {
		return nil, false, nil // 未实现 @@asyncIterator: 交回同步可迭代分发
	}
	fn, err := vm.getSymbolMember(o, sym)
	if err != nil {
		return nil, false, err
	}
	if fn == object.UndefinedSingleton || fn == object.NullSingleton {
		return nil, false, nil // nullish: 回退同步可迭代 (Gox for-await 设计)
	}
	if !object.IsCallable(fn) {
		return nil, false, vm.throwNamedError("TypeError",
			"%s[Symbol.asyncIterator] is not a function", o.Inspect())
	}
	res, err := vm.callFunction(fn, o, nil)
	if err != nil {
		return nil, false, err
	}
	switch r := res.(type) {
	case *object.Generator:
		return r, true, nil
	case *object.JSIterator:
		return runtime.NewCallbackIterator(r.Next), true, nil
	}
	// Object 即迭代器 (async 迭代器的常见实现): 包 Async-from-Sync 语义
	// wrapper (own next 懒缓存 —— V8 实测原生 @@asyncIterator 的 next
	// method 也是首取后缓存; throw/return 每次 GetMethod, 见
	// wrapSyncIterForAsync)。用 hasNextKey (非触发式) 判定: GetProperty
	// 会多触发一次 getter, 破坏 yield* 异步委托的属性访问顺序
	// (r6e5qp, test262 yield-star-async-next 等)。
	if obj, isObj := res.(*object.Object); isObj && hasNextKey(obj) {
		return vm.wrapSyncIterForAsync(obj, false), true, nil
	}
	// 调用结果不是对象: GetIterator 要求抛 TypeError。
	return nil, false, vm.throwNamedError("TypeError",
		"%s is not an object (async iterator result)", res.Inspect())
}

// lookupSymbolProperty 已迁至 object.LookupSymbolProperty (原型链 Symbol 键查找)。

// setIndex 实现索引赋值 obj[index] = val。
// inStaticInit 表示当前帧是 class 静态初始化帧 (决定函数对象自有属性的
// 可枚举性: 静态方法/访问器不可枚举, 字段可枚举)。

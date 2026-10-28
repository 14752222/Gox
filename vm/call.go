package vm

import (
	"fmt"
	"strings"

	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/object"
)

// constructValue 实现 new fn(...args) 的核心语义 (供 object.ConstructFunction 桥):
// 内建构造器直接调用; 闭包先以 fn.prototype 为原型建 this, 返回对象则用之。
func (vm *VM) constructValue(fn object.Value, args []object.Value) (object.Value, error) {
	if b, ok := fn.(*object.BuiltinFunction); ok {
		r := b.Fn(args...)
		if r == nil {
			return object.UndefinedSingleton, nil
		}
		return r, nil
	}
	// 闭包构造必须与 OP_NEW 同口径: 派生类的 super(...) 依赖 pendingNewTarget
	// 走「父构造结果替换本帧 this」分支, 否则 `class D extends Uint8Array{}`
	// 经 Reflect.construct / SpeciesCreate 构造出来的只是普通对象 (实例方法
	// 全部报 receiver must be a TypedArray)。
	if c, ok := fn.(*object.Closure); ok {
		if c.IsArrow || (c.Fn != nil && (c.Fn.IsGenerator || c.Fn.IsAsync)) {
			return object.UndefinedSingleton, fmt.Errorf("TypeError: %s is not a constructor", describeCallee(fn))
		}
	}
	var proto object.Value
	if pv, ok := fn.GetProperty("prototype"); ok && pv != nil {
		proto = pv
	}
	var newObj object.Value
	if proto != nil {
		newObj = object.NewObjectWithProto(proto)
	} else {
		newObj = object.NewPlainObject()
	}
	// 闭包构造走 callClosure + runFrom (与 OP_NEW 同路径): 派生类的 super(...)
	// 依赖 pendingNewTarget 走「父构造结果替换本帧 this」分支, 且隐式返回
	// undefined 时须采用构造器帧最终的 this。否则 `class D extends Uint8Array{}`
	// 经 Reflect.construct / SpeciesCreate 构造出来的只是普通对象 (实例方法
	// 全部报 receiver must be a TypedArray)。
	if c, ok := fn.(*object.Closure); ok {
		bound := &object.Closure{
			Fn:             c.Fn,
			Env:            c.Env,
			This:           newObj,
			NewTarget:      c.NewTarget,
			IsArrow:        c.IsArrow,
			CapturedLocals: c.CapturedLocals,
			CreatedAtFrame: c.CreatedAtFrame,
			IterEpoch:      c.IterEpoch,
		}
		prevNT, prevHas := vm.pendingNewTarget, vm.hasPendingNewTarget
		vm.pendingNewTarget = fn
		vm.hasPendingNewTarget = true
		startIdx := vm.frameIdx + 1
		if err := vm.callClosure(bound, args); err != nil {
			vm.pendingNewTarget, vm.hasPendingNewTarget = prevNT, prevHas
			vm.unwindFramesTo(startIdx)
			return nil, err
		}
		ctorFrame := vm.frames[startIdx]
		if err := vm.runFrom(startIdx); err != nil {
			vm.pendingNewTarget, vm.hasPendingNewTarget = prevNT, prevHas
			vm.unwindFramesTo(startIdx)
			return nil, err
		}
		vm.pendingNewTarget, vm.hasPendingNewTarget = prevNT, prevHas
		var ctorThis object.Value
		if ctorFrame != nil {
			ctorThis = ctorFrame.This
		}
		result := vm.stack.Pop()
		if object.IsObjectValue(result) {
			return result, nil
		}
		if object.IsTemporalExotic(result) {
			// 派生类 super() 返回 Temporal 内建实例: 内部槽寄存到派生类 this。
			target := ctorThis
			if target == nil || target == newObj {
				target = newObj
			}
			if object.StoreTemporalSlot(target, result) {
				return target, nil
			}
			return result, nil
		}
		if ctorThis != nil && ctorThis != newObj {
			return ctorThis, nil
		}
		return newObj, nil
	}
	r, err := vm.callFunction(fn, newObj, args)
	if err != nil {
		return nil, err
	}
	// 构造器返回对象则以其为结果, 否则用新建的 this
	if r != nil && object.IsObjectValue(r) {
		return r, nil
	}
	return newObj, nil
}

func (vm *VM) callFunction(fn object.Value, this object.Value, args []object.Value) (result object.Value, err error) {
	// 微任务检查点 (ES perform-microtask-checkpoint): frameIdx == -1 表示
	// 这是一次顶层 Go→JS 调用 (定时器/空闲/严格回调、GUI 事件派发等),
	// JS 栈将在返回时清空, 故返回前 drain Promise Jobs。脚本执行期
	// (frameIdx >= 0) 的回调桥调用不 drain —— job 必须等同步代码跑完。
	topLevel := vm.frameIdx == -1
	defer func() {
		if topLevel {
			vm.drainMicrotasks()
		}
	}()
	// Rx 单元可直接调用: count() 等价 count.value (GetX 语义)
	//
	// computed 求值抛错时 RxValue 会把原始抛出值写进回调桥双槽 (rvE6lH 方案 a)。
	// 这里必须立刻取走并以 error 返回 —— 交给外层桥重新 SetCallbackError,
	// throw 出的原值才不会滞留在值槽里被下一个两槽消费者误读 (静默错值)。
	if obs, ok := fn.(object.ObservableState); ok {
		v := obs.RxValue()
		if err := object.TakeCallbackError(); err != nil {
			object.TakeCallbackErrorValue()
			return object.UndefinedSingleton, err
		}
		return v, nil
	}
	switch callee := fn.(type) {
	case *object.BuiltinFunction:
		result := callee.Fn(args...)
		if result == nil {
			return object.UndefinedSingleton, nil
		}
		return result, nil

	case *object.BuiltinMethod:
		result := callee.Fn(this, args...)
		if result == nil {
			return object.UndefinedSingleton, nil
		}
		// 内建**访问器 getter** 以"返回 *Error"作为抛出信号 (与 OP_CALL
		// 的 throwIfError 一致)。经此桥 (object.CallFunction) 展开 getter 时,
		// 若不在此转换, 返回值会被当作普通值持有 —— e.g.
		// `TypedArray.prototype.byteOffset` 应抛 TypeError, 实际却拿到
		// 一个 TypeError 对象 (get-%typedarray% 系列的 invoked-as-accessor)。
		//
		// 只对访问器 (名字以 "get " 开头) 生效: 普通内建方法返回 *Error
		// 在若干内部调用点 (Temporal 的 FromXxx 辅助等) 是"把错误值交给
		// 调用方判定"的既有约定, 一律转换会破坏那些路径。
		if errObj, isErr := result.(*object.Error); isErr && strings.HasPrefix(callee.Name, "get ") {
			return nil, &ThrowError{Value: errObj}
		}
		return result, nil

	case *object.Closure:
		// 绑定 this: 箭头函数复用自身的词法 this; 非箭头函数一律以传入的
		// this 作接收者 (this 为 nil 即"无接收者", 等价 undefined —— 交给
		// callClosure 按 sloppy 归一到 globalThis, 不再沿用闭包创建时的 this)。
		bound := callee
		if !callee.IsArrow {
			bound = &object.Closure{
				Fn:             callee.Fn,
				Env:            callee.Env,
				This:           this,
				NewTarget:      callee.NewTarget,
				IsArrow:        callee.IsArrow,
				CapturedLocals: callee.CapturedLocals,
				CreatedAtFrame: callee.CreatedAtFrame,
			}
		}
		// generator 函数: 不执行函数体, 返回 Generator 对象 (对齐 OP_CALL;
		// 否则生成器体被同步执行, 首个 yield 触发 "yield outside generator")。
		// 有形参前导段时先跑前导帧同步绑定形参, 再取出冻结好的 Generator。
		if bound.Fn != nil && bound.Fn.IsGenerator {
			if genHasPrologue(bound.Fn) {
				startIdx := vm.frameIdx + 1
				if err := vm.setupGenPrologueFrame(bound, args); err != nil {
					vm.unwindFramesTo(startIdx)
					return nil, err
				}
				if err := vm.runFrom(startIdx); err != nil {
					vm.unwindFramesTo(startIdx)
					return nil, err
				}
				return vm.stack.Pop(), nil
			}
			return object.NewGenerator(bound, args), nil
		}
		// 记录当前帧索引，新帧从这里 +1
		startIdx := vm.frameIdx + 1
		if err := vm.callClosure(bound, args); err != nil {
			// 子帧内抛出且未被捕获: 回收已压入的帧, 否则帧栈损坏,
			// 调用方 (回调桥) 继续执行时会跑飞
			vm.unwindFramesTo(startIdx)
			return nil, err
		}
		// 执行子帧直到返回
		if err := vm.runFrom(startIdx); err != nil {
			vm.unwindFramesTo(startIdx)
			return nil, err
		}
		// 返回值在栈顶
		return vm.stack.Pop(), nil
	}
	return nil, fmt.Errorf("TypeError: %s is not a function", describeCallee(fn))
}

// unwindFramesTo 回收 frameIdx >= startIdx 的所有帧。
// 用于嵌套调用 (回调桥) 抛出未捕获异常后的帧栈恢复。
// 每层帧在压栈时保留了调用点的栈高度, 回收时把本帧残留的栈值一并清掉。

func (vm *VM) createClosure(meta *bytecode.FunctionMetadata, frame *Frame) *object.Closure {
	// 保存当前帧的常量池引用
	fn := metaToCompiledFunction(meta, frame.Constants.Constants)

	// 捕获外层局部变量: 共享当前帧的 Locals 数组本身，而不是拷一份快照。
	//
	// 拷快照会让兄弟闭包各自持有副本 —— 离开创建帧后它们就彻底失联，于是
	// `const [inc, get] = mk(); inc(); get()` 读不到彼此的修改。
	// 共享同一个数组后，写操作 (OP_STORE → SharedCells) 对所有捕获者同时可见；
	// 该数组随闭包存活 (GC 保活)，等价于 ECMAScript 的 binding cell 逃逸到堆。
	//
	// ⚠ 帧从闭包装配而来时 (callClosure 用 CapturedLocals 拷出新 Locals,
	// 原始 binding cell 在 frame.SharedCells), 必须取 SharedCells 作捕获数组:
	// 取帧 Locals 会让「在 getter/method 体内创建的闭包」捕获 getter 帧的
	// 私有拷贝 —— getter 每次属性访问都被重新调用, 内层闭包每次读到创建时
	// 的快照, 与外层 cell 失联 (r6e5qp: `make().foo()` 恒 1, 本应累加;
	// test262 async-generator yield* 委托的 nextCount 记序模式全被它挡下)。
	var captured []object.Value
	if meta.BaseSlot > 0 {
		// 捕获前缀的精确上界 (编译期扫描指令流得出, 见 compiler.computeCapturePrefixLen)。
		// BaseSlot 是外层作用域槽总数的粗粒度上界, 会把创建帧自身的局部也
		// 捎带进前缀; CapturePrefixLen 只覆盖本函数真正引用的外层 slot。
		prefixLen := meta.CapturePrefixLen
		if prefixLen <= 0 {
			prefixLen = meta.BaseSlot
		}
		// frame.SharedCells 是本帧外层的原始 binding cell 数组 (callClosure
		// 装配闭包帧时 frame.Locals 只是它的拷贝)。捕获前缀整体落在 cell
		// 数组内时直接共享它: 内层闭包与共享链不断。否则 (前缀需要本帧
		// 自身的局部) 整段取帧数组 —— 自身段本就随每次调用新建, 帧数组
		// 作宿主是安全的 (被闭包保活)。
		//
		// 反例 (r6e5qp): getter 体内再定义的闭包若按帧数组捕获, getter 每次
		// 属性访问都重新调用, 内层闭包读到的是本次帧的私有快照, 与外层
		// cell 失联 —— make().foo() 恒返回 1; test262 async-generator yield*
		// 委托的 get-next/nextCount 记序模式全被它挡下。
		src := frame.Locals
		if len(frame.SharedCells) > 0 && prefixLen <= len(frame.SharedCells) {
			src = frame.SharedCells
		}
		// 裁剪到 prefixLen: 闭包只会访问 slot < prefixLen, 不裁剪会让整帧
		// Locals 在调用时被拷进内层 slot 区, 把尚未初始化的绑定"填上"外层
		// 残留值, TDZ 检测随之失效; 且前缀必须覆盖 prefixLen, 否则内层
		// LOAD 读到 NewFrame 的 nil 误报 TDZ。
		if prefixLen <= len(src) {
			captured = src[:prefixLen]
		} else {
			captured = src
		}
	}

	// this 绑定: 只有箭头函数 (以及编译器合成的 async 内层 generator —
	// 见 LexicalThis) 在创建时按词法捕获所在帧的 this (含主帧的 globalThis);
	// 其它非箭头函数的 this 由**调用形态**决定, 不在此绑定 —— 留 nil, 由
	// callClosure 按 sloppy 规则归一 (裸调用 → globalThis)。若给所有非箭头
	// 都写入外层 this, 裸调用会错误继承创建处的方法 this。
	var thisVal object.Value
	if meta.IsArrow || meta.LexicalThis {
		thisVal = vm.frameThis(frame)
	}

	// new.target 绑定: 只有箭头函数按词法捕获所在帧生效的 new.target (与 this
	// 同型)。箭头对 new.target 词法透明, 其取值必须解析到词法外层非箭头函数
	// —— 在**创建时**捕获才能跨越「箭头被返回后于别处调用」的情形 (rNAtZs)。
	// 非箭头留 nil, 由 callClosure 依调用形态 (OP_NEW / super 传递 / 普通调用)
	// 决定。async 内层合成 generator (LexicalThis) 不捕获: async 函数本身是
	// 普通函数, 其 new.target 与是否合成 generator 无关。
	var newTargetVal object.Value
	if meta.IsArrow {
		newTargetVal = vm.frameNewTarget(frame)
	}

	// M2: 记录"这个函数属于哪个源码单元" —— 模块导出的函数被入口调用时,
	// 抛错帧属于模块的 .ts, 而不是当前(入口)VM 的单元。只登记模块单元:
	// 入口函数的帧可直接回退 mainUnit, 登记它们只会让注册表无谓增长。
	if vm.units != nil && vm.mainUnit != nil && vm.mainUnit.isModule {
		vm.units.put(fn, vm.mainUnit)
	}
	// 函数对象自身的 [[Prototype]]: 按函数种类选择内建原型
	// (普通/箭头/类构造器 → %Function.prototype%; function* →
	// %GeneratorFunction.prototype%; async function → %AsyncFunction.prototype%;
	// async function* → %AsyncGeneratorFunction.prototype%)。这些原型对象由
	// stdlib 装配并注册 (object.GetXxxPrototype); 尚未注册时保持 nil，
	// 由 object.FuncPrototypeOf 回退到全局 %Function.prototype%。
	funcProto := object.FuncPrototypeForCompiled(fn)

	return &object.Closure{
		Fn:             fn,
		Env:            vm.globals,
		This:           thisVal,
		NewTarget:      newTargetVal,
		IsArrow:        meta.IsArrow,
		CapturedLocals: captured,
		CreatedAtFrame: vm.frameIdx,
		IterEpoch:      frame.Epoch,
		FuncPrototype:  funcProto,
	}
}

// trackClosure 记录闭包到当前帧的 CreatedClosures 列表。
// 用于 OP_STORE 时将外层变量修改传播到所有子闭包 (outer → closure)。

func (vm *VM) trackClosure(closure *object.Closure) {
	frame := vm.currentFrame()
	frame.CreatedClosures = append(frame.CreatedClosures, closure)
}

// callClosure 调用闭包函数。
// 创建新帧，复制捕获的局部变量，放置参数。

// sameConstantSlice 判断 pool 包装的常量切片与 slice 是否为同一底层数组。
// 空切片与空切片视为相同 (两种包装的 Get 行为一致: 越界返回 undefined)。
func sameConstantSlice(pool *bytecode.ConstantPool, slice []object.Value) bool {
	if pool == nil {
		return false
	}
	pc := pool.Constants
	if len(pc) != len(slice) {
		return false
	}
	if len(slice) == 0 {
		return true
	}
	return &pc[0] == &slice[0]
}

func (vm *VM) callClosure(closure *object.Closure, args []object.Value) error {
	fn := closure.Fn
	if fn == nil {
		return fmt.Errorf("VM: closure has no function")
	}

	// 检查调用栈深度 (jsThrow 使栈溢出 RangeError 能被 try/catch 捕获，
	// 也能经回调桥的 rethrowBridgeError 正确转回 JS 抛出流程)
	if vm.frameIdx >= MaxFrames-1 {
		return &jsThrow{Name: "RangeError", Message: "Maximum call stack size exceeded"}
	}

	// 创建新帧: 使用闭包自带的常量池 (跨模块时不同于 vm.constants)
	// T2-4 热点优化: 同模块调用 (fn.Constants 与 vm.constants 底层数组同一)
	// 直接复用 VM 的常量池包装, 免去每次调用一次 &ConstantPool{} 堆分配
	// (fib28 剖面: 每次调用 1 个, 占全部分配的 ~16%)。
	constants := vm.constants
	if fn.Constants != nil && !sameConstantSlice(vm.constants, fn.Constants) {
		constants = &bytecode.ConstantPool{Constants: fn.Constants}
	}
	frame := vm.acquireFrame(fn.Instructions, constants, fn.NumLocals)
	frame.Closure = closure
	// 本帧生效的 this: 箭头取词法捕获, 非箭头按 sloppy 归一 (裸调用 → globalThis)。
	frame.This = vm.resolveFrameThis(closure)
	// 本帧的 new.target:
	//   - 箭头函数: 取创建时词法捕获的 NewTarget (闭包携带), 与调用形态无关;
	//   - 构造调用 (OP_NEW) 或 super() 传递 (OP_NEW_TARGET_MARK): 取
	//     pendingNewTarget;
	//   - 其余: 取闭包自带的 NewTarget (直接 eval 包装闭包由 stdlib 写入调用者
	//     的 new.target; 普通函数为 nil ⇒ undefined)。
	// pendingNewTarget 是一次性的 (仅对紧邻的这次调用有效), 无论是否消费都清零。
	if closure.IsArrow {
		frame.NewTarget = closure.NewTarget
	} else if vm.hasPendingNewTarget {
		frame.NewTarget = vm.pendingNewTarget
	} else {
		frame.NewTarget = closure.NewTarget
	}
	if frame.NewTarget == nil {
		frame.NewTarget = object.UndefinedSingleton
	}
	vm.hasPendingNewTarget = false
	vm.pendingNewTarget = nil
	// 记录进入本帧时的栈高度, 返回时据此截断清理本帧残留栈值
	frame.StackBase = vm.stack.Len()

	// 复制捕获的外层局部变量作为初值
	if len(closure.CapturedLocals) > 0 {
		copy(frame.Locals, closure.CapturedLocals)
	}
	// 但写入必须回流到共享的 binding cell，供兄弟闭包与后续调用观察
	frame.SharedCells = closure.CapturedLocals

	// 放置参数: 参数从 BaseSlot 开始排列
	baseSlot := fn.BaseSlot

	// 检查是否有 rest 参数 (最后一个参数)
	hasRest := len(fn.Parameters) > 0 && fn.Parameters[len(fn.Parameters)-1].Rest
	restParamIndex := len(fn.Parameters) - 1

	if hasRest {
		// 非 rest 参数正常放置
		numRegular := restParamIndex
		for i := 0; i < numRegular; i++ {
			slot := baseSlot + i
			if slot < len(frame.Locals) {
				if i < len(args) {
					frame.Locals[slot] = args[i]
				} else {
					frame.Locals[slot] = object.UndefinedSingleton
				}
			}
		}
		// rest 参数: 收集剩余参数到数组
		var restElements []object.Value
		if len(args) > numRegular {
			restElements = make([]object.Value, len(args)-numRegular)
			copy(restElements, args[numRegular:])
		}
		restSlot := baseSlot + restParamIndex
		if restSlot < len(frame.Locals) {
			frame.Locals[restSlot] = object.NewArray(restElements)
		}
	} else {
		// 无 rest: 仅放置声明的前 NumParameters 个参数, 多余实参只进入 arguments
		placeCount := len(args)
		if placeCount > fn.NumParameters {
			placeCount = fn.NumParameters
		}
		for i := 0; i < placeCount; i++ {
			slot := baseSlot + i
			if slot < len(frame.Locals) {
				frame.Locals[slot] = args[i]
			}
		}
		// 剩余参数填充 undefined
		for i := placeCount; i < fn.NumParameters; i++ {
			slot := baseSlot + i
			if slot < len(frame.Locals) {
				frame.Locals[slot] = object.UndefinedSingleton
			}
		}
	}

	// 创建 arguments 对象 (类数组, 含全部实参)。放在参数放置之后, 避免被参数覆盖。
	// T2-4 热点优化: 函数体从未引用 arguments (编译期扫描 SkipArguments) 时
	// 跳过物化 —— 免去 argsCopy 切片 + arguments 数组两次堆分配/调用。
	if !fn.SkipArguments && fn.ArgumentsSlot >= 0 && fn.ArgumentsSlot < len(frame.Locals) {
		argsCopy := make([]object.Value, len(args))
		copy(argsCopy, args)
		frame.Locals[fn.ArgumentsSlot] = object.NewArray(argsCopy)
	}

	// 命名函数表达式的自引用: 名字槽位指向闭包自身 (递归入口)
	if fn.SelfSlot >= 0 && fn.SelfSlot < len(frame.Locals) {
		frame.Locals[fn.SelfSlot] = closure
	}

	vm.pushFrame(frame)
	return nil
}

// genHasPrologue 报告该生成器函数是否需要在调用时同步执行形参前导段
// (默认值/解构)。DeferParams 的例外是普通 async 函数的内层 generator:
// 它的形参绑定必须留在 __spawn 驱动路径内 (错误转 rejected Promise)。

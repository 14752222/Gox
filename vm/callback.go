package vm

import (
	"github.com/14752222/Gox/object"
	"sync"
)

// runtimeState 收敛 vm 包的可变全局状态 (原散落的包级 currentVM 变量)。
// 当前正在执行的 VM 实例, 供 stdlib 回调 JS 闭包时定位;
// 通过 swap/current 访问, 多 VM 或并发测试可显式替换整个 state 以隔离实例。
type runtimeState struct {
	mu        sync.RWMutex
	currentVM *VM
}

// state 是 vm 包唯一的可变全局状态对象。
var state = &runtimeState{}

// current 返回当前正在执行的 VM (无则 nil)。
func (s *runtimeState) current() *VM {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentVM
}

// swap 把当前 VM 设为 v, 返回恢复函数 (通常 defer 调用)。
func (s *runtimeState) swap(v *VM) (restore func()) {
	s.mu.Lock()
	prev := s.currentVM
	s.currentVM = v
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		s.currentVM = prev
		s.mu.Unlock()
	}
}

// setCallbackErrorValueFromThrow 把 Go 侧的运行期错误还原成原始 JS 抛出值,
// 存入 object 层供 stdlib 作为 rejection reason。
//
// 必须走这里而不是让 stdlib 重新包一层: callbackError 只是 Go 字符串
// (已含 "Error: " 前缀), 重新包一层会得到 "Error: Error: x" 双前缀,
// 且丢失原始 Error 对象 —— 而规范要求 catch 侧拿到的与抛出的
// 严格相等 (===)。

func setCallbackErrorValueFromThrow(err error) {
	if te, ok := err.(*ThrowError); ok {
		object.SetCallbackErrorValue(te.Value)
	} else if jt, ok := err.(*jsThrow); ok {
		// 栈溢出等结构化错误: 恢复为对应类型的 Error 对象,
		// 供 stdlib 的 callbackThrown 保留错误类型传播
		object.SetCallbackErrorValue(object.NewErrorWithName(jt.Name, jt.Message))
	}
}

func init() {
	// 注册回调桥: stdlib → object → vm
	//
	// 错误信号统一走 object 层的 SetCallbackError/TakeCallbackError:
	// 消费即清除。过去 VM 还有一份自己的 callbackErr 字段，只在个别
	// OP_CALL 位点被顺手消费 —— stdlib 层消费错误信号后 VM 层的副本
	// 会永久残留，之后任何一个内建函数调用都会被这个陈旧错误"击落"
	// (表现为回调链莫名中断且无任何报告)。
	object.SetCallFunction(func(fn object.Value, this object.Value, args []object.Value) object.Value {
		cur := state.current()
		if cur == nil {
			return object.UndefinedSingleton
		}
		result, err := cur.callFunction(fn, this, args)
		if err != nil {
			// 向 object 层暴露错误信号: 返回值无法区分
			// "函数正常返回" 与 "函数抛出了非 Error 异常"。
			object.SetCallbackError(err)
			// 保留原始抛出值 (throw x 的 x), 供 rejection reason 使用
			setCallbackErrorValueFromThrow(err)
			return object.UndefinedSingleton
		}
		return result
	})
	// 注册构造回调: object.ConstructFunction (ArraySpeciesCreate 等) → vm
	object.SetConstructFunction(func(fn object.Value, args []object.Value) object.Value {
		cur := state.current()
		if cur == nil {
			return object.UndefinedSingleton
		}
		result, err := cur.constructValue(fn, args)
		if err != nil {
			object.SetCallbackError(err)
			setCallbackErrorValueFromThrow(err)
			return object.UndefinedSingleton
		}
		return result
	})
	// 注册 generator 驱动回调: object.GeneratorNext → vm.genResume
	object.SetGeneratorNext(func(gen *object.Generator, arg object.Value) (object.Value, bool) {
		cur := state.current()
		if cur == nil {
			return object.UndefinedSingleton, true
		}
		val, done, err := cur.genResume(gen, arg)
		if err != nil {
			object.SetCallbackError(err)
			setCallbackErrorValueFromThrow(err)
			return object.UndefinedSingleton, true
		}
		return val, done
	})
	// 注册 generator 异常恢复回调: object.GeneratorThrow → vm.genThrow
	object.SetGeneratorThrow(func(gen *object.Generator, throwVal object.Value) (object.Value, bool) {
		cur := state.current()
		if cur == nil {
			return object.UndefinedSingleton, true
		}
		val, done, err := cur.genThrow(gen, throwVal)
		if err != nil {
			object.SetCallbackError(err)
			setCallbackErrorValueFromThrow(err)
			return object.UndefinedSingleton, true
		}
		return val, done
	})
	// 注册 generator return 完成回调: object.GeneratorReturn → vm.genReturn
	object.SetGeneratorReturn(func(gen *object.Generator, returnVal object.Value) (object.Value, bool) {
		cur := state.current()
		if cur == nil {
			return returnVal, true
		}
		val, done, err := cur.genReturn(gen, returnVal)
		if err != nil {
			object.SetCallbackError(err)
			setCallbackErrorValueFromThrow(err)
			return object.UndefinedSingleton, true
		}
		return val, done
	})
}

// VM 是 JavaScript 字节码虚拟机。

func (vm *VM) checkCallbackErr() error {
	err := object.TakeCallbackError()
	if err == nil {
		return nil
	}
	// 本次确有一次桥失败: 两槽同进同出。此处是我们亲手把错误信号取走的地方，
	// 值槽若留着，之后任何一个两槽消费者都会把它当成"本次抛出的值"。
	object.TakeCallbackErrorValue()
	return err
}

// throwIfError 判断内建函数的返回值是否应作为异常抛出，是则执行 throw 流程。
//
// 语义: 内建函数返回 *object.Error 时，通常表示"操作失败，请抛出异常"
// (如 JSON.parse 的语法错误、Array.prototype.map 收到非函数回调)。
// 例外: ReturnIsValue 标记的内建函数 (如 Error/TypeError 构造器) 返回的
// Error 是普通值——new Error("x") 应返回错误对象而不是抛出它。
//
// 返回值: thrown=true 表示已进入 throw 流程 (调用方应 continue 或返回 err，
// 切勿再把 result 压栈); thrown=false 表示 result 是普通值，正常压栈。

func (vm *VM) invokeWithThis(fn, thisVal object.Value, args []object.Value) error {
	// 一次性 new.target 传递标记 (super() 前由 OP_NEW_TARGET_MARK 置位):
	// 只有被调是 Closure 时由 callClosure 消费装配父构造器帧。内建/代理被调
	// 不装配 JS 帧, 一般直接丢弃以免泄漏给后续无关调用 —— 但 TypedArray
	// 派生类的 super(...) 是例外 (见下), 故先记录标记再决定是否清除。
	superCall := vm.hasPendingNewTarget
	if _, isClosure := fn.(*object.Closure); !isClosure {
		if !(superCall && isTypedArrayConstructCtor(fn)) {
			vm.hasPendingNewTarget = false
			vm.pendingNewTarget = nil
		}
	}
	switch callee := fn.(type) {
	case *object.BuiltinFunction:
		// 内建函数: 不传 this，直接传参数
		// super() 且父类是内建构造器 (如 class C extends Temporal.Duration):
		// 规范里 super() 走 [[Construct]], 内建构造器的 tgCtorGuard 依赖
		// IsConstructCall() 判定 new.target !== undefined。此处临时进入
		// 构造调用窗口, 让派生类的 super() 不被误判为普通函数调用。
		if superCall {
			object.EnterConstructCall()
		}
		result := callee.Fn(args...)
		if superCall {
			object.LeaveConstructCall()
		}
		if result == nil {
			result = object.UndefinedSingleton
		}
		if thrown, err := vm.throwIfError(result, callee.ReturnIsValue); thrown {
			return err
		}
		// TypedArray 派生类 super(...): 编译器把 super() 编成 OP_CALL_METHOD
		// (普通方法调用), 基类是内建构造器; 但规范里 super() 走 [[Construct]],
		// 结果 (Integer-Indexed exotic 实例) 必须成为派生类构造器帧的 this ——
		// 否则派生实例只是普通对象, 原型方法全部报 "receiver must be a
		// TypedArray"。此处把新帧 this 换成返回的 TypedArray。
		if superCall && thisVal != nil {
			if ta, isTA := result.(*object.TypedArray); isTA {
				// 派生类实例的 [[Prototype]] 应为派生类原型 (MyU8.prototype),
				// 而非同类型的 Uint8Array.prototype。new.target 即派生构造器闭包,
				// 取其 prototype 作为覆盖。
				if nt, ok := vm.pendingNewTarget.(*object.Closure); ok {
					if pv, found := nt.GetProperty("prototype"); found && pv != nil {
						ta.ProtoOverride = pv
					}
				}
				if f := vm.currentFrame(); f != nil {
					f.This = ta
				}
			}
			vm.hasPendingNewTarget = false
			vm.pendingNewTarget = nil
		}
		vm.stack.Push(result)
		if err := vm.checkCallbackErr(); err != nil {
			return vm.rethrowBridgeError(err)
		}
	case *object.BuiltinMethod:
		// 内建方法: this 作为第一个参数传递
		result := callee.Fn(thisVal, args...)
		if result == nil {
			result = object.UndefinedSingleton
		}
		if thrown, err := vm.throwIfError(result, false); thrown {
			return err
		}
		vm.stack.Push(result)
		if err := vm.checkCallbackErr(); err != nil {
			return vm.rethrowBridgeError(err)
		}
	case *object.Closure:
		// 箭头函数: this 是创建时的词法绑定, 与调用形态 (含方法调用) 无关 ——
		// 原样调用, 不重绑 thisVal。非箭头函数: 用 thisVal 作接收者创建绑定闭包
		// (callClosure 再按 sloppy 规则把 undefined/null 归一为 globalThis)。
		bound := callee
		if !callee.IsArrow {
			bound = &object.Closure{
				Fn:             callee.Fn,
				Env:            callee.Env,
				This:           thisVal,
				NewTarget:      callee.NewTarget,
				IsArrow:        callee.IsArrow,
				CapturedLocals: callee.CapturedLocals,
			}
		}
		// generator 方法调用: 创建 Generator (this 绑定保留在闭包中)。
		// 有形参前导段时同样先建立前导帧, 由主循环同步绑定形参后冻结。
		if bound.Fn != nil && bound.Fn.IsGenerator {
			if genHasPrologue(bound.Fn) {
				if err := vm.setupGenPrologueFrame(bound, args); err != nil {
					return vm.throwJSError(err)
				}
			} else {
				vm.stack.Push(object.NewGenerator(bound, args))
			}
		} else {
			if err := vm.callClosure(bound, args); err != nil {
				return vm.throwJSError(err)
			}
		}
	case *object.Proxy:
		// 代理方法调用: 转发到 apply trap，this 为 thisVal
		result, err := vm.proxyApply(callee, thisVal, args)
		if err != nil {
			return vm.rethrowBridgeError(err)
		}
		vm.stack.Push(result)
	default:
		return vm.throwNamedError("TypeError", "%s is not a function", describeCallee(fn))
	}
	return nil
}

// isTypedArrayConstructCtor 报告 fn 是否为某个 TypedArray 构造器内建函数
// (Uint8Array / BigInt64Array 等)。供派生类 super(...) 识别父构造器。
func isTypedArrayConstructCtor(fn object.Value) bool {
	b, ok := fn.(*object.BuiltinFunction)
	return ok && b.IsTypedArrayCtor
}

func describeCallee(v object.Value) string {
	if v == nil {
		return "undefined"
	}
	if name := builtinName(v); name != "" {
		return name
	}
	s := v.Inspect()
	if len(s) > 64 {
		s = s[:64] + "..."
	}
	return s
}

// consumeEvalMark 消费一次 OP_EVAL_MARK / OP_EVAL_MARK_INIT (及 roiE5Z 的
// OP_EVAL_MARK_HOME 家族): 弹出被调值后调用。
// 只有当被调恰为全局 %eval% 内建 (直接 eval 规范要求引用的值即内建本身)
// 时:
//   - 把**调用者帧生效的 this** 经 object 层桥传给 eval 内建 (direct eval
//     的 this 绑定与调用者一致 —— 规范 sec-performeval);
//   - 把调用者帧的 new.target 与「该语境是否允许 new.target」一并传入;
//   - 把调用点的 super home 上下文 (标记携带) 传入 (roiE5Z);
//   - 若本次是类字段初始化器内的直接 eval, 再置位 stdlib 的一次性受限
//     标志, 让 eval 内建进入补充早错模式。
//
// 否则直接丢弃标记 —— 被局部变量遮蔽的 eval 不是直接 eval, 既不应继承 this
// 也不应受限, 标记也不能泄漏给后续无关的 eval 调用。
// 无论是否命中都把 vm.pendingDirectEval / pendingEvalInit / pendingEvalHome*
// 清零 (标记只对紧邻的这一次调用有效)。

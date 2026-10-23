package compiler

import (
	"fmt"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/object"
)

func (c *Compiler) compileExpression(expr ast.Expression) error {
	switch node := expr.(type) {
	case *ast.IntegerLiteral:
		idx := c.constants.AddConstant(object.NewInt(node.Value))
		c.emitter.Emit(bytecode.OP_CONST, idx)
		return nil
	case *ast.FloatLiteral:
		idx := c.constants.AddConstant(object.NewNumber(node.Value))
		c.emitter.Emit(bytecode.OP_CONST, idx)
		return nil
	case *ast.BigIntLiteral:
		// parser 已校验过合法性，此处不会失败；仍保留错误处理以防御
		// 手工构造的 AST (如 eval 注入场景)。
		v, ok := object.ParseBigIntLiteral(node.Raw)
		if !ok {
			return fmt.Errorf("invalid BigInt literal: %q", node.Raw)
		}
		idx := c.constants.AddConstant(object.NewBigInt(v))
		c.emitter.Emit(bytecode.OP_CONST, idx)
		return nil
	case *ast.StringLiteral:
		idx := c.constants.AddConstant(object.NewString(node.Value))
		c.emitter.Emit(bytecode.OP_CONST, idx)
		return nil
	case *ast.BooleanLiteral:
		if node.Value {
			c.emitter.EmitNoOperand(bytecode.OP_TRUE)
		} else {
			c.emitter.EmitNoOperand(bytecode.OP_FALSE)
		}
		return nil
	case *ast.NullLiteral:
		c.emitter.EmitNoOperand(bytecode.OP_NULL)
		return nil
	case *ast.UndefinedLiteral:
		c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
		return nil
	case *ast.RegexLiteral:
		// 将正则字面量编译为常量 (RegExp 对象)
		re, err := object.NewRegExp(node.Pattern, node.Flags)
		if err != nil {
			return err
		}
		idx := c.constants.AddConstant(re)
		c.emitter.Emit(bytecode.OP_CONST, idx)
		return nil
	case *ast.Identifier:
		return c.compileIdentifier(node)
	case *ast.BinaryExpression:
		return c.compileBinaryExpression(node)
	case *ast.UnaryExpression:
		return c.compileUnaryExpression(node)
	case *ast.AssignmentExpression:
		return c.compileAssignmentExpression(node)
	case *ast.LogicalExpression:
		return c.compileLogicalExpression(node)
	case *ast.SequenceExpression:
		return c.compileSequenceExpression(node)
	case *ast.ConditionalExpression:
		return c.compileConditionalExpression(node)
	case *ast.CallExpression:
		return c.compileCallExpression(node)
	case *ast.MemberExpression:
		return c.compileMemberExpression(node)
	case *ast.OptionalMemberExpression:
		return c.compileOptionalMember(node)
	case *ast.OptionalCallExpression:
		return c.compileOptionalCall(node)
	case *ast.ArrayLiteral:
		return c.compileArrayLiteral(node)
	case *ast.ObjectLiteral:
		return c.compileObjectLiteral(node)
	case *ast.TemplateLiteral:
		return c.compileTemplateLiteral(node)
	case *ast.TaggedTemplateExpression:
		return c.compileTaggedTemplate(node)
	case *ast.DynamicImportExpression:
		return c.compileDynamicImport(node)
	case *ast.FunctionExpression:
		return c.compileFunctionExpression(node)
	case *ast.ClassExpression:
		return c.compileClassExpression(node)
	case *ast.PrivateIdentifier:
		// 裸私有名只合法于 `#x in obj` 的左操作数 —— 编译为混编码键常量,
		// 与 OP_IN 的 [key, obj] 栈约定一致 (compileBinaryExpression 先左后右)。
		// 出现在其他位置 (如 + #x) 会在后续运算指令以错误类型消费,
		// 运行时报 TypeError —— 与"语法位置受限"的工程取舍一致。
		return c.emitPrivateKey("#" + node.Name)
	case *ast.ArrowFunctionExpression:
		return c.compileArrowFunctionExpression(node)
	case *ast.ThisExpression:
		c.emitter.EmitNoOperand(bytecode.OP_THIS)
		return nil
	case *ast.MetaProperty:
		// new.target: 从当前帧的构造目标读取。普通调用/箭头函数帧为 undefined,
		// 构造调用 (OP_NEW) 与 super() 传递 (OP_NEW_TARGET_MARK) 时由 VM 写入。
		c.emitter.EmitNoOperand(bytecode.OP_NEW_TARGET)
		return nil
	case *ast.YieldExpression:
		// yield* expr: 委托给可迭代对象。
		// async generator 体内走异步委托 (每步 await), 其余走同步委托。
		if node.Delegate && node.Value != nil {
			if c.asyncGeneratorBody {
				return c.compileYieldDelegateAsync(node.Value)
			}
			return c.compileYieldDelegate(node.Value)
		}
		// yield [expr]: 编译 expr (默认 undefined), 然后 OP_YIELD 暂停。
		//
		// async generator 体内 `yield X` 规范语义是 AsyncGeneratorYield(Await(X))
		// —— 值必须先 await (reject 则当作在该 yield 点抛出; test262
		// yield-promise-reject-next)。await 在编译期显式发射 (OP_AWAIT),
		// 于是驱动层对 OP_YIELD 的值不再二次 await: 这恰好让 yield* 委托
		// (compileYieldDelegateAsync, 委托值按规范**不** await) 与普通 yield
		// 共用同一条 OP_YIELD 结算路径。
		if node.Value != nil {
			if err := c.compileExpression(node.Value); err != nil {
				return err
			}
		} else {
			c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
		}
		if c.asyncGeneratorBody {
			c.emitter.EmitNoOperand(bytecode.OP_AWAIT)
		}
		c.emitter.EmitNoOperand(bytecode.OP_YIELD)
		return nil
	case *ast.AwaitExpression:
		// await expr: 在 async 函数/generator 的内层 generator 中编译为挂起点
		// (恢复时压入 Promise 的 resolved 值作为表达式结果)。
		//
		// async generator 体内用 OP_AWAIT: 它与 OP_YIELD 的帧语义完全相同,
		// 但会让异步生成器驱动识别为内部挂起点 (等待后自动恢复), 与消费者
		// 可见的 yield (OP_YIELD) 区分开。普通 async 函数体内仍是 OP_YIELD。
		//
		// 模块顶层 (fnDepth==0) 的 await 是顶层 await (TLA): 它同样编为
		// OP_YIELD, 但主单元本身成为生成器 —— 由 vm.RunCompiledAsync 驱动。
		if c.moduleMode && c.fnDepth == 0 {
			c.topLevelAwait = true
		}
		if err := c.compileExpression(node.Argument); err != nil {
			return err
		}
		if c.asyncGeneratorBody {
			c.emitter.EmitNoOperand(bytecode.OP_AWAIT)
		} else {
			c.emitter.EmitNoOperand(bytecode.OP_YIELD)
		}
		return nil
	case *ast.NewExpression:
		return c.compileNewExpression(node)
	case *ast.SuperExpression:
		return fmt.Errorf("compiler: unexpected super (must be followed by '(' or '.')")
	default:
		return fmt.Errorf("unsupported expression type: %T", expr)
	}
}

// compileYieldDelegate 编译 yield* expr: 逐个取出 expr 的迭代值并 yield，
// 直到迭代结束。整个表达式的值为 undefined (不取 delegate 的 return 值)。
//
// 迭代器存放在隐藏局部槽位而不是操作数栈上: OP_YIELD 挂起时只保存
// StackBase 之上的栈值，若迭代器留在栈上跨 yield 存活，generator 正常
// 返回时它会残留在调用方栈上 (rebuildGenFrame 把保存值恢复到帧基之下)。

func (c *Compiler) compileYieldDelegate(expr ast.Expression) error {
	if err := c.compileExpression(expr); err != nil {
		return err
	}
	c.emitter.EmitNoOperand(bytecode.OP_GET_ITERATOR)
	sym := c.scope.Define("%yield*iter%", false)
	c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))

	loopStart := c.emitter.Pos()
	c.emitter.Emit(bytecode.OP_LOAD, uint16(sym.Slot))
	c.emitter.EmitNoOperand(bytecode.OP_ITER_NEXT)
	// OP_ITER_NEXT 迭代结束时压入 undefined —— 与 for-of 一致用 NULL 判断
	c.emitter.EmitNoOperand(bytecode.OP_DUP)
	endJump := c.emitter.EmitJump(bytecode.OP_JUMP_IF_NULL)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	// [iter, v] → [v]: 迭代器副本不能跨 YIELD 存活 (见函数头注释)
	c.emitter.EmitNoOperand(bytecode.OP_SWAP)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.EmitNoOperand(bytecode.OP_YIELD)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弃掉 next() 传入的恢复值
	c.emitter.Emit(bytecode.OP_LOOP, uint16(loopStart))

	c.emitter.PatchJump(endJump)
	// 跳转时栈为 [iter, u, u] (JUMP_IF_NULL 只窥视不弹出): 清空后
	// 压入 undefined 作为 yield* 表达式的值
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
	return nil
}

// compileYieldDelegateAsync 编译 async generator 体内的 yield* 异步委托
// (规范 AsyncGeneratorYieldDelegate / YieldExpression: yield*)。
//
// 三种完成类型都必须在挂起点被拦截 (规范 7.a/7.b/7.c):
//   - normal: step = Await(iter.next(recv)); 未完成则 yield step.value;
//             恢复值作为下一轮 recv。done 时 yield* 表达式的值 = step.value。
//   - throw:  step = Await(GetMethod(iter,"throw").call(iter, e)); 继续循环;
//             无 throw 方法 ⇒ AsyncIteratorClose(iter) 后抛 TypeError。
//   - return: step = Await(GetMethod(iter,"return").call(iter, v));
//             done ⇒ 完成生成器 (值 Await(step.value));
//             未完成 ⇒ yield step.value 继续循环 (received 归为 normal);
//             无 return 方法 ⇒ Await(v) 后以 v 完成生成器。
//
// return 完成的拦截不能走 catch (规范: catch 不拦 return), 故用新增的
// OP_PUSH_RET_TRY 在委托条目上注册一个 return 处理 PC —— 与 catchPC 拦截
// throw 对称 (见 vm.handleReturnInner)。
//
// 被委托迭代器与递归状态一律放隐藏局部槽 (iter/recv/step/out/err/retv),
// 绝不留在操作数栈上跨挂起点 —— OP_AWAIT/OP_YIELD 挂起时只保存帧基之上的
// 栈值, 跨挂起存活的迭代器会成为残缺值, 污染调用方栈 (见重建帧语义)。

func (c *Compiler) compileYieldDelegateAsync(expr ast.Expression) error {
	if err := c.compileExpression(expr); err != nil {
		return err
	}
	// [src] → [asyncIter]  (Symbol.asyncIterator 优先, 回退同步可迭代形状)
	c.emitter.EmitNoOperand(bytecode.OP_GET_ASYNC_ITERATOR)
	iterSym := c.scope.Define("%yield*async-iter%", false)
	c.emitter.Emit(bytecode.OP_STORE, uint16(iterSym.Slot)) // []
	recvSym := c.scope.Define("%yield*async-recv%", false)  // next() 传来的 normal 值
	stepSym := c.scope.Define("%yield*async-step%", false)
	outSym := c.scope.Define("%yield*async-out%", false) // 待 yield 的值 (跨挂在槽)
	errSym := c.scope.Define("%yield*async-err%", false)
	retvSym := c.scope.Define("%yield*async-retv%", false)
	// received 初值 undefined (首次 next() 传 undefined)
	c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
	c.emitter.Emit(bytecode.OP_STORE, uint16(recvSym.Slot))

	doneIdx := c.constants.AddConstant(object.NewString("done"))
	valueIdx := c.constants.AddConstant(object.NewString("value"))
	throwIdx := c.constants.AddConstant(object.NewString("throw"))
	returnIdx := c.constants.AddConstant(object.NewString("return"))

	// ---- 循环头: step = await iter.next(received), 校验为对象 ----
	loopStart := c.emitter.Pos()
	c.emitter.Emit(bytecode.OP_LOAD, uint16(iterSym.Slot))
	c.emitter.Emit(bytecode.OP_LOAD, uint16(recvSym.Slot))
	c.emitter.EmitNoOperand(bytecode.OP_ASYNC_ITER_NEXT_ARG) // [step]
	c.emitter.EmitNoOperand(bytecode.OP_AWAIT)               // [resolvedStep]
	c.emitGlobalLoad("__async_iter_check")
	c.emitter.Emit(bytecode.OP_CALL, 1)                     // [checkedStep]
	c.emitter.Emit(bytecode.OP_STORE, uint16(stepSym.Slot)) // []
	toProcess := c.emitter.EmitJump(bytecode.OP_JUMP)

	// ---- 消费者 throw(e) 的转发落点 (栈顶 = 抛入的 e) ----
	// 规范 7.b: received 为 throw 时调 iter.throw(e), 结果当作步进结果继续;
	// throw 方法缺失 ⇒ AsyncIteratorClose(iter) 后抛 TypeError。
	throwHandler := c.emitter.Pos()
	c.emitter.Emit(bytecode.OP_STORE, uint16(errSym.Slot)) // []
	c.emitter.Emit(bytecode.OP_LOAD, uint16(iterSym.Slot))
	c.emitter.Emit(bytecode.OP_GET_PROP, throwIdx) // [tfn]
	c.emitter.EmitNoOperand(bytecode.OP_DUP)       // [tfn, tfn]
	noThrow := c.emitter.EmitJump(bytecode.OP_JUMP_IF_NULL)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // [tfn]
	c.emitter.Emit(bytecode.OP_LOAD, uint16(iterSym.Slot))
	c.emitter.Emit(bytecode.OP_LOAD, uint16(errSym.Slot))
	c.emitter.Emit(bytecode.OP_CALL_METHOD, 1) // [result]
	c.emitter.EmitNoOperand(bytecode.OP_AWAIT)
	c.emitGlobalLoad("__async_iter_check")
	c.emitter.Emit(bytecode.OP_CALL, 1)
	c.emitter.Emit(bytecode.OP_STORE, uint16(stepSym.Slot))
	toProcess2 := c.emitter.EmitJump(bytecode.OP_JUMP)

	c.emitter.PatchJump(noThrow)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹掉 DUP 的 tfn
	// AsyncIteratorClose(iter): GetMethod(iter,"return"), 可调用则以 iter 为
	// this 调之 (参数 undefined), 丢弃结果。再抛 TypeError。
	c.emitAsyncIteratorClose(iterSym.Slot, returnIdx)
	c.emitThrowNamedError("TypeError", "iterator does not provide a 'throw' method")

	// ---- 消费者 return(v) 的转发落点 (栈顶 = 传入的 v) ----
	// 规范 7.c: 调 iter.return(v), 结果 awaited; done ⇒ 完成生成器; 未完成 ⇒
	// 把结果 value yield 给消费者, 恢复值作为 normal received 继续循环;
	// return 方法缺失 ⇒ Await(v) 后以 v 完成生成器。
	returnHandler := c.emitter.Pos()
	c.emitter.Emit(bytecode.OP_STORE, uint16(retvSym.Slot)) // []
	c.emitter.Emit(bytecode.OP_LOAD, uint16(iterSym.Slot))
	c.emitter.Emit(bytecode.OP_GET_PROP, returnIdx) // [rfn]
	c.emitter.EmitNoOperand(bytecode.OP_DUP)        // [rfn, rfn]
	noReturn := c.emitter.EmitJump(bytecode.OP_JUMP_IF_NULL)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // [rfn]
	c.emitter.Emit(bytecode.OP_LOAD, uint16(iterSym.Slot))
	c.emitter.Emit(bytecode.OP_LOAD, uint16(retvSym.Slot))
	c.emitter.Emit(bytecode.OP_CALL_METHOD, 1) // [result]
	c.emitter.EmitNoOperand(bytecode.OP_AWAIT)
	c.emitGlobalLoad("__async_iter_check")
	c.emitter.Emit(bytecode.OP_CALL, 1)
	c.emitter.Emit(bytecode.OP_STORE, uint16(stepSym.Slot))
	// step.done ?
	c.emitter.Emit(bytecode.OP_LOAD, uint16(stepSym.Slot))
	c.emitter.Emit(bytecode.OP_GET_PROP, doneIdx) // [done]
	retDone := c.emitter.EmitJump(bytecode.OP_JUMP_IF_TRUE)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 未完成: 弹掉 done
	c.emitter.Emit(bytecode.OP_LOAD, uint16(stepSym.Slot))
	c.emitter.Emit(bytecode.OP_GET_PROP, valueIdx)
	c.emitter.Emit(bytecode.OP_STORE, uint16(outSym.Slot))
	yieldAgain := c.emitter.EmitJump(bytecode.OP_JUMP) // 回到挂起点再 yield

	c.emitter.PatchJump(retDone)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹掉 done (true)
	c.emitter.Emit(bytecode.OP_LOAD, uint16(stepSym.Slot))
	c.emitter.Emit(bytecode.OP_GET_PROP, valueIdx)
	c.emitter.EmitNoOperand(bytecode.OP_AWAIT) // 规范: 完成值需 Await
	c.emitter.EmitNoOperand(bytecode.OP_RETURN)

	c.emitter.PatchJump(noReturn)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹掉 DUP 的 rfn
	c.emitter.Emit(bytecode.OP_LOAD, uint16(retvSym.Slot))
	c.emitter.EmitNoOperand(bytecode.OP_AWAIT) // 规范: 缺 return 方法时 Await(v)
	c.emitter.EmitNoOperand(bytecode.OP_RETURN)

	// ---- 处理已取得的 step ----
	c.emitter.PatchJump(toProcess)
	c.emitter.PatchJump(toProcess2)
	c.emitter.Emit(bytecode.OP_LOAD, uint16(stepSym.Slot))
	c.emitter.Emit(bytecode.OP_GET_PROP, doneIdx) // [done]
	endJump := c.emitter.EmitJump(bytecode.OP_JUMP_IF_TRUE)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 假值路径: 弹出 done
	c.emitter.Emit(bytecode.OP_LOAD, uint16(stepSym.Slot))
	c.emitter.Emit(bytecode.OP_GET_PROP, valueIdx)
	c.emitter.Emit(bytecode.OP_STORE, uint16(outSym.Slot)) // []

	// ---- 挂起点: 一个 try 条目同时拦截 throw (catchPC) 与 return (retPC) ----
	c.emitter.PatchJump(yieldAgain)
	c.emitter.Emit(bytecode.OP_PUSH_TRY, uint16(throwHandler))
	c.emitter.Emit(bytecode.OP_PUSH_RET_TRY, uint16(returnHandler))
	c.emitter.Emit(bytecode.OP_LOAD, uint16(outSym.Slot))
	c.emitter.EmitNoOperand(bytecode.OP_YIELD) // [received]
	c.emitter.EmitNoOperand(bytecode.OP_POP_TRY)
	c.emitter.Emit(bytecode.OP_STORE, uint16(recvSym.Slot))
	c.emitter.Emit(bytecode.OP_LOOP, uint16(loopStart))

	// ---- 结束路径: 取 step.value 作为 yield* 表达式的值 ----
	c.emitter.PatchJump(endJump)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹掉 done
	c.emitter.Emit(bytecode.OP_LOAD, uint16(stepSym.Slot))
	c.emitter.Emit(bytecode.OP_GET_PROP, valueIdx)
	return nil
}

// emitThrowNamedError 发射「无条件抛出某个全局命名错误」的指令序列:
//
//	CONST "<msg>"                ; [msg]
//	LOAD_GLOBAL "<name>"         ; [msg, ctor]
//	CALL 1                       ; [errObj]
//	THROW                        ; 抛出

func (c *Compiler) emitThrowNamedError(name, msg string) {
	idx := c.constants.AddConstant(object.NewString(msg))
	c.emitter.Emit(bytecode.OP_CONST, idx)
	c.emitGlobalLoad(name)
	c.emitter.Emit(bytecode.OP_CALL, 1)
	c.emitter.EmitNoOperand(bytecode.OP_THROW)
}

// emitAsyncIteratorClose 发射 AsyncIteratorClose(iter) (规范 7.4.11 的简化):
// GetMethod(iter,"return"); 为 null/undefined 则跳过, 否则以 iter 为 this
// 调用之 (参数 undefined), 丢弃返回值。iter 取局部槽 slot。
//
//	LOAD iter                     ; [iter]
//	GET_PROP "return"             ; [rfn]
//	DUP                           ; [rfn, rfn]
//	JUMP_IF_NULL done             ; (窥视)
//	POP                           ; [rfn]
//	LOAD iter                     ; [rfn, iter]
//	CALL_METHOD 0                 ; [result]
//	AWAIT                         ; [resolved]
//	POP                           ; []
//	done:
//	... (JUMP_IF_NULL 只窥视, 跳转时栈上残留 [rfn], 需在 done 处弹掉)

func (c *Compiler) emitAsyncIteratorClose(iterSlot int, returnIdx uint16) {
	c.emitter.Emit(bytecode.OP_LOAD, uint16(iterSlot))
	c.emitter.Emit(bytecode.OP_GET_PROP, returnIdx)
	c.emitter.EmitNoOperand(bytecode.OP_DUP)
	done := c.emitter.EmitJump(bytecode.OP_JUMP_IF_NULL)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.Emit(bytecode.OP_LOAD, uint16(iterSlot))
	c.emitter.Emit(bytecode.OP_CALL_METHOD, 0)
	c.emitter.EmitNoOperand(bytecode.OP_AWAIT)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	after := c.emitter.EmitJump(bytecode.OP_JUMP)
	c.emitter.PatchJump(done)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 栈上残留的 rfn (null/undefined)
	c.emitter.PatchJump(after)
}

// isGlobalScope 返回当前是否处于全局作用域 (depth 0) 且非模块模式。
// 全局作用域的变量读写走 GLOBAL/DECLARE 指令，写入共享的全局环境，
// 从而支持 REPL 跨输入状态保持。模块模式保持旧行为 (局部 slot)，隔离模块命名空间。

func (c *Compiler) emitGlobalLoad(name string) {
	idx := c.constants.AddConstant(object.NewString(name))
	c.emitter.Emit(bytecode.OP_LOAD_GLOBAL, idx)
}

// emitThrowReferenceError 发射「无条件抛出 ReferenceError」的指令序列。
// OP_CALL 约定实参在栈上、callee 在栈顶 (见 compileCallExpression), 故:
//
//	CONST "<msg>"                  ; [msg]
//	LOAD_GLOBAL "ReferenceError"   ; [msg, ctor]
//	CALL 1                         ; [errObj]  (Error 可作函数调用, 返回新实例)
//	THROW                          ; 抛出
//
// 用于形参默认值的 TDZ 违例 (引用自身/后位形参) —— 该处按规范必须是
// ReferenceError, 且**不得**回退去读外层/全局同名绑定 (形参环境已将该名
// 绑进 TDZ, 查找到此为止)。编译期无法直接构造 Error 实例, 故走运行时构造。

func (c *Compiler) emitThrowReferenceError(name string) {
	idx := c.constants.AddConstant(object.NewString(
		fmt.Sprintf("Cannot access '%s' before initialization", name)))
	c.emitter.Emit(bytecode.OP_CONST, idx)
	c.emitGlobalLoad("ReferenceError")
	c.emitter.Emit(bytecode.OP_CALL, 1)
	c.emitter.EmitNoOperand(bytecode.OP_THROW)
}

// emitSuperLoad 加载 extends 的父类构造器。
//
// 为什么不能直接 emitGlobalLoad: 模块模式 (被 import 的模块) 里父类名是模块
// 顶层的局部槽位, 不是全局变量 —— 早期实现对 super 一律走 LOAD_GLOBAL, 于是
// `class C extends B {}` 在模块里报 "B is not defined"。这里先按作用域解析
// (局部槽位), 解析不到再退回全局 (入口脚本场景)。

func (c *Compiler) emitSuperLoad(name string) {
	if sym := c.scope.Resolve(name); sym != nil && !(sym.Depth == 0 && !c.moduleMode) {
		c.emitLoad(sym)
		return
	}
	c.emitGlobalLoad(name)
}

// emitTypeOfGlobal 发射针对未绑定标识符的 typeof 指令。
// 与 OP_LOAD_GLOBAL + OP_TYPEOF 的区别: 名字未定义时不抛 ReferenceError，
// 而是得到 "undefined"。

func (c *Compiler) emitTypeOfGlobal(name string) {
	idx := c.constants.AddConstant(object.NewString(name))
	c.emitter.Emit(bytecode.OP_TYPEOF_GLOBAL, idx)
}

// emitGlobalStore 发射全局变量存储指令 (按名字写全局环境, 值保留在栈上)。

func (c *Compiler) emitGlobalStore(name string) {
	idx := c.constants.AddConstant(object.NewString(name))
	c.emitter.Emit(bytecode.OP_STORE_GLOBAL, idx)
}

// emitAssignmentStore 发射对**名字**的赋值存储 (脚本/全局语境)。
//   - 顶层 let 绑定 (sym 非 nil 且非 const 非 var): 走 OP_STORE_LEXICAL_GLOBAL
//     —— 绑定尚未由声明语句创建时抛 TDZ ReferenceError;
//   - 未声明名 (sym==nil) 在严格模式下 → OP_STORE_UNDECLARED
//     (运行期无同名绑定则抛 ReferenceError); sloppy 下仍走 OP_STORE_GLOBAL
//     (隐式建全局属性, 规范 sloppy 语义);
//   - 其余已解析全局绑定 → OP_STORE_GLOBAL (const 由运行期 IsConst 拦)。

func (c *Compiler) emitAssignmentStore(name string, sym *Symbol) {
	if sym != nil && !c.moduleMode && !sym.IsConst && !sym.IsVarLike {
		idx := c.constants.AddConstant(object.NewString(name))
		c.emitter.Emit(bytecode.OP_STORE_LEXICAL_GLOBAL, idx)
		return
	}
	if sym == nil && c.strict {
		idx := c.constants.AddConstant(object.NewString(name))
		c.emitter.Emit(bytecode.OP_STORE_UNDECLARED, idx)
		return
	}
	c.emitGlobalStore(name)
}

// emitGlobalDeclare 发射全局变量声明指令 (按名字写入全局环境, 弹出值)。
// 与 emitGlobalStore 的区别: 声明语义上总是定义绑定 (已存在时更新),
// 用于 let/const 与 import 绑定。

func (c *Compiler) emitGlobalDeclare(name string) {
	idx := c.constants.AddConstant(object.NewString(name))
	c.emitter.Emit(bytecode.OP_DECLARE, idx)
}

// emitLoad 根据符号作用域发射加载指令。
// 全局符号 → OP_LOAD_GLOBAL (按名字); 局部符号 → OP_LOAD (按 slot)。
// computeCapturePrefixLen 扫描已编译函数体的指令流, 得出它实际引用的
// 外层局部槽的精确上界 (= max referenced outer slot + 1)。
//
// 为什么不用 BaseSlot (外层作用域槽总数): BaseSlot 会把「创建帧自身的
// 局部」也捎带进本函数的捕获前缀。VM 创建闭包时若按 BaseSlot 从创建帧
// 的拷贝数组 (帧 Locals) 取捕获, 内层闭包读到的是创建帧私有快照, 与
// 外层 binding cell 失联; 若按精确上界, 引用只落在外层 cell 数组时,
// 捕获就能直接共享那颗 cell (即 frame.SharedCells 底层) ——
// test262 async-generator yield* 委托系列的 get-next/nextCount 记序
// 模式依赖此语义 (r6e5qp)。
//
// 只统计「槽位语义」的指令 (LOAD/STORE/STORE_CONST), 且仅取 operand <
// baseSlot 的 (本函数自身的槽位不属外层引用)。上界偏大只会保守回退帧
// 数组 (旧行为), 不会错。
//
// ===== 嵌套函数的前缀必须并入 (ryB64Z 同族, rtWB7N) =====
//
// 只扫本函数指令流是不够的: 本函数体内**定义的嵌套函数**也会引用外层槽,
// 而它是在**本函数的帧里**创建的 —— createClosure 取捕获源的判据是
// `prefixLen <= len(frame.SharedCells)`, SharedCells 就是本函数闭包捕获
// 到的那段外层 cell。本函数若没把内层需要的槽位捕获进来, 内层就退化到
// 读本函数帧的 Locals —— 那里对应的是本函数**自己**的槽位 (尚未初始化),
// 于是内层读外层变量读到 nil, 表现为
// "Cannot access lexical declaration before initialization"。
//
// 最小复现 (工厂 + JSX 回调):
//
//	function myh(tag, props) { props.onDraw(1); return 1; }
//	export function make() {
//	  const n = () => 7;                                  // 外层 slot 3
//	  const W = () => myh("canvas", { onDraw: () => n() }); // W 自己不引用 n
//	  return { W };
//	}
//
// W 自己只引用 slot 0 ⇒ 旧算法 prefix=1; 而 W 体内的 onDraw 要 slot 3 ⇒
// 需要 prefix=4。W 少捕获的 slot 1..3 让 onDraw 读到 W 帧的 nil 槽位。
//
// 所以这里对每条建函数的指令取它的元数据, 把子函数的 CapturePrefixLen
// 并入上界。子函数的元数据在它 finalize 时已算好 (含它自己的嵌套), 而父子
// 共用同一套 slot 编号, 直接取 max 即可 —— 不需要再递归扫一遍子函数体。

func (c *Compiler) emitLoad(sym *Symbol) {
	if sym.Depth == 0 && !c.moduleMode {
		c.emitGlobalLoad(sym.Name)
	} else {
		c.emitter.Emit(bytecode.OP_LOAD, uint16(sym.Slot))
	}
}

// emitStore 根据符号作用域发射存储指令。
// 全局符号 → OP_STORE_GLOBAL (按名字); 局部符号 → OP_STORE (按 slot)。

func (c *Compiler) emitStore(sym *Symbol) {
	if sym.Depth == 0 && !c.moduleMode {
		c.emitGlobalStore(sym.Name)
	} else {
		c.emitLocalStore(sym)
	}
}

// emitLocalStore 发射「写局部槽位」的指令 (sym 为已解析的局部符号)。
// const 绑定改发 OP_STORE_CONST_GUARD (运行期抛 TypeError), 其余 OP_STORE。
// 用于**赋值位置** (x = v、复合赋值、++/--、解构/for-of 赋值目标) ——
// 与 OP_STORE_CONST (声明期一次性初始化, 允许 TDZ 的 nil 槽) 相对。
// 模块顶层的 const 也走局部槽位 (见 isGlobalScope: 模块模式恒 false),
// 故这里必须自己守卫, 否则模块顶层 const 完全可变。

func (c *Compiler) emitLocalStore(sym *Symbol) {
	if sym.IsConst {
		nameIdx := c.constants.AddConstant(object.NewString(sym.Name))
		c.emitter.Emit(bytecode.OP_STORE_CONST_GUARD, nameIdx)
		return
	}
	// let 绑定 (非 var 非 const): 赋值位置若槽位仍为 nil 即处于 TDZ,
	// 须抛 ReferenceError —— 发 OP_STORE_LEXICAL (槽位仍为 nil 即 TDZ,
	// 抛 ReferenceError); var / 其它发 OP_STORE (var 提升即 undefined, 无 TDZ)。
	if !sym.IsVarLike {
		c.emitter.Emit(bytecode.OP_STORE_LEXICAL, uint16(sym.Slot))
		return
	}
	c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
}

// emitIdentifierAssign 发射标识符赋值 (解构赋值的目标)。
// 已解析的局部符号写槽位，其余走全局存储 —— 与普通 x = v 一致。

func (c *Compiler) emitIdentifierAssign(name string) {
	sym := c.scope.Resolve(name)
	if c.withAffects(sym) {
		// with 体内: 先查 with 对象链, 未命中再回退 (值从栈顶弹出)。
		c.emitter.Emit(bytecode.OP_WITH_STORE, c.emitWithRef(c.buildWithRef(name, sym)))
		return
	}
	if sym == nil || (sym.Depth == 0 && !c.moduleMode) {
		c.emitAssignmentStore(name, sym)
	} else {
		c.emitLocalStore(sym)
	}
}

func (c *Compiler) compileIdentifier(node *ast.Identifier) error {
	// arguments: 解析到当前函数的 arguments 槽位
	if node.Value == "arguments" && c.currentArgumentsSlot >= 0 {
		c.emitter.Emit(bytecode.OP_LOAD, uint16(c.currentArgumentsSlot))
		return nil
	}

	// 形参默认值求值中的 TDZ: 引用自身或后位形参必须抛 ReferenceError,
	// 而不是读到尚未初始化的槽位/外层同名绑定 (规范 9.2.12)。
	if c.paramTDZNames != nil && c.paramTDZNames[node.Value] &&
		c.scope.FuncLayer() == c.paramTDZScope {
		c.emitThrowReferenceError(node.Value)
		return nil
	}

	sym := c.scope.Resolve(node.Value)
	if c.withAffects(sym) {
		// with 体内的自由标识符: 运行时先查 with 对象链 (规范 13.11.7)。
		c.emitter.Emit(bytecode.OP_WITH_LOAD, c.emitWithRef(c.buildWithRef(node.Value, sym)))
		return nil
	}
	if sym != nil {
		c.emitLoad(sym)
	} else {
		// 可能是全局变量
		c.emitGlobalLoad(node.Value)
	}
	return nil
}

func (c *Compiler) compileBinaryExpression(node *ast.BinaryExpression) error {
	// 编译左操作数
	if err := c.compileExpression(node.Left); err != nil {
		return err
	}
	// 编译右操作数
	if err := c.compileExpression(node.Right); err != nil {
		return err
	}
	// 发射运算指令
	switch node.Operator {
	case "+":
		c.emitter.EmitNoOperand(bytecode.OP_ADD)
	case "-":
		c.emitter.EmitNoOperand(bytecode.OP_SUB)
	case "*":
		c.emitter.EmitNoOperand(bytecode.OP_MUL)
	case "/":
		c.emitter.EmitNoOperand(bytecode.OP_DIV)
	case "%":
		c.emitter.EmitNoOperand(bytecode.OP_MOD)
	case "**":
		c.emitter.EmitNoOperand(bytecode.OP_POW)
	case "==":
		c.emitter.EmitNoOperand(bytecode.OP_EQ)
	case "!=":
		c.emitter.EmitNoOperand(bytecode.OP_NOT_EQ)
	case "===":
		c.emitter.EmitNoOperand(bytecode.OP_STRICT_EQ)
	case "!==":
		c.emitter.EmitNoOperand(bytecode.OP_STRICT_NE)
	case "<":
		c.emitter.EmitNoOperand(bytecode.OP_LT)
	case ">":
		c.emitter.EmitNoOperand(bytecode.OP_GT)
	case "<=":
		c.emitter.EmitNoOperand(bytecode.OP_LTE)
	case ">=":
		c.emitter.EmitNoOperand(bytecode.OP_GTE)
	case "&":
		c.emitter.EmitNoOperand(bytecode.OP_BIT_AND)
	case "|":
		c.emitter.EmitNoOperand(bytecode.OP_BIT_OR)
	case "^":
		c.emitter.EmitNoOperand(bytecode.OP_BIT_XOR)
	case "<<":
		c.emitter.EmitNoOperand(bytecode.OP_SHL)
	case ">>":
		c.emitter.EmitNoOperand(bytecode.OP_SHR)
	case ">>>":
		c.emitter.EmitNoOperand(bytecode.OP_USHR)
	case "instanceof":
		c.emitter.EmitNoOperand(bytecode.OP_INSTANCEOF)
	case "in":
		c.emitter.EmitNoOperand(bytecode.OP_IN)
	default:
		return fmt.Errorf("unknown binary operator: %s", node.Operator)
	}
	return nil
}

func (c *Compiler) compileUnaryExpression(node *ast.UnaryExpression) error {
	if node.Prefix {
		switch node.Operator {
		case "-":
			if err := c.compileExpression(node.Right); err != nil {
				return err
			}
			c.emitter.EmitNoOperand(bytecode.OP_NEG)
		case "!":
			if err := c.compileExpression(node.Right); err != nil {
				return err
			}
			c.emitter.EmitNoOperand(bytecode.OP_NOT)
		case "~":
			if err := c.compileExpression(node.Right); err != nil {
				return err
			}
			c.emitter.EmitNoOperand(bytecode.OP_BIT_NOT)
		case "+":
			if err := c.compileExpression(node.Right); err != nil {
				return err
			}
			// 一元正号: ToNumber。此前这里是编译期 NOP，导致 +"5" 仍是字符串，
			// 也无法拒绝 BigInt (规范要求 +1n 抛 TypeError)，故改为运行时指令。
			c.emitter.EmitNoOperand(bytecode.OP_TO_NUMBER)
		case "typeof":
			// typeof 未声明变量应返回 "undefined" 而不抛 ReferenceError。
			// 注意: 不能因为编译期符号表里查不到就断定"未声明" —— 标准库的
			// 全局对象 (Math/Number/Array/JSON/...) 是运行时注入到全局环境的，
			// 编译期符号表一无所知。因此这里发射 OP_TYPEOF_GLOBAL，
			// 由 VM 在运行时做非抛出的全局查找: 找到就返回真实类型的名字，
			// 找不到才返回 "undefined"。
			if ident, ok := node.Right.(*ast.Identifier); ok {
				isArguments := ident.Value == "arguments" && c.currentArgumentsSlot >= 0
				if !isArguments && c.scope.Resolve(ident.Value) == nil {
					c.emitTypeOfGlobal(ident.Value)
					break
				}
			}
			if err := c.compileExpression(node.Right); err != nil {
				return err
			}
			c.emitter.EmitNoOperand(bytecode.OP_TYPEOF)
		case "++":
			return c.compileIncDec(node.Right, true, true) // prefix, increment
		case "--":
			return c.compileIncDec(node.Right, false, true) // prefix, decrement
		case "delete":
			return c.compileDelete(node.Right)
		case "void":
			// void expr → 求值 expr, 丢弃, 返回 undefined
			if err := c.compileExpression(node.Right); err != nil {
				return err
			}
			c.emitter.EmitNoOperand(bytecode.OP_POP)
			c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
		}
	} else {
		// 后缀 ++/--
		switch node.Operator {
		case "++":
			return c.compileIncDec(node.Right, true, false) // postfix, increment
		case "--":
			return c.compileIncDec(node.Right, false, false) // postfix, decrement
		}
	}
	return nil
}

// compileDelete 编译 delete 运算符。

func (c *Compiler) compileDelete(target ast.Expression) error {
	// strict 下 `delete 标识符` 是 SyntaxError (规范 Early Error: 不能删除
	// 词法/变量绑定)。sloppy 下合法 (返回 true, 见下)。
	if _, ok := target.(*ast.Identifier); ok && c.strict {
		return fmt.Errorf("compiler: SyntaxError: delete of an unqualified identifier in strict mode")
	}
	// delete obj.prop 或 delete obj[idx]
	if member, ok := target.(*ast.MemberExpression); ok {
		// delete obj.#x: 规范早错, 解析器已拦; 这里兜底只为杜绝下面
		// Property.(*ast.Identifier) 断言在私有访问(Property=nil)上 panic。
		if member.Private != "" {
			return fmt.Errorf("compiler: SyntaxError: 'delete' of private member '%s' is not allowed", member.Private)
		}
		// 编译对象
		if err := c.compileExpression(member.Object); err != nil {
			return err
		} // [obj]
		if member.Computed {
			// obj[expr]: 编译键 → [obj, key]
			if err := c.compileExpression(member.Property); err != nil {
				return err
			}
		} else {
			// obj.prop: 压入属性名字符串 → [obj, "prop"]
			propName := member.Property.(*ast.Identifier).Value
			idx := c.constants.AddConstant(object.NewString(propName))
			c.emitter.Emit(bytecode.OP_CONST, idx)
		}
		// OP_DELETE 弹出 [obj, key], 推入 true/false
		c.emitter.EmitNoOperand(bytecode.OP_DELETE)
		return nil
	}
	// delete 标识符: with 体内先查 with 对象链 —— 命中则删除该属性并返回 true。
	// (普通作用域里的 delete 标识符仍是合法但无效的真值, 见下方兜底。)
	if ident, ok := target.(*ast.Identifier); ok {
		sym := c.scope.Resolve(ident.Value)
		if c.withAffects(sym) {
			c.emitter.Emit(bytecode.OP_WITH_DELETE, c.emitWithRef(c.buildWithRef(ident.Value, sym)))
			return nil
		}
		// sloppy 下 delete 标识符按引用基分三类 (规范 UnaryExpression:
		// delete + 环境记录 DeleteBinding):
		//   ① 顶层 var / 函数声明 / 隐式赋值全局 / 未绑定名 —— 基是全局
		//      对象, 走自有属性删除 (var/函数声明不可配置 → false;
		//      隐式/未绑定 → true);
		//   ② 顶层 let/const/class 与模块顶层绑定 —— 基是全局词法(声明式)
		//      环境, 不可删 → false;
		//   ③ 内层局部绑定 (局部变量/形参) —— 同为声明式环境 → false。
		// 旧实现一律编译成「求值标识符 + POP + TRUE」: 既不删属性, 又给
		// 未定义名徒增一次 ReferenceError。
		isGlobalVarLike := sym == nil ||
			(sym.Depth == 0 && !c.moduleMode && sym.IsVarLike)
		if !isGlobalVarLike {
			// ② ③: 词法绑定不可删 (引用基不是全局对象)。
			c.emitter.EmitNoOperand(bytecode.OP_FALSE)
			return nil
		}
		// ①: 按名删全局对象的自有属性。不写 OP_THIS + OP_DELETE: 函数体内
		// this 是调用方接收者, module 顶层 this 是 undefined, 都不该影响
		// 全局对象的属性删除; 也不走 LOAD_GLOBAL "globalThis" (裸 VM 未装配
		// 该绑定)。用专用指令直接查全局环境。
		nameIdx := c.constants.AddConstant(object.NewString(ident.Value))
		c.emitter.Emit(bytecode.OP_DELETE_GLOBAL, nameIdx)
		return nil
	}
	// delete 普通表达式: 求值后丢弃, 返回 true (简化)
	if err := c.compileExpression(target); err != nil {
		return err
	}
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.EmitNoOperand(bytecode.OP_TRUE)
	return nil
}

func (c *Compiler) compileAssignmentExpression(node *ast.AssignmentExpression) error {
	// 逻辑赋值 &&= ||= ??= : 短路语义
	if node.Operator == "&&=" || node.Operator == "||=" || node.Operator == "??=" {
		return c.compileLogicalAssignment(node)
	}

	// 处理解构赋值: [a, b] = arr  或  { a, b } = obj
	if _, ok := node.Left.(*ast.ArrayPattern); ok {
		return c.compileDestructureAssignment(node, bindAssign)
	}
	if _, ok := node.Left.(*ast.ObjectPattern); ok {
		return c.compileDestructureAssignment(node, bindAssign)
	}

	switch left := node.Left.(type) {
	case *ast.Identifier:
		// strict 下 eval / arguments 不能作赋值目标 (规范 Early Error)。
		if c.strict && (left.Value == "eval" || left.Value == "arguments") {
			return fmt.Errorf("compiler: SyntaxError: assignment to '%s' is not allowed in strict mode", left.Value)
		}
		sym := c.scope.Resolve(left.Value)
		if c.withAffects(sym) {
			// with 体内自由标识符的赋值: 先查对象链, 未命中再回退。
			ref := c.emitWithRef(c.buildWithRef(left.Value, sym))
			if node.Operator == "=" {
				if err := c.compileNamedExpression(node.Right, left.Value); err != nil {
					return err
				}
				c.emitter.EmitNoOperand(bytecode.OP_DUP)
				c.emitter.Emit(bytecode.OP_WITH_STORE, ref)
			} else {
				c.emitter.Emit(bytecode.OP_WITH_LOAD, ref)
				if err := c.compileExpression(node.Right); err != nil {
					return err
				}
				c.emitCompoundOp(node.Operator)
				c.emitter.EmitNoOperand(bytecode.OP_DUP)
				c.emitter.Emit(bytecode.OP_WITH_STORE, ref)
			}
			return nil
		}
		if sym == nil || (sym.Depth == 0 && !c.moduleMode) {
			// 全局变量: 读写共享全局环境
			if node.Operator == "=" {
				if err := c.compileNamedExpression(node.Right, left.Value); err != nil {
					return err
				}
				c.emitter.EmitNoOperand(bytecode.OP_DUP)
				c.emitAssignmentStore(left.Value, sym)
			} else {
				// 复合赋值: x += val → LOAD old, 编译右值, OP, DUP, STORE_GLOBAL
				c.emitGlobalLoad(left.Value)
				if err := c.compileExpression(node.Right); err != nil {
					return err
				}
				c.emitCompoundOp(node.Operator)
				c.emitter.EmitNoOperand(bytecode.OP_DUP)
				c.emitAssignmentStore(left.Value, sym)
			}
			return nil
		}

		if node.Operator == "=" {
			// x = val: 编译右值, DUP, STORE
			if err := c.compileNamedExpression(node.Right, left.Value); err != nil {
				return err
			}
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
			c.emitLocalStore(sym)
		} else {
			// 复合赋值: x += val → LOAD old, 编译右值, OP, DUP, STORE
			c.emitter.Emit(bytecode.OP_LOAD, uint16(sym.Slot))
			if err := c.compileExpression(node.Right); err != nil {
				return err
			}
			c.emitCompoundOp(node.Operator)
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
			c.emitLocalStore(sym)
		}

	case *ast.MemberExpression:
		// obj.prop = val / obj[key] OP= val 共用同一条路径:
		// 先压 [obj, key]，简单赋值直接 SET_INDEX，复合赋值走 DUP2 取旧值写回。
		if err := c.compileMemberRef(left); err != nil {
			return err
		}
		if node.Operator == "=" {
			if err := c.compileExpression(node.Right); err != nil {
				return err
			}
			// SET_INDEX: pops val, key, obj; pushes val → [val]
			c.emitter.EmitNoOperand(bytecode.OP_SET_INDEX)
			return nil
		}
		return c.emitCompoundMemberAssign(node)
	}
	return nil
}

// prescanScope 预登记语句列表中的绑定名 (let/const/function)。
// 只建立符号，不发射任何指令；执行时绑定仍按源码顺序被初始化。
// 同一作用域内的重复声明 (let/let、let/const、let/function 等) 在此
// 即为编译期 SyntaxError；函数声明与函数声明同名除外 (函数重定义语义)。
// destructureSyntheticName 是解构声明在 AST 中的合成名。
// 解构目标 (let [a, b] = x) 的合成名不登记，但其模式里的**真实绑定名**
// 必须在此登记 (见 prescanDeclarator) —— 否则列表顶部提升编译的函数体
// 解析不到列表后面解构声明的绑定，在非全局作用域退化成 ReferenceError。

func (c *Compiler) compileMemberRef(m *ast.MemberExpression) error {
	// obj.#x = val: 与公有 [obj, key] 约定一致, key 是混编码常量,
	// SET_INDEX / 复合赋值 (DUP2+GET_INDEX+SET_INDEX) 全部复用。
	if m.Private != "" {
		if err := c.compileExpression(m.Object); err != nil {
			return err
		}
		return c.emitPrivateKey(m.Private)
	}
	if err := c.compileExpression(m.Object); err != nil {
		return err
	}
	if m.Computed {
		// 注意: 这里**不能**发 OP_TO_PROPERTY_KEY。成员引用只产出未转换的
		// [obj, keyRaw] (规范 EvaluatePropertyAccessWithExpressionKey 只取
		// propertyNameValue; ToPropertyKey 属于 GetValue/PutValue 内部)。
		// 简单赋值 base[prop] = expr() 要求 ToPropertyKey 晚于右值求值
		// (assignment/target-member-computed-reference.js: prop.toString 抛
		// Test262Error, 而 expr() 抛 DummyError ⇒ 必须看到 DummyError)。
		// 键会被用两次的场合 (复合赋值 / 逻辑赋值 / ++/--) 由各自发射点
		// 在 DUP2 前自行调用 emitToPropertyKeyOnce()。
		return c.compileExpression(m.Property)
	}
	ident, ok := m.Property.(*ast.Identifier)
	if !ok {
		return fmt.Errorf("compiler: unsupported member target")
	}
	idx := c.constants.AddConstant(object.NewString(ident.Value))
	c.emitter.Emit(bytecode.OP_CONST, idx) // [obj, "prop"]
	return nil
}

// emitToPropertyKeyOnce 在栈顶是计算成员键的**原始值**时, 就地把键按
// ToPropertyKey 转成原语, 供后续同一引用上的**两次**索引操作共用。
//
// 只在「键会被用两次」的场合调用 (复合赋值 / 逻辑赋值 / ++/--), 且必须在
// 复制引用的 DUP2 **之前**调用 —— 转换结果留在栈上, DUP2 复制的是已转换
// 的键, GET_INDEX 与 SET_INDEX 因此只触发一次 ToPropertyKey
// (compound-assignment/S11.13.2_A7.*_T4: ToPropertyKey(prop) is only
// called once)。
//
// 简单赋值 / for-of 目标 / 解构目标每处键只用一次, 转换交给 SET_INDEX
// 自身完成 —— 这样 ToPropertyKey 落在右值求值之后, 求值顺序才合规。

func (c *Compiler) emitToPropertyKeyOnce() {
	c.emitter.EmitNoOperand(bytecode.OP_TO_PROPERTY_KEY)
}

// emitCompoundMemberAssign 发射成员复合赋值 (obj.k += v / obj[k] *= v)。
//
// 进入时栈上已有 [obj, key]。需要三样东西: 旧值、右值、以及写回用的 obj/key。
// 由于 obj/key 已被压入，先用 OP_DUP2 各复制一份:
//
//	[obj, key] → DUP2 → [obj, key, obj, key] → GET_INDEX → [obj, key, old]
//	→ 编译右值 → [obj, key, old, val] → OP → [obj, key, new] → SET_INDEX → [new]
//
// 这样 obj 与 key 各只求值一次 (obj[f()] += v 中 f 只调用一次)，
// 且 SET_INDEX 之后栈顶就是赋值表达式的值，与简单赋值一致。

func (c *Compiler) emitCompoundMemberAssign(node *ast.AssignmentExpression) error {
	// 键只转一次, 供下面 GET_INDEX 与 SET_INDEX 共用 (S11.13.2_A7.*_T4)。
	c.emitToPropertyKeyOnce()
	c.emitter.EmitNoOperand(bytecode.OP_DUP2)
	c.emitter.EmitNoOperand(bytecode.OP_GET_INDEX)
	if err := c.compileExpression(node.Right); err != nil {
		return err
	}
	c.emitCompoundOp(node.Operator)
	c.emitter.EmitNoOperand(bytecode.OP_SET_INDEX)
	return nil
}

// compileLogicalAssignment 处理逻辑赋值 &&= ||= ??= (短路语义)。
// x ||= y 等价于 x = x || y, 但只在需要时计算右侧, 且 x 被求值一次。

func (c *Compiler) compileLogicalAssignment(node *ast.AssignmentExpression) error {
	op := node.Operator

	switch left := node.Left.(type) {
	case *ast.Identifier:
		sym := c.scope.Resolve(left.Value)
		if c.withAffects(sym) {
			// with 体内逻辑赋值: 读当前值 (对象链) → 短路判断 → 写回 (对象链)。
			ref := c.emitWithRef(c.buildWithRef(left.Value, sym))
			c.emitter.Emit(bytecode.OP_WITH_LOAD, ref)
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
			skip := c.emitLogicalSkip(op)
			c.emitter.EmitNoOperand(bytecode.OP_POP)
			c.emitter.EmitNoOperand(bytecode.OP_POP)
			if err := c.compileExpression(node.Right); err != nil {
				return err
			}
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
			c.emitter.Emit(bytecode.OP_WITH_STORE, ref)
			done := c.emitter.EmitJump(bytecode.OP_JUMP)
			c.emitter.PatchJump(skip)
			c.emitter.EmitNoOperand(bytecode.OP_POP)
			c.emitter.PatchJump(done)
			return nil
		}
		isGlobal := sym == nil || (sym.Depth == 0 && !c.moduleMode)

		// 加载当前值
		if isGlobal {
			c.emitGlobalLoad(left.Value)
		} else {
			c.emitter.Emit(bytecode.OP_LOAD, uint16(sym.Slot))
		}
		c.emitter.EmitNoOperand(bytecode.OP_DUP)

		// 根据运算符选择跳过条件: 短路时跳过赋值
		skip := c.emitLogicalSkip(op)

		// 非短路路径: 弹出旧值, 计算右侧, 存储
		c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出 DUP 副本
		c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出原始值
		if err := c.compileExpression(node.Right); err != nil {
			return err
		}
		c.emitter.EmitNoOperand(bytecode.OP_DUP)
		if isGlobal {
			c.emitGlobalStore(left.Value)
		} else {
			c.emitLocalStore(sym)
		}
		done := c.emitter.EmitJump(bytecode.OP_JUMP)

		// 短路路径: 弹出 DUP 副本, 保留原始值
		c.emitter.PatchJump(skip)
		c.emitter.EmitNoOperand(bytecode.OP_POP)
		c.emitter.PatchJump(done)
		return nil

	case *ast.MemberExpression:
		// obj[key] ||= y (短路语义, 结果是当前值或赋值后的值)
		//
		// 与复合赋值同样用 DUP2 保留引用，避免二次求值 obj/key:
		//
		//	[obj,key] → DUP2 → [obj,key,obj,key] → GET_INDEX → [obj,key,val]
		//	→ DUP → [obj,key,val,val] → 短路则跳到 cleanup
		//	→ POP,POP → [obj,key] → 编译右值 → SET_INDEX → [y] → JUMP end
		//	cleanup: [obj,key,val,val] → POP → SWAP → POP → SWAP → POP → [val]
		if err := c.compileMemberRef(left); err != nil {
			return err
		}
		// 键只转一次, 供下面 GET_INDEX 与 SET_INDEX 共用。
		c.emitToPropertyKeyOnce()
		c.emitter.EmitNoOperand(bytecode.OP_DUP2)
		c.emitter.EmitNoOperand(bytecode.OP_GET_INDEX) // → [obj, key, val]
		c.emitter.EmitNoOperand(bytecode.OP_DUP)       // [obj, key, val, val]
		skip := c.emitLogicalSkip(op)                  // 短路则跳 (栈顶 val 不弹出)
		c.emitter.EmitNoOperand(bytecode.OP_POP)       // [obj, key, val]
		c.emitter.EmitNoOperand(bytecode.OP_POP)       // [obj, key]

		// 非短路路径: 计算右侧并写回
		if err := c.compileExpression(node.Right); err != nil {
			return err
		}
		// SET_INDEX 弹出 val,key,obj 并推回 val (保留结果)
		c.emitter.EmitNoOperand(bytecode.OP_SET_INDEX) // [obj,key,y] → [y]
		done2 := c.emitter.EmitJump(bytecode.OP_JUMP)

		// 短路路径清理: [obj, key, val, val] 丢弃 obj/key，只留 val
		c.emitter.PatchJump(skip)
		c.emitter.EmitNoOperand(bytecode.OP_POP)  // [obj, key, val]
		c.emitter.EmitNoOperand(bytecode.OP_SWAP) // [obj, val, key]
		c.emitter.EmitNoOperand(bytecode.OP_POP)  // [obj, val]
		c.emitter.EmitNoOperand(bytecode.OP_SWAP) // [val, obj]
		c.emitter.EmitNoOperand(bytecode.OP_POP)  // [val]

		// 两条路径汇聚于此
		c.emitter.PatchJump(done2)
		return nil
	}
	return fmt.Errorf("unsupported logical assignment target: %T", node.Left)
}

// emitLogicalSkip 根据逻辑赋值运算符发射跳过赋值的跳转。
// 短路条件:
//
//	||= : 当前值为真值 → 跳过
//	&&= : 当前值为假值 → 跳过
//	??= : 当前值为非 nullish → 跳过
//
// 返回跳转指令位置 (调用方需 PatchJump)。

func (c *Compiler) emitLogicalSkip(op string) int {
	switch op {
	case "||=":
		return c.emitter.EmitJump(bytecode.OP_JUMP_IF_TRUE)
	case "&&=":
		return c.emitter.EmitJump(bytecode.OP_JUMP_IF_FALSE)
	case "??=":
		return c.emitter.EmitJump(bytecode.OP_JUMP_IF_NOT_NULL)
	}
	return c.emitter.EmitJump(bytecode.OP_JUMP)
}

// bindKind 区分解构模式的三种绑定登记口径 —— 解构编译里唯一的语义分叉点。
//
//	- bindAssign:  赋值解构 `[a] = x` —— 不声明, 写已有绑定;
//	- bindLexical: let/const 声明解构 —— 绑定登记在**当前 (块) 作用域**;
//	- bindVar:     var 声明解构 —— 绑定登记在**函数作用域层** (FuncLayer),
//	  并参与 var 提升 (emitVarHoistInits)。
//
// bindVar 的必要性 (r4McL4): `for (var [p] of …) {}` 之后循环外读 p 必须拿到
// 值; 若按 bindLexical 走, p 落在循环体的块作用域里, 块退出即消失 → 读 p 退化
// 成 OP_LOAD_GLOBAL → 运行期 ReferenceError (Node 全程有值, 是 Gox 的缺陷)。

func (c *Compiler) compileIncDec(target ast.Expression, isInc, isPrefix bool) error {
	if ident, ok := target.(*ast.Identifier); ok {
		sym := c.scope.Resolve(ident.Value)
		if c.withAffects(sym) {
			// with 体内: 读改写都走对象链 (未命中回退外层)。
			ref := c.emitWithRef(c.buildWithRef(ident.Value, sym))
			c.emitter.Emit(bytecode.OP_WITH_LOAD, ref) // [old]
			if isPrefix {
				c.emitIncDecOp(isInc)
				c.emitter.EmitNoOperand(bytecode.OP_DUP)
				c.emitter.Emit(bytecode.OP_WITH_STORE, ref)
			} else {
				c.emitter.EmitNoOperand(bytecode.OP_DUP)
				c.emitIncDecOp(isInc)
				c.emitter.Emit(bytecode.OP_WITH_STORE, ref)
			}
			return nil
		}
		if sym == nil {
			// 未声明名: 走**运行期**解析路径, 而不是编译期直接返回错。
			// OP_LOAD_GLOBAL 在找不到绑定时抛 ReferenceError —— 这一步是
			// 可被 try/catch 捕获的 (规范: GetValue 未解析引用 ⇒ 运行期
			// ReferenceError); 反之若名字确实存在 (如宿主注入的全局),
			// 读-改-写照常工作。sloppy 与 strict 都适用 (未声明读属于运行期错)。
			c.emitGlobalLoad(ident.Value) // [old] (缺失时运行时抛错)
			if isPrefix {
				c.emitIncDecOp(isInc)
				c.emitter.EmitNoOperand(bytecode.OP_DUP)
				c.emitAssignmentStore(ident.Value, nil)
			} else {
				c.emitter.EmitNoOperand(bytecode.OP_DUP)
				c.emitIncDecOp(isInc)
				c.emitAssignmentStore(ident.Value, nil)
			}
			return nil
		}

		// 加载旧值
		c.emitLoad(sym) // [old]

		if isPrefix {
			// 前缀: ++i → 返回新值
			// [old] → TO_NUMBER → [num] → push 1 → [num, 1] → OP → [new]
			// → DUP → [new, new] → STORE → [new]
			c.emitIncDecOp(isInc)
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
			c.emitStore(sym)
		} else {
			// 后缀: i++ → 返回旧值
			// [old] → DUP → [old, old] → TO_NUMBER → [old, num] → push 1
			// → [old, num, 1] → OP → [old, new] → STORE → [old]
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
			c.emitIncDecOp(isInc)
			c.emitStore(sym)
		}
		return nil
	}
	// 成员: obj.k++ / obj[k]-- (含前缀与后缀)
	if member, ok := target.(*ast.MemberExpression); ok {
		if err := c.compileMemberRef(member); err != nil {
			return err
		}
		// 键只转一次, 供下面 GET_INDEX 与 SET_INDEX 共用。
		c.emitToPropertyKeyOnce()
		// [obj, key] → DUP2 → [obj, key, obj, key] → GET_INDEX → [obj, key, old]
		c.emitter.EmitNoOperand(bytecode.OP_DUP2)
		c.emitter.EmitNoOperand(bytecode.OP_GET_INDEX)

		if isPrefix {
			// 前缀: [obj, key, old] → TO_NUMBER → [obj, key, num] → push 1 → OP
			// → [obj, key, new] → SET_INDEX (推回写入值) → [new]
			c.emitIncDecOp(isInc)
			c.emitter.EmitNoOperand(bytecode.OP_SET_INDEX)
			return nil
		}
		// 后缀: [obj, key, old] → DUP_BELOW2 → [old, obj, key, old]
		// → TO_NUMBER → [old, obj, key, num] → push 1 → OP → [old, obj, key, new]
		// → SET_INDEX → [old, new] → POP → [old]
		c.emitter.EmitNoOperand(bytecode.OP_DUP_BELOW2)
		c.emitIncDecOp(isInc)
		c.emitter.EmitNoOperand(bytecode.OP_SET_INDEX)
		c.emitter.EmitNoOperand(bytecode.OP_POP)
		return nil
	}
	// 更新表达式的操作数必须是简单赋值目标 (标识符 / 成员); 对字面量等 `1++`
	// 是语法错误 (InvalidAssignmentTargetType), 按 SyntaxError 报, 以便 test262
	// 的 negative:parse 用例按正确类型判定 (parse-err-syntax-2.js 等)。
	return fmt.Errorf("SyntaxError: ++/-- only supports identifiers")
}

// emitIncDecOp 发射 ++/-- 的数值步骤。
//
// 用单条 OP_INC/OP_DEC 而非「ToNumber + 1 + ADD」: 规范 13.4.4.1 要求
// ToNumeric (BigInt 保持 BigInt) 且加数与被加数同类型 (BigInt 加 1n)。
// 拆成指令流必然把 1 编码成 Number 常量, 对 BigInt 触发混合运算 TypeError。
func (c *Compiler) emitIncDecOp(isInc bool) {
	if isInc {
		c.emitter.EmitNoOperand(bytecode.OP_INC)
	} else {
		c.emitter.EmitNoOperand(bytecode.OP_DEC)
	}
}

// emitCompoundOp 发射复合赋值的运算指令

func (c *Compiler) emitCompoundOp(op string) {
	switch op {
	case "+=":
		c.emitter.EmitNoOperand(bytecode.OP_ADD)
	case "-=":
		c.emitter.EmitNoOperand(bytecode.OP_SUB)
	case "*=":
		c.emitter.EmitNoOperand(bytecode.OP_MUL)
	case "/=":
		c.emitter.EmitNoOperand(bytecode.OP_DIV)
	case "%=":
		c.emitter.EmitNoOperand(bytecode.OP_MOD)
	case "**=":
		c.emitter.EmitNoOperand(bytecode.OP_POW)
	case "&=":
		c.emitter.EmitNoOperand(bytecode.OP_BIT_AND)
	case "|=":
		c.emitter.EmitNoOperand(bytecode.OP_BIT_OR)
	case "^=":
		c.emitter.EmitNoOperand(bytecode.OP_BIT_XOR)
	case "<<=":
		c.emitter.EmitNoOperand(bytecode.OP_SHL)
	case ">>=":
		c.emitter.EmitNoOperand(bytecode.OP_SHR)
	case ">>>=":
		c.emitter.EmitNoOperand(bytecode.OP_USHR)
	}
}

func (c *Compiler) compileLogicalExpression(node *ast.LogicalExpression) error {
	// 编译左操作数
	if err := c.compileExpression(node.Left); err != nil {
		return err
	}

	if node.Operator == "??" {
		// ?? 空值合并: 左操作数为 nullish 时才计算右操作数
		// 非 nullish → 跳转到末尾 (左操作数作为结果)
		notNull := c.emitter.EmitJump(bytecode.OP_JUMP_IF_NOT_NULL)
		c.emitter.EmitNoOperand(bytecode.OP_POP)
		if err := c.compileExpression(node.Right); err != nil {
			return err
		}
		c.emitter.PatchJump(notNull)
		return nil
	}

	var jumpOp bytecode.Opcode
	if node.Operator == "&&" {
		jumpOp = bytecode.OP_JUMP_IF_FALSE
	} else {
		jumpOp = bytecode.OP_JUMP_IF_TRUE
	}
	// 短路: 如果左操作数满足短路条件，跳过右操作数 (左操作数留在栈上作为结果)
	shortCircuit := c.emitter.EmitJump(jumpOp)

	// 不短路: 弹出左操作数，计算右操作数
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	if err := c.compileExpression(node.Right); err != nil {
		return err
	}
	c.emitter.PatchJump(shortCircuit)
	return nil
}

// compileSequenceExpression 编译逗号运算符 (a, b, c)。
// 依次求值每个表达式, 弹出前 n-1 个结果, 栈顶保留最后一个值。

func (c *Compiler) compileSequenceExpression(node *ast.SequenceExpression) error {
	n := len(node.Expressions)
	for i, expr := range node.Expressions {
		if err := c.compileExpression(expr); err != nil {
			return err
		}
		if i < n-1 {
			c.emitter.EmitNoOperand(bytecode.OP_POP)
		}
	}
	return nil
}

func (c *Compiler) compileConditionalExpression(node *ast.ConditionalExpression) error {
	// 编译条件
	if err := c.compileExpression(node.Condition); err != nil {
		return err
	}
	jumpFalse := c.emitter.EmitJump(bytecode.OP_JUMP_IF_FALSE)
	c.emitter.EmitNoOperand(bytecode.OP_POP)

	// consequence
	if err := c.compileExpression(node.Consequence); err != nil {
		return err
	}
	jumpEnd := c.emitter.EmitJump(bytecode.OP_JUMP)

	// alternative
	c.emitter.PatchJump(jumpFalse)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	if err := c.compileExpression(node.Alternative); err != nil {
		return err
	}
	c.emitter.PatchJump(jumpEnd)
	return nil
}

func (c *Compiler) compileCallExpression(node *ast.CallExpression) error {
	// super(...): 调用父构造函数 (this = 当前 this, 返回值丢弃)
	if super, ok := node.Function.(*ast.SuperExpression); ok {
		if c.currentSuperClass == "" {
			return fmt.Errorf("compiler: super call outside class")
		}
		_ = super
		// super(...args): 实参个数运行期才知道, 走「实参在数组里」的方法调用。
		// 栈: [fn] → [fn, this] → [fn, this, argsArray] → CALL_METHOD_SPREAD。
		if hasSpreadArgs(node.Arguments) {
			c.emitSuperLoad(c.currentSuperClass)
			c.emitter.EmitNoOperand(bytecode.OP_THIS)
			if err := c.compileArgumentsArray(node.Arguments); err != nil {
				return err
			}
			c.emitter.EmitNoOperand(bytecode.OP_NEW_TARGET_MARK)
			c.emitter.EmitNoOperand(bytecode.OP_CALL_METHOD_SPREAD)
			return nil
		}
		// LOAD_GLOBAL SuperClass → OP_THIS → 参数 → OP_CALL_METHOD
		// 注意: 这里不 emit POP, 返回值 (父构造结果) 留在栈上由外层语句/表达式消费,
		// 否则表达式语句还会再补一个 POP, 造成双重弹出破坏栈。
		c.emitSuperLoad(c.currentSuperClass)      // [fn]
		c.emitter.EmitNoOperand(bytecode.OP_THIS) // [fn, this]
		for _, arg := range node.Arguments {
			if err := c.compileExpression(arg); err != nil {
				return err
			}
		}
		c.emitter.EmitNoOperand(bytecode.OP_NEW_TARGET_MARK)
		c.emitter.Emit(bytecode.OP_CALL_METHOD, uint16(len(node.Arguments)))
		return nil
	}

	// 计算成员调用: obj[k](args) (看板 rYFTlt)。
	//
	// 与 obj.m(args) 完全同形, **必须绑定接收者** —— 规范里 CallExpression 的
	// 被调是 Reference (有 base), `arr[Symbol.iterator]()` 的 this 就是 arr。
	// 此前只有 `!member.Computed` 走方法路径, 计算成员一律退化成裸调用
	// (先算实参、再算 obj[k]、最后 OP_CALL 不传 this), 于是
	// `arr[Symbol.iterator]()` 直接 TypeError。
	//
	// 取成员用 GET_INDEX 而非 GET_PROP, 其余与方法调用逐条对齐:
	//   [obj] → DUP → [obj, obj] → key → GET_INDEX → [obj, fn] → SWAP →
	//   [fn, obj] → 实参 → CALL_METHOD。
	// 求值序也顺带修正: 旧路径先算实参再算 obj[k], 规范是 obj → k → 实参。
	if member, ok := node.Function.(*ast.MemberExpression); ok && member.Computed &&
		member.Private == "" && !hasSpreadArgs(node.Arguments) {
		if _, isSuperObj := member.Object.(*ast.SuperExpression); !isSuperObj {
			if err := c.compileExpression(member.Object); err != nil {
				return err
			} // [obj]
			c.emitter.EmitNoOperand(bytecode.OP_DUP) // [obj, obj]
			if err := c.compileExpression(member.Property); err != nil {
				return err
			} // [obj, obj, key]
			c.emitter.EmitNoOperand(bytecode.OP_GET_INDEX) // [obj, fn]
			c.emitter.EmitNoOperand(bytecode.OP_SWAP)      // [fn, obj]
			for _, arg := range node.Arguments {
				if err := c.compileExpression(arg); err != nil {
					return err
				}
			} // [fn, obj, arg1, ..., argN]
			c.emitter.Emit(bytecode.OP_CALL_METHOD, uint16(len(node.Arguments)))
			return nil
		}
		// super[k]() 不在本单范围内, 落到下面的通用路径 (维持原行为)。
	}

	// 检查是否是方法调用: obj.method(args)
	if member, ok := node.Function.(*ast.MemberExpression); ok && !member.Computed && !hasSpreadArgs(node.Arguments) {
		// super.method(args): 从父 prototype 取方法, this 绑定当前 this
		if _, isSuperObj := member.Object.(*ast.SuperExpression); isSuperObj {
			if c.evalSuperHome != nil {
				// 直接 eval 单元 (roiE5Z): SuperProperty 语境下 super.method()
				// 合法 —— base 与 super.x 同一套 home 解析。
				if err := c.emitEvalSuperBase(); err != nil {
					return err
				}
			} else if c.currentSuperClass == "" {
				return fmt.Errorf("compiler: super method call outside class")
			} else {
				c.emitSuperLoad(c.currentSuperClass)
				pidx := c.constants.AddConstant(object.NewString("prototype"))
				c.emitter.Emit(bytecode.OP_GET_PROP, pidx)
			}
			// super.#m(): 规范早错, 解析器已拦; 兜底防 Property 断言 panic。
			if member.Private != "" {
				return fmt.Errorf("compiler: SyntaxError: private member access on 'super' is not allowed")
			}
			propName := member.Property.(*ast.Identifier).Value
			keyIdx := c.constants.AddConstant(object.NewString(propName))
			c.emitter.Emit(bytecode.OP_GET_PROP, keyIdx) // [fn]
			c.emitter.EmitNoOperand(bytecode.OP_THIS)    // [fn, this]
			for _, arg := range node.Arguments {
				if err := c.compileExpression(arg); err != nil {
					return err
				}
			}
			c.emitter.Emit(bytecode.OP_CALL_METHOD, uint16(len(node.Arguments)))
			return nil
		}
		// 方法调用: 编译 obj, DUP, GET_PROP, SWAP, 编译参数, CALL_METHOD
		if err := c.compileExpression(member.Object); err != nil {
			return err
		} // [obj]
		c.emitter.EmitNoOperand(bytecode.OP_DUP) // [obj, obj]
		if member.Private != "" {
			// 私有方法调用: fn 经混编码动态键取 (GET_INDEX 弹 [obj, key] 压 fn)。
			// [obj, obj] → key → GET_INDEX → [obj, fn]
			if err := c.emitPrivateKey(member.Private); err != nil {
				return err
			}
			c.emitter.EmitNoOperand(bytecode.OP_GET_INDEX) // [obj, fn]
		} else {
			propName := member.Property.(*ast.Identifier).Value
			idx := c.constants.AddConstant(object.NewString(propName))
			c.emitter.Emit(bytecode.OP_GET_PROP, idx) // [obj, fn]
		}
		c.emitter.EmitNoOperand(bytecode.OP_SWAP) // [fn, obj]
		// 编译参数
		for _, arg := range node.Arguments {
			if err := c.compileExpression(arg); err != nil {
				return err
			}
		} // [fn, obj, arg1, ..., argN]
		c.emitter.Emit(bytecode.OP_CALL_METHOD, uint16(len(node.Arguments)))
		return nil
	}

	// 检查是否有 spread 参数
	hasSpread := hasSpreadArgs(node.Arguments)

	// 直接 eval 调用: 发射 OP_EVAL_MARK。被调表达式必须是标识符 `eval`
	// (而非 (0, eval) / eval.call 这类间接形式)。标记在 OP_CALL/CALL_SPREAD
	// 之前发射, VM 消费时会再核对被调确为全局 %eval% (遮蔽场景不误判)。
	//
	// 两个用途, 用两条无操作数标记区分:
	//   - OP_EVAL_MARK: 所有直接 eval。让 VM 把**调用者帧的 this** 传给 eval,
	//     实现规范要求的 "direct eval 的 this 绑定与调用者一致"。
	//   - OP_EVAL_MARK_INIT: 仅类字段初始化器内的直接 eval。在上一标记基础上
	//     额外让 eval 内建进入受限模式 (PerformEval 补充早错: 源码含
	//     arguments/super 调用 → SyntaxError)。
	// 另有一族 home 标记 (roiE5Z): 外层函数有 [[HomeObject]] 时, 把 home
	// 上下文随标记带出, 让 eval 源码的 SuperProperty (super.x) 合法并按
	// home 解析 (PerformEval 18.2.1.1.1 的 inMethod 语境)。无 home 语境
	// (全局/普通函数/箭头内的直接 eval) 只发不带 home 的标记, eval 源码
	// 里的 super.x 维持 SyntaxError。home 标记与 INIT 标记前后组合:
	// [HOME(..)] [HOME_STATIC(..)] [SUPER(..)] [INIT?] <call>。
	directEval := isDirectEvalCallee(node.Function)
	restrictedEval := c.inClassFieldInit && directEval

	emitEvalMarks := func() {
		if !directEval {
			return
		}
		// home 上下文先行 (操作数 = 常量池里的名字), 供 VM 经桥传给 eval 内建。
		// 注意: 发了 home 标记就**不再**发普通 OP_EVAL_MARK / _INIT —— home
		// 标记本身已置 pendingDirectEval, 而普通标记会清空 home (其语义就是
		// 「本语境无 home」)。受限 (字段初始化器) 语境由 _INIT 叠加在 home
		// 标记之后 (VM 侧 _INIT 不清 home)。
		homeMarked := false
		if c.evalHome.ThisHome {
			// 对象字面量方法: home 无名字, 运行期取调用者帧的 this。
			c.emitter.EmitNoOperand(bytecode.OP_EVAL_MARK_HOME_THIS)
			homeMarked = true
		} else if c.evalHome.Name != "" {
			nameIdx := c.constants.AddConstant(object.NewString(c.evalHome.Name))
			if c.evalHome.Static {
				c.emitter.Emit(bytecode.OP_EVAL_MARK_HOME_STATIC, nameIdx)
				if c.evalHome.SuperName != "" {
					superIdx := c.constants.AddConstant(object.NewString(c.evalHome.SuperName))
					c.emitter.Emit(bytecode.OP_EVAL_MARK_SUPER, superIdx)
				}
			} else {
				c.emitter.Emit(bytecode.OP_EVAL_MARK_HOME, nameIdx)
			}
			homeMarked = true
		}
		if restrictedEval {
			c.emitter.EmitNoOperand(bytecode.OP_EVAL_MARK_INIT)
		} else if !homeMarked {
			c.emitter.EmitNoOperand(bytecode.OP_EVAL_MARK)
		}
	}

	if hasSpread {
		// 有 spread: 收集参数到数组，再用 OP_CALL_SPREAD 调用
		if err := c.compileArgumentsArray(node.Arguments); err != nil {
			return err
		}
		emitEvalMarks()
		// 编译函数
		if err := c.compileExpression(node.Function); err != nil {
			return err
		}
		c.emitter.Emit(bytecode.OP_CALL_SPREAD, 0)
	} else {
		// 无 spread: 原有逻辑
		// 编译参数 (从左到右)
		for _, arg := range node.Arguments {
			if err := c.compileExpression(arg); err != nil {
				return err
			}
		}
		emitEvalMarks()
		// 编译函数
		if err := c.compileExpression(node.Function); err != nil {
			return err
		}
		// 调用
		c.emitter.Emit(bytecode.OP_CALL, uint16(len(node.Arguments)))
	}
	return nil
}

// isDirectEvalCallee 报告被调表达式是否形如直接 eval 调用 —— 即解析为
// 标识符 `eval` 的引用。规范上直接 eval 还要求该引用的值恰为 %eval%
// 内建 (运行期判定); 这里只做语法侧识别, 与主流引擎的静态识别一致。
// 间接形式 (成员访问 `eval.call`、序列 `(0, eval)` 等) 返回 false。

func isDirectEvalCallee(fn ast.Expression) bool {
	id, ok := fn.(*ast.Identifier)
	return ok && id.Value == "eval"
}

// hasSpreadArgs 检查参数列表中是否有 spread 元素

func hasSpreadArgs(args []ast.Expression) bool {
	for _, arg := range args {
		if _, ok := arg.(*ast.SpreadElement); ok {
			return true
		}
	}
	return false
}

// compileArgumentsArray 把实参列表 (含 spread 元素) 收集成一个数组压栈。
// 供 OP_CALL_SPREAD / OP_CALL_METHOD_SPREAD 这类「实参个数运行期才知道」的调用复用。

func (c *Compiler) compileArgumentsArray(args []ast.Expression) error {
	c.emitter.Emit(bytecode.OP_NEW_ARRAY, 0)
	for _, arg := range args {
		if spread, ok := arg.(*ast.SpreadElement); ok {
			if err := c.compileExpression(spread.Argument); err != nil {
				return err
			}
			c.emitter.EmitNoOperand(bytecode.OP_ARRAY_SPREAD)
		} else {
			if err := c.compileExpression(arg); err != nil {
				return err
			}
			c.emitter.EmitNoOperand(bytecode.OP_ARRAY_PUSH)
		}
	}
	return nil
}

func (c *Compiler) compileMemberExpression(node *ast.MemberExpression) error {
	// obj.#x: 私有访问编译为运行时动态键 (GET_INDEX)。
	// 键是 \x00<prefix>:<name> 混编码 —— 前缀在编译期由 currentPrivatePrefix
	// 决定, 类外访问在这里报编译错。
	if node.Private != "" {
		if err := c.compileExpression(node.Object); err != nil {
			return err
		}
		if err := c.emitPrivateKey(node.Private); err != nil {
			return err
		}
		c.emitter.EmitNoOperand(bytecode.OP_GET_INDEX)
		return nil
	}

	// super.prop: 访问父类 prototype 上的属性
	if super, ok := node.Object.(*ast.SuperExpression); ok {
		_ = super
		// 直接 eval 单元 (roiE5Z): home 语境由调用点随标记带出 (见
		// Compiler.SetEvalSuperHome)。按 home 解析 SuperProperty:
		//   - 实例语境: base = Object.getPrototypeOf(<Name>.prototype)
		//     —— 有 extends 时是父类 prototype, 无 extends 时是
		//     Object.prototype (基类方法的 super.x 同样合法);
		//   - 静态语境: base = <SuperName> (父类构造器本身; Gox 未链接
		//     ctor.__proto__, 不能走 __proto__), 无 extends 时退化为
		//     Object.getPrototypeOf(<Name>) = Function.prototype;
		//   - ThisHome (对象字面量方法): base = Object.getPrototypeOf(this)
		//     —— 近似: 接收者即方法宿主时成立 (o.m() 直接调用)。
		if c.evalSuperHome != nil {
			return c.emitEvalSuperProperty(node)
		}
		if c.currentSuperClass == "" {
			return fmt.Errorf("compiler: super property access outside class")
		}
		c.emitSuperLoad(c.currentSuperClass)
		pidx := c.constants.AddConstant(object.NewString("prototype"))
		c.emitter.Emit(bytecode.OP_GET_PROP, pidx)
		if node.Computed {
			if err := c.compileExpression(node.Property); err != nil {
				return err
			}
			c.emitter.EmitNoOperand(bytecode.OP_GET_INDEX)
		} else {
			propName := node.Property.(*ast.Identifier).Value
			idx := c.constants.AddConstant(object.NewString(propName))
			c.emitter.Emit(bytecode.OP_GET_PROP, idx)
		}
		return nil
	}

	// 编译对象
	if err := c.compileExpression(node.Object); err != nil {
		return err
	}
	if node.Computed {
		// obj[expr]
		if err := c.compileExpression(node.Property); err != nil {
			return err
		}
		c.emitter.EmitNoOperand(bytecode.OP_GET_INDEX)
	} else {
		// obj.prop
		propName := node.Property.(*ast.Identifier).Value
		idx := c.constants.AddConstant(object.NewString(propName))
		c.emitter.Emit(bytecode.OP_GET_PROP, idx)
	}
	return nil
}

// emitEvalSuperBase 生成「直接 eval 单元里 super 的 base 对象」入栈代码
// (roiE5Z)。栈: [] → [base]。home 上下文的三种解析路径见
// compileMemberExpression 的 super 分支注释。
//
// 名字解析一律走 emitSuperLoad (先作用域后全局): eval 单元是全局脚本,
// 类名落在全局环境 (顶层 class 声明 / var 赋值的类表达式); 类名是外层
// 局部时运行期抛 ReferenceError —— 与 Gox 既有 super 静态名模型同边界。

func (c *Compiler) emitEvalSuperBase() error {
	h := c.evalSuperHome
	switch {
	case h.ThisHome:
		// 对象字面量方法: home = this (调用者帧的 this 经 eval 包装函数代入)。
		c.emitter.EmitNoOperand(bytecode.OP_THIS)
		c.emitter.EmitNoOperand(bytecode.OP_GET_PROTO)
	case h.Static && h.SuperName != "":
		// 静态语境 + extends: base = 父类构造器本身 (无 .prototype 一层)。
		c.emitSuperLoad(h.SuperName)
	default:
		// 实例语境 (含静态基类): base = getProto(home[.prototype])。
		c.emitSuperLoad(h.Name)
		if !h.Static {
			pidx := c.constants.AddConstant(object.NewString("prototype"))
			c.emitter.Emit(bytecode.OP_GET_PROP, pidx)
		}
		c.emitter.EmitNoOperand(bytecode.OP_GET_PROTO)
	}
	return nil
}

// emitEvalSuperProperty 生成「直接 eval 单元里 super.prop / super[expr]」
// 的代码 (roiE5Z)。栈序: [base] → [val] (与普通成员访问尾段一致)。

func (c *Compiler) emitEvalSuperProperty(node *ast.MemberExpression) error {
	if err := c.emitEvalSuperBase(); err != nil {
		return err
	}
	if node.Computed {
		if err := c.compileExpression(node.Property); err != nil {
			return err
		}
		c.emitter.EmitNoOperand(bytecode.OP_GET_INDEX)
		return nil
	}
	propName := node.Property.(*ast.Identifier).Value
	idx := c.constants.AddConstant(object.NewString(propName))
	c.emitter.Emit(bytecode.OP_GET_PROP, idx)
	return nil
}

// compileOptionalMember 编译可选链属性访问 a?.b 或 a?.[b]。
// 若 a 为 null/undefined, 整体短路为 undefined。

func (c *Compiler) compileOptionalMember(node *ast.OptionalMemberExpression) error {
	// 编译对象
	if err := c.compileExpression(node.Object); err != nil {
		return err
	} // [a]
	// 非 nullish 时跳转到属性访问 (栈上 a 保留)
	notNull := c.emitter.EmitJump(bytecode.OP_JUMP_IF_NOT_NULL)
	// a 为 nullish: 产生 undefined
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
	jumpEnd := c.emitter.EmitJump(bytecode.OP_JUMP)
	// 非 nullish 路径
	c.emitter.PatchJump(notNull) // 栈: [a]
	if node.Computed {
		if err := c.compileExpression(node.Property); err != nil {
			return err
		}
		c.emitter.EmitNoOperand(bytecode.OP_GET_INDEX)
	} else {
		propName := node.Property.(*ast.Identifier).Value
		idx := c.constants.AddConstant(object.NewString(propName))
		c.emitter.Emit(bytecode.OP_GET_PROP, idx)
	}
	c.emitter.PatchJump(jumpEnd)
	return nil
}

// compileOptionalCall 编译可选链调用 a?.() 或 a?.b(args)。

func (c *Compiler) compileOptionalCall(node *ast.OptionalCallExpression) error {
	// 编译函数表达式
	if err := c.compileExpression(node.Function); err != nil {
		return err
	} // [fn]
	notNull := c.emitter.EmitJump(bytecode.OP_JUMP_IF_NOT_NULL)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
	jumpEnd := c.emitter.EmitJump(bytecode.OP_JUMP)
	c.emitter.PatchJump(notNull) // 栈: [fn]
	// 编译参数并调用
	for _, arg := range node.Arguments {
		if err := c.compileExpression(arg); err != nil {
			return err
		}
	}
	c.emitter.Emit(bytecode.OP_CALL, uint16(len(node.Arguments)))
	c.emitter.PatchJump(jumpEnd)
	return nil
}

func (c *Compiler) compileArrayLiteral(node *ast.ArrayLiteral) error {
	// 检查是否有 spread 元素
	hasSpread := false
	for _, elem := range node.Elements {
		if _, ok := elem.(*ast.SpreadElement); ok {
			hasSpread = true
			break
		}
	}

	if !hasSpread {
		// 无 spread: 使用 OP_NEW_ARRAY
		for _, elem := range node.Elements {
			// 空洞 ([1, , 2]): 元素为 nil, 推洞标记 (写入数组时转为真洞,
			// 与显式 undefined 元素区分: forEach/hasOwnProperty 不可见)。
			if elem == nil {
				c.emitter.EmitNoOperand(bytecode.OP_HOLE)
				continue
			}
			if err := c.compileExpression(elem); err != nil {
				return err
			}
		}
		c.emitter.Emit(bytecode.OP_NEW_ARRAY, uint16(len(node.Elements)))
	} else {
		// 有 spread: 逐步构建数组
		c.emitter.Emit(bytecode.OP_NEW_ARRAY, 0) // 空数组
		for _, elem := range node.Elements {
			if elem == nil {
				// 空洞 ([...a, , b]): 推洞标记。
				c.emitter.EmitNoOperand(bytecode.OP_HOLE)
				c.emitter.EmitNoOperand(bytecode.OP_ARRAY_PUSH)
				continue
			}
			if spread, ok := elem.(*ast.SpreadElement); ok {
				// spread: [...arr] → 展开所有元素
				if err := c.compileExpression(spread.Argument); err != nil {
					return err
				}
				c.emitter.EmitNoOperand(bytecode.OP_ARRAY_SPREAD)
			} else {
				// 普通元素
				if err := c.compileExpression(elem); err != nil {
					return err
				}
				c.emitter.EmitNoOperand(bytecode.OP_ARRAY_PUSH)
			}
		}
	}
	return nil
}

func (c *Compiler) compileObjectLiteral(node *ast.ObjectLiteral) error {
	c.emitter.EmitNoOperand(bytecode.OP_NEW_OBJECT)
	for _, sp := range node.Spread {
		// 对象展开: {...src}
		if err := c.compileExpression(sp); err != nil {
			return err
		} // [obj, src]
		c.emitter.EmitNoOperand(bytecode.OP_OBJECT_SPREAD) // [obj]
	}
	// withThisHome 在「方法定义 / 访问器」的 home object 语境里编译函数值:
	// 对象字面量方法的 [[HomeObject]] 是字面量产生的对象本身, 没有可加载
	// 的名字, 只能运行期取 this (evalHome.ThisHome, 见 emitEvalMarks 与
	// eval 单元的代码生成)。{ m: function(){} } / { m: v } 这类非方法值
	// **不**设 —— 它们是普通函数/表达式, 没有 home object (roiE5Z)。
	withThisHome := func(fn func() error) error {
		prev := c.evalHome
		c.evalHome = evalHomeCtx{ThisHome: true}
		defer func() { c.evalHome = prev }()
		return fn()
	}
	for _, prop := range node.Properties {
		if prop.Computed {
			// 计算属性: [expr]: value / [expr]() {} / get [expr]() / set [expr](v)
			if prop.Kind == ast.PROP_GETTER || prop.Kind == ast.PROP_SETTER {
				// 动态键访问器: [obj] → DUP → fn → key → SET_x_DYN → [obj]
				c.emitter.EmitNoOperand(bytecode.OP_DUP)
				fn, ok := prop.Value.(*ast.FunctionExpression)
				if !ok {
					return fmt.Errorf("compiler: getter/setter value is not a function")
				}
				name := "get/set <computed>"
				if id, ok := prop.Key.(*ast.Identifier); ok {
					name = id.Value
				}
				var meta *bytecode.FunctionMetadata
				if err := withThisHome(func() error {
					var merr error
					// 访问器是"方法定义": 无 [[Construct]] / 无 prototype (rlGCky)。
					c.methodFnDepth++
					meta, merr = c.compileFunctionWithStrict(fn.Strict, name, fn.Parameters, fn.Body, false, fn.IsGenerator, fn.IsAsync)
					c.methodFnDepth--
					return merr
				}); err != nil {
					return err
				}
				idx := c.constants.AddConstant(meta)
				c.emitter.Emit(bytecode.OP_FUNCTION, idx) // [obj, obj, fn]
				if err := c.compileExpression(prop.Key); err != nil {
					return err
				} // [obj, obj, fn, key]
				if prop.Kind == ast.PROP_GETTER {
					c.emitter.EmitNoOperand(bytecode.OP_SET_GETTER_DYN)
				} else {
					c.emitter.EmitNoOperand(bytecode.OP_SET_SETTER_DYN)
				}
				continue
			}
			// 计算键值/方法: [expr]: value / [expr]() {}
			// [obj] → DUP → [obj, obj]
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
			// 编译值。计算键**方法** ([k]() {}) 有 home object; 计算键值
			// ([k]: v) 是普通表达式, 没有 home (roiE5Z)。
			compileVal := func() error {
				if prop.Kind != ast.PROP_INIT {
					// 方法定义: 无 [[Construct]] / 无 prototype (rlGCky)。
					c.methodFnDepth++
					err := c.compileExpression(prop.Value)
					c.methodFnDepth--
					return err
				}
				return c.compileExpression(prop.Value)
			}
			if prop.Kind == ast.PROP_METHOD {
				if err := withThisHome(compileVal); err != nil {
					return err
				}
			} else if err := compileVal(); err != nil {
				return err
			} // [obj, obj, val]
			// 编译键表达式
			if err := c.compileExpression(prop.Key); err != nil {
				return err
			} // [obj, obj, val, key]
			// SWAP → [obj, obj, key, val]
			c.emitter.EmitNoOperand(bytecode.OP_SWAP)
			// SET_INDEX → pops val, key, obj; pushes val → [obj, val]
			c.emitter.EmitNoOperand(bytecode.OP_SET_INDEX)
			// POP val → [obj]
			c.emitter.EmitNoOperand(bytecode.OP_POP)
		} else if prop.Kind == ast.PROP_GETTER || prop.Kind == ast.PROP_SETTER {
			// getter/setter: 编译为函数 → [obj, fn] → SET_GETTER/SETTER → [obj]
			fn, ok := prop.Value.(*ast.FunctionExpression)
			if !ok {
				return fmt.Errorf("compiler: getter/setter value is not a function")
			}
			name := prop.Key.(*ast.Identifier).Value
			// 访问器函数的 name 按规范带前缀: get x / set x。
			prefix := "get "
			if prop.Kind == ast.PROP_SETTER {
				prefix = "set "
			}
			var meta *bytecode.FunctionMetadata
			if err := withThisHome(func() error {
				var merr error
				// 访问器是"方法定义": 无 [[Construct]] / 无 prototype (rlGCky)。
				c.methodFnDepth++
				meta, merr = c.compileFunctionWithStrict(fn.Strict, prefix+name, fn.Parameters, fn.Body, false, fn.IsGenerator, fn.IsAsync)
				c.methodFnDepth--
				return merr
			}); err != nil {
				return err
			}
			idx := c.constants.AddConstant(meta)
			c.emitter.Emit(bytecode.OP_FUNCTION, idx) // [obj, fn]
			keyIdx := c.constants.AddConstant(object.NewString(name))
			if prop.Kind == ast.PROP_GETTER {
				c.emitter.Emit(bytecode.OP_SET_GETTER, keyIdx)
			} else {
				c.emitter.Emit(bytecode.OP_SET_SETTER, keyIdx)
			}
		} else {
			// 普通属性 / 简写 / 方法定义
			// 键名先取出: 匿名函数/类定义要按属性名命名 (NamedEvaluation)。
			keyName := prop.Key.(*ast.Identifier).Value
			// { __proto__: v } 是原型设值 (B.3.1), 不是属性定义, 不参与命名 ——
			// 故其匿名函数值保持 ""。而 { __proto__(){} } (PROP_METHOD) 是真正
			// 的属性, 照常命名为 "__proto__"。
			protoSetter := prop.Kind == ast.PROP_INIT && !prop.Shorthand && keyName == "__proto__"
			var err error
			compileVal := func() error {
				if protoSetter {
					return c.compileExpression(prop.Value)
				}
				if prop.Kind != ast.PROP_INIT {
					// 方法定义: 无 [[Construct]] / 无 prototype (rlGCky)。
					c.methodFnDepth++
					cerr := c.compileNamedExpression(prop.Value, keyName)
					c.methodFnDepth--
					return cerr
				}
				return c.compileNamedExpression(prop.Value, keyName)
			}
			// 方法定义 { m(){} } / { get x(){} } 有 home object;
			// 数据属性 (含简写、函数表达式值) 没有 (roiE5Z)。
			if prop.Kind == ast.PROP_METHOD {
				err = withThisHome(compileVal)
			} else {
				err = compileVal()
			}
			if err != nil {
				return err
			} // [obj, val]
			// 设置属性
			idx := c.constants.AddConstant(object.NewString(keyName))
			c.emitter.Emit(bytecode.OP_SET_PROP, idx) // [obj]
		}
	}
	return nil
}

func (c *Compiler) compileTemplateLiteral(node *ast.TemplateLiteral) error {
	// OP_TEMPLATE_START [部分数]
	// 每个部分: 编译表达式, OP_TEMPLATE_PART
	// OP_TEMPLATE_END
	numParts := len(node.Quasis) + len(node.Expressions)
	c.emitter.Emit(bytecode.OP_TEMPLATE_START, uint16(numParts))

	for i, expr := range node.Expressions {
		// 字符串部分
		if i < len(node.Quasis) {
			idx := c.constants.AddConstant(object.NewString(node.Quasis[i]))
			c.emitter.Emit(bytecode.OP_CONST, idx)
			c.emitter.EmitNoOperand(bytecode.OP_TEMPLATE_PART)
		}
		// 表达式部分
		if err := c.compileExpression(expr); err != nil {
			return err
		}
		c.emitter.EmitNoOperand(bytecode.OP_TEMPLATE_PART)
	}
	// 最后的字符串部分
	if len(node.Quasis) > len(node.Expressions) {
		idx := c.constants.AddConstant(object.NewString(node.Quasis[len(node.Quasis)-1]))
		c.emitter.Emit(bytecode.OP_CONST, idx)
		c.emitter.EmitNoOperand(bytecode.OP_TEMPLATE_PART)
	}

	c.emitter.EmitNoOperand(bytecode.OP_TEMPLATE_END)
	return nil
}

// compileTaggedTemplate 编译 tagged template: tag`a${1}b${2}`
// 编译 tag 表达式 → 各插值表达式 → OP_TAGGED_TEMPLATE (操作数 = strings 数组常量)。

func (c *Compiler) compileTaggedTemplate(node *ast.TaggedTemplateExpression) error {
	// 编译 tag 函数
	if err := c.compileExpression(node.Tag); err != nil {
		return err
	}

	// 编译插值表达式 (栈上顺序: [tag, expr1, expr2, ...])
	for _, expr := range node.Template.Expressions {
		if err := c.compileExpression(expr); err != nil {
			return err
		}
	}

	// 构建 strings 数组 (含 raw 属性) 作为常量
	// 注意: 编译期 ArrayProto 可能尚未初始化, 数组原型由 VM 在运行时重建 (OP_TAGGED_TEMPLATE)
	stringsArr := object.NewArray([]object.Value{})
	for _, q := range node.Template.Quasis {
		stringsArr.Elements = append(stringsArr.Elements, object.NewString(q))
	}
	// raw 数组 (相同内容, 简化; 真实 JS 中 raw 保留未转义形式)
	rawArr := object.NewArray([]object.Value{})
	for _, q := range node.Template.Quasis {
		rawArr.Elements = append(rawArr.Elements, object.NewString(q))
	}
	stringsArr.SetProperty("raw", rawArr)

	idx := c.constants.AddConstant(stringsArr)
	c.emitter.Emit(bytecode.OP_TAGGED_TEMPLATE, idx)
	return nil
}

// compileDynamicImport 编译动态 import() 表达式。
// 编译模块路径表达式 → OP_DYNAMIC_IMPORT (加载模块并包装为 Promise)。

func (c *Compiler) compileDynamicImport(node *ast.DynamicImportExpression) error {
	if err := c.compileExpression(node.Source); err != nil {
		return err
	}
	if node.IsSourcePhase {
		// import.source(spec): 规范 16.2.1.7.2 —— SourceTextModuleRecord 的
		// GetModuleSource 恒抛 SyntaxError, 所以这里不加载模块, 只把 specifier
		// 求值 (ToString 副作用仍要发生) 后 reject 一个 SyntaxError。
		c.emitter.EmitNoOperand(bytecode.OP_IMPORT_SOURCE)
		return nil
	}
	c.emitter.EmitNoOperand(bytecode.OP_DYNAMIC_IMPORT)
	return nil
}

// ===== 函数编译 =====

func (c *Compiler) compileNewExpression(node *ast.NewExpression) error {
	// new C(...args): 实参个数要运行期才知道, 而 OP_NEW 的 argc 是编译期常量
	// —— 与 OP_CALL_SPREAD 同辙: 先把实参收成一个数组 (栈: [argsArray, callee]),
	// 再由 OP_NEW_SPREAD 摊平。此前这里直接逐个编译实参, 遇到 spread 就掉到
	// "unsupported expression type: *ast.SpreadElement" 编译错 —— built-ins 里
	// Temporal 的官方 harness (temporalHelpers.js) 全靠 `new construct(...args)`
	// 转发实参, 一整族用例因为这一句停在编译期, 连引擎实现都摸不到。
	if hasSpreadArgs(node.Arguments) {
		if err := c.compileArgumentsArray(node.Arguments); err != nil {
			return err
		}
		if err := c.compileExpression(node.Callee); err != nil {
			return err
		}
		c.emitter.Emit(bytecode.OP_NEW_SPREAD, 0)
		return nil
	}
	// 编译参数
	for _, arg := range node.Arguments {
		if err := c.compileExpression(arg); err != nil {
			return err
		}
	}
	// 编译构造函数
	if err := c.compileExpression(node.Callee); err != nil {
		return err
	}
	c.emitter.Emit(bytecode.OP_NEW, uint16(len(node.Arguments)))
	return nil
}

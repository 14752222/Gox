package compiler

import (
	"fmt"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/object"
)

func (c *Compiler) compileForOfStatement(stmt *ast.ForOfStatement) error {
	if stmt.Await {
		return c.compileForAwaitOfStatement(stmt)
	}

	closeSlot := c.allocHiddenSlot()

	// 编译可迭代对象并取迭代器, 存进隐藏槽 (栈不留副本 —— 循环头每轮从槽里
	// LOAD, body 阶段栈完全干净)。
	if err := c.compileExpression(stmt.Iterable); err != nil {
		return err
	}
	c.emitter.EmitNoOperand(bytecode.OP_GET_ITERATOR)    // [iter]
	c.emitter.Emit(bytecode.OP_STORE, uint16(closeSlot)) // []

	loopStart := c.emitter.Pos()
	c.emitter.Emit(bytecode.OP_LOAD, uint16(closeSlot)) // [iter]
	// 同步迭代一步: [iter] → [iter, step] (step 是 {value, done} 对象)。
	// next() 抛异常时 VM 先清空 closeSlot 再重抛 (等价 iteratorRecord.[[done]]
	// = true), 且该异常发生在 close handler 覆盖范围之外 ⇒ 不调 return()。
	c.emitter.Emit(bytecode.OP_ITER_STEP, uint16(closeSlot)) // [iter, step]

	// 检查 step.done (GET_PROP 弹 obj 压结果, 故先 DUP 保住 step):
	// [iter, step] → DUP → [iter, step, step] → GET_PROP "done" →
	// [iter, step, done] → JUMP_IF_TRUE (不弹) → 假值 POP → [iter, step]
	doneIdx := c.constants.AddConstant(object.NewString("done"))
	c.emitter.EmitNoOperand(bytecode.OP_DUP)
	c.emitter.Emit(bytecode.OP_GET_PROP, doneIdx) // [iter, step, done]
	endJump := c.emitter.EmitJump(bytecode.OP_JUMP_IF_TRUE)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 假值路径: 弹出 done → [iter, step]
	valueIdx := c.constants.AddConstant(object.NewString("value"))
	c.emitter.Emit(bytecode.OP_GET_PROP, valueIdx) // [iter, value]

	// ---- 本轮迭代的 close handler: 只覆盖绑定 + body ----
	c.emitter.EmitNoOperand(bytecode.OP_PUSH_SCOPE)
	prevScope := c.scope
	c.scope = NewSymbolScope(prevScope)
	// 运行时: 纯 finally 形状 (catchPC=0), 异常走 finallyPC → closePC。
	c.emitter.Emit(bytecode.OP_PUSH_TRY, 0)
	closeFin := c.emitter.EmitJump(bytecode.OP_PUSH_FINALLY)
	// 控制上下文先压栈: close 条目随后追加, 处于 ctx.tryScopes 之外 ——
	// break/return 穿出时由 emitTryUnwind 解退并内联 close; continue 靠
	// ctx.selfClose 把它排除 (循环底常规 POP_TRY 摘掉)。
	ctx := c.pushControl(c.takePendingLabel(), true)
	ctx.selfClose = 1
	// 编译期镜像: body 里的 break/return 由 emitTryUnwind 内联 close。
	savedTryLen := len(c.tryScopes)
	c.tryScopes = append(c.tryScopes, tryScope{hasFinally: true, closeSlot: closeSlot, closeSync: true})

	// 本对象的作用域槽位全部在 PUSH_SCOPE 之后分配 —— 但只有 **[块作用域内
	// 新分配]** 的槽位才属于"每轮新建的词法绑定"(循环变量 + 循环体内的
	// let/const/函数声明)。函数作用域层的 var (已提升, 槽位在块之前分配)
	// 与外层块的 let 都是**单实例**, 闭包必须共享同一颗 cell —— 绝不能被
	// 迭代边界克隆。sealFrom 记下块作用域内首个新槽位, 迭代边界只克隆
	// [sealFrom:] (见 vm.go OP_ITER_BOUNDARY)。
	// 注: 上面的 try/finally 建立代码不分配 locals 槽位, 故与 sealFrom 取值无涉。
	sealFrom := c.scope.NumLocals()

	// 绑定本次迭代的值 (栈顶)。三种形状:
	//   简单绑定 for (let x of arr)          → 直接存进新建的槽位
	//   解构绑定 for (const [a, b] of pairs) → 交给 compilePatternBind 按模式拆开
	//   var 绑定 for (var x of arr)          → 存进**函数作用域层**的共享槽位
	// 进来时栈是 [.., value]、离开时回到 [..] —— compilePatternBind 末尾自带
	// POP, 与 OP_STORE 消耗栈顶值的语义对齐。
	varKind := bytecode.OP_STORE
	if _, ok := stmt.VarDecl.(*ast.ConstStatement); ok {
		varKind = bytecode.OP_STORE_CONST
	}
	if stmt.Pattern != nil {
		// 解构绑定 vs 解构赋值目标, 靠 VarDecl 是否为 nil 区分:
		//   声明绑定 for (const [a, b] of pairs) → VarDecl 是 __destructure__
		//     空壳, kind=bindLexical: 每轮迭代是新的块作用域 (上面已 PUSH_SCOPE),
		//     解构出来的名字声明进这个作用域, 所以各轮的绑定互不影响 ——
		//     闭包捕获到的是各自的槽位。
		//   赋值目标 for ([a, b] of xs) → VarDecl 为 nil, kind=bindAssign:
		//     每轮是**赋值**, 写入外部已声明的绑定 (node 实测: for ([a,b] of …)
		//     修改的就是外层 a/b)。
		// 三态分流: VarDecl 为 nil 是**赋值**目标 (for ([a, b] of xs) 写外部
		// 已有绑定); var 声明落函数作用域层; let/const 落每轮块作用域。
		pattKind := bindAssign
		if stmt.VarDecl != nil {
			pattKind = bindLexical
			if _, isVar := stmt.VarDecl.(*ast.VarStatement); isVar {
				pattKind = bindVar
			}
		}
		if err := c.compilePatternBind(stmt.Pattern, pattKind); err != nil {
			c.tryScopes = c.tryScopes[:savedTryLen]
			return err
		}
	} else if stmt.Target != nil {
		// 无声明赋值目标 for (x of xs) / for (obj.k of xs): 每轮迭代是
		// 赋值, 与 var 形态一样写**外部**绑定 (不是每轮新声明)
		if err := c.compileForOfTargetAssign(stmt.Target); err != nil {
			c.tryScopes = c.tryScopes[:savedTryLen]
			return err
		}
	} else if _, isVarDecl := stmt.VarDecl.(*ast.VarStatement); isVarDecl {
		// var: 所有迭代共享函数作用域层的同一绑定 (闭包捕获同一槽位 ——
		// 这正是 var 循环的经典语义), 每轮只是重新赋值。
		fn := c.scope.FuncLayer()
		sym := fn.ResolveLocal(stmt.Variable.Value)
		if sym == nil {
			sym = fn.Define(stmt.Variable.Value, false)
			sym.IsVarLike = true
			sym.Declared = true
		}
		if fn.Parent() == nil && !c.moduleMode {
			// 全局函数层: var 存全局环境 (顶层没有 frame locals 槽位存储)
			nameIdx := c.constants.AddConstant(object.NewString(stmt.Variable.Value))
			c.emitter.Emit(bytecode.OP_STORE_GLOBAL, nameIdx)
		} else {
			c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
		}
	} else {
		sym := c.scope.Define(stmt.Variable.Value, varKind == bytecode.OP_STORE_CONST)
		c.emitter.Emit(varKind, uint16(sym.Slot))
	}
	// 绑定消耗 value 后残留的 [iter] → [] (body 阶段栈干净)
	c.emitter.EmitNoOperand(bytecode.OP_POP)

	// 循环体
	if err := c.compileLoopBody(stmt.Body.Statements); err != nil {
		c.tryScopes = c.tryScopes[:savedTryLen]
		return err
	}

	c.tryScopes = c.tryScopes[:savedTryLen]
	c.scope = prevScope
	c.emitter.EmitNoOperand(bytecode.OP_POP_SCOPE)

	// continue 跳回迭代头 (经过迭代边界: 每次迭代的绑定互不影响)
	c.emitter.Emit(bytecode.OP_ITER_BOUNDARY, uint16(sealFrom))
	iterPos := c.emitter.Pos()
	for _, jmp := range ctx.continueJumps {
		c.emitter.ReplaceJumpTarget(jmp, uint16(iterPos))
	}
	c.emitter.EmitNoOperand(bytecode.OP_POP_TRY)
	c.emitter.Emit(bytecode.OP_LOOP, uint16(loopStart))

	// done 出口: [iter, step, done] → 逐个弹出 (正常完成不 close)
	c.emitter.PatchJump(endJump)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出 done (JUMP_IF_TRUE 不弹)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出 step
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出 iter

	// break 也汇到这里 (break 点已在 emitTryUnwind 内联过 close + POP_TRY)
	for _, jmp := range ctx.breakJumps {
		c.emitter.PatchJump(jmp)
	}
	exitJump := c.emitter.EmitJump(bytecode.OP_JUMP) // 正常出口: 跳过 closePC 块

	// ---- 控制转移 / 异常穿出的 close 落点 ----
	// handleThrowInner / handleReturnInner 的 finallyPC 分支: 栈已截回
	// entry.stackBase, 挂起完成记在 tryStack 条目的 pendingVal / pendingReturn
	// 上。用 OP_ITER_CLOSE_ABRUPT 跑 close —— close 自身的异常按挂起完成
	// 的类型取舍 (throw 穿出丢弃, return/break/continue 穿出传播, 规范
	// 7.4.6 步骤 6~8), 然后 END_FINALLY 重抛挂起完成。
	// (此前这里是无条件 swallow, 把 generator return() 穿出时应报的
	//  TypeError 也一起吞了。)
	c.emitter.PatchJump(closeFin)
	c.emitter.Emit(bytecode.OP_ITER_CLOSE_ABRUPT, uint16(closeSlot))
	c.emitter.EmitNoOperand(bytecode.OP_END_FINALLY)
	c.emitter.PatchJump(exitJump)

	c.popControl()
	return nil
}

// compileForAwaitOfStatement 编译 for await (binding of iterable) { body }。
//
// 只允许出现在 async 函数体内: async 函数编译成「wrapper + 内层 generator」,
// 函数体在内层 generator 里执行, OP_YIELD 暂停帧由 __spawn 驱动 (yield 出
// Promise → 等 resolve → 把 resolve 值作为恢复值传回)。for-await 的每一步
// 「取下一个」恰好就是这个形状:
//
//	LOAD slot            → [iter]        迭代器存隐藏局部槽 (收尾序列也要用)
//	ASYNC_ITER_NEXT      → [iter, step]  step = next() 结果 (可能 Promise)
//	OP_YIELD             → [iter, step'] Promise 时 step' = resolve 值;
//	                                     同步形状 __spawn 原样回传, 同一位置
//	.step.done           → 真则跳出 (done 正常出口不 close, node 实测)
//	.step.value          → OP_YIELD 解包 每步的 value 也要 await (node 实测:
//	                                     for await (x of [p, 2]) → [1, 2])
//	绑定给头部 (复用同步 for-of 的三形状绑定代码)
//
// 「await 检查」: parser 不跟踪 async 上下文 (见 parseForAwaitOfStatement
// 注释), 编译期校验 —— 当前函数不是 async 时报 SyntaxError (规范上 for-await
// 体外是早错)。
//
// == AsyncIteratorClose (P3) ==
// break / return / body 抛异常穿出循环体时, 必须调用迭代器的 return() 并
// await 其结果 (没有 return 方法则跳过)。挂载机制复用 try/finally 的
// rMkA8D 基础设施:
//   - 运行时: 每轮迭代绑定前 PUSH_TRY 0 + PUSH_FINALLY closePC, body 抛
//     异常由 handleThrowInner 走 finallyPC 分支跳到 closePC → close 序列
//     → END_FINALLY 重抛原异常。handler 只覆盖绑定+body: next()/value 的
//     await 拒绝不触发 close (node 实测 next-reject 不调 return)。
//   - 编译期: close 条目压入 tryScopes (tryScope.closeSlot), body 里的
//     break/return 经 emitTryUnwind 自动内联 close 序列 —— 标签 break、
//     嵌套 for-await、穿多层 try 全部由既有机制按由内向外顺序收尾。
//   - close 异常的优先级 (node 实测钉死): body 抛异常时 close 序列里的
//     异常被吞掉 (原异常胜出, closePC 处再套一层 catch 丢弃); break/return
//     路径上 close 的异常照常传播。done 正常出口与 continue 不调 return()。

func (c *Compiler) compileForAwaitOfStatement(stmt *ast.ForOfStatement) error {
	// async 函数体内合法; 模块顶层 (TLA) 同样合法 —— 它与顶层 await 走
	// 同一条"主单元当生成器"的驱动路径。
	if c.moduleMode && c.fnDepth == 0 {
		c.topLevelAwait = true
	} else if !c.inAsyncFunction {
		return fmt.Errorf("compiler: SyntaxError: 'for await...of' is only allowed inside an async function")
	}

	closeSlot := c.allocHiddenSlot()

	// 编译可迭代表达式并取异步迭代器, 存进隐藏槽 (栈不留副本 ——
	// 循环头每轮从槽里 LOAD, body 阶段栈完全干净)
	if err := c.compileExpression(stmt.Iterable); err != nil {
		return err
	}
	c.emitter.EmitNoOperand(bytecode.OP_GET_ASYNC_ITERATOR) // [iter]
	c.emitter.EmitNoOperand(bytecode.OP_DUP)                // [iter, iter]
	c.emitter.Emit(bytecode.OP_STORE, uint16(closeSlot))    // [iter]
	c.emitter.EmitNoOperand(bytecode.OP_POP)                // []

	loopStart := c.emitter.Pos()
	c.emitter.Emit(bytecode.OP_LOAD, uint16(closeSlot)) // [iter]
	// 异步迭代一步: [iter] → [iter, step]; 挂起点等待 Promise
	// (同步形状的 step 原样穿过), 恢复值即步进结果对象。
	c.emitter.EmitNoOperand(bytecode.OP_ASYNC_ITER_NEXT)
	// async generator 体内这是内部挂起点 (OP_AWAIT), 普通 async 函数体内是
	// OP_YIELD —— 二者帧语义相同, 区别只在驱动如何识别挂起点 (消费者可见性)。
	if c.asyncGeneratorBody {
		c.emitter.EmitNoOperand(bytecode.OP_AWAIT) // [iter, step]
	} else {
		c.emitter.EmitNoOperand(bytecode.OP_YIELD) // [iter, step]
	}

	// 检查 step.done (GET_PROP 弹 obj 压结果, 故先 DUP 保住 step):
	// [iter, step] → DUP → [iter, step, step] → GET_PROP "done" →
	// [iter, step, done] → JUMP_IF_TRUE (不弹) → 假值 POP → [iter, step]
	doneIdx := c.constants.AddConstant(object.NewString("done"))
	c.emitter.EmitNoOperand(bytecode.OP_DUP)
	c.emitter.Emit(bytecode.OP_GET_PROP, doneIdx) // [iter, step, done]
	endJump := c.emitter.EmitJump(bytecode.OP_JUMP_IF_TRUE)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 假值路径: 弹出 done → [iter, step]

	// 取 value 并 await 解包 (CreateAsyncFromSyncIterator 语义: 同步迭代器
	// 吐出的 value 是 Promise 时要解包; 非 Promise 原样穿过)
	valueIdx := c.constants.AddConstant(object.NewString("value"))
	c.emitter.Emit(bytecode.OP_GET_PROP, valueIdx) // [iter, value]
	if c.asyncGeneratorBody {
		c.emitter.EmitNoOperand(bytecode.OP_AWAIT)
	} else {
		c.emitter.EmitNoOperand(bytecode.OP_YIELD)
	}

	// ---- 本轮迭代的 close handler: 只覆盖绑定 + body ----
	c.emitter.EmitNoOperand(bytecode.OP_PUSH_SCOPE)
	prevScope := c.scope
	c.scope = NewSymbolScope(prevScope)
	// 迭代边界只克隆块内新分配的槽位 (循环变量 + body 的 let/const), 函数层
	// var 与外层 let 保持单实例共享 —— 与同步 for-of 同口径 (见其处详解)。
	sealFrom := c.scope.NumLocals()
	// 运行时: 纯 finally 形状 (catchPC=0), 异常走 finallyPC → closePC。
	c.emitter.Emit(bytecode.OP_PUSH_TRY, 0)
	closeFin := c.emitter.EmitJump(bytecode.OP_PUSH_FINALLY)
	// 控制上下文先压栈: close 条目随后追加, 处于 ctx.tryScopes 之外 ——
	// break/return 穿出时由 emitTryUnwind 解退并内联 close; continue 靠
	// ctx.selfClose 把它排除 (循环底常规 POP_TRY 摘掉)。
	ctx := c.pushControl(c.takePendingLabel(), true)
	ctx.selfClose = 1
	// 编译期镜像: body 里的 break/return 由 emitTryUnwind 内联 close。
	savedTryLen := len(c.tryScopes)
	c.tryScopes = append(c.tryScopes, tryScope{hasFinally: true, closeSlot: closeSlot})

	varKind := bytecode.OP_STORE
	if _, ok := stmt.VarDecl.(*ast.ConstStatement); ok {
		varKind = bytecode.OP_STORE_CONST
	}
	if stmt.Pattern != nil {
		// 与同步 for-of 同口径: VarDecl 为 nil 的解构是**赋值**目标
		// 三态分流: VarDecl 为 nil 是**赋值**目标 (for ([a, b] of xs) 写外部
		// 已有绑定); var 声明落函数作用域层; let/const 落每轮块作用域。
		pattKind := bindAssign
		if stmt.VarDecl != nil {
			pattKind = bindLexical
			if _, isVar := stmt.VarDecl.(*ast.VarStatement); isVar {
				pattKind = bindVar
			}
		}
		if err := c.compilePatternBind(stmt.Pattern, pattKind); err != nil {
			c.tryScopes = c.tryScopes[:savedTryLen]
			return err
		}
	} else if stmt.Target != nil {
		// for await (x of xs) / for await (obj.k of xs): 每轮赋值给外部绑定
		if err := c.compileForOfTargetAssign(stmt.Target); err != nil {
			c.tryScopes = c.tryScopes[:savedTryLen]
			return err
		}
	} else if _, isVarDecl := stmt.VarDecl.(*ast.VarStatement); isVarDecl {
		fn := c.scope.FuncLayer()
		sym := fn.ResolveLocal(stmt.Variable.Value)
		if sym == nil {
			sym = fn.Define(stmt.Variable.Value, false)
			sym.IsVarLike = true
			sym.Declared = true
		}
		if fn.Parent() == nil && !c.moduleMode {
			// 全局函数层: var 存全局环境 (顶层没有 frame locals 槽位存储)
			nameIdx := c.constants.AddConstant(object.NewString(stmt.Variable.Value))
			c.emitter.Emit(bytecode.OP_STORE_GLOBAL, nameIdx)
		} else {
			c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
		}
	} else {
		sym := c.scope.Define(stmt.Variable.Value, varKind == bytecode.OP_STORE_CONST)
		c.emitter.Emit(varKind, uint16(sym.Slot))
	}
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 绑定消耗 value 后残留的 [iter] → [] (body 阶段栈干净)

	// 循环体
	if err := c.compileLoopBody(stmt.Body.Statements); err != nil {
		c.tryScopes = c.tryScopes[:savedTryLen]
		return err
	}

	c.tryScopes = c.tryScopes[:savedTryLen]
	c.scope = prevScope
	c.emitter.EmitNoOperand(bytecode.OP_POP_SCOPE)

	// 循环底: continue 跳回迭代头 (先摘掉本轮 close handler 再回跳)
	c.emitter.Emit(bytecode.OP_ITER_BOUNDARY, uint16(sealFrom))
	iterPos := c.emitter.Pos()
	for _, jmp := range ctx.continueJumps {
		c.emitter.ReplaceJumpTarget(jmp, uint16(iterPos))
	}
	c.emitter.EmitNoOperand(bytecode.OP_POP_TRY)
	c.emitter.Emit(bytecode.OP_LOOP, uint16(loopStart))

	// done 出口: [iter, step, done] → 逐个弹出 (正常完成不 close, node 实测)
	c.emitter.PatchJump(endJump)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出 done (JUMP_IF_TRUE 不弹)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出 step
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出 iter

	// break 也汇到这里 (break 点已在 emitTryUnwind 内联过 close + POP_TRY)
	for _, jmp := range ctx.breakJumps {
		c.emitter.PatchJump(jmp)
	}
	exitJump := c.emitter.EmitJump(bytecode.OP_JUMP) // 正常出口: 跳过 closePC 块

	// ---- 异常路径的 close 落点 ----
	// handleThrowInner 的 finallyPC 分支: 栈已截回 entry.stackBase (干净),
	// 错误值记在 tryStack 条目的 pendingVal 上。跑 close 序列 (套一层
	// catch 丢弃 close 自身的异常 —— node: 原异常优先), 然后 END_FINALLY
	// 重抛原异常。
	c.emitter.PatchJump(closeFin)
	swallowTry := c.emitter.EmitJump(bytecode.OP_PUSH_TRY)
	c.emitIteratorCloseSequence(closeSlot)
	c.emitter.EmitNoOperand(bytecode.OP_POP_TRY)
	overSwallow := c.emitter.EmitJump(bytecode.OP_JUMP)
	// close 序列自己抛异常: 丢弃它, 让 END_FINALLY 重抛原异常
	c.emitter.PatchJump(swallowTry)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.PatchJump(overSwallow)
	c.emitter.EmitNoOperand(bytecode.OP_END_FINALLY)
	c.emitter.PatchJump(exitJump)

	c.popControl()
	return nil
}

func (c *Compiler) compileForOfTargetAssign(target ast.Expression) error {
	switch t := target.(type) {
	case *ast.Identifier:
		// for (x of xs): 与 x = v 的写入路径完全一致
		c.emitIdentifierAssign(t.Value)
		return nil
	case *ast.MemberExpression:
		// for (obj.k of xs) / for (obj.#p of xs):
		// compileMemberRef 约定栈上是 [obj, key], 但值已在栈顶 ——
		// [val, obj, key] → DUP_BELOW2+POP ×2 → [obj, key, val]
		// → SET_INDEX (推回写入值) → [val] → POP → []
		if err := c.compileMemberRef(t); err != nil {
			return err
		}
		c.emitter.EmitNoOperand(bytecode.OP_DUP_BELOW2)
		c.emitter.EmitNoOperand(bytecode.OP_POP)
		c.emitter.EmitNoOperand(bytecode.OP_DUP_BELOW2)
		c.emitter.EmitNoOperand(bytecode.OP_POP)
		c.emitter.EmitNoOperand(bytecode.OP_SET_INDEX)
		c.emitter.EmitNoOperand(bytecode.OP_POP)
		return nil
	}
	return fmt.Errorf("compiler: unsupported for...of assignment target: %T", target)
}

func (c *Compiler) compileForInStatement(stmt *ast.ForInStatement) error {
	// 编译被迭代对象
	if err := c.compileExpression(stmt.Iterable); err != nil {
		return err
	}
	// 初始化键迭代器 (栈顶保留对象, VM 内部持有键列表)
	c.emitter.EmitNoOperand(bytecode.OP_FOR_IN_INIT)

	loopStart := c.emitter.Pos()
	// 取下一个键
	c.emitter.EmitNoOperand(bytecode.OP_FOR_IN_NEXT)
	// 栈顶是键或 undefined (迭代结束)

	// 检查是否结束
	c.emitter.EmitNoOperand(bytecode.OP_DUP)
	endJump := c.emitter.EmitJump(bytecode.OP_JUMP_IF_NULL)
	c.emitter.EmitNoOperand(bytecode.OP_POP)

	// 块作用域
	c.emitter.EmitNoOperand(bytecode.OP_PUSH_SCOPE)
	prevScope := c.scope
	c.scope = NewSymbolScope(prevScope)
	// 同 for-of: 迭代边界只克隆块作用域内新分配的槽位 (每轮新建的词法绑定),
	// var (函数作用域, 槽位在块之前分配) 与外层 let 保持单实例共享。
	sealFrom := c.scope.NumLocals()

	// 绑定本次迭代的值 (栈顶)。四条路径, 与 for-of 头部**逐条同构** (for-in 每轮
	// 产出的"值"是键字符串, 与 for-of 产出的元素在绑定语义上无差别):
	//   解构    for ([a,b] in o) / for (const [a,b] in o) → compilePatternBind
	//   赋值    for (x in o) / for (obj.k in o)           → compileForOfTargetAssign
	//   var     for (var x in o)                          → 函数作用域层共享槽位
	//   let/const for (let x in o)                        → 每轮新建块作用域槽位
	if stmt.Pattern != nil {
		// 三态分流 (与 for-of 同口径): VarDecl 为 nil 是**赋值**目标
		// (for ([a, b] in o) 写外部已有绑定); var 声明落函数作用域层;
		// let/const 落每轮块作用域。
		pattKind := bindAssign
		if stmt.VarDecl != nil {
			pattKind = bindLexical
			if _, isVar := stmt.VarDecl.(*ast.VarStatement); isVar {
				pattKind = bindVar
			}
		}
		if err := c.compilePatternBind(stmt.Pattern, pattKind); err != nil {
			return err
		}
	} else if stmt.Target != nil {
		// 无声明赋值目标 for (x in o) / for (obj.k in o): 每轮是赋值, 写外部绑定。
		if err := c.compileForOfTargetAssign(stmt.Target); err != nil {
			return err
		}
	} else {
		varKind := bytecode.OP_STORE
		if _, ok := stmt.VarDecl.(*ast.ConstStatement); ok {
			varKind = bytecode.OP_STORE_CONST
		}
		if _, isVarDecl := stmt.VarDecl.(*ast.VarStatement); isVarDecl {
			// var: 共享函数作用域层绑定 (语义与 for-of 的 var 分支一致)
			fn := c.scope.FuncLayer()
			sym := fn.ResolveLocal(stmt.Variable.Value)
			if sym == nil {
				sym = fn.Define(stmt.Variable.Value, false)
				sym.IsVarLike = true
				sym.Declared = true
			}
			if fn.Parent() == nil && !c.moduleMode {
				nameIdx := c.constants.AddConstant(object.NewString(stmt.Variable.Value))
				c.emitter.Emit(bytecode.OP_STORE_GLOBAL, nameIdx)
			} else {
				c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
			}
		} else {
			sym := c.scope.Define(stmt.Variable.Value, varKind == bytecode.OP_STORE_CONST)
			c.emitter.Emit(varKind, uint16(sym.Slot))
		}
	}

	ctx := c.pushControl(c.takePendingLabel(), true)

	// 循环体
	if err := c.compileLoopBody(stmt.Body.Statements); err != nil {
		return err
	}

	c.scope = prevScope
	c.emitter.EmitNoOperand(bytecode.OP_POP_SCOPE)

	// 迭代边界: 本轮迭代创建的闭包定版
	c.emitter.Emit(bytecode.OP_ITER_BOUNDARY, uint16(sealFrom))
	iterPos := c.emitter.Pos()
	for _, jmp := range ctx.continueJumps {
		c.emitter.ReplaceJumpTarget(jmp, uint16(iterPos))
	}
	c.emitter.Emit(bytecode.OP_LOOP, uint16(loopStart))

	// 迭代结束: 清理栈 (正常终止路径)
	c.emitter.PatchJump(endJump)
	c.emitter.EmitNoOperand(bytecode.OP_POP)        // 弹出 DUP 副本
	c.emitter.EmitNoOperand(bytecode.OP_POP)        // 弹出键值
	c.emitter.EmitNoOperand(bytecode.OP_FOR_IN_END) // 弹出迭代器状态

	// 跳过 break 清理路径
	skipBreakCleanup := c.emitter.EmitJump(bytecode.OP_JUMP)

	// break 退出路径: 跳到这里时栈顶是迭代器, 只需弹一次
	for _, jmp := range ctx.breakJumps {
		c.emitter.PatchJump(jmp)
	}
	c.emitter.EmitNoOperand(bytecode.OP_FOR_IN_END)

	// 正常终止路径越过 break 清理
	c.emitter.PatchJump(skipBreakCleanup)

	c.popControl()
	return nil
}

func (c *Compiler) compileForStatement(stmt *ast.ForStatement) error {
	// for (using x = expr; cond; upd): 释放落点是整个 for 语句 —— 资源在
	// init 登记, 循环结束 (含 break / return / 抛异常穿出) 时逆序释放。
	if us, ok := stmt.Init.(*ast.UsingStatement); ok {
		return c.withDisposeScopeFor([]*ast.UsingStatement{us}, func() error {
			return c.compileForStatementCore(stmt)
		})
	}
	return c.compileForStatementCore(stmt)
}

func (c *Compiler) compileForStatementCore(stmt *ast.ForStatement) error {
	c.emitter.EmitNoOperand(bytecode.OP_PUSH_SCOPE)
	prevScope := c.scope
	c.scope = NewSymbolScope(prevScope)
	// 迭代边界只克隆本块作用域内新分配的槽位 (init 的 let 循环变量 + body 的
	// let/const)。函数层 var 与外层 let 必须保持单实例共享 —— 见 for-of 处详解。
	sealFrom := c.scope.NumLocals()

	// init
	if stmt.Init != nil {
		if err := c.compileStatement(stmt.Init); err != nil {
			return err
		}
	}

	loopStart := c.emitter.Pos()
	ctx := c.pushControl(c.takePendingLabel(), true)

	// condition
	if stmt.Condition != nil {
		if err := c.compileExpression(stmt.Condition); err != nil {
			return err
		}
		jumpFalse := c.emitter.EmitJump(bytecode.OP_JUMP_IF_FALSE)
		c.emitter.EmitNoOperand(bytecode.OP_POP)

		// 循环体
		// 进入新的块作用域
		c.emitter.EmitNoOperand(bytecode.OP_PUSH_SCOPE)
		bodyScope := c.scope
		c.scope = NewSymbolScope(bodyScope)
		if err := c.withDisposeScope(stmt.Body.Statements, func() error {
			return c.compileStatements(stmt.Body.Statements)
		}); err != nil {
			return err
		}
		c.scope = bodyScope
		c.emitter.EmitNoOperand(bytecode.OP_POP_SCOPE)

		// continue 跳到 update; 此处同时是迭代边界 (per-iteration 绑定)
		c.emitter.Emit(bytecode.OP_ITER_BOUNDARY, uint16(sealFrom))
		continueStart := c.emitter.Pos()
		// update
		if stmt.Update != nil {
			if err := c.compileExpression(stmt.Update.(*ast.ExpressionStatement).Expression); err != nil {
				return err
			}
			c.emitter.EmitNoOperand(bytecode.OP_POP)
		}
		c.emitter.Emit(bytecode.OP_LOOP, uint16(loopStart))

		for _, jmp := range ctx.continueJumps {
			c.emitter.ReplaceJumpTarget(jmp, uint16(continueStart))
		}

		// 循环结束
		c.emitter.PatchJump(jumpFalse)
		c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出条件值

		for _, jmp := range ctx.breakJumps {
			c.emitter.PatchJump(jmp)
		}
	} else {
		// 无条件循环
		c.emitter.EmitNoOperand(bytecode.OP_PUSH_SCOPE)
		bodyScope := c.scope
		c.scope = NewSymbolScope(bodyScope)
		if err := c.withDisposeScope(stmt.Body.Statements, func() error {
			return c.compileStatements(stmt.Body.Statements)
		}); err != nil {
			return err
		}
		c.scope = bodyScope
		c.emitter.EmitNoOperand(bytecode.OP_POP_SCOPE)

		// continue 跳到 update; 此处同时是迭代边界 (per-iteration 绑定)
		c.emitter.Emit(bytecode.OP_ITER_BOUNDARY, uint16(sealFrom))
		continueStart := c.emitter.Pos()
		// update (无条件循环同样需要编译 update 段)
		if stmt.Update != nil {
			if err := c.compileExpression(stmt.Update.(*ast.ExpressionStatement).Expression); err != nil {
				return err
			}
			c.emitter.EmitNoOperand(bytecode.OP_POP)
		}
		c.emitter.Emit(bytecode.OP_LOOP, uint16(loopStart))

		for _, jmp := range ctx.continueJumps {
			c.emitter.ReplaceJumpTarget(jmp, uint16(continueStart))
		}
		for _, jmp := range ctx.breakJumps {
			c.emitter.PatchJump(jmp)
		}
	}

	c.popControl()
	c.scope = prevScope
	c.emitter.EmitNoOperand(bytecode.OP_POP_SCOPE)
	return nil
}

// ===== throw / try-catch / switch / import-export 编译 =====

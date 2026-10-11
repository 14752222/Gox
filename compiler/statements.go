package compiler

import (
	"fmt"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/object"
)

func (c *Compiler) compileLetStatement(stmt *ast.LetStatement) error {
	// 解构赋值: let [a, b] = arr  或  let { x, y } = obj
	// 首项是解构时不走下面的普通绑定登记 —— 但仍必须继续编译逗号续接项
	// (stmt.More), 如 `let [a] = [1], b = 2;` / `let [a] = [1], [b] = [2];`。
	if stmt.Name.Value == destructureSyntheticName {
		assign, ok := stmt.Value.(*ast.AssignmentExpression)
		if !ok {
			return fmt.Errorf("compiler: malformed destructuring declaration")
		}
		if err := c.compileDestructureAssignment(assign, bindLexical); err != nil {
			return err
		}
	} else if stmt.Value != nil {
		// 无初始化器 (let x;) 也必须注册符号，
		// 否则后续 x = ... 会被当作全局变量处理。
		// 规范: let x; 等价于 let x = undefined —— 必须显式写入 undefined，
		// 否则局部槽保持未初始化态 (读取触发 TDZ 报错)、全局则根本未声明。
		if err := c.compileNamedExpression(stmt.Value, stmt.Name.Value); err != nil {
			return err
		}
		sym, err := c.declareOnce(stmt.Name.Value, false, false)
		if err != nil {
			return err
		}
		if c.isGlobalScope() {
			// 全局作用域: 声明写入共享全局环境 (支持 REPL 跨输入状态保持)
			nameIdx := c.constants.AddConstant(object.NewString(stmt.Name.Value))
			c.emitter.Emit(bytecode.OP_DECLARE, nameIdx)
		} else {
			c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
		}
	} else {
		sym, err := c.declareOnce(stmt.Name.Value, false, false)
		if err != nil {
			return err
		}
		c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
		if c.isGlobalScope() {
			nameIdx := c.constants.AddConstant(object.NewString(stmt.Name.Value))
			c.emitter.Emit(bytecode.OP_DECLARE, nameIdx)
		} else {
			c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
		}
	}

	// 多条声明: let a = 1, b = 2; / let [x] = a, z = 3;
	for _, d := range stmt.More {
		if d.Name != nil && d.Name.Value == destructureSyntheticName {
			// 解构声明项 (let [x] = a, {y} = b): Value 是合成形态的
			// AssignmentExpression{Left: 模式}, 与首项解构路径同构。
			assign, ok := d.Value.(*ast.AssignmentExpression)
			if !ok {
				return fmt.Errorf("compiler: malformed destructuring declarator")
			}
			if err := c.compileDestructureAssignment(assign, bindLexical); err != nil {
				return err
			}
			continue
		}
		if d.Value != nil {
			if err := c.compileNamedExpression(d.Value, d.Name.Value); err != nil {
				return err
			}
			sym, err := c.declareOnce(d.Name.Value, false, false)
			if err != nil {
				return err
			}
			if c.isGlobalScope() {
				nameIdx := c.constants.AddConstant(object.NewString(d.Name.Value))
				c.emitter.Emit(bytecode.OP_DECLARE, nameIdx)
			} else {
				c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
			}
		} else {
			sym, err := c.declareOnce(d.Name.Value, false, false)
			if err != nil {
				return err
			}
			c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
			if c.isGlobalScope() {
				nameIdx := c.constants.AddConstant(object.NewString(d.Name.Value))
				c.emitter.Emit(bytecode.OP_DECLARE, nameIdx)
			} else {
				c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
			}
		}
	}
	return nil
}

// declareFuncLayerVar 在**函数作用域层**登记一个 var 绑定 (var 语义)。
//
//	- 块里声明的 var 在块外可见 (绑定在函数层);
//	- 重复声明合法 (函数层已有同名 → 复用绑定);
//	- 提升 (compileStatements 的 emitVarHoistInits 已在函数层发射 undefined)。
//
// var/var、var/函数声明、var/参数 → 复用绑定; let/const 与 var 同层同名 →
// SyntaxError。解构 var 的目标名一律走这里 (见 bindKind/bindPatternTarget),
// 而不是 let 的块级 declareOnce —— 这是 r4McL4 的核心修复点。

func (c *Compiler) compileVarStatement(stmt *ast.VarStatement) error {
	// 解构首项: var [a, b] = arr / var { x } = obj。
	// 首项是解构时不走下面的普通绑定登记, 但**不能**直接 return —— 逗号续接项
	// (stmt.More) 还没编译, 如 `var [p] = [1], [q] = [2];` / `var [p] = [1], x = 2;`。
	firstIsDestructure := stmt.Name.Value == destructureSyntheticName

	declareVar := c.declareFuncLayerVar
	emitAssign := c.emitVarAssign

	if firstIsDestructure {
		assign, ok := stmt.Value.(*ast.AssignmentExpression)
		if !ok {
			return fmt.Errorf("compiler: malformed destructuring declaration")
		}
		if err := c.compileDestructureAssignment(assign, bindVar); err != nil {
			return err
		}
	} else if stmt.Value != nil {
		if err := c.compileNamedExpression(stmt.Value, stmt.Name.Value); err != nil {
			return err
		}
		sym, err := declareVar(stmt.Name.Value)
		if err != nil {
			return err
		}
		emitAssign(stmt.Name.Value, sym)
	}
	for _, d := range stmt.More {
		if d.Name != nil && d.Name.Value == destructureSyntheticName {
			// 解构声明项 (var [p] = [1], [q] = [2]): 与首项解构路径同构。
			assign, ok := d.Value.(*ast.AssignmentExpression)
			if !ok {
				return fmt.Errorf("compiler: malformed destructuring declarator")
			}
			if err := c.compileDestructureAssignment(assign, bindVar); err != nil {
				return err
			}
			continue
		}
		if d.Value != nil {
			if err := c.compileNamedExpression(d.Value, d.Name.Value); err != nil {
				return err
			}
		} else {
			c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
		}
		sym, err := declareVar(d.Name.Value)
		if err != nil {
			return err
		}
		emitAssign(d.Name.Value, sym)
	}
	return nil
}

func (c *Compiler) compileConstStatement(stmt *ast.ConstStatement) error {
	// 解构赋值: const [a, b] = arr  或  const { x, y } = obj。
	// 首项是解构时不走下面的普通绑定登记, 但**不能**直接 return —— 逗号续接项
	// (stmt.More) 还没编译, 如 `const [a] = [1], [b] = [2];`。
	if stmt.Name.Value == destructureSyntheticName {
		assign, ok := stmt.Value.(*ast.AssignmentExpression)
		if !ok {
			return fmt.Errorf("compiler: malformed destructuring declaration")
		}
		if err := c.compileDestructureAssignment(assign, bindLexical); err != nil {
			return err
		}
	} else {
		if err := c.compileNamedExpression(stmt.Value, stmt.Name.Value); err != nil {
			return err
		}
		sym, err := c.declareOnce(stmt.Name.Value, true, false)
		if err != nil {
			return err
		}
		if c.isGlobalScope() {
			// 全局作用域: 声明写入共享全局环境 (const 绑定)
			nameIdx := c.constants.AddConstant(object.NewString(stmt.Name.Value))
			c.emitter.Emit(bytecode.OP_DECLARE_CONST, nameIdx)
		} else {
			c.emitter.Emit(bytecode.OP_STORE_CONST, uint16(sym.Slot))
		}
	}

	// 多条声明: const a = 1, b = 2; / const [a, b] = x, {c} = y;
	for _, d := range stmt.More {
		if d.Name != nil && d.Name.Value == destructureSyntheticName {
			// 解构声明项: 与首项解构路径同构 (全部声明项都是绑定名, 不可重复)。
			assign, ok := d.Value.(*ast.AssignmentExpression)
			if !ok {
				return fmt.Errorf("compiler: malformed destructuring declarator")
			}
			if err := c.compileDestructureAssignment(assign, bindLexical); err != nil {
				return err
			}
			continue
		}
		if err := c.compileNamedExpression(d.Value, d.Name.Value); err != nil {
			return err
		}
		sym, err := c.declareOnce(d.Name.Value, true, false)
		if err != nil {
			return err
		}
		if c.isGlobalScope() {
			nameIdx := c.constants.AddConstant(object.NewString(d.Name.Value))
			c.emitter.Emit(bytecode.OP_DECLARE_CONST, nameIdx)
		} else {
			c.emitter.Emit(bytecode.OP_STORE_CONST, uint16(sym.Slot))
		}
	}
	return nil
}

// ==================== using / await using (ES2023) ====================

// collectUsingDecls 收集一个语句列表**本层**的 using 声明 (不进嵌套块/函数)。
// 嵌套块的 using 由该块自己的 compileBlockStatement 处理。

func (c *Compiler) compileLoopBody(stmts []ast.Statement) error {
	return c.withDisposeScope(stmts, func() error {
		for _, s := range stmts {
			if err := c.compileStatement(s); err != nil {
				return err
			}
		}
		return nil
	})
}

// compileUsingStatement 编译 using / await using 声明 (非 for-of 头形态)。
//
// 每个绑定项: 求值初始化器 → 复制一份 → 存 const 绑定 (using 绑定不可赋值,
// 赋值在运行期抛 TypeError) → OP_DISPOSE_ADD 登记资源。登记时初始化器为
// null/undefined 则跳过; 非对象或缺失释放方法抛 TypeError (运行时判定)。

func (c *Compiler) compileUsingStatement(stmt *ast.UsingStatement) error {
	res := c.disposeResources[stmt]
	if len(res) == 0 {
		return fmt.Errorf("compiler: using declaration outside a dispose scope")
	}
	i := 0
	compileOne := func(name *ast.Identifier, value ast.Expression) error {
		if name == nil || i >= len(res) {
			return fmt.Errorf("compiler: using resource slot mismatch")
		}
		r := res[i]
		i++
		if err := c.compileNamedExpression(value, name.Value); err != nil {
			return err
		}
		c.emitter.EmitNoOperand(bytecode.OP_DUP) // 留一份值给 OP_DISPOSE_ADD
		sym, err := c.declareOnce(name.Value, true, false)
		if err != nil {
			return err
		}
		if c.isGlobalScope() {
			nameIdx := c.constants.AddConstant(object.NewString(name.Value))
			c.emitter.Emit(bytecode.OP_DECLARE_CONST, nameIdx)
		} else {
			c.emitter.Emit(bytecode.OP_STORE_CONST, uint16(sym.Slot))
		}
		ridx := c.constants.AddConstant(r)
		c.emitter.Emit(bytecode.OP_DISPOSE_ADD, ridx)
		return nil
	}
	if err := compileOne(stmt.Name, stmt.Value); err != nil {
		return err
	}
	for _, d := range stmt.More {
		if err := compileOne(d.Name, d.Value); err != nil {
			return err
		}
	}
	return nil
}

// allocFinallyRetSlot 惰性分配 (并全函数复用) 「穿 finally 的 return 值」暂存槽。
//
// 槽号取当前作用域链上所有层 nextSlot 的最大值 —— 活跃变量都落在各自作用域的
// nextSlot 之下, 因此该号必然空闲 (已结束的兄弟块复用同一批槽位不冲突: 它们的
// 变量早已出作用域)。分配后把链上每层的 nextSlot 都抬到它之上, 否则之后在同一
// 链上新建的作用域会再次发出同一个槽号。

func (c *Compiler) compileReturnStatement(stmt *ast.ReturnStatement) error {
	hasValue := stmt.ReturnValue != nil
	if hasValue {
		if err := c.compileExpression(stmt.ReturnValue); err != nil {
			return err
		}
	}
	// 不在任何 try 内: 直接返回 (原路径)。
	if len(c.tryScopes) == 0 {
		if hasValue {
			c.emitter.EmitNoOperand(bytecode.OP_RETURN)
		} else {
			c.emitter.EmitNoOperand(bytecode.OP_RETURN_VOID)
		}
		return nil
	}
	// 穿 try 体返回: 值先挪进隐藏槽, 再内联跑完所有 finally, 最后才真返回。
	// 值经槽位而非求值栈传递 —— finally 体里的 return/break/continue 会把
	// 栈上的残值带到不相干的位置。
	if hasValue {
		c.emitter.Emit(bytecode.OP_STORE, uint16(c.allocFinallyRetSlot()))
	}
	if err := c.emitTryUnwind(0); err != nil {
		return err
	}
	if hasValue {
		c.emitter.Emit(bytecode.OP_LOAD, uint16(c.allocFinallyRetSlot()))
		c.emitter.EmitNoOperand(bytecode.OP_RETURN)
	} else {
		c.emitter.EmitNoOperand(bytecode.OP_RETURN_VOID)
	}
	return nil
}

func (c *Compiler) compileBlockStatement(block *ast.BlockStatement) error {
	c.emitter.EmitNoOperand(bytecode.OP_PUSH_SCOPE)
	prevScope := c.scope
	c.scope = NewSymbolScope(prevScope)

	err := c.withDisposeScope(block.Statements, func() error {
		return c.compileStatements(block.Statements)
	})

	c.scope = prevScope
	c.emitter.EmitNoOperand(bytecode.OP_POP_SCOPE)
	return err
}

// ==================== with 语句 (对象环境记录) ====================

// withAffects 报告: 解析到 sym 的标识符是否必须走 with 动态查找。
//
// 规则 (规范 13.11.7): with 的对象环境记录插在「with 语句所在环境」与
// 「语句体声明」之间。因此深度 ≤ 最内层 with.depth 的绑定 (含全局/未绑定,
// 视作 depth -1) 会被 with 对象遮蔽, 必须运行时查找; 深度更大的绑定
// (语句体内 let/const/参数等) 在对象环境之内, 照常走编译期槽位。

func (c *Compiler) compileWithStatement(stmt *ast.WithStatement) error {
	// 对象表达式在外层环境求值 (不受本 with 影响)。
	if err := c.compileExpression(stmt.Object); err != nil {
		return err
	}
	// ToObject: 规范 14.11.2 在绑定对象环境记录前对值做 ToObject,
	// null/undefined 在此抛 TypeError (即使 with 体从不执行)。
	c.emitter.EmitNoOperand(bytecode.OP_WITH_ENTER)
	// 合成槽位: 名字含空格, 用户标识符不可能与之冲突。
	c.withSlotSeq++
	sym := c.scope.Define(fmt.Sprintf(" with#%d", c.withSlotSeq), false)
	c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))

	c.withScopes = append(c.withScopes, withScope{slot: sym.Slot, depth: c.scope.Depth()})
	err := c.compileStatement(stmt.Body)
	c.withScopes = c.withScopes[:len(c.withScopes)-1]
	return err
}

//
// 规范 13.15.7 (CatchClauseEvaluation) 要求 catch 参数绑定在一个独立的
// declarative environment 中, catch 体嵌套其内。此前实现直接 Define 在
// 当前 scope, 有两个可观测缺陷:
//  1. 同名覆盖: 外层已有同名绑定时 Define 覆盖符号表条目, catch 体结束后
//     外层名字解析继续命中 catch 参数 → catch 内赋值写穿外层绑定;
//  2. 顶层写穿: 顶层 try/catch 走 emitGlobalStore 按名字写全局, 直接覆盖
//     全局词法绑定 (let/const)。
//
// 修复: catch 参数放进独立子 scope; 其槽位并入 parent 的 nextSlot ——
// 保证帧 numLocals 覆盖 catch 参数槽, 且后续声明不复用该槽。

func (c *Compiler) compileCatchBodyWithParam(param ast.Expression, body *ast.BlockStatement) error {
	c.emitter.EmitNoOperand(bytecode.OP_PUSH_SCOPE)
	prevScope := c.scope
	c.scope = NewSymbolScope(prevScope)
	// catch 绑定可以是解构模式 (catch ([a, b]) / catch ({x}));
	// 被抛值已在栈顶, compilePatternBind 消费它并声明各绑定。
	if id, ok := param.(*ast.Identifier); ok {
		sym := c.scope.Define(id.Value, false)
		c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
	} else if err := c.compilePatternBind(param, bindLexical); err != nil {
		return err
	}
	// catch 作用域内新分配的槽 (含解构隐藏槽抬高的函数层 nextSlot) 要回灌,
	// 取 max 避免把 allocHiddenSlot 抬高过的 nextSlot 又降回去 (槽复用)。
	if c.scope.nextSlot > prevScope.nextSlot {
		prevScope.nextSlot = c.scope.nextSlot
	}

	if err := c.compileBlockStatement(body); err != nil {
		return err
	}

	c.scope = prevScope
	c.emitter.EmitNoOperand(bytecode.OP_POP_SCOPE)
	return nil
}

func (c *Compiler) compileIfStatement(stmt *ast.IfStatement) error {
	// 编译条件
	if err := c.compileExpression(stmt.Condition); err != nil {
		return err
	}
	// 条件为假跳过 consequence
	jumpFalse := c.emitter.EmitJump(bytecode.OP_JUMP_IF_FALSE)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出条件值

	// consequence
	if err := c.compileBlockStatement(stmt.Consequence); err != nil {
		return err
	}

	if stmt.Alternative != nil {
		// 跳过 alternative
		jumpEnd := c.emitter.EmitJump(bytecode.OP_JUMP)
		// alternative: 弹出条件值
		c.emitter.PatchJump(jumpFalse)
		c.emitter.EmitNoOperand(bytecode.OP_POP)
		if err := c.compileBlockStatement(stmt.Alternative); err != nil {
			return err
		}
		c.emitter.PatchJump(jumpEnd)
	} else {
		// 无 else: 跳过 false path 的 POP
		jumpEnd := c.emitter.EmitJump(bytecode.OP_JUMP)
		c.emitter.PatchJump(jumpFalse)
		c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出条件值
		c.emitter.PatchJump(jumpEnd)
	}
	return nil
}

func (c *Compiler) compileWhileStatement(stmt *ast.WhileStatement) error {
	loopStart := c.emitter.Pos()

	// 编译条件
	if err := c.compileExpression(stmt.Condition); err != nil {
		return err
	}
	jumpFalse := c.emitter.EmitJump(bytecode.OP_JUMP_IF_FALSE)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出条件值

	// 循环体 (需要块作用域但不在 compileBlockStatement 中，因为有 break/continue 管理)
	c.emitter.EmitNoOperand(bytecode.OP_PUSH_SCOPE)
	prevScope := c.scope
	c.scope = NewSymbolScope(prevScope)
	// 迭代边界只克隆块内新分配的槽位 (body 的 let/const)。函数层 var 与外层
	// let 保持单实例共享 (见 for-of 处详解)。
	sealFrom := c.scope.NumLocals()

	ctx := c.pushControl(c.takePendingLabel(), true)

	if err := c.compileLoopBody(stmt.Body.Statements); err != nil {
		return err
	}

	c.scope = prevScope
	c.emitter.EmitNoOperand(bytecode.OP_POP_SCOPE)

	// 迭代边界: 提交本轮创建的闭包 (body 内 let 的 per-iteration 语义)
	c.emitter.Emit(bytecode.OP_ITER_BOUNDARY, uint16(sealFrom))
	iterPos := c.emitter.Pos()
	// continue 跳回循环头
	for _, jmp := range ctx.continueJumps {
		c.emitter.ReplaceJumpTarget(jmp, uint16(iterPos))
	}
	// 跳回条件检查
	c.emitter.Emit(bytecode.OP_LOOP, uint16(loopStart))

	// 循环结束: 弹出条件值
	c.emitter.PatchJump(jumpFalse)
	c.emitter.EmitNoOperand(bytecode.OP_POP)

	// break 跳到这里
	for _, jmp := range ctx.breakJumps {
		c.emitter.PatchJump(jmp)
	}

	c.popControl()
	return nil
}

func (c *Compiler) compileDoWhileStatement(stmt *ast.DoWhileStatement) error {
	loopStart := c.emitter.Pos()

	// 块作用域
	c.emitter.EmitNoOperand(bytecode.OP_PUSH_SCOPE)
	prevScope := c.scope
	c.scope = NewSymbolScope(prevScope)
	// 迭代边界只克隆块内新分配的槽位 (body 的 let/const); 函数层 var 与外层
	// let 保持单实例共享 (见 for-of 处详解)。
	sealFrom := c.scope.NumLocals()

	ctx := c.pushControl(c.takePendingLabel(), true)

	// 先执行循环体
	if err := c.compileLoopBody(stmt.Body.Statements); err != nil {
		return err
	}

	c.scope = prevScope
	c.emitter.EmitNoOperand(bytecode.OP_POP_SCOPE)

	// continue 跳到条件检查 (body 之后); 此处同时是迭代边界
	c.emitter.Emit(bytecode.OP_ITER_BOUNDARY, uint16(sealFrom))
	condPos := c.emitter.Pos()
	for _, jmp := range ctx.continueJumps {
		c.emitter.ReplaceJumpTarget(jmp, uint16(condPos))
	}

	// 编译条件
	if err := c.compileExpression(stmt.Condition); err != nil {
		return err
	}
	// 条件为假则退出; 为真则弹出条件值后跳回循环体
	exitJump := c.emitter.EmitJump(bytecode.OP_JUMP_IF_FALSE)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出条件值 (真值路径)
	c.emitter.Emit(bytecode.OP_LOOP, uint16(loopStart))

	// 条件为假 / break 跳到这里 (退出循环)
	c.emitter.PatchJump(exitJump)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出条件值 (假值路径)
	for _, jmp := range ctx.breakJumps {
		c.emitter.PatchJump(jmp)
	}

	c.popControl()
	return nil
}

// compileForOfStatement 编译同步 for-of 语句 for (binding of iterable) { body }。
//
// 迭代与收尾按规范 ForIn/OfBodyEvaluation + IteratorClose (7.4.6) 实现:
//   - 取一次迭代器, 存进隐藏局部槽 closeSlot (循环头每轮 LOAD, body 阶段
//     求值栈完全干净)。
//   - 每步用 OP_ITER_STEP 取 {value, done}, **按 .done 判结束**。早先的实现
//     用 OP_ITER_NEXT 并以「值 === undefined/null」当结束信号, 会把迭代器
//     正常产出的 null/undefined 误判成结束 (test262 iterator-close-via-* 用
//     value: null, 会整段不进入循环体), 已一并纠正。
//   - break / return / body 抛异常穿出循环体时必须 IteratorClose (调
//     iterator.return(), 没有该方法则静默跳过)。挂载机制完全复用 for-await
//     的 try/finally 基础设施 (rMkA8D): 每轮绑定+body 前面 PUSH_TRY 0 +
//     PUSH_FINALLY closePC; body 抛异常由 handleThrowInner 走 finallyPC 跳到
//     closePC → close 序列 → END_FINALLY 重抛原异常。编译器侧把 close 条目
//     (closeSlot + closeSync) 压进 tryScopes, body 里的 break/return 由
//     emitTryUnwind 自动内联 OP_ITER_CLOSE —— 标签 break、嵌套 for-of、穿
//     多层 try 全部由既有机制按由内向外顺序收尾。
//   - 异常优先级 (node 实测, 与 for-await 同口径): body 抛异常时 close 序列里
//     的异常被吞掉 (原异常胜出, closePC 处套一层 catch 丢弃); break/return
//     路径上 close 的异常照常传播。continue 与 done 正常出口不调 return()。
//   - GetIterator 自身抛错时**没有迭代器**, 不进入任何 close 路径 (handler 在
//     取到迭代器之后才压入)。

func (c *Compiler) compileThrowStatement(stmt *ast.ThrowStatement) error {
	if err := c.compileExpression(stmt.Value); err != nil {
		return err
	}
	c.emitter.EmitNoOperand(bytecode.OP_THROW)
	return nil
}

func (c *Compiler) compileTryStatement(stmt *ast.TryStatement) error {
	// 无 catch 无 finally: 不需要 try handler，直接编译 body。
	if stmt.CatchBody == nil && stmt.FinallyBody == nil {
		return c.compileBlockStatement(stmt.Body)
	}

	// 目标字节码形状 (三条路径汇入同一个 finally 块):
	//
	//   PUSH_TRY <catchPC|0>      ; 异常时 handleThrowInner 按 catch/finally 分发
	//   PUSH_FINALLY <finallyPC>  ; (仅当有 finally)
	//   <try body>
	//   POP_TRY                   ; try 正常完成, 摘掉处理器
	//   JUMP tryStateNormal       ; ── 正常路径跳过 catch ──
	// catchPC:                      ; 异常落点, 栈顶是错误值
	//   [PUSH_FINALLY <finallyPC>] ; 有 finally 时给 catch 体再挂一层 finally 保护
	//   <catch body>              ; (catch 参数从栈顶取; 无参数则 POP)
	//   POP_TRY
	//   JUMP afterCatch           ; ── catch 正常完成 ──
	// tryStateNormal:              ; try / catch 两条正常路径汇合
	//   (无 finally 时到这就结束了, PatchJump(skipCatch) 落在这)
	// finallyPC:                   ; 异常路径进入 finally: 挂起值已由
	//                              ; handleThrowInner 记在 tryStack 条目上
	//                              ; (tryEntry.inFinally/pendingVal), 不往栈上放错误值
	// afterCatch:
	//   <finally body>
	//   END_FINALLY               ; 栈顶是本帧的 inFinally 条目则重抛它, 否则正常继续
	//
	// 历史缺陷 (riUpgO, 2026-10-03 修): 旧实现给「无 catch 但有 finally」的形状
	// 伪造了一个只 POP 错误值的假 catch 处理器 ⇒ 异常被丢弃; 且正常路径的
	// skipCatch 跳到整个语句末尾, 直接跳过 finally 块 ⇒ finally 在无异常时
	// 从不执行。VM 侧 handleThrowInner 的 finallyPC 分支(标记条目 inFinally →
	// 跳 finally → END_FINALLY 重抛)是完备的, 只是编译器从未让它走通。
	//
	// 控制转移 (rMkA8D, 2026-10-04 修): return / break / continue 跳出 try 体时
	// 不经过上面任何一条路径 —— 编译器在转移点现场内联一份 finally 体
	// (见 emitTryUnwind), 逐层发 POP_TRY 后紧跟真正的转移指令。

	hasCatch := stmt.CatchBody != nil
	hasFinally := stmt.FinallyBody != nil

	// PUSH_TRY: catch 存在时 catchPC 指向 catch 体, 否则 0 (纯 finally 形状,
	// 让 handleThrowInner 走它的 finallyPC 分支)。
	var tryJump int
	if hasCatch {
		tryJump = c.emitter.EmitJump(bytecode.OP_PUSH_TRY)
	} else {
		pos := c.emitter.Emit(bytecode.OP_PUSH_TRY, 0)
		_ = pos
	}

	// PUSH_FINALLY 占位 (回填到 finally 体入口)。
	var finallyJump int
	if hasFinally {
		finallyJump = c.emitter.EmitJump(bytecode.OP_PUSH_FINALLY)
	}

	// try body —— 编译器视角同步压入本 try 的处理器条目, 让 try 体里的
	// return/break/continue 知道要收尾几层 finally (rMkA8D)。
	savedTryLen := len(c.tryScopes)
	c.tryScopes = append(c.tryScopes, tryScope{hasFinally: hasFinally, finallyBody: stmt.FinallyBody, closeSlot: -1})
	bodyErr := c.compileBlockStatement(stmt.Body)
	if len(c.tryScopes) > savedTryLen {
		c.tryScopes = c.tryScopes[:savedTryLen]
	}
	if bodyErr != nil {
		return bodyErr
	}
	c.emitter.EmitNoOperand(bytecode.OP_POP_TRY)
	skipCatch := c.emitter.EmitJump(bytecode.OP_JUMP)

	// === catch 体 (异常落点, 栈顶 = 错误值) ===
	if hasCatch {
		c.emitter.PatchJump(tryJump)

		var catchFinally int
		if hasFinally {
			// catch 体自身再挂一层 finally 保护: catch 里再 throw 也要经过 finally。
			// PUSH_FINALLY 只改栈顶条目的 finallyPC, 不建条目 —— 外层 try 条目
			// 已在进入 catch 时被 handleThrow 消费, 必须先 PUSH_TRY 0 立个纯
			// finally 条目 (catchPC=0), 否则这个 PUSH_FINALLY 会把 finallyPC
			// 错写到更外层的条目上 (riUpgO)。
			c.emitter.Emit(bytecode.OP_PUSH_TRY, 0)
			catchFinally = c.emitter.EmitJump(bytecode.OP_PUSH_FINALLY)
			// 编译期镜像同步压栈: catch 体里的 return/break/continue 同样要
			// 先收尾这层 finally 保护条目 (rMkA8D)。
			c.tryScopes = append(c.tryScopes, tryScope{hasFinally: true, finallyBody: stmt.FinallyBody, closeSlot: -1})
		}

		var catchErr error
		if stmt.CatchParam != nil {
			catchErr = c.compileCatchBodyWithParam(stmt.CatchParam, stmt.CatchBody)
		} else {
			c.emitter.EmitNoOperand(bytecode.OP_POP)
			catchErr = c.compileBlockStatement(stmt.CatchBody)
		}
		if hasFinally && len(c.tryScopes) > 0 {
			c.tryScopes = c.tryScopes[:len(c.tryScopes)-1]
		}
		if catchErr != nil {
			return catchErr
		}
		// 摘掉 catch 体开头那层 finally 保护条目 —— 只在 hasFinally 时才有。
		//
		// 绝不能无条件发 POP_TRY: 无 finally 时 catch 体开头**没有**压入保护条目
		// (上面 `if hasFinally` 才发 PUSH_TRY 0), 而进入 catch 时原始 try 条目
		// 已被 handleThrowInner 弹出 ⇒ 无条件的 POP_TRY 会弹掉更外层 try 的条目,
		// 让外层 finally 整段被跳过 (probe4: `try{ try{}catch(){} throw x }finally{}`
		// 的外层 finally 不执行)。
		if hasFinally {
			c.emitter.EmitNoOperand(bytecode.OP_POP_TRY)
		}
		skipFinally := c.emitter.EmitJump(bytecode.OP_JUMP)

		if hasFinally {
			// catch 的 finally 保护条目指向同一个 finally 体 (finally 只编译一份)。
			c.emitter.PatchJump(catchFinally)
		}
		c.emitter.PatchJump(skipFinally)
	}

	// === tryStateNormal: try/catch 的正常路径汇合点 ===
	// 无 finally 时它就是语句出口 (旧实现的 skipCatch 曾跳过 finally —— 缺陷点)。
	c.emitter.PatchJump(skipCatch)

	// === finally 体 (共享一份, 三条路径都到这) ===
	if hasFinally {
		c.emitter.PatchJump(finallyJump) // try 侧 PUSH_FINALLY 的目标
		if err := c.compileBlockStatement(stmt.FinallyBody); err != nil {
			return err
		}
		c.emitter.EmitNoOperand(bytecode.OP_END_FINALLY)
	}

	return nil
}

func (c *Compiler) compileSwitchStatement(stmt *ast.SwitchStatement) error {
	// 编译判别表达式
	if err := c.compileExpression(stmt.Discriminant); err != nil {
		return err
	}

	// 收集 case 跳转和 break 跳转
	var caseJumps []int // JUMP_IF_TRUE_POP 的位置
	var defaultJump int // 跳到 default 的位置

	// switch 本身是 break 目标 (case 体内的 break 作用于 switch, 而非外层循环)
	ctx := c.pushControl(c.takePendingLabel(), false)

	hasDefault := false
	defaultIdx := -1

	// 第一遍: 为每个 case 生成比较代码。
	// 栈约定: 比较阶段栈=[disc]; 匹配经 JUMP_IF_TRUE_POP 弹掉比较结果后跳入
	// case 体 (栈仍=[disc]); 不匹配落地的 POP 弹掉比较结果 (栈=[disc])。
	// case 体入口不再放 POP —— 此前的"体入口 POP"在 fall-through (连续空壳
	// case 或穿透 case) 顺序执行时会被逐个多弹, 打穿调用方栈 (T04 panic 根因之二)。
	for i, sc := range stmt.Cases {
		if sc.Test == nil {
			// default case: 无比较代码
			hasDefault = true
			defaultIdx = i
			continue
		}
		// DUP 判别值, 编译测试值, ===
		c.emitter.EmitNoOperand(bytecode.OP_DUP)
		if err := c.compileExpression(sc.Test); err != nil {
			return err
		}
		c.emitter.EmitNoOperand(bytecode.OP_STRICT_EQ)
		// 匹配: 弹出比较结果并跳到 case 体
		jump := c.emitter.EmitJump(bytecode.OP_JUMP_IF_TRUE_POP)
		c.emitter.EmitNoOperand(bytecode.OP_POP) // 不匹配: 弹出比较结果
		caseJumps = append(caseJumps, jump)
	}

	// 无匹配: 跳到 default 体 (栈=[disc], 与 case 体入口一致) 或末尾
	defaultJump = c.emitter.EmitJump(bytecode.OP_JUMP)

	// 第二遍: 编译每个 case 体
	caseBodyStart := make([]int, len(stmt.Cases))
	caseJumpIdx := 0
	for i, sc := range stmt.Cases {
		caseBodyStart[i] = c.emitter.Pos()

		if sc.Test != nil {
			// 回填匹配跳转到这里 (体首条指令)
			c.emitter.PatchJump(caseJumps[caseJumpIdx])
			caseJumpIdx++
		}

		// 编译 case 体语句
		for _, s := range sc.Statements {
			if err := c.compileStatement(s); err != nil {
				return err
			}
		}
	}

	// switch 末尾: 统一弹出判别值。
	// 到达此处的路径 (break / fall-through 落空 / 无 default) 栈上都剩 [disc];
	// 有 default 时无匹配路径直接进 default 体 (同样持有 [disc])。
	discPopPos := c.emitter.Pos()
	c.emitter.EmitNoOperand(bytecode.OP_POP)

	if hasDefault {
		c.emitter.ReplaceJumpTarget(defaultJump, uint16(caseBodyStart[defaultIdx]))
	} else {
		c.emitter.ReplaceJumpTarget(defaultJump, uint16(discPopPos))
	}

	// break 跳转: 也落到末尾 POP 处 (break 时栈=[disc], 统一弹出)
	for _, jmp := range ctx.breakJumps {
		c.emitter.ReplaceJumpTarget(jmp, uint16(discPopPos))
	}
	c.popControl()

	return nil
}

// ===== break / continue / 标签语句 =====

func (c *Compiler) compileBreakStatement(stmt *ast.BreakStatement) error {
	label := ""
	if stmt.Label != nil {
		label = stmt.Label.Value
	}
	ctx := c.lookupControl(label, false)
	if ctx == nil {
		return fmt.Errorf("Uncaught SyntaxError: Illegal break statement")
	}
	// 目标在若干层 try 之外时, 先把这些 try 的 finally 跑掉再跳 (rMkA8D)。
	if err := c.emitTryUnwind(ctx.tryScopes); err != nil {
		return err
	}
	ctx.breakJumps = append(ctx.breakJumps, c.emitter.EmitJump(bytecode.OP_JUMP))
	return nil
}

func (c *Compiler) compileContinueStatement(stmt *ast.ContinueStatement) error {
	label := ""
	if stmt.Label != nil {
		label = stmt.Label.Value
	}
	ctx := c.lookupControl(label, true)
	if ctx == nil {
		return fmt.Errorf("Uncaught SyntaxError: Undefined label '%s'", label)
	}
	// continue 只能作用于循环
	if !ctx.isLoop {
		return fmt.Errorf("Uncaught SyntaxError: Illegal continue statement: 'continue' must be inside a loop")
	}
	// 目标在若干层 try 之外时, 先把这些 try 的 finally 跑掉再跳 (rMkA8D)。
	// selfClose: continue 回到本循环不算穿出它, 自己的 close 条目不解退
	// (留在 tryStack 上服务下一轮, 由循环底的 POP_TRY 摘掉)。
	if err := c.emitTryUnwind(ctx.tryScopes + ctx.selfClose); err != nil {
		return err
	}
	ctx.continueJumps = append(ctx.continueJumps, c.emitter.EmitJump(bytecode.OP_LOOP))
	return nil
}

func (c *Compiler) compileLabeledStatement(stmt *ast.LabeledStatement) error {
	// 早错: 同一语句链上重复的标签 ⇒ SyntaxError (sec-labelled-statements-
	// static-semantics-early-errors, ContainsDuplicateLabels)。此前不拦会
	// 一路跑到运行期 (test262 module-code/early-dup-lables.js)。
	if c.hasActiveLabel(stmt.Label.Value) {
		return fmt.Errorf("SyntaxError: Label '%s' has already been declared", stmt.Label.Value)
	}
	// 若 body 是循环, 设置 pendingLabel 让循环编译函数把标签绑定到循环 context。
	// 这样 continue outer 可以跳回外层循环头部, 而 break outer 跳出外层循环。
	if isLoopStatement(stmt.Body) {
		c.pendingLabel = stmt.Label.Value
		err := c.compileStatement(stmt.Body)
		c.pendingLabel = ""
		return err
	}

	// 非循环 body: 用独立 context 管理 break。
	// 块作用域由 block 编译处理。
	if _, ok := stmt.Body.(*ast.BlockStatement); ok {
		ctx := c.pushControl(stmt.Label.Value, false)
		err := c.compileStatement(stmt.Body)
		// break 跳到这里 (标签块的末尾)
		for _, jmp := range ctx.breakJumps {
			c.emitter.PatchJump(jmp)
		}
		c.popControl()
		return err
	}

	// 非块非循环语句: 编译 body 即可 (标签本身无控制流意义, 但保留结构)
	return c.compileStatement(stmt.Body)
}

// hasActiveLabel 报告 label 是否已在当前语句链上激活 (pendingLabel 或任一
// 活跃控制上下文)。用于重复标签早错。

func (c *Compiler) hasActiveLabel(label string) bool {
	if c.pendingLabel == label {
		return true
	}
	for _, ctx := range c.controlStack {
		if ctx.label == label {
			return true
		}
	}
	return false
}

// isLoopStatement 判断语句是否为循环语句。

func isLoopStatement(s ast.Statement) bool {
	switch s.(type) {
	case *ast.WhileStatement, *ast.DoWhileStatement, *ast.ForStatement, *ast.ForInStatement, *ast.ForOfStatement:
		return true
	}
	return false
}

// ===== Class 编译 =====

// compileClassDeclaration 编译 class 声明。
// 指令序列:
//  1. 编译 constructor 函数 → [ctor]
//  2. 创建 prototype 对象 → [ctor, proto]
//  3. 实例方法挂到 proto
//  4. extends: proto.Proto = SuperClass.prototype
//  5. ctor.prototype = proto
//  6. 静态方法挂到 ctor
//  7. 声明类名 (全局变量)

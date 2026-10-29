package compiler

import (
	"fmt"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/object"
)

func (c *Compiler) compileParamBinding(params []*ast.Parameter, paramSlots []int) error {
	// 预收集每个形参的绑定名, 供默认值求值时的 TDZ 判定 (见 paramTDZNames)。
	paramNames := make([][]string, len(params))
	for i, param := range params {
		paramNames[i] = parameterBoundNames(param)
	}
	fnScope := c.scope.FuncLayer()

	for i, param := range params {
		if param.Default == nil {
			continue
		}
		slot := paramSlots[i]
		c.emitter.Emit(bytecode.OP_LOAD, uint16(slot))
		// 简单参数 (无解构模式) 的默认值是匿名函数/类定义时按其参数名命名;
		// 解构模式参数无单一名, 不命名。
		defName := ""
		if param.Pattern == nil {
			defName = param.Name
		}
		// TDZ: 求值第 i 个形参的默认值时, 本形参及之后 (i..n-1) 的所有绑定名
		// 仍未初始化, 引用即 ReferenceError (规范 9.2.12)。
		prevNames, prevScope := c.paramTDZNames, c.paramTDZScope
		c.paramTDZNames = collectNamesFrom(paramNames, i)
		c.paramTDZScope = fnScope
		err := c.compileDestructureDefaultNamed(param.Default, defName)
		c.paramTDZNames, c.paramTDZScope = prevNames, prevScope
		if err != nil {
			return err
		}
		c.emitter.Emit(bytecode.OP_STORE, uint16(slot))
	}
	for i, param := range params {
		if param.Pattern == nil {
			continue
		}
		slot := paramSlots[i]
		c.emitter.Emit(bytecode.OP_LOAD, uint16(slot))
		if err := c.compilePatternBind(param.Pattern, bindLexical); err != nil {
			return err
		}
	}
	return nil
}

// parameterBoundNames 返回单个形参引入的所有绑定名 (简单参数即其名;
// 解构模式递归收集嵌套目标名; rest 同样按目标收集)。仅用于 TDZ 判定。

func parameterBoundNames(param *ast.Parameter) []string {
	if param == nil {
		return nil
	}
	if param.Pattern != nil {
		var out []string
		collectBindingNames(param.Pattern, &out)
		return out
	}
	if param.Name != "" {
		return []string{param.Name}
	}
	return nil
}

// collectBindingNames 递归收集解构模式里的所有绑定名 (数组/对象可嵌套)。
// 空洞 (Target==nil) 与默认值表达式跳过 —— 与 parser.collectPatternNames 同口径。

func collectBindingNames(expr ast.Expression, out *[]string) {
	switch n := expr.(type) {
	case *ast.Identifier:
		*out = append(*out, n.Value)
	case *ast.ArrayPattern:
		for _, e := range n.Elements {
			if e == nil || e.Target == nil {
				continue
			}
			collectBindingNames(e.Target, out)
		}
	case *ast.ObjectPattern:
		for _, pr := range n.Properties {
			if pr == nil || pr.Value == nil {
				continue
			}
			collectBindingNames(pr.Value, out)
		}
		if n.RestTarget != nil {
			collectBindingNames(n.RestTarget, out)
		}
	}
}

// collectNamesFrom 把 paramNames[start:] 里的名字并成一个集合, 供 TDZ 查询。

func collectNamesFrom(paramNames [][]string, start int) map[string]bool {
	set := map[string]bool{}
	for i := start; i < len(paramNames); i++ {
		for _, n := range paramNames[i] {
			set[n] = true
		}
	}
	return set
}

// compileClassMethodToObject 将 class 实例方法编译为函数并挂到栈顶下方的对象上。
// 栈: [..., obj] → 编译方法函数 → [..., obj, fn] → SET_PROP/SETTER → [..., obj]

func (c *Compiler) compileFunctionDeclaration(stmt *ast.FunctionDeclaration) error {
	// 先在当前作用域定义函数名 (允许递归调用)
	sym, err := c.declareOnce(stmt.Name.Value, false, true)
	if err != nil {
		return err
	}

	// 编译函数体为独立的 FunctionMetadata
	fnMeta, err := c.compileFunctionWithStrict(
		stmt.Strict,
		stmt.Name.Value,
		stmt.Parameters,
		stmt.Body,
		false,            // isArrow
		stmt.IsGenerator, // function*
		stmt.IsAsync,     // async function
	)
	if err != nil {
		return err
	}
	idx := c.constants.AddConstant(fnMeta)
	c.emitter.Emit(bytecode.OP_FUNCTION, idx)
	if c.isGlobalScope() {
		// 全局作用域: 函数名写入共享全局环境 (函数声明允许重定义)
		nameIdx := c.constants.AddConstant(object.NewString(stmt.Name.Value))
		c.emitter.Emit(bytecode.OP_DECLARE_FUNC, nameIdx)
	} else {
		c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
	}
	return nil
}

// ===== 表达式编译 =====

func (c *Compiler) compileFunction(name string, params []*ast.Parameter, body *ast.BlockStatement, isArrow, isGenerator, isAsync bool) (*bytecode.FunctionMetadata, error) {
	return c.compileFunctionSelf(name, "", params, body, isArrow, isGenerator, isAsync)
}

// compileFunctionWithStrict 在指定的 strict 上下文中编译函数体, 收尾后恢复
// 外层上下文。函数体的严格性由**节点**携带 (parser 已按继承 + 指令算好);
// 这里只负责把它落成 c.strict, 供编译期早错与 meta.IsStrict 盖章使用。

func (c *Compiler) compileFunctionWithStrict(strict bool, name string, params []*ast.Parameter, body *ast.BlockStatement, isArrow, isGenerator, isAsync bool) (*bytecode.FunctionMetadata, error) {
	prev := c.strict
	c.strict = strict
	meta, err := c.compileFunction(name, params, body, isArrow, isGenerator, isAsync)
	c.strict = prev
	return meta, err
}

// compileFunctionSelf 编译函数，可选绑定命名函数表达式的自引用。
// selfName 非空时 (仅函数表达式场景)，在函数作用域内定义 selfName 指向
// 函数自身 (ES 规范 NamedFunctionExpression 作用域)，VM 调用时把闭包
// 写入对应槽位。参数与 selfName 同名时参数优先 (规范行为)。

func (c *Compiler) compileFunctionSelf(name, selfName string, params []*ast.Parameter, body *ast.BlockStatement, isArrow, isGenerator, isAsync bool) (*bytecode.FunctionMetadata, error) {
	// 函数嵌套层数 +1: 顶层 await (TLA) 只在 fnDepth==0 判定, 任何函数
	// (含 async 的内层 generator 体) 都在 >=1 层。defer 覆盖所有返回路径,
	// 包括下方 async / async generator 的早退分支。
	c.fnDepth++
	defer func() { c.fnDepth-- }()

	// IsMethod 归位: 本函数是否"方法定义"由调用方 (方法编译点) 经
	// methodFnDepth 置位; 读取后清 0, 使函数体内嵌套的函数声明/表达式
	// 恢复默认 (非方法), 退出时恢复外层值 (方法嵌方法)。
	isMethod := c.methodFnDepth > 0
	prevMethodDepth := c.methodFnDepth
	c.methodFnDepth = 0
	defer func() { c.methodFnDepth = prevMethodDepth }()

	// 非箭头函数开辟独立的 arguments 作用域, 其内的 eval 不再受「类字段
	// 初始化器」规则约束 (规范: 该规则按直接 eval 的运行上下文判定, 嵌套
	// 普通函数的上下文不是初始化器)。箭头函数无独立 arguments 作用域,
	// 保持外层标记 —— 于是初始化器里箭头体内的直接 eval 仍受限。
	// defer 覆盖下方所有返回路径 (含 async/generator 的早退)。
	prevFieldInit := c.inClassFieldInit
	if !isArrow {
		c.inClassFieldInit = false
	}
	defer func() { c.inClassFieldInit = prevFieldInit }()

	// async generator: wrapper 创建并返回 AsyncGenerator 对象,
	// 内层 generator 的 await 编为 OP_AWAIT、yield 编为 OP_YIELD。
	if isAsync && isGenerator {
		return c.compileAsyncGeneratorSelf(name, selfName, params, body, isArrow, isMethod)
	}
	// async 函数: 编译为 wrapper (返回 __spawn(generator)), 内层 generator 处理 await→yield
	if isAsync {
		return c.compileAsyncFunctionSelf(name, selfName, params, body, isArrow, isMethod)
	}

	// 内层 generator 标记只影响本层参数放置, 入口立即消费 (见字段注释)。
	restPreCollected := c.innerGenRestPreCollected
	c.innerGenRestPreCollected = false

	// 创建新的作用域
	prevScope := c.scope
	baseSlot := prevScope.NumLocals() // 函数自身变量的起始槽位
	fnScope := NewFunctionScope(prevScope)
	c.scope = fnScope

	// 定义参数
	paramSpecs := make([]bytecode.ParameterSpec, len(params))
	paramSlots := make([]int, len(params))
	for i, param := range params {
		if param.Pattern != nil {
			// 解构模式参数: 分配隐藏槽位存原始参数值,
			// 函数入口处解构到各局部变量。
			// rest 解构参数 (...[a, b] / ...{a}): 槽位存 VM 收集好的剩余实参数组,
			// 再按模式拆开 (内层 generator 场景 wrapper 已收好, restPreCollected)。
			paramSpecs[i] = bytecode.ParameterSpec{
				Name:       fmt.Sprintf("__param_%d", i),
				HasDefault: param.Default != nil,
				IsRest:     param.Rest && !restPreCollected,
			}
			sym := fnScope.Define(paramSpecs[i].Name, false)
			paramSlots[i] = sym.Slot
			continue
		}
		paramSpecs[i] = bytecode.ParameterSpec{
			Name:       param.Name,
			HasDefault: param.Default != nil,
			IsRest:     param.Rest && !restPreCollected,
		}
		sym := fnScope.Define(param.Name, false)
		sym.Declared = true  // 参数是真实声明: 函数体内 let 同名 → SyntaxError
		sym.IsVarLike = true // 参数即 var 绑定: 函数体内 var 同名复用此绑定
		paramSlots[i] = sym.Slot
	}

	// 预留 arguments 槽位。
	// 简化: 所有函数 (含箭头) 都有独立 arguments 对象 (非严格 ES 语义, 箭头函数应继承外层)。
	argSym := fnScope.Define("__arguments__", false)
	argSym.Declared = true
	argumentsSlot := argSym.Slot
	// 记录进入函数前的 arguments 槽位, 便于恢复
	prevArgumentsSlot := c.currentArgumentsSlot
	c.currentArgumentsSlot = argumentsSlot

	// 命名函数表达式的自引用绑定: 名字在函数作用域内指向函数自身。
	// 参数已有同名绑定时不覆盖 (参数遮蔽函数名, 规范行为)。
	selfSlot := -1
	if selfName != "" && fnScope.ResolveLocal(selfName) == nil {
		sym := fnScope.Define(selfName, true)
		sym.Declared = true // 函数体内 let 同名 → SyntaxError (规范行为)
		selfSlot = sym.Slot
	}

	// 编译函数体
	prevEmitter := c.emitter
	c.emitter = NewEmitter()
	// 函数体的 srcPositions 是独立 offset 空间 (独立 emitter), 切换到新表;
	// 编译完存进 FunctionMetadata.Positions, 与外层表互不污染 (T05)。
	prevSrcPositions := c.srcPositions
	c.srcPositions = nil

	// 函数边界重置控制流: 标签/break/continue 不能跨函数。
	// try 条目镜像同理清空 —— 内层函数的控制转移绝不能收尾外层帧的 finally。
	prevControlStack := c.controlStack
	c.controlStack = nil
	prevPendingLabel := c.pendingLabel
	c.pendingLabel = ""
	prevTryScopes := c.tryScopes
	c.tryScopes = nil
	prevFinallyRetSlot := c.finallyRetSlot
	c.finallyRetSlot = -1
	// using 释放作用域同样不跨函数边界: 内层函数体里出现的 using 只属于它自己。
	prevDisposeResources := c.disposeResources
	c.disposeResources = nil
	// defer: 保证错误早退路径也恢复, 否则 compileTryStatement 的 [:len-1] 会 panic (rCzckg 回归)。
	defer func() {
		c.tryScopes = prevTryScopes
		c.finallyRetSlot = prevFinallyRetSlot
		c.disposeResources = prevDisposeResources
	}()

	// 参数默认值 + 解构模式绑定 (rest 收集由 VM 依 ParameterSpec.IsRest 完成)。
	if err := c.compileParamBinding(params, paramSlots); err != nil {
		return nil, err
	}

	// 形参前导段到此结束 (下一字节即函数体首指令)。生成器函数调用时
	// VM 会先同步执行 [0, paramPrologueEnd) 完成形参绑定, 再冻结为
	// Generator; 函数体仍推迟到首次 next()。无默认值/解构模式则无前导段。
	paramPrologueEnd := 0
	for _, param := range params {
		if param.Default != nil || param.Pattern != nil {
			paramPrologueEnd = c.emitter.Pos()
			break
		}
	}

	// 在函数体内不自动添加 PUSH_SCOPE/POP_SCOPE (函数本身已有作用域)
	// 函数体里的 using 声明: 在函数体退出 (含 return / 抛异常) 时逆序释放。
	if err := c.withDisposeScope(body.Statements, func() error {
		return c.compileStatements(body.Statements)
	}); err != nil {
		return nil, err
	}
	// 默认返回 undefined
	c.emitter.EmitNoOperand(bytecode.OP_RETURN_VOID)

	fnIns := c.emitter.Bytes()
	fnSrcPositions := c.srcPositions
	c.srcPositions = prevSrcPositions
	c.emitter = prevEmitter
	c.scope = prevScope
	c.currentArgumentsSlot = prevArgumentsSlot
	c.controlStack = prevControlStack
	c.pendingLabel = prevPendingLabel
	c.tryScopes = prevTryScopes
	c.finallyRetSlot = prevFinallyRetSlot

	meta := bytecode.NewFunctionMetadata(
		name, fnIns,
		fnScope.NumLocals(),
		len(params),
		paramSpecs,
		isArrow,
	)
	meta.BaseSlot = baseSlot
	meta.CapturePrefixLen = computeCapturePrefixLen(fnIns, baseSlot, c.constants)
	meta.ArgumentsSlot = argumentsSlot
	meta.SkipArguments = !argumentsObserved(fnIns, argumentsSlot)
	meta.SelfSlot = selfSlot
	meta.IsGenerator = isGenerator
	meta.IsAsync = false
	meta.IsMethod = isMethod
	meta.ParamPrologueEnd = paramPrologueEnd
	meta.IsStrict = c.strict
	meta.Positions = toSrcPosList(fnSrcPositions)
	return meta, nil
}

// compileAsyncFunction 编译 async 函数。
// async function f(a) { body } 等价于:
//
//	function f(a) { return __spawn((function* (a) { body' }) (a)); }
//
// 其中 body' 把 await X 编译为 yield X (由内层 generator 支持)。
// wrapper 的字节码:
//
//	LOAD_GLOBAL __spawn
//	FUNCTION <genIdx>      ; 创建 generator 闭包 (未启动)
//	LOAD 参数 slots...
//	CALL n                 ; 创建 Generator 对象 (参数存入 Args)
//	CALL 1                 ; __spawn(gen) → Promise
//	RETURN

func (c *Compiler) compileAsyncFunction(name string, params []*ast.Parameter, body *ast.BlockStatement) (*bytecode.FunctionMetadata, error) {
	// 非箭头入口 (async function 声明/表达式走这里)。
	return c.compileAsyncFunctionSelf(name, "", params, body, false, false)
}

// compileAsyncFunctionSelf 把异步函数编译成两段: wrapper + 内层 generator。
//
// isArrow 必须由调用方如实传入 —— 它决定 wrapper 与内层 generator 两个
// FunctionMetadata 的 IsArrow。箭头函数在调用时**不重绑 this**(vm 按
// Closure.IsArrow 判断), 漏了这个, `async () => this.x` 里的 this 就会变成
// 调用时的接收者。
// isMethod 只盖在 wrapper 的 meta 上 (对外可见的函数对象是 wrapper):
// 方法定义产出的 async 函数无 prototype 自有属性。

func (c *Compiler) compileAsyncFunctionSelf(name, selfName string, params []*ast.Parameter, body *ast.BlockStatement, isArrow, isMethod bool) (*bytecode.FunctionMetadata, error) {
	// 1. 编译内层 generator (同一参数, await 编译为 yield)
	// 自引用绑定传播到内层: await 所在的用户代码在内层执行。
	// inAsyncFunction 标志在内层体编译期间为真: for await...of 的
	// OP_YIELD 机制依赖 async 的 wrapper+generator 结构, 只在此合法。
	prevInAsync := c.inAsyncFunction
	c.inAsyncFunction = true
	// 强制 asyncGeneratorBody=false: 若本 async 函数嵌套在 async generator
	// 体内, 继承下来的标志会让 await 误编为 OP_AWAIT。
	prevAGBody := c.asyncGeneratorBody
	c.asyncGeneratorBody = false
	// wrapper 已把 rest 实参收成数组传入, 内层不再重复收集 (见字段注释)。
	c.innerGenRestPreCollected = true
	genMeta, err := c.compileFunctionSelf(name, selfName, params, body, isArrow, true, false)
	c.innerGenRestPreCollected = false
	c.asyncGeneratorBody = prevAGBody
	c.inAsyncFunction = prevInAsync
	if err != nil {
		return nil, err
	}
	genMeta.DeferParams = true
	// 内层 generator 用 OP_CALL 在 wrapper 帧里被调用 (无接收者), 必须
	// 词法继承 wrapper 帧的 this —— 否则 async 方法体里的 this 会丢成
	// undefined/globalThis (async 方法/async-generator 方法 this 回归)。
	genMeta.LexicalThis = true
	genIdx := c.constants.AddConstant(genMeta)

	// 2. 创建 wrapper 作用域并定义参数
	prevScope := c.scope
	baseSlot := prevScope.NumLocals()
	wrapperScope := NewFunctionScope(prevScope)
	c.scope = wrapperScope

	paramSpecs := make([]bytecode.ParameterSpec, len(params))
	paramSlots := make([]int, len(params))
	for i, param := range params {
		if param.Pattern != nil {
			// 解构模式参数: wrapper 只负责把原始实参放进隐藏槽再转交内层
			// generator (内层做解构); rest 模式参数在此收集剩余实参。
			paramSpecs[i] = bytecode.ParameterSpec{
				Name:       fmt.Sprintf("__param_%d", i),
				HasDefault: param.Default != nil,
				IsRest:     param.Rest,
			}
		} else {
			paramSpecs[i] = bytecode.ParameterSpec{
				Name:       param.Name,
				HasDefault: param.Default != nil,
				IsRest:     param.Rest,
			}
		}
		sym := wrapperScope.Define(paramSpecs[i].Name, false)
		sym.Declared = true
		paramSlots[i] = sym.Slot
	}
	argSym := wrapperScope.Define("__arguments__", false)
	argSym.Declared = true
	argumentsSlot := argSym.Slot

	// 3. 编译 wrapper 体
	prevEmitter := c.emitter
	c.emitter = NewEmitter()
	prevControlStack := c.controlStack
	c.controlStack = nil
	prevPendingLabel := c.pendingLabel
	c.pendingLabel = ""
	prevTryScopes := c.tryScopes
	c.tryScopes = nil
	prevFinallyRetSlot := c.finallyRetSlot
	c.finallyRetSlot = -1
	// using 释放作用域同样不跨函数边界: 内层函数体里出现的 using 只属于它自己。
	prevDisposeResources := c.disposeResources
	c.disposeResources = nil
	// defer: 保证错误早退路径也恢复, 否则 compileTryStatement 的 [:len-1] 会 panic (rCzckg 回归)。
	defer func() {
		c.tryScopes = prevTryScopes
		c.finallyRetSlot = prevFinallyRetSlot
		c.disposeResources = prevDisposeResources
	}()

	spawnIdx := c.constants.AddConstant(object.NewString("__spawn"))
	// 调用约定: fn 必须在栈顶。先压参数, 再 FUNCTION 创建 gen closure,
	// CALL n 弹出 fn=genClosure + 参数 → 创建 Generator。
	for _, slot := range paramSlots {
		c.emitter.Emit(bytecode.OP_LOAD, uint16(slot)) // [param...]
	}
	c.emitter.Emit(bytecode.OP_FUNCTION, uint16(genIdx))      // [param..., genClosure]
	c.emitter.Emit(bytecode.OP_CALL, uint16(len(paramSlots))) // [genObj]
	c.emitter.Emit(bytecode.OP_LOAD_GLOBAL, spawnIdx)         // [genObj, spawn]
	c.emitter.Emit(bytecode.OP_CALL, 1)                       // [promise]
	c.emitter.EmitNoOperand(bytecode.OP_RETURN)

	wrapperIns := c.emitter.Bytes()
	c.emitter = prevEmitter
	c.scope = prevScope
	c.controlStack = prevControlStack
	c.pendingLabel = prevPendingLabel
	c.tryScopes = prevTryScopes
	c.finallyRetSlot = prevFinallyRetSlot

	meta := bytecode.NewFunctionMetadata(name, wrapperIns, wrapperScope.NumLocals(), len(params), paramSpecs, isArrow)
	meta.BaseSlot = baseSlot
	meta.CapturePrefixLen = computeCapturePrefixLen(wrapperIns, baseSlot, c.constants)
	meta.ArgumentsSlot = argumentsSlot
	meta.SkipArguments = !argumentsObserved(wrapperIns, argumentsSlot)
	meta.IsAsync = true
	meta.IsMethod = isMethod
	meta.IsStrict = c.strict
	return meta, nil
}

// compileAsyncGeneratorSelf 把 async generator 编译成两段: wrapper + 内层 generator。
//
// async function* f(a) { body } 等价于:
//
//	function f(a) { return __async_generator((function* (a) { body' }) (a)); }
//
// 其中 body' 把 yield X 编为 OP_YIELD、await X 编为 OP_AWAIT (见 compileExpression)。
// wrapper 调用内层 generator 函数得到 Generator 对象 (IsGenerator=true 时调用
// 不执行体, 只创建对象), 再交给 __async_generator 包装成带异步迭代协议的
// AsyncGenerator 对象并返回 —— 因此 f() 的结果不是 Promise, 而是 AsyncGenerator。
//
// 复用 compileAsyncFunctionSelf 的参数作用域与 wrapper 结构, 仅替换收尾调用。

func (c *Compiler) compileAsyncGeneratorSelf(name, selfName string, params []*ast.Parameter, body *ast.BlockStatement, isArrow, isMethod bool) (*bytecode.FunctionMetadata, error) {
	// 1. 编译内层 generator。asyncGeneratorBody=true 让 await 编为 OP_AWAIT;
	//    inAsyncFunction=true 让 for await...of 在主路径内合法。
	prevAGBody := c.asyncGeneratorBody
	c.asyncGeneratorBody = true
	prevInAsync := c.inAsyncFunction
	c.inAsyncFunction = true
	c.innerGenRestPreCollected = true
	genMeta, err := c.compileFunctionSelf(name, selfName, params, body, isArrow, true, false)
	c.innerGenRestPreCollected = false
	c.inAsyncFunction = prevInAsync
	c.asyncGeneratorBody = prevAGBody
	if err != nil {
		return nil, err
	}
	// 同 compileAsyncFunctionSelf: 内层 generator 词法继承 wrapper 帧 this。
	genMeta.LexicalThis = true
	genIdx := c.constants.AddConstant(genMeta)

	// 2. 创建 wrapper 作用域并定义参数
	prevScope := c.scope
	baseSlot := prevScope.NumLocals()
	wrapperScope := NewFunctionScope(prevScope)
	c.scope = wrapperScope

	paramSpecs := make([]bytecode.ParameterSpec, len(params))
	paramSlots := make([]int, len(params))
	for i, param := range params {
		if param.Pattern != nil {
			paramSpecs[i] = bytecode.ParameterSpec{
				Name:       fmt.Sprintf("__param_%d", i),
				HasDefault: param.Default != nil,
				IsRest:     param.Rest,
			}
		} else {
			paramSpecs[i] = bytecode.ParameterSpec{
				Name:       param.Name,
				HasDefault: param.Default != nil,
				IsRest:     param.Rest,
			}
		}
		sym := wrapperScope.Define(paramSpecs[i].Name, false)
		sym.Declared = true
		paramSlots[i] = sym.Slot
	}
	argSym := wrapperScope.Define("__arguments__", false)
	argSym.Declared = true
	argumentsSlot := argSym.Slot

	// 合成自引用槽: wrapper 体要把"自己"的 .prototype 传给 __async_generator,
	// 用作 async generator 实例的 [[Prototype]]（规范: 每个 async generator
	// 函数对象有自己的 .prototype, 其实例 [[Prototype]] 指向它, 而该对象
	// 的 [[Prototype]] 才是 %AsyncGeneratorPrototype%）。名字用用户不可能
	// 写出的控制符前缀, 保证不与形参冲突。
	selfSym := wrapperScope.Define("\x00agself", false)
	selfSym.Declared = true
	selfSlot := selfSym.Slot

	// 3. 编译 wrapper 体
	prevEmitter := c.emitter
	c.emitter = NewEmitter()
	prevControlStack := c.controlStack
	c.controlStack = nil
	prevPendingLabel := c.pendingLabel
	c.pendingLabel = ""
	prevTryScopes := c.tryScopes
	c.tryScopes = nil
	prevFinallyRetSlot := c.finallyRetSlot
	c.finallyRetSlot = -1
	defer func() {
		c.tryScopes = prevTryScopes
		c.finallyRetSlot = prevFinallyRetSlot
	}()

	helperIdx := c.constants.AddConstant(object.NewString("__async_generator"))
	protoIdx := c.constants.AddConstant(object.NewString("prototype"))
	// 调用约定: fn 必须在栈顶。先压参数, 再 FUNCTION 创建 gen closure,
	// CALL n 弹出 fn=genClosure + 参数 → 创建 Generator。
	for _, slot := range paramSlots {
		c.emitter.Emit(bytecode.OP_LOAD, uint16(slot)) // [param...]
	}
	c.emitter.Emit(bytecode.OP_FUNCTION, uint16(genIdx))      // [param..., genClosure]
	c.emitter.Emit(bytecode.OP_CALL, uint16(len(paramSlots))) // [genObj]
	// 把自己 (.prototype) 作为第 2 个实参传给 __async_generator:
	// 实例的 [[Prototype]] 必须是本函数自己的 .prototype 对象 (惰性创建,
	// [[Prototype]] = %AsyncGeneratorPrototype%), 而非直接是后者。
	c.emitter.Emit(bytecode.OP_LOAD, uint16(selfSlot)) // [genObj, wrapperClosure]
	c.emitter.Emit(bytecode.OP_GET_PROP, protoIdx)     // [genObj, wrapperProto]
	c.emitter.Emit(bytecode.OP_LOAD_GLOBAL, helperIdx) // [genObj, wrapperProto, __async_generator]
	c.emitter.Emit(bytecode.OP_CALL, 2)                // [asyncGenerator]
	c.emitter.EmitNoOperand(bytecode.OP_RETURN)

	wrapperIns := c.emitter.Bytes()
	c.emitter = prevEmitter
	c.scope = prevScope
	c.controlStack = prevControlStack
	c.pendingLabel = prevPendingLabel
	c.tryScopes = prevTryScopes
	c.finallyRetSlot = prevFinallyRetSlot

	meta := bytecode.NewFunctionMetadata(name, wrapperIns, wrapperScope.NumLocals(), len(params), paramSpecs, isArrow)
	meta.BaseSlot = baseSlot
	meta.CapturePrefixLen = computeCapturePrefixLen(wrapperIns, baseSlot, c.constants)
	meta.ArgumentsSlot = argumentsSlot
	meta.SkipArguments = !argumentsObserved(wrapperIns, argumentsSlot)
	meta.SelfSlot = selfSlot
	meta.IsAsync = true
	// wrapper 的 [[Prototype]] 是 %AsyncGeneratorFunction.prototype%，
	// 单靠 IsAsync/IsGenerator 无法与普通 async 函数区分 (内层体才 IsGenerator)，
	// 故显式标记种类供 vm.createClosure 选择函数对象原型 (见 object.FuncPrototypeOf)。
	meta.IsAsyncGenerator = true
	meta.IsMethod = isMethod
	meta.IsStrict = c.strict
	return meta, nil
}

// isAnonymousFunctionDefinition 报告表达式是否为"匿名函数/类定义"—— 即
// NamedEvaluation 会据上下文赋予名字的那些形式: 箭头函数、无名字的 function
// 表达式、无名字的 class 表达式。
//
// 解析器已折叠括号 ((function(){}) 直接解析成 FunctionExpression), 与规范
// IsAnonymousFunctionDefinition 对 ParenthesizedExpression 透明的规定天然一致;
// 逗号表达式 (0, function(){}) / 成员 / 实参等形态则不是匿名函数定义, 不会被命名。

func isAnonymousFunctionDefinition(expr ast.Expression) bool {
	switch e := expr.(type) {
	case *ast.ArrowFunctionExpression:
		return true
	case *ast.FunctionExpression:
		return e.Name == nil
	case *ast.ClassExpression:
		return e.Name == nil
	}
	return false
}

// namedTargetName 返回解构/参数默认值这类绑定目标的名字。只有标识符目标参与
// NamedEvaluation (成员目标、嵌套模式目标都不会给匿名函数命名), 故非标识符
// 一律返回 ""。

func namedTargetName(target ast.Expression) string {
	if id, ok := target.(*ast.Identifier); ok {
		return id.Value
	}
	return ""
}

// compileNamedExpression 按 NamedEvaluation 语义编译表达式: 若表达式是匿名
// 函数/类定义, 用 name 作为其函数名编译; 否则丢弃名字提示、原样编译。
//
// 关键纪律 —— "不该有名字" 的形态绝不能走到这里: 成员赋值右侧 (o.k = fn)、
// 实参、下标、逗号表达式、数组/对象字面量元素等都不经过本函数, 因此不会被
// 误命名。只有"绑定到标识符名"或"属性定义"的语境才调用它。

func (c *Compiler) compileNamedExpression(expr ast.Expression, name string) error {
	if !isAnonymousFunctionDefinition(expr) {
		// 非匿名函数/类定义: 名字提示被丢弃 (NamedEvaluation 就此终止)。
		return c.compileExpression(expr)
	}
	switch e := expr.(type) {
	case *ast.ArrowFunctionExpression:
		return c.compileArrowFunctionNamed(e, name)
	case *ast.FunctionExpression:
		return c.compileFunctionExpressionNamed(e, name)
	case *ast.ClassExpression:
		return c.compileClassExpressionNamed(e, name)
	}
	return c.compileExpression(expr)
}

func (c *Compiler) compileFunctionExpression(node *ast.FunctionExpression) error {
	return c.compileFunctionExpressionNamed(node, "")
}

// compileFunctionExpressionNamed 编译函数表达式; 匿名时用 nameHint 作为名字
// (NamedEvaluation)。名字**只**作为函数对象的 name 属性, 不建立函数体内可见的
// 自引用绑定 (故 selfName 仍为空, 与规范一致: `var f = function(){}` 里 f 只在
// 外层可见, 函数体内引用 f 走外层绑定而非自引用槽)。

func (c *Compiler) compileFunctionExpressionNamed(node *ast.FunctionExpression, nameHint string) error {
	var name string
	selfName := ""
	if node.Name != nil {
		name = node.Name.Value
		// 命名函数表达式: 函数体内名字可见且指向自身 (递归入口)
		selfName = name
	} else {
		name = nameHint
	}
	prevStrict := c.strict
	c.strict = node.Strict
	meta, err := c.compileFunctionSelf(name, selfName, node.Parameters, node.Body, false, node.IsGenerator, node.IsAsync)
	c.strict = prevStrict
	if err != nil {
		return err
	}
	idx := c.constants.AddConstant(meta)
	c.emitter.Emit(bytecode.OP_FUNCTION, idx)
	return nil
}

func (c *Compiler) compileArrowFunctionExpression(node *ast.ArrowFunctionExpression) error {
	return c.compileArrowFunctionNamed(node, "")
}

// compileArrowFunctionNamed 编译箭头函数; nameHint 是其 name 属性的初始值
// (NamedEvaluation 时由绑定语境给出, 否则为空字符串 —— 规范里匿名箭头
// 函数的 name 就是 "")。

func (c *Compiler) compileArrowFunctionNamed(node *ast.ArrowFunctionExpression, nameHint string) error {
	name := nameHint
	// isAsync 走 compileAsyncFunctionSelf (wrapper + 内层 generator), 但 isArrow
	// 一路传下去 —— 否则 `async () => this.x` 的 this 会被调用时的接收者覆盖。
	meta, err := c.compileFunctionWithStrict(node.Strict, name, node.Parameters, getBlockFromBody(node.Body), true, false, node.IsAsync)
	if err != nil {
		return err
	}
	idx := c.constants.AddConstant(meta)
	c.emitter.Emit(bytecode.OP_ARROW_FUNC, idx)
	return nil
}

// getBlockFromBody 将箭头函数体转换为 BlockStatement。
// 如果 Body 已经是 BlockStatement，直接返回。
// 如果 Body 是表达式，创建一个包含 return 语句的 BlockStatement。

func getBlockFromBody(body ast.Node) *ast.BlockStatement {
	if block, ok := body.(*ast.BlockStatement); ok {
		return block
	}
	// 表达式体: 自动 return
	if expr, ok := body.(ast.Expression); ok {
		return &ast.BlockStatement{
			Statements: []ast.Statement{
				&ast.ReturnStatement{ReturnValue: expr},
			},
		}
	}
	return &ast.BlockStatement{}
}

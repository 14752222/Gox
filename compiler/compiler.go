package compiler

import (
	"fmt"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/bytecode"
)

type Compiler struct {
	emitter   *Emitter
	constants *bytecode.ConstantPool
	scope     *SymbolScope

	// controlStack 管理循环/switch/标签块的控制流上下文。
	// 每个循环或可标注语句 push 一个 context, 结束 pop。
	// break/continue 无标签时作用于最内层 context, 有标签时作用于匹配的标签 context。
	controlStack []*controlContext

	// pendingLabel 由 compileLabeledStatement 设置, 供紧随其后的循环编译读取。
	// 这样 `outer: for(...)` 可以把标签绑定到 for 的 context 上, 使 continue outer 也能工作。
	pendingLabel string

	// moduleMode 标记模块编译。
	// 模块有自己的作用域，其顶层变量不应写入共享全局环境 (避免跨模块命名冲突)，
	// 因此模块模式下全局作用域变量走局部 slot (旧行为)。
	// 普通脚本/REPL 模式下全局变量走 GLOBAL/DECLARE 指令，
	// 写入共享的 globals 环境，从而支持 REPL 跨输入状态保持。
	moduleMode bool

	// strict 是当前的严格模式上下文。Compile 入口按 program.Strict||moduleMode
	// 置初值; 每进一个函数/类体按该节点的 Strict (类体恒 true) 设/恢。
	// 供编译期早错 (delete 标识符 / eval·arguments 作赋值目标) 与
	// FunctionMetadata.IsStrict 盖章。
	strict bool

	// methodFnDepth > 0 表示正在编译"方法定义"函数 (对象字面量简洁方法 /
	// 访问器 / class 方法/静态方法)。compileFunctionSelf 入口据此给
	// FunctionMetadata 盖 IsMethod, 并在编译函数体前清 0 —— 函数体内
	// 嵌套的函数声明/表达式不是方法, 各自恢复。计数而非 bool: 方法体内
	// 再出现方法 (对象字面量嵌对象字面量) 也能正确归位。
	methodFnDepth int

	// currentArgumentsSlot 记录当前函数 (非箭头) 的 arguments 槽位。
	// 全局作用域或箭头函数为 -1 (箭头函数继承外层 arguments, 见 argumentsSlotStack)。
	currentArgumentsSlot int
	// argumentsSlotStack 保存嵌套函数的 arguments 槽位, 用于箭头函数继承外层。
	argumentsSlotStack []int

	// paramTDZNames / paramTDZScope 表达「形参默认值求值时的 TDZ 集合」。
	// 规范 9.2.12 FunctionDeclarationInstantiation: 非简单形参列表的形参绑定
	// 逐个按序初始化; 求值第 i 个形参的默认值时, 第 i..n-1 个形参名仍处于
	// TDZ (未初始化), 引用它们必须抛 ReferenceError (形参 in 自身/后位形参)。
	// 只在编译形参默认值期间非空, 且仅当引用点仍位于同一函数层
	// (c.scope.FuncLayer() == paramTDZScope) 时生效 —— 嵌套函数内不拦截
	// (那里读的是已捕获的绑定, 交给正常路径)。
	paramTDZNames map[string]bool
	paramTDZScope *SymbolScope

	// stmtPosTable 是 parser 侧收集的语句→源码位置 side-table (T05)。
	// SetStmtPos 注入; compileStatements 据此生成 srcPositions。
	stmtPosTable ast.PositionTable
	// srcPositions 记录"语句首条指令 offset → 源码位置"。VM 运行时
	// 错误用当前 PC 最近(≤PC)的表项渲染源码帧。函数体内的指令在独立
	// 的 CompiledFunction 里, 不污染主指令流的 offset。
	srcPositions map[int]ast.Pos

	// currentPrivatePrefix 记录当前正在编译的 class 的私有名键前缀。
	// 私有字段/方法落成普通属性, 但键是 "\x00<prefix>:<name>" 的混编码:
	// - \x00 让外部常规访问 (obj.k / obj["#x"] / Object.keys) 全部摸不到;
	// - prefix 每类唯一 (类名+序号), 子类/同名类互不串槽。
	// 类体外的 #x 访问在此前缀为空时报编译错 (类外私有访问非法)。
	currentPrivatePrefix string

	// privateClassSeq 私有类序号: 每个 class 递增, 拼进私有键前缀保证唯一。
	privateClassSeq int
	// 仅在编译 class 方法时非空。
	currentSuperClass string

	// evalHome 是「当前编译位置外围的 home object 上下文」——规范意义即
	// 外层函数的 [[HomeObject]] 是否可得。仅在**直接 eval 调用点**被读取
	// (emitEvalMarks): 非空时把 home 上下文随 OP_EVAL_MARK 家族的指令带出,
	// 让 eval 单元的 super.x 合法 (PerformEval 18.2.1.1.1 inMethod 语境)。
	//
	// 与 currentSuperClass 的关系: 后者驱动普通 super 代码生成 (仅 extends
	// 类), 前者只服务 eval 标记, 覆盖全部方法宿主 (含基类/对象字面量方法)。
	// 故意**不在函数边界重置**: 与 currentSuperClass 同纪律 —— 嵌套普通函数
	// 沿用外层 home (规范上应断, 属既有近似, 见 docs/roiE5Z 分析文档)。
	// 各宿主编译点设置后 defer 恢复, 不会跨编译单元泄漏。
	evalHome evalHomeCtx

	// evalSuperHome 非 nil 时, 本单元是「带 super home 语境的直接 eval 单元」
	// (stdlib 经编译桥的 EvalCompileOptions.SuperHome 注入): SuperProperty
	// (super.x) 合法并按 home 解析; SuperCall (super()) 恒 SyntaxError
	// (currentSuperClass 在 eval 单元恒为 "", 两条 super 调用分支原样拦下)。
	evalSuperHome *evalSuperHomeCtx
	// staticKeysSlot / staticKeysUsed: 静态成员 ComputedPropertyName 的
	// 预求值键数组槽位与消费计数 (r4hv9u)。
	//
	// 规范 ClassElementList Evaluation: 静态成员的 ComputedPropertyName 在
	// **外层函数上下文**求值 (外层是生成器时 `get [yield 9](){}` 合法), 而
	// 合成静态初始化函数 __static_init__ 是非生成器帧 —— 把计算键编进去会
	// 让 yield 在运行时撞 "yield outside generator"。故 compileClassBody 把
	// 全部静态计算键按定义顺序在外层求值收进数组, 作唯一实参传进合成函数;
	// 合成函数内 staticKeysSlot >= 0 时各计算键从该数组按序取
	// (staticKeysUsed 计数), 不再原地编译表达式。
	staticKeysSlot int
	staticKeysUsed int

	// tryScopes 是当前函数内「仍活跃」的 try 处理器条目的编译期镜像 ——
	// 对应运行时 vm.tryStack 里属于当前帧的那一段。长度即 try 嵌套深度。
	//
	// 用途 (rMkA8D): return / break / continue 需要跳出一个或多个 try 时,
	// 必须由内向外逐个 POP_TRY, 并对带 finally 的条目**内联执行 finally 体**,
	// 之后才真正转移控制。这是 ES「控制转移穿过 try 体时先跑 finally」的落点。
	// 跨函数边界必须清空 (见 compileFunctionSelf) —— 内层函数的控制转移
	// 不该触碰外层帧的 try 条目。
	tryScopes []tryScope

	// finallyRetSlot 是「穿 finally 的 return 值」暂存槽 (惰性分配, -1 = 未分配)。
	//
	// 值必须经槽位而不是求值栈传递: 内联的 finally 体里若还有 return /
	// break / continue, 留在栈上的值会被那条跳转带到不相干的位置。
	// 每个函数一份, 进出函数时保存/恢复。
	finallyRetSlot int

	// disposeResources 记录当前正在编译的 using 作用域里, 每个 UsingStatement
	// 声明的资源记录所落的隐藏槽 (按声明顺序)。compileUsingStatement 据此登记;
	// 作用域外为 nil。进出 using 作用域时保存/恢复 (可嵌套)。
	disposeResources map[*ast.UsingStatement][]*bytecode.DisposeResource

	// inAsyncFunction 标记正在编译 async 函数的**内层 generator** 体
	// (await 的实际执行处, for await...of 只在此合法)。进出内层体时
	// 保存/恢复; 普通函数/顶层恒为 false, for-await 在那里是 SyntaxError。
	inAsyncFunction bool

	// asyncGeneratorBody 标记正在编译的正是 **async generator** 的内层体。
	// 为真时 await 编为 OP_AWAIT (与 yield 的 OP_YIELD 区分, 供异步生成器
	// 驱动识别内部挂起点); for-await 的异步步进也同样编 OP_AWAIT。
	// 普通 async 函数的内层体为 false (await 仍 OP_YIELD)。
	asyncGeneratorBody bool

	// fnDepth 记录当前编译位置所处的函数嵌套层数 (模块/脚本顶层为 0)。
	// 用于区分"模块顶层的顶层 await (TLA)"与"函数体内的 await" —— 顶层
	// await 需要 VM 把主单元当生成器驱动 (见 vm.RunCompiledAsync)。
	fnDepth int

	// topLevelAwait 标记本编译单元 (模块) 顶层出现过 await / for await。
	// 置位后 Compile 产物的"主单元"含 OP_YIELD 挂起点, 模块求值必须走异步
	// 驱动; VM 由 HasTopLevelAwait 读取该标志决定驱动方式。
	topLevelAwait bool

	// staticImports 记录模块顶层静态 import / 再导出 (export ... from) 的
	// 模块说明符, 按出现顺序、去重。模块加载器据此在运行模块本体的**之前**
	// 先求值全部依赖 (规范 InnerModuleEvaluation): 只要图里有模块含顶层
	// await, 整图就按异步路径 leaf-to-root 求值。函数体内的 import() 是
	// 动态导入 (OP_DYNAMIC_IMPORT), 不进这里。
	staticImports []string
	staticSeen    map[string]bool

	// innerGenRestPreCollected 标记「即将编译的函数是 async / async-generator
	// 的**内层 generator**」。这类函数的 wrapper 已经把末尾 rest 实参收成数组,
	// 再作为**单个实参**传给内层 —— 所以内层末位参数 (含解构模式) 不能再按
	// rest 收集, 否则会双重包裹 (async function f(...a){} 旧返回 [[1,2,3]])。
	// 由 compileAsyncFunctionSelf / compileAsyncGeneratorSelf 置位,
	// compileFunctionSelf 入口立即消费, 绝不泄漏到体内嵌套函数。
	innerGenRestPreCollected bool

	// inClassFieldInit 标记正在编译某个 class **字段初始化器表达式**。
	// 为真时, 该表达式里出现的直接 eval 调用 (`eval(...)`) 会额外发射
	// OP_EVAL_MARK —— 规范 sec-performeval-rules-in-initializer: 字段初始化
	// 器内的直接 eval 源码含 arguments 是早错 (间接 eval 不受限)。
	// 进入非箭头函数体时清空 (嵌套普通函数有自己的 arguments 作用域,
	// 其内的 eval 不再受初始化器规则约束); 箭头函数保持外层值 (无独立
	// arguments 作用域, 规范上继续受约束 —— 见 nested-direct-eval 用例)。
	inClassFieldInit bool

	// withScopes 是当前词法位置**仍活跃**的 with 语句上下文栈 (内层在后),
	// 每一项记下承载 with 对象的局部槽位 slot 与进入 with 时的作用域深度 depth。
	//
	// with 体内自由标识符的编译据此决定: 若该标识符解析到的绑定深度 ≤ 最内层
	// with 的 depth (或是全局/未绑定), 就不能沿用编译期槽位, 必须发射
	// OP_WITH_LOAD/STORE/DELETE 走运行时对象查找 (见 withAffects / withChainFor)。
	// 对象槽位是普通局部槽 —— with 体里创建的闭包按捕获前缀拿到它, 于是
	// 「闭包捕获 with 环境」自然成立; 也无需 enter/exit 指令与异常回退。
	withScopes []withScope
	// withSlotSeq 为合成 with 对象槽名计数, 保证同名不冲突。
	withSlotSeq int
}

// withScope 是一个活跃 with 上下文的编译期描述。

type withScope struct {
	slot  int // 承载 with 对象的局部槽位
	depth int // 进入 with 时所在作用域深度 (该深度及更外层的绑定会被 with 遮蔽)
}

// tryScope 是一个活跃 try 处理器条目的编译期描述 (对应运行时 vm.tryStack 的一条)。
//
// 一个 try/catch/finally 语句会按需压入 1~2 条:
//   - try 体: 一条 (运行时 PUSH_TRY [catchPC] 必然发射);
//   - catch 体: 仅当存在 finally 时再压一条 (运行时 catch 体开头的
//     PUSH_TRY 0 + PUSH_FINALLY, 保证 catch 里再 throw 也过 finally)。
//
// finallyBody 是该条目的 finally 体 (两条共享同一份 AST)。
//
// closeSlot >= 0 时该条目是 for-await 的「迭代器收尾」条目 (AsyncIteratorClose):
// 控制转移穿出循环体 (break / return / throw) 时, 内联「调迭代器 return()
// 并 await 其结果」的收尾序列 —— closeSlot 是存放迭代器的隐藏局部槽。
// 它复用 finally 的运行时机制 (PUSH_TRY 0 + PUSH_FINALLY), 只是内联的
// 不是用户 finally 体而是 close 序列。

type tryScope struct {
	hasFinally  bool
	finallyBody *ast.BlockStatement
	closeSlot   int

	// closeSync 为 true 时该 closeSlot 条目是**同步** for-of 的迭代器收尾
	// (IteratorClose, 规范 7.4.6): 内联发射 OP_ITER_CLOSE, 而非 for-await
	// 的 AsyncIteratorClose 序列 (后者带 await)。closeSlot >= 0 时二者互斥。
	closeSync bool

	// dispose 非 nil 时该条目是 using 作用域的释放条目: 控制转移穿出时
	// 内联发射 OP_DISPOSE_EXIT <dispose.idx> (常量池索引, 指向 *DisposeScope)。
	// 它与 finally 复用同一套运行时机制 (PUSH_TRY 0 + PUSH_FINALLY), 只是
	// 内联的不是用户 finally 体而是资源释放序列。nil 表示非释放条目。
	dispose *disposeEntry
}

// disposeEntry 是一个已登记常量池条目的 using 释放作用域。

type disposeEntry struct {
	scope *bytecode.DisposeScope
	idx   uint16
}

// controlContext 表示一个循环/switch/标签块的控制流上下文。

func New() *Compiler {
	return &Compiler{
		emitter:              NewEmitter(),
		constants:            bytecode.NewConstantPool(),
		scope:                NewFunctionScope(nil),
		currentArgumentsSlot: -1,
		finallyRetSlot:       -1,
	}
}

// evalHomeCtx 描述「外层方法的 home object」——即规范 [[HomeObject]] 在
// Gox 静态名模型下的等价物。仅在直接 eval 调用点读取 (emitEvalMarks)。
//
//   - Name:     home 所在类名 (类方法/字段初始化器/静态语境)。匿名类
//     ("<anonymous>") 与无名场景留空, 表示无可加载的 home。
//   - Static:   home 是类本身 (static 方法/静态块) 而非 prototype。
//   - SuperName: 仅 Static 且类有 extends: 静态 super base = 父类构造器。
//   - ThisHome: home 是对象字面量方法宿主 (无名字, 运行期取 this)。

func (c *Compiler) SetModuleMode(v bool) { c.moduleMode = v }

// SetEvalSuperHome 注入「直接 eval 单元的 super home 上下文」——由 vm 编译桥
// 在编译带 home 语境的 eval 源码前调用 (见 object.EvalSuperHome)。注入后
// 本单元的 SuperProperty 合法并按 home 解析; SuperCall 仍拦 (currentSuperClass
// 在 eval 单元恒空)。nil / home.Has=false 表示无 home 语境 (super.x 报错)。

func (c *Compiler) HasTopLevelAwait() bool { return c.topLevelAwait }

// StaticImports 返回模块顶层静态依赖的模块说明符 (去重、保序)。

func (c *Compiler) StaticImports() []string { return c.staticImports }

// noteStaticImport 登记一条顶层静态依赖说明符 (去重)。

func (c *Compiler) Bytes() bytecode.Instructions { return c.emitter.Bytes() }

// Constants 返回常量池。

func (c *Compiler) Constants() *bytecode.ConstantPool { return c.constants }

// NumLocals 返回主程序的局部变量数。

func (c *Compiler) NumLocals() int { return c.scope.NumLocals() }

// Compile 编译一个 AST 程序。

func (c *Compiler) Compile(program *ast.Program) error {
	// 严格性: ScriptBody 含 "use strict" 指令, 或本单元是 module (恒严格)。
	c.strict = program.Strict || c.moduleMode
	stmts := programStatements(program)
	// 模块顶层可能有 using 声明: 在主单元建立释放作用域 (程序结束/异常时释放)。
	return c.withDisposeScope(stmts, func() error {
		return c.compileStatements(stmts)
	})
}

// jsxFactoryModule / jsxFactoryName 是 JSX 的缺省工厂: parser 把小写标签降级成
// h(...) 调用 (见 parser/jsx.go), 而 h 的家在 gx/gfx。

func (c *Compiler) compileStatements(stmts []ast.Statement) error {
	if err := c.prescanScope(stmts); err != nil {
		return err
	}

	// var 提升初始化: 只在函数作用域层做 (块里编译时不发射 —— var 在函数体
	// prescan 时已递归收集并登记, 块的 prescan 会命中已有符号直接复用)。
	if c.scope.IsFuncLayer() {
		if err := c.emitVarHoistInits(stmts); err != nil {
			return err
		}
	}

	hoisted := make(map[ast.Statement]bool)
	for _, stmt := range stmts {
		fd, ok := stmt.(*ast.FunctionDeclaration)
		if !ok {
			continue
		}
		if err := c.compileFunctionDeclaration(fd); err != nil {
			return err
		}
		hoisted[stmt] = true
	}

	for _, stmt := range stmts {
		if hoisted[stmt] {
			continue
		}
		// 语句级位置表: 语句首条指令 offset → 该语句的源码位置 (T05)。
		// 运行时错误按 PC 最近表项回溯到出错语句所在行。
		start := c.emitter.Pos()
		if err := c.compileStatement(stmt); err != nil {
			return err
		}
		if pos, ok := c.stmtPosTable[stmt]; ok && c.emitter.Pos() > start {
			if c.srcPositions == nil {
				c.srcPositions = make(map[int]ast.Pos)
			}
			c.srcPositions[start] = pos
		}
	}
	return nil
}

// SetStmtPos 注入 parser 收集的语句位置表 (compileSource 接线)。

func (c *Compiler) SetStmtPos(tbl ast.PositionTable) { c.stmtPosTable = tbl }

// toSrcPosList 把语句位置表转为按 offset 升序的 SrcPos 列表
// (挂到 FunctionMetadata.Positions, 供 VM 运行时错误回溯)。

func toSrcPosList(m map[int]ast.Pos) []bytecode.SrcPos {
	if len(m) == 0 {
		return nil
	}
	out := make([]bytecode.SrcPos, 0, len(m))
	for off, p := range m {
		out = append(out, bytecode.SrcPos{Offset: off, Line: p.Line, Col: p.Col})
	}
	bytecode.SortSrcPos(out)
	return out
}

// StmtPositions 返回生成的字节码位置映射 (可能为 nil —— 无位置信息时
// VM 侧跳过源码帧渲染)。

func (c *Compiler) StmtPositions() map[int]ast.Pos { return c.srcPositions }

// varBinding 是一个待提升初始化的 var 绑定。

func (c *Compiler) compileStatement(stmt ast.Statement) error {
	switch node := stmt.(type) {
	case *ast.ExpressionStatement:
		if node.Expression == nil {
			// 空语句 (单独的 ;): 无操作
			return nil
		}
		if err := c.compileExpression(node.Expression); err != nil {
			return err
		}
		c.emitter.EmitNoOperand(bytecode.OP_POP) // 表达式语句: 丢弃结果
		return nil
	case *ast.LetStatement:
		return c.compileLetStatement(node)
	case *ast.VarStatement:
		return c.compileVarStatement(node)
	case *ast.ConstStatement:
		return c.compileConstStatement(node)
	case *ast.UsingStatement:
		return c.compileUsingStatement(node)
	case *ast.ReturnStatement:
		return c.compileReturnStatement(node)
	case *ast.BlockStatement:
		return c.compileBlockStatement(node)
	case *ast.IfStatement:
		return c.compileIfStatement(node)
	case *ast.WhileStatement:
		return c.compileWhileStatement(node)
	case *ast.DoWhileStatement:
		return c.compileDoWhileStatement(node)
	case *ast.WithStatement:
		return c.compileWithStatement(node)
	case *ast.ForOfStatement:
		return c.compileForOfStatement(node)
	case *ast.ForInStatement:
		return c.compileForInStatement(node)
	case *ast.ForStatement:
		return c.compileForStatement(node)
	case *ast.BreakStatement:
		return c.compileBreakStatement(node)
	case *ast.ContinueStatement:
		return c.compileContinueStatement(node)
	case *ast.LabeledStatement:
		return c.compileLabeledStatement(node)
	case *ast.FunctionDeclaration:
		return c.compileFunctionDeclaration(node)
	case *ast.ThrowStatement:
		return c.compileThrowStatement(node)
	case *ast.TryStatement:
		return c.compileTryStatement(node)
	case *ast.SwitchStatement:
		return c.compileSwitchStatement(node)
	case *ast.ImportDeclaration:
		return c.compileImportDeclaration(node)
	case *ast.ExportDeclaration:
		return c.compileExportDeclaration(node)
	case *ast.ClassDeclaration:
		return c.compileClassDeclaration(node)
	default:
		return fmt.Errorf("unsupported statement type: %T", stmt)
	}
}

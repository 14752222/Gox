package compiler

import (
	"fmt"
	"sort"
	"strings"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/lexer"
	"github.com/14752222/Gox/object"
)

// Compiler 将 AST 编译为字节码。
// 遍历 AST 树，为每个节点生成对应的字节码指令。
// 输出: Instructions (字节码) + ConstantPool (常量池) + 函数元数据。
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

	// currentArgumentsSlot 记录当前函数 (非箭头) 的 arguments 槽位。
	// 全局作用域或箭头函数为 -1 (箭头函数继承外层 arguments, 见 argumentsSlotStack)。
	currentArgumentsSlot int
	// argumentsSlotStack 保存嵌套函数的 arguments 槽位, 用于箭头函数继承外层。
	argumentsSlotStack []int

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

	// inAsyncFunction 标记正在编译 async 函数的**内层 generator** 体
	// (await 的实际执行处, for await...of 只在此合法)。进出内层体时
	// 保存/恢复; 普通函数/顶层恒为 false, for-await 在那里是 SyntaxError。
	inAsyncFunction bool

	// asyncGeneratorBody 标记正在编译的正是 **async generator** 的内层体。
	// 为真时 await 编为 OP_AWAIT (与 yield 的 OP_YIELD 区分, 供异步生成器
	// 驱动识别内部挂起点); for-await 的异步步进也同样编 OP_AWAIT。
	// 普通 async 函数的内层体为 false (await 仍 OP_YIELD)。
	asyncGeneratorBody bool
}

// tryScope 是一个活跃 try 处理器条目的编译期描述 (对应运行时 vm.tryStack 的一条)。
//
// 一个 try/catch/finally 语句会按需压入 1~2 条:
//   - try 体: 一条 (运行时 PUSH_TRY [catchPC] 必然发射);
//   - catch 体: 仅当存在 finally 时再压一条 (运行时 catch 体开头的
//     PUSH_TRY 0 + PUSH_FINALLY, 保证 catch 里再 throw 也过 finally)。
//
// finallyBody 是该条目的 finally 体 (两条共享同一份 AST)。
type tryScope struct {
	hasFinally  bool
	finallyBody *ast.BlockStatement
}

// controlContext 表示一个循环/switch/标签块的控制流上下文。
type controlContext struct {
	label         string // 空表示未标注
	isLoop        bool   // continue 只对循环有效
	breakJumps    []int  // 待回填的 break 跳转位置
	continueJumps []int  // 待回填的 continue 跳转位置

	// tryScopes 是创建本 context 时的 try 嵌套深度。
	// break / continue 跳出本 context 时, 需要收尾 (len(c.tryScopes) - tryScopes)
	// 层 try: 更深说明目标在 try 之外, 必须先跑 finally 再跳;
	// 相等说明目标仍在所有活跃 try 之内, 直接跳即可 (finally 不跑)。
	tryScopes int
}

// pushControl 压入一个控制上下文, 返回其指针。
func (c *Compiler) pushControl(label string, isLoop bool) *controlContext {
	ctx := &controlContext{label: label, isLoop: isLoop, tryScopes: len(c.tryScopes)}
	c.controlStack = append(c.controlStack, ctx)
	return ctx
}

// popControl 弹出最内层控制上下文并返回。
func (c *Compiler) popControl() *controlContext {
	last := c.controlStack[len(c.controlStack)-1]
	c.controlStack = c.controlStack[:len(c.controlStack)-1]
	return last
}

// lookupControl 查找标签或最内层循环对应的控制上下文。
// label 为空时返回最内层 context (必须有)。否则返回 label 匹配的 context。
func (c *Compiler) lookupControl(label string, isContinue bool) *controlContext {
	if label == "" {
		// 空栈 (顶层裸 break/continue): 返回 nil, 由调用方报 SyntaxError,
		// 而不是越界 panic 崩掉整个进程。
		if len(c.controlStack) == 0 {
			return nil
		}
		return c.controlStack[len(c.controlStack)-1]
	}
	for i := len(c.controlStack) - 1; i >= 0; i-- {
		ctx := c.controlStack[i]
		if ctx.label == label {
			return ctx
		}
	}
	return nil
}

// takePendingLabel 取出并清空 pendingLabel, 供循环/switch 编译读取。
func (c *Compiler) takePendingLabel() string {
	label := c.pendingLabel
	c.pendingLabel = ""
	return label
}

// New 创建新编译器。
func New() *Compiler {
	return &Compiler{
		emitter:              NewEmitter(),
		constants:            bytecode.NewConstantPool(),
		scope:                NewFunctionScope(nil),
		currentArgumentsSlot: -1,
		finallyRetSlot:       -1,
	}
}

// SetModuleMode 设置模块编译模式。
func (c *Compiler) SetModuleMode(v bool) { c.moduleMode = v }

// Bytes 返回编译后的字节码。
func (c *Compiler) Bytes() bytecode.Instructions { return c.emitter.Bytes() }

// Constants 返回常量池。
func (c *Compiler) Constants() *bytecode.ConstantPool { return c.constants }

// NumLocals 返回主程序的局部变量数。
func (c *Compiler) NumLocals() int { return c.scope.NumLocals() }

// Compile 编译一个 AST 程序。
func (c *Compiler) Compile(program *ast.Program) error {
	return c.compileStatements(programStatements(program))
}

// jsxFactoryModule / jsxFactoryName 是 JSX 的缺省工厂: parser 把小写标签降级成
// h(...) 调用 (见 parser/jsx.go), 而 h 的家在 gx/gfx。
const (
	jsxFactoryModule = "gx/gfx"
	jsxFactoryName   = "h"
)

// programStatements 返回待编译的语句列表, 必要时在最前面补一条缺省工厂导入。
//
// 为什么需要这一步: JSX 降级发生在 parser, 于是"用了 JSX 但没导入 h"的脚本
// **能编译通过**, 直到挂载那一刻才 `ReferenceError: h is not defined` ——
// 症状 (窗口起不来) 与原因 (某个 import 少了) 隔得很远。这里把它当缺省运行时
// 补齐 (与 Babel 的 automatic runtime 同一思路): 只要本文件出现过小写标签的
// JSX, 就补一条 `import { h } from "gx/gfx"`。
//
// 两种不补的情况:
//   - 本文件已经绑定了 h (import / let / const / function / class, 含解构):
//     用户自己指定了工厂, 一律尊重 —— 一个字节都不动;
//   - 程序里没有小写标签的 JSX (纯 <Comp/> 是组件调用, 根本用不到 h)。
//
// 补出来的这条 import 与手写的完全等价 (同样走 OP_IMPORT + 导出绑定), 所以显式
// `import { h } from "gox"` 之类的写法照旧可用, 也照旧优先。
func programStatements(program *ast.Program) []ast.Statement {
	if !program.UsesJSX || bindsNameAtTopLevel(program.Statements, jsxFactoryName) {
		return program.Statements
	}
	imp := &ast.ImportDeclaration{
		Token:        lexer.Token{Type: lexer.IMPORT, Literal: "import", Line: 1, Column: 1},
		NamedImports: []ast.NamedImport{{Imported: jsxFactoryName, Local: jsxFactoryName}},
		Source:       jsxFactoryModule,
	}
	return append([]ast.Statement{imp}, program.Statements...)
}

// bindsNameAtTopLevel 报告顶层语句里有没有对 name 的绑定。
//
// 只看**顶层**: 函数体内的同名绑定管不到顶层的 JSX, 而顶层补进来的 import 会被
// 内层的同名声明按普通作用域规则遮蔽。
func bindsNameAtTopLevel(stmts []ast.Statement, name string) bool {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.ImportDeclaration:
			if s.DefaultName == name || s.Namespace == name {
				return true
			}
			// 认**本地名**: `import { h as _h }` 并没有绑 h, 不能算已绑定;
			// `import { _h as h }` 才是把 h 绑进来了。
			for _, n := range s.NamedImports {
				if n.Local == name || (n.Local == "" && n.Imported == name) {
					return true
				}
			}
		case *ast.LetStatement:
			if declaratorBindsName(s.Name, s.Value, name) {
				return true
			}
			for _, d := range s.More {
				if declaratorBindsName(d.Name, d.Value, name) {
					return true
				}
			}
		case *ast.ConstStatement:
			if declaratorBindsName(s.Name, s.Value, name) {
				return true
			}
			for _, d := range s.More {
				if declaratorBindsName(d.Name, d.Value, name) {
					return true
				}
			}
		case *ast.FunctionDeclaration:
			if s.Name != nil && s.Name.Value == name {
				return true
			}
		case *ast.ClassDeclaration:
			if s.Name != nil && s.Name.Value == name {
				return true
			}
		}
	}
	return false
}

// declaratorBindsName 判断一个声明项 (含解构) 是否绑定了 name —— 解构声明在 AST
// 里是 Name="__destructure__" + Value=AssignmentExpression (Left 是模式),
// 所以这里要走一遍模式: `const { h } = gfx` 同样是"用户自己指定了 h"。
func declaratorBindsName(ident *ast.Identifier, value ast.Expression, name string) bool {
	if ident == nil {
		return false
	}
	if ident.Value == name {
		return true
	}
	if ident.Value != destructureSyntheticName {
		return false
	}
	assign, ok := value.(*ast.AssignmentExpression)
	if !ok {
		return false
	}
	return patternBindsName(assign.Left, name)
}

// patternBindsName 在解构模式里找绑定名 (数组 / 对象 / 嵌套都走一遍)。
func patternBindsName(pattern ast.Expression, name string) bool {
	switch p := pattern.(type) {
	case *ast.Identifier:
		return p.Value == name
	case *ast.ArrayPattern:
		for _, el := range p.Elements {
			if el != nil && patternBindsName(el.Target, name) {
				return true
			}
		}
	case *ast.ObjectPattern:
		for _, prop := range p.Properties {
			if prop != nil && patternBindsName(prop.Value, name) {
				return true
			}
		}
	}
	return false
}

// compileStatements 编译一个语句列表，并实现 ECMAScript 的声明提升。
//
//  1. prescanScope: 先把列表里的 let/const/function 绑定登记到当前作用域
//     (只登记符号, 不发射指令)。这是函数提升能正确工作的前提 —— 提升到
//     列表顶部的函数体在编译时要能解析到列表后面才出现的 let 变量:
//
//     let x = 1;            // 提升的 f 需要捕获 x
//     f(); function f() { return x; }
//
//  2. var 提升 (T04): 在**函数作用域层** (含全局) 的语句列表, 先递归收集
//     列表内**全部** var 声明 (包括嵌套块里的 —— var 穿透块), 对每个新的
//     var 绑定发射 `OP_UNDEFINED + OP_STORE` (全局层用 OP_DECLARE), 即
//     `var x` 的 "声明即 undefined" 提升语义。必须先于函数声明提升发射:
//     同名时函数声明的值要覆盖 var 的 undefined (ES 进入期初始化顺序)。
//
//  3. 再编译列表里的 function 声明 (绑定 + 创建函数对象)，其余语句按源码
//     顺序编译，已提升的声明跳过以免重复创建。
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
type varBinding struct {
	name string
	slot int // 全局层不用 (走 OP_DECLARE)
}

// emitVarHoistInits 递归收集语句列表里的全部 var 绑定 (不进嵌套函数体),
// 对函数作用域层里**新出现**的名字发射 undefined 初始化。
// 层内已有的绑定 (参数 / 函数声明 / 重复 var) 不再初始化 —— 参数不能被
// undefined 覆盖, 函数声明的值必须保留。
func (c *Compiler) emitVarHoistInits(stmts []ast.Statement) error {
	var bindings []varBinding
	if err := c.collectVarBindings(stmts, &bindings, 0); err != nil {
		return err
	}
	isGlobal := c.isGlobalScope()
	for _, b := range bindings {
		c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
		if isGlobal {
			nameIdx := c.constants.AddConstant(object.NewString(b.name))
			c.emitter.Emit(bytecode.OP_DECLARE, nameIdx)
		} else {
			c.emitter.Emit(bytecode.OP_STORE, uint16(b.slot))
		}
	}
	return nil
}

// collectVarBindings 递归收集 var 绑定并登记到函数作用域层。
// 遍历只穿透控制流块 (if/for/while/switch/try/label/block), **不进入**
// 嵌套函数 (函数声明/函数表达式/箭头/类) —— 那是新的函数作用域。
func (c *Compiler) collectVarBindings(stmts []ast.Statement, out *[]varBinding, depth int) error {
	if depth > 64 { // 防御性深度上限 (病态嵌套), 64 层块远超正常代码
		return nil
	}
	for _, stmt := range stmts {
		if err := c.collectVarBindingsStmt(stmt, out, depth); err != nil {
			return err
		}
	}
	return nil
}

func (c *Compiler) collectVarBindingsStmt(stmt ast.Statement, out *[]varBinding, depth int) error {
	declare := func(name string) error {
		fn := c.scope.FuncLayer()
		if sym := fn.ResolveLocal(name); sym != nil {
			// 参数 (IsVarLike) / 函数声明 / 更早的 var 同名: 复用绑定;
			// let/const 与 var 同层同名 → SyntaxError
			if !sym.IsVarLike && !sym.IsFnDecl {
				return fmt.Errorf("SyntaxError: Identifier '%s' has already been declared", name)
			}
			return nil
		}
		sym := fn.Define(name, false)
		sym.IsVarLike = true
		sym.Declared = true
		*out = append(*out, varBinding{name: name, slot: sym.Slot})
		return nil
	}

	switch node := stmt.(type) {
	case *ast.VarStatement:
		if node.Name != nil && node.Name.Value != destructureSyntheticName {
			if err := declare(node.Name.Value); err != nil {
				return err
			}
		}
		for _, d := range node.More {
			if d.Name != nil && d.Name.Value != destructureSyntheticName {
				if err := declare(d.Name.Value); err != nil {
					return err
				}
			}
		}
	case *ast.ExportDeclaration:
		// `export var Color;` 必须参与 var 提升: esbuild 把 `export enum` 降级成
		// 这个形态 (后接 IIFE 给 Color 赋值)。不提升的话 Color 根本没被声明,
		// 导出会去读全局 → "Color is not defined" (M2 缺口)。导出不引入新作用域,
		// 所以 depth 不递增。
		if node.Declaration != nil {
			return c.collectVarBindingsStmt(node.Declaration, out, depth)
		}
	case *ast.BlockStatement:
		return c.collectVarBindings(node.Statements, out, depth+1)
	case *ast.IfStatement:
		if node.Consequence != nil {
			if err := c.collectVarBindings(node.Consequence.Statements, out, depth+1); err != nil {
				return err
			}
		}
		if node.Alternative != nil {
			if err := c.collectVarBindings(node.Alternative.Statements, out, depth+1); err != nil {
				return err
			}
		}
	case *ast.ForStatement:
		if node.Init != nil {
			if err := c.collectVarBindingsStmt(node.Init, out, depth+1); err != nil {
				return err
			}
		}
		if node.Body != nil {
			if err := c.collectVarBindings(node.Body.Statements, out, depth+1); err != nil {
				return err
			}
		}
	case *ast.ForOfStatement:
		if _, isVarDecl := node.VarDecl.(*ast.VarStatement); isVarDecl && node.Variable != nil {
			if err := declare(node.Variable.Value); err != nil {
				return err
			}
		}
		if node.Body != nil {
			if err := c.collectVarBindings(node.Body.Statements, out, depth+1); err != nil {
				return err
			}
		}
	case *ast.ForInStatement:
		if _, isVarDecl := node.VarDecl.(*ast.VarStatement); isVarDecl && node.Variable != nil {
			if err := declare(node.Variable.Value); err != nil {
				return err
			}
		}
		if node.Body != nil {
			if err := c.collectVarBindings(node.Body.Statements, out, depth+1); err != nil {
				return err
			}
		}
	case *ast.WhileStatement:
		if node.Body != nil {
			if err := c.collectVarBindings(node.Body.Statements, out, depth+1); err != nil {
				return err
			}
		}
	case *ast.DoWhileStatement:
		if node.Body != nil {
			if err := c.collectVarBindings(node.Body.Statements, out, depth+1); err != nil {
				return err
			}
		}
	case *ast.SwitchStatement:
		for _, cs := range node.Cases {
			if err := c.collectVarBindings(cs.Statements, out, depth+1); err != nil {
				return err
			}
		}
	case *ast.TryStatement:
		for _, blk := range []*ast.BlockStatement{node.Body, node.CatchBody, node.FinallyBody} {
			if blk != nil {
				if err := c.collectVarBindings(blk.Statements, out, depth+1); err != nil {
					return err
				}
			}
		}
	case *ast.LabeledStatement:
		if err := c.collectVarBindingsStmt(node.Body, out, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// ===== 语句编译 =====

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

func (c *Compiler) compileLetStatement(stmt *ast.LetStatement) error {
	// 解构赋值: let [a, b] = arr  或  let { x, y } = obj
	if stmt.Name.Value == "__destructure__" {
		if assign, ok := stmt.Value.(*ast.AssignmentExpression); ok {
			return c.compileDestructureAssignment(assign, true)
		}
	}

	// 无初始化器 (let x;) 也必须注册符号，
	// 否则后续 x = ... 会被当作全局变量处理。
	// 规范: let x; 等价于 let x = undefined —— 必须显式写入 undefined，
	// 否则局部槽保持未初始化态 (读取触发 TDZ 报错)、全局则根本未声明。
	if stmt.Value != nil {
		if err := c.compileExpression(stmt.Value); err != nil {
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

	// 多条声明: let a = 1, b = 2;
	for _, d := range stmt.More {
		if d.Value != nil {
			if err := c.compileExpression(d.Value); err != nil {
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

// compileVarStatement 编译 var 声明。
//
// 与 let 的根本分野: 绑定登记在**函数作用域层** (FuncLayer), 而不是当前块。
// 这一个落点同时给出 var 的三条语义:
//   - 块里声明的 var 在块外可见 (绑定在函数层);
//   - 重复声明合法 (函数层已有同名 → 复用绑定, 不报错不重复分配槽位);
//   - 提升 (compileStatements 在函数层 prescan 时已对无值绑定发射 undefined)。
//
// 无初始化器的 var 语句不发射任何指令: 提升初始化已把槽位写成 undefined,
// 而同名函数声明的提升值 (函数对象) 也不能被 var 语句覆盖。
// 解构 var 借用 let 的合成名路径: 绑定落点经 compileDestructureAssignment
// 的 declare 分支沿 FuncLayer 登记 (见 patternBindVar 辅助)。
func (c *Compiler) compileVarStatement(stmt *ast.VarStatement) error {
	// 解构: var [a, b] = arr / var { x } = obj
	if stmt.Name.Value == destructureSyntheticName {
		if assign, ok := stmt.Value.(*ast.AssignmentExpression); ok {
			return c.compileDestructureAssignment(assign, true)
		}
	}

	fn := c.scope.FuncLayer()

	declareVar := func(name string) (*Symbol, error) {
		if sym := fn.ResolveLocal(name); sym != nil {
			// let/const 与 var 同层同名 → SyntaxError;
			// var/var、var/fn、var/参数 → 复用绑定
			if !sym.IsVarLike && !sym.IsFnDecl {
				return nil, fmt.Errorf("SyntaxError: Identifier '%s' has already been declared", name)
			}
			if sym.IsConst {
				return nil, fmt.Errorf("SyntaxError: Identifier '%s' has already been declared", name)
			}
			return sym, nil
		}
		sym := fn.Define(name, false)
		sym.IsVarLike = true
		sym.Declared = true
		return sym, nil
	}

	emitAssign := func(name string, sym *Symbol) {
		// 全局函数层: var 是全局属性。提升初始化已 OP_DECLARE 过一次,
		// 这里必须走 STORE_GLOBAL (存在则赋值) —— 再发 OP_DECLARE 会撞
		// 运行时的重声明检查。
		if fn.Parent() == nil && !c.moduleMode {
			nameIdx := c.constants.AddConstant(object.NewString(name))
			c.emitter.Emit(bytecode.OP_STORE_GLOBAL, nameIdx)
		} else {
			c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
		}
	}

	if stmt.Value != nil {
		if err := c.compileExpression(stmt.Value); err != nil {
			return err
		}
		sym, err := declareVar(stmt.Name.Value)
		if err != nil {
			return err
		}
		emitAssign(stmt.Name.Value, sym)
	}
	for _, d := range stmt.More {
		if d.Value != nil {
			if err := c.compileExpression(d.Value); err != nil {
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
	// 解构赋值: const [a, b] = arr  或  const { x, y } = obj
	if stmt.Name.Value == "__destructure__" {
		if assign, ok := stmt.Value.(*ast.AssignmentExpression); ok {
			return c.compileDestructureAssignment(assign, true)
		}
	}

	if err := c.compileExpression(stmt.Value); err != nil {
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

	// 多条声明: const a = 1, b = 2;
	for _, d := range stmt.More {
		if err := c.compileExpression(d.Value); err != nil {
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

// allocFinallyRetSlot 惰性分配 (并全函数复用) 「穿 finally 的 return 值」暂存槽。
//
// 槽号取当前作用域链上所有层 nextSlot 的最大值 —— 活跃变量都落在各自作用域的
// nextSlot 之下, 因此该号必然空闲 (已结束的兄弟块复用同一批槽位不冲突: 它们的
// 变量早已出作用域)。分配后把链上每层的 nextSlot 都抬到它之上, 否则之后在同一
// 链上新建的作用域会再次发出同一个槽号。
func (c *Compiler) allocFinallyRetSlot() int {
	if c.finallyRetSlot >= 0 {
		return c.finallyRetSlot
	}
	slot := 0
	for s := c.scope; s != nil; s = s.Parent() {
		if s.nextSlot > slot {
			slot = s.nextSlot
		}
		if s.IsFuncLayer() {
			break
		}
	}
	c.finallyRetSlot = slot
	next := slot + 1
	for s := c.scope; s != nil; s = s.Parent() {
		if s.nextSlot < next {
			s.nextSlot = next
		}
		if s.IsFuncLayer() {
			break
		}
	}
	return slot
}

// emitTryUnwind 生成「从当前控制点退出到 targetDepth 层 try 之外」的收尾代码:
// 由内向外逐个 POP_TRY, 并对带 finally 的条目**内联执行 finally 体**。
// 调用方随后必须紧接着发真正的转移指令 (OP_RETURN / OP_JUMP / OP_LOOP)。
//
// finally 体在这里被重复编译一份 —— 这是有意的: finally 何时执行取决于控制
// 转移点, 而字节码没有「子程序返回」原语。重复编译对声明是安全的:
//   - let/const/class/function 声明落在各自新开的块作用域里, 互不干扰;
//   - var 绑定已在函数入口统一 hoist 并标记 IsVarLike, 再次编译走复用分支
//     (见 compileVarStatement 的 declareVar), 不会误报重复声明。
func (c *Compiler) emitTryUnwind(targetDepth int) error {
	for i := len(c.tryScopes) - 1; i >= targetDepth; i-- {
		sc := c.tryScopes[i]
		// 先摘掉本条目: finally 体里再抛异常时不该重新进入自己。
		c.emitter.EmitNoOperand(bytecode.OP_POP_TRY)
		if !sc.hasFinally || sc.finallyBody == nil {
			continue
		}
		// 内联编译 finally 体。此刻本条目及更内层条目已「退出」, 故把视角截到
		// i 层 —— finally 体里若还有 return/break/continue, 它只需再收尾
		// 0..i-1 这些仍活跃的 try。
		// 用「满切片表达式」saved[:i:i] 把容量也压到 i, 免得递归编译中途
		// append 把新条目写回 saved 的底层数组、污染外层视角。
		saved := c.tryScopes
		c.tryScopes = saved[:i:i]
		err := c.compileBlockStatement(sc.finallyBody)
		c.tryScopes = saved
		if err != nil {
			return err
		}
	}
	return nil
}

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

	if err := c.compileStatements(block.Statements); err != nil {
		return err
	}

	c.scope = prevScope
	c.emitter.EmitNoOperand(bytecode.OP_POP_SCOPE)
	return nil
}

// compileCatchBodyWithParam 把栈顶异常值绑定到 catch 参数并编译 catch 体。
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
func (c *Compiler) compileCatchBodyWithParam(param *ast.Identifier, body *ast.BlockStatement) error {
	c.emitter.EmitNoOperand(bytecode.OP_PUSH_SCOPE)
	prevScope := c.scope
	c.scope = NewSymbolScope(prevScope)
	sym := c.scope.Define(param.Value, false)
	c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
	prevScope.nextSlot = c.scope.nextSlot

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

	ctx := c.pushControl(c.takePendingLabel(), true)

	for _, s := range stmt.Body.Statements {
		if err := c.compileStatement(s); err != nil {
			return err
		}
	}

	c.scope = prevScope
	c.emitter.EmitNoOperand(bytecode.OP_POP_SCOPE)

	// 迭代边界: 提交本轮创建的闭包 (body 内 let 的 per-iteration 语义)
	c.emitter.EmitNoOperand(bytecode.OP_ITER_BOUNDARY)
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

	ctx := c.pushControl(c.takePendingLabel(), true)

	// 先执行循环体
	for _, s := range stmt.Body.Statements {
		if err := c.compileStatement(s); err != nil {
			return err
		}
	}

	c.scope = prevScope
	c.emitter.EmitNoOperand(bytecode.OP_POP_SCOPE)

	// continue 跳到条件检查 (body 之后); 此处同时是迭代边界
	c.emitter.EmitNoOperand(bytecode.OP_ITER_BOUNDARY)
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

func (c *Compiler) compileForOfStatement(stmt *ast.ForOfStatement) error {
	if stmt.Await {
		return c.compileForAwaitOfStatement(stmt)
	}
	// 编译可迭代对象
	if err := c.compileExpression(stmt.Iterable); err != nil {
		return err
	}
	// 获取迭代器
	c.emitter.EmitNoOperand(bytecode.OP_GET_ITERATOR)

	loopStart := c.emitter.Pos()
	// 迭代下一步
	c.emitter.EmitNoOperand(bytecode.OP_ITER_NEXT)
	// 栈顶是值或 undefined (迭代结束)

	// 检查是否迭代结束
	c.emitter.EmitNoOperand(bytecode.OP_DUP)
	endJump := c.emitter.EmitJump(bytecode.OP_JUMP_IF_NULL)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出检查的 undefined

	// 块作用域
	c.emitter.EmitNoOperand(bytecode.OP_PUSH_SCOPE)
	prevScope := c.scope
	c.scope = NewSymbolScope(prevScope)

	// 绑定本次迭代的值 (栈顶)。三种形状:
	//   简单绑定 for (let x of arr)          → 直接存进新建的槽位
	//   解构绑定 for (const [a, b] of pairs) → 交给 compilePatternBind 按模式拆开
	//   var 绑定 for (var x of arr)          → 存进**函数作用域层**的共享槽位
	// 两边进来时栈都是 [.., value]、离开时都回到 [..] —— compilePatternBind 末尾
	// 自带 POP, 与 OP_STORE 消耗栈顶值的语义对齐。
	varKind := bytecode.OP_STORE
	if _, ok := stmt.VarDecl.(*ast.ConstStatement); ok {
		varKind = bytecode.OP_STORE_CONST
	}
	if stmt.Pattern != nil {
		// 解构绑定 vs 解构赋值目标, 靠 VarDecl 是否为 nil 区分:
		//   声明绑定 for (const [a, b] of pairs) → VarDecl 是 __destructure__
		//     空壳, isDecl=true: 每轮迭代是新的块作用域 (上面已 PUSH_SCOPE),
		//     解构出来的名字声明进这个作用域, 所以各轮的绑定互不影响 ——
		//     闭包捕获到的是各自的槽位。
		//   赋值目标 for ([a, b] of xs) → VarDecl 为 nil, isDecl=false:
		//     每轮是**赋值**, 写入外部已声明的绑定 (node 实测: for ([a,b] of …)
		//     修改的就是外层 a/b)。
		//
		// 注: 解构出来的名字按 let 语义登记 (compilePatternBind 内部是
		// declareOnce(…, false, …)), 与 const [a, b] = … 的现有口径一致。本运行时
		// 的 const **只在全局词法绑定上强制** (见 vm.go 的 OP_STORE_GLOBAL);
		// 局部槽位根本不查 (compiler.Symbol.IsConst 目前无人读取), 所以
		// for (const [a, b] of …) 的 a/b 可被重新赋值 —— 这是既有边界, 不是本次
		// 解构支持引入的。
		if err := c.compilePatternBind(stmt.Pattern, stmt.VarDecl != nil); err != nil {
			return err
		}
	} else if stmt.Target != nil {
		// 无声明赋值目标 for (x of xs) / for (obj.k of xs): 每轮迭代是
		// 赋值, 与 var 形态一样写**外部**绑定 (不是每轮新声明)
		if err := c.compileForOfTargetAssign(stmt.Target); err != nil {
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

	ctx := c.pushControl(c.takePendingLabel(), true)

	// 循环体
	for _, s := range stmt.Body.Statements {
		if err := c.compileStatement(s); err != nil {
			return err
		}
	}

	c.scope = prevScope
	c.emitter.EmitNoOperand(bytecode.OP_POP_SCOPE)

	// continue 跳回迭代头 (经过迭代边界: 每次迭代的绑定互不影响)
	c.emitter.EmitNoOperand(bytecode.OP_ITER_BOUNDARY)
	iterPos := c.emitter.Pos()
	for _, jmp := range ctx.continueJumps {
		c.emitter.ReplaceJumpTarget(jmp, uint16(iterPos))
	}
	c.emitter.Emit(bytecode.OP_LOOP, uint16(loopStart))

	// 迭代结束: 清理栈上的残留值
	c.emitter.PatchJump(endJump)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出 DUP 的副本 (null/undefined)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出 ITER_NEXT 的值

	// break 跳到这里 → 栈上只剩迭代器
	for _, jmp := range ctx.breakJumps {
		c.emitter.PatchJump(jmp)
	}
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出迭代器

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
//	[iter]
//	ASYNC_ITER_NEXT      → [iter, step]     step = next() 结果 (可能 Promise)
//	OP_YIELD             → [iter, step']    Promise 时 step' = resolve 值;
//	                                           同步形状 __spawn 原样回传, 同一位置
//	.step.done           → 真则跳出 (break 也汇到这里, 一路只弹 iter)
//	.step.value          → 绑定给头部 (复用同步 for-of 的三形状绑定代码)
//
// 「await 检查」: parser 不跟踪 async 上下文 (见 parseForAwaitOfStatement
// 注释), 编译期校验 —— 当前函数不是 async 时报 SyntaxError (规范上 for-await
// 体外是早错)。
func (c *Compiler) compileForAwaitOfStatement(stmt *ast.ForOfStatement) error {
	if !c.inAsyncFunction {
		return fmt.Errorf("compiler: SyntaxError: 'for await...of' is only allowed inside an async function")
	}

	// 编译可迭代表达式并取异步迭代器
	if err := c.compileExpression(stmt.Iterable); err != nil {
		return err
	}
	c.emitter.EmitNoOperand(bytecode.OP_GET_ASYNC_ITERATOR)

	loopStart := c.emitter.Pos()
	// 异步迭代一步: [iter] → [iter, step]; OP_YIELD 等待 Promise
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

	// 块作用域 (与同步 for-of 一致: 每轮新绑定)
	c.emitter.EmitNoOperand(bytecode.OP_PUSH_SCOPE)
	prevScope := c.scope
	c.scope = NewSymbolScope(prevScope)

	// 取 value 并按三形状绑定 (与 compileForOfStatement 同构, 栈约定一致:
	// 进来 [iter, step] 栈顶是 step, GET_PROP value 换成要绑定的值)
	valueIdx := c.constants.AddConstant(object.NewString("value"))
	c.emitter.Emit(bytecode.OP_GET_PROP, valueIdx) // [iter, value]

	varKind := bytecode.OP_STORE
	if _, ok := stmt.VarDecl.(*ast.ConstStatement); ok {
		varKind = bytecode.OP_STORE_CONST
	}
	if stmt.Pattern != nil {
		// 与同步 for-of 同口径: VarDecl 为 nil 的解构是**赋值**目标
		if err := c.compilePatternBind(stmt.Pattern, stmt.VarDecl != nil); err != nil {
			return err
		}
	} else if stmt.Target != nil {
		// for await (x of xs) / for await (obj.k of xs): 每轮赋值给外部绑定
		if err := c.compileForOfTargetAssign(stmt.Target); err != nil {
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
			nameIdx := c.constants.AddConstant(object.NewString(stmt.Variable.Value))
			c.emitter.Emit(bytecode.OP_STORE_GLOBAL, nameIdx)
		} else {
			c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
		}
	} else {
		sym := c.scope.Define(stmt.Variable.Value, varKind == bytecode.OP_STORE_CONST)
		c.emitter.Emit(varKind, uint16(sym.Slot))
	}

	ctx := c.pushControl(c.takePendingLabel(), true)

	// 循环体
	for _, s := range stmt.Body.Statements {
		if err := c.compileStatement(s); err != nil {
			return err
		}
	}

	c.scope = prevScope
	c.emitter.EmitNoOperand(bytecode.OP_POP_SCOPE)

	// continue 跳回异步迭代头
	c.emitter.EmitNoOperand(bytecode.OP_ITER_BOUNDARY)
	iterPos := c.emitter.Pos()
	for _, jmp := range ctx.continueJumps {
		c.emitter.ReplaceJumpTarget(jmp, uint16(iterPos))
	}
	c.emitter.Emit(bytecode.OP_LOOP, uint16(loopStart))

	// done 为真路径跳到这里: [iter, step, done] → POP×2 → [iter]
	c.emitter.PatchJump(endJump)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出 done (JUMP_IF_TRUE 不弹)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出 step

	// break 也汇到这里: 循环体内无残留, 栈上只剩 [iter]
	for _, jmp := range ctx.breakJumps {
		c.emitter.PatchJump(jmp)
	}
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出 iter (两条路径共用)

	c.popControl()
	return nil
}

// compileForOfTargetAssign 把栈顶的本轮迭代值赋给 for-of 的无声明赋值目标。
// 进来 [.., value], 离开 [..] (值被消耗, 与 OP_STORE 的语义对齐)。
// 每轮迭代是**赋值**而非声明: 写外部已有绑定, 未声明时隐式全局 (与 x = v
// 的 sloppy 口径一致)。
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

	// 声明循环变量
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

	ctx := c.pushControl(c.takePendingLabel(), true)

	// 循环体
	for _, s := range stmt.Body.Statements {
		if err := c.compileStatement(s); err != nil {
			return err
		}
	}

	c.scope = prevScope
	c.emitter.EmitNoOperand(bytecode.OP_POP_SCOPE)

	// 迭代边界: 本轮迭代创建的闭包定版
	c.emitter.EmitNoOperand(bytecode.OP_ITER_BOUNDARY)
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
	c.emitter.EmitNoOperand(bytecode.OP_PUSH_SCOPE)
	prevScope := c.scope
	c.scope = NewSymbolScope(prevScope)

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
		if err := c.compileStatements(stmt.Body.Statements); err != nil {
			return err
		}
		c.scope = bodyScope
		c.emitter.EmitNoOperand(bytecode.OP_POP_SCOPE)

		// continue 跳到 update; 此处同时是迭代边界 (per-iteration 绑定)
		c.emitter.EmitNoOperand(bytecode.OP_ITER_BOUNDARY)
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
		if err := c.compileStatements(stmt.Body.Statements); err != nil {
			return err
		}
		c.scope = bodyScope
		c.emitter.EmitNoOperand(bytecode.OP_POP_SCOPE)

		// continue 跳到 update; 此处同时是迭代边界 (per-iteration 绑定)
		c.emitter.EmitNoOperand(bytecode.OP_ITER_BOUNDARY)
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
	c.tryScopes = append(c.tryScopes, tryScope{hasFinally: hasFinally, finallyBody: stmt.FinallyBody})
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
			c.tryScopes = append(c.tryScopes, tryScope{hasFinally: true, finallyBody: stmt.FinallyBody})
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
	if err := c.emitTryUnwind(ctx.tryScopes); err != nil {
		return err
	}
	ctx.continueJumps = append(ctx.continueJumps, c.emitter.EmitJump(bytecode.OP_LOOP))
	return nil
}

func (c *Compiler) compileLabeledStatement(stmt *ast.LabeledStatement) error {
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
func (c *Compiler) compileClassDeclaration(node *ast.ClassDeclaration) error {
	if err := c.compileClassBody(node.Name.Value, node.SuperClass, node.Methods, node.Statics, node.Fields); err != nil {
		return err
	}
	// 类值在栈顶 → 声明类名并绑定
	className := node.Name.Value
	sym, err := c.declareOnce(className, true, false)
	if err != nil {
		return err
	}
	if c.isGlobalScope() && !c.moduleMode {
		nameIdx := c.constants.AddConstant(object.NewString(className))
		c.emitter.Emit(bytecode.OP_DECLARE, nameIdx)
	} else {
		c.emitter.Emit(bytecode.OP_STORE_CONST, uint16(sym.Slot))
	}
	return nil
}

// compileClassBody 编译 class 声明/表达式共有的主体部分
// (constructor + prototype + 实例/静态方法 + extends)。
// 返回时类值 (constructor) 在栈顶。
// privateKey 把裸私有名 (#x 形式或已去 # 的裸名) 编成混编码属性键。
// name 容忍带/不带 # 两种形式。
func (c *Compiler) privateKey(name string) string {
	n := name
	if len(n) > 0 && n[0] == '#' {
		n = n[1:]
	}
	if c.currentPrivatePrefix == "" {
		// 不在类体内: 拿不到前缀, 交给调用方报错; 这里给个占位避免空键
		return "\x00<nowhere>:" + n
	}
	return "\x00" + c.currentPrivatePrefix + ":" + n
}

// emitPrivateKey 发射私有键字符串常量 (栈上多一个 key)。
func (c *Compiler) emitPrivateKey(name string) error {
	if c.currentPrivatePrefix == "" {
		n := name
		if len(n) > 0 && n[0] == '#' {
			n = n[1:]
		}
		return fmt.Errorf("compiler: '#%s' is not allowed outside class", n)
	}
	idx := c.constants.AddConstant(object.NewString(c.privateKey(name)))
	c.emitter.Emit(bytecode.OP_CONST, idx)
	return nil
}

func (c *Compiler) compileClassBody(className string, superClass ast.Expression, methods, statics []*ast.ClassMethod, fields []*ast.ClassField) error {
	// 父类名 (super 引用目标; 仅支持 Identifier 形式的 extends)
	superName := ""
	if superClass != nil {
		if ident, ok := superClass.(*ast.Identifier); ok {
			superName = ident.Value
		} else {
			return fmt.Errorf("compiler: class extends must reference an identifier")
		}
	}

	// 找到 constructor (显式或默认)
	var ctor *ast.ClassMethod
	for _, m := range methods {
		if m.IsConstructor {
			ctor = m
			break
		}
	}

	// 私有名前缀: 类名 + 全局序号保证唯一 (同名类/嵌套类互不串槽)。
	// 序号只用于键混编, 不影响任何可观察行为。
	c.privateClassSeq++
	prevPrefix := c.currentPrivatePrefix
	c.currentPrivatePrefix = fmt.Sprintf("%s\x01%d", className, c.privateClassSeq)

	// 编译 constructor
	prevSuper := c.currentSuperClass
	c.currentSuperClass = superName
	ctorMeta, err := c.compileClassConstructor(fields, ctor, className, superName)
	if err != nil {
		return err
	}
	c.currentSuperClass = prevSuper
	ctorIdx := c.constants.AddConstant(ctorMeta)
	c.emitter.Emit(bytecode.OP_FUNCTION, ctorIdx) // [ctor]

	// 创建 prototype 对象
	c.emitter.EmitNoOperand(bytecode.OP_NEW_OBJECT) // [ctor, proto]

	// 实例方法挂到 prototype
	for _, m := range methods {
		if m.IsConstructor {
			continue
		}
		// 私有方法/访问器: 挂 proto 但用混编码键 (this.#m() 调用走同键 GET_INDEX)
		if m.IsPrivate && m.Body != nil {
			prevSuperP := c.currentSuperClass
			c.currentSuperClass = superName
			meta, err := c.compileFunction(m.Name, m.Parameters, m.Body, false, m.IsGenerator, m.IsAsync)
			c.currentSuperClass = prevSuperP
			if err != nil {
				return err
			}
			midx := c.constants.AddConstant(meta)
			c.emitter.Emit(bytecode.OP_FUNCTION, midx) // [ctor, proto, fn]
			pkeyIdx := c.constants.AddConstant(object.NewString(c.privateKey(m.Name)))
			switch {
			case m.IsGetter:
				c.emitter.Emit(bytecode.OP_SET_GETTER, pkeyIdx)
			case m.IsSetter:
				c.emitter.Emit(bytecode.OP_SET_SETTER, pkeyIdx)
			default:
				c.emitter.Emit(bytecode.OP_SET_PROP, pkeyIdx) // [ctor, proto]
			}
			continue
		}
		if err := c.compileClassMethodToObject(m, superName); err != nil {
			return err
		}
	} // [ctor, proto]

	// extends: proto.Proto = SuperClass.prototype
	if superName != "" {
		// 栈: [ctor, proto] → LOAD_GLOBAL Super → GET_PROP prototype → [ctor, proto, parent]
		c.emitSuperLoad(superName)
		pidx := c.constants.AddConstant(object.NewString("prototype"))
		c.emitter.Emit(bytecode.OP_GET_PROP, pidx)
		// OP_SET_PROTO: 弹出 parent, 设置下方对象 (proto) 的原型 → [ctor, proto]
		c.emitter.EmitNoOperand(bytecode.OP_SET_PROTO)
	}

	// ctor.prototype = proto → [ctor]
	pidx := c.constants.AddConstant(object.NewString("prototype"))
	c.emitter.Emit(bytecode.OP_SET_PROP, pidx)

	// 静态方法挂到 ctor
	for _, m := range statics {
		// 静态字段 (static f = expr): 解析器把它放进 Statics 且 Body 为 nil。
		// 字段初始化语义尚未实现 —— 此处跳过而不是 nil deref 崩掉编译进程
		// (崩进程会让 test262 分片子进程整片孤儿)。
		if m.Body == nil {
			// 静态私有字段 (static #x = v): 挂到 ctor, 键混编码。
			if m.IsPrivate {
				if err := c.compileExpression(m.FieldValue); err != nil {
					return err
				}
				keyIdx := c.constants.AddConstant(object.NewString(c.privateKey(m.Name)))
				c.emitter.Emit(bytecode.OP_SET_PROP, keyIdx) // [ctor]
			}
			continue
		}
		// 编译静态方法函数
		prevSuper2 := c.currentSuperClass
		c.currentSuperClass = superName
		meta, err := c.compileFunction(m.Name, m.Parameters, m.Body, false, m.IsGenerator, m.IsAsync)
		if err != nil {
			return err
		}
		c.currentSuperClass = prevSuper2
		midx := c.constants.AddConstant(meta)
		if m.ComputedKey != nil && !m.IsGetter && !m.IsSetter {
			// 动态键静态方法: SET_INDEX 弹 [obj, key, val] 三元组,
			// 先 DUP ctor 让写入消耗副本 (getter/setter 走 DYN 弹2保留 obj, 不需要)。
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
		}
		c.emitter.Emit(bytecode.OP_FUNCTION, midx) // [ctor, fn]
		if m.ComputedKey != nil {
			// 动态键静态方法: [ctor(, ctor), fn] → key → DYN 访问器/SET_INDEX
			if err := c.compileExpression(m.ComputedKey); err != nil {
				return err
			}
			switch {
			case m.IsGetter:
				// [ctor, fn, key] → SET_GETTER_DYN(弹2留obj) → [ctor]
				c.emitter.EmitNoOperand(bytecode.OP_SET_GETTER_DYN)
			case m.IsSetter:
				c.emitter.EmitNoOperand(bytecode.OP_SET_SETTER_DYN)
			default:
				c.emitter.EmitNoOperand(bytecode.OP_SWAP)
				c.emitter.EmitNoOperand(bytecode.OP_SET_INDEX)
				c.emitter.EmitNoOperand(bytecode.OP_POP)
			}
			continue
		}
		keyIdx := c.constants.AddConstant(object.NewString(m.Name))
		switch {
		case m.IsGetter:
			c.emitter.Emit(bytecode.OP_SET_GETTER, keyIdx) // [ctor]
		case m.IsSetter:
			c.emitter.Emit(bytecode.OP_SET_SETTER, keyIdx) // [ctor]
		default:
			c.emitter.Emit(bytecode.OP_SET_PROP, keyIdx) // [ctor]
		}
	}
	c.currentPrivatePrefix = prevPrefix
	return nil
}

// compileClassExpression 编译 class 表达式。
// 与声明的差别: 类名不进作用域 (匿名类 Name 为 nil), 类值直接
// 作为表达式结果留在栈顶。
func (c *Compiler) compileClassExpression(node *ast.ClassExpression) error {
	className := "<anonymous>"
	if node.Name != nil {
		className = node.Name.Value
	}
	return c.compileClassBody(className, node.SuperClass, node.Methods, node.Statics, node.Fields)
}

// compileClassConstructor 编译 class 的 constructor 函数。
// 若无显式 constructor 则生成默认构造。
// 实例字段赋值指令插入 constructor 开头。
func (c *Compiler) compileClassConstructor(fields []*ast.ClassField, ctor *ast.ClassMethod, className, superName string) (*bytecode.FunctionMetadata, error) {
	prevScope := c.scope
	baseSlot := prevScope.NumLocals()
	fnScope := NewFunctionScope(prevScope)
	c.scope = fnScope

	paramSpecs := []bytecode.ParameterSpec{}
	paramSlots := []int{}
	if ctor != nil {
		for _, param := range ctor.Parameters {
			paramSpecs = append(paramSpecs, bytecode.ParameterSpec{Name: param.Name, HasDefault: param.Default != nil, IsRest: param.Rest})
			sym := fnScope.Define(param.Name, false)
			sym.Declared = true  // 参数是真实声明: 函数体内 let 同名 → SyntaxError
			sym.IsVarLike = true // 参数即 var 绑定: 函数体内 var 同名复用此绑定
			paramSlots = append(paramSlots, sym.Slot)
		}
	}

	// 预留 arguments 槽位
	argSym := fnScope.Define("__arguments__", false)
	argSym.Declared = true
	argumentsSlot := argSym.Slot
	prevArgumentsSlot := c.currentArgumentsSlot
	c.currentArgumentsSlot = argumentsSlot

	// 编译函数体
	prevEmitter := c.emitter
	c.emitter = NewEmitter()
	// 函数体的 srcPositions 是独立 offset 空间 (独立 emitter), 切换到新表;
	// 编译完存进 FunctionMetadata.Positions, 与外层表互不污染 (T05)。
	prevSrcPositions := c.srcPositions
	c.srcPositions = nil
	prevControlStack := c.controlStack
	c.controlStack = nil
	prevPendingLabel := c.pendingLabel
	c.pendingLabel = ""
	prevTryScopes := c.tryScopes
	c.tryScopes = nil
	prevFinallyRetSlot := c.finallyRetSlot
	c.finallyRetSlot = -1
	// defer: 保证错误早退路径也恢复, 否则 compileTryStatement 的 [:len-1] 会 panic (rCzckg 回归)。
	defer func() {
		c.tryScopes = prevTryScopes
		c.finallyRetSlot = prevFinallyRetSlot
	}()

	// 隐式 constructor + 有父类: 语义等价于 constructor(...args){ super(...args) }。
	// 必须先转发父构造 (父类实例字段 + 父构造体) 再跑本类字段初始化 —— 顺序与规范
	// 一致 (父字段先于子字段)。旧实现隐式构造是空体, 父类实例字段从不初始化
	// (静默全 undefined) —— 即 rCzckg。
	if ctor == nil && superName != "" {
		// [fn] → [fn, this] → [fn, this, arguments] → CALL_METHOD_SPREAD
		c.emitSuperLoad(superName)
		c.emitter.EmitNoOperand(bytecode.OP_THIS)
		c.emitter.Emit(bytecode.OP_LOAD, uint16(argumentsSlot))
		c.emitter.EmitNoOperand(bytecode.OP_CALL_METHOD_SPREAD)
		// super() 的返回值 (父构造结果) 丢弃: 隐式构造体里没有表达式语句层。
		c.emitter.EmitNoOperand(bytecode.OP_POP)
	}

	// 实例字段赋值: this.field = value / this[expr] = value
	for _, field := range fields {
		c.emitter.EmitNoOperand(bytecode.OP_THIS)
		if field.IsPrivate {
			// 私有字段: this[#name] = value —— 键是混编码字符串常量
			// (\x00<prefix>:<name>), 外部任何常规访问都摸不到。
			if field.Value != nil {
				if err := c.compileExpression(field.Value); err != nil {
					return nil, err
				}
			} else {
				c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
			}
			if err := c.emitPrivateKey(field.Name); err != nil {
				return nil, err
			}
			// [this, val, key] → SWAP → [this, key, val] → SET_INDEX → [val] → POP
			c.emitter.EmitNoOperand(bytecode.OP_SWAP)
			c.emitter.EmitNoOperand(bytecode.OP_SET_INDEX)
			c.emitter.EmitNoOperand(bytecode.OP_POP)
			continue
		}
		if field.ComputedKey != nil {
			// 动态键字段: SET_INDEX 弹 [obj, key, val] 三元组, 需先 DUP this
			// 让写入消耗副本、原 this 留在栈底 (constructor 栈约定)。
			// [this] → DUP → [this, this] → val → key → SWAP → [this, this, key, val]
			// → SET_INDEX(弹3压1) → [this, val] → POP → [this]
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
			if field.Value != nil {
				if err := c.compileExpression(field.Value); err != nil {
					return nil, err
				}
			} else {
				c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
			}
			if err := c.compileExpression(field.ComputedKey); err != nil {
				return nil, err
			}
			c.emitter.EmitNoOperand(bytecode.OP_SWAP)
			c.emitter.EmitNoOperand(bytecode.OP_SET_INDEX)
			c.emitter.EmitNoOperand(bytecode.OP_POP)
			continue
		}
		if field.Value != nil {
			if err := c.compileExpression(field.Value); err != nil {
				return nil, err
			}
		} else {
			c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
		}
		keyIdx := c.constants.AddConstant(object.NewString(field.Name))
		c.emitter.Emit(bytecode.OP_SET_PROP, keyIdx)
	}

	// constructor 体
	// 防御: ctor 理论上恒有 Body (parseClassMember 只在 '(' 形状下置
	// IsConstructor), 但历史上有字段名 constructor 混进来的形状
	// (IsConstructor=true + Body=nil, 2026-10-05 修于 parser 层);
	// 这里 nil 检查保证再出现时是编译错而不是进程 panic。
	if ctor != nil && ctor.Body != nil {
		if err := c.compileStatements(ctor.Body.Statements); err != nil {
			return nil, err
		}
	}
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

	meta := bytecode.NewFunctionMetadata("constructor", fnIns, fnScope.NumLocals(), len(paramSpecs), paramSpecs, false)
	meta.BaseSlot = baseSlot
	meta.ArgumentsSlot = argumentsSlot
	meta.Positions = toSrcPosList(fnSrcPositions)
	_ = paramSlots
	return meta, nil
}

// compileClassMethodToObject 将 class 实例方法编译为函数并挂到栈顶下方的对象上。
// 栈: [..., obj] → 编译方法函数 → [..., obj, fn] → SET_PROP/SETTER → [..., obj]
func (c *Compiler) compileClassMethodToObject(m *ast.ClassMethod, superName string) error {
	prevSuper := c.currentSuperClass
	c.currentSuperClass = superName
	meta, err := c.compileFunction(m.Name, m.Parameters, m.Body, false, m.IsGenerator, m.IsAsync)
	if err != nil {
		return err
	}
	c.currentSuperClass = prevSuper
	midx := c.constants.AddConstant(meta)
	if m.ComputedKey != nil && !m.IsGetter && !m.IsSetter {
		// 动态键普通方法: SET_INDEX 弹 [obj, key, val] 三元组,
		// 先 DUP obj 让写入消耗副本 (getter/setter 走 DYN 弹2保留 obj, 不需要)。
		c.emitter.EmitNoOperand(bytecode.OP_DUP)
	}
	c.emitter.Emit(bytecode.OP_FUNCTION, midx)
	if m.ComputedKey != nil {
		// 动态键: 栈 [obj(, obj), fn] → key → [obj, (obj,) fn, key] → DYN 访问器/SET_INDEX
		if err := c.compileExpression(m.ComputedKey); err != nil {
			return err
		}
		switch {
		case m.IsGetter:
			c.emitter.EmitNoOperand(bytecode.OP_SET_GETTER_DYN)
		case m.IsSetter:
			c.emitter.EmitNoOperand(bytecode.OP_SET_SETTER_DYN)
		default:
			c.emitter.EmitNoOperand(bytecode.OP_SWAP) // [obj, fn, key] → [obj, key, fn] 对齐 SET_INDEX 栈序
			c.emitter.EmitNoOperand(bytecode.OP_SET_INDEX)
			c.emitter.EmitNoOperand(bytecode.OP_POP)
		}
		return nil
	}
	propKey := m.Name
	if m.IsPrivate {
		propKey = c.privateKey(m.Name)
	}
	keyIdx := c.constants.AddConstant(object.NewString(propKey))
	if m.IsGetter {
		c.emitter.Emit(bytecode.OP_SET_GETTER, keyIdx)
	} else if m.IsSetter {
		c.emitter.Emit(bytecode.OP_SET_SETTER, keyIdx)
	} else {
		c.emitter.Emit(bytecode.OP_SET_PROP, keyIdx)
	}
	return nil
}

func (c *Compiler) compileImportDeclaration(stmt *ast.ImportDeclaration) error {
	// 先校验"从内置模块导入的名字" (不发射任何指令就返回错误, 不留半截字节码)
	if err := checkBuiltinImportNames(stmt); err != nil {
		return err
	}
	// 编译模块导入: 加载模块并绑定导出
	// OP_IMPORT 操作数 = 模块路径常量索引
	sourceIdx := c.constants.AddConstant(object.NewString(stmt.Source))
	c.emitter.Emit(bytecode.OP_IMPORT, sourceIdx)

	// 栈顶现在是模块的导出对象
	// 绑定导入
	if stmt.Namespace != "" {
		// import * as ns from "..."
		sym, err := c.declareOnce(stmt.Namespace, false, false)
		if err != nil {
			return err
		}
		if c.isGlobalScope() {
			c.emitGlobalDeclare(stmt.Namespace)
		} else {
			c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
		}
	} else if stmt.DefaultName != "" {
		// import defaultName from "..."
		// 获取 default 导出
		idx := c.constants.AddConstant(object.NewString("default"))
		c.emitter.Emit(bytecode.OP_GET_PROP, idx)
		sym, err := c.declareOnce(stmt.DefaultName, false, false)
		if err != nil {
			return err
		}
		if c.isGlobalScope() {
			c.emitGlobalDeclare(stmt.DefaultName)
		} else {
			c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
		}
		if len(stmt.NamedImports) > 0 {
			// 还需要命名导入
			// 重新加载模块 (或 DUP)
			c.emitter.Emit(bytecode.OP_IMPORT, sourceIdx)
			for _, item := range stmt.NamedImports {
				c.emitter.EmitNoOperand(bytecode.OP_DUP)
				// 取的是**模块导出名**, 绑定的是**本地名**
				nameIdx := c.constants.AddConstant(object.NewString(item.Imported))
				c.emitter.Emit(bytecode.OP_GET_PROP, nameIdx)
				sym, err := c.declareOnce(item.Local, false, false)
				if err != nil {
					return err
				}
				if c.isGlobalScope() {
					c.emitGlobalDeclare(item.Local)
				} else {
					c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
				}
			}
			c.emitter.EmitNoOperand(bytecode.OP_POP)
		}
	} else if len(stmt.NamedImports) > 0 {
		// import { a, b as c } from "..."
		for _, item := range stmt.NamedImports {
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
			// 取的是**模块导出名**, 绑定的是**本地名**
			nameIdx := c.constants.AddConstant(object.NewString(item.Imported))
			c.emitter.Emit(bytecode.OP_GET_PROP, nameIdx)
			sym, err := c.declareOnce(item.Local, false, false)
			if err != nil {
				return err
			}
			if c.isGlobalScope() {
				c.emitGlobalDeclare(item.Local)
			} else {
				c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
			}
		}
		c.emitter.EmitNoOperand(bytecode.OP_POP)
	} else {
		// 副作用导入: 丢弃模块对象
		c.emitter.EmitNoOperand(bytecode.OP_POP)
	}

	return nil
}

// ===== 内置模块的命名导入校验 =====

// checkBuiltinImportNames 校验"从内置模块导入的名字"确实在那个模块的导出表里。
//
// 命名导入在运行时只是一次 GET_PROP, 名字错了就**静默拿到 undefined** ——
// 症状离原因很远, 实测踩到的两类:
//
//	import { alert } from "gx/gfx";   // alert 在 gx/dialog ⇒ 调用时 "alert is not a function"
//	import { each } from "gx/view";   // each 是元素级指令, 没有任何模块导出它 ⇒ 静默失效
//	import x from "gox";              // 内置模块没有 default ⇒ x 恒为 undefined
//
// 内置模块的导出表在编译进程里是现成的 (object 注册表), 所以这条能在编译期拦住
// —— 报错里带上"它在哪个模块 / 是不是元素级指令 / 是不是拼错"。
//
// **只对已注册的内置模块生效**: 宿主没链接那个模块 (导出表不可知) 与文件模块
// 一律放行。也就是说这个检查只会让原本"编译通过但运行时静默出错"的程序变成
// 编译期报错, 不会改变任何能正常工作的程序的字节码。
func checkBuiltinImportNames(stmt *ast.ImportDeclaration) error {
	// 与 vm.loadModule 的保留命名空间一致: "gox" 与 "gx/..." 才是内置模块
	if stmt.Source != "gox" && !strings.HasPrefix(stmt.Source, "gx/") {
		return nil
	}
	exports, ok := object.LookupBuiltinModule(stmt.Source)
	if !ok {
		// 没注册 (宿主没链接 / 名字写错): 交给运行时 loadModule 报"未知的内置模块",
		// 那里会列出全部可用的模块名, 信息更全
		return nil
	}
	for _, item := range stmt.NamedImports {
		// 拿**模块导出名**去核对 —— 别名 (item.Local) 是本文件自己的名字,
		// 与模块导出表无关。若用别名核对, `{ createSignal as cs }` 会误报
		// "没有导出 cs"; 若把别名拆成多个 string, 则会误报 `没有导出 "as"`。
		name := item.Imported
		if _, ok := exports[name]; ok {
			continue
		}
		// 带别名时把本地名也报出来, 否则用户对着源码里的 `as cs` 会困惑
		// 报错为什么谈的是另一个名字。
		localHint := ""
		if item.Local != "" && item.Local != name {
			localHint = fmt.Sprintf(" (本地名 %s)", item.Local)
		}
		return fmt.Errorf("import {%s%s} from \"%s\": %s 没有导出 %q%s",
			name, localHint, stmt.Source, stmt.Source, name, importMissHint(stmt.Source, name, exports))
	}
	if stmt.DefaultName != "" {
		if _, ok := exports["default"]; !ok {
			return fmt.Errorf("import %s from \"%s\": 内置模块没有 default 导出%s",
				stmt.DefaultName, stmt.Source, importMissHint(stmt.Source, "default", exports))
		}
	}
	return nil
}

// importMissStaticHints 收那些"根本不是导出、但最容易被 import"的名字。
// 它们不在任何模块的导出表里, 所以只能在名字上硬编码 (与跨模块提示互斥:
// 跨模块命中的名字不会走到这里)。
var importMissStaticHints = map[string]string{
	"each":     "元素级指令: 写成 JSX 属性 each={rows}, 不从模块 import",
	"show":     "元素级指令: 写成 JSX 属性 show={cond}, 不从模块 import",
	"fallback": "元素级指令的配套属性: 写在带 each / show 的元素上",
	"key":      "元素级指令的配套属性: 写在带 each 的元素上",
	"stable":   "元素级指令的配套属性: 写在带 each 的元素上",
	"For":      "已改名为元素级指令 each (2026-09-20, 见 docs/gui-guide.md §8.2)",
	"Show":     "已改名为元素级指令 show (2026-09-20, 见 docs/gui-guide.md §8.2)",
	"window":   "window 是内置元素标签 <window>, 不是模块导出",
}

// importMissHint 拼出"这个不存在的导出名到底该怎么写"的提示后缀。
// 优先级: 跨模块 (它在 gx/dialog) > 静态提示 (元素级指令) > 拼写相近 > 列出可用导出。
func importMissHint(spec, name string, exports map[string]object.Value) string {
	for _, other := range object.RegisteredBuiltinModules() {
		if other == spec {
			continue
		}
		otherExports, ok := object.LookupBuiltinModule(other)
		if !ok {
			continue
		}
		if _, ok := otherExports[name]; ok {
			return fmt.Sprintf(" (它在 %s)", other)
		}
	}
	if hint, ok := importMissStaticHints[name]; ok {
		return " (" + hint + ")"
	}
	if near := nearestExportName(name, exports); near != "" {
		return fmt.Sprintf(" (想写的是 %q? 可用导出: %s)", near, exportNameList(exports))
	}
	return fmt.Sprintf(" (可用导出: %s)", exportNameList(exports))
}

// nearestExportName 在导出表里找与 name 最接近的名字 (大小写差异或编辑距离 ≤2)。
// 找不到返回 ""。
func nearestExportName(name string, exports map[string]object.Value) string {
	best := ""
	bestDist := 3
	for candidate := range exports {
		if strings.EqualFold(candidate, name) {
			return candidate
		}
		if d := editDistance(name, candidate); d < bestDist {
			best, bestDist = candidate, d
		}
	}
	return best
}

// editDistance 是标准 Levenshtein 距离 (两行滚动数组, O(n*m) 时间 O(m) 空间)。
func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = minInt(prev[j]+1, minInt(curr[j-1]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// exportNameList 按字典序列出导出名 (gox 这种并集模块太长, 截断到 12 个)。
func exportNameList(exports map[string]object.Value) string {
	names := make([]string, 0, len(exports))
	for name := range exports {
		names = append(names, name)
	}
	sort.Strings(names)
	const max = 12
	if len(names) > max {
		return strings.Join(names[:max], ", ") + ", …"
	}
	return strings.Join(names, ", ")
}

// compileExportDeclaration 编译 export 声明。
//
// 四类形态分别落到不同的运行期机制:
//
//	export * from "m"          → OP_EXPORT_STAR (星号再导出, 读时解析)
//	export {a as b} from "m"   → OP_EXPORT_FROM (具名再导出/命名空间再导出)
//	export {a as b}            → 本地命名导出 (模块模式走 OP_EXPORT_BINDING)
//	export <declaration>       → 编译声明 + 按名导出 / default 导出
//
// 关于"活绑定": 模块模式下本地 let/const/var/function/class 导出用
// OP_EXPORT_BINDING 记录"导出名 → 模块顶层帧槽位", 导入方取用时才读取槽位,
// 因此 `export let a = 1; a = 2;` 会被读到 2 (规范要求的 live binding)。
// 入口脚本(全局模式)的顶层绑定落在共享全局环境, 没有帧槽位, 退回值导出。
func (c *Compiler) compileExportDeclaration(stmt *ast.ExportDeclaration) error {
	// 1) export * from "m": 记录源模块, 读时转发其自有可枚举导出 (不含 default)
	if stmt.IsStar {
		specIdx := c.constants.AddConstant(object.NewString(stmt.Source))
		c.emitter.Emit(bytecode.OP_EXPORT_STAR, specIdx)
		return nil
	}

	// 2) export ... from "m": 具名再导出 / export * as ns
	//    常量 = [模块路径, 源导出名, 目标导出名]; 源导出名 "*" 表示命名空间对象。
	if stmt.Source != "" {
		for _, sp := range stmt.Specifiers {
			composite := object.NewArray([]object.Value{
				object.NewString(stmt.Source),
				object.NewString(sp.Local),
				object.NewString(sp.Exported),
			})
			idx := c.constants.AddConstant(composite)
			c.emitter.Emit(bytecode.OP_EXPORT_FROM, idx)
		}
		return nil
	}

	// 3) export { a, b as c }: 本地命名导出 (可别名)
	if len(stmt.Specifiers) > 0 {
		for _, sp := range stmt.Specifiers {
			c.emitLocalExport(sp.Local, sp.Exported)
		}
		return nil
	}

	// 4) export <declaration>
	if stmt.Declaration == nil {
		return nil
	}
	if stmt.IsDefault {
		return c.compileDefaultExport(stmt.Declaration)
	}
	if err := c.compileStatement(stmt.Declaration); err != nil {
		return err
	}
	// 声明里绑定的每个名字都导出一次 (覆盖多 declarator 与解构)
	for _, name := range exportedNames(stmt.Declaration) {
		c.emitLocalExport(name, name)
	}
	return nil
}

// emitLocalExport 把一个本模块绑定以 exported 名字导出。
//
// 模块模式的顶层绑定有帧槽位 (OP_STORE/OP_STORE_CONST), 用 OP_EXPORT_BINDING
// 记录槽位读取器 → 活绑定。全局模式的顶层绑定按名字落在共享全局环境, 退回
// "取值导出": 载入当前值再 OP_EXPORT。
func (c *Compiler) emitLocalExport(local, exported string) {
	sym := c.scope.Resolve(local)
	if sym != nil && !(sym.Depth == 0 && !c.moduleMode) {
		composite := object.NewArray([]object.Value{
			object.NewString(exported),
			object.NewInt(int64(sym.Slot)),
		})
		idx := c.constants.AddConstant(composite)
		c.emitter.Emit(bytecode.OP_EXPORT_BINDING, idx)
		return
	}
	// 全局/未解析: 按名字取全局值后导出 (与旧行为一致, 从不静默丢失导出)
	c.emitGlobalLoad(local)
	nameIdx := c.constants.AddConstant(object.NewString(exported))
	c.emitter.Emit(bytecode.OP_EXPORT, nameIdx)
}

// compileDefaultExport 编译 export default。
//
// default 导出始终是"值"而非活绑定: 规范里 default 是独立导出项, 具名默认
// 函数/类只把名字作为模块内局部绑定 (见 parser.parseExportDefault)。因此这里
// 先编译声明登记局部绑定, 再把绑定当前值作为 default 导出; 表达式/匿名函数/
// 匿名类则直接求值导出。
func (c *Compiler) compileDefaultExport(decl ast.Statement) error {
	nameIdx := c.constants.AddConstant(object.NewString("default"))
	switch d := decl.(type) {
	case *ast.ExpressionStatement:
		// 表达式只求值一次: 值入栈 → OP_EXPORT 弹出。
		if err := c.compileExpression(d.Expression); err != nil {
			return err
		}
	case *ast.FunctionDeclaration:
		if err := c.compileFunctionDeclaration(d); err != nil {
			return err
		}
		if err := c.loadDeclaredBinding(d.Name); err != nil {
			return err
		}
	case *ast.ClassDeclaration:
		if err := c.compileClassDeclaration(d); err != nil {
			return err
		}
		if err := c.loadDeclaredBinding(d.Name); err != nil {
			return err
		}
	default:
		return fmt.Errorf("compiler: unsupported export default declaration %T", decl)
	}
	c.emitter.Emit(bytecode.OP_EXPORT, nameIdx)
	return nil
}

// loadDeclaredBinding 加载刚声明的绑定值到栈顶。
// 关键: 用 emitLoad 而不是裸 OP_LOAD —— 全局模式下绑定在共享全局环境里
// (OP_DECLARE_FUNC/OP_DECLARE), 裸 OP_LOAD 会去读从未初始化的帧槽位,
// 触发 "Cannot access lexical declaration before initialization" (TDZ 误报)。
func (c *Compiler) loadDeclaredBinding(name *ast.Identifier) error {
	if name == nil {
		return fmt.Errorf("compiler: export default declaration has no name")
	}
	sym := c.scope.Resolve(name.Value)
	if sym == nil {
		return fmt.Errorf("compiler: export default binding %q not found", name.Value)
	}
	c.emitLoad(sym)
	return nil
}

// exportedNames 返回一个"声明导出"里所有被绑定的名字。
// 覆盖: 多 declarator (export let a, b) 与解构 (export const {x, y} = o)。
func exportedNames(decl ast.Statement) []string {
	switch d := decl.(type) {
	case *ast.LetStatement:
		return declaratorExportNames(d.Name, d.Value, d.More)
	case *ast.ConstStatement:
		return declaratorExportNames(d.Name, d.Value, d.More)
	case *ast.VarStatement:
		return declaratorExportNames(d.Name, d.Value, d.More)
	case *ast.FunctionDeclaration:
		if d.Name != nil {
			return []string{d.Name.Value}
		}
	case *ast.ClassDeclaration:
		if d.Name != nil {
			return []string{d.Name.Value}
		}
	}
	return nil
}

// declaratorExportNames 收集声明项绑定的名字 (含解构合成的 "__destructure__")。
func declaratorExportNames(name *ast.Identifier, value ast.Expression, more []ast.Declarator) []string {
	var out []string
	collect := func(n *ast.Identifier, v ast.Expression) {
		if n == nil {
			return
		}
		if n.Value == destructureSyntheticName {
			collectPatternExportNames(v, &out)
			return
		}
		out = append(out, n.Value)
	}
	collect(name, value)
	for _, d := range more {
		collect(d.Name, d.Value)
	}
	return out
}

// collectPatternExportNames 从解构声明 (Name="__destructure__", Value 是
// AssignmentExpression{Left: 模式}) 里收集绑定名。
func collectPatternExportNames(value ast.Expression, out *[]string) {
	assign, ok := value.(*ast.AssignmentExpression)
	if !ok {
		return
	}
	collectPatternNames(assign.Left, out)
}

// collectPatternNames 递归走解构模式收集绑定名。
func collectPatternNames(pattern ast.Expression, out *[]string) {
	switch p := pattern.(type) {
	case *ast.Identifier:
		if p.Value != destructureSyntheticName {
			*out = append(*out, p.Value)
		}
	case *ast.ArrayPattern:
		for _, el := range p.Elements {
			if el != nil {
				collectPatternNames(el.Target, out)
			}
		}
	case *ast.ObjectPattern:
		for _, prop := range p.Properties {
			if prop != nil {
				collectPatternNames(prop.Value, out)
			}
		}
	case *ast.AssignmentExpression:
		// 带默认值的元素: {a = 1} / [a = 1]
		collectPatternNames(p.Left, out)
	}
}

func (c *Compiler) compileFunctionDeclaration(stmt *ast.FunctionDeclaration) error {
	// 先在当前作用域定义函数名 (允许递归调用)
	sym, err := c.declareOnce(stmt.Name.Value, false, true)
	if err != nil {
		return err
	}

	// 编译函数体为独立的 FunctionMetadata
	fnMeta, err := c.compileFunction(
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
	case *ast.YieldExpression:
		// yield* expr: 委托给可迭代对象 (见 compileYieldDelegate)
		if node.Delegate && node.Value != nil {
			return c.compileYieldDelegate(node.Value)
		}
		// yield [expr]: 编译 expr (默认 undefined), 然后 OP_YIELD 暂停
		if node.Value != nil {
			if err := c.compileExpression(node.Value); err != nil {
				return err
			}
		} else {
			c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
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

// isGlobalScope 返回当前是否处于全局作用域 (depth 0) 且非模块模式。
// 全局作用域的变量读写走 GLOBAL/DECLARE 指令，写入共享的全局环境，
// 从而支持 REPL 跨输入状态保持。模块模式保持旧行为 (局部 slot)，隔离模块命名空间。
func (c *Compiler) isGlobalScope() bool {
	return c.scope.Depth() == 0 && !c.moduleMode
}

// emitGlobalLoad 发射全局变量加载指令 (按名字查全局环境)。
func (c *Compiler) emitGlobalLoad(name string) {
	idx := c.constants.AddConstant(object.NewString(name))
	c.emitter.Emit(bytecode.OP_LOAD_GLOBAL, idx)
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

// emitGlobalDeclare 发射全局变量声明指令 (按名字写入全局环境, 弹出值)。
// 与 emitGlobalStore 的区别: 声明语义上总是定义绑定 (已存在时更新),
// 用于 let/const 与 import 绑定。
func (c *Compiler) emitGlobalDeclare(name string) {
	idx := c.constants.AddConstant(object.NewString(name))
	c.emitter.Emit(bytecode.OP_DECLARE, idx)
}

// emitLoad 根据符号作用域发射加载指令。
// 全局符号 → OP_LOAD_GLOBAL (按名字); 局部符号 → OP_LOAD (按 slot)。
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
		c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
	}
}

// emitIdentifierAssign 发射标识符赋值 (解构赋值的目标)。
// 已解析的局部符号写槽位，其余走全局存储 —— 与普通 x = v 一致。
func (c *Compiler) emitIdentifierAssign(name string) {
	sym := c.scope.Resolve(name)
	if sym == nil || (sym.Depth == 0 && !c.moduleMode) {
		c.emitGlobalStore(name)
	} else {
		c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
	}
}

func (c *Compiler) compileIdentifier(node *ast.Identifier) error {
	// arguments: 解析到当前函数的 arguments 槽位
	if node.Value == "arguments" && c.currentArgumentsSlot >= 0 {
		c.emitter.Emit(bytecode.OP_LOAD, uint16(c.currentArgumentsSlot))
		return nil
	}

	sym := c.scope.Resolve(node.Value)
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
		return c.compileDestructureAssignment(node, false)
	}
	if _, ok := node.Left.(*ast.ObjectPattern); ok {
		return c.compileDestructureAssignment(node, false)
	}

	switch left := node.Left.(type) {
	case *ast.Identifier:
		sym := c.scope.Resolve(left.Value)
		if sym == nil || (sym.Depth == 0 && !c.moduleMode) {
			// 全局变量: 读写共享全局环境
			if node.Operator == "=" {
				if err := c.compileExpression(node.Right); err != nil {
					return err
				}
				c.emitter.EmitNoOperand(bytecode.OP_DUP)
				c.emitGlobalStore(left.Value)
			} else {
				// 复合赋值: x += val → LOAD old, 编译右值, OP, DUP, STORE_GLOBAL
				c.emitGlobalLoad(left.Value)
				if err := c.compileExpression(node.Right); err != nil {
					return err
				}
				c.emitCompoundOp(node.Operator)
				c.emitter.EmitNoOperand(bytecode.OP_DUP)
				c.emitGlobalStore(left.Value)
			}
			return nil
		}

		if node.Operator == "=" {
			// x = val: 编译右值, DUP, STORE
			if err := c.compileExpression(node.Right); err != nil {
				return err
			}
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
			c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
		} else {
			// 复合赋值: x += val → LOAD old, 编译右值, OP, DUP, STORE
			c.emitter.Emit(bytecode.OP_LOAD, uint16(sym.Slot))
			if err := c.compileExpression(node.Right); err != nil {
				return err
			}
			c.emitCompoundOp(node.Operator)
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
			c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
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
// 解构目标 (let [a, b] = x) 不经 prescan 登记 symbolic 名字，
// 真正的绑定名在 compilePatternBind 阶段登记。
const destructureSyntheticName = "__destructure__"

func (c *Compiler) prescanScope(stmts []ast.Statement) error {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.LetStatement:
			if s.Name != nil && s.Name.Value != destructureSyntheticName {
				if err := c.prescanDeclare(s.Name.Value, false, false); err != nil {
					return err
				}
			}
			for _, d := range s.More {
				if d.Name != nil && d.Name.Value != destructureSyntheticName {
					if err := c.prescanDeclare(d.Name.Value, false, false); err != nil {
						return err
					}
				}
			}
		case *ast.ConstStatement:
			if s.Name != nil && s.Name.Value != destructureSyntheticName {
				if err := c.prescanDeclare(s.Name.Value, true, false); err != nil {
					return err
				}
			}
			for _, d := range s.More {
				if d.Name != nil && d.Name.Value != destructureSyntheticName {
					if err := c.prescanDeclare(d.Name.Value, true, false); err != nil {
						return err
					}
				}
			}
		case *ast.FunctionDeclaration:
			if s.Name != nil {
				if err := c.prescanDeclare(s.Name.Value, false, true); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// prescanDeclare 在声明提升预登记阶段登记绑定名。
// 名字已存在说明同一作用域内有重复声明 (或与参数重名) → SyntaxError；
// 例外: 函数声明与函数声明/var 同名互容 (var f 与 function f 的提升共存语义)。
func (c *Compiler) prescanDeclare(name string, isConst, isFnDecl bool) error {
	if sym := c.scope.ResolveLocal(name); sym != nil {
		if isFnDecl && (sym.IsFnDecl || sym.IsVarLike) {
			return nil
		}
		return fmt.Errorf("SyntaxError: Identifier '%s' has already been declared", name)
	}
	sym := c.scope.Define(name, isConst)
	sym.IsFnDecl = isFnDecl
	return nil
}

// declareOnce 在当前作用域登记绑定 (prescan 之后的正式编译阶段调用)。
//
// prescan 只登记名字 (Declared=false)，编译到声明语句时在此认领符号，
// 复用 prescan 分配的槽位。认领时发现符号已被认领 = 同一作用域内
// 真实的重复声明 → SyntaxError；函数声明与已认领的函数声明/var 绑定
// 同名除外。解构/class/import 不经 prescan，首次编译到时在此直接登记并认领。
func (c *Compiler) declareOnce(name string, isConst, isFnDecl bool) (*Symbol, error) {
	if sym := c.scope.ResolveLocal(name); sym != nil {
		if sym.Declared && !(isFnDecl && (sym.IsFnDecl || sym.IsVarLike)) {
			return nil, fmt.Errorf("SyntaxError: Identifier '%s' has already been declared", name)
		}
		sym.Declared = true
		if isFnDecl {
			sym.IsFnDecl = true
		}
		return sym, nil
	}
	sym := c.scope.Define(name, isConst)
	sym.Declared = true
	sym.IsFnDecl = isFnDecl
	return sym, nil
}

// compileMemberRef 编译成员引用的两个部分，栈上留下 [obj, key]。
// obj 与 key 各自只求值一次 —— 复合/逻辑赋值需要重复用到这个引用，
// 但绝不能重复求值 `obj[f()] += v` 里的 f。
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
			c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
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

// compileDestructureAssignment 处理解构。
// isDecl=true: 声明解构 (let/const [a] = x)，目标按声明登记 (含重声明检查)；
// isDecl=false: 赋值解构 ([a] = x)，目标按普通赋值处理 (写已有绑定)。
func (c *Compiler) compileDestructureAssignment(node *ast.AssignmentExpression, isDecl bool) error {
	// 编译右值 (被解构的值)
	if err := c.compileExpression(node.Right); err != nil {
		return err
	}
	if !isDecl {
		// 赋值表达式的值是右值: 留一份副本，解构只消耗另一份
		c.emitter.EmitNoOperand(bytecode.OP_DUP)
	}

	return c.compilePatternBind(node.Left, isDecl)
}

// compilePatternBind 对栈顶的值执行解构绑定。
// 解构后栈顶的被解构值被弹出。
// 支持 ArrayPattern / ObjectPattern / 嵌套模式。
func (c *Compiler) compilePatternBind(pattern ast.Expression, isDecl bool) error {
	switch pattern := pattern.(type) {
	case *ast.ArrayPattern:
		// 数组解构按迭代协议取值: 先把栈顶的被解构值物化为数组，
		// 使 generator/字符串/Set 等可迭代对象也能解构，
		// 非可迭代值 (如 null) 在此抛 TypeError —— 与 ECMAScript 一致。
		// [val] → [val, []] → [arr, val] → [materialized]
		c.emitter.Emit(bytecode.OP_PACK_ARRAY, 0)
		c.emitter.EmitNoOperand(bytecode.OP_SWAP)
		c.emitter.EmitNoOperand(bytecode.OP_ARRAY_SPREAD)

		for i, elem := range pattern.Elements {
			// DUP 数组 → [arr, arr]
			c.emitter.EmitNoOperand(bytecode.OP_DUP)

			if elem.Rest {
				// ...rest: 收集剩余元素到新数组
				// [arr, arr] → push start → [arr, arr, i] → SLICE → [arr, restArr]
				c.emitter.Emit(bytecode.OP_INT, uint16(i))
				c.emitter.EmitNoOperand(bytecode.OP_ARRAY_SLICE)
			} else {
				// 压入索引 → [arr, arr, i]
				c.emitter.Emit(bytecode.OP_INT, uint16(i))
				// 获取元素 → [arr, val]
				c.emitter.EmitNoOperand(bytecode.OP_GET_INDEX)
			}

			// 处理默认值 (仅非 rest 元素支持默认值)
			if elem.Default != nil && !elem.Rest {
				if err := c.compileDestructureDefault(elem.Default); err != nil {
					return err
				}
			}

			// 存储到目标 (标识符 / 嵌套模式)
			if ident, ok := elem.Target.(*ast.Identifier); ok {
				if isDecl {
					sym, err := c.declareOnce(ident.Value, false, false)
					if err != nil {
						return err
					}
					if c.isGlobalScope() {
						nameIdx := c.constants.AddConstant(object.NewString(ident.Value))
						c.emitter.Emit(bytecode.OP_DECLARE, nameIdx)
					} else {
						c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
					}
				} else {
					// 赋值解构: 写入已有绑定 (与 x = v 语义一致)
					c.emitIdentifierAssign(ident.Value)
				}
			} else if elem.Target != nil {
				// 嵌套解构: [ [a, b], c ] = arr
				if err := c.compilePatternBind(elem.Target, isDecl); err != nil {
					return err
				}
			}

			// rest 必须是最后一个元素
			if elem.Rest {
				break
			}
		}

	case *ast.ObjectPattern:
		for _, prop := range pattern.Properties {
			// DUP 对象 → [obj, obj]
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
			// 压入属性名 → [obj, obj, name]
			var keyName string
			if prop.Shorthand {
				keyName = prop.Value.(*ast.Identifier).Value
			} else {
				if ident, ok := prop.Key.(*ast.Identifier); ok {
					keyName = ident.Value
				}
			}
			idx := c.constants.AddConstant(object.NewString(keyName))
			c.emitter.Emit(bytecode.OP_CONST, idx)
			// 获取属性 → [obj, val]
			c.emitter.EmitNoOperand(bytecode.OP_GET_INDEX)

			// 处理默认值
			if prop.Default != nil {
				if err := c.compileDestructureDefault(prop.Default); err != nil {
					return err
				}
			}

			// 存储到目标 (标识符 / 嵌套模式)
			if ident, ok := prop.Value.(*ast.Identifier); ok {
				if isDecl {
					sym, err := c.declareOnce(ident.Value, false, false)
					if err != nil {
						return err
					}
					if c.isGlobalScope() {
						nameIdx := c.constants.AddConstant(object.NewString(ident.Value))
						c.emitter.Emit(bytecode.OP_DECLARE, nameIdx)
					} else {
						c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
					}
				} else {
					// 赋值解构: 写入已有绑定 (与 x = v 语义一致)
					c.emitIdentifierAssign(ident.Value)
				}
			} else if prop.Value != nil {
				if err := c.compilePatternBind(prop.Value, isDecl); err != nil {
					return err
				}
			}
		}

	default:
		return fmt.Errorf("compiler: unsupported destructure pattern %T", pattern)
	}

	// 弹出被解构的值
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	return nil
}

// compileDestructureDefault 实现解构默认值: 栈顶为值 [val],
// 若 val 为 null/undefined 则用默认表达式替换 (短路)。
// 结束后栈顶为最终值 [val 或 default]。
func (c *Compiler) compileDestructureDefault(def ast.Expression) error {
	// [val] → [val, val]
	c.emitter.EmitNoOperand(bytecode.OP_DUP)
	// 非 nullish → 保留 val, 跳到末尾
	notNull := c.emitter.EmitJump(bytecode.OP_JUMP_IF_NOT_NULL)
	// nullish 路径: 弹出副本与原始值, 用默认值替换
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	if err := c.compileExpression(def); err != nil {
		return err
	}
	done := c.emitter.EmitJump(bytecode.OP_JUMP)
	// 非 nullish 路径: 弹出副本, 保留原始值
	c.emitter.PatchJump(notNull)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.PatchJump(done)
	return nil
}

// compileIncDec 处理 ++/-- 操作。
// target: 操作目标表达式 (通常是 Identifier)
// isInc: true=++, false=--
// isPrefix: true=前缀, false=后缀
func (c *Compiler) compileIncDec(target ast.Expression, isInc, isPrefix bool) error {
	if ident, ok := target.(*ast.Identifier); ok {
		sym := c.scope.Resolve(ident.Value)
		if sym == nil {
			return fmt.Errorf("ReferenceError: %s is not defined", ident.Value)
		}

		// 加载旧值
		c.emitLoad(sym) // [old]

		if isPrefix {
			// 前缀: ++i → 返回新值
			// [old] → TO_NUMBER → [num] → push 1 → [num, 1] → OP → [new]
			// → DUP → [new, new] → STORE → [new]
			c.emitter.EmitNoOperand(bytecode.OP_TO_NUMBER)
			c.emitter.Emit(bytecode.OP_INT, 1)
			c.emitIncDecOp(isInc)
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
			c.emitStore(sym)
		} else {
			// 后缀: i++ → 返回旧值
			// [old] → DUP → [old, old] → TO_NUMBER → [old, num] → push 1
			// → [old, num, 1] → OP → [old, new] → STORE → [old]
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
			c.emitter.EmitNoOperand(bytecode.OP_TO_NUMBER)
			c.emitter.Emit(bytecode.OP_INT, 1)
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
		// [obj, key] → DUP2 → [obj, key, obj, key] → GET_INDEX → [obj, key, old]
		c.emitter.EmitNoOperand(bytecode.OP_DUP2)
		c.emitter.EmitNoOperand(bytecode.OP_GET_INDEX)

		if isPrefix {
			// 前缀: [obj, key, old] → TO_NUMBER → [obj, key, num] → push 1 → OP
			// → [obj, key, new] → SET_INDEX (推回写入值) → [new]
			c.emitter.EmitNoOperand(bytecode.OP_TO_NUMBER)
			c.emitter.Emit(bytecode.OP_INT, 1)
			c.emitIncDecOp(isInc)
			c.emitter.EmitNoOperand(bytecode.OP_SET_INDEX)
			return nil
		}
		// 后缀: [obj, key, old] → DUP_BELOW2 → [old, obj, key, old]
		// → TO_NUMBER → [old, obj, key, num] → push 1 → OP → [old, obj, key, new]
		// → SET_INDEX → [old, new] → POP → [old]
		c.emitter.EmitNoOperand(bytecode.OP_DUP_BELOW2)
		c.emitter.EmitNoOperand(bytecode.OP_TO_NUMBER)
		c.emitter.Emit(bytecode.OP_INT, 1)
		c.emitIncDecOp(isInc)
		c.emitter.EmitNoOperand(bytecode.OP_SET_INDEX)
		c.emitter.EmitNoOperand(bytecode.OP_POP)
		return nil
	}
	return fmt.Errorf("++/-- only supports identifiers")
}

// emitIncDecOp 发射 ++/-- 的加减指令。
func (c *Compiler) emitIncDecOp(isInc bool) {
	if isInc {
		c.emitter.EmitNoOperand(bytecode.OP_ADD)
	} else {
		c.emitter.EmitNoOperand(bytecode.OP_SUB)
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
		c.emitter.Emit(bytecode.OP_CALL_METHOD, uint16(len(node.Arguments)))
		return nil
	}

	// 检查是否是方法调用: obj.method(args)
	if member, ok := node.Function.(*ast.MemberExpression); ok && !member.Computed && !hasSpreadArgs(node.Arguments) {
		// super.method(args): 从父 prototype 取方法, this 绑定当前 this
		if _, isSuperObj := member.Object.(*ast.SuperExpression); isSuperObj {
			if c.currentSuperClass == "" {
				return fmt.Errorf("compiler: super method call outside class")
			}
			// LOAD_GLOBAL Super → GET_PROP prototype → GET_PROP method → OP_THIS → args → CALL_METHOD
			c.emitSuperLoad(c.currentSuperClass)
			pidx := c.constants.AddConstant(object.NewString("prototype"))
			c.emitter.Emit(bytecode.OP_GET_PROP, pidx)
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

	if hasSpread {
		// 有 spread: 收集参数到数组，再用 OP_CALL_SPREAD 调用
		if err := c.compileArgumentsArray(node.Arguments); err != nil {
			return err
		}
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
		// 编译函数
		if err := c.compileExpression(node.Function); err != nil {
			return err
		}
		// 调用
		c.emitter.Emit(bytecode.OP_CALL, uint16(len(node.Arguments)))
	}
	return nil
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
			if err := c.compileExpression(elem); err != nil {
				return err
			}
		}
		c.emitter.Emit(bytecode.OP_NEW_ARRAY, uint16(len(node.Elements)))
	} else {
		// 有 spread: 逐步构建数组
		c.emitter.Emit(bytecode.OP_NEW_ARRAY, 0) // 空数组
		for _, elem := range node.Elements {
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
				meta, err := c.compileFunction(name, fn.Parameters, fn.Body, false, fn.IsGenerator, fn.IsAsync)
				if err != nil {
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
			// 编译值
			if err := c.compileExpression(prop.Value); err != nil {
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
			meta, err := c.compileFunction("get "+name, fn.Parameters, fn.Body, false, fn.IsGenerator, fn.IsAsync)
			if err != nil {
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
			// 编译值 (简写时 prop.Value 是同名 Identifier)
			if err := c.compileExpression(prop.Value); err != nil {
				return err
			} // [obj, val]
			// 设置属性
			keyName := prop.Key.(*ast.Identifier).Value
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
	c.emitter.EmitNoOperand(bytecode.OP_DYNAMIC_IMPORT)
	return nil
}

// ===== 函数编译 =====

func (c *Compiler) compileFunction(name string, params []*ast.Parameter, body *ast.BlockStatement, isArrow, isGenerator, isAsync bool) (*bytecode.FunctionMetadata, error) {
	return c.compileFunctionSelf(name, "", params, body, isArrow, isGenerator, isAsync)
}

// compileFunctionSelf 编译函数，可选绑定命名函数表达式的自引用。
// selfName 非空时 (仅函数表达式场景)，在函数作用域内定义 selfName 指向
// 函数自身 (ES 规范 NamedFunctionExpression 作用域)，VM 调用时把闭包
// 写入对应槽位。参数与 selfName 同名时参数优先 (规范行为)。
func (c *Compiler) compileFunctionSelf(name, selfName string, params []*ast.Parameter, body *ast.BlockStatement, isArrow, isGenerator, isAsync bool) (*bytecode.FunctionMetadata, error) {
	// async generator: wrapper 创建并返回 AsyncGenerator 对象,
	// 内层 generator 的 await 编为 OP_AWAIT、yield 编为 OP_YIELD。
	if isAsync && isGenerator {
		return c.compileAsyncGeneratorSelf(name, selfName, params, body, isArrow)
	}
	// async 函数: 编译为 wrapper (返回 __spawn(generator)), 内层 generator 处理 await→yield
	if isAsync {
		return c.compileAsyncFunctionSelf(name, selfName, params, body, isArrow)
	}

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
			// 函数入口处解构到各局部变量
			paramSpecs[i] = bytecode.ParameterSpec{
				Name:       fmt.Sprintf("__param_%d", i),
				HasDefault: false,
				IsRest:     false,
			}
			sym := fnScope.Define(paramSpecs[i].Name, false)
			paramSlots[i] = sym.Slot
			continue
		}
		paramSpecs[i] = bytecode.ParameterSpec{
			Name:       param.Name,
			HasDefault: param.Default != nil,
			IsRest:     param.Rest,
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
	// defer: 保证错误早退路径也恢复, 否则 compileTryStatement 的 [:len-1] 会 panic (rCzckg 回归)。
	defer func() {
		c.tryScopes = prevTryScopes
		c.finallyRetSlot = prevFinallyRetSlot
	}()

	// 默认参数处理: 对有默认值的参数，检查是否为 undefined
	for i, param := range params {
		if param.Default == nil {
			continue
		}
		slot := paramSlots[i]

		// LOAD param_slot → [param_val]
		c.emitter.Emit(bytecode.OP_LOAD, uint16(slot))
		// JUMP_IF_NULL → 如果为 null/undefined，跳到设置默认值 (不弹出)
		defJump := c.emitter.EmitJump(bytecode.OP_JUMP_IF_NULL)
		// 不为空: 弹出已加载的值，跳过默认值设置
		c.emitter.EmitNoOperand(bytecode.OP_POP)
		endJump := c.emitter.EmitJump(bytecode.OP_JUMP)
		// 设置默认值
		c.emitter.PatchJump(defJump)
		c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出 undefined
		if err := c.compileExpression(param.Default); err != nil {
			return nil, err
		}
		c.emitter.Emit(bytecode.OP_STORE, uint16(slot))
		// 结束
		c.emitter.PatchJump(endJump)
	}

	// rest 参数处理: 最后一个参数如果是 rest，收集剩余参数
	if len(params) > 0 && params[len(params)-1].Rest {
		restParam := params[len(params)-1]
		restSlot := len(params) - 1 // rest 参数的 slot
		// 在 VM 的 callClosure 中处理 rest 参数收集
		// 这里只需要标记 IsRest，VM 会自动处理
		_ = restParam
		_ = restSlot
	}

	// 解构模式参数绑定: LOAD 隐藏槽 → 解构到局部变量
	// 顺序在默认参数处理之后 (默认值已填入隐藏槽)
	for i, param := range params {
		if param.Pattern == nil {
			continue
		}
		slot := paramSlots[i]
		c.emitter.Emit(bytecode.OP_LOAD, uint16(slot))
		if err := c.compilePatternBind(param.Pattern, true); err != nil {
			return nil, err
		}
	}

	// 在函数体内不自动添加 PUSH_SCOPE/POP_SCOPE (函数本身已有作用域)
	if err := c.compileStatements(body.Statements); err != nil {
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
	meta.ArgumentsSlot = argumentsSlot
	meta.SelfSlot = selfSlot
	meta.IsGenerator = isGenerator
	meta.IsAsync = false
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
	return c.compileAsyncFunctionSelf(name, "", params, body, false)
}

// compileAsyncFunctionSelf 把异步函数编译成两段: wrapper + 内层 generator。
//
// isArrow 必须由调用方如实传入 —— 它决定 wrapper 与内层 generator 两个
// FunctionMetadata 的 IsArrow。箭头函数在调用时**不重绑 this**(vm 按
// Closure.IsArrow 判断), 漏了这个, `async () => this.x` 里的 this 就会变成
// 调用时的接收者。
func (c *Compiler) compileAsyncFunctionSelf(name, selfName string, params []*ast.Parameter, body *ast.BlockStatement, isArrow bool) (*bytecode.FunctionMetadata, error) {
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
	genMeta, err := c.compileFunctionSelf(name, selfName, params, body, isArrow, true, false)
	c.asyncGeneratorBody = prevAGBody
	c.inAsyncFunction = prevInAsync
	if err != nil {
		return nil, err
	}
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
				HasDefault: false,
				IsRest:     false,
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
	// defer: 保证错误早退路径也恢复, 否则 compileTryStatement 的 [:len-1] 会 panic (rCzckg 回归)。
	defer func() {
		c.tryScopes = prevTryScopes
		c.finallyRetSlot = prevFinallyRetSlot
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
	meta.ArgumentsSlot = argumentsSlot
	meta.IsAsync = true
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
func (c *Compiler) compileAsyncGeneratorSelf(name, selfName string, params []*ast.Parameter, body *ast.BlockStatement, isArrow bool) (*bytecode.FunctionMetadata, error) {
	// 1. 编译内层 generator。asyncGeneratorBody=true 让 await 编为 OP_AWAIT;
	//    inAsyncFunction=true 让 for await...of 在主路径内合法。
	prevAGBody := c.asyncGeneratorBody
	c.asyncGeneratorBody = true
	prevInAsync := c.inAsyncFunction
	c.inAsyncFunction = true
	genMeta, err := c.compileFunctionSelf(name, selfName, params, body, isArrow, true, false)
	c.inAsyncFunction = prevInAsync
	c.asyncGeneratorBody = prevAGBody
	if err != nil {
		return nil, err
	}
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
				HasDefault: false,
				IsRest:     false,
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
	defer func() {
		c.tryScopes = prevTryScopes
		c.finallyRetSlot = prevFinallyRetSlot
	}()

	helperIdx := c.constants.AddConstant(object.NewString("__async_generator"))
	// 调用约定: fn 必须在栈顶。先压参数, 再 FUNCTION 创建 gen closure,
	// CALL n 弹出 fn=genClosure + 参数 → 创建 Generator。
	for _, slot := range paramSlots {
		c.emitter.Emit(bytecode.OP_LOAD, uint16(slot)) // [param...]
	}
	c.emitter.Emit(bytecode.OP_FUNCTION, uint16(genIdx))      // [param..., genClosure]
	c.emitter.Emit(bytecode.OP_CALL, uint16(len(paramSlots))) // [genObj]
	c.emitter.Emit(bytecode.OP_LOAD_GLOBAL, helperIdx)        // [genObj, __async_generator]
	c.emitter.Emit(bytecode.OP_CALL, 1)                       // [asyncGenerator]
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
	meta.ArgumentsSlot = argumentsSlot
	meta.IsAsync = true
	return meta, nil
}

func (c *Compiler) compileFunctionExpression(node *ast.FunctionExpression) error {
	var name string
	selfName := ""
	if node.Name != nil {
		name = node.Name.Value
		// 命名函数表达式: 函数体内名字可见且指向自身 (递归入口)
		selfName = name
	}
	meta, err := c.compileFunctionSelf(name, selfName, node.Parameters, node.Body, false, node.IsGenerator, node.IsAsync)
	if err != nil {
		return err
	}
	idx := c.constants.AddConstant(meta)
	c.emitter.Emit(bytecode.OP_FUNCTION, idx)
	return nil
}

func (c *Compiler) compileArrowFunctionExpression(node *ast.ArrowFunctionExpression) error {
	name := "arrow"
	// isAsync 走 compileAsyncFunctionSelf (wrapper + 内层 generator), 但 isArrow
	// 一路传下去 —— 否则 `async () => this.x` 的 this 会被调用时的接收者覆盖。
	meta, err := c.compileFunction(name, node.Parameters, getBlockFromBody(node.Body), true, false, node.IsAsync)
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

func (c *Compiler) compileNewExpression(node *ast.NewExpression) error {
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

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

	// currentSuperClass 记录当前 class 方法的父类名 (super 引用目标)。
	// 仅在编译 class 方法时非空。
	currentSuperClass string
}

// controlContext 表示一个循环/switch/标签块的控制流上下文。
type controlContext struct {
	label         string // 空表示未标注
	isLoop        bool   // continue 只对循环有效
	breakJumps    []int  // 待回填的 break 跳转位置
	continueJumps []int  // 待回填的 continue 跳转位置
}

// pushControl 压入一个控制上下文, 返回其指针。
func (c *Compiler) pushControl(label string, isLoop bool) *controlContext {
	ctx := &controlContext{label: label, isLoop: isLoop}
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
		scope:                NewSymbolScope(nil),
		currentArgumentsSlot: -1,
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
		NamedImports: []string{jsxFactoryName},
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
			for _, n := range s.NamedImports {
				if n == name {
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
//  2. 再编译列表里的 function 声明 (绑定 + 创建函数对象)，其余语句按源码
//     顺序编译，已提升的声明跳过以免重复创建。
func (c *Compiler) compileStatements(stmts []ast.Statement) error {
	if err := c.prescanScope(stmts); err != nil {
		return err
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
		if err := c.compileStatement(stmt); err != nil {
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

func (c *Compiler) compileReturnStatement(stmt *ast.ReturnStatement) error {
	if stmt.ReturnValue != nil {
		if err := c.compileExpression(stmt.ReturnValue); err != nil {
			return err
		}
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

	// 绑定本次迭代的值 (栈顶)。两种形状:
	//   简单绑定 for (let x of arr)          → 直接存进新建的槽位
	//   解构绑定 for (const [a, b] of pairs) → 交给 compilePatternBind 按模式拆开
	// 两边进来时栈都是 [.., value]、离开时都回到 [..] —— compilePatternBind 末尾
	// 自带 POP, 与 OP_STORE 消耗栈顶值的语义对齐。
	varKind := bytecode.OP_STORE
	if _, ok := stmt.VarDecl.(*ast.ConstStatement); ok {
		varKind = bytecode.OP_STORE_CONST
	}
	if stmt.Pattern != nil {
		// isDecl=true: 每轮迭代是新的块作用域 (上面已 PUSH_SCOPE), 解构出来的
		// 名字声明进这个作用域, 所以各轮的绑定互不影响 —— 闭包捕获到的是各自
		// 的槽位。
		//
		// 注: 解构出来的名字按 let 语义登记 (compilePatternBind 内部是
		// declareOnce(…, false, …)), 与 const [a, b] = … 的现有口径一致。本运行时
		// 的 const **只在全局词法绑定上强制** (见 vm.go 的 OP_STORE_GLOBAL);
		// 局部槽位根本不查 (compiler.Symbol.IsConst 目前无人读取), 所以
		// for (const [a, b] of …) 的 a/b 可被重新赋值 —— 这是既有边界, 不是本次
		// 解构支持引入的。
		if err := c.compilePatternBind(stmt.Pattern, true); err != nil {
			return err
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
	sym := c.scope.Define(stmt.Variable.Value, varKind == bytecode.OP_STORE_CONST)
	c.emitter.Emit(varKind, uint16(sym.Slot))

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
	// 计算 catch 和 finally 的跳转目标
	var catchPC int
	var finallyPC int

	// try body
	if stmt.CatchBody != nil {
		catchPC = c.emitter.EmitJump(bytecode.OP_PUSH_TRY) // 暂时占位
	} else if stmt.FinallyBody != nil {
		catchPC = c.emitter.EmitJump(bytecode.OP_PUSH_TRY) // 暂时占位
	} else {
		// 无 catch 无 finally: 不需要 try handler，直接编译 body
		return c.compileBlockStatement(stmt.Body)
	}

	// 如果有 finally，设置 finallyPC
	if stmt.FinallyBody != nil {
		finallyPC = c.emitter.EmitJump(bytecode.OP_PUSH_FINALLY) // 暂时占位
	}

	// try body
	if err := c.compileBlockStatement(stmt.Body); err != nil {
		return err
	}

	// try 正常结束: 弹出 try handler
	c.emitter.EmitNoOperand(bytecode.OP_POP_TRY)

	// 跳过 catch body，跳到 finally (或结束)
	skipCatch := c.emitter.EmitJump(bytecode.OP_JUMP)

	// === catch handler ===
	c.emitter.PatchJump(catchPC) // 回填 PUSH_TRY 的 catch 目标
	if stmt.CatchBody != nil {
		// 如果有 finally，catch body 也需要 finally 保护
		if stmt.FinallyBody != nil {
			// 重新 push try with only finally (catchPC = 0xFFFF 表示无 catch)
			catchFinally := c.emitter.EmitJump(bytecode.OP_PUSH_FINALLY)
			c.emitter.PatchJump(finallyPC) // 回填 PUSH_FINALLY (try body 的)
			finallyPC = catchFinally       // catch body 的 finally

			// 弹出 catch 参数 (在栈上)
			if stmt.CatchParam != nil {
				sym := c.scope.Define(stmt.CatchParam.Value, false)
				if c.isGlobalScope() {
					c.emitGlobalStore(stmt.CatchParam.Value)
				} else {
					c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
				}
			} else {
				c.emitter.EmitNoOperand(bytecode.OP_POP)
			}

			if err := c.compileBlockStatement(stmt.CatchBody); err != nil {
				return err
			}
			c.emitter.EmitNoOperand(bytecode.OP_POP_TRY)
			skipFinally := c.emitter.EmitJump(bytecode.OP_JUMP)
			c.emitter.PatchJump(catchFinally)
			// 跳过重复的 finally body
			c.emitter.PatchJump(skipFinally)
		} else {
			// 无 finally 的 catch
			if stmt.CatchParam != nil {
				sym := c.scope.Define(stmt.CatchParam.Value, false)
				if c.isGlobalScope() {
					c.emitGlobalStore(stmt.CatchParam.Value)
				} else {
					c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
				}
			} else {
				c.emitter.EmitNoOperand(bytecode.OP_POP)
			}
			if err := c.compileBlockStatement(stmt.CatchBody); err != nil {
				return err
			}
		}
	} else if stmt.FinallyBody != nil {
		// 无 catch 但有 finally: 异常到这里，设置 pendingError
		// 弹出错误值并暂存
		c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出错误值 (会通过 pendingError 传递)
		c.emitter.PatchJump(finallyPC)
	}

	// === finally body ===
	if stmt.FinallyBody != nil {
		if stmt.CatchBody == nil {
			// 无 catch: finallyPC 已经在 catchPC 位置回填
		}
		if err := c.compileBlockStatement(stmt.FinallyBody); err != nil {
			return err
		}
		c.emitter.EmitNoOperand(bytecode.OP_END_FINALLY)
	}

	c.emitter.PatchJump(skipCatch)

	return nil
}

func (c *Compiler) compileSwitchStatement(stmt *ast.SwitchStatement) error {
	// 编译判别表达式
	if err := c.compileExpression(stmt.Discriminant); err != nil {
		return err
	}

	// 收集 case 跳转和 break 跳转
	var caseJumps []int  // JUMP_IF_TRUE 的位置
	var caseStarts []int // 每个 case 体的起始位置
	var defaultJump int  // 跳到 default 的位置

	// switch 本身是 break 目标 (case 体内的 break 作用于 switch, 而非外层循环)
	ctx := c.pushControl(c.takePendingLabel(), false)

	hasDefault := false
	defaultIdx := -1

	// 第一遍: 为每个 case 生成比较代码
	for i, sc := range stmt.Cases {
		if sc.Test == nil {
			// default case
			hasDefault = true
			defaultIdx = i
			caseStarts = append(caseStarts, -1) // 占位
			continue
		}
		// DUP 判别值, 编译测试值, ===
		c.emitter.EmitNoOperand(bytecode.OP_DUP)
		if err := c.compileExpression(sc.Test); err != nil {
			return err
		}
		c.emitter.EmitNoOperand(bytecode.OP_STRICT_EQ)
		// 如果匹配，跳到 case 体
		jump := c.emitter.EmitJump(bytecode.OP_JUMP_IF_TRUE)
		c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出比较结果
		caseJumps = append(caseJumps, jump)
		caseStarts = append(caseStarts, -1) // 占位
	}

	// 默认跳转
	if hasDefault {
		defaultJump = c.emitter.EmitJump(bytecode.OP_JUMP)
	} else {
		// 无 default: 跳到结束
		defaultJump = c.emitter.EmitJump(bytecode.OP_JUMP)
	}

	// 弹出判别值
	c.emitter.EmitNoOperand(bytecode.OP_POP)

	// 第二遍: 编译每个 case 体
	caseBodyStart := make([]int, len(stmt.Cases))
	caseJumpIdx := 0
	for i, sc := range stmt.Cases {
		caseBodyStart[i] = c.emitter.Pos()

		if sc.Test == nil {
			// default body
		} else {
			// 回填 JUMP_IF_TRUE 到这里
			c.emitter.PatchJump(caseJumps[caseJumpIdx])
			// 弹出比较结果 (JUMP_IF_TRUE 不弹出)
			c.emitter.EmitNoOperand(bytecode.OP_POP)
			caseJumpIdx++
		}

		// 编译 case 体语句
		for _, s := range sc.Statements {
			if err := c.compileStatement(s); err != nil {
				return err
			}
		}
	}

	// 回填所有 JUMP_IF_TRUE
	// (已在上面回填)

	// 回填 default jump
	if hasDefault {
		c.emitter.ReplaceJumpTarget(defaultJump, uint16(caseBodyStart[defaultIdx]))
	} else {
		// 跳到末尾
		c.emitter.PatchJump(defaultJump)
	}

	// break 跳转
	for _, jmp := range ctx.breakJumps {
		c.emitter.PatchJump(jmp)
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
		return fmt.Errorf("Uncaught SyntaxError: Undefined label '%s'", label)
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
	className := node.Name.Value

	// 父类名 (super 引用目标; 仅支持 Identifier 形式的 extends)
	superName := ""
	if node.SuperClass != nil {
		if ident, ok := node.SuperClass.(*ast.Identifier); ok {
			superName = ident.Value
		} else {
			return fmt.Errorf("compiler: class extends must reference an identifier")
		}
	}

	// 找到 constructor (显式或默认)
	var ctor *ast.ClassMethod
	for _, m := range node.Methods {
		if m.IsConstructor {
			ctor = m
			break
		}
	}

	// 编译 constructor
	prevSuper := c.currentSuperClass
	c.currentSuperClass = superName
	ctorMeta, err := c.compileClassConstructor(node, ctor, className, superName)
	if err != nil {
		return err
	}
	c.currentSuperClass = prevSuper
	ctorIdx := c.constants.AddConstant(ctorMeta)
	c.emitter.Emit(bytecode.OP_FUNCTION, ctorIdx) // [ctor]

	// 创建 prototype 对象
	c.emitter.EmitNoOperand(bytecode.OP_NEW_OBJECT) // [ctor, proto]

	// 实例方法挂到 prototype
	for _, m := range node.Methods {
		if m.IsConstructor {
			continue
		}
		if err := c.compileClassMethodToObject(m, superName); err != nil {
			return err
		}
	} // [ctor, proto]

	// extends: proto.Proto = SuperClass.prototype
	if superName != "" {
		// 栈: [ctor, proto] → LOAD_GLOBAL Super → GET_PROP prototype → [ctor, proto, parent]
		c.emitGlobalLoad(superName)
		pidx := c.constants.AddConstant(object.NewString("prototype"))
		c.emitter.Emit(bytecode.OP_GET_PROP, pidx)
		// OP_SET_PROTO: 弹出 parent, 设置下方对象 (proto) 的原型 → [ctor, proto]
		c.emitter.EmitNoOperand(bytecode.OP_SET_PROTO)
	}

	// ctor.prototype = proto → [ctor]
	pidx := c.constants.AddConstant(object.NewString("prototype"))
	c.emitter.Emit(bytecode.OP_SET_PROP, pidx)

	// 静态方法挂到 ctor
	for _, m := range node.Statics {
		// 编译静态方法函数
		prevSuper2 := c.currentSuperClass
		c.currentSuperClass = superName
		meta, err := c.compileFunction(m.Name, m.Parameters, m.Body, false, false, false)
		if err != nil {
			return err
		}
		c.currentSuperClass = prevSuper2
		midx := c.constants.AddConstant(meta)
		c.emitter.Emit(bytecode.OP_FUNCTION, midx) // [ctor, fn]
		keyIdx := c.constants.AddConstant(object.NewString(m.Name))
		c.emitter.Emit(bytecode.OP_SET_PROP, keyIdx) // [ctor]
	}

	// 声明类名
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

// compileClassConstructor 编译 class 的 constructor 函数。
// 若无显式 constructor 则生成默认构造。
// 实例字段赋值指令插入 constructor 开头。
func (c *Compiler) compileClassConstructor(node *ast.ClassDeclaration, ctor *ast.ClassMethod, className, superName string) (*bytecode.FunctionMetadata, error) {
	prevScope := c.scope
	baseSlot := prevScope.NumLocals()
	fnScope := NewSymbolScope(prevScope)
	c.scope = fnScope

	paramSpecs := []bytecode.ParameterSpec{}
	paramSlots := []int{}
	if ctor != nil {
		for _, param := range ctor.Parameters {
			paramSpecs = append(paramSpecs, bytecode.ParameterSpec{Name: param.Name, HasDefault: param.Default != nil, IsRest: param.Rest})
			sym := fnScope.Define(param.Name, false)
			sym.Declared = true // 参数是真实声明: 函数体内 let 同名 → SyntaxError
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
	prevControlStack := c.controlStack
	c.controlStack = nil
	prevPendingLabel := c.pendingLabel
	c.pendingLabel = ""

	// 实例字段赋值: this.field = value
	for _, field := range node.Fields {
		c.emitter.EmitNoOperand(bytecode.OP_THIS)
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
	if ctor != nil {
		if err := c.compileStatements(ctor.Body.Statements); err != nil {
			return nil, err
		}
	}
	c.emitter.EmitNoOperand(bytecode.OP_RETURN_VOID)

	fnIns := c.emitter.Bytes()
	c.emitter = prevEmitter
	c.scope = prevScope
	c.currentArgumentsSlot = prevArgumentsSlot
	c.controlStack = prevControlStack
	c.pendingLabel = prevPendingLabel

	meta := bytecode.NewFunctionMetadata("constructor", fnIns, fnScope.NumLocals(), len(paramSpecs), paramSpecs, false)
	meta.BaseSlot = baseSlot
	meta.ArgumentsSlot = argumentsSlot
	_ = paramSlots
	return meta, nil
}

// compileClassMethodToObject 将 class 实例方法编译为函数并挂到栈顶下方的对象上。
// 栈: [..., obj] → 编译方法函数 → [..., obj, fn] → SET_PROP/SETTER → [..., obj]
func (c *Compiler) compileClassMethodToObject(m *ast.ClassMethod, superName string) error {
	prevSuper := c.currentSuperClass
	c.currentSuperClass = superName
	meta, err := c.compileFunction(m.Name, m.Parameters, m.Body, false, false, false)
	if err != nil {
		return err
	}
	c.currentSuperClass = prevSuper
	midx := c.constants.AddConstant(meta)
	c.emitter.Emit(bytecode.OP_FUNCTION, midx)
	keyIdx := c.constants.AddConstant(object.NewString(m.Name))
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
			for _, name := range stmt.NamedImports {
				c.emitter.EmitNoOperand(bytecode.OP_DUP)
				nameIdx := c.constants.AddConstant(object.NewString(name))
				c.emitter.Emit(bytecode.OP_GET_PROP, nameIdx)
				sym, err := c.declareOnce(name, false, false)
				if err != nil {
					return err
				}
				if c.isGlobalScope() {
					c.emitGlobalDeclare(name)
				} else {
					c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
				}
			}
			c.emitter.EmitNoOperand(bytecode.OP_POP)
		}
	} else if len(stmt.NamedImports) > 0 {
		// import { a, b } from "..."
		for _, name := range stmt.NamedImports {
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
			nameIdx := c.constants.AddConstant(object.NewString(name))
			c.emitter.Emit(bytecode.OP_GET_PROP, nameIdx)
			sym, err := c.declareOnce(name, false, false)
			if err != nil {
				return err
			}
			if c.isGlobalScope() {
				c.emitGlobalDeclare(name)
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
	for _, name := range stmt.NamedImports {
		if _, ok := exports[name]; ok {
			continue
		}
		return fmt.Errorf("import {%s} from \"%s\": %s 没有导出 %q%s",
			name, stmt.Source, stmt.Source, name, importMissHint(stmt.Source, name, exports))
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

func (c *Compiler) compileExportDeclaration(stmt *ast.ExportDeclaration) error {
	// 先编译声明
	if stmt.Declaration != nil {
		if err := c.compileStatement(stmt.Declaration); err != nil {
			return err
		}
	}

	// export { a, b }: 注册导出名
	for _, name := range stmt.NamedExports {
		// 使用 OP_EXPORT 操作数 = 导出名常量索引
		nameIdx := c.constants.AddConstant(object.NewString(name))
		// 加载变量值
		sym := c.scope.Resolve(name)
		if sym != nil {
			c.emitLoad(sym)
		} else {
			c.emitGlobalLoad(name)
		}
		c.emitter.Emit(bytecode.OP_EXPORT, nameIdx)
	}

	// export const x = ... : 自动导出
	if stmt.Declaration != nil && !stmt.IsDefault && len(stmt.NamedExports) == 0 {
		switch decl := stmt.Declaration.(type) {
		case *ast.LetStatement:
			nameIdx := c.constants.AddConstant(object.NewString(decl.Name.Value))
			sym := c.scope.Resolve(decl.Name.Value)
			if sym != nil {
				c.emitLoad(sym)
			}
			c.emitter.Emit(bytecode.OP_EXPORT, nameIdx)
		case *ast.ConstStatement:
			nameIdx := c.constants.AddConstant(object.NewString(decl.Name.Value))
			sym := c.scope.Resolve(decl.Name.Value)
			if sym != nil {
				c.emitLoad(sym)
			}
			c.emitter.Emit(bytecode.OP_EXPORT, nameIdx)
		case *ast.FunctionDeclaration:
			nameIdx := c.constants.AddConstant(object.NewString(decl.Name.Value))
			sym := c.scope.Resolve(decl.Name.Value)
			if sym != nil {
				c.emitLoad(sym)
			}
			c.emitter.Emit(bytecode.OP_EXPORT, nameIdx)
		}
	}

	// export default ...
	if stmt.IsDefault {
		nameIdx := c.constants.AddConstant(object.NewString("default"))
		// 值已经在栈上 (来自 Declaration 的编译)
		// 但 Declaration 可能是 ExpressionStatement (已 POP) 或 FunctionDeclaration (已 STORE)
		if exprStmt, ok := stmt.Declaration.(*ast.ExpressionStatement); ok {
			c.compileExpression(exprStmt.Expression)
		} else if fnDecl, ok := stmt.Declaration.(*ast.FunctionDeclaration); ok {
			sym := c.scope.Resolve(fnDecl.Name.Value)
			if sym != nil {
				c.emitter.Emit(bytecode.OP_LOAD, uint16(sym.Slot))
			}
		}
		c.emitter.Emit(bytecode.OP_EXPORT, nameIdx)
	}

	return nil
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
		// await expr: 在 async 的内层 generator 中编译为 yield expr
		// (恢复时压入 Promise 的 resolved 值作为表达式结果)
		if err := c.compileExpression(node.Argument); err != nil {
			return err
		}
		c.emitter.EmitNoOperand(bytecode.OP_YIELD)
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
// 函数声明与已有函数声明同名除外。
func (c *Compiler) prescanDeclare(name string, isConst, isFnDecl bool) error {
	if sym := c.scope.ResolveLocal(name); sym != nil {
		if isFnDecl && sym.IsFnDecl {
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
// 真实的重复声明 → SyntaxError；函数声明与已认领的函数声明同名除外。
// 解构/class/import 不经 prescan，首次编译到时在此直接登记并认领。
func (c *Compiler) declareOnce(name string, isConst, isFnDecl bool) (*Symbol, error) {
	if sym := c.scope.ResolveLocal(name); sym != nil {
		if sym.Declared && !(isFnDecl && sym.IsFnDecl) {
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
		// LOAD_GLOBAL SuperClass → OP_THIS → 参数 → OP_CALL_METHOD
		// 注意: 这里不 emit POP, 返回值 (父构造结果) 留在栈上由外层语句/表达式消费,
		// 否则表达式语句还会再补一个 POP, 造成双重弹出破坏栈。
		c.emitGlobalLoad(c.currentSuperClass)     // [fn]
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
			c.emitGlobalLoad(c.currentSuperClass)
			pidx := c.constants.AddConstant(object.NewString("prototype"))
			c.emitter.Emit(bytecode.OP_GET_PROP, pidx)
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
		propName := member.Property.(*ast.Identifier).Value
		idx := c.constants.AddConstant(object.NewString(propName))
		c.emitter.Emit(bytecode.OP_GET_PROP, idx) // [obj, fn]
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
		c.emitter.Emit(bytecode.OP_NEW_ARRAY, 0)
		for _, arg := range node.Arguments {
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

func (c *Compiler) compileMemberExpression(node *ast.MemberExpression) error {
	// super.prop: 访问父类 prototype 上的属性
	if super, ok := node.Object.(*ast.SuperExpression); ok {
		_ = super
		if c.currentSuperClass == "" {
			return fmt.Errorf("compiler: super property access outside class")
		}
		c.emitGlobalLoad(c.currentSuperClass)
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
			// 计算属性: [expr]: value
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
	// async 函数: 编译为 wrapper (返回 __spawn(generator)), 内层 generator 处理 await→yield
	if isAsync {
		return c.compileAsyncFunctionSelf(name, selfName, params, body)
	}

	// 创建新的作用域
	prevScope := c.scope
	baseSlot := prevScope.NumLocals() // 函数自身变量的起始槽位
	fnScope := NewSymbolScope(prevScope)
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
		sym.Declared = true // 参数是真实声明: 函数体内 let 同名 → SyntaxError
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

	// 函数边界重置控制流: 标签/break/continue 不能跨函数。
	prevControlStack := c.controlStack
	c.controlStack = nil
	prevPendingLabel := c.pendingLabel
	c.pendingLabel = ""

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
	c.emitter = prevEmitter
	c.scope = prevScope
	c.currentArgumentsSlot = prevArgumentsSlot
	c.controlStack = prevControlStack
	c.pendingLabel = prevPendingLabel

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
	return c.compileAsyncFunctionSelf(name, "", params, body)
}

func (c *Compiler) compileAsyncFunctionSelf(name, selfName string, params []*ast.Parameter, body *ast.BlockStatement) (*bytecode.FunctionMetadata, error) {
	// 1. 编译内层 generator (同一参数, await 编译为 yield)
	// 自引用绑定传播到内层: await 所在的用户代码在内层执行
	genMeta, err := c.compileFunctionSelf(name, selfName, params, body, false, true, false)
	if err != nil {
		return nil, err
	}
	genIdx := c.constants.AddConstant(genMeta)

	// 2. 创建 wrapper 作用域并定义参数
	prevScope := c.scope
	baseSlot := prevScope.NumLocals()
	wrapperScope := NewSymbolScope(prevScope)
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

	meta := bytecode.NewFunctionMetadata(name, wrapperIns, wrapperScope.NumLocals(), len(params), paramSpecs, false)
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
	meta, err := c.compileFunction(name, node.Parameters, getBlockFromBody(node.Body), true, false, false)
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

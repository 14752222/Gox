package compiler

import (
	"fmt"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/lexer"
	"github.com/14752222/Gox/object"
)

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

	// selfClose 是本 context 自己拥有的 for-await close 条目数 (0 或 1)。
	// continue 回到本循环**不等于**穿出它 —— 本轮的 close handler 还要
	// 服务下一轮, 所以 continue 的 try 收尾深度要加上 selfClose, 把自己
	// 的 close 条目排除在解退之外 (解退到它为止, POP_TRY 交给循环底的
	// 常规路径); break / return 穿出循环则照常把它解退并内联 close。
	selfClose int
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

type evalHomeCtx struct {
	Name      string
	Static    bool
	SuperName string
	ThisHome  bool
}

// evalSuperHomeCtx 是 eval 单元的 super home 上下文 (由 vm 编译桥经
// EvalCompileOptions.SuperHome 注入, 见 object.EvalSuperHome)。字段同
// evalHomeCtx; 语义差异: 它描述的是 **eval 源码自己** 的 super 解析目标。

type evalSuperHomeCtx struct {
	Name      string
	Static    bool
	SuperName string
	ThisHome  bool
}

// SetModuleMode 设置模块编译模式。

func (c *Compiler) SetEvalSuperHome(h *object.EvalSuperHome) {
	if h == nil || !h.Has {
		c.evalSuperHome = nil
		return
	}
	c.evalSuperHome = &evalSuperHomeCtx{
		Name:      h.Name,
		Static:    h.Static,
		SuperName: h.SuperName,
		ThisHome:  h.ThisHome,
	}
}

// HasTopLevelAwait 报告本模块顶层是否含 await / for await (TLA)。
// 为真时主单元字节码含 OP_YIELD 挂起点, 模块求值必须由 VM 异步驱动
// (vm.RunCompiledAsync) —— 直接 RunCompiled 会得到 "yield outside generator"。

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
			c.emitter.Emit(bytecode.OP_DECLARE_VAR, nameIdx)
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

// collectVarDeclaratorNames 收集一个 var 声明项的绑定名并登记提升。
//
// 解构声明项 (Name 是合成名 "__destructure__") 收集的是模式里的**真实绑定名**
// —— var 解构的目标与普通 var 一样提升到函数作用域层 (r4McL4): 不提升的话
// 绑定槽位会在块/循环内部才分配 (晚于循环的 sealFrom), 会被 OP_ITER_BOUNDARY
// 当作每轮词法绑定克隆, 且块外读取退化成 OP_LOAD_GLOBAL → ReferenceError。

func (c *Compiler) collectVarDeclaratorNames(name *ast.Identifier, value ast.Expression, declare func(string) error) error {
	if name == nil {
		return nil
	}
	if name.Value == destructureSyntheticName {
		for _, n := range ast.PatternBoundNames(value) {
			if err := declare(n); err != nil {
				return err
			}
		}
		return nil
	}
	return declare(name.Value)
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
		if node.Name != nil {
			if err := c.collectVarDeclaratorNames(node.Name, node.Value, declare); err != nil {
				return err
			}
		}
		for _, d := range node.More {
			if err := c.collectVarDeclaratorNames(d.Name, d.Value, declare); err != nil {
				return err
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
		if _, isVarDecl := node.VarDecl.(*ast.VarStatement); isVarDecl {
			if node.Variable != nil {
				if err := declare(node.Variable.Value); err != nil {
					return err
				}
			}
			// 解构 var (for (var [p] of …)): Variable 为 nil, 真实绑定名只在
			// Pattern 里 —— 同样提升到函数作用域层 (r4McL4)。
			if node.Pattern != nil {
				var names []string
				collectBindingNames(node.Pattern, &names)
				for _, n := range names {
					if err := declare(n); err != nil {
						return err
					}
				}
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
	case *ast.WithStatement:
		// with 体的 var 仍归函数作用域 (with 不改变声明作用域)。
		// 体可以是块 (交给 BlockStatement 分支) 或单条语句 (如 var x = 1)。
		if node.Body != nil {
			if err := c.collectVarBindingsStmt(node.Body, out, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// ===== 语句编译 =====

func (c *Compiler) declareFuncLayerVar(name string) (*Symbol, error) {
	fn := c.scope.FuncLayer()
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

// emitVarAssign 发射 var 绑定的赋值指令 (栈顶值写入该绑定并消费)。
//
// with 体内: var 赋值同样先查 with 对象 (var 绑定在函数层, 属被遮蔽范围)。
// 全局函数层: var 是全局属性。提升初始化已 OP_DECLARE_VAR 过一次, 这里必须
// 走 STORE_GLOBAL (存在则赋值) —— 再发 OP_DECLARE 会撞运行时的重声明检查。

func (c *Compiler) emitVarAssign(name string, sym *Symbol) {
	if c.withAffects(sym) {
		c.emitter.Emit(bytecode.OP_WITH_STORE, c.emitWithRef(c.buildWithRef(name, sym)))
		return
	}
	fn := c.scope.FuncLayer()
	if fn.Parent() == nil && !c.moduleMode {
		nameIdx := c.constants.AddConstant(object.NewString(name))
		c.emitter.Emit(bytecode.OP_STORE_GLOBAL, nameIdx)
	} else {
		c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
	}
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
// 解构 var 借用 let 的合成名路径 (合成名 "__destructure__"), 但绑定落点与
// let 不同: 经 bindPatternTarget 的 bindVar 分支沿 FuncLayer 登记 (见
// bindKind / declareFuncLayerVar)。

func collectUsingDecls(stmts []ast.Statement) []*ast.UsingStatement {
	var out []*ast.UsingStatement
	for _, s := range stmts {
		if us, ok := s.(*ast.UsingStatement); ok {
			out = append(out, us)
		}
	}
	return out
}

// withDisposeScope 编译一个语句列表, 并为其**本层**的 using 声明建立释放作用域:
//   - 为每个声明项分配两个隐藏局部槽 (资源值 / 释放方法), 初始化为 undefined
//     (方法槽保持 undefined = 未登记, 释放时跳过);
//   - 登记 tryScope{dispose} 条目, 运行时对应 PUSH_TRY 0 + PUSH_FINALLY <exit>;
//   - body 正常结束后 POP_TRY + JUMP; exit 处发 OP_DISPOSE_EXIT + OP_END_FINALLY;
//   - return / break / continue 穿出时由 emitTryUnwind 内联 OP_DISPOSE_EXIT;
//   - 抛异常时由运行时 finally 机制跳到 exit 执行释放。
//
// 无 using 声明时直接调 body (零开销)。

func (c *Compiler) withDisposeScope(stmts []ast.Statement, body func() error) error {
	return c.withDisposeScopeFor(collectUsingDecls(stmts), body)
}

// withDisposeScopeFor 与 withDisposeScope 相同, 但直接给出要登记的 using 声明
// (供 for 头部这种「声明不在语句列表里」的场景使用)。

func (c *Compiler) withDisposeScopeFor(usings []*ast.UsingStatement, body func() error) error {
	if len(usings) == 0 {
		return body()
	}

	scope := &bytecode.DisposeScope{}
	byStmt := make(map[*ast.UsingStatement][]*bytecode.DisposeResource)
	for _, us := range usings {
		n := 1 + len(us.More)
		for i := 0; i < n; i++ {
			res := &bytecode.DisposeResource{
				ValueSlot:  c.allocHiddenSlot(),
				MethodSlot: c.allocHiddenSlot(),
				Async:      us.IsAwait,
			}
			scope.Resources = append(scope.Resources, res)
			byStmt[us] = append(byStmt[us], res)
		}
	}
	// 隐藏槽初始化为 undefined。
	for _, res := range scope.Resources {
		c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
		c.emitter.Emit(bytecode.OP_STORE, uint16(res.ValueSlot))
		c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
		c.emitter.Emit(bytecode.OP_STORE, uint16(res.MethodSlot))
	}
	idx := c.constants.AddConstant(scope)

	c.emitter.Emit(bytecode.OP_PUSH_TRY, 0)
	finallyJump := c.emitter.EmitJump(bytecode.OP_PUSH_FINALLY)

	prevMap := c.disposeResources
	c.disposeResources = byStmt
	savedTryLen := len(c.tryScopes)
	c.tryScopes = append(c.tryScopes, tryScope{dispose: &disposeEntry{scope: scope, idx: idx}})
	bodyErr := body()
	if len(c.tryScopes) > savedTryLen {
		c.tryScopes = c.tryScopes[:savedTryLen]
	}
	c.disposeResources = prevMap
	if bodyErr != nil {
		return bodyErr
	}
	// 无 catch 体 ⇒ try 体正常结束后 POP_TRY 紧跟释放代码, 直接落进 finally。
	// (参照 compileTryStatement 里 skipCatch 的落点: 正常路径 JUMP 到 finally
	// 体入口, 绝不跳过它 —— 早期版本把 JUMP 目标回填到 END_FINALLY 之后,
	// 导致「正常落到块尾」这一条路径从不释放。)
	c.emitter.EmitNoOperand(bytecode.OP_POP_TRY)
	c.emitter.PatchJump(finallyJump)
	c.emitter.Emit(bytecode.OP_DISPOSE_EXIT, idx)
	c.emitter.EmitNoOperand(bytecode.OP_END_FINALLY)
	return nil
}

// compileLoopBody 编译循环体语句列表 (while/do/for/for-of/for-in 的 body),
// 并套上 using 释放作用域。调用前调用方已 PUSH_SCOPE 并建立块作用域。

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
		// 先摘掉本条目: finally/close 序列里再抛异常时不该重新进入自己。
		c.emitter.EmitNoOperand(bytecode.OP_POP_TRY)
		if sc.dispose != nil {
			// using 释放条目: 内联发射资源释放序列 (逆序调用各资源的方法)。
			// 进来时栈为空, 离开也是空。
			c.emitter.Emit(bytecode.OP_DISPOSE_EXIT, sc.dispose.idx)
			continue
		}
		if sc.closeSlot >= 0 {
			// 迭代器收尾条目: 内联 IteratorClose (同步 for-of) 或
			// AsyncIteratorClose (for-await)。进来时栈为空 (穿出的控制转移点
			// 都保证语句级干净), 离开也是空。
			if sc.closeSync {
				c.emitSyncIterClose(sc.closeSlot)
			} else {
				c.emitIteratorCloseSequence(sc.closeSlot)
			}
			continue
		}
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

// allocHiddenSlot 分配一个当前函数内的隐藏局部槽 (帧级 locals, 不与任何
// 命名绑定关联)。与 allocFinallyRetSlot 同一套「抬高整条作用域链 nextSlot」
// 的口径, 但不缓存 —— 每个 for-await 各得一个独立槽, 也就无需跨函数
// 保存/恢复编译器状态。

func (c *Compiler) allocHiddenSlot() int {
	slot := 0
	for s := c.scope; s != nil; s = s.Parent() {
		if s.nextSlot > slot {
			slot = s.nextSlot
		}
		if s.IsFuncLayer() {
			break
		}
	}
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

// emitIteratorCloseSequence 内联生成 AsyncIteratorClose 的收尾序列:
// 调用槽内迭代器的 return() (没有就跳过), await 其结果后丢弃。
// 进来栈应为空, 离开也是空。return() 若抛异常, 异常照常向上传播 ——
// 是否吞掉由调用点决定 (node 实测: body 抛异常路径上 close 的异常被
// 吞掉、原异常胜出; break 路径上 close 的异常照常传播), 本函数不处理。
//
// 挂起点与 for-await 步进同口径: async generator 体内 OP_AWAIT,
// 普通 async 函数体内 OP_YIELD。

func (c *Compiler) emitIteratorCloseSequence(slot int) {
	retIdx := c.constants.AddConstant(object.NewString("return"))
	c.emitter.Emit(bytecode.OP_LOAD, uint16(slot)) // [iter]
	c.emitter.EmitNoOperand(bytecode.OP_DUP)       // [iter, iter]
	c.emitter.Emit(bytecode.OP_GET_PROP, retIdx)   // [iter, ret]
	c.emitter.EmitNoOperand(bytecode.OP_DUP)       // [iter, ret, ret]
	noRet := c.emitter.EmitJump(bytecode.OP_JUMP_IF_NULL)
	// 有 return(): CALL_METHOD 0 的栈约定是 [fn, this], 交换成 [ret, iter]
	c.emitter.EmitNoOperand(bytecode.OP_SWAP) // [ret, iter]
	c.emitter.Emit(bytecode.OP_CALL_METHOD, 0)
	if c.asyncGeneratorBody {
		c.emitter.EmitNoOperand(bytecode.OP_AWAIT)
	} else {
		c.emitter.EmitNoOperand(bytecode.OP_YIELD)
	}
	c.emitter.EmitNoOperand(bytecode.OP_POP) // 丢弃 return() 结果 → []
	overNoRet := c.emitter.EmitJump(bytecode.OP_JUMP)
	// 无 return: [iter, ret] → []
	c.emitter.PatchJump(noRet)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.PatchJump(overNoRet)
}

func (c *Compiler) withAffects(sym *Symbol) bool {
	if len(c.withScopes) == 0 {
		return false
	}
	d := -1
	if sym != nil {
		d = sym.Depth
	}
	return c.withScopes[len(c.withScopes)-1].depth >= d
}

// withChainFor 返回该标识符需要**由内向外**依次查询的 with 对象槽位。
// 只包含「比绑定更内层」的 with 上下文 —— 一旦 binding 在某 with 之外,
// 该 with 及其更外层都不应参与 (否则会误读到本应被内层声明遮蔽的属性)。

func (c *Compiler) withChainFor(sym *Symbol) []int {
	d := -1
	if sym != nil {
		d = sym.Depth
	}
	chain := make([]int, 0, len(c.withScopes))
	for i := len(c.withScopes) - 1; i >= 0; i-- {
		if c.withScopes[i].depth < d {
			break
		}
		chain = append(chain, c.withScopes[i].slot)
	}
	return chain
}

// buildWithRef 为一个自由标识符构造常量池引用 (含回退目标)。
// 回退口径与 compileIdentifier/emitLoad 保持一致: 全局符号 (或未绑定)
// 按名回退全局, 局部符号回退槽位。

func (c *Compiler) buildWithRef(name string, sym *Symbol) *bytecode.WithRef {
	ref := &bytecode.WithRef{Name: name, Slots: c.withChainFor(sym)}
	if sym == nil || (sym.Depth == 0 && !c.moduleMode) {
		ref.LocalFallback = false
	} else {
		ref.LocalFallback = true
		ref.FallbackSlot = sym.Slot
		ref.IsConst = sym.IsConst
	}
	return ref
}

// emitWithRef 把 WithRef 加入常量池并返回操作数索引。

func (c *Compiler) emitWithRef(ref *bytecode.WithRef) uint16 {
	return c.constants.AddConstant(ref)
}

// compileWithStatement 编译 with (obj) 语句体。
//
// 实现: 对象表达式在进入 with 前求值**一次**, 经 OP_WITH_ENTER 做 ToObject
// (null/undefined 抛 TypeError), 再存入一个合成局部槽 (OP_STORE)。with 体里
// 受影响的自由标识符编译为 OP_WITH_LOAD/STORE/DELETE, 其 WithRef.Slots 携带
// 该槽位链 —— 运行时按槽取对象逐个查属性。因为槽位是普通局部槽, with 体内
// 创建的闭包天然把它捕获进 CapturedLocals, 于是「闭包引用 with 对象」也能
// 正确解析。

func (c *Compiler) isGlobalScope() bool {
	return c.scope.Depth() == 0 && !c.moduleMode
}

// emitGlobalLoad 发射全局变量加载指令 (按名字查全局环境)。

func computeCapturePrefixLen(ins bytecode.Instructions, baseSlot int, consts *bytecode.ConstantPool) int {
	maxRef := 0
	for off := 0; off+bytecode.InstructionSize <= len(ins); off += bytecode.InstructionSize {
		op, operand := bytecode.ReadInstruction(ins, off)
		switch op {
		case bytecode.OP_LOAD, bytecode.OP_STORE, bytecode.OP_STORE_CONST, bytecode.OP_DECLARE_VAR:
			if int(operand) < baseSlot && int(operand)+1 > maxRef {
				maxRef = int(operand) + 1
			}
		case bytecode.OP_FUNCTION, bytecode.OP_ARROW_FUNC, bytecode.OP_CLOSURE:
			// 本函数体内定义的嵌套函数: 它引用的外层槽必须由本函数代为捕获。
			// 常量池里不是函数元数据 (理论上不该发生) 就跳过 —— 保守取 0。
			if consts == nil {
				continue
			}
			sub, ok := consts.Get(uint16(operand)).(*bytecode.FunctionMetadata)
			if !ok || sub == nil {
				continue
			}
			// 只并入 < baseSlot 的那一段: >= baseSlot 的槽位是**本函数自己的
			// 局部**, 由本函数帧的 Locals 直接提供 (内层闭包在本帧里创建时
			// 正是从 frame.Locals 取它们), 不需要也不该由本函数"捕获" ——
			// 把它们算进来会让前缀膨胀过 baseSlot, 捕获源随之从
			// SharedCells (活 cell) 退回帧数组 (拷贝), 与外层 cell 失联
			// (r6e5qp 那类"读不到更新"的问题)。
			n := sub.CapturePrefixLen
			if n > baseSlot {
				n = baseSlot
			}
			if n > maxRef {
				maxRef = n
			}
		}
	}
	return maxRef
}

const destructureSyntheticName = "__destructure__"

func (c *Compiler) prescanScope(stmts []ast.Statement) error {
	for _, stmt := range stmts {
		switch s := stmt.(type) {
		case *ast.LetStatement:
			if err := c.prescanDeclarator(s.Name, s.Value, false); err != nil {
				return err
			}
			for _, d := range s.More {
				if err := c.prescanDeclarator(d.Name, d.Value, false); err != nil {
					return err
				}
			}
		case *ast.ConstStatement:
			if err := c.prescanDeclarator(s.Name, s.Value, true); err != nil {
				return err
			}
			for _, d := range s.More {
				if err := c.prescanDeclarator(d.Name, d.Value, true); err != nil {
					return err
				}
			}
		case *ast.UsingStatement:
			// using 绑定是 const 语义 (不可赋值): 预登记为 const, 供 TDZ 与
			// 重声明早错复用。
			if s.Name != nil {
				if err := c.prescanDeclare(s.Name.Value, true, false); err != nil {
					return err
				}
			}
			for _, d := range s.More {
				if d.Name != nil {
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
		case *ast.ExportDeclaration:
			// `export function f(){}` / `export const x = …` 在 AST 里是
			// ExportDeclaration 包着真正的声明, 所以上面几个 case 看不到它们。
			// 不拆包的话被导出的绑定永远不参与预登记 —— 症状与 import 那条完全
			// 一样 (函数被提升编译时解析不到同级后位的 export function),
			// 且更常见: 模块里"先写调用方、后写被调方"是自然写法。
			// 只处理"声明式导出"; `export {a}` / `export … from` 不引入新绑定。
			// export default 的匿名声明同样不引入可被引用的名字。
			if s.IsDefault || s.Declaration == nil {
				continue
			}
			if err := c.prescanScope([]ast.Statement{s.Declaration}); err != nil {
				return err
			}
		case *ast.ImportDeclaration:
			// 导入绑定也必须预登记 —— 与上面 let/const 同理, 但缺口更隐蔽:
			// compileStatements 先把列表里的函数声明**整体提升编译**, 再按源码
			// 序编译其余语句。于是 `import {X} from "m"` 写在文件顶部、函数
			// 声明紧跟其后时, 函数体在 import 语句"被编译到"之前就要解析 X。
			// 此时作用域里还没有 X 的符号, 解析退化成 OP_LOAD_GLOBAL, 运行期
			// 报 `ReferenceError: X is not defined` —— 而 X 明明就在同一文件
			// 顶部 import 了。
			//
			// 只在**非全局作用域**预登记: 全局作用域 (入口脚本) 的 import 走
			// OP_DECLARE 写进共享全局环境 (见 compileImportDeclaration 的
			// isGlobalScope 分支), 靠按名查全局侥幸可用; 而模块顶层/函数体/块
			// 必须靠预登记。若在全局层也预登记, compileImportDeclaration 稍后
			// 的 declareOnce 会认领到同一符号从而改走 OP_STORE 局部槽位, 破坏
			// REPL 跨输入的状态保持语义。
			if c.isGlobalScope() {
				continue
			}
			// 命名导入: 绑的是**本地名**, 与 compileImportDeclaration 口径一致。
			for _, item := range s.NamedImports {
				if item.Local == "" {
					continue
				}
				if err := c.prescanDeclare(item.Local, true, false); err != nil {
					return err
				}
			}
			// 默认导入与命名空间导入各绑一个名。
			if s.DefaultName != "" {
				if err := c.prescanDeclare(s.DefaultName, true, false); err != nil {
					return err
				}
			}
			if s.Namespace != "" {
				if err := c.prescanDeclare(s.Namespace, true, false); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// prescanDeclarator 预登记一个声明项的名字。
//
// 普通声明项 (Name 是真实标识符) 直接登记; 解构声明项 (Name 是合成名
// "__destructure__") 登记的是模式里的真实绑定名 —— 它们只存在于
// Value 的 AssignmentExpression 左值 (模式) 中, 靠 ast.PatternBoundNames 取出。
//
// 为什么解构也必须预登记: compileStatements 先把列表里所有函数声明提升编译,
// 再按源码序编译其余语句。于是「function f(){ return x; } 出现在 const [x]=… 之前」
// 时, f 的函数体在 x 被登记之前就要解析 x。全局作用域 (脚本顶层) 靠按名查全局
// 侥幸可用, 但非全局作用域 (模块顶层 / 函数体 / 块) 必须靠预登记, 否则解析失败
// 退化成 OP_LOAD_GLOBAL → 运行期 ReferenceError。
//
// isConst 只作用于**普通**名: 解构声明项一律按 let 语义 (isConst=false) 预登记,
// 与 bindPatternTarget 的解构声明登记口径保持一致 (该路径本就按 let 登记,
// 是既有边界, 不在本次修复范围)。

func (c *Compiler) prescanDeclarator(name *ast.Identifier, value ast.Expression, isConst bool) error {
	if name == nil {
		return nil
	}
	if name.Value == destructureSyntheticName {
		for _, n := range ast.PatternBoundNames(value) {
			if err := c.prescanDeclare(n, false, false); err != nil {
				return err
			}
		}
		return nil
	}
	return c.prescanDeclare(name.Value, isConst, false)
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

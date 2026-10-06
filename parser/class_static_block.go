package parser

import (
	"github.com/14752222/Gox/ast"
)

// ===== ES2022 类静态初始化块 static { ... }（看板单 rJ56bZ, 2026-10-06）=====
//
// 规范 ClassStaticBlock : static { ClassStaticBlockBody }。
//
//   - 与静态字段**同层**: 按定义顺序在类定义求值处执行（parseClassMember 把
//     它和静态字段一样放进 cls.Statics，天然保序; compiler 按顺序发射）。
//   - 块内 `this` = 构造器本身（compiler 以 ctor 为 this 调用合成函数）。
//   - 有独立作用域: ClassStaticBlockBody 是一个 OrdinaryFunctionCreate, 故
//     `var` 不泄漏到类外（compiler 把块体编成独立函数, var 落在该函数作用域）。
//
// 早错（规范 ClassStaticBlockBody / ClassStaticBlock 的 Static Semantics）:
//   - StatementList[~Yield, +Await, ~Return]: return 是语法错误; yield 是语法
//     错误; await 在该上下文是关键字 → ContainsAwait 早错。跨函数边界不判定
//     （内层函数有自己的上下文）。
//   - ContainsArguments: 块内直接引用 arguments 是语法错误（箭头无自己的
//     arguments, 继续向内扫; 普通函数是边界）。
//   - HasDirectSuper: 块内直接 super() 调用是语法错误（super.x 合法）。
//   - ContainsDuplicateLabels: 重复标签是语法错误。
//   - LexicallyDeclaredNames 重复 / 与 VarDeclaredNames 相交: 由
//     parseBlockStatement → checkBlockRedeclaration 覆盖（本文件不重复）。
//
//   - break/continue 的未定义目标由 compiler 的 control 栈校验（报
//     SyntaxError, 满足 test262 negative:parse 判定），本文件不重复实现。

// parseStaticBlock 解析 static { ... } 静态初始化块。
// 入口约定: curToken 位于块的 '{'（parseClassMember 消费 static 后前进到此）。
// 返回时 curToken 前进到块结束 '}' 之后的下一个 token（与其它成员解析一致）。
func (p *Parser) parseStaticBlock(member *ast.ClassMethod) *ast.ClassMethod {
	member.IsStaticBlock = true

	// 块内 +Await 语境: 按规范 await 在此是关键字, 但 ContainsAwait 本身即早错
	// ⇒ 直接置 allowAwait=false, 使 `await <操作数>` 在解析点报 SyntaxError。
	// 内层函数（含箭头以外的函数）各自 setAllowAwait 重置, 不受影响。
	restoreAwait := p.setAllowAwait(false)
	body := p.parseBlockStatement() // 块级早错 (lex/var 重声明) 在此覆盖
	restoreAwait()

	// 上下文早错扫描 (return/yield/arguments/super()/重复标签)。
	if body != nil {
		p.checkClassStaticBlockEarlyErrors(body)
	}
	// 与其它成员解析一致: 前进到块结束 '}' 之后的下一个 token, 否则外层
	// class 成员循环会把块的 '}' 误当类体结束。
	p.nextToken()
	member.Body = body
	return member
}

// classStaticBlockScan 收集静态块体内的上下文敏感早错标记。
type classStaticBlockScan struct {
	hasReturn    bool // ~Return: 块内直接 return
	hasYield     bool // ~Yield: 块内直接 yield
	hasAwait     bool // ContainsAwait: 块内直接 await
	hasArguments bool // ContainsArguments: 块内直接引用 arguments
	hasSuperCall bool // HasDirectSuper: 块内直接 super() 调用
}

// checkClassStaticBlockEarlyErrors 对静态块体做上下文早错扫描。
func (p *Parser) checkClassStaticBlockEarlyErrors(body *ast.BlockStatement) {
	// 已有解析错误时 AST 可能不完整, 继续扫描意义不大且可能误报。
	if len(p.errors.Errors) > 0 {
		return
	}
	var s classStaticBlockScan
	scanClassStaticBlock(body, &s, true)

	if s.hasReturn {
		p.errors.Add("SyntaxError: 'return' not allowed in class static block", body.Token.Line, body.Token.Column)
	}
	if s.hasYield {
		p.errors.Add("SyntaxError: 'yield' not allowed in class static block", body.Token.Line, body.Token.Column)
	}
	if s.hasAwait {
		p.errors.Add("SyntaxError: 'await' not allowed in class static block", body.Token.Line, body.Token.Column)
	}
	if s.hasArguments {
		p.errors.Add("SyntaxError: 'arguments' not allowed in class static block", body.Token.Line, body.Token.Column)
	}
	if s.hasSuperCall {
		p.errors.Add("SyntaxError: 'super' call not allowed in class static block", body.Token.Line, body.Token.Column)
	}
	// ContainsDuplicateLabels（跨嵌套块累计; 函数/类边界切断）。
	if p.staticBlockHasDuplicateLabels(body.Statements, nil) {
		p.errors.Add("SyntaxError: label has already been declared in class static block", body.Token.Line, body.Token.Column)
	}
}

// scanClassStaticBlock 遍历节点, 收集静态块早错标记。
//
// 边界规则（规范「不跨函数/类边界」）:
//   - 普通函数（声明/表达式）与类声明/表达式: 完整边界, 整体跳过;
//   - 箭头函数: 没有自己的 return 作用域边界（return 是其自身的）⇒ 不再向
//     下判定 return; 但箭头没有独立的 arguments, 且 yield/super 继承外层
//     静态块 ⇒ 继续扫描这几个标记（allowReturn=false）。
func scanClassStaticBlock(n ast.Node, s *classStaticBlockScan, allowReturn bool) {
	switch node := n.(type) {
	case nil:
		return
	case *ast.ReturnStatement:
		if allowReturn {
			s.hasReturn = true
		}
		if node.ReturnValue != nil {
			scanClassStaticBlock(node.ReturnValue, s, allowReturn)
		}
		return
	case *ast.YieldExpression:
		s.hasYield = true
		if node.Value != nil {
			scanClassStaticBlock(node.Value, s, allowReturn)
		}
		return
	case *ast.AwaitExpression:
		s.hasAwait = true
		scanClassStaticBlock(node.Argument, s, allowReturn)
		return
	case *ast.Identifier:
		if node.Value == "arguments" {
			s.hasArguments = true
		}
		return
	case *ast.MemberExpression:
		// 非计算属性名 obj.arguments 里的 arguments 是属性名, 不是标识符引用。
		scanClassStaticBlock(node.Object, s, allowReturn)
		if node.Computed {
			scanClassStaticBlock(node.Property, s, allowReturn)
		}
		return
	case *ast.OptionalMemberExpression:
		scanClassStaticBlock(node.Object, s, allowReturn)
		if node.Computed {
			scanClassStaticBlock(node.Property, s, allowReturn)
		}
		return
	case *ast.ObjectLiteral:
		// 字面量的键（非计算）不是引用; 简写 { arguments } 的 Value 才是引用。
		for _, pair := range node.Properties {
			if pair == nil {
				continue
			}
			if pair.Computed {
				scanClassStaticBlock(pair.Key, s, allowReturn)
			}
			scanClassStaticBlock(pair.Value, s, allowReturn)
		}
		for _, sp := range node.Spread {
			scanClassStaticBlock(sp, s, allowReturn)
		}
		return
	case *ast.CallExpression:
		if _, ok := node.Function.(*ast.SuperExpression); ok {
			s.hasSuperCall = true
		}
		scanClassStaticBlock(node.Function, s, allowReturn)
		for _, a := range node.Arguments {
			scanClassStaticBlock(a, s, allowReturn)
		}
		return
	case *ast.NewExpression:
		scanClassStaticBlock(node.Callee, s, allowReturn)
		for _, a := range node.Arguments {
			scanClassStaticBlock(a, s, allowReturn)
		}
		return
	case *ast.ArrowFunctionExpression:
		scanClassStaticBlock(node.Body, s, false)
		return
	case *ast.FunctionExpression, *ast.FunctionDeclaration, *ast.ClassDeclaration, *ast.ClassExpression:
		return // 完整边界
	}
	for _, child := range childNodes(n) {
		scanClassStaticBlock(child, s, allowReturn)
	}
}

// staticBlockHasDuplicateLabels 判定语句列表内是否出现重复标签（规范
// Static Semantics: ContainsDuplicateLabels）。
//
// 口径与规范一致: labelSet 只沿**标签的 LabelledItem 与嵌套语句列表**向下
// 传递, 兄弟语句各用进入时的原 labelSet —— 于是 `x: { x: 0 }` 是重复,
// 而两个互不嵌套的 `{ x: 0 } { x: 0 }` 不算重复（Node 22 实测一致）。
func (p *Parser) staticBlockHasDuplicateLabels(stmts []ast.Statement, labelSet map[string]bool) bool {
	for _, s := range stmts {
		if p.staticBlockStmtHasDuplicateLabels(s, labelSet) {
			return true
		}
	}
	return false
}

func (p *Parser) staticBlockStmtHasDuplicateLabels(s ast.Statement, labelSet map[string]bool) bool {
	switch v := s.(type) {
	case *ast.LabeledStatement:
		label := v.Label.Value
		if labelSet[label] {
			return true
		}
		ns := make(map[string]bool, len(labelSet)+1)
		for k := range labelSet {
			ns[k] = true
		}
		ns[label] = true
		return p.staticBlockStmtHasDuplicateLabels(v.Body, ns)
	case *ast.BlockStatement:
		return p.staticBlockHasDuplicateLabels(v.Statements, labelSet)
	case *ast.IfStatement:
		if v.Consequence != nil && p.staticBlockHasDuplicateLabels(v.Consequence.Statements, labelSet) {
			return true
		}
		if v.Alternative != nil && p.staticBlockHasDuplicateLabels(v.Alternative.Statements, labelSet) {
			return true
		}
	case *ast.ForStatement:
		return v.Body != nil && p.staticBlockHasDuplicateLabels(v.Body.Statements, labelSet)
	case *ast.ForInStatement:
		return v.Body != nil && p.staticBlockHasDuplicateLabels(v.Body.Statements, labelSet)
	case *ast.ForOfStatement:
		return v.Body != nil && p.staticBlockHasDuplicateLabels(v.Body.Statements, labelSet)
	case *ast.WhileStatement:
		return v.Body != nil && p.staticBlockHasDuplicateLabels(v.Body.Statements, labelSet)
	case *ast.DoWhileStatement:
		return v.Body != nil && p.staticBlockHasDuplicateLabels(v.Body.Statements, labelSet)
	case *ast.TryStatement:
		if v.Body != nil && p.staticBlockHasDuplicateLabels(v.Body.Statements, labelSet) {
			return true
		}
		if v.CatchBody != nil && p.staticBlockHasDuplicateLabels(v.CatchBody.Statements, labelSet) {
			return true
		}
		if v.FinallyBody != nil && p.staticBlockHasDuplicateLabels(v.FinallyBody.Statements, labelSet) {
			return true
		}
	case *ast.SwitchStatement:
		for _, c := range v.Cases {
			if c != nil && p.staticBlockHasDuplicateLabels(c.Statements, labelSet) {
				return true
			}
		}
	}
	return false
}

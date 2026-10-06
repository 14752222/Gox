package parser

import (
	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/lexer"
)

// ==================== 块级早错 (rUZN3k 第 1 块) ====================
//
// 规范 sec-block-static-semantics-early-errors:
//   Block : { StatementList }
//   It is a Syntax Error if any element of the LexicallyDeclaredNames of
//   StatementList also occurs in the VarDeclaredNames of StatementList.
//
// 另有若干「声明只能在模块顶层 / 语句位置」的位置早错, 以及同一行缺分号的
// ASI 限制 —— 这些此前完全没拦, 让 test262 的 negative:parse 用例一路跑到
// 运行期 ($DONOTEVALUATE 的抛错), 记为误通过/误失败。
//
// 本文件的检查都在 parseBlockStatement 收尾时对**已解析出的语句列表**做
// 静态扫描 —— 不额外做词法/语法前瞻, 因此不会引入新的解析歧义。

// blockVarDeclaredNames 收集一个语句列表的 VarDeclaredNames。
//
// 规范 (Static Semantics: VarDeclaredNames):
//   - VariableStatement (var) 的绑定名都算;
//   - 嵌套 Block 的 var 也算**当前块**的 VarDeclaredNames (var 提升到函数
//     作用域, 但从块的角度 "名字出现在这个块里"), 这正是
//     inner-block-var-redeclaration-attempt-after-let.js 要抓的形态;
//   - 循环体 (for/while/do)、if/else 体里的 var 同理递归收进来;
//   - 函数声明 / class 声明自身不算 var 名, 但函数**体内**的 var 不外泄
//     (函数边界切断), 所以递归时遇到函数/类/箭头函数体要停下。
//
// 为控制复杂度, 只递归「语句直落 + 普通控制流体 + 嵌套块」这几类容器;
// 更深的结构 (如 for-of 声明头) 交给各自分支处理。
func blockVarDeclaredNames(stmts []ast.Statement) []string {
	var names []string
	for _, s := range stmts {
		names = append(names, stmtVarNames(s)...)
	}
	return names
}

func stmtVarNames(s ast.Statement) []string {
	switch v := s.(type) {
	case *ast.VarStatement:
		return declNames(v.Name, v.More)
	case *ast.BlockStatement:
		return blockVarDeclaredNames(v.Statements)
	case *ast.IfStatement:
		var names []string
		if v.Consequence != nil {
			names = append(names, blockVarDeclaredNames(v.Consequence.Statements)...)
		}
		if v.Alternative != nil {
			names = append(names, blockVarDeclaredNames(v.Alternative.Statements)...)
		}
		return names
	case *ast.ForStatement:
		var names []string
		if v.Init != nil {
			names = append(names, stmtVarNames(v.Init)...)
		}
		if v.Body != nil {
			names = append(names, blockVarDeclaredNames(v.Body.Statements)...)
		}
		return names
	case *ast.ForInStatement:
		var names []string
		names = append(names, forHeadVarNames(v.VarDecl)...)
		if v.Body != nil {
			names = append(names, blockVarDeclaredNames(v.Body.Statements)...)
		}
		return names
	case *ast.ForOfStatement:
		var names []string
		names = append(names, forHeadVarNames(v.VarDecl)...)
		if v.Body != nil {
			names = append(names, blockVarDeclaredNames(v.Body.Statements)...)
		}
		return names
	case *ast.WhileStatement:
		if v.Body != nil {
			return blockVarDeclaredNames(v.Body.Statements)
		}
	case *ast.DoWhileStatement:
		if v.Body != nil {
			return blockVarDeclaredNames(v.Body.Statements)
		}
	case *ast.TryStatement:
		var names []string
		if v.Body != nil {
			names = append(names, blockVarDeclaredNames(v.Body.Statements)...)
		}
		if v.CatchBody != nil {
			names = append(names, blockVarDeclaredNames(v.CatchBody.Statements)...)
		}
		if v.FinallyBody != nil {
			names = append(names, blockVarDeclaredNames(v.FinallyBody.Statements)...)
		}
		return names
	case *ast.SwitchStatement:
		var names []string
		for _, c := range v.Cases {
			if c == nil {
				continue
			}
			names = append(names, blockVarDeclaredNames(c.Statements)...)
		}
		return names
	}
	return nil
}

// forHeadVarNames 收 for-in / for-of 头部的 var 绑定名。
// Gox 里 for 头的 var 可能是 VarStatement 或表达式语句 (赋值目标),
// 只有 var 声明才计入 VarDeclaredNames。
func forHeadVarNames(left ast.Statement) []string {
	if v, ok := left.(*ast.VarStatement); ok {
		return declNames(v.Name, v.More)
	}
	return nil
}

// declNames 把声明名 (可能含合成解构名 __destructure__) 收成名字列表。
// 解构声明 (var [a,b] = x) 在 Gox 里解析成 Name=__destructure__ 的赋值形态,
// 其真实绑定名要到运行时才展开; 这类合成名一律跳过 (不参与早错), 避免误报。
func declNames(name *ast.Identifier, more []ast.Declarator) []string {
	var out []string
	add := func(id *ast.Identifier) {
		if id == nil || id.Value == "" || id.Value == "__destructure__" {
			return
		}
		out = append(out, id.Value)
	}
	add(name)
	for _, d := range more {
		add(d.Name)
	}
	return out
}

// blockLexicalEntry 是一个 LexicallyDeclaredNames 条目。
// sloppyFnDupOK 表示该声明是**普通 function 声明** (非 async / 非 generator):
// 按 annex B (B.3.3), sloppy 模式下块内重复的普通函数声明不作为
// LexicallyDeclaredNames 的重复错误 —— 故参与「与 var 冲突」检查, 但豁免
// 「lexical 重复」检查。async function / generator 声明不在此列 (node 22
// 实测: `{ async function f(){} async function f(){} }` 是 SyntaxError)。
type blockLexicalEntry struct {
	name          string
	sloppyFnDupOK bool
}

// blockLexicallyDeclaredNames 收集一个语句列表的 LexicallyDeclaredNames。
//
// 规范 (Static Semantics: LexicallyDeclaredNames):
//   - let / const 声明;
//   - class 声明;
//   - 函数声明 (在块里是 lexical, 且 **不** 递归进函数体);
//   - 嵌套 Block 的 lexical 名**不**上抛 (block 是独立的词法边界)。
func blockLexicallyDeclaredNames(stmts []ast.Statement) []blockLexicalEntry {
	var out []blockLexicalEntry
	for _, s := range stmts {
		out = append(out, stmtLexicalEntries(s)...)
	}
	return out
}

func stmtLexicalEntries(s ast.Statement) []blockLexicalEntry {
	var names []blockLexicalEntry
	add := func(ns []string, sloppyOK bool) {
		for _, n := range ns {
			names = append(names, blockLexicalEntry{name: n, sloppyFnDupOK: sloppyOK})
		}
	}
	switch v := s.(type) {
	case *ast.LetStatement:
		add(declNames(v.Name, v.More), false)
	case *ast.ConstStatement:
		add(declNames(v.Name, v.More), false)
	case *ast.UsingStatement:
		// using 声明也是 LexicallyDeclaredNames (sec-...-early-errors),
		// 且与 let/const 一样互斥重声明。
		add(declNames(v.Name, v.More), false)
	case *ast.ClassDeclaration:
		if v.Name != nil {
			add([]string{v.Name.Value}, false)
		}
	case *ast.FunctionDeclaration:
		if v.Name != nil {
			// 仅普通同步非 generator 的函数声明享受 annex B 豁免。
			add([]string{v.Name.Value}, !v.IsAsync && !v.IsGenerator)
		}
	}
	return names
}

// checkBlockRedeclaration 执行块级的两条早错:
//  1. LexicallyDeclaredNames ∩ VarDeclaredNames ≠ ∅ ⇒ SyntaxError
//     (sec-block-static-semantics-early-errors);
//  2. LexicallyDeclaredNames 含重复条目 ⇒ SyntaxError (同上),
//     普通函数声明在 sloppy 模式下豁免 (annex B B.3.3)。
//
// 只在块里做 (模块/脚本顶层另有模块语义, 不在此处拦)。
//
// 报错位置取块起始的 { 所在行 (与 V8 报错位置大致对齐, 精确位置不影响
// test262 negative 判定)。
func (p *Parser) checkBlockRedeclaration(block *ast.BlockStatement) {
	if block == nil {
		return
	}
	entries := blockLexicallyDeclaredNames(block.Statements)
	if len(entries) == 0 {
		return
	}
	lexical := make([]string, 0, len(entries))
	for _, e := range entries {
		lexical = append(lexical, e.name)
	}

	// 规则 1: lexical ∩ var
	varNames := blockVarDeclaredNames(block.Statements)
	if len(varNames) > 0 {
		varSet := make(map[string]bool, len(varNames))
		for _, n := range varNames {
			varSet[n] = true
		}
		for _, n := range lexical {
			if varSet[n] {
				p.errors.Add("Identifier '"+n+"' has already been declared",
					block.Token.Line, block.Token.Column)
				return // 一条足够, 避免同一块里重复报
			}
		}
	}

	// 规则 2: lexical 内部重复。
	//
	// annex B (B.3.3) 让 sloppy 模式块内**重复的普通函数声明**不作为重复
	// 错误; 但任一普通函数与**非豁免**声明 (let/const/class/async function/
	// generator) 同名仍是错误。故:
	//   - 非豁免条目: 与见过的任何同名条目 (豁免或非) 冲突;
	//   - 豁免条目:   仅与见过的**非豁免**同名条目冲突。
	// 用两个集合分别记录「见过的非豁免名」与「见过的豁免名」。
	nonExempt := make(map[string]bool, len(entries))
	exemptFn := make(map[string]bool, len(entries))
	for _, e := range entries {
		if e.sloppyFnDupOK {
			// 普通函数: 与已有非豁免同名即冲突。
			if nonExempt[e.name] {
				p.errors.Add("Identifier '"+e.name+"' has already been declared",
					block.Token.Line, block.Token.Column)
				return
			}
			exemptFn[e.name] = true
			continue
		}
		// 非豁免声明: 与任何已有同名 (豁免或非) 即冲突。
		if nonExempt[e.name] || exemptFn[e.name] {
			p.errors.Add("Identifier '"+e.name+"' has already been declared",
				block.Token.Line, block.Token.Column)
			return
		}
		nonExempt[e.name] = true
	}
}

// ==================== for 头部声明 vs 语句体 var 重声明早错 (路线 E) ====================
//
// 规范:
//   sec-for-in-and-for-of-statements-static-semantics-early-errors
//     IterationStatement : for ( ForDeclaration in/of Expression ) Statement
//     - It is a Syntax Error if any element of the BoundNames of ForDeclaration
//       also occurs in the VarDeclaredNames of Statement.
//   sec-for-statement-static-semantics-early-errors
//     ForStatement : for ( LexicalDeclaration Expression? ; Expression? ) Statement
//     - It is a Syntax Error if any element of the BoundNames of LexicalDeclaration
//       also occurs in the VarDeclaredNames of Statement.
//
// ForDeclaration / LexicalDeclaration 只含 let / const / using (含 await using);
// **var 头部**是 VariableDeclaration, 不参与本检查 (for (var x of []) { var x; } 合法)。
//
// 语句体的 VarDeclaredNames 递归进嵌套块 / 控制流 / 嵌套 for 头部, 但**不进函数体**
// (blockVarDeclaredNames / stmtVarNames 在函数边界即停), 与规范一致。
//
// 与 checkBlockRedeclaration 是**两条不同的规则** (那条是「块内 lexical ∩ var」,
// 这条是「for 头部 lexical ∩ 循环体 var」), 不会重复报同一对名字:
//   for (let x of []) { var x; }  ← 循环体块自身没有 lexical 声明, 块检查不触发。
func (p *Parser) checkForHeadRedeclaration(stmt ast.Statement) {
	var headNames []string
	var body *ast.BlockStatement
	var tok lexer.Token
	switch v := stmt.(type) {
	case *ast.ForOfStatement:
		if v == nil {
			return
		}
		headNames = forOfHeadLexicalNames(v)
		body, tok = v.Body, v.Token
	case *ast.ForInStatement:
		if v == nil {
			return
		}
		headNames = forDeclHeadNames(v.VarDecl)
		body, tok = v.Body, v.Token
	case *ast.ForStatement:
		if v == nil {
			return
		}
		headNames = forDeclHeadNames(v.Init)
		body, tok = v.Body, v.Token
	default:
		return
	}
	if len(headNames) == 0 || body == nil {
		return
	}
	varSet := make(map[string]bool)
	for _, n := range blockVarDeclaredNames(body.Statements) {
		varSet[n] = true
	}
	for _, n := range headNames {
		if varSet[n] {
			p.errors.Add("Identifier '"+n+"' has already been declared", tok.Line, tok.Column)
			return // 一条足够, 避免同一 for 里重复报
		}
	}
}

// forDeclHeadNames 取一条 for 头部**词法声明** (ForDeclaration / LexicalDeclaration)
// 的 BoundNames; var 头部 (VariableDeclaration) 或非声明头部返回 nil。
func forDeclHeadNames(head ast.Statement) []string {
	switch v := head.(type) {
	case *ast.LetStatement:
		if v == nil {
			return nil
		}
		return append(declNames(v.Name, v.More), destructuredNamesIn(v.Name, v.Value, v.More)...)
	case *ast.ConstStatement:
		if v == nil {
			return nil
		}
		return append(declNames(v.Name, v.More), destructuredNamesIn(v.Name, v.Value, v.More)...)
	case *ast.UsingStatement:
		if v == nil {
			return nil
		}
		return append(declNames(v.Name, v.More), destructuredNamesIn(v.Name, v.Value, v.More)...)
	}
	return nil
}

// forOfHeadLexicalNames 取 for-of 头部的词法绑定名。
//
// 与 for-in / 传统 for 不同: for-of 的**解构绑定**不走声明项的 Value, 而是把真实
// 名字放在 ForOfStatement.Pattern 里, VarDecl 只留一个合成名空壳 (见 ast 注释)。
// 这里补上 Pattern 的绑定名 (与赋值解构无关: VarDecl==nil 的赋值形态无词法声明)。
func forOfHeadLexicalNames(v *ast.ForOfStatement) []string {
	names := forDeclHeadNames(v.VarDecl)
	if v.Pattern != nil {
		// 复用共享的模式遍历 (ast.PatternBoundNames 期望初始值形如
		// AssignmentExpression{Left: 模式}, 这里包一层同一形状)。
		names = append(names, ast.PatternBoundNames(&ast.AssignmentExpression{Left: v.Pattern})...)
	}
	return names
}

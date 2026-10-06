package parser

import (
	"github.com/14752222/Gox/ast"
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

// blockLexicallyDeclaredNames 收集一个语句列表的 LexicallyDeclaredNames。
//
// 规范 (Static Semantics: LexicallyDeclaredNames):
//   - let / const 声明;
//   - class 声明;
//   - 函数声明 (在块里是 lexical, 且 **不** 递归进函数体);
//   - 嵌套 Block 的 lexical 名**不**上抛 (block 是独立的词法边界)。
//
// 注意: 只有「语句位置」的声明才算。if 体里裸的 function 声明 (annexB)
// 与本题无关, 这里也照收 —— 与 var 冲突时同样报冲突。
func blockLexicallyDeclaredNames(stmts []ast.Statement) []string {
	var out []string
	for _, s := range stmts {
		out = append(out, stmtLexicalNames(s)...)
	}
	return out
}

func stmtLexicalNames(s ast.Statement) []string {
	switch v := s.(type) {
	case *ast.LetStatement:
		return declNames(v.Name, v.More)
	case *ast.ConstStatement:
		return declNames(v.Name, v.More)
	case *ast.ClassDeclaration:
		if v.Name != nil {
			return []string{v.Name.Value}
		}
	case *ast.FunctionDeclaration:
		if v.Name != nil {
			return []string{v.Name.Value}
		}
	}
	return nil
}

// checkBlockRedeclaration 执行「LexicallyDeclaredNames ∩ VarDeclaredNames ≠ ∅
// ⇒ SyntaxError」。只在块里做 (模块/脚本顶层另有模块语义, 不在此处拦)。
//
// 报错位置取块起始的 { 所在行 (与 V8 报错位置大致对齐, 精确位置不影响
// test262 negative 判定)。
func (p *Parser) checkBlockRedeclaration(block *ast.BlockStatement) {
	if block == nil {
		return
	}
	lexical := blockLexicallyDeclaredNames(block.Statements)
	if len(lexical) == 0 {
		return
	}
	varNames := blockVarDeclaredNames(block.Statements)
	if len(varNames) == 0 {
		return
	}
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

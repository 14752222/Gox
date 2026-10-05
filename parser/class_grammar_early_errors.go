package parser

import (
	"fmt"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/lexer"
)

// ===== class 语法级早错集中校验（rpEXH2 剩余类别, 2026-10-05）=====
//
// 与 class_early_errors.go（重复私有名 / 未声明私有名 / 字段初始化器 arguments·
// super）互补，这里集中处理规范 ClassElement / ClassTail 早错里**按成员名与
// 成员形态**判定的那一批（此前解析器全部放行，negative 用例因此全红）:
//
//  1. static 成员命名为 "prototype"（字段或方法；含 get/set/gen/async 各种
//     形态）。规范: ClassElement : static MethodDefinition 与 static
//     FieldDefinition 的 PropName 为 "prototype" 都是 SyntaxError。**未加**
//     static 的同名实例成员合法；计算属性名（[expr]）与私有名（#prototype）
//     豁免。命名字段 "constructor" 已由 parseClassMember 就地拦截。
//  2. 特殊方法命名为 "constructor"（getter/setter/generator/async）。规范:
//     SpecialMethod 为 true 时 PropName 不得为 "constructor"。
//  3. 同一 class 体出现多于一个 constructor。
//  4. 方法体内**直接**调用 super()（HasDirectSuper）: 非 constructor 方法一律
//     非法；static 方法一律非法；constructor 仅在无 heritage 时非法。
//     super.x 属性访问合法，不拦；箭头函数继续向内扫（继承外层 super），
//     内层普通函数有自己的 super 语境，不下钻。
//
// 口径全部经 Node 22 实测（见提交说明）。挂载点: parseClassDeclaration /
// parseClassExpression 成员循环之后（与 checkClassEarlyErrors 同处）。

// checkClassGrammarEarlyErrors 校验上述四类 class 语法早错。
// superClass 为 nil 表示该类没有 extends 子句。
func (p *Parser) checkClassGrammarEarlyErrors(superClass ast.Expression, methods, statics []*ast.ClassMethod, fields []*ast.ClassField) {
	// 解析已出错时 AST 可能不完整（如对象模式里残留 nil Property），
	// 继续遍历会 panic；此时也没必要再补早错（本身已是解析失败）。
	if len(p.errors.Errors) > 0 {
		return
	}
	// ---- 1. static 成员名不得为 "prototype" ----
	for _, m := range statics {
		if m.ComputedKey != nil || m.IsPrivate {
			continue // 计算属性名 / #私有名豁免
		}
		if m.Name == "prototype" {
			p.addError("SyntaxError: classes may not have a static property named 'prototype'")
		}
	}

	// ---- 2. 特殊方法名不得为 "constructor" + 3. 重复 constructor ----
	ctorCount := 0
	for _, m := range methods {
		if !m.IsConstructor {
			continue
		}
		ctorCount++
		switch {
		case m.IsGetter || m.IsSetter:
			p.addError("SyntaxError: class constructor may not be an accessor")
		case m.IsGenerator:
			p.addError("SyntaxError: class constructor may not be a generator")
		case m.IsAsync:
			p.addError("SyntaxError: class constructor may not be an async method")
		}
	}
	if ctorCount > 1 {
		p.addError("SyntaxError: a class may only have one constructor")
	}

	// ---- 4. HasDirectSuper ----
	// 非 constructor 方法体内直接 super() 调用 → 早错。
	// constructor 仅在无 heritage 时非法（有 heritage 的 super() 是正常转发）。
	for _, m := range methods {
		if m.Body == nil || !exprContainsSuperCall(m.Body) {
			continue
		}
		if m.IsConstructor {
			if superClass == nil {
				p.addError("SyntaxError: 'super' keyword unexpected here")
			}
		} else {
			p.addError("SyntaxError: 'super' keyword unexpected here")
		}
	}
	// static 方法体内直接 super() 调用 → 早错（static 无构造语义）。
	for _, m := range statics {
		if m.Body != nil && exprContainsSuperCall(m.Body) {
			p.addError("SyntaxError: 'super' keyword unexpected here")
		}
	}
}

// checkClassFieldTermination 校验 ClassElement : FieldDefinition 的 ASI 约束。
//
// 规范里字段定义无逗号/无分号时的结束只允许两种: 显式 ';'，或受限产生式的
// ASI（下一个 token 前有行终止符），外加紧跟 '}' 的情况。若字段（裸字段或带
// 初始化器）结束后，下一个 token 既不是 ';'/'}'/EOF、又与字段末 token 处于
// **同一行**，则是 SyntaxError。Node 22 实测:
//
//	class C { x y }            // SyntaxError: Unexpected identifier 'y'
//	class C { x = 1 method(){} }// SyntaxError: Unexpected identifier 'method'
//	class C { #x #y }          // SyntaxError: Unexpected identifier '#y'
//	class C { get # m(){} }    // SyntaxError: Invalid or unexpected token
//	class C { x\n y }          // 合法（ASI）
//
// 调用时机: 字段分支做完最后一次 nextToken 之后、返回之前（此时 p.curToken()
// 是字段后的 token，p.tokens[p.pos-1] 是字段末 token）。
func (p *Parser) checkClassFieldTermination() {
	if p.pos == 0 || p.pos > len(p.tokens) {
		return
	}
	cur := p.curToken()
	switch cur.Type {
	case lexer.SEMICOLON, lexer.RBRACE, lexer.EOF:
		return
	}
	prev := p.tokens[p.pos-1]
	if cur.Line != prev.Line {
		return // ASI: 换行分隔
	}
	p.addError(fmt.Sprintf("SyntaxError: unexpected token %s after class field (missing ';'?)", cur.Type))
}

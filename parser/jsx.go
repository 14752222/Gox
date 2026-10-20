package parser

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/lexer"
)

// JSX 解析: 在 parser 层把 JSX 语法糖直接降级为普通调用表达式,
// compiler 与字节码无需任何改动。
//
//	<text font={20}>hi</text>          →  h("text", {font: 20}, "hi")
//	<rect width={8} />                 →  h("rect", {width: 8})
//	<Counter step={2}>hi</Counter>     →  Counter({step: 2}, "hi")
//
// 约定:
//   - 小写开头的标签降级为字符串; 大写开头视为组件, 降级为作用域内的
//     标识符引用 (词法解析 —— 组件的**绑定**不自动注入, 只有下面说的
//     JSX 工厂 h 例外)。
//   - 元素标签用到的工厂名恒为 `h`。若本文件没有绑定 h, compiler 会自动补
//     一条 `import { h } from "gx/gfx"` (ast.Program.UsesJSX 这个标记就是
//     为此留的) —— 于是"用了 JSX 却忘了 import h"不再是一次挂载期崩溃
//     (ReferenceError: h is not defined), 而是照常出窗口。本文件已绑定 h
//     (import / let / const / function ...) 时一个字节都不动: 自定义工厂优先。
//   - 属性名含 - 或 : 时用字符串键 (data-id), 否则用标识符键。
//   - 无属性时第二个参数传 null。
//   - 子节点按出现顺序作为 h 的后续参数。插值表达式 `{expr}` 降级成
//     `() => expr` (响应式 computed 约定, 由渲染侧包 effect 订阅), 所以
//     `<text>count:{ count()}</text>` 是活的; 常量字面量、**显式**函数子节点
//     `{() => ...}` 与裸标识符 `{sig}` 原样传递 (见 jsxReactiveChild)。
//   - 子文本空白按 Babel/Solid 规则规整: 逐行首尾去空白, 纯空白行删除,
//     保留行以单个空格连接。

// parseJSXElement 解析一个 JSX 元素并降级为 h(...) 调用。
// 进入时 curToken 是 JSX_LT; 返回时 curToken 位于整个元素结束之后。
func (p *Parser) parseJSXElement() ast.Expression {
	if !p.enterNesting("JSX element") {
		return nil
	}
	defer p.leaveNesting()

	ltTok := p.curToken()
	if !p.peekTokenIs(lexer.IDENTIFIER) {
		p.addError(fmt.Sprintf("expected JSX tag name after '<', got %s", p.peekToken().Type))
		return nil
	}
	p.nextToken() // cur = 标签名
	nameTok := p.curToken()
	p.nextToken() // cur = 第一个属性名 / GT / JSX_SELF_CLOSE

	// 小写标签 (元素) 要降级成 h(...) 调用 ⇒ 本文件必须有一个 h 在作用域里。
	// 记下来是为了让 compiler 在缺 h 时自动补 `import { h } from "gx/gfx"`,
	// 而不是等到挂载那一刻才 ReferenceError (见 buildJSXCall 的说明)。
	// 大写标签 (<Counter/>) 只是组件调用, 不需要 h。
	if jsxDesugarsToFactory(nameTok) {
		p.usedJSXFactory = true
	}

	// ---- 属性区 ----
	props := []*ast.Property{}
	for p.curTokenIs(lexer.IDENTIFIER) {
		attrTok := p.curToken()
		var val ast.Expression

		if p.peekTokenIs(lexer.ASSIGN) {
			p.nextToken() // cur = '='
			p.nextToken() // cur = 属性值开始
			switch {
			case p.curTokenIs(lexer.STRING_LITERAL):
				val = &ast.StringLiteral{Token: p.curToken(), Value: p.curToken().Literal}
			case p.curTokenIs(lexer.LBRACE):
				p.nextToken() // 进入插值表达式
				if p.curTokenIs(lexer.RBRACE) {
					// attr={} → null (空插值无意义, 与省略等价)
					val = &ast.NullLiteral{Token: nullToken(attrTok)}
				} else {
					val = p.parseExpression(LOWEST)
					// 约定: parseExpression 返回后 cur 停在表达式最后一个令牌,
					// 结束的 } 位于 peek 位
					if !p.peekTokenIs(lexer.RBRACE) {
						p.addError(fmt.Sprintf("expected '}' to close JSX attribute value, got %s", p.peekToken().Type))
						return nil
					}
					p.nextToken() // cur = '}'
				}
			default:
				p.addError(fmt.Sprintf("JSX attribute value must be a string or {expression}, got %s", p.curToken().Type))
				return nil
			}
		} else {
			// 裸属性 (如 <input disabled />) → true
			val = &ast.BooleanLiteral{
				Token: lexer.Token{Type: lexer.TRUE, Literal: "true", Line: attrTok.Line, Column: attrTok.Column},
				Value: true,
			}
		}

		props = append(props, &ast.Property{
			Token: attrTok,
			Key:   jsxPropKey(attrTok),
			Value: val,
			Kind:  ast.PROP_INIT,
		})
		p.nextToken() // cur = 下一个属性名 / GT / JSX_SELF_CLOSE
	}

	// ---- 子节点区 ----
	var children []ast.Expression
	switch {
	case p.curTokenIs(lexer.JSX_SELF_CLOSE):
		// cur 停在 /> 上 (引擎约定: 前缀函数结束时 cur 位于表达式最后一个令牌)
	case p.curTokenIs(lexer.GT):
		p.nextToken() // cur = 第一个子节点令牌
		for {
			switch {
			case p.curTokenIs(lexer.JSX_TEXT):
				if text := normalizeJSXText(p.curToken().Literal); text != "" {
					children = append(children, &ast.StringLiteral{Token: p.curToken(), Value: text})
				}
				p.nextToken()
			case p.curTokenIs(lexer.LBRACE):
				braceTok := p.curToken()
				p.nextToken() // 进入子节点插值表达式
				if !p.curTokenIs(lexer.RBRACE) {
					expr := p.parseExpression(LOWEST)
					if expr == nil {
						return nil
					}
					// 约定: parseExpression 返回后 cur 停在表达式最后一个令牌,
					// 结束的 } 位于 peek 位
					if !p.peekTokenIs(lexer.RBRACE) {
						p.addError(fmt.Sprintf("expected '}' to close JSX child expression, got %s", p.peekToken().Type))
						return nil
					}
					p.nextToken() // cur = '}'
					children = append(children, jsxReactiveChild(braceTok, expr))
				}
				// {} 空插值 → 跳过 (Babel 语义: 无子节点)
				p.nextToken()
			case p.curTokenIs(lexer.JSX_LT):
				child := p.parseJSXElement()
				if child == nil {
					return nil
				}
				children = append(children, child)
				// 子元素结束时 cur 停在它自己的闭合令牌上, 越过它回到父级子节点流
				p.nextToken()
			case p.curTokenIs(lexer.JSX_CLOSE):
				if p.curToken().Literal != nameTok.Literal {
					p.addError(fmt.Sprintf("JSX closing tag mismatch: expected </%s>, got </%s>",
						nameTok.Literal, p.curToken().Literal))
					return nil
				}
				// cur 停在 </name> 上, 由调用方越过
				goto elementDone
			case p.curTokenIs(lexer.EOF):
				p.addError(fmt.Sprintf("unterminated JSX element <%s>", nameTok.Literal))
				return nil
			default:
				p.addError(fmt.Sprintf("unexpected token %s in JSX children", p.curToken().Type))
				return nil
			}
		}
	default:
		p.addError(fmt.Sprintf("expected '>' or '/>' in JSX tag <%s>, got %s", nameTok.Literal, p.curToken().Type))
		return nil
	}

elementDone:
	return buildJSXCall(ltTok, nameTok, props, children)
}

// buildJSXCall 组装降级结果:
//
//	小写元素:  h("tag", props, ...children)
//	组件标签:  Comp(props, ...children)   (大写开头, 作用域内标识符)
func buildJSXCall(ltTok, nameTok lexer.Token, props []*ast.Property, children []ast.Expression) ast.Expression {
	var propsArg ast.Expression
	if len(props) == 0 {
		propsArg = &ast.NullLiteral{Token: nullToken(ltTok)}
	} else {
		propsArg = &ast.ObjectLiteral{Token: ltTok, Properties: props}
	}
	args := append([]ast.Expression{propsArg}, children...)

	if ident, ok := jsxTagArg(nameTok).(*ast.Identifier); ok {
		return &ast.CallExpression{Token: ltTok, Function: ident, Arguments: args}
	}
	return &ast.CallExpression{
		Token:     ltTok,
		Function:  &ast.Identifier{Token: ltTok, Value: "h"},
		Arguments: append([]ast.Expression{jsxTagArg(nameTok)}, args...),
	}
}

// jsxReactiveChild 把子节点插值表达式**包成箭头函数**, 除非它本来就该是
// 静态值或函数。
//
// 这是 Solid 的 JSX 语义: `{count()}` 与 `{() => count()}` 等价。差别只在
// 求值时机 —— 不包的话, 表达式在 h() 被调用之前就求值完了, 渲染侧收到的是
// 一个标量 (数字/字符串), 只能建成静态文本节点; 之后 signal 怎么变界面都不动,
// 而且不报错不警告 (文本节点天生收标量)。包一层之后它变成函数子节点, 由渲染
// 侧包 effect 订阅, 于是 `<text>count:{ count()}</text>` 才是活的。
//
// 不包的三类 (包了就是错的):
//   - 字面量常量: `{"hi"}` / `{42}` 本身就是常量, 包一层只多一个空转的 effect。
//     (模板字面量不算 —— “ {`hi ${name}`} “ 含插值, 照样要包);
//   - 箭头/函数表达式: `{() => ...}` 是用户**显式**写下的函数子节点约定, 再包
//     一层就成了"返回函数的函数", 渲染侧只能把它 ToString 成一段文本;
//   - 裸标识符: `{Row}` / `{sig}` 与函数子节点/取值函数约定同形 —— each 的
//     渲染函数子节点就是这么传的, 包一层会让 For 引擎拿到"返回函数的函数"
//     (每行渲染出 [Function: ...])。裸标识符原样传递后, 函数值的响应式由
//     wireChild / mountValue 的 callable 分支兜住; 普通数组变量仍是静态的,
//     要响应式写调用形态 `{rows()}`。
//
// 位置信息用 `{` 令牌: 降级出来的箭头在源码里没有对应的 "=>", 报错行号落在
// 插值起点最接近用户看到的代码。
func jsxReactiveChild(braceTok lexer.Token, expr ast.Expression) ast.Expression {
	if expr == nil || isStaticJSXChild(expr) {
		return expr
	}
	return &ast.ArrowFunctionExpression{
		Token: lexer.Token{Type: lexer.ARROW, Literal: "=>", Line: braceTok.Line, Column: braceTok.Column},
		Body:  expr,
	}
}

// isStaticJSXChild 报告这个子节点表达式是否**不需要**包响应式箭头。
// 判据是"它是不是一个已经定了的值 / 已经是函数子节点约定"。
func isStaticJSXChild(expr ast.Expression) bool {
	switch expr.(type) {
	case *ast.ArrowFunctionExpression, *ast.FunctionExpression, *ast.Identifier:
		return true // 显式函数子节点 / 裸标识符 (见 jsxReactiveChild 注释)
	case *ast.StringLiteral, *ast.IntegerLiteral, *ast.FloatLiteral,
		*ast.BigIntLiteral, *ast.BooleanLiteral, *ast.NullLiteral:
		return true // 字面量常量
	}
	return false
}

// nullToken 造一个 Literal 为 "null" 的令牌 (NullLiteral.String 需要)。
func nullToken(ref lexer.Token) lexer.Token {
	return lexer.Token{Type: lexer.NULL, Literal: "null", Line: ref.Line, Column: ref.Column}
}

// jsxTagArg 生成标签参数: 小写开头 → 字符串; 大写开头 → 组件标识符引用。
func jsxTagArg(nameTok lexer.Token) ast.Expression {
	name := nameTok.Literal
	if first, _ := utf8.DecodeRuneInString(name); unicode.IsUpper(first) {
		return &ast.Identifier{Token: nameTok, Value: name}
	}
	return &ast.StringLiteral{Token: nameTok, Value: name}
}

// jsxDesugarsToFactory 报告一个标签是否会降级成 h(...) 调用:
// 小写开头的是元素 (走 h), 大写开头的是组件 (直接调用标识符)。
// 与 buildJSXCall 同源 (都从 jsxTagArg 的返回值判类型), 不重复分类规则。
func jsxDesugarsToFactory(nameTok lexer.Token) bool {
	_, isComponent := jsxTagArg(nameTok).(*ast.Identifier)
	return !isComponent
}

// jsxPropKey 生成属性键。本引擎的对象属性键统一用 Identifier 承载,
// Value 放原始名字 (含 - 或 : 的名字与 {"a-b": 1} 的处理方式一致)。
func jsxPropKey(attrTok lexer.Token) ast.Expression {
	return &ast.Identifier{Token: attrTok, Value: attrTok.Literal}
}

// normalizeJSXText 规整 JSX 子文本空白 (Babel/Solid 语义):
// 逐行首尾去空白、删除纯空白行, 保留行以单个空格连接。
func normalizeJSXText(raw string) string {
	lines := strings.Split(raw, "\n")
	kept := make([]string, 0, len(lines))
	for _, ln := range lines {
		if t := strings.TrimSpace(ln); t != "" {
			kept = append(kept, t)
		}
	}
	return strings.Join(kept, " ")
}

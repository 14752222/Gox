package parser

import (
	"fmt"

	"github.com/14752222/Gox/ast"
)

// ===== strict 模式的解析期早错 (r63RpV Phase 1) =====
//
// 规范 (ES2023 §11.2.2 / §14.1.2 / §15.1 等): 同一份形参列表里出现重复名、
// 或形参名叫 eval / arguments, 在严格模式下都是 SyntaxError; 非严格下前者
// 合法 (后位同名参数覆盖前位)。判定 strict 的时点必须在**函数体解析完之后**
// —— 因为 `function f(a, a) { "use strict"; }` 的严格性来自函数体内的指令,
// 而形参在指令之前就已解析完。故这些检查都在 parseFunctionBodyWithStrict
// 返回"生效 strict"之后调用。

// checkStrictFunctionParams 对"生效为 strict"的函数做形参名早错校验:
//   - 重复形参名 ⇒ SyntaxError;
//   - 形参名叫 eval / arguments ⇒ SyntaxError。
// 只报第一条错, 避免同一形参列表刷屏。
func (p *Parser) checkStrictFunctionParams(params []*ast.Parameter) {
	seen := map[string]bool{}
	for _, param := range params {
		if param == nil {
			continue
		}
		var names []string
		if param.Pattern != nil {
			collectPatternNames(param.Pattern, &names)
		} else if param.Name != "" {
			names = append(names, param.Name)
		}
		for _, name := range names {
			if seen[name] {
				p.addError(fmt.Sprintf(
					"SyntaxError: duplicate parameter name '%s' is not allowed in strict mode", name))
				return
			}
			seen[name] = true
			if name == "eval" || name == "arguments" {
				p.addError(fmt.Sprintf(
					"SyntaxError: '%s' is not a valid parameter name in strict mode", name))
				return
			}
		}
	}
}

// checkClassMethodsStrictParams 对类方法 (恒严格) 做形参名早错校验。
func (p *Parser) checkClassMethodsStrictParams(methods, statics []*ast.ClassMethod) {
	check := func(list []*ast.ClassMethod) {
		for _, m := range list {
			if m != nil {
				p.checkStrictFunctionParams(m.Parameters)
			}
		}
	}
	check(methods)
	check(statics)
}

// isSimpleParameterList 报告形参列表是否为「简单形参列表」
// (规范 14.1.2 IsSimpleParameterList): 每个形参都是不带默认值的 SingleNameBinding。
// 任一形参带默认值、是解构模式、或是 rest, 列表即为非简单。
func isSimpleParameterList(params []*ast.Parameter) bool {
	for _, param := range params {
		if param == nil {
			continue
		}
		if param.Rest || param.Default != nil || param.Pattern != nil {
			return false
		}
	}
	return true
}

// checkNonSimpleDuplicateParams 报告非简单形参列表里的重复绑定名早错。
// 规范 (ES2023 §14.1.2 Static Semantics: Early Errors):
//
//	FormalParameters : FormalParameterList
//	  - It is a Syntax Error if IsSimpleParameterList is false and
//	    BoundNames contains any duplicate elements.
//
// 即 function f(x = 0, x) {} / function f([x], x) {} / function f(...x, ...y) {}
// 在**非严格**下也是 SyntaxError; 而简单列表的重复名在 sloppy 下合法
// (后位覆盖前位), 由 checkStrictFunctionParams 只在 strict 时拦。
func (p *Parser) checkNonSimpleDuplicateParams(params []*ast.Parameter) {
	if isSimpleParameterList(params) {
		return
	}
	seen := map[string]bool{}
	for _, param := range params {
		if param == nil {
			continue
		}
		var names []string
		if param.Pattern != nil {
			collectPatternNames(param.Pattern, &names)
		} else if param.Name != "" {
			names = append(names, param.Name)
		}
		for _, name := range names {
			if seen[name] {
				p.addError(fmt.Sprintf(
					"SyntaxError: duplicate parameter name '%s' is not allowed with a non-simple parameter list", name))
				return
			}
			seen[name] = true
		}
	}
}

// collectPatternNames 把解构模式里的所有绑定名收集到 out (数组/对象可嵌套)。
// 只收集 Identifier 目标; 空洞 (Target==nil) 与默认值表达式跳过。
func collectPatternNames(expr ast.Expression, out *[]string) {
	switch n := expr.(type) {
	case *ast.Identifier:
		*out = append(*out, n.Value)
	case *ast.ArrayPattern:
		for _, e := range n.Elements {
			if e == nil || e.Target == nil {
				continue
			}
			collectPatternNames(e.Target, out)
		}
	case *ast.ObjectPattern:
		for _, pr := range n.Properties {
			if pr == nil || pr.Value == nil {
				continue
			}
			collectPatternNames(pr.Value, out)
		}
	}
}

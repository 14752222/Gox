package parser

import (
	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/lexer"
)

// ==================== 模块 (ModuleItemList) 早期错误 (rTI1PN) ====================
//
// 规范 sec-module-semantics-static-semantics-early-errors 对 ModuleItemList 有
// 若干**静态语义早错** —— 只要解析出 AST 就能判定, 不需要执行:
//
//   1. LexicallyDeclaredNames 有重复条目 ⇒ SyntaxError。
//      (模块顶层 function/class/let/const 都是 lexical; 不像 sloppy 脚本里
//       普通 function 可重复。见 early-dup-top-function*.js)
//   2. LexicallyDeclaredNames ∩ VarDeclaredNames ≠ ∅ ⇒ SyntaxError。
//      (var f; function f(){} —— 见 parse-err-hoist-lex-fun.js)
//   3. ExportedNames 有重复条目 ⇒ SyntaxError。
//      (export {x}; export {x} / export default; export {y as default}
//       / export {x as z}; export * as z from "m" —— early-dup-export-*.js)
//   4. ExportedBindings ⊆ (VarDeclaredNames ∪ LexicallyDeclaredNames) ⇒ 否则
//      SyntaxError (export { unresolvable } / export { Number } ——
//       early-export-unresolvable.js / early-export-global.js)。
//   5. 模块恒严格: 严格保留字 (public/private/yield/…)、eval、arguments 不能作
//      顶层绑定名 (early-strict-mode.js / early-import-{eval,arguments}.js)。
//
// 本文件在 ParseProgram 收尾时对**已解析出的顶层语句列表**做一遍静态扫描 ——
// 不引入新的词法/语法前瞻, 因此不会制造解析歧义。门控在 Parser.moduleEE:
// test262 的 module 用例在 Gox 里按脚本执行 (入口 compileSource(src,false)),
// 但语义上是模块, 故宿主 (vm/runner) 经 SetModuleEarlyErrors 单独开启本检查。
// 普通脚本 (moduleEE=false) 完全不受影响。

// moduleBinding 是一个顶层绑定名及其声明位置。
type moduleBinding struct {
	name string
	tok  lexer.Token
}

// moduleExport 是一条 ExportedNames 条目。
type moduleExport struct {
	name string
	tok  lexer.Token
}

// checkModuleEarlyErrors 执行上面 5 条早错。只报**第一条**错 (按规范"任一违反
// 即 SyntaxError"), 避免同一文件刷屏。
func (p *Parser) checkModuleEarlyErrors(program *ast.Program) {
	if program == nil {
		return
	}
	stmts := program.Statements

	// ── 收集顶层 lexical 绑定名 (含 import 绑定; 再导出不引入本地绑定) ──
	var lexical []moduleBinding
	for _, s := range stmts {
		lexical = append(lexical, topLevelLexicalBindings(s)...)
		if imp, ok := s.(*ast.ImportDeclaration); ok {
			lexical = append(lexical, importBindingNames(imp)...)
		}
	}

	// ── 顶层 var 名 (含嵌套块里的 var —— 提升到模块作用域) ──
	varNames := blockVarDeclaredNames(stmts)
	varSet := make(map[string]bool, len(varNames))
	for _, n := range varNames {
		varSet[n] = true
	}
	// 顶层 var 绑定名 (带位置) —— 只用于「严格保留字不能作绑定名」的检查;
	// 嵌套块里的 var 位置追踪成本高, 且 early-strict-mode 用例是顶层 var。
	var varBindings []moduleBinding
	for _, s := range stmts {
		varBindings = append(varBindings, topLevelVarBindings(s)...)
	}

	declared := make(map[string]bool, len(lexical)+len(varNames))
	for _, n := range varNames {
		declared[n] = true
	}

	// ── 规则 5 / 1: 严格保留字 + lexical 重复 ──
	for _, b := range varBindings {
		if isModuleRestrictedBindingName(b.name) {
			p.errorAt(b.tok, "SyntaxError: '"+b.name+"' is not a valid binding name in module code")
			return
		}
	}
	seenLex := make(map[string]bool, len(lexical))
	for _, b := range lexical {
		if isModuleRestrictedBindingName(b.name) {
			p.errorAt(b.tok, "SyntaxError: '"+b.name+"' is not a valid binding name in module code")
			return
		}
		if seenLex[b.name] {
			p.errorAt(b.tok, "SyntaxError: Identifier '"+b.name+"' has already been declared")
			return
		}
		seenLex[b.name] = true
		declared[b.name] = true
	}

	// ── 规则 2: lexical ∩ var ──
	for _, b := range lexical {
		if varSet[b.name] {
			p.errorAt(b.tok, "SyntaxError: Identifier '"+b.name+"' has already been declared")
			return
		}
	}

	// ── 解构声明的真实绑定名 (只供规则 4: 导出绑定的存在性) ──
	//
	// 解构声明 (const [todos, setTodos] = …) 在 AST 里只留下合成名
	// __destructure__ + 赋值形态, 真实名字要到运行时才展开 —— 上面的
	// declNames 因此一律跳过合成名 (那是块级重复检查"宁漏不误杀"的取舍)。
	// 但规则 4 判的是「导出的绑定存在吗」, 这里不能把"列不出来"当成
	// "不存在": 否则
	//
	//	const [a, b] = f();  export { a };
	//
	// 会被误判成 "export 'a' is not defined in module"。真实案例见
	// scaffold/template/src/store.js (createSignal 解构后 export {}) ——
	// 它让脚手架默认工程直接挂不上窗, 且三平台 ci 一起红。
	// 只补进 declared: lexical 保持原样, 规则 1 / 2 / 5 的口径不动。
	for _, s := range stmts {
		for _, n := range destructuredDeclNames(s) {
			declared[n] = true
		}
	}

	// ── 规则 3 / 4: 导出名唯一 + 导出绑定已声明 ──
	seenExp := make(map[string]bool)
	for _, s := range stmts {
		ed, ok := s.(*ast.ExportDeclaration)
		if !ok {
			continue
		}
		for _, ex := range exportNames(ed) {
			if seenExp[ex.name] {
				p.errorAt(ex.tok, "SyntaxError: duplicate export '"+ex.name+"'")
				return
			}
			seenExp[ex.name] = true
		}
		// 再导出 (export ... from "m") 与 export * 的 local 指向**源模块**,
		// 不是本模块绑定, 不作存在性检查。
		if ed.Source != "" || ed.IsStar {
			continue
		}
		for _, sp := range ed.Specifiers {
			if sp.Local == "*" {
				continue
			}
			if !declared[sp.Local] {
				p.errorAt(ed.Token, "SyntaxError: export '"+sp.Local+"' is not defined in module")
				return
			}
		}
	}
}

// errorAt 用指定 token 的位置登记一条解析错误。
func (p *Parser) errorAt(tok lexer.Token, msg string) {
	p.errors.Add(msg, tok.Line, tok.Column)
}

// isModuleRestrictedBindingName 报告名字是否在模块 (恒严格) 里不能作绑定名:
// 严格模式保留字, 以及 eval / arguments。
func isModuleRestrictedBindingName(name string) bool {
	switch name {
	case "implements", "interface", "package", "private", "protected",
		"public", "static", "enum", "eval", "arguments":
		return true
	}
	return false
}

// topLevelLexicalBindings 收一条顶层语句的 lexical 绑定名。
//
//   - let/const/class/function 声明 (模块顶层均为 lexical);
//   - export 声明: 其 Declaration 里的绑定名 —— `export function f(){}` 的 f、
//     `export default function f(){}` 的 f 都是模块内局部绑定 (后者只导出 default,
//     但 f 仍是 lexical 名, 可与其他声明冲突);
//   - `export { x }` 只**引用**已有绑定, 不引入新名。
func topLevelLexicalBindings(s ast.Statement) []moduleBinding {
	ed, ok := s.(*ast.ExportDeclaration)
	if ok {
		if ed.Declaration == nil {
			return nil
		}
		return statementBoundBindings(ed.Declaration)
	}
	return statementBoundBindings(s)
}

// statementBoundBindings 收一条声明语句本身引入的绑定名 (不含嵌套)。
func statementBoundBindings(s ast.Statement) []moduleBinding {
	switch v := s.(type) {
	case *ast.LetStatement:
		return namedBindings(v.Token, declNames(v.Name, v.More))
	case *ast.ConstStatement:
		return namedBindings(v.Token, declNames(v.Name, v.More))
	case *ast.ClassDeclaration:
		if v.Name != nil {
			return []moduleBinding{{name: v.Name.Value, tok: v.Token}}
		}
	case *ast.FunctionDeclaration:
		if v.Name != nil {
			return []moduleBinding{{name: v.Name.Value, tok: v.Token}}
		}
	}
	return nil
}

// topLevelVarBindings 收一条**顶层** var 声明的绑定名 (用于严格保留字检查)。
// export var x 也算。
func topLevelVarBindings(s ast.Statement) []moduleBinding {
	if ed, ok := s.(*ast.ExportDeclaration); ok {
		if ed.Declaration == nil {
			return nil
		}
		s = ed.Declaration
	}
	if v, ok := s.(*ast.VarStatement); ok {
		return namedBindings(v.Token, declNames(v.Name, v.More))
	}
	return nil
}

// importBindingNames 收一条 import 声明引入的本地绑定名。
func importBindingNames(imp *ast.ImportDeclaration) []moduleBinding {
	var out []moduleBinding
	add := func(name string) {
		if name != "" {
			out = append(out, moduleBinding{name: name, tok: imp.Token})
		}
	}
	add(imp.DefaultName)
	add(imp.Namespace)
	for _, n := range imp.NamedImports {
		add(n.Local)
	}
	return out
}

// exportNames 收一条 export 声明贡献的 ExportedNames。
//
//   - export default … ⇒ "default";
//   - export * from "m" ⇒ 无 (星号再导出不产生具名导出);
//   - export * as ns from "m" ⇒ ns (以 Specifier Local="*" 记录);
//   - export { … } ⇒ 每个 Specifier 的 Exported;
//   - export <声明> ⇒ 声明的绑定名 (export var x / export function f / export class C)。
func exportNames(ed *ast.ExportDeclaration) []moduleExport {
	if ed.IsDefault {
		return []moduleExport{{name: "default", tok: ed.Token}}
	}
	if ed.IsStar {
		return nil
	}
	if len(ed.Specifiers) > 0 {
		out := make([]moduleExport, 0, len(ed.Specifiers))
		for _, sp := range ed.Specifiers {
			out = append(out, moduleExport{name: sp.Exported, tok: ed.Token})
		}
		return out
	}
	if ed.Declaration != nil {
		var out []moduleExport
		for _, n := range exportedDeclNames(ed.Declaration) {
			out = append(out, moduleExport{name: n, tok: ed.Token})
		}
		return out
	}
	return nil
}

// exportedDeclNames 收 `export <声明>` 里贡献 ExportedNames 的绑定名 —— 与
// lexical 绑定不同, 这里 **var** 也算 (`export var x` 的导出名是 x)。
func exportedDeclNames(s ast.Statement) []string {
	switch v := s.(type) {
	case *ast.VarStatement:
		return declNames(v.Name, v.More)
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

func namedBindings(tok lexer.Token, names []string) []moduleBinding {
	out := make([]moduleBinding, 0, len(names))
	for _, n := range names {
		out = append(out, moduleBinding{name: n, tok: tok})
	}
	return out
}

// destructuredDeclNames 收一条**顶层声明语句**里解构绑定的真实名字。
// `export <解构声明>` 也算 (与顶层 lexical 收集同口径)。
func destructuredDeclNames(s ast.Statement) []string {
	if ed, ok := s.(*ast.ExportDeclaration); ok {
		if ed.Declaration == nil {
			return nil
		}
		s = ed.Declaration
	}
	switch v := s.(type) {
	case *ast.LetStatement:
		return destructuredNamesIn(v.Name, v.Value, v.More)
	case *ast.ConstStatement:
		return destructuredNamesIn(v.Name, v.Value, v.More)
	case *ast.VarStatement:
		return destructuredNamesIn(v.Name, v.Value, v.More)
	}
	return nil
}

// destructuredNamesIn 从一条声明语句的声明项里收解构绑定的名字
// (名字不是合成名的声明项 —— 即非解构 —— 一律跳过)。
func destructuredNamesIn(name *ast.Identifier, value ast.Expression, more []ast.Declarator) []string {
	var out []string
	collect := func(n *ast.Identifier, v ast.Expression) {
		if n == nil || n.Value != ast.DestructureSyntheticName {
			return
		}
		out = append(out, ast.PatternBoundNames(v)...)
	}
	collect(name, value)
	for _, d := range more {
		collect(d.Name, d.Value)
	}
	return out
}

package parser

import (
	"fmt"
	"strings"

	"github.com/14752222/Gox/ast"
)

// ===== class 私有名早错集中校验（rpEXH2, 2026-10-05）=====
//
// 背景: 私有字段 #name 落地后，test262 一批「规范早错」用例从遮羞布下暴露出来 ——
// 此前 `#` 判 ILLEGAL、整个文件解析失败，negative 用例反而全挂在通过线上；
// 特性落地文件能解析了，这批用例集体翻红。这里补齐三类解析期早错:
//
//  1. 重复私有名: 同一 class 体内同名私有成员只能有一个（static 与实例、
//     字段/方法/getter/setter 之间都不能重名 —— 规范 ClassTail 早错）。
//  2. 实例字段初始化器含 arguments / super(...) 调用（字段初始化器不在
//     arguments 作用域内; super() 调用只能出现在 constructor 里）。
//  3. 引用了任何外层 class 都未声明的私有名（this.#y 而 #y 不在任何
//     包围类的成员表里 —— Node 报 SyntaxError: Private field '#y' must be
//     declared in an enclosing class）。
//
// 私有名的语义环境是**栈式**的: 内层 class 体里的 obj.#x 先查内层声明表,
// 未命中再查外层 class（沿嵌套链向外），全落空才是早错。所以第 3 类不能
// 只看单个 class —— 引用在哪个 class 里出现、那个 class 的外层链上有谁,
// 解析时就是知道的。实现为解析点收集:
//
//   - Parser 维护 privEnvStack (每进一个 class 体压一层, 出来弹掉);
//   - parsePrivateMemberExpression 在引用点把名字记进「当前最内层」的
//     pending 列表;
//   - class 体收尾 checkClassEarlyErrors 时, 把 pending 里**在本层声明表
//     未命中**的名字上抛给外层 (外层若也没接住, 最外层收尾时报早错)。
//     —— 这正是「内层 class 可以引用外层私有名」的规范行为。
//
// 挂载点: parseClassDeclaration / parseClassExpression 的成员循环收尾处。

// privEnv 是一层 class 私有名环境。
type privEnv struct {
	declared    map[string]bool // 本 class 声明的私有名 (含 # 前缀)
	pending     []string        // 本 class 体内引用、本层未命中的私有名 (待上抛)
	dupReported bool
}

// pushPrivEnv / popPrivEnv 由 class 解析入口配对调用。
func (p *Parser) pushPrivEnv() {
	p.privEnvStack = append(p.privEnvStack, &privEnv{declared: map[string]bool{}})
}

func (p *Parser) popPrivEnv() {
	if n := len(p.privEnvStack); n > 0 {
		p.privEnvStack = p.privEnvStack[:n-1]
	}
}

// notePrivateRef 在引用点记录一个私有名（obj.#x 出现处调用）。
// 没有环境（顶层代码引用私有名）时立即报早错 —— 顶层没有包围类。
func (p *Parser) notePrivateRef(name string) {
	if len(p.privEnvStack) == 0 {
		p.addError(fmt.Sprintf("SyntaxError: private field '#%s' must be declared in an enclosing class",
			strings.TrimPrefix(name, "#")))
		return
	}
	env := p.privEnvStack[len(p.privEnvStack)-1]
	env.pending = append(env.pending, name)
}

// checkClassEarlyErrors 对解析完的 class 体做早错校验并收尾本层环境。
// 调用时机: 成员循环之后、expected '}' 校验之前。
// 必须与 pushPrivEnv 配对（在 class 解析入口 push, 本函数末尾 pop）。
func (p *Parser) checkClassEarlyErrors(methods, statics []*ast.ClassMethod, fields []*ast.ClassField) {
	env := p.privEnvStack[len(p.privEnvStack)-1]

	// ---- 1. 重复私有名: 收集本层声明表 ----
	// getter + setter 同名成对是合法的（规范上它们是同一个访问器槽的
	// 读写两个半边）: accessor 之间允许恰好 get+set 一对; 两个 getter、
	// 两个 setter、accessor 与字段/方法同名仍是早错。
	reportDup := func(name string) {
		p.addError(fmt.Sprintf("SyntaxError: duplicate private member '#%s' in class",
			strings.TrimPrefix(name, "#")))
	}
	hasGet := map[string]bool{}
	hasSet := map[string]bool{}
	nonAcc := map[string]bool{}
	for _, m := range methods {
		if !m.IsPrivate {
			continue
		}
		if m.IsGetter || m.IsSetter {
			if nonAcc[m.Name] || (m.IsGetter && hasGet[m.Name]) || (m.IsSetter && hasSet[m.Name]) {
				reportDup(m.Name)
			}
			if m.IsGetter {
				hasGet[m.Name] = true
			} else {
				hasSet[m.Name] = true
			}
		} else {
			if nonAcc[m.Name] || hasGet[m.Name] || hasSet[m.Name] {
				reportDup(m.Name)
			}
			nonAcc[m.Name] = true
		}
		env.declared[m.Name] = true
	}
	for _, m := range statics {
		if m.IsPrivate {
			if env.declared[m.Name] {
				reportDup(m.Name)
			}
			env.declared[m.Name] = true
		}
	}
	for _, f := range fields {
		if f.IsPrivate {
			if env.declared[f.Name] {
				reportDup(f.Name)
			}
			env.declared[f.Name] = true
		}
	}

	// ---- 2. 字段初始化器: arguments / super() 调用 ----
	checkInit := func(v ast.Expression) {
		if v == nil {
			return
		}
		if exprContainsArguments(v) {
			p.addError("SyntaxError: 'arguments' is not allowed in class field initializer")
		}
		if exprContainsSuperCall(v) {
			p.addError("SyntaxError: 'super' call is not allowed in class field initializer")
		}
	}
	for _, f := range fields {
		checkInit(f.Value)
	}
	for _, m := range statics {
		checkInit(m.FieldValue)
	}

	// ---- 3. 未解析私有名: 本层 pending 里未命中声明表的上抛外层 ----
	// 最外层 (栈只剩本层) 时还剩下的就是真正无处可归的引用 → 早错。
	// (环境的弹出由 class 解析入口的 defer popPrivEnv 负责。)
	for _, name := range env.pending {
		if env.declared[name] {
			continue
		}
		if len(p.privEnvStack) > 1 {
			outer := p.privEnvStack[len(p.privEnvStack)-2]
			outer.pending = append(outer.pending, name)
		} else {
			p.addError(fmt.Sprintf("SyntaxError: private field '#%s' must be declared in an enclosing class",
				strings.TrimPrefix(name, "#")))
		}
	}
}

// exprContainsArguments 报告表达式树里是否直接引用了 arguments。
// 嵌套普通函数体内出现 arguments 是合法的（那是内层自己的 arguments）,
// 箭头函数没有自己的 arguments —— 继续向内扫（规范行为）。
func exprContainsArguments(expr ast.Expression) bool {
	found := false
	var walk func(ast.Node)
	walk = func(n ast.Node) {
		if found || n == nil {
			return
		}
		switch node := n.(type) {
		case *ast.Identifier:
			if node.Value == "arguments" {
				found = true
			}
			return
		case *ast.FunctionExpression:
			return // 内层函数有自己的 arguments 作用域
		case *ast.ClassDeclaration, *ast.ClassExpression:
			return // 内层 class 体: 其字段初始化器有自己的 arguments 规则
		}
		for _, child := range childNodes(n) {
			walk(child)
		}
	}
	walk(expr)
	return found
}

// exprContainsSuperCall 报告语法树里是否出现 super(...) 调用。
// super.x 属性访问在字段初始化器里是合法的（可读父原型），不拦。
// 参数放宽为 ast.Node，便于对方法体（*ast.BlockStatement）与字段初始化器
// （ast.Expression）共用同一遍历。
func exprContainsSuperCall(node ast.Node) bool {
	found := false
	var walk func(ast.Node)
	walk = func(n ast.Node) {
		if found || n == nil {
			return
		}
		if call, ok := n.(*ast.CallExpression); ok {
			if _, isSuper := call.Function.(*ast.SuperExpression); isSuper {
				found = true
				return
			}
		}
		for _, child := range childNodes(n) {
			walk(child)
		}
	}
	walk(node)
	return found
}

// childNodes 返回 AST 节点的直接子节点（早错扫描的通用遍历用）。
// 覆盖会出现在字段初始化器/方法体里的节点类型; 未覆盖的类型视为叶子
// （宁可少报不误报 —— 早错校验漏掉的路径不产生假阳性）。
func childNodes(n ast.Node) []ast.Node {
	var out []ast.Node
	switch node := n.(type) {
	// ---- 语句 ----
	case *ast.BlockStatement:
		for _, s := range node.Statements {
			out = append(out, s)
		}
	case *ast.ExpressionStatement:
		out = append(out, node.Expression)
	case *ast.LetStatement:
		out = append(out, node.Name)
		if node.Value != nil {
			out = append(out, node.Value)
		}
		for _, d := range node.More {
			out = append(out, d.Name)
			if d.Value != nil {
				out = append(out, d.Value)
			}
		}
	case *ast.VarStatement:
		out = append(out, node.Name)
		if node.Value != nil {
			out = append(out, node.Value)
		}
		for _, d := range node.More {
			out = append(out, d.Name)
			if d.Value != nil {
				out = append(out, d.Value)
			}
		}
	case *ast.ConstStatement:
		out = append(out, node.Name)
		if node.Value != nil {
			out = append(out, node.Value)
		}
		for _, d := range node.More {
			out = append(out, d.Name)
			if d.Value != nil {
				out = append(out, d.Value)
			}
		}
	case *ast.ReturnStatement:
		if node.ReturnValue != nil {
			out = append(out, node.ReturnValue)
		}
	case *ast.IfStatement:
		out = append(out, node.Condition)
		out = append(out, node.Consequence)
		if node.Alternative != nil {
			out = append(out, node.Alternative)
		}
	case *ast.ForStatement:
		if node.Init != nil {
			out = append(out, node.Init)
		}
		if node.Condition != nil {
			out = append(out, node.Condition)
		}
		if node.Update != nil {
			out = append(out, node.Update)
		}
		out = append(out, node.Body)
	case *ast.ForInStatement:
		if node.Iterable != nil {
			out = append(out, node.Iterable)
		}
		out = append(out, node.Body)
	case *ast.ForOfStatement:
		if node.Iterable != nil {
			out = append(out, node.Iterable)
		}
		out = append(out, node.Body)
	case *ast.WhileStatement:
		out = append(out, node.Condition)
		out = append(out, node.Body)
	case *ast.DoWhileStatement:
		out = append(out, node.Body)
		out = append(out, node.Condition)
	case *ast.TryStatement:
		out = append(out, node.Body)
		if node.CatchBody != nil {
			out = append(out, node.CatchBody)
		}
		if node.FinallyBody != nil {
			out = append(out, node.FinallyBody)
		}
	case *ast.ThrowStatement:
		out = append(out, node.Value)
	case *ast.SwitchStatement:
		out = append(out, node.Discriminant)
		for _, c := range node.Cases {
			// *ast.SwitchCase 未实现 Node 接口, 内联展开子节点
			if c.Test != nil {
				out = append(out, c.Test)
			}
			for _, s := range c.Statements {
				out = append(out, s)
			}
		}
	case *ast.LabeledStatement:
		out = append(out, node.Body)
	case *ast.BreakStatement, *ast.ContinueStatement:
		// 叶子
	case *ast.ClassDeclaration, *ast.ClassExpression:
		return nil // 各遍历器自己拦截

	// ---- 表达式 ----
	case *ast.Identifier:
		// 叶子
	case *ast.IntegerLiteral, *ast.FloatLiteral, *ast.BigIntLiteral,
		*ast.StringLiteral, *ast.BooleanLiteral,
		*ast.NullLiteral, *ast.UndefinedLiteral, *ast.RegexLiteral:
		// 叶子
	case *ast.ArrayLiteral:
		for _, e := range node.Elements {
			out = append(out, e)
		}
	case *ast.ObjectLiteral:
		// *ast.Property 未实现 Node 接口, 内联展开子节点
		for _, pair := range node.Properties {
			if pair == nil {
				continue // 解析恢复期可能残留 nil 属性, 防御
			}
			out = append(out, pair.Key)
			if pair.Value != nil {
				out = append(out, pair.Value)
			}
		}
		for _, sp := range node.Spread {
			out = append(out, sp)
		}
	case *ast.FunctionExpression:
		return nil // 嵌套函数: 各调用方自己决定是否下钻
	case *ast.ArrowFunctionExpression:
		out = append(out, node.Body)
	case *ast.CallExpression:
		out = append(out, node.Function)
		for _, a := range node.Arguments {
			out = append(out, a)
		}
	case *ast.NewExpression:
		out = append(out, node.Callee)
		for _, a := range node.Arguments {
			out = append(out, a)
		}
	case *ast.MemberExpression:
		out = append(out, node.Object)
		if node.Property != nil && node.Private == "" {
			out = append(out, node.Property)
		}
	case *ast.OptionalMemberExpression:
		out = append(out, node.Object)
		out = append(out, node.Property)
	case *ast.OptionalCallExpression:
		out = append(out, node.Function)
		for _, a := range node.Arguments {
			out = append(out, a)
		}
	case *ast.BinaryExpression:
		out = append(out, node.Left)
		out = append(out, node.Right)
	case *ast.UnaryExpression:
		out = append(out, node.Right)
	case *ast.AssignmentExpression:
		out = append(out, node.Left)
		out = append(out, node.Right)
	case *ast.LogicalExpression:
		out = append(out, node.Left)
		out = append(out, node.Right)
	case *ast.SequenceExpression:
		for _, e := range node.Expressions {
			out = append(out, e)
		}
	case *ast.ConditionalExpression:
		out = append(out, node.Condition)
		out = append(out, node.Consequence)
		out = append(out, node.Alternative)
	case *ast.TemplateLiteral:
		for _, e := range node.Expressions {
			out = append(out, e)
		}
	case *ast.TaggedTemplateExpression:
		out = append(out, node.Tag)
		out = append(out, node.Template)
	case *ast.SpreadElement:
		out = append(out, node.Argument)
	case *ast.YieldExpression:
		if node.Value != nil {
			out = append(out, node.Value)
		}
	case *ast.AwaitExpression:
		out = append(out, node.Argument)
	case *ast.SuperExpression, *ast.ThisExpression, *ast.PrivateIdentifier:
		// 叶子
	default:
		// 未知类型当叶子
	}
	return out
}

package ast

import "github.com/14752222/Gox/lexer"

// Node 是所有 AST 节点的基础接口。
// 每个 AST 节点都能返回其关联的首个令牌的字面量，用于调试。
type Node interface {
	TokenLiteral() string
	String() string
}

// Pos 是源码位置 (1 起始的行列)。
type Pos struct {
	Line int
	Col  int
}

// PositionTable 记录语句节点 → 源码位置。
// 由 parser 在 parseStatement 单点收集, 编译器据此建立
// "字节码 offset → 源码位置" 映射, 供运行时错误渲染源码帧 (T05)。
// 用指针 side-table 而非给几十个节点类型加字段, 保持 AST 定义瘦。
type PositionTable map[Statement]Pos

// Statement 是所有语句节点的接口。
// 语句产生副作用，不直接产生值。
type Statement interface {
	Node
	statementNode()
}

// Expression 是所有表达式节点的接口。
// 表达式产生值。
type Expression interface {
	Node
	expressionNode()
}

// ==================== 程序根节点 ====================

// Program 是 AST 的根节点，包含一个语句列表。
type Program struct {
	Statements []Statement

	// Positions 记录每条语句的源码位置 (parseStatement 单点收集)。
	// 运行时错误渲染源码帧用 (T05); 无位置信息的节点不在表内。
	Positions PositionTable `json:"-"`

	// UsesJSX 报告本程序里是否有**小写标签**的 JSX (如 <view> / <column>)。
	//
	// 那些元素在 parser 层被降级成 h(...) 调用 (见 parser/jsx.go)，所以程序
	// 需要一个 h 在作用域里；而大写标签 (<Counter/>) 只是组件调用，用不到 h。
	// compiler 按这个标记决定要不要补一条 `import { h } from "gx/gfx"` ——
	// 没有它的话，"用了 JSX 却没导入 h"的脚本能编译通过，却在挂载那一刻炸
	// ReferenceError: h is not defined。
	UsesJSX bool

	// Strict 报告本 ScriptBody 是否处于严格模式 (含 "use strict" 指令, 或
	// 为 module 顶层 —— 模块恒严格)。由 parser 的指令序言识别填写, 供
	// compiler 决定编译期早错与运行期语义 (未声明赋值 / this 归一)。
	Strict bool
}

func (p *Program) TokenLiteral() string {
	if len(p.Statements) > 0 {
		return p.Statements[0].TokenLiteral()
	}
	return ""
}

func (p *Program) String() string {
	var result string
	for _, s := range p.Statements {
		result += s.String() + "\n"
	}
	return result
}

// ==================== 标识符 ====================

// Identifier 表示一个标识符 (变量名、函数名等)。
type Identifier struct {
	Token lexer.Token
	Value string
}

func (i *Identifier) TokenLiteral() string { return i.Token.Literal }
func (i *Identifier) String() string       { return i.Value }
func (i *Identifier) expressionNode()      {}

// ==================== let 声明 ====================

// LetStatement 表示 let 变量声明语句。
// 例如: let x = 5;  或  let y;
// Declarator 表示 let/const 声明中的一个声明项 (用于 let a = 1, b = 2;)。
type Declarator struct {
	Name  *Identifier
	Value Expression // 可为 nil (let b;)
}

type LetStatement struct {
	Token lexer.Token // LET 令牌
	Name  *Identifier
	Value Expression   // 可为 nil (let x;)
	More  []Declarator // 额外的声明项 (let a = 1, b = 2; 中的 b = 2)
}

func (ls *LetStatement) TokenLiteral() string { return ls.Token.Literal }
func (ls *LetStatement) String() string {
	var result string
	result = ls.TokenLiteral() + " " + ls.Name.String()
	if ls.Value != nil {
		result += " = " + ls.Value.String()
	}
	for _, d := range ls.More {
		result += ", " + d.Name.String()
		if d.Value != nil {
			result += " = " + d.Value.String()
		}
	}
	result += ";"
	return result
}
func (ls *LetStatement) statementNode() {}

// ==================== var 声明 ====================

// VarStatement 表示 var 变量声明语句 (函数作用域 + 提升 + 允许重复声明)。
// 例如: var x = 5;  或  var a = 1, b;  或  var [x, y] = pair;
// 与 let 的分野见 parser 对 VAR 的处理与 compiler 的 compileVarStatement ——
// var 的绑定登记在最近的**函数作用域层**, 而不是当前块。
type VarStatement struct {
	Token lexer.Token // VAR 令牌
	Name  *Identifier
	Value Expression   // 可为 nil (var x;)
	More  []Declarator // 额外的声明项 (var a = 1, b = 2; 中的 b = 2)
}

func (vs *VarStatement) TokenLiteral() string { return vs.Token.Literal }
func (vs *VarStatement) String() string {
	var result string
	result = vs.TokenLiteral() + " " + vs.Name.String()
	if vs.Value != nil {
		result += " = " + vs.Value.String()
	}
	for _, d := range vs.More {
		result += ", " + d.Name.String()
		if d.Value != nil {
			result += " = " + d.Value.String()
		}
	}
	result += ";"
	return result
}
func (vs *VarStatement) statementNode() {}

// ==================== const 声明 ====================
// ConstStatement 表示 const 常量声明语句。
// 例如: const PI = 3.14;
type ConstStatement struct {
	Token lexer.Token // CONST 令牌
	Name  *Identifier
	Value Expression   // const 必须有初始值
	More  []Declarator // 额外的声明项 (const a = 1, b = 2;)
}

func (cs *ConstStatement) TokenLiteral() string { return cs.Token.Literal }
func (cs *ConstStatement) String() string {
	result := cs.TokenLiteral() + " " + cs.Name.String() + " = " + cs.Value.String()
	for _, d := range cs.More {
		result += ", " + d.Name.String() + " = " + d.Value.String()
	}
	result += ";"
	return result
}
func (cs *ConstStatement) statementNode() {}

// ==================== 表达式语句 ====================

// ExpressionStatement 表示一个作为语句使用的表达式。
// 例如: foo();  或  1 + 2;
type ExpressionStatement struct {
	Token      lexer.Token
	Expression Expression
}

func (es *ExpressionStatement) TokenLiteral() string { return es.Token.Literal }
func (es *ExpressionStatement) String() string {
	if es.Expression != nil {
		return es.Expression.String() + ";"
	}
	return ""
}
func (es *ExpressionStatement) statementNode() {}

// ==================== return 语句 ====================

// ReturnStatement 表示 return 语句。
type ReturnStatement struct {
	Token       lexer.Token
	ReturnValue Expression // 可为 nil (return;)
}

func (rs *ReturnStatement) TokenLiteral() string { return rs.Token.Literal }
func (rs *ReturnStatement) String() string {
	if rs.ReturnValue != nil {
		return rs.TokenLiteral() + " " + rs.ReturnValue.String() + ";"
	}
	return rs.TokenLiteral() + ";"
}
func (rs *ReturnStatement) statementNode() {}

// ==================== 块语句 ====================

// BlockStatement 表示用花括号包围的语句块。
// 例如: { let x = 5; return x; }
type BlockStatement struct {
	Token      lexer.Token // {
	Statements []Statement
}

func (bs *BlockStatement) TokenLiteral() string { return bs.Token.Literal }
func (bs *BlockStatement) String() string {
	var result string
	result = "{\n"
	for _, s := range bs.Statements {
		result += "  " + s.String() + "\n"
	}
	result += "}"
	return result
}
func (bs *BlockStatement) statementNode() {}

// ==================== if 语句 ====================

// IfStatement 表示 if/else 语句。
// 例如: if (x > 0) { ... } else { ... }
type IfStatement struct {
	Token       lexer.Token // IF
	Condition   Expression
	Consequence *BlockStatement
	Alternative *BlockStatement // 可为 nil (无 else)
}

func (is *IfStatement) TokenLiteral() string { return is.Token.Literal }
func (is *IfStatement) String() string {
	result := "if (" + is.Condition.String() + ") " + is.Consequence.String()
	if is.Alternative != nil {
		result += " else " + is.Alternative.String()
	}
	return result
}
func (is *IfStatement) statementNode() {}

// ==================== for 语句 ====================

// ForStatement 表示传统的 for 循环。
// 例如: for (let i = 0; i < 10; i++) { ... }
type ForStatement struct {
	Token     lexer.Token // FOR
	Init      Statement   // 初始化: let i = 0; 或表达式语句，可为 nil
	Condition Expression  // 条件，可为 nil (无限循环)
	Update    Statement   // 更新: i++，可为 nil
	Body      *BlockStatement
}

func (fs *ForStatement) TokenLiteral() string { return fs.Token.Literal }
func (fs *ForStatement) String() string {
	result := "for ("
	if fs.Init != nil {
		result += fs.Init.String() + " "
	} else {
		result += "; "
	}
	if fs.Condition != nil {
		result += fs.Condition.String() + " "
	} else {
		result += "; "
	}
	if fs.Update != nil {
		result += fs.Update.String()
	}
	result += ") " + fs.Body.String()
	return result
}
func (fs *ForStatement) statementNode() {}

// ==================== for...of 语句 ====================

// ForOfStatement 表示 for...of 循环。
// 例如: for (let x of arr) { ... }  /  for (const [a, b] of pairs) { ... }
//
// 绑定有两种, 互斥: 简单绑定用 Variable; **解构绑定**用 Pattern。
// 解构时 VarDecl 只留一个带合成名 (__destructure__) 的空壳, 唯一的用途是让
// 编译器分辨 let / const —— 与 let [a, b] = … 那条路径的口径一致。
type ForOfStatement struct {
	Token    lexer.Token // FOR
	Keyword  lexer.Token // OF
	VarDecl  Statement   // let x / const x（解构绑定见上）
	Variable *Identifier // 迭代变量名; 解构绑定时为 nil
	Pattern  Expression  // ArrayPattern / ObjectPattern; 简单绑定时为 nil
	Target   Expression  // 无声明形态的赋值目标 (标识符/成员访问); 声明形态为 nil
	Iterable Expression
	Body     *BlockStatement
	// Await 表示 for await...of (异步迭代)。仅允许出现在 async 函数内
	// (编译器校验); String() 反映该形态。
	Await bool
}

func (fos *ForOfStatement) TokenLiteral() string { return fos.Token.Literal }
func (fos *ForOfStatement) String() string {
	head := ""
	if fos.VarDecl != nil {
		head = fos.VarDecl.String()
	} else if fos.Pattern != nil {
		head = fos.Pattern.String()
	} else if fos.Target != nil {
		head = fos.Target.String()
	}
	kw := "for ("
	if fos.Await {
		kw = "for await ("
	}
	return kw + head + " of " + fos.Iterable.String() + ") " + fos.Body.String()
}
func (fos *ForOfStatement) statementNode() {}

// ForInStatement 表示 for...in 循环。
// 例如: for (let k in obj) { ... }
type ForInStatement struct {
	Token    lexer.Token // FOR
	VarDecl  Statement   // let k (LetStatement 或 ConstStatement)
	Variable *Identifier // 迭代变量名
	Iterable Expression
	Body     *BlockStatement
}

func (fis *ForInStatement) TokenLiteral() string { return fis.Token.Literal }
func (fis *ForInStatement) String() string {
	return "for (" + fis.VarDecl.String() + " in " + fis.Iterable.String() + ") " + fis.Body.String()
}
func (fis *ForInStatement) statementNode() {}

// ==================== while 语句 ====================

// WhileStatement 表示 while 循环。
type WhileStatement struct {
	Token     lexer.Token // WHILE
	Condition Expression
	Body      *BlockStatement
}

func (ws *WhileStatement) TokenLiteral() string { return ws.Token.Literal }
func (ws *WhileStatement) String() string {
	return "while (" + ws.Condition.String() + ") " + ws.Body.String()
}
func (ws *WhileStatement) statementNode() {}

// DoWhileStatement 表示 do...while 循环。
type DoWhileStatement struct {
	Token     lexer.Token // DO
	Condition Expression
	Body      *BlockStatement
}

func (dws *DoWhileStatement) TokenLiteral() string { return dws.Token.Literal }
func (dws *DoWhileStatement) String() string {
	return "do " + dws.Body.String() + " while (" + dws.Condition.String() + ")"
}
func (dws *DoWhileStatement) statementNode() {}

// ==================== with 语句 ====================

// WithStatement 表示 sloppy 模式下的 with 语句: with (对象) 语句体。
//
// 语义 (规范 13.11.7): 先求值 Object 得到对象, 再在对象环境记录中求值
// Body —— Body 内对自由标识符的解析先查对象的同名属性 (Symbol.unscopables
// 排除的属性除外), 未命中再沿外层作用域解析。Body 可以是一个块或单条语句。
type WithStatement struct {
	Token  lexer.Token // WITH
	Object Expression  // 被引入作用域的对象表达式 (进入 with 前求值一次)
	Body   Statement   // 语句体 (BlockStatement 或单条语句)
}

func (ws *WithStatement) TokenLiteral() string { return ws.Token.Literal }
func (ws *WithStatement) String() string {
	body := ""
	if ws.Body != nil {
		body = ws.Body.String()
	}
	return "with (" + ws.Object.String() + ") " + body
}
func (ws *WithStatement) statementNode() {}

// ==================== break / continue 语句 ====================

type BreakStatement struct {
	Token lexer.Token
	Label *Identifier // 可选, break outer
}

func (bs *BreakStatement) TokenLiteral() string { return bs.Token.Literal }
func (bs *BreakStatement) String() string {
	if bs.Label != nil {
		return "break " + bs.Label.String() + ";"
	}
	return "break;"
}
func (bs *BreakStatement) statementNode() {}

type ContinueStatement struct {
	Token lexer.Token
	Label *Identifier // 可选, continue outer
}

func (cs *ContinueStatement) TokenLiteral() string { return cs.Token.Literal }
func (cs *ContinueStatement) String() string {
	if cs.Label != nil {
		return "continue " + cs.Label.String() + ";"
	}
	return "continue;"
}
func (cs *ContinueStatement) statementNode() {}

// LabeledStatement 表示标签语句。例如: outer: for (...) { ... }
type LabeledStatement struct {
	Token lexer.Token // 标识符 token
	Label *Identifier
	Body  Statement
}

func (ls *LabeledStatement) TokenLiteral() string { return ls.Token.Literal }
func (ls *LabeledStatement) String() string       { return ls.Label.String() + ": " + ls.Body.String() }
func (ls *LabeledStatement) statementNode()       {}

// ==================== 函数声明 ====================

// FunctionDeclaration 表示具名函数声明。
// 例如: function foo(a, b) { return a + b; }
type FunctionDeclaration struct {
	Token       lexer.Token // FUNCTION
	Name        *Identifier
	Parameters  []*Parameter
	Body        *BlockStatement
	IsGenerator bool // function* 生成器函数
	IsAsync     bool // async function 异步函数
	// Strict 报告该函数体是否严格: 继承外层 strict, 或自身函数体含
	// "use strict" 指令。供 compiler 盖 FunctionMetadata.IsStrict。
	Strict bool
}

func (fd *FunctionDeclaration) TokenLiteral() string { return fd.Token.Literal }
func (fd *FunctionDeclaration) String() string {
	params := ""
	for i, p := range fd.Parameters {
		if i > 0 {
			params += ", "
		}
		params += p.String()
	}
	star := ""
	if fd.IsGenerator {
		star = "*"
	}
	async := ""
	if fd.IsAsync {
		async = "async "
	}
	return async + "function" + star + " " + fd.Name.String() + "(" + params + ") " + fd.Body.String()
}
func (fd *FunctionDeclaration) statementNode() {}

// ==================== 函数表达式 ====================

// FunctionExpression 表示匿名函数表达式。
// 例如: let f = function(a) { return a; };
type FunctionExpression struct {
	Token       lexer.Token // FUNCTION
	Name        *Identifier // 可为 nil (匿名函数)
	Parameters  []*Parameter
	Body        *BlockStatement
	IsGenerator bool // function* 生成器函数
	IsAsync     bool // async function 异步函数
	// Strict 见 FunctionDeclaration.Strict。
	Strict bool
}

func (fe *FunctionExpression) TokenLiteral() string { return fe.Token.Literal }
func (fe *FunctionExpression) String() string {
	params := ""
	for i, p := range fe.Parameters {
		if i > 0 {
			params += ", "
		}
		params += p.String()
	}
	name := ""
	if fe.Name != nil {
		name = fe.Name.String()
	}
	star := ""
	if fe.IsGenerator {
		star = "*"
	}
	async := ""
	if fe.IsAsync {
		async = "async "
	}
	return async + "function" + star + " " + name + "(" + params + ") " + fe.Body.String()
}
func (fe *FunctionExpression) expressionNode() {}

// ==================== 箭头函数 ====================

// ArrowFunctionExpression 表示 ES6 箭头函数。
// 例如: (a, b) => a + b  或  (x) => { return x * 2; }
// async 箭头: async () => … / async (a, b) => … / async x => …
type ArrowFunctionExpression struct {
	Token      lexer.Token // ARROW (=>)
	IsAsync    bool        // async 箭头函数 (IsAsync 与普通箭头共用同一份编译路径)
	Parameters []*Parameter
	Body       Node // *BlockStatement 或 Expression (隐式返回)
	// Strict 见 FunctionDeclaration.Strict。箭头函数体若为块体且含
	// "use strict" 指令, 或继承外层 strict, 则为 true。
	Strict bool
}

func (af *ArrowFunctionExpression) TokenLiteral() string { return af.Token.Literal }
func (af *ArrowFunctionExpression) String() string {
	prefix := ""
	if af.IsAsync {
		prefix = "async "
	}
	params := ""
	for i, p := range af.Parameters {
		if i > 0 {
			params += ", "
		}
		params += p.String()
	}
	return prefix + "(" + params + ") => " + af.Body.String()
}
func (af *ArrowFunctionExpression) expressionNode() {}

// ==================== 函数参数 ====================

// Parameter 表示函数参数。
// 可以是简单标识符、默认参数、rest 参数或解构模式。
type Parameter struct {
	Token   lexer.Token
	Name    string     // 参数名 (简单参数时使用)
	Pattern Expression // 解构模式 (nil = 简单参数)
	Default Expression // 默认值 (nil = 无默认值)
	Rest    bool       // 是否为 rest 参数 (...args)
}

func (p *Parameter) String() string {
	if p.Rest {
		return "..." + p.Name
	}
	if p.Default != nil {
		return p.Name + " = " + p.Default.String()
	}
	return p.Name
}

// ==================== 字面量表达式 ====================

// IntegerLiteral 表示整数字面量。
type IntegerLiteral struct {
	Token lexer.Token
	Value int64
}

func (il *IntegerLiteral) TokenLiteral() string { return il.Token.Literal }
func (il *IntegerLiteral) String() string       { return il.Token.Literal }
func (il *IntegerLiteral) expressionNode()      {}

// FloatLiteral 表示浮点数字面量。
type FloatLiteral struct {
	Token lexer.Token
	Value float64
}

func (fl *FloatLiteral) TokenLiteral() string { return fl.Token.Literal }
func (fl *FloatLiteral) String() string       { return fl.Token.Literal }

// BigIntLiteral 表示 BigInt 字面量 (如 1n, 0xFFn, 0b1010n)。
//
// Raw 是词法阶段去掉 "n" 后缀后的数字文本，仍可能带进制前缀 (0x/0b/0o)
// 与数字分隔符 (_)。进制解析延后到编译期 (object.ParseBigIntLiteral)，
// 这样 AST 层不必依赖 math/big，也便于错误信息定位到源码位置。
type BigIntLiteral struct {
	Token lexer.Token
	Raw   string
}

func (bl *BigIntLiteral) TokenLiteral() string { return bl.Token.Literal }
func (bl *BigIntLiteral) String() string       { return bl.Token.Literal + "n" }
func (bl *BigIntLiteral) expressionNode()      {}
func (fl *FloatLiteral) expressionNode()       {}

// StringLiteral 表示字符串字面量。
type StringLiteral struct {
	Token lexer.Token
	Value string
}

func (sl *StringLiteral) TokenLiteral() string { return sl.Token.Literal }
func (sl *StringLiteral) String() string       { return "\"" + sl.Value + "\"" }
func (sl *StringLiteral) expressionNode()      {}

// TemplateLiteral 表示 ES6 模板字面量。
// 例如: `Hello ${name}, you are ${age}!`
// Quasis 是静态文本部分，Expressions 是插值表达式部分。
// 它们交替排列: Quasis[0] + Expressions[0] + Quasis[1] + ...
type TemplateLiteral struct {
	Token       lexer.Token  // 起始反引号
	Quasis      []string     // 静态文本部分
	Expressions []Expression // 插值表达式
}

func (tl *TemplateLiteral) TokenLiteral() string { return tl.Token.Literal }
func (tl *TemplateLiteral) String() string {
	result := "`"
	for i := range tl.Quasis {
		result += tl.Quasis[i]
		if i < len(tl.Expressions) {
			result += "${" + tl.Expressions[i].String() + "}"
		}
	}
	result += "`"
	return result
}
func (tl *TemplateLiteral) expressionNode() {}

// TaggedTemplateExpression 表示带标签的模板字面量。
// 例如: tag`hello ${name}`
type TaggedTemplateExpression struct {
	Token    lexer.Token // 标签后的反引号
	Tag      Expression  // 标签函数表达式
	Template *TemplateLiteral
}

func (tt *TaggedTemplateExpression) TokenLiteral() string { return tt.Token.Literal }
func (tt *TaggedTemplateExpression) String() string {
	return tt.Tag.String() + tt.Template.String()
}
func (tt *TaggedTemplateExpression) expressionNode() {}

// DynamicImportExpression 表示动态 import() 表达式。
// 例如: import("./mod.js")
type DynamicImportExpression struct {
	Token  lexer.Token // import 关键字
	Source Expression  // 模块路径表达式
}

func (di *DynamicImportExpression) TokenLiteral() string { return di.Token.Literal }
func (di *DynamicImportExpression) String() string {
	return "import(" + di.Source.String() + ")"
}
func (di *DynamicImportExpression) expressionNode() {}

// PrivateIdentifier 表示裸私有名表达式, 只作为 `#x in obj` 的左操作数
// (其余位置的 #x 都是 obj.#x 成员访问, 由 MemberExpression.Private 表达)。
type PrivateIdentifier struct {
	Token lexer.Token // PRIVATE_NAME token, Literal 含 #
	Name  string      // 不含 # 的裸名
}

func (pi *PrivateIdentifier) TokenLiteral() string { return pi.Token.Literal }
func (pi *PrivateIdentifier) String() string       { return "#" + pi.Name }
func (pi *PrivateIdentifier) expressionNode()      {}

// ==================== Class ====================

// ClassMethod 表示 class 中的一个方法。
type ClassMethod struct {
	Token         lexer.Token
	Name          string     // 方法名
	ComputedKey   Expression // 计算属性名 [expr] (非 nil 时优先于 Name)
	IsConstructor bool       // 是否为 constructor
	IsStatic      bool       // 是否 static 方法
	IsGetter      bool       // 是否 getter
	IsSetter      bool       // 是否 setter
	IsGenerator   bool       // 是否生成器方法: *name() {}
	IsAsync       bool       // 是否 async 方法: async name() {}
	IsPrivate     bool       // 是否 #name 私有成员 (Name 含前导 #)
	Parameters    []*Parameter
	Body          *BlockStatement
	FieldValue    Expression // 字段值 (方法解析时若为字段则非 nil)
}

// ClassField 表示 class 的实例字段。
type ClassField struct {
	Token       lexer.Token
	Name        string     // 字段名 (私有字段含前导 #)
	IsPrivate   bool       // 是否 #name 私有字段
	ComputedKey Expression // 计算属性名 [expr] (非 nil 时优先于 Name)
	Value       Expression // 字段值 (nil = 无初始化)
}

// ClassDeclaration 表示 class 声明。
// 例如: class Point { constructor(x) {...} sum() {...} }
type ClassDeclaration struct {
	Token      lexer.Token    // class 关键字
	Name       *Identifier    // 类名
	SuperClass Expression     // extends 表达式 (nil = 无继承)
	Methods    []*ClassMethod // 实例方法
	Statics    []*ClassMethod // 静态方法
	Fields     []*ClassField  // 实例字段
}

func (cd *ClassDeclaration) TokenLiteral() string { return cd.Token.Literal }
func (cd *ClassDeclaration) String() string       { return "class " + cd.Name.String() }
func (cd *ClassDeclaration) statementNode()       {}

// ClassExpression 表示 class 表达式。
// 例如: var C = class {}; var D = class Name extends Base {...}
// 与声明的区别: 类名可选 (匿名类), 表达式有值 (类本身)。
type ClassExpression struct {
	Token      lexer.Token    // class 关键字
	Name       *Identifier    // 类名 (匿名类为 nil)
	SuperClass Expression     // extends 表达式 (nil = 无继承)
	Methods    []*ClassMethod // 实例方法
	Statics    []*ClassMethod // 静态方法
	Fields     []*ClassField  // 实例字段
}

func (ce *ClassExpression) expressionNode()      {}
func (ce *ClassExpression) TokenLiteral() string { return ce.Token.Literal }
func (ce *ClassExpression) String() string {
	if ce.Name != nil {
		return "class " + ce.Name.String()
	}
	return "class"
}

// SuperExpression 表示 super 关键字。
// 在 class 方法中: super(...) 调用父构造函数, super.method() 调用父类方法。
type SuperExpression struct {
	Token lexer.Token
}

func (se *SuperExpression) TokenLiteral() string { return se.Token.Literal }
func (se *SuperExpression) String() string       { return "super" }
func (se *SuperExpression) expressionNode()      {}

// YieldExpression 表示 yield 表达式 (仅出现在 generator 函数中)。
// 例如: function* gen() { yield 1; }
type YieldExpression struct {
	Token    lexer.Token // YIELD
	Value    Expression  // yield 的表达式 (可为 nil: yield;)
	Delegate bool        // yield* expr: 委托给可迭代对象
}

func (ye *YieldExpression) TokenLiteral() string { return ye.Token.Literal }
func (ye *YieldExpression) String() string {
	op := "yield"
	if ye.Delegate {
		op = "yield*"
	}
	if ye.Value == nil {
		return op
	}
	return op + " " + ye.Value.String()
}
func (ye *YieldExpression) expressionNode() {}

// AwaitExpression 表示 await 表达式 (仅出现在 async 函数中)。
// 例如: async function f() { await p; }
type AwaitExpression struct {
	Token    lexer.Token // AWAIT
	Argument Expression  // await 的表达式
}

func (ae *AwaitExpression) TokenLiteral() string { return ae.Token.Literal }
func (ae *AwaitExpression) String() string       { return "await " + ae.Argument.String() }
func (ae *AwaitExpression) expressionNode()      {}

// BooleanLiteral 表示布尔字面量 true/false。
type BooleanLiteral struct {
	Token lexer.Token
	Value bool
}

func (bl *BooleanLiteral) TokenLiteral() string { return bl.Token.Literal }
func (bl *BooleanLiteral) String() string       { return bl.Token.Literal }
func (bl *BooleanLiteral) expressionNode()      {}

// NullLiteral 表示 null 字面量。
type NullLiteral struct {
	Token lexer.Token
}

func (nl *NullLiteral) TokenLiteral() string { return nl.Token.Literal }
func (nl *NullLiteral) String() string       { return "null" }
func (nl *NullLiteral) expressionNode()      {}

// UndefinedLiteral 表示 undefined 字面量。
type UndefinedLiteral struct {
	Token lexer.Token
}

func (ul *UndefinedLiteral) TokenLiteral() string { return ul.Token.Literal }
func (ul *UndefinedLiteral) String() string       { return "undefined" }
func (ul *UndefinedLiteral) expressionNode()      {}

// RegexLiteral 表示正则表达式字面量。
// 例如: /pattern/flags
type RegexLiteral struct {
	Token   lexer.Token // / 开始
	Pattern string      // 正则模式
	Flags   string      // 标志位 (g, i, m, s, u, y)
}

func (rl *RegexLiteral) TokenLiteral() string { return rl.Token.Literal }
func (rl *RegexLiteral) String() string       { return "/" + rl.Pattern + "/" + rl.Flags }
func (rl *RegexLiteral) expressionNode()      {}

// ==================== 数组和对象字面量 ====================

// ArrayLiteral 表示数组字面量。
// 例如: [1, 2, 3]
type ArrayLiteral struct {
	Token    lexer.Token // [
	Elements []Expression

	// TrailingComma 记录「最后一个元素之后还有逗号」(如 [1, 2, , 或 [a, b,])。
	// 数组字面量本身合法, 但转成解构赋值模式时, rest 之后带尾逗号是早错
	// ([...b,] = x ⇒ SyntaxError), 需要这一个比特才能判出来。
	TrailingComma bool
}

func (al *ArrayLiteral) TokenLiteral() string { return al.Token.Literal }
func (al *ArrayLiteral) String() string {
	elems := ""
	for i, e := range al.Elements {
		if i > 0 {
			elems += ", "
		}
		elems += e.String()
	}
	return "[" + elems + "]"
}
func (al *ArrayLiteral) expressionNode() {}

// PropertyKind 表示对象属性的种类。
type PropertyKind int

const (
	PROP_INIT   PropertyKind = iota // key: value
	PROP_METHOD                     // method() {}
	PROP_GETTER                     // get name() {}
	PROP_SETTER                     // set name(v) {}
)

// Property 表示对象字面量中的一个属性。
type Property struct {
	Token     lexer.Token
	Key       Expression // 属性键 (Identifier, StringLiteral, 或计算表达式)
	Value     Expression // 属性值
	Kind      PropertyKind
	Computed  bool // [表达式] 形式的计算属性名
	Shorthand bool // { name } 等价于 { name: name }
}

func (p *Property) String() string {
	if p.Shorthand {
		return p.Key.String()
	}
	if p.Computed {
		return "[" + p.Key.String() + "]: " + p.Value.String()
	}
	if p.Kind == PROP_METHOD {
		return p.Key.String() + "()" // 简化表示
	}
	if p.Kind == PROP_GETTER {
		return "get " + p.Key.String() + "()"
	}
	if p.Kind == PROP_SETTER {
		return "set " + p.Key.String() + "(v)"
	}
	return p.Key.String() + ": " + p.Value.String()
}

// ObjectLiteral 表示对象字面量。
// 例如: { name: "Alice", age: 30 }
type ObjectLiteral struct {
	Token      lexer.Token // {
	Properties []*Property
	Spread     []Expression // 展开对象 ({ ...a, y: 2 } 中的 a)
	// SpreadIsLast 表示唯一的展开项是否直接收尾 (后面紧跟 '}')。
	// 字面量里 `{...a, b}` 合法, 但转成解构模式时 rest 必须最后 ⇒
	// literalToPattern 靠它区分 `{a, ...r}` 与 `{...r, a}`。
	SpreadIsLast bool
}

func (ol *ObjectLiteral) TokenLiteral() string { return ol.Token.Literal }
func (ol *ObjectLiteral) String() string {
	parts := make([]string, 0, len(ol.Properties)+len(ol.Spread))
	for _, sp := range ol.Spread {
		parts = append(parts, "..."+sp.String())
	}
	for _, p := range ol.Properties {
		parts = append(parts, p.String())
	}
	return "{" + joinStrings(parts, ", ") + "}"
}
func (ol *ObjectLiteral) expressionNode() {}

// ==================== 运算表达式 ====================

// BinaryExpression 表示二元运算表达式。
// 例如: a + b, x === y
type BinaryExpression struct {
	Token    lexer.Token // 运算符令牌
	Left     Expression
	Operator string
	Right    Expression
}

func (be *BinaryExpression) TokenLiteral() string { return be.Token.Literal }
func (be *BinaryExpression) String() string {
	return "(" + be.Left.String() + " " + be.Operator + " " + be.Right.String() + ")"
}
func (be *BinaryExpression) expressionNode() {}

// UnaryExpression 表示一元运算表达式。
// 例如: !x, -y, typeof z, ++i, j--
type UnaryExpression struct {
	Token    lexer.Token // 运算符令牌
	Operator string
	Right    Expression
	Prefix   bool // true = 前置 (++i), false = 后置 (i++)
}

func (ue *UnaryExpression) TokenLiteral() string { return ue.Token.Literal }
func (ue *UnaryExpression) String() string {
	if ue.Prefix {
		return "(" + ue.Operator + ue.Right.String() + ")"
	}
	return "(" + ue.Right.String() + ue.Operator + ")"
}
func (ue *UnaryExpression) expressionNode() {}

// AssignmentExpression 表示赋值表达式。
// 例如: x = 5, y += 3
type AssignmentExpression struct {
	Token    lexer.Token // 运算符令牌
	Left     Expression  // 赋值目标 (Identifier 或 MemberExpression)
	Operator string      // "=", "+=" 等
	Right    Expression
}

func (ae *AssignmentExpression) TokenLiteral() string { return ae.Token.Literal }
func (ae *AssignmentExpression) String() string {
	return ae.Left.String() + " " + ae.Operator + " " + ae.Right.String()
}
func (ae *AssignmentExpression) expressionNode() {}

// LogicalExpression 表示逻辑运算表达式 (&&, ||)。
// 短路求值: && 左侧为假时直接返回左侧值；|| 左侧为真时直接返回左侧值。
type LogicalExpression struct {
	Token    lexer.Token
	Left     Expression
	Operator string // "&&" 或 "||"
	Right    Expression
}

func (le *LogicalExpression) TokenLiteral() string { return le.Token.Literal }
func (le *LogicalExpression) String() string {
	return "(" + le.Left.String() + " " + le.Operator + " " + le.Right.String() + ")"
}
func (le *LogicalExpression) expressionNode() {}

// SequenceExpression 表示逗号运算符表达式 (a, b, c)。
// 依次求值每个表达式, 返回最后一个的值。
type SequenceExpression struct {
	Token       lexer.Token // 第一个逗号
	Expressions []Expression
}

func (se *SequenceExpression) TokenLiteral() string { return se.Token.Literal }
func (se *SequenceExpression) String() string {
	parts := make([]string, len(se.Expressions))
	for i, e := range se.Expressions {
		parts[i] = e.String()
	}
	return "(" + joinStrings(parts, ", ") + ")"
}
func (se *SequenceExpression) expressionNode() {}

// ==================== 调用和成员访问 ====================

// CallExpression 表示函数调用。
// 例如: foo(a, b), obj.method()
type CallExpression struct {
	Token     lexer.Token // (
	Function  Expression  // 被调用的表达式
	Arguments []Expression
}

func (ce *CallExpression) TokenLiteral() string { return ce.Token.Literal }
func (ce *CallExpression) String() string {
	args := ""
	for i, a := range ce.Arguments {
		if i > 0 {
			args += ", "
		}
		args += a.String()
	}
	return ce.Function.String() + "(" + args + ")"
}
func (ce *CallExpression) expressionNode() {}

// MemberExpression 表示属性访问。
// 例如: obj.prop (点访问), arr[0] (方括号访问)
type MemberExpression struct {
	// Private 非 nil 时是 this.#name 访问: 值为不带 # 的私有名。
	// 与 Property 互斥 (私有访问编译为运行时动态键, 不走编译期属性常量)。
	Private string
	Token    lexer.Token // . 或 [
	Object   Expression
	Property Expression // Identifier (点访问) 或 Expression (方括号访问)
	Computed bool       // a[b] = true, a.b = false
}

func (me *MemberExpression) TokenLiteral() string { return me.Token.Literal }
func (me *MemberExpression) String() string {
	if me.Computed {
		return me.Object.String() + "[" + me.Property.String() + "]"
	}
	return me.Object.String() + "." + me.Property.String()
}
func (me *MemberExpression) expressionNode() {}

// OptionalMemberExpression 表示可选链属性访问 a?.b 或 a?.[b]。
// 当 Object 为 null/undefined 时整体短路为 undefined。
type OptionalMemberExpression struct {
	Token    lexer.Token // ?.
	Object   Expression
	Property Expression // Identifier (点访问) 或 Expression (方括号访问)
	Computed bool
}

func (ome *OptionalMemberExpression) TokenLiteral() string { return ome.Token.Literal }
func (ome *OptionalMemberExpression) String() string {
	if ome.Computed {
		return ome.Object.String() + "?.[" + ome.Property.String() + "]"
	}
	return ome.Object.String() + "?." + ome.Property.String()
}
func (ome *OptionalMemberExpression) expressionNode() {}

// OptionalCallExpression 表示可选链调用 a?.()。
type OptionalCallExpression struct {
	Token     lexer.Token // ?.
	Function  Expression
	Arguments []Expression
}

func (oce *OptionalCallExpression) TokenLiteral() string { return oce.Token.Literal }
func (oce *OptionalCallExpression) String() string {
	args := ""
	for i, a := range oce.Arguments {
		if i > 0 {
			args += ", "
		}
		args += a.String()
	}
	return oce.Function.String() + "?.(" + args + ")"
}
func (oce *OptionalCallExpression) expressionNode() {}

// ==================== 条件表达式 ====================

// ConditionalExpression 表示三元条件表达式。
// 例如: x > 0 ? "positive" : "negative"
type ConditionalExpression struct {
	Token       lexer.Token // ?
	Condition   Expression
	Consequence Expression
	Alternative Expression
}

func (ce *ConditionalExpression) TokenLiteral() string { return ce.Token.Literal }
func (ce *ConditionalExpression) String() string {
	return ce.Condition.String() + " ? " + ce.Consequence.String() + " : " + ce.Alternative.String()
}
func (ce *ConditionalExpression) expressionNode() {}

// ==================== 扩展元素 ====================

// SpreadElement 表示 ES6 扩展运算符。
// 用于数组字面量: [...arr] 和函数调用: f(...args)
type SpreadElement struct {
	Token    lexer.Token // ...
	Argument Expression
}

func (se *SpreadElement) TokenLiteral() string { return se.Token.Literal }
func (se *SpreadElement) String() string {
	return "..." + se.Argument.String()
}
func (se *SpreadElement) expressionNode() {}

// ==================== 解构模式 ====================

// PatternElement 表示数组解构中的一个元素。
type PatternElement struct {
	Token   lexer.Token
	Target  Expression // Identifier 或嵌套的 ArrayPattern/ObjectPattern
	Default Expression // 默认值 (nil = 无)
	Rest    bool       // ...rest
}

func (pe *PatternElement) String() string {
	result := ""
	if pe.Rest {
		result += "..."
	}
	// Target 为 nil 表示数组模式里的空洞 (elision): `[, a]` / `[a, , b]`。
	// 输出空串, 使 ArrayPattern.String() 拼出的 `[, a]` 与源码形状一致。
	if pe.Target != nil {
		result += pe.Target.String()
	}
	if pe.Default != nil {
		result += " = " + pe.Default.String()
	}
	return result
}

// ArrayPattern 表示数组解构模式。
// 例如: [a, b, ...rest]
type ArrayPattern struct {
	Token    lexer.Token // [
	Elements []*PatternElement
}

func (ap *ArrayPattern) TokenLiteral() string { return ap.Token.Literal }
func (ap *ArrayPattern) String() string {
	elems := ""
	for i, e := range ap.Elements {
		if i > 0 {
			elems += ", "
		}
		elems += e.String()
	}
	return "[" + elems + "]"
}
func (ap *ArrayPattern) expressionNode() {}

// PatternProperty 表示对象解构中的一个属性绑定。
type PatternProperty struct {
	Token     lexer.Token
	Key       Expression // 属性名 (Identifier, StringLiteral, 数字字面量, 或计算表达式)
	Value     Expression // 绑定目标 (Identifier 或嵌套 Pattern)
	Default   Expression // 默认值 (nil = 无)
	Shorthand bool       // { name } 等价于 { name: name }
	Computed  bool       // [表达式] 形式的计算属性名
}

func (pp *PatternProperty) String() string {
	if pp.Computed {
		result := "[" + pp.Key.String() + "]: " + pp.Value.String()
		if pp.Default != nil {
			result += " = " + pp.Default.String()
		}
		return result
	}
	if pp.Shorthand {
		result := pp.Value.String()
		if pp.Default != nil {
			result += " = " + pp.Default.String()
		}
		return result
	}
	result := pp.Key.String() + ": " + pp.Value.String()
	if pp.Default != nil {
		result += " = " + pp.Default.String()
	}
	return result
}

// ObjectPattern 表示对象解构模式。
// 例如: { a, b: c, d = default_val, ...rest }
type ObjectPattern struct {
	Token      lexer.Token // {
	Properties []*PatternProperty
	RestTarget Expression // ...rest 的绑定目标 (nil = 无 rest)
}

func (op *ObjectPattern) TokenLiteral() string { return op.Token.Literal }
func (op *ObjectPattern) String() string {
	props := ""
	for i, p := range op.Properties {
		if i > 0 {
			props += ", "
		}
		props += p.String()
	}
	if op.RestTarget != nil {
		if props != "" {
			props += ", "
		}
		props += "..." + op.RestTarget.String()
	}
	return "{" + props + "}"
}
func (op *ObjectPattern) expressionNode() {}

// ==================== this 表达式 ====================

// ThisExpression 表示 this 关键字。
type ThisExpression struct {
	Token lexer.Token
}

func (te *ThisExpression) TokenLiteral() string { return te.Token.Literal }
func (te *ThisExpression) String() string       { return "this" }
func (te *ThisExpression) expressionNode()      {}

// ==================== new 表达式 ====================

// NewExpression 表示 new 表达式。
// 例如: new Date()
type NewExpression struct {
	Token     lexer.Token // NEW
	Callee    Expression
	Arguments []Expression
}

func (ne *NewExpression) TokenLiteral() string { return ne.Token.Literal }
func (ne *NewExpression) String() string {
	args := ""
	for i, a := range ne.Arguments {
		if i > 0 {
			args += ", "
		}
		args += a.String()
	}
	return "new " + ne.Callee.String() + "(" + args + ")"
}
func (ne *NewExpression) expressionNode() {}

// ==================== throw 语句 ====================

// ThrowStatement 表示 throw 语句。
// 例如: throw new Error("oops");
type ThrowStatement struct {
	Token lexer.Token // THROW
	Value Expression  // 抛出的值
}

func (ts *ThrowStatement) TokenLiteral() string { return ts.Token.Literal }
func (ts *ThrowStatement) String() string {
	return ts.TokenLiteral() + " " + ts.Value.String() + ";"
}
func (ts *ThrowStatement) statementNode() {}

// ==================== try/catch/finally 语句 ====================

// TryStatement 表示 try/catch/finally 语句。
// 例如: try { ... } catch(e) { ... } finally { ... }
type TryStatement struct {
	Token       lexer.Token     // TRY
	Body        *BlockStatement // try 块
	CatchParam  Expression      // catch 绑定 (Identifier 或 ArrayPattern/ObjectPattern; nil = 可选 catch binding)
	CatchBody   *BlockStatement // catch 块 (可为 nil)
	FinallyBody *BlockStatement // finally 块 (可为 nil)
}

func (ts *TryStatement) TokenLiteral() string { return ts.Token.Literal }
func (ts *TryStatement) String() string {
	result := "try " + ts.Body.String()
	if ts.CatchBody != nil {
		param := ""
		if ts.CatchParam != nil {
			param = "(" + ts.CatchParam.String() + ") "
		}
		result += " catch " + param + ts.CatchBody.String()
	}
	if ts.FinallyBody != nil {
		result += " finally " + ts.FinallyBody.String()
	}
	return result
}
func (ts *TryStatement) statementNode() {}

// ==================== switch 语句 ====================

// SwitchCase 表示 switch 语句中的一个 case。
type SwitchCase struct {
	Token      lexer.Token // CASE 或 DEFAULT
	Test       Expression  // 条件表达式 (nil 表示 default)
	Statements []Statement // case 体
}

func (sc *SwitchCase) String() string {
	result := ""
	if sc.Test != nil {
		result += "case " + sc.Test.String() + ":"
	} else {
		result += "default:"
	}
	for _, s := range sc.Statements {
		result += " " + s.String()
	}
	return result
}

// SwitchStatement 表示 switch/case/default 语句。
type SwitchStatement struct {
	Token        lexer.Token   // SWITCH
	Discriminant Expression    // 判别表达式
	Cases        []*SwitchCase // case 列表
}

func (ss *SwitchStatement) TokenLiteral() string { return ss.Token.Literal }
func (ss *SwitchStatement) String() string {
	result := "switch (" + ss.Discriminant.String() + ") {\n"
	for _, c := range ss.Cases {
		result += "  " + c.String() + "\n"
	}
	result += "}"
	return result
}
func (ss *SwitchStatement) statementNode() {}

// ==================== import / export 语句 ====================

// NamedImport 表示命名导入里的**一项**, 含可选别名。
//
// 为什么不是单个 string: `import { x as y }` 有**两个**名字, 语义完全不同 ——
// Imported 是"模块里导出叫什么"(决定 GET_PROP 取哪个属性), Local 是"本文件里
// 绑成什么"(决定声明哪个变量)。早先只存一个 string, 于是 `{ x as y }` 被拆成
// 三个独立名字 [x, as, y], 别名直接丢失:
//
//	import { x as y } from "./m.js";   // y 静默为 undefined
//	import { f as g } from "gx/solid"; // 误报 `没有导出 "as"`
//
// Imported == Local 时即为无别名的普通形式 (`import { x }`), String() 会省略 `as`。
type NamedImport struct {
	Imported string // 模块导出的名字
	Local    string // 本文件绑定的名字 (省略 as 时与 Imported 相同)
}

// ImportDeclaration 表示 import 语句。
// 例如: import { foo, bar } from "./mod.js"
//
//	import * as ns from "./mod.js"
//	import defaultName from "./mod.js"
//	import { foo as bar } from "./mod.js"
type ImportDeclaration struct {
	Token        lexer.Token   // IMPORT
	DefaultName  string        // 默认导入名 (可为 "")
	NamedImports []NamedImport // 命名导入列表 (含别名)
	Namespace    string        // 命名空间导入名 (可为 "")
	Source       string        // 模块路径
}

func (id *ImportDeclaration) TokenLiteral() string { return id.Token.Literal }
func (id *ImportDeclaration) String() string {
	parts := []string{}
	if id.DefaultName != "" {
		parts = append(parts, id.DefaultName)
	}
	if id.Namespace != "" {
		parts = append(parts, "* as "+id.Namespace)
	}
	if len(id.NamedImports) > 0 {
		names := make([]string, 0, len(id.NamedImports))
		for _, n := range id.NamedImports {
			if n.Local == "" || n.Local == n.Imported {
				names = append(names, n.Imported)
			} else {
				names = append(names, n.Imported+" as "+n.Local)
			}
		}
		parts = append(parts, "{ "+joinStrings(names, ", ")+" }")
	}
	// 副作用导入 (`import "./m.js"`) 没有任何绑定, 不能拼出 "import  from" 的双空格
	if len(parts) == 0 {
		return "import \"" + id.Source + "\";"
	}
	return "import " + joinStrings(parts, ", ") + " from \"" + id.Source + "\";"
}
func (id *ImportDeclaration) statementNode() {}

// ExportSpecifier 表示 `export { Local as Exported }` / `export { Local as Exported } from "m"`
// 里的一项。
//
// 为什么需要 Local/Exported 两个名字, 而不是像早期那样只存一个字符串:
//   - `export { a as b }` 里 a 是"本模块(或来源模块)里的名字", b 才是对外导出的名字。
//     只存一个字符串会把 `a as b` 当成"导出 a 和 b 两个名字", 别名语义整个丢失
//     (import 侧的 NamedImport 已经踩过同一个坑, 见其注释)。
type ExportSpecifier struct {
	Local    string // 本模块(或来源模块)里的名字; export * as ns 时为 "*"
	Exported string // 对外导出的名字
}

// ExportDeclaration 表示 export 语句。
//
// 覆盖以下形态:
//
//	export { a, b as c }                 // 本地命名导出 (可别名)
//	export { a, b as c } from "./m.js"   // 具名再导出 (活绑定转发)
//	export * from "./m.js"               // 星号再导出 (不含 default, 不覆盖本地)
//	export * as ns from "./m.js"         // 命名空间再导出
//	export {}                            // 空导出 (合法, 无副作用)
//	export default expression
//	export default function f() {}       // 具名函数: f 是模块内局部绑定, 不作命名导出
//	export default class C {}            // 具名类: 同上
//	export var/let/const ...             // 声明并导出
//	export function/async function/class ...
type ExportDeclaration struct {
	Token       lexer.Token       // EXPORT
	IsDefault   bool              // 是否为 export default
	IsStar      bool              // export * from "..."
	Declaration Statement         // 被导出的声明 (let/const/var/function/class)
	Specifiers  []ExportSpecifier // export { ... } 列表 (含别名与再导出)
	Source      string            // 有 "from" 时非空 ("export ... from <Source>")
}

func (ed *ExportDeclaration) TokenLiteral() string { return ed.Token.Literal }
func (ed *ExportDeclaration) String() string {
	if ed.IsDefault {
		if ed.Declaration != nil {
			return "export default " + ed.Declaration.String()
		}
		return "export default;"
	}
	if ed.IsStar {
		return "export * from \"" + ed.Source + "\";"
	}
	if len(ed.Specifiers) > 0 {
		parts := make([]string, 0, len(ed.Specifiers))
		for _, sp := range ed.Specifiers {
			if sp.Local == sp.Exported {
				parts = append(parts, sp.Local)
			} else {
				parts = append(parts, sp.Local+" as "+sp.Exported)
			}
		}
		s := "export { " + joinStrings(parts, ", ") + " }"
		if ed.Source != "" {
			s += " from \"" + ed.Source + "\""
		}
		return s + ";"
	}
	if ed.Declaration != nil {
		return "export " + ed.Declaration.String()
	}
	return "export {};"
}
func (ed *ExportDeclaration) statementNode() {}

func joinStrings(strs []string, sep string) string {
	result := ""
	for i, s := range strs {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}

package parser

import (
	"testing"

	"github.com/14752222/Gox/lexer"
)

// JSX 解析测试: 断言 JSX 输入与手写 h(...) 调用产出完全相同的 AST。
// 通过对比 program.String() 实现等价性判定。

// parseToString 解析源码并返回其 AST 的字符串表示。
func parseToString(t *testing.T, input string) string {
	t.Helper()
	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)
	return program.String()
}

// assertDesugar 断言 JSX 源码与等价的手写调用产出相同的 AST。
func assertDesugar(t *testing.T, jsx, handwritten string) {
	t.Helper()
	got := parseToString(t, jsx)
	want := parseToString(t, handwritten)
	if got != want {
		t.Fatalf("JSX desugar mismatch:\n jsx:         %s\n got  (AST):  %s\n want (AST):  %s", jsx, got, want)
	}
}

// TestJSXDesugarBasic 基本降级: 文本属性值、表达式属性值、文本子节点。
func TestJSXDesugarBasic(t *testing.T) {
	assertDesugar(t,
		`x = <text font={20}>hi</text>;`,
		`x = h("text", {font: 20}, "hi");`)
}

// TestJSXDesugarSelfClose 自闭合与无属性 (props 传 null)。
func TestJSXDesugarSelfClose(t *testing.T) {
	assertDesugar(t,
		`x = <rect width={8} />;`,
		`x = h("rect", {width: 8});`)
	assertDesugar(t,
		`x = <br />;`,
		`x = h("br", null);`)
}

// TestJSXDesugarComponent 大写标签 → 组件标识符调用。
func TestJSXDesugarComponent(t *testing.T) {
	assertDesugar(t,
		`x = <Counter step={2}>hi</Counter>;`,
		`x = Counter({step: 2}, "hi");`)
}

// TestJSXDesugarFunctionChild 函数子节点原样传递 (响应式 computed 约定)。
func TestJSXDesugarFunctionChild(t *testing.T) {
	assertDesugar(t,
		"x = <text font={20}>{() => `count: ${count()}`}</text>;",
		"x = h(\"text\", {font: 20}, () => `count: ${count()}`);")
}

// TestJSXDesugarEventAndUpdater 事件回调与更新函数 setter。
func TestJSXDesugarEventAndUpdater(t *testing.T) {
	assertDesugar(t,
		`x = <button onClick={() => setCount(c => c + 1)}>加一</button>;`,
		`x = h("button", {onClick: () => setCount(c => c + 1)}, "加一");`)
}

// TestJSXDesugarNested 嵌套元素按序作为 h 的后续参数。
func TestJSXDesugarNested(t *testing.T) {
	assertDesugar(t,
		"x =\n  <column gap={8}>\n    <text font={20}>a</text>\n    <row>b</row>\n  </column>;",
		`x = h("column", {gap: 8}, h("text", {font: 20}, "a"), h("row", null, "b"));`)
}

// TestJSXDesugarWhitespace 子文本空白规整 (Babel/Solid 语义):
// 逐行 trim、纯空白行删除、保留行以单空格连接。
func TestJSXDesugarWhitespace(t *testing.T) {
	assertDesugar(t,
		"x = <p>\n  hello\n  world\n</p>;",
		`x = h("p", null, "hello world");`)
	assertDesugar(t,
		"x =\n  <button>\n    加一\n  </button>;",
		`x = h("button", null, "加一");`)
	// 文本与表达式混排
	assertDesugar(t,
		"x = <p>Total: {n} items</p>;",
		`x = h("p", null, "Total:", n, "items");`)
}

// TestJSXDesugarAttrForms 裸属性 → true; 含 - 的属性名; 字符串属性值。
func TestJSXDesugarAttrForms(t *testing.T) {
	assertDesugar(t,
		`x = <input disabled />;`,
		`x = h("input", {disabled: true});`)
	assertDesugar(t,
		`x = <label data-role="name" />;`,
		`x = h("label", {"data-role": "name"});`)
}

// TestJSXInExpressionPositions JSX 出现在各种表达式位置。
func TestJSXInExpressionPositions(t *testing.T) {
	// return
	assertDesugar(t,
		"function f() { return <a>x</a>; }",
		`function f() { return h("a", null, "x"); }`)
	// 括号内
	assertDesugar(t,
		"x = (<a>x</a>);",
		`x = (h("a", null, "x"));`)
	// 数组元素
	assertDesugar(t,
		"x = [<a/>, <b/>];",
		`x = [h("a", null), h("b", null)];`)
	// 条件与嵌套插值内的 JSX
	assertDesugar(t,
		"x = <ul>{items.map(i => <li>{i}</li>)}</ul>;",
		`x = h("ul", null, items.map(i => h("li", null, i)));`)
	// 比较表达式里 `<` 不受影响
	assertDesugar(t,
		"x = a < b;",
		`x = a < b;`)
}

// TestJSXErrors 非法 JSX 应产生解析错误而非崩溃。
func TestJSXErrors(t *testing.T) {
	cases := []string{
		"x = <div>t</span>;",     // 闭合标签不匹配
		"x = <div>t",             // 未闭合
		"x = <div attr=weird/>;", // 属性值形式非法
	}
	for _, src := range cases {
		l := lexer.New(src)
		p := New(l)
		p.ParseProgram()
		if !p.Errors().HasErrors() {
			t.Fatalf("expected parse errors for %q", src)
		}
	}
}

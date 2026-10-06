package parser

import "testing"

// ===== 生成器体内 yield 作标签标识符早错 (rDc5ui, 2026-10-06) =====
//
// 规范 LabelIdentifier : Identifier —— It is a Syntax Error if this production
// has a [Yield] parameter and StringValue of Identifier is "yield"。
// 仅 generator / async-generator 函数体 (含其中箭头) 拦; sloppy 非生成器代码里
// yield 是合法 IdentifierName, `yield: ;` 仍是标签语句 (参照 test262
// {language/expressions,language/statements}/{async-,}generator[s]/
// [named-]yield-as-label-identifier.js)。

func TestYieldAsLabelIdentifierInGenerator(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// 报错: 生成器 / async-generator 体内 yield 作标签名
		{"generator 声明", `function* g(){ yield: ; }`, true},
		{"async generator 声明", `async function* g(){ yield: ; }`, true},
		{"generator 表达式", `(function*(){ yield: ; });`, true},
		{"async generator 表达式", `(async function*(){ yield: ; });`, true},
		{"匿名 generator 表达式", `(function*{ yield: ; });`, true},
		{"换行分隔也拦", "function* g(){ yield\n: ; }", true},
		{"嵌套标签语句", `function* g(){ outer: yield: ; }`, true},
		{"if 单语句体", `function* g(){ if (x) yield: ; }`, true},
		{"对象生成器方法", `({ *m(){ yield: ; } });`, true},
		{"对象 async 生成器方法", `({ async *m(){ yield: ; } });`, true},
		{"类生成器方法", `class C { *m(){ yield: ; } }`, true},
		{"类 async 生成器方法", `class C { async *m(){ yield: ; } }`, true},
		{"类私有生成器方法", `class C { *#m(){ yield: ; } }`, true},
		{"类静态生成器方法", `class C { static *m(){ yield: ; } }`, true},
		{"箭头继承生成器语境", `function* g(){ () => { yield: ; }; }`, true},
		{"async 箭头继承", `function* g(){ async () => { yield: ; }; }`, true},
		{"export default 匿名生成器", `export default function*(){ yield: ; };`, true},
		{"export default 具名生成器", `export default function* g(){ yield: ; };`, true},
		// 合法: 非生成器语境 yield 是普通 IdentifierName
		{"sloppy 顶层标签", `yield: ;`, false},
		{"sloppy 顶层标签带体", `yield: 1;`, false},
		{"普通函数体内", `function f(){ yield: ; }`, false},
		{"async 函数体内", `async function f(){ yield: ; }`, false},
		{"生成器内嵌普通函数", `function* g(){ function f(){ yield: ; } }`, false},
		{"生成器内嵌箭头以外层 yield 表达式", `function* g(){ var x = yield; }`, false},
		{"生成器方法返回普通函数", `class C { *m(){ return function(){ yield: ; }; } }`, false},
		{"普通标签名", `function* g(){ outer: 1; }`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := parseSrc(t, tc.src)
			if ok == tc.wantErr {
				t.Errorf("wantErr=%v 但 ok=%v (无解析错误=%v)\nsrc: %s", tc.wantErr, ok, ok, tc.src)
			}
		})
	}
}

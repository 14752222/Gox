package parser

import "testing"

// ===== 块级早错 (rUZN3k 第 1 块) =====
//
// sec-block-static-semantics-early-errors:
//   LexicallyDeclaredNames(StatementList) ∩ VarDeclaredNames(StatementList) ≠ ∅
//   ⇒ SyntaxError。
//
// 每条期望值都与 Node 22 实测一致 (node --check)。

func TestBlockLexicalVarRedeclaration(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// ---- 必须报错: 同块 let/const/class/function ∩ var ----
		{"let 与 var 同名", `{ let f; var f }`, true},
		{"var 在前 let 在后", `{ var f; let f }`, true},
		{"const 与 var 同名", `{ const f = 1; var f }`, true},
		{"class 与 var 同名", `{ class f {} var f }`, true},
		{"function 声明与 var 同名", `{ function f(){} var f }`, true},
		{"let 与 var 同函数体内", `function x() { { let f; var f; } }`, true},
		// 嵌套块里的 var 也算当前块 VarDeclaredNames (var 提升)
		{"内层块 var 与外层 let 冲突", `{ let f; { var f; } }`, true},
		{"外层 let 在内层块 var 之后", `{ { var f; } let f; }`, true},
		// if / 循环体内的 var 也算
		{"if 体 var 与块 let 冲突", `{ let f; if (true) { var f; } }`, true},
		{"for 体 var 与块 let 冲突", `{ let f; for (;;) { var f; } }`, true},

		// ---- 合法: 不得报错 ----
		{"纯 let 重复在不同块", `{ let f; } { let f; }`, false},
		{"var 重复合法", `{ var f; var f; }`, false},
		{"let 与不同名 var", `{ let f; var g; }`, false},
		{"内层块的 let 不与外层 var 冲突", `{ var f; { let f; } }`, false},
		{"函数体内 let 与外部 var 不同名", `{ var g; function f(){ let g; } }`, false},
		// 函数边界切断: 函数体内的 var 不外泄
		{"函数体内 var 不与外部 let 冲突", `{ let f; function g(){ var f; } }`, false},
		// 解构合成名不参与
		{"解构 var 不与 let 冲突", `{ let f; var [a] = b; }`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := parseSrc(t, tc.src)
			if ok == tc.wantErr {
				t.Errorf("wantErr=%v 但解析结果 ok=%v\nsrc: %s", tc.wantErr, ok, tc.src)
			}
		})
	}
}

// TestBlockPositionDeclEarlyErrors import/export 只能出现在模块顶层。
// (test262 module-code/parse-err-decl-pos-*.js; node 22 实测一致)
func TestBlockPositionDeclEarlyErrors(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"块内 import", `{ import x from "y" }`, true},
		{"块内 export default", `{ export default null }`, true},
		{"块内 export 在语句后", `{ void 0; export default null; }`, true},
		{"函数体内 import", `function f(){ import x from "y" }`, true},
		{"else 单语句 export", `if (true) { } else export default null;`, true},
		// 合法: 顶层
		{"顶层 import", `import x from "y";`, false},
		{"顶层 export default", `export default null;`, false},
		{"顶层副作用 import", `import "y";`, false},
		{"动态 import 表达式在块里", `function f(){ import("y") }`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := parseSrc(t, tc.src)
			if ok == tc.wantErr {
				t.Errorf("wantErr=%v 但解析结果 ok=%v\nsrc: %s", tc.wantErr, ok, tc.src)
			}
		})
	}
}

// TestSameLineASIMissing 同一行缺分号 ⇒ SyntaxError (换行可 ASI)。
// (test262 asi/S7.9_A10_T7/T8/T9, S7.9.2_A1_T2)
func TestSameLineASIMissing(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"表达式语句同行第二个标识符", `x = 1 y = 2`, true},
		{"调用同行缺分号", `foo() bar()`, true},
		{"var 同行缺分号", `var x = 1 var y = 2`, true},
		{"let 同行缺分号", `let x = 1 let y = 2`, true},
		// 合法: 换行 → ASI
		{"换行插入分号", "x = 1\ny = 2", false},
		{"分号正常", `x = 1; y = 2`, false},
		{"行尾分号", `x = 1;`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := parseSrc(t, tc.src)
			if ok == tc.wantErr {
				t.Errorf("wantErr=%v 但解析结果 ok=%v\nsrc: %q", tc.wantErr, ok, tc.src)
			}
		})
	}
}

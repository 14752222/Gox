package vm

import (
	"strings"
	"testing"
)

// var 支持 (T04/Test262 前置) 的语义回归 —— 每条用例对应规范行为的一角,
// 而不是"能跑就行"。var 与 let 的分界是本组测试的真正主题。
func TestVarBasics(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{"basic", `var x = 5; x`, "5"},
		{"no-init", `var z; z === undefined`, "true"},
		{"multi", `var m = 1, n = 2; m + n`, "3"},
		{"redeclare", `var x = 1; var x = 2; x`, "2"},
		{"redeclare-mixed-init", `var x; var x = 9; x`, "9"},
		{"hoist-read", `typeof x + "," + (x === undefined); var x = 1`, "undefined,true"},
		{"block-escapes", `{ var b = 7 } b`, "7"},
		{"fn-scope-escapes", `function f(){ if(true){ var q = 3 } return q } f()`, "3"},
		{"for-classic-shared", `let s=0; for (var i = 0; i < 3; i++) { s += i } s + "," + i`, "3,3"},
		{"for-var-closure-shared", `let fns=[]; for (var j = 0; j < 3; j++) { fns.push(()=>j) } fns[0]()+","+fns[2]()`, "3,3"},
		{"for-let-closure-per-iter", `let fns2=[]; for (let k = 0; k < 3; k++) { fns2.push(()=>k) } fns2[0]()+","+fns2[2]()`, "0,2"},
		{"for-of-var", `let out=[]; for (var e of [10,20]) { out.push(e) } out.join("-")`, "10-20"},
		{"for-in-var", `let keys=[]; let obj={a:1,b:2}; for (var k in obj) { keys.push(k) } keys.join("")`, "ab"},
		{"var-fn-hoist-kept", `function g(){ var f; function f(){ return 2 } return f() } g()`, "2"},
		{"var-assign-after-fn", `function h(){ var f = 1; function f(){ return 2 } return f } h()`, "1"},
		{"param-reuse", `function g(a){ var a = a + 1; return a } g(5)`, "6"},
		{"global-fn-reads", `var gv = 10; function h(){ return gv } h()`, "10"},
		{"destructure", `var [p, q] = [1, 2]; p + q`, "3"},
		{"let-shadows-var", `var o = 1; { let o = 2 } o`, "1"},
		{"var-in-try", `try { var t = 1; throw "x" } catch(e) {} t`, "1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := Eval(tc.src)
			if err != nil {
				t.Fatalf("%s: %v", tc.src, err)
			}
			if v == nil || v.Inspect() != tc.want {
				t.Fatalf("%s: got %v, want %s", tc.src, v, tc.want)
			}
		})
	}
}

// var 与词法声明的冲突边界 (SyntaxError)。
func TestVarConflicts(t *testing.T) {
	cases := []string{
		`var v = 1; let v = 2`,   // var 后 let 同层
		`let v = 1; var v = 2`,   // let 后 var 同层
		`var v = 1; const v = 2`, // var 后 const 同层
	}
	for _, src := range cases {
		_, err := Eval(src)
		if err == nil {
			t.Fatalf("%s: expected SyntaxError, got none", src)
		}
		if !strings.Contains(err.Error(), "already been declared") {
			t.Fatalf("%s: unexpected error: %v", src, err)
		}
	}
}

// 跨层同名是合法的 (var 函数层, let 块层)。
func TestVarLexicalCrossLayer(t *testing.T) {
	v, err := Eval(`var x = "fn"; { let x = "block" } x`)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if v.Inspect() != "fn" {
		t.Fatalf("got %v", v)
	}
}

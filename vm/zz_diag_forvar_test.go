package vm

import "testing"

// 诊断: for(var of) 在**函数体内**的闭包共享语义。
func TestDiagForVarOfInFunction(t *testing.T) {
	// (a) 函数体内 var —— 走 frame.Locals 槽位路径
	got := evalWithStdlib(t, `
		function mk() {
			var fns = [];
			for (var x of [1,2,3]) { fns.push(function(){ return x; }); }
			return fns.map(function(f){ return f(); });
		}
		JSON.stringify(mk());
	`)
	t.Logf("(a) function-body var of => %s", got.Inspect())

	// (b) 函数体内 let —— 对照
	got2 := evalWithStdlib(t, `
		function mk() {
			var fns = [];
			for (let x of [1,2,3]) { fns.push(function(){ return x; }); }
			return fns.map(function(f){ return f(); });
		}
		JSON.stringify(mk());
	`)
	t.Logf("(b) function-body let of => %s", got2.Inspect())

	// (c) 函数体内 var in
	got3 := evalWithStdlib(t, `
		function mk() {
			var fns = [];
			for (var k in {a:1,b:2,c:3}) { fns.push(function(){ return k; }); }
			return fns.map(function(f){ return f(); });
		}
		JSON.stringify(mk());
	`)
	t.Logf("(c) function-body var in => %s", got3.Inspect())

	// (d) 箭头函数形态
	got4 := evalWithStdlib(t, `
		function mk() {
			var fns = [];
			for (var x of [1,2,3]) { fns.push(() => x); }
			return fns.map(function(f){ return f(); });
		}
		JSON.stringify(mk());
	`)
	t.Logf("(d) function-body var of arrow => %s", got4.Inspect())
}

package vm

import "testing"

// ===== 循环迭代绑定语义: var/外层 let 共享 vs let/const 每轮新建 (rUm0q6) =====
//
// 规范要点 (node v22 实测基准):
//   - for (var x of/in …) 的 x 是**函数作用域单实例**绑定, 所有轮次的闭包
//     共享同一颗 cell ⇒ 全部看到最终值 [3,3,3] / ["c","c","c"]。
//   - for (let/const x of/in …) 的 x **每轮新建** ⇒ 各闭包看到各自的值。
//   - 循环体外层的 var / let 被体内改写时, 所有闭包看到共享终值。
//   - 循环体内 let/const 亦是每轮新建。
//
// 根因历史: OP_ITER_BOUNDARY 克隆整个 Locals 数组 + 清空 CreatedClosures,
// 把共享绑定也一并"定版", 于是 var 循环退化成 let 语义 (在**函数体内**才
// 复现; 顶层 var 走全局存储不受影响 —— 这正是"脚本模式假阴性"陷阱)。

// assertJSON 断言 JS 表达式 `JSON.stringify(e)` 的字符串结果。
func assertJSON(t *testing.T, e, want string) {
	t.Helper()
	got := evalWithStdlib(t, "JSON.stringify("+e+")")
	assertString(t, got, want)
}

func TestForVarBindingSharedClosure(t *testing.T) {
	// for (var x of …): 函数体内, 闭包共享同一绑定 → 终值
	assertJSON(t, `(function(){
		var fns = [];
		for (var x of [1,2,3]) { fns.push(function(){ return x; }); }
		return fns.map(function(f){ return f(); });
	})()`, `[3,3,3]`)

	// 箭头函数形态
	assertJSON(t, `(function(){
		var fns = [];
		for (var x of [1,2,3]) { fns.push(() => x); }
		return fns.map(function(f){ return f(); });
	})()`, `[3,3,3]`)

	// for (var k in …)
	assertJSON(t, `(function(){
		var fns = [];
		for (var k in {a:1,b:2,c:3}) { fns.push(function(){ return k; }); }
		return fns.map(function(f){ return f(); });
	})()`, `["c","c","c"]`)

	// 体内改写 var
	assertJSON(t, `(function(){
		var fns = [];
		for (var w of [1,2,3]) { w = w * 10; fns.push(function(){ return w; }); }
		return fns.map(function(f){ return f(); });
	})()`, `[30,30,30]`)
}

func TestForLetBindingPerIterationClosure(t *testing.T) {
	// for (let x of …): 每轮新建 → 各自的值 (回归护栏: 别把 let 修坏)
	assertJSON(t, `(function(){
		var fns = [];
		for (let x of [1,2,3]) { fns.push(function(){ return x; }); }
		return fns.map(function(f){ return f(); });
	})()`, `[1,2,3]`)

	assertJSON(t, `(function(){
		var fns = [];
		for (let k in {a:1,b:2}) { fns.push(function(){ return k; }); }
		return fns.map(function(f){ return f(); });
	})()`, `["a","b"]`)

	// 循环体内 let 也每轮新建 (即使循环变量是 var)
	assertJSON(t, `(function(){
		var fns = [];
		for (var x of [10,20,30]) { let b = x; fns.push(function(){ return b; }); }
		return fns.map(function(f){ return f(); });
	})()`, `[10,20,30]`)

	// 经典 for 的 let 计数器
	assertJSON(t, `(function(){
		var fns = [];
		for (let i = 0; i < 3; i++) { fns.push(function(){ return i; }); }
		return fns.map(function(f){ return f(); });
	})()`, `[0,1,2]`)
}

func TestOuterBindingMutatedInLoopShared(t *testing.T) {
	// 外层 let 被体内改写: 所有闭包看到共享终值 (不是逐轮快照)
	assertJSON(t, `(function(){
		let o = 0; var fns = [];
		for (let x of [1,2,3]) { o = o + x; fns.push(function(){ return o; }); }
		return fns.map(function(f){ return f(); });
	})()`, `[6,6,6]`)

	assertJSON(t, `(function(){
		let o = 0; var fns = [];
		for (var x of [1,2,3]) { o = o + x; fns.push(function(){ return o; }); }
		return fns.map(function(f){ return f(); });
	})()`, `[6,6,6]`)

	// while / do-while: 同一根因
	assertJSON(t, `(function(){
		let o = 0; var fns = []; var i = 0;
		while (i < 3) { i = i + 1; o = o + i; fns.push(function(){ return o; }); }
		return fns.map(function(f){ return f(); });
	})()`, `[6,6,6]`)

	assertJSON(t, `(function(){
		let o = 0; var fns = []; var i = 0;
		do { i = i + 1; o = o + i; fns.push(function(){ return o; }); } while (i < 3);
		return fns.map(function(f){ return f(); });
	})()`, `[6,6,6]`)
}

func TestNestedLoopBindingCapture(t *testing.T) {
	// 外层 var + 内层 let (嵌套): 外层终值, 内层逐轮
	assertJSON(t, `(function(){
		var fns = [];
		for (var i of [1,2]) {
			for (let j of [10,20]) { fns.push(function(){ return [i,j]; }); }
		}
		return fns.map(function(f){ return f(); });
	})()`, `[[2,10],[2,20],[2,10],[2,20]]`)

	// 内外都 let
	assertJSON(t, `(function(){
		var fns = [];
		for (let i of [1,2]) {
			for (let j of [10,20]) { fns.push(function(){ return [i,j]; }); }
		}
		return fns.map(function(f){ return f(); });
	})()`, `[[1,10],[1,20],[2,10],[2,20]]`)

	// 外层 var 在内层循环体里被反复改写 → 共享终值
	assertJSON(t, `(function(){
		var fns = [];
		for (var i of [1,2]) {
			for (let j of [10,20]) { i = i*100; fns.push(function(){ return [i,j]; }); }
		}
		return fns.map(function(f){ return f(); });
	})()`, `[[20000,10],[20000,20],[20000,10],[20000,20]]`)
}

package vm

import "testing"

// TestGetterNestedClosureSharedCell 是 r6e5qp 第 1 项的前置回归。
//
// 旧缺陷: createClosure 用 `frame.Locals[:BaseSlot]` 作捕获数组, 而闭包
// 装配帧的 Locals 是 callClosure 从 CapturedLocals **拷贝**来的新数组
// (原始 binding cell 在 frame.SharedCells)。于是对象字面量 getter 体内
// 再定义的闭包捕获到 getter 帧的私有拷贝 —— 每次属性访问 getter 被重新
// 调用, 新拷贝, 内层闭包读到的一直是创建时的快照:
//
//	function make() {
//	  var n = 0;
//	  return { get foo() { return function() { n++; return n; }; } };
//	}
//	make().foo() // 旧实现恒为 1, 规范 & Node 为累加 1,2,3...
//
// 触发面: test262 async-generator yield* 委托系列 (get next + nextCount
// 记序模式)、以及大量对象迭代器协议用例 (eachNext 等)。
//
// 期望值与 Node 实测一致。
func TestGetterNestedClosureSharedCell(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{
			"getter 内嵌 function 经方法式调用累加",
			`function make() {
			   var n = 0;
			   return { get foo() { return function() { n++; return n; } } };
			 }
			 var o = make();
			 var out = [];
			 out.push(o.foo());
			 out.push(o.foo());
			 out.push(o.foo());
			 out.push((o.foo)());
			 __out.push("1:" + out.join(","));`,
			"1:1,2,3,4\n",
		},
		{
			"method 内对象字面量 getter 内嵌 function (三层)",
			`var obj = {
			   [Symbol.iterator]() {
			     var count = 0;
			     return {
			       get next() {
			         return function() { count++; log.push("call #" + count); return count; };
			       }
			     };
			   }
			 };
			 var log = [];
			 var it = obj[Symbol.iterator]();
			 var r = [];
			 r.push(it.next());
			 r.push(it.next());
			 r.push(it.next());
			 __out.push("2:" + r.join(",") + " " + log.join(","));`,
			"2:1,2,3 call #1,call #2,call #3\n",
		},
		{
			"getter 直接累加外层变量 (单层守卫)",
			`function make() {
			   var n = 0;
			   return { get val() { n++; return n; } };
			 }
			 var o = make();
			 __out.push("3:" + o.val + "," + o.val + "," + o.val);`,
			"3:1,2,3\n",
		},
		{
			"绑定后调用与兄弟闭包共享 (既有语义守卫)",
			`function make() {
			   var n = 0;
			   return { get foo() { return function() { n++; return n; } } };
			 }
			 var o = make();
			 var f = o.foo;
			 var g = o.foo;
			 var out = [];
			 out.push(f(), g(), f(), g());
			 __out.push("4:" + out.join(","));`,
			"4:1,2,3,4\n",
		},
		{
			"不同实例的 cell 互相独立 (守卫)",
			`function make() {
			   var n = 0;
			   return { get foo() { return function() { n++; return n; } } };
			 }
			 var a = make();
			 var b = make();
			 var out = [];
			 out.push(a.foo(), b.foo(), a.foo(), b.foo());
			 __out.push("5:" + out.join(","));`,
			"5:1,1,2,2\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := evalOut(t, tc.src); got != tc.want {
				t.Errorf("want:\n%s\ngot:\n%s", tc.want, got)
			}
		})
	}
}

package vm

import (
	"testing"
)

// ===== 静态成员计算属性名在外层上下文求值 (r4hv9u) =====
//
// 规范 ClassElementList Evaluation: 静态成员的 ComputedPropertyName 在**外层
// 函数上下文**求值 —— 外层是生成器时 `static get [yield 9](){}` 合法。旧实现
// 把静态计算键编进 __static_init__ 合成函数 (非生成器帧), yield 在运行时撞
// TypeError "yield outside generator" (test262:
// cpn-class-{expr,decl}-accessors-computed-property-name-from-yield-expression.js)。
// 现在 compileClassBody 把全部静态计算键按定义顺序在外层预求值成数组传进
// 合成函数, 本组测试钉住这条通道的求值顺序与值。

// TestStaticComputedKeyYieldInGenerator
// 生成器体内 class 的实例/静态访问器计算键用 yield 求值。
func TestStaticComputedKeyYieldInGenerator(t *testing.T) {
	src := `
function* g() {
  let C = class {
    get [yield 9]() { return 1; }
    set [yield 9](v) { }
    static get [yield 9]() { return 2; }
    static set [yield 9](v) { }
  };
  return C[9] * 10 + (new C())[9];
}
let it = g();
let sum = 0;
let x = it.next(9);
while (!x.done) { sum = sum + x.value; x = it.next(9); }
sum + x.value
`
	// next(9) 让每个 yield 9 求值为 9 ⇒ 计算键均为 "9": 四个计算键各产出 9
	// (9*4), return C[9]*10 + c[9] = 2*10+1 = 21, 合计 57。
	assertNumber(t, evalJS(t, src), 57)
}

// TestStaticComputedKeyEvalOrder
// 静态计算键按定义顺序在外层求值 (先于静态字段初始化器)。
func TestStaticComputedKeyEvalOrder(t *testing.T) {
	src := `
let order = [];
let k = i => { order.push("k" + i); return "p" + i; };
class C {
  static [k(1)] = (order.push("f1"), 10);
  static [k(2)] = (order.push("f2"), 20);
  static m = order.push("m");
}
order.join(",")
`
	// 计算键先于字段初始化器 (规范 ClassElementList Evaluation 先求值全部
	// ComputedPropertyName, 再 ClassFieldDefinitionEvaluation 跑静态初始化器)。
	assertString(t, evalJS(t, src), "k1,k2,f1,f2,m")
}

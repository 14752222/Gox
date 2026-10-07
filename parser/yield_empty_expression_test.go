package parser

import (
	"testing"
)

// ===== 空 yield 在表达式位置 (rmiC4k, 2026-10-07) =====
//
// 规范 YieldExpression : `yield` 与 `yield [no LineTerminator here] AssignmentExpression`
// 并列 —— 零参 `yield` 本身是完整的 AssignmentExpression, 可出现在任何
// AssignmentExpression 允许的位置 (数组/对象字面量元素与展开、实参、括号、
// 条件表达式分支、赋值右侧、逗号序列、计算属性名……)。
//
// 关键不变量: parseExpression 返回后 curToken 停在表达式**末 token**上。空 yield
// 的末 token 就是 YIELD, 故 parseYieldExpression 提前 return 时不得消费终结符;
// 否则调用方 (数组字面量/实参表/括号/条件表达式) 用 peekToken 找终结符会集体
// 错位 (test262 expressions/yield/rhs-omitted.js、{generators,async-generator}/
// yield-spread-{arr-single,arr-multiple,obj}.js、class 与 object 方法同族)。
//
// ⚠ 另一条约束: 空 yield 不得进中缀循环 —— 它已是完整 AssignmentExpression,
// `yield` 换行后跟 `*` 必须报 SyntaxError (generators/yield-star-after-newline.js)。

// TestEmptyYieldInExpressionPosition 覆盖空 yield 可出现的各类表达式位置。
func TestEmptyYieldInExpressionPosition(t *testing.T) {
	valid := []string{
		// 数组 / 对象字面量元素
		`function* g(){ return [yield]; }`,
		`function* g(){ return [yield, 1]; }`,
		`function* g(){ return ({a: yield}); }`,
		// 展开位置
		`function* g(){ return [...yield]; }`,
		`function* g(){ return ({...yield}); }`,
		// 实参 (单参 / 带逗号)
		`function* g(){ return f(yield); }`,
		`function* g(){ return f(yield, 1); }`,
		// 括号
		`function* g(){ return (yield); }`,
		// 条件表达式两个分支
		`function* g(){ return x ? yield : 1; }`,
		`function* g(){ return x ? 1 : yield; }`,
		// 逗号序列 (yield, yield 是两条 assignment 级表达式)
		`function* g(){ yield, yield; }`,
		// 赋值右侧 / 一元·二元的操作数位置容器
		`function* g(){ var x = yield; }`,
		`function* g(){ var x = [yield]; }`,
		`function* g(){ return yield + 1; }`,
		`function* g(){ if (yield) ; }`,
		// 计算属性名
		`function* g(){ return { [yield]: 1 }; }`,
		// 解构默认值 / for-of 可迭代对象
		`function* g(){ var x; [ x = yield ] = []; }`,
		`function* g(){ var x; for (x of yield) ; }`,
		// 动态 import 实参
		`function* g(){ return import(yield); }`,
		// 生成器形参默认值里的箭头**体** (yield 属于外层生成器, 合法)
		`function* g(x = () => yield){}`,
		// 换行后的空 yield 走 ASI: `yield; +1;` 两条语句, 合法
		"function* g(){ yield\n+1; }",
	}
	for _, src := range valid {
		t.Run(src, func(t *testing.T) {
			if _, ok := parseSrc(t, src); !ok {
				t.Errorf("空 yield 该位置应合法, 但报了语法错误\nsrc: %s", src)
			}
		})
	}
}

// TestEmptyYieldDoesNotRegressNonEmptyYield 非空 / 委托 yield 形态一行未受影响
// (它们是本区域最容易被"顺手改坏"的邻居)。
func TestEmptyYieldDoesNotRegressNonEmptyYield(t *testing.T) {
	valid := []string{
		`function* g(){ yield; }`,
		`function* g(){ return yield; }`,
		`function* g(){ return yield 1; }`,
		`function* g(){ var x = yield; }`,
		`function* g(){ yield * g(); }`,
		// 委托: `yield *` 与右操作数之间**允许**换行 (无 [no LineTerminator here])
		"function* g(){ yield *\ng(); }",
		// 扩展/箭头体等
		`function* g(){ return [...yield 1]; }`,
	}
	for _, src := range valid {
		t.Run(src, func(t *testing.T) {
			if _, ok := parseSrc(t, src); !ok {
				t.Errorf("非空/委托 yield 形态应合法, 但报了语法错误\nsrc: %s", src)
			}
		})
	}
}

// TestEmptyYieldEarlyErrorsPreserved 空 yield 相关的早错**不得**因本次放宽而丢失。
// 这批正是历史上 dc15392/aeaf943 撤回「cur 停在 YIELD」版本时出 46 例回归的那块
// —— 形参区早错现已由 yieldReservedInParams 显式承接。
func TestEmptyYieldEarlyErrorsPreserved(t *testing.T) {
	invalid := []string{
		// 换行后单独的 `*` 无法起头语句 ⇒ SyntaxError
		// (test262 generators/yield-star-after-newline.js 一族)
		"function* g(){ yield\n* 1; }",
		// 生成器形参 [+Yield]: 形参名 / 默认值里出现 YieldExpression 皆早错
		`function* g(yield){}`,
		`function* g(x = yield){}`,
		`function* g(x = [yield]){}`,
		`function* g(x = f(yield)){}`,
		`function* g(x = a ? yield : b){}`,
		`function* g(x = class { [yield](){} }){}`,
		// 生成器体内箭头形参继承 [+Yield]
		`function* g(){ (x = yield) => {}; }`,
		// 严格模式非生成器形参默认值: yield 是保留字, 也不是 YieldExpression
		`"use strict"; function f(x = yield){}`,
		// 严格模式非生成器里裸 yield (dstr/*-yield{,-ident}-invalid.js, onlyStrict)
		`"use strict"; 0, [ x = yield ] = [];`,
		`"use strict"; 0, [ x[yield] ] = [];`,
		// class 体恒严格, 裸 yield 早错
		`class C { m(){ yield } }`,
		// 生成器体内 yield 作标签名早错
		`function* g(){ yield: 1; }`,
	}
	for _, src := range invalid {
		t.Run(src, func(t *testing.T) {
			if _, ok := parseSrc(t, src); ok {
				t.Errorf("该形态应报 SyntaxError, 但静默通过\nsrc: %s", src)
			}
		})
	}
}

// TestEmptyYieldSloppyIdentifierUnaffected 上一轮 (rmg9Qy) 修好的「sloppy 非生成器
// 里 yield 作普通标识符」绝不能回归。
func TestEmptyYieldSloppyIdentifierUnaffected(t *testing.T) {
	valid := []string{
		`var yield = 1; yield + 1;`,
		`var yield = 1; yield[0];`,
		`var yield = 1; yield(1);`,
		`function f() { var yield = 1; return yield; }`,
		`function* g() { function f(yield) { return yield; } }`,
	}
	for _, src := range valid {
		t.Run(src, func(t *testing.T) {
			if _, ok := parseSrc(t, src); !ok {
				t.Errorf("sloppy 下 yield 作标识符应合法, 但报了语法错误\nsrc: %s", src)
			}
		})
	}
}

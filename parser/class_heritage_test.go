package parser

import "testing"

// ===== ClassHeritage 早错 + 裸计算字段（rO13zU 剩余两项, 2026-10-05）=====
//
// 规范 ClassHeritage : extends LeftHandSideExpression。箭头函数是
// AssignmentExpression 而非 LeftHandSideExpression ⇒ 裸箭头 heritage 是解析期
// SyntaxError；整体被括号包裹的 (() => {}) 是合法的括号组形式，必须放行。
// 由于 AST 丢弃括号，只能在 heritage 起始 token 上做箭头探测（见
// class_grammar_early_errors.go 的 classHeritageStartsBareArrow）。
//
// 期望值全部经 Node 22 实测（test262 class-heritage-*-arrow-heritage 4 例为
// negative parse/SyntaxError；heritage-arrow-function.js 为带括号正例）。
func TestClassHeritageArrowEarlyError(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// ---- 裸箭头 heritage: 必须报解析错 ----
		{"声明 裸箭头", `class X extends () => {} {}`, true},
		{"声明 裸 async 箭头", `class X extends async () => {} {}`, true},
		{"声明 裸多参箭头", `class X extends (a, b) => {} {}`, true},
		{"声明 裸单参标识符箭头", `class X extends a => {} {}`, true},
		{"声明 裸 async 单参箭头", `class X extends async a => {} {}`, true},
		{"表达式 裸箭头", `(class extends () => {} {})`, true},
		{"表达式 裸 async 箭头", `(class extends async () => {} {})`, true},

		// ---- 带括号的箭头 heritage: 合法（不得误伤）----
		{"声明 带括号箭头", `class X extends (() => {}) {}`, false},
		{"声明 带括号 async 箭头", `class X extends (async () => {}) {}`, false},
		{"表达式 带括号箭头", `(class extends (() => {}) {})`, false},
		{"声明 带括号单参箭头", `class X extends (a => {}) {}`, false},
		{"声明 带括号条件式", `class X extends (a ? b : c) {}`, false},

		// ---- 普通 heritage: 合法 ----
		{"标识符 heritage", `class X extends Base {}`, false},
		{"带括号标识符 heritage", `class X extends (Base) {}`, false},
		{"成员访问 heritage", `class X extends a.b {}`, false},
		{"调用 heritage", `class X extends f() {}`, false},
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

// TestClassComputedBareField 覆盖计算属性名的裸字段形式 ClassElement :
// ClassElementName ;（无初始化器）。此前解析器无条件报
// "computed property name must be followed by '(' or '='"，把合法的
// class C { static ["prototype"]; } 判成非法（既有缺陷）。
//
// 终止仍受 ClassElementName 之后的 ASI 约束：同行直接跟下一个成员名是
// SyntaxError（与命名裸字段一致）。
func TestClassComputedBareField(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// ---- 合法 ----
		{"静态计算裸字段 prototype", `class C { static ["prototype"]; }`, false},
		{"实例计算裸字段", `class C { ["x"]; }`, false},
		{"实例计算裸字段 无分号", `class C { [x] }`, false},
		{"静态计算裸字段 换行", "class C {\n  static [x]\n  y\n}", false},
		{"计算裸字段 后接同行方法", `class C { [x]; m(){} }`, false},

		// ---- 仍应报错 ----
		{"计算裸字段 同行裸字段", `class C { [x] y }`, true},
		{"计算裸字段 同行方法名缺分隔", `class C { [x] method(){} }`, true},

		// ---- 既有形态回归 ----
		{"计算字段 初始化器", `class C { [x] = 1; }`, false},
		{"计算方法", `class C { [x](){} }`, false},
		{"静态命名裸字段 prototype 仍错", `class C { static prototype; }`, true},
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

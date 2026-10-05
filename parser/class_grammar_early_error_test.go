package parser

import "testing"

// ===== class 语法级早错（rpEXH2 剩余类别, 2026-10-05）=====
//
// 覆盖 parser/class_grammar_early_errors.go 的四类 + 字段 ASI 约束:
//
//  1. static 成员名不得为 "prototype"（计算属性名 / #私有名豁免）
//  2. 特殊方法名不得为 "constructor"（getter/setter/generator/async）
//  3. 同一 class 体只能有一个 constructor
//  4. HasDirectSuper: 非 constructor 方法 / static 方法体内 super() 非法;
//     constructor 仅在无 heritage 时非法（super.x 属性访问合法）
//  5. ClassElement : FieldDefinition 的 ASI 约束（同行的下一成员名非法）
//
// 全部期望值经 Node 22 实测对齐（见提交说明）。

func TestClassGrammarEarlyErrors(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// ---- 1. static 成员名 prototype: 必须报错 ----
		{"static 裸字段 prototype", `class C { static prototype; }`, true},
		{"static 字段 prototype 初始化", `class C { static prototype = 1; }`, true},
		{"static 方法 prototype", `class C { static prototype(){} }`, true},
		{"static getter prototype", `class C { static get prototype(){} }`, true},
		{"static setter prototype", `class C { static set prototype(v){} }`, true},
		{"static 生成器 prototype", `class C { static *prototype(){} }`, true},
		{"static async 方法 prototype", `class C { static async prototype(){} }`, true},
		{"static async 生成器 prototype", `class C { static async *prototype(){} }`, true},
		{"class 表达式同形", `const C = class { static prototype; };`, true},

		// ---- 1. static prototype: 合法 ----
		{"实例方法 prototype", `class C { prototype(){} }`, false},
		{"实例字段 prototype", `class C { prototype; }`, false},
		{"实例 getter prototype", `class C { get prototype(){} }`, false},
		{"static 计算方法 prototype", `class C { static ["prototype"](){} }`, false},
		{"static 私有名 #prototype", `class C { static #prototype; }`, false},

		// ---- 2. 特殊方法名 constructor: 必须报错 ----
		{"getter constructor", `class C { get constructor(){} }`, true},
		{"setter constructor", `class C { set constructor(v){} }`, true},
		{"生成器 constructor", `class C { *constructor(){} }`, true},
		{"async constructor", `class C { async constructor(){} }`, true},
		{"async 生成器 constructor", `class C { async *constructor(){} }`, true},

		// ---- 2. constructor: 合法 ----
		{"普通 constructor", `class C { constructor(){} }`, false},
		{"static 方法 constructor", `class C { static constructor(){} }`, false},
		{"static getter constructor", `class C { static get constructor(){} }`, false},
		{"计算属性 constructor", `class C { ["constructor"](){} }`, false},

		// ---- 3. 重复 constructor: 必须报错 ----
		{"两个 constructor", `class C { constructor(){} constructor(){} }`, true},
		{"constructor 与计算属性 constructor", `class C { constructor(){} ["constructor"](){} }`, false},

		// ---- 4. HasDirectSuper: 必须报错 ----
		{"普通方法 super()", `class C extends Object { m(){ super(); } }`, true},
		{"静态方法 super()", `class C extends Object { static m(){ super(); } }`, true},
		{"静态私有方法 super()", `class C extends Object { static #m(){ super(); } }`, true},
		{"静态生成器 super()", `class C extends Object { static *m(){ super(); } }`, true},
		{"无 heritage 的 constructor super()", `class C { constructor(){ super(); } }`, true},
		{"箭头函数内 super() 仍算", `class C extends Object { m(){ (() => super())(); } }`, true},

		// ---- 4. HasDirectSuper: 合法 ----
		{"有 heritage 的 constructor super()", `class C extends Object { constructor(){ super(); } }`, false},
		{"方法内 super.x 属性访问", `class C extends Object { m(){ super.toString(); } }`, false},
		{"静态方法内 super.x 属性访问", `class C extends Object { static m(){ super.x; } }`, false},

		// ---- 5. 字段 ASI 约束: 必须报错 ----
		{"带初始化器后同行方法", `class C { x = 1 method(){} }`, true},
		{"裸字段后同行标识符", `class C { x y }`, true},
		{"静态裸字段后同行标识符", `class C { static x y }`, true},
		{"私有名同行两个", `class C { #x #y }`, true},
		{"get # 空格私有名", `class C { get # m(){} }`, true},
		{"set # 空格私有名", `class C { set # m(v){} }`, true},
		{"static get # 空格", `class C { static get # m(){} }`, true},

		// ---- 5. 字段 ASI 约束: 合法 ----
		{"裸字段后同行右花括号", `class C { x }`, false},
		{"字段分号后同行字段", `class C { x; y }`, false},
		{"初始化器换行后方法", "class C {\n  x = 1\n  method(){}\n}", false},
		{"裸字段换行后裸字段", "class C {\n  x\n  y\n}", false},
		{"私有名换行后私有名", "class C {\n  #x\n  #y\n}", false},
		{"分号后同行方法", `class C { x = 1; method(){} }`, false},

		// ---- hashbang 不在源码开头: 必须报错 ----
		{"语句块内 hashbang", "{\n  #!\n}", true},
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

package parser

import "testing"

// ===== class 私有名三类早错（rpEXH2 其余三类, 2026-10-05）=====
//
// 1. 重复私有名（static/实例、字段/方法/getter/setter 间不得重名;
//    唯一例外: getter+setter 恰好一对）
// 2. 实例/静态字段初始化器含 arguments 或 super(...) 调用
// 3. 引用未在任何包围类声明的私有名（含「类外引用」与「嵌套类引用外层
//    未声明名」两种; 嵌套类引用外层**已声明**名是合法的 —— 私有名环境
//    沿 class 嵌套链向外解析）
//
// 每条期望值都与 Node 实测口径对齐。

func TestClassPrivateEarlyErrors(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// ---- 重复私有名: 必须报错 ----
		{"两个同名字段", `class A { #x; #x; }`, true},
		{"字段+同名方法", `class A { #x; #x(){} }`, true},
		{"字段+同名 getter", `class A { #x; get #x(){} }`, true},
		{"实例+静态同名", `class A { #x; static #x; }`, true},
		{"两个同名 getter", `class A { get #x(){} get #x(){} }`, true},
		{"两个同名 setter", `class A { set #x(v){} set #x(v){} }`, true},
		{"静态字段+同名静态方法", `class A { static #x = 1; static #x(){} }`, true},

		// ---- 重复私有名: 合法 ----
		{"getter+setter 同名成对", `class A { get #x(){} set #x(v){} }`, false},
		{"setter 先 getter 后", `class A { set #x(v){} get #x(){} }`, false},
		{"不同名互不影响", `class A { #x; #y; }`, false},
		{"类表达式同形", `const A = class { #x; #x; };`, true},

		// ---- 字段初始化器: 必须报错 ----
		{"实例字段含 arguments", `class A { #x = arguments; }`, true},
		{"公有字段含 arguments", `class A { x = arguments; }`, true},
		{"箭头函数体内 arguments 仍算", `class A { x = (() => arguments)(); }`, true},
		{"静态字段含 arguments", `class A { static x = arguments; }`, true},
		{"嵌套函数体内 arguments 不算", `class A { x = function(){ return arguments; }; }`, false},

		// ---- 未声明私有名引用: 必须报错 ----
		{"方法体引用未声明名", `class A { m(){ return this.#y; } }`, true},
		{"getter 体引用未声明名", `class A { get g(){ return this.#y; } }`, true},
		{"字段初始化器引用未声明名", `class A { #x = this.#y; }`, true},
		{"静态字段引用未声明名", `class A { static y = this.#z; }`, true},
		{"顶层引用私有名", `this.#x;`, true},
		{"普通函数内引用私有名", `function f(o){ return o.#x; }`, true},
		{"嵌套类引用外层未声明名", `class A { m(){ class B { n(){ return this.#nope; } } } }`, true},

		// ---- 未声明私有名引用: 合法 ----
		{"方法体引用本类声明", `class A { #x; m(){ return this.#x; } }`, false},
		{"引用可在使用之后声明", `class A { m(){ return this.#x; } #x; }`, false},
		{"嵌套类引用外层已声明名", `class A { #x; m(){ class B { n(){ return this.#x; } } return B; } }`, false},
		{"嵌套类自己的声明", `class A { m(){ class B { #y; n(){ return this.#y; } } return B; } }`, false},
		{"类表达式内引用本类声明", `const A = class { #x; m(){ return this.#x; } };`, false},
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

package parser

import "testing"

// ===== 形参早错 (rGXXZ6 / r9JAuo, 2026-10-06) =====
//
// 覆盖三类此前漏拦的形参语法早错, 期望值均与 Node 22 一致:
//  1. rest 形参带初始化器        function f(...x = []) {}
//  2. 非简单形参列表的重复绑定名  function f(x = 0, x) {}  (sloppy 也报)
//  3. 对象模式 shorthand 用保留字 ({ default } / { extends }) => {}

func TestRestParamInitializer(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		{"rest 带数组默认值", `function f(...x = []) {}`, true},
		{"rest 带标量默认值", `function f(...x = 1) {}`, true},
		{"rest 解构带默认值", `function f(...[a] = []) {}`, true},
		{"箭头 rest 带默认值", `(...x = []) => {}`, true},
		{"async rest 带默认值", `async function f(...x = []) {}`, true},
		{"方法 rest 带默认值", `({ m(...x = []) {} });`, true},
		// 合法: rest 本身, 以及 rest + 解构 (不带默认值)
		{"rest 裸标识符", `function f(...x) {}`, false},
		{"rest 解构模式", `function f(...[a, b]) {}`, false},
		{"普通参数带默认值", `function f(a, b = 1) {}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := parseSrc(t, tc.src)
			if ok == tc.wantErr {
				t.Errorf("wantErr=%v 但 ok=%v\nsrc: %s", tc.wantErr, ok, tc.src)
			}
		})
	}
}

func TestNonSimpleDuplicateParams(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// 非简单列表: 重复名必须报错 (与 strict 无关)
		{"默认值 + 重名", `function f(x = 0, x) {}`, true},
		{"重名 + 默认值", `function f(x, x = 0) {}`, true},
		{"解构 + 重名", `function f([x], x) {}`, true},
		{"对象解构 + 重名", `function f({x}, x) {}`, true},
		{"对象简写 + 重名", `function f({x = 1}, x) {}`, true},
		{"rest 使列表非简单", `function f(x, x, ...r) {}`, true},
		{"箭头 默认值 + 重名", `(x = 0, x) => {}`, true},
		{"async 默认值 + 重名", `async function f(x = 0, x) {}`, true},
		{"生成器 默认值 + 重名", `function* f(x = 0, x) {}`, true},
		// 简单列表: sloppy 下重复名合法
		{"简单列表重复名", `function f(a, a) { return a; }`, false},
		// 非简单但无重复
		{"非简单无重复", `function f(x = 0, y) {}`, false},
		{"解构无重复", `function f([a, b], c = 1) {}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := parseSrc(t, tc.src)
			if ok == tc.wantErr {
				t.Errorf("wantErr=%v 但 ok=%v\nsrc: %s", tc.wantErr, ok, tc.src)
			}
		})
	}
}

func TestUniqueParamNamesForArrow(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// 箭头形参 = UniqueFormalParameters: 重复名恒报错
		{"箭头简单重复名", `0, (a, a) => {};`, true},
		{"async 箭头重复名", `0, async (a, a) => {};`, true},
		{"单参箭头去括号不适用", `var g = a => a;`, false},
		{"箭头无重复", `var g = (a, b) => a + b;`, false},
		// 普通函数在 sloppy 简单列表下重复名合法 (对照)
		{"普通函数 sloppy 简单重复", `function f(a, a) { return a; }`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := parseSrc(t, tc.src)
			if ok == tc.wantErr {
				t.Errorf("wantErr=%v 但 ok=%v\nsrc: %s", tc.wantErr, ok, tc.src)
			}
		})
	}
}

func TestUseStrictWithNonSimpleParams(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// 非简单形参 + 体含 "use strict" 指令 ⇒ SyntaxError (14.1.2)
		{"rest + use strict", `function f(a, ...r) { "use strict"; }`, true},
		{"默认值 + use strict", `function f(a = 1) { "use strict"; }`, true},
		{"解构 + use strict", `function f({a}) { "use strict"; }`, true},
		{"数组解构 + use strict", `function f([a]) { "use strict"; }`, true},
		{"箭头 rest + use strict", `var g = (...r) => { "use strict"; };`, true},
		{"async 默认值 + use strict", `async function f(a = 1) { "use strict"; }`, true},
		{"生成器 rest + use strict", `function* f(...r) { "use strict"; }`, true},
		{"对象方法 rest + use strict", `({ m(...r) { "use strict"; } });`, true},
		{"对象访问器非简单 + use strict", `({ set s(a = 1) { "use strict"; } });`, true},
		// 合法: 简单列表 + use strict; 非简单列表无指令
		{"简单列表 + use strict", `function f(a) { "use strict"; }`, false},
		{"无参 + use strict", `function f() { "use strict"; }`, false},
		{"非简单列表无指令", `function f(a = 1) { return a; }`, false},
		{"非简单列表继承 strict (无自身指令)", `"use strict"; function f(a = 1) { return a; }`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := parseSrc(t, tc.src)
			if ok == tc.wantErr {
				t.Errorf("wantErr=%v 但 ok=%v\nsrc: %s", tc.wantErr, ok, tc.src)
			}
		})
	}
}

func TestShorthandReservedWord(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// shorthand 键同时是绑定/引用名 → 保留字非法
		{"对象模式 default", `function f({default}) {}`, true},
		{"对象模式 extends", `function f({extends}) {}`, true},
		{"对象模式 if", `function f({if}) {}`, true},
		{"箭头对象模式 default", `var g = ({default}) => {};`, true},
		{"箭头对象模式 extends", `var g = ({extends}) => {};`, true},
		{"赋值模式 default", `({default} = x);`, true},
		{"赋值模式 extends", `({extends} = x);`, true},
		{"带默认值 shorthand default", `function f({default = 1}) {}`, true},
		// 合法: 冒号形式 (键只是 PropertyName), 以及非保留字 shorthand
		{"冒号形式 default", `var o = {default: 1};`, false},
		{"解构冒号形式 default", `function f({default: d}) { return d; }`, false},
		{"普通 shorthand", `function f({a, b}) { return a + b; }`, false},
		{"解构冒号形式 extends", `var {extends: e} = obj;`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := parseSrc(t, tc.src)
			if ok == tc.wantErr {
				t.Errorf("wantErr=%v 但 ok=%v\nsrc: %s", tc.wantErr, ok, tc.src)
			}
		})
	}
}

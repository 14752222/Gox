package parser

import "testing"

// ===== rest 元素必须是最后一项的早错（rpEXH2 的一类, 2026-10-05）=====
//
// 规范: ArrayBindingPattern / FormalParameters / ArrayAssignmentPattern 里
// rest 之后不得再有任何元素, **连尾逗号也不行**。此前解析器完全不拦,
// `[...a, b]` 一路放到运行期 —— 189 条 test262 用例 (dstr/*rest-not-final*)
// 因此全挂在 phase:parse 上。
//
// 每条期望值都与 Node 实测一致 (F:/tmp/mprobe/restchk*.js)。

func TestRestMustBeLast(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr bool
	}{
		// ---- 绑定模式: 必须报错 ----
		{"绑定模式 rest 后还有标识符", `let [...a, b] = x;`, true},
		{"绑定模式 rest 后跟尾逗号", `let [...a,] = x;`, true},
		{"嵌套数组模式 rest 非末项", `let [...[a], b] = x;`, true},
		{"嵌套对象模式 rest 非末项", `let [...{x}, y] = x;`, true},
		{"形参里的数组模式 rest 非末项", `function f([...a, b]){}`, true},
		{"var 形状", `var [...a, b] = x;`, true},
		{"const 形状", `const [...a, b] = x;`, true},

		// ---- 绑定模式: 合法, 不得报错 ----
		{"rest 是唯一元素", `let [...a] = x;`, false},
		{"rest 在末项", `let [a, ...b] = x;`, false},
		{"内层模式 rest 在自己的末项", `let [a, [b, ...c]] = x;`, false},
		// 注: `let [a, {b, ...c}] = x` (内层**对象**模式的 rest) 不在此列 ——
		// 对象 rest 绑定模式整体未实现 (parseObjectPattern 不收 SPREAD_REST),
		// 是与本项目无关的独立缺口, 不属于「rest 位置」早错的范畴。
		{"rest 是内层模式且外层还有元素", `function f([x, ...y], z){}`, false},

		// ---- 形参表: 必须报错 ----
		{"rest 参数后还有参数", `function f(...a, b){}`, true},
		{"rest 参数后跟尾逗号", `function f(...a,){}`, true},
		{"中间 rest", `function f(a, ...b, c){}`, true},
		{"生成器 rest 非末项", `function* g(...a, b){}`, true},
		{"类方法 rest 非末项", `class C{m(...a, b){}}`, true},
		{"箭头函数 rest 非末项", `([...a, b]) => {}`, true},
		{"async 箭头函数 rest 非末项", `async (...a, b) => {}`, true},

		// ---- 形参表: 合法 ----
		{"rest 参数在末项", `function f(...a){}`, false},
		{"rest 参数前有普通参数", `function f(a, ...b){}`, false},

		// ---- 赋值模式: 必须报错 ----
		{"赋值模式 rest 后还有元素", `[a, ...b, c] = x;`, true},
		{"赋值模式 rest 后跟尾逗号", `[...b,] = x;`, true},
		{"赋值模式末项 rest 后跟尾逗号", `[a, ...b,] = x;`, true},

		// ---- 赋值模式 / 数组字面量: 合法 ----
		{"赋值模式 rest 在末项", `[a, ...b] = x;`, false},
		{"数组字面量 spread 位置随意", `let arr = [1, ...b, 3];`, false},
		{"数组字面量尾逗号 + spread", `let arr2 = [1, ...b,];`, false},
		{"数组字面量尾逗号无 rest", `let arr3 = [1, 2,];`, false},
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

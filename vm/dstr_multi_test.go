package vm

import "testing"

// TestMultiDeclaratorDestructuring (rWLQcV) —— 一条声明里多个声明项、含解构模式
// 的端到端语义。
//
// 回归背景: parser 只在**首项**处理解构模式, 逗号续接项只接受普通标识符, 于是
// `var [p] = [1], [q] = [2];` / `const [a, b] = [1, 2], {c} = {c: 3};` 这类
// 合法写法直接 SyntaxError。修复后首项/续接项都走同一条脱糖路径, 且编译器在
// 首项是解构时也会继续编译 More。
//
// 每条用例都与 Node v22 实测对齐 (见探针 probe_pos.js)。
func TestMultiDeclaratorDestructuring(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		// 首项解构 + 续接项解构
		{"var-arr-arr", `var [p] = [1], [q] = [2]; p + "," + q`, "1,2"},
		{"var-obj-obj", `var {a} = ({a: 1}), {b} = ({b: 2}); a + "," + b`, "1,2"},
		{"var-three", `var [p] = [1], [q] = [2], [r] = [3]; p + q + r`, "6"},
		{"let-arr-arr", `let [p] = [1], [q] = [2]; p + "," + q`, "1,2"},
		{"let-mixed", `let {x} = {x: 1}, [y] = [2], z = 3; x + "," + y + "," + z`, "1,2,3"},
		{"const-arr-obj", `const [a, b] = [1, 2], {c} = {c: 3}; a + b + c`, "6"},
		{"const-obj-obj", `const {a} = {a: 1}, {b} = {b: 2}; a + b`, "3"},

		// 首项普通 + 续接项解构
		{"var-plain-arr", `var p = 1, [q] = [2]; p + "," + q`, "1,2"},
		{"var-plain-obj", `var p = 1, {a} = ({a: 2}); p + "," + a`, "1,2"},
		{"var-plain-destr-plain", `var a = 1, [b] = [2], c = 3; a + b + c`, "6"},

		// 首项解构 + 续接项普通
		{"var-arr-plain", `var [p] = [1], q = 2; p + "," + q`, "1,2"},
		{"var-arr-plain-plain", `var [p] = [1], q = 2, r = 3; p + q + r`, "6"},

		// 默认值 / 嵌套 / rest
		{"defaults", `var [p = 9] = [], q = 2; p + "," + q`, "9,2"},
		{"defaults-2", `var [p] = [1], [q = 9] = []; p + "," + q`, "1,9"},
		{"const-default", `const {a = 1} = {}, [b = 2] = []; a + "," + b`, "1,2"},
		{"nested", `var [[a]] = [[1]], [b] = [2]; a + "," + b`, "1,2"},
		{"nested-mixed", `var [{a}] = [{a: 1}], {b} = {b: 2}; a + "," + b`, "1,2"},
		{"rename", `var {a: x} = {a: 1}, [y] = [2]; x + "," + y`, "1,2"},
		{"rest", `var [...r] = [1, 2], [q] = [3]; r.join("-") + "," + q`, "1-2,3"},

		// for 头部 (init 段是多声明项)
		{"for-var", `var out = []; for (var [a] = [1], [b] = [2]; out.length < 1;) { out = [a, b]; break; } out.join(",")`, "1,2"},
		{"for-let", `var out = []; for (let [a] = [1], [b] = [2]; out.length < 1;) { out = [a, b]; break; } out.join(",")`, "1,2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, err := Eval(tc.src)
			if err != nil {
				t.Fatalf("%s: %v", tc.src, err)
			}
			if v == nil || v.Inspect() != tc.want {
				t.Fatalf("%s: got %v, want %s", tc.src, v, tc.want)
			}
		})
	}
}

// 负例: 解构声明项缺初始化器 / 重复绑定仍须报错。
func TestMultiDeclaratorDestructuringErrors(t *testing.T) {
	bad := []string{
		`var [p];`,
		`var p = 1, [q];`,
		`let [p], [q];`,
		`const [a] = [1], b;`,
		`let [a] = [1], a = 2;`,
	}
	for _, src := range bad {
		if _, err := Eval(src); err == nil {
			t.Fatalf("%s: 应当报错却通过了", src)
		}
	}
}

package vm

import "testing"

// ===== 形参默认值求值时机 / TDZ (r9JAuo, 2026-10-06) =====
//
// 规范 9.2.12 FunctionDeclarationInstantiation: 非简单形参列表的形参按序
// 初始化; 求值第 i 个形参的默认值时, 第 i..n-1 个形参名仍处于 TDZ, 引用
// 它们 (含自身) 必须抛 ReferenceError, 且**不得**回退读外层/全局同名绑定。
// 断言统一走「IIFE 里 try/catch 返回 e.name 或字面结果」, 与错误类型解耦。
func TestParamDefaultTDZ(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			"引用自身",
			`(function(){ try { (function(x = x) { return x; })(); return "ok"; } catch(e){ return e.name; } })()`,
			"ReferenceError",
		},
		{
			"引用后位形参",
			`(function(){ try { (function(x = y, y) { return [x,y]; })(undefined, 5); return "ok"; } catch(e){ return e.name; } })()`,
			"ReferenceError",
		},
		{
			"后位形参名与外层全局同名时仍抛 (不回退全局)",
			`(function(){ var y = 99; try { (function(x = y, y) { return x; })(undefined, 5); return "ok"; } catch(e){ return e.name; } })()`,
			"ReferenceError",
		},
		{
			"引用前位形参合法",
			`String((function(y, x = y) { return x; })(7))`,
			"7",
		},
		{
			"前位形参默认值可见",
			`String((function(x = 41, y = x + 1) { return y; })())`,
			"42",
		},
		{
			"实参就位时不求值默认",
			`String((function(x = y, y) { return x; })(1, 2))`,
			"1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := testEvalCatch(t, tc.src)
			if err != nil {
				t.Fatalf("vm error: %v", err)
			}
			if got := res.Inspect(); got != tc.want {
				t.Errorf("got %q, want %q\nsrc: %s", got, tc.want, tc.src)
			}
		})
	}
}

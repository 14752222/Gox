package vm

import "testing"

// TestCompileErrorInsideTryDoesNotPanic 是「编译器在 try 体内遇编译错时 panic」
// 的回归守卫 (2026-10-05 修, rCzckg 探针在工作区发现)。
//
// 背景 (rMkA8D 引入的回归):
//
//	rMkA8D 让 compileClassConstructor / compileFunctionSelf /
//	compileAsyncFunctionSelf 进入时把 c.tryScopes 清空 (函数边界不能跨),
//	但**错误早退路径**没有恢复它; 而 compileTryStatement 在检查
//	bodyErr 之前就执行 c.tryScopes[:len(c.tryScopes)-1], len==0 时
//	panic: slice bounds out of range [:-1], 整个编译进程 abort。
//
// 触发形状: try 体里出现一个「编译期就报错」的类/函数成员 ——
// `try { class C { constructor() { super(); } } } catch (e) {}` 即 100% 复现
// (旧二进制 panic, 新二进制返回 compiler error)。
//
// 修法: 三处保存点改用 defer 恢复 (所有退出路径都还原), 且两处 pop 用
// 保存的长度做上界判断, 即便状态被污染也不会越界。
func TestCompileErrorInsideTryDoesNotPanic(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"try_catch+class_bad_super", `try { class C { constructor() { super(); } } } catch (e) {}`},
		{"try_finally+class_bad_super", `try { class C { constructor() { super(); } } } finally {}`},
		{"try_catch+function_bad_super", `try { function f() { super(); } } catch (e) {}`},
		{"nested_try+class_bad_super", `function g() { try { class C { constructor() { super(); } } } catch (e) {} } g();`},
		{"try_inside_class_method", `class Z { m() { try { class C { constructor() { super(); } } } catch (e) {} } } new Z().m();`},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("编译 panic (应返回错误而非崩溃进程): %v", r)
				}
			}()
			if _, err := EvalVM(tc.src); err == nil {
				t.Fatalf("期望编译错误, 实际 err == nil")
			}
		})
	}
}

// TestClassInTryThenReturnThroughFinally 是「正常路径不回归」守卫:
// try 体内声明类 + return 穿 finally, finally 必须执行, 返回值必须正确。
// defer 恢复若不生效, 这里的 finally 内联会错乱 (rMkA8D 之前该形状
// 干脆完全不跑 finally, 见 docs/v1-roadmap §十一)。
func TestClassInTryThenReturnThroughFinally(t *testing.T) {
	got := evalOut(t, `
		function f() {
			try {
				class C { constructor() { this.x = 1 } }
				return "r" + new C().x;
			} finally {
				__out.push("fin");
			}
		}
		__out.push(f());
	`)
	if got != "fin\nr1\n" {
		t.Fatalf("finally 未按预期执行, got=%q want=%q", got, "fin\nr1\n")
	}
}

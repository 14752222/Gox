package vm

// ===== ++/-- 读未声明名: 运行期 ReferenceError (可被 try/catch 捕获) =====
//
// 看板 r63RpV 子项 2。规范: GetValue 对未解析引用抛 ReferenceError 是
// **运行期** 错误 —— 必须可被 try/catch 捕获, 而不是编译期直接失败
// (编译期错不可捕获; 且会误伤名字存在时 (sloppy 隐式全局 / 宿主注入)
// 的读-改-写路径)。
//
// 实现: compileIncDec 对未声明名不再 return error, 改走
// emitGlobalLoad (OP_LOAD_GLOBAL) + 运行期解析 (f1a46fb 内 1181ce2)。
// 同类: arguments 裸读/自增走同一条 OP_LOAD_GLOBAL 路径。

import (
	"testing"
)

// TestIncDecUndeclaredThrowsCatchableRuntimeError x++/--x 读未声明名是
// 运行期 ReferenceError, try/catch 能抓住, 且捕获后执行继续。
func TestIncDecUndeclaredThrowsCatchableRuntimeError(t *testing.T) {
	res := evalJS(t, `try { x++ } catch (e) { e.name + "|" + e.message }`)
	assertString(t, res, "ReferenceError|x is not defined")

	res = evalJS(t, `try { --y } catch (e) { e.name + "|" + e.message }`)
	assertString(t, res, "ReferenceError|y is not defined")

	// 捕获后语句继续执行 (编译期错会整体失败, 到不了这里)。
	res = evalJS(t, `var log = ""; try { x++ } catch (e) { log = "caught"; } log + "|done";`)
	assertString(t, res, "caught|done")
}

// TestArgumentsUndeclaredThrowsRuntimeError 非函数体内裸 arguments 引用/
// 自增: 运行期 ReferenceError (同类 x++, 同一 OP_LOAD_GLOBAL 路径)。
func TestArgumentsUndeclaredThrowsRuntimeError(t *testing.T) {
	res := evalJS(t, `try { arguments } catch (e) { e.name + "|" + e.message }`)
	assertString(t, res, "ReferenceError|arguments is not defined")

	res = evalJS(t, `try { arguments++ } catch (e) { e.name + "|" + e.message }`)
	assertString(t, res, "ReferenceError|arguments is not defined")
}

// TestIncDecResolvesGlobalAtRuntime 运行期解析路径对**存在**的名字照常
// 工作: sloppy 赋值隐式建的全局可被 ++ 读到并写回 (宿主注入全局同理)。
// 这是子项 2 的另一半验收 —— 不能把"未声明"一刀切成编译期错。
func TestIncDecResolvesGlobalAtRuntime(t *testing.T) {
	res := evalJS(t, `q = 1; q++; q++; q;`)
	assertNumber(t, res, 3)

	// 已存在的 sloppy 隐式全局走运行期路径, 不抛错 (自包含单条程序)。
	res = evalJS(t, `q = 1; var r = 0; try { q++ } catch (e) { r = 1 } q === 2 && r === 0;`)
	assertBoolean(t, res, true)
}

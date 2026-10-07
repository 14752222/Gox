package vm

import (
	"path/filepath"
	"strings"
	"testing"
)

// 顶层**未捕获的抛出值**渲染: 必须走 JS 的 ToString 语义 (用户对象调其自定义
// toString), 保住构造器类型名 —— 不能落回对象字面量的 Inspect。
//
// 起因 (batch7 LOST): test262 的
//   language/module-code/eval-self-abrupt.js               (throw new Test262Error())
//   language/module-code/eval-export-dflt-expr-err-eval.js
// 期望 runtime 抛 Test262Error, 而**模块入口**错误路径
// (EvalModuleFileVMWithGlobals) 直接 fmt.Errorf("vm error: %v", AttachFrame(err)),
// 落到 ThrowError.Error() 的 Inspect —— `{ message: "" }`, 类型名全丢。
// runner 的 negative.type 只看首行, 于是判 LOST。
//
// 修法: 模块入口改走与 script 入口一致的 vm.uncaughtError (ToString 语义)。
func TestModuleEntryUncaughtThrowRendersViaToString(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"entry.js": "function MyError(m){ this.message = m || \"\"; }\n" +
			"MyError.prototype.toString = function(){ return \"MyError: \" + this.message; };\n" +
			"throw new MyError();\n",
	})
	_, err := EvalModuleFileVM(filepath.Join(dir, "entry.js"))
	if err == nil {
		t.Fatal("期望抛出 MyError, 实得成功")
	}
	head := errFirstLine(err.Error())
	// 关键断言: 首行含构造器类型名 (renderer 走了 toString)。
	if !strings.Contains(head, "MyError") {
		t.Fatalf("首行未含构造器名 MyError, 实得: %q\n完整: %s", head, err.Error())
	}
	// 不得退化成对象字面量 Inspect。
	if strings.Contains(head, "{ message:") {
		t.Fatalf("首行退化成 Inspect 形状, 实得: %q", head)
	}
}

// 同一渲染口径下, script 入口 (EvalVM) 与模块入口对**同一抛出值**给出一致的
// 展示 —— 两个入口不再各自为政。
func TestUncaughtThrowRenderingConsistentAcrossEntries(t *testing.T) {
	src := "function E2(){ this.message = \"\"; }\n" +
		"E2.prototype.toString = function(){ return \"E2: \"; };\n" +
		"throw new E2();\n"

	_, serr := EvalVM(src)
	if serr == nil {
		t.Fatal("EvalVM 期望抛出")
	}

	dir := writeModuleDir(t, map[string]string{"entry.js": src})
	_, merr := EvalModuleFileVM(filepath.Join(dir, "entry.js"))
	if merr == nil {
		t.Fatal("EvalModuleFileVM 期望抛出")
	}

	sh, mh := errFirstLine(serr.Error()), errFirstLine(merr.Error())
	if sh != mh {
		t.Fatalf("两入口首行不一致:\n  script=%q\n  module=%q", sh, mh)
	}
	if !strings.Contains(mh, "E2") {
		t.Fatalf("首行未含构造器名 E2, 实得: %q", mh)
	}
}

// 内建 Error 实例维持既有 "Name: message" 渲染 (不因本次改动被 toString
// 语义带偏: Error 实例走 *object.Error 快路径)。
func TestUncaughtBuiltinErrorRenderingUnchanged(t *testing.T) {
	_, err := EvalVM(`throw new TypeError("boom");`)
	if err == nil {
		t.Fatal("期望抛出 TypeError")
	}
	head := errFirstLine(err.Error())
	if !strings.Contains(head, "TypeError: boom") {
		t.Fatalf("内建 Error 渲染变了, 实得: %q", head)
	}
}

// errFirstLine 取错误消息首行 (去首尾空白) —— 与 cmd/gox 的 errorHead 同口径,
// 也是 test262 negative.type 的判据。
func errFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

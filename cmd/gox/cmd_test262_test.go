package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestErrorHead 钉住 negative.type 匹配只取首行 —— 错误消息里的「源码片段 +
// 插入符」不参与类型匹配（否则回显的源码行含类型名就会误判通过）。
func TestErrorHead(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"vm error: TypeError: boom", "vm error: TypeError: boom"},
		{
			"vm error: { message: \"\" }\n    --> f.js:4:16\n  |\n 4 | if (x === 0) { throw new Test262Error(); }\n  |                ^",
			`vm error: { message: "" }`,
		},
		{
			// 修复后的真实形状 (渲染走 ToString, 类型名进首行), 首行含 Test262Error。
			"vm error: Test262Error: \n    --> f.js:4:16\n  |\n 4 | if (x === 0) { throw new Test262Error(); }\n  |                ^",
			"vm error: Test262Error:",
		},
		{"", ""},
		{"  leading trimmed  ", "leading trimmed"},
	}
	for _, tc := range tests {
		if got := errorHead(tc.in); got != tc.want {
			t.Errorf("errorHead(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestJudgePhaseNegTypeUsesHeadOnly 是 r23xdR 的核心回归：
// 期望 type: Test262Error，但引擎消息首行不含该类型名（类型名只出现在
// **被回显的源码行**里）时必须判失败；首行含类型名时才判通过。
//
// 注：早先这个用例拿 `vm error: { message: "" }` 当"首行无类型名"的样本 ——
// 那是渲染走 Inspect 丢掉构造器名的 bug 产物（已修，见 vm.uncaughtError /
// EvalModuleFileVMWithGlobals）。判据本身（只看首行、不吃源码回显）与
// 被渲染成什么无关，故改用等价形状继续钉住判据。
func TestJudgePhaseNegTypeUsesHeadOnly(t *testing.T) {
	c := &test262Case{Negative: "runtime", NegType: "Test262Error"}

	// 侥幸形状：首行无 Test262Error，源码回显行有。
	echoed := errors.New("vm error: some other error\n" +
		"    --> f.js:4:16\n" +
		"  |\n" +
		" 4 | if (x === 0) { throw new Test262Error(); }\n" +
		"  |                ^")
	if ok, _, _ := judgePhase(c, echoed); ok {
		t.Fatalf("源码回显含类型名不应判通过（遮羞布）: %v", echoed)
	}

	// 真形状：引擎消息首行就带类型名。
	real := errors.New("vm error: Test262Error: nope\n    --> f.js:2:1\n  |\n 2 | x;\n  | ^")
	if ok, phase, msg := judgePhase(c, real); !ok {
		t.Fatalf("首行含 Test262Error 应判通过, 实得 phase=%s msg=%s", phase, msg)
	}

	// 阶段不符仍要失败：期望 runtime，实际 parse。
	if ok, _, _ := judgePhase(c, errors.New("parser errors:\nboom")); ok {
		t.Fatalf("phase 不符不应判通过")
	}
}

// ===== CLI 启动路径回归 (rMWHi1) =====

// test262Root 找 test262 仓库: 优先 $GOX_TEST262, 否则按常见位置猜。
//
// 找不到就 skip —— 这条断言要的是"套件在的时候 CLI 必须能跑完", 套件不在时它
// 无从断言 (CI 上没克隆 test262 的环境不该因此红)。
func test262Root(t *testing.T) string {
	t.Helper()
	if v := os.Getenv("GOX_TEST262"); v != "" {
		return v
	}
	for _, c := range []string{"test262", "../test262", "../../test262", "../../../test262"} {
		if dirExists(filepath.Join(c, "test")) && dirExists(filepath.Join(c, "harness")) {
			return c
		}
	}
	t.Skip("未找到 test262 仓库: 设 GOX_TEST262 指向它 (或把 test262 克隆到仓库根)")
	return ""
}

// captureStdout 跑 fn 并接住它打到 os.Stdout 的东西 (用例结果就是打在那里的)。
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	saved := os.Stdout
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()
	fn()
	os.Stdout = saved
	w.Close()
	out := <-done
	r.Close()
	return out
}

// TestTest262CLIStartsWithoutGUIHost 是 rMWHi1 (win32 网卡枚举 SIGSEGV) 的 CLI 侧
// 回归。
//
// 事故形态值得记住: 崩发生在 win32 后端的**包 init** 里, 所以 `gox test262` 这种
// 一行 GUI 代码都不碰的路径也是 100% 启动即崩 —— "用不用 GUI"根本不影响它, 只
// 要二进制链进了那个包。修复后枚举是懒加载的 (host.go 的 reportNetworkLazy),
// 且"init 里不许有平台 IO"由 tools/initpurity 的 CI 闸门守住。
//
// 这条测试跑在 Linux 上, 而 Linux 根本不编译 win32 后端, 所以它钉住的其实是另一
// 半性质: **CLI 路径不依赖 GUI 后端** —— 哪天有人把宿主/后端初始化挪进 CLI 的
// 公共路径 (在 Windows 上才会红的那种错误), 它会在 Linux 上先红一次。
func TestTest262CLIStartsWithoutGUIHost(t *testing.T) {
	root := test262Root(t)
	// -one 模式在**本进程**内直跑单个用例, 不 spawn 分片子进程 —— 断言的就是
	// "这条 CLI 路径自己能不能起来" (分片子进程走 os.Executable(), 在 go test 里
	// 那是测试二进制, 不是 gox)。
	const one = "language/expressions/addition/bigint-and-number.js"
	out := captureStdout(t, func() {
		runTest262([]string{"-root", root, "-one", one})
	})
	var got test262Result
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("-one 应输出单个用例的 JSON, 实际 %q (%v)", out, err)
	}
	if got.RelPath != one {
		t.Fatalf("用例路径应为 %q, 实际 %q", one, got.RelPath)
	}
	if !got.Pass {
		t.Fatalf("CLI 应跑完该用例并判通过, 实际 phase=%s err=%s", got.Phase, got.Err)
	}
}

package main

import (
	"errors"
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
// 期望 type: Test262Error，实际首行只有 `{ message: "" }`（类型名只出现在
// **被回显的源码行**里）时必须判失败；首行含类型名时才判通过。
func TestJudgePhaseNegTypeUsesHeadOnly(t *testing.T) {
	c := &test262Case{Negative: "runtime", NegType: "Test262Error"}

	// 侥幸形状：首行无 Test262Error，源码回显行有。
	echoed := errors.New("vm error: { message: \"\" }\n" +
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

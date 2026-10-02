package vm

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// 持久化 TS 夹具 (testdata/ts/) —— 与 vm_ts_test.go 的临时目录用例互补:
// 这里的文件进仓库、可被人肉打开, 覆盖"类型注解 / enum / 装饰器 / TSX /
// 跨 .ts import / 故意报错"六类, 并作为 M2 出口验收的稳定证据。

func fixtureDir() string { return filepath.Join("..", "testdata", "ts") }

func fixture(name string) string { return filepath.Join(fixtureDir(), name) }

// TestFixtureCrossImport 跨 .ts import + enum 求和: "9 green"。
func TestFixtureCrossImport(t *testing.T) {
	v, err := EvalFile(fixture("entry.ts"))
	if err != nil {
		t.Fatalf("entry.ts: %v", err)
	}
	if got := v.Inspect(); got != "9 green" {
		t.Fatalf("entry.ts = %q, 期望 \"9 green\"", got)
	}
}

// TestFixtureAnnotations interface / 泛型无关注解 / as 断言。
func TestFixtureAnnotations(t *testing.T) {
	v, err := EvalFile(fixture("annotations.ts"))
	if err != nil {
		t.Fatalf("annotations.ts: %v", err)
	}
	if got := v.Inspect(); got != "5" {
		t.Fatalf("annotations.ts = %q, 期望 5", got)
	}
}

// TestFixtureDecorator legacy 装饰器被降级后仍能执行。
func TestFixtureDecorator(t *testing.T) {
	v, err := EvalFile(fixture("decorator.ts"))
	if err != nil {
		t.Fatalf("decorator.ts (装饰器未降级?): %v", err)
	}
	if got := v.Inspect(); got != "w" {
		t.Fatalf("decorator.ts = %q, 期望 \"w\"", got)
	}
}

// TestFixtureTSX TSX 组件夹具。
func TestFixtureTSX(t *testing.T) {
	v, err := EvalFile(fixture("component.tsx"))
	if err != nil {
		t.Fatalf("component.tsx: %v", err)
	}
	if got := v.Inspect(); got != "box" {
		t.Fatalf("component.tsx = %q, 期望 \"box\"", got)
	}
}

// TestFixtureBroken 故意报错样例: 转译错误带 .ts 行号。
func TestFixtureBroken(t *testing.T) {
	src, err := os.ReadFile(fixture("broken.ts"))
	if err != nil {
		t.Fatal(err)
	}
	wantLine := 0
	for i, line := range strings.Split(string(src), "\n") {
		if strings.Contains(line, "const broken") {
			wantLine = i + 1
			break
		}
	}
	_, err = EvalFile(fixture("broken.ts"))
	if err == nil {
		t.Fatal("broken.ts 应当报错")
	}
	if !strings.Contains(err.Error(), "broken.ts:"+strconv.Itoa(wantLine)) {
		t.Errorf("错误应定位到 broken.ts:%d: %v", wantLine, err)
	}
}

// TestFixtureModuleRuntimeFrame 运行时错误 + enum 行数漂移 → 帧定位到
// throw_module.ts 里 throw 的那一行 (动态按源码算行号, 不写死)。
func TestFixtureModuleRuntimeFrame(t *testing.T) {
	src, err := os.ReadFile(fixture("throw_module.ts"))
	if err != nil {
		t.Fatal(err)
	}
	wantLine := 0
	for i, line := range strings.Split(string(src), "\n") {
		if strings.Contains(line, "throw new Error") {
			wantLine = i + 1
			break
		}
	}
	if wantLine == 0 {
		t.Fatal("夹具里找不到 throw 语句")
	}

	_, err = EvalFile(fixture("throw_entry.ts"))
	if err == nil {
		t.Fatal("throw_entry.ts 应当抛出")
	}
	msg := err.Error()
	if !strings.Contains(msg, "fixture-boom") {
		t.Fatalf("错误消息丢了原文: %v", err)
	}
	want := "throw_module.ts:" + strconv.Itoa(wantLine) + ":"
	if !strings.Contains(msg, want) {
		t.Errorf("帧未定位到 %s (enum 改变行数后未映射?):\n%s", want, msg)
	}
	if !strings.Contains(msg, `throw new Error("fixture-boom");`) {
		t.Errorf("帧未展示 .ts 原文行:\n%s", msg)
	}
}

package vm

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/14752222/Gox/tstransform"
)

// M2 P0-1: 报错定位到 .ts 源码行。
//
// 这组测试刻意用会**改变行数**的构造 (enum / namespace) 把"转译后 JS 行号"
// 与"用户 .ts 行号"掰开 —— 如果实现只是把 TS 原文塞进 srcText 而不做行映射,
// 这些用例会拿到错位的源码行, 直接红。

// TestTSRuntimeFrameEntry enum 展开后入口运行时错误仍定位到 .ts 正确行。
func TestTSRuntimeFrameEntry(t *testing.T) {
	dir := t.TempDir()
	// .ts 行号: 1 enum / 2 function / 3 throw / 4 } / 5 boom()
	tsWrite(t, dir, "main.ts", `enum Color { Red, Green = 5, Blue }
function boom(): number {
  throw new Error("bang");
}
boom();
`)
	_, err := EvalFile(filepath.Join(dir, "main.ts"))
	if err == nil {
		t.Fatal("应当抛出运行时错误")
	}
	msg := err.Error()
	if !strings.Contains(msg, "bang") {
		t.Fatalf("错误消息丢了原文: %v", err)
	}
	// 帧必须指向 .ts 第 3 行 (throw 所在行)。
	if !strings.Contains(msg, "main.ts:3:") {
		t.Errorf("帧未定位到 .ts:3 (enum 改变行数后未映射?):\n%s", msg)
	}
	// 展示的源码必须是用户写的 TS 原文那一行。
	if !strings.Contains(msg, `throw new Error("bang");`) {
		t.Errorf("帧未展示 .ts 原文行:\n%s", msg)
	}
}

// TestTSRuntimeFrameModule 被 import 的 .ts 模块内报错也要有 .ts 源码帧。
func TestTSRuntimeFrameModule(t *testing.T) {
	dir := t.TempDir()
	tsWrite(t, dir, "main.ts", `import { boom } from "./lib.ts"; boom();`)
	// lib.ts 行号: 1 enum / 2 function / 3 throw / 4 } / 5 export
	tsWrite(t, dir, "lib.ts", `enum E { A, B, C }
export function boom(): void {
  throw new Error("module-boom");
}
`)
	_, err := EvalFile(filepath.Join(dir, "main.ts"))
	if err == nil {
		t.Fatal("模块内错误应当抛出")
	}
	msg := err.Error()
	if !strings.Contains(msg, "module-boom") {
		t.Fatalf("错误消息丢了原文: %v", err)
	}
	if !strings.Contains(msg, "lib.ts:3:") {
		t.Errorf("模块内错误未定位到 lib.ts:3 (此前模块内报错根本没有源码帧):\n%s", msg)
	}
	if !strings.Contains(msg, `throw new Error("module-boom");`) {
		t.Errorf("模块帧未展示 .ts 原文行:\n%s", msg)
	}
}

// TestTSRuntimeFrameNamespaceAfterShift namespace 展开同样要能映射。
func TestTSRuntimeFrameNamespaceAfterShift(t *testing.T) {
	dir := t.TempDir()
	// .ts 行号: 1 namespace / 2 function / 3 throw / 4 } / 5 boom()
	tsWrite(t, dir, "main.ts", `namespace Util { export const v = 1; }
function boom(): number {
  throw new Error("ns-boom");
}
boom();
`)
	_, err := EvalFile(filepath.Join(dir, "main.ts"))
	if err == nil {
		t.Fatal("应当抛出 (boom 内 throw)")
	}
	if !strings.Contains(err.Error(), "main.ts:3:") {
		t.Errorf("namespace 展开后未映射到 .ts:3:\n%s", err.Error())
	}
	if !strings.Contains(err.Error(), `throw new Error("ns-boom");`) {
		t.Errorf("帧未展示 .ts 原文行:\n%s", err.Error())
	}
}

// TestRemapSourceError 引擎 parser 在转译后 JS 上报错时, 行号引用应被改写回 .ts。
func TestRemapSourceError(t *testing.T) {
	res, err := tstransform.Transform([]byte("enum E { A, B }\nconsole.log(E.B);\n"), "z.ts")
	if err != nil {
		t.Fatal(err)
	}
	// 模拟 parser 的 sourceError (格式见 parser/errors.go: "line N:M: msg")。
	in := &sourceError{parse: true, msg: "line 3:1: unexpected token"}
	out := remapSourceError(in, res.LineMap)
	if out == in {
		t.Fatal("有映射时 sourceError 应被改写")
	}
	if !strings.Contains(out.Error(), "mapped to .ts") {
		t.Errorf("改写后的消息应带映射标注, 得到: %s", out.Error())
	}
	// 无映射时原样返回。
	if got := remapSourceError(in, nil); got != in {
		t.Error("无映射时应原样返回")
	}
	// 非 parse 阶段 (compiler 错误不带行号) 不应改写。
	if got := remapSourceError(&sourceError{msg: "compile boom"}, res.LineMap); got.Error() != "compile boom" {
		t.Errorf("compiler 错误不该被改写: %s", got.Error())
	}
}

// TestRenderFrameFallbackAnnotated 映射缺失时必须回退到转译后 JS 并标注,
// 绝不拿 JS 行号去索引 .ts 原文。直接驱动 renderFrame 以确定性地覆盖该分支。
func TestRenderFrameFallbackAnnotated(t *testing.T) {
	v := New(nil, nil, 0)
	v.SetSourceInfo("x.ts", "const a: number = 1;\nconst b: string = \"y\";\n")
	// 故意给 nil 行映射 —— 模拟 esbuild 没吐 mappings 的极端情况。
	v.SetTranspileMap(nil, "const a = 1;\nconst b = \"y\";\n")
	frame := v.renderFrame(2, 1)
	if !strings.Contains(frame, "(line mapped from JS") {
		t.Errorf("映射缺失时必须标注回退, 得到:\n%s", frame)
	}
	if !strings.Contains(frame, `const b = "y";`) {
		t.Errorf("回退应展示转译后 JS 文本, 得到:\n%s", frame)
	}
}

// TestRenderFrameMappedNoAnnotation 映射成功时不加回退标注 (精确即精确)。
func TestRenderFrameMappedNoAnnotation(t *testing.T) {
	res, err := tstransform.Transform([]byte("enum E { A, B }\nboom();\n"), "y.ts")
	if err != nil {
		t.Fatal(err)
	}
	v := New(nil, nil, 0)
	v.SetSourceInfo("y.ts", "enum E { A, B }\nboom();\n")
	v.SetTranspileMap(res.LineMap, string(res.Code))
	// 找 console/boom 在 JS 里的行号, 映射回 .ts 第 2 行。
	jsLine := 0
	for i, l := range strings.Split(string(res.Code), "\n") {
		if strings.Contains(l, "boom()") {
			jsLine = i + 1
			break
		}
	}
	if jsLine == 0 {
		t.Fatalf("产物里找不到 boom():\n%s", res.Code)
	}
	frame := v.renderFrame(jsLine, 1)
	if strings.Contains(frame, "line mapped from JS") {
		t.Errorf("映射成功时不应出现回退标注:\n%s", frame)
	}
	if !strings.Contains(frame, "y.ts:2:") {
		t.Errorf("映射后应指向 .ts:2:\n%s", frame)
	}
}

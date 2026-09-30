package vm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tsWrite 把文件写入临时目录 (自动建父目录)。
func tsWrite(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEvalFileTS 类型注解在入口文件上被剥掉, 运行时只见 JS 语义。
func TestEvalFileTS(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "main.ts")
	src := `
interface Point { x: number; y: number }
function dist(p: Point): number {
  return Math.sqrt(p.x * p.x + p.y * p.y);
}
	const p: Point = { x: 3, y: 4 };
	let label: string = "dist=" as string;
	dist(p) + " " + label;
`
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	v, err := EvalFile(p)
	if err != nil {
		t.Fatalf("EvalFile(.ts): %v", err)
	}
	if got := v.Inspect(); got != "5 dist=" {
		t.Fatalf("结果 = %q, 期望 %q", got, "5 dist=")
	}
}

// TestImportTSViaJSSpecifier ".js 说明符回落到 .ts 文件" —— TS 官方 ESM 风格。
func TestImportTSViaJSSpecifier(t *testing.T) {
	dir := t.TempDir()
	tsWrite(t, dir, "main.ts", `
import { double, PI } from "./math.js";
double(PI);
`)
	tsWrite(t, dir, "math.ts", `
export const PI: number = 3.0;
export function double(n: number): number { return n * 2; }
`)
	v, err := EvalFile(filepath.Join(dir, "main.ts"))
	if err != nil {
		t.Fatalf("import ./math.js 应解析到 math.ts: %v", err)
	}
	if got := v.Inspect(); got != "6" {
		t.Fatalf("结果 = %q, 期望 6", got)
	}
}

// TestImportTSViaTSSpecifier 显式 .tsx 说明符 + JSX 模块。
// 用大写组件标签: 小写标签会触发编译器的 h 缺省工厂自动补齐 (import gx/gfx),
// vm 单测环境没有注册 GUI 内置模块。
func TestImportTSViaTSSpecifier(t *testing.T) {
	dir := t.TempDir()
	tsWrite(t, dir, "main.tsx", `
import { Widget } from "./widget.tsx";
Widget().kind;
`)
	tsWrite(t, dir, "widget.tsx", `
// 大写标签 = 组件调用, 不触发 h 缺省工厂补齐 (单测环境没有 gx/gfx)
const Box = () => ({ kind: "box" });
export const Widget = () => <Box />;
`)
	if _, err := EvalFile(filepath.Join(dir, "main.tsx")); err != nil {
		t.Fatalf(".tsx 模块 (含 JSX) 加载失败: %v", err)
	}
}

// TestImportExtensionless 无后缀说明符依次补 .js/.ts/.tsx。
func TestImportExtensionless(t *testing.T) {
	dir := t.TempDir()
	tsWrite(t, dir, "main.js", `import { v } from "./mod"; v;`)
	// 只有 .ts 版本存在 —— 应命中
	tsWrite(t, dir, "mod.ts", `export const v = "ts版";`)
	v, err := EvalFile(filepath.Join(dir, "main.js"))
	if err != nil {
		t.Fatalf("无后缀 import 应命中 .ts: %v", err)
	}
	if got := v.Inspect(); got != "ts版" {
		t.Fatalf("结果 = %q", got)
	}
}

// TestImportIndexResolution 目录说明符走 index.js/index.ts。
func TestImportIndexResolution(t *testing.T) {
	dir := t.TempDir()
	tsWrite(t, dir, "main.ts", `import { v } from "./lib"; v;`)
	tsWrite(t, dir, "lib/index.ts", `export const v = "index";`)
	v, err := EvalFile(filepath.Join(dir, "main.ts"))
	if err != nil {
		t.Fatalf("目录 index 解析失败: %v", err)
	}
	if got := v.Inspect(); got != "index" {
		t.Fatalf("结果 = %q", got)
	}
}

// TestImportMissingListsCandidates 全部落空时错误列出候选。
// 导入值必须被真正使用: 未使用的导入会被编译器剪枝, 模块根本不会加载。
func TestImportMissingListsCandidates(t *testing.T) {
	dir := t.TempDir()
	tsWrite(t, dir, "main.ts", `import { v } from "./nope.js"; v;`)
	_, err := EvalFile(filepath.Join(dir, "main.ts"))
	if err == nil {
		t.Fatal("应当报 Cannot find module")
	}
	for _, want := range []string{"./nope.js", "nope.ts", "nope.tsx", "tried"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("错误信息缺 %q: %v", want, err)
		}
	}
}

// TestJSModuleImportsTS JS 工程混用 TS 模块 (渐进迁移场景)。
func TestJSModuleImportsTS(t *testing.T) {
	dir := t.TempDir()
	tsWrite(t, dir, "main.js", `
import { triple } from "./util.ts";
triple(3);
`)
	tsWrite(t, dir, "util.ts", `export function triple(n: number): number { return n * 3 }`)
	v, err := EvalFile(filepath.Join(dir, "main.js"))
	if err != nil {
		t.Fatalf("JS 入口 import .ts 模块失败: %v", err)
	}
	if got := v.Inspect(); got != "9" {
		t.Fatalf("结果 = %q", got)
	}
}

// TestTSErrorSurfaced 转译错误原样浮出 (带行号)。
func TestTSErrorSurfaced(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "broken.ts")
	if err := os.WriteFile(p, []byte("const x: = 1;\nconst y = 2;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := EvalFile(p)
	if err == nil {
		t.Fatal("TS 语法错误应报错")
	}
	if !strings.Contains(err.Error(), "broken.ts:1") {
		t.Errorf("错误应带 文件:行: %v", err)
	}
}

// TestModuleCacheKeyUsesResolvedPath 缓存键用最终解析路径:
// "./side.js" 与 "./side" 两种写法命中同一个模块实例 (只执行一次)。
// 注: 入口脚本两次导入同一模块须用不同导出名 (引擎入口对同名导入的
// 重复声明检测是既有行为, 与 TS 无关)。
func TestModuleCacheKeyUsesResolvedPath(t *testing.T) {
	dir := t.TempDir()
	tsWrite(t, dir, "main.ts", `
import { nextA } from "./side.js";
import { nextB } from "./side";
nextA() + " " + nextB();
`)
	tsWrite(t, dir, "side.ts", `
let n = 0;
const next = () => ++n;
export const nextA = next;
export const nextB = next;
`)
	v, err := EvalFile(filepath.Join(dir, "main.ts"))
	if err != nil {
		t.Fatal(err)
	}
	// 同一模块只执行一次: 两种说明符拿到同一份导出 (同一个 n), "1 2";
	// 若按说明符各执行一遍会是 "1 1"。
	if got := v.Inspect(); got != "1 2" {
		t.Fatalf("结果 = %q, 期望 \"1 2\" (缓存键应按解析后路径去重)", got)
	}
}

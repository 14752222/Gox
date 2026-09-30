package tstransform

import (
	"strings"
	"testing"
)

// mustTransform 转译并断言成功, 返回 JS 文本。
func mustTransform(t *testing.T, src, name string) string {
	t.Helper()
	out, err := ToJS([]byte(src), name)
	if err != nil {
		t.Fatalf("ToJS(%s) 失败: %v", name, err)
	}
	return string(out)
}

func TestIsTS(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"src/main.ts", true},
		{"src/main.tsx", true},
		{"src/main.TS", true}, // 大小写不敏感
		{"src/main.jsx", true},
		{"src/main.mts", true},
		{"src/main.js", false},
		{"src/main.json", false},
		{"main", false},
		{"src/gox.d.ts", true},
	}
	for _, c := range cases {
		if got := IsTS(c.path); got != c.want {
			t.Errorf("IsTS(%q) = %v, 期望 %v", c.path, got, c.want)
		}
	}
}

func TestStripAnnotations(t *testing.T) {
	out := mustTransform(t, `
let n: number = 1;
const s: string = "x";
function f(a: number, b: string = "d"): number { return a; }
`, "a.ts")
	for _, want := range []string{"let n = 1", `const s = "x"`, "function f(a, b = \"d\")"} {
		if !strings.Contains(out, want) {
			t.Errorf("产出缺 %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, ": number") || strings.Contains(out, ": string") {
		t.Errorf("类型注解没剥干净:\n%s", out)
	}
}

func TestStripInterfacesAndAliases(t *testing.T) {
	out := mustTransform(t, `
interface Point { x: number; y: number }
type Name = string | number;
const p: Point = { x: 1, y: 2 };
let n: Name = "s";
`, "b.ts")
	if strings.Contains(out, "interface") || strings.Contains(out, "type ") {
		t.Errorf("interface/type 声明没删掉:\n%s", out)
	}
	if !strings.Contains(out, "{ x: 1, y: 2 }") || !strings.Contains(out, `n = "s"`) {
		t.Errorf("值表达式被误伤:\n%s", out)
	}
}

func TestGenericErasure(t *testing.T) {
	out := mustTransform(t, `
function first<T>(xs: T[]): T { return xs[0]; }
const n = first<number>([1, 2]);
class Box<T extends object> { v?: T }
`, "c.ts")
	if !strings.Contains(out, "function first(xs)") {
		t.Errorf("泛型参数没擦除:\n%s", out)
	}
	if !strings.Contains(out, "first([1, 2])") {
		t.Errorf("泛型调用实参没擦除:\n%s", out)
	}
	if strings.Contains(out, "<T") || strings.Contains(out, "<number>") {
		t.Errorf("泛型标注残留:\n%s", out)
	}
}

func TestAssertionsAndOptional(t *testing.T) {
	out := mustTransform(t, `
const v = JSON.parse("{}") as Record<string, unknown>;
const w = v satisfies object;
function g(a?: number, b = 2) { return a ?? b; }
const u = undefined!;
`, "d.ts")
	if strings.Contains(out, " as ") || strings.Contains(out, "satisfies") || strings.Contains(out, "undefined!") {
		t.Errorf("断言没剥干净:\n%s", out)
	}
	if !strings.Contains(out, "function g(a, b = 2)") {
		t.Errorf("可选参数形态不对:\n%s", out)
	}
}

func TestTSXPreserved(t *testing.T) {
	out := mustTransform(t, `
interface P { n: number }
const C = (p: P) => <window title="x"><text>{p.n}</text></window>;
export default C;
`, "e.tsx")
	// JSX 必须原样保留 —— 引擎 parser 的 JSX 降级接手
	if !strings.Contains(out, `<window title="x">`) || !strings.Contains(out, "</window>") {
		t.Errorf("JSX 被动了:\n%s", out)
	}
	if strings.Contains(out, "interface") || strings.Contains(out, "p: P") {
		t.Errorf("类型没剥干净:\n%s", out)
	}
}

func TestEnumAndNamespaceConverted(t *testing.T) {
	// esbuild 对 enum/namespace 做等价转换而不是拒绝 —— 这是选它而非自研
	// strip-only 的直接理由
	out := mustTransform(t, `
enum Color { Red, Green = 5, Blue }
namespace Util { export const v = 1; }
console.log(Color.Green, Util.v);
`, "f.ts")
	if !strings.Contains(out, "Color") || !strings.Contains(out, "5") {
		t.Errorf("enum 没有转换出可运行的等价物:\n%s", out)
	}
}

func TestImportExportKept(t *testing.T) {
	out := mustTransform(t, `
import { h } from "gox";
import { App } from "./app.js";
export type Id = string;
export interface Todo { id: Id }
export { App };
`, "g.ts")
	// h 未使用会被 esbuild 按 TS 语义删掉 —— 无害: 引擎编译器对用了
	// 小写标签 JSX 的程序会自动补 import { h } from "gx/gfx"（见
	// compiler.go 的 JSX 缺省工厂）。这里只断言它确实删了（固定语义）,
	// 防止将来 esbuild 升级后行为漂移没人知道。
	if strings.Contains(out, `"gox"`) {
		t.Errorf("未使用的 h 导入应按 TS 语义删除 (引擎会自动补):\n%s", out)
	}
	if !strings.Contains(out, `export { App }`) || !strings.Contains(out, `import { App }`) {
		t.Errorf("值导出/导入被误删:\n%s", out)
	}
	if strings.Contains(out, "interface") || strings.Contains(out, "export type") {
		t.Errorf("类型声明没删掉:\n%s", out)
	}
}

func TestErrorReporting(t *testing.T) {
	_, err := ToJS([]byte("const x: = 1;"), "bad.ts")
	if err == nil {
		t.Fatal("语法错误应当报错")
	}
	if !strings.Contains(err.Error(), "bad.ts") {
		t.Errorf("错误信息应带文件名: %v", err)
	}
	if !strings.Contains(err.Error(), "1:") {
		t.Errorf("错误信息应带行号: %v", err)
	}
}

func TestJSXFileChannel(t *testing.T) {
	// .jsx 与 .tsx 走同一条保留通道
	out := mustTransform(t, `const C = () => <column><text>hi</text></column>;`, "h.jsx")
	if !strings.Contains(out, "<column>") {
		t.Errorf(".jsx 的 JSX 被动了:\n%s", out)
	}
}

package tstransform

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lineOf 返回 text 里第一条包含 substr 的行号 (1-based)。
func lineOf(t *testing.T, text, substr string) int {
	t.Helper()
	for i, line := range strings.Split(text, "\n") {
		if strings.Contains(line, substr) {
			return i + 1
		}
	}
	t.Fatalf("在产物里找不到 %q:\n%s", substr, text)
	return 0
}

// TestLineMapPlainStripping 纯类型剥离不改变行; 每个语句应映射回同一行。
func TestLineMapPlainStripping(t *testing.T) {
	src := `let a: number = 1;
let b: string = "x";
console.log(a, b);
`
	res, err := Transform([]byte(src), "plain.ts")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	if res.LineMap.IsEmpty() {
		t.Fatal("期望拿到行映射, 实际为空")
	}
	for _, line := range []int{1, 2, 3} {
		got, ok := res.LineMap.MapLine(line)
		if !ok || got != line {
			t.Errorf("MapLine(%d) = %d, ok=%v; 期望原样 %d (纯剥离不改行)", line, got, ok, line)
		}
	}
}

// TestLineMapEnumShiftsLines 是 P0-1 的核心断言: enum 会把一行展开成多行,
// 之后的语句在 JS 里的行号 ≠ 源里的行号 —— 映射必须把它们指回 .ts 的正确行。
func TestLineMapEnumShiftsLines(t *testing.T) {
	src := `enum Color { Red, Green = 5, Blue }
const c: Color = Color.Green;
console.log(c);
`
	res, err := Transform([]byte(src), "enum.ts")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	js := string(res.Code)

	// enum 展开后, console.log 在 JS 里的行号一定晚于 3 (源里的行号)。
	jsConsoleLine := lineOf(t, js, "console.log")
	if jsConsoleLine <= 3 {
		t.Fatalf("预期 enum 改变行数, 但 console.log 仍在 JS 第 %d 行\n%s", jsConsoleLine, js)
	}
	// 但映射回源必须是第 3 行 (console.log 在 .ts 里的位置)。
	got, ok := res.LineMap.MapLine(jsConsoleLine)
	if !ok || got != 3 {
		t.Errorf("MapLine(JS %d) = %d, ok=%v; 期望映射回 .ts 第 3 行\n--- JS ---\n%s",
			jsConsoleLine, got, ok, js)
	}
	// const 那行同理, 应映射回 .ts 第 2 行。
	jsConstLine := lineOf(t, js, "const c = ")
	if got, ok := res.LineMap.MapLine(jsConstLine); !ok || got != 2 {
		t.Errorf("MapLine(JS %d) = %d, ok=%v; 期望映射回 .ts 第 2 行", jsConstLine, got, ok)
	}
}

// TestLineMapNamespaceShifts 与 enum 同理, 覆盖 namespace。
func TestLineMapNamespaceShifts(t *testing.T) {
	src := `namespace Util { export const v = 1; }
console.log(Util.v);
`
	res, err := Transform([]byte(src), "ns.ts")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	js := string(res.Code)
	jsLine := lineOf(t, js, "console.log")
	if got, ok := res.LineMap.MapLine(jsLine); !ok || got != 2 {
		t.Errorf("namespace 展开后 console.log 应映射回 .ts 第 2 行, 得到 %d (ok=%v)\n%s", got, ok, js)
	}
}

// TestLineMapMapPosColumn 列也要能映射 (至少落在合理范围)。
func TestLineMapMapPosColumn(t *testing.T) {
	src := "const value: number = 42;\nconsole.log(value);\n"
	res, err := Transform([]byte(src), "col.ts")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	line, col, ok := res.LineMap.MapPos(2, 1)
	if !ok || line != 2 {
		t.Errorf("MapPos(2,1) = %d:%d, ok=%v; 期望第 2 行", line, col, ok)
	}
	if col < 1 {
		t.Errorf("列应 >= 1, 得到 %d", col)
	}
}

// TestLineMapRemapMessage 解析错误里的 JS 行号应能被改写回 .ts 行号。
func TestLineMapRemapMessage(t *testing.T) {
	src := "enum Color { Red, Green = 5, Blue }\nconsole.log(Color.Green);\n"
	res, err := Transform([]byte(src), "remap.ts")
	if err != nil {
		t.Fatalf("Transform: %v", err)
	}
	// 空映射表: 原样返回, 不做任何改写 (调用方只在有映射时调用, 这里是兜底)。
	if got := (&LineMap{}).RemapMessage("line 999:3: unexpected token"); got != "line 999:3: unexpected token" {
		t.Errorf("空映射表应原样返回, 得到:\n%s", got)
	}
	// 构造一个确定能映射的行号引用 (第 3 行一定有 mapping)。
	if !strings.Contains(res.LineMap.RemapMessage("line 3:1: x"), "mapped to .ts") {
		t.Errorf("可映射的行号应被改写并标注, 得到:\n%s", res.LineMap.RemapMessage("line 3:1: x"))
	}
}

// TestDecoratorDowngraded 装饰器必须被 esbuild 降级成 JS (引擎 parser 不认 @)。
func TestDecoratorDowngraded(t *testing.T) {
	src := `
function log(target: any) { return target; }
@log
class Greeter {
  name: string = "g";
}
const g = new Greeter();
g.name;
`
	res, err := Transform([]byte(src), "decorated.ts")
	if err != nil {
		t.Fatalf("带装饰器的 TS 应能转译: %v", err)
	}
	out := string(res.Code)
	if strings.Contains(out, "@log") {
		t.Errorf("装饰器语法未降级 (parser 会挂):\n%s", out)
	}
	// esbuild 的 legacy 装饰器辅助函数。
	if !strings.Contains(out, "Greeter") {
		t.Errorf("类定义丢失:\n%s", out)
	}
}

// TestLineMapEmptyForNoSourcemap 确保"无映射"时 IsEmpty 为真 (降级判据)。
func TestLineMapEmptyForNoSourcemap(t *testing.T) {
	lm, err := DecodeLineMap(nil)
	if err != nil || lm != nil {
		t.Fatalf("空 map 应返回 (nil, nil), 得到 (%v, %v)", lm, err)
	}
	if !lm.IsEmpty() {
		t.Error("nil LineMap 的 IsEmpty 应为 true")
	}
}

// TestCacheRoundTrip 直接验证 encodeJSON → DecodeLineMap 的自洽性 (缓存命中
// 时正是走这条路径), 避免"缓存读回来映射丢了"这种静默退化。
func TestCacheRoundTrip(t *testing.T) {
	src := "enum E { A, B }\nconsole.log(E.B);\n"
	res, err := Transform([]byte(src), "roundtrip.ts")
	if err != nil {
		t.Fatal(err)
	}
	encoded := res.LineMap.encodeJSON()
	back, err := DecodeLineMap([]byte(encoded))
	if err != nil {
		t.Fatalf("解码重编码的 map 失败: %v", err)
	}
	jsLine := lineOf(t, string(res.Code), "console.log")
	want, _ := res.LineMap.MapLine(jsLine)
	got, ok := back.MapLine(jsLine)
	if !ok || got != want {
		t.Errorf("缓存往返后映射漂移: 原 %d, 回来 %d (ok=%v)", want, got, ok)
	}
}

// TestTransformCachedHit 用"篡改缓存文件"的方式证明第二次真的读了缓存,
// 而不是靠时间/日志这种不可靠信号。
func TestTransformCachedHit(t *testing.T) {
	t.Setenv("GOX_CACHE_DIR", t.TempDir())
	src := []byte("const n: number = 1;\nn;\n")
	path := filepath.Join(t.TempDir(), "hit.ts")

	res1, err := TransformCached(src, path)
	if err != nil {
		t.Fatalf("第一次: %v", err)
	}
	key := cacheKey(src, path)
	cf := cacheFilePath(key)
	if _, err := os.ReadFile(cf); err != nil {
		t.Fatalf("第一次应写入缓存 %s: %v", cf, err)
	}
	// 把缓存里的 Code 换成一个哨兵: 若第二次命中, 返回的必是哨兵。
	sentinel := "/* SENTINEL-FROM-CACHE */\n0;\n"
	rec := cacheRecord{Version: cacheVersion, Code: sentinel}
	b, _ := json.Marshal(rec)
	if err := os.WriteFile(cf, b, 0o644); err != nil {
		t.Fatalf("篡改缓存失败: %v", err)
	}
	res2, err := TransformCached(src, path)
	if err != nil {
		t.Fatalf("第二次: %v", err)
	}
	if string(res2.Code) != sentinel {
		t.Errorf("第二次没命中缓存 (也未读到哨兵)\n第一次: %s\n第二次: %s", res1.Code, res2.Code)
	}
}

// TestTransformCacheInvalidatedByContent 内容变了键就变, 不会读到老缓存。
func TestTransformCacheInvalidatedByContent(t *testing.T) {
	path := "/tmp/x.ts"
	k1 := cacheKey([]byte("const a: number = 1;"), path)
	k2 := cacheKey([]byte("const a: number = 2;"), path)
	if k1 == k2 {
		t.Fatal("内容不同却得到同一个缓存键 —— 会脏读")
	}
	// 路径不同键也不同
	if cacheKey([]byte("const a: number = 1;"), path) == cacheKey([]byte("const a: number = 1;"), "/tmp/y.ts") {
		t.Error("路径不同却得到同一个缓存键")
	}
}

// TestTransformCacheCorrupt 缓存文件损坏时必须安全重建, 不影响正确性。
func TestTransformCacheCorrupt(t *testing.T) {
	t.Setenv("GOX_CACHE_DIR", t.TempDir())
	src := []byte("const v: string = \"ok\";\nv;\n")
	path := filepath.Join(t.TempDir(), "corrupt.ts")
	key := cacheKey(src, path)
	cf := cacheFilePath(key)
	if err := os.MkdirAll(filepath.Dir(cf), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cf, []byte("{ this is not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := TransformCached(src, path)
	if err != nil {
		t.Fatalf("损坏缓存应安全重建而不是报错: %v", err)
	}
	if !strings.Contains(string(res.Code), `"ok"`) {
		t.Errorf("重建产物不对:\n%s", res.Code)
	}
}

// TestTransformCacheMissingDir 缓存目录不存在时自动创建。
func TestTransformCacheMissingDir(t *testing.T) {
	base := filepath.Join(t.TempDir(), "deep", "nested", "cache")
	t.Setenv("GOX_CACHE_DIR", base)
	src := []byte("let x: number = 1;\nx;\n")
	path := filepath.Join(t.TempDir(), "fresh.ts")
	if _, err := TransformCached(src, path); err != nil {
		t.Fatalf("缓存目录缺失时应自动创建: %v", err)
	}
	if !strings.HasPrefix(cacheFilePath(cacheKey(src, path)), base) {
		t.Errorf("缓存目录未按 GOX_CACHE_DIR 落盘: %s", cacheFilePath(cacheKey(src, path)))
	}
}

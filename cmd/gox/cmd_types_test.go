package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGeneratedTypesMatchTemplate 把"模板里的 gox.d.ts 就是 `gox types` 的产物"
// 钉成测试: 生成结果与模板文件必须逐字节一致 (diff 为空)。
//
// 失败时的修复动作只有一个: 在仓库根跑
//
//	go run ./cmd/gox types scaffold/template/ts/src/gox.d.ts
//
// 这条约束的价值在于: 内置模块导出面一旦变化 (gfx 加了个内置函数), 模板里的
// .d.ts 若没同步, IDE 就会少提示/误报 —— 测试把它变成红灯而不是静默漂移。
func TestGeneratedTypesMatchTemplate(t *testing.T) {
	dir := filepath.Join("..", "..", "scaffold", "template", "ts", "src")
	want, err := os.ReadFile(filepath.Join(dir, "gox.d.ts"))
	if err != nil {
		t.Fatalf("读模板 gox.d.ts: %v", err)
	}
	got := generateTypes(dir)
	if got != string(want) {
		// 给出第一处差异, 方便定位。
		gl := strings.Split(got, "\n")
		wl := strings.Split(string(want), "\n")
		for i := 0; i < len(gl) || i < len(wl); i++ {
			var g, w string
			if i < len(gl) {
				g = gl[i]
			}
			if i < len(wl) {
				w = wl[i]
			}
			if g != w {
				t.Fatalf("模板 gox.d.ts 与 `gox types` 产物不一致 (首个差异第 %d 行):\n 模板: %q\n 生成: %q\n\n请重跑: go run ./cmd/gox types scaffold/template/ts/src/gox.d.ts",
					i+1, w, g)
			}
		}
		t.Fatal("模板 gox.d.ts 与生成结果不一致")
	}
}

// TestGeneratedTypesCoverage 生成的声明必须覆盖关键内置模块, 且对无法推断的
// 签名标注「类型待补」而不是编造具体类型。
func TestGeneratedTypesCoverage(t *testing.T) {
	dir := t.TempDir()
	// 放一个用户模块, 验证清单会列出它的导出。
	if err := os.WriteFile(filepath.Join(dir, "helper.js"),
		[]byte("export function twice(n) { return n * 2; }\nexport const marker = 1;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := generateTypes(dir)

	for _, mod := range []string{
		`declare module "gox"`, `declare module "gx/gfx"`, `declare module "gx/solid"`,
		`declare module "gx/view"`, `declare module "gx/router"`, `declare module "gx/screen"`,
		`declare module "gx/viewport"`, `declare module "gx/theme"`, `declare module "gx/dev"`,
		`declare module "fs"`, `declare module "path"`, `declare module "http"`, `declare module "process"`,
	} {
		if !strings.Contains(out, mod) {
			t.Errorf("生成结果缺 %s", mod)
		}
	}
	if !strings.Contains(out, "类型待补") {
		t.Error("无法推断的签名必须标注「类型待补」")
	}
	if !strings.Contains(out, "(...args: any[]): any") {
		t.Error("函数签名应使用 (...args: any[]): any")
	}
	if !strings.Contains(out, "declare namespace JSX") {
		t.Error("应包含 JSX 命名空间 (内置元素宽松声明)")
	}
	// 用户模块清单
	if !strings.Contains(out, "helper.js") || !strings.Contains(out, "twice") {
		t.Errorf("用户模块导出清单未包含 helper.js 的 twice:\n%s", out)
	}
}

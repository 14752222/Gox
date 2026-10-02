package vm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== node_modules 解析 + 内置核心模块 import 的端到端测试 =====
//
// 全部用 t.TempDir() 造夹具，**不联网**。覆盖:
//   - 包入口解析顺序 exports > module > main > browser > index
//   - exports 的子路径键与条件裁剪 (import / default)
//   - 子路径直接文件解析、目录爬升、作用域包
//   - 找不到时错误列出候选路径
//   - 纯 JS npm 包在 VM 里直接 import 成功并调用导出
//   - fs/path/http/process 作为内置模块的 default / 命名导入

// writeNpmTree 在临时目录里写出一棵可含子目录的文件树，返回根目录。
func writeNpmTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, src := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return dir
}

// evalNpmEntry 执行入口脚本并返回最后一个表达式的值。
func evalNpmEntry(t *testing.T, dir, entry string) object.Value {
	t.Helper()
	v, err := EvalFileVM(filepath.Join(dir, filepath.FromSlash(entry)))
	if err != nil {
		t.Fatalf("EvalFileVM %s: %v", entry, err)
	}
	return v.LastPopped()
}

// ===== 入口字段解析顺序 =====

func TestNpmResolveMain(t *testing.T) {
	dir := writeNpmTree(t, map[string]string{
		"node_modules/pkg/package.json": `{"name":"pkg","main":"lib/main.js"}`,
		"node_modules/pkg/lib/main.js":  `export const tag = "main";`,
		"app.js":                        `import { tag } from "pkg"; tag`,
	})
	assertString(t, evalNpmEntry(t, dir, "app.js"), "main")
}

func TestNpmResolveModulePreferredOverMain(t *testing.T) {
	// 同时有 module 与 main 时，ESM-first 取 module。
	dir := writeNpmTree(t, map[string]string{
		"node_modules/pkg/package.json": `{"name":"pkg","main":"cjs.js","module":"esm.js"}`,
		"node_modules/pkg/cjs.js":       `export const tag = "cjs";`,
		"node_modules/pkg/esm.js":       `export const tag = "esm";`,
		"app.js":                        `import { tag } from "pkg"; tag`,
	})
	assertString(t, evalNpmEntry(t, dir, "app.js"), "esm")
}

func TestNpmResolveExportsRoot(t *testing.T) {
	dir := writeNpmTree(t, map[string]string{
		"node_modules/pkg/package.json": `{"name":"pkg","main":"main.js","exports":"./exp.js"}`,
		"node_modules/pkg/main.js":      `export const tag = "main";`,
		"node_modules/pkg/exp.js":       `export const tag = "exports";`,
		"app.js":                        `import { tag } from "pkg"; tag`,
	})
	assertString(t, evalNpmEntry(t, dir, "app.js"), "exports")
}

func TestNpmResolveExportsConditionImport(t *testing.T) {
	// 条件对象: import 优先于 default，require 跳过。
	dir := writeNpmTree(t, map[string]string{
		"node_modules/pkg/package.json": `{"name":"pkg","exports":{".":{"require":"./cjs.js","import":"./esm.js","default":"./def.js"}}}`,
		"node_modules/pkg/cjs.js":       `export const tag = "cjs";`,
		"node_modules/pkg/esm.js":       `export const tag = "esm";`,
		"node_modules/pkg/def.js":       `export const tag = "def";`,
		"app.js":                        `import { tag } from "pkg"; tag`,
	})
	assertString(t, evalNpmEntry(t, dir, "app.js"), "esm")
}

func TestNpmResolveExportsConditionDefaultOnly(t *testing.T) {
	// 只有 default 条件时取 default（require 不参与）。
	dir := writeNpmTree(t, map[string]string{
		"node_modules/pkg/package.json": `{"name":"pkg","exports":{".":{"require":"./cjs.js","default":"./def.js"}}}`,
		"node_modules/pkg/cjs.js":       `export const tag = "cjs";`,
		"node_modules/pkg/def.js":       `export const tag = "def";`,
		"app.js":                        `import { tag } from "pkg"; tag`,
	})
	assertString(t, evalNpmEntry(t, dir, "app.js"), "def")
}

func TestNpmResolveExportsSubpath(t *testing.T) {
	dir := writeNpmTree(t, map[string]string{
		"node_modules/pkg/package.json": `{"name":"pkg","exports":{".":"./index.js","./fp":"./fp/index.js"}}`,
		"node_modules/pkg/index.js":     `export const tag = "root";`,
		"node_modules/pkg/fp/index.js":  `export const tag = "fp";`,
		"app.js":                        `import { tag } from "pkg/fp"; tag`,
	})
	assertString(t, evalNpmEntry(t, dir, "app.js"), "fp")
}

func TestNpmResolveDirectSubpathFile(t *testing.T) {
	// 无 exports 时子路径直接当文件解析（补后缀 + 目录 index）。
	dir := writeNpmTree(t, map[string]string{
		"node_modules/pkg/package.json": `{"name":"pkg","main":"index.js"}`,
		"node_modules/pkg/index.js":     `export const tag = "root";`,
		"node_modules/pkg/util.js":      `import { base } from "./base.js"; export const tag = base + "-util";`,
		"node_modules/pkg/base.js":      `export const base = "b";`,
		"app.js":                        `import { tag } from "pkg/util"; tag`,
	})
	assertString(t, evalNpmEntry(t, dir, "app.js"), "b-util")
}

func TestNpmResolveScopedPackage(t *testing.T) {
	dir := writeNpmTree(t, map[string]string{
		"node_modules/@scope/pkg/package.json": `{"name":"@scope/pkg","main":"index.js"}`,
		"node_modules/@scope/pkg/index.js":     `export const tag = "scoped";`,
		"app.js":                               `import { tag } from "@scope/pkg"; tag`,
	})
	assertString(t, evalNpmEntry(t, dir, "app.js"), "scoped")
}

func TestNpmResolveWalksUpDirectories(t *testing.T) {
	// 入口在 proj/src/deep 下，node_modules 在 proj/node_modules —— 逐级向上找到。
	dir := writeNpmTree(t, map[string]string{
		"node_modules/pkg/package.json": `{"name":"pkg","main":"index.js"}`,
		"node_modules/pkg/index.js":     `export const tag = "walked-up";`,
		"src/deep/app.js":               `import { tag } from "pkg"; tag`,
	})
	assertString(t, evalNpmEntry(t, dir, "src/deep/app.js"), "walked-up")
}

func TestNpmResolveNotFoundReportsCandidates(t *testing.T) {
	dir := writeNpmTree(t, map[string]string{
		"src/app.js": `import { x } from "missing-pkg"; x`,
	})
	_, err := EvalFileVM(filepath.Join(dir, "src", "app.js"))
	if err == nil {
		t.Fatal("缺失包应当报错")
	}
	msg := err.Error()
	for _, want := range []string{"missing-pkg", "node_modules", "src", "app.js"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("报错应包含 %q 以便排错，实际:\n%s", want, msg)
		}
	}
}

// ===== 出口验收: 纯 JS npm 包直接可用 =====

func TestNpmEndToEndPureJSPackage(t *testing.T) {
	// 一个"纯 JS 包": package.json + exports + 包内相对 import + 具名导出。
	dir := writeNpmTree(t, map[string]string{
		"node_modules/greeter/package.json": `{"name":"greeter","exports":{".":{"import":"./index.js","default":"./index.js"}}}`,
		"node_modules/greeter/index.js":     "import { suffix } from \"./suffix.js\";\nexport function greet(name) { return \"hi \" + name + suffix; }",
		"node_modules/greeter/suffix.js":    `export const suffix = "!";`,
		"app.js":                            `import { greet } from "greeter"; greet("zed")`,
	})
	assertString(t, evalNpmEntry(t, dir, "app.js"), "hi zed!")
}

func TestNpmEndToEndDefaultImportOfPackage(t *testing.T) {
	// 包显式提供 export default 时，默认导入可用。
	dir := writeNpmTree(t, map[string]string{
		"node_modules/doubler/package.json": `{"name":"doubler","main":"index.js"}`,
		"node_modules/doubler/index.js":     `export default function dbl(n) { return n * 2; }`,
		"app.js":                            `import dbl from "doubler"; dbl(21)`,
	})
	assertNumber(t, evalNpmEntry(t, dir, "app.js"), 42)
}

// ===== 内置核心模块: default / 命名导入 =====

func TestBuiltinModuleDefaultImportFS(t *testing.T) {
	assertString(t, evalJS(t, `import fs from "fs"; typeof fs.readFileSync`), "function")
	assertString(t, evalJS(t, `import fs from "fs"; typeof fs.existsSync`), "function")
}

func TestBuiltinModuleNamedImportFS(t *testing.T) {
	assertString(t, evalJS(t, `import { readFileSync } from "fs"; typeof readFileSync`), "function")
}

func TestBuiltinModuleNamespaceImportFS(t *testing.T) {
	assertString(t, evalJS(t, `import * as fs from "fs"; typeof fs.writeFileSync`), "function")
}

func TestBuiltinModuleImportPathHTTPProcess(t *testing.T) {
	assertString(t, evalJS(t, `import path from "path"; typeof path.join`), "function")
	assertString(t, evalJS(t, `import { join } from "path"; typeof join`), "function")
	assertString(t, evalJS(t, `import http from "http"; typeof http.createServer`), "function")
	assertString(t, evalJS(t, `import process from "process"; typeof process.cwd`), "function")
}

func TestBuiltinModuleFSActuallyWorks(t *testing.T) {
	// 通过 import 拿到 fs 后真的能读写文件（不只是 typeof 是 function）。
	dir := t.TempDir()
	file := filepath.Join(dir, "via-import.txt")
	evalJS(t, `import fs from "fs"; fs.writeFileSync(`+q(file)+`, 'imported fs')`)
	assertString(t, evalJS(t, `import fs from "fs"; fs.readFileSync(`+q(file)+`)`), "imported fs")
}

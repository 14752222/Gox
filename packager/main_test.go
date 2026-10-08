package main

import (
	goparser "go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/14752222/Gox/lexer"
	"github.com/14752222/Gox/parser"
	"github.com/14752222/Gox/scaffold"
	"github.com/14752222/Gox/vm"
)

// writeTempFile 写一个测试文件 (自动建父目录)。
func writeTempFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRewriteTSImportSpecifiers 说明符改写: 显式 TS 扩展名 → .js, 其余不动。
func TestRewriteTSImportSpecifiers(t *testing.T) {
	in := `import { a } from "./a.ts";
import { b } from './b.tsx';
export { c } from "../c.mts";
import { d } from "./d.js";
import { e } from "./e";
`
	out := string(rewriteTSImportSpecifiers([]byte(in)))
	for _, want := range []string{`"./a.js"`, `'./b.js'`, `"../c.js"`, `"./d.js"`, `"./e"`} {
		if !strings.Contains(out, want) {
			t.Errorf("改写结果缺 %q:\n%s", want, out)
		}
	}
}

// TestResolveImportPathFallbacks 与 vm 侧一致的回落顺序。
func TestResolveImportPathFallbacks(t *testing.T) {
	dir := t.TempDir()
	writeTempFile(t, filepath.Join(dir, "math.ts"), "export const PI = 3;")
	writeTempFile(t, filepath.Join(dir, "raw.js"), "export const r = 1;")
	writeTempFile(t, filepath.Join(dir, "lib", "index.tsx"), "export const L = 1;")

	cases := map[string]string{
		"./math.js": "math.ts", // .js 说明符回落到 .ts
		"./math":    "math.ts", // 无扩展名依次试探
		"./raw":     "raw.js",
		"./lib":     "lib/index.tsx", // 目录 → index
		"./raw.js":  "raw.js",
		"./missing": "",
	}
	for spec, wantSuffix := range cases {
		got, ok := resolveImportPath(dir, spec)
		if wantSuffix == "" {
			if ok {
				t.Errorf("resolveImportPath(%q) 应失败, 得到 %s", spec, got)
			}
			continue
		}
		if !ok || !strings.HasSuffix(filepath.ToSlash(got), wantSuffix) {
			t.Errorf("resolveImportPath(%q) = %q ok=%v, 期望后缀 %q", spec, got, ok, wantSuffix)
		}
	}
}

// TestPrepareAppFilesRunnable 端到端: TS 入口 → 转译 → 嵌入 JS → VM 真执行。
// 这是 P0-3 "能产出可运行产物" 的直接凭据 (去掉 go build 壳, 验证脚本语义)。
func TestPrepareAppFilesRunnable(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "main.ts")
	writeTempFile(t, entry, `import { triple } from "./util.ts";
triple(7);
`)
	writeTempFile(t, filepath.Join(dir, "util.ts"), `export function triple(n: number): number { return n * 3; }
`)

	// 收集 (打包器的 collectModules)
	src, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{entry: src}
	if err := collectModules(entry, src, files, false); err != nil {
		t.Fatalf("collectModules: %v", err)
	}
	app, err := prepareAppFiles(entry, files)
	if err != nil {
		t.Fatalf("prepareAppFiles: %v", err)
	}
	// 入口固定 main.js, 模块由 util.ts 改名为 util.js
	if _, ok := app["main.js"]; !ok {
		t.Fatalf("缺少入口 main.js, 实际键: %v", keysOf(app))
	}
	if _, ok := app["util.js"]; !ok {
		t.Fatalf("TS 模块应改名为 util.js, 实际键: %v", keysOf(app))
	}
	if strings.Contains(string(app["main.js"]), ".ts") {
		t.Errorf("入口里的 .ts 说明符未改写:\n%s", app["main.js"])
	}

	// 把 app 目录落地, 真跑一遍 —— 证明嵌入产物可运行。
	outDir := t.TempDir()
	for rel, data := range app {
		writeTempFile(t, filepath.Join(outDir, filepath.FromSlash(rel)), string(data))
	}
	v, err := vm.EvalFileVM(filepath.Join(outDir, "main.js"))
	if err != nil {
		t.Fatalf("嵌入产物执行失败: %v", err)
	}
	if got := v.LastPopped().Inspect(); got != "21" {
		t.Fatalf("结果 = %q, 期望 21", got)
	}
}

// TestPrepareAppFilesScaffoldTS 用真实 --ts 模板走一遍打包预处理, 产物全部是
// 可被引擎 parser/compiler 接受的 JS (P0-3 的模板级凭据)。
func TestPrepareAppFilesScaffoldTS(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tsapp")
	if _, err := scaffold.Create(scaffold.Options{Dir: dir, TS: true}); err != nil {
		t.Fatalf("Create(TS): %v", err)
	}
	entry := filepath.Join(dir, "src", "main.tsx")
	src, err := os.ReadFile(entry)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{entry: src}
	if err := collectModules(entry, src, files, false); err != nil {
		t.Fatalf("collectModules(TS 模板): %v", err)
	}
	app, err := prepareAppFiles(entry, files)
	if err != nil {
		t.Fatalf("prepareAppFiles: %v", err)
	}
	if len(app) < 5 {
		t.Fatalf("模板模块数偏少 (期望 main + app + theme/store + 组件): %v", keysOf(app))
	}
	for rel, data := range app {
		if !strings.HasSuffix(rel, ".js") {
			t.Errorf("嵌入文件 %s 不是 .js (TS 模块必须转译改名)", rel)
		}
		if strings.Contains(string(data), "interface ") || strings.Contains(string(data), ": number") {
			t.Errorf("%s 里仍有 TS 类型语法:\n%s", rel, data)
		}
		// 只做 parse 级校验: compiler.Compile 会做内置模块导出名检查, 而打包器
		// 测试进程没链接 gfx (无头环境), "gox" 聚合导出不全 —— 那是测试环境的
		// 缺失, 不是打包产物的问题。模板的 compile 级校验在 scaffold 包里
		// (TestGeneratedTSScriptsCompile) 已经覆盖。
		l := lexer.New(string(data))
		p := parser.New(l)
		_ = p.ParseProgram()
		if p.Errors().HasErrors() {
			t.Errorf("%s 转译产物解析失败:\n%s\n--- JS ---\n%s", rel, p.Errors().String(), data)
		}
	}
}

// ===== 打包产物的 process.argv 注入 (看板 r1q3Gy) =====
//
// 生成的宿主 main.go 必须把宿主的 os.Args 补齐成
// [可执行文件, 脚本路径, ...用户参数] 再注入 process.argv —— 打包产物没有
// "脚本路径" 这个宿主参数, 不补的话用户参数会从索引 1 起, 与源码跑
// (`gox app.js a.log`, 索引 2 起) 错一位, 同一份脚本两种跑法读到的不一样。
//
// 宿主 main.go 的生成者就是本包; packager 是 main 包、不能被产物 import,
// 所以这段补位逻辑只能以**模板字面量**存在 ⇒ 这里靠文本断言看住它:
// 1) 两个模板都注入了; 2) 注入在建 VM 之前 (process.argv 是进程级快照);
// 3) 模板仍是语法合法的 Go (用 go/parser 兜住手改模板时的低级错误)。

const haveProcessArgvInjection = "stdlib.SetProcessArgv(append([]string{os.Args[0], entry}, os.Args[1:]...))"

func TestAppTemplatesForwardProcessArgv(t *testing.T) {
	templates := map[string]string{"cli": appMainGo, "gui": appMainGoGUI}
	for name, tmpl := range templates {
		if !strings.Contains(tmpl, haveProcessArgvInjection) {
			t.Errorf("%s 模板缺 process.argv 注入 %q —— 打包版会把用户参数放在索引 1 (源码跑在索引 2)",
				name, haveProcessArgvInjection)
		}
		// 注入必须早于 VM 构造: process.argv 是模块构建期快照
		if inj, vmNew := strings.Index(tmpl, "SetProcessArgv"), strings.Index(tmpl, "stdlib.SetupGlobals()"); inj < 0 || vmNew < 0 || inj > vmNew {
			t.Errorf("%s 模板里注入位置不对 (inject@%d, VM 构造@%d): 快照先落下就改不动了",
				name, inj, vmNew)
		}
		// 模板手改必须有语法兜底: go/parser 只解析, 不做类型检查/依赖解析
		if _, err := goparser.ParseFile(token.NewFileSet(), "main.go", tmpl, goparser.SkipObjectResolution); err != nil {
			t.Errorf("%s 模板不是合法的 Go 源码: %v", name, err)
		}
	}
}

func keysOf(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

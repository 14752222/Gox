package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gox lint 的测试。
//
// ## 为什么每条规则都要配"反例"
//
// lint 这类工具的失效方式不是崩溃，而是**安静地什么都不报** —— 一条永远输出
// "ok" 的规则比没有规则更糟，因为它会给人虚假的安全感（apps/ 跑一遍零命中，
// 既可能是代码干净，也可能是规则死了）。所以每条规则都必须同时钉住：
//
//  1. 陷阱代码**必须**命中；
//  2. 正确写法**必须不**命中（误报是 lint 的头号死因 —— 噪声一多，人就把
//     整个工具关掉了）。
//
// 嵌套元素与组件调用那两条尤其要单独钉：JSX 降级成 h(h(...)) 之后，它们和
// "快照子节点"在 AST 上长得一模一样，第一版实现就因为它们误报了满屏。

// lintRun 在一个临时目录里跑 lint，返回命中列表。
func lintRun(t *testing.T, files map[string]string, only ...string) []lintFinding {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("建目录: %v", err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatalf("写文件: %v", err)
		}
	}
	collected, err := lintCollectFiles(dir)
	if err != nil {
		t.Fatalf("lintCollectFiles: %v", err)
	}
	onlySet := map[string]bool{}
	for _, r := range only {
		onlySet[r] = true
	}
	var out []lintFinding
	for _, f := range collected {
		found, err := lintCheckFile(f, dir, onlySet, nil)
		if err != nil {
			t.Fatalf("lint %s: %v", f.display, err)
		}
		out = append(out, found...)
	}
	return out
}

// lintHas 报告命中里有没有某规则的、消息含 frag 的一条。
func lintHas(findings []lintFinding, rule, frag string) bool {
	for _, f := range findings {
		if f.rule == rule && strings.Contains(f.message, frag) {
			return true
		}
	}
	return false
}

// TestLintSnapshotChild 规则 1：子节点写成快照。
func TestLintSnapshotChild(t *testing.T) {
	const src = `
import { createSignal } from "gx/solid";
const [count, setCount] = createSignal(0);
const [name] = createSignal("x");

export default function App() {
	return (
		<column>
			<text>{count()}</text>
			<text>{count}</text>
			<text>{() => name()}</text>
		</column>
	);
}
`
	found := lintRun(t, map[string]string{"src/app.js": src}, "snapshot-child")

	if len(found) != 1 {
		t.Fatalf("命中数 = %d, want 1 (只有 {count()} 该报; 实际 = %v)", len(found), found)
	}
	if !lintHas(found, "snapshot-child", "count()") {
		t.Fatalf("没抓到快照子节点 (实际 = %v)", found)
	}
	// 行号必须指向写错的那一行
	if found[0].line != 9 {
		t.Fatalf("行号 = %d, want 9 (应指向 <text>{count()}</text>)", found[0].line)
	}
}

// TestLintSnapshotChildNotFooledByNesting 嵌套元素与组件调用不许误报。
//
// 这两条是真实踩过的坑：JSX 降级后 `<column><text>{n()}</text></column>` 变成
// h("column", null, h("text", null, n()))，外层 h 的子节点就是个调用表达式 ——
// 第一版实现因此把**每一个嵌套元素**都报成快照。
func TestLintSnapshotChildNotFooledByNesting(t *testing.T) {
	const src = `
import { createSignal } from "gx/solid";
const [n] = createSignal(1);

function Toolbar() {
	return <row><text>toolbar</text></row>;
}

export default function App() {
	return (
		<column>
			<Toolbar />
			<row>
				<text>{n}</text>
			</row>
		</column>
	);
}
`
	found := lintRun(t, map[string]string{"src/app.js": src}, "snapshot-child")
	if len(found) != 0 {
		t.Fatalf("嵌套元素 / 组件调用不该被当成快照 (实际 = %v)", found)
	}
}

// TestLintSlashComment 规则 2：子节点区的 // 不是注释。
func TestLintSlashComment(t *testing.T) {
	const src = `
export default function App() {
	return (
		<column>
			// 这行不是注释, 会被渲染出来
			{/* 这才是注释 */}
			<text>正常文本</text>
		</column>
	);
}
`
	found := lintRun(t, map[string]string{"src/app.js": src}, "slash-comment")
	if len(found) != 1 {
		t.Fatalf("命中数 = %d, want 1 (只有 // 那行该报; 实际 = %v)", len(found), found)
	}
	if !lintHas(found, "slash-comment", "//") {
		t.Fatalf("没抓到子节点区的 // (实际 = %v)", found)
	}
}

// TestLintEachNoKey 规则 3：each 无 key。
func TestLintEachNoKey(t *testing.T) {
	const src = `
import { obs } from "gox";
const rows = obs([]);

export default function App() {
	return (
		<column>
			<view each={() => rows.items} key="id">{(r) => <text>{r.id}</text>}</view>
			<view each={() => rows.items}>{(r) => <text>{r.id}</text>}</view>
		</column>
	);
}
`
	found := lintRun(t, map[string]string{"src/app.js": src}, "each-no-key")
	if len(found) != 1 {
		t.Fatalf("命中数 = %d, want 1 (只有无 key 的那个该报; 实际 = %v)", len(found), found)
	}
}

// TestLintLibImportsGox 规则 4：lib/ 不许 import gox。
//
// 顺带钉住"lib/ 判定用相对路径"这条 —— 用绝对路径判的话，任何放在磁盘
// /lib/ 下的工程都会全库误报。
func TestLintLibImportsGox(t *testing.T) {
	files := map[string]string{
		"src/lib/parse.js": `import { h } from "gx/gfx";
export function parse(s) { return JSON.parse(s); }
`,
		"src/lib/pure.js": `export function add(a, b) { return a + b; }
`,
		"src/app.js": `import { h, render } from "gx/gfx";
export default function App() { return <column></column>; }
`,
	}
	found := lintRun(t, files, "lib-imports-gox")
	if len(found) != 1 {
		t.Fatalf("命中数 = %d, want 1 (只有 lib/parse.js 该报; 实际 = %v)", len(found), found)
	}
	if !strings.Contains(found[0].file, "lib") {
		t.Fatalf("命中的文件 = %q, 应位于 lib/ 下", found[0].file)
	}
}

// TestLintSkipDirs 扫描要跳过 node_modules 等目录：那里的代码不是我们的源码，
// 扫了只会产出几百条改不动的噪声。
func TestLintSkipDirs(t *testing.T) {
	files := map[string]string{
		"node_modules/dep/index.js": `<view each={x}>{() => x()}</view>`,
		"dist/bundle.js":            `<view each={x}>{() => x()}</view>`,
		"src/app.js":                `export default function App() { return <column></column>; }`,
	}
	found := lintRun(t, files)
	if len(found) != 0 {
		t.Fatalf("node_modules / dist 里的代码不该被扫 (实际 = %v)", found)
	}
}

// TestLintTSMapsLineNumbersBack .tsx 的行号必须映射回原文。
//
// 为什么值得单测：报一个错的行号比不报更糟 —— 它会让人去改一个无辜的地方。
// 这里故意在类型注解之间留空行，让 JS 与 TS 的行号错开，映射错了必然失配。
func TestLintTSMapsLineNumbersBack(t *testing.T) {
	const src = `import { createSignal } from "gx/solid";

interface Props {
	title: string;
}

const [count] = createSignal(0);

export default function App(props: Props) {
	return (
		<column>
			<text>{count()}</text>
		</column>
	);
}
`
	found := lintRun(t, map[string]string{"src/app.tsx": src}, "snapshot-child")
	if len(found) != 1 {
		t.Fatalf("命中数 = %d, want 1 (实际 = %v)", len(found), found)
	}
	// 原文里 <text>{count()}</text> 在第 12 行
	if found[0].line != 12 {
		t.Fatalf("行号 = %d, want 12 —— .tsx 的行号必须映射回原文, 否则报的位置是错的",
			found[0].line)
	}
}

// TestLintUnknownRuleRejected 拼错的规则名必须报错，而不是被安静地忽略 ——
// `--skip=snapshoot-child` 若被当成"没有这条规则所以跳过"，lint 会一路绿着
// 却什么都没查。
func TestLintUnknownRuleRejected(t *testing.T) {
	if err := lintValidateRules(map[string]bool{"snapshoot-child": true}, nil); err == nil {
		t.Fatal("拼错的规则名没被拒绝")
	}
	if err := lintValidateRules(nil, map[string]bool{"snapshot-child": true}); err != nil {
		t.Fatalf("合法规则名被拒绝: %v", err)
	}
}

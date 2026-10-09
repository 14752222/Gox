package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"unicode"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/lexer"
	"github.com/14752222/Gox/parser"
	"github.com/14752222/Gox/tstransform"
)

// gox lint —— 把"违反了不会报错、只会静默失效"的那几条规矩机器化（rG73bo 3/3）。
//
// ## 为什么需要它，以及为什么只有静态分析能救
//
// apps/README.md 的「写应用时必须守的几条」列了 5 条规矩，开头一句就是
// "这几条**违反了不会报错**，只会静默失效"。其中第 1 条（子节点写成快照）是
// 踩得最多的一条，而它**运行期判不出来** —— 实测：
//
//	{n}          → 信号变了会更新   ✓  getter 是函数，走响应式子节点
//	{n()}        → 永远不更新       ✗  求值成普通字符串，与手写 "init" 无法区分
//	{() => n()}  → 信号变了会更新   ✓
//
// `{n()}` 在被 h() 收到之前就求值完了，h() 拿到的只是一个字符串 —— 那一刻
// "它曾经是个 signal" 这件事已经彻底消失了。gui-guide §8.1 因此写下"这条只能
// 靠纪律，不能靠工具兜底"。但**静态分析看得到调用表达式**：源码里 `count()` 是
// 一次调用，写成 `{() => count()}` 才是不求值的传递方式 —— 这正是 lint 的领域。
// 所以本子命令存在的意义，就是把那句"只能靠纪律"改掉一半。
//
// ## 为什么用 Gox 自己的 parser
//
// 自举：不需要引入任何 JS 解析器依赖。JSX 在 parser 层就降级成 h(...) 调用
// （parser/jsx.go），所以 lint 看到的是**降级后的普通调用表达式** —— 这反而
// 让规则变简单了：子节点就是 h() 的第 3 个及以后的实参，属性就是第 2 个实参
// 那个对象字面量。代价是拿不到"原文里写的是 JSX 还是手写 h()"，但两条路有
// 完全相同的陷阱，同样该报。
//
// ## 规则来源
//
// 四条规则全部来自 apps/README.md §写应用时必须守的几条，不是这里发明的：
//
//	snapshot-child     第 1 条  子节点写成快照（{sig()}）
//	slash-comment      第 2 条  JSX 子节点区的 // 不是注释
//	each-no-key        第 3 条  列表用 each 时必须 key
//	lib-imports-gox    第 4 条  lib/ 保持纯逻辑，不 import gox
//
// lint 只是这些明文规矩的执行者 —— 规矩本身若改了，改的是文档，不是这里。

// lintRule 是一条规则。ID 供 --only/--skip 引用，也出现在输出里；
// Doc 记出处，提醒后来者：规矩在文档里，这里只是执行者。
type lintRule struct {
	ID  string
	Doc string
}

var lintRules = []lintRule{
	{"each-no-key", "apps/README 第 3 条"},
	{"lib-imports-gox", "apps/README 第 4 条"},
	{"slash-comment", "apps/README 第 2 条"},
	{"snapshot-child", "apps/README 第 1 条"},
}

// lintFile 是单个被检查文件的上下文。
type lintFile struct {
	path    string
	display string // 相对项目根的路径，输出用
	inLib   bool   // 是否位于 lib/ 目录（规则 4 用）
	// srcPos 把"转译后 JS 的行列"映射回原文行列。.js 文件是恒等映射。
	//
	// 带**列**号映射而不是只映射行：esbuild 会把 `return (` 与下一行的
	// `<column>` 合并成一行（实测：原文 3 行 → JS 2 行），此时行级映射必然
	// 偏 1 —— 报一个错的行号比不报更糟，它会让人去改一个无辜的地方。
	srcPos func(line, col int) (int, int)
}

// lintFinding 是一条命中。
type lintFinding struct {
	file    string
	line    int
	column  int
	rule    string
	message string
}

func (f lintFinding) String() string {
	return fmt.Sprintf("%s:%d  [%s]  %s", f.file, f.line, f.rule, f.message)
}

// lintSkipDirs 是扫描时跳过的目录。node_modules 与 dist 里的代码不是我们的
// 源码，扫了只会产出几百条改不动的噪声 —— 噪声是所有 lint 工具的死因。
var lintSkipDirs = map[string]bool{
	"node_modules": true,
	"dist":         true,
	"build":        true,
	".git":         true,
	"out":          true,
	"coverage":     true,
}

// lintExts 认的扩展名。.ts/.tsx 先过类型剥离（行号经 LineMap 映射回原文）。
var lintExts = map[string]bool{
	".js": true, ".jsx": true, ".mjs": true, ".cjs": true,
	".ts": true, ".tsx": true, ".mts": true, ".cts": true,
}

// runLint 实现 `gox lint [路径]`。
func runLint(args []string) {
	root := "."
	only := map[string]bool{}
	skip := map[string]bool{}
	jsonOut := false

	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-h" || a == "--help":
			fmt.Fprint(os.Stdout, `用法: gox lint [路径] [选项]

把"违反了不会报错、只会静默失效"的几条规矩机器化（apps/README.md
§写应用时必须守的几条）。规则全部来自那份清单，lint 只是执行者。

规则:
  snapshot-child    JSX 子节点写成快照: {sig()} 在传入前就求值完了, 之后信号
                    变了界面也不动。写成 {() => sig()} 或 {sig} 才订阅更新。
  slash-comment     JSX 子节点区的 // 不是注释, 而是会被渲染出来的文本子节点。
                    注释要写 {/* … */} 或挪到 JSX 外面。
  each-no-key       列表用 each 时没给 key: 列表更新只能整组重建, keyed 复用
                    与 fallback 都失效。
  lib-imports-gox   lib/ 下 import 了 gx/* 模块: lib/ 应保持纯逻辑 (不碰 UI、
                    不碰 signal), 否则脱离 UI 单测这条路就断了。

选项:
  --only <规则>     只跑指定规则 (可重复)
  --skip <规则>     跳过指定规则 (可重复)
  --json            输出 JSON (供 CI 消费)
  -h, --help        显示这份帮助

退出码: 0 = 没有命中; 1 = 有命中; 2 = 用法错误。
`)
			return
		case a == "--only" || a == "--skip":
			i++
			if i >= len(args) {
				lintFatal("%s 后面要跟规则名", a)
			}
			for _, r := range strings.Split(args[i], ",") {
				if r = strings.TrimSpace(r); r != "" {
					if a == "--only" {
						only[r] = true
					} else {
						skip[r] = true
					}
				}
			}
		case strings.HasPrefix(a, "--only="):
			for _, r := range strings.Split(strings.TrimPrefix(a, "--only="), ",") {
				if r = strings.TrimSpace(r); r != "" {
					only[r] = true
				}
			}
		case strings.HasPrefix(a, "--skip="):
			for _, r := range strings.Split(strings.TrimPrefix(a, "--skip="), ",") {
				if r = strings.TrimSpace(r); r != "" {
					skip[r] = true
				}
			}
		case a == "--json":
			jsonOut = true
		case strings.HasPrefix(a, "-"):
			lintFatal("未知选项: " + a)
		default:
			root = a
		}
	}

	if err := lintValidateRules(only, skip); err != nil {
		lintFatal("%v", err)
	}

	files, err := lintCollectFiles(root)
	if err != nil {
		lintFatal("%v", err)
	}
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "gox lint: %s 下没有找到 .js/.jsx/.ts/.tsx 文件\n", root)
		os.Exit(2)
	}

	var findings []lintFinding
	for _, f := range files {
		found, err := lintCheckFile(f, root, only, skip)
		if err != nil {
			// 单个文件解析失败不该让整个 lint 失效 —— 那会让"修到一半"的
			// 工程拿不到任何报告。报出来，继续跑别的。
			fmt.Fprintf(os.Stderr, "gox lint: 跳过 %s: %v\n", f.display, err)
			continue
		}
		findings = append(findings, found...)
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].file != findings[j].file {
			return findings[i].file < findings[j].file
		}
		return findings[i].line < findings[j].line
	})

	if jsonOut {
		lintPrintJSON(os.Stdout, root, findings)
	} else {
		lintPrintText(os.Stdout, root, len(files), findings)
	}

	if len(findings) > 0 {
		os.Exit(1)
	}
}

// lintValidateRules 校验 --only/--skip 给的规则名真实存在。
func lintValidateRules(only, skip map[string]bool) error {
	known := map[string]bool{}
	for _, r := range lintRules {
		known[r.ID] = true
	}
	for _, set := range []map[string]bool{only, skip} {
		for r := range set {
			if !known[r] {
				return fmt.Errorf("未知规则 %q (可用: %s)", r, strings.Join(lintRuleIDs(), ", "))
			}
		}
	}
	return nil
}

func lintRuleIDs() []string {
	out := make([]string, 0, len(lintRules))
	for _, r := range lintRules {
		out = append(out, r.ID)
	}
	sort.Strings(out)
	return out
}

// lintCollectFiles 递归收集待检查文件。
func lintCollectFiles(root string) ([]*lintFile, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("路径 %s 无效: %w", root, err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("读不到 %s: %w", root, err)
	}
	var out []*lintFile
	if !info.IsDir() {
		out = append(out, &lintFile{path: absRoot, display: root})
		return out, nil
	}
	err = filepath.WalkDir(absRoot, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 单个不可读目录不该杀掉整个 lint
		}
		if d.IsDir() {
			if p != absRoot && lintSkipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !lintExts[strings.ToLower(filepath.Ext(p))] {
			return nil
		}
		rel, rerr := filepath.Rel(absRoot, p)
		if rerr != nil {
			rel = p
		}
		out = append(out, &lintFile{path: p, display: rel})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

// lintCheckFile 检查单个文件。
func lintCheckFile(f *lintFile, root string, only, skip map[string]bool) ([]lintFinding, error) {
	src, err := os.ReadFile(f.path)
	if err != nil {
		return nil, err
	}

	f.srcPos = func(line, col int) (int, int) { return line, col }
	if tstransform.IsTS(f.path) {
		res, terr := tstransform.Transform(src, f.path)
		if terr != nil {
			return nil, terr
		}
		src = res.Code
		// 只接 MapPos（带列），不用 MapLine（只有行）：类型剥离会让两行合并成
		// 一行，行级映射在那种地方必然偏 1。映射不到时降级用 JS 行列 —— 宁可
		// 偏一点，也不假报精确。
		if lm := res.LineMap; lm != nil && !lm.IsEmpty() {
			f.srcPos = func(line, col int) (int, int) {
				if col <= 0 {
					col = 1
				}
				if sl, sc, ok := lm.MapPos(line, col); ok && sl > 0 {
					return sl, sc
				}
				return line, col
			}
		}
	}

	relForLib := f.display
	if filepath.IsAbs(relForLib) {
		relForLib = f.path
	}
	// lib/ 判定用**相对项目根**的路径：否则任何位于绝对路径 /lib/ 下的工程
	// 都会被误判成"在 lib/ 里"。
	f.inLib = false
	for _, part := range strings.Split(filepath.ToSlash(relForLib), "/") {
		if part == "lib" {
			f.inLib = true
			break
		}
	}

	p := parser.New(lexer.New(string(src)))
	prog := p.ParseProgram()
	if errs := p.Errors(); errs.HasErrors() {
		return nil, fmt.Errorf("解析失败: %s", errs.String())
	}

	var out []lintFinding
	emit := func(rule, msg string, line, col int) {
		if len(only) > 0 && !only[rule] {
			return
		}
		if skip[rule] {
			return
		}
		if line <= 0 {
			line = 1
		}
		sl, sc := f.srcPos(line, col)
		out = append(out, lintFinding{file: f.display, line: sl, column: sc, rule: rule, message: msg})
	}

	// —— 规则 4：lib/ 不许 import gox（与 h() 无关，单独走一遍）——
	if f.inLib {
		for _, st := range prog.Statements {
			imp, ok := st.(*ast.ImportDeclaration)
			if !ok {
				continue
			}
			if lintIsGoxModule(imp.Source) {
				emit("lib-imports-gox", fmt.Sprintf(
					"lib/ 下 import 了 %q —— lib/ 要保持纯逻辑（不碰 UI、不碰 signal），"+
						"否则脱离 UI 单测这条路就断了", imp.Source),
					imp.Token.Line, imp.Token.Column)
			}
		}
	}

	// —— 规则 1/2/3：都在 h(...) 调用上 ——
	lintWalkExprs(prog, func(e ast.Expression) {
		call, ok := e.(*ast.CallExpression)
		if !ok {
			return
		}
		fn, ok := call.Function.(*ast.Identifier)
		if !ok || fn.Value != "h" {
			return
		}
		// 只有"元素标签"形态才是 JSX 降级产物：h(字符串标签, props, ...children)。
		// h(Counter, ...) 这种组件调用不是，别插手。
		if len(call.Arguments) < 2 {
			return
		}
		if _, ok := call.Arguments[0].(*ast.StringLiteral); !ok {
			return
		}

		// 规则 3：each 无 key
		if props, ok := call.Arguments[1].(*ast.ObjectLiteral); ok {
			hasEach, hasKey := false, false
			eachLine, eachCol := 0, 0
			for _, prop := range props.Properties {
				// Key 是 Expression（{a:1} 是 Identifier，{"a-b":1} 是 StringLiteral）
				switch k := prop.Key.(type) {
				case *ast.Identifier:
					switch k.Value {
					case "each":
						hasEach, eachLine, eachCol = true, prop.Token.Line, prop.Token.Column
					case "key":
						hasKey = true
					}
				case *ast.StringLiteral:
					switch k.Value {
					case "each":
						hasEach, eachLine, eachCol = true, prop.Token.Line, prop.Token.Column
					case "key":
						hasKey = true
					}
				}
			}
			if hasEach && !hasKey {
				emit("each-no-key",
					"each 列表没有 key —— 更新时只能整组重建，keyed 复用与 fallback "+
						"都失效（apps/README 第 3 条）", eachLine, eachCol)
			}
		}

		// 规则 1/2：子节点（第 3 个及以后的实参）
		for _, child := range call.Arguments[2:] {
			switch c := child.(type) {
			case *ast.CallExpression:
				// —— 两类调用**不是**快照，必须排除，否则这条规则会变成噪声 ——
				//
				// 1. 嵌套的 JSX 元素：`<column><text>{n()}</text></column>` 降级
				//    成 h("column", null, h("text", null, n()))，外层 h 的第 3 个
				//    实参就是内层那个 h(...) 调用。不排除的话，每一个嵌套元素
				//    都会被当成快照 —— 那就是满屏误报。
				// 2. 组件调用：`<Toolbar/>` 降级成 Toolbar(...)，它返回的是元素而
				//    不是"某个值"。约定是**大写开头**，据此排除。
				if id, ok := c.Function.(*ast.Identifier); ok {
					if id.Value == "h" {
						continue
					}
					if r := []rune(id.Value); len(r) > 0 && unicode.IsUpper(r[0]) {
						continue
					}
				}
				// 剩下的是小写函数调用 —— signal getter 嫌疑（{sig()} 形态）
				name := "<调用>"
				if id, ok := c.Function.(*ast.Identifier); ok {
					name = id.Value + "()"
				} else if m, ok := c.Function.(*ast.MemberExpression); ok {
					name = m.String() + "()"
				}
				// 行号取**子节点自己**的位置而不是整个 h() 调用：一个跨多行的
				// JSX 元素里，外层 h( 的 token 往往在上几行，报它等于让人从
				// "<column>" 开始猜。
				emit("snapshot-child", fmt.Sprintf(
					"子节点 %s 是**调用**, 它的值在进入 h() 之前就求值完了, 之后信号"+
						"变了界面也不动。要订阅更新请写 {%s} —— 或 {() => %s}",
					name, strings.TrimSuffix(name, "()"), name),
					c.Token.Line, c.Token.Column)
			case *ast.StringLiteral:
				// 规则 2：子节点区的 // 不是注释
				if strings.HasPrefix(strings.TrimSpace(c.Value), "//") {
					emit("slash-comment",
						"子节点区的 // 不是注释, 它会被当成一个文本子节点渲染出来 —— "+
							"注释要写 {/* … */} 或挪到 JSX 外面（apps/README 第 2 条）",
						c.Token.Line, c.Token.Column)
				}
			}
		}
	})

	return out, nil
}

// lintIsGoxModule 报告模块路径是否是 gox 内置模块（gx/* 或 gox）。
func lintIsGoxModule(src string) bool {
	return src == "gox" || strings.HasPrefix(src, "gx/")
}

// lintWalkExprs 递归遍历 AST，对每个表达式调用 fn。
//
// 用反射而不是枚举所有节点类型：ast 包有上百个节点类型，手写遍历器每加一种
// 语法就要记得来这里补一次 —— 那是个**必然会漏**的维护负担，而漏掉的语法糖
// 会让 lint 安静地放过一整类代码，比报错糟得多。反射的代价（lint 只在手动跑）
// 换"新增语法自动被覆盖"，划算。
//
// seen 防环：遍历器不看字段语义，只要节点间存在回指就会无限递归。
func lintWalkExprs(v interface{}, fn func(ast.Expression)) {
	lintWalkValue(reflect.ValueOf(v), fn, map[uintptr]bool{})
}

func lintWalkValue(rv reflect.Value, fn func(ast.Expression), seen map[uintptr]bool) {
	if !rv.IsValid() {
		return
	}
	switch rv.Kind() {
	case reflect.Ptr, reflect.Interface:
		if rv.IsNil() {
			return
		}
		if rv.Kind() == reflect.Ptr {
			p := rv.Pointer()
			if seen[p] {
				return
			}
			seen[p] = true
		}
		lintWalkValue(rv.Elem(), fn, seen)
	case reflect.Slice, reflect.Array:
		for i := 0; i < rv.Len(); i++ {
			lintWalkValue(rv.Index(i), fn, seen)
		}
	case reflect.Struct:
		for i := 0; i < rv.NumField(); i++ {
			// 未导出字段不能 Interface()（会 panic），但它的子元素仍可能是
			// 导出的，所以照常递归进去，只在最后取接口时判断。
			lintWalkValue(rv.Field(i), fn, seen)
		}
	}
	// 只在**指针层**回调：AST 表达式节点都是指针，而它们常被装在
	// []Expression 这样的接口切片里 —— 接口层与指针层各回调一次会让每条
	// 命中报两遍。
	if rv.Kind() == reflect.Ptr && rv.CanInterface() {
		if e, ok := rv.Interface().(ast.Expression); ok {
			fn(e)
		}
	}
}

func lintPrintText(w *os.File, root string, nfiles int, findings []lintFinding) {
	fmt.Fprintf(w, "gox lint: 扫描 %s（%d 个文件）\n\n", root, nfiles)
	if len(findings) == 0 {
		fmt.Fprintf(w, "ok  没有命中（规则: %s）\n", strings.Join(lintRuleIDs(), ", "))
		return
	}
	byRule := map[string]int{}
	for _, f := range findings {
		byRule[f.rule]++
		fmt.Fprintf(w, "%s\n", f.String())
	}
	rules := make([]string, 0, len(byRule))
	for r := range byRule {
		rules = append(rules, r)
	}
	sort.Strings(rules)
	fmt.Fprintf(w, "\n共 %d 处命中：", len(findings))
	for i, r := range rules {
		if i > 0 {
			fmt.Fprintf(w, "，")
		}
		fmt.Fprintf(w, " %s ×%d", r, byRule[r])
	}
	fmt.Fprintf(w, "\n\n这些规则全部出自 apps/README.md §写应用时必须守的几条 —— "+
		"它们的共同点是违反后**不报错**，只会静默失效。\n")
}

func lintPrintJSON(w *os.File, root string, findings []lintFinding) {
	fmt.Fprintf(w, "{\n  \"root\": %q,\n  \"count\": %d,\n  \"findings\": [\n", root, len(findings))
	for i, f := range findings {
		comma := ","
		if i == len(findings)-1 {
			comma = ""
		}
		fmt.Fprintf(w, "    {\"file\": %q, \"line\": %d, \"rule\": %q, \"message\": %q}%s\n",
			f.file, f.line, f.rule, f.message, comma)
	}
	fmt.Fprintf(w, "  ]\n}\n")
}

func lintFatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "gox lint: "+format+"\n", args...)
	os.Exit(2)
}

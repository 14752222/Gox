// Command initpurity 是「包级 init() 不得做平台 IO / 宏内核调用」的静态闸门。
//
// # 为什么有这个工具
//
// 2026-10-08 的事故：gfx/win32 的 init() 在进程启动阶段枚举网卡，结构体布局
// 错位时连 test262 跑批都启动即 SIGSEGV（fault addr 恒定 0x322b，跨两周的四个
// 旧二进制全崩）。单点已修（改成懒加载），但没有任何机制阻止下一次 —— 崩溃面
// 被从「单个能力」放大到「全部 CLI 用法」，包括本该与 GUI 完全无关的
// `gox run` / test262。
//
// 教训是结构性的：**init() 是被链接进来的包无条件执行的代码，没有调用点、
// 没有开关、也没有 recover 的机会**。任何一次平台调用放进去，就等价于「所有
// 二进制在 main 之前都必须先成功完成这次平台 IO」。这个约束不该靠人记住，
// 要靠机器。
//
// # 判据
//
// 对每个包级 `func init()`，沿**本包内的调用链**做可达性分析（init → helper →
// …），链上任意一跳出现下列调用即违规：
//
//   - 平台后端包：`…/gfx/win32`、`…/gfx/cocoa`、`…/gfx/x11`、`…/gfx/android`、
//     `…/gfx/harmony` 等（按导入路径末段判定，见 platformPkgs）
//   - 平台系统调用 / 进程 / 网络：syscall、os/exec、net 及其子包
//   - 宏内核入口（见 kernelEntryFuncs）：在 init 里起一个调度器，等于让
//     「import 这个包」变成「启动一个运行时」
//
// 为什么连 helper 都要追：直接把调用写进 init 是少数，多数情况是 init 调
// `setupX()`、setupX 再调平台 API —— 只查 init 函数体本身会漏掉这一类，
// 而 rMWHi1 正是这个形态（init → 上报 → 枚举网卡）。
//
// 为什么**不看 build tag**：windows-only 的 init 在 Linux 上不编译，但它照样
// 会在 Windows 上炸。本工具解析所有 .go 源文件，与构建平台无关。
//
// # 误报与豁免
//
// 判据是**黑名单**，不是「init 里什么都不许干」—— 注册表式 init（登记组件、
// 登记内建模块、装 map）是这个仓库的正常写法，一律放行。
//
// 确需豁免的位点，在违规那一行行尾或紧邻上行登记，必须带理由：
//
//	somePlatformCall() // initpurity:allow 理由：xxx
//
// 没有理由的 allow 记不住当时为什么放行，会被后人当成「这里可以随便加」。
// 缺理由时工具按违规处理，并指出「豁免缺理由」。
//
// # 用法
//
//	go run ./tools/initpurity ./...                              # 扫全仓（CI 姿势）
//	go run ./tools/initpurity ./gfx/...                          # 只扫子树
//	go run ./tools/initpurity ./tools/initpurity/testdata/bad    # 自测用样本
//
// 退出码：0 = 通过；1 = 存在违规；2 = 工具自身出错（解析失败等，同样视为红）。
// 输出含「文件:行号 + 完整调用链」，便于直接从 CI 日志跳到源码。
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// platformPkgs：本仓库内「导入路径末段命中即为平台后端包」的集合。
// 判末段而不是全路径，是为了让未来新增的 gfx/<新平台> 自动进闸 ——
// 新平台包本来就是最该被管住的那一类。
var platformPkgs = map[string]bool{
	"win32":   true,
	"cocoa":   true,
	"x11":     true,
	"android": true,
	"harmony": true,
	"ohos":    true,
	"ios":     true,
	"wayland": true,
}

// platformImportPrefixes：平台 / 宏内核侧的标准库与三方包前缀。
var platformImportPrefixes = []string{
	"syscall",
	"os/exec",
	"net", // net 及 net/http 等子包：监听/拨号都算平台 IO
	"golang.org/x/sys",
	"golang.org/x/mobile",
}

// kernelEntryFuncs：宏内核入口函数名 → 命中理由。
// 在 init 里调用它们 = 替使用者决定「什么时候启动运行时」，
// 而 import 一个包不该有这个副作用。
var kernelEntryFuncs = map[string]string{
	"RunApp":        "宏内核入口：启动应用事件循环",
	"StartGUI":      "宏内核入口：启动 GUI 线程",
	"Bootstrap":     "宏内核入口：引导运行时",
	"StartLoop":     "宏内核入口：启动循环",
	"RunEventLoop":  "宏内核入口：启动事件循环",
	"StartHostLoop": "宏内核入口：启动宿主循环",
}

// skipDirs：不参与扫描的目录。
// testdata 单独说明：里面放着**故意违规**的样本（自测用），默认扫描必须跳过，
// 否则本工具永远红；自测时由测试显式传入具体路径。
var skipDirs = map[string]bool{
	".git": true, ".mimosa": true, ".workbuddy": true, ".idea": true,
	"dist": true, "node_modules": true, "vendor": true, "testdata": true,
	"website": true, "npm": true, "gox-logo-concepts": true,
}

type violation struct {
	file string
	line int
	call string // 命中那一条的具体调用文本
	why  string // 为什么算违规
	path string // init → …→ 命中点
}

func main() {
	roots := os.Args[1:]
	if len(roots) == 0 {
		roots = []string{"./..."}
	}

	dirs, err := expandRoots(roots)
	if err != nil {
		fmt.Fprintf(os.Stderr, "initpurity: %v\n", err)
		os.Exit(2)
	}

	var all []violation
	for _, dir := range dirs {
		v, err := checkDir(dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "initpurity: %v\n", err)
			os.Exit(2)
		}
		all = append(all, v...)
	}

	sort.SliceStable(all, func(i, j int) bool {
		if all[i].file != all[j].file {
			return all[i].file < all[j].file
		}
		return all[i].line < all[j].line
	})

	if len(all) == 0 {
		fmt.Printf("initpurity: ok —— %d 个包，init() 里没有平台 IO / 宏内核调用\n", len(dirs))
		return
	}

	fmt.Printf("initpurity: %d 处 init() 违规 —— init 在 main 之前无条件执行，"+
		"平台调用会把「这个能力崩了」放大成「进程起不来」\n\n", len(all))
	for _, v := range all {
		fmt.Printf("  %s:%d\n", v.file, v.line)
		fmt.Printf("      调用链：%s\n", v.path)
		fmt.Printf("      命中：%s\n", v.call)
		fmt.Printf("      判据：%s\n\n", v.why)
	}
	fmt.Print("如何放行：把平台调用挪出 init（懒加载 / 显式调用点）；" +
		"确属必要的，在违规行加 `// initpurity:allow 理由：…`（理由必填）。\n")
	os.Exit(1)
}

// expandRoots 把模式展开成目录列表。刻意只支持两种写法：字面目录与 `dir/...`。
// 不需要 go/packages 的完整语义（build tag 求值、module 解析），而且如上所述，
// 本工具**故意忽略 build tag**。
func expandRoots(roots []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	for _, r := range roots {
		clean := filepath.Clean(r)
		if strings.HasSuffix(clean, "...") {
			clean = strings.TrimSuffix(clean, "...")
			clean = strings.TrimSuffix(filepath.Clean(clean), string(filepath.Separator))
			if clean == "" || clean == "." {
				clean = "."
			}
			err := filepath.Walk(clean, func(p string, info os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				if info.IsDir() && skipDirs[filepath.Base(p)] {
					return filepath.SkipDir
				}
				if !info.IsDir() {
					return nil
				}
				gs, _ := filepath.Glob(filepath.Join(p, "*.go"))
				if len(gs) == 0 {
					return nil // 没有 .go 的目录不算包，别让报告里的包数虚高
				}
				if !seen[p] {
					seen[p] = true
					out = append(out, p)
				}
				return nil
			})
			if err != nil {
				return nil, fmt.Errorf("遍历 %s 失败：%w", r, err)
			}
			continue
		}
		if !seen[clean] {
			seen[clean] = true
			out = append(out, clean)
		}
	}
	sort.Strings(out)
	return out, nil
}

type funcDecl struct {
	decl *ast.FuncDecl
	file *ast.File
}

// pkg 是单包的分析上下文。
type pkg struct {
	fset  *token.FileSet
	name  string
	funcs map[string]funcDecl // 包级函数名 → 声明（含所属文件，用于取 import 表）
}

func checkDir(dir string) ([]violation, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		if fi.IsDir() {
			return false
		}
		// 跳测试文件：测试里的 init 只影响测试进程，不会进产物二进制
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		// 语法错误不该让闸门假装通过：解析失败 = 红
		return nil, fmt.Errorf("解析 %s 失败：%w", dir, err)
	}

	var out []violation
	names := make([]string, 0, len(pkgs))
	for n := range pkgs {
		names = append(names, n)
	}
	sort.Strings(names)

	for _, name := range names {
		p := &pkg{fset: fset, name: name, funcs: map[string]funcDecl{}}
		var inits []funcDecl
		files := make([]*ast.File, 0, len(pkgs[name].Files))
		for _, f := range pkgs[name].Files {
			files = append(files, f)
		}
		sort.Slice(files, func(i, j int) bool {
			return fset.Position(files[i].Pos()).Filename < fset.Position(files[j].Pos()).Filename
		})
		// 先建全包符号表：init 调到的 helper 可能在同包的另一个文件里
		for _, f := range files {
			for _, d := range f.Decls {
				fn, ok := d.(*ast.FuncDecl)
				if !ok || fn.Recv != nil || fn.Name == nil {
					continue
				}
				if fn.Name.Name == "init" {
					inits = append(inits, funcDecl{fn, f})
					continue
				}
				if _, exists := p.funcs[fn.Name.Name]; !exists {
					p.funcs[fn.Name.Name] = funcDecl{fn, f}
				}
			}
		}
		for _, init := range inits {
			out = append(out, p.walk(init, nil, map[string]bool{})...)
		}
	}
	return out, nil
}

// walk 从 init（或链上任一函数）出发收集可达的违规调用。
// path 是到达 fn 之前的调用链（不含 fn 自身）；visiting 防环。
func (p *pkg) walk(fn funcDecl, path []string, visiting map[string]bool) []violation {
	if fn.decl == nil || fn.decl.Body == nil {
		return nil
	}
	name := fn.decl.Name.Name
	if visiting[name] {
		return nil // 相互递归的 helper 不该把工具挂死
	}
	visiting[name] = true
	defer delete(visiting, name)

	cur := append(append([]string{}, path...), name)
	imports := collectImports(fn.file)
	comments := commentLines(p.fset, fn.file)

	var out []violation
	var visit func(ast.Node) bool
	visit = func(n ast.Node) bool {
		// 闭包体**默认不进**：init 里出现 func(){…} 绝大多数是「注册一个回调」
		// （object.NewBuiltin / RegisterBuiltinModule 那一族），它在用户调用时才
		// 跑，不是 init 时跑 —— 而且这正是平台调用该去的正确位置。把所有闭包体
		// 都算进 init 的可达集，会把整个 stdlib 全判违规（实测 11 处全是这一类）。
		// 唯一的例外是立即调用表达式 `func(){…}()`：它确实在 init 里同步执行。
		if fl, ok := n.(*ast.FuncLit); ok {
			_ = fl
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		pos := p.fset.Position(call.Pos())
		chain := strings.Join(append(append([]string{}, cur...), callText(call)), " → ")

		// IIFE：`func(){ … }()` —— 同步执行，按 init 的一部分看
		if fl, isIIFE := call.Fun.(*ast.FuncLit); isIIFE {
			ast.Inspect(fl.Body, visit)
			return false
		}

		switch fun := call.Fun.(type) {
		case *ast.SelectorExpr:
			if id, isIdent := fun.X.(*ast.Ident); isIdent {
				// 1) 平台包调用：pkg.Fn(...)
				if impPath, isImport := imports[id.Name]; isImport {
					if why, bad := platformImportWhy(impPath); bad {
						if v := p.hit(pos, callText(call), why, chain, comments); v.why != "" {
							out = append(out, v)
						}
						return true
					}
				}
				// 2) 宏内核入口：不管 X 是不是平台包都拦
				if why, bad := kernelEntryFuncs[fun.Sel.Name]; bad {
					if v := p.hit(pos, callText(call), why, chain, comments); v.why != "" {
						out = append(out, v)
					}
				}
			}
		case *ast.Ident:
			// 3) 本包函数：继续下钻（rMWHi1 的形态：init → helper → 平台 API）
			if next, exists := p.funcs[fun.Name]; exists && !visiting[fun.Name] {
				out = append(out, p.walk(next, cur, visiting)...)
			}
		}
		return true
	}
	ast.Inspect(fn.decl.Body, visit)
	return out
}

// hit 生成一条违规；命中点自带「行尾豁免」时降级为通过。
func (p *pkg) hit(pos token.Position, call, why, chain string, comments map[int]string) violation {
	v := violation{file: pos.Filename, line: pos.Line, call: call, why: why, path: chain}
	if txt, ok := comments[pos.Line]; ok {
		reason, has, needReason := allowReason(txt)
		if has {
			if needReason {
				v.why = why + "；且豁免注释缺理由（`// initpurity:allow 理由：…`），按违规处理"
			} else {
				v.why = "" // 已豁免
			}
			v.call = call + "  // 豁免：" + reason
		}
	}
	return v
}

// platformImportWhy 判定导入路径是否属于平台 IO / 宏内核侧，并给出理由。
func platformImportWhy(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	base := path
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if strings.HasPrefix(path, "github.com/14752222/Gox/") && platformPkgs[base] {
		return fmt.Sprintf("平台后端包 %s —— 它只在对应平台上有意义，"+
			"在 init 里调用等于要求所有平台的二进制在 main 之前先完成一次平台调用", path), true
	}
	for _, pre := range platformImportPrefixes {
		if path == pre || strings.HasPrefix(path, pre+"/") {
			return fmt.Sprintf("平台 IO 包 %s —— init 里做系统调用 / 进程 / 网络，"+
				"失败会让进程直接起不来，而不是让某个能力不可用", path), true
		}
	}
	return "", false
}

// allowReason 解析豁免注释。返回 (理由, 是否豁免注释, 是否缺理由)。
func allowReason(text string) (string, bool, bool) {
	t := strings.TrimSpace(text)
	t = strings.TrimPrefix(t, "//")
	t = strings.TrimSpace(t)
	if !strings.HasPrefix(t, "initpurity:allow") {
		return "", false, false
	}
	reason := strings.TrimSpace(strings.TrimPrefix(t, "initpurity:allow"))
	reason = strings.TrimSpace(strings.TrimPrefix(reason, "理由："))
	return reason, true, reason == ""
}

// commentLines 把文件里的注释按行号索引，供行尾豁免匹配。
func commentLines(fset *token.FileSet, f *ast.File) map[int]string {
	m := map[int]string{}
	if f == nil {
		return m
	}
	for _, cg := range f.Comments {
		for _, c := range cg.List {
			m[fset.Position(c.Pos()).Line] = c.Text
		}
	}
	return m
}

// collectImports 抽出「文件内可用名字 → 导入路径」。
// 没写别名的按路径末段算（Go 默认规则）；`_`/`.` 导入不入表。
func collectImports(f *ast.File) map[string]string {
	m := map[string]string{}
	if f == nil {
		return m
	}
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if imp.Name != nil {
			if imp.Name.Name == "_" || imp.Name.Name == "." {
				continue
			}
			m[imp.Name.Name] = path
			continue
		}
		base := path
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		m[base] = path
	}
	return m
}

// callText 把调用点渲染成 `pkg.Fn()` / `Fn()` 文本。
func callText(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		if id, ok := fun.X.(*ast.Ident); ok {
			return fmt.Sprintf("%s.%s()", id.Name, fun.Sel.Name)
		}
		return fun.Sel.Name + "()"
	case *ast.Ident:
		return fun.Name + "()"
	}
	return "(调用)"
}

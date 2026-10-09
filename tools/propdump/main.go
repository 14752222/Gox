// Command propdump 从 gfx/*.go 的**属性读取点**反推「内核认得哪些属性 / 事件」，
// 供 scripts/gen-props-golden.py 生成 docs/props.golden.json（看板 rgQsDD）。
//
// ## 为什么是"反推"而不是抄一张表
//
// 约 60 个标签 × 各自认识的 prop/事件，手抄必然错；而抽错的代价不是"少了条
// 警告"，是**误报一个真在用的属性** —— 用户会因此把整个开关关掉，那比不做
// 更糟。所以判据只有一条硬规矩：**只统计读点**。
//
//   - `n.PropNum("width")` / `n.Props["width"]` 是读 ⇒ 内核认 width；
//   - `n.Props["width"] = v` 是写 ⇒ 只说明有人存它，不说明有人读它。
//
// ## 三条口径（与生成脚本共用，改这里要一起改）
//
//  1. 读点要**归到标签**。读点散在 layoutButton / drawButton 这些函数里，靠
//     调用图回推：`case "button": drawButton(n)` ⇒ drawButton 只在 button 下
//     被调用。标签上下文取自 `switch x.Tag` 的 case 与 `if x.Tag == "t"`。
//  2. **判不出来就当通用**（all=true）。追踪不到的函数（没有调用点 / 经由函数
//     值调用 / 事件泵直接进来）一律记作"所有标签都读"。过宽只是漏报（少警告
//     一次），过窄才是误报 —— 误报是这个开关的头号死因。
//  3. 键名以**标识符**出现时（`n.PropNum(name)`），真正的名字要到调用点才拿到：
//     记下"第几个形参是属性名"，再把调用点的字面量实参回填（propBoolOr(n,
//     "autoplay", false) 就是这样被认出来的）。
//
// 用法：go run ./tools/propdump [dir]     （JSON 打到 stdout，默认 dir=gfx）
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 读点的种类。事件单独一档：它的报错文案与属性不同（"内核还没实现" vs "拼错了"）。
const (
	kindProp  = "prop"
	kindEvent = "event"
)

// 属性读取器（*GuiNode 的方法）：第 1 个实参就是属性名。
var nodeAccessors = map[string]string{
	"PropNum":     kindProp,
	"PropStr":     kindProp,
	"PropBool":    kindProp,
	"PropHas":     kindProp,
	"PropHandler": kindEvent,
}

// 包级读取 / 派发助手：属性名是它们第 idx 个实参（第 0 个是节点）。
var helpers = map[string]struct {
	idx  int
	kind string
}{
	"effectivePropNum":     {1, kindProp},
	"effectivePropNumOk":   {1, kindProp},
	"callHandler":          {1, kindEvent}, // render.go 的事件派发点
	"callHandlerWithPoint": {1, kindEvent},
	"handlerInChain":       {1, kindEvent}, // hittest.go
}

// tagSet 是一组标签。all=true 表示"所有标签"（判不出归属时的保守值）。
type tagSet struct {
	all  bool
	tags map[string]struct{}
}

func newTagSet() *tagSet { return &tagSet{tags: map[string]struct{}{}} }

func allTags() *tagSet { return &tagSet{all: true} }

func (t *tagSet) add(name string) {
	if name != "" {
		t.tags[name] = struct{}{}
	}
}

func (t *tagSet) empty() bool { return !t.all && len(t.tags) == 0 }

func (t *tagSet) union(o *tagSet) {
	if o == nil {
		return
	}
	if o.all {
		t.all = true
		return
	}
	for k := range o.tags {
		t.tags[k] = struct{}{}
	}
}

func (t *tagSet) eq(o *tagSet) bool {
	if t == nil || o == nil {
		return t == o
	}
	if t.all != o.all || len(t.tags) != len(o.tags) {
		return false
	}
	for k := range t.tags {
		if _, ok := o.tags[k]; !ok {
			return false
		}
	}
	return true
}

// site 是一个读取点。tags=nil 表示"用所属函数的标签集合"。
type site struct {
	name string
	kind string
	tags *tagSet
}

// call 是一个调用点，用于调用图与"字面量实参回填"。
type call struct {
	callee string
	args   []ast.Expr
	tags   *tagSet
}

// closure 是函数体内的局部闭包（`side := func(name string) int {…}`）。
type closure struct {
	params     []string
	propParams map[int]string // 形参下标 → 该位置被当成属性名用（值是种类）
}

type funcInfo struct {
	decl       *ast.FuncDecl
	name       string
	file       string
	params     []string
	self       map[string]bool // "自己的节点"：*GuiNode 类型的 receiver 与形参
	nodeParams map[int]bool    // 形参下标 → 该位置是 *GuiNode（调用图判"读的是谁"用）
	propParams map[int]string
	sites      []site
	calls      []call
	isRoot     bool // 含 `switch x.Tag` ⇒ 按标签分派的根
	reach      *tagSet
	closures   map[string]*closure
	rangeLits  map[string][]string // range 变量 → []string{…} 里的字面量
	writes     map[*ast.IndexExpr]struct{}
}

type inEdge struct {
	caller  *funcInfo
	tags    *tagSet
	unknown bool // 调用时传进去的不是调用方"自己的节点" ⇒ 被调方读的是谁判不出
}

// otherNode 报告这个实参是不是"别的节点的属性"（子节点 / 父节点 / 临时变量）。
//
// 布局代码大量读**子节点**的属性：`for _, c := range n.Children { c.PropNum(
// "flexGrow") }` —— 这个读点归属的标签是"子节点的标签"，而它是谁这里判不出
// （layoutStack 只在 column 下跑，但它的子节点可以是任何标签）。把它记到
// column 名下，`<button flexGrow>` 就会被判成未知属性 —— 正是要消灭的误报。
func (fi *funcInfo) otherNode(e ast.Expr) bool { return !fi.isSelf(e) }

func (fi *funcInfo) isSelf(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && fi.self[id.Name]
}

// recvTags 返回读点该用的标签集合。读"别人的节点" ⇒ 通用。
func (fi *funcInfo) recvTags(recv ast.Expr, ts *tagSet) *tagSet {
	if recv != nil && !fi.isSelf(recv) {
		return allTags()
	}
	return ts
}

// agg 是一个名字（属性或事件）的汇总。
type agg struct {
	all  bool
	tags map[string]struct{}
}

type entry struct {
	Name string   `json:"name"`
	All  bool     `json:"all"`
	Tags []string `json:"tags"`
}

type output struct {
	Tags   []string `json:"tags"`
	Props  []entry  `json:"props"`
	Events []entry  `json:"events"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "propdump:", err)
		os.Exit(1)
	}
}

func run() error {
	args := os.Args[1:]
	debug := false
	if len(args) > 0 && args[0] == "-debug" {
		debug = true
		args = args[1:]
	}
	dir := "gfx"
	if len(args) > 0 {
		dir = args[0]
	}
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		return err
	}
	if len(pkgs) != 1 {
		return fmt.Errorf("%s 下应有且只有一个 Go 包，实际 %d 个", dir, len(pkgs))
	}

	var funcs []*funcInfo
	tags := map[string]struct{}{}
	for _, pkg := range pkgs {
		for path, f := range pkg.Files {
			collectTags(f, tags)
			for _, d := range f.Decls {
				if fd, ok := d.(*ast.FuncDecl); ok {
					funcs = append(funcs, newFuncInfo(fd, filepath.Base(path)))
				}
			}
		}
	}
	// parser 返回的是 map（迭代序不定）：排序只为让调试输出与不动点收敛顺序
	// 可复现 —— 不动点结果与顺序无关，但确定性本身就值得买。
	sort.Slice(funcs, func(i, j int) bool {
		if funcs[i].file != funcs[j].file {
			return funcs[i].file < funcs[j].file
		}
		return funcs[i].decl.Pos() < funcs[j].decl.Pos()
	})

	for _, fi := range funcs {
		// 顶层传 nil = "这个函数的标签归属由调用方决定"（见 propagate）。
		// 传 allTags() 会让函数体内每个调用点都带"任意标签"，传播一层就把
		// 整张图染成通用 —— 那是把"判不出"误当成了"真的是通用"。
		fi.walk(fi.decl.Body, nil)
	}
	// 回填：键名以形参形式传递时，真正的名字在调用点的字面量实参上
	byName := map[string][]*funcInfo{}
	for _, fi := range funcs {
		byName[fi.name] = append(byName[fi.name], fi)
	}
	for _, fi := range funcs {
		for _, c := range fi.calls {
			for _, callee := range byName[c.callee] {
				if callee == fi {
					continue // 递归不贡献新信息
				}
				tags := c.tags
				if callee.calledWithOtherNode(fi, c) {
					tags = allTags() // 传进去的是别的节点：读的是谁判不出
				}
				for i, a := range c.args {
					kind, ok := callee.propParams[i]
					if !ok {
						continue
					}
					if s := litString(a); s != "" {
						fi.addSite(s, kind, tags)
					}
				}
			}
		}
	}

	propagate(funcs)
	if debug {
		dumpReach(funcs)
	}
	out := emit(funcs, tags)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func newFuncInfo(fd *ast.FuncDecl, file string) *funcInfo {
	fi := &funcInfo{
		decl:       fd,
		name:       fd.Name.Name,
		file:       file,
		self:       map[string]bool{},
		nodeParams: map[int]bool{},
		propParams: map[int]string{},
		closures:   map[string]*closure{},
		rangeLits:  map[string][]string{},
		writes:     map[*ast.IndexExpr]struct{}{},
	}
	if fd.Type.Params != nil {
		pos := 0 // 形参**位置**（一个 Field 可能有多个名字：`minName, maxName string`）
		for _, p := range fd.Type.Params.List {
			for _, n := range p.Names {
				if isNodeType(p.Type) {
					fi.nodeParams[pos] = true
				}
				fi.params = append(fi.params, n.Name)
				pos++
			}
		}
	}
	for _, f := range []*ast.FieldList{fd.Recv, fd.Type.Params} {
		if f == nil {
			continue
		}
		for _, p := range f.List {
			if !isNodeType(p.Type) {
				continue
			}
			for _, n := range p.Names {
				fi.self[n.Name] = true
			}
		}
	}
	return fi
}

// isNodeType 报告这个类型是不是节点（`*GuiNode`）。用它认"自己的节点"：
// 其余一切（子节点变量、父节点、临时节点）都算"别人的节点"。
func isNodeType(e ast.Expr) bool {
	switch t := e.(type) {
	case *ast.StarExpr:
		return isNodeType(t.X)
	case *ast.Ident:
		return t.Name == "GuiNode"
	}
	return false
}

// walk 以 ts 为当前标签集合走一段语法树，收集读点 / 调用点 / 标签上下文。
func (fi *funcInfo) walk(n ast.Node, ts *tagSet) {
	if n == nil {
		return
	}
	ast.Inspect(n, func(node ast.Node) bool {
		if node == nil {
			return false
		}
		switch v := node.(type) {
		case *ast.SwitchStmt:
			if isTagSwitch(v) {
				fi.isRoot = true
				fi.walkTagSwitch(v, ts)
				return false
			}
		case *ast.IfStmt:
			if lit, ok := tagEqLit(v.Cond); ok {
				if v.Init != nil {
					fi.walk(v.Init, ts)
				}
				bt := newTagSet()
				bt.add(lit)
				fi.walk(v.Body, bt)
				// else 臂是"标签不等于它"的那一侧：能落到哪些标签这里判不出，
				// 交给所属函数的标签集合（保守）。
				fi.walk(v.Else, ts)
				return false
			}
		case *ast.AssignStmt:
			fi.markWrites(v)
		case *ast.RangeStmt:
			fi.markRangeLits(v)
		case *ast.CallExpr:
			fi.visitCall(v, ts)
		case *ast.IndexExpr:
			if isPropsExpr(v.X) {
				if _, w := fi.writes[v]; w {
					return true // 写点：不算"内核认这个属性"
				}
				fi.arg(v.Index, kindProp, fi.recvTags(v.X.(*ast.SelectorExpr).X, ts))
			}
		}
		return true
	})
}

// walkTagSwitch 按 `case "button", "submit":` 的标签分派。
func (fi *funcInfo) walkTagSwitch(sw *ast.SwitchStmt, outer *tagSet) {
	for _, stmt := range sw.Body.List {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok {
			continue
		}
		ts := allTags()
		if len(cc.List) > 0 {
			cur := newTagSet()
			for _, e := range cc.List {
				cur.add(litString(e))
			}
			if !cur.empty() {
				ts = cur
			}
			// 外层已经限定了标签（如 if n.Tag=="x" 里的 switch）：取交集
			if outer != nil && !outer.all {
				inter := newTagSet()
				for k := range cur.tags {
					if _, ok := outer.tags[k]; ok {
						inter.add(k)
					}
				}
				if !inter.empty() {
					ts = inter
				}
			}
		}
		for _, s := range cc.Body {
			fi.walk(s, ts)
		}
	}
}

func (fi *funcInfo) markWrites(as *ast.AssignStmt) {
	for _, l := range as.Lhs {
		if ix, ok := l.(*ast.IndexExpr); ok && isPropsExpr(ix.X) {
			fi.writes[ix] = struct{}{}
		}
	}
	// 局部闭包：`side := func(name string) int {…}`
	if len(as.Lhs) == 1 && len(as.Rhs) == 1 {
		if id, ok := as.Lhs[0].(*ast.Ident); ok {
			if fl, ok := as.Rhs[0].(*ast.FuncLit); ok {
				fi.closures[id.Name] = newClosure(fl)
			}
		}
	}
}

func (fi *funcInfo) markRangeLits(rs *ast.RangeStmt) {
	cl, ok := rs.X.(*ast.CompositeLit)
	if !ok {
		return
	}
	var lits []string
	for _, e := range cl.Elts {
		if s := litString(e); s != "" {
			lits = append(lits, s)
		}
	}
	if len(lits) == 0 {
		return
	}
	for _, n := range []ast.Expr{rs.Key, rs.Value} {
		if id, ok := n.(*ast.Ident); ok && id.Name != "_" {
			fi.rangeLits[id.Name] = lits
		}
	}
}

func (fi *funcInfo) visitCall(v *ast.CallExpr, ts *tagSet) {
	var callee string
	var recv ast.Expr
	switch f := v.Fun.(type) {
	case *ast.Ident:
		callee = f.Name
	case *ast.SelectorExpr:
		callee = f.Sel.Name
		recv = f.X
	default:
		return // 函数值调用：追踪不到，它带的读点按"通用"处理
	}
	if kind, ok := nodeAccessors[callee]; ok && len(v.Args) > 0 {
		fi.arg(v.Args[0], kind, fi.recvTags(recv, ts))
	} else if h, ok := helpers[callee]; ok && len(v.Args) > h.idx {
		fi.arg(v.Args[h.idx], h.kind, fi.recvTags(v.Args[0], ts))
	}
	fi.calls = append(fi.calls, call{callee: callee, args: v.Args, tags: ts})
	// 局部闭包调用：`side("paddingTop")`
	if c, ok := fi.closures[callee]; ok {
		for i, a := range v.Args {
			if kind, ok := c.propParams[i]; ok {
				if s := litString(a); s != "" {
					fi.addSite(s, kind, ts)
				}
			}
		}
	}
}

// arg 处理"属性名"这个实参：字面量直接记，标识符留到调用点回填。
func (fi *funcInfo) arg(e ast.Expr, kind string, ts *tagSet) {
	switch a := e.(type) {
	case *ast.BasicLit:
		if s := litString(a); s != "" {
			fi.addSite(s, kind, ts)
		}
	case *ast.Ident:
		for i, p := range fi.params {
			if p == a.Name {
				fi.propParams[i] = kind
				return
			}
		}
		if lits, ok := fi.rangeLits[a.Name]; ok {
			for _, l := range lits {
				fi.addSite(l, kind, ts)
			}
		}
	}
}

func (fi *funcInfo) addSite(name, kind string, ts *tagSet) {
	fi.sites = append(fi.sites, site{name: name, kind: kind, tags: ts})
}

// calledWithOtherNode 报告这次调用是不是把"调用方自己的节点之外"的节点传了
// 进去（`clampDim(c, …)` 里 c 是子节点）。是的话被调方的读点归属判不出 ⇒ 通用。
func (callee *funcInfo) calledWithOtherNode(caller *funcInfo, c call) bool {
	for i, a := range c.args {
		if callee.nodeParams[i] && caller.otherNode(a) {
			return true
		}
	}
	return false
}

// newClosure 记下局部闭包"哪几个形参是属性名"。
func newClosure(fl *ast.FuncLit) *closure {
	c := &closure{propParams: map[int]string{}}
	if fl.Type.Params != nil {
		for _, p := range fl.Type.Params.List {
			for _, n := range p.Names {
				c.params = append(c.params, n.Name)
			}
		}
	}
	mark := func(e ast.Expr, kind string) {
		id, ok := e.(*ast.Ident)
		if !ok {
			return
		}
		for i, p := range c.params {
			if p == id.Name {
				c.propParams[i] = kind
			}
		}
	}
	// 只看这个闭包自己的体：外层函数的同名形参不是它要找的那个。
	ast.Inspect(fl.Body, func(node ast.Node) bool {
		switch v := node.(type) {
		case *ast.CallExpr:
			var callee string
			switch f := v.Fun.(type) {
			case *ast.Ident:
				callee = f.Name
			case *ast.SelectorExpr:
				callee = f.Sel.Name
			}
			if kind, ok := nodeAccessors[callee]; ok && len(v.Args) > 0 {
				mark(v.Args[0], kind)
			} else if h, ok := helpers[callee]; ok && len(v.Args) > h.idx {
				mark(v.Args[h.idx], h.kind)
			}
		case *ast.IndexExpr:
			if isPropsExpr(v.X) {
				mark(v.Index, kindProp)
			}
		}
		return true
	})
	return c
}

// propagate 沿调用图把标签集合从"按标签分派的根"传播到各个函数。
func propagate(funcs []*funcInfo) {
	byName := map[string][]*funcInfo{}
	for _, fi := range funcs {
		byName[fi.name] = append(byName[fi.name], fi)
	}
	incoming := map[*funcInfo][]inEdge{}
	for _, fi := range funcs {
		for _, c := range fi.calls {
			for _, callee := range byName[c.callee] {
				if callee == fi {
					continue
				}
				incoming[callee] = append(incoming[callee], inEdge{
					caller:  fi,
					tags:    c.tags,
					unknown: callee.calledWithOtherNode(fi, c),
				})
			}
		}
	}
	for _, fi := range funcs {
		if fi.isRoot || len(incoming[fi]) == 0 {
			// 分派根 / 没有调用点：它对任意标签都进得来 ⇒ 通用
			fi.reach = allTags()
		} else {
			fi.reach = newTagSet()
		}
	}
	// 单调增长，必然收敛（轮数只作保险）
	for round := 0; round < len(funcs)+4; round++ {
		changed := false
		for _, fi := range funcs {
			if fi.reach.all {
				continue
			}
			acc := newTagSet()
			for _, e := range incoming[fi] {
				if e.unknown {
					acc.all = true // 传进来的是别人的节点：读的是哪个标签判不出
					break
				}
				if e.tags != nil && !e.tags.empty() {
					acc.union(e.tags) // 调用点自带标签（在 case "button": 里调的）
					continue
				}
				acc.union(e.caller.reach)
			}
			if !acc.eq(fi.reach) {
				fi.reach = acc
				changed = true
			}
		}
		if !changed {
			break
		}
	}
	// 收尾：一个标签都没推出来 = 判不出归属 ⇒ 通用（过宽只漏报，过窄才误报）
	for _, fi := range funcs {
		if fi.reach.empty() {
			fi.reach = allTags()
		}
	}
}

// dumpReach 打出每个函数的标签归属（误报核对时用：一眼看出"为什么这个读取
// 点被判成通用"）。
func dumpReach(funcs []*funcInfo) {
	for _, fi := range funcs {
		if len(fi.sites) == 0 {
			continue
		}
		what := "all"
		if !fi.reach.all {
			ts := make([]string, 0, len(fi.reach.tags))
			for t := range fi.reach.tags {
				ts = append(ts, t)
			}
			sort.Strings(ts)
			what = strings.Join(ts, ",")
		}
		fmt.Fprintf(os.Stderr, "%s:%d\t%s\t%s\n", fi.file, fi.decl.Pos(), fi.name, what)
	}
}

func emit(funcs []*funcInfo, tags map[string]struct{}) output {
	props := map[string]*agg{}
	events := map[string]*agg{}
	for _, fi := range funcs {
		for _, s := range fi.sites {
			table := props
			if s.kind == kindEvent || strings.HasPrefix(s.name, "on") {
				table = events
			}
			a := table[s.name]
			if a == nil {
				a = &agg{tags: map[string]struct{}{}}
				table[s.name] = a
			}
			ts := s.tags
			if ts == nil {
				ts = fi.reach
			}
			if ts == nil || ts.all || ts.empty() {
				a.all = true
				continue
			}
			for k := range ts.tags {
				a.tags[k] = struct{}{}
			}
		}
	}
	out := output{}
	for t := range tags {
		out.Tags = append(out.Tags, t)
	}
	sort.Strings(out.Tags)
	out.Props = sortedEntries(props)
	out.Events = sortedEntries(events)
	return out
}

func sortedEntries(m map[string]*agg) []entry {
	out := make([]entry, 0, len(m))
	for name, a := range m {
		e := entry{Name: name, All: a.all}
		for t := range a.tags {
			e.Tags = append(e.Tags, t)
		}
		sort.Strings(e.Tags)
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ── 小工具 ──────────────────────────────────────────────────────────────────

func litString(e ast.Expr) string {
	b, ok := e.(*ast.BasicLit)
	if !ok || b.Kind != token.STRING || len(b.Value) < 2 || b.Value[0] != '"' {
		return ""
	}
	return b.Value[1 : len(b.Value)-1]
}

func isPropsExpr(e ast.Expr) bool {
	sel, ok := e.(*ast.SelectorExpr)
	return ok && sel.Sel != nil && sel.Sel.Name == "Props"
}

func isTagSwitch(sw *ast.SwitchStmt) bool {
	if sw.Tag == nil {
		return false
	}
	sel, ok := sw.Tag.(*ast.SelectorExpr)
	return ok && sel.Sel != nil && sel.Sel.Name == "Tag"
}

func tagEqLit(e ast.Expr) (string, bool) {
	bin, ok := e.(*ast.BinaryExpr)
	if !ok || bin.Op != token.EQL {
		return "", false
	}
	sel, ok := bin.X.(*ast.SelectorExpr)
	if !ok || sel.Sel == nil || sel.Sel.Name != "Tag" {
		return "", false
	}
	if s := litString(bin.Y); s != "" {
		return s, true
	}
	return "", false
}

// collectTags 取 gfx/node.go 的 knownTags（与 check-registries.py 同一真源）。
func collectTags(f *ast.File, into map[string]struct{}) {
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.VAR {
			continue
		}
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Names) == 0 || vs.Names[0].Name != "knownTags" {
				continue
			}
			for _, v := range vs.Values {
				cl, ok := v.(*ast.CompositeLit)
				if !ok {
					continue
				}
				for _, el := range cl.Elts {
					if kv, ok := el.(*ast.KeyValueExpr); ok {
						if s := litString(kv.Key); s != "" {
							into[s] = struct{}{}
						}
					}
				}
			}
		}
	}
}

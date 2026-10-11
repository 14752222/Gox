package compiler

import (
	"fmt"
	"sort"
	"strings"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/object"
)

func (c *Compiler) noteStaticImport(spec string) {
	if spec == "" {
		return
	}
	if c.staticSeen == nil {
		c.staticSeen = map[string]bool{}
	}
	if c.staticSeen[spec] {
		return
	}
	c.staticSeen[spec] = true
	c.staticImports = append(c.staticImports, spec)
}

// Bytes 返回编译后的字节码。

func (c *Compiler) compileImportDeclaration(stmt *ast.ImportDeclaration) error {
	// 先校验"从内置模块导入的名字" (不发射任何指令就返回错误, 不留半截字节码)
	if err := checkBuiltinImportNames(stmt); err != nil {
		return err
	}
	// 登记为静态依赖: 模块加载器在运行本体前先求值全部静态依赖。
	c.noteStaticImport(stmt.Source)
	// 编译模块导入: 加载模块并绑定导出
	// OP_IMPORT 操作数 = 模块路径常量索引
	sourceIdx := c.constants.AddConstant(object.NewString(stmt.Source))
	c.emitter.Emit(bytecode.OP_IMPORT, sourceIdx)

	// 栈顶现在是模块的导出对象
	// 绑定导入
	if stmt.Namespace != "" {
		// import * as ns from "..."
		sym, err := c.declareOnce(stmt.Namespace, false, false)
		if err != nil {
			return err
		}
		if c.isGlobalScope() {
			c.emitGlobalDeclare(stmt.Namespace)
		} else {
			c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
		}
	} else if stmt.DefaultName != "" {
		// import defaultName from "..."
		// 获取 default 导出
		idx := c.constants.AddConstant(object.NewString("default"))
		c.emitter.Emit(bytecode.OP_GET_PROP, idx)
		sym, err := c.declareOnce(stmt.DefaultName, false, false)
		if err != nil {
			return err
		}
		if c.isGlobalScope() {
			c.emitGlobalDeclare(stmt.DefaultName)
		} else {
			c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
		}
		if len(stmt.NamedImports) > 0 {
			// 还需要命名导入
			// 重新加载模块 (或 DUP)
			c.emitter.Emit(bytecode.OP_IMPORT, sourceIdx)
			for _, item := range stmt.NamedImports {
				c.emitter.EmitNoOperand(bytecode.OP_DUP)
				// 取的是**模块导出名**, 绑定的是**本地名**
				nameIdx := c.constants.AddConstant(object.NewString(item.Imported))
				c.emitter.Emit(bytecode.OP_GET_PROP, nameIdx)
				sym, err := c.declareOnce(item.Local, false, false)
				if err != nil {
					return err
				}
				if c.isGlobalScope() {
					c.emitGlobalDeclare(item.Local)
				} else {
					c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
				}
			}
			c.emitter.EmitNoOperand(bytecode.OP_POP)
		}
	} else if len(stmt.NamedImports) > 0 {
		// import { a, b as c } from "..."
		for _, item := range stmt.NamedImports {
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
			// 取的是**模块导出名**, 绑定的是**本地名**
			nameIdx := c.constants.AddConstant(object.NewString(item.Imported))
			c.emitter.Emit(bytecode.OP_GET_PROP, nameIdx)
			sym, err := c.declareOnce(item.Local, false, false)
			if err != nil {
				return err
			}
			if c.isGlobalScope() {
				c.emitGlobalDeclare(item.Local)
			} else {
				c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
			}
		}
		c.emitter.EmitNoOperand(bytecode.OP_POP)
	} else {
		// 副作用导入: 丢弃模块对象
		c.emitter.EmitNoOperand(bytecode.OP_POP)
	}

	return nil
}

// ===== 内置模块的命名导入校验 =====

// checkBuiltinImportNames 校验"从内置模块导入的名字"确实在那个模块的导出表里。
//
// 命名导入在运行时只是一次 GET_PROP, 名字错了就**静默拿到 undefined** ——
// 症状离原因很远, 实测踩到的两类:
//
//	import { alert } from "gx/gfx";   // alert 在 gx/dialog ⇒ 调用时 "alert is not a function"
//	import { each } from "gx/view";   // each 是元素级指令, 没有任何模块导出它 ⇒ 静默失效
//	import x from "gox";              // 内置模块没有 default ⇒ x 恒为 undefined
//
// 内置模块的导出表在编译进程里是现成的 (object 注册表), 所以这条能在编译期拦住
// —— 报错里带上"它在哪个模块 / 是不是元素级指令 / 是不是拼错"。
//
// **只对已注册的内置模块生效**: 宿主没链接那个模块 (导出表不可知) 与文件模块
// 一律放行。也就是说这个检查只会让原本"编译通过但运行时静默出错"的程序变成
// 编译期报错, 不会改变任何能正常工作的程序的字节码。

func checkBuiltinImportNames(stmt *ast.ImportDeclaration) error {
	// 与 vm.loadModule 的保留命名空间一致: "gox" 与 "gx/..." 才是内置模块
	if stmt.Source != "gox" && !strings.HasPrefix(stmt.Source, "gx/") {
		return nil
	}
	exports, ok := object.LookupBuiltinModule(stmt.Source)
	if !ok {
		// 没注册 (宿主没链接 / 名字写错): 交给运行时 loadModule 报"未知的内置模块",
		// 那里会列出全部可用的模块名, 信息更全
		return nil
	}
	for _, item := range stmt.NamedImports {
		// 拿**模块导出名**去核对 —— 别名 (item.Local) 是本文件自己的名字,
		// 与模块导出表无关。若用别名核对, `{ createSignal as cs }` 会误报
		// "没有导出 cs"; 若把别名拆成多个 string, 则会误报 `没有导出 "as"`。
		name := item.Imported
		if _, ok := exports[name]; ok {
			continue
		}
		// 带别名时把本地名也报出来, 否则用户对着源码里的 `as cs` 会困惑
		// 报错为什么谈的是另一个名字。
		localHint := ""
		if item.Local != "" && item.Local != name {
			localHint = fmt.Sprintf(" (本地名 %s)", item.Local)
		}
		return fmt.Errorf("import {%s%s} from \"%s\": %s 没有导出 %q%s",
			name, localHint, stmt.Source, stmt.Source, name, importMissHint(stmt.Source, name, exports))
	}
	if stmt.DefaultName != "" {
		if _, ok := exports["default"]; !ok {
			return fmt.Errorf("import %s from \"%s\": 内置模块没有 default 导出%s",
				stmt.DefaultName, stmt.Source, importMissHint(stmt.Source, "default", exports))
		}
	}
	return nil
}

// importMissStaticHints 收那些"根本不是导出、但最容易被 import"的名字。
// 它们不在任何模块的导出表里, 所以只能在名字上硬编码 (与跨模块提示互斥:
// 跨模块命中的名字不会走到这里)。

var importMissStaticHints = map[string]string{
	"each":     "元素级指令: 写成 JSX 属性 each={rows}, 不从模块 import",
	"show":     "元素级指令: 写成 JSX 属性 show={cond}, 不从模块 import",
	"fallback": "元素级指令的配套属性: 写在带 each / show 的元素上",
	"key":      "元素级指令的配套属性: 写在带 each 的元素上",
	"stable":   "元素级指令的配套属性: 写在带 each 的元素上",
	"For":      "已改名为元素级指令 each (2026-09-20, 见 docs/gui-guide.md §8.2)",
	"Show":     "已改名为元素级指令 show (2026-09-20, 见 docs/gui-guide.md §8.2)",
	"window":   "window 是内置元素标签 <window>, 不是模块导出",
}

// importMissHint 拼出"这个不存在的导出名到底该怎么写"的提示后缀。
// 优先级: 跨模块 (它在 gx/dialog) > 静态提示 (元素级指令) > 拼写相近 > 列出可用导出。

func importMissHint(spec, name string, exports map[string]object.Value) string {
	for _, other := range object.RegisteredBuiltinModules() {
		if other == spec {
			continue
		}
		otherExports, ok := object.LookupBuiltinModule(other)
		if !ok {
			continue
		}
		if _, ok := otherExports[name]; ok {
			return fmt.Sprintf(" (它在 %s)", other)
		}
	}
	if hint, ok := importMissStaticHints[name]; ok {
		return " (" + hint + ")"
	}
	if near := nearestExportName(name, exports); near != "" {
		return fmt.Sprintf(" (想写的是 %q? 可用导出: %s)", near, exportNameList(exports))
	}
	return fmt.Sprintf(" (可用导出: %s)", exportNameList(exports))
}

// nearestExportName 在导出表里找与 name 最接近的名字 (大小写差异或编辑距离 ≤2)。
// 找不到返回 ""。

func nearestExportName(name string, exports map[string]object.Value) string {
	best := ""
	bestDist := 3
	for candidate := range exports {
		if strings.EqualFold(candidate, name) {
			return candidate
		}
		if d := editDistance(name, candidate); d < bestDist {
			best, bestDist = candidate, d
		}
	}
	return best
}

// editDistance 是标准 Levenshtein 距离 (两行滚动数组, O(n*m) 时间 O(m) 空间)。

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 {
		return len(rb)
	}
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = minInt(prev[j]+1, minInt(curr[j-1]+1, prev[j-1]+cost))
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// exportNameList 按字典序列出导出名 (gox 这种并集模块太长, 截断到 12 个)。

func exportNameList(exports map[string]object.Value) string {
	names := make([]string, 0, len(exports))
	for name := range exports {
		names = append(names, name)
	}
	sort.Strings(names)
	const max = 12
	if len(names) > max {
		return strings.Join(names[:max], ", ") + ", …"
	}
	return strings.Join(names, ", ")
}

// compileExportDeclaration 编译 export 声明。
//
// 四类形态分别落到不同的运行期机制:
//
//	export * from "m"          → OP_EXPORT_STAR (星号再导出, 读时解析)
//	export {a as b} from "m"   → OP_EXPORT_FROM (具名再导出/命名空间再导出)
//	export {a as b}            → 本地命名导出 (模块模式走 OP_EXPORT_BINDING)
//	export <declaration>       → 编译声明 + 按名导出 / default 导出
//
// 关于"活绑定": 模块模式下本地 let/const/var/function/class 导出用
// OP_EXPORT_BINDING 记录"导出名 → 模块顶层帧槽位", 导入方取用时才读取槽位,
// 因此 `export let a = 1; a = 2;` 会被读到 2 (规范要求的 live binding)。
// 入口脚本(全局模式)的顶层绑定落在共享全局环境, 没有帧槽位, 退回值导出。

func (c *Compiler) compileExportDeclaration(stmt *ast.ExportDeclaration) error {
	// 1) export * from "m": 记录源模块, 读时转发其自有可枚举导出 (不含 default)
	if stmt.IsStar {
		c.noteStaticImport(stmt.Source)
		specIdx := c.constants.AddConstant(object.NewString(stmt.Source))
		c.emitter.Emit(bytecode.OP_EXPORT_STAR, specIdx)
		return nil
	}

	// 2) export ... from "m": 具名再导出 / export * as ns
	//    常量 = [模块路径, 源导出名, 目标导出名]; 源导出名 "*" 表示命名空间对象。
	if stmt.Source != "" {
		c.noteStaticImport(stmt.Source)
		for _, sp := range stmt.Specifiers {
			composite := object.NewArray([]object.Value{
				object.NewString(stmt.Source),
				object.NewString(sp.Local),
				object.NewString(sp.Exported),
			})
			idx := c.constants.AddConstant(composite)
			c.emitter.Emit(bytecode.OP_EXPORT_FROM, idx)
		}
		return nil
	}

	// 3) export { a, b as c }: 本地命名导出 (可别名)
	if len(stmt.Specifiers) > 0 {
		for _, sp := range stmt.Specifiers {
			c.emitLocalExport(sp.Local, sp.Exported)
		}
		return nil
	}

	// 4) export <declaration>
	if stmt.Declaration == nil {
		return nil
	}
	if stmt.IsDefault {
		return c.compileDefaultExport(stmt.Declaration)
	}
	if err := c.compileStatement(stmt.Declaration); err != nil {
		return err
	}
	// 声明里绑定的每个名字都导出一次 (覆盖多 declarator 与解构)
	for _, name := range exportedNames(stmt.Declaration) {
		c.emitLocalExport(name, name)
	}
	return nil
}

// emitLocalExport 把一个本模块绑定以 exported 名字导出。
//
// 模块模式的顶层绑定有帧槽位 (OP_STORE/OP_STORE_CONST), 用 OP_EXPORT_BINDING
// 记录槽位读取器 → 活绑定。全局模式的顶层绑定按名字落在共享全局环境, 退回
// "取值导出": 载入当前值再 OP_EXPORT。

func (c *Compiler) emitLocalExport(local, exported string) {
	sym := c.scope.Resolve(local)
	if sym != nil && !(sym.Depth == 0 && !c.moduleMode) {
		composite := object.NewArray([]object.Value{
			object.NewString(exported),
			object.NewInt(int64(sym.Slot)),
		})
		idx := c.constants.AddConstant(composite)
		c.emitter.Emit(bytecode.OP_EXPORT_BINDING, idx)
		return
	}
	// 全局/未解析: 按名字取全局值后导出 (与旧行为一致, 从不静默丢失导出)
	c.emitGlobalLoad(local)
	nameIdx := c.constants.AddConstant(object.NewString(exported))
	c.emitter.Emit(bytecode.OP_EXPORT, nameIdx)
}

// compileDefaultExport 编译 export default。
//
// 具名默认导出 (`export default function fn(){}` / `export default class C {}`)
// 是**活绑定**: 规范 16.2.3.7 把它记成 LocalExportEntry
// {[[ExportName]]:"default", [[LocalName]]:"fn"} —— default 指向模块内的局部
// 绑定 fn, 于是模块内 `fn = 2` 之后导入方读 imported.default 必须是 2
// (test262: dynamic-import/usage/*gtbndng-indirect-update-dflt*)。模块模式下
// 因此走 emitLocalExport (OP_EXPORT_BINDING 记录槽位), 而不是取值导出。
//
// 表达式/匿名函数/匿名类默认导出则没有可供引用的局部名 (规范的 localName 是
// 不可书写的 "*default*"), 只能取值导出: 先求值入栈, 再 OP_EXPORT 弹出。

func (c *Compiler) compileDefaultExport(decl ast.Statement) error {
	nameIdx := c.constants.AddConstant(object.NewString("default"))
	switch d := decl.(type) {
	case *ast.ExpressionStatement:
		// 表达式只求值一次: 值入栈 → OP_EXPORT 弹出。
		// 匿名函数/类默认导出按规范命名为 "default"。
		if err := c.compileNamedExpression(d.Expression, "default"); err != nil {
			return err
		}
	case *ast.FunctionDeclaration:
		if err := c.compileFunctionDeclaration(d); err != nil {
			return err
		}
		if d.Name != nil && c.moduleMode {
			c.emitLocalExport(d.Name.Value, "default")
			return nil
		}
		if err := c.loadDeclaredBinding(d.Name); err != nil {
			return err
		}
	case *ast.ClassDeclaration:
		if err := c.compileClassDeclaration(d); err != nil {
			return err
		}
		if d.Name != nil && c.moduleMode {
			c.emitLocalExport(d.Name.Value, "default")
			return nil
		}
		if err := c.loadDeclaredBinding(d.Name); err != nil {
			return err
		}
	default:
		return fmt.Errorf("compiler: unsupported export default declaration %T", decl)
	}
	c.emitter.Emit(bytecode.OP_EXPORT, nameIdx)
	return nil
}

// loadDeclaredBinding 加载刚声明的绑定值到栈顶。
// 关键: 用 emitLoad 而不是裸 OP_LOAD —— 全局模式下绑定在共享全局环境里
// (OP_DECLARE_FUNC/OP_DECLARE), 裸 OP_LOAD 会去读从未初始化的帧槽位,
// 触发 "Cannot access lexical declaration before initialization" (TDZ 误报)。

func (c *Compiler) loadDeclaredBinding(name *ast.Identifier) error {
	if name == nil {
		return fmt.Errorf("compiler: export default declaration has no name")
	}
	sym := c.scope.Resolve(name.Value)
	if sym == nil {
		return fmt.Errorf("compiler: export default binding %q not found", name.Value)
	}
	c.emitLoad(sym)
	return nil
}

// exportedNames 返回一个"声明导出"里所有被绑定的名字。
// 覆盖: 多 declarator (export let a, b) 与解构 (export const {x, y} = o)。

func exportedNames(decl ast.Statement) []string {
	switch d := decl.(type) {
	case *ast.LetStatement:
		return declaratorExportNames(d.Name, d.Value, d.More)
	case *ast.ConstStatement:
		return declaratorExportNames(d.Name, d.Value, d.More)
	case *ast.VarStatement:
		return declaratorExportNames(d.Name, d.Value, d.More)
	case *ast.FunctionDeclaration:
		if d.Name != nil {
			return []string{d.Name.Value}
		}
	case *ast.ClassDeclaration:
		if d.Name != nil {
			return []string{d.Name.Value}
		}
	}
	return nil
}

// declaratorExportNames 收集声明项绑定的名字 (含解构合成的 "__destructure__")。

func declaratorExportNames(name *ast.Identifier, value ast.Expression, more []ast.Declarator) []string {
	var out []string
	collect := func(n *ast.Identifier, v ast.Expression) {
		if n == nil {
			return
		}
		if n.Value == destructureSyntheticName {
			out = append(out, ast.PatternBoundNames(v)...)
			return
		}
		out = append(out, n.Value)
	}
	collect(name, value)
	for _, d := range more {
		collect(d.Name, d.Value)
	}
	return out
}

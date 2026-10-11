package vm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/14752222/Gox/compiler"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/tstransform"
)

type ModuleExports struct {
	Default object.Value
	Named   map[string]object.Value

	// bindings: 活绑定导出。导出名 → 读取模块顶层帧槽位的函数。
	bindings map[string]func() object.Value
	// forwards: 具名/命名空间再导出。导出名 → 源模块 + 源导出名 ("*" = 命名空间)。
	forwards map[string]forwardRef
	// stars: export * from 的源模块 (按出现顺序, 保证枚举确定性)。
	stars []*ModuleExports
	// order: 导出名登记顺序 (Named/bindings/forwards 共享)。
	order []string

	// rec 是异步模块图求值的状态 (规范 ExecuteAsyncModule 一族)。内置模块与
	// 早期实现直接塞进 Named 的模块为 nil (视作已求值)。见 moduleRecord。
	rec *moduleRecord
}

// forwardRef 是一次再导出转发。

type forwardRef struct {
	src  *ModuleExports
	name string // 源模块里的导出名; "*" 表示导出整个命名空间对象
}

func newModuleExports() *ModuleExports {
	return &ModuleExports{Named: map[string]object.Value{}}
}

func (m *ModuleExports) noteOrder(name string) {
	for _, n := range m.order {
		if n == name {
			return
		}
	}
	m.order = append(m.order, name)
}

// setValue 记录一个值导出 (default 单独存 Default)。

func (m *ModuleExports) setValue(name string, v object.Value) {
	if name == "default" {
		m.Default = v
	} else {
		if m.Named == nil {
			m.Named = map[string]object.Value{}
		}
		m.Named[name] = v
	}
	m.noteOrder(name)
}

// setBinding 记录一个活绑定导出: get 返回模块顶层帧槽位的当前值。

func (m *ModuleExports) setBinding(name string, get func() object.Value) {
	if m.bindings == nil {
		m.bindings = map[string]func() object.Value{}
	}
	m.bindings[name] = get
	m.noteOrder(name)
}

// setForward 记录一次再导出转发。

func (m *ModuleExports) setForward(name string, src *ModuleExports, srcName string) {
	if m.forwards == nil {
		m.forwards = map[string]forwardRef{}
	}
	m.forwards[name] = forwardRef{src: src, name: srcName}
	m.noteOrder(name)
}

func (m *ModuleExports) addStar(src *ModuleExports) {
	m.stars = append(m.stars, src)
}

// resolveWith 返回导出名 name 的当前值 (第二个返回值表示是否可解析),
// 并携带 building (命名空间构造集)。
//
// path 用于打断循环再导出 (A export * from B, B export * from A): 一个模块在
// 本次解析链上只参与一次, 否则环会无限递归。规范里循环再导出最终由
// ResolveExport 的 null/ambiguous 结果终止, 这里以"链上重复即放弃"等效实现。
//
// 为什么需要两个集合: path 是"当前解析链"的环检测 (进入即标记、返回即清除),
// 而 building 是"本次命名空间物化过程中正在构造的那些模块"的集合, 必须跨整棵
// 递归共享。`export * as a from B` + `export * as b from A` 这种命名空间环里,
// 若每层都用新集合, A→B→A→B… 会一路递归到栈溢出; 共享 building 后第二次遇到
// 已构造中的模块直接给空对象占位, 环即终止。

func (m *ModuleExports) resolveWith(name string, path, building map[*ModuleExports]bool) (object.Value, bool) {
	if m == nil {
		return nil, false
	}
	if path[m] {
		return nil, false
	}
	path[m] = true
	defer delete(path, m)

	// 1) 本模块直接导出。
	if m.Named != nil {
		if v, ok := m.Named[name]; ok {
			return v, true
		}
	}
	if name == "default" && m.Default != nil {
		return m.Default, true
	}
	if m.bindings != nil {
		if get, ok := m.bindings[name]; ok {
			if v := get(); v != nil {
				return v, true
			}
			// 槽位尚未初始化 (循环导入的部分导出): 当作 undefined,
			// 与旧实现"属性缺失 → GET_PROP 得 undefined"一致, 不抛错。
			return object.UndefinedSingleton, true
		}
	}

	// 2) 具名再导出 (优先级高于 export *)。
	if m.forwards != nil {
		if f, ok := m.forwards[name]; ok {
			if f.name == "*" {
				return f.src.buildNamespace(building), true
			}
			return f.src.resolveWith(f.name, path, building)
		}
	}

	// 3) export * (default 永不转发)。
	if name != "default" {
		var found object.Value
		hit := 0
		for _, s := range m.stars {
			if v, ok := s.resolveWith(name, path, building); ok {
				found = v
				hit++
			}
		}
		if hit == 1 {
			return found, true
		}
		// hit > 1: 多个星号源同名 → 规范判为 ambiguous, 不导出。
	}
	return nil, false
}

// exportNames 返回本模块对外可见的导出名, 顺序确定 (见 ModuleExports 注释)。

func (m *ModuleExports) exportNames(seen map[*ModuleExports]bool) []string {
	if m == nil || seen[m] {
		return nil
	}
	seen[m] = true
	defer delete(seen, m)

	var names []string
	have := map[string]bool{}
	add := func(n string) {
		if !have[n] {
			have[n] = true
			names = append(names, n)
		}
	}
	for _, n := range m.order {
		add(n)
	}
	// order 缺失的名字 (内置模块直接塞 Named) 字典序补末尾, 保证确定性。
	var extra []string
	for n := range m.Named {
		if !have[n] {
			extra = append(extra, n)
		}
	}
	if len(extra) > 0 {
		sort.Strings(extra)
		for _, n := range extra {
			add(n)
		}
	}
	for _, s := range m.stars {
		for _, n := range s.exportNames(seen) {
			if n == "default" {
				continue // default 不参与星号转发
			}
			add(n)
		}
	}
	return names
}

// isLiveExport 判断导出名 name 在 m 上是否是**活导出**: 值来自模块顶层帧槽位
// (bindings) 或再导出转发 (forwards/stars), 源端后续改动应能被命名空间读到。
//
// 只有活的导出才需要在命名空间上装读取器 (见 liveExportDesc); 直接塞进
// Named/Default 的静态导出 (内置模块、字面量导出) 是常量, 物化一次就够 ——
// 也给它们保留普通数据属性, 免得 console.log(内建命名空间) 之类路径看到
// 访问器而退化。

func (m *ModuleExports) isLiveExport(name string, path map[*ModuleExports]bool) bool {
	if m == nil || path[m] {
		return false
	}
	path[m] = true
	defer delete(path, m)

	if m.bindings != nil {
		if _, ok := m.bindings[name]; ok {
			return true
		}
	}
	if m.forwards != nil {
		if f, ok := m.forwards[name]; ok {
			// "*" 是命名空间再导出, 其值是另一个命名空间对象: 保持物化语义
			// (每次读取都新建对象会破坏 ns.a === ns.a 的同一性), 不视为活导出。
			if f.name == "*" {
				return false
			}
			return f.src.isLiveExport(f.name, path)
		}
	}
	if m.Named != nil {
		if _, ok := m.Named[name]; ok {
			return false
		}
	}
	if name == "default" && m.Default != nil {
		return false
	}
	if name == "default" {
		return false
	}
	for _, s := range m.stars {
		if s.isLiveExport(name, path) {
			return true
		}
	}
	return false
}

// liveExportDesc 构造命名空间上一个**活导出**的属性描述符。
//
// 规范里模块命名空间是 exotic object (11.4.6 Module Namespace Exotic
// Objects): 它的 [[Get]] 每次都回源模块的绑定槽取值, 所以导入方在模块求值
// 之后修改导出绑定 ——
//
//	var x = 1; export { x }; ... x = 2;
//
// —— 命名空间上读到的必须是新值。Gox 的命名空间是普通 *object.Object, 物化时
// 若直接 SetProperty 就把活绑定固化成了快照, `imported.x` 永远是物化那一刻的
// 值 (test262: dynamic-import/usage/*gtbndng-indirect-update*)。
//
// 这里以"读取时回读"的访问器等效实现: getter 每次调用 resolveWith, 拿到源模块
// 导出槽的当前值。setter 留空对应规范 [[Set]] 直接返回 false —— 无 setter 的
// 访问器在非严格模式静默失败、严格模式抛 TypeError, 与之同构。描述符取
// enumerable:true / configurable:false, 与规范命名空间属性一致。

func (m *ModuleExports) liveExportDesc(name string) object.PropertyDescriptor {
	getter := object.NewBuiltin("get "+name, func(args ...object.Value) object.Value {
		v, ok := m.resolveWith(name, map[*ModuleExports]bool{}, map[*ModuleExports]bool{})
		if !ok || v == nil {
			return object.UndefinedSingleton
		}
		return v
	})
	return object.PropertyDescriptor{
		Value:        object.NewAccessor(getter, nil),
		Writable:     false,
		Enumerable:   true,
		Configurable: false,
	}
}

// buildNamespace 把模块导出物化成一个命名空间对象。
//
// 这是导入方唯一拿到的"模块视图": OP_IMPORT / 动态 import / `export * as ns`
// 都走它。活导出 (顶层词法声明 / 具名再导出) 落成读取器、每次读取回源模块
// 导出槽; 静态导出 (内置模块塞进 Named 的值) 落成数据属性。见 isLiveExport /
// liveExportDesc 的说明。
//
// building 是"正在构造的模块"集合, 跨整棵递归共享, 打断 `export * as ns`
// 形成的命名空间环 (见 resolveWith 的注释)。

func (m *ModuleExports) buildNamespace(building map[*ModuleExports]bool) *object.Object {
	obj := object.NewObject()
	if m == nil {
		return obj
	}
	if building == nil {
		building = map[*ModuleExports]bool{}
	}
	if building[m] {
		return obj
	}
	building[m] = true
	defer delete(building, m)

	for _, name := range m.exportNames(map[*ModuleExports]bool{}) {
		if name == "default" {
			continue // default 单独放在最后, 保持直觉顺序
		}
		v, ok := m.resolveWith(name, map[*ModuleExports]bool{}, building)
		if !ok {
			continue // 二义/未解析: 不放进命名空间
		}
		if m.isLiveExport(name, map[*ModuleExports]bool{}) {
			obj.DefineOwnProperty(name, m.liveExportDesc(name))
			continue
		}
		obj.SetProperty(name, v)
	}
	if v, ok := m.resolveWith("default", map[*ModuleExports]bool{}, building); ok {
		if m.isLiveExport("default", map[*ModuleExports]bool{}) {
			obj.DefineOwnProperty("default", m.liveExportDesc("default"))
		} else {
			obj.SetProperty("default", v)
		}
	}
	return obj
}

// currentVM 是当前正在执行的 VM 实例。
// 用于从 stdlib 回调 JS 闭包时找到正确的 VM。

func (vm *VM) ensureCurrentExports() *ModuleExports {
	if vm.currentExports == nil {
		vm.currentExports = newModuleExports()
	}
	return vm.currentExports
}

// bindingOperand 解析 OP_EXPORT_BINDING 的操作数: [导出名, 槽位]。

func (vm *VM) bindingOperand(frame *Frame, operand uint16) (string, int, bool) {
	arr, ok := frame.Constants.Get(operand).(*object.Array)
	if !ok || len(arr.Elements) != 2 {
		return "", 0, false
	}
	name, ok1 := arr.Elements[0].(*object.String)
	slot, ok2 := arr.Elements[1].(*object.Number)
	if !ok1 || !ok2 {
		return "", 0, false
	}
	return name.Value, int(slot.Value), true
}

// reexportOperand 解析 OP_EXPORT_FROM 的操作数: [模块路径, 源导出名, 目标导出名]。

func (vm *VM) reexportOperand(frame *Frame, operand uint16) (string, string, string, bool) {
	arr, ok := frame.Constants.Get(operand).(*object.Array)
	if !ok || len(arr.Elements) != 3 {
		return "", "", "", false
	}
	spec, ok1 := arr.Elements[0].(*object.String)
	srcName, ok2 := arr.Elements[1].(*object.String)
	target, ok3 := arr.Elements[2].(*object.String)
	if !ok1 || !ok2 || !ok3 {
		return "", "", "", false
	}
	return spec.Value, srcName.Value, target.Value, true
}

// loadModule 加载并执行模块，返回导出对象。
// 使用模块缓存避免重复加载。

func (vm *VM) loadModule(spec string) (*ModuleExports, error) {
	// 内置模块 (如 "gx/solid"、"gox") 优先于文件系统解析
	if exports, ok := object.LookupBuiltinModule(spec); ok {
		mod := newModuleExports()
		mod.Named = exports
		vm.modules[spec] = mod
		return mod, nil
	}

	// "gox" 与 "gx/..." 是保留的内置模块命名空间: 未命中注册表时直接
	// 报错并列出可用模块, 不再落到文件系统解析 —— 否则拼写错误会变成
	// 莫名其妙的 "Cannot find module 'gx/dialg'" 文件读取错误。
	if spec == "gox" || strings.HasPrefix(spec, "gx/") {
		return nil, fmt.Errorf("Cannot find module '%s' (unknown builtin module; available: %s)",
			spec, strings.Join(object.RegisteredBuiltinModules(), ", "))
	}

	// 解析模块路径: 裸说明符 (node_modules) 与相对/绝对路径分开走
	absPath, resolveErr := vm.resolveModule(spec)
	if resolveErr != nil {
		return nil, resolveErr
	}

	return vm.loadModuleFile(spec, absPath)
}

// loadModuleFile 从已解析的磁盘路径加载、编译并执行模块。
// 与 loadModule 拆开是为了让"解析"与"加载执行"各自可测（node_modules
// 解析的测试只关心前者，不需要跑完整条编译执行链）。
//
// moduleSyntaxError 标记「模块的解析/编译失败属于语法错误」: 动态 import 据此
// 把 reject 对象的 name 设为 SyntaxError (而非笼统的 Error)。

type moduleSyntaxError struct{ msg string }

func (e *moduleSyntaxError) Error() string { return e.msg }

// moduleEvalError 携带模块**求值期**拒绝的原始 JS 值 (rec.errVal) 与文本。
//
// 为什么需要独立的错误类型: loadModuleFile 过去用 fmt.Errorf 把拒绝原因压成
// 字符串, 使动态 import 只能拿 err.Error() 重新包一个 name="Error" 的对象 ——
// 模块顶层 `throw new TypeError()` 到 catch 侧就退化成通用 Error (test262
// nested-async-function-*-eval-rqstd-abrupt-typeerror 断言 error.name）。
// 这里把 JS 值带出来, 由 OP_DYNAMIC_IMPORT 直接用 errVal 作 rejection reason。

type moduleEvalError struct {
	val  object.Value
	text string
}

func (e *moduleEvalError) Error() string { return e.text }

// moduleLoadErrorValue 把模块加载错误归一为要抛出的 JS 值。
// moduleEvalError 携带模块求值期的原始拒绝值 (保住 throw 的对象类型),
// 其余情况维持旧的 name="Error" 包装 (SyntaxError 由调用点另行判定)。

func moduleLoadErrorValue(err error) object.Value {
	if me, ok := err.(*moduleEvalError); ok && me.val != nil {
		return me.val
	}
	return object.NewErrorWithName("Error", err.Error())
}

func (vm *VM) loadModuleFile(spec, absPath string) (*ModuleExports, error) {
	// 检查缓存
	if mod, ok := vm.modules[absPath]; ok {
		if r := mod.rec; r != nil && r.status == moduleRejected {
			return nil, &moduleEvalError{val: r.errVal, text: r.errText}
		}
		return mod, nil
	}

	// 读取文件; TS 家族 (.ts/.tsx/.mts/.cts/.jsx) 先过类型剥离转译,
	// 进编译管线的永远是 JS —— parser/compiler 不感知 TS 的存在。
	// orig 是用户原文, code 是进编译管线的 JS (两者分开: M2 源码帧要展示原文)。
	orig, err := osReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("Cannot find module '%s'", spec)
	}
	code := orig
	var lineMap *tstransform.LineMap
	isTS := tstransform.IsTS(absPath)
	if isTS {
		res, terr := tstransform.TransformCached(orig, absPath)
		if terr != nil {
			return nil, terr
		}
		code = res.Code
		lineMap = res.LineMap
	}

	// 编译模块
	c, err := compileSource(string(code), true)
	if err != nil {
		if isTS {
			err = remapSourceError(err, lineMap)
		}
		if se, ok := err.(*sourceError); ok && se.parse {
			return nil, &moduleSyntaxError{msg: fmt.Sprintf("Module parse error: %s", se.msg)}
		}
		// 编译器报的语法错误 (如模块内重复声明) 同样是 SyntaxError, 供
		// dynamic import 的 reject 对象取正确 name。
		if strings.Contains(err.Error(), "SyntaxError") {
			return nil, &moduleSyntaxError{msg: fmt.Sprintf("Module compile error: %v", err)}
		}
		return nil, fmt.Errorf("Module compile error: %v", err)
	}

	// 异步模块图求值: 先注册模块记录 (含循环导入防线), 再按 leaf-to-root 求值
	// 静态依赖, 最后运行本体。只要依赖里有模块含顶层 await 且尚未完成, 本体
	// 就延后到依赖全部完成后再跑 (规范 InnerModuleEvaluation 的 async 分支)。
	mod := newModuleExports()
	rec := &moduleRecord{
		absPath:     absPath,
		status:      moduleEvaluating,
		completion:  object.NewPromise(),
		staticSpecs: c.StaticImports(),
	}
	mod.rec = rec
	vm.modules[absPath] = mod

	modVM := NewWithGlobals(c.Bytes(), c.Constants(), c.NumLocals(), vm.globals)
	modVM.modules = vm.modules
	modVM.moduleBase = dirOf(absPath)
	modVM.currentExports = mod
	// 模块顶层 this 必须是 undefined (与 script 顶层 this = globalThis 相对)。
	modVM.moduleMode = true
	// 源码单元注册表跨 VM 共享: 模块里定义的函数之后可能在入口 VM 上被调用,
	// 抛错时要用模块自己的单元渲染帧 (M2 P0-1)。
	modVM.units = vm.units
	// M2 P0-1: 被 import 的模块此前不注入源码信息 ⇒ 模块内报错没有源码帧。
	// 这里补齐 (原文 + 行映射 + 语句位置表), 与入口脚本同等对待。
	modVM.SetSourceInfo(absPath, string(orig))
	if isTS {
		modVM.SetTranspileMap(lineMap, string(code))
	}
	modVM.mainUnit.isModule = true // 标记为模块单元: 其函数登记进 units 注册表
	modVM.SetStmtPositions(c.StmtPositions())

	// 依赖先求值。求值在加载方 VM 上进行, 但相对说明符必须按**本模块**目录
	// 解析 (与本体执行同基准), 故临时切换 moduleBase。
	savedBase := vm.moduleBase
	vm.moduleBase = dirOf(absPath)
	vm.evalModuleDeps(rec)
	vm.moduleBase = savedBase
	if rec.status == moduleRejected {
		// 依赖求值失败 (编译错误 / 依赖被拒): 本体不运行。
		return nil, &moduleEvalError{val: rec.errVal, text: fmt.Sprintf("Module execution error: %v", rec.errText)}
	}
	if rec.pendingDeps == 0 {
		vm.runModuleBody(mod, rec, modVM, c)
	} else {
		// 依赖未就绪: 记录本体闭包, 依赖完成时由 startDeferred 触发。
		rec.status = modulePending
		rec.runBody = func() { vm.runModuleBody(mod, rec, modVM, c) }
	}
	if rec.status == moduleRejected {
		return nil, &moduleEvalError{val: rec.errVal, text: fmt.Sprintf("Module execution error: %v", rec.errText)}
	}
	return mod, nil
}

// ── 异步模块图求值 (规范 16.2.1.5.2 ExecuteAsyncModule /
// AsyncModuleExecutionFulfilled / AsyncModuleExecutionRejected 的 Gox 实现) ──
//
// Gox 的同步 Promise 模型下, 模块求值必须能在"顶层 await 挂起"时把控制权交回,
// 待 promise 结算后再恢复 (见 modulePendingEval)。同时, 依赖了未完成异步模块的
// 模块, 其本体要等到依赖完成才运行 (record.pendingDeps/dependents), 从而得到
// 规范的 leaf-to-root 求值/完成顺序, 并让 rejection 沿依赖边传播而不继续执行。

type moduleEvalStatus int

const (
	moduleEvaluating moduleEvalStatus = iota // 正在加载/求值
	modulePending                            // 依赖未就绪, 或自身 TLA 挂起
	moduleEvaluated                          // 已完成
	moduleRejected                           // 已拒绝
)

// moduleRecord 是一个模块的求值状态 (按 ModuleExports.rec 挂靠)。

type moduleRecord struct {
	absPath     string
	staticSpecs []string // 顶层静态依赖说明符 (来自 compiler.StaticImports)

	status  moduleEvalStatus
	errVal  object.Value // 拒绝原因 (JS 值)
	errText string       // 拒绝消息 (Go 侧, 供 OP_IMPORT/dynamic import 取用)

	completion *object.Promise // 求值结算: fulfilled(undefined) / rejected(原因)

	pendingDeps int             // 尚未完成的直接依赖数
	dependents  []*moduleRecord // 等待本模块完成的依赖方

	runBody     func()             // 依赖就绪后运行本体的闭包 (延后时非 nil)
	pendingEval *modulePendingEval // 自身 TLA 挂起时的待恢复执行状态
}

// modulePendingEval 保存一个被顶层 await 挂起的模块求值 (gen 可在结算后恢复)。

type modulePendingEval struct {
	modVM *VM
	gen   *object.Generator
	rec   *moduleRecord
}

// evalModuleDeps 先求值 rec 的全部静态依赖。未完成的依赖计入 pendingDeps 并
// 登记为 dependents; 依赖被拒则本模块直接拒绝。循环 (同 SCC, 依赖可达本模块)
// 不等待, 保持既有的"部分导出"语义。

func (vm *VM) evalModuleDeps(rec *moduleRecord) {
	for _, spec := range rec.staticSpecs {
		dep, err := vm.loadModule(spec)
		if err != nil {
			vm.rejectRecord(rec, errorToJSValue(err), err.Error())
			return
		}
		depRec := dep.rec
		if depRec == nil || depRec.status == moduleEvaluated {
			continue
		}
		if depRec.status == moduleRejected {
			vm.rejectRecord(rec, depRec.errVal, depRec.errText)
			return
		}
		// 循环检测: 依赖能沿 import 边到达本模块 ⇒ 同 SCC, 不等待。
		if vm.specReaches(depRec, rec) {
			continue
		}
		rec.pendingDeps++
		depRec.dependents = append(depRec.dependents, rec)
	}
}

// specReaches 报告 from 是否沿**静态依赖说明符**到达 to (含 from==to)。
//
// 用说明符而非"已解析依赖"来判环: 依赖边是在 loadModule 返回后才登记的, 环上
// 的那条边 (正在求值的依赖) 尚未登记; 而说明符在模块注册时就已知, 且按模块
// 自身目录解析后可达目标 ⇒ 是真环 (同 SCC)。这样也能把"promise 回调重入"
// 引发的假父子关系 (模块 A 由 B 求值期间的副作用回调触发, 但 A 并不在 B 的
// import 边上) 与真环区分开 —— 前者 A 必须等待 B。

func (vm *VM) specReaches(from, to *moduleRecord) bool {
	seen := map[string]bool{}
	var dfs func(r *moduleRecord) bool
	dfs = func(r *moduleRecord) bool {
		if r == to {
			return true
		}
		if r == nil || seen[r.absPath] {
			return false
		}
		seen[r.absPath] = true
		saved := vm.moduleBase
		vm.moduleBase = dirOf(r.absPath)
		var found bool
		for _, spec := range r.staticSpecs {
			abs, err := vm.resolveModule(spec)
			if err != nil {
				continue
			}
			dep := vm.modules[abs]
			if dep == nil || dep.rec == nil {
				continue
			}
			if dfs(dep.rec) {
				found = true
				break
			}
		}
		vm.moduleBase = saved
		return found
	}
	return dfs(from)
}

// runModuleBody 运行模块本体。含 TLA 走可挂起的生成器驱动; 其余同步执行。

func (vm *VM) runModuleBody(mod *ModuleExports, rec *moduleRecord, modVM *VM, c *compiler.Compiler) {
	modVM.currentExports = mod
	modVM.moduleBase = dirOf(rec.absPath)
	if c.HasTopLevelAwait() {
		gen := &object.Generator{
			PC:            0,
			Started:       false,
			PrologueBound: true,
			Locals:        make([]object.Value, c.NumLocals()),
			Constants:     c.Constants().Constants,
			Instructions:  c.Bytes(),
		}
		pe := &modulePendingEval{modVM: modVM, gen: gen, rec: rec}
		rec.pendingEval = pe
		// 必须在模块自己的 VM 上驱动: 主单元字节码的帧/currentExports 都属于
		// modVM, 用加载方 VM 驱动会把导出登记到加载方 (namespace 变空)。
		modVM.driveModule(pe, object.UndefinedSingleton, false)
		return
	}
	runErr := modVM.RunCompiled(c)
	if runErr != nil {
		vm.rejectRecord(rec, errorToJSValue(runErr), modVM.AttachFrame(runErr).Error())
		return
	}
	vm.fulfillRecord(rec)
}

// driveModule 恢复一个模块顶层生成器, 直到完成或挂在 pending Promise 上。

func (vm *VM) driveModule(pe *modulePendingEval, arg object.Value, isThrow bool) {
	val, done, genErr, panicErr := vm.stepGen(pe, arg, isThrow)
	for {
		if panicErr != nil {
			vm.rejectRecord(pe.rec, errorToJSValue(panicErr), panicErr.Error())
			return
		}
		if done {
			if genErr != nil {
				vm.rejectRecord(pe.rec, errorToJSValue(genErr), pe.modVM.AttachFrame(genErr).Error())
			} else {
				vm.fulfillRecord(pe.rec)
			}
			return
		}
		// 挂在 await: val 是操作数。非 Promise 立即恢复 (Gox 同步 Promise 模型)。
		p, ok := val.(*object.Promise)
		if !ok {
			val, done, genErr, panicErr = vm.stepGen(pe, val, false)
			continue
		}
		vm.armPromiseResume(pe, p)
		return
	}
}

// stepGen 在 panic 保护下对模块生成器做一次 resume/throw。

func (vm *VM) stepGen(pe *modulePendingEval, arg object.Value, isThrow bool) (object.Value, bool, error, error) {
	saved := currentVM
	currentVM = vm
	defer func() { currentVM = saved }()
	var val object.Value
	var done bool
	var genErr error
	panicErr := vm.runProtected(func() error {
		if isThrow {
			val, done, genErr = vm.genThrow(pe.gen, arg)
		} else {
			val, done, genErr = vm.genResume(pe.gen, arg)
		}
		return nil
	})
	return val, done, genErr, panicErr
}

// armPromiseResume 在 await 的 Promise 上注册恢复回调。已结算的 Promise 会
// 同步触发回调 (继续驱动模块); pending 的等结算后 (通常由定时器/后续代码)
// 触发。settled 标志防止 then/catch 双触发。

func (vm *VM) armPromiseResume(pe *modulePendingEval, p *object.Promise) {
	settled := false
	p.Then(object.NewBuiltin("__module_tla_fulfilled", func(args ...object.Value) object.Value {
		if settled {
			return object.UndefinedSingleton
		}
		settled = true
		var res object.Value = object.UndefinedSingleton
		if len(args) > 0 {
			res = args[0]
		}
		vm.driveModule(pe, res, false)
		return object.UndefinedSingleton
	}))
	p.Catch(object.NewBuiltin("__module_tla_rejected", func(args ...object.Value) object.Value {
		if settled {
			return object.UndefinedSingleton
		}
		settled = true
		var reason object.Value = object.UndefinedSingleton
		if len(args) > 0 {
			reason = args[0]
		}
		vm.driveModule(pe, reason, true)
		return object.UndefinedSingleton
	}))
}

// fulfillRecord 标记模块完成: 结算 completion, 并唤醒依赖就绪的依赖方。

func (vm *VM) fulfillRecord(rec *moduleRecord) {
	if rec.status == moduleEvaluated || rec.status == moduleRejected {
		return
	}
	rec.status = moduleEvaluated
	rec.pendingEval = nil
	rec.completion.Resolve(object.UndefinedSingleton)
	dependents := rec.dependents
	rec.dependents = nil
	for _, d := range dependents {
		if d.status == moduleRejected {
			continue
		}
		d.pendingDeps--
		if d.pendingDeps <= 0 {
			d.pendingDeps = 0
			vm.startDeferred(d)
		}
	}
}

// rejectRecord 标记模块拒绝: 本体不运行, 原因沿依赖边传播到全部依赖方。

func (vm *VM) rejectRecord(rec *moduleRecord, val object.Value, text string) {
	if rec.status == moduleEvaluated || rec.status == moduleRejected {
		return
	}
	rec.status = moduleRejected
	rec.pendingEval = nil
	if text == "" {
		text = "module evaluation failed"
	}
	rec.errVal = val
	rec.errText = text
	rec.completion.Reject(val)
	dependents := rec.dependents
	rec.dependents = nil
	for _, d := range dependents {
		vm.rejectRecord(d, val, text)
	}
}

// startDeferred 在依赖全部完成后运行被延后的模块本体。

func (vm *VM) startDeferred(rec *moduleRecord) {
	if rec.status != modulePending || rec.runBody == nil {
		return
	}
	rb := rec.runBody
	rec.runBody = nil
	rb()
}

// errorToJSValue 把 Go 侧错误还原成 JS 抛出值 (供 rejection reason 使用)。

func (vm *VM) SetModuleBase(path string) {
	vm.moduleBase = path
}

// ===== 辅助方法 =====

// globalThisValue 返回全局对象绑定 (globals 里由 stdlib 装配); 未装配 (裸 VM)
// 时回退 undefined。

func resolvePath(base, spec string) string {
	if filepath.IsAbs(spec) {
		return filepath.Clean(spec)
	}
	return filepath.Clean(filepath.Join(base, spec))
}

// resolveModule 把模块说明符解析为磁盘上的真实文件。
//
// 两类说明符分开处理：
//   - 裸说明符（"lodash" / "@scope/pkg" / "lodash/fp"）→ node_modules 解析；
//   - 相对/绝对路径（"./x.js" / "/abs/x.js"）→ 原有文件解析。
//
// 裸说明符在 node_modules 里找不到时，回退到原有"相对 moduleBase 当路径解析"
// 的行为 —— 历史脚本里有 `import "src/util.js"` 这种靠 moduleBase 的写法，
// 不能因为引入 npm 解析就把它判死。两条路都落空时报错同时列出两份候选。

func (vm *VM) resolveModule(spec string) (string, error) {
	if !isBareSpecifier(spec) {
		return vm.resolveModuleFile(spec)
	}
	nmPath, nmErr := vm.resolveNodeModule(spec)
	if nmErr == nil {
		return nmPath, nil
	}
	legacyPath, legacyErr := vm.resolveModuleFile(spec)
	if legacyErr == nil {
		return legacyPath, nil
	}
	// 都失败: 合并报错, 让用户一次看到 node_modules 与相对路径两边的候选。
	return "", fmt.Errorf("Cannot find module '%s'\n  [node_modules] %v\n  [相对 moduleBase] %v",
		spec, nmErr, legacyErr)
}

// isBareSpecifier 判断说明符是否为"裸包名"（需要 node_modules 解析）。
//
// 排除：相对路径（./ ../ . ..）、绝对路径（/ 或 \ 开头）、Windows 盘符
// （C:\...）。"gox" / "gx/*" 已由 loadModule 在此函数之前拦下。

func isBareSpecifier(spec string) bool {
	if spec == "" || spec == "." || spec == ".." {
		return false
	}
	if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") {
		return false
	}
	if strings.HasPrefix(spec, "/") || strings.HasPrefix(spec, `\`) {
		return false
	}
	if len(spec) >= 2 && spec[1] == ':' { // Windows 盘符 C:\...
		return false
	}
	return true
}

// splitPackageSpec 把裸说明符拆成"包名 + 子路径"。
//
//	"lodash"            → ("lodash", "")
//	"lodash/fp"         → ("lodash", "fp")
//	"@scope/pkg"        → ("@scope/pkg", "")
//	"@scope/pkg/sub"    → ("@scope/pkg", "sub")

func splitPackageSpec(spec string) (name, sub string) {
	if strings.HasPrefix(spec, "@") {
		parts := strings.SplitN(spec, "/", 3)
		if len(parts) < 2 {
			return spec, "" // 非法作用域名（"@scope" 单独），交给后续报错
		}
		name = parts[0] + "/" + parts[1]
		if len(parts) == 3 {
			sub = parts[2]
		}
		return name, sub
	}
	if i := strings.IndexByte(spec, '/'); i >= 0 {
		return spec[:i], spec[i+1:]
	}
	return spec, ""
}

// moduleEntryExtensions 是 node_modules 入口解析尝试的源码后缀，顺序即优先级。
// 在相对导入的 .js/.ts/.tsx 之外补上 npm 世界常见的 .mjs（ESM 真后缀）与
// .cjs（CommonJS，v0 能解析但运行会因 require 未定义而报错 —— 见 docs/npm-compat.md）。

var moduleEntryExtensions = []string{".js", ".mjs", ".cjs", ".jsx", ".ts", ".tsx", ".mts", ".cts"}

// resolveNodeModule 从 moduleBase 起逐级向上查找 node_modules/<pkg>，
// 命中后按 package.json 的 exports > module > main > browser > index.*
// 顺序解析入口。
//
// 顺序理由（v1 口径，写死在此避免后来者当 bug）：
//  1. exports —— Node 12+ 的官方、权威入口声明，也是唯一能表达"子路径 +
//     条件"的字段，最精确，所以最高优先；
//  2. module —— 打包器时代的事实标准，指向 ESM 入口。Gox 是 ESM-first，
//     ESM 入口优先于 CJS 的 main；
//  3. main —— Node 的经典入口（多为 CJS）。Gox v0 不做 CJS：main 指向 CJS
//     时这里**仍会解析成功**，失败发生在运行期（require is not defined）。
//     这是刻意的 loud failure，不在此处静默跳过；
//  4. browser —— 浏览器条件入口（v1 只认字符串形式；对象映射形式不解析）；
//  5. index.js / index.ts / index.tsx —— 老包不带任何入口字段时的兜底。
//
// 失败时报出所有尝试过的候选路径（含逐级 node_modules 目录），沿用
// resolveModuleFile 的可排错风格。

func (vm *VM) resolveNodeModule(spec string) (string, error) {
	name, sub := splitPackageSpec(spec)
	if name == "" || strings.HasSuffix(name, "/") {
		return "", fmt.Errorf("Cannot find module '%s' (非法包名)", spec)
	}

	start := vm.moduleBase
	if start == "" {
		if wd, err := os.Getwd(); err == nil {
			start = wd
		}
	}
	if !filepath.IsAbs(start) {
		if abs, err := filepath.Abs(start); err == nil {
			start = abs
		}
	}
	start = filepath.Clean(start)

	var candidates []string
	add := func(p string) {
		candidates = append(candidates, filepath.Clean(p))
	}

	dir := start
	for {
		pkgDir := filepath.Join(dir, "node_modules", filepath.FromSlash(name))
		if fi, err := os.Stat(pkgDir); err == nil && fi.IsDir() {
			if path, ok := resolvePackageEntry(pkgDir, name, sub, add); ok {
				return path, nil
			}
		} else {
			add(pkgDir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break // 已到根
		}
		dir = parent
	}
	return "", fmt.Errorf("Cannot find module '%s' (tried: %s)", spec, strings.Join(candidates, ", "))
}

// resolvePackageEntry 在已命中的包目录里解析入口文件或子路径。
// 成功返回真实文件路径；失败返回 false（尝试过的候选已通过 add 记录）。

func resolvePackageEntry(pkgDir, pkgName, sub string, add func(string)) (string, bool) {
	pj, ok := readPackageJSON(filepath.Join(pkgDir, "package.json"))
	if !ok {
		add(filepath.Join(pkgDir, "package.json"))
	}

	if sub != "" {
		// 子路径：先按 exports 的 "./sub" 键匹配（条件裁剪见 matchExports）
		if pj != nil {
			if raw, has := pj["exports"]; has {
				if target, matched := matchExports(raw, "./"+sub); matched {
					if path, ok := resolvePackageTarget(pkgDir, target, add); ok {
						return path, true
					}
				}
			}
		}
		// exports 缺该子路径时 v1 宽容处理：直接把 <pkgDir>/<sub> 当文件解析。
		// （Node 会抛 ERR_PACKAGE_PATH_NOT_EXPORTED；v1 选择更宽松，见
		// docs/npm-compat.md 的"exports 条件裁剪"边界说明。）
		if path, ok := resolvePackageFile(filepath.Join(pkgDir, filepath.FromSlash(sub)), add); ok {
			return path, true
		}
		return "", false
	}

	// 根入口：exports 的 "." 键
	if pj != nil {
		if raw, has := pj["exports"]; has {
			if target, matched := matchExports(raw, "."); matched {
				if path, ok := resolvePackageTarget(pkgDir, target, add); ok {
					return path, true
				}
			}
		}
		// exports 未命中 → module > main > browser 逐字段退化
		for _, field := range []string{"module", "main", "browser"} {
			raw, has := pj[field]
			if !has {
				continue
			}
			s, isStr := raw.(string)
			if !isStr || s == "" {
				// browser 常见对象映射形式；v1 不解析，记一条候选便于排错
				add(filepath.Join(pkgDir, field+"(对象映射形式, v1 不支持)"))
				continue
			}
			if path, ok := resolvePackageFile(filepath.Join(pkgDir, filepath.FromSlash(s)), add); ok {
				return path, true
			}
		}
	}

	// 兜底 index.*（老式无 package.json / 无入口字段的包）
	if path, ok := resolvePackageFile(filepath.Join(pkgDir, "index"), add); ok {
		return path, true
	}
	return "", false
}

// matchExports 在 package.json 的 exports 字段里找 key（"." 或 "./sub"）对应的目标。
//
// v1 支持的形态（够用即可；复杂形态显式不支持并在注释里写死）：
//   - "exports": "./index.js"                     → 字符串，仅对 "." 生效
//   - "exports": { ".": "./index.js", "./x": … }  → 以 "." 开头的子路径表
//   - "exports": { "import": …, "default": … }    → 直接写在 exports 上的条件表
//   - 目标值: 字符串 | 条件对象 | 数组（取第一个可用的）
//   - 条件对象按 import > default 取；require 跳过（v0 不做 CJS）
//
// 不支持：通配子路径（"./*"）、多层嵌套条件的复杂组合。落空时返回
// matched=false，调用方退化为直接文件解析并列出候选。

func matchExports(raw any, key string) (any, bool) {
	switch v := raw.(type) {
	case string:
		if key == "." {
			return v, true
		}
		return nil, false
	case map[string]any:
		if hasSubpathKeys(v) {
			target, ok := v[key]
			if !ok {
				return nil, false
			}
			return selectCondition(target), true
		}
		// 无 "." 键 → 直接挂在 exports 上的条件表，只服务根入口
		if key == "." {
			return selectCondition(v), true
		}
		return nil, false
	}
	return nil, false
}

// hasSubpathKeys 判断 exports 对象是否是"子路径表"（至少有一个键以 "." 开头）。

func hasSubpathKeys(m map[string]any) bool {
	for k := range m {
		if strings.HasPrefix(k, ".") {
			return true
		}
	}
	return false
}

// selectCondition 从 exports 目标值里按条件优先级选出一个路径字符串。
// v1 条件优先级: import > default。require 刻意跳过（Gox v0 不做 CommonJS，
// 见 docs/npm-compat.md）。数组取第一个能选出的元素。

func selectCondition(target any) any {
	switch v := target.(type) {
	case string:
		return v
	case []any:
		for _, e := range v {
			if r := selectCondition(e); r != nil {
				return r
			}
		}
		return nil
	case map[string]any:
		for _, cond := range []string{"import", "default"} {
			if t, ok := v[cond]; ok {
				if r := selectCondition(t); r != nil {
					return r
				}
			}
		}
		return nil // 其它条件（browser/node/types…）v1 不认
	}
	return nil
}

// resolvePackageTarget 把 exports 目标（相对包目录的 "./..." 路径）解析为文件。
// 非 "./" 开头的目标（指向其它包的裸说明符）v1 不支持。

func resolvePackageTarget(pkgDir string, target any, add func(string)) (string, bool) {
	s, ok := target.(string)
	if !ok || s == "" || !strings.HasPrefix(s, "./") {
		return "", false
	}
	return resolvePackageFile(filepath.Join(pkgDir, filepath.FromSlash(s)), add)
}

// resolvePackageFile 把一个"可能是文件 / 可能是目录 / 可能缺后缀"的目标
// 解析为真实文件。尝试过的路径都通过 add 记录（便于报错排错）。

func resolvePackageFile(target string, add func(string)) (string, bool) {
	if fi, err := os.Stat(target); err == nil {
		if !fi.IsDir() {
			return target, true
		}
		// 命中目录 → 其下 index.*
		for _, ext := range moduleEntryExtensions {
			p := filepath.Join(target, "index"+ext)
			add(p)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p, true
			}
		}
		return "", false
	}
	// 缺后缀 → 依次补后缀
	for _, ext := range moduleEntryExtensions {
		p := target + ext
		add(p)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p, true
		}
	}
	return "", false
}

// readPackageJSON 读取并解析 package.json。失败返回 (nil, false)，
// 调用方按"无 package.json 的老式包"处理。

func readPackageJSON(path string) (map[string]any, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	// 用 any 而非强类型，是因为 exports 的形态多变（字符串/对象/数组嵌套）。
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, false
	}
	return m, true
}

// resolveModuleFile 把模块说明符解析为磁盘上真实存在的文件。
//
// 在 "spec 原样就是文件" 的传统行为上为 TS 模板补三条解析规则:
//  1. "./app.js" 找不到时依次尝试 "./app.ts" "./app.tsx" —— TS 官方 ESM
//     风格鼓励源码里写 .js 后缀（即编译产物的后缀）；
//  2. 无后缀的 spec 依次尝试 spec + ".js" / ".ts" / ".tsx"；
//  3. 命中目录时依次尝试其下 index.js / index.ts / index.tsx。
//
// 全部落空时错误信息列出全部尝试过的候选，一眼看出差的是哪个文件。

func (vm *VM) resolveModuleFile(spec string) (string, error) {
	abs := func(p string) string { return resolvePath(vm.moduleBase, p) }

	var candidates []string
	seen := map[string]bool{}
	add := func(p string) {
		a := abs(p)
		if !seen[a] {
			seen[a] = true
			candidates = append(candidates, a)
		}
	}

	switch {
	case strings.HasSuffix(spec, ".ts") || strings.HasSuffix(spec, ".tsx") ||
		strings.HasSuffix(spec, ".mts") || strings.HasSuffix(spec, ".cts") ||
		strings.HasSuffix(spec, ".jsx"):
		// 显式 TS 家族后缀: 原样一个候选
		add(spec)
	default:
		add(spec)
		trimmed := strings.TrimSuffix(spec, ".js")
		if trimmed != spec {
			// "./app.js" → "./app.ts" / "./app.tsx"
			add(trimmed + ".ts")
			add(trimmed + ".tsx")
		} else {
			// 无后缀 → 补 .js / .ts / .tsx
			add(spec + ".js")
			add(spec + ".ts")
			add(spec + ".tsx")
		}
	}

	// 目录 index: 复制一份候选再迭代 (循环里要追加)
	for _, cand := range append([]string(nil), candidates...) {
		if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
			add(filepath.Join(cand, "index.js"))
			add(filepath.Join(cand, "index.ts"))
			add(filepath.Join(cand, "index.tsx"))
		}
	}

	for _, cand := range candidates {
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand, nil
		}
	}
	return "", fmt.Errorf("Cannot find module '%s' (tried: %s)",
		spec, strings.Join(candidates, ", "))
}

// dirOf 返回路径的目录部分。

func dirOf(path string) string {
	return filepath.Dir(path)
}

// storeGlobalBinding 把 val 写入全局环境里 name 的绑定, 处理三种形态:
//
//  1. 访问器绑定 (Object.defineProperty(globalThis, "x", {get/set})): 有 setter
//     时调用它 (规范 Object Environment Record 的 SetMutableBinding 走
//     Set(bindings, N, V, S), 即对属性做 [[Set]]); 只读访问器在严格模式下
//     应抛 TypeError, 这里按「静默忽略」处理以与 Gox 既有的「不建模属性写入
//     失败」保持一致 —— 真值语义由 x ^= 3 那条 test262 用例覆盖 getter 侧。
//  2. 已存在的普通绑定: 更新其值。
//  3. 不存在: 隐式创建 globalThis 的自有可配置属性 (sloppy 语义; 调用方已
//     保证 strict 路径不会走到这里)。

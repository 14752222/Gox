package vm

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/compiler"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
	"github.com/14752222/Gox/stdlib"
	"github.com/14752222/Gox/tstransform"
)

// MaxFrames 是调用栈最大深度 (防止无限递归)。
const MaxFrames = 2048

// stackTraceOn: GOX_TRACE_STACK=1 时逐指令打印 op/pc/栈深 (T04 栈失衡定位)。
var stackTraceOn = os.Getenv("GOX_TRACE_STACK") == "1"

// ThrowError 包装 JS throw 抛出的值，用于在 Go 错误返回链中传递。
type ThrowError struct {
	Value object.Value
}

func (e *ThrowError) Error() string {
	return e.Value.Inspect()
}

// thrownDisplayString 渲染顶层**未捕获**抛值的展示文本: 优先按 ToString
// 语义, 对非 Error 对象调用其原型链/自有属性上的**用户自定义** toString
// (JS 闭包) 并取其字符串结果; 缺失、非闭包 (如内建 Object.prototype.toString)、
// 调用抛错、panic 或返回非字符串时, 回退到既有 Inspect (对象字面量) 渲染。
//
//   - Error 实例: 维持 Inspect ("Name: message"), 行为不变;
//   - 原始值 (string/number/...): Inspect 即 ToString 展示, 维持既有;
//   - 用户对象带自定义 toString: 调用之 (Test262Error / {toString(){...}});
//   - 其余 (裸对象等): 回退 Inspect, 不退化成 "[object Object]"。
//
// 渲染路径绝不被用户 toString 带崩: callToString 内恢复 currentVM 并以
// recover 兜底。
func (vm *VM) thrownDisplayString(v object.Value) string {
	if v == nil {
		return "undefined"
	}
	if _, isErr := v.(*object.Error); isErr {
		return v.Inspect()
	}
	obj, ok := v.(*object.Object)
	if !ok {
		return v.Inspect()
	}
	ts, exists := obj.GetProperty("toString")
	if !exists {
		return v.Inspect()
	}
	closure, isClosure := ts.(*object.Closure)
	if !isClosure {
		// 内建 toString (如 Object.prototype.toString → "[object Object]")
		// 不算自定义, 回退 Inspect 以免裸对象丢信息。
		return v.Inspect()
	}
	if s, ok := vm.callToString(closure, obj); ok {
		return s
	}
	return v.Inspect()
}

// callToString 在恢复 currentVM 与 panic 保护下调用用户的 toString(闭包),
// 返回其字符串结果; toString 抛错/panic/返回非字符串 ⇒ ok=false (回退 Inspect)。
func (vm *VM) callToString(fn *object.Closure, this object.Value) (result string, ok bool) {
	// 顶层 execute() 已返回、currentVM 被恢复为旧值; 渲染时要调回调桥
	// (object.CallFunction → currentVM.callFunction) 执行用户 toString,
	// 故在此重新注册 currentVM, 退出时恢复。
	saved := currentVM
	currentVM = vm
	defer func() {
		currentVM = saved
		if r := recover(); r != nil {
			result, ok = "", false
		}
	}()
	rv := object.CallFunction(fn, this)
	if object.TakeCallbackError() != nil {
		return "", false
	}
	if s, isStr := rv.(*object.String); isStr {
		return s.Value, true
	}
	return "", false
}

// uncaughtError 渲染顶层未捕获错误: throw 出的值按 thrownDisplayString
// (ToString 语义 + Inspect 回退) 渲染, 其余错误维持既有形式; 尽力附带源码帧。
// 渲染后的消息使错误**首行**即 ToString(thrownValue) 结果 —— test262 runner
// 对 negative.runtime 的 NegType 匹配 (首行) 由此能见到如 "Test262Error"。
func (vm *VM) uncaughtError(err error) error {
	framed := vm.AttachFrame(err) // 先在有 throw 位置状态时算好源码帧
	frame := ""
	if fe, ok := framed.(*FrameError); ok {
		frame = fe.Frame
	}
	var te *ThrowError
	if errors.As(err, &te) {
		msg := "vm error: " + vm.thrownDisplayString(te.Value)
		if frame != "" {
			return &FrameError{Inner: errors.New(msg), Frame: frame}
		}
		return errors.New(msg)
	}
	return fmt.Errorf("vm error: %v", framed)
}

// YieldSignal 表示 generator 执行到 yield 时的暂停信号。
// genResume 捕获该信号后保存状态并返回 (value, done=false)。
type YieldSignal struct {
	gen   *object.Generator
	value object.Value
}

func (e *YieldSignal) Error() string {
	return "yield"
}

// tryEntry 是 try-catch-finally 的处理器条目。
type tryEntry struct {
	catchPC   int // catch 块的 PC (0 = 无 catch)
	finallyPC int // finally 块的 PC (0 = 无 finally)
	stackBase int // 进入 try 时的栈高度
	frameIdx  int // 进入 try 时的帧索引

	// inFinally 标记本条目已进入自己的 finally 体 (由 handleThrowInner 置位)。
	//
	// 此时条目**留在 tryStack 上**, 由 pendingVal 承载挂起的异常值, 直到:
	//   - finally 体正常结束 ⇒ OP_END_FINALLY 弹出它并重新抛出 pendingVal;
	//   - finally 体里又抛出异常 ⇒ 展开经过本条目时把它丢弃 (新异常覆盖旧异常)。
	//
	// 挂起值不放在 VM 全局变量里, 是因为旧的全局 pendingThrow 有两类坏形状:
	//   (1) finally 里 return / throw 直接离开本帧 ⇒ 挂起值没机会被清, 残留到
	//       之后某个毫不相干的 END_FINALLY 上被误重抛;
	//   (2) 嵌套 finally 互相覆盖 ⇒ 外层挂起值被内层清掉, 原异常被静默吞掉。
	// 挂在条目上则天然随条目出栈而消失, 嵌套时各条目各持一份。
	inFinally bool
	// pendingVal 是进入 finally 时挂起的异常值 (inFinally 为真时有效)。
	pendingVal object.Value

	// pendingReturn 标记挂起值是一个 **return 完成** 而非 throw 完成
	// (inFinally 为真时有效)。async generator 的 return() 从挂起点注入
	// `return v` 时需要展开体内 finally: 进入 finally 时置位, OP_END_FINALLY
	// 结束时据此把完成向外传播而不是重新抛出。
	pendingReturn bool
}

// ModuleExports 存储模块的导出。
//
// 导出有三种来源, 解析优先级 (规范: 显式命名导出 > export *):
//  1. Named/Default/bindings: 本模块的直接导出 (default 走 Default, 词法声明走
//     bindings 活绑定);
//  2. forwards: 具名再导出 `export {a as b} from "m"` 与命名空间再导出
//     `export * as ns from "m"` —— 读时才去源模块导出槽取值, 是"取值时解析"的
//     活绑定等效语义 (见 resolve);
//  3. stars: `export * from "m"` —— 转发源模块的自有可枚举导出, **不含 default**,
//     且**不覆盖**本模块已存在的同名导出 (规范 16.2.3.5 GetExportedNames /
//     ResolveExport)。
//
// 枚举顺序口径 (Object.keys(namespace)): 先按本模块导出登记顺序 (order),
// 再按星号源出现顺序拼接各自登记顺序并去重; 没有登记顺序的导出 (内置模块直接
// 塞进 Named 的 map) 按名字字典序补在末尾。Go map 迭代是随机的, 不排序会让
// namespace 每次物化的键序都不同 —— 这里给一个稳定口径。
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

// buildNamespace 把模块导出物化成一个命名空间对象。
//
// 这是导入方唯一拿到的"模块视图": OP_IMPORT / 动态 import / `export * as ns`
// 都走它。因为是物化时逐名调用 resolve, 再导出转发读到的就是源模块导出槽的
// **当前**值 —— 即"取值时读取源模块导出槽"的语义 (比在 export 语句处立刻拷贝
// 更接近规范的活绑定)。
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
		obj.SetProperty(name, v)
	}
	if v, ok := m.resolveWith("default", map[*ModuleExports]bool{}, building); ok {
		obj.SetProperty("default", v)
	}
	return obj
}

// currentVM 是当前正在执行的 VM 实例。
// 用于从 stdlib 回调 JS 闭包时找到正确的 VM。
var currentVM *VM

// setCallbackErrorValueFromThrow 把 Go 侧的运行期错误还原成原始 JS 抛出值,
// 存入 object 层供 stdlib 作为 rejection reason。
//
// 必须走这里而不是让 stdlib 重新包一层: callbackError 只是 Go 字符串
// (已含 "Error: " 前缀), 重新包一层会得到 "Error: Error: x" 双前缀,
// 且丢失原始 Error 对象 —— 而规范要求 catch 侧拿到的与抛出的
// 严格相等 (===)。
func setCallbackErrorValueFromThrow(err error) {
	if te, ok := err.(*ThrowError); ok {
		object.SetCallbackErrorValue(te.Value)
	} else if jt, ok := err.(*jsThrow); ok {
		// 栈溢出等结构化错误: 恢复为对应类型的 Error 对象,
		// 供 stdlib 的 callbackThrown 保留错误类型传播
		object.SetCallbackErrorValue(object.NewErrorWithName(jt.Name, jt.Message))
	}
}

func init() {
	// 注册回调桥: stdlib → object → vm
	//
	// 错误信号统一走 object 层的 SetCallbackError/TakeCallbackError:
	// 消费即清除。过去 VM 还有一份自己的 callbackErr 字段，只在个别
	// OP_CALL 位点被顺手消费 —— stdlib 层消费错误信号后 VM 层的副本
	// 会永久残留，之后任何一个内建函数调用都会被这个陈旧错误"击落"
	// (表现为回调链莫名中断且无任何报告)。
	object.SetCallFunction(func(fn object.Value, this object.Value, args []object.Value) object.Value {
		if currentVM == nil {
			return object.UndefinedSingleton
		}
		result, err := currentVM.callFunction(fn, this, args)
		if err != nil {
			// 向 object 层暴露错误信号: 返回值无法区分
			// "函数正常返回" 与 "函数抛出了非 Error 异常"。
			object.SetCallbackError(err)
			// 保留原始抛出值 (throw x 的 x), 供 rejection reason 使用
			setCallbackErrorValueFromThrow(err)
			return object.UndefinedSingleton
		}
		return result
	})
	// 注册 generator 驱动回调: object.GeneratorNext → vm.genResume
	object.SetGeneratorNext(func(gen *object.Generator, arg object.Value) (object.Value, bool) {
		if currentVM == nil {
			return object.UndefinedSingleton, true
		}
		val, done, err := currentVM.genResume(gen, arg)
		if err != nil {
			object.SetCallbackError(err)
			setCallbackErrorValueFromThrow(err)
			return object.UndefinedSingleton, true
		}
		return val, done
	})
	// 注册 generator 异常恢复回调: object.GeneratorThrow → vm.genThrow
	object.SetGeneratorThrow(func(gen *object.Generator, throwVal object.Value) (object.Value, bool) {
		if currentVM == nil {
			return object.UndefinedSingleton, true
		}
		val, done, err := currentVM.genThrow(gen, throwVal)
		if err != nil {
			object.SetCallbackError(err)
			setCallbackErrorValueFromThrow(err)
			return object.UndefinedSingleton, true
		}
		return val, done
	})
	// 注册 generator return 完成回调: object.GeneratorReturn → vm.genReturn
	object.SetGeneratorReturn(func(gen *object.Generator, returnVal object.Value) (object.Value, bool) {
		if currentVM == nil {
			return returnVal, true
		}
		val, done, err := currentVM.genReturn(gen, returnVal)
		if err != nil {
			object.SetCallbackError(err)
			setCallbackErrorValueFromThrow(err)
			return object.UndefinedSingleton, true
		}
		return val, done
	})
}

// VM 是 JavaScript 字节码虚拟机。
type VM struct {
	frames     []*Frame             // 调用栈
	frameIdx   int                  // 当前帧索引 (栈顶)
	stack      *Stack               // 操作数栈
	globals    *runtime.Environment // 全局变量环境
	constants  *bytecode.ConstantPool
	lastPopped object.Value // 最后弹出的值 (用于测试)

	// try-catch-finally 支持。
	// 「进入 finally 后待重抛的异常」不是 VM 全局状态: 它挂在 tryStack 的条目上
	// (tryEntry.inFinally / pendingVal), 这样控制转移离开 finally 时不会残留。
	tryStack []tryEntry // try 处理器栈

	// 模块系统
	modules        map[string]*ModuleExports // 模块缓存 (按绝对路径)
	moduleBase     string                    // 模块基准路径 (用于解析相对路径)
	currentExports *ModuleExports            // 当前模块的导出对象
	// moduleMode 标记"本 VM 的主帧是模块顶层"。仅影响主帧 this 取值:
	// script 顶层 this = globalThis, module 顶层 this = undefined (见 OP_THIS)。
	moduleMode bool

	// generator 支持
	currentGenerator *object.Generator // 当前正在执行的 generator (OP_YIELD 时使用)

	// throwBoundary 是当前 runFrom 子执行允许解退到的最低帧索引。
	// 回调桥 (callFunction→runFrom) 的子帧里抛出的异常，不允许直接
	// 解退到外层帧的 try 处理器 —— 否则帧/PC 被改写而 Go 侧的内建
	// 循环 (如 map) 仍在继续执行，错误值会被误当作回调返回值。
	// 边界外的处理器留给错误传播回外层后、由外层的抛出路径匹配。
	throwBoundary int

	// pendingDirectEval 标记「紧接着的这次调用是直接 eval 候选」(OP_EVAL_MARK /
	// OP_EVAL_MARK_INIT 置位)。OP_CALL / OP_CALL_SPREAD 取出后立即清零, 仅当
	// 被调恰为全局 %eval% 内建时才把调用者帧的 this 传给 eval 内建。按调用
	// 取用 (而非 stdlib 包级全局常驻) 可避免多 VM 并发互相干扰与遮蔽泄漏。
	pendingDirectEval bool

	// pendingEvalInit 是 pendingDirectEval 的细化: 本次直接 eval 发生在类
	// 字段初始化器内 (OP_EVAL_MARK_INIT 置位)。命中时额外让 eval 内建进入
	// 受限早错模式。随 pendingDirectEval 一同清零。
	pendingEvalInit bool

	// pendingEvalHome* / hasPendingEvalHome: OP_EVAL_MARK_HOME 家族的
	// 指令携带的「直接 eval 调用点 super home 上下文」(roiE5Z)。名字取自
	// 指令操作数 (常量池里的类名/父类名, 执行标记的帧解析)。与 this /
	// new.target 两桥同纪律: 紧跟的 OP_CALL / OP_CALL_SPREAD 经
	// consumeEvalMark 取出并清零 (无论被调是否恰为 %eval%), 不会泄漏给
	// 后续无关调用; 不带 home 语境的 OP_EVAL_MARK 也会清空它们 (表示
	// 「该语境无 home」)。注意 OP_EVAL_MARK_INIT **不清** home —— 它可
	// 叠加在 HOME 标记之后 (字段初始化器 + home 语境)。
	pendingEvalHomeName   string
	pendingEvalHomeStatic bool
	pendingEvalHomeThis   bool
	pendingEvalSuperName  string
	hasPendingEvalHome    bool

	// pendingNewTarget / hasPendingNewTarget: super() 调用的 new.target 传递。
	// OP_NEW_TARGET_MARK (编译器在 super(...) 前发射) 把**当前帧的 new.target**
	// 记到这里; 紧随其后的方法调用装配父构造器帧时消费 (见 callClosure)。
	// 规范 sec-super-keyword: Construct(func, argList, GetNewTarget()) —— 父构造器
	// 里的 new.target 即派生类最初的构造目标。OP_NEW 也复用同一通道 (写被 new 的
	// 构造器)。消费即清零, 非 super 调用不置位。
	pendingNewTarget    object.Value
	hasPendingNewTarget bool

	// 模板字面量分段收集器 (支持嵌套): 每层对应一个 OP_TEMPLATE_START，
	// 该层内 quasi/表达式产生的字符串依次 append，OP_TEMPLATE_END 时 join 入栈。
	// 不用操作数栈保存段的原因是模板可能作为二元运算的操作数出现——
	// 栈上模板段之外还有外层操作数，无法区分边界。
	tplParts [][]object.Value

	// ── 运行时错误源码帧 (T05 / M2) ──
	// mainPositions 是主脚本"语句首条指令 offset → 源码位置"的升序表;
	// 函数体错误查抛出帧闭包的 Positions 表 (offset 空间按编译单元隔离)。
	//
	// M2: 位置表记录的是**编译单元 (转译后 JS)** 的行列, 而展示要给用户 .ts 原文。
	// 所以源码信息按"编译单元"建模为 srcUnit:
	//   mainUnit  当前脚本 (入口/模块顶层) 的单元;
	//   units     *CompiledFunction → 它所属单元的注册表 (跨 VM 共享 —— 模块 A
	//             导出的函数可能被入口调用, 那时抛错帧属于 A 的单元, 而当前 VM
	//             是入口)。注册发生在 createClosure。
	srcFile        string // 顶层脚本文件名 (兼容 SetSourceInfo 的读取方)
	srcText        string // 顶层脚本文本
	mainUnit       *srcUnit
	units          *unitRegistry
	mainPositions  []object.SrcPos
	lastThrowPC    int
	hasThrowPC     bool
	lastThrowFrame *Frame

	// ── panic 诊断 (T04) ──
	// 主循环每取指一条记录一次, panic 恢复时输出, 用于定位 VM 缺陷。
	dbgOp     bytecode.Opcode // 最近取出的 opcode
	dbgPC     int             // 该指令的起始 PC (取指前)
	dbgFrames int             // 当前帧数
}

// New 创建虚拟机。
// ins: 主程序字节码, constants: 常量池, numLocals: 主程序局部变量数。
func New(ins bytecode.Instructions, constants *bytecode.ConstantPool, numLocals int) *VM {
	frames := make([]*Frame, MaxFrames)
	frames[0] = NewFrame(ins, constants, numLocals)
	return &VM{
		frames:    frames,
		frameIdx:  0,
		stack:     NewStack(),
		globals:   runtime.NewEnvironment(),
		constants: constants,
		modules:   map[string]*ModuleExports{},
		units:     newUnitRegistry(),
	}
}

// NewWithGlobals 创建带预设全局变量的虚拟机。
func NewWithGlobals(ins bytecode.Instructions, constants *bytecode.ConstantPool, numLocals int, globals *runtime.Environment) *VM {
	frames := make([]*Frame, MaxFrames)
	frames[0] = NewFrame(ins, constants, numLocals)
	return &VM{
		frames:    frames,
		frameIdx:  0,
		stack:     NewStack(),
		globals:   globals,
		constants: constants,
		modules:   map[string]*ModuleExports{},
		units:     newUnitRegistry(),
	}
}

// Globals 返回全局环境。
func (vm *VM) Globals() *runtime.Environment { return vm.globals }

// RunTimers 运行定时器事件循环。
// 阻塞等待下一个定时器到期并执行其回调，直到没有活跃定时器。
// 返回执行期间遇到的错误 (如有)。
func (vm *VM) RunTimers() error {
	return vm.RunTimersUntil(time.Time{})
}

// RunTimersUntil 运行定时器事件循环直到指定时间 (或没有活跃任务)。
// 用于等待定时器回调执行完成。
// 同时驱动 requestIdleCallback: 事件循环空闲 (无到期任务) 时派发空闲回调,
// 空闲回调的 deadline.timeRemaining() 预算为距下一个定时任务的时间 (上限 50ms)。
// 同时消费严格定时器 (setStrictInterval/setStrictTimeout) 的到期信号:
// 在等待普通定时器的空闲期内批量执行严格定时器回调, 保证两者在同一线程串行执行。
func (vm *VM) RunTimersUntil(until time.Time) error {
	return vm.runTimersLoopProtected(until, nil)
}

// RunTimersWithPump 运行事件循环, 空闲等待交给外部事件泵。
//
// pump(maxWait) 应在最多 maxWait 时间内等待并处理外部事件 (如窗口消息):
//   - maxWait > 0: 等待外部事件或超时, 二者先到即返回
//   - maxWait <= 0: 无定时任务时无限期等待外部事件
//   - 返回 false 表示外部事件源已关闭 (如窗口销毁), 事件循环退出
//
// 与 RunTimers 的关键差异: 没有定时器任务时循环不会退出 —— 生命周期由
// pump 决定。GUI 模式用它把消息泵接入定时器调度 (所有回调仍在同一线程
// 串行执行)。
func (vm *VM) RunTimersWithPump(pump func(maxWait time.Duration) bool) error {
	return vm.runTimersLoopProtected(time.Time{}, pump)
}

// runTimersLoopProtected 注册 currentVM 并在 panic 保护下运行事件循环。
// pump 为 nil 时是纯定时器语义 (RunTimersUntil), 非 nil 时由 pump 接管
// 所有空闲等待。
func (vm *VM) runTimersLoopProtected(until time.Time, pump func(maxWait time.Duration) bool) error {
	// 事件循环期间注册 currentVM，保证回调链中的 object.CallFunction 桥
	// (如 Promise resolve 触发的 .then 回调) 能找到正确的 VM 实例。
	// 主脚本执行结束后 currentVM 会被恢复为 nil，若不在此处重新注册，
	// 定时器回调里 resolve 的 Promise 的 then 回调会被静默丢弃。
	saved := currentVM
	currentVM = vm
	defer func() { currentVM = saved }()

	return vm.runProtected(func() error { return vm.runTimersLoop(until, pump) })
}

// runProtected 在 defer recover 中执行 fn。
// VM/stdlib 某处缺陷导致的 Go panic 不再直接杀死整个进程，而是转为
// InternalError 结束当前执行。注意: panic 后 VM 状态可能已损坏，
// 恢复出的错误必须向外传播、不能吞掉后继续用同一 VM 跑。
func (vm *VM) runProtected(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			// GOX_PANIC_TRACE=1 时输出 Go 调用栈: VM 内部的越界/空指针
			// 是引擎缺陷, 栈是定位它的第一手资料 (T04 修复辅助)。
			if os.Getenv("GOX_PANIC_TRACE") != "" {
				fmt.Fprintln(os.Stderr, "=== GOX panic trace ===")
				fmt.Fprintf(os.Stderr, "vm state: op=%v pc=%d frames=%d stackDepth=%d\n",
					vm.dbgOp, vm.dbgPC, vm.dbgFrames, vm.stack.Len())
				// 反汇编 panic 帧附近指令窗口: PC 已越过当前指令 3 字节。
				if vm.frameIdx >= 0 && vm.frames[vm.frameIdx] != nil {
					fi := vm.frames[vm.frameIdx]
					start := vm.dbgPC - 10*bytecode.InstructionSize
					if start < 0 {
						start = 0
					}
					end := vm.dbgPC + 6*bytecode.InstructionSize
					if end > len(fi.Instructions) {
						end = len(fi.Instructions)
					}
					fmt.Fprintf(os.Stderr, "--- frame#%d instructions around pc=%d ---\n", vm.frameIdx, vm.dbgPC)
					for pc := start; pc < end; pc += bytecode.InstructionSize {
						op := bytecode.ReadOpcode(fi.Instructions, pc)
						mark := "   "
						if pc == vm.dbgPC {
							mark = ">> "
						}
						fmt.Fprintf(os.Stderr, "%s%04d  %-18s %d\n", mark, pc, op.Name(), bytecode.ReadOperand(fi.Instructions, pc+1))
					}
					for i := 0; i <= vm.frameIdx; i++ {
						if fr := vm.frames[i]; fr != nil {
							fmt.Fprintf(os.Stderr, "frame#%d StackBase=%d locals=%d\n", i, fr.StackBase, len(fr.Locals))
						}
					}
					fmt.Fprintf(os.Stderr, "tryStack=%d entries:", len(vm.tryStack))
					for _, te := range vm.tryStack {
						fmt.Fprintf(os.Stderr, " {catch:%d frame:%d base:%d}", te.catchPC, te.frameIdx, te.stackBase)
					}
					fmt.Fprintln(os.Stderr)
				}
				debug.PrintStack()
			}
			err = fmt.Errorf("InternalError: VM panic: %v", r)
		}
	}()
	return fn()
}

// runTimersLoop 是事件循环主循环体 (由 runTimersLoopProtected 包裹执行)。
// pump 非 nil 时 (GUI 模式), 所有空闲等待交给 pump 处理外部事件。
func (vm *VM) runTimersLoop(until time.Time, pump func(maxWait time.Duration) bool) error {
	scheduler := object.GlobalScheduler()
	strict := object.GlobalStrictScheduler()
	for {
		// 0) 严格定时器到期信号优先消费 (排队/抢占模式共用此入口)。
		//    有信号时先执行, 保证回调尽早运行。
		if interrupts := strict.TakeStrictInterrupts(64); len(interrupts) > 0 {
			if err := vm.runStrictDispatches(interrupts, until); err != nil {
				return err
			}
			continue
		}

		// 退出时间检查
		if !until.IsZero() {
			now := time.Now()
			if !until.After(now) {
				return nil
			}
		}

		// 1) 已超时的空闲回调: 无论是否空闲都强制派发 (didTimeout=true)
		if expired := scheduler.ExpiredIdleCallbacks(); len(expired) > 0 {
			if err := vm.dispatchIdleCallbacks(expired, true, 0); err != nil {
				return err
			}
			continue
		}

		// 计算下一次唤醒源: 普通定时器/空闲超时 与 严格定时器 取最早。
		wait, found := scheduler.NextFireIn()
		strictWait, strictFound := strict.NextStrictFireIn()
		if strictFound && (!found || strictWait < wait) {
			wait = strictWait
			found = true
		}

		// 2) 空闲期派发: 无任何任务, 或下一个任务前有足够间隙
		if (!found || wait >= object.IdleMinGap) && scheduler.HasIdleCallbacks() {
			if cd := scheduler.IdleDispatchCooldown(); cd > 0 {
				if !until.IsZero() && time.Now().Add(cd).After(until) {
					return nil
				}
				if pump != nil {
					if !pump(cd) {
						return nil
					}
				} else {
					time.Sleep(cd)
				}
				continue
			}
			budget := object.IdleBudget
			if found && wait < budget {
				budget = wait
			}
			cbs := scheduler.TakeIdleCallbacks()
			if err := vm.dispatchIdleCallbacks(cbs, false, budget); err != nil {
				return err
			}
			continue
		}

		if !found {
			// 无普通定时器、无即将到期的严格定时器、无空闲回调。
			// 但派发队列里可能仍有残留信号 (一次性严格定时器触发后已从
			// 调度器移除, NextStrictFireIn 返回 false, 信号却还在队列中),
			// 必须先消费完再退出, 否则回调被静默丢弃。
			if interrupts := strict.TakeStrictInterrupts(64); len(interrupts) > 0 {
				if err := vm.runStrictDispatches(interrupts, until); err != nil {
					return err
				}
				continue
			}
			if pump == nil {
				return nil
			}
			// GUI 模式: 无定时器任务也持续泵外部事件, 直到事件源关闭。
			// maxWait<=0 表示无限期等待外部事件。
			if !pump(0) {
				return nil
			}
			continue
		}

		// 等待到最早唤醒点。
		if pump != nil {
			// GUI 模式: 等待期间由 pump 处理窗口消息; 严格定时器信号在
			// 循环顶部下一轮消费, 不会积压丢失。
			if !pump(wait) {
				return nil
			}
		} else {
			// 等待期间持续检查严格定时器信号,
			// 避免排队模式信号积压 (回调尽量及时, 绝不丢弃)。
			for wait > 0 {
				if interrupts := strict.TakeStrictInterrupts(64); len(interrupts) > 0 {
					if err := vm.runStrictDispatches(interrupts, until); err != nil {
						return err
					}
					break
				}
				sleepFor := wait
				if !until.IsZero() {
					if remain := time.Until(until); remain < sleepFor {
						sleepFor = remain
					}
				}
				if sleepFor <= 0 {
					break
				}
				time.Sleep(sleepFor)
				wait = 0
			}
		}

		// 执行到期的普通定时器
		due := scheduler.DueTimers()
		for _, t := range due {
			if !t.Active || t.Callback == nil {
				continue
			}
			// 超过退出时间则不再执行新回调 (正在执行的回调无法中断)
			if !until.IsZero() && !time.Now().Before(until) {
				scheduler.Reschedule(t)
				continue
			}
			_, err := vm.callFunction(t.Callback, nil, nil)
			if err != nil {
				scheduler.Reschedule(t)
				return err
			}
			scheduler.Reschedule(t)
		}
	}
}

// runStrictDispatches 执行一批严格定时器到期信号。
// 为每个信号构造 info 参数对象 (scheduledTime/dueTime/early/late/skipped),
// 并在当前 VM 上下文调用其回调。队列模式与抢占模式共用此执行路径。
// until 非零时, 超过该时间不再启动新的回调 (单个正在执行的回调无法中断)。
func (vm *VM) runStrictDispatches(dispatches []object.StrictDispatch, until time.Time) error {
	for _, d := range dispatches {
		t := d.Ticker
		if t == nil || !t.Active.Load() || t.Callback == nil {
			continue
		}
		if !until.IsZero() && !time.Now().Before(until) {
			return nil
		}
		info := newStrictInfo(d.ScheduledTime, d.DueTime)
		if _, err := vm.callFunction(t.Callback, nil, []object.Value{info}); err != nil {
			return err
		}
		// 一次性定时器执行后失效
		if !t.Repeat {
			t.Active.Store(false)
		}
	}
	return nil
}

// newStrictInfo 构造严格定时器回调的 info 参数对象。
func newStrictInfo(scheduled, due time.Time) *object.Object {
	info := object.NewObject()
	info.SetProperty("scheduledTime", object.NewNumber(float64(scheduled.UnixMilli())))
	info.SetProperty("dueTime", object.NewNumber(float64(due.UnixMilli())))
	early := scheduled.Sub(due)
	if early < 0 {
		early = 0
	}
	late := due.Sub(scheduled)
	if late < 0 {
		late = 0
	}
	info.SetProperty("early", object.NewNumber(float64(early)/float64(time.Millisecond)))
	info.SetProperty("late", object.NewNumber(float64(late)/float64(time.Millisecond)))
	info.SetProperty("skipped", object.NewNumber(0))
	return info
}

// dispatchIdleCallbacks 派发空闲回调, 为每个回调构造 deadline 参数对象。
// didTimeout=true 表示因 options.timeout 到期被迫派发 (此时预算为 0)。
func (vm *VM) dispatchIdleCallbacks(cbs []*object.IdleCallback, didTimeout bool, budget time.Duration) error {
	for _, cb := range cbs {
		if !cb.Active || cb.Callback == nil {
			continue
		}
		deadline := newIdleDeadline(budget, didTimeout)
		if _, err := vm.callFunction(cb.Callback, nil, []object.Value{deadline}); err != nil {
			return err
		}
	}
	return nil
}

// newIdleDeadline 构造 requestIdleCallback 的 deadline 参数对象:
//   - didTimeout: 是否因超时被迫派发
//   - timeRemaining(): 剩余时间预算 (毫秒), 随时间递减, 最小为 0
func newIdleDeadline(budget time.Duration, didTimeout bool) *object.Object {
	start := time.Now()
	d := object.NewObject()
	d.SetProperty("didTimeout", object.NewBoolean(didTimeout))
	d.SetProperty("timeRemaining", object.NewBuiltin("timeRemaining", func(args ...object.Value) object.Value {
		rem := budget - time.Since(start)
		if rem < 0 {
			rem = 0
		}
		return object.NewNumber(float64(rem) / float64(time.Millisecond))
	}))
	return d
}

// LastPopped 返回最后从栈弹出的值。
func (vm *VM) LastPopped() object.Value { return vm.lastPopped }

// currentFrame 返回当前执行帧。
func (vm *VM) currentFrame() *Frame { return vm.frames[vm.frameIdx] }

// pushFrame 压入新帧。
func (vm *VM) pushFrame(f *Frame) {
	vm.frameIdx++
	vm.frames[vm.frameIdx] = f
}

// popFrame 弹出当前帧。
// 传播闭包变量修改到上一帧: 仅当闭包创建于上一帧时传播修改过的 slot。
func (vm *VM) popFrame() *Frame {
	f := vm.frames[vm.frameIdx]
	vm.frames[vm.frameIdx] = nil
	vm.frameIdx--

	// 同步解退本帧注册的 try 处理器: try 块内 return / 异常透传等
	// 路径会跳过 OP_POP_TRY, 残留的死条目会在后续 throw 时被
	// handleThrow 消费 —— 把调用帧的 PC 劫持到已退出帧的 catchPC,
	// 字节码跨编译单元串台, 随即栈失衡 panic。
	for len(vm.tryStack) > 0 && vm.tryStack[len(vm.tryStack)-1].frameIdx > vm.frameIdx {
		vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]
	}

	// 传播闭包变量修改 (closure → outer frame)
	// 仅当闭包创建于上一帧时才传播，避免跨帧变量错位
	if f.Closure != nil && f.ModifiedSlots != nil && len(f.ModifiedSlots) > 0 && vm.frameIdx >= 0 {
		if f.Closure.CreatedAtFrame == vm.frameIdx {
			outerFrame := vm.frames[vm.frameIdx]
			if outerFrame != nil {
				for slot := range f.ModifiedSlots {
					if slot < len(f.Closure.CapturedLocals) && slot < len(outerFrame.Locals) {
						val := f.Closure.CapturedLocals[slot]
						outerFrame.Locals[slot] = val
						if outerFrame.Closure != nil && slot < len(outerFrame.Closure.CapturedLocals) {
							outerFrame.Closure.CapturedLocals[slot] = val
						}
					}
				}
			}
		}
	}

	return f
}

// Run 启动 VM 执行循环。
func (vm *VM) Run() error {
	return vm.execute()
}

// RunCompiled 从编译器输出启动执行。
func (vm *VM) RunCompiled(c *compiler.Compiler) error {
	vm.frames[0] = NewFrame(c.Bytes(), c.Constants(), c.NumLocals())
	vm.frameIdx = 0
	return vm.execute()
}

// RunCompiledAsync 以"主单元即生成器"的方式驱动含顶层 await 的模块。
//
// 模块顶层是 +Await 上下文 (TLA): 顶层 await 被编译成主单元字节码里的
// OP_YIELD 挂起点。这里把主单元包装成一个无闭包生成器 (Closure==nil),
// 逐次恢复; 每次挂起后按 await 语义结算操作数。
//
// 已知边界 (本版刻意不做): 结算建立在 Gox 的"同步 Promise 模型"上 ——
// 非 Promise 原样透传, 已结算的 Promise 由 Then 回调同步取值。
// **pending** 的 Promise (典型: `await new Promise(r => setTimeout(r, 0))`)
// 需要真实异步模块图求值 (§16.2.1.5.2 ExecuteAsyncModule /
// AsyncModuleExecutionFulfilled), 本版不实现: 直接报错而不是在模块加载期
// 重入事件循环 —— 模块加载发生在入口脚本执行期, 期间跑事件循环会污染
// runner 的包级状态并导致分片超时 (实测)。
//
// 与 RunCompiled 的差别仅在驱动方式: 主帧仍是 frame#0, 顶层声明的槽位与
// OP_EXPORT_BINDING 的读取器都指向同一份 Locals (rebuildGenFrame 对
// Closure==nil 的生成器强制复用数组)。
func (vm *VM) RunCompiledAsync(c *compiler.Compiler) error {
	gen := &object.Generator{
		PC:            0,
		Started:       false,
		PrologueBound: true, // 首帧由 gen.PC/Locals 直接重建, 无闭包可调用
		Locals:        make([]object.Value, c.NumLocals()),
		Constants:     c.Constants().Constants,
		Instructions:  c.Bytes(),
	}
	saved := currentVM
	currentVM = vm
	defer func() { currentVM = saved }()
	return vm.runProtected(func() error { return vm.driveMainUnit(gen) })
}

// driveMainUnit 驱动主单元生成器直到完成。
func (vm *VM) driveMainUnit(gen *object.Generator) error {
	val, done, err := vm.genResume(gen, object.UndefinedSingleton)
	for !done {
		if err != nil {
			return err
		}
		res, thrown := vm.settleTopLevelAwait(val)
		if thrown != nil {
			// await 的 Promise 被 reject: 作为异常抛回生成器 (体内 try/catch 可捕获)
			val, done, err = vm.genThrow(gen, thrown)
		} else {
			val, done, err = vm.genResume(gen, res)
		}
	}
	return err
}

// settleTopLevelAwait 结算一个顶层 await 的操作数, 返回 (resolved, thrown)。
// thrown 非 nil 表示该值是被 reject 的 Promise。
//
// 非 Promise 原样透传; 已结算 Promise 由 Then/Catch 回调同步给出值 (Gox
// 同步 Promise 模型); pending Promise 不支持 (见 RunCompiledAsync 说明)。
func (vm *VM) settleTopLevelAwait(val object.Value) (object.Value, object.Value) {
	p, ok := val.(*object.Promise)
	if !ok {
		return val, nil // 非 Promise: await 原样透传
	}
	var got bool
	var resolved object.Value
	var thrown object.Value
	p.Then(object.NewBuiltin("__tla_ok", func(args ...object.Value) object.Value {
		got = true
		if len(args) > 0 {
			resolved = args[0]
		} else {
			resolved = object.UndefinedSingleton
		}
		return object.UndefinedSingleton
	}))
	p.Catch(object.NewBuiltin("__tla_err", func(args ...object.Value) object.Value {
		got = true
		if len(args) > 0 {
			thrown = args[0]
		} else {
			thrown = object.UndefinedSingleton
		}
		return object.UndefinedSingleton
	}))
	if !got {
		return object.UndefinedSingleton,
			object.NewErrorWithName("Error", "top-level await: promise did not settle synchronously")
	}
	if thrown != nil {
		return object.UndefinedSingleton, thrown
	}
	return resolved, nil
}

// execute 设置当前 VM 并从主帧开始执行。
func (vm *VM) execute() error {
	saved := currentVM
	currentVM = vm
	defer func() { currentVM = saved }()
	return vm.runProtected(func() error { return vm.runFrom(0) })
}

// runFrom 从指定帧索引开始执行指令循环。
// startFrameIdx: 起始帧索引，循环持续到 vm.frameIdx < startFrameIdx。
// 用于主程序执行 (startFrameIdx=0) 和回调子帧执行。
func (vm *VM) runFrom(startFrameIdx int) error {
	savedBoundary := vm.throwBoundary
	vm.throwBoundary = startFrameIdx
	defer func() { vm.throwBoundary = savedBoundary }()
	for vm.frameIdx >= startFrameIdx {
		frame := vm.currentFrame()

		// 形参前导帧: 前导段执行完毕 (PC 到达 StopPC) 即冻结为 Generator,
		// 函数体留待首次 next()。冻结时把 Generator 放到调用点的结果槽位。
		if frame.PendingGen != nil && frame.PC >= frame.StopPC {
			vm.freezeGenPrologue(frame)
			continue
		}

		// 检查是否到达字节码末尾
		if frame.PC >= len(frame.Instructions) {
			if vm.frameIdx == 0 {
				// 主程序结束: 返回栈顶值 (如果有)
				if vm.stack.Len() > 0 {
					vm.lastPopped = vm.stack.Pop()
				}
				return nil
			}
			// 函数没有显式 return (应该有 OP_RETURN_VOID)
			base := frame.StackBase
			vm.popFrame()
			if vm.stack.Len() > base {
				vm.stack.Truncate(base)
			}
			vm.stack.Push(object.UndefinedSingleton)
			continue
		}

		// 取指
		op := bytecode.ReadOpcode(frame.Instructions, frame.PC)
		operand := bytecode.ReadOperand(frame.Instructions, frame.PC+1)
		vm.dbgOp, vm.dbgPC, vm.dbgFrames = op, frame.PC, vm.frameIdx+1
		if stackTraceOn {
			fmt.Fprintf(os.Stderr, "[trace] f#%d pc=%-5d %-14s depth=%d\n", vm.frameIdx, frame.PC, op.Name(), vm.stack.Len())
		}
		frame.PC += bytecode.InstructionSize

		// 解码-执行
		switch op {
		// ===== 栈操作 =====
		case bytecode.OP_NOP:
			// 空操作
		case bytecode.OP_POP:
			vm.lastPopped = vm.stack.Pop()
		case bytecode.OP_DUP:
			vm.stack.Push(vm.stack.Peek())
		case bytecode.OP_SWAP:
			a := vm.stack.Pop()
			b := vm.stack.Pop()
			vm.stack.Push(a)
			vm.stack.Push(b)
		case bytecode.OP_ITER_BOUNDARY:
			// 迭代边界: 换一组 binding cell，本轮迭代创建的闭包就此"定版"。
			//
			// 闭包共享创建帧的 Locals 数组 (而非快照)，所以每一次 STORE 都会被
			// 前几轮创建的闭包看到。这里把 Locals 换成当前值的一份拷贝:
			//   - 已创建的闭包仍引用旧数组 → 保留本轮迭代的值；
			//   - 后续迭代写入新数组 → 不再回溯影响它们。
			// 即 ECMAScript 的 per-iteration binding，在本 VM 的 cell 模型下的等价实现。
			old := frame.Locals
			fresh := make([]object.Value, len(old))
			copy(fresh, old)
			frame.Locals = fresh
			// 同时切断 OP_STORE 的"向本帧创建的子闭包传播"这条更老的路径:
			// 它会直接写 c.CapturedLocals[slot]，而那正是旧数组，等同于把后续
			// 迭代的值写回已经定版的闭包。
			frame.CreatedClosures = nil
		case bytecode.OP_DUP_BELOW2:
			// [a, b, c] → [c, a, b, c]: 栈顶值复制一份并插到下方两个值之下
			cVal := vm.stack.Pop()
			bVal := vm.stack.Pop()
			aVal := vm.stack.Pop()
			vm.stack.Push(cVal)
			vm.stack.Push(aVal)
			vm.stack.Push(bVal)
			vm.stack.Push(cVal)
		case bytecode.OP_DUP2:
			// 复制栈顶两个值并保持顺序: [a, b] → [a, b, a, b]。
			// 成员复合赋值 (obj.k += v) 需要它: obj/key 各留一份供 SET_INDEX，
			// 同时顶部保留一份供 GET_INDEX 取旧值。
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			vm.stack.Push(a)
			vm.stack.Push(b)
			vm.stack.Push(a)
			vm.stack.Push(b)
		case bytecode.OP_POP_N:
			n := int(operand)
			for i := 0; i < n; i++ {
				vm.stack.Pop()
			}

		// ===== 常量加载 =====
		case bytecode.OP_CONST:
			vm.stack.Push(frame.Constants.Get(operand))
		case bytecode.OP_NULL:
			vm.stack.Push(object.NullSingleton)
		case bytecode.OP_UNDEFINED:
			vm.stack.Push(object.UndefinedSingleton)
		case bytecode.OP_TRUE:
			vm.stack.Push(object.NewBoolean(true))
		case bytecode.OP_FALSE:
			vm.stack.Push(object.NewBoolean(false))
		case bytecode.OP_INT:
			// operand 直接作为 int16 值
			val := int16(operand)
			vm.stack.Push(object.NewNumber(float64(val)))

		// ===== 变量操作 =====
		case bytecode.OP_LOAD:
			slot := int(operand)
			if slot >= len(frame.Locals) {
				return fmt.Errorf("VM: LOAD slot %d out of range (locals: %d)", slot, len(frame.Locals))
			}
			val := frame.Locals[slot]
			if val == nil {
				// TDZ: let/const 绑定在执行到声明语句前不可访问。
				// 与 ECMAScript 一致抛 ReferenceError，而非返回 undefined。
				if terr := vm.throwJSError(&jsThrow{
					"ReferenceError",
					"Cannot access lexical declaration before initialization",
				}); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(val)
		case bytecode.OP_STORE:
			slot := int(operand)
			val := vm.stack.Pop()
			if slot >= len(frame.Locals) {
				for len(frame.Locals) <= slot {
					frame.Locals = append(frame.Locals, object.UndefinedSingleton)
				}
			}
			frame.Locals[slot] = val
			// 0. 写回共享 binding cell (兄弟闭包与后续调用据此观察到新值)
			if slot < len(frame.SharedCells) {
				frame.SharedCells[slot] = val
			}
			// 1. 更新当前帧闭包的捕获变量 (closure → 同一闭包下次调用)
			if frame.Closure != nil && slot < len(frame.Closure.CapturedLocals) {
				frame.Closure.CapturedLocals[slot] = val
			}
			// 2. 向本帧创建的子闭包传播外层变量修改 (outer → closure)
			for _, c := range frame.CreatedClosures {
				if slot < len(c.CapturedLocals) {
					c.CapturedLocals[slot] = val
				}
			}
			// 3. 标记 slot 为已修改 (用于 popFrame 时向上一帧传播)
			if frame.ModifiedSlots == nil {
				frame.ModifiedSlots = make(map[int]bool)
			}
			frame.ModifiedSlots[slot] = true
		case bytecode.OP_STORE_CONST:
			slot := int(operand)
			val := vm.stack.Pop()
			if slot >= len(frame.Locals) {
				for len(frame.Locals) <= slot {
					frame.Locals = append(frame.Locals, object.UndefinedSingleton)
				}
			}
			frame.Locals[slot] = val
			// 0. 写回共享 binding cell
			if slot < len(frame.SharedCells) {
				frame.SharedCells[slot] = val
			}
			// 1. 更新当前帧闭包的捕获变量
			if frame.Closure != nil && slot < len(frame.Closure.CapturedLocals) {
				frame.Closure.CapturedLocals[slot] = val
			}
			// 2. 向子闭包传播
			for _, c := range frame.CreatedClosures {
				if slot < len(c.CapturedLocals) {
					c.CapturedLocals[slot] = val
				}
			}
			// 3. 标记为已修改
			if frame.ModifiedSlots == nil {
				frame.ModifiedSlots = make(map[int]bool)
			}
			frame.ModifiedSlots[slot] = true
		case bytecode.OP_STORE_CONST_GUARD:
			// 对已声明的局部 const 槽位赋值 —— 无条件拒绝 (可被 try/catch 捕获)。
			// 与 OP_STORE_CONST 的分工: 那条只在**声明期**发射一次 (槽位可能
			// 尚为 TDZ 的 nil), 这条只出现在**赋值位置**, 此时 const 必已初始化,
			// 故直接抛 TypeError。弹出值以保持栈平衡 (编译器在存储前 DUP 保留了
			// 表达式结果, 与 OP_STORE 一致)。
			vm.stack.Pop()
			name := ""
			if s, ok := frame.Constants.Get(operand).(*object.String); ok {
				name = s.Value
			}
			if err := vm.throwNamedError("TypeError", "Assignment to constant variable: %s", name); err != nil {
				return err
			}
		case bytecode.OP_LOAD_GLOBAL:
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				val, found := vm.globals.Get(s.Value)
				if !found {
					if err := vm.throwNamedError("ReferenceError", "%s is not defined", s.Value); err != nil {
						return err
					}
					continue
				}
				// 全局访问器绑定 (Object.defineProperty(globalThis, "x", {get})):
				// 全局环境记录的绑定值存的是 *object.Accessor, 必须显式调用
				// getter 取回属性值 —— 否则读出来的是 Accessor 对象本身
				// (typeof x 得 "object"、`x ^= 3` 把 Accessor 当左值参与运算)。
				// 与 GlobalObject.GetProperty / unscopablesBlocked 同纪律:
				// getter 是用户闭包, 经回调桥可能抛出, 立即消费。
				if acc, isAcc := val.(*object.Accessor); isAcc {
					if acc.Getter == nil || !object.IsCallable(acc.Getter) {
						vm.stack.Push(object.UndefinedSingleton)
						continue
					}
					res := object.CallFunction(acc.Getter, vm.globalThisValue())
					if err := vm.checkCallbackErr(); err != nil {
						if terr := vm.rethrowBridgeError(err); terr != nil {
							return terr
						}
						continue
					}
					vm.stack.Push(res)
					continue
				}
				vm.stack.Push(val)
			}
		case bytecode.OP_STORE_GLOBAL:
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				// 弹出值 (与 OP_STORE 语义一致)。若需保留表达式结果, 编译器会在存储前 DUP。
				val := vm.stack.Pop()
				if vm.globals.IsConst(s.Value) {
					if err := vm.throwNamedError("TypeError", "Assignment to constant variable: %s", s.Value); err != nil {
						return err
					}
					continue
				}
				if err := vm.storeGlobalBinding(s.Value, val); err != nil {
					return err
				}
			}
		case bytecode.OP_STORE_UNDECLARED:
			// 严格模式下对**未声明名字**的赋值 (编译器在 sym==nil 且 strict 时发射)。
			// 与 OP_STORE_GLOBAL 相反: 运行期环境里没有同名绑定就抛 ReferenceError,
			// 而不是隐式建全局属性。宿主注入名 (self/console/$DONE/内建) 都在全局
			// 环境里, 故 `console = ...` 之类照常写入; 只拦真正未声明的名字。
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				val := vm.stack.Pop()
				if vm.globals.IsConst(s.Value) {
					if err := vm.throwNamedError("TypeError", "Assignment to constant variable: %s", s.Value); err != nil {
						return err
					}
					continue
				}
				if _, exists := vm.globals.Get(s.Value); exists {
					if err := vm.storeGlobalBinding(s.Value, val); err != nil {
						return err
					}
				} else {
					if err := vm.throwNamedError("ReferenceError", "%s is not defined", s.Value); err != nil {
						return err
					}
					continue
				}
			}
		case bytecode.OP_DECLARE:
			// 顶层 let/class/import 声明: 弹出值写入全局环境。
			// 与持久化的全局词法绑定冲突时报 SyntaxError (不可被 try/catch 捕获，
			// 与规范的早期错误语义一致)。REPL 每行独立编译，跨行重声明在此发现。
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				val := vm.stack.Pop()
				if err := vm.globals.DeclareGlobal(s.Value, val, false, false); err != nil {
					return err
				}
			}
		case bytecode.OP_DECLARE_CONST:
			// 顶层 const 声明: 弹出值写入全局环境 (const 词法绑定)。
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				val := vm.stack.Pop()
				if err := vm.globals.DeclareGlobal(s.Value, val, true, false); err != nil {
					return err
				}
			}
		case bytecode.OP_DECLARE_FUNC:
			// 顶层函数声明: 允许函数互相重定义，但不可覆盖已有词法声明。
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				val := vm.stack.Pop()
				if err := vm.globals.DeclareGlobal(s.Value, val, false, true); err != nil {
					return err
				}
			}
		case bytecode.OP_DECLARE_VAR:
			// 顶层 var 提升期绑定创建: var 是 globalThis 的自有属性 (不可配置)。
			// 与 let 的 OP_DECLARE 区分 —— 后者只进全局词法环境，不是自有属性。
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				val := vm.stack.Pop()
				if err := vm.globals.DeclareGlobalVar(s.Value, val); err != nil {
					return err
				}
			}

		// ===== 算术运算 =====
		case bytecode.OP_ADD:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			result, err := vm.addValues(a, b)
			if err != nil {
				if terr := vm.throwJSError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(result)
		case bytecode.OP_SUB, bytecode.OP_MUL, bytecode.OP_DIV, bytecode.OP_MOD,
			bytecode.OP_POW, bytecode.OP_BIT_AND, bytecode.OP_BIT_OR, bytecode.OP_BIT_XOR,
			bytecode.OP_SHL, bytecode.OP_SHR, bytecode.OP_USHR:
			// BigInt 参与时走独立分支: JS 禁止 BigInt 与 Number 隐式混合运算。
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			result, err := vm.binaryArithmetic(op, a, b)
			if err != nil {
				if terr := vm.throwJSError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(result)
		case bytecode.OP_NEG, bytecode.OP_BIT_NOT:
			a := vm.stack.Pop()
			result, err := vm.unaryArithmetic(op, a)
			if err != nil {
				if terr := vm.throwJSError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(result)

		// ===== 比较和逻辑 =====
		case bytecode.OP_EQ:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			vm.stack.Push(object.NewBoolean(looseEquals(a, b)))
		case bytecode.OP_NOT_EQ:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			vm.stack.Push(object.NewBoolean(!looseEquals(a, b)))
		case bytecode.OP_STRICT_EQ:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			vm.stack.Push(object.NewBoolean(strictEquals(a, b)))
		case bytecode.OP_STRICT_NE:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			vm.stack.Push(object.NewBoolean(!strictEquals(a, b)))
		case bytecode.OP_LT, bytecode.OP_GT, bytecode.OP_LTE, bytecode.OP_GTE:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			res, err := relationalCompare(op, a, b)
			if err != nil {
				return err
			}
			vm.stack.Push(object.NewBoolean(res))
		case bytecode.OP_NOT:
			a := vm.stack.Pop()
			vm.stack.Push(object.NewBoolean(object.IsFalsy(a)))
		case bytecode.OP_AND, bytecode.OP_OR:
			// 短路逻辑由编译器处理，这里不应该直接执行
			// 但如果出现，按二元操作处理
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			if op == bytecode.OP_AND {
				vm.stack.Push(object.NewBoolean(a.IsTruthy() && b.IsTruthy()))
			} else {
				vm.stack.Push(object.NewBoolean(a.IsTruthy() || b.IsTruthy()))
			}
		case bytecode.OP_NULL_COALESCE:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			if _, isNull := a.(*object.Null); isNull {
				vm.stack.Push(b)
			} else if _, isUndef := a.(*object.Undefined); isUndef {
				vm.stack.Push(b)
			} else {
				vm.stack.Push(a)
			}

		// ===== 跳转 =====
		case bytecode.OP_JUMP:
			frame.PC = int(operand)
		case bytecode.OP_JUMP_IF_TRUE:
			if vm.stack.Peek().IsTruthy() {
				frame.PC = int(operand)
			}
		case bytecode.OP_JUMP_IF_TRUE_POP:
			// 条件真: 弹出条件值并跳转; 假: 不弹 (由后续 POP 弹), 继续。
			// switch case 匹配专用: 消除"体入口 POP"导致的 fall-through 多弹。
			if vm.stack.Peek().IsTruthy() {
				vm.stack.Pop()
				frame.PC = int(operand)
			}
		case bytecode.OP_JUMP_IF_FALSE:
			if !vm.stack.Peek().IsTruthy() {
				frame.PC = int(operand)
			}
		case bytecode.OP_JUMP_IF_NULL:
			val := vm.stack.Peek()
			if _, isNull := val.(*object.Null); isNull {
				frame.PC = int(operand)
			} else if _, isUndef := val.(*object.Undefined); isUndef {
				frame.PC = int(operand)
			}
		case bytecode.OP_JUMP_IF_NOT_NULL:
			val := vm.stack.Peek()
			_, isNull := val.(*object.Null)
			_, isUndef := val.(*object.Undefined)
			if !isNull && !isUndef {
				frame.PC = int(operand)
			}
		case bytecode.OP_LOOP:
			frame.PC = int(operand)

		// ===== 函数 =====
		case bytecode.OP_FUNCTION, bytecode.OP_ARROW_FUNC:
			fnMeta := frame.Constants.Get(operand)
			meta, ok := fnMeta.(*bytecode.FunctionMetadata)
			if !ok {
				return fmt.Errorf("VM: expected FunctionMetadata at constant %d, got %T", operand, fnMeta)
			}
			// 创建 Closure
			closure := vm.createClosure(meta, frame)
			vm.trackClosure(closure)
			vm.stack.Push(closure)
		case bytecode.OP_CLOSURE:
			// 同 OP_FUNCTION
			fnMeta := frame.Constants.Get(operand)
			meta, ok := fnMeta.(*bytecode.FunctionMetadata)
			if !ok {
				return fmt.Errorf("VM: expected FunctionMetadata at constant %d", operand)
			}
			closure := vm.createClosure(meta, frame)
			vm.trackClosure(closure)
			vm.stack.Push(closure)
		case bytecode.OP_CALL:
			numArgs := int(operand)
			// 弹出函数
			fn := vm.stack.Pop()
			// 上一指令若是 OP_EVAL_MARK, 这一拍就是被标记的直接 eval 调用。
			// 仅当被调确为全局 %eval% 内建时才置受限标志 (其余情况丢弃)。
			vm.consumeEvalMark(fn)
			// 收集参数 (栈上是 arg1, arg2, ..., argN, 逆序弹出)
			args := make([]object.Value, numArgs)
			for i := numArgs - 1; i >= 0; i-- {
				args[i] = vm.stack.Pop()
			}

			switch callee := fn.(type) {
			case *object.BuiltinFunction:
				result := callee.Fn(args...)
				if result == nil {
					result = object.UndefinedSingleton
				}
				// 返回 *object.Error 的内建函数: 作为异常抛出 (Error 构造器除外)
				if thrown, err := vm.throwIfError(result, callee.ReturnIsValue); thrown {
					if err != nil {
						return err
					}
					continue
				}
				vm.stack.Push(result)
				if err := vm.checkCallbackErr(); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
			case *object.Closure:
				// generator 函数调用: 不执行函数体, 返回 Generator 对象。
				// 有形参前导段 (默认值/解构) 时, 先建立前导帧 —— 由主循环
				// 同步执行前导段后冻结为 Generator (规范: 形参绑定在调用时完成)。
				if callee.Fn != nil && callee.Fn.IsGenerator {
					if genHasPrologue(callee.Fn) {
						if err := vm.setupGenPrologueFrame(callee, args); err != nil {
							if terr := vm.throwJSError(err); terr != nil {
								return terr
							}
							continue
						}
					} else {
						vm.stack.Push(object.NewGenerator(callee, args))
					}
				} else {
					if err := vm.callClosure(callee, args); err != nil {
						if terr := vm.throwJSError(err); terr != nil {
							return terr
						}
						continue
					}
				}

			case *object.Proxy:
				// 代理: 转发到 apply trap (this 为 undefined)
				result, err := vm.proxyApply(callee, object.UndefinedSingleton, args)
				if err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				vm.stack.Push(result)

			case object.ObservableState:
				// Rx 单元直接调用: count() 等价 count.value (GetX 语义)
				vm.stack.Push(callee.RxValue())

			default:
				if err := vm.throwNamedError("TypeError", "%s is not a function", describeCallee(fn)); err != nil {
					return err
				}
				continue
			}

		case bytecode.OP_RETURN:
			val := vm.stack.Pop()
			base := frame.StackBase
			vm.popFrame()
			// 截断本帧残留的栈值, 防止污染调用方栈
			if vm.stack.Len() > base {
				vm.stack.Truncate(base)
			}
			vm.stack.Push(val)
		case bytecode.OP_RETURN_VOID:
			base := frame.StackBase
			vm.popFrame()
			if vm.stack.Len() > base {
				vm.stack.Truncate(base)
			}
			vm.stack.Push(object.UndefinedSingleton)
		case bytecode.OP_YIELD, bytecode.OP_AWAIT:
			// generator 的 yield / async generator 体内的 await:
			// 弹出表达式值, 保存帧状态, 暂停执行。
			// 恢复时 (genResume) 压入传入的 arg 作为 yield/await 表达式的值。
			val := vm.stack.Pop()
			gen := vm.currentGenerator
			if gen == nil {
				return fmt.Errorf("TypeError: yield outside generator")
			}
			// OP_AWAIT 标记为内部挂起点 (async generator 驱动据此自动恢复);
			// OP_YIELD 是消费者可见的 yield 挂起点。
			gen.LastYieldIsAwait = op == bytecode.OP_AWAIT
			curFrame := vm.currentFrame()
			gen.PC = curFrame.PC // 恢复点: yield 之后的下一条指令
			gen.Locals = curFrame.Locals
			gen.Constants = curFrame.Constants.Constants
			gen.Instructions = curFrame.Instructions
			// 保存属于 generator 帧的 try 处理器条目。挂起期间不能留在全局
			// tryStack 上: 恢复时帧深度可能不同，且无关代码抛出的异常绝不能
			// 被挂起中的 generator 捕获。栈基址/帧索引保存相对值。
			gen.PendingTries = nil
			for len(vm.tryStack) > 0 && vm.tryStack[len(vm.tryStack)-1].frameIdx >= vm.frameIdx {
				te := vm.tryStack[len(vm.tryStack)-1]
				vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]
				gen.PendingTries = append([]object.GenTryEntry{{
					CatchPC:       te.catchPC,
					FinallyPC:     te.finallyPC,
					RelStackBase:  te.stackBase - curFrame.StackBase,
					RelFrameIdx:   te.frameIdx - vm.frameIdx,
					InFinally:     te.inFinally,
					PendingVal:    te.pendingVal,
					PendingReturn: te.pendingReturn,
				}}, gen.PendingTries...)
			}
			// 保存帧栈残留的中间值 (如 2 + (yield 3) 中的 2)
			if vm.stack.Len() > curFrame.StackBase {
				n := vm.stack.Len() - curFrame.StackBase
				gen.SavedStack = make([]object.Value, n)
				for i := 0; i < n; i++ {
					gen.SavedStack[i] = vm.stack.PeekAt(n - 1 - i)
				}
				vm.stack.Truncate(curFrame.StackBase)
			} else {
				gen.SavedStack = nil
			}
			vm.popFrame()
			return &YieldSignal{gen: gen, value: val}
		case bytecode.OP_CALL_SPREAD:
			// 参数在数组中，栈: [args_array, func]
			fn := vm.stack.Pop()
			vm.consumeEvalMark(fn)
			arr := vm.stack.Pop()
			var args []object.Value
			if a, ok := arr.(*object.Array); ok {
				args = a.Elements
			}
			switch callee := fn.(type) {
			case *object.BuiltinFunction:
				result := callee.Fn(args...)
				if result == nil {
					result = object.UndefinedSingleton
				}
				if thrown, err := vm.throwIfError(result, callee.ReturnIsValue); thrown {
					if err != nil {
						return err
					}
					continue
				}
				vm.stack.Push(result)
				if err := vm.checkCallbackErr(); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
			case *object.Closure:
				if err := vm.callClosure(callee, args); err != nil {
					if terr := vm.throwJSError(err); terr != nil {
						return terr
					}
					continue
				}
			case *object.Proxy:
				result, err := vm.proxyApply(callee, object.UndefinedSingleton, args)
				if err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				vm.stack.Push(result)
			default:
				if err := vm.throwNamedError("TypeError", "%s is not a function", describeCallee(fn)); err != nil {
					return err
				}
				continue
			}
		case bytecode.OP_CALL_METHOD:
			// 方法调用: 栈 [fn, this, arg1, ..., argN]
			numArgs := int(operand)
			args := make([]object.Value, numArgs)
			for i := numArgs - 1; i >= 0; i-- {
				args[i] = vm.stack.Pop()
			}
			thisVal := vm.stack.Pop()
			fn := vm.stack.Pop()
			if err := vm.invokeWithThis(fn, thisVal, args); err != nil {
				return err
			}
		case bytecode.OP_CALL_METHOD_SPREAD:
			// 方法调用但实参在数组里: 栈 [fn, this, argsArray]。
			// 隐式 constructor 的 super(...arguments) 与用户手写 super(...args) 走这里。
			arr := vm.stack.Pop()
			thisVal := vm.stack.Pop()
			fn := vm.stack.Pop()
			var args []object.Value
			if a, ok := arr.(*object.Array); ok {
				args = a.Elements
			}
			if err := vm.invokeWithThis(fn, thisVal, args); err != nil {
				return err
			}
		case bytecode.OP_EVAL_MARK:
			// 编译器在「直接 eval」调用前发射此指令。只在本 VM 上置
			// 「下一次调用是直接 eval 候选」标记, 无栈效果。真正生效由紧随
			// 其后的 OP_CALL / OP_CALL_SPREAD 判定: 仅当被调恰为全局 %eval%
			// 内建时才把调用者帧的 this 传给 eval, 否则丢弃 —— eval 被局部
			// 变量遮蔽时既不继承 this 也不会泄漏给后续调用。
			//
			// 不带 home 语境的直接 eval 只发此标记: 同时清空 pending 的
			// super home (roiE5Z), 表示「该语境无 home, eval 源码里的
			// super.x 应维持 SyntaxError」。带 home 语境的调用点不发此标记
			// (改发 HOME 家族, 见下), 避免把刚设置的 home 又清掉。
			vm.pendingDirectEval = true
			vm.clearPendingEvalHome()
		case bytecode.OP_EVAL_MARK_INIT:
			// 同 OP_EVAL_MARK, 但额外标记「类字段初始化器内」—— 让 eval 内建
			// 进入受限早错模式 (PerformEval 补充早错)。
			// 注意: 不清 home —— 字段初始化器本身有 home (类 prototype),
			// 该语境由前置的 OP_EVAL_MARK_HOME 标记携带 (roiE5Z)。
			vm.pendingDirectEval = true
			vm.pendingEvalInit = true
		case bytecode.OP_EVAL_MARK_HOME:
			// roiE5Z: 直接 eval 调用点带 super home (实例语境, 类方法 /
			// 字段初始化器 / 箭头继承的外层)。operand = home 类名常量索引。
			vm.pendingDirectEval = true
			vm.clearPendingEvalHome()
			vm.setPendingEvalHomeName(operand, frame)
		case bytecode.OP_EVAL_MARK_HOME_STATIC:
			// roiE5Z: 静态语境的 home (static 方法 / 静态初始化块)。
			// operand = home 类名常量索引。
			vm.pendingDirectEval = true
			vm.clearPendingEvalHome()
			vm.pendingEvalHomeStatic = true
			vm.setPendingEvalHomeName(operand, frame)
		case bytecode.OP_EVAL_MARK_HOME_THIS:
			// roiE5Z: 对象字面量方法的 home —— 无可加载的名字, 运行期取
			// 调用者帧的 this。consumeEvalMark 依 HomeThis 置桥。
			vm.pendingDirectEval = true
			vm.clearPendingEvalHome()
			vm.pendingEvalHomeThis = true
			vm.hasPendingEvalHome = true
		case bytecode.OP_EVAL_MARK_SUPER:
			// roiE5Z: 静态语境且类有 extends —— 父类名 (静态 super base)。
			// 与 HOME_STATIC 成对出现 (HOME_STATIC 在前, 不清后续标记)。
			vm.pendingDirectEval = true
			if s := vm.markName(operand, frame); s != "" {
				vm.pendingEvalSuperName = s
			}
		case bytecode.OP_NEW_TARGET_MARK:
			// 编译器在 super(...) 调用前发射。把当前帧生效的 new.target 记为
			// 「下一次调用要继承的构造目标」, 由紧随其后的方法调用装配父构造器帧
			// 时消费 (callClosure)。无栈效果。非 super 调用不发射, 不继承。
			if f := vm.currentFrame(); f != nil {
				vm.pendingNewTarget = f.NewTarget
				vm.hasPendingNewTarget = true
			}
		case bytecode.OP_NEW:
			// new Constructor(args...) — 简化实现
			numArgs := int(operand)
			fn := vm.stack.Pop()
			args := make([]object.Value, numArgs)
			for i := numArgs - 1; i >= 0; i-- {
				args[i] = vm.stack.Pop()
			}
			// 创建新对象
			newObj := object.NewObject()
			if closure, ok := fn.(*object.Closure); ok {
				// generator / async 函数不是构造器: new 必须抛 TypeError
				// (规范: 它们没有 [[Construct]])。旧实现把 new generator 当作
				// 普通调用返回 Generator，与规范不符且掩盖用法错误。
				if closure.Fn != nil && (closure.Fn.IsGenerator || closure.Fn.IsAsync) {
					if err := vm.throwNamedError("TypeError", "%s is not a constructor", describeCallee(fn)); err != nil {
						return err
					}
					continue
				}
				// 设置新对象的原型为构造函数的 prototype (支持 instanceof)
				if !closure.IsArrow {
					if cp, _ := closure.GetProperty("prototype"); cp != nil {
						if protoObj, ok := cp.(*object.Object); ok {
							newObj.Proto = protoObj
						} else {
							newObj.Proto = cp
						}
					}
				}
				// 设置 this 为新对象
				//
				// 必须结转 CapturedLocals: 构造函数体里对外层作用域绑定的引用
				// （module 模式下的 `super`、嵌套类里对 enclosing 变量的取值等）
				// 编成 OP_LOAD slot，靠帧装配时拷贝 CapturedLocals 前缀取值。
				// 漏掉它 → 前缀全 nil → 读外层绑定报 TDZ（rIIXSR，2026-10-05）。
				newClosure := &object.Closure{
					Fn:             closure.Fn,
					Env:            closure.Env,
					This:           newObj,
					NewTarget:      closure.NewTarget,
					IsArrow:        closure.IsArrow,
					CapturedLocals: closure.CapturedLocals,
					CreatedAtFrame: closure.CreatedAtFrame,
				}
				// 调用构造函数 (同步执行到返回)
				//
				// 错误必须先回收本帧再走抛出流程 (与 OP_CALL_METHOD 的
				// callClosure/runFrom 错误路径同一约定)。此前直接 `return err`:
				//   - 有问题的错误逃过 handleThrow ⇒ 外层 `try { new X() } catch`
				//     抓不到 (rVI6Eb);
				//   - 且不回收构造函数帧 ⇒ 帧栈残留。
				//
				// 构造调用: 被 new 的构造器即本帧 new.target (规范 EvaluateNew →
				// Construct(constructor, argList), newTarget 缺省为 constructor)。
				vm.pendingNewTarget = fn
				vm.hasPendingNewTarget = true
				startIdx := vm.frameIdx + 1
				if err := vm.callClosure(newClosure, args); err != nil {
					vm.unwindFramesTo(startIdx)
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				if err := vm.runFrom(startIdx); err != nil {
					vm.unwindFramesTo(startIdx)
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				// 如果构造函数返回对象，使用返回的对象
				result := vm.stack.Pop()
				if _, isObj := result.(*object.Object); isObj {
					// 使用构造函数返回的对象
					vm.stack.Push(result)
				} else {
					// 使用 newObj
					vm.stack.Push(newObj)
				}
			} else if builtin, ok := fn.(*object.BuiltinFunction); ok {
				result := builtin.Fn(args...)
				if result == nil {
					result = newObj
				}
				// 内建构造器返回 Error 对象时抛出异常 (如 new RegExp("[") → SyntaxError)
				// ReturnIsValue 的构造器 (如 Error) 返回的 Error 是值，不抛出
				if errObj, isErr := result.(*object.Error); isErr && !builtin.ReturnIsValue {
					if !vm.handleThrow(errObj) {
						return &ThrowError{Value: errObj}
					}
					continue
				}
				vm.stack.Push(result)
				if err := vm.checkCallbackErr(); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
			} else if proxy, ok := fn.(*object.Proxy); ok {
				// 代理构造: 转发到 construct trap
				result, err := vm.proxyConstruct(proxy, args)
				if err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				vm.stack.Push(result)
			} else {
				if err := vm.throwNamedError("TypeError", "%s is not a constructor", describeCallee(fn)); err != nil {
					return err
				}
				continue
			}

		// ===== 对象和数组 =====
		case bytecode.OP_NEW_ARRAY:
			n := int(operand)
			elements := make([]object.Value, n)
			for i := n - 1; i >= 0; i-- {
				elements[i] = vm.stack.Pop()
			}
			vm.stack.Push(object.NewArray(elements))
		case bytecode.OP_NEW_OBJECT:
			// 对象字面量 / class 基础类的 prototype 对象: 规范里都是
			// "普通对象", [[Prototype]] = %Object.prototype%。
			vm.stack.Push(object.NewPlainObject())
		case bytecode.OP_SET_PROTO:
			// 栈: [obj, parent] → obj.Proto = parent
			parent := vm.stack.Pop()
			obj := vm.stack.Peek()
			if o, ok := obj.(*object.Object); ok {
				o.Proto = parent
			}
		case bytecode.OP_GET_PROTO:
			// 栈: [obj] → [proto] (roiE5Z: eval 单元 super.x 的 base 解析)。
			// proto 为 nil (原型链尽头) 时压 null, 与规范
			// Object.getPrototypeOf(Object.prototype) === null 一致。
			obj := vm.stack.Pop()
			proto := protoOf(obj)
			if proto == nil {
				proto = object.NullSingleton
			}
			vm.stack.Push(proto)
		case bytecode.OP_GET_PROP:
			propNameVal := frame.Constants.Get(operand)
			propName := ""
			if s, ok := propNameVal.(*object.String); ok {
				propName = s.Value
			}
			obj := vm.stack.Pop()
			// null/undefined 属性读取抛 TypeError (规范要求；
			// 静默返回 undefined 会掩盖程序错误)
			if obj == object.NullSingleton || obj == object.UndefinedSingleton {
				if err := vm.throwNamedError("TypeError",
					"Cannot read properties of %s (reading '%s')", obj.Inspect(), propName); err != nil {
					return err
				}
				continue
			}
			// Proxy: 转发到 get trap
			if proxy, ok := obj.(*object.Proxy); ok {
				val, err := vm.proxyGet(proxy, propName, obj)
				if err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				vm.stack.Push(val)
				continue
			}
			val, found := obj.GetProperty(propName)
			// getter 可能经回调桥执行用户代码并抛出异常: 立即消费
			// 挂起的错误信号并走抛出流程，避免残留到之后的内建调用点
			if err := vm.checkCallbackErr(); err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
			if !found {
				vm.stack.Push(object.UndefinedSingleton)
			} else {
				vm.stack.Push(val)
			}
		case bytecode.OP_SET_PROP:
			val := vm.stack.Pop()
			obj := vm.stack.Peek() // 保留对象在栈上
			// 属性名在常量池中
			propNameVal := frame.Constants.Get(operand)
			propName := ""
			if s, ok := propNameVal.(*object.String); ok {
				propName = s.Value
			}
			// null/undefined 属性写入抛 TypeError (规范要求)
			if obj == object.NullSingleton || obj == object.UndefinedSingleton {
				if err := vm.throwNamedError("TypeError",
					"Cannot set properties of %s (setting '%s')", obj.Inspect(), propName); err != nil {
					return err
				}
				continue
			}
			// Proxy: 转发到 set trap
			if proxy, ok := obj.(*object.Proxy); ok {
				if err := vm.proxySet(proxy, propName, val, obj); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				continue
			}
			obj.SetProperty(propName, val)
			// setter 可能经回调桥执行用户代码并抛出异常: 立即消费
			if err := vm.checkCallbackErr(); err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
		case bytecode.OP_SET_GETTER, bytecode.OP_SET_SETTER:
			// 栈: [obj, fn]; 设置 getter/setter 属性
			fn := vm.stack.Pop()
			obj := vm.stack.Peek() // 保留对象在栈上
			propNameVal := frame.Constants.Get(operand)
			propName := ""
			if s, ok := propNameVal.(*object.String); ok {
				propName = s.Value
			}
			if o, ok := obj.(*object.Object); ok {
				if op == bytecode.OP_SET_GETTER {
					o.DefineAccessor(propName, fn, nil)
				} else {
					o.DefineAccessor(propName, nil, fn)
				}
			} else {
				vm.defineClosureAccessor(obj, propName, fn, op == bytecode.OP_SET_GETTER)
			}
		case bytecode.OP_SET_GETTER_DYN, bytecode.OP_SET_SETTER_DYN:
			// 动态键访问器 (计算属性名): 栈 [obj, fn, key]
			key := vm.stack.Pop()
			fn := vm.stack.Pop()
			obj := vm.stack.Peek() // 保留对象在栈上
			// Symbol 键 (get [Symbol.x](){}) 必须落进 SymbolProperties 键空间,
			// 不能 toJSString 成 "Symbol(Symbol.x)" 字符串键 —— 否则
			// obj[Symbol.x] / Object.getOwnPropertySymbols 全部查不到。
			if sym, isSym := key.(*object.Symbol); isSym {
				if o, ok := obj.(*object.Object); ok {
					if op == bytecode.OP_SET_GETTER_DYN {
						o.DefineSymbolAccessor(sym, fn, nil)
					} else {
						o.DefineSymbolAccessor(sym, nil, fn)
					}
				}
				continue
			}
			propName := toJSString(key)
			if o, ok := obj.(*object.Object); ok {
				if op == bytecode.OP_SET_GETTER_DYN {
					o.DefineAccessor(propName, fn, nil)
				} else {
					o.DefineAccessor(propName, nil, fn)
				}
			} else {
				vm.defineClosureAccessor(obj, propName, fn, op == bytecode.OP_SET_GETTER_DYN)
			}
		case bytecode.OP_TAGGED_TEMPLATE:
			// 栈: [tag, expr1, expr2, ...]; 先弹出插值表达式, 再弹出 tag
			stringsVal := frame.Constants.Get(operand)
			quasisArr, ok := stringsVal.(*object.Array)
			if !ok {
				return fmt.Errorf("VM: expected strings array constant for tagged template")
			}
			// 运行时重建 strings 数组: 编译期数组的原型 (ArrayProto) 尚未初始化,
			// 此处用 object.NewArray 使原型绑定到 ArrayProto (数组方法可用)
			stringsArr := object.NewArray(append([]object.Value{}, quasisArr.Elements...))
			rawArr := object.NewArray(append([]object.Value{}, quasisArr.Elements...))
			stringsArr.SetProperty("raw", rawArr)

			numExprs := len(stringsArr.Elements) - 1
			args := make([]object.Value, 0, numExprs+1)
			args = append(args, stringsArr)
			// 栈上插值顺序为 [e1, e2, ...]，而弹栈是逆序 (先 eN)。
			// 直接 append 会得到 [eN, ..., e1]，插值顺序整体反转。
			vals := make([]object.Value, numExprs)
			for i := numExprs - 1; i >= 0; i-- {
				vals[i] = vm.stack.Pop()
			}
			args = append(args, vals...)
			fn := vm.stack.Pop()
			// 调用 tag 函数
			switch callee := fn.(type) {
			case *object.BuiltinFunction:
				result := callee.Fn(args...)
				if result == nil {
					result = object.UndefinedSingleton
				}
				vm.stack.Push(result)
			case *object.Closure:
				if err := vm.callClosure(callee, args); err != nil {
					if terr := vm.throwJSError(err); terr != nil {
						return terr
					}
					continue
				}
			default:
				if err := vm.throwNamedError("TypeError", "%s is not a function", describeCallee(fn)); err != nil {
					return err
				}
				continue
			}
		case bytecode.OP_DYNAMIC_IMPORT:
			// 动态 import(): 弹出模块路径, 加载模块, 包装为 resolved Promise
			specVal := vm.stack.Pop()
			spec := toJSString(specVal)
			modExports, err := vm.loadModule(spec)
			if err != nil {
				// 加载失败: reject Promise。模块**语法错误** (解析/编译失败)
				// 的 reject 名是 SyntaxError, 其余 (解析不到模块等) 为 Error。
				name := "Error"
				if _, ok := err.(*moduleSyntaxError); ok {
					name = "SyntaxError"
				}
				p := object.NewPromise()
				p.Reject(object.NewErrorWithName(name, err.Error()))
				vm.stack.Push(p)
				continue
			}
			if r := modExports.rec; r != nil && r.status != moduleEvaluated {
				// 模块仍在异步求值中 (含 TLA 挂起 / 依赖未就绪): 动态 import 的
				// Promise 等它完成后才 resolve 命名空间 (ContinueDynamicImport)。
				ns := r.completion.Then(object.NewBuiltin("__dyn_import_ns", func(args ...object.Value) object.Value {
					return modExports.buildNamespace(nil)
				}))
				vm.stack.Push(ns)
				continue
			}
			p := object.NewPromise()
			p.Resolve(modExports.buildNamespace(nil))
			vm.stack.Push(p)
		case bytecode.OP_GET_INDEX:
			index := vm.stack.Pop()
			obj := vm.stack.Pop()
			// Proxy: 转发到 get trap (键转为字符串)
			if proxy, ok := obj.(*object.Proxy); ok {
				key := toJSString(index)
				val, err := vm.proxyGet(proxy, key, obj)
				if err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				vm.stack.Push(val)
				continue
			}
			// null/undefined 索引读取抛 TypeError (规范要求)
			if obj == object.NullSingleton || obj == object.UndefinedSingleton {
				if err := vm.throwNamedError("TypeError",
					"Cannot read properties of %s (reading '%s')", obj.Inspect(), toJSString(index)); err != nil {
					return err
				}
				continue
			}
			val := vm.getIndex(obj, index)
			// GetProperty 触发 getter 时, getter 是 JS 闭包 ⇒ 经回调桥调用,
			// 抛出的异常只记录在 object.callbackError 上 (GetProperty 无 error
			// 返回), 必须在此刻取出重抛。否则错误会残留到之后某个不相干的内建
			// 调用才被消费 —— 那时外层 try 处理器可能已 POP_TRY 出栈, 异常变成
			// 未捕获 (rvdPPH: 解构成员目标 x.y=... / [x.y]=... 的 setter 抛)。
			if err := vm.checkCallbackErr(); err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(val)
		case bytecode.OP_SET_INDEX:
			val := vm.stack.Pop()
			index := vm.stack.Pop()
			obj := vm.stack.Pop()
			// Proxy: 转发到 set trap
			if proxy, ok := obj.(*object.Proxy); ok {
				if err := vm.proxySet(proxy, toJSString(index), val, obj); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				vm.stack.Push(val)
				continue
			}
			// null/undefined 索引写入抛 TypeError (规范要求)
			if obj == object.NullSingleton || obj == object.UndefinedSingleton {
				if err := vm.throwNamedError("TypeError",
					"Cannot set properties of %s (setting '%s')", obj.Inspect(), toJSString(index)); err != nil {
					return err
				}
				continue
			}
			vm.setIndex(obj, index, val)
			// SetProperty 触发 setter 时, setter 是 JS 闭包 ⇒ 经回调桥调用,
			// 抛出的异常只记录在 object.callbackError 上 (SetProperty 无 error
			// 返回), 必须在此刻取出重抛。否则错误会残留到之后某个不相干的内建
			// 调用才被消费 —— 那时外层 try 处理器可能已 POP_TRY 出栈, 异常变成
			// 未捕获 (rvdPPH: 解构成员目标 x.y=... / [x.y]=... 的 setter 抛)。
			if err := vm.checkCallbackErr(); err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(val)
		case bytecode.OP_ARRAY_PUSH:
			// 弹出值，追加到栈顶下方的数组
			val := vm.stack.Pop()
			arr := vm.stack.Peek()
			if a, ok := arr.(*object.Array); ok {
				a.Elements = append(a.Elements, val)
			}
		case bytecode.OP_ARRAY_SPREAD:
			// 弹出可迭代对象，展开所有元素追加到栈顶下方的数组
			iterable := vm.stack.Pop()
			arr := vm.stack.Peek()
			a, ok := arr.(*object.Array)
			if !ok {
				continue
			}
			// 生成器展开: iterable 本身是 Generator (如 [...genFn()])
			if gen, ok := iterable.(*object.Generator); ok {
				// 栈布局与 OP_ITER_NEXT 保持一致: 把 generator 放回栈顶再驱动
				vm.stack.Push(gen)
				for {
					val, done, err := vm.genResume(gen, object.UndefinedSingleton)
					if err != nil {
						return err
					}
					if done {
						break
					}
					a.Elements = append(a.Elements, val)
				}
				vm.stack.Pop() // 弹出 generator，留下数组
				continue
			}
			// 对象实现了 [Symbol.iterator]: VM 层预解析 (生成器结果只能由 VM 驱动)。
			// 解析出 Generator 时走上面的驱动循环 —— GetIterable 纯 Go 层接管不了。
			if resolved, ok, err := vm.resolveSymbolIterator(iterable); err != nil {
				return err
			} else if gen, isGen := resolved.(*object.Generator); ok && isGen {
				vm.stack.Push(gen)
				for {
					val, done, err := vm.genResume(gen, object.UndefinedSingleton)
					if err != nil {
						return err
					}
					if done {
						break
					}
					a.Elements = append(a.Elements, val)
				}
				vm.stack.Pop() // 弹出 generator，留下数组
				continue
			} else if objIter, isObj := resolved.(*object.Object); ok && isObj {
				// 用户自建迭代器对象: 同步调 next() 逐步收集。
				for {
					step, err := vm.syncIterStep(objIter)
					if err != nil {
						if terr := vm.rethrowBridgeError(err); terr != nil {
							return terr
						}
						break
					}
					stepObj, isStep := step.(*object.Object)
					if !isStep {
						break
					}
					doneVal, _ := stepObj.GetProperty("done")
					if doneVal != nil && doneVal.IsTruthy() {
						break
					}
					val, _ := stepObj.GetProperty("value")
					if val == nil {
						val = object.UndefinedSingleton
					}
					a.Elements = append(a.Elements, val)
				}
				continue
			} else if ok {
				iterable = resolved
			}
			iter, hasIter := runtime.GetIterable(iterable)
			if !hasIter {
				if err := vm.throwNamedError("TypeError", "%s is not iterable", iterable.Inspect()); err != nil {
					return err
				}
				continue
			}
			for {
				val, done := iter.Next()
				if done {
					break
				}
				a.Elements = append(a.Elements, val)
			}
		case bytecode.OP_ARRAY_SLICE:
			// 栈: [arr, start] → 弹出 start, arr, 推入 arr[start:] 新数组 (解构 rest)
			start := int(toNumber(vm.stack.Pop()))
			src := vm.stack.Pop()
			a, ok := src.(*object.Array)
			if !ok {
				vm.stack.Push(object.NewArray(nil))
				continue
			}
			if start < 0 {
				start = 0
			}
			if start > len(a.Elements) {
				start = len(a.Elements)
			}
			rest := make([]object.Value, len(a.Elements)-start)
			copy(rest, a.Elements[start:])
			vm.stack.Push(object.NewArray(rest))
		case bytecode.OP_OBJECT_SPREAD:
			// 弹出源对象, 复制其自有属性到栈顶下方的目标对象
			src := vm.stack.Pop()
			dst := vm.stack.Peek()
			to, ok := dst.(*object.Object)
			if !ok {
				continue
			}
			if so, ok := src.(*object.Object); ok {
				for k, desc := range so.Properties {
					to.SetProperty(k, desc.Value)
				}
			} else if arr, ok := src.(*object.Array); ok {
				for i, v := range arr.Elements {
					to.SetProperty(fmt.Sprintf("%d", i), v)
				}
			}

		// ===== 模板字面量 =====
		case bytecode.OP_TEMPLATE_START:
			// 打开一层分段收集器 (operand = 总部分数，实际用不上:
			// 各段值已按 quasi → PART / 表达式 → PART 的顺序各自入栈后被消费)
			vm.tplParts = append(vm.tplParts, nil)
		case bytecode.OP_TEMPLATE_PART:
			// 栈顶一定是当前模板的某一段 (quasi 常量或表达式结果)，
			// 转为字符串后只进收集器，不再碰栈 —— 这是与旧实现的本质区别:
			// 旧代码向下 peek 并弹走"看起来像字符串"的值，会把模板外的
			// 操作数 (如二元加法的左操作数) 误并进模板，最终造成栈下溢。
			val := vm.stack.Pop()
			str := toJSString(val)
			depth := len(vm.tplParts)
			if depth == 0 {
				// 防御: 字节码不完整时退化为直接把段值压栈
				vm.stack.Push(object.NewString(str))
				continue
			}
			vm.tplParts[depth-1] = append(vm.tplParts[depth-1], object.NewString(str))
		case bytecode.OP_TEMPLATE_END:
			// 拼接本层所有段，压回操作数栈
			depth := len(vm.tplParts)
			if depth == 0 {
				continue
			}
			parts := vm.tplParts[depth-1]
			vm.tplParts = vm.tplParts[:depth-1]
			total := 0
			for _, p := range parts {
				if s, ok := p.(*object.String); ok {
					total += len(s.Value)
				}
			}
			if total > maxStringLength {
				if terr := vm.throwJSError(&jsThrow{
					Name: "RangeError", Message: "Invalid string length"}); terr != nil {
					return terr
				}
				continue
			}
			var sb strings.Builder
			for _, p := range parts {
				if s, ok := p.(*object.String); ok {
					sb.WriteString(s.Value)
				}
			}
			vm.stack.Push(object.NewString(sb.String()))

		// ===== 解构和展开 =====
		case bytecode.OP_DESTRUCTURE:
			// 简化实现: 后续完善
		case bytecode.OP_SPREAD:
			// 简化实现: 后续完善
		case bytecode.OP_PACK_ARRAY:
			// 收集剩余参数到数组
			n := int(operand)
			elements := make([]object.Value, n)
			for i := n - 1; i >= 0; i-- {
				elements[i] = vm.stack.Pop()
			}
			vm.stack.Push(object.NewArray(elements))
		case bytecode.OP_PACK_OBJECT:
			// 简化实现

		// ===== 迭代器 =====
		case bytecode.OP_GET_ITERATOR:
			val := vm.stack.Pop()
			// generator 对象本身可作为迭代器 (由 OP_ITER_NEXT 驱动)
			if _, ok := val.(*object.Generator); ok {
				vm.stack.Push(val)
				continue
			}
			// 普通对象实现了 [Symbol.iterator]: 在 VM 层调用该方法 ——
			// 生成器方法的返回值 (*object.Generator) 只能由 VM 帧驱动,
			// runtime.GetIterable 的 Go 层适配器接管不了。
			if resolved, ok, err := vm.resolveSymbolIterator(val); err != nil {
				// Symbol.iterator 方法自身抛错: 必须走抛出流程, 否则外层
				// try/catch 抓不到 (解构/for-of 的 iter-get-err 用例)。
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			} else if ok {
				vm.stack.Push(resolved)
				continue
			}
			iter, ok := runtime.GetIterable(val)
			if !ok {
				if err := vm.throwNamedError("TypeError", "%s is not iterable", val.Inspect()); err != nil {
					return err
				}
				continue
			}
			vm.stack.Push(iter)
		case bytecode.OP_ITER_NEXT:
			// 不弹出迭代器，只读取栈顶的迭代器并获取下一个值
			iter := vm.stack.Peek()
			// generator: 通过 VM 驱动前进一步
			if gen, ok := iter.(*object.Generator); ok {
				val, done, err := vm.genResume(gen, object.UndefinedSingleton)
				if err != nil {
					return err
				}
				if done {
					vm.stack.Push(object.UndefinedSingleton)
				} else {
					vm.stack.Push(val)
				}
				continue
			}
			if it, ok := iter.(*runtime.Iterator); ok {
				val, done := it.Next()
				if done {
					vm.stack.Push(object.UndefinedSingleton)
				} else {
					vm.stack.Push(val)
				}
			} else if it, ok := iter.(*object.Object); ok {
				// 用户自建迭代器 ({ next }): 调 next(), 读 {value, done}。
				// 只做同步调用 —— 返回 Promise 的 async 迭代器不该出现在
				// 同步 for-of / 解构路径上。
				nextFn, found := it.GetProperty("next")
				if !found || !object.IsCallable(nextFn) {
					if err := vm.throwNamedError("TypeError", "iterator has no callable next()"); err != nil {
						return err
					}
					continue
				}
				res, err := vm.callFunction(nextFn, it, nil)
				if err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				// 规范 7.4.2 step 2: next() 返回值必须是 Object, 否则 TypeError
				// (yield* / for-of 的 star-rhs-iter-nrml-next-call-non-obj)。
				if res == nil || !object.IsObjectLike(res) {
					if err := vm.throwNamedError("TypeError", "iterator next() must return an object"); err != nil {
						return err
					}
					continue
				}
				if step, ok := res.(*object.Object); ok {
					doneVal, _ := step.GetProperty("done")
					if doneVal != nil && doneVal.IsTruthy() {
						vm.stack.Push(object.UndefinedSingleton)
					} else {
						val, _ := step.GetProperty("value")
						if val == nil {
							val = object.UndefinedSingleton
						}
						vm.stack.Push(val)
					}
					continue
				}
				vm.stack.Push(object.UndefinedSingleton)
			} else {
				vm.stack.Push(object.UndefinedSingleton)
			}
		case bytecode.OP_ITER_STEP:
			// 同步迭代一步: 读栈顶迭代器 (不弹出), 压入 {value, done} 步进对象。
			// operand = 迭代器隐藏槽号。next() 抛异常 (abrupt) 时先清空槽,
			// 等价 iteratorRecord.[[done]] = true (规范 7.4.6 IteratorStepValue
			// 步骤 2) —— 异常路径的 IteratorClose 收尾据此跳过 return()
			// (thrw-close-skip 一族)。
			iter := vm.stack.Peek()
			step, err := vm.syncIterStep(iter)
			if err != nil {
				if slot := int(operand); slot < len(frame.Locals) {
					frame.Locals[slot] = object.UndefinedSingleton
				}
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(step)
		case bytecode.OP_ITER_CLOSE:
			// IteratorClose: 弹出迭代器, 有 return 方法就调它 (Generator 走
			// 内建 return), 丢弃返回值。无 return → 跳过; 非可调用 → TypeError。
			// return 自身抛错照常传播 (是否吞掉由调用点的 try 决定)。
			iter := vm.stack.Pop()
			if err := vm.iteratorClose(iter); err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
		case bytecode.OP_DISPOSE_ADD:
			// using 资源登记 (见 bytecode.DisposeResource 与 AddDisposableResource)。
			res, _ := frame.Constants.Get(operand).(*bytecode.DisposeResource)
			v := vm.stack.Pop()
			if err := vm.disposeAdd(res, v); err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
		case bytecode.OP_DISPOSE_EXIT:
			// using 作用域退出: 逆序释放 (见 bytecode.DisposeScope)。
			scope, _ := frame.Constants.Get(operand).(*bytecode.DisposeScope)
			if err := vm.disposeExit(scope); err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
		case bytecode.OP_REQUIRE_OBJECT_COERCIBLE:
			// RequireObjectCoercible: 栈顶 null/undefined → TypeError (不弹出)。
			v := vm.stack.Peek()
			if isNullish(v) {
				if err := vm.throwNamedError("TypeError",
					"Cannot destructure '%s' as it is %s.", v.Inspect(), v.Inspect()); err != nil {
					return err
				}
			}
		case bytecode.OP_OBJECT_REST:
			// 对象 rest: [src, excluded] → [src, restObj]
			excluded := vm.stack.Pop()
			src := vm.stack.Peek()
			rest, err := vm.objectRest(src, excluded)
			if err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(rest)
		case bytecode.OP_FOR_IN_INIT:
			// 弹出对象, 推入对象键迭代器
			val := vm.stack.Pop()
			switch v := val.(type) {
			case *object.Object:
				vm.stack.Push(runtime.NewObjectKeysIterator(v))
			case *object.Array:
				// for...in 遍历数组的索引键 ("0", "1", ...)
				keys := make([]string, len(v.Elements))
				for i := range v.Elements {
					keys[i] = strconv.Itoa(i)
				}
				iter := runtime.NewObjectKeysIteratorWithKeys(v, keys)
				vm.stack.Push(iter)
			default:
				vm.stack.Push(runtime.NewObjectKeysIterator(object.NewObject()))
			}
		case bytecode.OP_FOR_IN_NEXT:
			// 读取栈顶迭代器, 取下一个键
			iter := vm.stack.Peek()
			if it, ok := iter.(*runtime.Iterator); ok {
				val, done := it.Next()
				if done {
					vm.stack.Push(object.UndefinedSingleton)
				} else {
					vm.stack.Push(val)
				}
			} else {
				vm.stack.Push(object.UndefinedSingleton)
			}
		case bytecode.OP_FOR_IN_END:
			// 弹出迭代器 (清理栈)
			vm.stack.Pop()

		case bytecode.OP_GET_ASYNC_ITERATOR:
			// for await...of 头部: 弹出可迭代对象, 推进「next 可调用」的迭代器。
			// 优先级与规范一致: Symbol.asyncIterator 方法 > 同步可迭代形状
			// (generator 本体 / runtime.Iterator / 带 next 的对象 —— 数组、
			// 字符串等在 stdlib 转换后都是这些形状)。
			val := vm.stack.Pop()
			// 0) 本身就是异步生成器: 它即自己的 [Symbol.asyncIterator]()
			//    (返回自身), 无需再解析, 直接作为迭代器交给 ASYNC_ITER_NEXT。
			if _, isAG := val.(*object.AsyncGenerator); isAG {
				vm.stack.Push(val)
				continue
			}
			// 1) [Symbol.asyncIterator]() —— 只在 VM 层调用 (返回值可能是
			//    generator/闭包, Go 侧适配器驱动不了)。
			if resolved, ok, err := vm.resolveAsyncSymbolIterator(val); err != nil {
				return err
			} else if ok {
				vm.stack.Push(resolved)
				continue
			}
		// 2) 同步形状原样交给 ASYNC_ITER_NEXT (它统一处理 next 调用)。
		// 「裸 next 对象」是 Gox 的非规范扩展 (for-await 自建迭代器),
		// 判定用 hasNextKey (非触发式) —— GetProperty 会多触发一次
		// getter, 破坏属性访问顺序 (r6e5qp)。
		if _, isGen := val.(*object.Generator); isGen {
			vm.stack.Push(val)
			continue
		}
		if _, isIter := val.(*runtime.Iterator); isIter {
			vm.stack.Push(val)
			continue
		}
		if o, isObj := val.(*object.Object); isObj && hasNextKey(o) {
			// 包 Async-from-Sync wrapper: next method 懒缓存 (V8 实测行为),
			// throw/return 每次 GetMethod —— 见 wrapSyncIterForAsync。
			vm.stack.Push(vm.wrapSyncIterForAsync(o))
			continue
		}
		// 3) 同步可迭代 (数组/字符串/Map 等, 规范允许 for-await 与 yield*
		// 委托消费): [Symbol.iterator] 解析 (generator 结果只能 VM 驱动)
		// 或 runtime.GetIterable 适配, 转成迭代器形状。JS 迭代器对象再包
		// Async-from-Sync wrapper (next method 懒缓存 —— V8 实测第二次
		// next 不再触发 get next; r6e5qp test262 yield-star-sync-next)。
		if resolved, ok, err := vm.resolveSymbolIterator(val); err != nil {
			return err
		} else if ok {
			vm.stack.Push(vm.wrapSyncIterForAsync(resolved))
			continue
		}
			if iter, hasIter := runtime.GetIterable(val); hasIter {
				vm.stack.Push(iter)
				continue
			}
			if err := vm.throwNamedError("TypeError", "%s is not async-iterable", val.Inspect()); err != nil {
				return err
			}
			continue
		case bytecode.OP_ASYNC_ITER_NEXT:
			// for await...of 循环头: 读栈顶迭代器, 调 next() 把「步进结果」
			// 压栈。结果可能是 Promise (由编译器发射的 OP_YIELD 交给 __spawn
			// 驱动: resolve 值作为 yield 的恢复值再取 .value/.done), 也可能
			// 直接是 {value, done} 对象 (同步迭代器被 for-await 消费的形状)。
			iter := vm.stack.Peek()
			switch it := iter.(type) {
			case *object.Generator:
				// 同步 generator 被 for await 消费: 驱动一步。
				// 结果包成 {value, done} 对象, 与 next() 调用形状统一。
				val, done, err := vm.genResume(it, object.UndefinedSingleton)
				if err != nil {
					return err
				}
				step := object.NewObject()
				step.SetProperty("value", val)
				step.SetProperty("done", object.NewBoolean(done))
				vm.stack.Push(step)
			case *runtime.Iterator:
				val, done := it.Next()
				step := object.NewObject()
				step.SetProperty("value", val)
				step.SetProperty("done", object.NewBoolean(done))
				vm.stack.Push(step)
			case *object.Object:
				// 对象迭代器: 调它的 next() 方法 (this = 迭代器本身)。
				// Async-from-Sync wrapper (有 wrapperSrcKey): next method 懒
				// 取一次并缓存, this = 源同步迭代器 (V8 语义)。
				nextFn, found := it.GetProperty("next")
				recv := iter
				if nf, src, ok := vm.syncWrapperNext(it); ok {
					nextFn, found, recv = nf, true, src
				}
				if !found || !object.IsCallable(nextFn) {
					if err := vm.throwNamedError("TypeError", "async iterator has no callable next()"); err != nil {
						return err
					}
					continue
				}
				res, err := vm.callFunction(nextFn, recv, nil)
				if err != nil {
					return err
				}
				vm.stack.Push(res)
			case *object.AsyncGenerator:
				// 异步生成器: 调它的 next() 方法 (this = 生成器本身), 得到
				// Promise<{value, done}>。编译器随后的 OP_YIELD/OP_AWAIT 等待它。
				nextFn, found := it.GetProperty("next")
				if !found || !object.IsCallable(nextFn) {
					if err := vm.throwNamedError("TypeError", "async iterator has no callable next()"); err != nil {
						return err
					}
					continue
				}
				res, err := vm.callFunction(nextFn, it, nil)
				if err != nil {
					return err
				}
				vm.stack.Push(res)
			default:
				if err := vm.throwNamedError("TypeError", "%s is not async-iterable", iter.Inspect()); err != nil {
					return err
				}
				continue
			}
		case bytecode.OP_ASYNC_ITER_NEXT_ARG:
			// async generator 的 yield* 委托: 同 ASYNC_ITER_NEXT, 但把消费者
			// 传入的 next(v) 实参转发给被委托迭代器。栈 [iter, arg] → [step]。
			arg := vm.stack.Pop()
			iter := vm.stack.Pop()
			switch it := iter.(type) {
			case *object.Generator:
				// 同步 generator 被异步委托: 传入 arg 作为其 yield 的恢复值。
				val, done, err := vm.genResume(it, arg)
				if err != nil {
					return err
				}
				step := object.NewObject()
				step.SetProperty("value", val)
				step.SetProperty("done", object.NewBoolean(done))
				vm.stack.Push(step)
			case *runtime.Iterator:
				val, done := it.Next()
				step := object.NewObject()
				step.SetProperty("value", val)
				step.SetProperty("done", object.NewBoolean(done))
				vm.stack.Push(step)
			case *object.Object, *object.AsyncGenerator:
				var nextFn object.Value
				var found bool
				recv := iter // this: 默认迭代器本身; wrapper 场景用源对象
				if ag, isAG := it.(*object.AsyncGenerator); isAG {
					nextFn, found = ag.GetProperty("next")
				} else {
					o := it.(*object.Object)
					nextFn, found = o.GetProperty("next")
					// Async-from-Sync wrapper: next method 懒取缓存, this =
					// 源同步迭代器 (V8 语义)。
					if nf, src, ok := vm.syncWrapperNext(o); ok {
						nextFn, found, recv = nf, true, src
					}
				}
				if !found || !object.IsCallable(nextFn) {
					if err := vm.throwNamedError("TypeError", "async iterator has no callable next()"); err != nil {
						return err
					}
					continue
				}
				res, err := vm.callFunction(nextFn, recv, []object.Value{arg})
				if err != nil {
					return err
				}
				vm.stack.Push(res)
			default:
				if err := vm.throwNamedError("TypeError", "%s is not async-iterable", iter.Inspect()); err != nil {
					return err
				}
				continue
			}

		// ===== 作用域 =====
		case bytecode.OP_PUSH_SCOPE, bytecode.OP_POP_SCOPE:
			// 局部变量使用 slot 管理，作用域操作在 VM 中是 NOP

		// ===== with 语句 (对象环境记录, 规范 13.11.7) =====
		case bytecode.OP_WITH_ENTER:
			// 对象表达式结果 → ToObject (规范 14.11.2): null / undefined 抛
			// TypeError, 其余原样压回 (原始值按包装对象语义由 GetProperty 承接)。
			objVal := vm.stack.Pop()
			if objVal == object.NullSingleton || objVal == object.UndefinedSingleton {
				if err := vm.throwNamedError("TypeError",
					"Cannot convert %s to object", objVal.Inspect()); err != nil {
					return err
				}
				continue
			}
			vm.stack.Push(objVal)
		case bytecode.OP_WITH_LOAD:
			ref := withRefOf(frame, operand)
			if ref == nil {
				return fmt.Errorf("VM: WITH_LOAD expects *WithRef constant at %d", operand)
			}
			_, val, found, werr := vm.withResolve(frame, ref)
			if werr != nil {
				if terr := vm.rethrowBridgeError(werr); terr != nil {
					return terr
				}
				continue
			}
			if found {
				vm.stack.Push(val)
				continue
			}
			// 未命中: 按回退语义取值。
			if !ref.LocalFallback {
				if gv, ok := vm.globals.Get(ref.Name); ok {
					vm.stack.Push(gv)
				} else if err := vm.throwNamedError("ReferenceError", "%s is not defined", ref.Name); err != nil {
					return err
				}
				continue
			}
			if ref.FallbackSlot < len(frame.Locals) && frame.Locals[ref.FallbackSlot] != nil {
				vm.stack.Push(frame.Locals[ref.FallbackSlot])
			} else if err := vm.throwNamedError("ReferenceError",
				"Cannot access lexical declaration '%s' before initialization", ref.Name); err != nil {
				return err
			}
		case bytecode.OP_WITH_STORE:
			ref := withRefOf(frame, operand)
			if ref == nil {
				return fmt.Errorf("VM: WITH_STORE expects *WithRef constant at %d", operand)
			}
			val := vm.stack.Pop()
			owner, _, found, werr := vm.withResolve(frame, ref)
			if werr != nil {
				if terr := vm.rethrowBridgeError(werr); terr != nil {
					return terr
				}
				continue
			}
			if found {
				if err := vm.withSet(owner, ref.Name, val); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
				}
				continue
			}
			// 未命中: 写回退目标。
			if !ref.LocalFallback {
				if vm.globals.IsConst(ref.Name) {
					if err := vm.throwNamedError("TypeError", "Assignment to constant variable: %s", ref.Name); err != nil {
						return err
					}
					continue
				}
				if _, exists := vm.globals.Get(ref.Name); exists {
					vm.globals.Set(ref.Name, val)
				} else {
					// 隐式赋值创建的全局: globalThis 的自有可配置属性。
					vm.globals.DeclareImplicit(ref.Name, val)
				}
				continue
			}
			if ref.IsConst {
				if err := vm.throwNamedError("TypeError", "Assignment to constant variable: %s", ref.Name); err != nil {
					return err
				}
				continue
			}
			vm.storeLocalSlot(frame, ref.FallbackSlot, val)
		case bytecode.OP_WITH_DELETE:
			ref := withRefOf(frame, operand)
			if ref == nil {
				return fmt.Errorf("VM: WITH_DELETE expects *WithRef constant at %d", operand)
			}
			owner, _, found, werr := vm.withResolve(frame, ref)
			if werr != nil {
				if terr := vm.rethrowBridgeError(werr); terr != nil {
					return terr
				}
				continue
			}
			if found {
				vm.withDelete(owner, ref.Name)
			}
			// delete 标识符统一返回 true (with 对象属性删除 / 无绑定均可删)。
			vm.stack.Push(object.NewBoolean(true))

		// ===== 类型操作 =====
		case bytecode.OP_TO_NUMBER:
			val := vm.stack.Pop()
			result, err := toNumberValue(val)
			if err != nil {
				if terr := vm.throwJSError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(result)
		case bytecode.OP_TO_PROPERTY_KEY:
			// 成员引用的键在此**只转一次**: 之后 GET_INDEX / SET_INDEX 共用
			// 这份结果, 带自定义 toString 的对象键不会被求值两次。
			v := vm.stack.Pop()
			vm.stack.Push(toPropertyKey(v))
			// 对象键的转换会调用用户 toString ⇒ 经回调桥可能抛出, 立即重抛,
			// 否则错误会残留到之后某个不相干的内建调用才被消费 (见 GET_INDEX)。
			if err := vm.checkCallbackErr(); err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
			}
		case bytecode.OP_TYPEOF:
			val := vm.stack.Pop()
			vm.stack.Push(object.NewString(object.TypeOf(val)))
		case bytecode.OP_TYPEOF_GLOBAL:
			// typeof 作用于编译期未绑定的标识符。
			// 该标识符可能是运行时注入的全局对象 (Math/Number/Array/...)，
			// 也可能是真正的未声明变量 —— 后者按规范返回 "undefined" 而非抛错，
			// 因此这里走非抛出的全局查找。
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				if val, found := vm.globals.Get(s.Value); found {
					vm.stack.Push(object.NewString(object.TypeOf(val)))
				} else {
					vm.stack.Push(object.NewString("undefined"))
				}
			} else {
				vm.stack.Push(object.NewString("undefined"))
			}
		case bytecode.OP_INSTANCEOF:
			// instanceof: 栈顶是 Constructor (右), 下方是 obj (左)。
			// 检查 obj 的原型链是否包含 Constructor.prototype。
			right := vm.stack.Pop()
			left := vm.stack.Pop()
			vm.stack.Push(vm.instanceOf(left, right))
		case bytecode.OP_IN:
			// in 运算符: key in obj。左操作数(key)先压, 右操作数(obj)后压, 栈顶是 obj。
			obj := vm.stack.Pop()
			key := vm.stack.Pop()
			vm.stack.Push(vm.inOperator(obj, key))
		case bytecode.OP_THIS:
			frame := vm.currentFrame()
			if frame.Closure == nil {
				// 主帧: script 顶层 this = globalThis (与严格模式无关);
				// module 顶层 this = undefined。复用 globals 里已装配的
				// globalThis 绑定以保证 `this === globalThis` 的对象同一性;
				// 未装配时保持 undefined (无 stdlib 的裸 VM)。
				if vm.moduleMode {
					vm.stack.Push(object.UndefinedSingleton)
				} else {
					vm.stack.Push(vm.globalThisValue())
				}
			} else if frame.This != nil {
				// 普通函数帧: callClosure 已按 sloppy 规则归一并写入 frame.This。
				vm.stack.Push(frame.This)
			} else {
				vm.stack.Push(object.UndefinedSingleton)
			}
		case bytecode.OP_NEW_TARGET:
			// new.target: 当前帧的构造目标。callClosure 已在装配帧时写入
			// (构造调用为被 new 的构造器 / super() 为调用者的构造目标); 其余
			// (普通调用、箭头函数) 为 nil, 归一为 undefined。主帧 (顶层) 语法上
			// 不可能出现 new.target (解析期已拦), 兜底也返回 undefined。
			frame := vm.currentFrame()
			if frame != nil && frame.NewTarget != nil {
				vm.stack.Push(frame.NewTarget)
			} else {
				vm.stack.Push(object.UndefinedSingleton)
			}
		case bytecode.OP_DELETE:
			// 栈: [obj, key] → 删除 obj 上的 key 属性, 推入 true/false
			key := vm.stack.Pop()
			obj := vm.stack.Pop()
			// 键归一 (ToPropertyKey): 之前只认 String/Symbol, 于是
			// `delete obj[0]` / `delete obj[objKey]` 会静默返回 false 且不删属性。
			switch key.(type) {
			case *object.String, *object.Symbol:
			default:
				key = object.NewString(toJSString(key))
				if err := vm.checkCallbackErr(); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
				}
			}
			if o, ok := obj.(*object.Object); ok {
				if s, ok := key.(*object.String); ok {
					vm.stack.Push(object.NewBoolean(o.DeleteProperty(s.Value)))
				} else if sym, ok := key.(*object.Symbol); ok {
					vm.stack.Push(object.NewBoolean(o.DeleteSymbolProperty(sym)))
				} else {
					vm.stack.Push(object.NewBoolean(false))
				}
			} else if g, ok := obj.(*object.GlobalObject); ok {
				// globalThis: 删除对应全局环境记录里的自有可配置绑定。
				// 此前 GlobalObject 掉进最下方 else 分支 ⇒ 恒返回 true 却什么都没删,
				// 于是 `delete this.x`(getter 自删) 后 `x in this` 仍为 true,
				// 且后续对 x 的严格赋值因「绑定仍存在」而不抛 ReferenceError
				// (compound-assignment/…-putvalue-lref--v--* 一族 23 例)。
				if s, ok := key.(*object.String); ok {
					vm.stack.Push(object.NewBoolean(g.DeleteOwn(s.Value)))
				} else {
					// 全局环境无符号键绑定: 无可删, 按规范返回 true。
					vm.stack.Push(object.NewBoolean(true))
				}
			} else if arr, ok := obj.(*object.Array); ok {
				// 数组删除: 先查自有属性描述符 —— 不可配置的索引 (如
				// Object.defineProperty(arguments, "0", {configurable:false})
				// 之后的 mapped arguments) [[Delete]] 必须返回 false 且不删;
				// 之前无条件删并恒返回 true (arguments-object/mapped/
				// mapped-arguments-nonconfigurable-delete-1.js)。
				// 注意 key 已在上方按 ToPropertyKey 归一为字符串, 故这里
				// 数字索引用 "0"/"1" 形态匹配。删除走 Array.DeleteOwn, 由它
				// 按描述符可配置性裁决并同步 Elements / propKeyOrder。
				if s, ok := key.(*object.String); ok {
					vm.stack.Push(object.NewBoolean(arr.DeleteOwn(s.Value)))
				} else {
					vm.stack.Push(object.NewBoolean(false))
				}
			} else {
				vm.stack.Push(object.NewBoolean(true))
			}

		// ===== 控制 =====
		case bytecode.OP_BREAK, bytecode.OP_CONTINUE:
			// break/continue 已由编译器转换为 OP_JUMP/OP_LOOP
			// 如果直接出现，跳转到 operand
			frame.PC = int(operand)

		// ===== try/catch/finally =====
		case bytecode.OP_PUSH_TRY:
			// operand = catchPC (0 = 无 catch)
			vm.tryStack = append(vm.tryStack, tryEntry{
				catchPC:   int(operand),
				finallyPC: 0,
				stackBase: vm.stack.Len(),
				frameIdx:  vm.frameIdx,
			})
		case bytecode.OP_PUSH_FINALLY:
			// operand = finallyPC，设置在栈顶 try 条目上
			if len(vm.tryStack) > 0 {
				vm.tryStack[len(vm.tryStack)-1].finallyPC = int(operand)
			}
		case bytecode.OP_POP_TRY:
			// try 块正常完成，弹出处理器
			if len(vm.tryStack) > 0 {
				vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]
			}
		case bytecode.OP_THROW:
			val := vm.stack.Pop()
			if !vm.handleThrow(val) {
				// 无处理器: 返回错误
				return &ThrowError{Value: val}
			}
			// 异常已被捕获，继续执行 (PC 已被 handleThrow 设置)
		case bytecode.OP_END_FINALLY:
			// finally 块结束。栈顶若正是本帧刚跑完 finally 的条目 (inFinally),
			// 说明它是「带着挂起异常」进来的 ⇒ 弹出它并重新抛出挂起值;
			// 否则本 finally 是从「try 正常完成 / catch 完成」路径进来的,
			// 没有挂起值, 直接继续。
			if n := len(vm.tryStack); n > 0 {
				top := vm.tryStack[n-1]
				if top.inFinally && top.frameIdx == vm.frameIdx {
					vm.tryStack = vm.tryStack[:n-1]
					if top.pendingReturn {
						// return 完成: 继续向外传播 (展开外层 finally),
						// 无更多 finally 时完成 return —— 等价 OP_RETURN。
						if !vm.handleReturn(top.pendingVal) {
							val := top.pendingVal
							base := vm.currentFrame().StackBase
							vm.popFrame()
							if vm.stack.Len() > base {
								vm.stack.Truncate(base)
							}
							vm.stack.Push(val)
							continue
						}
					} else if !vm.handleThrow(top.pendingVal) {
						return &ThrowError{Value: top.pendingVal}
					}
				}
			}

		// ===== 模块系统 =====
		case bytecode.OP_IMPORT:
			// operand = 模块路径常量索引
			specVal := frame.Constants.Get(operand)
			spec := ""
			if s, ok := specVal.(*object.String); ok {
				spec = s.Value
			}
			modExports, err := vm.loadModule(spec)
			if err != nil {
				// 模块加载失败作为异常
				errVal := object.NewErrorWithName("Error", err.Error())
				if !vm.handleThrow(errVal) {
					return &ThrowError{Value: errVal}
				}
				continue
			}
			// 推入模块命名空间对象 (物化时解析再导出/星号导出, 见 buildNamespace)
			vm.stack.Push(modExports.buildNamespace(nil))

		case bytecode.OP_EXPORT:
			// operand = 导出名常量索引，栈顶是导出值
			nameVal := frame.Constants.Get(operand)
			exportName := ""
			if s, ok := nameVal.(*object.String); ok {
				exportName = s.Value
			}
			val := vm.stack.Pop()
			vm.ensureCurrentExports().setValue(exportName, val)

		case bytecode.OP_EXPORT_BINDING:
			// operand = 常量池索引 → [导出名(str), 槽位(int)]
			// 记录"导出名 → 当前(模块顶层)帧槽位"的读取器, 不存值快照。
			name, slot, ok := vm.bindingOperand(frame, operand)
			if !ok {
				return fmt.Errorf("VM: malformed EXPORT_BINDING operand at pc %d", frame.PC)
			}
			locals := frame.Locals
			vm.ensureCurrentExports().setBinding(name, func() object.Value {
				if slot < len(locals) {
					return locals[slot]
				}
				return nil
			})

		case bytecode.OP_EXPORT_FROM:
			// operand = 常量池索引 → [模块路径(str), 源导出名(str), 目标导出名(str)]
			// 记录转发 (读时读取源模块导出槽), 而不是当场拷贝值。
			spec, srcName, target, ok := vm.reexportOperand(frame, operand)
			if !ok {
				return fmt.Errorf("VM: malformed EXPORT_FROM operand at pc %d", frame.PC)
			}
			src, err := vm.loadModule(spec)
			if err != nil {
				errVal := object.NewErrorWithName("Error", err.Error())
				if !vm.handleThrow(errVal) {
					return &ThrowError{Value: errVal}
				}
				continue
			}
			vm.ensureCurrentExports().setForward(target, src, srcName)

		case bytecode.OP_EXPORT_STAR:
			// operand = 模块路径常量索引; 记录星号再导出源。
			specVal := frame.Constants.Get(operand)
			spec := ""
			if s, ok := specVal.(*object.String); ok {
				spec = s.Value
			}
			src, err := vm.loadModule(spec)
			if err != nil {
				errVal := object.NewErrorWithName("Error", err.Error())
				if !vm.handleThrow(errVal) {
					return &ThrowError{Value: errVal}
				}
				continue
			}
			vm.ensureCurrentExports().addStar(src)

		default:
			return fmt.Errorf("VM: unknown opcode 0x%02x (%s)", op, op.Name())
		}
	}
	return nil
}

// callFunction 从 Go 代码调用 JS 函数 (闭包或内建函数)。
// 用于 stdlib 回调桥: 当 BuiltinMethod (如 Array.prototype.map) 需要
// 调用用户传入的 JS 闭包时，通过此方法执行子帧。
func (vm *VM) callFunction(fn object.Value, this object.Value, args []object.Value) (object.Value, error) {
	// Rx 单元可直接调用: count() 等价 count.value (GetX 语义)
	if obs, ok := fn.(object.ObservableState); ok {
		return obs.RxValue(), nil
	}
	switch callee := fn.(type) {
	case *object.BuiltinFunction:
		result := callee.Fn(args...)
		if result == nil {
			return object.UndefinedSingleton, nil
		}
		return result, nil

	case *object.BuiltinMethod:
		result := callee.Fn(this, args...)
		if result == nil {
			return object.UndefinedSingleton, nil
		}
		return result, nil

	case *object.Closure:
		// 绑定 this: 箭头函数复用自身的词法 this; 非箭头函数一律以传入的
		// this 作接收者 (this 为 nil 即"无接收者", 等价 undefined —— 交给
		// callClosure 按 sloppy 归一到 globalThis, 不再沿用闭包创建时的 this)。
		bound := callee
		if !callee.IsArrow {
			bound = &object.Closure{
				Fn:             callee.Fn,
				Env:            callee.Env,
				This:           this,
				NewTarget:      callee.NewTarget,
				IsArrow:        callee.IsArrow,
				CapturedLocals: callee.CapturedLocals,
				CreatedAtFrame: callee.CreatedAtFrame,
			}
		}
		// generator 函数: 不执行函数体, 返回 Generator 对象 (对齐 OP_CALL;
		// 否则生成器体被同步执行, 首个 yield 触发 "yield outside generator")。
		// 有形参前导段时先跑前导帧同步绑定形参, 再取出冻结好的 Generator。
		if bound.Fn != nil && bound.Fn.IsGenerator {
			if genHasPrologue(bound.Fn) {
				startIdx := vm.frameIdx + 1
				if err := vm.setupGenPrologueFrame(bound, args); err != nil {
					vm.unwindFramesTo(startIdx)
					return nil, err
				}
				if err := vm.runFrom(startIdx); err != nil {
					vm.unwindFramesTo(startIdx)
					return nil, err
				}
				return vm.stack.Pop(), nil
			}
			return object.NewGenerator(bound, args), nil
		}
		// 记录当前帧索引，新帧从这里 +1
		startIdx := vm.frameIdx + 1
		if err := vm.callClosure(bound, args); err != nil {
			// 子帧内抛出且未被捕获: 回收已压入的帧, 否则帧栈损坏,
			// 调用方 (回调桥) 继续执行时会跑飞
			vm.unwindFramesTo(startIdx)
			return nil, err
		}
		// 执行子帧直到返回
		if err := vm.runFrom(startIdx); err != nil {
			vm.unwindFramesTo(startIdx)
			return nil, err
		}
		// 返回值在栈顶
		return vm.stack.Pop(), nil
	}
	return nil, fmt.Errorf("TypeError: %s is not a function", describeCallee(fn))
}

// unwindFramesTo 回收 frameIdx >= startIdx 的所有帧。
// 用于嵌套调用 (回调桥) 抛出未捕获异常后的帧栈恢复。
// 每层帧在压栈时保留了调用点的栈高度, 回收时把本帧残留的栈值一并清掉。
func (vm *VM) unwindFramesTo(startIdx int) {
	for vm.frameIdx >= startIdx {
		f := vm.frames[vm.frameIdx]
		base := f.StackBase
		vm.popFrame()
		// 清理本帧执行期间残留的栈值 (含嵌套帧遗留)
		for vm.stack.Len() > base {
			vm.stack.Pop()
		}
	}
}

// checkCallbackErr 检查回调执行中是否产生了错误，如有则返回并清除。
// 错误信号由 object 层持有 (消费即清除)，这里只做读取转发。
func (vm *VM) checkCallbackErr() error {
	return object.TakeCallbackError()
}

// throwIfError 判断内建函数的返回值是否应作为异常抛出，是则执行 throw 流程。
//
// 语义: 内建函数返回 *object.Error 时，通常表示"操作失败，请抛出异常"
// (如 JSON.parse 的语法错误、Array.prototype.map 收到非函数回调)。
// 例外: ReturnIsValue 标记的内建函数 (如 Error/TypeError 构造器) 返回的
// Error 是普通值——new Error("x") 应返回错误对象而不是抛出它。
//
// 返回值: thrown=true 表示已进入 throw 流程 (调用方应 continue 或返回 err，
// 切勿再把 result 压栈); thrown=false 表示 result 是普通值，正常压栈。
func (vm *VM) throwIfError(result object.Value, returnsRaw bool) (thrown bool, err error) {
	errObj, isErr := result.(*object.Error)
	if !isErr || returnsRaw {
		return false, nil
	}
	if !vm.handleThrow(errObj) {
		return true, &ThrowError{Value: errObj}
	}
	return true, nil
}

// throwJSError 把结构化运行时错误 (*jsThrow) 转换为 JS 异常并走正常抛出流程。
//
// 返回值语义:
//   - nil: 异常已被 catch/finally 捕获，PC 已改写，调用方应 continue
//     (切勿再把运算结果压栈——栈已被 handleThrow 恢复到 try 入口高度)
//   - 非 nil: 没有匹配的处理器，异常继续向上传播，调用方应 return
//
// 非 *jsThrow 的普通 Go error 不属于 JS 语言级异常，原样返回。
func (vm *VM) throwJSError(err error) error {
	var jt *jsThrow
	if !errors.As(err, &jt) {
		return err
	}
	errObj := object.NewErrorWithName(jt.Name, jt.Message)
	if !vm.handleThrow(errObj) {
		return &ThrowError{Value: errObj}
	}
	return nil
}

// throwNamedError 构造命名 JS 错误并走正常抛出流程。
// 与裸 return fmt.Errorf 的区别: 这里的错误能被 try/catch 捕获。
// 返回 nil 表示已被 catch/finally 接住 (调用方应 continue)；
// 非 nil 表示异常继续向外传播 (调用方应 return)。
func (vm *VM) throwNamedError(name, format string, a ...any) error {
	errObj := object.NewErrorWithName(name, fmt.Sprintf(format, a...))
	if !vm.handleThrow(errObj) {
		return &ThrowError{Value: errObj}
	}
	return nil
}

// rethrowBridgeError 把回调桥 (object.CallFunction) 报告的错误转回 JS
// 抛出流程。桥另一侧的异常此前以裸 Go error 直接 return，导致
// getter/Proxy/数组方法回调里的任何异常都逃出 try/catch。
//   - *ThrowError: 恢复其原始抛出值 (throw x 的 x)
//   - *jsThrow:    构造命名错误 (如栈溢出 RangeError)
//   - 其余:        非 JS 语言级异常，原样返回
func (vm *VM) rethrowBridgeError(err error) error {
	if te, ok := err.(*ThrowError); ok {
		if !vm.handleThrow(te.Value) {
			return te
		}
		return nil
	}
	return vm.throwJSError(err)
}

// ===== Proxy trap 转发 =====

// proxyTrap 调用 handler 上的 trap 函数。
// 参数: proxy=代理对象, trapName=trap 名, args=trap 参数。
// 返回: (trap 返回值, 是否存在 trap, 执行错误)。
// 如果 handler 没有该 trap 或 handler 不是对象，返回 (nil, false, nil)，
// 调用方应执行默认 (绕过) 行为。
func (vm *VM) proxyTrap(proxy *object.Proxy, trapName string, args []object.Value) (object.Value, bool, error) {
	// 已撤销的代理: 所有操作抛 TypeError
	if proxy.IsRevoked {
		return nil, true, fmt.Errorf("TypeError: Cannot perform '%s' on a proxy that has been revoked", trapName)
	}
	handler := proxy.Handler
	if handler == nil {
		return nil, false, nil
	}
	trap := object.GetProxyTrap(handler, trapName)
	if trap == nil {
		return nil, false, nil
	}
	// 调用 trap，this 绑定为 handler
	result, err := vm.callFunction(trap, handler, args)
	if err != nil {
		return nil, true, err
	}
	return result, true, nil
}

// proxyGet 处理对代理的属性读取。
// receiver 是接收者 (通常就是代理本身)。
func (vm *VM) proxyGet(proxy *object.Proxy, key string, receiver object.Value) (object.Value, error) {
	res, handled, err := vm.proxyTrap(proxy, "get", []object.Value{proxy.Target, object.NewString(key), receiver})
	if err != nil {
		return nil, err
	}
	if !handled {
		// 无 trap: 直接从目标读取
		val, found := proxy.Target.GetProperty(key)
		if !found || val == nil {
			return object.UndefinedSingleton, nil
		}
		return val, nil
	}
	if res == nil {
		return object.UndefinedSingleton, nil
	}
	return res, nil
}

// proxySet 处理对代理的属性写入。
func (vm *VM) proxySet(proxy *object.Proxy, key string, val, receiver object.Value) error {
	res, handled, err := vm.proxyTrap(proxy, "set", []object.Value{proxy.Target, object.NewString(key), val, receiver})
	if err != nil {
		return err
	}
	if !handled {
		// 无 trap: 直接写入目标
		proxy.Target.SetProperty(key, val)
		return nil
	}
	// 有 trap: 若 trap 返回 falsy，视为静默失败 (非严格模式)
	_ = res
	return nil
}

// proxyHas 处理对代理的属性存在性检查 (in 操作符)。
func (vm *VM) proxyHas(proxy *object.Proxy, key string) (bool, error) {
	res, handled, err := vm.proxyTrap(proxy, "has", []object.Value{proxy.Target, object.NewString(key)})
	if err != nil {
		return false, err
	}
	if !handled {
		_, found := proxy.Target.GetProperty(key)
		return found, nil
	}
	return object.IsTruthyValue(res), nil
}

// inOperator 实现 `key in obj` 语义。
// obj 为 Proxy 时走 has trap; 否则沿属性/原型链检查 key 是否存在。
func (vm *VM) inOperator(obj, key object.Value) object.Value {
	k := propKey(key)

	// Proxy: 走 has trap
	if p, ok := obj.(*object.Proxy); ok {
		found, err := vm.proxyHas(p, k)
		if err != nil {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(found)
	}

	// 普通对象: 沿原型链检查
	if obj == nil {
		return object.NewBoolean(false)
	}

	// 数组索引需检查是否真正存在: 越界索引不算属性存在 (GetProperty 对越界
	// 返回 Undefined+found=true, 用于 arr[i] 取 undefined, 但 in 语义要求 false)。
	if arr, ok := obj.(*object.Array); ok {
		if idx, err := strconv.Atoi(k); err == nil {
			return object.NewBoolean(idx >= 0 && idx < len(arr.Elements))
		}
	}

	_, found := obj.GetProperty(k)
	return object.NewBoolean(found)
}

// instanceOf 实现 `obj instanceof Constructor` 语义。
// 通过原型链检查: Constructor.prototype 是否出现在 obj 的原型链上。
// 该运行时未实现标准内置构造器的统一原型模型, 因此做了实用化处理:
// 对通过 new 构造的对象 (其 Proto 被设为构造器原型) 沿链查找; 对
// 已知的内置类型 (Array/Map/Set/RegExp/Promise/Error 等) 做类型名匹配。
func (vm *VM) instanceOf(left, right object.Value) object.Value {
	// 右侧必须是可调用构造器; 但内置构造器 (如 Array) 是 *object.Object,
	// IsCallable 不识别, 因此 Object 类型也允许继续匹配。
	_, isObj := right.(*object.Object)
	if !object.IsCallable(right) && !isObj {
		return object.NewBoolean(false)
	}

	// Proxy 作为构造器: 递归解包
	if p, ok := right.(*object.Proxy); ok {
		if pt, ok := p.Target.(*object.Proxy); ok {
			return vm.instanceOf(left, pt)
		}
	}

	// 规范 7.3.19 OrdinaryHasInstance 第 3 步: 左值不是对象时直接返回
	// false, 且**不读取** rval.prototype —— getter/setter 不得被触发
	// (test262 instanceof/prototype-getter-with-primitive.js: 在
	// Function.prototype 上装抛错 getter 后 `0 instanceof Function.prototype`
	// 必须静默得 false)。此前无条件读 ctorProto, 该用例依赖
	// defineProperty 对函数类是 no-op 才"意外"通过; 自有属性接口打通后
	// 必须按规范短路。
	if !object.IsObjectLike(left) {
		return object.NewBoolean(false)
	}

	// 右侧构造器的 prototype 属性
	var ctorProto object.Value
	switch c := right.(type) {
	case *object.Object:
		ctorProto, _ = c.GetProperty("prototype")
	case *object.BuiltinFunction:
		// 内建构造器 (Object/Array/String/Number/...) 的 prototype
		ctorProto, _ = c.GetProperty("prototype")
	case *object.Closure:
		// 闭包构造器: 取 prototype 属性 (new A() 时实例的原型)
		ctorProto, _ = c.GetProperty("prototype")
	}

	// 沿 left 的原型链查找 ctorProto
	if ctorProto != nil {
		cur := left
		// 最多沿链查 64 层防止循环
		for i := 0; i < 64 && cur != nil; i++ {
			if cur == ctorProto {
				return object.NewBoolean(true)
			}
			cur = protoOf(cur)
		}
	}

	// 内置类型匹配兜底
	return object.NewBoolean(matchBuiltinType(left, right))
}

// protoOf 返回值的原型对象 (沿对象模型的原型字段)。
func protoOf(v object.Value) object.Value {
	switch t := v.(type) {
	case *object.Object:
		return t.Proto
	case *object.Array:
		return t.GetProto()
	// 函数对象的 [[Prototype]] 按种类指向内建原型 (Function/GeneratorFunction/
	// AsyncFunction/AsyncGeneratorFunction.prototype)，用于 instanceof 等沿链查找。
	case *object.Closure:
		return object.FuncPrototypeOf(t)
	case *object.BuiltinFunction:
		return object.FuncPrototypeOf(t)
	case *object.BuiltinMethod:
		return object.FuncPrototypeOf(t)
	// Temporal 类型把原型放在类型注册表里 (见 object.SetTemporalProto)，
	// 各自实现了 GetProto()。缺了这些分支，instanceof 会退化成名称匹配，
	// 而构造器名 ("Instant") 与类型标识并不对应。
	case *object.TemporalInstant:
		return t.GetProto()
	case *object.TemporalPlainDateTime:
		return t.GetProto()
	case *object.TemporalPlainDate:
		return t.GetProto()
	case *object.TemporalPlainTime:
		return t.GetProto()
	case *object.TemporalPlainYearMonth:
		return t.GetProto()
	case *object.TemporalPlainMonthDay:
		return t.GetProto()
	case *object.TemporalZonedDateTime:
		return t.GetProto()
	case *object.TemporalDuration:
		return t.GetProto()
	case *object.TemporalTimeZone:
		return t.GetProto()
	case *object.TemporalCalendar:
		return t.GetProto()
	}
	return nil
}

// matchBuiltinType 对内置类型做名称匹配 (instanceof Array/Map/Set/...)。
func matchBuiltinType(left, right object.Value) bool {
	// 通过构造器内建名判断
	ctorName := builtinName(right)
	if ctorName == "" {
		return false
	}
	lt := left.Type()
	switch ctorName {
	case "Array":
		return lt == object.ARRAY_OBJ
	case "Object":
		// 数组、对象、函数等都是 Object 的实例
		return lt == object.OBJECT_OBJ || lt == object.ARRAY_OBJ || object.IsCallable(left) ||
			lt == object.MAP_OBJ || lt == object.SET_OBJ || lt == object.REGEXP_OBJ ||
			lt == object.PROMISE_OBJ || lt == object.ERROR_OBJ
	case "Map":
		return lt == object.MAP_OBJ
	case "Set":
		return lt == object.SET_OBJ
	case "RegExp":
		return lt == object.REGEXP_OBJ
	case "Promise":
		return lt == object.PROMISE_OBJ
	case "Error", "TypeError", "RangeError", "ReferenceError", "SyntaxError":
		return lt == object.ERROR_OBJ
	case "SuppressedError":
		// explicit resource management: 释放期合成错误。精确按 Name 匹配 ——
		// 不能并进上面那组 (那组对任意 ERROR_OBJ 都返回真)。
		if e, ok := left.(*object.Error); ok {
			return e.Name == "SuppressedError"
		}
		return false
	case "String":
		return lt == object.STRING_OBJ
	case "Number":
		return lt == object.NUMBER_OBJ
	case "Boolean":
		return lt == object.BOOLEAN_OBJ
	case "Function":
		return object.IsCallable(left)
	}
	return false
}

// builtinName 返回内置构造器/函数的名称。
func builtinName(v object.Value) string {
	switch f := v.(type) {
	case *object.BuiltinFunction:
		if f.Name != "" {
			return f.Name
		}
	case *object.BuiltinMethod:
		if f.Name != "" {
			return f.Name
		}
	}
	// 通过属性名兜底 (closure 构造器一般带 name)
	if o, ok := v.(*object.Object); ok {
		if nv, found := o.GetProperty("name"); found {
			if s, ok := nv.(*object.String); ok {
				return s.Value
			}
		}
	}
	return ""
}

// describeCallee 生成错误信息中对"被当作函数调用的值"的简短描述。
//
// 直接用 Inspect 会把整个内建对象 (含 prototype 上的几十个方法) 塞进错误信息，
// 例如 `Array(3)` 未定义时会打印几千字符。这里对超长描述做截断，
// 并优先使用构造器/函数的名字。
// invokeWithThis 以 thisVal 为 this 调用 fn, args 为实参。
//
// OP_CALL_METHOD 与 OP_CALL_METHOD_SPREAD 共用: 两者只差实参来源 (栈上定长 vs
// 数组), 调用语义完全一致。返回值遵守 VM 的统一约定 ——
//   - nil: 调用已完成 (结果已压栈), 或异常已被 catch/finally 接住, 调用方继续;
//   - 非 nil: 异常继续向外传播, 调用方 return。
func (vm *VM) invokeWithThis(fn, thisVal object.Value, args []object.Value) error {
	// 一次性 new.target 传递标记 (super() 前由 OP_NEW_TARGET_MARK 置位):
	// 只有被调是 Closure 时由 callClosure 消费装配父构造器帧; 内建/代理被调
	// 不装配 JS 帧, 这里直接丢弃, 避免泄漏给后续无关调用。
	if _, isClosure := fn.(*object.Closure); !isClosure {
		vm.hasPendingNewTarget = false
		vm.pendingNewTarget = nil
	}
	switch callee := fn.(type) {
	case *object.BuiltinFunction:
		// 内建函数: 不传 this，直接传参数
		result := callee.Fn(args...)
		if result == nil {
			result = object.UndefinedSingleton
		}
		if thrown, err := vm.throwIfError(result, callee.ReturnIsValue); thrown {
			return err
		}
		vm.stack.Push(result)
		if err := vm.checkCallbackErr(); err != nil {
			return vm.rethrowBridgeError(err)
		}
	case *object.BuiltinMethod:
		// 内建方法: this 作为第一个参数传递
		result := callee.Fn(thisVal, args...)
		if result == nil {
			result = object.UndefinedSingleton
		}
		if thrown, err := vm.throwIfError(result, false); thrown {
			return err
		}
		vm.stack.Push(result)
		if err := vm.checkCallbackErr(); err != nil {
			return vm.rethrowBridgeError(err)
		}
	case *object.Closure:
		// 箭头函数: this 是创建时的词法绑定, 与调用形态 (含方法调用) 无关 ——
		// 原样调用, 不重绑 thisVal。非箭头函数: 用 thisVal 作接收者创建绑定闭包
		// (callClosure 再按 sloppy 规则把 undefined/null 归一为 globalThis)。
		bound := callee
		if !callee.IsArrow {
			bound = &object.Closure{
				Fn:             callee.Fn,
				Env:            callee.Env,
				This:           thisVal,
				NewTarget:      callee.NewTarget,
				IsArrow:        callee.IsArrow,
				CapturedLocals: callee.CapturedLocals,
			}
		}
		// generator 方法调用: 创建 Generator (this 绑定保留在闭包中)。
		// 有形参前导段时同样先建立前导帧, 由主循环同步绑定形参后冻结。
		if bound.Fn != nil && bound.Fn.IsGenerator {
			if genHasPrologue(bound.Fn) {
				if err := vm.setupGenPrologueFrame(bound, args); err != nil {
					return vm.throwJSError(err)
				}
			} else {
				vm.stack.Push(object.NewGenerator(bound, args))
			}
		} else {
			if err := vm.callClosure(bound, args); err != nil {
				return vm.throwJSError(err)
			}
		}
	case *object.Proxy:
		// 代理方法调用: 转发到 apply trap，this 为 thisVal
		result, err := vm.proxyApply(callee, thisVal, args)
		if err != nil {
			return vm.rethrowBridgeError(err)
		}
		vm.stack.Push(result)
	default:
		return vm.throwNamedError("TypeError", "%s is not a function", describeCallee(fn))
	}
	return nil
}

func describeCallee(v object.Value) string {
	if v == nil {
		return "undefined"
	}
	if name := builtinName(v); name != "" {
		return name
	}
	s := v.Inspect()
	if len(s) > 64 {
		s = s[:64] + "..."
	}
	return s
}

// consumeEvalMark 消费一次 OP_EVAL_MARK / OP_EVAL_MARK_INIT (及 roiE5Z 的
// OP_EVAL_MARK_HOME 家族): 弹出被调值后调用。
// 只有当被调恰为全局 %eval% 内建 (直接 eval 规范要求引用的值即内建本身)
// 时:
//   - 把**调用者帧生效的 this** 经 object 层桥传给 eval 内建 (direct eval
//     的 this 绑定与调用者一致 —— 规范 sec-performeval);
//   - 把调用者帧的 new.target 与「该语境是否允许 new.target」一并传入;
//   - 把调用点的 super home 上下文 (标记携带) 传入 (roiE5Z);
//   - 若本次是类字段初始化器内的直接 eval, 再置位 stdlib 的一次性受限
//     标志, 让 eval 内建进入补充早错模式。
//
// 否则直接丢弃标记 —— 被局部变量遮蔽的 eval 不是直接 eval, 既不应继承 this
// 也不应受限, 标记也不能泄漏给后续无关的 eval 调用。
// 无论是否命中都把 vm.pendingDirectEval / pendingEvalInit / pendingEvalHome*
// 清零 (标记只对紧邻的这一次调用有效)。
func (vm *VM) consumeEvalMark(fn object.Value) {
	if !vm.pendingDirectEval {
		return
	}
	vm.pendingDirectEval = false
	// init 受限标志与 this 桥都是一次性的: 无论本次是否命中 eval 都清零。
	initRestricted := vm.pendingEvalInit
	vm.pendingEvalInit = false
	superHome := vm.takePendingEvalHome()
	if vm.globals == nil {
		return
	}
	if v, ok := vm.globals.Get("eval"); ok && v == fn {
		thisVal, callerStrict := vm.callerEvalContext()
		object.SetDirectEvalThis(thisVal, callerStrict)
		// 直接 eval 的 new.target 上下文: 调用者帧的 new.target 与「该语境是否
		// 允许 new.target」。规范只允许「非箭头函数体内的直接 eval」含 new.target。
		nt, ntAllowed := vm.callerEvalNewTarget()
		object.SetDirectEvalNewTarget(nt, ntAllowed)
		// 直接 eval 的 super home 上下文 (roiE5Z): 无 home 语境的调用点
		// Has=false, eval 源码里的 super.x 维持 SyntaxError。
		object.SetDirectEvalSuperHome(superHome)
		if initRestricted {
			stdlib.MarkDirectEvalInit()
		}
	}
}

// clearPendingEvalHome 清空 pending 的 super home 上下文 (取出即清零,
// 见 consumeEvalMark 的同款纪律)。
func (vm *VM) clearPendingEvalHome() {
	vm.pendingEvalHomeName = ""
	vm.pendingEvalHomeStatic = false
	vm.pendingEvalHomeThis = false
	vm.pendingEvalSuperName = ""
	vm.hasPendingEvalHome = false
}

// takePendingEvalHome 取出并清零 pending 的 super home 上下文, 组装成
// object.EvalSuperHome 供 stdlib 的 eval 内建消费 (Has=false = 无 home)。
func (vm *VM) takePendingEvalHome() object.EvalSuperHome {
	h := object.EvalSuperHome{
		Name:      vm.pendingEvalHomeName,
		Static:    vm.pendingEvalHomeStatic,
		ThisHome:  vm.pendingEvalHomeThis,
		SuperName: vm.pendingEvalSuperName,
		Has:       vm.hasPendingEvalHome,
	}
	vm.clearPendingEvalHome()
	return h
}

// markName 取 OP_EVAL_MARK 家族指令操作数指向的常量池字符串 (home 类名 /
// 父类名)。取不到 (非字符串常量) 时返回空串, 由调用方按「无名字」处理。
func (vm *VM) markName(operand uint16, frame *Frame) string {
	if frame == nil || frame.Constants == nil {
		return ""
	}
	if s, ok := frame.Constants.Get(operand).(*object.String); ok {
		return s.Value
	}
	return ""
}

// setPendingEvalHomeName 记录 OP_EVAL_MARK_HOME / _STATIC 的 home 类名。
// 不清零其它 pending 标记 (HOME → SUPER 成对出现时 SUPER 要保留)。
func (vm *VM) setPendingEvalHomeName(operand uint16, frame *Frame) {
	if name := vm.markName(operand, frame); name != "" {
		vm.pendingEvalHomeName = name
		vm.hasPendingEvalHome = true
	}
}

// callerEvalContext 求"当前帧生效的 this"与"当前帧是否严格" —— 供直接 eval
// 继承。主帧按 script(globalThis, 非严格)/module(undefined, 严格) 区分
// (与 OP_THIS 同口径); 函数帧直接用 callClosure 已归一的 frame.This 与
// 闭包 Fn.IsStrict。
//
// 严格性影响 eval 代码的 thisValue: 调用者严格时 eval 代码恒严格, thisValue
// 原样保留 (undefined 不归一为 globalThis, 规范 PerformEval: strictCaller)。
func (vm *VM) callerEvalContext() (object.Value, bool) {
	frame := vm.currentFrame()
	if frame == nil {
		return object.UndefinedSingleton, false
	}
	if frame.This != nil {
		strict := frame.Closure != nil && frame.Closure.Fn != nil && frame.Closure.Fn.IsStrict
		return frame.This, strict
	}
	if frame.Closure == nil {
		// 主帧: script 顶层 this = globalThis (非严格); module 顶层 = undefined
		// (模块恒严格)。
		if vm.moduleMode {
			return object.UndefinedSingleton, true
		}
		return vm.globalThisValue(), false
	}
	strict := frame.Closure.Fn != nil && frame.Closure.Fn.IsStrict
	if frame.Closure.This != nil {
		return frame.Closure.This, strict
	}
	return object.UndefinedSingleton, strict
}

// callerEvalNewTarget 求"当前帧生效的 new.target"与"该语境是否允许 eval 源码
// 出现 new.target" —— 供直接 eval 继承。规范 sec-scripts-static-semantics-early-
// errors: NewTarget 只在「非箭头函数体内的直接 eval」合法, 故 allowed 仅当调用者
// 帧是非箭头函数 (frame.Closure != nil && !IsArrow) 时为真。主帧 (script/module
// 顶层) 与箭头帧均为 false —— 前者不是函数代码, 后者对 Contains 透明。
func (vm *VM) callerEvalNewTarget() (object.Value, bool) {
	frame := vm.currentFrame()
	if frame == nil {
		return object.UndefinedSingleton, false
	}
	allowed := frame.Closure != nil && !frame.Closure.IsArrow
	nt := frame.NewTarget
	if nt == nil {
		nt = object.UndefinedSingleton
	}
	return nt, allowed
}

// propKey 将值转换为属性键字符串 (与 stdlib.toPropKey 一致)。
func propKey(v object.Value) string {
	switch k := v.(type) {
	case *object.String:
		return k.Value
	case *object.Number:
		return k.Inspect()
	case *object.Symbol:
		return k.Inspect()
	}
	return v.Inspect()
}

func (vm *VM) proxyApply(proxy *object.Proxy, thisArg object.Value, args []object.Value) (object.Value, error) {
	res, handled, err := vm.proxyTrap(proxy, "apply", []object.Value{proxy.Target, thisArg, object.NewArray(args)})
	if err != nil {
		return nil, err
	}
	if !handled {
		// 无 trap: 直接调用目标
		return vm.callFunction(proxy.Target, thisArg, args)
	}
	return res, nil
}

// proxyConstruct 处理对代理 (目标为构造函数) 的 new 调用。
func (vm *VM) proxyConstruct(proxy *object.Proxy, args []object.Value) (object.Value, error) {
	res, handled, err := vm.proxyTrap(proxy, "construct", []object.Value{proxy.Target, object.NewArray(args)})
	if err != nil {
		return nil, err
	}
	if !handled {
		// 无 trap: 直接以 new 方式调用目标
		newObj := object.NewObject()
		result, err := vm.callFunction(proxy.Target, newObj, args)
		if err != nil {
			return nil, err
		}
		// 构造语义: 若构造器返回对象则用之，否则用新对象
		if result != nil && object.IsObjectLike(result) {
			return result, nil
		}
		return newObj, nil
	}
	return res, nil
}

// isCallable 检查值是否可作为函数调用 (含函数目标代理)。
func isCallable(v object.Value) bool {
	if object.IsCallable(v) {
		return true
	}
	if p, ok := v.(*object.Proxy); ok {
		return isCallable(p.Target)
	}
	return false
}

// handleThrow 处理异常抛出。
// 检查 tryStack 中是否有匹配的 catch/finally 处理器。
// 如果找到: 设置 PC 和栈，返回 true。
// 如果未找到: 返回 false (异常将传播到上层)。
// handleThrow 包装异常处理器搜索: 进入搜索前记录抛出点 PC (T05 源码帧)。
// 若最终无处理器匹配 (返回 false), 该 PC 就是出错指令位置; 若有 catch/
// finally 匹配, PC 会被改写、异常不算未捕获, 记录值作废 (下次抛出覆盖)。
func (vm *VM) handleThrow(val object.Value) bool {
	if vm.frameIdx >= 0 && vm.frameIdx < len(vm.frames) && vm.frames[vm.frameIdx] != nil {
		vm.lastThrowPC = vm.currentFrame().PC
		vm.lastThrowFrame = vm.currentFrame()
		vm.hasThrowPC = true
	}
	matched := vm.handleThrowInner(val)
	if matched {
		vm.hasThrowPC = false
	}
	return matched
}

func (vm *VM) handleThrowInner(val object.Value) bool {
	for len(vm.tryStack) > 0 {
		entry := vm.tryStack[len(vm.tryStack)-1]

		// 边界检查: 处理器在外层帧 (低于当前子执行的起始帧) 时不在此处
		// 解退。让异常以 ThrowError 返回给回调桥，由外层的抛出路径
		// (throwIfError/rethrowBridgeError) 在正确的嵌套层级匹配它。
		if entry.frameIdx < vm.throwBoundary {
			return false
		}

		// 防御: 丢弃指向"尚未进入的帧"的死条目。try 块内 return 跳过
		// OP_POP_TRY 会留下 frameIdx 大于当前帧的死条目, 若被 handleThrow
		// 当作处理器消费, 会把当前帧 PC 劫持到已退出帧的 catchPC —— 字节码
		// 跨编译单元串台, 随即栈失衡 panic (T04 panic 家族根因之一)。
		// 死条目在这里被安全丢弃, 继续向外层找真正的处理器。
		if entry.frameIdx > vm.frameIdx {
			vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]
			continue
		}

		// 本条目正在跑自己的 finally: 现在这个异常正是从那个 finally 体里抛出来
		// 并穿出去的 ⇒ 丢弃本条目挂起的旧异常 (新异常覆盖旧异常), 继续向外找。
		// 这就是「finally 里 throw 会替换原异常」的落点; 同时保证不会有挂起值
		// 残留到之后某个 END_FINALLY 上被误重抛。
		if entry.inFinally {
			vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]
			continue
		}

		// 如果 try 条目在不同的帧中，先弹出帧。
		// 栈恢复以帧的 StackBase 为准截断 (与 OP_RETURN 一致) ——
		// 旧的"每帧无条件 Pop 一次"假设帧恰好遗留一个值, 在调用方
		// 实参求值阶段发生异常时会误弹调用方压在栈上的中间值。
		if vm.frameIdx > entry.frameIdx {
			for vm.frameIdx > entry.frameIdx {
				base := vm.frames[vm.frameIdx].StackBase
				vm.popFrame()
				if vm.stack.Len() > base {
					vm.stack.Truncate(base)
				}
			}
		}

		vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]

		// 恢复栈到 try 开始时的高度
		for vm.stack.Len() > entry.stackBase {
			vm.stack.Pop()
		}

		if entry.catchPC > 0 {
			// 有 catch: 推入错误值，跳到 catch 块
			vm.stack.Push(val)
			vm.currentFrame().PC = entry.catchPC
			return true
		}
		if entry.finallyPC > 0 {
			// 有 finally 但无 catch: 条目**留在 tryStack 上**并标记 inFinally,
			// 由 pendingVal 承载挂起值, 然后跳进 finally 体。
			entry.inFinally = true
			entry.pendingVal = val
			vm.tryStack = append(vm.tryStack, entry)
			vm.currentFrame().PC = entry.finallyPC
			return true
		}
		// 无 catch 无 finally: 继续向上查找
	}
	return false
}

// handleReturn 把 return 完成沿 try 处理器栈向外传播。
//
// 与 handleThrow 的关键区别: catch 块**不**拦截 return 完成 (规范:
// Completion 为 return 时只展开 finally)。因此这里跳过 catch-only 条目,
// 只对有 finally 的条目进入其 finally 体。返回 true 表示已找到 finally
// 并把 PC 劫持到 finallyPC。
func (vm *VM) handleReturn(val object.Value) bool {
	return vm.handleReturnInner(val)
}

func (vm *VM) handleReturnInner(val object.Value) bool {
	for len(vm.tryStack) > 0 {
		entry := vm.tryStack[len(vm.tryStack)-1]

		// 与 handleThrowInner 同款边界/死条目防御 (见其注释)。
		if entry.frameIdx < vm.throwBoundary {
			return false
		}
		if entry.frameIdx > vm.frameIdx {
			vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]
			continue
		}
		// 已在自身 finally 体内: return 从 finally 里穿出 ⇒ 丢弃挂起完成, 继续向外。
		if entry.inFinally {
			vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]
			continue
		}

		if vm.frameIdx > entry.frameIdx {
			for vm.frameIdx > entry.frameIdx {
				base := vm.frames[vm.frameIdx].StackBase
				vm.popFrame()
				if vm.stack.Len() > base {
					vm.stack.Truncate(base)
				}
			}
		}

		vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]

		for vm.stack.Len() > entry.stackBase {
			vm.stack.Pop()
		}

		if entry.finallyPC > 0 {
			entry.inFinally = true
			entry.pendingReturn = true
			entry.pendingVal = val
			vm.tryStack = append(vm.tryStack, entry)
			vm.currentFrame().PC = entry.finallyPC
			return true
		}
		// 无 finally: return 完成不被 catch 捕获, 继续向外展开。
	}
	return false
}

// ensureCurrentExports 返回当前模块的导出表, 必要时惰性创建。
// 导出指令 (OP_EXPORT 系列) 统一走它, 避免各处重复判空。
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

func (vm *VM) loadModuleFile(spec, absPath string) (*ModuleExports, error) {
	// 检查缓存
	if mod, ok := vm.modules[absPath]; ok {
		if r := mod.rec; r != nil && r.status == moduleRejected {
			return nil, fmt.Errorf("%s", r.errText)
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
		return nil, fmt.Errorf("Module execution error: %v", rec.errText)
	}
	if rec.pendingDeps == 0 {
		vm.runModuleBody(mod, rec, modVM, c)
	} else {
		// 依赖未就绪: 记录本体闭包, 依赖完成时由 startDeferred 触发。
		rec.status = modulePending
		rec.runBody = func() { vm.runModuleBody(mod, rec, modVM, c) }
	}
	if rec.status == moduleRejected {
		return nil, fmt.Errorf("Module execution error: %v", rec.errText)
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
func errorToJSValue(err error) object.Value {
	if te, ok := err.(*ThrowError); ok {
		return te.Value
	}
	if jt, ok := err.(*jsThrow); ok {
		return object.NewErrorWithName(jt.Name, jt.Message)
	}
	if err == nil {
		return object.UndefinedSingleton
	}
	return object.NewErrorWithName("Error", err.Error())
}

// SetModuleBase 设置模块基准路径。
func (vm *VM) SetModuleBase(path string) {
	vm.moduleBase = path
}

// ===== 辅助方法 =====

// globalThisValue 返回全局对象绑定 (globals 里由 stdlib 装配); 未装配 (裸 VM)
// 时回退 undefined。
func (vm *VM) globalThisValue() object.Value {
	if g, ok := vm.globals.Get("globalThis"); ok {
		return g
	}
	return object.UndefinedSingleton
}

// normalizedThis 实现 sloppy 模式的 this 归一: 非箭头函数被以 undefined/null
// (含"无接收者的裸调用"——等价于 undefined 接收者) 调用时, this 替换为
// globalThis; 有真实接收者 (对象 / 构造实例 / 基类 this) 原样返回。
//
// Gox 目前没有 strict 模式, 一律按 sloppy 处理 (不引入 strict 分支)。
func (vm *VM) normalizedThis(t object.Value) object.Value {
	if t == nil || t == object.UndefinedSingleton || t == object.NullSingleton {
		return vm.globalThisValue()
	}
	return t
}

// resolveFrameThis 由被调闭包算出该帧生效的 this:
//   - 箭头函数: 词法绑定, 取闭包携带的 This (创建时从所在帧捕获);
//   - 严格函数: 原样保留 (undefined/null 不归一 —— 规范 strict 语义);
//   - 非箭头 sloppy 函数: 按 sloppy 归一 (undefined/null → globalThis)。
func (vm *VM) resolveFrameThis(closure *object.Closure) object.Value {
	if closure == nil {
		return nil
	}
	if closure.IsArrow {
		return closure.This
	}
	if closure.Fn != nil && closure.Fn.IsStrict {
		return closure.This
	}
	return vm.normalizedThis(closure.This)
}

// frameThis 求"当前帧的 this", 供箭头函数在创建时做词法捕获。
// 优先取帧上已归一的 This; 主帧再按 script(globalThis)/module(undefined) 区分。
func (vm *VM) frameThis(frame *Frame) object.Value {
	if frame.This != nil {
		return frame.This
	}
	if frame.Closure == nil {
		if vm.moduleMode {
			return object.UndefinedSingleton
		}
		return vm.globalThisValue()
	}
	if frame.Closure.This != nil {
		return frame.Closure.This
	}
	return object.UndefinedSingleton
}

// frameNewTarget 求"当前帧的 new.target", 供箭头函数在创建时做词法捕获。
// 帧上已归一的 NewTarget 优先; 主帧 (frame.Closure == nil, script/module 顶层)
// 语法上不允许 new.target, 但兜底返回 undefined。普通函数帧的 NewTarget 恒非
// nil (callClosure 装配时写入), 故这里返回的非 nil 值可直接作为捕获结果。
func (vm *VM) frameNewTarget(frame *Frame) object.Value {
	if frame != nil && frame.NewTarget != nil {
		return frame.NewTarget
	}
	return object.UndefinedSingleton
}

// ===== with 语句运行时支持 (对象环境记录) =====

// withRefOf 从常量池取出 OP_WITH_* 的操作数所指的 *WithRef。
func withRefOf(frame *Frame, operand uint16) *bytecode.WithRef {
	ref, _ := frame.Constants.Get(operand).(*bytecode.WithRef)
	return ref
}

// withResolve 沿 ref.Slots (内层在前) 逐个取 with 对象, 查 ref.Name 属性。
//
// 命中返回 (对象, 值, true); 属性不存在或被 Symbol.unscopables 排除返回
// (nil, nil, false)。属性 getter / unscopables getter 抛出的用户异常以
// error 返回 —— 调用方须走 JS 抛出流程 (rethrowBridgeError)。
func (vm *VM) withResolve(frame *Frame, ref *bytecode.WithRef) (object.Value, object.Value, bool, error) {
	for _, slot := range ref.Slots {
		if slot < 0 || slot >= len(frame.Locals) {
			continue
		}
		obj := frame.Locals[slot]
		if obj == nil {
			continue
		}
		val, found, err := vm.withGetBinding(obj, ref.Name)
		if err != nil {
			return nil, nil, false, err
		}
		if found {
			return obj, val, true, nil
		}
	}
	return nil, nil, false, nil
}

// withGetBinding 判断 with 对象 obj 是否绑定 name 并取其值 (HasBinding + Get)。
// 属性不存在 → (nil, false); 存在但被 @@unscopables 排除 → 按不存在处理。
func (vm *VM) withGetBinding(obj object.Value, name string) (object.Value, bool, error) {
	if p, ok := obj.(*object.Proxy); ok {
		has, err := vm.proxyHas(p, name)
		if err != nil {
			return nil, false, err
		}
		if !has {
			return nil, false, nil
		}
		v, err := vm.proxyGet(p, name, p)
		if err != nil {
			return nil, false, err
		}
		return v, true, nil
	}
	if o, ok := obj.(*object.Object); ok {
		val, found := o.GetProperty(name)
		if err := vm.checkCallbackErr(); err != nil {
			return nil, false, err
		}
		if !found {
			return nil, false, nil
		}
		blocked, err := vm.unscopablesBlocked(o, name)
		if err != nil {
			return nil, false, err
		}
		if blocked {
			return nil, false, nil
		}
		return val, true, nil
	}
	// 其余类型 (原始值包装 / 数组 / 函数对象等) 走通用 GetProperty 通路;
	// 它们没有 @@unscopables 语义, 不做排除判定。
	val, found := obj.GetProperty(name)
	if err := vm.checkCallbackErr(); err != nil {
		return nil, false, err
	}
	if !found {
		return nil, false, nil
	}
	return val, true, nil
}

// unscopablesBlocked 判断对象 o 的 @@unscopables 是否把 name 排除出 with 作用域。
// 仅在属性已确认存在后调用 (规范: HasBinding 先 HasProperty 再读 @@unscopables)。
func (vm *VM) unscopablesBlocked(o *object.Object, name string) (bool, error) {
	un, found := object.LookupSymbolProperty(o, object.GetGlobalSymbol("Symbol.unscopables"))
	if !found {
		return false, nil
	}
	// LookupSymbolProperty 返回原始访问器, 需显式调用其 getter (可能抛异常)。
	if acc, isAcc := un.(*object.Accessor); isAcc {
		if acc.Getter == nil || !object.IsCallable(acc.Getter) {
			return false, nil
		}
		un = object.CallFunction(acc.Getter, o)
		if err := vm.checkCallbackErr(); err != nil {
			return false, err
		}
	}
	uo, ok := un.(*object.Object)
	if !ok {
		// 非对象 (含 undefined/null/原始值) 不排除任何属性。
		return false, nil
	}
	bval, bfound := uo.GetProperty(name)
	if err := vm.checkCallbackErr(); err != nil {
		return false, err
	}
	if bfound && bval != nil && bval.IsTruthy() {
		return true, nil
	}
	return false, nil
}

// withSet 把 val 写入 with 对象的 name 属性 (SetMutableBinding)。
func (vm *VM) withSet(owner object.Value, name string, val object.Value) error {
	switch o := owner.(type) {
	case *object.Proxy:
		return vm.proxySet(o, name, val, o)
	default:
		owner.SetProperty(name, val)
		return nil
	}
}

// withDelete 删除 with 对象上的 name 属性 (delete 标识符语义)。
func (vm *VM) withDelete(owner object.Value, name string) {
	if o, ok := owner.(*object.Object); ok {
		o.DeleteProperty(name)
	}
	// 其余类型 (含 Proxy) 暂无属性删除通路, 静默跳过 —— delete 结果仍为 true。
}

// storeLocalSlot 向当前帧的局部槽写入值, 并复刻 OP_STORE 的传播语义
// (SharedCells / 闭包捕获 / 子闭包 / ModifiedSlots)。仅供 with 的局部回退路径使用。
func (vm *VM) storeLocalSlot(frame *Frame, slot int, val object.Value) {
	for len(frame.Locals) <= slot {
		frame.Locals = append(frame.Locals, object.UndefinedSingleton)
	}
	frame.Locals[slot] = val
	if slot < len(frame.SharedCells) {
		frame.SharedCells[slot] = val
	}
	if frame.Closure != nil && slot < len(frame.Closure.CapturedLocals) {
		frame.Closure.CapturedLocals[slot] = val
	}
	for _, cl := range frame.CreatedClosures {
		if slot < len(cl.CapturedLocals) {
			cl.CapturedLocals[slot] = val
		}
	}
	if frame.ModifiedSlots == nil {
		frame.ModifiedSlots = make(map[int]bool)
	}
	frame.ModifiedSlots[slot] = true
}

// createClosure 从 FunctionMetadata 创建闭包。
// 捕获当前帧的外层局部变量 (slots 0..BaseSlot-1)。
func (vm *VM) createClosure(meta *bytecode.FunctionMetadata, frame *Frame) *object.Closure {
	// 保存当前帧的常量池引用
	fn := metaToCompiledFunction(meta, frame.Constants.Constants)

	// 捕获外层局部变量: 共享当前帧的 Locals 数组本身，而不是拷一份快照。
	//
	// 拷快照会让兄弟闭包各自持有副本 —— 离开创建帧后它们就彻底失联，于是
	// `const [inc, get] = mk(); inc(); get()` 读不到彼此的修改。
	// 共享同一个数组后，写操作 (OP_STORE → SharedCells) 对所有捕获者同时可见；
	// 该数组随闭包存活 (GC 保活)，等价于 ECMAScript 的 binding cell 逃逸到堆。
	//
	// ⚠ 帧从闭包装配而来时 (callClosure 用 CapturedLocals 拷出新 Locals,
	// 原始 binding cell 在 frame.SharedCells), 必须取 SharedCells 作捕获数组:
	// 取帧 Locals 会让「在 getter/method 体内创建的闭包」捕获 getter 帧的
	// 私有拷贝 —— getter 每次属性访问都被重新调用, 内层闭包每次读到创建时
	// 的快照, 与外层 cell 失联 (r6e5qp: `make().foo()` 恒 1, 本应累加;
	// test262 async-generator yield* 委托的 nextCount 记序模式全被它挡下)。
	var captured []object.Value
	if meta.BaseSlot > 0 {
		// 捕获前缀的精确上界 (编译期扫描指令流得出, 见 compiler.computeCapturePrefixLen)。
		// BaseSlot 是外层作用域槽总数的粗粒度上界, 会把创建帧自身的局部也
		// 捎带进前缀; CapturePrefixLen 只覆盖本函数真正引用的外层 slot。
		prefixLen := meta.CapturePrefixLen
		if prefixLen <= 0 {
			prefixLen = meta.BaseSlot
		}
		// frame.SharedCells 是本帧外层的原始 binding cell 数组 (callClosure
		// 装配闭包帧时 frame.Locals 只是它的拷贝)。捕获前缀整体落在 cell
		// 数组内时直接共享它: 内层闭包与共享链不断。否则 (前缀需要本帧
		// 自身的局部) 整段取帧数组 —— 自身段本就随每次调用新建, 帧数组
		// 作宿主是安全的 (被闭包保活)。
		//
		// 反例 (r6e5qp): getter 体内再定义的闭包若按帧数组捕获, getter 每次
		// 属性访问都重新调用, 内层闭包读到的是本次帧的私有快照, 与外层
		// cell 失联 —— make().foo() 恒返回 1; test262 async-generator yield*
		// 委托的 get-next/nextCount 记序模式全被它挡下。
		src := frame.Locals
		if len(frame.SharedCells) > 0 && prefixLen <= len(frame.SharedCells) {
			src = frame.SharedCells
		}
		// 裁剪到 prefixLen: 闭包只会访问 slot < prefixLen, 不裁剪会让整帧
		// Locals 在调用时被拷进内层 slot 区, 把尚未初始化的绑定"填上"外层
		// 残留值, TDZ 检测随之失效; 且前缀必须覆盖 prefixLen, 否则内层
		// LOAD 读到 NewFrame 的 nil 误报 TDZ。
		if prefixLen <= len(src) {
			captured = src[:prefixLen]
		} else {
			captured = src
		}
	}

	// this 绑定: 只有箭头函数 (以及编译器合成的 async 内层 generator —
	// 见 LexicalThis) 在创建时按词法捕获所在帧的 this (含主帧的 globalThis);
	// 其它非箭头函数的 this 由**调用形态**决定, 不在此绑定 —— 留 nil, 由
	// callClosure 按 sloppy 规则归一 (裸调用 → globalThis)。若给所有非箭头
	// 都写入外层 this, 裸调用会错误继承创建处的方法 this。
	var thisVal object.Value
	if meta.IsArrow || meta.LexicalThis {
		thisVal = vm.frameThis(frame)
	}

	// new.target 绑定: 只有箭头函数按词法捕获所在帧生效的 new.target (与 this
	// 同型)。箭头对 new.target 词法透明, 其取值必须解析到词法外层非箭头函数
	// —— 在**创建时**捕获才能跨越「箭头被返回后于别处调用」的情形 (rNAtZs)。
	// 非箭头留 nil, 由 callClosure 依调用形态 (OP_NEW / super 传递 / 普通调用)
	// 决定。async 内层合成 generator (LexicalThis) 不捕获: async 函数本身是
	// 普通函数, 其 new.target 与是否合成 generator 无关。
	var newTargetVal object.Value
	if meta.IsArrow {
		newTargetVal = vm.frameNewTarget(frame)
	}

	// M2: 记录"这个函数属于哪个源码单元" —— 模块导出的函数被入口调用时,
	// 抛错帧属于模块的 .ts, 而不是当前(入口)VM 的单元。只登记模块单元:
	// 入口函数的帧可直接回退 mainUnit, 登记它们只会让注册表无谓增长。
	if vm.units != nil && vm.mainUnit != nil && vm.mainUnit.isModule {
		vm.units.put(fn, vm.mainUnit)
	}
	// 函数对象自身的 [[Prototype]]: 按函数种类选择内建原型
	// (普通/箭头/类构造器 → %Function.prototype%; function* →
	// %GeneratorFunction.prototype%; async function → %AsyncFunction.prototype%;
	// async function* → %AsyncGeneratorFunction.prototype%)。这些原型对象由
	// stdlib 装配并注册 (object.GetXxxPrototype); 尚未注册时保持 nil，
	// 由 object.FuncPrototypeOf 回退到全局 %Function.prototype%。
	funcProto := object.FuncPrototypeForCompiled(fn)

	return &object.Closure{
		Fn:             fn,
		Env:            vm.globals,
		This:           thisVal,
		NewTarget:      newTargetVal,
		IsArrow:        meta.IsArrow,
		CapturedLocals: captured,
		CreatedAtFrame: vm.frameIdx,
		FuncPrototype:  funcProto,
	}
}

// trackClosure 记录闭包到当前帧的 CreatedClosures 列表。
// 用于 OP_STORE 时将外层变量修改传播到所有子闭包 (outer → closure)。
func (vm *VM) trackClosure(closure *object.Closure) {
	frame := vm.currentFrame()
	frame.CreatedClosures = append(frame.CreatedClosures, closure)
}

// callClosure 调用闭包函数。
// 创建新帧，复制捕获的局部变量，放置参数。
func (vm *VM) callClosure(closure *object.Closure, args []object.Value) error {
	fn := closure.Fn
	if fn == nil {
		return fmt.Errorf("VM: closure has no function")
	}

	// 检查调用栈深度 (jsThrow 使栈溢出 RangeError 能被 try/catch 捕获，
	// 也能经回调桥的 rethrowBridgeError 正确转回 JS 抛出流程)
	if vm.frameIdx >= MaxFrames-1 {
		return &jsThrow{Name: "RangeError", Message: "Maximum call stack size exceeded"}
	}

	// 创建新帧: 使用闭包自带的常量池 (跨模块时不同于 vm.constants)
	constants := vm.constants
	if fn.Constants != nil {
		constants = &bytecode.ConstantPool{Constants: fn.Constants}
	}
	frame := NewFrame(fn.Instructions, constants, fn.NumLocals)
	frame.Closure = closure
	// 本帧生效的 this: 箭头取词法捕获, 非箭头按 sloppy 归一 (裸调用 → globalThis)。
	frame.This = vm.resolveFrameThis(closure)
	// 本帧的 new.target:
	//   - 箭头函数: 取创建时词法捕获的 NewTarget (闭包携带), 与调用形态无关;
	//   - 构造调用 (OP_NEW) 或 super() 传递 (OP_NEW_TARGET_MARK): 取
	//     pendingNewTarget;
	//   - 其余: 取闭包自带的 NewTarget (直接 eval 包装闭包由 stdlib 写入调用者
	//     的 new.target; 普通函数为 nil ⇒ undefined)。
	// pendingNewTarget 是一次性的 (仅对紧邻的这次调用有效), 无论是否消费都清零。
	if closure.IsArrow {
		frame.NewTarget = closure.NewTarget
	} else if vm.hasPendingNewTarget {
		frame.NewTarget = vm.pendingNewTarget
	} else {
		frame.NewTarget = closure.NewTarget
	}
	if frame.NewTarget == nil {
		frame.NewTarget = object.UndefinedSingleton
	}
	vm.hasPendingNewTarget = false
	vm.pendingNewTarget = nil
	// 记录进入本帧时的栈高度, 返回时据此截断清理本帧残留栈值
	frame.StackBase = vm.stack.Len()

	// 复制捕获的外层局部变量作为初值
	if len(closure.CapturedLocals) > 0 {
		copy(frame.Locals, closure.CapturedLocals)
	}
	// 但写入必须回流到共享的 binding cell，供兄弟闭包与后续调用观察
	frame.SharedCells = closure.CapturedLocals

	// 放置参数: 参数从 BaseSlot 开始排列
	baseSlot := fn.BaseSlot

	// 检查是否有 rest 参数 (最后一个参数)
	hasRest := len(fn.Parameters) > 0 && fn.Parameters[len(fn.Parameters)-1].Rest
	restParamIndex := len(fn.Parameters) - 1

	if hasRest {
		// 非 rest 参数正常放置
		numRegular := restParamIndex
		for i := 0; i < numRegular; i++ {
			slot := baseSlot + i
			if slot < len(frame.Locals) {
				if i < len(args) {
					frame.Locals[slot] = args[i]
				} else {
					frame.Locals[slot] = object.UndefinedSingleton
				}
			}
		}
		// rest 参数: 收集剩余参数到数组
		var restElements []object.Value
		if len(args) > numRegular {
			restElements = make([]object.Value, len(args)-numRegular)
			copy(restElements, args[numRegular:])
		}
		restSlot := baseSlot + restParamIndex
		if restSlot < len(frame.Locals) {
			frame.Locals[restSlot] = object.NewArray(restElements)
		}
	} else {
		// 无 rest: 仅放置声明的前 NumParameters 个参数, 多余实参只进入 arguments
		placeCount := len(args)
		if placeCount > fn.NumParameters {
			placeCount = fn.NumParameters
		}
		for i := 0; i < placeCount; i++ {
			slot := baseSlot + i
			if slot < len(frame.Locals) {
				frame.Locals[slot] = args[i]
			}
		}
		// 剩余参数填充 undefined
		for i := placeCount; i < fn.NumParameters; i++ {
			slot := baseSlot + i
			if slot < len(frame.Locals) {
				frame.Locals[slot] = object.UndefinedSingleton
			}
		}
	}

	// 创建 arguments 对象 (类数组, 含全部实参)。放在参数放置之后, 避免被参数覆盖。
	if fn.ArgumentsSlot >= 0 && fn.ArgumentsSlot < len(frame.Locals) {
		argsCopy := make([]object.Value, len(args))
		copy(argsCopy, args)
		frame.Locals[fn.ArgumentsSlot] = object.NewArray(argsCopy)
	}

	// 命名函数表达式的自引用: 名字槽位指向闭包自身 (递归入口)
	if fn.SelfSlot >= 0 && fn.SelfSlot < len(frame.Locals) {
		frame.Locals[fn.SelfSlot] = closure
	}

	vm.pushFrame(frame)
	return nil
}

// genHasPrologue 报告该生成器函数是否需要在调用时同步执行形参前导段
// (默认值/解构)。DeferParams 的例外是普通 async 函数的内层 generator:
// 它的形参绑定必须留在 __spawn 驱动路径内 (错误转 rejected Promise)。
func genHasPrologue(fn *object.CompiledFunction) bool {
	return fn != nil && fn.IsGenerator && !fn.DeferParams && fn.ParamPrologueEnd > 0
}

// setupGenPrologueFrame 为需要同步绑定形参的生成器建立前导帧。
// callClosure 先把实参放入形参槽位, 前导段 (默认值/解构) 由主循环执行;
// 执行到 StopPC 时 runFrom 会调用 freezeGenPrologue 冻结为 Generator。
// 前导段抛出的异常沿正常抛出流程传播 (生成器形参错误是**同步抛出**)。
func (vm *VM) setupGenPrologueFrame(closure *object.Closure, args []object.Value) error {
	if err := vm.callClosure(closure, args); err != nil {
		return err
	}
	frame := vm.currentFrame()
	frame.PendingGen = object.NewGenerator(closure, args)
	frame.StopPC = closure.Fn.ParamPrologueEnd
	return nil
}

// freezeGenPrologue 把形参前导帧冻结为 Generator:
// 保存帧状态 (PC/Locals/Instructions/Constants) 到 Generator, 弹出前导帧,
// 并把 Generator 放到调用点的结果槽位 (帧基处)。
func (vm *VM) freezeGenPrologue(frame *Frame) {
	gen := frame.PendingGen
	gen.PC = frame.PC
	gen.Locals = frame.Locals
	gen.Instructions = frame.Instructions
	gen.Constants = frame.Constants.Constants
	gen.SavedStack = nil
	gen.PendingTries = nil
	gen.PrologueBound = true

	base := frame.StackBase
	vm.popFrame()
	if vm.stack.Len() > base {
		vm.stack.Truncate(base)
	}
	vm.stack.Push(gen)
}

// genResume 驱动生成器前进:
// - 首次: 以 gen.Args 调用闭包创建帧
// - 恢复: 重建保存的帧, 压入 arg 作为 yield 表达式的结果
// 运行到下一个 yield (返回 YieldSignal) 或函数结束。
// 返回: (yield 值/返回值, 是否完成)。
func (vm *VM) genResume(gen *object.Generator, arg object.Value) (object.Value, bool, error) {
	if gen.Done {
		return object.UndefinedSingleton, true, nil
	}

	if !gen.Started {
		// 形参前导段已在调用时执行完毕: 直接重建帧从函数体起始处恢复,
		// 不压入 arg (首次 next 的实参被忽略)。
		if gen.PrologueBound {
			gen.Started = true
			gen.PrologueBound = false
			if gen.PC >= len(gen.Instructions) {
				gen.Done = true
				return object.UndefinedSingleton, true, nil
			}
			vm.rebuildGenFrame(gen, true)
			return vm.runSuspendedGen(gen)
		}
		// 首次启动: 正常调用闭包 (参数来自创建时的 Args)
		if err := vm.callClosure(gen.Closure, gen.Args); err != nil {
			gen.Done = true
			return object.UndefinedSingleton, true, err
		}
		gen.Started = true
		return vm.runSuspendedGen(gen)
	}
	if gen.PC >= len(gen.Instructions) {
		gen.Done = true
		return object.UndefinedSingleton, true, nil
	}
	// 重建帧，压入 arg 作为 yield 表达式的值 (栈顶)
	vm.rebuildGenFrame(gen, false)
	vm.stack.Push(arg)
	return vm.runSuspendedGen(gen)
}

// genThrow 把 throwVal 作为异常抛入暂停在 yield 点的 generator。
// await 的 promise 被 reject 时的恢复路径: generator 体内的 try/catch
// 可以捕获该异常；未捕获时 generator 终止并返回错误。
func (vm *VM) genThrow(gen *object.Generator, throwVal object.Value) (object.Value, bool, error) {
	if gen.Done {
		return object.UndefinedSingleton, true, nil
	}
	if !gen.Started {
		// 尚未启动: 无帧可恢复，视为立即抛出
		gen.Done = true
		return object.UndefinedSingleton, true, &ThrowError{Value: throwVal}
	}
	if gen.PC >= len(gen.Instructions) {
		gen.Done = true
		return object.UndefinedSingleton, true, nil
	}
	genFrame := vm.rebuildGenFrame(gen, false)
	genFrameIdx := vm.frameIdx

	// 仅当 generator 帧自身挂有 try 处理器时才查找处理器:
	// tryStack 中残留的更深/更浅帧条目属于无关执行上下文，
	// 把异常交给它们会破坏帧栈。
	hasOwnHandler := false
	for _, te := range vm.tryStack {
		if te.frameIdx == vm.frameIdx {
			hasOwnHandler = true
			break
		}
	}
	if !hasOwnHandler || !vm.handleThrow(throwVal) {
		gen.Done = true
		// rebuildGenFrame 刚压入的帧必须回收: 否则它留在帧栈上压住
		// 外层 async wrapper 帧, wrapper 的 OP_RETURN 会在错误栈基上
		// 取值 ⇒ async 调用返回函数体的值而不是 Promise (rZn4IS)。
		// 注意顺序: 先取 frame/索引再 handleThrow, 因为 handleThrow
		// 可能已经把 vm.frameIdx 降到更低。
		vm.unwindGenFrame(genFrame, genFrameIdx)
		return object.UndefinedSingleton, true, &ThrowError{Value: throwVal}
	}

	return vm.runSuspendedGen(gen)
}

// genReturn 把 returnVal 作为 return 完成注入暂停在 yield 点的 generator。
// async generator 的 return() 走这里: 展开体内 finally 后返回 (returnVal, true)。
// 尚未启动或体内无 finally 时直接关闭, 等价规范中 suspendedStart/completed
// 状态下 return 立即结算 {value: returnVal, done: true}。
func (vm *VM) genReturn(gen *object.Generator, returnVal object.Value) (object.Value, bool, error) {
	if gen.Done {
		return returnVal, true, nil
	}
	if !gen.Started {
		gen.Done = true
		return returnVal, true, nil
	}
	if gen.PC >= len(gen.Instructions) {
		gen.Done = true
		return returnVal, true, nil
	}
	// 只有体内挂有 finally 时才需要重建帧执行收尾; 否则直接关闭。
	hasFinally := false
	for _, te := range gen.PendingTries {
		if te.FinallyPC > 0 {
			hasFinally = true
			break
		}
	}
	if !hasFinally {
		gen.Done = true
		return returnVal, true, nil
	}
	genFrame := vm.rebuildGenFrame(gen, false)
	genFrameIdx := vm.frameIdx
	if !vm.handleReturn(returnVal) {
		gen.Done = true
		vm.unwindGenFrame(genFrame, genFrameIdx)
		return returnVal, true, nil
	}
	return vm.runSuspendedGen(gen)
}

// rebuildGenFrame 重建 generator 暂停时保存的帧 (含恢复 try 处理器条目)。
//
// 帧基必须在压入 SavedStack **之前**取当前栈高: SavedStack 是挂起时
// 「帧基之上」的中间值 (如 2 + (yield 3) 中的 2), 恢复时它们仍应位于
// 帧基之上。若像早期实现那样在压入之后再取栈高 (帧基被抬到 SavedStack
// 之上), 则 SavedStack 落到帧基之下, 一旦上层表达式把残留值消费掉、栈
// 降回帧基之下, try 条目的绝对 stackBase 就会小于帧基 —— async generator
// 在求值外层表达式时恢复生成器 (操作数栈带中间值) 的场景会因此越界崩溃。
//
// reuseLocals 为真时直接复用 gen.Locals (不拷贝): 形参前导段在**调用时**
// 由独立的"前导帧"执行, 该帧里默认值表达式创建的闭包捕获的是前导帧
// Locals 的切片 (= gen.Locals)。若此处拷贝成新数组, 函数体对形参的 STORE
// 只会写进副本, 前导段闭包仍读到旧数组 —— `function* g(a, f = () => a)
// { a = 6; yield f() }` 会错读到形参初值。复用同一数组使这些闭包与函数体
// 共享绑定, 与"前导段与函数体同一帧"的语义一致。
func (vm *VM) rebuildGenFrame(gen *object.Generator, reuseLocals bool) *Frame {
	// 主单元生成器 (模块顶层 TLA, Closure==nil): 全程复用同一份 Locals
	// 数组。顶层导出绑定 (OP_EXPORT_BINDING) 的读取器持有该数组引用,
	// 恢复时若拷贝成新数组, await 之后的赋值对导出方不可见。
	if gen.Closure == nil {
		reuseLocals = true
	}
	// 帧基 = 当前栈高 (SavedStack 之前的顶端)。必须先取再压 SavedStack:
	// SavedStack 是挂起时「帧基之上」的中间值 (如 2 + (yield 3) 中的 2),
	// 恢复后仍应位于帧基之上; 若压入后再取栈高, 帧基被抬到 SavedStack 之上,
	// 这些值就落到帧基之下 —— 生成器 return/catch 时按帧基截断会把它们
	// 留给调用方 (栈残留), 且其绝对 stackBase 与 try 条目错位可致下溢。
	stackBase := vm.stack.Len()
	// 压入保存的帧栈残留值 (顺序与保存时一致), 位于帧基之上
	for _, v := range gen.SavedStack {
		vm.stack.Push(v)
	}
	locals := gen.Locals
	if !reuseLocals {
		locals = make([]object.Value, len(gen.Locals))
		copy(locals, gen.Locals)
	}
	frame := &Frame{
		Instructions: gen.Instructions,
		PC:           gen.PC,
		Locals:       locals,
		Closure:      gen.Closure,
		This:         vm.resolveFrameThis(gen.Closure),
		Constants:    &bytecode.ConstantPool{Constants: gen.Constants},
		StackBase:    stackBase,
	}
	if reuseLocals {
		// 复用同一数组 ⇒ 把它同时作为 binding cell, 使前导段闭包与
		// 函数体的 STORE 走同一份存储 (见函数头注释)。
		frame.SharedCells = gen.Locals
	}
	vm.pushFrame(frame)
	// 把 yield 时保存的 try 处理器条目按相对值换算后重新挂回
	for _, te := range gen.PendingTries {
		vm.tryStack = append(vm.tryStack, tryEntry{
			catchPC:       te.CatchPC,
			finallyPC:     te.FinallyPC,
			stackBase:     frame.StackBase + te.RelStackBase,
			frameIdx:      vm.frameIdx + te.RelFrameIdx,
			inFinally:     te.InFinally,
			pendingVal:    te.PendingVal,
			pendingReturn: te.PendingReturn,
		})
	}
	gen.PendingTries = nil
	return frame
}

// runSuspendedGen 运行已重建帧的 generator 直到下一个 yield 或结束。
//
func (vm *VM) runSuspendedGen(gen *object.Generator) (object.Value, bool, error) {
	genFrameIdx := vm.frameIdx
	frame := vm.currentFrame()
	prevGen := vm.currentGenerator
	vm.currentGenerator = gen
	err := vm.runFrom(genFrameIdx)
	vm.currentGenerator = prevGen
	return vm.finishGenRun(gen, frame, genFrameIdx, err)
}

// finishGenRun 处理 generator 一次恢复运行的收尾。
func (vm *VM) finishGenRun(gen *object.Generator, frame *Frame, genFrameIdx int, err error) (object.Value, bool, error) {
	if err != nil {
		if ysig, ok := err.(*YieldSignal); ok {
			gen.Value = ysig.value
			return ysig.value, false, nil
		}
		// 其他错误: generator 终止。
		//
		// 必须把 generator 帧弹掉。runLoop 因异常返回时该帧仍留在帧栈上
		// (只有跑到边界的正常路径才会自然 popFrame), 而外层 async 的
		// wrapper 帧正等着执行 OP_RETURN 把 __spawn 交出的 promise 返回给
		// 调用方。帧泄漏会让 wrapper 帧被压住、OP_RETURN 在错误栈基上取值
		// ⇒ async 调用返回 undefined 而不是 Promise (2026-10-02, rZn4IS)。
		gen.Done = true
		vm.unwindGenFrame(frame, genFrameIdx)
		return object.UndefinedSingleton, true, err
	}

	// 正常结束: 弹出返回值
	gen.Done = true
	var result object.Value = object.UndefinedSingleton
	if vm.stack.Len() > frame.StackBase {
		result = vm.stack.Pop()
	}
	gen.Value = result
	return result, true, nil
}

// unwindGenFrame 回收因异常终止而仍留在帧栈上的 generator 帧。
//
// 只弹到 generator 自己那一帧为止 (含), 绝不动外层帧 —— 外层的 async
// wrapper 还要继续执行 OP_RETURN。栈按该帧的 StackBase 截断, 与
// OP_RETURN 的收尾语义一致。
func (vm *VM) unwindGenFrame(frame *Frame, genFrameIdx int) {
	for vm.frameIdx > genFrameIdx {
		vm.popFrame()
	}
	if vm.frameIdx == genFrameIdx {
		vm.popFrame()
	}
	if vm.stack.Len() > frame.StackBase {
		vm.stack.Truncate(frame.StackBase)
	}
}

// addValues 实现 JavaScript 的 + 运算符 (数字加法或字符串拼接)。
// maxStringLength 是 VM 侧字符串长度上限 (与 stdlib.maxStringLength 一致，
// 1<<30 字节)。字符串拼接无上限时，`s = s + s` 翻倍可在数秒内请求到
// TB 级分配，Go 的 OOM 是不可 recover 的致命错误 —— 必须在拼接前拦截。
const maxStringLength = 1 << 30

// concatStrings 带上限的字符串拼接。超限时返回 RangeError (Invalid string
// length)，与 String.prototype.repeat/padStart 的既有行为一致。
func concatStrings(a, b string) (string, error) {
	if len(a)+len(b) > maxStringLength {
		return "", &jsThrow{Name: "RangeError", Message: "Invalid string length"}
	}
	return a + b, nil
}

func (vm *VM) addValues(a, b object.Value) (object.Value, error) {
	// 字符串拼接
	if aStr, ok := a.(*object.String); ok {
		s, err := concatStrings(aStr.Value, toJSString(b))
		if err != nil {
			return nil, err
		}
		return object.NewString(s), nil
	}
	if bStr, ok := b.(*object.String); ok {
		s, err := concatStrings(toJSString(a), bStr.Value)
		if err != nil {
			return nil, err
		}
		return object.NewString(s), nil
	}
	// 数组拼接 (简化)
	if aArr, ok := a.(*object.Array); ok {
		if bArr, ok := b.(*object.Array); ok {
			elements := make([]object.Value, 0, len(aArr.Elements)+len(bArr.Elements))
			elements = append(elements, aArr.Elements...)
			elements = append(elements, bArr.Elements...)
			return object.NewArray(elements), nil
		}
	}
	// BigInt 加法。
	// 字符串拼接已在上面处理 (BigInt 与 String 相加时走 ToString 得到 "1")，
	// 因此这里只需区分 BigInt+BigInt 与 BigInt+非 BigInt 两种情况。
	aBig, aIsBig := a.(*object.BigInt)
	bBig, bIsBig := b.(*object.BigInt)
	if aIsBig || bIsBig {
		if aIsBig && bIsBig {
			return aBig.Add(bBig), nil
		}
		return nil, errMixBigInt
	}

	// 数字加法
	aNum := toNumber(a)
	bNum := toNumber(b)
	return object.NewNumber(aNum + bNum), nil
}

// getIndex 实现索引访问 obj[index]。
func (vm *VM) getIndex(obj, index object.Value) object.Value {
	// 键归一 (ToPropertyKey): 编译器对「键只用一次」的场合 (简单赋值 /
	// for-of 目标 / 解构目标) 不再提前发 OP_TO_PROPERTY_KEY, 由这里补 ——
	// 这样 ToPropertyKey 落在右值求值之后, 求值顺序合规。对已被
	// OP_TO_PROPERTY_KEY 转过一次的键是幂等的 (原语原样返回), 不会二次
	// 触发用户 toString。对象键转换会调用户代码, 故需消费回调桥异常。
	index = vm.normalizeIndexKey(index)
	switch o := obj.(type) {
	case *object.Array:
		// 已通过 Object.defineProperty 定义过描述符 (索引访问器 / 不可写)
		// 的数组, 索引读必须走描述符通道 (GetProperty 调 getter), 与自有
		// 属性接口保持一致。PropDescs 为空 (绝大多数数组) 时走下方的快速
		// 路径, 行为与此前完全一致。
		if len(o.PropDescs) > 0 {
			val, found := o.GetProperty(toJSString(index))
			if !found || val == nil {
				return object.UndefinedSingleton
			}
			return val
		}
		if n, ok := index.(*object.Number); ok {
			idx := int(n.Value)
			if idx >= 0 && idx < len(o.Elements) {
				if o.Elements[idx] == nil {
					return object.UndefinedSingleton
				}
				return o.Elements[idx]
			}
		}
		// 字符串索引
		if s, ok := index.(*object.String); ok {
			val, _ := o.GetProperty(s.Value)
			return val
		}
		return object.UndefinedSingleton

	case *object.String:
		if n, ok := index.(*object.Number); ok {
			idx := int(n.Value)
			if idx >= 0 && idx < len(o.Value) {
				return object.NewString(string(o.Value[idx]))
			}
			return object.UndefinedSingleton
		}
		// 字符串键: "0"/"1" 这类索引先按码元取字符, 其余 (length /
		// 原型方法, 如 s["length"] === 5) 走属性查找 —— 必须与 s.length
		// 同口径, 否则 `let {length} = str` 解构拿到 undefined。
		if s, ok := index.(*object.String); ok {
			if idx, err := strconv.Atoi(s.Value); err == nil && idx >= 0 {
				if idx < len(o.Value) {
					return object.NewString(string(o.Value[idx]))
				}
				return object.UndefinedSingleton
			}
			if val, found := o.GetProperty(s.Value); found {
				return val
			}
			return object.UndefinedSingleton
		}
		return object.UndefinedSingleton

	case *object.TypedArray:
		if n, ok := index.(*object.Number); ok {
			idx := int(n.Value)
			if idx >= 0 && idx < o.Length {
				return o.GetElement(idx)
			}
			return object.UndefinedSingleton
		}
		if s, ok := index.(*object.String); ok {
			val, _ := o.GetProperty(s.Value)
			return val
		}
		return object.UndefinedSingleton

	case object.RxIndexed:
		if n, ok := index.(*object.Number); ok {
			return o.GetIndexedElement(int(n.Value))
		}
		if s, ok := index.(*object.String); ok {
			val, _ := o.(object.Value).GetProperty(s.Value)
			return val
		}
		return object.UndefinedSingleton

	case *object.Object:
		if s, ok := index.(*object.String); ok {
			val, found := o.GetProperty(s.Value)
			if !found {
				return object.UndefinedSingleton
			}
			return val
		}
		// Symbol 键: 检查自身及原型链上的 Symbol 属性。访问器 (getter)
		// 必须展开调用 (this = 原接收者 o) —— 如 `get [Symbol.iterator](){}`。
		// getter 抛错经回调桥记录, 由 GET_INDEX 之后的 checkCallbackErr 消费。
		if sym, ok := index.(*object.Symbol); ok {
			if desc, found := object.LookupSymbolPropertyDescriptor(o, sym); found {
				if acc, isAcc := desc.Value.(*object.Accessor); isAcc {
					if acc.Getter != nil && object.IsCallable(acc.Getter) {
						v := object.CallFunction(acc.Getter, o)
						if v == nil {
							v = object.UndefinedSingleton
						}
						return v
					}
					return object.UndefinedSingleton
				}
				if desc.Value == nil {
					return object.UndefinedSingleton
				}
				return desc.Value
			}
			return object.UndefinedSingleton
		}
		// 数字等其他键型按 ToPropertyKey 语义转字符串 (o[2] === o["2"])
		val, found := o.GetProperty(toJSString(index))
		if !found {
			return object.UndefinedSingleton
		}
		return val

	default:
		// 其余类型 (GlobalObject / Map / Set / Closure / Error / RegExp / Promise ...)
		// 的字符串键读取统一走 Value 接口 —— 与 setIndex 的 default 兜底分支对称。
		//
		// 必须守住的不变量: obj["k"] 恒等于 obj.k (二者只是同一属性访问的两种写法)。
		// 修复前这些类型会直接落到本函数末尾返回 undefined, 于是:
		//   globalThis.z      -> 42          globalThis["z"]     -> undefined  (错)
		//   map.size          -> 1           map["size"]         -> undefined  (错)
		// 更要命的是 ++ / -- / += 作用在成员表达式上时, compileIncDec 走的是
		// GET_INDEX 路径 (见 compiler.compileMemberRef + OP_DUP2/OP_GET_INDEX),
		// 所以 `globalThis.n++` 读到 undefined, ToNumber 后算出 NaN —— 而完全同形的
		// `obj.n++` (普通对象) 一切正常, 极难排查。
		if s, ok := index.(*object.String); ok {
			val, found := obj.GetProperty(s.Value)
			if !found {
				return object.UndefinedSingleton
			}
			return val
		}
		// Symbol 键: 实现了 GetSymbolProperty 的类型 (如 AsyncGenerator 的
		// [Symbol.asyncIterator]) 在此响应; 其余维持原有行为返回 undefined。
		if sym, ok := index.(*object.Symbol); ok {
			if sp, ok := obj.(interface {
				GetSymbolProperty(*object.Symbol) (object.Value, bool)
			}); ok {
				if val, found := sp.GetSymbolProperty(sym); found {
					return val
				}
			}
			return object.UndefinedSingleton
		}
		// 数字等键型按 ToPropertyKey 语义转字符串 (o[2] === o["2"])
		val, found := obj.GetProperty(toJSString(index))
		if !found {
			return object.UndefinedSingleton
		}
		return val
	}
}

// defineClosureAccessor 在函数对象 (class 构造器) 上定义静态访问器:
// 存入 Closure.Props 的 *object.Accessor, 由 Closure.Get/SetProperty 解释。
// obj 非 *object.Closure 时静默忽略 (与 SET_GETTER 对未知类型的行为一致)。
func (vm *VM) defineClosureAccessor(obj object.Value, propName string, fn object.Value, isGetter bool) {
	c, ok := obj.(*object.Closure)
	if !ok {
		return
	}
	var acc *object.Accessor
	if cur, exists := c.Props[propName]; exists {
		if a, isAcc := cur.(*object.Accessor); isAcc {
			acc = a
		}
	}
	if acc == nil {
		acc = &object.Accessor{}
		c.SetProperty(propName, acc)
	}
	if isGetter {
		acc.Getter = fn
	} else {
		acc.Setter = fn
	}
}

// resolveSymbolIterator 预解析实现了 [Symbol.iterator] 的普通对象:
// 在 VM 层调用该方法并适配返回值。返回 (迭代器, true, nil) 表示已解析;
// (nil, false, nil) 表示没有该方法 (交回 GetIterable 的具体类型分发)。
// 生成器方法的返回值是 *object.Generator, 只有 VM 能驱动其字节码帧,
// 因此这一步必须在 VM 层做而不能依赖 runtime.GetIterable。
// makeIterStep 把 (value, done) 包成 next() 结果的形状 {value, done}。
func makeIterStep(val object.Value, done bool) *object.Object {
	if val == nil {
		val = object.UndefinedSingleton
	}
	step := object.NewObject()
	step.SetProperty("value", val)
	step.SetProperty("done", object.NewBoolean(done))
	return step
}

// syncIterStep 同步驱动迭代器一步, 返回 {value, done} 步进对象 (数组解构用)。
// 覆盖三种形状: 生成器 (VM 帧驱动)、runtime.Iterator (数组/字符串/Map/Set
// 等的 Go 适配器)、用户自建 { next } 对象 (调其 next())。
// 返回的 error 交给 rethrowBridgeError / throwJSError 走抛出流程。
func (vm *VM) syncIterStep(iter object.Value) (object.Value, error) {
	switch it := iter.(type) {
	case *object.Generator:
		val, done, err := vm.genResume(it, object.UndefinedSingleton)
		if err != nil {
			return nil, err
		}
		return makeIterStep(val, done), nil
	case *runtime.Iterator:
		val, done := it.Next()
		return makeIterStep(val, done), nil
	case *object.Object:
		nextFn, found := it.GetProperty("next")
		if !found || !object.IsCallable(nextFn) {
			return nil, &jsThrow{Name: "TypeError", Message: "iterator has no callable next()"}
		}
		res, err := vm.callFunction(nextFn, it, nil)
		if err != nil {
			return nil, err
		}
		if err := vm.checkCallbackErr(); err != nil {
			return nil, err
		}
		// 规范 7.4.2 step 2: next() 的返回值必须是 Object, 否则 TypeError。
		if res == nil || !object.IsObjectLike(res) {
			return nil, &jsThrow{Name: "TypeError", Message: "iterator next() must return an object"}
		}
		return res, nil
	}
	return nil, &jsThrow{Name: "TypeError", Message: "value is not an iterator"}
}

// iteratorClose 实现规范 7.4.6 IteratorClose: 取迭代器的 return 方法,
// null/undefined 直接跳过; 非可调用 → TypeError; 否则以迭代器为 this 调用,
// 丢弃返回值。生成器没有暴露 return 属性 (object.Generator.GetProperty 只
// 认 next), 这里直接用内建 return 语义注入 return 完成并展开 finally。
// 返回的 error 交给调用点决定 rethrow (异常是否被 try 吞掉)。
func (vm *VM) iteratorClose(iter object.Value) error {
	if iter == nil || isNullish(iter) {
		return nil
	}
	switch it := iter.(type) {
	case *object.Generator:
		if _, _, err := vm.genReturn(it, object.UndefinedSingleton); err != nil {
			return err
		}
		return vm.checkCallbackErr()
	case *object.Object:
		ret, found := it.GetProperty("return")
		if !found || ret == nil || isNullish(ret) {
			return nil
		}
		if !object.IsCallable(ret) {
			return &jsThrow{Name: "TypeError", Message: "iterator return method is not callable"}
		}
		res, err := vm.callFunction(ret, it, nil)
		if err != nil {
			return err
		}
		if err := vm.checkCallbackErr(); err != nil {
			return err
		}
		// 规范 7.4.6 step 9: return() 的返回值必须是 Object, 否则 TypeError
		// (return null / undefined / 数字都算; 数组/函数等对象类通过)。
		if res == nil || !object.IsObjectLike(res) {
			return &jsThrow{Name: "TypeError", Message: "iterator return method must return an object"}
		}
		return nil
	}
	// runtime.Iterator / Array / String 等其它形状没有 return 方法: 跳过。
	return nil
}

// ===== explicit resource management (using / await using) =====

// disposeAdd 实现 AddDisposableResource + CreateDisposableResource
// (sync-dispose hint) 的登记步骤:
//   - v 为 null/undefined: 跳过 (规范 step 1a; async 下等价无操作);
//   - v 非对象: TypeError;
//   - 取释放方法 (sync: @@dispose; async: @@asyncDispose, 缺失回退 @@dispose);
//     方法为 null/undefined 或非可调用: TypeError;
//   - 把值与方法写入两个隐藏局部槽 (方法 getter 只在此时读取一次)。
//
// 返回非 nil 表示要抛出的异常 (交给 rethrowBridgeError 走抛出流程)。
func (vm *VM) disposeAdd(res *bytecode.DisposeResource, v object.Value) error {
	if res == nil {
		return nil
	}
	if isNullish(v) {
		return nil
	}
	if !object.IsObjectLike(v) {
		return &jsThrow{Name: "TypeError", Message: "using value must be an object"}
	}
	method, err := vm.getDisposeMethod(v, res.Async)
	if err != nil {
		return err
	}
	if isNullish(method) {
		return &jsThrow{Name: "TypeError", Message: "using value has no dispose method"}
	}
	if !object.IsCallable(method) {
		return &jsThrow{Name: "TypeError", Message: "using value's dispose method is not callable"}
	}
	vm.setLocalSlot(res.ValueSlot, v)
	vm.setLocalSlot(res.MethodSlot, method)
	return nil
}

// getDisposeMethod 取释放方法: sync 取 @@dispose; async 先取 @@asyncDispose,
// 为 null/undefined 时回退 @@dispose (规范 GetDisposeMethod)。属性读取会触发
// getter, 且只发生一次。
func (vm *VM) getDisposeMethod(v object.Value, async bool) (object.Value, error) {
	if async {
		m, err := vm.getSymbolMember(v, object.SymbolAsyncDispose())
		if err != nil {
			return nil, err
		}
		if !isNullish(m) {
			return m, nil
		}
	}
	return vm.getSymbolMember(v, object.SymbolDispose())
}

// getSymbolMember 以 Symbol 为键读取属性, 展开访问器 getter (this = 原接收者)。
// object.LookupSymbolProperty 只返回描述符里的原始值 (不展开 getter), 而
// `{ get [Symbol.dispose]() {...} }` 必须调用 getter, 故这里自带展开。
func (vm *VM) getSymbolMember(obj object.Value, sym *object.Symbol) (object.Value, error) {
	if sym == nil {
		return object.UndefinedSingleton, nil
	}
	if o, ok := obj.(*object.Object); ok {
		for cur := o; cur != nil; {
			if desc, found := cur.GetSymbolPropertyDescriptor(sym); found {
				if acc, isAcc := desc.Value.(*object.Accessor); isAcc {
					if acc.Getter != nil && object.IsCallable(acc.Getter) {
						res, err := vm.callFunction(acc.Getter, o, nil)
						if err != nil {
							return nil, err
						}
						if err := vm.checkCallbackErr(); err != nil {
							return nil, err
						}
						if res == nil {
							res = object.UndefinedSingleton
						}
						return res, nil
					}
					return object.UndefinedSingleton, nil
				}
				if desc.Value == nil {
					return object.UndefinedSingleton, nil
				}
				return desc.Value, nil
			}
			next, ok := cur.Proto.(*object.Object)
			if !ok {
				break
			}
			cur = next
		}
		return object.UndefinedSingleton, nil
	}
	if sp, ok := obj.(interface {
		GetSymbolProperty(*object.Symbol) (object.Value, bool)
	}); ok {
		if val, found := sp.GetSymbolProperty(sym); found {
			if val == nil {
				return object.UndefinedSingleton, nil
			}
			return val, nil
		}
	}
	return object.UndefinedSingleton, nil
}

// setLocalSlot 把值写入当前帧的局部槽, 必要时扩展 Locals (编译器已保证
// 该槽在 NumLocals 内, 这里是防御性兜底)。
func (vm *VM) setLocalSlot(slot int, v object.Value) {
	if slot < 0 {
		return
	}
	f := vm.currentFrame()
	for len(f.Locals) <= slot {
		f.Locals = append(f.Locals, object.UndefinedSingleton)
	}
	if v == nil {
		v = object.UndefinedSingleton
	}
	f.Locals[slot] = v
}

// getLocalSlot 读取当前帧的局部槽 (越界返回 undefined)。
func (vm *VM) getLocalSlot(slot int) object.Value {
	f := vm.currentFrame()
	if slot < 0 || slot >= len(f.Locals) || f.Locals[slot] == nil {
		return object.UndefinedSingleton
	}
	return f.Locals[slot]
}

// disposeExit 实现 DisposeResources (规范): 逆序释放 scope 里的全部资源,
// 每个资源以 ResourceValue 为 this 调用其方法。
//
// 错误合成口径 (与本帧挂起异常的关系):
//   - 若释放方法抛错, 且当前帧正**带着挂起异常**跑 finally (tryStack 栈顶
//     是本帧的 inFinally 条目), 则把新错误与原挂起异常合成 SuppressedError
//     (error = 新错误, suppressed = 原挂起异常), 并写回条目 pendingVal ——
//     随后 OP_END_FINALLY 会重抛它;
//   - 若无挂起异常, 释放方法抛错直接向外传播 (替换正常/return 完成)。
//
// 返回值非 nil 表示需要抛出的异常 (仅无挂起异常的情况)。
func (vm *VM) disposeExit(scope *bytecode.DisposeScope) error {
	if scope == nil || len(scope.Resources) == 0 {
		return nil
	}
	// 判断本帧是否正在跑一个带挂起异常的 finally (从栈顶往下找本帧的 inFinally
	// 条目)。是则以它的 pendingVal 作为「已有完成」, 释放错误与之合成。
	// 只记下标不取指针: 释放方法调用可能触发嵌套调用, 让 tryStack 底层数组
	// 重新分配, 指针会失效。
	pendingIdx := -1
	var pendingErr object.Value
	for i := len(vm.tryStack) - 1; i >= 0; i-- {
		top := vm.tryStack[i]
		if top.inFinally && top.frameIdx == vm.frameIdx {
			pendingIdx = i
			pendingErr = top.pendingVal
			break
		}
	}
	hadPending := pendingIdx >= 0

	for i := len(scope.Resources) - 1; i >= 0; i-- {
		res := scope.Resources[i]
		method := vm.getLocalSlot(res.MethodSlot)
		if isNullish(method) {
			continue // 未登记 (初始化为 null/undefined) 或已释放
		}
		value := vm.getLocalSlot(res.ValueSlot)
		_, err := vm.callFunction(method, value, nil)
		if err == nil {
			if cerr := vm.checkCallbackErr(); cerr != nil {
				err = cerr
			}
		}
		if err == nil {
			continue
		}
		// 释放抛错: 取出 JS 抛出值 (非 JS 级异常原样上抛)。
		var thrown object.Value
		var te *ThrowError
		if errors.As(err, &te) {
			thrown = te.Value
		} else if vr := vm.throwJSErrorValue(err); vr != nil {
			thrown = vr
		} else {
			return err
		}
		if hadPending {
			pendingErr = object.NewSuppressedError(thrown, pendingErr)
		} else {
			pendingErr = thrown
		}
	}

	if !hadPending {
		if pendingErr != nil {
			return &ThrowError{Value: pendingErr}
		}
		return nil
	}
	// 把 (可能合成过的) 错误写回挂起条目, 交给 OP_END_FINALLY 重抛。
	if pendingIdx < len(vm.tryStack) {
		vm.tryStack[pendingIdx].pendingVal = pendingErr
		return nil
	}
	// 条目已不在 (极端情形): 直接抛出。
	if pendingErr != nil {
		return &ThrowError{Value: pendingErr}
	}
	return nil
}

// throwJSErrorValue 把非 *ThrowError 的 Go error (如 *jsThrow) 转成 JS 异常值。
// 不是 JS 语言级异常时返回 nil。
func (vm *VM) throwJSErrorValue(err error) object.Value {
	var jt *jsThrow
	if errors.As(err, &jt) {
		return object.NewErrorWithName(jt.Name, jt.Message)
	}
	return nil
}


// 语义 (规范 7.3.25): 新建对象, 复制 source 的自有可枚举属性 (字符串键 + Symbol
// 键, 按 OrdinaryOwnPropertyKeys 顺序, 触发 getter), 跳过 excluded 中的键。
// 非对象源 (Number/Boolean/Symbol/BigInt) 得空对象; 字符串按 UTF-16 索引字符复制。
func (vm *VM) objectRest(src, excluded object.Value) (object.Value, error) {
	rest := object.NewObject()
	excludedStr := map[string]bool{}
	excludedSym := map[uint64]bool{}
	if arr, ok := excluded.(*object.Array); ok {
		for _, k := range arr.Elements {
			if sym, isSym := k.(*object.Symbol); isSym {
				excludedSym[sym.ID] = true
			} else {
				excludedStr[object.ToString(k)] = true
			}
		}
	}
	switch s := src.(type) {
	case *object.Object:
		for _, k := range s.EnumerableKeys() {
			if excludedStr[k] {
				continue
			}
			v, _ := s.GetProperty(k) // 触发 getter (访问器 this = s)
			if err := vm.checkCallbackErr(); err != nil {
				return nil, err
			}
			// CreateDataProperty 语义: 绕过 __proto__ setter 等原型链副作用,
			// 直接建自有可枚举数据属性 (源的自有 "__proto__" 要原样复制)。
			rest.DefineOwnProperty(k, object.DataProperty(v))
		}
		for _, sym := range s.SymbolKeys() {
			desc, ok := s.SymbolProperties[sym.ID]
			if !ok || !desc.Enumerable || excludedSym[sym.ID] {
				continue
			}
			var v object.Value = desc.Value
			if acc, isAcc := desc.Value.(*object.Accessor); isAcc {
				if acc.Getter != nil && object.IsCallable(acc.Getter) {
					var err error
					v, err = vm.callFunction(acc.Getter, s, nil)
					if err != nil {
						return nil, err
					}
					if err := vm.checkCallbackErr(); err != nil {
						return nil, err
					}
				} else {
					v = object.UndefinedSingleton
				}
			}
			if v == nil {
				v = object.UndefinedSingleton
			}
			rest.SetSymbolProperty(sym, v)
		}
	case *object.Array:
		for i, v := range s.Elements {
			k := strconv.Itoa(i)
			if excludedStr[k] {
				continue
			}
			if v == nil {
				v = object.UndefinedSingleton
			}
			rest.DefineOwnProperty(k, object.DataProperty(v))
		}
	case *object.String:
		for i, ch := range object.SplitCharsUTF16(s.Value) {
			k := strconv.Itoa(i)
			if excludedStr[k] {
				continue
			}
			rest.DefineOwnProperty(k, object.DataProperty(object.NewString(ch)))
		}
	}
	return rest, nil
}

// hasNextKey 沿原型链检查对象的 "next" 键是否存在, 不触发 getter。
// Gox 的非规范扩展: 「裸 next 对象」本身可作迭代器 (for-await/for-of 的
// 自建迭代器形状)。用描述符查找而非 GetProperty: 提前访问 next getter
// 会多产生一次属性访问, 破坏 yield* 委托的属性访问顺序
// (r6e5qp, test262 yield-star-*-next 系列记的就是这个序)。
func hasNextKey(o *object.Object) bool {
	for cur := o; cur != nil; {
		if _, found := cur.Properties["next"]; found {
			return true
		}
		next, ok := cur.Proto.(*object.Object)
		if !ok || next == nil {
			return false
		}
		cur = next
	}
	return false
}

func (vm *VM) resolveSymbolIterator(val object.Value) (object.Value, bool, error) {	o, ok := val.(*object.Object)
	if !ok {
		return nil, false, nil
	}
	sym := object.GetGlobalSymbol("Symbol.iterator")
	if sym == nil {
		return nil, false, nil
	}
	fn, err := vm.getSymbolMember(o, sym)
	if err != nil {
		return nil, false, err
	}
	if !object.IsCallable(fn) {
		return nil, false, nil
	}
	res, err := vm.callFunction(fn, o, nil)
	if err != nil {
		return nil, false, err
	}
	switch r := res.(type) {
	case *object.Generator:
		return r, true, nil
	case *object.JSIterator:
		return runtime.NewCallbackIterator(r.Next), true, nil
	case *object.Object:
		// 用户自建迭代器对象 ({ next, return }): 只要 next 键存在就认。
		// 由 OP_ITER_STEP / OP_ITER_NEXT 的对象分支驱动 (调 next())。
		// 用 hasNextKey (非触发式) 判定, 不能再 GetProperty——提前访问
		// next getter 会多一次属性访问 (r6e5qp 属性访问顺序)。
		if hasNextKey(r) {
			return r, true, nil
		}
	}
	return nil, false, nil
}

// resolveAsyncSymbolIterator 预解析实现了 [Symbol.asyncIterator] 的对象:
// 在 VM 层调用该方法并适配返回值 (与 resolveSymbolIterator 同构, 只是
// 查的 Symbol 键不同)。返回 (迭代器, true, nil) 表示已解析;
// (nil, false, nil) 表示没有该方法 (交回同步可迭代形状分发)。
//
// 规范 GetMethod/GetIterator(hint=async): @@asyncIterator 一旦存在, 就
// 必须按其值处理 —— 值为 nullish 才回退同步可迭代; 值不可调用, 或调用
// 结果不是对象, 一律 TypeError (不得再退回 [Symbol.iterator]，否则
// Symbol.iterator 的 getter 会被意外触发)。
func (vm *VM) resolveAsyncSymbolIterator(val object.Value) (object.Value, bool, error) {
	o, ok := val.(*object.Object)
	if !ok {
		return nil, false, nil
	}
	sym := object.GetGlobalSymbol("Symbol.asyncIterator")
	if sym == nil {
		return nil, false, nil
	}
	if _, present := object.LookupSymbolPropertyDescriptor(o, sym); !present {
		return nil, false, nil // 未实现 @@asyncIterator: 交回同步可迭代分发
	}
	fn, err := vm.getSymbolMember(o, sym)
	if err != nil {
		return nil, false, err
	}
	if fn == object.UndefinedSingleton || fn == object.NullSingleton {
		return nil, false, nil // nullish: 回退同步可迭代 (Gox for-await 设计)
	}
	if !object.IsCallable(fn) {
		return nil, false, vm.throwNamedError("TypeError",
			"%s[Symbol.asyncIterator] is not a function", o.Inspect())
	}
	res, err := vm.callFunction(fn, o, nil)
	if err != nil {
		return nil, false, err
	}
	switch r := res.(type) {
	case *object.Generator:
		return r, true, nil
	case *object.JSIterator:
		return runtime.NewCallbackIterator(r.Next), true, nil
	}
	// Object 即迭代器 (async 迭代器的常见实现): 包 Async-from-Sync 语义
	// wrapper (own next 懒缓存 —— V8 实测原生 @@asyncIterator 的 next
	// method 也是首取后缓存; throw/return 每次 GetMethod, 见
	// wrapSyncIterForAsync)。用 hasNextKey (非触发式) 判定: GetProperty
	// 会多触发一次 getter, 破坏 yield* 异步委托的属性访问顺序
	// (r6e5qp, test262 yield-star-async-next 等)。
	if obj, isObj := res.(*object.Object); isObj && hasNextKey(obj) {
		return vm.wrapSyncIterForAsync(obj), true, nil
	}
	// 调用结果不是对象: GetIterator 要求抛 TypeError。
	return nil, false, vm.throwNamedError("TypeError",
		"%s is not an object (async iterator result)", res.Inspect())
}

// lookupSymbolProperty 已迁至 object.LookupSymbolProperty (原型链 Symbol 键查找)。

// setIndex 实现索引赋值 obj[index] = val。
func (vm *VM) setIndex(obj, index, val object.Value) {
	// 键归一 (ToPropertyKey): 见 getIndex 同款说明。简单赋值 base[prop] = v
	// 的键转换在此发生 ⇒ 晚于右值求值 (assignment/
	// target-member-computed-reference.js)。复合赋值等已在编译期转过一次,
	// 这里是幂等的。
	index = vm.normalizeIndexKey(index)
	switch o := obj.(type) {
	case *object.Array:
		// 已通过 Object.defineProperty 定义过描述符的数组, 索引写走
		// SetProperty 的描述符语义 (访问器调 setter / 不可写静默失败)。
		// PropDescs 为空时走下方快速路径, 行为与此前完全一致。
		if len(o.PropDescs) > 0 {
			o.SetProperty(toJSString(index), val)
			return
		}
		if n, ok := index.(*object.Number); ok {
			idx := int(n.Value)
			if idx >= 0 {
				for len(o.Elements) <= idx {
					o.Elements = append(o.Elements, object.UndefinedSingleton)
				}
				o.Elements[idx] = val
				return
			}
		}
		// 非数字索引 (如 arr.foo = 1) 走通用属性设置
		if s, ok := index.(*object.String); ok {
			o.SetProperty(s.Value, val)
		}
	case *object.TypedArray:
		if n, ok := index.(*object.Number); ok {
			idx := int(n.Value)
			if idx >= 0 {
				// 越界写按规范静默忽略 (setElement 内部处理)
				o.SetElement(idx, val)
			}
			return
		}
		if s, ok := index.(*object.String); ok {
			o.SetProperty(s.Value, val)
		}
	case object.RxIndexed:
		if n, ok := index.(*object.Number); ok {
			o.SetIndexedElement(int(n.Value), val)
		}
	case *object.Object:
		if s, ok := index.(*object.String); ok {
			o.SetProperty(s.Value, val)
		} else if sym, ok := index.(*object.Symbol); ok {
			o.SetSymbolProperty(sym, val)
		} else {
			// 数字等其他键型按 ToPropertyKey 语义转字符串 (o[2] === o["2"])
			o.SetProperty(toJSString(index), val)
		}
	default:
		// 其余类型 (Error/RegExp/Closure/Promise 等) 的字符串键赋值
		// 统一走 Value 接口。基础类型 (String/Number/null/undefined) 的
		// SetProperty 是无操作，与 JS 原始值语义一致。
		if s, ok := index.(*object.String); ok {
			obj.SetProperty(s.Value, val)
		} else if sym, ok := index.(*object.Symbol); ok {
			if sp, ok := obj.(interface {
				SetSymbolProperty(*object.Symbol, object.Value)
			}); ok {
				sp.SetSymbolProperty(sym, val)
			}
		}
	}
}

// normalizeIndexKey 把计算成员键的原始值按 ToPropertyKey 归一。
//
// 幂等: 已是原语 (String/Symbol/Number/BigInt/Boolean/null/undefined) 的键
// 原样返回 —— 数字键保留给数组/类型化数组的数字索引快路径, 符号键保留给
// 符号属性通道。仅对象键 (可能带用户 toString/valueOf) 会在这里转成字符串,
// 因此**同一引用的两次索引操作只触发一次用户代码**。
//
// 转换可能调用用户 toString ⇒ 经回调桥抛出: 这里只做转换, 异常由
// 调用方 (OP_GET_INDEX / OP_SET_INDEX) 紧跟的 checkCallbackErr 消费;
// 非 opcode 调用点 (如内建直接调 getIndex) 由 object 层的回调错误机制兜底。
func (vm *VM) normalizeIndexKey(index object.Value) object.Value {
	switch index.Type() {
	case object.STRING_OBJ, object.SYMBOL_OBJ,
		object.NUMBER_OBJ, object.BOOLEAN_OBJ, object.BIGINT_OBJ,
		object.NULL_OBJ, object.UNDEFINED_OBJ:
		return index
	}
	return toPropertyKey(index)
}

// ===== 类型转换辅助函数 =====

// toNumber 将任意值转换为 float64。
func toNumber(v object.Value) float64 {
	switch val := v.(type) {
	case *object.Number:
		return val.Value
	case *object.Boolean:
		if val.Value {
			return 1
		}
		return 0
	case *object.Null:
		return 0
	case *object.Undefined:
		return math.NaN()
	case *object.String:
		return object.ParseJSNumber(val.Value)
	default:
		return math.NaN()
	}
}

// toJSString 将任意值转换为 JavaScript 字符串表示。
// 使用 ECMAScript ToString 语义 (数组 join、对象 [object Object]、
// 自定义 toString 优先)，见 object.ToString。
func toJSString(v object.Value) string {
	return object.ToString(v)
}

// toPropertyKey 实现规范 ToPropertyKey 的「求值一次」语义, 供 OP_TO_PROPERTY_KEY 使用。
//
// 已经是原语的键原样返回: 原语到属性键的转换是纯函数、重复调用不可观测,
// 而保留数字/符号键还能让数组 / 类型化数组的数字索引快路径原样工作。
// 只有对象键 (可带用户 toString/valueOf) 需要在此转成字符串一次, 之后
// GET_INDEX 与 SET_INDEX 共用它, 不再二次触发用户代码。
func toPropertyKey(v object.Value) object.Value {
	switch v.Type() {
	case object.STRING_OBJ, object.SYMBOL_OBJ,
		object.NUMBER_OBJ, object.BOOLEAN_OBJ, object.BIGINT_OBJ,
		object.NULL_OBJ, object.UNDEFINED_OBJ:
		return v
	}
	return object.NewString(object.ToString(v))
}

// looseEquals 实现 JavaScript 的 == (宽松相等)。
func looseEquals(a, b object.Value) bool {
	// 同类型直接比较
	if a.Type() == b.Type() {
		return strictEquals(a, b)
	}
	// null == undefined
	if isNullish(a) && isNullish(b) {
		return true
	}
	// BigInt == BigInt/Number/String (数学值比较)
	if a.Type() == object.BIGINT_OBJ || b.Type() == object.BIGINT_OBJ {
		return bigIntLooseEqual(a, b)
	}
	// number == string
	if a.Type() == object.NUMBER_OBJ && b.Type() == object.STRING_OBJ {
		return toNumber(a) == toNumber(b)
	}
	if a.Type() == object.STRING_OBJ && b.Type() == object.NUMBER_OBJ {
		return toNumber(a) == toNumber(b)
	}
	// boolean == other
	if a.Type() == object.BOOLEAN_OBJ {
		return toNumber(a) == toNumber(b)
	}
	if b.Type() == object.BOOLEAN_OBJ {
		return toNumber(a) == toNumber(b)
	}
	return false
}

// strictEquals 实现 JavaScript 的 === (严格相等)。
func strictEquals(a, b object.Value) bool {
	if a.Type() != b.Type() {
		return false
	}
	switch av := a.(type) {
	case *object.Number:
		if bv, ok := b.(*object.Number); ok {
			return av.Value == bv.Value
		}
	case *object.String:
		if bv, ok := b.(*object.String); ok {
			return av.Value == bv.Value
		}
	case *object.Boolean:
		if bv, ok := b.(*object.Boolean); ok {
			return av.Value == bv.Value
		}
	case *object.Null:
		_, ok := b.(*object.Null)
		return ok
	case *object.Undefined:
		_, ok := b.(*object.Undefined)
		return ok
	case *object.Symbol:
		if bv, ok := b.(*object.Symbol); ok {
			return av.ID == bv.ID
		}
	case *object.BigInt:
		if bv, ok := b.(*object.BigInt); ok {
			return bigIntStrictEqual(av, bv)
		}
	}
	// 引用类型 (对象/数组/函数/Map/Set/RegExp/...): 按引用同一性比较。
	// ECMAScript 的 IsStrictlyEqual 对 Object 类型即"是否同一个引用"，
	// 缺少这一分支时连 `o === o` 都会得到 false。
	// 所有 Value 实现都是指针类型，接口比较即指针比较。
	return a == b
}

// isNullish 检查值是否为 null 或 undefined。
func isNullish(v object.Value) bool {
	_, isNull := v.(*object.Null)
	_, isUndef := v.(*object.Undefined)
	return isNull || isUndef
}

// ===== 便捷函数 =====

// Eval 编译并执行 JS 源码，返回最后一个表达式的值。
func Eval(input string) (object.Value, error) {
	vm, err := EvalVM(input)
	if err != nil {
		return nil, err
	}
	return vm.LastPopped(), nil
}

// EvalVM 编译并执行 JS 源码，返回 VM 实例 (用于访问定时器等运行时状态)。
func EvalVM(input string) (*VM, error) {
	c, err := compileSource(input, false)
	if err != nil {
		return nil, evalEntryError(err)
	}

	vm := NewWithGlobals(c.Bytes(), c.Constants(), c.NumLocals(), stdlib.SetupGlobals())
	if err := vm.RunCompiled(c); err != nil {
		return nil, vm.uncaughtError(err)
	}
	return vm, nil
}

// EvalWithGlobals 使用预设全局变量编译并执行 JS 源码。
func EvalWithGlobals(input string, globals *runtime.Environment) (object.Value, error) {
	c, err := compileSource(input, false)
	if err != nil {
		return nil, evalEntryError(err)
	}

	vm := NewWithGlobals(c.Bytes(), c.Constants(), c.NumLocals(), globals)
	if err := vm.RunCompiled(c); err != nil {
		return nil, vm.uncaughtError(err)
	}

	return vm.LastPopped(), nil
}

// EvalFile 读取并执行 JS 脚本文件。
// 与 Eval 的区别: 会以文件所在目录作为模块基准路径, 使入口脚本中的相对
// import/export (如 `import x from "./mod.js"`) 能正确解析。
func EvalFile(path string) (object.Value, error) {
	vm, err := EvalFileVM(path)
	if err != nil {
		return nil, err
	}
	return vm.LastPopped(), nil
}

// EvalFileVM 读取并执行 JS 脚本文件, 返回 VM 实例。
// 供需要继续驱动事件循环 (严格定时器回调等) 的调用方使用。
// TS 家族文件 (.ts/.tsx/...) 先过 tstransform 类型剥离再进编译管线;
// 源码帧展示用户写的 .ts 原文, 并用转译行映射把 JS 行列翻回 .ts (M2 P0-1)。
func EvalFileVM(path string) (*VM, error) {
	return evalFileVM(path, false)
}

// EvalFileVMModuleEarlyErrors 与 EvalFileVM 相同, 但把入口源码**按模块语义做
// 早期错误判定** (重复导出名 / 未声明导出 / 顶层 return·yield / 严格保留字绑定
// 等), 而执行仍按脚本语义 (compiler moduleMode=false) —— 运行路径与 EvalFileVM
// 完全一致, 只多拦一批应当在编译期就报的 SyntaxError。供 test262 runner 处理
// flags:module 的用例 (它们语义是模块, 但 Gox 以脚本方式执行)。
func EvalFileVMModuleEarlyErrors(path string) (*VM, error) {
	return evalFileVM(path, true)
}

func evalFileVM(path string, moduleEE bool) (*VM, error) {
	orig, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read file: %v", err)
	}
	// TS 家族 (.ts/.tsx/...) 先过类型剥离; code 是进编译管线的 JS, orig 是
	// 用户原文 (源码帧展示的是它 —— M2: 报错定位到 .ts 源码行)。
	code := orig
	var lineMap *tstransform.LineMap
	isTS := tstransform.IsTS(path)
	if isTS {
		res, terr := tstransform.TransformCached(orig, path)
		if terr != nil {
			return nil, terr
		}
		code = res.Code
		lineMap = res.LineMap
	}

	// evalTopLevel=false: 本入口是 Script/Module 语境的 Go 侧求值, 不是 JS
	// eval 语境; 顶层 using 已由 parser.usingAllowed (script 顶层为 false) 拦截。
	c, err := compileSourceOpts(string(code), false, moduleEE, false, false, nil)
	if err != nil {
		if isTS {
			err = remapSourceError(err, lineMap)
		}
		return nil, evalEntryError(err)
	}

	vm := NewWithGlobals(c.Bytes(), c.Constants(), c.NumLocals(), stdlib.SetupGlobals())
	vm.SetModuleBase(filepath.Dir(path))
	// T05/M2 源码帧: 注入原文 + 行映射 + 语句位置表, 未捕获异常渲染出错行。
	vm.SetSourceInfo(path, string(orig))
	if isTS {
		vm.SetTranspileMap(lineMap, string(code))
	}
	vm.SetStmtPositions(c.StmtPositions())
	if err := vm.RunCompiled(c); err != nil {
		return nil, vm.uncaughtError(err)
	}

	return vm, nil
}

// EvalModuleFileVM 读取并执行一个 ESM 模块文件，把它当作**入口模块**，返回 VM。
//
// 与 EvalFileVM（把文件当 script 入口）的区别: 入口本身按模块单元编译执行 ——
// 模块顶层恒严格、顶层 this 为 undefined、顶层声明落在模块命名空间而非全局
// 对象。非模块脚本用 EvalFileVM；test262 的 module 用例走这里 (rNR2Zk)。
func EvalModuleFileVM(path string) (*VM, error) {
	return EvalModuleFileVMWithGlobals(path, stdlib.SetupGlobals())
}

// EvalModuleFileVMWithGlobals 是 EvalModuleFileVM 的变体，使用调用方给定的
// 全局环境 —— 供 test262 runner 先把 harness 按 script 执行、把它的绑定注入
// 全局环境（assert / Test262Error / $DONE …），再以**模块入口**执行用例：
// 这样被 import 的 fixture 与自导入副本也能看到 harness 绑定（模块顶层声明只在
// 模块命名空间里，fixture 看不到，会报 "assert is not defined"）。
func EvalModuleFileVMWithGlobals(path string, globals *runtime.Environment) (*VM, error) {
	absEntry, aerr := filepath.Abs(path)
	if aerr != nil {
		absEntry = filepath.Clean(path)
	}
	orig, err := os.ReadFile(absEntry)
	if err != nil {
		return nil, fmt.Errorf("cannot read file: %v", err)
	}
	code := orig
	var lineMap *tstransform.LineMap
	isTS := tstransform.IsTS(absEntry)
	if isTS {
		res, terr := tstransform.TransformCached(orig, absEntry)
		if terr != nil {
			return nil, terr
		}
		code = res.Code
		lineMap = res.LineMap
	}

	// moduleMode=true 且 moduleEE=true: 真模块入口既要模块运行语义 (顶层恒严格 /
	// 顶层 this=undefined / 顶层声明进模块命名空间), 也要按模块早错规则拦截
	// (重复导出名 / 未声明导出 / 顶层 return·yield 等) —— 二者互补, 缺一则
	// test262 的 module 负例漏拦 (rTI1PN) 或模块语义不落地 (rNR2Zk)。
	c, err := compileSourceOpts(string(code), true, true, false, false, nil)
	if err != nil {
		if isTS {
			err = remapSourceError(err, lineMap)
		}
		return nil, evalEntryError(err)
	}

	vm := NewWithGlobals(c.Bytes(), c.Constants(), c.NumLocals(), globals)
	vm.SetModuleBase(dirOf(absEntry))
	// 模块顶层 this = undefined (与 script 顶层 this = globalThis 相对)。
	vm.moduleMode = true
	vm.currentExports = newModuleExports()
	// 循环导入防线: 入口模块先注册导出对象再执行 —— 执行期间若 import 自己/
	// 成环，命中缓存拿到这份(填充中的)导出对象，而不是重新编译执行到栈溢出。
	vm.modules[absEntry] = vm.currentExports
	vm.SetSourceInfo(absEntry, string(orig))
	if isTS {
		vm.SetTranspileMap(lineMap, string(code))
	}
	vm.mainUnit.isModule = true
	vm.SetStmtPositions(c.StmtPositions())
	if err := vm.RunCompiled(c); err != nil {
		// 与 script 入口 (EvalFileVM / EvalVM) 一致走 uncaughtError: 未捕获的
		// **抛出值**按 ToString 语义渲染 (用户对象调其自定义 toString, 保住
		// 构造器类型名), 而不是直接 %v 落到 ThrowError.Error() 的 Inspect
		// (会把 `throw new Test262Error()` 渲染成 `{ message: "" }` 丢类型名)。
		return nil, vm.uncaughtError(err)
	}

	return vm, nil
}

// joinStrings 连接字符串切片。
func joinStrings(strs []string, sep string) string {
	return strings.Join(strs, sep)
}

// osReadFile 读取文件内容。
func osReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// resolvePath 解析模块相对路径。
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
func (vm *VM) storeGlobalBinding(name string, val object.Value) error {
	if cur, exists := vm.globals.Get(name); exists {
		if acc, isAcc := cur.(*object.Accessor); isAcc {
			if acc.Setter != nil && object.IsCallable(acc.Setter) {
				object.CallFunction(acc.Setter, vm.globalThisValue(), val)
				if err := vm.checkCallbackErr(); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
				}
			}
			// 无 setter 的访问器: 写入被忽略 (见上注)。
			return nil
		}
		return vm.globals.Set(name, val)
	}
	// 隐式赋值创建的全局: globalThis 的自有可配置属性。
	vm.globals.DeclareImplicit(name, val)
	return nil
}

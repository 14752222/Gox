package vm

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
	"github.com/14752222/Gox/stdlib"
	"github.com/14752222/Gox/tstransform"
)

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

func osReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// resolvePath 解析模块相对路径。

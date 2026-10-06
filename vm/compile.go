package vm

import (
	"fmt"

	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/compiler"
	"github.com/14752222/Gox/lexer"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/parser"
)

// 本文件是全仓库唯一的 "源码 → 字节码" 编译入口。
//
// 之前 lexer→parser→compiler 管线在 vm 的 4 个入口 (loadModule / EvalVM /
// EvalWithGlobals / EvalFileVM) 与 stdlib (eval / new Function) 各写了一份;
// 现在统一收敛到这里, stdlib 侧经 object.SetCompileSource 编译桥间接使用。

// sourceError 区分解析/编译两个失败阶段, 调用方据此保留各自的错误消息格式。
type sourceError struct {
	parse bool
	msg   string
}

func (e *sourceError) Error() string { return e.msg }

func init() {
	object.SetCompileSource(compileForBridge)
	object.SetCompileSourceAllowingNewTarget(compileForBridgeAllowingNewTarget)
	object.SetCompileSourceEval(compileForBridgeEval)
}

// compileSource 编译 JS 源码。moduleMode 为 true 时模块有自己的命名空间,
// 顶层变量不写入共享全局环境 (见 loadModule)。
func compileSource(src string, moduleMode bool) (*compiler.Compiler, error) {
	return compileSourceOpts(src, moduleMode, false, false, false)
}

// compileSourceOpts 是 compileSource 的带选项版本。
//
//   - moduleEE 单独开启「模块早期错误」判定 (不改变 moduleMode 带来的运行语义)。
//     见 parser.SetModuleEarlyErrors 与 vm.EvalFileVMModuleEarlyErrors: test262 的
//     module 用例在 Gox 里按脚本执行 (moduleMode=false), 但需要按 Module 的早错
//     规则拦截 (重复导出名/未声明导出/顶层 return 等)。
//   - forbidNewTarget 仅供 compileForBridge (eval / Function 构造器) 使用: Gox 的
//     eval 是 stdlib 侧的全局包装函数, 编译期只看到 `(function(){ ... })`, 无从
//     判定 eval 语境是否允许 new.target, 故保守地整单元禁止
//     (见 parser.Parser.newTargetForbidden)。
//   - evalTopLevel 仅供 eval 编译桥使用: 标记本编译单元是 eval 的源码, 使
//     parser 对「合成函数体的直接语句 (blockOrFnDepth==1)」上的 using /
//     await using 声明报早错 (规范: eval 按 Script goal 解析, UsingDeclaration
//     不被 Block/FunctionBody 等包含即 SyntaxError; 见 parser.usingDeclAllowed)。
//     new Function 的体是真正的 FunctionBody, **不**置本标志。
func compileSourceOpts(src string, moduleMode, moduleEE, forbidNewTarget, evalTopLevel bool) (*compiler.Compiler, error) {
	p := parser.New(lexer.New(src))
	p.SetModule(moduleMode) // 模块顶层恒严格, 供解析期早错判定
	if moduleEE {
		p.SetModuleEarlyErrors(true)
	}
	if forbidNewTarget {
		p.SetNewTargetForbidden(true)
	}
	if evalTopLevel {
		p.SetEvalTopLevel(true)
	}
	program := p.ParseProgram()
	if p.Errors().HasErrors() {
		return nil, &sourceError{parse: true, msg: p.Errors().String()}
	}
	c := compiler.New()
	c.SetModuleMode(moduleMode)
	c.SetStmtPos(program.Positions) // T05: 语句位置表 → 运行时错误源码帧
	if err := c.Compile(program); err != nil {
		return nil, &sourceError{msg: err.Error()}
	}
	return c, nil
}

// compileForBridge 是注册给 object.CompileSource 的实现:
// 编译源码并取出常量池中的顶层包装函数, 供 stdlib 的 new Function /
// 类字段初始化器合成检查组装闭包后执行。
//
// 以 forbidNewTarget=true 编译: 包装源码的顶层是函数体, 若照常放行
// new.target, Function 构造器产物里的 new.target 会被静默求成 undefined 而不
// 是规范要求的行为。Gox 的全局模型无从区分这些语境, 故一律禁止 (保守近似)。
//
// **不置 evalTopLevel**: 本桥服务的是 new Function (FunctionBody, using 合法)
// 与合成 class 检查, 均非 eval 语境。eval 语境走 compileForBridgeEval
// (看板 rabcWh)。
func compileForBridge(src string) (*object.CompiledFunction, error) {
	return bridgeCompile(src, true, false)
}

// compileForBridgeEval 是注册给 object.CompileSourceEval 的实现:
// global/indirect/箭头 eval 的源码 (stdlib 的 runGlobalEval 非允许 new.target
// 分支)。相对 compileForBridge 只多置 evalTopLevel —— eval 源码按 Script goal
// 解析, 其顶层的 using / await using 声明是 SyntaxError
// (规范 sec-let-const-using-and-await-using-declarations-static-semantics-
// early-errors; test262 using-not-allowed-at-top-level-of-eval.js)。
func compileForBridgeEval(src string) (*object.CompiledFunction, error) {
	return bridgeCompile(src, true, true)
}

// compileForBridgeAllowingNewTarget 与 compileForBridgeEval 同 (eval 语境,
// evalTopLevel=true), 但**放行** new.target。
// 仅「非箭头函数体内的直接 eval」这一语境由 stdlib 经 object.CompileSource-
// AllowingNewTarget 使用 —— 该语境下 eval 源码 (含其内箭头) 出现 new.target 合法,
// 值由 VM 写入的调用者 new.target 决定 (见 runGlobalEval / SetDirectEvalNewTarget)。
// 其余语境 (global/indirect/箭头 eval、Function 构造器) 仍走各自桥 (禁止/非 eval)。
func compileForBridgeAllowingNewTarget(src string) (*object.CompiledFunction, error) {
	return bridgeCompile(src, false, true)
}

// bridgeCompile 是三条编译桥的公用躯干: forbidNewTarget 控制 new.target 早错,
// evalTopLevel 控制 eval 顶层 using / await using 早错 (见 compileSourceOpts)。
func bridgeCompile(src string, forbidNewTarget, evalTopLevel bool) (*object.CompiledFunction, error) {
	c, err := compileSourceOpts(src, false, false, forbidNewTarget, evalTopLevel)
	if err != nil {
		return nil, err
	}
	meta := lastFunctionMeta(c)
	if meta == nil {
		return nil, fmt.Errorf("compile: no function metadata in output")
	}
	return metaToCompiledFunction(meta, c.Constants().Constants), nil
}

// lastFunctionMeta 取常量池中最后一个函数元数据。
// "(function(){...})" 这类包装源码的顶层函数是最后一个加入常量池的。
func lastFunctionMeta(c *compiler.Compiler) *bytecode.FunctionMetadata {
	var meta *bytecode.FunctionMetadata
	for i := 0; i < c.Constants().Len(); i++ {
		if fm, ok := c.Constants().Get(uint16(i)).(*bytecode.FunctionMetadata); ok {
			meta = fm
		}
	}
	return meta
}

// metaToCompiledFunction 把编译产物中的函数元数据转换为可调用的
// CompiledFunction (createClosure 与编译桥共用)。
func metaToCompiledFunction(meta *bytecode.FunctionMetadata, consts []object.Value) *object.CompiledFunction {
	fn := &object.CompiledFunction{
		Instructions:  meta.Instructions,
		NumLocals:      meta.NumLocals,
		NumParameters:  meta.NumParameters,
		Name:           meta.Name,
		IsArrow:        meta.IsArrow,
		IsGenerator:    meta.IsGenerator,
		IsAsync:        meta.IsAsync,
		IsAsyncGenerator: meta.IsAsyncGenerator,
		IsStrict:         meta.IsStrict,
		BaseSlot:       meta.BaseSlot,
		ArgumentsSlot:  meta.ArgumentsSlot,
		SelfSlot:       meta.SelfSlot,
		ParamPrologueEnd: meta.ParamPrologueEnd,
		DeferParams:    meta.DeferParams,
		Constants:      consts,
		Positions:      srcPosList(meta.Positions), // T05: 函数体语句位置表
	}
	for _, ps := range meta.Parameters {
		fn.Parameters = append(fn.Parameters, object.ParameterInfo{
			Name:    ps.Name,
			Default: ps.HasDefault,
			Rest:    ps.IsRest,
		})
	}
	return fn
}

// srcPosList 把 bytecode 层的位置表转成 object 层的结构 (两包各自定义
// SrcPos, 避免 object → bytecode 之外的依赖; vm 是唯一同时引用两包的层)。
func srcPosList(in []bytecode.SrcPos) []object.SrcPos {
	if len(in) == 0 {
		return nil
	}
	out := make([]object.SrcPos, len(in))
	for i, p := range in {
		out[i] = object.SrcPos{Offset: p.Offset, Line: p.Line, Col: p.Col}
	}
	return out
}

// evalEntryError 保留 Eval/EvalVM/EvalWithGlobals/EvalFileVM 一族的
// 错误消息格式。
func evalEntryError(err error) error {
	if se, ok := err.(*sourceError); ok && se.parse {
		return fmt.Errorf("parser errors:\n%s", se.msg)
	}
	return fmt.Errorf("compiler error: %v", err)
}

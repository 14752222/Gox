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
}

// compileSource 编译 JS 源码。moduleMode 为 true 时模块有自己的命名空间,
// 顶层变量不写入共享全局环境 (见 loadModule)。
func compileSource(src string, moduleMode bool) (*compiler.Compiler, error) {
	p := parser.New(lexer.New(src))
	program := p.ParseProgram()
	if p.Errors().HasErrors() {
		return nil, &sourceError{parse: true, msg: p.Errors().String()}
	}
	c := compiler.New()
	c.SetModuleMode(moduleMode)
	if err := c.Compile(program); err != nil {
		return nil, &sourceError{msg: err.Error()}
	}
	return c, nil
}

// compileForBridge 是注册给 object.CompileSource 的实现:
// 编译源码并取出常量池中的顶层包装函数, 供 stdlib 的 eval / new Function
// 组装闭包后执行。
func compileForBridge(src string) (*object.CompiledFunction, error) {
	c, err := compileSource(src, false)
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
		NumLocals:     meta.NumLocals,
		NumParameters: meta.NumParameters,
		Name:          meta.Name,
		IsArrow:       meta.IsArrow,
		IsGenerator:   meta.IsGenerator,
		IsAsync:       meta.IsAsync,
		BaseSlot:      meta.BaseSlot,
		ArgumentsSlot: meta.ArgumentsSlot,
		SelfSlot:      meta.SelfSlot,
		Constants:     consts,
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

// evalEntryError 保留 Eval/EvalVM/EvalWithGlobals/EvalFileVM 一族的
// 错误消息格式。
func evalEntryError(err error) error {
	if se, ok := err.(*sourceError); ok && se.parse {
		return fmt.Errorf("parser errors:\n%s", se.msg)
	}
	return fmt.Errorf("compiler error: %v", err)
}

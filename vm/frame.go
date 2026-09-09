package vm

import (
	"js-runtime/bytecode"
	"js-runtime/object"
)

// Frame 表示 VM 调用栈中的一个帧。
// 每次函数调用创建一个新帧，函数返回时弹出。
//
// 帧包含:
//   - Instructions: 当前正在执行的字节码
//   - PC: 程序计数器 (字节偏移，每条指令 3 字节)
//   - Locals: 局部变量数组 (通过 OP_LOAD/OP_STORE 按 slot 索引)
//   - Closure: 如果是闭包调用，关联的闭包对象
//   - Constants: 常量池引用 (所有帧共享同一个)
//   - ModifiedSlots: 本帧中修改过的 slot 集合 (用于 popFrame 时向上一帧传播闭包变量修改)
//   - CreatedClosures: 在本帧中创建的闭包列表 (用于 OP_STORE 时向子闭包传播外层变量修改)
type Frame struct {
	Instructions    bytecode.Instructions
	PC              int
	Locals          []object.Value
	Closure         *object.Closure
	Constants       *bytecode.ConstantPool
	ModifiedSlots   map[int]bool      // 修改过的 slot (用于 popFrame 传播)
	CreatedClosures []*object.Closure // 本帧创建的闭包 (用于 STORE 传播)
	StackBase       int               // 进入本帧时栈高度 (返回时截断到此处)
}

// NewFrame 创建新的调用帧。
func NewFrame(ins bytecode.Instructions, constants *bytecode.ConstantPool, numLocals int) *Frame {
	locals := make([]object.Value, numLocals)
	for i := range locals {
		locals[i] = object.UndefinedSingleton
	}
	return &Frame{
		Instructions: ins,
		PC:           0,
		Locals:       locals,
		Constants:    constants,
	}
}

// NewClosureFrame 为闭包调用创建新帧。
// captured 是从创建闭包的帧中捕获的局部变量。
func NewClosureFrame(meta *bytecode.FunctionMetadata, constants *bytecode.ConstantPool, captured []object.Value, closure *object.Closure) *Frame {
	locals := make([]object.Value, meta.NumLocals)
	for i := range locals {
		locals[i] = object.UndefinedSingleton
	}
	copy(locals, captured)

	return &Frame{
		Instructions: meta.Instructions,
		PC:           0,
		Locals:       locals,
		Closure:      closure,
		Constants:    constants,
	}
}

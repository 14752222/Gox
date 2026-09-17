package compiler

import "github.com/14752222/Gox/bytecode"

// Emitter 是字节码发射辅助器。
// 提供便捷的方法来生成指令、管理跳转标签和回填。
type Emitter struct {
	instructions bytecode.Instructions // 累积的字节码
}

// NewEmitter 创建新的发射器。
func NewEmitter() *Emitter {
	return &Emitter{
		instructions: bytecode.Instructions{},
	}
}

// Emit 发射一条指令。
func (e *Emitter) Emit(op bytecode.Opcode, operand uint16) int {
	pos := len(e.instructions) // 记录指令位置
	e.instructions = append(e.instructions, bytecode.Make(op, operand)...)
	return pos
}

// EmitNoOperand 发射一条无操作数指令。
func (e *Emitter) EmitNoOperand(op bytecode.Opcode) int {
	return e.Emit(op, 0)
}

// Bytes 返回已发射的字节码。
func (e *Emitter) Bytes() bytecode.Instructions {
	return e.instructions
}

// Pos 返回当前字节码长度 (即下一条指令的位置)。
func (e *Emitter) Pos() int {
	return len(e.instructions)
}

// ReplaceJumpTarget 回填跳转指令的目标地址。
// pos 是跳转指令的位置，target 是目标地址。
func (e *Emitter) ReplaceJumpTarget(pos int, target uint16) {
	e.instructions.ReplaceOperand(pos, target)
}

// EmitJump 发射一条跳转指令，返回指令位置 (用于后续回填)。
func (e *Emitter) EmitJump(op bytecode.Opcode) int {
	return e.Emit(op, 0) // 暂时填 0，后续回填
}

// PatchJump 回填跳转目标。
func (e *Emitter) PatchJump(pos int) {
	target := uint16(e.Pos())
	e.ReplaceJumpTarget(pos, target)
}

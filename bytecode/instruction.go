package bytecode

import "encoding/binary"

// Instructions 是字节码指令序列 (字节数组)。
// 每 InstructionSize(3) 字节为一条指令: [opcode(1)] [operand(2, big-endian)]。
type Instructions []byte

// Make 编码一条指令: 1 字节操作码 + 2 字节操作数 (大端序 uint16)。
// 返回 3 字节的指令。
func Make(op Opcode, operand uint16) Instructions {
	return Instructions{byte(op), byte(operand >> 8), byte(operand & 0xFF)}
}

// MakeNoOperand 编码一条无操作数指令 (operand = 0)。
func MakeNoOperand(op Opcode) Instructions {
	return Make(op, 0)
}

// ReadOpcode 从指令序列中读取指定偏移处的操作码。
func ReadOpcode(ins Instructions, offset int) Opcode {
	if offset >= len(ins) {
		return OP_NOP
	}
	return Opcode(ins[offset])
}

// ReadOperand 从指令序列中读取指定偏移处的操作数 (2 字节大端序)。
func ReadOperand(ins Instructions, offset int) uint16 {
	if offset+2 > len(ins) {
		return 0
	}
	return binary.BigEndian.Uint16(ins[offset : offset+2])
}

// ReadInstruction 从指定偏移处读取一条完整指令 (3 字节)。
func ReadInstruction(ins Instructions, offset int) (Opcode, uint16) {
	return ReadOpcode(ins, offset), ReadOperand(ins, offset+1)
}

// Append 将一条指令追加到指令序列。
func (ins *Instructions) Append(op Opcode, operand uint16) {
	*ins = append(*ins, Make(op, operand)...)
}

// AppendNoOperand 追加一条无操作数指令。
func (ins *Instructions) AppendNoOperand(op Opcode) {
	*ins = append(*ins, MakeNoOperand(op)...)
}

// Len 返回指令条数 (非字节数)。
func (ins Instructions) Len() int {
	return len(ins) / InstructionSize
}

// ReplaceInstruction 替换指定偏移处的指令 (用于回填跳转目标)。
func (ins Instructions) ReplaceInstruction(offset int, op Opcode, operand uint16) {
	if offset+InstructionSize > len(ins) {
		return
	}
	ins[offset] = byte(op)
	binary.BigEndian.PutUint16(ins[offset+1:offset+3], operand)
}

// ReplaceOperand 仅替换指定偏移处的操作数 (用于回填跳转目标)。
func (ins Instructions) ReplaceOperand(offset int, operand uint16) {
	if offset+InstructionSize > len(ins) {
		return
	}
	binary.BigEndian.PutUint16(ins[offset+1:offset+3], operand)
}

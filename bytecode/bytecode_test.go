package bytecode

import (
	"testing"

	"github.com/14752222/Gox/object"
)

func TestInstructionEncoding(t *testing.T) {
	ins := Make(OP_CONST, 42)
	if len(ins) != InstructionSize {
		t.Fatalf("expected instruction size %d, got %d", InstructionSize, len(ins))
	}
	op := Opcode(ins[0])
	if op != OP_CONST {
		t.Fatalf("expected OP_CONST, got %s", op)
	}
	operand := ReadOperand(ins, 1)
	if operand != 42 {
		t.Fatalf("expected operand 42, got %d", operand)
	}
}

func TestInstructionAppend(t *testing.T) {
	var ins Instructions
	ins.Append(OP_CONST, 1)
	ins.Append(OP_ADD, 0)
	ins.AppendNoOperand(OP_RETURN)

	if ins.Len() != 3 {
		t.Fatalf("expected 3 instructions, got %d", ins.Len())
	}

	// Verify each instruction
	op, operand := ReadInstruction(ins, 0)
	if op != OP_CONST || operand != 1 {
		t.Fatalf("instruction 0: expected OP_CONST 1, got %s %d", op, operand)
	}

	op, operand = ReadInstruction(ins, InstructionSize)
	if op != OP_ADD || operand != 0 {
		t.Fatalf("instruction 1: expected OP_ADD 0, got %s %d", op, operand)
	}

	op, operand = ReadInstruction(ins, 2*InstructionSize)
	if op != OP_RETURN || operand != 0 {
		t.Fatalf("instruction 2: expected OP_RETURN 0, got %s %d", op, operand)
	}
}

func TestReplaceOperand(t *testing.T) {
	ins := Make(OP_JUMP, 0)
	ins.ReplaceOperand(0, 999)
	op, operand := ReadInstruction(ins, 0)
	if op != OP_JUMP || operand != 999 {
		t.Fatalf("expected OP_JUMP 999, got %s %d", op, operand)
	}
}

func TestConstantPool(t *testing.T) {
	cp := NewConstantPool()

	// Add constants
	idx1 := cp.AddConstant(object.NewInt(42))
	idx2 := cp.AddConstant(object.NewString("hello"))
	idx3 := cp.AddConstant(object.NewInt(42)) // duplicate

	if idx1 != idx3 {
		t.Fatalf("duplicate constant should have same index: idx1=%d, idx3=%d", idx1, idx3)
	}
	if idx2 == idx1 {
		t.Fatal("different constants should have different indices")
	}

	// Get
	val := cp.Get(idx1)
	if num, ok := val.(*object.Number); !ok || num.Value != 42 {
		t.Fatalf("expected 42, got %v", val)
	}
}

func TestFunctionMetadata(t *testing.T) {
	var ins Instructions
	ins.Append(OP_CONST, 0)
	ins.AppendNoOperand(OP_RETURN)

	fm := NewFunctionMetadata("add", ins, 2, 2, []ParameterSpec{
		{Name: "a"}, {Name: "b"},
	}, false)

	if fm.Name != "add" {
		t.Fatalf("expected 'add', got %q", fm.Name)
	}
	if fm.NumParameters != 2 {
		t.Fatalf("expected 2 params, got %d", fm.NumParameters)
	}
	if fm.IsArrow {
		t.Fatal("expected non-arrow function")
	}
}

func TestDisassemble(t *testing.T) {
	cp := NewConstantPool()
	idx := cp.AddConstant(object.NewInt(42))

	var ins Instructions
	ins.Append(OP_CONST, idx)
	ins.Append(OP_CONST, idx)
	ins.AppendNoOperand(OP_ADD)
	ins.AppendNoOperand(OP_RETURN)

	result := Disassemble(ins, cp)
	if result == "" {
		t.Fatal("expected non-empty disassembly")
	}
	// Should contain opcode names
	if !contains(result, "CONST") {
		t.Fatal("disassembly should contain CONST")
	}
	if !contains(result, "ADD") {
		t.Fatal("disassembly should contain ADD")
	}
	if !contains(result, "RETURN") {
		t.Fatal("disassembly should contain RETURN")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

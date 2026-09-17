package compiler

import (
	"testing"

	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/lexer"
	"github.com/14752222/Gox/parser"
)

// compile 编译 JS 源码，返回编译器。
func compile(t *testing.T, input string) *Compiler {
	l := lexer.New(input)
	p := parser.New(l)
	program := p.ParseProgram()
	if p.Errors().HasErrors() {
		t.Fatalf("parser errors: %s", p.Errors().String())
	}

	c := New()
	if err := c.Compile(program); err != nil {
		t.Fatalf("compiler error: %v", err)
	}
	return c
}

// instructionAt 获取指定索引处的指令
func instructionAt(c *Compiler, idx int) (bytecode.Opcode, uint16) {
	ins := c.Bytes()
	return bytecode.ReadInstruction(ins, idx*bytecode.InstructionSize)
}

func TestCompileLetStatement(t *testing.T) {
	c := compile(t, `let x = 5;`)
	ins := c.Bytes()

	// 期望: OP_CONST 0 (const idx 0 = 5), OP_DECLARE 1 (name_idx 1 = "x")
	// 全局作用域的 let 声明写入共享全局环境 (OP_DECLARE), 支持 REPL 跨输入状态保持
	op, operand := bytecode.ReadInstruction(ins, 0)
	if op != bytecode.OP_CONST {
		t.Fatalf("expected OP_CONST, got %s", op)
	}
	if operand != 0 {
		t.Fatalf("expected const idx 0, got %d", operand)
	}

	op, operand = bytecode.ReadInstruction(ins, bytecode.InstructionSize)
	if op != bytecode.OP_DECLARE {
		t.Fatalf("expected OP_DECLARE, got %s", op)
	}
	if operand != 1 {
		t.Fatalf("expected name idx 1, got %d", operand)
	}
}

func TestCompileLetStatementModuleMode(t *testing.T) {
	// 模块模式下全局变量走局部 slot (旧行为), 隔离模块命名空间
	l := lexer.New(`let x = 5;`)
	p := parser.New(l)
	program := p.ParseProgram()
	if p.Errors().HasErrors() {
		t.Fatalf("parser errors: %s", p.Errors().String())
	}

	c := New()
	c.SetModuleMode(true) // 必须在 Compile 前设置
	if err := c.Compile(program); err != nil {
		t.Fatalf("compile error: %v", err)
	}
	ins := c.Bytes()

	op, _ := bytecode.ReadInstruction(ins, bytecode.InstructionSize)
	if op != bytecode.OP_STORE {
		t.Fatalf("expected OP_STORE in module mode, got %s", op)
	}
}

func TestCompileBinaryExpression(t *testing.T) {
	c := compile(t, `1 + 2;`)
	ins := c.Bytes()

	// OP_CONST 0 (1), OP_CONST 1 (2), OP_ADD
	op, _ := bytecode.ReadInstruction(ins, 0)
	if op != bytecode.OP_CONST {
		t.Fatalf("expected OP_CONST, got %s", op)
	}
	op, _ = bytecode.ReadInstruction(ins, bytecode.InstructionSize)
	if op != bytecode.OP_CONST {
		t.Fatalf("expected OP_CONST, got %s", op)
	}
	op, _ = bytecode.ReadInstruction(ins, 2*bytecode.InstructionSize)
	if op != bytecode.OP_ADD {
		t.Fatalf("expected OP_ADD, got %s", op)
	}
}

func TestCompileIfStatement(t *testing.T) {
	c := compile(t, `if (true) { 1; } else { 2; }`)
	ins := c.Bytes()

	// 应包含 OP_TRUE, OP_JUMP_IF_FALSE, OP_CONST, OP_JUMP, OP_CONST
	hasJumpIfFalse := false
	hasJump := false
	for pc := 0; pc < len(ins); pc += bytecode.InstructionSize {
		op, _ := bytecode.ReadInstruction(ins, pc)
		if op == bytecode.OP_JUMP_IF_FALSE {
			hasJumpIfFalse = true
		}
		if op == bytecode.OP_JUMP {
			hasJump = true
		}
	}
	if !hasJumpIfFalse {
		t.Fatal("expected OP_JUMP_IF_FALSE in if statement bytecode")
	}
	if !hasJump {
		t.Fatal("expected OP_JUMP in if-else bytecode")
	}
}

func TestCompileWhileStatement(t *testing.T) {
	c := compile(t, `while (true) { 1; }`)
	ins := c.Bytes()

	// 应包含 OP_LOOP (循环回跳)
	hasLoop := false
	for pc := 0; pc < len(ins); pc += bytecode.InstructionSize {
		op, _ := bytecode.ReadInstruction(ins, pc)
		if op == bytecode.OP_LOOP {
			hasLoop = true
		}
	}
	if !hasLoop {
		t.Fatal("expected OP_LOOP in while bytecode")
	}
}

func TestCompileFunctionDeclaration(t *testing.T) {
	c := compile(t, `function add(a, b) { return a + b; }`)
	ins := c.Bytes()

	// 应包含 OP_FUNCTION
	hasFunction := false
	for pc := 0; pc < len(ins); pc += bytecode.InstructionSize {
		op, _ := bytecode.ReadInstruction(ins, pc)
		if op == bytecode.OP_FUNCTION {
			hasFunction = true
		}
	}
	if !hasFunction {
		t.Fatal("expected OP_FUNCTION in function declaration bytecode")
	}

	// 检查常量池中有 FunctionMetadata
	found := false
	for _, val := range c.Constants().Constants {
		if _, ok := val.(*bytecode.FunctionMetadata); ok {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("expected FunctionMetadata in constant pool")
	}
}

func TestCompileArrowFunction(t *testing.T) {
	c := compile(t, `let f = (a, b) => a + b;`)
	ins := c.Bytes()

	// 应包含 OP_ARROW_FUNC
	hasArrowFunc := false
	for pc := 0; pc < len(ins); pc += bytecode.InstructionSize {
		op, _ := bytecode.ReadInstruction(ins, pc)
		if op == bytecode.OP_ARROW_FUNC {
			hasArrowFunc = true
		}
	}
	if !hasArrowFunc {
		t.Fatal("expected OP_ARROW_FUNC in arrow function bytecode")
	}

	// 检查 FunctionMetadata.IsArrow = true
	for _, val := range c.Constants().Constants {
		if fm, ok := val.(*bytecode.FunctionMetadata); ok {
			if !fm.IsArrow {
				t.Fatal("expected IsArrow=true for arrow function")
			}
		}
	}
}

func TestCompileArrayLiteral(t *testing.T) {
	c := compile(t, `[1, 2, 3];`)
	ins := c.Bytes()

	// OP_CONST x3, OP_NEW_ARRAY 3
	op, operand := bytecode.ReadInstruction(ins, 3*bytecode.InstructionSize)
	if op != bytecode.OP_NEW_ARRAY {
		t.Fatalf("expected OP_NEW_ARRAY, got %s", op)
	}
	if operand != 3 {
		t.Fatalf("expected 3 elements, got %d", operand)
	}
}

func TestCompileObjectLiteral(t *testing.T) {
	c := compile(t, `let obj = { name: "Alice", age: 30 };`)
	ins := c.Bytes()

	// 应包含 OP_NEW_OBJECT 和 OP_SET_PROP
	hasNewObject := false
	hasSetProp := false
	for pc := 0; pc < len(ins); pc += bytecode.InstructionSize {
		op, _ := bytecode.ReadInstruction(ins, pc)
		if op == bytecode.OP_NEW_OBJECT {
			hasNewObject = true
		}
		if op == bytecode.OP_SET_PROP {
			hasSetProp = true
		}
	}
	if !hasNewObject {
		t.Fatal("expected OP_NEW_OBJECT")
	}
	if !hasSetProp {
		t.Fatal("expected OP_SET_PROP")
	}
}

func TestCompileTemplateLiteral(t *testing.T) {
	c := compile(t, "`Hello ${42}!`")
	ins := c.Bytes()

	// 应包含 OP_TEMPLATE_START, OP_TEMPLATE_PART, OP_TEMPLATE_END
	hasStart := false
	hasPart := false
	hasEnd := false
	for pc := 0; pc < len(ins); pc += bytecode.InstructionSize {
		op, _ := bytecode.ReadInstruction(ins, pc)
		switch op {
		case bytecode.OP_TEMPLATE_START:
			hasStart = true
		case bytecode.OP_TEMPLATE_PART:
			hasPart = true
		case bytecode.OP_TEMPLATE_END:
			hasEnd = true
		}
	}
	if !hasStart {
		t.Fatal("expected OP_TEMPLATE_START")
	}
	if !hasPart {
		t.Fatal("expected OP_TEMPLATE_PART")
	}
	if !hasEnd {
		t.Fatal("expected OP_TEMPLATE_END")
	}
}

func TestCompileCallExpression(t *testing.T) {
	c := compile(t, `foo(1, 2);`)
	ins := c.Bytes()

	// 应包含 OP_CALL 2
	for pc := 0; pc < len(ins); pc += bytecode.InstructionSize {
		op, operand := bytecode.ReadInstruction(ins, pc)
		if op == bytecode.OP_CALL {
			if operand != 2 {
				t.Fatalf("expected OP_CALL 2, got OP_CALL %d", operand)
			}
			return
		}
	}
	t.Fatal("expected OP_CALL in call expression bytecode")
}

func TestCompileLogicalExpression(t *testing.T) {
	c := compile(t, `a && b;`)
	ins := c.Bytes()

	// 应包含 OP_JUMP_IF_FALSE (短路)
	hasJumpIfFalse := false
	for pc := 0; pc < len(ins); pc += bytecode.InstructionSize {
		op, _ := bytecode.ReadInstruction(ins, pc)
		if op == bytecode.OP_JUMP_IF_FALSE {
			hasJumpIfFalse = true
		}
	}
	if !hasJumpIfFalse {
		t.Fatal("expected OP_JUMP_IF_FALSE for && short-circuit")
	}
}

func TestCompileBlockScope(t *testing.T) {
	c := compile(t, `{ let x = 1; }`)
	ins := c.Bytes()

	// 应包含 OP_PUSH_SCOPE 和 OP_POP_SCOPE
	hasPushScope := false
	hasPopScope := false
	for pc := 0; pc < len(ins); pc += bytecode.InstructionSize {
		op, _ := bytecode.ReadInstruction(ins, pc)
		if op == bytecode.OP_PUSH_SCOPE {
			hasPushScope = true
		}
		if op == bytecode.OP_POP_SCOPE {
			hasPopScope = true
		}
	}
	if !hasPushScope {
		t.Fatal("expected OP_PUSH_SCOPE for block")
	}
	if !hasPopScope {
		t.Fatal("expected OP_POP_SCOPE for block")
	}
}

func TestDisassemble(t *testing.T) {
	c := compile(t, `let x = 1 + 2;`)
	ins := c.Bytes()
	result := bytecode.Disassemble(ins, c.Constants())

	if result == "" {
		t.Fatal("expected non-empty disassembly")
	}
}

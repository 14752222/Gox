package bytecode

import (
	"fmt"
	"strings"

	"js-runtime/object"
)

// ConstantPool 存储编译期产生的常量。
// 常量按顺序索引，操作数引用常量时使用索引。
// 常量类型: Number, String, Boolean, Null, Undefined, FunctionMetadata, DestructurePattern
type ConstantPool struct {
	Constants []object.Value
}

// NewConstantPool 创建空常量池。
func NewConstantPool() *ConstantPool {
	return &ConstantPool{
		Constants: []object.Value{},
	}
}

// AddConstant 添加常量到池中，返回索引。
// 如果已有相同常量，返回已有索引 (去重)。
func (cp *ConstantPool) AddConstant(val object.Value) uint16 {
	// 去重: 查找已有常量
	for i, existing := range cp.Constants {
		if constantsEqual(existing, val) {
			return uint16(i)
		}
	}
	idx := uint16(len(cp.Constants))
	cp.Constants = append(cp.Constants, val)
	return idx
}

// Get 获取指定索引的常量。
func (cp *ConstantPool) Get(idx uint16) object.Value {
	if int(idx) < len(cp.Constants) {
		return cp.Constants[idx]
	}
	return object.UndefinedSingleton
}

// Len 返回常量池大小。
func (cp *ConstantPool) Len() int { return len(cp.Constants) }

// constantsEqual 比较两个常量值是否相等 (用于去重)。
func constantsEqual(a, b object.Value) bool {
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
	case *object.BigInt:
		if bv, ok := b.(*object.BigInt); ok {
			return av.Value.Cmp(bv.Value) == 0
		}
	case *object.Null:
		_, ok := b.(*object.Null)
		return ok
	case *object.Undefined:
		_, ok := b.(*object.Undefined)
		return ok
	case *object.Array:
		// 数组不去重 (每次创建新的)
		return false
	case *object.Object:
		// 对象不去重
		return false
	}
	return false
}

// ===== 函数元数据 =====

// FunctionMetadata 存储编译后的函数信息。
// 包含函数体的字节码、参数信息和局部变量数。
type FunctionMetadata struct {
	Name          string          // 函数名
	Instructions  Instructions    // 函数体字节码
	NumLocals     int             // 局部变量数 (含参数和外层捕获)
	NumParameters int             // 参数个数
	Parameters    []ParameterSpec // 参数规格
	IsArrow       bool            // 是否箭头函数
	IsGenerator   bool            // 是否生成器函数 (function*)
	IsAsync       bool            // 是否 async 函数
	BaseSlot      int             // 函数自身变量的起始槽位 (外层作用域的变量数)
	ArgumentsSlot int             // arguments 对象槽位 (-1 表示未使用/箭头函数)
}

// ParameterSpec 描述函数参数规格。
type ParameterSpec struct {
	Name       string // 参数名
	HasDefault bool   // 是否有默认值
	IsRest     bool   // 是否剩余参数 (...)
}

// NewFunctionMetadata 创建函数元数据。
func NewFunctionMetadata(name string, ins Instructions, numLocals, numParams int, params []ParameterSpec, isArrow bool) *FunctionMetadata {
	return &FunctionMetadata{
		Name:          name,
		Instructions:  ins,
		NumLocals:     numLocals,
		NumParameters: numParams,
		Parameters:    params,
		IsArrow:       isArrow,
		BaseSlot:      0,
		ArgumentsSlot: -1,
	}
}

// Inspect 返回函数元数据的可读表示 (实现 object.Value 接口)
func (fm *FunctionMetadata) Type() object.ObjectType { return object.COMPILED_FUNCTION_OBJ }
func (fm *FunctionMetadata) Inspect() string {
	return fmt.Sprintf("[Function: %s]", fm.Name)
}
func (fm *FunctionMetadata) IsTruthy() bool { return true }
func (fm *FunctionMetadata) GetProperty(name string) (object.Value, bool) {
	switch name {
	case "name":
		return object.NewString(fm.Name), true
	case "length":
		return object.NewInt(int64(fm.NumParameters)), true
	}
	return nil, false
}
func (fm *FunctionMetadata) SetProperty(name string, val object.Value) {}

// ===== 解构模式 =====

// DestructurePattern 描述解构赋值的模式。
type DestructurePattern struct {
	IsArray  bool                 // true = 数组解构, false = 对象解构
	Bindings []DestructureBinding // 各绑定项
}

// DestructureBinding 描述解构中的一个绑定。
type DestructureBinding struct {
	Name       string // 变量名 (对象解构时为键名)
	Alias      string // 别名 (对象解构的 {key: alias})
	HasDefault bool   // 是否有默认值
	DefaultIdx uint16 // 默认值在常量池中的索引
	IsRest     bool   // 是否是剩余项 (...rest)
}

// NewDestructurePattern 创建解构模式。
func NewDestructurePattern(isArray bool, bindings []DestructureBinding) *DestructurePattern {
	return &DestructurePattern{
		IsArray:  isArray,
		Bindings: bindings,
	}
}

// ===== 反汇编器 =====

// Disassemble 将字节码反汇编为可读字符串。
// offset 是指令在整个字节码中的字节偏移。
func Disassemble(ins Instructions, cp *ConstantPool) string {
	var sb strings.Builder
	pc := 0
	for pc < len(ins) {
		op, operand := ReadInstruction(ins, pc)
		sb.WriteString(fmt.Sprintf("%04d  %-18s", pc, op.Name()))
		if operand != 0 || hasOperand(op) {
			sb.WriteString(fmt.Sprintf(" %d", operand))
			// 如果是常量加载指令，显示常量值
			if cp != nil && isConstantOp(op) {
				val := cp.Get(operand)
				if val != nil {
					sb.WriteString(fmt.Sprintf("  ; %s", val.Inspect()))
				}
			}
		}
		sb.WriteString("\n")
		pc += InstructionSize
	}
	return sb.String()
}

// hasOperand 判断操作码是否有有意义的操作数。
func hasOperand(op Opcode) bool {
	switch op {
	case OP_NOP, OP_NULL, OP_UNDEFINED, OP_TRUE, OP_FALSE,
		OP_POP, OP_DUP, OP_SWAP, OP_DUP2, OP_DUP_BELOW2,
		OP_ADD, OP_SUB, OP_MUL, OP_DIV, OP_MOD, OP_POW, OP_NEG,
		OP_BIT_AND, OP_BIT_OR, OP_BIT_XOR, OP_SHL, OP_SHR, OP_USHR,
		OP_EQ, OP_NOT_EQ, OP_STRICT_EQ, OP_STRICT_NE,
		OP_LT, OP_GT, OP_LTE, OP_GTE, OP_NOT,
		OP_RETURN, OP_RETURN_VOID,
		OP_NEW_OBJECT,
		OP_TEMPLATE_END,
		OP_PUSH_SCOPE, OP_POP_SCOPE,
		OP_THIS,
		OP_YIELD,
		OP_TO_NUMBER:
		return false
	}
	return true
}

// isConstantOp 判断操作码是否引用常量池。
func isConstantOp(op Opcode) bool {
	switch op {
	case OP_CONST:
		return true
	}
	return false
}

package bytecode

import (
	"fmt"
	"sort"
	"strings"

	"github.com/14752222/Gox/object"
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
	case *WithRef:
		bv, ok := b.(*WithRef)
		if !ok {
			return false
		}
		if av.Name != bv.Name || av.LocalFallback != bv.LocalFallback ||
			av.FallbackSlot != bv.FallbackSlot || av.IsConst != bv.IsConst ||
			len(av.Slots) != len(bv.Slots) {
			return false
		}
		for i := range av.Slots {
			if av.Slots[i] != bv.Slots[i] {
				return false
			}
		}
		return true
	}
	return false
}

// ===== with 语句的动态查找引用 =====

// WithRef 是 with 语句体内**一个自由标识符**的编译期描述, 作为常量池条目
// 由 OP_WITH_LOAD / OP_WITH_STORE / OP_WITH_DELETE 的操作数引用。
//
// 运行时按 Slots (内层在前) 依次取出 with 对象, 查 Name 属性; 命中即用之。
// 全部未命中 (含被 Symbol.unscopables 排除) 时按回退语义处理:
//   - LocalFallback 为真: 读/写当前帧的局部槽位 FallbackSlot (可能来自闭包
//     捕获前缀);
//   - LocalFallback 为假: 按名读写全局环境 (与 OP_LOAD_GLOBAL/STORE_GLOBAL 同口径)。
type WithRef struct {
	Name          string
	Slots         []int // with 对象所在局部槽位链, 内层在前
	LocalFallback bool  // true = 回退局部槽; false = 回退全局按名
	FallbackSlot  int   // LocalFallback 为真时有效
	IsConst       bool  // 回退绑定为 const (写入报 TypeError)
}

func (w *WithRef) Type() object.ObjectType                    { return object.OBJECT_OBJ }
func (w *WithRef) Inspect() string                            { return "[WithRef " + w.Name + "]" }
func (w *WithRef) IsTruthy() bool                             { return true }
func (w *WithRef) GetProperty(string) (object.Value, bool)    { return nil, false }
func (w *WithRef) SetProperty(string, object.Value)           {}

// ===== explicit resource management (using / await using) =====

// DisposeResource 描述一个 using 声明的资源记录在**帧局部槽**中的落点,
// 作为常量池条目由 OP_DISPOSE_ADD 的操作数引用 (登记时用), 也作为
// DisposeScope.Resources 的元素 (释放时用)。
//
//   - ValueSlot  资源值所在隐藏局部槽 (登记时写入);
//   - MethodSlot 释放方法所在隐藏局部槽 (登记时写入; 未登记时保持 undefined);
//   - Async      true = await using (取 @@asyncDispose, 释放结果需 await)。
//
// 资源记录不用 VM 全局栈而用帧局部槽, 是因为槽随帧 (含 generator 挂起)
// 一起保存/恢复, 天然支持资源跨 yield 存活。
type DisposeResource struct {
	ValueSlot  int
	MethodSlot int
	Async      bool
}

func (d *DisposeResource) Type() object.ObjectType         { return object.OBJECT_OBJ }
func (d *DisposeResource) Inspect() string                 { return "[DisposeResource]" }
func (d *DisposeResource) IsTruthy() bool                  { return true }
func (d *DisposeResource) GetProperty(string) (object.Value, bool) { return nil, false }
func (d *DisposeResource) SetProperty(string, object.Value)       {}

// DisposeScope 描述一个 using 作用域的全部资源 (按声明顺序), 由
// OP_DISPOSE_EXIT 的操作数引用, 释放时按逆序处理。
type DisposeScope struct {
	Resources []*DisposeResource
}

func (d *DisposeScope) Type() object.ObjectType         { return object.OBJECT_OBJ }
func (d *DisposeScope) Inspect() string                 { return "[DisposeScope]" }
func (d *DisposeScope) IsTruthy() bool                  { return true }
func (d *DisposeScope) GetProperty(string) (object.Value, bool) { return nil, false }
func (d *DisposeScope) SetProperty(string, object.Value)       {}

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
	// IsStrict 报告该函数体是否处于严格模式 (继承外层, 或自身含 "use strict"
	// 指令; 类方法/模块恒严格)。VM 据此决定 this 归一、未声明赋值等运行期语义。
	IsStrict bool
	// IsAsyncGenerator 标识 async generator 的 wrapper。注意: 该 wrapper 的
	// IsAsync=true 但 IsGenerator=false (内层体才是 generator)，单靠两者无法
	// 与普通 async 函数区分 —— 而二者的函数对象 [[Prototype]] 不同
	// (AsyncGeneratorFunction.prototype vs AsyncFunction.prototype)，故显式记。
	IsAsyncGenerator bool
	// LexicalThis 标识"该函数的 this 必须按词法捕获创建帧的 this"。
	// 仅编译器合成的 async/async-generator 内层 generator 需要: wrapper 以
	// 方法/函数形态被调用后, 在同一帧里 OP_FUNCTION 建出内层 generator 并立刻
	// 调用, 内层必须继承 wrapper 帧的 this (它用 OP_CALL 调用, 无接收者)。
	// 用户手写函数不用本标记 (它们的 this 由调用形态决定)。
	LexicalThis bool
	BaseSlot         int             // 函数自身变量的起始槽位 (外层作用域的变量数)
	ArgumentsSlot    int             // arguments 对象槽位 (-1 表示未使用/箭头函数)
	SelfSlot         int             // 命名函数表达式的自引用槽位 (-1 表示无)
	// Positions 是函数体语句的源码位置表 (offset 升序, 可为 nil)。
	// 运行时错误渲染源码帧用 (T05)。
	Positions []SrcPos

	// ParamPrologueEnd 是形参绑定前导段在 Instructions 中的结束偏移
	// (0 表示无前导段)。规范要求生成器函数在**调用时**同步完成形参绑定
	// (默认值求值 / 解构 / 形参作用域建立), 只有函数体推迟到首次 next()。
	// VM 据此在调用时先运行 [0, ParamPrologueEnd) 再冻结为 Generator。
	// 有默认值或解构模式的形参才产生前导段 (纯名字/rest 形参无副作用)。
	ParamPrologueEnd int

	// DeferParams 为真表示形参绑定由内层驱动 (__spawn) 在首次驱动时执行,
	// 而非在调用时冻结。仅用于普通 async 函数的内层 generator: 规范要求
	// async 函数形参求值抛错时返回 **rejected Promise**, 而不像生成器那样
	// 同步抛出 —— 故其形参绑定必须留在 __spawn 驱动路径内以便错误转 rejection。
	DeferParams bool
}

// SrcPos 是语句的源码位置。Offset 是语句首条指令在**所属编译单元**
// (主脚本或函数体) 指令流里的位置 —— 不同编译单元的 offset 空间独立。
// (与 object.SrcPos 结构相同但独立定义: bytecode 不能反向依赖 object
// 之外的包方向约束, 由 vm 层做两套结构的转换。)
type SrcPos struct {
	Offset int
	Line   int
	Col    int
}

// SortSrcPos 按 Offset 升序排序 (编译器从 map 转换时调用)。
func SortSrcPos(s []SrcPos) {
	sort.Slice(s, func(i, j int) bool { return s[i].Offset < s[j].Offset })
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
		SelfSlot:      -1,
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
		OP_POP, OP_DUP, OP_SWAP, OP_DUP2, OP_DUP_BELOW2, OP_ITER_BOUNDARY,
		OP_ITER_CLOSE,
		OP_REQUIRE_OBJECT_COERCIBLE, OP_OBJECT_REST,
		OP_ADD, OP_SUB, OP_MUL, OP_DIV, OP_MOD, OP_POW, OP_NEG,
		OP_BIT_AND, OP_BIT_OR, OP_BIT_XOR, OP_SHL, OP_SHR, OP_USHR,
		OP_EQ, OP_NOT_EQ, OP_STRICT_EQ, OP_STRICT_NE,
		OP_LT, OP_GT, OP_LTE, OP_GTE, OP_NOT,
		OP_RETURN, OP_RETURN_VOID,
		OP_NEW_OBJECT,
		OP_TEMPLATE_END,
		OP_PUSH_SCOPE, OP_POP_SCOPE,
		OP_THIS,
		OP_YIELD, OP_AWAIT,
		OP_TO_NUMBER, OP_TO_PROPERTY_KEY,
		OP_NEW_TARGET,
		OP_NEW_TARGET_MARK:
		return false
	}
	return true
}

// isConstantOp 判断操作码是否引用常量池。
func isConstantOp(op Opcode) bool {
	switch op {
	case OP_CONST, OP_WITH_LOAD, OP_WITH_STORE, OP_WITH_DELETE,
		OP_DISPOSE_ADD, OP_DISPOSE_EXIT:
		return true
	}
	return false
}

// Package bytecode 定义了虚拟机的字节码指令集。
//
// 指令编码: 定长 3 字节 = [opcode(1)] [operand(2, big-endian uint16)]
//
// 简单的 3 字节定长编码使解码非常简单:
//   - 读取 1 字节得到 opcode
//   - 读取 2 字节 (大端序) 得到操作数
//   - PC 前进 3 字节
//
// 操作数用途因指令而异:
//   - 常量加载指令: 操作数 = 常量池索引
//   - 变量指令: 操作数 = 符号表槽位号
//   - 跳转指令: 操作数 = 目标地址 (字节偏移)
//   - 函数调用: 操作数 = 参数个数
//   - 无操作数指令: 操作数 = 0 (忽略)
package bytecode

// Opcode 是操作码类型 (1 字节，0x00-0xFF)。
type Opcode byte

// ===== 指令分区 =====
// 每个分区 16 个操作码，按功能分类。

const (
	// 0x00-0x0F: 栈操作
	OP_NOP   Opcode = 0x00 // 空操作
	OP_POP   Opcode = 0x01 // 弹出栈顶
	OP_DUP   Opcode = 0x02 // 复制栈顶
	OP_SWAP  Opcode = 0x03 // 交换栈顶两个值
	OP_POP_N Opcode = 0x04 // 弹出 N 个值 (操作数 = N)

	// 0x10-0x1F: 常量加载
	OP_CONST     Opcode = 0x10 // 加载常量池[operand]到栈顶
	OP_NULL      Opcode = 0x11 // 加载 null
	OP_UNDEFINED Opcode = 0x12 // 加载 undefined
	OP_TRUE      Opcode = 0x13 // 加载 true
	OP_FALSE     Opcode = 0x14 // 加载 false
	OP_INT        Opcode = 0x15 // 加载小整数 (operand 直接作为 int16 值)

	// 0x20-0x2F: 变量操作
	OP_LOAD        Opcode = 0x20 // 加载局部变量 [slot] 到栈顶
	OP_STORE       Opcode = 0x21 // 将栈顶存入局部变量 [slot]
	OP_STORE_CONST Opcode = 0x22 // 将栈顶存入 const 变量 [slot]
	OP_LOAD_GLOBAL Opcode = 0x23 // 加载全局变量 [name_idx]
	OP_STORE_GLOBAL Opcode = 0x24 // 存入全局变量 [name_idx]
	OP_DECLARE     Opcode = 0x25 // 声明变量 [name_idx] (let)
	OP_DECLARE_CONST Opcode = 0x26 // 声明 const 变量 [name_idx]

	// 0x30-0x3F: 算术运算
	OP_ADD  Opcode = 0x30 // 栈顶两值相加 (弹出 a, b, 推入 a+b)
	OP_SUB  Opcode = 0x31
	OP_MUL  Opcode = 0x32
	OP_DIV  Opcode = 0x33
	OP_MOD  Opcode = 0x34
	OP_POW  Opcode = 0x35
	OP_NEG  Opcode = 0x36 // 一元负号 (弹出 a, 推入 -a)
	OP_BIT_AND Opcode = 0x37
	OP_BIT_OR  Opcode = 0x38
	OP_BIT_XOR Opcode = 0x39
	OP_SHL     Opcode = 0x3A // 左移
	OP_SHR     Opcode = 0x3B // 右移
	OP_USHR    Opcode = 0x3C // 无符号右移
	OP_BIT_NOT Opcode = 0x3D // 按位取反 ~ (弹出 a, 推入 ~a)

	// 0x40-0x4F: 比较和逻辑
	OP_EQ        Opcode = 0x40 // ==
	OP_NOT_EQ    Opcode = 0x41 // !=
	OP_STRICT_EQ Opcode = 0x42 // ===
	OP_STRICT_NE Opcode = 0x43 // !==
	OP_LT        Opcode = 0x44
	OP_GT        Opcode = 0x45
	OP_LTE       Opcode = 0x46
	OP_GTE       Opcode  = 0x47
	OP_NOT       Opcode = 0x48 // 逻辑非 !
	OP_AND       Opcode = 0x49 // 逻辑与 (短路，已由编译器处理)
	OP_OR        Opcode = 0x4A // 逻辑或 (短路，已由编译器处理)
	OP_NULL_COALESCE Opcode = 0x4B // ??

	// 0x50-0x5F: 跳转
	OP_JUMP          Opcode = 0x50 // 无条件跳转到 [target]
	OP_JUMP_IF_TRUE  Opcode = 0x51 // 栈顶为真则跳转 (不弹出)
	OP_JUMP_IF_FALSE Opcode = 0x52 // 栈顶为假则跳转 (不弹出)
	OP_JUMP_IF_NULL  Opcode = 0x53 // 栈顶为 null/undefined 则跳转
	OP_JUMP_IF_NOT_NULL Opcode = 0x55 // 栈顶非 null/undefined 则跳转 (用于 ?? 短路)
	OP_LOOP          Opcode = 0x54 // 循环回跳 (同 JUMP，语义标记)

	// 0x60-0x6F: 函数
	OP_CALL        Opcode = 0x60 // 调用函数，参数个数 [n]
	OP_RETURN      Opcode = 0x61 // 返回栈顶值
	OP_RETURN_VOID Opcode = 0x62 // 返回 undefined
	OP_FUNCTION    Opcode = 0x63 // 创建函数 (operand = 函数元数据索引)
	OP_ARROW_FUNC  Opcode = 0x64 // 创建箭头函数
	OP_CLOSURE     Opcode = 0x65 // 创建闭包 (operand = 函数元数据索引)
	OP_NEW         Opcode = 0x66 // new 构造函数调用
	OP_CALL_SPREAD Opcode = 0x67 // 调用函数 (参数在数组中, operand=参数个数)
	OP_CALL_METHOD Opcode = 0x68 // 调用方法 (栈: [fn, this, args...], operand=参数个数)

	// 0x70-0x7F: 对象和数组
	OP_NEW_ARRAY  Opcode = 0x70 // 创建数组 (operand = 元素个数)
	OP_NEW_OBJECT Opcode = 0x71 // 创建空对象
	OP_GET_PROP   Opcode = 0x72 // 获取属性 obj.prop (operand = 属性名常量索引)
	OP_SET_PROP   Opcode = 0x73 // 设置属性 obj.prop = val
	OP_GET_INDEX  Opcode = 0x74 // 获取索引 obj[idx]
	OP_SET_INDEX  Opcode = 0x75 // 设置索引 obj[idx] = val
	OP_ARRAY_PUSH Opcode = 0x76 // 弹出值，追加到栈顶下方的数组
	OP_ARRAY_SPREAD Opcode = 0x77 // 弹出可迭代对象，展开所有元素追加到栈顶下方的数组
	OP_ARRAY_SLICE Opcode = 0x78 // 弹出 [start, arr], 推入 arr[start:] 新数组 (用于解构 rest)
	OP_OBJECT_SPREAD Opcode = 0x79 // 弹出对象, 复制其自有属性到栈顶下方的对象 (对象展开)
	OP_SET_GETTER Opcode = 0x7A // 设置 getter 属性 (栈: [obj, fn], operand=属性名常量索引)
	OP_SET_SETTER Opcode = 0x7B // 设置 setter 属性 (栈: [obj, fn], operand=属性名常量索引)
	OP_TAGGED_TEMPLATE Opcode = 0x7C // tagged template (栈: [tag, expr1..N], operand=strings数组常量索引)
	OP_DYNAMIC_IMPORT Opcode = 0x7D // 动态 import() (栈顶: 模块路径字符串 → 推入 Promise)
	OP_SET_PROTO Opcode = 0x7E // 设置对象原型 (栈: [obj, parent] → obj.Proto = parent)
	OP_YIELD Opcode = 0x7F // generator 的 yield (弹出表达式值, 暂停帧; 恢复时压入传入值)

	// 0x80-0x8F: 模板字面量
	OP_TEMPLATE_START Opcode = 0x80 // 开始模板拼接 (operand = 部分数)
	OP_TEMPLATE_PART  Opcode = 0x81 // 添加一个部分到模板
	OP_TEMPLATE_END    Opcode = 0x82 // 完成模板拼接，结果入栈

	// 0x90-0x9F: 解构和展开
	OP_DESTRUCTURE  Opcode = 0x90 // 解构赋值 (operand = 解构模式索引)
	OP_SPREAD       Opcode = 0x91 // 展开可迭代对象
	OP_PACK_ARRAY   Opcode = 0x92 // 收集剩余参数到数组
	OP_PACK_OBJECT  Opcode = 0x93 // 收集剩余属性到对象

	// 0xA0-0xAF: 迭代器
	OP_GET_ITERATOR Opcode = 0xA0 // 获取迭代器
	OP_ITER_NEXT    Opcode = 0xA1 // 迭代器前进一步
	OP_FOR_IN_INIT  Opcode = 0xA2 // for...in 初始化键迭代
	OP_FOR_IN_NEXT  Opcode = 0xA3 // for...in 取下一个键
	OP_FOR_IN_END   Opcode = 0xA4 // for...in 清理迭代器状态

	// 0xB0-0xBF: 作用域
	OP_PUSH_SCOPE Opcode = 0xB0 // 进入新块作用域
	OP_POP_SCOPE  Opcode = 0xB1 // 退出块作用域

	// 0xC0-0xCF: 类型操作
	OP_TYPEOF     Opcode = 0xC0 // typeof
	OP_INSTANCEOF Opcode = 0xC1 // instanceof
	OP_THIS       Opcode = 0xC2 // 加载 this
	OP_DELETE     Opcode = 0xC3 // delete 属性
	OP_IN         Opcode = 0xC4 // in 运算符 (key in obj)

	// 0xD0-0xDF: 控制
	OP_BREAK       Opcode = 0xD0 // break (跳转到循环外)
	OP_CONTINUE    Opcode = 0xD1 // continue (跳转到循环头)
	OP_PUSH_TRY    Opcode = 0xD2 // push try handler (operand = catchPC, 0=no catch)
	OP_PUSH_FINALLY Opcode = 0xD3 // set finallyPC on top try entry (operand = finallyPC)
	OP_POP_TRY     Opcode = 0xD4 // pop try handler (try completed normally)
	OP_THROW       Opcode = 0xD5 // throw exception (pop value from stack)
	OP_END_FINALLY Opcode = 0xD6 // end finally block (re-throw pending error if any)

	// 0xE0-0xEF: 模块
	OP_IMPORT  Opcode = 0xE0 // import module (operand = module spec constant index)
	OP_EXPORT  Opcode = 0xE1 // export value (operand = export name constant index)
)

// InstructionSize 是每条指令的字节长度 (固定 3 字节)。
const InstructionSize = 3

// opcodeNames 将操作码映射到可读名称 (用于反汇编)。
var opcodeNames = map[Opcode]string{
	OP_NOP: "NOP", OP_POP: "POP", OP_DUP: "DUP", OP_SWAP: "SWAP", OP_POP_N: "POP_N",
	OP_CONST: "CONST", OP_NULL: "NULL", OP_UNDEFINED: "UNDEFINED",
	OP_TRUE: "TRUE", OP_FALSE: "FALSE", OP_INT: "INT",
	OP_LOAD: "LOAD", OP_STORE: "STORE", OP_STORE_CONST: "STORE_CONST",
	OP_LOAD_GLOBAL: "LOAD_GLOBAL", OP_STORE_GLOBAL: "STORE_GLOBAL", OP_DECLARE: "DECLARE", OP_DECLARE_CONST: "DECLARE_CONST",
	OP_ADD: "ADD", OP_SUB: "SUB", OP_MUL: "MUL", OP_DIV: "DIV",
	OP_MOD: "MOD", OP_POW: "POW", OP_NEG: "NEG",
	OP_BIT_AND: "BIT_AND", OP_BIT_OR: "BIT_OR", OP_BIT_XOR: "BIT_XOR",
	OP_SHL: "SHL", OP_SHR: "SHR", OP_USHR: "USHR", OP_BIT_NOT: "BIT_NOT",
	OP_EQ: "EQ", OP_NOT_EQ: "NOT_EQ", OP_STRICT_EQ: "STRICT_EQ",
	OP_STRICT_NE: "STRICT_NE", OP_LT: "LT", OP_GT: "GT",
	OP_LTE: "LTE", OP_GTE: "GTE", OP_NOT: "NOT",
	OP_AND: "AND", OP_OR: "OR", OP_NULL_COALESCE: "NULL_COALESCE",
	OP_JUMP: "JUMP", OP_JUMP_IF_TRUE: "JUMP_IF_TRUE",
	OP_JUMP_IF_FALSE: "JUMP_IF_FALSE", OP_JUMP_IF_NULL: "JUMP_IF_NULL",
	OP_JUMP_IF_NOT_NULL: "JUMP_IF_NOT_NULL",
	OP_LOOP: "LOOP",
	OP_CALL: "CALL", OP_RETURN: "RETURN", OP_RETURN_VOID: "RETURN_VOID",
	OP_FUNCTION: "FUNCTION", OP_ARROW_FUNC: "ARROW_FUNC", OP_CLOSURE: "CLOSURE",
	OP_NEW: "NEW", OP_CALL_SPREAD: "CALL_SPREAD", OP_CALL_METHOD: "CALL_METHOD",
	OP_NEW_ARRAY: "NEW_ARRAY", OP_NEW_OBJECT: "NEW_OBJECT",
	OP_GET_PROP: "GET_PROP", OP_SET_PROP: "SET_PROP",
	OP_GET_INDEX: "GET_INDEX", OP_SET_INDEX: "SET_INDEX",
	OP_ARRAY_PUSH: "ARRAY_PUSH", OP_ARRAY_SPREAD: "ARRAY_SPREAD", OP_ARRAY_SLICE: "ARRAY_SLICE",
	OP_OBJECT_SPREAD: "OBJECT_SPREAD",
	OP_SET_GETTER: "SET_GETTER", OP_SET_SETTER: "SET_SETTER",
	OP_TAGGED_TEMPLATE: "TAGGED_TEMPLATE",
	OP_DYNAMIC_IMPORT: "DYNAMIC_IMPORT",
	OP_SET_PROTO: "SET_PROTO",
	OP_YIELD: "YIELD",
	OP_TEMPLATE_START: "TEMPLATE_START", OP_TEMPLATE_PART: "TEMPLATE_PART",
	OP_TEMPLATE_END: "TEMPLATE_END",
	OP_DESTRUCTURE: "DESTRUCTURE", OP_SPREAD: "SPREAD",
	OP_PACK_ARRAY: "PACK_ARRAY", OP_PACK_OBJECT: "PACK_OBJECT",
	OP_GET_ITERATOR: "GET_ITERATOR", OP_ITER_NEXT: "ITER_NEXT",
	OP_FOR_IN_INIT: "FOR_IN_INIT", OP_FOR_IN_NEXT: "FOR_IN_NEXT", OP_FOR_IN_END: "FOR_IN_END",
	OP_PUSH_SCOPE: "PUSH_SCOPE", OP_POP_SCOPE: "POP_SCOPE",
	OP_TYPEOF: "TYPEOF", OP_INSTANCEOF: "INSTANCEOF",
	OP_THIS: "THIS", OP_DELETE: "DELETE", OP_IN: "IN",
	OP_BREAK: "BREAK", OP_CONTINUE: "CONTINUE",
	OP_PUSH_TRY: "PUSH_TRY", OP_PUSH_FINALLY: "PUSH_FINALLY",
	OP_POP_TRY: "POP_TRY", OP_THROW: "THROW", OP_END_FINALLY: "END_FINALLY",
	OP_IMPORT: "IMPORT", OP_EXPORT: "EXPORT",
}

// Name 返回操作码的可读名称。
func (op Opcode) Name() string {
	if name, ok := opcodeNames[op]; ok {
		return name
	}
	return "UNKNOWN"
}

// String 返回操作码的字符串表示。
func (op Opcode) String() string { return op.Name() }

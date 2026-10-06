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
	OP_DUP2  Opcode = 0x05 // 复制栈顶两个值 (保持顺序): [a, b] → [a, b, a, b]
	// DUP_BELOW2: [a, b, c] → [c, a, b, c]，把栈顶值塞到下方两个值之下。
	// 后缀 obj.k++ 需要它: 旧值要留在栈底作为表达式结果，而 obj/key/new
	// 必须排在其上才能直接喂给 SET_INDEX。
	OP_DUP_BELOW2 Opcode = 0x06

	OP_ITER_BOUNDARY Opcode = 0x07 // 循环迭代边界: 提交本轮创建的闭包 (for-let per-iteration 绑定)

	// 0x10-0x1F: 常量加载
	OP_CONST     Opcode = 0x10 // 加载常量池[operand]到栈顶
	OP_NULL      Opcode = 0x11 // 加载 null
	OP_UNDEFINED Opcode = 0x12 // 加载 undefined
	OP_TRUE      Opcode = 0x13 // 加载 true
	OP_FALSE     Opcode = 0x14 // 加载 false
	OP_INT       Opcode = 0x15 // 加载小整数 (operand 直接作为 int16 值)

	// 0x20-0x2F: 变量操作
	OP_LOAD          Opcode = 0x20 // 加载局部变量 [slot] 到栈顶
	OP_STORE         Opcode = 0x21 // 将栈顶存入局部变量 [slot]
	OP_STORE_CONST   Opcode = 0x22 // 将栈顶存入 const 变量 [slot]
	OP_LOAD_GLOBAL   Opcode = 0x23 // 加载全局变量 [name_idx]
	OP_STORE_GLOBAL  Opcode = 0x24 // 存入全局变量 [name_idx]
	OP_DECLARE       Opcode = 0x25 // 声明变量 [name_idx] (let)
	OP_DECLARE_CONST Opcode = 0x26 // 声明 const 变量 [name_idx]
	OP_DECLARE_FUNC  Opcode = 0x27 // 声明顶层函数 [name_idx] (允许重定义)
	// OP_STORE_UNDECLARED: 严格模式下对**未声明名字**的赋值 [name_idx]。
	// 语义: 运行期环境中已存在同名绑定则写入 (宿主注入名 self/console/$DONE
	// 等不受影响), 否则抛 ReferenceError —— 与 sloppy 的 OP_STORE_GLOBAL
	// (无条件建全局属性) 相对。编译期由 compiler 在 sym==nil 且 strict 时发射。
	OP_STORE_UNDECLARED Opcode = 0x28

	// 0x30-0x3F: 算术运算
	OP_ADD     Opcode = 0x30 // 栈顶两值相加 (弹出 a, b, 推入 a+b)
	OP_SUB     Opcode = 0x31
	OP_MUL     Opcode = 0x32
	OP_DIV     Opcode = 0x33
	OP_MOD     Opcode = 0x34
	OP_POW     Opcode = 0x35
	OP_NEG     Opcode = 0x36 // 一元负号 (弹出 a, 推入 -a)
	OP_BIT_AND Opcode = 0x37
	OP_BIT_OR  Opcode = 0x38
	OP_BIT_XOR Opcode = 0x39
	OP_SHL     Opcode = 0x3A // 左移
	OP_SHR     Opcode = 0x3B // 右移
	OP_USHR    Opcode = 0x3C // 无符号右移
	OP_BIT_NOT Opcode = 0x3D // 按位取反 ~ (弹出 a, 推入 ~a)

	// 0x40-0x4F: 比较和逻辑
	OP_EQ            Opcode = 0x40 // ==
	OP_NOT_EQ        Opcode = 0x41 // !=
	OP_STRICT_EQ     Opcode = 0x42 // ===
	OP_STRICT_NE     Opcode = 0x43 // !==
	OP_LT            Opcode = 0x44
	OP_GT            Opcode = 0x45
	OP_LTE           Opcode = 0x46
	OP_GTE           Opcode = 0x47
	OP_NOT           Opcode = 0x48 // 逻辑非 !
	OP_AND           Opcode = 0x49 // 逻辑与 (短路，已由编译器处理)
	OP_OR            Opcode = 0x4A // 逻辑或 (短路，已由编译器处理)
	OP_NULL_COALESCE Opcode = 0x4B // ??

	// 0x50-0x5F: 跳转
	OP_JUMP             Opcode = 0x50 // 无条件跳转到 [target]
	OP_JUMP_IF_TRUE     Opcode = 0x51 // 栈顶为真则跳转 (不弹出)
	OP_JUMP_IF_FALSE    Opcode = 0x52 // 栈顶为假则跳转 (不弹出)
	OP_JUMP_IF_NULL     Opcode = 0x53 // 栈顶为 null/undefined 则跳转
	OP_JUMP_IF_NOT_NULL Opcode = 0x55 // 栈顶非 null/undefined 则跳转 (用于 ?? 短路)
	OP_LOOP             Opcode = 0x54 // 循环回跳 (同 JUMP，语义标记)

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
	// OP_CALL_METHOD_SPREAD: 调用方法但实参在数组里 (栈: [fn, this, argsArray])。
	// 用于「转发运行期实参」的场景 —— 隐式 constructor 的 super(...arguments)
	// 与用户手写的 super(...args): argc 是编译期常量, 表达不了运行期实参个数。
	OP_CALL_METHOD_SPREAD Opcode = 0x69

	// OP_EVAL_MARK: 无操作数。标记紧随其后的 OP_CALL / OP_CALL_SPREAD 是一次
	// 「直接 eval」调用 (编译器在被调表达式是标识符 `eval` 时发射)。VM 据此
	// 在本实例上暂存标记, 待被调值弹出后仅当恰为全局 %eval% 内建时, 把
	// **调用者帧生效的 this** 传给 eval 内建 —— 规范 sec-performeval: direct
	// eval 的 this 绑定与调用者一致 (间接 eval (0, eval) 不发射此标记, 按全局
	// eval 处理, this = globalThis)。
	OP_EVAL_MARK Opcode = 0x6A

	// OP_EVAL_MARK_INIT: 无操作数。语义同 OP_EVAL_MARK, 但额外标记本次直接
	// eval 发生在「类字段初始化器」内 —— 除继承调用者 this 外, 还让 eval
	// 内建进入受限模式 (PerformEval 补充早错: 源码含 arguments 引用或 super
	// 调用 → SyntaxError)。间接 eval 不发射此标记, 不受限。
	OP_EVAL_MARK_INIT Opcode = 0x6B

	// 0x70-0x7F: 对象和数组
	OP_NEW_ARRAY       Opcode = 0x70 // 创建数组 (operand = 元素个数)
	OP_NEW_OBJECT      Opcode = 0x71 // 创建空对象
	OP_GET_PROP        Opcode = 0x72 // 获取属性 obj.prop (operand = 属性名常量索引)
	OP_SET_PROP        Opcode = 0x73 // 设置属性 obj.prop = val
	OP_GET_INDEX       Opcode = 0x74 // 获取索引 obj[idx]
	OP_SET_INDEX       Opcode = 0x75 // 设置索引 obj[idx] = val
	OP_ARRAY_PUSH      Opcode = 0x76 // 弹出值，追加到栈顶下方的数组
	OP_ARRAY_SPREAD    Opcode = 0x77 // 弹出可迭代对象，展开所有元素追加到栈顶下方的数组
	OP_ARRAY_SLICE     Opcode = 0x78 // 弹出 [start, arr], 推入 arr[start:] 新数组 (用于解构 rest)
	OP_OBJECT_SPREAD   Opcode = 0x79 // 弹出对象, 复制其自有属性到栈顶下方的对象 (对象展开)
	OP_SET_GETTER      Opcode = 0x7A // 设置 getter 属性 (栈: [obj, fn], operand=属性名常量索引)
	OP_SET_SETTER      Opcode = 0x7B // 设置 setter 属性 (栈: [obj, fn], operand=属性名常量索引)
	OP_TAGGED_TEMPLATE Opcode = 0x7C // tagged template (栈: [tag, expr1..N], operand=strings数组常量索引)
	OP_DYNAMIC_IMPORT  Opcode = 0x7D // 动态 import() (栈顶: 模块路径字符串 → 推入 Promise)
	OP_SET_PROTO       Opcode = 0x7E // 设置对象原型 (栈: [obj, parent] → obj.Proto = parent)
	OP_YIELD           Opcode = 0x7F // generator 的 yield (弹出表达式值, 暂停帧; 恢复时压入传入值)

	// 0x80-0x8F: 模板字面量
	OP_TEMPLATE_START Opcode = 0x80 // 开始模板拼接 (operand = 部分数)
	OP_TEMPLATE_PART  Opcode = 0x81 // 添加一个部分到模板
	OP_TEMPLATE_END   Opcode = 0x82 // 完成模板拼接，结果入栈

	// OP_AWAIT 是 async generator 体内的 await 挂起点。
	// 帧语义与 OP_YIELD 完全相同 (暂停帧, 恢复时压入传入值), 唯一区别是
	// VM 会把 Generator.LastYieldIsAwait 置真, 让异步生成器驱动知道这是
	// 内部挂起点 (等待值后自动恢复) 而非消费者可见的 yield。
	// 仅在 async generator 体内发射; 普通 async 函数/同步 generator/for-await
	// 在非生成器 async 体里仍用 OP_YIELD。
	OP_AWAIT Opcode = 0x8F

	// 0x90-0x9F: 解构和展开
	OP_DESTRUCTURE Opcode = 0x90 // 解构赋值 (operand = 解构模式索引)
	OP_SPREAD      Opcode = 0x91 // 展开可迭代对象
	OP_PACK_ARRAY  Opcode = 0x92 // 收集剩余参数到数组
	OP_PACK_OBJECT Opcode = 0x93 // 收集剩余属性到对象

	// 0xA0-0xAF: 迭代器
	OP_GET_ITERATOR Opcode = 0xA0 // 获取迭代器
	OP_ITER_NEXT    Opcode = 0xA1 // 迭代器前进一步
	OP_FOR_IN_INIT  Opcode = 0xA2 // for...in 初始化键迭代
	OP_FOR_IN_NEXT  Opcode = 0xA3 // for...in 取下一个键
	OP_FOR_IN_END   Opcode = 0xA4 // for...in 清理迭代器状态
	// OP_GET_ASYNC_ITERATOR: 获取异步迭代器 (for await...of 头部)。
	// 弹出可迭代对象, 推进一个「next 可调用」的迭代器:
	// 有 Symbol.asyncIterator 方法的对象调用之; 同步可迭代对象
	// (generator/数组/字符串/[Symbol.iterator]) 原样交给 ASYNC_ITER_NEXT。
	OP_GET_ASYNC_ITERATOR Opcode = 0xA5
	// OP_ASYNC_ITER_NEXT: 异步迭代一步 (for await...of 循环头)。
	// 不弹迭代器: 读栈顶迭代器, 调其 next() (对象) 或驱动 (generator/
	// runtime.Iterator), 把「步进结果」压栈 —— 可能是 Promise (由后续
	// OP_YIELD 交给 __spawn 解析) 或直接的 {value, done} 对象。
	OP_ASYNC_ITER_NEXT Opcode = 0xA6
	// OP_ASYNC_ITER_NEXT_ARG: 同 OP_ASYNC_ITER_NEXT, 但把实参传给 next(v)。
	// 供 async generator 的 yield* 异步委托使用 (AsyncGeneratorYieldDelegate):
	// 消费者 next(v) 的值必须转发给被委托迭代器的 next(v)。
	// 栈: [iter, arg] → [step] (弹出两者, 压入步进结果/其 Promise)。
	OP_ASYNC_ITER_NEXT_ARG Opcode = 0xA7
	// OP_ITER_STEP: 同步迭代一步 (数组解构绑定/赋值用)。
	// 与 OP_ASYNC_ITER_NEXT 同款栈语义 (读栈顶迭代器, 不弹出), 但只产出
	// 同步的 {value, done} 对象 —— 不会出现 Promise 分支 (async 迭代器
	// 不是同步可迭代对象)。覆盖 Generator / runtime.Iterator /
	// 普通对象 (调其 next()) 三种形状。
	// 栈: [iter] → [iter, step]
	OP_ITER_STEP Opcode = 0xA8
	// OP_ITER_CLOSE: IteratorClose (规范 7.4.6)。弹出栈顶迭代器:
	// 取其 return 方法 (Generator 走内建 return), 为 null/undefined 则跳过;
	// 非可调用 → TypeError; 否则以迭代器为 this 调用之, 丢弃返回值并传播
	// 其异常。栈: [iter] → []
	OP_ITER_CLOSE Opcode = 0xA9
	// OP_REQUIRE_OBJECT_COERCIBLE: 对象解构的 RequireObjectCoercible。
	// 读栈顶 (不弹出): null/undefined → TypeError; 其余原样保留。
	// 空对象模式 `{} = null` 也必须在取属性前抛, 故不能靠 GET_INDEX 兜。
	// 栈: [v] → [v]
	OP_REQUIRE_OBJECT_COERCIBLE Opcode = 0xAA
	// OP_OBJECT_REST: 对象解构的 rest 收集 (CopyDataProperties 语义)。
	// 弹出栈顶排除键数组, 读其下方的源值, 新建对象复制源的自有**可枚举**
	// 属性 (字符串键 + Symbol 键, 按 OrdinaryOwnPropertyKeys 顺序, 触发
	// getter), 跳过排除键; 非对象源 (Number/Boolean/Symbol) 得空对象,
	// 字符串按索引字符复制。栈: [src, excluded] → [src, restObj]
	OP_OBJECT_REST Opcode = 0xAB

	// 0xB0-0xBF: 作用域
	OP_PUSH_SCOPE Opcode = 0xB0 // 进入新块作用域
	OP_POP_SCOPE  Opcode = 0xB1 // 退出块作用域
	// OP_WITH_LOAD / OP_WITH_STORE / OP_WITH_DELETE: with 语句体内自由
	// 标识符的动态查找。operand = 常量池索引, 指向 *WithRef —— 它带有
	// 「要查的名字」「with 对象所在局部槽位链 (内层在前)」以及「未命中时的
	// 回退目标 (局部槽位或全局按名)」。这些指令只在 with 体内发射。
	//   LOAD:  查 with 链 → 命中压入其值; 未命中走回退。
	//   STORE: 弹出值 → 查 with 链 → 命中写回该对象; 未命中走回退。
	//   DELETE: 查 with 链 → 命中删除该属性并压 true; 未命中压 true。
	// Symbol.unscopables 排除的属性按「未命中」处理。
	OP_WITH_LOAD   Opcode = 0xB2
	OP_WITH_STORE  Opcode = 0xB3
	OP_WITH_DELETE Opcode = 0xB4
	// OP_WITH_ENTER: 进入 with 前把对象表达式的结果做一次 ToObject
	// (规范 14.11.2 WithStatementEvaluation): null / undefined 抛 TypeError,
	// 其余原样压回。编译器在对象表达式之后、OP_STORE 之前发射一次 ——
	// 保证对象只求值一次、且 with 体从不执行也仍会做类型检查。
	OP_WITH_ENTER Opcode = 0xB5

	// 0xC0-0xCF: 类型操作
	OP_TYPEOF        Opcode = 0xC0 // typeof
	OP_INSTANCEOF    Opcode = 0xC1 // instanceof
	OP_THIS          Opcode = 0xC2 // 加载 this
	OP_DELETE        Opcode = 0xC3 // delete 属性
	OP_IN            Opcode = 0xC4 // in 运算符 (key in obj)
	OP_TYPEOF_GLOBAL Opcode = 0xC5 // typeof 未绑定的标识符 (操作数 = 名字常量索引, 不抛 ReferenceError)

	// 0xD0-0xDF: 控制
	OP_BREAK            Opcode = 0xD0 // break (跳转到循环外)
	OP_CONTINUE         Opcode = 0xD1 // continue (跳转到循环头)
	OP_PUSH_TRY         Opcode = 0xD2 // push try handler (operand = catchPC, 0=no catch)
	OP_PUSH_FINALLY     Opcode = 0xD3 // set finallyPC on top try entry (operand = finallyPC)
	OP_POP_TRY          Opcode = 0xD4 // pop try handler (try completed normally)
	OP_THROW            Opcode = 0xD5 // throw exception (pop value from stack)
	OP_END_FINALLY      Opcode = 0xD6 // end finally block (re-throw pending error if any)
	OP_JUMP_IF_TRUE_POP Opcode = 0xD7 // 条件真则弹出条件值并跳转; 假则不弹继续 (switch case 匹配)
	OP_SET_GETTER_DYN   Opcode = 0xD8 // 动态键 getter: 栈 [obj, fn, key], 键为运行时值 (计算属性名)
	OP_SET_SETTER_DYN   Opcode = 0xD9 // 动态键 setter: 栈 [obj, fn, key]

	// 0xE0-0xEF: 模块
	OP_IMPORT Opcode = 0xE0 // import module (operand = module spec constant index)
	OP_EXPORT Opcode = 0xE1 // export value (operand = export name constant index)
	// OP_EXPORT_BINDING: 导出一个模块顶层的词法绑定 (活绑定)。
	// 不弹栈; operand = 常量池索引, 指向 [导出名(str), 槽位(int)] 数组。
	// VM 记录"导出名 → 顶层帧 Locals[slot]"的读取器, 导入方取用时才读值,
	// 于是导出后的再赋值能被观察到 (ES live binding 语义)。
	OP_EXPORT_BINDING Opcode = 0xE2
	// OP_EXPORT_FROM: 具名再导出 / export * as ns。
	// operand = 常量池索引, 指向 [模块路径(str), 源导出名(str), 目标导出名(str)] 数组;
	// 源导出名为 "*" 时导出整个命名空间对象。记录转发, 读时读取源模块导出槽。
	OP_EXPORT_FROM Opcode = 0xE3
	// OP_EXPORT_STAR: export * from "..."。
	// operand = 模块路径常量索引; 记录星号再导出 (不含 default, 不覆盖本地同名)。
	OP_EXPORT_STAR Opcode = 0xE4

	// 0xF0-0xFF: 显式类型转换
	OP_TO_NUMBER Opcode = 0xF0 // 一元 + (ToNumber): BigInt 抛 TypeError
)

// InstructionSize 是每条指令的字节长度 (固定 3 字节)。
const InstructionSize = 3

// opcodeNames 将操作码映射到可读名称 (用于反汇编)。
var opcodeNames = map[Opcode]string{
	OP_NOP: "NOP", OP_POP: "POP", OP_DUP: "DUP", OP_SWAP: "SWAP", OP_POP_N: "POP_N",
	OP_DUP2: "DUP2", OP_DUP_BELOW2: "DUP_BELOW2", OP_ITER_BOUNDARY: "ITER_BOUNDARY",
	OP_CONST: "CONST", OP_NULL: "NULL", OP_UNDEFINED: "UNDEFINED",
	OP_TRUE: "TRUE", OP_FALSE: "FALSE", OP_INT: "INT",
	OP_LOAD: "LOAD", OP_STORE: "STORE", OP_STORE_CONST: "STORE_CONST",
	OP_LOAD_GLOBAL: "LOAD_GLOBAL", OP_STORE_GLOBAL: "STORE_GLOBAL", OP_DECLARE: "DECLARE", OP_DECLARE_CONST: "DECLARE_CONST",
	OP_STORE_UNDECLARED: "STORE_UNDECLARED",
	OP_DECLARE_FUNC: "DECLARE_FUNC",
	OP_ADD:          "ADD", OP_SUB: "SUB", OP_MUL: "MUL", OP_DIV: "DIV",
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
	OP_JUMP_IF_TRUE_POP: "JUMP_IF_TRUE_POP",
	OP_SET_GETTER_DYN:   "SET_GETTER_DYN", OP_SET_SETTER_DYN: "SET_SETTER_DYN",
	OP_LOOP: "LOOP",
	OP_CALL: "CALL", OP_RETURN: "RETURN", OP_RETURN_VOID: "RETURN_VOID",
	OP_FUNCTION: "FUNCTION", OP_ARROW_FUNC: "ARROW_FUNC", OP_CLOSURE: "CLOSURE",
	OP_NEW: "NEW", OP_CALL_SPREAD: "CALL_SPREAD", OP_CALL_METHOD: "CALL_METHOD",
	OP_CALL_METHOD_SPREAD: "CALL_METHOD_SPREAD",
	OP_EVAL_MARK:          "EVAL_MARK",
	OP_EVAL_MARK_INIT:     "EVAL_MARK_INIT",
	OP_NEW_ARRAY: "NEW_ARRAY", OP_NEW_OBJECT: "NEW_OBJECT",
	OP_GET_PROP: "GET_PROP", OP_SET_PROP: "SET_PROP",
	OP_GET_INDEX: "GET_INDEX", OP_SET_INDEX: "SET_INDEX",
	OP_ARRAY_PUSH: "ARRAY_PUSH", OP_ARRAY_SPREAD: "ARRAY_SPREAD", OP_ARRAY_SLICE: "ARRAY_SLICE",
	OP_OBJECT_SPREAD: "OBJECT_SPREAD",
	OP_SET_GETTER:    "SET_GETTER", OP_SET_SETTER: "SET_SETTER",
	OP_TAGGED_TEMPLATE: "TAGGED_TEMPLATE",
	OP_DYNAMIC_IMPORT:  "DYNAMIC_IMPORT",
	OP_SET_PROTO:       "SET_PROTO",
	OP_YIELD:           "YIELD",
	OP_TEMPLATE_START:  "TEMPLATE_START", OP_TEMPLATE_PART: "TEMPLATE_PART",
	OP_TEMPLATE_END: "TEMPLATE_END",
	OP_AWAIT:        "AWAIT",
	OP_DESTRUCTURE:  "DESTRUCTURE", OP_SPREAD: "SPREAD",
	OP_PACK_ARRAY: "PACK_ARRAY", OP_PACK_OBJECT: "PACK_OBJECT",
	OP_GET_ITERATOR: "GET_ITERATOR", OP_ITER_NEXT: "ITER_NEXT",
	OP_GET_ASYNC_ITERATOR: "GET_ASYNC_ITERATOR", OP_ASYNC_ITER_NEXT: "ASYNC_ITER_NEXT",
	OP_ASYNC_ITER_NEXT_ARG: "ASYNC_ITER_NEXT_ARG",
	OP_ITER_STEP:           "ITER_STEP", OP_ITER_CLOSE: "ITER_CLOSE",
	OP_REQUIRE_OBJECT_COERCIBLE: "REQUIRE_OBJECT_COERCIBLE", OP_OBJECT_REST: "OBJECT_REST",
	OP_FOR_IN_INIT: "FOR_IN_INIT", OP_FOR_IN_NEXT: "FOR_IN_NEXT", OP_FOR_IN_END: "FOR_IN_END",
	OP_PUSH_SCOPE: "PUSH_SCOPE", OP_POP_SCOPE: "POP_SCOPE",
	OP_WITH_LOAD: "WITH_LOAD", OP_WITH_STORE: "WITH_STORE", OP_WITH_DELETE: "WITH_DELETE",
	OP_WITH_ENTER: "WITH_ENTER",
	OP_TYPEOF: "TYPEOF", OP_INSTANCEOF: "INSTANCEOF",
	OP_THIS: "THIS", OP_DELETE: "DELETE", OP_IN: "IN", OP_TYPEOF_GLOBAL: "TYPEOF_GLOBAL",
	OP_BREAK: "BREAK", OP_CONTINUE: "CONTINUE",
	OP_PUSH_TRY: "PUSH_TRY", OP_PUSH_FINALLY: "PUSH_FINALLY",
	OP_POP_TRY: "POP_TRY", OP_THROW: "THROW", OP_END_FINALLY: "END_FINALLY",
	OP_IMPORT: "IMPORT", OP_EXPORT: "EXPORT",
	OP_EXPORT_BINDING: "EXPORT_BINDING", OP_EXPORT_FROM: "EXPORT_FROM", OP_EXPORT_STAR: "EXPORT_STAR",
	OP_TO_NUMBER: "TO_NUMBER",
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

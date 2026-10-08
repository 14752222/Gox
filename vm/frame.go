package vm

import (
	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/object"
)

// sealMark 记录一次迭代边界: 该边界起, slot >= SealFrom 的是"每轮新建"的
// 词法绑定, 只传播给 Epoch >= Epoch 的闭包; slot < SealFrom 的共享绑定不受限。
type sealMark struct {
	SealFrom int
	Epoch    int
}

// setSealMark 登记一次迭代边界。同一 sealFrom 只保留最新 Epoch (旧边界对该
// 槽位而言已被新边界取代 —— 任何更早的闭包都已被正确排除), 条目数 = 循环
// 嵌套层数, 有界。
func (f *Frame) setSealMark(sealFrom, epoch int) {
	for i := range f.SealMarks {
		if f.SealMarks[i].SealFrom == sealFrom {
			f.SealMarks[i].Epoch = epoch
			return
		}
	}
	f.SealMarks = append(f.SealMarks, sealMark{SealFrom: sealFrom, Epoch: epoch})
}

// cellFloor 返回写入 slot 时应遵循的"cell 下界" epoch:
//   - 若存在 sealFrom <= slot 的迭代边界 (该 slot 是每轮新建的尾段), 返回
//     其中最晚边界的 Epoch —— 只有创建于该边界之后的闭包才与写入者共享 cell;
//   - 否则返回 -1, 表示该 slot 是共享绑定 (var / 外层 let / 边界前分配),
//     写入应传播给**全部**已创建闭包。
func (f *Frame) cellFloor(slot int) int {
	floor := -1
	for i := range f.SealMarks {
		if f.SealMarks[i].SealFrom <= slot && f.SealMarks[i].Epoch > floor {
			floor = f.SealMarks[i].Epoch
		}
	}
	return floor
}

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
type Frame struct {	Instructions    bytecode.Instructions
	PC              int
	Locals          []object.Value
	Closure         *object.Closure
	// This 是本帧**生效**的 this 绑定 (已按 sloppy 规则归一)。callClosure 在
	// 装配帧时写入: 箭头帧取其词法捕获的 this, 非箭头帧把 undefined/null 归一
	// 为 globalThis。主帧 (frame.Closure == nil) 保持 nil, 由 OP_THIS 按
	// script/module 决定取值。与 Closure.This (创建时携带的原始绑定) 区分开。
	This            object.Value
	// NewTarget 是本帧的 `new.target` 绑定 (构造目标)。callClosure 装配帧时:
	//   - 构造调用 (OP_NEW): 写被 new 的构造器;
	//   - super() 调用 (OP_NEW_TARGET_MARK 传递): 写调用者帧的 NewTarget
	//     —— 派生类构造器里 super() 父构造器看到的 new.target 是最初被 new 的那个;
	//   - 其余 (普通调用/箭头): nil, 由 OP_NEW_TARGET 归一为 undefined。
	NewTarget       object.Value
	Constants       *bytecode.ConstantPool
	ModifiedSlots   map[int]bool      // 修改过的 slot (用于 popFrame 传播)
	CreatedClosures []*object.Closure // 本帧创建的闭包 (用于 STORE 传播)
	SharedCells     []object.Value    // 外层 binding cell (== 创建它的帧的 Locals 数组)

	// ===== 迭代边界的共享前缀模型 (rUm0q6) =====
	//
	// OP_ITER_BOUNDARY 只应"定版"**每轮新建的词法绑定** (循环变量 + 循环体内
	// 的 let/const), 而 var (函数作用域) 与外层 let 是**单实例**, 所有轮次的
	// 闭包必须共享同一颗 cell。旧实现直接清空 CreatedClosures + 克隆整个 Locals
	// 数组, 把不该定版的共享绑定也一并封死 ⇒ 闭包看到的是各自轮次的快照
	// (for(var x of …) 得到 [1,2,3] 而非规范要求的 [3,3,3])。
	//
	// 现模型: 迭代边界携带 sealFrom (块作用域内首个新槽位), 只把该边界之后的
	// 写入视为"新 cell"; 边界之前的 slot (< sealFrom) 视为共享, 照常传播给
	// 全部已创建闭包。Epoch 每过一次边界 +1; 闭包记录创建时的 Epoch。
	Epoch     int          // 已执行的迭代边界次数
	SealMarks []sealMark   // 各边界的 (sealFrom → 过界后的 Epoch), 按 sealFrom 去重取最新
	StackBase       int               // 进入本帧时栈高度 (返回时截断到此处)

	// PendingGen 非 nil 表示本帧是生成器/异步生成器的"形参前导帧":
	// 帧内指令只覆盖形参绑定前导段 [0, StopPC)。执行到 PC >= StopPC 时
	// 冻结为 Generator (见 runFrom/freezeGenPrologue), 函数体随后才在
	// 首次 next() 时运行。规范要求形参绑定在调用时同步完成。
	PendingGen *object.Generator
	StopPC     int
}

// NewFrame 创建新的调用帧。
//
// Locals 刻意保持零值 (nil): nil 表示"该绑定尚未初始化"，OP_LOAD 读到它时
// 抛 ReferenceError —— 这就是 let/const 的 TDZ (暂时性死区)。
// 若像以前那样预填 undefined，声明前访问会静默得到 undefined。
func NewFrame(ins bytecode.Instructions, constants *bytecode.ConstantPool, numLocals int) *Frame {
	locals := make([]object.Value, numLocals)
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

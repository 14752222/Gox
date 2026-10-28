package vm

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

func (vm *VM) execute() error {
	defer state.swap(vm)()
	return vm.runProtected(func() error {
		err := vm.runFrom(0)
		// 微任务检查点: 脚本顶层同步执行完成后 drain Promise Jobs
		// (即使脚本抛错也先 drain, 与浏览器 "script 结束即 checkpoint" 一致)。
		// job 的执行会动操作数栈, 需保住脚本的完成值 (lastPopped)。
		savedPopped := vm.lastPopped
		vm.drainMicrotasks()
		vm.lastPopped = savedPopped
		return err
	})
}

// runFrom 从指定帧索引开始执行指令循环。
// startFrameIdx: 起始帧索引，循环持续到 vm.frameIdx < startFrameIdx。
// 用于主程序执行 (startFrameIdx=0) 和回调子帧执行。

func (vm *VM) runFrom(startFrameIdx int) error {
	savedBoundary := vm.throwBoundary
	vm.throwBoundary = startFrameIdx
	defer func() { vm.throwBoundary = savedBoundary }()
	for vm.frameIdx >= startFrameIdx {
		frame := vm.currentFrame()

		// 形参前导帧: 前导段执行完毕 (PC 到达 StopPC) 即冻结为 Generator,
		// 函数体留待首次 next()。冻结时把 Generator 放到调用点的结果槽位。
		if frame.PendingGen != nil && frame.PC >= frame.StopPC {
			vm.freezeGenPrologue(frame)
			continue
		}

		// 检查是否到达字节码末尾
		if frame.PC >= len(frame.Instructions) {
			if vm.frameIdx == 0 {
				// 主程序结束: 返回栈顶值 (如果有)
				if vm.stack.Len() > 0 {
					vm.lastPopped = vm.stack.Pop()
				}
				return nil
			}
			// 函数没有显式 return (应该有 OP_RETURN_VOID)
			base := frame.StackBase
			vm.popFrame()
			if vm.stack.Len() > base {
				vm.stack.Truncate(base)
			}
			vm.stack.Push(object.UndefinedSingleton)
			continue
		}

		// 取指
		op := bytecode.ReadOpcode(frame.Instructions, frame.PC)
		operand := bytecode.ReadOperand(frame.Instructions, frame.PC+1)
		vm.dbgOp, vm.dbgPC, vm.dbgFrames = op, frame.PC, vm.frameIdx+1
		if stackTraceOn {
			fmt.Fprintf(os.Stderr, "[trace] f#%d pc=%-5d %-14s depth=%d\n", vm.frameIdx, frame.PC, op.Name(), vm.stack.Len())
		}
		frame.PC += bytecode.InstructionSize

		// 解码-执行
		switch op {
		// ===== 栈操作 =====
		case bytecode.OP_NOP:
			// 空操作
		case bytecode.OP_POP:
			vm.lastPopped = vm.stack.Pop()
		case bytecode.OP_DUP:
			vm.stack.Push(vm.stack.Peek())
		case bytecode.OP_SWAP:
			a := vm.stack.Pop()
			b := vm.stack.Pop()
			vm.stack.Push(a)
			vm.stack.Push(b)
		case bytecode.OP_ITER_BOUNDARY:
			// 迭代边界: 把**本轮迭代新分配的词法绑定** (slot >= sealFrom) 定版,
			// 使本轮创建的闭包保留本轮的值, 后续迭代的写入不再回溯影响它们。
			//
			// sealFrom 由编译器给出 = 循环块作用域内首个新槽位 (循环变量 +
			// 体内 let/const)。**slot < sealFrom 的是共享绑定** —— 函数作用域
			// 的 var (已提升, 槽位在块之前分配) 与外层 let: 所有轮次的闭包必须
			// 共享同一颗 cell, 它们的写入照常传播 (见 OP_STORE 的传播判据)。
			//
			// 实现: 克隆 Locals 数组 (尾部随新数组走, 旧数组留给已定版闭包);
			// 不清空 CreatedClosures —— 保留全部已创建闭包供共享前缀传播,
			// 靠 Epoch/SealMarks 区分"哪些闭包该收到这次写入"。
			sealFrom := int(operand)
			if sealFrom < 0 {
				sealFrom = 0
			}
			if sealFrom > len(frame.Locals) {
				sealFrom = len(frame.Locals)
			}
			old := frame.Locals
			fresh := make([]object.Value, len(old))
			copy(fresh, old)
			frame.Locals = fresh
			// 记录本次边界: slot >= sealFrom 只传播给 epoch >= 新值的闭包。
			frame.Epoch++
			frame.setSealMark(sealFrom, frame.Epoch)
		case bytecode.OP_DUP_BELOW2:
			// [a, b, c] → [c, a, b, c]: 栈顶值复制一份并插到下方两个值之下
			cVal := vm.stack.Pop()
			bVal := vm.stack.Pop()
			aVal := vm.stack.Pop()
			vm.stack.Push(cVal)
			vm.stack.Push(aVal)
			vm.stack.Push(bVal)
			vm.stack.Push(cVal)
		case bytecode.OP_DUP2:
			// 复制栈顶两个值并保持顺序: [a, b] → [a, b, a, b]。
			// 成员复合赋值 (obj.k += v) 需要它: obj/key 各留一份供 SET_INDEX，
			// 同时顶部保留一份供 GET_INDEX 取旧值。
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			vm.stack.Push(a)
			vm.stack.Push(b)
			vm.stack.Push(a)
			vm.stack.Push(b)
		case bytecode.OP_POP_N:
			n := int(operand)
			for i := 0; i < n; i++ {
				vm.stack.Pop()
			}

		// ===== 常量加载 =====
		case bytecode.OP_CONST:
			vm.stack.Push(frame.Constants.Get(operand))
		case bytecode.OP_NULL:
			vm.stack.Push(object.NullSingleton)
		case bytecode.OP_UNDEFINED:
			vm.stack.Push(object.UndefinedSingleton)
		case bytecode.OP_HOLE:
			vm.stack.Push(object.HoleSingleton)
		case bytecode.OP_TRUE:
			vm.stack.Push(object.NewBoolean(true))
		case bytecode.OP_FALSE:
			vm.stack.Push(object.NewBoolean(false))
		case bytecode.OP_INT:
			// operand 直接作为 int16 值
			val := int16(operand)
			vm.stack.Push(object.NewNumber(float64(val)))

		// ===== 变量操作 =====
		case bytecode.OP_LOAD:
			slot := int(operand)
			if slot >= len(frame.Locals) {
				return fmt.Errorf("VM: LOAD slot %d out of range (locals: %d)", slot, len(frame.Locals))
			}
			val := frame.Locals[slot]
			if val == nil {
				// TDZ: let/const 绑定在执行到声明语句前不可访问。
				// 与 ECMAScript 一致抛 ReferenceError，而非返回 undefined。
				if terr := vm.throwJSError(&jsThrow{
					"ReferenceError",
					"Cannot access lexical declaration before initialization",
				}); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(val)
		case bytecode.OP_STORE:
			slot := int(operand)
			val := vm.stack.Pop()
			if slot >= len(frame.Locals) {
				for len(frame.Locals) <= slot {
					frame.Locals = append(frame.Locals, object.UndefinedSingleton)
				}
			}
			frame.Locals[slot] = val
			// 0. 写回共享 binding cell (兄弟闭包与后续调用据此观察到新值)
			if slot < len(frame.SharedCells) {
				frame.SharedCells[slot] = val
			}
			// 1. 更新当前帧闭包的捕获变量 (closure → 同一闭包下次调用)
			if frame.Closure != nil && slot < len(frame.Closure.CapturedLocals) {
				frame.Closure.CapturedLocals[slot] = val
			}
			// 2. 向本帧创建的子闭包传播外层变量修改 (outer → closure)。
			// 判据见 Frame.cellFloor: 共享绑定 (var / 外层 let) 传播给全部
			// 已创建闭包; 迭代边界的尾段 slot 只传播给同一轮次的闭包 (floor)。
			floor := frame.cellFloor(slot)
			for _, c := range frame.CreatedClosures {
				if floor >= 0 && c.IterEpoch < floor {
					continue // 更早轮次的闭包已定版, 不回溯
				}
				if slot < len(c.CapturedLocals) {
					c.CapturedLocals[slot] = val
				}
			}
			// 3. 标记 slot 为已修改 (用于 popFrame 时向上一帧传播)
			if frame.ModifiedSlots == nil {
				frame.ModifiedSlots = make(map[int]bool)
			}
			frame.ModifiedSlots[slot] = true
		case bytecode.OP_STORE_CONST:
			slot := int(operand)
			val := vm.stack.Pop()
			if slot >= len(frame.Locals) {
				for len(frame.Locals) <= slot {
					frame.Locals = append(frame.Locals, object.UndefinedSingleton)
				}
			}
			frame.Locals[slot] = val
			// 0. 写回共享 binding cell
			if slot < len(frame.SharedCells) {
				frame.SharedCells[slot] = val
			}
			// 1. 更新当前帧闭包的捕获变量
			if frame.Closure != nil && slot < len(frame.Closure.CapturedLocals) {
				frame.Closure.CapturedLocals[slot] = val
			}
			// 2. 向子闭包传播 (判据同 OP_STORE: 见 Frame.cellFloor)
			floor := frame.cellFloor(slot)
			for _, c := range frame.CreatedClosures {
				if floor >= 0 && c.IterEpoch < floor {
					continue
				}
				if slot < len(c.CapturedLocals) {
					c.CapturedLocals[slot] = val
				}
			}
			// 3. 标记为已修改
			if frame.ModifiedSlots == nil {
				frame.ModifiedSlots = make(map[int]bool)
			}
			frame.ModifiedSlots[slot] = true
		case bytecode.OP_STORE_CONST_GUARD:
			// 对已声明的局部 const 槽位赋值 —— 无条件拒绝 (可被 try/catch 捕获)。
			// 与 OP_STORE_CONST 的分工: 那条只在**声明期**发射一次 (槽位可能
			// 尚为 TDZ 的 nil), 这条只出现在**赋值位置**, 此时 const 必已初始化,
			// 故直接抛 TypeError。弹出值以保持栈平衡 (编译器在存储前 DUP 保留了
			// 表达式结果, 与 OP_STORE 一致)。
			vm.stack.Pop()
			name := ""
			if s, ok := frame.Constants.Get(operand).(*object.String); ok {
				name = s.Value
			}
			if err := vm.throwNamedError("TypeError", "Assignment to constant variable: %s", name); err != nil {
				return err
			}
		case bytecode.OP_STORE_LEXICAL:
			// 对局部 let 绑定赋值 —— 槽位若仍为 nil 说明尚未求值到声明语句
			// (TDZ), 抛 ReferenceError; 否则与 OP_STORE 同。与 OP_LOAD 的 TDZ
			// 守卫对称。弹出值以保持栈平衡 (编译器在存储前 DUP 保留了表达式结果)。
			slot := int(operand)
			val := vm.stack.Pop()
			if slot < len(frame.Locals) && frame.Locals[slot] == nil {
				if terr := vm.throwJSError(&jsThrow{
					"ReferenceError",
					"Cannot access lexical declaration before initialization",
				}); terr != nil {
					return terr
				}
				continue
			}
			if slot >= len(frame.Locals) {
				for len(frame.Locals) <= slot {
					frame.Locals = append(frame.Locals, object.UndefinedSingleton)
				}
			}
			frame.Locals[slot] = val
			if slot < len(frame.SharedCells) {
				frame.SharedCells[slot] = val
			}
			if frame.Closure != nil && slot < len(frame.Closure.CapturedLocals) {
				frame.Closure.CapturedLocals[slot] = val
			}
			// 向子闭包传播的判据必须与 OP_STORE 一致 (见 Frame.cellFloor):
			// 迭代边界内每轮新建的词法绑定 (slot >= sealFrom) 只传播给同一
			// 轮次的闭包 —— 否则 `for (let j…)` 的 `++j` 会回溯改写更早轮次
			// 已定版闭包捕获的 j, 闭包全部读到终值 (0,1,2,3,4 → 5,5,5,5,5)。
			floor := frame.cellFloor(slot)
			for _, c := range frame.CreatedClosures {
				if floor >= 0 && c.IterEpoch < floor {
					continue // 更早轮次的闭包已定版, 不回溯
				}
				if slot < len(c.CapturedLocals) {
					c.CapturedLocals[slot] = val
				}
			}
			if frame.ModifiedSlots == nil {
				frame.ModifiedSlots = make(map[int]bool)
			}
			frame.ModifiedSlots[slot] = true
		case bytecode.OP_LOAD_GLOBAL:
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				// T2-4 热点优化: 全局读取内联缓存。键 = 常量池里的名字
				// 字符串对象 (同一调用点恒定), 槽 = operand 直映射; 绑定
				// 版本不符时整体作废重填。fib28 剖面: 全局查找
				// (Environment.Get + map hash) 占 CPU ~15%, 每次递归调用
				// 都要按名字哈希查全全局表。
				var val object.Value
				var found bool
				idx := int(operand) & (len(vm.globalICKey) - 1)
				if vm.globalICVersion == vm.globals.Version() && vm.globalICKey[idx] == s {
					val, found = vm.globalICVal[idx], vm.globalICFound[idx]
				} else {
					if vm.globalICVersion != vm.globals.Version() {
						vm.globalICVersion = vm.globals.Version()
						for i := range vm.globalICKey {
							vm.globalICKey[i] = nil
						}
					}
					val, found = vm.globals.Get(s.Value)
					vm.globalICKey[idx] = s
					vm.globalICVal[idx] = val
					vm.globalICFound[idx] = found
				}
				if !found {
					if err := vm.throwNamedError("ReferenceError", "%s is not defined", s.Value); err != nil {
						return err
					}
					continue
				}
				// 全局访问器绑定 (Object.defineProperty(globalThis, "x", {get})):
				// 全局环境记录的绑定值存的是 *object.Accessor, 必须显式调用
				// getter 取回属性值 —— 否则读出来的是 Accessor 对象本身
				// (typeof x 得 "object"、`x ^= 3` 把 Accessor 当左值参与运算)。
				// 与 GlobalObject.GetProperty / unscopablesBlocked 同纪律:
				// getter 是用户闭包, 经回调桥可能抛出, 立即消费。
				if acc, isAcc := val.(*object.Accessor); isAcc {
					if acc.Getter == nil || !object.IsCallable(acc.Getter) {
						vm.stack.Push(object.UndefinedSingleton)
						continue
					}
					res := object.CallFunction(acc.Getter, vm.globalThisValue())
					if err := vm.checkCallbackErr(); err != nil {
						if terr := vm.rethrowBridgeError(err); terr != nil {
							return terr
						}
						continue
					}
					vm.stack.Push(res)
					continue
				}
				vm.stack.Push(val)
			}
		case bytecode.OP_STORE_GLOBAL:
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				// 弹出值 (与 OP_STORE 语义一致)。若需保留表达式结果, 编译器会在存储前 DUP。
				val := vm.stack.Pop()
				if vm.globals.IsConst(s.Value) {
					if err := vm.throwNamedError("TypeError", "Assignment to constant variable: %s", s.Value); err != nil {
						return err
					}
					continue
				}
				if err := vm.storeGlobalBinding(s.Value, val); err != nil {
					return err
				}
			}
		case bytecode.OP_STORE_LEXICAL_GLOBAL:
			// 对脚本顶层 let 绑定赋值 [name_idx]: 尚无绑定即 TDZ, 抛 ReferenceError;
			// 否则按 OP_STORE_GLOBAL 写入。弹出值保持栈平衡 (编译器在存储前 DUP)。
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				val := vm.stack.Pop()
				if _, exists := vm.globals.Get(s.Value); !exists {
					if err := vm.throwJSError(&jsThrow{
						"ReferenceError",
						"Cannot access lexical declaration before initialization",
					}); err != nil {
						return err
					}
					continue
				}
				if vm.globals.IsConst(s.Value) {
					if err := vm.throwNamedError("TypeError", "Assignment to constant variable: %s", s.Value); err != nil {
						return err
					}
					continue
				}
				if err := vm.storeGlobalBinding(s.Value, val); err != nil {
					return err
				}
			}
		case bytecode.OP_STORE_UNDECLARED:
			// 严格模式下对**未声明名字**的赋值 (编译器在 sym==nil 且 strict 时发射)。
			// 与 OP_STORE_GLOBAL 相反: 运行期环境里没有同名绑定就抛 ReferenceError,
			// 而不是隐式建全局属性。宿主注入名 (self/console/$DONE/内建) 都在全局
			// 环境里, 故 `console = ...` 之类照常写入; 只拦真正未声明的名字。
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				val := vm.stack.Pop()
				if vm.globals.IsConst(s.Value) {
					if err := vm.throwNamedError("TypeError", "Assignment to constant variable: %s", s.Value); err != nil {
						return err
					}
					continue
				}
				if _, exists := vm.globals.Get(s.Value); exists {
					if err := vm.storeGlobalBinding(s.Value, val); err != nil {
						return err
					}
				} else {
					if err := vm.throwNamedError("ReferenceError", "%s is not defined", s.Value); err != nil {
						return err
					}
					continue
				}
			}
		case bytecode.OP_DECLARE:
			// 顶层 let/class/import 声明: 弹出值写入全局环境。
			// 与持久化的全局词法绑定冲突时报 SyntaxError (不可被 try/catch 捕获，
			// 与规范的早期错误语义一致)。REPL 每行独立编译，跨行重声明在此发现。
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				val := vm.stack.Pop()
				if err := vm.globals.DeclareGlobal(s.Value, val, false, false); err != nil {
					return err
				}
			}
		case bytecode.OP_DECLARE_CONST:
			// 顶层 const 声明: 弹出值写入全局环境 (const 词法绑定)。
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				val := vm.stack.Pop()
				if err := vm.globals.DeclareGlobal(s.Value, val, true, false); err != nil {
					return err
				}
			}
		case bytecode.OP_DECLARE_FUNC:
			// 顶层函数声明: 允许函数互相重定义，但不可覆盖已有词法声明。
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				val := vm.stack.Pop()
				if err := vm.globals.DeclareGlobal(s.Value, val, false, true); err != nil {
					return err
				}
			}
		case bytecode.OP_DECLARE_VAR:
			// 顶层 var 提升期绑定创建: var 是 globalThis 的自有属性 (不可配置)。
			// 与 let 的 OP_DECLARE 区分 —— 后者只进全局词法环境，不是自有属性。
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				val := vm.stack.Pop()
				if err := vm.globals.DeclareGlobalVar(s.Value, val); err != nil {
					return err
				}
			}

		// ===== 算术运算 =====
		case bytecode.OP_ADD:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			result, err := vm.addValues(a, b)
			if err != nil {
				if terr := vm.throwJSError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(result)
		case bytecode.OP_SUB, bytecode.OP_MUL, bytecode.OP_DIV, bytecode.OP_MOD,
			bytecode.OP_POW, bytecode.OP_BIT_AND, bytecode.OP_BIT_OR, bytecode.OP_BIT_XOR,
			bytecode.OP_SHL, bytecode.OP_SHR, bytecode.OP_USHR:
			// BigInt 参与时走独立分支: JS 禁止 BigInt 与 Number 隐式混合运算。
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			result, err := vm.binaryArithmetic(op, a, b)
			if err != nil {
				if terr := vm.throwJSError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(result)
		case bytecode.OP_NEG, bytecode.OP_BIT_NOT:
			a := vm.stack.Pop()
			result, err := vm.unaryArithmetic(op, a)
			if err != nil {
				if terr := vm.throwJSError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(result)

		// ===== 比较和逻辑 =====
		case bytecode.OP_EQ:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			vm.stack.Push(object.NewBoolean(looseEquals(a, b)))
		case bytecode.OP_NOT_EQ:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			vm.stack.Push(object.NewBoolean(!looseEquals(a, b)))
		case bytecode.OP_STRICT_EQ:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			vm.stack.Push(object.NewBoolean(strictEquals(a, b)))
		case bytecode.OP_STRICT_NE:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			vm.stack.Push(object.NewBoolean(!strictEquals(a, b)))
		case bytecode.OP_LT, bytecode.OP_GT, bytecode.OP_LTE, bytecode.OP_GTE:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			res, err := relationalCompare(op, a, b)
			if err != nil {
				if terr := vm.throwJSError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(object.NewBoolean(res))
		case bytecode.OP_NOT:
			a := vm.stack.Pop()
			vm.stack.Push(object.NewBoolean(object.IsFalsy(a)))
		case bytecode.OP_AND, bytecode.OP_OR:
			// 短路逻辑由编译器处理，这里不应该直接执行
			// 但如果出现，按二元操作处理
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			if op == bytecode.OP_AND {
				vm.stack.Push(object.NewBoolean(a.IsTruthy() && b.IsTruthy()))
			} else {
				vm.stack.Push(object.NewBoolean(a.IsTruthy() || b.IsTruthy()))
			}
		case bytecode.OP_NULL_COALESCE:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			if _, isNull := a.(*object.Null); isNull {
				vm.stack.Push(b)
			} else if _, isUndef := a.(*object.Undefined); isUndef {
				vm.stack.Push(b)
			} else {
				vm.stack.Push(a)
			}

		// ===== 跳转 =====
		case bytecode.OP_JUMP:
			frame.PC = int(operand)
		case bytecode.OP_JUMP_IF_TRUE:
			if vm.stack.Peek().IsTruthy() {
				frame.PC = int(operand)
			}
		case bytecode.OP_JUMP_IF_TRUE_POP:
			// 条件真: 弹出条件值并跳转; 假: 不弹 (由后续 POP 弹), 继续。
			// switch case 匹配专用: 消除"体入口 POP"导致的 fall-through 多弹。
			if vm.stack.Peek().IsTruthy() {
				vm.stack.Pop()
				frame.PC = int(operand)
			}
		case bytecode.OP_JUMP_IF_FALSE:
			if !vm.stack.Peek().IsTruthy() {
				frame.PC = int(operand)
			}
		case bytecode.OP_JUMP_IF_NULL:
			val := vm.stack.Peek()
			if _, isNull := val.(*object.Null); isNull {
				frame.PC = int(operand)
			} else if _, isUndef := val.(*object.Undefined); isUndef {
				frame.PC = int(operand)
			}
		case bytecode.OP_JUMP_IF_NOT_NULL:
			val := vm.stack.Peek()
			_, isNull := val.(*object.Null)
			_, isUndef := val.(*object.Undefined)
			if !isNull && !isUndef {
				frame.PC = int(operand)
			}
		case bytecode.OP_LOOP:
			frame.PC = int(operand)

		// ===== 函数 =====
		case bytecode.OP_FUNCTION, bytecode.OP_ARROW_FUNC:
			fnMeta := frame.Constants.Get(operand)
			meta, ok := fnMeta.(*bytecode.FunctionMetadata)
			if !ok {
				return fmt.Errorf("VM: expected FunctionMetadata at constant %d, got %T", operand, fnMeta)
			}
			// 创建 Closure
			closure := vm.createClosure(meta, frame)
			vm.trackClosure(closure)
			vm.stack.Push(closure)
		case bytecode.OP_CLOSURE:
			// 同 OP_FUNCTION
			fnMeta := frame.Constants.Get(operand)
			meta, ok := fnMeta.(*bytecode.FunctionMetadata)
			if !ok {
				return fmt.Errorf("VM: expected FunctionMetadata at constant %d", operand)
			}
			closure := vm.createClosure(meta, frame)
			vm.trackClosure(closure)
			vm.stack.Push(closure)
		case bytecode.OP_CALL:
			numArgs := int(operand)
			// 弹出函数
			fn := vm.stack.Pop()
			// 上一指令若是 OP_EVAL_MARK, 这一拍就是被标记的直接 eval 调用。
			// 仅当被调确为全局 %eval% 内建时才置受限标志 (其余情况丢弃)。
			vm.consumeEvalMark(fn)
			// 收集参数 (栈上是 arg1, arg2, ..., argN, 逆序弹出)
			// T2-4 热点优化: 参数装进 VM 级复用缓冲。只有 *object.Closure
			// (非生成器) 分支直接使用该缓冲 —— callClosure 会同步把参数
			// 拷进 frame.Locals / rest 数组 / arguments 副本, 不保留切片;
			// 内建与生成器分支各自拷贝一份 (它们可能长期持有 args)。
			if cap(vm.callArgsScratch) < numArgs {
				vm.callArgsScratch = make([]object.Value, numArgs)
			}
			args := vm.callArgsScratch[:numArgs]
			for i := numArgs - 1; i >= 0; i-- {
				args[i] = vm.stack.Pop()
			}

			switch callee := fn.(type) {
			case *object.BuiltinFunction:
				builtinArgs := make([]object.Value, numArgs)
				copy(builtinArgs, args)
				result := callee.Fn(builtinArgs...)
				if result == nil {
					result = object.UndefinedSingleton
				}
				// 返回 *object.Error 的内建函数: 作为异常抛出 (Error 构造器除外)
				if thrown, err := vm.throwIfError(result, callee.ReturnIsValue); thrown {
					if err != nil {
						return err
					}
					continue
				}
				vm.stack.Push(result)
				if err := vm.checkCallbackErr(); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
			case *object.BuiltinMethod:
				// 内建方法被**裸调用** (看板 rYFTlt): `const m = arr.map; m(fn)`
				// 或 `AIP[Symbol.asyncDispose]` 取出来再调。此前本分支缺失,
				// 于是落到 default 报 "xxx is not a function" —— 而同一个值
				// `typeof` 明明是 "function", 极难归因。
				//
				// this 是 **undefined**, 不按 sloppy 归一成 globalThis ——
				// 内建函数是 strict 的, 裸调用拿到的就是 undefined
				// (Node 实测: `var v = Object.prototype.valueOf; v()` 抛
				// TypeError, 而 `v.call(globalThis)` 正常返回)。照 sloppy
				// 归一会让 ToObject 成功, 于是该抛的 TypeError 不抛。
				builtinArgs := make([]object.Value, numArgs)
				copy(builtinArgs, args)
				result := callee.Fn(object.UndefinedSingleton, builtinArgs...)
				if result == nil {
					result = object.UndefinedSingleton
				}
				if thrown, err := vm.throwIfError(result, false); thrown {
					if err != nil {
						return err
					}
					continue
				}
				vm.stack.Push(result)
				if err := vm.checkCallbackErr(); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
			case *object.Closure:
				// generator 函数调用: 不执行函数体, 返回 Generator 对象。
				// 有形参前导段 (默认值/解构) 时, 先建立前导帧 —— 由主循环
				// 同步执行前导段后冻结为 Generator (规范: 形参绑定在调用时完成)。
				// 生成器会长期持有 args (gen.Args), 必须拷出复用缓冲。
				if callee.Fn != nil && callee.Fn.IsGenerator {
					genArgs := make([]object.Value, numArgs)
					copy(genArgs, args)
					args = genArgs
					if genHasPrologue(callee.Fn) {
						if err := vm.setupGenPrologueFrame(callee, args); err != nil {
							if terr := vm.throwJSError(err); terr != nil {
								return terr
							}
							continue
						}
					} else {
						vm.stack.Push(object.NewGenerator(callee, args))
					}
				} else {
					if err := vm.callClosure(callee, args); err != nil {
						if terr := vm.throwJSError(err); terr != nil {
							return terr
						}
						continue
					}
				}

			case *object.Proxy:
				// 代理: 转发到 apply trap (this 为 undefined)
				// trap 回调可长期持有 args, 拷出复用缓冲。
				proxyArgs := make([]object.Value, numArgs)
				copy(proxyArgs, args)
				result, err := vm.proxyApply(callee, object.UndefinedSingleton, proxyArgs)
				if err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				vm.stack.Push(result)

			case object.ObservableState:
				// Rx 单元直接调用: count() 等价 count.value (GetX 语义)
				//
				// computed 的求值函数抛错时 RxValue 会把原始抛出值写进回调桥双槽
				// (看板 rvE6lH 方案 a), 而 GetProperty 没有 error 返回通道 ——
				// 必须在此刻取出重抛, 否则信号残留到之后某个不相干的内建调用点
				// 才被消费, 外层 try 处理器可能早已出栈 (同 rvdPPH 的形态)。
				rv := callee.RxValue()
				if err := vm.checkCallbackErr(); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				vm.stack.Push(rv)

			default:
				if err := vm.throwNamedError("TypeError", "%s is not a function", describeCallee(fn)); err != nil {
					return err
				}
				continue
			}

		case bytecode.OP_RETURN:
			val := vm.stack.Pop()
			base := frame.StackBase
			vm.popFrame()
			// 截断本帧残留的栈值, 防止污染调用方栈
			if vm.stack.Len() > base {
				vm.stack.Truncate(base)
			}
			vm.stack.Push(val)
		case bytecode.OP_RETURN_VOID:
			base := frame.StackBase
			vm.popFrame()
			if vm.stack.Len() > base {
				vm.stack.Truncate(base)
			}
			vm.stack.Push(object.UndefinedSingleton)
		case bytecode.OP_YIELD, bytecode.OP_AWAIT:
			// generator 的 yield / async generator 体内的 await:
			// 弹出表达式值, 保存帧状态, 暂停执行。
			// 恢复时 (genResume) 压入传入的 arg 作为 yield/await 表达式的值。
			val := vm.stack.Pop()
			gen := vm.currentGenerator
			if gen == nil {
				return fmt.Errorf("TypeError: yield outside generator")
			}
			// OP_AWAIT 标记为内部挂起点 (async generator 驱动据此自动恢复);
			// OP_YIELD 是消费者可见的 yield 挂起点。
			gen.LastYieldIsAwait = op == bytecode.OP_AWAIT
			curFrame := vm.currentFrame()
			gen.PC = curFrame.PC // 恢复点: yield 之后的下一条指令
			gen.Locals = curFrame.Locals
			gen.Constants = curFrame.Constants.Constants
			gen.Instructions = curFrame.Instructions
			// 保存属于 generator 帧的 try 处理器条目。挂起期间不能留在全局
			// tryStack 上: 恢复时帧深度可能不同，且无关代码抛出的异常绝不能
			// 被挂起中的 generator 捕获。栈基址/帧索引保存相对值。
			gen.PendingTries = nil
			for len(vm.tryStack) > 0 && vm.tryStack[len(vm.tryStack)-1].frameIdx >= vm.frameIdx {
				te := vm.tryStack[len(vm.tryStack)-1]
				vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]
				gen.PendingTries = append([]object.GenTryEntry{{
					CatchPC:       te.catchPC,
					FinallyPC:     te.finallyPC,
					CatchReturnPC: te.catchReturnPC,
					RelStackBase:  te.stackBase - curFrame.StackBase,
					RelFrameIdx:   te.frameIdx - vm.frameIdx,
					InFinally:     te.inFinally,
					PendingVal:    te.pendingVal,
					PendingReturn: te.pendingReturn,
				}}, gen.PendingTries...)
			}
			// 保存帧栈残留的中间值 (如 2 + (yield 3) 中的 2)
			if vm.stack.Len() > curFrame.StackBase {
				n := vm.stack.Len() - curFrame.StackBase
				gen.SavedStack = make([]object.Value, n)
				for i := 0; i < n; i++ {
					gen.SavedStack[i] = vm.stack.PeekAt(n - 1 - i)
				}
				vm.stack.Truncate(curFrame.StackBase)
			} else {
				gen.SavedStack = nil
			}
			vm.popFrame()
			return &YieldSignal{gen: gen, value: val}
		case bytecode.OP_CALL_SPREAD:
			// 参数在数组中，栈: [args_array, func]
			fn := vm.stack.Pop()
			vm.consumeEvalMark(fn)
			arr := vm.stack.Pop()
			var args []object.Value
			if a, ok := arr.(*object.Array); ok {
				args = a.Elements
			}
			switch callee := fn.(type) {
			case *object.BuiltinFunction:
				result := callee.Fn(args...)
				if result == nil {
					result = object.UndefinedSingleton
				}
				if thrown, err := vm.throwIfError(result, callee.ReturnIsValue); thrown {
					if err != nil {
						return err
					}
					continue
				}
				vm.stack.Push(result)
				if err := vm.checkCallbackErr(); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
			case *object.BuiltinMethod:
				// 同 OP_CALL: 内建方法的 spread 裸调用 (看板 rYFTlt)。
				// this 同为 undefined (内建函数按 strict 语义, 不做 sloppy 归一)。
				result := callee.Fn(object.UndefinedSingleton, args...)
				if result == nil {
					result = object.UndefinedSingleton
				}
				if thrown, err := vm.throwIfError(result, false); thrown {
					if err != nil {
						return err
					}
					continue
				}
				vm.stack.Push(result)
				if err := vm.checkCallbackErr(); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
			case *object.Closure:
				if err := vm.callClosure(callee, args); err != nil {
					if terr := vm.throwJSError(err); terr != nil {
						return terr
					}
					continue
				}
			case *object.Proxy:
				result, err := vm.proxyApply(callee, object.UndefinedSingleton, args)
				if err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				vm.stack.Push(result)
			default:
				if err := vm.throwNamedError("TypeError", "%s is not a function", describeCallee(fn)); err != nil {
					return err
				}
				continue
			}
		case bytecode.OP_CALL_METHOD:
			// 方法调用: 栈 [fn, this, arg1, ..., argN]
			numArgs := int(operand)
			args := make([]object.Value, numArgs)
			for i := numArgs - 1; i >= 0; i-- {
				args[i] = vm.stack.Pop()
			}
			thisVal := vm.stack.Pop()
			fn := vm.stack.Pop()
			if err := vm.invokeWithThis(fn, thisVal, args); err != nil {
				return err
			}
		case bytecode.OP_CALL_METHOD_SPREAD:
			// 方法调用但实参在数组里: 栈 [fn, this, argsArray]。
			// 隐式 constructor 的 super(...arguments) 与用户手写 super(...args) 走这里。
			arr := vm.stack.Pop()
			thisVal := vm.stack.Pop()
			fn := vm.stack.Pop()
			var args []object.Value
			if a, ok := arr.(*object.Array); ok {
				args = a.Elements
			}
			if err := vm.invokeWithThis(fn, thisVal, args); err != nil {
				return err
			}
		case bytecode.OP_EVAL_MARK:
			// 编译器在「直接 eval」调用前发射此指令。只在本 VM 上置
			// 「下一次调用是直接 eval 候选」标记, 无栈效果。真正生效由紧随
			// 其后的 OP_CALL / OP_CALL_SPREAD 判定: 仅当被调恰为全局 %eval%
			// 内建时才把调用者帧的 this 传给 eval, 否则丢弃 —— eval 被局部
			// 变量遮蔽时既不继承 this 也不会泄漏给后续调用。
			//
			// 不带 home 语境的直接 eval 只发此标记: 同时清空 pending 的
			// super home (roiE5Z), 表示「该语境无 home, eval 源码里的
			// super.x 应维持 SyntaxError」。带 home 语境的调用点不发此标记
			// (改发 HOME 家族, 见下), 避免把刚设置的 home 又清掉。
			vm.pendingDirectEval = true
			vm.clearPendingEvalHome()
		case bytecode.OP_EVAL_MARK_INIT:
			// 同 OP_EVAL_MARK, 但额外标记「类字段初始化器内」—— 让 eval 内建
			// 进入受限早错模式 (PerformEval 补充早错)。
			// 注意: 不清 home —— 字段初始化器本身有 home (类 prototype),
			// 该语境由前置的 OP_EVAL_MARK_HOME 标记携带 (roiE5Z)。
			vm.pendingDirectEval = true
			vm.pendingEvalInit = true
		case bytecode.OP_EVAL_MARK_HOME:
			// roiE5Z: 直接 eval 调用点带 super home (实例语境, 类方法 /
			// 字段初始化器 / 箭头继承的外层)。operand = home 类名常量索引。
			vm.pendingDirectEval = true
			vm.clearPendingEvalHome()
			vm.setPendingEvalHomeName(operand, frame)
		case bytecode.OP_EVAL_MARK_HOME_STATIC:
			// roiE5Z: 静态语境的 home (static 方法 / 静态初始化块)。
			// operand = home 类名常量索引。
			vm.pendingDirectEval = true
			vm.clearPendingEvalHome()
			vm.pendingEvalHomeStatic = true
			vm.setPendingEvalHomeName(operand, frame)
		case bytecode.OP_EVAL_MARK_HOME_THIS:
			// roiE5Z: 对象字面量方法的 home —— 无可加载的名字, 运行期取
			// 调用者帧的 this。consumeEvalMark 依 HomeThis 置桥。
			vm.pendingDirectEval = true
			vm.clearPendingEvalHome()
			vm.pendingEvalHomeThis = true
			vm.hasPendingEvalHome = true
		case bytecode.OP_EVAL_MARK_SUPER:
			// roiE5Z: 静态语境且类有 extends —— 父类名 (静态 super base)。
			// 与 HOME_STATIC 成对出现 (HOME_STATIC 在前, 不清后续标记)。
			vm.pendingDirectEval = true
			if s := vm.markName(operand, frame); s != "" {
				vm.pendingEvalSuperName = s
			}
		case bytecode.OP_NEW_TARGET_MARK:
			// 编译器在 super(...) 调用前发射。把当前帧生效的 new.target 记为
			// 「下一次调用要继承的构造目标」, 由紧随其后的方法调用装配父构造器帧
			// 时消费 (callClosure)。无栈效果。非 super 调用不发射, 不继承。
			if f := vm.currentFrame(); f != nil {
				vm.pendingNewTarget = f.NewTarget
				vm.hasPendingNewTarget = true
			}
		case bytecode.OP_NEW_SPREAD:
			// 构造调用但实参在数组里: 栈 [argsArray, callee]。
			// argc 是运行期值 —— 摊平回栈上后交给 OP_NEW 的分派主体统一处理
			// (两条指令唯一的差别就是实参来源)。
			callee := vm.stack.Pop()
			arr := vm.stack.Pop()
			if array, ok := arr.(*object.Array); ok {
				for _, arg := range array.Elements {
					vm.stack.Push(arg)
				}
				operand = uint16(len(array.Elements))
			} else {
				operand = 0
			}
			vm.stack.Push(callee)
			fallthrough
		case bytecode.OP_NEW:
			// new Constructor(args...) — 简化实现
			numArgs := int(operand)
			fn := vm.stack.Pop()
			args := make([]object.Value, numArgs)
			for i := numArgs - 1; i >= 0; i-- {
				args[i] = vm.stack.Pop()
			}
			// 创建新对象
			newObj := object.NewObject()
			// IsConstructor 校验 (规范 EvaluateNew → 检查 IsConstructor(constructor))。
			// 之前只对 Closure 分支做了箭头/生成器拦截；内建函数里没有 [[Construct]]
			// 的那些 (原型方法/getter/命名空间函数) 会被错误地当普通调用执行
			// (Temporal 的 not-a-constructor 族即因此整体失败)。
			if !object.IsConstructor(fn) {
				if err := vm.throwNamedError("TypeError", "%s is not a constructor", describeCallee(fn)); err != nil {
					return err
				}
				continue
			}
			if closure, ok := fn.(*object.Closure); ok {
				// IsConstructor 校验 (ES: EvaluateNew → Construct 前检查
				// IsConstructor(constructor), 无 [[Construct]] 抛 TypeError):
				//   - 箭头函数 (IsArrow): 词法 this, 规范明确不可构造;
				//   - generator / async 函数: 没有 [[Construct]]。
				// 旧实现把 new 箭头函数当普通调用、new generator 返回 Generator,
				// 均与规范不符且掩盖用法错误。
				if closure.IsArrow || (closure.Fn != nil && (closure.Fn.IsGenerator || closure.Fn.IsAsync)) {
					if err := vm.throwNamedError("TypeError", "%s is not a constructor", describeCallee(fn)); err != nil {
						return err
					}
					continue
				}
				// 设置新对象的原型为构造函数的 prototype (支持 instanceof)
				if !closure.IsArrow {
					if cp, _ := closure.GetProperty("prototype"); cp != nil {
						if protoObj, ok := cp.(*object.Object); ok {
							newObj.Proto = protoObj
						} else {
							newObj.Proto = cp
						}
					}
				}
				// 设置 this 为新对象
				//
				// 必须结转 CapturedLocals: 构造函数体里对外层作用域绑定的引用
				// （module 模式下的 `super`、嵌套类里对 enclosing 变量的取值等）
				// 编成 OP_LOAD slot，靠帧装配时拷贝 CapturedLocals 前缀取值。
				// 漏掉它 → 前缀全 nil → 读外层绑定报 TDZ（rIIXSR，2026-10-05）。
				newClosure := &object.Closure{
					Fn:             closure.Fn,
					Env:            closure.Env,
					This:           newObj,
					NewTarget:      closure.NewTarget,
					IsArrow:        closure.IsArrow,
					CapturedLocals: closure.CapturedLocals,
					CreatedAtFrame: closure.CreatedAtFrame,
					IterEpoch:      closure.IterEpoch,
				}
				// 调用构造函数 (同步执行到返回)
				//
				// 错误必须先回收本帧再走抛出流程 (与 OP_CALL_METHOD 的
				// callClosure/runFrom 错误路径同一约定)。此前直接 `return err`:
				//   - 有问题的错误逃过 handleThrow ⇒ 外层 `try { new X() } catch`
				//     抓不到 (rVI6Eb);
				//   - 且不回收构造函数帧 ⇒ 帧栈残留。
				//
				// 构造调用: 被 new 的构造器即本帧 new.target (规范 EvaluateNew →
				// Construct(constructor, argList), newTarget 缺省为 constructor)。
				vm.pendingNewTarget = fn
				vm.hasPendingNewTarget = true
				startIdx := vm.frameIdx + 1
				if err := vm.callClosure(newClosure, args); err != nil {
					vm.unwindFramesTo(startIdx)
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				// 提前抓住构造器帧指针: runFrom 正常结束时会把帧弹出, 但派生类
				// 构造器 super() 换掉的 this 只体现在该帧上 (隐式返回 undefined
				// 时 OP_NEW 需据此取 exotic 实例, 而非普通对象占位)。
				ctorFrame := vm.frames[startIdx]
				if err := vm.runFrom(startIdx); err != nil {
					vm.unwindFramesTo(startIdx)
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				// 派生类构造器 (extends TypedArray 等): super() 走 [[Construct]]
				// 后被替换的 this 挂在构造器帧上, 且派生构造器通常不显式 return
				// (隐式返回 undefined) —— 此时必须采用构造器帧最终的 this, 而非
				// 新建的普通对象占位。否则 new MyU8(3) 只得到普通对象, 原型方法
				// 全部报 "receiver must be a TypedArray"。
				var ctorThis object.Value
				if ctorFrame != nil {
					ctorThis = ctorFrame.This
				}
				// 如果构造函数返回对象，使用返回的对象
				result := vm.stack.Pop()
				if object.IsObjectValue(result) {
					// 使用构造函数返回的对象
					vm.stack.Push(result)
				} else if object.IsTemporalExotic(result) {
					// 派生类 super() 返回 Temporal 内建实例 (如 new (class
					// extends Temporal.Duration)() 里的 super(...)): 规范要求
					// 实例仍是普通 Temporal 对象, 其内部槽寄存在派生类 this 上,
					// 使派生实例的内建方法可用。把内建实例存进帧 this 并采用之。
					target := ctorThis
					if target == nil || target == newObj {
						target = newObj
					}
					if !object.StoreTemporalSlot(target, result) {
						// target 非普通对象 (极端情况) —— 直接返回内建实例。
						vm.stack.Push(result)
					} else {
						vm.stack.Push(target)
					}
				} else if ctorThis != nil && ctorThis != newObj {
					// 派生构造器 super() 已把 this 换成 exotic 实例 (TypedArray),
					// 但派生 ctor 隐式返回 undefined —— 采用帧 this。
					vm.stack.Push(ctorThis)
				} else {
					// 使用 newObj
					vm.stack.Push(newObj)
				}
			} else if builtin, ok := fn.(*object.BuiltinFunction); ok {
				// 仅对 RequiresNew 内建置位"经 new 调用"窗口, 供 Temporal
				// 构造器区分 `new T(...)` 与普通调用 `T(...)` (后者须 TypeError)。
				// 对普通内建 (Date/Map 等) 不置位, 避免影响其内部再调用。
				if builtin.RequiresNew {
					object.EnterConstructCall()
				}
				result := builtin.Fn(args...)
				if builtin.RequiresNew {
					object.LeaveConstructCall()
				}
				if result == nil {
					result = newObj
				}
				// new Boolean/Number/String: 规范构造结果是包装对象
				// (普通对象 + [[PrimitiveValue]] 内部槽)，不是原始值本身。
				switch prim := result.(type) {
				case *object.Boolean:
					result = object.NewPrimitiveObject(prim, object.BooleanProto)
				case *object.Number:
					result = object.NewPrimitiveObject(prim, object.NumberProto)
				case *object.String:
					result = object.NewPrimitiveObject(prim, object.StringProto)
				}
				// 内建构造器返回 Error 对象时抛出异常 (如 new RegExp("[") → SyntaxError)
				// ReturnIsValue 的构造器 (如 Error) 返回的 Error 是值，不抛出
				if errObj, isErr := result.(*object.Error); isErr && !builtin.ReturnIsValue {
					if !vm.handleThrow(errObj) {
						return &ThrowError{Value: errObj}
					}
					continue
				}
				vm.stack.Push(result)
				if err := vm.checkCallbackErr(); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
			} else if proxy, ok := fn.(*object.Proxy); ok {
				// 代理构造: 转发到 construct trap
				result, err := vm.proxyConstruct(proxy, args)
				if err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				vm.stack.Push(result)
			} else {
				if err := vm.throwNamedError("TypeError", "%s is not a constructor", describeCallee(fn)); err != nil {
					return err
				}
				continue
			}

		// ===== 对象和数组 =====
		case bytecode.OP_NEW_ARRAY:
			n := int(operand)
			elements := make([]object.Value, n)
			for i := n - 1; i >= 0; i-- {
				e := vm.stack.Pop()
				if object.IsHole(e) {
					e = nil // 洞在 Elements 里以 nil 表示
				}
				elements[i] = e
			}
			vm.stack.Push(object.NewArray(elements))
		case bytecode.OP_NEW_OBJECT:
			// 对象字面量 / class 基础类的 prototype 对象: 规范里都是
			// "普通对象", [[Prototype]] = %Object.prototype%。
			vm.stack.Push(object.NewPlainObject())
		case bytecode.OP_SET_PROTO:
			// 栈: [obj, parent] → obj.Proto = parent
			parent := vm.stack.Pop()
			obj := vm.stack.Peek()
			switch o := obj.(type) {
			case *object.Object:
				o.Proto = parent
			case *object.Closure:
				// class 派生类的构造器是闭包: `class D extends C` 时规范要求
				// D.[[Prototype]] = C (不只 D.prototype.[[Prototype]] = C.prototype)。
				// 缺这一步会让 D 无法继承 C 的静态成员 / @@species 访问器。
				o.FuncPrototype = parent
			case *object.BuiltinFunction:
				o.FuncPrototype = parent
			}
		case bytecode.OP_GET_PROTO:
			// 栈: [obj] → [proto] (roiE5Z: eval 单元 super.x 的 base 解析)。
			// proto 为 nil (原型链尽头) 时压 null, 与规范
			// Object.getPrototypeOf(Object.prototype) === null 一致。
			obj := vm.stack.Pop()
			proto := protoOf(obj)
			if proto == nil {
				proto = object.NullSingleton
			}
			vm.stack.Push(proto)
		case bytecode.OP_GET_PROP:
			propNameVal := frame.Constants.Get(operand)
			propName := ""
			if s, ok := propNameVal.(*object.String); ok {
				propName = s.Value
			}
			obj := vm.stack.Pop()
			// null/undefined 属性读取抛 TypeError (规范要求；
			// 静默返回 undefined 会掩盖程序错误)
			if obj == object.NullSingleton || obj == object.UndefinedSingleton {
				if err := vm.throwNamedError("TypeError",
					"Cannot read properties of %s (reading '%s')", obj.Inspect(), propName); err != nil {
					return err
				}
				continue
			}
			// Proxy: 转发到 get trap
			if proxy, ok := obj.(*object.Proxy); ok {
				val, err := vm.proxyGet(proxy, propName, obj)
				if err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				vm.stack.Push(val)
				continue
			}
			val, found := obj.GetProperty(propName)
			// getter 可能经回调桥执行用户代码并抛出异常: 立即消费
			// 挂起的错误信号并走抛出流程，避免残留到之后的内建调用点
			if err := vm.checkCallbackErr(); err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
			if !found {
				vm.stack.Push(object.UndefinedSingleton)
			} else {
				vm.stack.Push(val)
			}
		case bytecode.OP_SET_CTOR:
			// 栈: [ctor, proto] → [ctor, proto]
			// 规范 ClassDefinitionEvaluation: 在实例原型上定义 constructor
			// 自有属性, 不可枚举、可写、可配置。不改变栈 (后续 OP_SET_PROP
			// prototype 仍要 [ctor, proto])。
			proto := vm.stack.Peek()
			ctor := vm.stack.PeekAt(1)
			if o, ok := proto.(*object.Object); ok && ctor != nil {
				o.DefineOwnProperty("constructor", object.PropertyDescriptor{
					Value: ctor, Writable: true, Enumerable: false, Configurable: true,
				})
			}
		case bytecode.OP_SET_PROP:
			val := vm.stack.Pop()
			obj := vm.stack.Peek() // 保留对象在栈上
			// 属性名在常量池中
			propNameVal := frame.Constants.Get(operand)
			propName := ""
			if s, ok := propNameVal.(*object.String); ok {
				propName = s.Value
			}
			// null/undefined 属性写入抛 TypeError (规范要求)
			if obj == object.NullSingleton || obj == object.UndefinedSingleton {
				if err := vm.throwNamedError("TypeError",
					"Cannot set properties of %s (setting '%s')", obj.Inspect(), propName); err != nil {
					return err
				}
				continue
			}
			// Proxy: 转发到 set trap
			if proxy, ok := obj.(*object.Proxy); ok {
				if err := vm.proxySet(proxy, propName, val, obj); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				continue
			}
			obj.SetProperty(propName, val)
			// 函数对象: 普通赋值/class 静态字段转可枚举 (静态方法/访问器保持不可枚举)。
			vm.markAssignmentEnumerable(obj, propName, val, vm.inStaticInitFrame(frame))
			// setter 可能经回调桥执行用户代码并抛出异常: 立即消费
			if err := vm.checkCallbackErr(); err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
		// class 方法定义 (规范 MakeMethod: DefineProperty, enumerable: false)。
		// 栈序与 OP_SET_PROP/SET_GETTER/SET_SETTER 一致, 只换描述符的
		// [[Enumerable]]。见 bytecode/opcode.go 里 OP_SET_METHOD 的说明。
		case bytecode.OP_SET_METHOD, bytecode.OP_SET_GETTER_NE, bytecode.OP_SET_SETTER_NE:
			val := vm.stack.Pop()
			obj := vm.stack.Peek() // 保留对象在栈上
			propNameVal := frame.Constants.Get(operand)
			propName := ""
			if s, ok := propNameVal.(*object.String); ok {
				propName = s.Value
			}
			if obj == object.NullSingleton || obj == object.UndefinedSingleton {
				if err := vm.throwNamedError("TypeError",
					"Cannot set properties of %s (setting '%s')", obj.Inspect(), propName); err != nil {
					return err
				}
				continue
			}
			switch o := obj.(type) {
			case *object.Object:
				switch op {
				case bytecode.OP_SET_METHOD:
					o.DefineOwn(propName, object.PropertyDescriptor{
						Value: val, Writable: true, Enumerable: false, Configurable: true,
					})
				case bytecode.OP_SET_GETTER_NE:
					o.DefineBuiltinAccessor(propName, val, nil)
				default:
					o.DefineBuiltinAccessor(propName, nil, val)
				}
			case *object.Closure:
				switch op {
				case bytecode.OP_SET_METHOD:
					o.DefineOwn(propName, object.PropertyDescriptor{
						Value: val, Writable: true, Enumerable: false, Configurable: true,
					})
				default:
					vm.defineClosureAccessor(obj, propName, val, op == bytecode.OP_SET_GETTER_NE)
				}
			default:
				vm.defineClosureAccessor(obj, propName, val, op == bytecode.OP_SET_GETTER_NE)
			}
			// setter 可能经回调桥执行用户代码并抛出异常: 立即消费
			if err := vm.checkCallbackErr(); err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
		// 计算键版: 栈 [obj, fn, key] —— 键在栈顶, 弹两留一。
		case bytecode.OP_SET_METHOD_DYN, bytecode.OP_SET_GETTER_DYN_NE, bytecode.OP_SET_SETTER_DYN_NE:
			key := vm.stack.Pop()
			val := vm.stack.Pop()
			obj := vm.stack.Peek() // 保留对象在栈上
			switch o := obj.(type) {
			case *object.Object:
				if sym, isSym := key.(*object.Symbol); isSym {
					if op == bytecode.OP_SET_METHOD_DYN {
						o.DefineOwnSymbolProperty(sym, object.PropertyDescriptor{
							Value: val, Writable: true, Enumerable: false, Configurable: true,
						})
					} else if op == bytecode.OP_SET_GETTER_DYN_NE {
						o.DefineSymbolAccessorNonEnumerable(sym, val, nil)
					} else {
						o.DefineSymbolAccessorNonEnumerable(sym, nil, val)
					}
					continue
				}
				propName := toJSString(key)
				switch op {
				case bytecode.OP_SET_METHOD_DYN:
					o.DefineOwn(propName, object.PropertyDescriptor{
						Value: val, Writable: true, Enumerable: false, Configurable: true,
					})
				case bytecode.OP_SET_GETTER_DYN_NE:
					o.DefineBuiltinAccessor(propName, val, nil)
				default:
					o.DefineBuiltinAccessor(propName, nil, val)
				}
			case *object.Closure:
				propName := toJSString(key)
				if op == bytecode.OP_SET_METHOD_DYN {
					o.DefineOwn(propName, object.PropertyDescriptor{
						Value: val, Writable: true, Enumerable: false, Configurable: true,
					})
				} else {
					vm.defineClosureAccessor(obj, propName, val, op == bytecode.OP_SET_GETTER_DYN_NE)
				}
			default:
				vm.defineClosureAccessor(obj, toJSString(key), val, op == bytecode.OP_SET_GETTER_DYN_NE)
			}
			if err := vm.checkCallbackErr(); err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
		case bytecode.OP_SET_GETTER, bytecode.OP_SET_SETTER:
			// 栈: [obj, fn]; 设置 getter/setter 属性
			fn := vm.stack.Pop()
			obj := vm.stack.Peek() // 保留对象在栈上
			propNameVal := frame.Constants.Get(operand)
			propName := ""
			if s, ok := propNameVal.(*object.String); ok {
				propName = s.Value
			}
			if o, ok := obj.(*object.Object); ok {
				if op == bytecode.OP_SET_GETTER {
					o.DefineAccessor(propName, fn, nil)
				} else {
					o.DefineAccessor(propName, nil, fn)
				}
			} else {
				vm.defineClosureAccessor(obj, propName, fn, op == bytecode.OP_SET_GETTER)
			}
		case bytecode.OP_SET_GETTER_DYN, bytecode.OP_SET_SETTER_DYN:
			// 动态键访问器 (计算属性名): 栈 [obj, fn, key]
			key := vm.stack.Pop()
			fn := vm.stack.Pop()
			obj := vm.stack.Peek() // 保留对象在栈上
			// Symbol 键 (get [Symbol.x](){}) 必须落进 SymbolProperties 键空间,
			// 不能 toJSString 成 "Symbol(Symbol.x)" 字符串键 —— 否则
			// obj[Symbol.x] / Object.getOwnPropertySymbols 全部查不到。
			if sym, isSym := key.(*object.Symbol); isSym {
				if o, ok := obj.(*object.Object); ok {
					if op == bytecode.OP_SET_GETTER_DYN {
						o.DefineSymbolAccessor(sym, fn, nil)
					} else {
						o.DefineSymbolAccessor(sym, nil, fn)
					}
				}
				continue
			}
			propName := toJSString(key)
			if o, ok := obj.(*object.Object); ok {
				if op == bytecode.OP_SET_GETTER_DYN {
					o.DefineAccessor(propName, fn, nil)
				} else {
					o.DefineAccessor(propName, nil, fn)
				}
			} else {
				vm.defineClosureAccessor(obj, propName, fn, op == bytecode.OP_SET_GETTER_DYN)
			}
		case bytecode.OP_TAGGED_TEMPLATE:
			// 栈: [tag, expr1, expr2, ...]; 先弹出插值表达式, 再弹出 tag
			stringsVal := frame.Constants.Get(operand)
			quasisArr, ok := stringsVal.(*object.Array)
			if !ok {
				return fmt.Errorf("VM: expected strings array constant for tagged template")
			}
			// 运行时重建 strings 数组: 编译期数组的原型 (ArrayProto) 尚未初始化,
			// 此处用 object.NewArray 使原型绑定到 ArrayProto (数组方法可用)
			stringsArr := object.NewArray(append([]object.Value{}, quasisArr.Elements...))
			rawArr := object.NewArray(append([]object.Value{}, quasisArr.Elements...))
			stringsArr.SetProperty("raw", rawArr)

			numExprs := len(stringsArr.Elements) - 1
			args := make([]object.Value, 0, numExprs+1)
			args = append(args, stringsArr)
			// 栈上插值顺序为 [e1, e2, ...]，而弹栈是逆序 (先 eN)。
			// 直接 append 会得到 [eN, ..., e1]，插值顺序整体反转。
			vals := make([]object.Value, numExprs)
			for i := numExprs - 1; i >= 0; i-- {
				vals[i] = vm.stack.Pop()
			}
			args = append(args, vals...)
			fn := vm.stack.Pop()
			// 调用 tag 函数
			switch callee := fn.(type) {
			case *object.BuiltinFunction:
				result := callee.Fn(args...)
				if result == nil {
					result = object.UndefinedSingleton
				}
				vm.stack.Push(result)
			case *object.Closure:
				if err := vm.callClosure(callee, args); err != nil {
					if terr := vm.throwJSError(err); terr != nil {
						return terr
					}
					continue
				}
			default:
				if err := vm.throwNamedError("TypeError", "%s is not a function", describeCallee(fn)); err != nil {
					return err
				}
				continue
			}
		case bytecode.OP_DYNAMIC_IMPORT:
			// 动态 import(): 弹出模块路径, 加载模块, 包装为 resolved Promise
			specVal := vm.stack.Pop()
			spec := toJSString(specVal)
			modExports, err := vm.loadModule(spec)
			if err != nil {
				// 加载失败: reject Promise。模块**语法错误** (解析/编译失败)
				// 的 reject 名是 SyntaxError, 其余 (解析不到模块等) 为 Error。
				// 求值期拒绝 (moduleEvalError) 直接透传原始 JS 值, 保住
				// 模块顶层 throw 的对象类型 (如 TypeError)。
				name := "Error"
				if _, ok := err.(*moduleSyntaxError); ok {
					name = "SyntaxError"
				}
				p := object.NewPromise()
				if me, ok := err.(*moduleEvalError); ok && me.val != nil {
					p.Reject(me.val)
				} else {
					p.Reject(object.NewErrorWithName(name, err.Error()))
				}
				vm.stack.Push(p)
				continue
			}
			if r := modExports.rec; r != nil && r.status != moduleEvaluated {
				// 模块仍在异步求值中 (含 TLA 挂起 / 依赖未就绪): 动态 import 的
				// Promise 等它完成后才 resolve 命名空间 (ContinueDynamicImport)。
				ns := r.completion.Then(object.NewBuiltin("__dyn_import_ns", func(args ...object.Value) object.Value {
					return modExports.buildNamespace(nil)
				}))
				vm.stack.Push(ns)
				continue
			}
			p := object.NewPromise()
			p.Resolve(modExports.buildNamespace(nil))
			vm.stack.Push(p)
		case bytecode.OP_IMPORT_SOURCE:
			// import.source(spec): 规范 16.2.1.7.2 —— SourceTextModuleRecord 的
			// GetModuleSource 恒抛 SyntaxError (source phase import 对文本模块
			// 不可用)。specifier 的 ToString 副作用照常发生, 之后 Promise 以
			// SyntaxError reject —— 用例形如
			// `import.source(obj).catch(e => assert.sameValue(e.name,'SyntaxError'))`。
			specVal := vm.stack.Pop()
			_ = toJSString(specVal)
			p := object.NewPromise()
			p.Reject(object.NewErrorWithName("SyntaxError",
				"source phase imports are not supported for source text modules"))
			vm.stack.Push(p)
		case bytecode.OP_GET_INDEX:
			index := vm.stack.Pop()
			obj := vm.stack.Pop()
			// Proxy: 转发到 get trap (键转为字符串)
			if proxy, ok := obj.(*object.Proxy); ok {
				key := toJSString(index)
				val, err := vm.proxyGet(proxy, key, obj)
				if err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				vm.stack.Push(val)
				continue
			}
			// null/undefined 索引读取抛 TypeError (规范要求)。
			// 注意: 这里**不得**对键做 ToPropertyKey —— 规范 GetValue 先
			// ToObject(base) (null/undefined 立即抛 TypeError), 再 ToPropertyKey。
			// 键只用于拼错误消息, 故仅在它已是 String 时取用, 否则给占位符;
			// 绝不调用 toJSString(index) (会触发对象键的用户 toString)。
			// null[prop] 必须抛 TypeError 且完全不碰 prop
			// (member-expression/computed-reference-null-or-undefined)。
			if obj == object.NullSingleton || obj == object.UndefinedSingleton {
				if err := vm.throwNamedError("TypeError",
					"Cannot read properties of %s (reading '%s')", obj.Inspect(), safeKeyForError(index)); err != nil {
					return err
				}
				continue
			}
			val := vm.getIndex(obj, index)
			// GetProperty 触发 getter 时, getter 是 JS 闭包 ⇒ 经回调桥调用,
			// 抛出的异常只记录在 object.callbackError 上 (GetProperty 无 error
			// 返回), 必须在此刻取出重抛。否则错误会残留到之后某个不相干的内建
			// 调用才被消费 —— 那时外层 try 处理器可能已 POP_TRY 出栈, 异常变成
			// 未捕获 (rvdPPH: 解构成员目标 x.y=... / [x.y]=... 的 setter 抛)。
			if err := vm.checkCallbackErr(); err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(val)
		case bytecode.OP_SET_INDEX:
			val := vm.stack.Pop()
			index := vm.stack.Pop()
			obj := vm.stack.Pop()
			// Proxy: 转发到 set trap
			if proxy, ok := obj.(*object.Proxy); ok {
				if err := vm.proxySet(proxy, toJSString(index), val, obj); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				vm.stack.Push(val)
				continue
			}
			// null/undefined 索引写入抛 TypeError (规范要求)。
			// 同 GET_INDEX: 不得对键做 ToPropertyKey (ToObject(base) 在先),
			// 键仅在已是 String 时用于错误消息 (assignment/
			// target-member-computed-reference-null-or-undefined)。
			if obj == object.NullSingleton || obj == object.UndefinedSingleton {
				if err := vm.throwNamedError("TypeError",
					"Cannot set properties of %s (setting '%s')", obj.Inspect(), safeKeyForError(index)); err != nil {
					return err
				}
				continue
			}
			vm.setIndex(obj, index, val, vm.inStaticInitFrame(frame))
			// SetProperty 触发 setter 时, setter 是 JS 闭包 ⇒ 经回调桥调用,
			// 抛出的异常只记录在 object.callbackError 上 (SetProperty 无 error
			// 返回), 必须在此刻取出重抛。否则错误会残留到之后某个不相干的内建
			// 调用才被消费 —— 那时外层 try 处理器可能已 POP_TRY 出栈, 异常变成
			// 未捕获 (rvdPPH: 解构成员目标 x.y=... / [x.y]=... 的 setter 抛)。
			if err := vm.checkCallbackErr(); err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(val)
		case bytecode.OP_ARRAY_PUSH:
			// 弹出值，追加到栈顶下方的数组
			val := vm.stack.Pop()
			if object.IsHole(val) {
				val = nil // 洞在 Elements 里以 nil 表示
			}
			arr := vm.stack.Peek()
			if a, ok := arr.(*object.Array); ok {
				a.Elements = append(a.Elements, val)
			}
		case bytecode.OP_ARRAY_SPREAD:
			// 弹出可迭代对象，展开所有元素追加到栈顶下方的数组
			iterable := vm.stack.Pop()
			arr := vm.stack.Peek()
			a, ok := arr.(*object.Array)
			if !ok {
				continue
			}
			// 生成器展开: iterable 本身是 Generator (如 [...genFn()])
			if gen, ok := iterable.(*object.Generator); ok {
				// 栈布局与 OP_ITER_NEXT 保持一致: 把 generator 放回栈顶再驱动
				vm.stack.Push(gen)
				for {
					val, done, err := vm.genResume(gen, object.UndefinedSingleton)
					if err != nil {
						return err
					}
					if done {
						break
					}
					a.Elements = append(a.Elements, val)
				}
				vm.stack.Pop() // 弹出 generator，留下数组
				continue
			}
			// 对象实现了 [Symbol.iterator]: VM 层预解析 (生成器结果只能由 VM 驱动)。
			// 解析出 Generator 时走上面的驱动循环 —— GetIterable 纯 Go 层接管不了。
			if resolved, ok, err := vm.resolveSymbolIterator(iterable); err != nil {
				return err
			} else if gen, isGen := resolved.(*object.Generator); ok && isGen {
				vm.stack.Push(gen)
				for {
					val, done, err := vm.genResume(gen, object.UndefinedSingleton)
					if err != nil {
						return err
					}
					if done {
						break
					}
					a.Elements = append(a.Elements, val)
				}
				vm.stack.Pop() // 弹出 generator，留下数组
				continue
			} else if objIter, isObj := resolved.(*object.Object); ok && isObj {
				// 用户自建迭代器对象: 同步调 next() 逐步收集。
				for {
					step, err := vm.syncIterStep(objIter)
					if err != nil {
						if terr := vm.rethrowBridgeError(err); terr != nil {
							return terr
						}
						break
					}
					stepObj, isStep := step.(*object.Object)
					if !isStep {
						break
					}
					doneVal, _ := stepObj.GetProperty("done")
					if doneVal != nil && doneVal.IsTruthy() {
						break
					}
					val, _ := stepObj.GetProperty("value")
					if val == nil {
						val = object.UndefinedSingleton
					}
					a.Elements = append(a.Elements, val)
				}
				continue
			} else if ok {
				// resolveSymbolIterator 对 JS 层迭代器 (arr.values() /
				// Array.prototype[@@iterator]() 的返回值) 已经包好了
				// *runtime.Iterator —— 直接驱动即可, 不要再丢回
				// runtime.GetIterable: 后者只认 JS 值, *runtime.Iterator 不在
				// 其中, 恒返回 false 并误报 "is not iterable"。
				//
				// 这条路径此前走不到 (内建原型上没有 @@iterator, 数组展开
				// 永远落在下面的 GetIterable 快路径), 装配 @@iterator 之后
				// 才第一次暴露 —— 是既有 bug, 不是新引入的。
				if it, isIter := resolved.(*runtime.Iterator); isIter {
					for {
						val, done := it.Next()
						if done {
							break
						}
						a.Elements = append(a.Elements, val)
					}
					continue
				}
				iterable = resolved
			}
			iter, hasIter := runtime.GetIterable(iterable)
			if !hasIter {
				if err := vm.throwNamedError("TypeError", "%s is not iterable", iterable.Inspect()); err != nil {
					return err
				}
				continue
			}
			for {
				val, done := iter.Next()
				if done {
					break
				}
				a.Elements = append(a.Elements, val)
			}
		case bytecode.OP_ARRAY_SLICE:
			// 栈: [arr, start] → 弹出 start, arr, 推入 arr[start:] 新数组 (解构 rest)
			start := int(toNumber(vm.stack.Pop()))
			src := vm.stack.Pop()
			a, ok := src.(*object.Array)
			if !ok {
				vm.stack.Push(object.NewArray(nil))
				continue
			}
			if start < 0 {
				start = 0
			}
			if start > len(a.Elements) {
				start = len(a.Elements)
			}
			rest := make([]object.Value, len(a.Elements)-start)
			copy(rest, a.Elements[start:])
			vm.stack.Push(object.NewArray(rest))
		case bytecode.OP_OBJECT_SPREAD:
			// 弹出源对象, 复制其可枚举自有属性到栈顶下方的对象 (CopyDataProperties)。
			src := vm.stack.Pop()
			dst := vm.stack.Peek()
			to, ok := dst.(*object.Object)
			if !ok {
				continue
			}
			if so, ok := src.(*object.Object); ok {
				for _, k := range so.EnumerableKeys() {
					if desc, exists := so.Properties[k]; exists {
						to.SetProperty(k, desc.Value)
					}
				}
			} else if arr, ok := src.(*object.Array); ok {
				for i, v := range arr.Elements {
					to.SetProperty(fmt.Sprintf("%d", i), v)
				}
			} else if store, ok := src.(object.OwnPropertyStore); ok {
				// 函数类 (Closure/BuiltinFunction/BuiltinMethod) 等: 走统一接口。
				for _, k := range store.EnumerableOwnKeys() {
					if v, found := src.(interface {
						GetProperty(string) (object.Value, bool)
					}).GetProperty(k); found {
						to.SetProperty(k, v)
					}
				}
			}

		// ===== 模板字面量 =====
		case bytecode.OP_TEMPLATE_START:
			// 打开一层分段收集器 (operand = 总部分数，实际用不上:
			// 各段值已按 quasi → PART / 表达式 → PART 的顺序各自入栈后被消费)
			vm.tplParts = append(vm.tplParts, nil)
		case bytecode.OP_TEMPLATE_PART:
			// 栈顶一定是当前模板的某一段 (quasi 常量或表达式结果)，
			// 转为字符串后只进收集器，不再碰栈 —— 这是与旧实现的本质区别:
			// 旧代码向下 peek 并弹走"看起来像字符串"的值，会把模板外的
			// 操作数 (如二元加法的左操作数) 误并进模板，最终造成栈下溢。
			val := vm.stack.Pop()
			str := toJSString(val)
			depth := len(vm.tplParts)
			if depth == 0 {
				// 防御: 字节码不完整时退化为直接把段值压栈
				vm.stack.Push(object.NewString(str))
				continue
			}
			vm.tplParts[depth-1] = append(vm.tplParts[depth-1], object.NewString(str))
		case bytecode.OP_TEMPLATE_END:
			// 拼接本层所有段，压回操作数栈
			depth := len(vm.tplParts)
			if depth == 0 {
				continue
			}
			parts := vm.tplParts[depth-1]
			vm.tplParts = vm.tplParts[:depth-1]
			total := 0
			for _, p := range parts {
				if s, ok := p.(*object.String); ok {
					total += len(s.Value)
				}
			}
			if total > maxStringLength {
				if terr := vm.throwJSError(&jsThrow{
					Name: "RangeError", Message: "Invalid string length"}); terr != nil {
					return terr
				}
				continue
			}
			var sb strings.Builder
			for _, p := range parts {
				if s, ok := p.(*object.String); ok {
					sb.WriteString(s.Value)
				}
			}
			vm.stack.Push(object.NewString(sb.String()))

		// ===== 解构和展开 =====
		case bytecode.OP_DESTRUCTURE:
			// 简化实现: 后续完善
		case bytecode.OP_SPREAD:
			// 简化实现: 后续完善
		case bytecode.OP_PACK_ARRAY:
			// 收集剩余参数到数组
			n := int(operand)
			elements := make([]object.Value, n)
			for i := n - 1; i >= 0; i-- {
				elements[i] = vm.stack.Pop()
			}
			vm.stack.Push(object.NewArray(elements))
		case bytecode.OP_PACK_OBJECT:
			// 简化实现

		// ===== 迭代器 =====
		case bytecode.OP_GET_ITERATOR:
			val := vm.stack.Pop()
			// generator 对象本身可作为迭代器 (由 OP_ITER_NEXT 驱动)
			if _, ok := val.(*object.Generator); ok {
				vm.stack.Push(val)
				continue
			}
			// 普通对象实现了 [Symbol.iterator]: 在 VM 层调用该方法 ——
			// 生成器方法的返回值 (*object.Generator) 只能由 VM 帧驱动,
			// runtime.GetIterable 的 Go 层适配器接管不了。
			if resolved, ok, err := vm.resolveSymbolIterator(val); err != nil {
				// 抛错已被 try/catch 处理器接住 (PC 已跳 catchPC): 不能继续
				// 往下走 runtime.GetIterable 兜底, 否则会在 catch 帧上多压一个
				// 迭代器导致栈失衡 —— 仅 continue 重新取指。
				if err == errHandledThrow {
					continue
				}
				// Symbol.iterator 方法自身抛错: 必须走抛出流程, 否则外层
				// try/catch 抓不到 (解构/for-of 的 iter-get-err 用例)。
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			} else if ok {
				vm.stack.Push(resolved)
				continue
			}
			iter, ok := runtime.GetIterable(val)
			if !ok {
				if err := vm.throwNamedError("TypeError", "%s is not iterable", val.Inspect()); err != nil {
					return err
				}
				continue
			}
			vm.stack.Push(iter)
		case bytecode.OP_ITER_NEXT:
			// 不弹出迭代器，只读取栈顶的迭代器并获取下一个值
			iter := vm.stack.Peek()
			// generator: 通过 VM 驱动前进一步
			if gen, ok := iter.(*object.Generator); ok {
				val, done, err := vm.genResume(gen, object.UndefinedSingleton)
				if err != nil {
					return err
				}
				if done {
					vm.stack.Push(object.UndefinedSingleton)
				} else {
					vm.stack.Push(val)
				}
				continue
			}
			if it, ok := iter.(*runtime.Iterator); ok {
				val, done := it.Next()
				if done {
					vm.stack.Push(object.UndefinedSingleton)
				} else {
					vm.stack.Push(val)
				}
			} else if it, ok := iter.(*object.Object); ok {
				// 用户自建迭代器 ({ next }): 调 next(), 读 {value, done}。
				// 只做同步调用 —— 返回 Promise 的 async 迭代器不该出现在
				// 同步 for-of / 解构路径上。
				nextFn, found := it.GetProperty("next")
				if !found || !object.IsCallable(nextFn) {
					if err := vm.throwNamedError("TypeError", "iterator has no callable next()"); err != nil {
						return err
					}
					continue
				}
				res, err := vm.callFunction(nextFn, it, nil)
				if err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				// 规范 7.4.2 step 2: next() 返回值必须是 Object, 否则 TypeError
				// (yield* / for-of 的 star-rhs-iter-nrml-next-call-non-obj)。
				if res == nil || !object.IsObjectLike(res) {
					if err := vm.throwNamedError("TypeError", "iterator next() must return an object"); err != nil {
						return err
					}
					continue
				}
				if step, ok := res.(*object.Object); ok {
					doneVal, _ := step.GetProperty("done")
					if doneVal != nil && doneVal.IsTruthy() {
						vm.stack.Push(object.UndefinedSingleton)
					} else {
						val, _ := step.GetProperty("value")
						if val == nil {
							val = object.UndefinedSingleton
						}
						vm.stack.Push(val)
					}
					continue
				}
				vm.stack.Push(object.UndefinedSingleton)
			} else {
				vm.stack.Push(object.UndefinedSingleton)
			}
		case bytecode.OP_ITER_STEP:
			// 同步迭代一步: 读栈顶迭代器 (不弹出), 压入 {value, done} 步进对象。
			// operand = 迭代器隐藏槽号。next() 抛异常 (abrupt) 时先清空槽,
			// 等价 iteratorRecord.[[done]] = true (规范 7.4.6 IteratorStepValue
			// 步骤 2) —— 异常路径的 IteratorClose 收尾据此跳过 return()
			// (thrw-close-skip 一族)。
			iter := vm.stack.Peek()
			step, err := vm.syncIterStep(iter)
			if err != nil {
				if slot := int(operand); slot < len(frame.Locals) {
					frame.Locals[slot] = object.UndefinedSingleton
				}
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(step)
		case bytecode.OP_ITER_CLOSE:
			// IteratorClose: 弹出迭代器, 有 return 方法就调它 (Generator 走
			// 内建 return), 丢弃返回值。无 return → 跳过; 非可调用 → TypeError。
			// return 自身抛错照常传播 (是否吞掉由调用点的 try 决定)。
			iter := vm.stack.Pop()
			if err := vm.iteratorClose(iter); err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
		case bytecode.OP_ITER_CLOSE_ABRUPT:
			// 控制转移穿出路径的 IteratorClose: 与 OP_ITER_CLOSE 同款收尾,
			// 但 close 自身的错按「挂起完成的类型」决定吞还是传 (见
			// pendingCompletionIsThrow 与 opcode 注释)。栈: [] → []
			// 直读隐藏槽 (不经过栈): finally 入口已把栈截断到条目栈基,
			// 走栈需要先补一次 LOAD, 而槽内值在挂起期间始终有效。
			var iterVal object.Value
			if int(operand) < len(frame.Locals) {
				iterVal = frame.Locals[operand]
			}
			if err := vm.iteratorClose(iterVal); err != nil {
				if vm.pendingCompletionIsThrow() {
					// 原完成是 throw ⇒ 丢弃 close 的错 (规范 7.4.6 步骤 6)。
					continue
				}
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
		case bytecode.OP_DISPOSE_ADD:
			// using 资源登记 (见 bytecode.DisposeResource 与 AddDisposableResource)。
			res, _ := frame.Constants.Get(operand).(*bytecode.DisposeResource)
			v := vm.stack.Pop()
			if err := vm.disposeAdd(res, v); err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
		case bytecode.OP_DISPOSE_EXIT:
			// using 作用域退出: 逆序释放 (见 bytecode.DisposeScope)。
			scope, _ := frame.Constants.Get(operand).(*bytecode.DisposeScope)
			if err := vm.disposeExit(scope); err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
		case bytecode.OP_REQUIRE_OBJECT_COERCIBLE:
			// RequireObjectCoercible: 栈顶 null/undefined → TypeError (不弹出)。
			v := vm.stack.Peek()
			if isNullish(v) {
				if err := vm.throwNamedError("TypeError",
					"Cannot destructure '%s' as it is %s.", v.Inspect(), v.Inspect()); err != nil {
					return err
				}
			}
		case bytecode.OP_OBJECT_REST:
			// 对象 rest: [src, excluded] → [src, restObj]
			excluded := vm.stack.Pop()
			src := vm.stack.Peek()
			rest, err := vm.objectRest(src, excluded)
			if err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(rest)
		case bytecode.OP_FOR_IN_INIT:
			// 弹出对象, 推入对象键迭代器
			val := vm.stack.Pop()
			switch v := val.(type) {
			case *object.Object:
				vm.stack.Push(runtime.NewObjectKeysIterator(v))
			case *object.Array:
				// for...in 遍历数组的索引键 ("0", "1", ...)
				keys := make([]string, len(v.Elements))
				for i := range v.Elements {
					keys[i] = strconv.Itoa(i)
				}
				iter := runtime.NewObjectKeysIteratorWithKeys(v, keys)
				vm.stack.Push(iter)
			default:
				// 函数类 (Closure/BuiltinFunction/BuiltinMethod) 等实现
				// OwnPropertyStore 的对象: 用自有可枚举键 (排除 name/length/
				// prototype 与内置/class 静态方法)。其余值 (原始值等) 空迭代。
				if store, ok := val.(object.OwnPropertyStore); ok {
					vm.stack.Push(runtime.NewObjectKeysIteratorWithKeys(val, store.EnumerableOwnKeys()))
				} else {
					vm.stack.Push(runtime.NewObjectKeysIterator(object.NewObject()))
				}
			}
		case bytecode.OP_FOR_IN_NEXT:
			// 读取栈顶迭代器, 取下一个键
			iter := vm.stack.Peek()
			if it, ok := iter.(*runtime.Iterator); ok {
				val, done := it.Next()
				if done {
					vm.stack.Push(object.UndefinedSingleton)
				} else {
					vm.stack.Push(val)
				}
			} else {
				vm.stack.Push(object.UndefinedSingleton)
			}
		case bytecode.OP_FOR_IN_END:
			// 弹出迭代器 (清理栈)
			vm.stack.Pop()

		case bytecode.OP_GET_ASYNC_ITERATOR:
			// for await...of 头部: 弹出可迭代对象, 推进「next 可调用」的迭代器。
			// 优先级与规范一致: Symbol.asyncIterator 方法 > 同步可迭代形状
			// (generator 本体 / runtime.Iterator / 带 next 的对象 —— 数组、
			// 字符串等在 stdlib 转换后都是这些形状)。
			val := vm.stack.Pop()
			// 0) 本身就是异步生成器: 它即自己的 [Symbol.asyncIterator]()
			//    (返回自身), 无需再解析, 直接作为迭代器交给 ASYNC_ITER_NEXT。
			if _, isAG := val.(*object.AsyncGenerator); isAG {
				vm.stack.Push(val)
				continue
			}
			// 1) [Symbol.asyncIterator]() —— 只在 VM 层调用 (返回值可能是
			//    generator/闭包, Go 侧适配器驱动不了)。
			if resolved, ok, err := vm.resolveAsyncSymbolIterator(val); err != nil {
				return err
			} else if ok {
				vm.stack.Push(resolved)
				continue
			}
			// 2) 同步形状原样交给 ASYNC_ITER_NEXT (它统一处理 next 调用)。
			// 「裸 next 对象」是 Gox 的非规范扩展 (for-await 自建迭代器),
			// 判定用 hasNextKey (非触发式) —— GetProperty 会多触发一次
			// getter, 破坏属性访问顺序 (r6e5qp)。
			if _, isGen := val.(*object.Generator); isGen {
				vm.stack.Push(val)
				continue
			}
			if _, isIter := val.(*runtime.Iterator); isIter {
				vm.stack.Push(val)
				continue
			}
			if o, isObj := val.(*object.Object); isObj && hasNextKey(o) {
				// 包 Async-from-Sync wrapper: next method 懒缓存 (V8 实测行为),
				// throw/return 每次 GetMethod —— 见 wrapSyncIterForAsync。
				// 裸 next 对象按「手写迭代器」处理 (fromSync=false): 值不解包。
				vm.stack.Push(vm.wrapSyncIterForAsync(o, false))
				continue
			}
			// 3) 同步可迭代 (数组/字符串/Map 等, 规范允许 for-await 与 yield*
			// 委托消费): [Symbol.iterator] 解析 (generator 结果只能 VM 驱动)
			// 或 runtime.GetIterable 适配, 转成迭代器形状。JS 迭代器对象再包
			// Async-from-Sync wrapper (next method 懒缓存 —— V8 实测第二次
			// next 不再触发 get next; r6e5qp test262 yield-star-sync-next)。
			if resolved, ok, err := vm.resolveSymbolIterator(val); err != nil {
				return err
			} else if ok {
				// 同步源 (CreateAsyncFromSyncIterator): 委托时步进值须解包。
				vm.stack.Push(vm.wrapSyncIterForAsync(resolved, true))
				continue
			}
			if iter, hasIter := runtime.GetIterable(val); hasIter {
				vm.stack.Push(iter)
				continue
			}
			if err := vm.throwNamedError("TypeError", "%s is not async-iterable", val.Inspect()); err != nil {
				return err
			}
			continue
		case bytecode.OP_ASYNC_ITER_NEXT:
			// for await...of 循环头: 读栈顶迭代器, 调 next() 把「步进结果」
			// 压栈。结果可能是 Promise (由编译器发射的 OP_YIELD 交给 __spawn
			// 驱动: resolve 值作为 yield 的恢复值再取 .value/.done), 也可能
			// 直接是 {value, done} 对象 (同步迭代器被 for-await 消费的形状)。
			iter := vm.stack.Peek()
			switch it := iter.(type) {
			case *object.Generator:
				// 同步 generator 被 for await 消费: 驱动一步。
				// 结果包成 {value, done} 对象, 与 next() 调用形状统一。
				val, done, err := vm.genResume(it, object.UndefinedSingleton)
				if err != nil {
					return err
				}
				step := object.NewObject()
				step.SetProperty("value", val)
				step.SetProperty("done", object.NewBoolean(done))
				vm.stack.Push(step)
			case *runtime.Iterator:
				val, done := it.Next()
				step := object.NewObject()
				step.SetProperty("value", val)
				step.SetProperty("done", object.NewBoolean(done))
				vm.stack.Push(step)
			case *object.Object:
				// 对象迭代器: 调它的 next() 方法 (this = 迭代器本身)。
				// Async-from-Sync wrapper (有 wrapperSrcKey): next method 懒
				// 取一次并缓存, this = 源同步迭代器 (V8 语义)。
				nextFn, found := it.GetProperty("next")
				recv := iter
				if nf, src, ok := vm.syncWrapperNext(it); ok {
					nextFn, found, recv = nf, true, src
				}
				// get next 是访问器且抛错: 立即消费回调桥错误并按抛出流程
				// 传播原始抛出值 (不能退化成 "no callable next()" TypeError)。
				if err := vm.checkCallbackErr(); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				if !found || !object.IsCallable(nextFn) {
					if err := vm.throwNamedError("TypeError", "async iterator has no callable next()"); err != nil {
						return err
					}
					continue
				}
				res, err := vm.callFunction(nextFn, recv, nil)
				if err != nil {
					return err
				}
				vm.stack.Push(res)
			case *object.AsyncGenerator:
				// 异步生成器: 调它的 next() 方法 (this = 生成器本身), 得到
				// Promise<{value, done}>。编译器随后的 OP_YIELD/OP_AWAIT 等待它。
				nextFn, found := it.GetProperty("next")
				if !found || !object.IsCallable(nextFn) {
					if err := vm.throwNamedError("TypeError", "async iterator has no callable next()"); err != nil {
						return err
					}
					continue
				}
				res, err := vm.callFunction(nextFn, it, nil)
				if err != nil {
					return err
				}
				vm.stack.Push(res)
			default:
				if err := vm.throwNamedError("TypeError", "%s is not async-iterable", iter.Inspect()); err != nil {
					return err
				}
				continue
			}
		case bytecode.OP_ASYNC_ITER_NEXT_ARG:
			// async generator 的 yield* 委托: 同 ASYNC_ITER_NEXT, 但把消费者
			// 传入的 next(v) 实参转发给被委托迭代器。栈 [iter, arg] → [step]。
			arg := vm.stack.Pop()
			iter := vm.stack.Pop()
			switch it := iter.(type) {
			case *object.Generator:
				// 同步 generator 被异步委托: 传入 arg 作为其 yield 的恢复值。
				val, done, err := vm.genResume(it, arg)
				if err != nil {
					return err
				}
				step := object.NewObject()
				step.SetProperty("value", val)
				step.SetProperty("done", object.NewBoolean(done))
				// 同步源: 步进值按 Async-from-Sync 解包 (%AsyncFromSyncIterator
				// Prototype%.next, valueWrapper = PromiseResolve(value))。
				vm.stack.Push(vm.asyncFromSyncAwaitedStep(step))
			case *runtime.Iterator:
				val, done := it.Next()
				step := object.NewObject()
				step.SetProperty("value", val)
				step.SetProperty("done", object.NewBoolean(done))
				// runtime.Iterator 只承载同步可迭代 (数组/字符串/Map ...):
				// 同款值解包。
				vm.stack.Push(vm.asyncFromSyncAwaitedStep(step))
			case *object.Object, *object.AsyncGenerator:
				var nextFn object.Value
				var found bool
				recv := iter // this: 默认迭代器本身; wrapper 场景用源对象
				if ag, isAG := it.(*object.AsyncGenerator); isAG {
					nextFn, found = ag.GetProperty("next")
				} else {
					o := it.(*object.Object)
					nextFn, found = o.GetProperty("next")
					// Async-from-Sync wrapper: next method 懒取缓存, this =
					// 源同步迭代器 (V8 语义)。
					if nf, src, ok := vm.syncWrapperNext(o); ok {
						nextFn, found, recv = nf, true, src
					}
				}
				// get next 抛错: 消费回调桥错误并按抛出流程传播原始抛出值。
				if err := vm.checkCallbackErr(); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				if !found || !object.IsCallable(nextFn) {
					if err := vm.throwNamedError("TypeError", "async iterator has no callable next()"); err != nil {
						return err
					}
					continue
				}
				res, err := vm.callFunction(nextFn, recv, []object.Value{arg})
				if err != nil {
					return err
				}
				// 同步源的 Async-from-Sync wrapper: 步进结果的值须按
				// PromiseResolve 解包 (%AsyncFromSyncIteratorPrototype%.next)。
				// 原生 @@asyncIterator 与裸 next 对象的值不解包。
				if o, isObj := iter.(*object.Object); isObj && vm.wrapperIsFromSync(o) {
					res = vm.asyncFromSyncAwaitedStep(res)
				}
				vm.stack.Push(res)
			default:
				if err := vm.throwNamedError("TypeError", "%s is not async-iterable", iter.Inspect()); err != nil {
					return err
				}
				continue
			}

		// ===== 作用域 =====
		case bytecode.OP_PUSH_SCOPE, bytecode.OP_POP_SCOPE:
			// 局部变量使用 slot 管理，作用域操作在 VM 中是 NOP

		// ===== with 语句 (对象环境记录, 规范 13.11.7) =====
		case bytecode.OP_WITH_ENTER:
			// 对象表达式结果 → ToObject (规范 14.11.2): null / undefined 抛
			// TypeError, 其余原样压回 (原始值按包装对象语义由 GetProperty 承接)。
			objVal := vm.stack.Pop()
			if objVal == object.NullSingleton || objVal == object.UndefinedSingleton {
				if err := vm.throwNamedError("TypeError",
					"Cannot convert %s to object", objVal.Inspect()); err != nil {
					return err
				}
				continue
			}
			vm.stack.Push(objVal)
		case bytecode.OP_WITH_LOAD:
			ref := withRefOf(frame, operand)
			if ref == nil {
				return fmt.Errorf("VM: WITH_LOAD expects *WithRef constant at %d", operand)
			}
			_, val, found, werr := vm.withResolve(frame, ref)
			if werr != nil {
				if terr := vm.rethrowBridgeError(werr); terr != nil {
					return terr
				}
				continue
			}
			if found {
				vm.stack.Push(val)
				continue
			}
			// 未命中: 按回退语义取值。
			if !ref.LocalFallback {
				if gv, ok := vm.globals.Get(ref.Name); ok {
					vm.stack.Push(gv)
				} else if err := vm.throwNamedError("ReferenceError", "%s is not defined", ref.Name); err != nil {
					return err
				}
				continue
			}
			if ref.FallbackSlot < len(frame.Locals) && frame.Locals[ref.FallbackSlot] != nil {
				vm.stack.Push(frame.Locals[ref.FallbackSlot])
			} else if err := vm.throwNamedError("ReferenceError",
				"Cannot access lexical declaration '%s' before initialization", ref.Name); err != nil {
				return err
			}
		case bytecode.OP_WITH_STORE:
			ref := withRefOf(frame, operand)
			if ref == nil {
				return fmt.Errorf("VM: WITH_STORE expects *WithRef constant at %d", operand)
			}
			val := vm.stack.Pop()
			owner, _, found, werr := vm.withResolve(frame, ref)
			if werr != nil {
				if terr := vm.rethrowBridgeError(werr); terr != nil {
					return terr
				}
				continue
			}
			if found {
				if err := vm.withSet(owner, ref.Name, val); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
				}
				continue
			}
			// 未命中: 写回退目标。
			if !ref.LocalFallback {
				if vm.globals.IsConst(ref.Name) {
					if err := vm.throwNamedError("TypeError", "Assignment to constant variable: %s", ref.Name); err != nil {
						return err
					}
					continue
				}
				if _, exists := vm.globals.Get(ref.Name); exists {
					vm.globals.Set(ref.Name, val)
				} else {
					// 隐式赋值创建的全局: globalThis 的自有可配置属性。
					vm.globals.DeclareImplicit(ref.Name, val)
				}
				continue
			}
			if ref.IsConst {
				if err := vm.throwNamedError("TypeError", "Assignment to constant variable: %s", ref.Name); err != nil {
					return err
				}
				continue
			}
			vm.storeLocalSlot(frame, ref.FallbackSlot, val)
		case bytecode.OP_WITH_DELETE:
			ref := withRefOf(frame, operand)
			if ref == nil {
				return fmt.Errorf("VM: WITH_DELETE expects *WithRef constant at %d", operand)
			}
			owner, _, found, werr := vm.withResolve(frame, ref)
			if werr != nil {
				if terr := vm.rethrowBridgeError(werr); terr != nil {
					return terr
				}
				continue
			}
			if found {
				vm.withDelete(owner, ref.Name)
			}
			// delete 标识符统一返回 true (with 对象属性删除 / 无绑定均可删)。
			vm.stack.Push(object.NewBoolean(true))

		// ===== 类型操作 =====
		case bytecode.OP_TO_NUMBER:
			val := vm.stack.Pop()
			result, err := toNumberValue(val)
			if err != nil {
				if terr := vm.throwJSError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(result)
		case bytecode.OP_INC, bytecode.OP_DEC:
			// ++/-- 的数值步骤: ToNumeric(old) ± 1 (同类型)。
			// BigInt 走 BigInt ± 1n (禁止与 Number 混合), 其余走 double。
			val := vm.stack.Pop()
			one := int64(1)
			if op == bytecode.OP_DEC {
				one = -1
			}
			if bi, ok := val.(*object.BigInt); ok {
				vm.stack.Push(bi.Add(object.NewBigIntInt64(one)))
				continue
			}
			num, err := toNumberValue(val)
			if err != nil {
				if terr := vm.throwJSError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(object.NewNumber(num.(*object.Number).Value + float64(one)))
		case bytecode.OP_TO_PROPERTY_KEY:
			// 栈: [obj, keyRaw] → [obj, keyTransformed]。
			//
			// 成员引用的键在此**只转一次**: 之后 GET_INDEX / SET_INDEX 共用
			// 这份结果, 带自定义 toString 的对象键不会被求值两次。
			//
			// 但 ToPropertyKey **必须晚于 RequireObjectCoercible(obj)**: 规范里
			// GetValue/PutValue 先 ToObject(base) (null/undefined 立即抛 TypeError),
			// 再 ToPropertyKey(键)。本指令发出的位置恰在复合赋值 / 逻辑赋值 /
			// ++/-- 的「键表达式求值之后、GET_INDEX 之前」, 此时栈上 obj 就在键
			// 之下, 故先就地做基数检查: 基为 null/undefined 时抛 TypeError 并且
			// **完全不碰键** (不得触发键的用户 toString)。
			// null[prop] *= v 场景: 修复前会先调 prop.toString() 抛 Test262Error,
			// 规范要求的是 TypeError (compound-assignment/S11.13.2_A7.*_T1|T2)。
			if base := vm.stack.PeekAt(1); base == object.NullSingleton || base == object.UndefinedSingleton {
				if err := vm.throwNamedError("TypeError",
					"Cannot read properties of %s", base.Inspect()); err != nil {
					return err
				}
				continue
			}
			v := vm.stack.Pop()
			vm.stack.Push(toPropertyKey(v))
			// 对象键的转换会调用用户 toString ⇒ 经回调桥可能抛出, 立即重抛,
			// 否则错误会残留到之后某个不相干的内建调用才被消费 (见 GET_INDEX)。
			if err := vm.checkCallbackErr(); err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
			}
		case bytecode.OP_TYPEOF:
			val := vm.stack.Pop()
			vm.stack.Push(object.NewString(object.TypeOf(val)))
		case bytecode.OP_TYPEOF_GLOBAL:
			// typeof 作用于编译期未绑定的标识符。
			// 该标识符可能是运行时注入的全局对象 (Math/Number/Array/...)，
			// 也可能是真正的未声明变量 —— 后者按规范返回 "undefined" 而非抛错，
			// 因此这里走非抛出的全局查找。
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				if val, found := vm.globals.Get(s.Value); found {
					vm.stack.Push(object.NewString(object.TypeOf(val)))
				} else {
					vm.stack.Push(object.NewString("undefined"))
				}
			} else {
				vm.stack.Push(object.NewString("undefined"))
			}
		case bytecode.OP_INSTANCEOF:
			// instanceof: 栈顶是 Constructor (右), 下方是 obj (左)。
			// 检查 obj 的原型链是否包含 Constructor.prototype。
			right := vm.stack.Pop()
			left := vm.stack.Pop()
			vm.stack.Push(vm.instanceOf(left, right))
		case bytecode.OP_IN:
			// in 运算符: key in obj。左操作数(key)先压, 右操作数(obj)后压, 栈顶是 obj。
			obj := vm.stack.Pop()
			key := vm.stack.Pop()
			vm.stack.Push(vm.inOperator(obj, key))
		case bytecode.OP_THIS:
			frame := vm.currentFrame()
			if frame.Closure == nil {
				// 主帧: script 顶层 this = globalThis (与严格模式无关);
				// module 顶层 this = undefined。复用 globals 里已装配的
				// globalThis 绑定以保证 `this === globalThis` 的对象同一性;
				// 未装配时保持 undefined (无 stdlib 的裸 VM)。
				if vm.moduleMode {
					vm.stack.Push(object.UndefinedSingleton)
				} else {
					vm.stack.Push(vm.globalThisValue())
				}
			} else if frame.This != nil {
				// 普通函数帧: callClosure 已按 sloppy 规则归一并写入 frame.This。
				vm.stack.Push(frame.This)
			} else {
				vm.stack.Push(object.UndefinedSingleton)
			}
		case bytecode.OP_NEW_TARGET:
			// new.target: 当前帧的构造目标。callClosure 已在装配帧时写入
			// (构造调用为被 new 的构造器 / super() 为调用者的构造目标); 其余
			// (普通调用、箭头函数) 为 nil, 归一为 undefined。主帧 (顶层) 语法上
			// 不可能出现 new.target (解析期已拦), 兜底也返回 undefined。
			frame := vm.currentFrame()
			if frame != nil && frame.NewTarget != nil {
				vm.stack.Push(frame.NewTarget)
			} else {
				vm.stack.Push(object.UndefinedSingleton)
			}
		case bytecode.OP_DELETE:
			// 栈: [obj, key] → 删除 obj 上的 key 属性, 推入 true/false
			key := vm.stack.Pop()
			obj := vm.stack.Pop()
			// 规范 delete 的 IsPropertyReference 分支先 ToObject(ref.[[Base]]):
			// 基为 null/undefined 时**立即抛 TypeError 且不碰键**。此检查必须
			// 在下面 ToPropertyKey 归一之前 —— 否则 `delete null[prop]` 会先调
			// prop.toString() 再静默返回 true (delete/member-computed-reference-
			// null/undefined 期望 TypeError)。
			if obj == object.NullSingleton || obj == object.UndefinedSingleton {
				if err := vm.throwNamedError("TypeError",
					"Cannot convert %s to object", obj.Inspect()); err != nil {
					return err
				}
				continue
			}
			// 键归一 (ToPropertyKey): 之前只认 String/Symbol, 于是
			// `delete obj[0]` / `delete obj[objKey]` 会静默返回 false 且不删属性。
			switch key.(type) {
			case *object.String, *object.Symbol:
			default:
				key = object.NewString(toJSString(key))
				if err := vm.checkCallbackErr(); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
				}
			}
			if o, ok := obj.(*object.Object); ok {
				if s, ok := key.(*object.String); ok {
					vm.stack.Push(object.NewBoolean(o.DeleteProperty(s.Value)))
				} else if sym, ok := key.(*object.Symbol); ok {
					vm.stack.Push(object.NewBoolean(o.DeleteSymbolProperty(sym)))
				} else {
					vm.stack.Push(object.NewBoolean(false))
				}
			} else if g, ok := obj.(*object.GlobalObject); ok {
				// globalThis: 删除对应全局环境记录里的自有可配置绑定。
				// 此前 GlobalObject 掉进最下方 else 分支 ⇒ 恒返回 true 却什么都没删,
				// 于是 `delete this.x`(getter 自删) 后 `x in this` 仍为 true,
				// 且后续对 x 的严格赋值因「绑定仍存在」而不抛 ReferenceError
				// (compound-assignment/…-putvalue-lref--v--* 一族 23 例)。
				if s, ok := key.(*object.String); ok {
					vm.stack.Push(object.NewBoolean(g.DeleteOwn(s.Value)))
				} else {
					// 全局环境无符号键绑定: 无可删, 按规范返回 true。
					vm.stack.Push(object.NewBoolean(true))
				}
			} else if arr, ok := obj.(*object.Array); ok {
				// 数组删除: 先查自有属性描述符 —— 不可配置的索引 (如
				// Object.defineProperty(arguments, "0", {configurable:false})
				// 之后的 mapped arguments) [[Delete]] 必须返回 false 且不删;
				// 之前无条件删并恒返回 true (arguments-object/mapped/
				// mapped-arguments-nonconfigurable-delete-1.js)。
				// 注意 key 已在上方按 ToPropertyKey 归一为字符串, 故这里
				// 数字索引用 "0"/"1" 形态匹配。删除走 Array.DeleteOwn, 由它
				// 按描述符可配置性裁决并同步 Elements / propKeyOrder。
				if s, ok := key.(*object.String); ok {
					vm.stack.Push(object.NewBoolean(arr.DeleteOwn(s.Value)))
				} else {
					vm.stack.Push(object.NewBoolean(false))
				}
			} else if ds, ok := obj.(object.PropDeleter); ok {
				// 函数类的 length/name 等自有属性事实来源在结构体字段,
				// delete 需落墓碑 (见 object.PropDeleter)。
				if s, ok := key.(*object.String); ok {
					vm.stack.Push(object.NewBoolean(ds.DeleteOwn(s.Value)))
				} else {
					vm.stack.Push(object.NewBoolean(false))
				}
			} else {
				vm.stack.Push(object.NewBoolean(true))
			}

		case bytecode.OP_DELETE_GLOBAL:
			// `delete 标识符` 的全局侧 (sloppy; 编译器已排除词法绑定,
			// 故这里的名字一律按 globalThis 自有属性删除)。语义等价于
			// delete globalThis[name]: 顶层 var / 函数声明不可配置 →
			// false; 隐式赋值全局 / 未绑定名 → true 并真正删掉绑定。
			// 不内联成 OP_THIS + OP_DELETE: 函数体内的 this 是调用方接收者,
			// module 顶层 this 是 undefined, 二者都会让删除落到错误的基上。
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				vm.stack.Push(object.NewBoolean(vm.globals.DeleteGlobalBinding(s.Value)))
			} else {
				// 名字常量恒为 String (编译器构造); 兜底保持栈平衡。
				vm.stack.Push(object.NewBoolean(false))
			}

		// ===== 控制 =====
		case bytecode.OP_BREAK, bytecode.OP_CONTINUE:
			// break/continue 已由编译器转换为 OP_JUMP/OP_LOOP
			// 如果直接出现，跳转到 operand
			frame.PC = int(operand)

		// ===== try/catch/finally =====
		case bytecode.OP_PUSH_TRY:
			// operand = catchPC (0 = 无 catch)
			vm.tryStack = append(vm.tryStack, tryEntry{
				catchPC:   int(operand),
				finallyPC: 0,
				stackBase: vm.stack.Len(),
				frameIdx:  vm.frameIdx,
			})
		case bytecode.OP_PUSH_FINALLY:
			// operand = finallyPC，设置在栈顶 try 条目上
			if len(vm.tryStack) > 0 {
				vm.tryStack[len(vm.tryStack)-1].finallyPC = int(operand)
			}
		case bytecode.OP_PUSH_RET_TRY:
			// operand = returnPC，设置在栈顶 try 条目上 (同 OP_PUSH_FINALLY 手法)。
			// yield* 异步委托用: 委托期消费者 return(v) 转发给被委托迭代器。
			if len(vm.tryStack) > 0 {
				vm.tryStack[len(vm.tryStack)-1].catchReturnPC = int(operand)
			}
		case bytecode.OP_POP_TRY:
			// try 块正常完成，弹出处理器
			if len(vm.tryStack) > 0 {
				vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]
			}
		case bytecode.OP_THROW:
			val := vm.stack.Pop()
			if !vm.handleThrow(val) {
				// 无处理器: 返回错误
				return &ThrowError{Value: val}
			}
			// 异常已被捕获，继续执行 (PC 已被 handleThrow 设置)
		case bytecode.OP_END_FINALLY:
			// finally 块结束。栈顶若正是本帧刚跑完 finally 的条目 (inFinally),
			// 说明它是「带着挂起异常」进来的 ⇒ 弹出它并重新抛出挂起值;
			// 否则本 finally 是从「try 正常完成 / catch 完成」路径进来的,
			// 没有挂起值, 直接继续。
			if n := len(vm.tryStack); n > 0 {
				top := vm.tryStack[n-1]
				if top.inFinally && top.frameIdx == vm.frameIdx {
					vm.tryStack = vm.tryStack[:n-1]
					if top.pendingReturn {
						// return 完成: 继续向外传播 (展开外层 finally),
						// 无更多 finally 时完成 return —— 等价 OP_RETURN。
						if !vm.handleReturn(top.pendingVal) {
							val := top.pendingVal
							base := vm.currentFrame().StackBase
							vm.popFrame()
							if vm.stack.Len() > base {
								vm.stack.Truncate(base)
							}
							vm.stack.Push(val)
							continue
						}
					} else if !vm.handleThrow(top.pendingVal) {
						return &ThrowError{Value: top.pendingVal}
					}
				}
			}

		// ===== 模块系统 =====
		case bytecode.OP_IMPORT:
			// operand = 模块路径常量索引
			specVal := frame.Constants.Get(operand)
			spec := ""
			if s, ok := specVal.(*object.String); ok {
				spec = s.Value
			}
			modExports, err := vm.loadModule(spec)
			if err != nil {
				// 模块加载失败作为异常
				errVal := moduleLoadErrorValue(err)
				if !vm.handleThrow(errVal) {
					return &ThrowError{Value: errVal}
				}
				continue
			}
			// 推入模块命名空间对象 (物化时解析再导出/星号导出, 见 buildNamespace)
			vm.stack.Push(modExports.buildNamespace(nil))

		case bytecode.OP_EXPORT:
			// operand = 导出名常量索引，栈顶是导出值
			nameVal := frame.Constants.Get(operand)
			exportName := ""
			if s, ok := nameVal.(*object.String); ok {
				exportName = s.Value
			}
			val := vm.stack.Pop()
			vm.ensureCurrentExports().setValue(exportName, val)

		case bytecode.OP_EXPORT_BINDING:
			// operand = 常量池索引 → [导出名(str), 槽位(int)]
			// 记录"导出名 → 当前(模块顶层)帧槽位"的读取器, 不存值快照。
			name, slot, ok := vm.bindingOperand(frame, operand)
			if !ok {
				return fmt.Errorf("VM: malformed EXPORT_BINDING operand at pc %d", frame.PC)
			}
			locals := frame.Locals
			vm.ensureCurrentExports().setBinding(name, func() object.Value {
				if slot < len(locals) {
					return locals[slot]
				}
				return nil
			})

		case bytecode.OP_EXPORT_FROM:
			// operand = 常量池索引 → [模块路径(str), 源导出名(str), 目标导出名(str)]
			// 记录转发 (读时读取源模块导出槽), 而不是当场拷贝值。
			spec, srcName, target, ok := vm.reexportOperand(frame, operand)
			if !ok {
				return fmt.Errorf("VM: malformed EXPORT_FROM operand at pc %d", frame.PC)
			}
			src, err := vm.loadModule(spec)
			if err != nil {
				errVal := moduleLoadErrorValue(err)
				if !vm.handleThrow(errVal) {
					return &ThrowError{Value: errVal}
				}
				continue
			}
			vm.ensureCurrentExports().setForward(target, src, srcName)

		case bytecode.OP_EXPORT_STAR:
			// operand = 模块路径常量索引; 记录星号再导出源。
			specVal := frame.Constants.Get(operand)
			spec := ""
			if s, ok := specVal.(*object.String); ok {
				spec = s.Value
			}
			src, err := vm.loadModule(spec)
			if err != nil {
				errVal := moduleLoadErrorValue(err)
				if !vm.handleThrow(errVal) {
					return &ThrowError{Value: errVal}
				}
				continue
			}
			vm.ensureCurrentExports().addStar(src)

		default:
			return fmt.Errorf("VM: unknown opcode 0x%02x (%s)", op, op.Name())
		}
	}
	return nil
}

// callFunction 从 Go 代码调用 JS 函数 (闭包或内建函数)。
// 用于 stdlib 回调桥: 当 BuiltinMethod (如 Array.prototype.map) 需要
// 调用用户传入的 JS 闭包时，通过此方法执行子帧。

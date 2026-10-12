package compiler

import (
	"fmt"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/object"
)

type bindKind int

const (
	bindAssign  bindKind = iota // [a] = x
	bindLexical                 // let/const [a] = x
	bindVar                     // var [a] = x
)

// compileDestructureAssignment 处理解构。
// kind 决定解构目标的绑定登记口径 (见 bindKind): bindLexical (let/const 声明)、
// bindVar (var 声明, 落函数作用域层)、bindAssign (赋值解构, 写已有绑定)。

func (c *Compiler) compileDestructureAssignment(node *ast.AssignmentExpression, kind bindKind) error {
	// 编译右值 (被解构的值)
	if err := c.compileExpression(node.Right); err != nil {
		return err
	}
	if kind == bindAssign {
		// 赋值表达式的值是右值: 留一份副本，解构只消耗另一份
		c.emitter.EmitNoOperand(bytecode.OP_DUP)
	}

	return c.compilePatternBind(node.Left, kind)
}

// compilePatternBind 对栈顶的值执行解构绑定。
// 解构后栈顶的被解构值被消费, 栈回到进入前的高度。
// 支持 ArrayPattern (迭代器驱动 + IteratorClose) / ObjectPattern / 嵌套模式。

func (c *Compiler) compilePatternBind(pattern ast.Expression, kind bindKind) error {
	switch pattern := pattern.(type) {
	case *ast.ArrayPattern:
		return c.compileArrayPatternBind(pattern, kind)
	case *ast.ObjectPattern:
		if err := c.compileObjectPatternBind(pattern, kind); err != nil {
			return err
		}
		c.emitter.EmitNoOperand(bytecode.OP_POP) // 弹出被解构的值
		return nil
	default:
		return fmt.Errorf("compiler: unsupported destructure pattern %T", pattern)
	}
}

// compileArrayPatternBind 以迭代器协议 (GetIterator → next → IteratorClose)
// 驱动数组解构绑定/赋值, 与规范 ArrayBindingPattern / ArrayAssignmentPattern 对齐。
// 栈顶是被解构值, 结束时被消费。
//
// 两条规范要点:
//   - 目标数少于迭代器产出、或绑定中途抛异常时, 只要迭代器**未耗尽**就必须
//     调 return() (IteratorClose); 耗尽 (收到 done) 则不调。
//   - 判据用「槽内迭代器是否已清空」表达: 收到 done 时把隐藏槽写成 undefined,
//     收尾序列 LOAD 到 undefined 自然跳过 —— 无需单独的 done 标志位。
//
// 异常路径复用 try/finally 基础设施: PUSH_TRY 0 + PUSH_FINALLY closePC 包住
// 绑定段, 异常落到 closePC 后套一层 swallow-try 跑 close (close 自身异常丢弃,
// 原异常经 END_FINALLY 重抛)。

func (c *Compiler) compileArrayPatternBind(pattern *ast.ArrayPattern, kind bindKind) error {
	iterSlot := c.allocHiddenSlot()
	c.emitter.EmitNoOperand(bytecode.OP_GET_ITERATOR)   // [iter]; 非可迭代 → TypeError
	c.emitter.Emit(bytecode.OP_STORE, uint16(iterSlot)) // []

	c.emitter.Emit(bytecode.OP_PUSH_TRY, 0)
	closeFin := c.emitter.EmitJump(bytecode.OP_PUSH_FINALLY)

	doneIdx := c.constants.AddConstant(object.NewString("done"))
	valueIdx := c.constants.AddConstant(object.NewString("value"))

	for _, elem := range pattern.Elements {
		if elem.Rest {
			if err := c.compileArrayRestCollect(iterSlot, elem.Target, kind, doneIdx, valueIdx); err != nil {
				return err
			}
			break
		}
		if err := c.emitArrayElementBind(iterSlot, elem, kind, doneIdx, valueIdx); err != nil {
			return err
		}
	}

	// 正常完成: 摘掉 close handler, 再按需 close (迭代器未耗尽才算)。
	c.emitter.EmitNoOperand(bytecode.OP_POP_TRY)
	c.emitSyncIterClose(iterSlot)
	exit := c.emitter.EmitJump(bytecode.OP_JUMP)

	// 控制转移/异常穿出落点: OP_ITER_CLOSE_ABRUPT 按挂起完成的类型决定
	// close 自身异常的取舍 —— throw 穿出丢弃 (规范 7.4.6 步骤 6), return /
	// break / continue 穿出则传播 (步骤 7/8)。此前这里是无条件 swallow
	// (PUSH_TRY + POP), 把「return 穿出时 return() 返回非对象 → TypeError」
	// 也一起吞了, 表现为 generator 的 return() 不抛 TypeError。
	c.emitter.PatchJump(closeFin)
	c.emitter.Emit(bytecode.OP_ITER_CLOSE_ABRUPT, uint16(iterSlot))
	c.emitter.EmitNoOperand(bytecode.OP_END_FINALLY)

	c.emitter.PatchJump(exit)
	return nil
}

// emitArrayElementBind 取一个数组元素 (elision 也取一次), 解默认值并绑定目标。

func (c *Compiler) emitArrayElementBind(iterSlot int, elem *ast.PatternElement, kind bindKind, doneIdx, valueIdx uint16) error {
	// 成员目标: 目标引用的求值必须先于 IteratorStepValue (规范 13.15.5
	// AssignmentElement 步骤 1, rvdPPH)。先把 obj/key 求值进隐藏槽, 取到值后再写回。
	objSlot, keySlot := -1, -1
	if kind == bindAssign {
		if m, ok := elem.Target.(*ast.MemberExpression); ok {
			var err error
			objSlot, keySlot, err = c.emitMemberRefStash(m)
			if err != nil {
				return err
			}
		}
	}
	c.emitter.Emit(bytecode.OP_LOAD, uint16(iterSlot)) // [it]
	// 迭代器已耗尽 (槽被清空): 不再调 next(), 直接给 undefined (规范:
	// iteratorRecord.[[done]] 为 true 时后续绑定不再步进)。
	slotEmpty := c.emitter.EmitJump(bytecode.OP_JUMP_IF_NULL)
	// ITER_STEP 带迭代器槽号: next() 抛异常时 VM 先把槽清空再重抛, 表示
	// iteratorRecord.[[done]] = true (规范 7.4.6 IteratorStepValue 在 abrupt
	// 时置 Done) —— 于是异常路径的 IteratorClose 收尾会跳过 return()。
	c.emitter.Emit(bytecode.OP_ITER_STEP, uint16(iterSlot)) // [it, step]
	c.emitter.EmitNoOperand(bytecode.OP_DUP)
	c.emitter.Emit(bytecode.OP_GET_PROP, doneIdx) // [it, step, done]
	notDone := c.emitter.EmitJump(bytecode.OP_JUMP_IF_FALSE)
	// done 路径: [it, step, done] → [] → 清槽 → [undefined]
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
	c.emitter.Emit(bytecode.OP_STORE, uint16(iterSlot)) // 迭代器耗尽: 清空槽
	c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
	doBind := c.emitter.EmitJump(bytecode.OP_JUMP)
	// 槽已空路径: [it(undefined)] → [undefined]
	c.emitter.PatchJump(slotEmpty)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
	slotEmptyDone := c.emitter.EmitJump(bytecode.OP_JUMP)
	// 未 done 路径: [it, step, done] → [value]
	c.emitter.PatchJump(notDone)
	c.emitter.EmitNoOperand(bytecode.OP_POP)       // 弹出 done
	c.emitter.Emit(bytecode.OP_GET_PROP, valueIdx) // [it, value]
	c.emitter.EmitNoOperand(bytecode.OP_SWAP)      // [value, it]
	c.emitter.EmitNoOperand(bytecode.OP_POP)       // [value]
	c.emitter.PatchJump(doBind)
	c.emitter.PatchJump(slotEmptyDone)
	// [value]
	if elem.Default != nil {
		if err := c.compileDestructureDefaultNamed(elem.Default, namedTargetName(elem.Target)); err != nil {
			return err
		}
	}
	if objSlot >= 0 {
		c.emitMemberRefStoreStashed(objSlot, keySlot)
		return nil
	}
	return c.bindPatternTarget(elem.Target, kind)
}

// compileArrayRestCollect 把迭代器剩余值收集成新数组, 绑定到 rest 目标。
// 收集到 done 为止 (耗尽), 故 rest 之后迭代器已全部消费、无需再 close。

func (c *Compiler) compileArrayRestCollect(iterSlot int, target ast.Expression, kind bindKind, doneIdx, valueIdx uint16) error {
	// 成员 rest 目标: 引用同样先于 rest 收集求值 (规范 AssignmentRestElement)。
	objSlot, keySlot := -1, -1
	if kind == bindAssign {
		if m, ok := target.(*ast.MemberExpression); ok {
			var err error
			objSlot, keySlot, err = c.emitMemberRefStash(m)
			if err != nil {
				return err
			}
		}
	}
	c.emitter.Emit(bytecode.OP_NEW_ARRAY, 0) // [arr]
	loopStart := c.emitter.Pos()
	c.emitter.Emit(bytecode.OP_LOAD, uint16(iterSlot))       // [arr, it]
	restDone := c.emitter.EmitJump(bytecode.OP_JUMP_IF_NULL) // 槽已空 → rest 为空数组
	c.emitter.Emit(bytecode.OP_ITER_STEP, uint16(iterSlot))  // [arr, it, step]
	c.emitter.EmitNoOperand(bytecode.OP_DUP)
	c.emitter.Emit(bytecode.OP_GET_PROP, doneIdx) // [arr, it, step, done]
	restEnd := c.emitter.EmitJump(bytecode.OP_JUMP_IF_TRUE)
	c.emitter.EmitNoOperand(bytecode.OP_POP)       // 弹出 done
	c.emitter.Emit(bytecode.OP_GET_PROP, valueIdx) // [arr, it, value]
	c.emitter.EmitNoOperand(bytecode.OP_SWAP)      // [arr, value, it]
	c.emitter.EmitNoOperand(bytecode.OP_POP)       // [arr, value]
	c.emitter.EmitNoOperand(bytecode.OP_ARRAY_PUSH)
	c.emitter.Emit(bytecode.OP_LOOP, uint16(loopStart))
	// 迭代器耗尽: [arr, it, step, done] → 清槽 → [arr]
	c.emitter.PatchJump(restEnd)
	c.emitter.EmitNoOperand(bytecode.OP_POP) // done
	c.emitter.EmitNoOperand(bytecode.OP_POP) // step
	c.emitter.EmitNoOperand(bytecode.OP_POP) // it
	c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
	c.emitter.Emit(bytecode.OP_STORE, uint16(iterSlot)) // 迭代器耗尽: 清空槽
	afterRest := c.emitter.EmitJump(bytecode.OP_JUMP)
	// 槽已空 (绑定前面的元素时已耗尽): [arr, it] → [arr]
	c.emitter.PatchJump(restDone)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.PatchJump(afterRest)
	// [arr] → 绑定 rest 目标 → []
	if objSlot >= 0 {
		c.emitMemberRefStoreStashed(objSlot, keySlot)
		return nil
	}
	return c.bindPatternTarget(target, kind)
}

// compileObjectPatternBind 对栈顶对象执行对象解构绑定 (属性遍历, 不走迭代器)。
// 结束时栈顶对象**仍保留** (调用方负责弹出), 与数组模式不同。
//
// 规范要点:
//   - 入口先 RequireObjectCoercible (null/undefined → TypeError), 空模式也要;
//   - 每个属性: 求键 (计算键求值) → 记入 excludedNames → GetV → 默认值 → 绑定;
//   - rest: 新建对象复制源的自有可枚举属性, 跳过 excludedNames。

func (c *Compiler) compileObjectPatternBind(pattern *ast.ObjectPattern, kind bindKind) error {
	c.emitter.EmitNoOperand(bytecode.OP_REQUIRE_OBJECT_COERCIBLE)

	hasRest := pattern.RestTarget != nil
	exclSlot := -1
	if hasRest {
		exclSlot = c.allocHiddenSlot()
		c.emitter.Emit(bytecode.OP_NEW_ARRAY, 0)
		c.emitter.Emit(bytecode.OP_STORE, uint16(exclSlot))
	}

	for _, prop := range pattern.Properties {
		// DUP 对象 → [obj, obj]
		c.emitter.EmitNoOperand(bytecode.OP_DUP)
		// 压入属性名 → [obj, obj, name]
		if prop.Computed {
			if err := c.compileExpression(prop.Key); err != nil {
				return err
			}
		} else {
			idx := c.constants.AddConstant(object.NewString(patternStaticKey(prop)))
			c.emitter.Emit(bytecode.OP_CONST, idx)
		}
		if hasRest {
			// 键记入 excludedNames: [obj,obj,key] → DUP → LOAD acc → SWAP → ARRAY_PUSH
			// (ARRAY_PUSH 追加后把 acc 留在栈顶 ⇒ 再 POP 掉)。
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
			c.emitter.Emit(bytecode.OP_LOAD, uint16(exclSlot))
			c.emitter.EmitNoOperand(bytecode.OP_SWAP)
			c.emitter.EmitNoOperand(bytecode.OP_ARRAY_PUSH)
			c.emitter.EmitNoOperand(bytecode.OP_POP)
		}
		// 成员目标: 目标引用的求值先于 GetV (规范 ObjectAssignmentPattern 的
		// AssignmentProperty 求值序, rvdPPH)。先把 obj/key 求值进隐藏槽。
		objSlot, keySlot := -1, -1
		if kind == bindAssign {
			if m, ok := prop.Value.(*ast.MemberExpression); ok {
				var err error
				objSlot, keySlot, err = c.emitMemberRefStash(m)
				if err != nil {
					return err
				}
			}
		}
		// 获取属性 → [obj, val]
		c.emitter.EmitNoOperand(bytecode.OP_GET_INDEX)

		// 处理默认值
		if prop.Default != nil {
			if err := c.compileDestructureDefaultNamed(prop.Default, namedTargetName(prop.Value)); err != nil {
				return err
			}
		}

		// 存储到目标 (标识符 / 成员 / 嵌套模式; nil 表示无)
		if objSlot >= 0 {
			c.emitMemberRefStoreStashed(objSlot, keySlot)
		} else if err := c.bindPatternTarget(prop.Value, kind); err != nil {
			return err
		}
	}

	if hasRest {
		// 成员 rest 目标: 引用先于 CopyDataProperties (rest 拷贝) 求值。
		objSlot, keySlot := -1, -1
		if kind == bindAssign {
			if m, ok := pattern.RestTarget.(*ast.MemberExpression); ok {
				var err error
				objSlot, keySlot, err = c.emitMemberRefStash(m)
				if err != nil {
					return err
				}
			}
		}
		// [obj] → [obj, excluded] → OBJECT_REST → [obj, restObj] → 绑定 rest 目标 → [obj]
		c.emitter.Emit(bytecode.OP_LOAD, uint16(exclSlot))
		c.emitter.EmitNoOperand(bytecode.OP_OBJECT_REST)
		if objSlot >= 0 {
			c.emitMemberRefStoreStashed(objSlot, keySlot)
		} else if err := c.bindPatternTarget(pattern.RestTarget, kind); err != nil {
			return err
		}
	}
	return nil
}

// patternStaticKey 取非计算属性键的字符串形式 (标识符 / 字符串 / 数字字面量)。

func patternStaticKey(prop *ast.PatternProperty) string {
	if id, ok := prop.Key.(*ast.Identifier); ok {
		return id.Value
	}
	return ""
}

// bindPatternTarget 把栈顶的值绑定到解构目标 (消费该值, 栈平衡)。
// target == nil 表示数组模式里的空洞 (elision): 直接丢弃值。

func (c *Compiler) bindPatternTarget(target ast.Expression, kind bindKind) error {
	if target == nil {
		c.emitter.EmitNoOperand(bytecode.OP_POP)
		return nil
	}
	switch t := target.(type) {
	case *ast.Identifier:
		if kind == bindAssign {
			// 赋值解构: 写入已有绑定 (与 x = v 语义一致)
			c.emitIdentifierAssign(t.Value)
			return nil
		}
		if kind == bindVar {
			// var 声明解构: 绑定登记到**函数作用域层** (块外可见 / 重复声明复用
			// / 被 emitVarHoistInits 提升为 undefined) —— r4McL4 的核心修复点。
			sym, err := c.declareFuncLayerVar(t.Value)
			if err != nil {
				return err
			}
			c.emitVarAssign(t.Value, sym)
			return nil
		}
		// let/const 声明解构: 登记在当前 (块) 作用域
		sym, err := c.declareOnce(t.Value, false, false)
		if err != nil {
			return err
		}
		if c.isGlobalScope() {
			nameIdx := c.constants.AddConstant(object.NewString(t.Value))
			c.emitter.Emit(bytecode.OP_DECLARE, nameIdx)
		} else {
			c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
		}
		return nil
	case *ast.MemberExpression:
		// 声明模式不允许成员目标 (parser 为 for-of 赋值 LHS 放行, 这里兜早错)。
		if kind != bindAssign {
			return fmt.Errorf("SyntaxError: invalid destructuring binding target")
		}
		// 赋值模式的成员目标 (x.y / x[k]): 值已在栈顶 [val]。
		// compileMemberRef 压 [val, obj, key] → 旋转 → SET_INDEX 写回。
		if err := c.compileMemberRef(t); err != nil {
			return err
		}
		c.emitMemberRefStoreFromStack()
		return nil
	case *ast.ArrayPattern, *ast.ObjectPattern:
		return c.compilePatternBind(target, kind)
	}
	return fmt.Errorf("compiler: unsupported destructuring target %T", target)
}

// emitMemberRefStoreFromStack 在栈为 [..., val, obj, key] 时写回 val:
// 两次 DUP_BELOW2 把 [val, obj, key] 旋成 [obj, key, val] → SET_INDEX 写回并
// 推回 val → [val] → POP。消费 val, 净栈深 -3 (相对进入时多了 obj/key 两份)。

func (c *Compiler) emitMemberRefStoreFromStack() {
	c.emitter.EmitNoOperand(bytecode.OP_DUP_BELOW2)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.EmitNoOperand(bytecode.OP_DUP_BELOW2)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.EmitNoOperand(bytecode.OP_SET_INDEX)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
}

// emitMemberRefStash 把成员目标的 obj/key 求值后存入两个隐藏槽并返回槽号。
// 用于解构赋值里「目标引用必须先于取值 (IteratorStepValue / GetV) 求值」的场景
// (规范 13.15.5 AssignmentElement 步骤 1, rvdPPH)。

func (c *Compiler) emitMemberRefStash(m *ast.MemberExpression) (int, int, error) {
	objSlot := c.allocHiddenSlot()
	keySlot := c.allocHiddenSlot()
	if err := c.compileMemberRef(m); err != nil {
		return -1, -1, err
	}
	c.emitter.Emit(bytecode.OP_STORE, uint16(keySlot))
	c.emitter.Emit(bytecode.OP_STORE, uint16(objSlot))
	return objSlot, keySlot, nil
}

// emitMemberRefStoreStashed 把栈顶值 [val] 写回 stash 的成员引用并消费它。
// [val] → LOAD obj,key → [val,obj,key] → emitMemberRefStoreFromStack。

func (c *Compiler) emitMemberRefStoreStashed(objSlot, keySlot int) {
	c.emitter.Emit(bytecode.OP_LOAD, uint16(objSlot))
	c.emitter.Emit(bytecode.OP_LOAD, uint16(keySlot))
	c.emitMemberRefStoreFromStack()
}

// emitSyncIterClose 生成同步 IteratorClose: LOAD 槽内迭代器 → ITER_CLOSE。
// 槽内是 undefined (迭代器已耗尽) 时 ITER_CLOSE 直接跳过。

func (c *Compiler) emitSyncIterClose(iterSlot int) {
	c.emitter.Emit(bytecode.OP_LOAD, uint16(iterSlot))
	c.emitter.EmitNoOperand(bytecode.OP_ITER_CLOSE)
}

// compileDestructureDefault 实现解构默认值: 栈顶为值 [val],
// 仅当 val **恰为 undefined** 时用默认表达式替换 (规范: Initializer 只对
// undefined 生效, null 保留)。结束后栈顶为最终值 [val 或 default]。

func (c *Compiler) compileDestructureDefault(def ast.Expression) error {
	return c.compileDestructureDefaultNamed(def, "")
}

// compileDestructureDefaultNamed 同 compileDestructureDefault, 但当默认值是匿名
// 函数/类定义时, 用 name 给它命名 (规范 IteratorBindingInitialization /
// ObjectBindingPattern 的 SingleNameBinding: 默认值经 NamedEvaluation 求值)。

func (c *Compiler) compileDestructureDefaultNamed(def ast.Expression, name string) error {
	// [val] → [val, isUndefined]
	c.emitter.EmitNoOperand(bytecode.OP_DUP)
	c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
	c.emitter.EmitNoOperand(bytecode.OP_STRICT_EQ)
	notUndefined := c.emitter.EmitJump(bytecode.OP_JUMP_IF_FALSE)
	// undefined 路径: 弹出判定位与原始值, 用默认值替换
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	if err := c.compileNamedExpression(def, name); err != nil {
		return err
	}
	done := c.emitter.EmitJump(bytecode.OP_JUMP)
	// 非 undefined 路径: 弹出判定位, 保留原始值
	c.emitter.PatchJump(notUndefined)
	c.emitter.EmitNoOperand(bytecode.OP_POP)
	c.emitter.PatchJump(done)
	return nil
}

// compileIncDec 处理 ++/-- 操作。
// target: 操作目标表达式 (通常是 Identifier)
// isInc: true=++, false=--
// isPrefix: true=前缀, false=后缀

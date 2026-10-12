package compiler

import (
	"fmt"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/object"
)

func (c *Compiler) compileClassDeclaration(node *ast.ClassDeclaration) error {
	if err := c.compileClassBody(node.Name.Value, node.SuperClass, node.Methods, node.Statics, node.Fields); err != nil {
		return err
	}
	// 类值在栈顶 → 声明类名并绑定
	className := node.Name.Value
	// 类声明的绑定是**可变**的词法绑定 (规范 CreateMutableBinding), 与 let
	// 同类, 不是 const —— `class C {}; C = 1;` 合法。此前误按 isConst=true
	// 登记 (且局部槽用 OP_STORE_CONST), 在 IsConst 尚无人读取时无害; 一旦
	// 赋值路径开始查 IsConst (OP_STORE_CONST_GUARD), 这个错标就会让
	// `classBinding = 44` 误抛 TypeError (test262 eval-gtbndng-local-bndng-cls)。
	sym, err := c.declareOnce(className, false, false)
	if err != nil {
		return err
	}
	if c.isGlobalScope() && !c.moduleMode {
		nameIdx := c.constants.AddConstant(object.NewString(className))
		c.emitter.Emit(bytecode.OP_DECLARE, nameIdx)
	} else {
		c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
	}
	return nil
}

// compileClassBody 编译 class 声明/表达式共有的主体部分
// (constructor + prototype + 实例/静态方法 + extends)。
// 返回时类值 (constructor) 在栈顶。
// privateKey 把裸私有名 (#x 形式或已去 # 的裸名) 编成混编码属性键。
// name 容忍带/不带 # 两种形式。

func (c *Compiler) privateKey(name string) string {
	n := name
	if len(n) > 0 && n[0] == '#' {
		n = n[1:]
	}
	if c.currentPrivatePrefix == "" {
		// 不在类体内: 拿不到前缀, 交给调用方报错; 这里给个占位避免空键
		return "\x00<nowhere>:" + n
	}
	return "\x00" + c.currentPrivatePrefix + ":" + n
}

// emitPrivateKey 发射私有键字符串常量 (栈上多一个 key)。

func (c *Compiler) emitPrivateKey(name string) error {
	if c.currentPrivatePrefix == "" {
		n := name
		if len(n) > 0 && n[0] == '#' {
			n = n[1:]
		}
		return fmt.Errorf("compiler: '#%s' is not allowed outside class", n)
	}
	idx := c.constants.AddConstant(object.NewString(c.privateKey(name)))
	c.emitter.Emit(bytecode.OP_CONST, idx)
	return nil
}

func (c *Compiler) compileClassBody(className string, superClass ast.Expression, methods, statics []*ast.ClassMethod, fields []*ast.ClassField) error {
	// 类体恒严格: 类方法 (含**隐式**构造器)、字段初始化器都按 strict 编译。
	// 显式方法走 compileFunctionWithStrict(true,...); 这里整体置 true 是为了
	// 让 compileClassConstructor 的 meta.IsStrict (c.strict) 也拿到 true ——
	// 隐式构造器没有 AST 节点, 只能靠上下文盖章 (设计风险 #8)。
	prevStrict := c.strict
	c.strict = true
	defer func() { c.strict = prevStrict }()

	// 父类名 (super 引用目标)。Identifier 形式直接用其名; 任意表达式形式
	// (如 `extends Temporal.Duration` / `extends mixin(Base)`) 先把表达式
	// 求值到一个**合成局部变量**, 再把该变量名当作 superName —— 这样后续
	// constructor / super() / 静态 super / eval-home 全部沿用同一套
	// emitSuperLoad(名字) 机制, 无需为每种语境另设表达式求值路径。
	superName := ""
	if superClass != nil {
		if ident, ok := superClass.(*ast.Identifier); ok {
			superName = ident.Value
		} else {
			c.superClassSeq++
			tmp := fmt.Sprintf("\x00super#%d", c.superClassSeq)
			// 先算父类表达式 (仅在当前位置求值一次, 符合规范 ClassDefinition
			// 对 ClassHeritage 的单次求值语义)。
			if err := c.compileExpression(superClass); err != nil {
				return err
			}
			sym, err := c.declareOnce(tmp, false, false)
			if err != nil {
				return err
			}
			if c.isGlobalScope() && !c.moduleMode {
				c.emitter.EmitNoOperand(bytecode.OP_DUP)
				nameIdx := c.constants.AddConstant(object.NewString(tmp))
				c.emitter.Emit(bytecode.OP_DECLARE, nameIdx)
				c.emitter.EmitNoOperand(bytecode.OP_POP)
			} else {
				c.emitter.Emit(bytecode.OP_STORE, uint16(sym.Slot))
			}
			superName = tmp
		}
	}

	// 找到 constructor (显式或默认)
	var ctor *ast.ClassMethod
	for _, m := range methods {
		if m.IsConstructor {
			ctor = m
			break
		}
	}

	// 私有名前缀: 类名 + 全局序号保证唯一 (同名类/嵌套类互不串槽)。
	// 序号只用于键混编, 不影响任何可观察行为。
	c.privateClassSeq++
	prevPrefix := c.currentPrivatePrefix
	c.currentPrivatePrefix = fmt.Sprintf("%s\x01%d", className, c.privateClassSeq)

	// 实例语境 home (roiE5Z): constructor / 实例方法 / 字段初始化器的
	// [[HomeObject]] = <className>.prototype。匿名类表达式没有可加载的
	// 类名 (LOAL_GLOBAL 无从解析), 不设 home —— 其中的直接 eval 保持
	// "super.x → SyntaxError" 的现状。
	prevHome := c.evalHome
	if className != "" && className != "<anonymous>" {
		c.evalHome = evalHomeCtx{Name: className, SuperName: superName}
	}
	defer func() { c.evalHome = prevHome }()

	// 编译 constructor
	prevSuper := c.currentSuperClass
	c.currentSuperClass = superName
	ctorMeta, err := c.compileClassConstructor(fields, ctor, className, superName)
	if err != nil {
		return err
	}
	c.currentSuperClass = prevSuper
	ctorIdx := c.constants.AddConstant(ctorMeta)
	c.emitter.Emit(bytecode.OP_FUNCTION, ctorIdx) // [ctor]

	// 创建 prototype 对象
	c.emitter.EmitNoOperand(bytecode.OP_NEW_OBJECT) // [ctor, proto]

	// 规范 ClassDefinitionEvaluation 步骤 15: 在实例原型上先定义 constructor
	// 自有属性 (不可枚举/可写/可配置), **早于**方法定义 —— 于是显式/computed
	// 名为 "constructor" 的方法定义会随后覆盖它 (computed-property-names/
	// class/method/constructor.js 依赖这一顺序)。OP_SET_CTOR 不动栈
	// ([ctor, proto])。
	c.emitter.EmitNoOperand(bytecode.OP_SET_CTOR)

	// 实例方法挂到 prototype
	for _, m := range methods {
		if m.IsConstructor {
			continue
		}
		// 私有方法/访问器: 挂 proto 但用混编码键 (this.#m() 调用走同键 GET_INDEX)
		if m.IsPrivate && m.Body != nil {
			prevSuperP := c.currentSuperClass
			c.currentSuperClass = superName
			// class 方法是"方法定义": 无 [[Construct]] / 无 prototype。
			c.methodFnDepth++
			meta, err := c.compileFunctionWithStrict(true, m.Name, m.Parameters, m.Body, false, m.IsGenerator, m.IsAsync)
			c.methodFnDepth--
			c.currentSuperClass = prevSuperP
			if err != nil {
				return err
			}
			midx := c.constants.AddConstant(meta)
			c.emitter.Emit(bytecode.OP_FUNCTION, midx) // [ctor, proto, fn]
			pkeyIdx := c.constants.AddConstant(object.NewString(c.privateKey(m.Name)))
			switch {
			case m.IsGetter:
				c.emitter.Emit(bytecode.OP_SET_GETTER, pkeyIdx)
			case m.IsSetter:
				c.emitter.Emit(bytecode.OP_SET_SETTER, pkeyIdx)
			default:
				c.emitter.Emit(bytecode.OP_SET_PROP, pkeyIdx) // [ctor, proto]
			}
			continue
		}
		if err := c.compileClassMethodToObject(m, superName); err != nil {
			return err
		}
	} // [ctor, proto]

	// extends: proto.Proto = SuperClass.prototype
	if superName != "" {
		// 栈: [ctor, proto] → LOAD_GLOBAL Super → GET_PROP prototype → [ctor, proto, parent]
		c.emitSuperLoad(superName)
		pidx := c.constants.AddConstant(object.NewString("prototype"))
		c.emitter.Emit(bytecode.OP_GET_PROP, pidx)
		// OP_SET_PROTO: 弹出 parent, 设置下方对象 (proto) 的原型 → [ctor, proto]
		c.emitter.EmitNoOperand(bytecode.OP_SET_PROTO)
	}

	// ctor.prototype = proto → [ctor]
	pidx := c.constants.AddConstant(object.NewString("prototype"))
	c.emitter.Emit(bytecode.OP_SET_PROP, pidx)

	// 静态成员: 在类定义求值处、按定义顺序执行 (方法定义与字段初始化器交错,
	// parser 把二者都放进 Statics 且保序)。其中的 `this` 必须是构造器本身、
	// 类名绑定也必须已指向构造器 —— 内联发射做不到 (this 会是外层函数的 this,
	// 而类名要到 class 声明语句末尾才绑定)。故把这些元素编进一个合成函数,
	// 再用 OP_CALL_METHOD 以 ctor 为 this 调用它。
	if len(statics) > 0 {
		// 静态语境 home (roiE5Z): static 方法 / 静态初始化块的 [[HomeObject]]
		// = 构造器本身。静态 super base = 父类构造器 (extends 时), Gox 未链接
		// ctor.__proto__, 故 SuperName 单独随标记带出。
		prevStaticHome := c.evalHome
		if className != "" && className != "<anonymous>" {
			c.evalHome = evalHomeCtx{Name: className, Static: true, SuperName: superName}
		}
		// 静态计算键预求值 (r4hv9u): 静态成员的 ComputedPropertyName 在外层函数
		// 上下文求值 (外层是生成器时 `static get [yield 9](){}` 合法), 而
		// __static_init__ 合成函数是非生成器帧 —— 故在外层按定义顺序求值收进数组
		// 作唯一实参传入, 合成函数内按序取用。
		passStaticKeys := false
		for _, m := range statics {
			if m.ComputedKey != nil {
				passStaticKeys = true
				break
			}
		}
		initMeta, err := c.compileStaticInitFn(className, superName, statics, passStaticKeys)
		c.evalHome = prevStaticHome
		if err != nil {
			return err
		}
		initIdx := c.constants.AddConstant(initMeta)
		// 栈: [ctor] → DUP → [ctor, ctor] → FUNCTION → [ctor, ctor, fn]
		//   → SWAP → [ctor, fn, ctor] → CALL_METHOD(0) → [ctor, result] → POP → [ctor]
		c.emitter.EmitNoOperand(bytecode.OP_DUP)
		c.emitter.Emit(bytecode.OP_FUNCTION, initIdx)
		c.emitter.EmitNoOperand(bytecode.OP_SWAP)
		if passStaticKeys {
			// [ctor, fn, ctor] → key1..keyN → NEW_ARRAY N → [ctor, fn, ctor, keys]
			// → CALL_METHOD(1) → [ctor, result]
			keyCount := 0
			for _, m := range statics {
				if m.ComputedKey == nil {
					continue
				}
				if err := c.compileExpression(m.ComputedKey); err != nil {
					return err
				}
				keyCount++
			}
			c.emitter.Emit(bytecode.OP_NEW_ARRAY, uint16(keyCount))
			c.emitter.Emit(bytecode.OP_CALL_METHOD, 1)
		} else {
			c.emitter.Emit(bytecode.OP_CALL_METHOD, 0)
		}
		c.emitter.EmitNoOperand(bytecode.OP_POP)
	}
	c.currentPrivatePrefix = prevPrefix
	return nil
}

// compileStaticInitFn 把类的静态元素 (静态方法 + 静态字段) 编成一个合成函数。
// 调用者以构造器为 this 调用它, 于是函数内 OP_THIS 即构造器; 函数作用域内
// 额外把类名绑定到一个局部槽 (= this), 满足 `static b = C.a + 1` 这类
// 「静态初始化器里引用类名」的规范语义。返回时类名的槽位由 VM 在入口按
// 下面的 STORE 序列写入。
//
// passStaticKeys: 调用方已在外层作用域按定义顺序求值全部静态 ComputedPropertyName
// 并收进数组作唯一实参 (r4hv9u)。此时合成函数多一个 __static_keys__ 形参槽
// (slot 0), 静态计算键一律从该数组按序取 (见 emitStaticKey), 不在本函数内
// 编译 ——本函数是非生成器帧, yield 等外层上下文构造在此无法执行。

func (c *Compiler) compileStaticInitFn(className, superName string, statics []*ast.ClassMethod, passStaticKeys bool) (*bytecode.FunctionMetadata, error) {
	prevScope := c.scope
	baseSlot := prevScope.NumLocals()
	fnScope := NewFunctionScope(prevScope)
	c.scope = fnScope

	// __static_keys__ 形参槽 (r4hv9u): 预求值的静态计算键数组, 必须是 slot 0
	// (VM 按 baseSlot+i 放置实参)。无静态计算键时不声明, 合成函数签名不变。
	keysSlot := -1
	if passStaticKeys {
		sym := fnScope.Define("__static_keys__", false)
		sym.Declared = true
		keysSlot = sym.Slot
	}
	prevKeysSlot := c.staticKeysSlot
	prevKeysUsed := c.staticKeysUsed
	c.staticKeysSlot = keysSlot
	c.staticKeysUsed = 0
	defer func() {
		c.staticKeysSlot = prevKeysSlot
		c.staticKeysUsed = prevKeysUsed
	}()

	// arguments 槽位 (静态初始化器内引用 arguments 是 parser 层早错, 但函数帧
	// 需要一个槽; 与 compileFunctionSelf 同口径)。
	argSym := fnScope.Define("__arguments__", false)
	argSym.Declared = true
	argumentsSlot := argSym.Slot
	prevArgumentsSlot := c.currentArgumentsSlot
	c.currentArgumentsSlot = argumentsSlot

	// 类名绑定: 局部槽指向构造器 (类体求值期间 ClassNameBinding 已初始化)。
	nameSlot := -1
	if className != "" && className != "<anonymous>" {
		sym := fnScope.Define(className, false)
		sym.Declared = true
		nameSlot = sym.Slot
	}

	prevEmitter := c.emitter
	c.emitter = NewEmitter()
	prevSrcPositions := c.srcPositions
	c.srcPositions = nil
	prevControlStack := c.controlStack
	c.controlStack = nil
	prevPendingLabel := c.pendingLabel
	c.pendingLabel = ""
	prevTryScopes := c.tryScopes
	c.tryScopes = nil
	prevFinallyRetSlot := c.finallyRetSlot
	c.finallyRetSlot = -1
	defer func() {
		c.tryScopes = prevTryScopes
		c.finallyRetSlot = prevFinallyRetSlot
	}()

	// 类名槽 = this (构造器)。
	if nameSlot >= 0 {
		c.emitter.EmitNoOperand(bytecode.OP_THIS)
		c.emitter.Emit(bytecode.OP_STORE, uint16(nameSlot))
	}
	// 类对象 = this, 压在栈底供各元素写入 (compileStaticElements 假定它在栈顶)。
	c.emitter.EmitNoOperand(bytecode.OP_THIS)
	if err := c.compileStaticElements(superName, statics); err != nil {
		return nil, err
	}
	c.emitter.EmitNoOperand(bytecode.OP_RETURN_VOID)

	fnIns := c.emitter.Bytes()
	fnSrcPositions := c.srcPositions
	c.srcPositions = prevSrcPositions
	c.emitter = prevEmitter
	c.scope = prevScope
	c.currentArgumentsSlot = prevArgumentsSlot
	c.controlStack = prevControlStack
	c.pendingLabel = prevPendingLabel
	c.tryScopes = prevTryScopes
	c.finallyRetSlot = prevFinallyRetSlot

	var initParams []bytecode.ParameterSpec
	numParams := 0
	if passStaticKeys {
		initParams = []bytecode.ParameterSpec{{Name: "__static_keys__"}}
		numParams = 1
	}
	meta := bytecode.NewFunctionMetadata("__static_init__", fnIns, fnScope.NumLocals(), numParams, initParams, false)
	meta.BaseSlot = baseSlot
	meta.CapturePrefixLen = computeCapturePrefixLen(fnIns, baseSlot, c.constants)
	meta.ArgumentsSlot = argumentsSlot
	meta.IsStrict = true
	meta.Positions = toSrcPosList(fnSrcPositions)
	return meta, nil
}

// emitStaticKey 发射「静态成员计算键的取值」: 把 __static_keys__ 数组的第
// staticKeysUsed 个元素压栈 (与 compileClassBody 预求值的顺序一致), 消费计数 +1。
// staticKeysSlot < 0 (无预求值通道) 时退化为原地编译表达式 ——正常路径不会
// 走到 (compileClassBody 只在存在静态计算键时才传数组)。

func (c *Compiler) emitStaticKey(key ast.Expression) error {
	if c.staticKeysSlot < 0 {
		return c.compileExpression(key)
	}
	c.emitter.Emit(bytecode.OP_LOAD, uint16(c.staticKeysSlot))
	idx := c.constants.AddConstant(object.NewInt(int64(c.staticKeysUsed)))
	c.emitter.Emit(bytecode.OP_CONST, idx)
	c.emitter.EmitNoOperand(bytecode.OP_GET_INDEX)
	c.staticKeysUsed++
	return nil
}

// compileStaticElements 发射静态方法/字段的挂载序列。
// 前提: 类对象 (构造器) 已在栈顶; 本函数保持其正好一份留在栈顶。
// 静态字段初始化器按定义顺序求值, this 即该构造器。

func (c *Compiler) compileStaticElements(superName string, statics []*ast.ClassMethod) error {
	prevSuper := c.currentSuperClass
	c.currentSuperClass = superName
	defer func() { c.currentSuperClass = prevSuper }()

	for _, m := range statics {
		// 静态初始化块 static { ... }: 与静态字段同层, 按定义顺序执行。
		// 规范 ClassStaticBlock 是一个 OrdinaryFunctionCreate, 有自己的作用域
		// (var 不泄漏) 且 this = 构造器本身 ⇒ 编成独立合成函数, 再以当前
		// 类对象为 this 调用 (与 compileClassBody 调用 __static_init__ 同一手法)。
		// 栈: [obj] → DUP → [obj, obj] → FUNCTION → [obj, obj, fn]
		//   → SWAP → [obj, fn, obj] → CALL_METHOD(0) → [obj, result] → POP → [obj]
		if m.IsStaticBlock {
			meta, err := c.compileFunctionWithStrict(true, "", nil, m.Body, false, false, false)
			if err != nil {
				return err
			}
			midx := c.constants.AddConstant(meta)
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
			c.emitter.Emit(bytecode.OP_FUNCTION, midx)
			c.emitter.EmitNoOperand(bytecode.OP_SWAP)
			c.emitter.Emit(bytecode.OP_CALL_METHOD, 0)
			c.emitter.EmitNoOperand(bytecode.OP_POP)
			continue
		}
		// 静态字段 (static f = expr / static #x = expr): 解析器把它放进
		// Statics 且 Body 为 nil。
		// 栈: [obj] → DUP → [obj, obj] → 值 → 键 → SET_* → [obj]
		if m.Body == nil {
			if m.IsPrivate {
				// 静态私有字段: 键混编码 (\x00<prefix>:<name>), 外部摸不到。
				// [obj] → 值 → [obj, val] → SET_PROP(命名键, 弹值留对象) → [obj]
				if m.FieldValue != nil {
					if err := c.compileFieldInitValue(m.FieldValue); err != nil {
						return err
					}
				} else {
					c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
				}
				keyIdx := c.constants.AddConstant(object.NewString(c.privateKey(m.Name)))
				c.emitter.Emit(bytecode.OP_SET_PROP, keyIdx)
				continue
			}
			if m.ComputedKey != nil {
				// 计算键静态字段: static [expr] = value
				c.emitter.EmitNoOperand(bytecode.OP_DUP)
				if m.FieldValue != nil {
					if err := c.compileFieldInitValue(m.FieldValue); err != nil {
						return err
					}
				} else {
					c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
				}
				if err := c.emitStaticKey(m.ComputedKey); err != nil {
					return err
				}
				c.emitter.EmitNoOperand(bytecode.OP_SWAP)
				c.emitter.EmitNoOperand(bytecode.OP_SET_INDEX)
				c.emitter.EmitNoOperand(bytecode.OP_POP)
				continue
			}
			// 命名静态字段: static f = value / 裸 static f
			c.emitter.EmitNoOperand(bytecode.OP_DUP) // [obj, obj]
			if m.FieldValue != nil {
				if err := c.compileFieldInitValue(m.FieldValue); err != nil {
					return err
				}
			} else {
				c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
			}
			keyIdx := c.constants.AddConstant(object.NewString(m.Name))
			c.emitter.Emit(bytecode.OP_SET_PROP, keyIdx) // [obj]
			continue
		}
		// 静态**私有**方法/访问器: 必须走混编码键 (与实例私有方法同键空间),
		// 否则会被写成名为 "#m" 的公有属性 —— 既让 this.#m 解析不到 (rWVt9D),
		// 又把私有成员泄漏给类外常规访问。
		if m.IsPrivate {
			// class 静态方法是"方法定义": 无 [[Construct]] / 无 prototype。
			c.methodFnDepth++
			meta, err := c.compileFunctionWithStrict(true, m.Name, m.Parameters, m.Body, false, m.IsGenerator, m.IsAsync)
			c.methodFnDepth--
			if err != nil {
				return err
			}
			midx := c.constants.AddConstant(meta)
			c.emitter.Emit(bytecode.OP_FUNCTION, midx) // [obj, fn]
			pkeyIdx := c.constants.AddConstant(object.NewString(c.privateKey(m.Name)))
			switch {
			case m.IsGetter:
				c.emitter.Emit(bytecode.OP_SET_GETTER, pkeyIdx)
			case m.IsSetter:
				c.emitter.Emit(bytecode.OP_SET_SETTER, pkeyIdx)
			default:
				c.emitter.Emit(bytecode.OP_SET_PROP, pkeyIdx)
			}
			continue
		}
		// 编译静态方法函数
		// class 静态方法是"方法定义": 无 [[Construct]] / 无 prototype。
		c.methodFnDepth++
		meta, err := c.compileFunctionWithStrict(true, m.Name, m.Parameters, m.Body, false, m.IsGenerator, m.IsAsync)
		c.methodFnDepth--
		if err != nil {
			return err
		}
		midx := c.constants.AddConstant(meta)
		if m.ComputedKey != nil && !m.IsGetter && !m.IsSetter {
			// 动态键静态方法: SET_INDEX 弹 [obj, key, val] 三元组,
			// 先 DUP obj 让写入消耗副本 (getter/setter 走 DYN 弹2保留 obj, 不需要)。
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
		}
		c.emitter.Emit(bytecode.OP_FUNCTION, midx) // [obj, fn]
		if m.ComputedKey != nil {
			// 动态键静态方法: [obj(, obj), fn] → key → DYN 访问器/SET_INDEX
			if err := c.emitStaticKey(m.ComputedKey); err != nil {
				return err
			}
			switch {
			case m.IsGetter:
				// [obj, fn, key] → SET_GETTER_DYN(弹2留obj) → [obj]
				c.emitter.EmitNoOperand(bytecode.OP_SET_GETTER_DYN)
			case m.IsSetter:
				c.emitter.EmitNoOperand(bytecode.OP_SET_SETTER_DYN)
			default:
				c.emitter.EmitNoOperand(bytecode.OP_SWAP)
				c.emitter.EmitNoOperand(bytecode.OP_SET_INDEX)
				c.emitter.EmitNoOperand(bytecode.OP_POP)
			}
			continue
		}
		keyIdx := c.constants.AddConstant(object.NewString(m.Name))
		switch {
		case m.IsGetter:
			c.emitter.Emit(bytecode.OP_SET_GETTER, keyIdx) // [obj]
		case m.IsSetter:
			c.emitter.Emit(bytecode.OP_SET_SETTER, keyIdx) // [obj]
		default:
			c.emitter.Emit(bytecode.OP_SET_PROP, keyIdx) // [obj]
		}
	}
	return nil
}

// compileClassExpression 编译 class 表达式。
// 与声明的差别: 类名不进作用域 (匿名类 Name 为 nil), 类值直接
// 作为表达式结果留在栈顶。

func (c *Compiler) compileClassExpression(node *ast.ClassExpression) error {
	return c.compileClassExpressionNamed(node, "")
}

// compileClassExpressionNamed 编译类表达式; 匿名类在 NamedEvaluation 语境下
// 用 nameHint 作为类名 (会体现在构造函数闭包的 name 上)。

func (c *Compiler) compileClassExpressionNamed(node *ast.ClassExpression, nameHint string) error {
	className := nameHint
	if node.Name != nil {
		className = node.Name.Value
	}
	if className == "" {
		className = "<anonymous>"
	}
	return c.compileClassBody(className, node.SuperClass, node.Methods, node.Statics, node.Fields)
}

// compileFieldInitValue 在「类字段初始化器」上下文中编译字段值表达式:
// 编译期间置 inClassFieldInit, 使其中的直接 eval 调用发射 OP_EVAL_MARK。
// 用 defer 保证任何返回路径都恢复, 避免标志泄漏到后续兄弟字段/方法。

func (c *Compiler) compileFieldInitValue(v ast.Expression) error {
	prev := c.inClassFieldInit
	c.inClassFieldInit = true
	defer func() { c.inClassFieldInit = prev }()
	return c.compileExpression(v)
}

// compileClassConstructor 编译 class 的 constructor 函数。
// 若无显式 constructor 则生成默认构造。
// 实例字段赋值指令插入 constructor 开头。

func (c *Compiler) compileClassConstructor(fields []*ast.ClassField, ctor *ast.ClassMethod, className, superName string) (*bytecode.FunctionMetadata, error) {
	prevScope := c.scope
	baseSlot := prevScope.NumLocals()
	fnScope := NewFunctionScope(prevScope)
	c.scope = fnScope

	paramSpecs := []bytecode.ParameterSpec{}
	paramSlots := []int{}
	if ctor != nil {
		for i, param := range ctor.Parameters {
			if param.Pattern != nil {
				// 解构模式参数: 隐藏槽存原始实参, 函数入口处解构到各绑定
				// (与 compileFunctionSelf 同口径; 默认值/解构由 compileParamBinding 生成)。
				name := fmt.Sprintf("__param_%d", i)
				paramSpecs = append(paramSpecs, bytecode.ParameterSpec{Name: name, HasDefault: param.Default != nil, IsRest: param.Rest})
				sym := fnScope.Define(name, false)
				paramSlots = append(paramSlots, sym.Slot)
				continue
			}
			paramSpecs = append(paramSpecs, bytecode.ParameterSpec{Name: param.Name, HasDefault: param.Default != nil, IsRest: param.Rest})
			sym := fnScope.Define(param.Name, false)
			sym.Declared = true  // 参数是真实声明: 函数体内 let 同名 → SyntaxError
			sym.IsVarLike = true // 参数即 var 绑定: 函数体内 var 同名复用此绑定
			paramSlots = append(paramSlots, sym.Slot)
		}
	}

	// 预留 arguments 槽位
	argSym := fnScope.Define("__arguments__", false)
	argSym.Declared = true
	argumentsSlot := argSym.Slot
	prevArgumentsSlot := c.currentArgumentsSlot
	c.currentArgumentsSlot = argumentsSlot

	// 编译函数体
	prevEmitter := c.emitter
	c.emitter = NewEmitter()
	// 函数体的 srcPositions 是独立 offset 空间 (独立 emitter), 切换到新表;
	// 编译完存进 FunctionMetadata.Positions, 与外层表互不污染 (T05)。
	prevSrcPositions := c.srcPositions
	c.srcPositions = nil
	prevControlStack := c.controlStack
	c.controlStack = nil
	prevPendingLabel := c.pendingLabel
	c.pendingLabel = ""
	prevTryScopes := c.tryScopes
	c.tryScopes = nil
	prevFinallyRetSlot := c.finallyRetSlot
	c.finallyRetSlot = -1
	// using 释放作用域同样不跨函数边界: 内层函数体里出现的 using 只属于它自己。
	prevDisposeResources := c.disposeResources
	c.disposeResources = nil
	// defer: 保证错误早退路径也恢复, 否则 compileTryStatement 的 [:len-1] 会 panic (rCzckg 回归)。
	defer func() {
		c.tryScopes = prevTryScopes
		c.finallyRetSlot = prevFinallyRetSlot
		c.disposeResources = prevDisposeResources
	}()

	// 参数默认值 + 解构模式绑定: 必须先于字段初始化与构造体执行
	// (规范: 参数在函数体运行前绑定)。ctor == nil 时无参数, 自然跳过。
	var ctorDeclaredParams []*ast.Parameter
	if ctor != nil {
		ctorDeclaredParams = ctor.Parameters
	}
	if err := c.compileParamBinding(ctorDeclaredParams, paramSlots); err != nil {
		return nil, err
	}

	// 隐式 constructor + 有父类: 语义等价于 constructor(...args){ super(...args) }。
	// 必须先转发父构造 (父类实例字段 + 父构造体) 再跑本类字段初始化 —— 顺序与规范
	// 一致 (父字段先于子字段)。旧实现隐式构造是空体, 父类实例字段从不初始化
	// (静默全 undefined) —— 即 rCzckg。
	if ctor == nil && superName != "" {
		// [fn] → [fn, this] → [fn, this, arguments] → CALL_METHOD_SPREAD
		c.emitSuperLoad(superName)
		c.emitter.EmitNoOperand(bytecode.OP_THIS)
		c.emitter.Emit(bytecode.OP_LOAD, uint16(argumentsSlot))
		// 隐式构造也是 super(...): new.target 沿链传给父构造器。
		c.emitter.EmitNoOperand(bytecode.OP_NEW_TARGET_MARK)
		c.emitter.EmitNoOperand(bytecode.OP_CALL_METHOD_SPREAD)
		// super() 的返回值 (父构造结果) 丢弃: 隐式构造体里没有表达式语句层。
		c.emitter.EmitNoOperand(bytecode.OP_POP)
	}

	// 实例字段赋值: this.field = value / this[expr] = value
	for _, field := range fields {
		c.emitter.EmitNoOperand(bytecode.OP_THIS)
		if field.IsPrivate {
			// 私有字段: this[#name] = value —— 键是混编码字符串常量
			// (\x00<prefix>:<name>), 外部任何常规访问都摸不到。
			if field.Value != nil {
				if err := c.compileFieldInitValue(field.Value); err != nil {
					return nil, err
				}
			} else {
				c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
			}
			if err := c.emitPrivateKey(field.Name); err != nil {
				return nil, err
			}
			// [this, val, key] → SWAP → [this, key, val] → SET_INDEX → [val] → POP
			c.emitter.EmitNoOperand(bytecode.OP_SWAP)
			c.emitter.EmitNoOperand(bytecode.OP_SET_INDEX)
			c.emitter.EmitNoOperand(bytecode.OP_POP)
			continue
		}
		if field.ComputedKey != nil {
			// 动态键字段: SET_INDEX 弹 [obj, key, val] 三元组, 需先 DUP this
			// 让写入消耗副本、原 this 留在栈底 (constructor 栈约定)。
			// [this] → DUP → [this, this] → val → key → SWAP → [this, this, key, val]
			// → SET_INDEX(弹3压1) → [this, val] → POP → [this]
			c.emitter.EmitNoOperand(bytecode.OP_DUP)
			if field.Value != nil {
				if err := c.compileFieldInitValue(field.Value); err != nil {
					return nil, err
				}
			} else {
				c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
			}
			if err := c.compileExpression(field.ComputedKey); err != nil {
				return nil, err
			}
			c.emitter.EmitNoOperand(bytecode.OP_SWAP)
			c.emitter.EmitNoOperand(bytecode.OP_SET_INDEX)
			c.emitter.EmitNoOperand(bytecode.OP_POP)
			continue
		}
		if field.Value != nil {
			if err := c.compileFieldInitValue(field.Value); err != nil {
				return nil, err
			}
		} else {
			c.emitter.EmitNoOperand(bytecode.OP_UNDEFINED)
		}
		keyIdx := c.constants.AddConstant(object.NewString(field.Name))
		c.emitter.Emit(bytecode.OP_SET_PROP, keyIdx)
	}

	// constructor 体
	// 防御: ctor 理论上恒有 Body (parseClassMember 只在 '(' 形状下置
	// IsConstructor), 但历史上有字段名 constructor 混进来的形状
	// (IsConstructor=true + Body=nil, 2026-10-05 修于 parser 层);
	// 这里 nil 检查保证再出现时是编译错而不是进程 panic。
	if ctor != nil && ctor.Body != nil {
		if err := c.compileStatements(ctor.Body.Statements); err != nil {
			return nil, err
		}
	}
	c.emitter.EmitNoOperand(bytecode.OP_RETURN_VOID)

	fnIns := c.emitter.Bytes()
	fnSrcPositions := c.srcPositions
	c.srcPositions = prevSrcPositions
	c.emitter = prevEmitter
	c.scope = prevScope
	c.currentArgumentsSlot = prevArgumentsSlot
	c.controlStack = prevControlStack
	c.pendingLabel = prevPendingLabel
	c.tryScopes = prevTryScopes
	c.finallyRetSlot = prevFinallyRetSlot

	// 类构造函数 (类值本身) 的 name 应为**类名** (规范 ClassDefinitionEvaluation
	// 步骤 SetFunctionName(F, className)); 匿名类先留空, 由 NamedEvaluation
	// 语境补名。此前硬编码 "constructor" 会让 `class X{}.name` 错误地是
	// "constructor"。
	ctorName := className
	if ctorName == "<anonymous>" {
		ctorName = ""
	}
	meta := bytecode.NewFunctionMetadata(ctorName, fnIns, fnScope.NumLocals(), len(paramSpecs), paramSpecs, false)
	meta.BaseSlot = baseSlot
	meta.CapturePrefixLen = computeCapturePrefixLen(fnIns, baseSlot, c.constants)
	meta.ArgumentsSlot = argumentsSlot
	meta.IsStrict = c.strict
	meta.Positions = toSrcPosList(fnSrcPositions)
	return meta, nil
}

// compileParamBinding 在函数入口生成参数默认值与解构模式绑定序列。
// 调用前提: 函数帧已建立, VM 已按 ParameterSpec 把实参放进 paramSlots 对应槽位
// (rest 参数已收集成数组)。普通函数与 class constructor 共用此逻辑。
//
//   - 默认值: 仅当槽内值**恰为 undefined** 时用默认表达式替换 (规范 9.2.10:
//     null 不触发默认, 会原样进入解构并触发 RequireObjectCoercible)。
//   - 解构模式: LOAD 隐藏槽 → 解构到各绑定 (默认值已在此之前填入隐藏槽)。

func (c *Compiler) compileClassMethodToObject(m *ast.ClassMethod, superName string) error {
	prevSuper := c.currentSuperClass
	c.currentSuperClass = superName
	// class 方法是"方法定义": 无 [[Construct]] / 无 prototype (构造函数走
	// compileClassConstructor, 不经此路径, 保留 prototype)。
	c.methodFnDepth++
	meta, err := c.compileFunctionWithStrict(true, m.Name, m.Parameters, m.Body, false, m.IsGenerator, m.IsAsync)
	c.methodFnDepth--
	c.currentSuperClass = prevSuper
	if err != nil {
		return err
	}
	midx := c.constants.AddConstant(meta)
	if m.ComputedKey != nil && !m.IsGetter && !m.IsSetter {
		// 动态键普通方法: SET_INDEX 弹 [obj, key, val] 三元组,
		// 先 DUP obj 让写入消耗副本 (getter/setter 走 DYN 弹2保留 obj, 不需要)。
		c.emitter.EmitNoOperand(bytecode.OP_DUP)
	}
	c.emitter.Emit(bytecode.OP_FUNCTION, midx)
	if m.ComputedKey != nil {
		// 动态键: 栈 [obj(, obj), fn] → key → [obj, (obj,) fn, key] → DYN 访问器/SET_INDEX
		if err := c.compileExpression(m.ComputedKey); err != nil {
			return err
		}
		switch {
		case m.IsGetter:
			c.emitter.EmitNoOperand(bytecode.OP_SET_GETTER_DYN)
		case m.IsSetter:
			c.emitter.EmitNoOperand(bytecode.OP_SET_SETTER_DYN)
		default:
			c.emitter.EmitNoOperand(bytecode.OP_SWAP) // [obj, fn, key] → [obj, key, fn] 对齐 SET_INDEX 栈序
			c.emitter.EmitNoOperand(bytecode.OP_SET_INDEX)
			c.emitter.EmitNoOperand(bytecode.OP_POP)
		}
		return nil
	}
	propKey := m.Name
	if m.IsPrivate {
		propKey = c.privateKey(m.Name)
	}
	keyIdx := c.constants.AddConstant(object.NewString(propKey))
	if m.IsGetter {
		c.emitter.Emit(bytecode.OP_SET_GETTER, keyIdx)
	} else if m.IsSetter {
		c.emitter.Emit(bytecode.OP_SET_SETTER, keyIdx)
	} else {
		c.emitter.Emit(bytecode.OP_SET_PROP, keyIdx)
	}
	return nil
}

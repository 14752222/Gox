package vm

import (
	"math"
	"strconv"
	"strings"

	"github.com/14752222/Gox/object"
)

func (vm *VM) inOperator(obj, key object.Value) object.Value {
	k := propKey(key)

	// Proxy: 走 has trap
	if p, ok := obj.(*object.Proxy); ok {
		found, err := vm.proxyHas(p, k)
		if err != nil {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(found)
	}

	// 普通对象: 沿原型链检查
	if obj == nil {
		return object.NewBoolean(false)
	}

	// 数组索引需检查是否真正存在: 越界索引不算属性存在 (GetProperty 对越界
	// 返回 Undefined+found=true, 用于 arr[i] 取 undefined, 但 in 语义要求 false)。
	if arr, ok := obj.(*object.Array); ok {
		if idx, err := strconv.Atoi(k); err == nil {
			return object.NewBoolean(idx >= 0 && idx < len(arr.Elements))
		}
	}

	_, found := obj.GetProperty(k)
	return object.NewBoolean(found)
}

// instanceOf 实现 `obj instanceof Constructor` 语义。
// 通过原型链检查: Constructor.prototype 是否出现在 obj 的原型链上。
// 该运行时未实现标准内置构造器的统一原型模型, 因此做了实用化处理:
// 对通过 new 构造的对象 (其 Proto 被设为构造器原型) 沿链查找; 对
// 已知的内置类型 (Array/Map/Set/RegExp/Promise/Error 等) 做类型名匹配。

func (vm *VM) instanceOf(left, right object.Value) object.Value {
	// 右侧必须是可调用构造器; 但内置构造器 (如 Array) 是 *object.Object,
	// IsCallable 不识别, 因此 Object 类型也允许继续匹配。
	_, isObj := right.(*object.Object)
	if !object.IsCallable(right) && !isObj {
		return object.NewBoolean(false)
	}

	// Proxy 作为构造器: 递归解包
	if p, ok := right.(*object.Proxy); ok {
		if pt, ok := p.Target.(*object.Proxy); ok {
			return vm.instanceOf(left, pt)
		}
	}

	// 规范 7.3.19 OrdinaryHasInstance 第 3 步: 左值不是对象时直接返回
	// false, 且**不读取** rval.prototype —— getter/setter 不得被触发
	// (test262 instanceof/prototype-getter-with-primitive.js: 在
	// Function.prototype 上装抛错 getter 后 `0 instanceof Function.prototype`
	// 必须静默得 false)。此前无条件读 ctorProto, 该用例依赖
	// defineProperty 对函数类是 no-op 才"意外"通过; 自有属性接口打通后
	// 必须按规范短路。
	if !object.IsObjectLike(left) {
		return object.NewBoolean(false)
	}

	// 右侧构造器的 prototype 属性
	var ctorProto object.Value
	switch c := right.(type) {
	case *object.Object:
		ctorProto, _ = c.GetProperty("prototype")
	case *object.BuiltinFunction:
		// 内建构造器 (Object/Array/String/Number/...) 的 prototype
		ctorProto, _ = c.GetProperty("prototype")
	case *object.Closure:
		// 闭包构造器: 取 prototype 属性 (new A() 时实例的原型)
		ctorProto, _ = c.GetProperty("prototype")
	}

	// 沿 left 的原型链查找 ctorProto
	if ctorProto != nil {
		cur := left
		// 最多沿链查 64 层防止循环
		for i := 0; i < 64 && cur != nil; i++ {
			if cur == ctorProto {
				return object.NewBoolean(true)
			}
			cur = protoOf(cur)
		}
	}

	// 内置类型匹配兜底
	return object.NewBoolean(matchBuiltinType(left, right))
}

// protoOf 返回值的原型对象 (沿对象模型的原型字段)。
// protoOf 是 object.ProtoOf 的薄包装 —— 原型链遍历的事实来源只有一处
// (见 object/proto.go 的说明): @@hasInstance 与 instanceof 必须给出同样的
// 结论, 两边各写一份类型穷举迟早分叉。

func protoOf(v object.Value) object.Value {
	return object.ProtoOf(v)
}

// matchBuiltinType / builtinName 是 object.MatchBuiltinType / object.BuiltinName
// 的薄包装 —— instanceof 与 %Function.prototype%[@@hasInstance] 必须给出同样的
// 结论, 两份匹配表迟早分叉。

func matchBuiltinType(left, right object.Value) bool {
	return object.MatchBuiltinType(left, right)
}

func builtinName(v object.Value) string { return object.BuiltinName(v) }

// describeCallee 生成错误信息中对"被当作函数调用的值"的简短描述。
//
// 直接用 Inspect 会把整个内建对象 (含 prototype 上的几十个方法) 塞进错误信息，
// 例如 `Array(3)` 未定义时会打印几千字符。这里对超长描述做截断，
// 并优先使用构造器/函数的名字。
// invokeWithThis 以 thisVal 为 this 调用 fn, args 为实参。
//
// OP_CALL_METHOD 与 OP_CALL_METHOD_SPREAD 共用: 两者只差实参来源 (栈上定长 vs
// 数组), 调用语义完全一致。返回值遵守 VM 的统一约定 ——
//   - nil: 调用已完成 (结果已压栈), 或异常已被 catch/finally 接住, 调用方继续;
//   - 非 nil: 异常继续向外传播, 调用方 return。

func propKey(v object.Value) string {
	switch k := v.(type) {
	case *object.String:
		return k.Value
	case *object.Number:
		return k.Inspect()
	case *object.Symbol:
		return k.Inspect()
	}
	return v.Inspect()
}

func isCallable(v object.Value) bool {
	if object.IsCallable(v) {
		return true
	}
	if p, ok := v.(*object.Proxy); ok {
		return isCallable(p.Target)
	}
	return false
}

// handleThrow 处理异常抛出。
// 检查 tryStack 中是否有匹配的 catch/finally 处理器。
// 如果找到: 设置 PC 和栈，返回 true。
// 如果未找到: 返回 false (异常将传播到上层)。
// handleThrow 包装异常处理器搜索: 进入搜索前记录抛出点 PC (T05 源码帧)。
// 若最终无处理器匹配 (返回 false), 该 PC 就是出错指令位置; 若有 catch/
// finally 匹配, PC 会被改写、异常不算未捕获, 记录值作废 (下次抛出覆盖)。

const maxStringLength = 1 << 30

// concatStrings 带上限的字符串拼接。超限时返回 RangeError (Invalid string
// length)，与 String.prototype.repeat/padStart 的既有行为一致。

func concatStrings(a, b string) (string, error) {
	if len(a)+len(b) > maxStringLength {
		return "", &jsThrow{Name: "RangeError", Message: "Invalid string length"}
	}
	return a + b, nil
}

func (vm *VM) addValues(a, b object.Value) (object.Value, error) {
	// 字符串拼接
	if aStr, ok := a.(*object.String); ok {
		s, err := concatStrings(aStr.Value, toJSString(b))
		if err != nil {
			return nil, err
		}
		return object.NewString(s), nil
	}
	if bStr, ok := b.(*object.String); ok {
		s, err := concatStrings(toJSString(a), bStr.Value)
		if err != nil {
			return nil, err
		}
		return object.NewString(s), nil
	}
	// 数组拼接 (简化)
	if aArr, ok := a.(*object.Array); ok {
		if bArr, ok := b.(*object.Array); ok {
			elements := make([]object.Value, 0, len(aArr.Elements)+len(bArr.Elements))
			elements = append(elements, aArr.Elements...)
			elements = append(elements, bArr.Elements...)
			return object.NewArray(elements), nil
		}
	}
	// BigInt 加法。
	// 字符串拼接已在上面处理 (BigInt 与 String 相加时走 ToString 得到 "1")，
	// 因此这里只需区分 BigInt+BigInt 与 BigInt+非 BigInt 两种情况。
	aBig, aIsBig := a.(*object.BigInt)
	bBig, bIsBig := b.(*object.BigInt)
	if aIsBig || bIsBig {
		if aIsBig && bIsBig {
			return aBig.Add(bBig), nil
		}
		return nil, errMixBigInt
	}

	// 数字加法
	aNum := toNumber(a)
	bNum := toNumber(b)
	return object.NewNumber(aNum + bNum), nil
}

// getIndex 实现索引访问 obj[index]。

func (vm *VM) getIndex(obj, index object.Value) object.Value {
	// 键归一 (ToPropertyKey): 编译器对「键只用一次」的场合 (简单赋值 /
	// for-of 目标 / 解构目标) 不再提前发 OP_TO_PROPERTY_KEY, 由这里补 ——
	// 这样 ToPropertyKey 落在右值求值之后, 求值顺序合规。对已被
	// OP_TO_PROPERTY_KEY 转过一次的键是幂等的 (原语原样返回), 不会二次
	// 触发用户 toString。对象键转换会调用户代码, 故需消费回调桥异常。
	index = vm.normalizeIndexKey(index)

	// Symbol 键**统一通道** —— 提到类型 switch 之前。
	//
	// 此前按类型穷举: *Array / *TypedArray / *Object / 函数类各写一份
	// `if sym, ok := index.(*object.Symbol); ok { return getSymbolIndexedValue }`,
	// 漏一个类型就静默回落 default 分支 (把 Symbol 转成字符串去查自有属性),
	// 恒得 undefined。*object.String 正是漏掉的那个: 即便
	// %String.prototype%[@@iterator] 已装配, `"abc"[Symbol.iterator]` 还是
	// undefined; *Map / *Set 同理。
	//
	// Symbol 键读取的语义与对象类型无关 (自身 + 原型链 + 访问器展开), 没理由
	// 按类型各写一遍。收口到这里, 新增对象类型不必再记得补分支。
	if sym, ok := index.(*object.Symbol); ok {
		return vm.getSymbolIndexedValue(obj, sym)
	}

	switch o := obj.(type) {
	case *object.Array:
		// 已通过 Object.defineProperty 定义过描述符 (索引访问器 / 不可写)
		// 的数组, 索引读必须走描述符通道 (GetProperty 调 getter), 与自有
		// 属性接口保持一致。PropDescs 为空 (绝大多数数组) 时走下方的快速
		// 路径, 行为与此前完全一致。
		if len(o.PropDescs) > 0 {
			val, found := o.GetProperty(toJSString(index))
			if !found || val == nil {
				return object.UndefinedSingleton
			}
			return val
		}
		if n, ok := index.(*object.Number); ok {
			idx := int(n.Value)
			if idx >= 0 && idx < len(o.Elements) {
				if o.Elements[idx] == nil {
					return object.UndefinedSingleton
				}
				return o.Elements[idx]
			}
		}
		// 字符串索引
		if s, ok := index.(*object.String); ok {
			val, _ := o.GetProperty(s.Value)
			return val
		}
		return object.UndefinedSingleton

	case *object.String:
		if n, ok := index.(*object.Number); ok {
			idx := int(n.Value)
			if idx >= 0 && idx < len(o.Value) {
				return object.NewString(string(o.Value[idx]))
			}
			return object.UndefinedSingleton
		}
		// 字符串键: "0"/"1" 这类索引先按码元取字符, 其余 (length /
		// 原型方法, 如 s["length"] === 5) 走属性查找 —— 必须与 s.length
		// 同口径, 否则 `let {length} = str` 解构拿到 undefined。
		if s, ok := index.(*object.String); ok {
			if idx, err := strconv.Atoi(s.Value); err == nil && idx >= 0 {
				if idx < len(o.Value) {
					return object.NewString(string(o.Value[idx]))
				}
				return object.UndefinedSingleton
			}
			if val, found := o.GetProperty(s.Value); found {
				return val
			}
			return object.UndefinedSingleton
		}
		return object.UndefinedSingleton

	case *object.TypedArray:
		if n, ok := index.(*object.Number); ok {
			idx := int(n.Value)
			if idx >= 0 && idx < o.Length {
				return o.GetElement(idx)
			}
			return object.UndefinedSingleton
		}
		if s, ok := index.(*object.String); ok {
			val, _ := o.GetProperty(s.Value)
			return val
		}
		return object.UndefinedSingleton

	case object.RxIndexed:
		if n, ok := index.(*object.Number); ok {
			return o.GetIndexedElement(int(n.Value))
		}
		if s, ok := index.(*object.String); ok {
			val, _ := o.(object.Value).GetProperty(s.Value)
			return val
		}
		return object.UndefinedSingleton

	case *object.Object:
		if s, ok := index.(*object.String); ok {
			val, found := o.GetProperty(s.Value)
			if !found {
				return object.UndefinedSingleton
			}
			return val
		}
		// Symbol 键: 检查自身及原型链上的 Symbol 属性。访问器 (getter)
		// 必须展开调用 (this = 原接收者 o) —— 如 `get [Symbol.iterator](){}`。
		// getter 抛错经回调桥记录, 由 GET_INDEX 之后的 checkCallbackErr 消费。
		if sym, ok := index.(*object.Symbol); ok {
			if desc, found := object.LookupSymbolPropertyDescriptor(o, sym); found {
				if acc, isAcc := desc.Value.(*object.Accessor); isAcc {
					if acc.Getter != nil && object.IsCallable(acc.Getter) {
						v := object.CallFunction(acc.Getter, o)
						if v == nil {
							v = object.UndefinedSingleton
						}
						return v
					}
					return object.UndefinedSingleton
				}
				if desc.Value == nil {
					return object.UndefinedSingleton
				}
				return desc.Value
			}
			return object.UndefinedSingleton
		}
		// 数字等其他键型按 ToPropertyKey 语义转字符串 (o[2] === o["2"])
		val, found := o.GetProperty(toJSString(index))
		if !found {
			return object.UndefinedSingleton
		}
		return val

	case *object.Closure, *object.BuiltinFunction, *object.BuiltinMethod:
		// 函数对象: 数字等键型按 ToPropertyKey 转字符串 (f[1] === f["1"])。
		// (Symbol 键已在上方统一通道处理。)
		if s, ok := index.(*object.String); ok {
			val, found := o.GetProperty(s.Value)
			if !found {
				return object.UndefinedSingleton
			}
			return val
		}
		val, found := o.GetProperty(toJSString(index))
		if !found {
			return object.UndefinedSingleton
		}
		return val

	default:
		// 其余类型 (GlobalObject / Map / Set / Closure / Error / RegExp / Promise ...)
		// 的字符串键读取统一走 Value 接口 —— 与 setIndex 的 default 兜底分支对称。
		//
		// 必须守住的不变量: obj["k"] 恒等于 obj.k (二者只是同一属性访问的两种写法)。
		// 修复前这些类型会直接落到本函数末尾返回 undefined, 于是:
		//   globalThis.z      -> 42          globalThis["z"]     -> undefined  (错)
		//   map.size          -> 1           map["size"]         -> undefined  (错)
		// 更要命的是 ++ / -- / += 作用在成员表达式上时, compileIncDec 走的是
		// GET_INDEX 路径 (见 compiler.compileMemberRef + OP_DUP2/OP_GET_INDEX),
		// 所以 `globalThis.n++` 读到 undefined, ToNumber 后算出 NaN —— 而完全同形的
		// `obj.n++` (普通对象) 一切正常, 极难排查。
		if s, ok := index.(*object.String); ok {
			val, found := obj.GetProperty(s.Value)
			if !found {
				return object.UndefinedSingleton
			}
			return val
		}
		// Symbol 键: 实现了 GetSymbolProperty 的类型 (如 AsyncGenerator 的
		// [Symbol.asyncIterator]) 在此响应; 其余维持原有行为返回 undefined。
		if sym, ok := index.(*object.Symbol); ok {
			if sp, ok := obj.(interface {
				GetSymbolProperty(*object.Symbol) (object.Value, bool)
			}); ok {
				if val, found := sp.GetSymbolProperty(sym); found {
					return val
				}
			}
			return object.UndefinedSingleton
		}
		// 数字等键型按 ToPropertyKey 语义转字符串 (o[2] === o["2"])
		val, found := obj.GetProperty(toJSString(index))
		if !found {
			return object.UndefinedSingleton
		}
		return val
	}
}

// getSymbolIndexedValue 读取数组/类型化数组的 Symbol 键属性值 (自身 + 原型链)。
// 访问器 (getter) 展开调用 (this = 原接收者) —— getter 抛错经 object 回调桥
// 记录, 由调用点 (OP_GET_INDEX) 之后的 checkCallbackErr 消费。

func (vm *VM) getSymbolIndexedValue(receiver object.Value, sym *object.Symbol) object.Value {
	desc, found := object.LookupSymbolPropertyDescriptorChain(receiver, sym)
	if !found {
		return object.UndefinedSingleton
	}
	if acc, isAcc := desc.Value.(*object.Accessor); isAcc {
		if acc.Getter != nil && object.IsCallable(acc.Getter) {
			v := object.CallFunction(acc.Getter, receiver)
			if v == nil {
				v = object.UndefinedSingleton
			}
			return v
		}
		return object.UndefinedSingleton
	}
	if desc.Value == nil {
		return object.UndefinedSingleton
	}
	return desc.Value
}

// markAssignmentEnumerable 把函数对象上刚被"赋值/定义"的自有属性转成可枚举
// (规范: 普通赋值与 class 静态字段的 [[Enumerable]] 为 true)。函数类的
// SetProperty 默认按内建语义置为不可枚举, 由这里按运行时上下文显式转正。
//
// 例外: 在 class 静态初始化帧 (__static_init__) 中, 若被写的是"方法定义"
// 产出的函数 (IsMethod) —— class 静态方法/访问器在规范里不可枚举 —— 保持
// 不可枚举。class 静态字段 (普通值 / 非方法函数) 仍可枚举。

func (vm *VM) markAssignmentEnumerable(obj object.Value, name string, val object.Value, inStaticInit bool) {
	if inStaticInit {
		if c, ok := val.(*object.Closure); ok && c.Fn != nil && c.Fn.IsMethod {
			return // class 静态方法: 不可枚举
		}
		if _, ok := val.(*object.Accessor); ok {
			return // class 静态访问器: 不可枚举
		}
	}
	switch o := obj.(type) {
	case *object.Closure:
		o.MarkEnumerable(name)
	case *object.BuiltinFunction:
		o.MarkEnumerable(name)
	}
}

// inStaticInitFrame 报告当前帧是否是 class 静态初始化合成帧。

func (vm *VM) inStaticInitFrame(frame *Frame) bool {
	return frame.Closure != nil && frame.Closure.Fn != nil && frame.Closure.Fn.Name == "__static_init__"
}

// defineClosureAccessor 在函数对象 (class 构造器) 上定义静态访问器:
// 存入 Closure.Props 的 *object.Accessor, 由 Closure.Get/SetProperty 解释。
// obj 非 *object.Closure 时静默忽略 (与 SET_GETTER 对未知类型的行为一致)。

func (vm *VM) defineClosureAccessor(obj object.Value, propName string, fn object.Value, isGetter bool) {
	c, ok := obj.(*object.Closure)
	if !ok {
		return
	}
	var acc *object.Accessor
	if cur, exists := c.Props[propName]; exists {
		if a, isAcc := cur.(*object.Accessor); isAcc {
			acc = a
		}
	}
	if acc == nil {
		acc = &object.Accessor{}
		c.SetProperty(propName, acc)
	}
	if isGetter {
		acc.Getter = fn
	} else {
		acc.Setter = fn
	}
}

// resolveSymbolIterator 预解析实现了 [Symbol.iterator] 的普通对象:
// 在 VM 层调用该方法并适配返回值。返回 (迭代器, true, nil) 表示已解析;
// (nil, false, nil) 表示没有该方法 (交回 GetIterable 的具体类型分发)。
// 生成器方法的返回值是 *object.Generator, 只有 VM 能驱动其字节码帧,
// 因此这一步必须在 VM 层做而不能依赖 runtime.GetIterable。
// makeIterStep 把 (value, done) 包成 next() 结果的形状 {value, done}。

func (vm *VM) objectRest(src, excluded object.Value) (object.Value, error) {
	rest := object.NewObject()
	excludedStr := map[string]bool{}
	excludedSym := map[uint64]bool{}
	if arr, ok := excluded.(*object.Array); ok {
		for _, k := range arr.Elements {
			if sym, isSym := k.(*object.Symbol); isSym {
				excludedSym[sym.ID] = true
			} else {
				excludedStr[object.ToString(k)] = true
			}
		}
	}
	switch s := src.(type) {
	case *object.Object:
		for _, k := range s.EnumerableKeys() {
			if excludedStr[k] {
				continue
			}
			v, _ := s.GetProperty(k) // 触发 getter (访问器 this = s)
			if err := vm.checkCallbackErr(); err != nil {
				return nil, err
			}
			// CreateDataProperty 语义: 绕过 __proto__ setter 等原型链副作用,
			// 直接建自有可枚举数据属性 (源的自有 "__proto__" 要原样复制)。
			rest.DefineOwnProperty(k, object.DataProperty(v))
		}
		for _, sym := range s.SymbolKeys() {
			desc, ok := s.SymbolProperties[sym.ID]
			if !ok || !desc.Enumerable || excludedSym[sym.ID] {
				continue
			}
			var v object.Value = desc.Value
			if acc, isAcc := desc.Value.(*object.Accessor); isAcc {
				if acc.Getter != nil && object.IsCallable(acc.Getter) {
					var err error
					v, err = vm.callFunction(acc.Getter, s, nil)
					if err != nil {
						return nil, err
					}
					if err := vm.checkCallbackErr(); err != nil {
						return nil, err
					}
				} else {
					v = object.UndefinedSingleton
				}
			}
			if v == nil {
				v = object.UndefinedSingleton
			}
			rest.SetSymbolProperty(sym, v)
		}
	case *object.Array:
		for i, v := range s.Elements {
			k := strconv.Itoa(i)
			if excludedStr[k] {
				continue
			}
			if v == nil {
				v = object.UndefinedSingleton
			}
			rest.DefineOwnProperty(k, object.DataProperty(v))
		}
	case *object.String:
		for i, ch := range object.SplitCharsUTF16(s.Value) {
			k := strconv.Itoa(i)
			if excludedStr[k] {
				continue
			}
			rest.DefineOwnProperty(k, object.DataProperty(object.NewString(ch)))
		}
	}
	return rest, nil
}

// hasNextKey 沿原型链检查对象的 "next" 键是否存在, 不触发 getter。
// Gox 的非规范扩展: 「裸 next 对象」本身可作迭代器 (for-await/for-of 的
// 自建迭代器形状)。用描述符查找而非 GetProperty: 提前访问 next getter
// 会多产生一次属性访问, 破坏 yield* 委托的属性访问顺序
// (r6e5qp, test262 yield-star-*-next 系列记的就是这个序)。

func hasNextKey(o *object.Object) bool {
	for cur := o; cur != nil; {
		if _, found := cur.Properties["next"]; found {
			return true
		}
		next, ok := cur.Proto.(*object.Object)
		if !ok || next == nil {
			return false
		}
		cur = next
	}
	return false
}

// hasSymbolProps 判断值自身或原型链上是否可能持有符号键属性 —— 即
// resolveSymbolIterator 是否值得走符号键通道。仅 *Object 与实现
// SymbolPropertyStore 的 *Array/*TypedArray 为真; 其余值交回
// runtime.GetIterable 的 Go 层兜底 (字符串/Map/Set 迭代器等)。

func hasSymbolProps(val object.Value) bool {
	switch v := val.(type) {
	case *object.Object:
		return true
	case object.SymbolPropertyStore:
		// 数组/类型化数组自身无符号键时, 其原型 (Array.prototype 等,
		// 为 *Object) 仍可能挂了 @@iterator; 沿 GetProto 继续判一层。
		_ = v
		return true
	}
	return false
}

// errHandledThrow 表示"抛错已被 try/catch 处理器接住" (PC 已跳到 catchPC)。
// 由 resolveSymbolIterator 在 @@iterator 非可调用 / 不可迭代时返回, 提示
// OP_GET_ITERATOR 直接 continue 重新取指, 不再执行后续兜底逻辑。

func (vm *VM) setIndex(obj, index, val object.Value, inStaticInit bool) {
	// 键归一 (ToPropertyKey): 见 getIndex 同款说明。简单赋值 base[prop] = v
	// 的键转换在此发生 ⇒ 晚于右值求值 (assignment/
	// target-member-computed-reference.js)。复合赋值等已在编译期转过一次,
	// 这里是幂等的。
	index = vm.normalizeIndexKey(index)
	switch o := obj.(type) {
	case *object.Array:
		// Symbol 键: 数组此前没有符号键槽, arr[Symbol.iterator] = fn 被静默
		// 丢弃。现落进数组自己的 SymbolProperties (用户赋值语义: 新建全 true
		// 数据属性, 或按既有描述符写入)。
		if sym, ok := index.(*object.Symbol); ok {
			o.SetSymbolProperty(sym, val)
			return
		}
		// 已通过 Object.defineProperty 定义过描述符的数组, 索引写走
		// SetProperty 的描述符语义 (访问器调 setter / 不可写静默失败)。
		// PropDescs 为空时走下方快速路径, 行为与此前完全一致。
		if len(o.PropDescs) > 0 {
			o.SetProperty(toJSString(index), val)
			return
		}
		if n, ok := index.(*object.Number); ok {
			idx := int(n.Value)
			if idx >= 0 {
				for len(o.Elements) <= idx {
					o.Elements = append(o.Elements, object.UndefinedSingleton)
				}
				o.Elements[idx] = val
				return
			}
		}
		// 非数字索引 (如 arr.foo = 1) 走通用属性设置
		if s, ok := index.(*object.String); ok {
			o.SetProperty(s.Value, val)
		}
	case *object.TypedArray:
		if sym, ok := index.(*object.Symbol); ok {
			o.SetSymbolProperty(sym, val)
			return
		}
		if n, ok := index.(*object.Number); ok {
			idx := int(n.Value)
			if idx >= 0 {
				// 越界写按规范静默忽略 (setElement 内部处理)
				o.SetElement(idx, val)
			}
			return
		}
		if s, ok := index.(*object.String); ok {
			o.SetProperty(s.Value, val)
		}
	case object.RxIndexed:
		if n, ok := index.(*object.Number); ok {
			o.SetIndexedElement(int(n.Value), val)
		}
	case *object.Object:
		if s, ok := index.(*object.String); ok {
			o.SetProperty(s.Value, val)
		} else if sym, ok := index.(*object.Symbol); ok {
			o.SetSymbolProperty(sym, val)
		} else {
			// 数字等其他键型按 ToPropertyKey 语义转字符串 (o[2] === o["2"])
			o.SetProperty(toJSString(index), val)
		}
	case *object.Closure, *object.BuiltinFunction, *object.BuiltinMethod:
		// 函数对象: Symbol 键落各自的符号键槽 (SymbolPropertyStore); 字符串键与
		// 数字等键型 (f[1] === f["1"]) 统一走 SetProperty + 转可枚举。
		// 此前数字键在 default 分支被静默丢弃 (default 只处理 String/Symbol),
		// 于是 `f[1] = 4` 之后 f[1] 恒为 undefined。
		if sym, ok := index.(*object.Symbol); ok {
			if sp, ok := o.(object.SymbolPropertyStore); ok {
				sp.SetSymbolProperty(sym, val)
			}
			return
		}
		name := toJSString(index)
		o.SetProperty(name, val)
		// 函数对象的计算键赋值: 与 OP_SET_PROP 一致地转可枚举。
		vm.markAssignmentEnumerable(o, name, val, inStaticInit)
	default:
		// 其余类型 (Error/RegExp/Closure/Promise 等) 的字符串键赋值
		// 统一走 Value 接口。基础类型 (String/Number/null/undefined) 的
		// SetProperty 是无操作，与 JS 原始值语义一致。
		if s, ok := index.(*object.String); ok {
			obj.SetProperty(s.Value, val)
			// 函数对象的计算键赋值: 与 OP_SET_PROP 一致地转可枚举。
			vm.markAssignmentEnumerable(obj, s.Value, val, inStaticInit)
		} else if sym, ok := index.(*object.Symbol); ok {
			if sp, ok := obj.(interface {
				SetSymbolProperty(*object.Symbol, object.Value)
			}); ok {
				sp.SetSymbolProperty(sym, val)
			}
		}
	}
}

// normalizeIndexKey 把计算成员键的原始值按 ToPropertyKey 归一。
//
// 幂等: 已是原语 (String/Symbol/Number/BigInt/Boolean/null/undefined) 的键
// 原样返回 —— 数字键保留给数组/类型化数组的数字索引快路径, 符号键保留给
// 符号属性通道。仅对象键 (可能带用户 toString/valueOf) 会在这里转成字符串,
// 因此**同一引用的两次索引操作只触发一次用户代码**。
//
// 转换可能调用用户 toString ⇒ 经回调桥抛出: 这里只做转换, 异常由
// 调用方 (OP_GET_INDEX / OP_SET_INDEX) 紧跟的 checkCallbackErr 消费;
// 非 opcode 调用点 (如内建直接调 getIndex) 由 object 层的回调错误机制兜底。

func (vm *VM) normalizeIndexKey(index object.Value) object.Value {
	switch index.Type() {
	case object.STRING_OBJ, object.SYMBOL_OBJ,
		object.NUMBER_OBJ, object.BOOLEAN_OBJ, object.BIGINT_OBJ,
		object.NULL_OBJ, object.UNDEFINED_OBJ:
		return index
	}
	return toPropertyKey(index)
}

// ===== 类型转换辅助函数 =====

// toNumber 将任意值转换为 float64。

func toNumber(v object.Value) float64 {
	switch val := v.(type) {
	case *object.Number:
		return val.Value
	case *object.Boolean:
		if val.Value {
			return 1
		}
		return 0
	case *object.Null:
		return 0
	case *object.Undefined:
		return math.NaN()
	case *object.String:
		return object.ParseJSNumber(val.Value)
	case *object.Date:
		// 规范 ToPrimitive(date, number): Date 的 @@toPrimitive 在 number
		// 提示下返回其 time value —— 这正是 `d1 - d2` / `+d` / 关系比较
		// 拿到毫秒差的唯一路径。本运行时的 toNumber 尚未实现通用的
		// ToPrimitive(@@toPrimitive → valueOf → toString) 协议, 这里按
		// Date 的规范语义定点补全: *.Date 的内部 time value 即 ToNumber 结果。
		return val.TimeValue()
	default:
		return math.NaN()
	}
}

// toJSString 将任意值转换为 JavaScript 字符串表示。
// 使用 ECMAScript ToString 语义 (数组 join、对象 [object Object]、
// 自定义 toString 优先)，见 object.ToString。

func toJSString(v object.Value) string {
	return object.ToString(v)
}

// toPropertyKey 实现规范 ToPropertyKey 的「求值一次」语义, 供 OP_TO_PROPERTY_KEY 使用。
//
// 已经是原语的键原样返回: 原语到属性键的转换是纯函数、重复调用不可观测,
// 而保留数字/符号键还能让数组 / 类型化数组的数字索引快路径原样工作。
// 只有对象键 (可带用户 toString/valueOf) 需要在此转成字符串一次, 之后
// GET_INDEX 与 SET_INDEX 共用它, 不再二次触发用户代码。

func toPropertyKey(v object.Value) object.Value {
	switch v.Type() {
	case object.STRING_OBJ, object.SYMBOL_OBJ,
		object.NUMBER_OBJ, object.BOOLEAN_OBJ, object.BIGINT_OBJ,
		object.NULL_OBJ, object.UNDEFINED_OBJ:
		return v
	}
	return object.NewString(object.ToString(v))
}

// safeKeyForError 为「基为 null/undefined」的成员访问错误的**消息**取键的
// 可读描述, 但**绝不触发用户代码**。
//
// 规范 GetValue/PutValue 在 ToObject(base) 抛 TypeError 时根本还没做
// ToPropertyKey, 因此这里不能调 toJSString(index): 那会触发对象键的
// toString, 把本该抛 TypeError 的场合变成抛用户异常 (test262
// member-expression/computed-reference-null-or-undefined 期望 TypeError,
// 且键的 toString 不得被调用)。只在键已是 String 时取用其值, 其余给占位符。

func safeKeyForError(index object.Value) string {
	if s, ok := index.(*object.String); ok {
		return s.Value
	}
	return "[unknown]"
}

// looseEquals 实现 JavaScript 的 == (宽松相等)。

func looseEquals(a, b object.Value) bool {
	// 同类型直接比较
	if a.Type() == b.Type() {
		return strictEquals(a, b)
	}
	// null == undefined
	if isNullish(a) && isNullish(b) {
		return true
	}
	// BigInt == BigInt/Number/String (数学值比较)
	if a.Type() == object.BIGINT_OBJ || b.Type() == object.BIGINT_OBJ {
		return bigIntLooseEqual(a, b)
	}
	// number == string
	if a.Type() == object.NUMBER_OBJ && b.Type() == object.STRING_OBJ {
		return toNumber(a) == toNumber(b)
	}
	if a.Type() == object.STRING_OBJ && b.Type() == object.NUMBER_OBJ {
		return toNumber(a) == toNumber(b)
	}
	// boolean == other
	if a.Type() == object.BOOLEAN_OBJ {
		return toNumber(a) == toNumber(b)
	}
	if b.Type() == object.BOOLEAN_OBJ {
		return toNumber(a) == toNumber(b)
	}
	return false
}

// strictEquals 实现 JavaScript 的 === (严格相等)。

func strictEquals(a, b object.Value) bool {
	if a.Type() != b.Type() {
		return false
	}
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
	case *object.Null:
		_, ok := b.(*object.Null)
		return ok
	case *object.Undefined:
		_, ok := b.(*object.Undefined)
		return ok
	case *object.Symbol:
		if bv, ok := b.(*object.Symbol); ok {
			return av.ID == bv.ID
		}
	case *object.BigInt:
		if bv, ok := b.(*object.BigInt); ok {
			return bigIntStrictEqual(av, bv)
		}
	}
	// 引用类型 (对象/数组/函数/Map/Set/RegExp/...): 按引用同一性比较。
	// ECMAScript 的 IsStrictlyEqual 对 Object 类型即"是否同一个引用"，
	// 缺少这一分支时连 `o === o` 都会得到 false。
	// 所有 Value 实现都是指针类型，接口比较即指针比较。
	return a == b
}

// isNullish 检查值是否为 null 或 undefined。

func isNullish(v object.Value) bool {
	_, isNull := v.(*object.Null)
	_, isUndef := v.(*object.Undefined)
	return isNull || isUndef
}

// ===== 便捷函数 =====

// Eval 编译并执行 JS 源码，返回最后一个表达式的值。

func joinStrings(strs []string, sep string) string {
	return strings.Join(strs, sep)
}

// osReadFile 读取文件内容。

package object

import "strconv"

// Array 表示 JavaScript 的数组类型。
// 数组是特殊的对象，其属性名为数字索引，有 length 属性。
type Array struct {
	Elements []Value
	// proto 是数组对象的原型 (指向 ArrayProto)
	// 在标准库初始化时通过 SetArrayProto 设置
	proto Value
	// ExtraProps 存储非数字索引的额外属性 (如 exec 返回的 index, input)
	ExtraProps map[string]Value
	// PropDescs 存储 Object.defineProperty 显式定义过的自有属性描述符
	// (按属性名)。存在描述符时, 读/写该属性必须经描述符语义
	// (访问器调 getter/setter、不可写数据属性拒绝赋值), 见 OwnPropertyStore。
	PropDescs map[string]PropertyDescriptor
	// propKeyOrder 记录 PropDescs 字符串键的定义顺序 (OwnKeys 用)。
	propKeyOrder []string
	// SymbolProperties 存储以 Symbol 为键的自有属性 (键为 Symbol 唯一 ID),
	// 与字符串键空间隔离。数组此前没有 Symbol 键槽, 导致
	// `arr[Symbol.iterator] = fn` 被静默丢弃 (数组解构/for-of 忽略被覆盖的
	// 迭代器)。实现 SymbolPropertyStore 后与 *Object 同构。
	SymbolProperties map[uint64]PropertyDescriptor
	// symbolKeyOrder 记录 Symbol 键的定义顺序 (getOwnPropertySymbols 用)。
	symbolKeyOrder []*Symbol
	// NonExtensible 表示 [[Extensible]] 为 false (Object.preventExtensions /
	// seal / freeze 后)。采用**反向标志**: 零值即"可扩展", 于是所有
	// `&Array{}` / NewArray 构造点无需改动即得到正确的默认语义 ——
	// *Object.Extensible 那种正向布尔必须在每个构造点显式置 true, 漏一处
	// 就让新数组变成不可扩展 (Array 的构造点远多于 *Object)。
	NonExtensible bool
}

// holeValue 是数组洞 (elision) 的栈上标记类型。
// 只在 VM 栈上短暂存在: OP_NEW_ARRAY / OP_ARRAY_PUSH 写入 Elements 时
// 转成 nil (Elements 里 nil 即洞, GetProperty/Has 均按洞处理)。
type holeValue struct{}

func (h *holeValue) Type() ObjectType                     { return UNDEFINED_OBJ }
func (h *holeValue) Inspect() string                      { return "undefined" }
func (h *holeValue) IsTruthy() bool                       { return false }
func (h *holeValue) GetProperty(name string) (Value, bool) { return nil, false }
func (h *holeValue) SetProperty(name string, val Value)   {}

// HoleSingleton 是洞标记单例。
var HoleSingleton Value = &holeValue{}

// IsHole 判断值是否为洞标记。
func IsHole(v Value) bool {
	_, ok := v.(*holeValue)
	return ok
}

// ArrayProto 是所有数组实例的原型对象。
// 由 stdlib 包初始化时设置。包含 push, pop, map 等方法。
var ArrayProto Value

// SetArrayProto 设置全局数组原型 (由 stdlib 调用)。
func SetArrayProto(p Value) { ArrayProto = p }

func (a *Array) Type() ObjectType { return ARRAY_OBJ }
func (a *Array) Inspect() string  { return a.inspect(newInspectGuard()) }

// inspect 带环检测的打印实现，供 Object.inspect 递归调用时共享访问集合。
func (a *Array) inspect(g *inspectGuard) string {
	if g.arrs[a] {
		return "[Circular]"
	}
	if g.depth >= inspectMaxDepth {
		return "[ ... ]"
	}
	g.arrs[a] = true
	g.depth++
	defer func() {
		delete(g.arrs, a)
		g.depth--
	}()

	var elems []string
	for _, e := range a.Elements {
		// 字符串在数组中显示带引号 (inspectValue 统一处理)
		elems = append(elems, inspectValue(e, g))
	}
	return "[" + joinStrings(elems, ", ") + "]"
}

func (a *Array) IsTruthy() bool { return true }

func (a *Array) GetProperty(name string) (Value, bool) {
	switch name {
	case "length":
		return NewInt(int64(len(a.Elements))), true
	}

	// 显式定义过的描述符 (Object.defineProperty): 访问器调用 getter
	// (this = 数组本身), 数据属性返回描述符的值。
	if d, ok := a.PropDescs[name]; ok {
		if acc, isAcc := d.Value.(*Accessor); isAcc {
			if acc.Getter != nil && IsCallable(acc.Getter) {
				return CallFunction(acc.Getter, a), true
			}
			return UndefinedSingleton, true
		}
		if d.Value == nil {
			return UndefinedSingleton, true
		}
		return d.Value, true
	}

	// 额外属性 (如 index, input)
	if a.ExtraProps != nil {
		if val, ok := a.ExtraProps[name]; ok {
			return val, true
		}
	}

	// 数字索引访问: 仅当元素真实存在（非洞）时返回；
	// 洞与越界索引按规范继续沿原型链查找 (Array.prototype[1]=1 时
	// [,,,][1] 应读到 1)。
	if idx, err := strconv.Atoi(name); err == nil {
		if idx >= 0 && idx < len(a.Elements) && a.Elements[idx] != nil {
			return a.Elements[idx], true
		}
	}

	// 原型链查找
	if a.proto != nil {
		if v, ok := a.proto.GetProperty(name); ok {
			return v, true
		}
	}
	// 数字索引读取未命中时仍返回 undefined (JS 语义: arr[100] === undefined，
	// 不抛错)。非数字属性保持 (nil,false) 让调用方区分"不存在"。
	if _, err := strconv.Atoi(name); err == nil {
		return UndefinedSingleton, true
	}
	return nil, false
}

// Has 实现数组的 HasProperty（含原型链、显式描述符；nil 元素视为洞）。
// Array.prototype 泛型算法 (forEach/map/reduce/...) 的"索引是否存在"
// 判定必须走这里 —— 直接看 Elements 会漏掉原型上的同名索引。
func (a *Array) Has(name string) bool {
	if name == "length" {
		return true
	}
	if _, ok := a.PropDescs[name]; ok {
		return true
	}
	if a.ExtraProps != nil {
		if _, ok := a.ExtraProps[name]; ok {
			return true
		}
	}
	if idx, err := strconv.Atoi(name); err == nil {
		if idx >= 0 && idx < len(a.Elements) && a.Elements[idx] != nil {
			return true
		}
	}
	if a.proto != nil {
		return HasPropertyOf(a.proto, name)
	}
	return false
}

// HasPropertyOf 对任意值做 HasProperty 检查（数组走 Has 以正确识别洞）。
func HasPropertyOf(v Value, name string) bool {
	if a, ok := v.(*Array); ok {
		return a.Has(name)
	}
	_, ok := v.GetProperty(name)
	return ok
}

func (a *Array) SetProperty(name string, val Value) {
	if name == "length" {
		// 可以通过设置 length 截断或扩展数组
		if n, ok := val.(*Number); ok {
			newLen := int(n.Value)
			if newLen >= 0 {
				if newLen < len(a.Elements) {
					a.Elements = a.Elements[:newLen]
					// 截断同时删除越界索引上的显式描述符 (规范: 缩小
					// length 会移除该位置的自有属性)。
					a.dropDescsFrom(newLen)
				} else {
					for len(a.Elements) < newLen {
						a.Elements = append(a.Elements, UndefinedSingleton)
					}
				}
			}
		}
		return
	}

	// 显式定义过的描述符优先: 访问器调用 setter (this = 数组本身);
	// 不可写数据属性静默失败 (非严格模式), 与 *Object.SetProperty 一致。
	if d, ok := a.PropDescs[name]; ok {
		if acc, isAcc := d.Value.(*Accessor); isAcc {
			if acc.Setter != nil && IsCallable(acc.Setter) {
				CallFunction(acc.Setter, a, val)
			}
			return
		}
		if !d.Writable {
			return
		}
		d.Value = val
		a.PropDescs[name] = d
		// 索引数据属性同步 Elements, 使不经描述符通道的读保持一致。
		if idx, err := strconv.Atoi(name); err == nil && idx >= 0 && idx < len(a.Elements) {
			a.Elements[idx] = val
		}
		return
	}

	// 数字索引设置。数组的 length 是"最大索引 + 1"的自有属性: 写入
	// idx >= len(Elements) 等价于**新增**一个自有属性, 因此 [[Extensible]]
	// 为 false 时必须失败 (规范 10.4.2.1 ArraySetLength / [[DefineOwnProperty]]
	// 对越界索引的 "if the new length is > oldLen" 分支)。
	// 已存在的索引 (idx < len(Elements)) 是改值, 不受可扩展性影响。
	if idx, err := strconv.Atoi(name); err == nil {
		if idx >= 0 {
			if idx >= len(a.Elements) {
				if a.NonExtensible {
					return
				}
				for len(a.Elements) <= idx {
					a.Elements = append(a.Elements, UndefinedSingleton)
				}
			}
			a.Elements[idx] = val
		}
		return
	}

	// 额外属性 (非索引字符串键): 不存在且不可扩展时静默失败。
	if a.NonExtensible {
		if _, exists := a.ExtraProps[name]; !exists {
			return
		}
	}
	if a.ExtraProps == nil {
		a.ExtraProps = make(map[string]Value)
	}
	a.ExtraProps[name] = val
}

// SetProto 设置数组的原型
func (a *Array) SetProto(p Value) { a.proto = p }

// GetProto 返回数组的原型
func (a *Array) GetProto() Value { return a.proto }

// ===== Symbol 键自有属性 (SymbolPropertyStore) =====
//
// 语义与 *Object 的对应方法一致, 只是存储落在 Array 自己的字段上。

// GetSymbolProperty 查自身 Symbol 键属性值。
func (a *Array) GetSymbolProperty(sym *Symbol) (Value, bool) {
	if a.SymbolProperties == nil {
		return nil, false
	}
	desc, ok := a.SymbolProperties[sym.ID]
	if !ok {
		return nil, false
	}
	if desc.Value == nil {
		return UndefinedSingleton, true
	}
	return desc.Value, true
}

// GetSymbolPropertyDescriptor 返回自身 Symbol 键属性描述符。
func (a *Array) GetSymbolPropertyDescriptor(sym *Symbol) (PropertyDescriptor, bool) {
	if a.SymbolProperties == nil {
		return PropertyDescriptor{}, false
	}
	d, ok := a.SymbolProperties[sym.ID]
	return d, ok
}

// SetSymbolProperty 用户赋值路径: 新建数据属性或按既有描述符写入。
// 已存在且不可写时静默失败 (与 *Object.SetSymbolProperty 一致)。
func (a *Array) SetSymbolProperty(sym *Symbol, val Value) {
	if a.SymbolProperties == nil {
		a.SymbolProperties = make(map[uint64]PropertyDescriptor)
	}
	if d, exists := a.SymbolProperties[sym.ID]; exists {
		if acc, isAcc := d.Value.(*Accessor); isAcc {
			if acc.Setter != nil && IsCallable(acc.Setter) {
				CallFunction(acc.Setter, a, val)
			}
			return
		}
		if d.Writable {
			d.Value = val
			a.SymbolProperties[sym.ID] = d
		}
		return
	}
	a.symbolKeyOrder = append(a.symbolKeyOrder, sym)
	a.SymbolProperties[sym.ID] = DataProperty(val)
}

// DefineOwnSymbolProperty 以完整描述符定义自身 Symbol 键属性。
func (a *Array) DefineOwnSymbolProperty(sym *Symbol, desc PropertyDescriptor) {
	if a.SymbolProperties == nil {
		a.SymbolProperties = make(map[uint64]PropertyDescriptor)
	}
	if _, exists := a.SymbolProperties[sym.ID]; !exists {
		a.symbolKeyOrder = append(a.symbolKeyOrder, sym)
	}
	a.SymbolProperties[sym.ID] = desc
}

// SymbolKeys 按插入顺序返回自身 Symbol 键。
func (a *Array) SymbolKeys() []*Symbol { return a.symbolKeyOrder }

// NewArray 创建数组值的便捷函数。
// 自动设置数组原型。
func NewArray(elements []Value) *Array {
	return &Array{Elements: elements, proto: ArrayProto}
}

// IsExtensible 报告数组的 [[Extensible]] (NonExtensible 的反)。
func (a *Array) IsExtensible() bool { return !a.NonExtensible }

// PreventExtensions 把数组置为不可扩展 (Object.preventExtensions / seal /
// freeze 的落点)。与 *Object.Extensible=false 语义一致。
func (a *Array) PreventExtensions() { a.NonExtensible = true }

// freezeProperties 实现 Object.seal / freeze 对数组自有属性的处理:
//   - 显式描述符 (PropDescs, 含越界索引) 一律改为不可配置, freeze 再置不可写;
//   - 范围内的普通索引是隐式数据属性 (writable/enumerable/configurable 均 true),
//     无法在 PropDescs 缺席时"变只读", 故为它们补写显式描述符;
//   - length 由 arrayLengthDescriptor 按规范恒不可配置, 无需改写。
//
// 不补写显式描述符的话, `Object.freeze([]); arr.push(1)` 仍能写入索引 ——
// pop/shift/push/unshift 的 set-length-array-is-frozen 族正依赖冻结生效。
func (a *Array) freezeProperties(freeze bool) {
	if a.PropDescs == nil {
		a.PropDescs = make(map[string]PropertyDescriptor)
	}
	for i, v := range a.Elements {
		k := Itoa(i)
		if d, ok := a.PropDescs[k]; ok {
			d.Configurable = false
			if freeze {
				d.Writable = false
			}
			a.PropDescs[k] = d
			continue
		}
		if v == nil {
			v = UndefinedSingleton
		}
		a.PropDescs[k] = PropertyDescriptor{
			Value:        v,
			Writable:     !freeze,
			Enumerable:   true,
			Configurable: false,
		}
	}
	for k, d := range a.PropDescs {
		if _, err := Atoi(k); err == nil {
			continue // 索引已在上面处理 (含越界索引)
		}
		if k == "length" {
			continue
		}
		d.Configurable = false
		if freeze {
			d.Writable = false
		}
		a.PropDescs[k] = d
	}
}

// joinStrings 连接字符串切片 (避免引入 strings 包)
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}

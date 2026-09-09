package object

import (
	"sort"
	"strconv"
)

// PropertyDescriptor 描述对象属性的元数据。
// 对于学习项目，简化为仅存储值和可写标志。
type PropertyDescriptor struct {
	Value    Value
	Writable bool // 是否可写 (const 属性为 false)
}

// Object 表示 JavaScript 的对象类型。
// 对象是一组键值对的集合，通过 Proto 字段实现原型链。
type Object struct {
	Properties map[string]PropertyDescriptor
	// SymbolProperties 存储以 Symbol 为键的属性 (键为 Symbol 的唯一 ID)。
	SymbolProperties map[uint64]PropertyDescriptor
	Proto            Value // 原型对象 (null 表示没有原型)
	// Extensible 表示对象是否可以添加新属性
	Extensible bool
	// InsertOrder 记录字符串键的插入顺序，供 Keys() 按 ECMAScript 的
	// OrdinaryOwnPropertyKeys 顺序返回。
	// 为 nil 时 (例如直接以字面量构造的 Object) Keys() 回退到排序行为，
	// 保证与旧行为兼容。
	InsertOrder []string
	// SymbolKeyList 按插入顺序记录 Symbol 键，供 getOwnPropertySymbols 枚举。
	SymbolKeyList []*Symbol
}

func (o *Object) Type() ObjectType { return OBJECT_OBJ }

// inspectMaxDepth 是 Inspect 递归打印的最大嵌套层数。
// 即便有环检测，超深结构仍可能拖慢输出，故再加一层深度兜底。
const inspectMaxDepth = 32

// inspectGuard 在 Inspect 递归过程中记录已访问的对象与数组，用于打破循环引用。
//
// 运行时中存在大量合法的环: Object.prototype.constructor 指回 Object 构造器，
// 而构造器又持有 prototype 属性。若不打断这条环，打印环上任一个对象都会
// 无限递归直至栈溢出 (在控制台打印内建对象时极易触发)。
type inspectGuard struct {
	objs  map[*Object]bool
	arrs  map[*Array]bool
	depth int
}

func newInspectGuard() *inspectGuard {
	return &inspectGuard{objs: make(map[*Object]bool), arrs: make(map[*Array]bool)}
}

// inspectValue 打印属性值: 容器类型走带环检测的内部实现，其余走各自的 Inspect。
func inspectValue(v Value, g *inspectGuard) string {
	if v == nil {
		return "null"
	}
	switch t := v.(type) {
	case *String:
		return `"` + t.Value + `"`
	case *Object:
		return t.inspect(g)
	case *Array:
		return t.inspect(g)
	default:
		return v.Inspect()
	}
}

func (o *Object) Inspect() string { return o.inspect(newInspectGuard()) }

func (o *Object) inspect(g *inspectGuard) string {
	if o.Properties == nil || len(o.Properties) == 0 {
		return "{}"
	}
	if g.objs[o] {
		return "[Circular]"
	}
	if g.depth >= inspectMaxDepth {
		return "{ ... }"
	}
	g.objs[o] = true
	g.depth++
	defer func() {
		delete(g.objs, o)
		g.depth--
	}()

	// 按键排序以保证输出稳定
	keys := make([]string, 0, len(o.Properties))
	for k := range o.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var pairs []string
	for _, k := range keys {
		val := o.Properties[k].Value
		if val == nil {
			val = UndefinedSingleton
		}
		pairs = append(pairs, k+": "+inspectValue(val, g))
	}
	return "{ " + joinStrings(pairs, ", ") + " }"
}

func (o *Object) IsTruthy() bool { return true }

func (o *Object) GetProperty(name string) (Value, bool) {
	// 沿原型链查找时保留原始接收者作为访问器 this (receiver)。
	// 例如: 实例 c 上访问 proto 的 getter 时, this 必须是 c 而非 proto。
	return o.getProperty(name, o)
}

// getProperty 沿原型链查找属性。receiver 始终是属性访问的初始接收者,
// 保证原型链上的访问器 (getter) 的 this 绑定到 receiver 而非属性所在对象。
func (o *Object) getProperty(name string, receiver Value) (Value, bool) {
	// 先查自身属性
	if desc, ok := o.Properties[name]; ok {
		// 访问器属性: 调用 getter (this = 初始接收者)
		if acc, isAcc := desc.Value.(*Accessor); isAcc {
			if acc.Getter != nil && IsCallable(acc.Getter) {
				return CallFunction(acc.Getter, receiver), true
			}
			return UndefinedSingleton, true
		}
		if desc.Value == nil {
			return UndefinedSingleton, true
		}
		return desc.Value, true
	}

	// 沿原型链查找 (receiver 不变)
	if o.Proto != nil {
		if protoObj, ok := o.Proto.(*Object); ok {
			return protoObj.getProperty(name, receiver)
		}
		return o.Proto.GetProperty(name)
	}

	return nil, false
}

func (o *Object) SetProperty(name string, val Value) {
	if o.Properties == nil {
		o.Properties = make(map[string]PropertyDescriptor)
	}

	// 如果属性已存在，更新值 (检查 writable)
	if desc, ok := o.Properties[name]; ok {
		// 自身访问器属性: 调用 setter
		if acc, isAcc := desc.Value.(*Accessor); isAcc {
			if acc.Setter != nil && IsCallable(acc.Setter) {
				CallFunction(acc.Setter, o, val)
			}
			return
		}
		if desc.Writable {
			o.Properties[name] = PropertyDescriptor{Value: val, Writable: true}
		}
		// 不可写属性，静默失败 (非严格模式)
		return
	}

	// 新属性: 若原型链上有该属性的 setter 访问器, 调用它 (this = 当前对象)
	if o.Proto != nil {
		if acc := findAccessorInChain(o.Proto, name); acc != nil && acc.Setter != nil && IsCallable(acc.Setter) {
			CallFunction(acc.Setter, o, val)
			return
		}
	}

	// 新属性
	if o.Extensible {
		o.Properties[name] = PropertyDescriptor{Value: val, Writable: true}
		o.appendKey(name)
	}
}

// DefineOwnProperty 直接写入属性描述符并维护插入顺序。
// 与 SetProperty 的区别: 不触发 setter、不检查 Extensible/Writable，
// 供 Object.defineProperty 等需要精确控制描述符的场景使用。
func (o *Object) DefineOwnProperty(name string, desc PropertyDescriptor) {
	if o.Properties == nil {
		o.Properties = make(map[string]PropertyDescriptor)
	}
	o.Properties[name] = desc
	o.appendKey(name)
}

// appendKey 记录属性的插入顺序 (已存在则忽略)。
func (o *Object) appendKey(name string) {
	for _, k := range o.InsertOrder {
		if k == name {
			return
		}
	}
	o.InsertOrder = append(o.InsertOrder, name)
}

// removeKey 从插入顺序记录中移除键。
func (o *Object) removeKey(name string) {
	for i, k := range o.InsertOrder {
		if k == name {
			o.InsertOrder = append(o.InsertOrder[:i], o.InsertOrder[i+1:]...)
			return
		}
	}
}

// DeleteProperty 删除自有属性。返回是否删除成功 (属性存在则 true)。
func (o *Object) DeleteProperty(name string) bool {
	if _, ok := o.Properties[name]; ok {
		delete(o.Properties, name)
		o.removeKey(name)
		return true
	}
	return false
}

// DeleteSymbolProperty 删除以 Symbol 为键的自有属性。
func (o *Object) DeleteSymbolProperty(sym *Symbol) bool {
	if o.SymbolProperties == nil {
		return false
	}
	if _, ok := o.SymbolProperties[sym.ID]; ok {
		delete(o.SymbolProperties, sym.ID)
		return true
	}
	return false
}

// NewObject 创建空对象的便捷函数
func NewObject() *Object {
	return &Object{
		Properties: make(map[string]PropertyDescriptor),
		Proto:      NullSingleton,
		Extensible: true,
	}
}

// NewObjectWithProto 创建指定原型的对象
func NewObjectWithProto(proto Value) *Object {
	return &Object{
		Properties: make(map[string]PropertyDescriptor),
		Proto:      proto,
		Extensible: true,
	}
}

// HasOwnProperty 检查对象是否有自有属性 (不查原型链)
func (o *Object) HasOwnProperty(name string) bool {
	_, ok := o.Properties[name]
	return ok
}

// GetSymbolProperty 通过 Symbol 键获取属性值。
func (o *Object) GetSymbolProperty(sym *Symbol) (Value, bool) {
	if o.SymbolProperties == nil {
		return nil, false
	}
	desc, ok := o.SymbolProperties[sym.ID]
	if !ok {
		return nil, false
	}
	if desc.Value == nil {
		return UndefinedSingleton, true
	}
	return desc.Value, true
}

// SetSymbolProperty 通过 Symbol 键设置属性值。
func (o *Object) SetSymbolProperty(sym *Symbol, val Value) {
	if o.SymbolProperties == nil {
		o.SymbolProperties = make(map[uint64]PropertyDescriptor)
	}
	if o.SymbolProperties == nil {
		return
	}
	if o.Extensible {
		if _, exists := o.SymbolProperties[sym.ID]; !exists {
			o.SymbolKeyList = append(o.SymbolKeyList, sym)
		}
		o.SymbolProperties[sym.ID] = PropertyDescriptor{Value: val, Writable: true}
	}
}

// SymbolKeys 按插入顺序返回对象的 Symbol 自有键。
func (o *Object) SymbolKeys() []*Symbol {
	return o.SymbolKeyList
}

// HasOwnSymbolProperty 检查对象是否有以 Symbol 为键的自有属性。
func (o *Object) HasOwnSymbolProperty(sym *Symbol) bool {
	if o.SymbolProperties == nil {
		return false
	}
	_, ok := o.SymbolProperties[sym.ID]
	return ok
}

// Keys 返回对象自有属性名。
//
// 遵循 ECMAScript 的 OrdinaryOwnPropertyKeys 顺序:
// 1. 整数索引键 (array index) 按数值升序排列在前；
// 2. 其余字符串键按插入顺序排列。
//
// 当 InsertOrder 为 nil (对象由字面量直接构造、未经过 SetProperty) 时，
// 回退到字典序排序，保证与历史行为一致。
func (o *Object) Keys() []string {
	keys := make([]string, 0, len(o.Properties))

	if o.InsertOrder == nil {
		for k := range o.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys
	}

	var indexKeys, strKeys []string
	seen := make(map[string]bool, len(o.Properties))

	collect := func(k string) {
		if seen[k] {
			return
		}
		if _, exists := o.Properties[k]; !exists {
			return
		}
		seen[k] = true
		if IsArrayIndexKey(k) {
			indexKeys = append(indexKeys, k)
		} else {
			strKeys = append(strKeys, k)
		}
	}

	for _, k := range o.InsertOrder {
		collect(k)
	}

	// 兜底: 某些代码路径直接写 Properties map 而未维护 InsertOrder，
	// 这些键按字典序追加，避免漏掉。
	rest := make([]string, 0)
	for k := range o.Properties {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	if len(rest) > 0 {
		sort.Strings(rest)
		for _, k := range rest {
			collect(k)
		}
	}

	sort.Slice(indexKeys, func(i, j int) bool {
		ni, _ := strconv.Atoi(indexKeys[i])
		nj, _ := strconv.Atoi(indexKeys[j])
		return ni < nj
	})

	keys = append(keys, indexKeys...)
	keys = append(keys, strKeys...)
	return keys
}

// IsArrayIndexKey 判断属性名是否为 ECMAScript 的 array index:
// 即规范化的十进制无符号 32 位整数字符串 ("0" ~ "4294967294")。
// 前导零、负号、超出范围的都不算。
func IsArrayIndexKey(k string) bool {
	if k == "" {
		return false
	}
	if k == "0" {
		return true
	}
	if k[0] < '1' || k[0] > '9' {
		return false
	}
	n := uint64(0)
	for i := 0; i < len(k); i++ {
		c := k[i]
		if c < '0' || c > '9' {
			return false
		}
		n = n*10 + uint64(c-'0')
		if n > 4294967294 {
			return false
		}
	}
	return true
}

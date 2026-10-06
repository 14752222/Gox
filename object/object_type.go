package object

import (
	"sort"
	"strconv"
)

// PropertyDescriptor 描述对象属性的元数据。
// 与 ECMAScript 的属性描述符对齐 (数据属性: Value/Writable/Enumerable/
// Configurable; 访问器属性: Value 存 *Accessor)。插入顺序由 Object.InsertOrder
// 单独维护。
type PropertyDescriptor struct {
	Value        Value
	Writable     bool // 是否可写 (const 属性为 false)
	Enumerable   bool // 是否参与 for-in / Object.keys / JSON 枚举
	Configurable bool // 是否可删除 / 可改描述符
}

// DataProperty 创建一个普通的"赋值语义"数据属性描述符: 全 true。
// 这是 `o.x = v` 的默认形态。
func DataProperty(val Value) PropertyDescriptor {
	return PropertyDescriptor{Value: val, Writable: true, Enumerable: true, Configurable: true}
}

// BuiltinProperty 创建内建属性的描述符: writable:true, enumerable:false,
// configurable:true。这是规范中绝大多数内建属性 (原型方法 / 构造器静态方法 /
// 命名空间成员, 如 Math.abs、Object.prototype.toString、Array.prototype.push)
// 的形态。与 DataProperty (用户赋值语义, 全 true) 相对。
func BuiltinProperty(val Value) PropertyDescriptor {
	return PropertyDescriptor{Value: val, Writable: true, Enumerable: false, Configurable: true}
}

// BuiltinConstProperty 创建内建常量的描述符: writable:false, enumerable:false,
// configurable:false。对应 Math.PI / Number.MAX_VALUE / Symbol.iterator 这类
// 规范里"不可写不可删"的常量属性。
func BuiltinConstProperty(val Value) PropertyDescriptor {
	return PropertyDescriptor{Value: val, Writable: false, Enumerable: false, Configurable: false}
}

// BuiltinSymbolProperty 创建 Symbol 键内建属性的描述符: writable:false,
// enumerable:false, configurable:true。
//
// 这是规范中 @@toStringTag 这类 well-known symbol 属性的标准形态 —— 它既
// 不可写也不可枚举，但可配置 (因此 `delete X.prototype[Symbol.toStringTag]`
// 能成功)。用户赋值 `o[Symbol.toStringTag] = v` 仍走 DataProperty (全 true)。
func BuiltinSymbolProperty(val Value) PropertyDescriptor {
	return PropertyDescriptor{Value: val, Writable: false, Enumerable: false, Configurable: true}
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
			// 赋值只改值, 不动 enumerable/configurable (规范 Set 语义)。
			desc.Value = val
			o.Properties[name] = desc
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
		o.Properties[name] = DataProperty(val)
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

// SetBuiltinProperty 以内建属性语义 (writable:true, enumerable:false,
// configurable:true) 注册属性。
//
// 内建注册 (各 setup* / 原型方法装配 / 构造器静态成员) 必须走此通道: 若与
// 用户赋值 `o.x = v` 共用 SetProperty, 内建属性会被错误地标为可枚举 ——
// 于是 Object.keys(Math) 返回 44 个成员 (规范应为 []), JSON.stringify(Math)
// 不再返回 "{}", for-in 也会枚举出原型方法与命名空间成员。
//
// 用户赋值路径仍走 SetProperty (enumerable:true), 因此 setup 之后用户对
// Object.prototype 等对象的新增属性 (Object.prototype.foo = 1) 依旧可枚举,
// 语义正确。
func (o *Object) SetBuiltinProperty(name string, val Value) {
	o.DefineOwnProperty(name, BuiltinProperty(val))
}

// SetBuiltinConstProperty 以内建常量语义 (writable:false, enumerable:false,
// configurable:false) 注册属性, 用于 Math.PI / Number.MAX_VALUE /
// Symbol.iterator 这类规范常量。
func (o *Object) SetBuiltinConstProperty(name string, val Value) {
	o.DefineOwnProperty(name, BuiltinConstProperty(val))
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

// DeleteProperty 删除自有属性。
// 返回是否删除成功: 属性不存在或不可配置 (Configurable=false) 时返回 false。
func (o *Object) DeleteProperty(name string) bool {
	if desc, ok := o.Properties[name]; ok {
		if !desc.Configurable {
			return false
		}
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
//
// 注意 [[Prototype]] 置为 null（而非 %Object.prototype%）: 历史实现以此形态
// 服务"宿主侧纯数据对象"，普通对象字面量请改用 NewPlainObject。
func NewObject() *Object {
	return &Object{
		Properties: make(map[string]PropertyDescriptor),
		Proto:      NullSingleton,
		Extensible: true,
	}
}

// NewPlainObject 创建"普通对象"(ordinary object): 其 [[Prototype]] 指向全局
// %Object.prototype%。
//
// 这是 ECMAScript 里 ObjectLiteral / ObjectCreate(%Object.prototype%) 的默认
// 形态 —— 规范规定 `{}` 的 [[Prototype]] 就是 Object.prototype，于是
// `({}).hasOwnProperty` / `.toString` / `.isPrototypeOf` 等经隐式原型链可达
// (见 test262 harness/verifyProperty.js 对 o.hasOwnProperty 的依赖)。
//
// %Object.prototype% 尚未注册 (例如原型自身构造期) 时回落 null, 与
// Object.prototype.[[Prototype]] === null 的规范要求一致。
func NewPlainObject() *Object {
	proto := objectPrototypeRef
	if proto == nil {
		proto = NullSingleton
	}
	return &Object{
		Properties: make(map[string]PropertyDescriptor),
		Proto:      proto,
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

// SetSymbolProperty 通过 Symbol 键设置属性值 (用户赋值路径, 对应
// OrdinarySetWithOwnDescriptor 的简化形态)。
//
// 已存在的属性遵循其描述符: 不可写 (writable:false) 时赋值静默失败 ——
// 这一点是内建 @@toStringTag (writable:false) 的语义要求，否则
// `X.prototype[Symbol.toStringTag] = v` 会绕过不可写性。
func (o *Object) SetSymbolProperty(sym *Symbol, val Value) {
	if o.SymbolProperties == nil {
		o.SymbolProperties = make(map[uint64]PropertyDescriptor)
	}
	if d, exists := o.SymbolProperties[sym.ID]; exists {
		if d.Writable {
			d.Value = val
			o.SymbolProperties[sym.ID] = d
		}
		return
	}
	if !o.Extensible {
		return
	}
	o.SymbolKeyList = append(o.SymbolKeyList, sym)
	o.SymbolProperties[sym.ID] = DataProperty(val)
}

// SetBuiltinSymbolProperty 以 Symbol 键内建属性语义 (writable:false,
// enumerable:false, configurable:true) 注册属性。内建原型上的
// Symbol.toStringTag 一律经此 —— 与用户赋值 SetSymbolProperty 区分开。
func (o *Object) SetBuiltinSymbolProperty(sym *Symbol, val Value) {
	if o.SymbolProperties == nil {
		o.SymbolProperties = make(map[uint64]PropertyDescriptor)
	}
	if _, exists := o.SymbolProperties[sym.ID]; !exists {
		o.SymbolKeyList = append(o.SymbolKeyList, sym)
	}
	o.SymbolProperties[sym.ID] = BuiltinSymbolProperty(val)
}

// GetSymbolPropertyDescriptor 返回 Symbol 键自有属性的描述符 (供
// Object.getOwnPropertyDescriptor 处理 Symbol 键)。
func (o *Object) GetSymbolPropertyDescriptor(sym *Symbol) (PropertyDescriptor, bool) {
	if o.SymbolProperties == nil {
		return PropertyDescriptor{}, false
	}
	d, ok := o.SymbolProperties[sym.ID]
	return d, ok
}

// DefineOwnSymbolProperty 以完整描述符定义 Symbol 键自有属性
// (Object.defineProperty 的 Symbol 键形态)。
func (o *Object) DefineOwnSymbolProperty(sym *Symbol, desc PropertyDescriptor) {
	if o.SymbolProperties == nil {
		o.SymbolProperties = make(map[uint64]PropertyDescriptor)
	}
	if _, exists := o.SymbolProperties[sym.ID]; !exists {
		o.SymbolKeyList = append(o.SymbolKeyList, sym)
	}
	o.SymbolProperties[sym.ID] = desc
}


// SymbolKeys 按插入顺序返回对象的 Symbol 自有键。
func (o *Object) SymbolKeys() []*Symbol {
	return o.SymbolKeyList
}

// LookupSymbolProperty 沿原型链查找以 Symbol 为键的属性。
// 迭代协议 ([Symbol.iterator] 等) 的查询统一走这里 —— 用户代码的
// Symbol 键存储在 SymbolProperties (按 sym.ID), 与字符串键空间隔离。
func LookupSymbolProperty(o *Object, sym *Symbol) (Value, bool) {
	for cur := o; cur != nil; {
		if val, found := cur.GetSymbolProperty(sym); found {
			return val, true
		}
		next, ok := cur.Proto.(*Object)
		if !ok || next == nil {
			break
		}
		cur = next
	}
	return nil, false
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
// Keys 返回自有键, 过滤掉私有混编码键 (\x00 前缀) —— 枚举类 API
// (Object.keys / JSON.stringify / for-in) 统一经此, 类私有成员天然不可见。
// 内部按名读写 (GetProperty/SetProperty) 不经此, 不受影响。
func (o *Object) Keys() []string {
	keys := make([]string, 0, len(o.Properties))

	if o.InsertOrder == nil {
		for k := range o.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return visibleKeys(keys)
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
	return visibleKeys(keys)
}

// EnumerableKeys 返回自有且 Enumerable=true 的字符串键, 顺序同 Keys。
// 供 for-in / Object.keys/values/entries / JSON.stringify / Object.assign
// 等"只枚举可枚举属性"的 API 使用 (getOwnPropertyNames / Reflect.ownKeys
// 仍用 Keys 取全部自有键)。
func (o *Object) EnumerableKeys() []string {
	all := o.Keys()
	out := make([]string, 0, len(all))
	for _, k := range all {
		if desc, ok := o.Properties[k]; ok && desc.Enumerable {
			out = append(out, k)
		}
	}
	return out
}

// IsPrivateHiddenKey 判定 key 是否为类私有成员的混编码存储键
// (\x00 前缀, 见 compiler.privateKey)。这类键对 JS 层一切常规枚举
// (Object.keys/values/entries、for-in、JSON.stringify) 都不可见。
func IsPrivateHiddenKey(key string) bool {
	return len(key) > 0 && key[0] == 0
}

// visibleKeys 过滤私有混编码键 (Keys 的收尾公用)。
func visibleKeys(keys []string) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		if !IsPrivateHiddenKey(k) {
			out = append(out, k)
		}
	}
	return out
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

package object

import (
	"sort"
	"strconv"
)

// Itoa 是 strconv.Itoa 的包内简写 (自有属性接口高频拼索引键)。
func Itoa(i int) string { return strconv.Itoa(i) }

// Atoi 是 strconv.Atoi 的包内简写 (索引键判定高频)。
func Atoi(s string) (int, error) { return strconv.Atoi(s) }

// OwnPropertyStore 是"任意对象类型的自有属性查询/定义"的统一抽象。
//
// 背景: Gox 的对象模型里 *Object 只是众多对象类型之一 —— 数组 (*Array)、
// 闭包 (*Closure)、内建函数 (*BuiltinFunction/*BuiltinMethod)、全局对象
// (*GlobalObject) 都是实现了 Value 接口的独立类型，属性存储各不相同
// (Elements / Props / 结构体字段 / 全局环境绑定)。Object.defineProperty /
// getOwnPropertyDescriptor / hasOwnProperty 一类内建此前只认 *Object,
// 导致数组上定义访问器不落地、函数上查描述符抛 TypeError。
//
// 本接口把"自有属性"的读取 (OwnKeys / EnumerableOwnKeys / HasOwn /
// OwnDescriptor) 与写入 (DefineOwn) 收口到一个接口上: 各类型自行实现
// 自己的存储细节，调用方 (stdlib 内建 / VM) 只面向接口编程，不再按类型
// 穷举 switch —— 与 vm.getIndex 的 default 兜底 / RxIndexed 接口化同一
// 思路。
//
// 实现者: *Object / *GlobalObject / *Array / *Closure / *BuiltinFunction /
// *BuiltinMethod。未实现的值 (原始值/字符串/Map/Set/Error 等) 由调用方
// 决定兜底语义 (通常抛 TypeError 或返回默认值)。
type OwnPropertyStore interface {
	// OwnKeys 返回全部自有属性键 (含不可枚举)，顺序遵循
	// OrdinaryOwnPropertyKeys: 整数索引升序在前，字符串键按插入顺序。
	OwnKeys() []string
	// EnumerableOwnKeys 返回可枚举的自有属性键 (Object.keys / for-in 口径)。
	EnumerableOwnKeys() []string
	// HasOwn 检查 name 是否为自有属性 (不沿原型链)。
	HasOwn(name string) bool
	// OwnDescriptor 返回 name 的自有属性描述符。
	OwnDescriptor(name string) (PropertyDescriptor, bool)
	// DefineOwn 以完整描述符定义/覆盖自有属性 (Object.defineProperty 落点)。
	// 返回 false 表示该实现不支持 (调用方按 no-op 处理)。
	DefineOwn(name string, desc PropertyDescriptor) bool
}

// PropDeleter 是"支持 delete 自有属性"的可选接口 (OP_DELETE 的落点扩展)。
// 函数类的 length/name 等自有属性事实来源在结构体字段, delete 需要把
// "已删除"落成墓碑 (PropertyDescriptor.Deleted), 故不能走 Object.DeleteProperty。
// 返回是否删除成功 (不可配置属性返回 false; 不存在的属性按规范返回 true)。
type PropDeleter interface {
	DeleteOwn(name string) bool
}

// ===== *Object =====
// 普通对象的自有属性就是 Properties map, 直接对接接口 (行为与此前
// 内建里的 *Object 分支完全一致)。

func (o *Object) OwnKeys() []string { return o.Keys() }

// DeleteOwn 让 *Object 也满足 PropDeleter (删除可配置自有属性)。
func (o *Object) DeleteOwn(name string) bool { return o.DeleteProperty(name) }

func (o *Object) EnumerableOwnKeys() []string { return o.EnumerableKeys() }

func (o *Object) HasOwn(name string) bool { return o.HasOwnProperty(name) }

func (o *Object) OwnDescriptor(name string) (PropertyDescriptor, bool) {
	d, ok := o.Properties[name]
	return d, ok
}

func (o *Object) DefineOwn(name string, desc PropertyDescriptor) bool {
	o.DefineOwnProperty(name, desc)
	return true
}

// ===== *GlobalObject =====

// DefineOwn 在 globalThis 上定义/新建自有属性 (对接全局环境记录)。
func (g *GlobalObject) DefineOwn(name string, desc PropertyDescriptor) bool {
	return g.DefineGlobal(name, desc)
}

// ===== *Array =====

// arrayLengthDescriptor 返回 length 自有属性的描述符 (规范: 可写、
// 不可枚举、不可配置)。
func arrayLengthDescriptor(n int) PropertyDescriptor {
	return PropertyDescriptor{Value: NewInt(int64(n)), Writable: true, Enumerable: false, Configurable: false}
}

// OwnKeys 返回数组自有键: 整数索引升序 + "length" + defineProperty 定义的
// 非索引字符串键 (按定义顺序)。与普通数组的历史输出 (索引..., "length")
// 兼容。索引键只由 Elements 范围产出 (DefineOwn 定义索引时已同步扩容),
// propKeyOrder 仅补非索引字符串键, 避免同一索引出现两次。
func (a *Array) OwnKeys() []string {
	keys := make([]string, 0, len(a.Elements)+len(a.PropDescs)+1)
	for i := range a.Elements {
		keys = append(keys, Itoa(i))
	}
	keys = append(keys, "length")
	seen := make(map[string]bool, len(a.Elements)+1)
	for i := range a.Elements {
		seen[Itoa(i)] = true
	}
	seen["length"] = true
	for _, k := range a.propKeyOrder {
		if seen[k] {
			continue
		}
		seen[k] = true
		keys = append(keys, k)
	}
	return keys
}

// EnumerableOwnKeys 返回可枚举自有键: 描述符未标记 enumerable:false 的
// 索引 + 可枚举的 defineProperty 字符串键。"length" 恒不可枚举。
func (a *Array) EnumerableOwnKeys() []string {
	keys := make([]string, 0, len(a.Elements))
	for i := range a.Elements {
		ks := Itoa(i)
		if d, ok := a.PropDescs[ks]; ok && !d.Enumerable {
			continue
		}
		keys = append(keys, ks)
	}
	for _, k := range a.propKeyOrder {
		// 索引键已由 Elements 循环覆盖 (不变量: 索引描述符恒在范围内),
		// 这里只补非索引字符串键, 避免重复。
		if k == "length" || IsArrayIndexKey(k) {
			continue
		}
		if d, ok := a.PropDescs[k]; ok && d.Enumerable {
			keys = append(keys, k)
		}
	}
	return keys
}

// HasOwn 检查数组自有属性: length / 越界内索引 / defineProperty 定义的键。
// 注意 ExtraProps (arr.foo = 1 的落点) 不计入 —— 与历史行为一致
// (既有 hasOwnProperty 对数组额外属性返 false)。
func (a *Array) HasOwn(name string) bool {
	if name == "length" {
		return true
	}
	if d, ok := a.PropDescs[name]; ok {
		_ = d
		return true
	}
	if idx, err := Atoi(name); err == nil && idx >= 0 {
		return idx < len(a.Elements)
	}
	return false
}

// OwnDescriptor 返回数组自有属性描述符:
//   - defineProperty 定义过的键: 返回存储的完整描述符 (访问器属性调用
//     getter 由 GetProperty 负责);
//   - "length": {writable:true, enumerable:false, configurable:false};
//   - 范围内的整数索引: 普通数据属性 {value, writable:true,
//     enumerable:true, configurable:true} (洞位值为 undefined)。
func (a *Array) OwnDescriptor(name string) (PropertyDescriptor, bool) {
	if d, ok := a.PropDescs[name]; ok {
		return d, true
	}
	if name == "length" {
		return arrayLengthDescriptor(len(a.Elements)), true
	}
	if idx, err := Atoi(name); err == nil && idx >= 0 && idx < len(a.Elements) {
		v := a.Elements[idx]
		if v == nil {
			v = UndefinedSingleton
		}
		return DataProperty(v), true
	}
	return PropertyDescriptor{}, false
}

// DeleteOwn 删除数组的自有属性, 遵循描述符的可配置性 ([[Delete]])。
//
//   - name 有显式描述符 (Object.defineProperty 定义): 不可配置 ⇒ false;
//     可配置 ⇒ 从 PropDescs 移除 (索引还会清出 Elements 与 propKeyOrder)。
//   - "length": 规范恒不可配置 ⇒ false。
//   - 范围内的普通索引: 可配置 ⇒ 置洞 (Elements[idx] = undefined) 并 true。
//   - 范围外: false (自有属性不存在)。
func (a *Array) DeleteOwn(name string) bool {
	if d, ok := a.PropDescs[name]; ok {
		if !d.Configurable {
			return false
		}
		delete(a.PropDescs, name)
		if idx, err := Atoi(name); err == nil && idx >= 0 && idx < len(a.Elements) {
			a.Elements[idx] = UndefinedSingleton
		}
		for i, k := range a.propKeyOrder {
			if k == name {
				a.propKeyOrder = append(a.propKeyOrder[:i], a.propKeyOrder[i+1:]...)
				break
			}
		}
		return true
	}
	if name == "length" {
		return false
	}
	if idx, err := Atoi(name); err == nil && idx >= 0 && idx < len(a.Elements) {
		a.Elements[idx] = UndefinedSingleton
		return true
	}
	return false
}

// DefineOwn 以完整描述符定义数组自有属性 (Object.defineProperty 落点)。
// 索引上的数据描述符会同步写入 Elements, 使不经描述符通道的读
// (Array.prototype 方法 / JSON / inspect) 仍看到定义的值; 访问器描述符
// 仅在索引位占位 undefined, 读取一律经 PropDescs 调用 getter。
func (a *Array) DefineOwn(name string, desc PropertyDescriptor) bool {
	if a.PropDescs == nil {
		a.PropDescs = make(map[string]PropertyDescriptor)
	}
	if _, exists := a.PropDescs[name]; !exists && name != "length" {
		a.propKeyOrder = append(a.propKeyOrder, name)
	}
	a.PropDescs[name] = desc
	if idx, err := Atoi(name); err == nil && idx >= 0 {
		for len(a.Elements) <= idx {
			a.Elements = append(a.Elements, UndefinedSingleton)
		}
		if _, isAcc := desc.Value.(*Accessor); !isAcc {
			if desc.Value == nil {
				a.Elements[idx] = UndefinedSingleton
			} else {
				a.Elements[idx] = desc.Value
			}
		} else {
			// 访问器: 元素位只作占位, 真实读取走 PropDescs。
			a.Elements[idx] = UndefinedSingleton
		}
	}
	return true
}

// dropDescsFrom 删除索引 >= from 的显式描述符 (length 截断时调用),
// 保持"PropDescs 的索引键恒在 Elements 范围内"这一不变量。
func (a *Array) dropDescsFrom(from int) {
	if len(a.PropDescs) == 0 {
		return
	}
	for k := range a.PropDescs {
		if idx, err := Atoi(k); err == nil && idx >= from {
			delete(a.PropDescs, k)
		}
	}
	if len(a.propKeyOrder) > 0 {
		kept := a.propKeyOrder[:0]
		for _, k := range a.propKeyOrder {
			if idx, err := Atoi(k); err == nil && idx >= from {
				continue
			}
			kept = append(kept, k)
		}
		a.propKeyOrder = kept
	}
}

// ===== 函数类 (*Closure / *BuiltinFunction / *BuiltinMethod) =====

// closureHasPrototypeOwn 判断闭包是否拥有 "prototype" 自有属性:
//   - 箭头函数、async 函数 (非生成器、非 async generator) 没有 prototype;
//   - 方法定义 (对象字面量简洁方法/访问器/class 方法) 产出的普通函数
//     同样没有 prototype (规范 14.3.9; test262 name-prototype-prop.js);
//   - generator / async-generator 方法例外 —— 它们**有** prototype
//     (test262 generator-prototype-prop.js 转正用例)。
//
// 与 Closure.GetProperty 的判定范围一致 (GetProperty 侧对方法仍保留懒创建
// 的既有行为, 这里只收口"自有属性可见性"口径)。
func closureHasPrototypeOwn(c *Closure) bool {
	if c.IsArrow {
		return false
	}
	if c.Fn != nil {
		if c.Fn.IsAsync && !c.Fn.IsGenerator && !c.Fn.IsAsyncGenerator {
			return false
		}
		if c.Fn.IsMethod && !c.Fn.IsGenerator && !c.Fn.IsAsyncGenerator {
			return false
		}
	}
	return true
}

// closureLengthDescriptor 返回函数 length 自有属性的描述符 (规范:
// 不可写、不可枚举、可配置)。
func closureLengthDescriptor(n int) PropertyDescriptor {
	return PropertyDescriptor{Value: NewInt(int64(n)), Writable: false, Enumerable: false, Configurable: true}
}

// functionNameDescriptor 返回函数 name 自有属性的描述符 (经
// NamePropertyOf 统一读取, 含 Closure.Props 显式覆盖)。
func functionNameDescriptor(v Value) (PropertyDescriptor, bool) {
	return NamePropertyOf(v)
}

// isFunctionStructuralKey 报告键是否为函数对象的结构性自有键
// (length / name / prototype)。这三个键由 OwnKeys 以固定前缀给出, 不参与
// 用户键的插入顺序表。
func isFunctionStructuralKey(name string) bool {
	return name == "length" || name == "name" || name == "prototype"
}

// noteFuncKey 记录函数类自有字符串键的创建顺序 (已存在则保持原位)。
// 结构性键不记入 (见 isFunctionStructuralKey)。
func noteFuncKey(order *[]string, name string) {
	if isFunctionStructuralKey(name) {
		return
	}
	for _, k := range *order {
		if k == name {
			return
		}
	}
	*order = append(*order, name)
}

// dropFuncKey 从函数类自有键顺序表中移除键: delete 后再定义同一键应回到末尾。
func dropFuncKey(order *[]string, name string) {
	for i, k := range *order {
		if k == name {
			*order = append((*order)[:i], (*order)[i+1:]...)
			return
		}
	}
}

// orderFuncKeys 把函数类自有字符串键集合按规范的 OrdinaryOwnPropertyKeys 归一:
// 整数索引键升序在前, 其余字符串键按 prefer 给出的创建顺序; prefer 未覆盖的键
// 按字典序追加 (兜底: 宿主直接写 map 而漏记顺序时不至于丢键)。
// keys 为全集 (可含重复, 内部去重), prefer 为期望顺序。
func orderFuncKeys(keys []string, prefer []string) []string {
	set := make(map[string]bool, len(keys))
	for _, k := range keys {
		set[k] = true
	}
	seen := make(map[string]bool, len(keys))
	var idx, str []string
	take := func(k string) {
		if !set[k] || seen[k] {
			return
		}
		seen[k] = true
		if IsArrayIndexKey(k) {
			idx = append(idx, k)
		} else {
			str = append(str, k)
		}
	}
	for _, k := range prefer {
		take(k)
	}
	rest := make([]string, 0)
	for _, k := range keys {
		if !seen[k] {
			rest = append(rest, k)
		}
	}
	if len(rest) > 0 {
		sort.Strings(rest)
		for _, k := range rest {
			take(k)
		}
	}
	if len(idx) > 1 {
		sort.Slice(idx, func(i, j int) bool {
			ni, _ := strconv.Atoi(idx[i])
			nj, _ := strconv.Atoi(idx[j])
			return ni < nj
		})
	}
	out := make([]string, 0, len(idx)+len(str))
	out = append(out, idx...)
	out = append(out, str...)
	return out
}

// funcPreferOrder 返回函数类自有键的期望创建顺序: 结构性键
// (length/name/prototype) 恒在最前 (SetFunctionLength / SetFunctionName /
// MakeConstructor 的创建序), 之后是用户键的插入顺序。
func funcPreferOrder(userOrder []string) []string {
	out := make([]string, 0, 3+len(userOrder))
	out = append(out, "length", "name", "prototype")
	return append(out, userOrder...)
}

// sortedStringKeys 返回 map 的排序键 (函数类自有键中无插入顺序信息,
// 排序保证输出确定)。
func sortedStringKeys(m map[string]PropertyDescriptor) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// activePropDescs 返回 PropDescs 中"未被删除"的键 (跳过墓碑)。
func activePropDescs(m map[string]PropertyDescriptor) []string {
	out := make([]string, 0, len(m))
	for k, d := range m {
		if d.Deleted {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// deletedProp 报告 PropDescs[name] 是否是一枚墓碑 (该属性已被 delete)。
func deletedProp(m map[string]PropertyDescriptor, name string) bool {
	if m == nil {
		return false
	}
	d, ok := m[name]
	return ok && d.Deleted
}

// markDeleted 在 PropDescs 上打墓碑 (供函数类的 DeleteOwn 复用)。
func markDeleted(m *map[string]PropertyDescriptor, name string) {
	if *m == nil {
		*m = make(map[string]PropertyDescriptor)
	}
	(*m)[name] = PropertyDescriptor{Deleted: true}
}

// --- *Closure ---

// SetNonEnumerableProperty 把已存在的 Props 键标记为不可枚举 (class 静态
// 方法/访问器、宿主注册的内建静态成员经 SetProperty 写入后默认即落此; 该
// 方法供 VM / 宿主在需要时按上下文显式补标)。
func (c *Closure) SetNonEnumerableProperty(name string) {
	if c.NonEnumProps == nil {
		c.NonEnumProps = make(map[string]bool)
	}
	c.NonEnumProps[name] = true
}

func (c *Closure) OwnKeys() []string {
	keys := []string{}
	if !deletedProp(c.PropDescs, "length") {
		keys = append(keys, "length")
	}
	if !deletedProp(c.PropDescs, "name") {
		keys = append(keys, "name")
	}
	if closureHasPrototypeOwn(c) && !deletedProp(c.PropDescs, "prototype") {
		keys = append(keys, "prototype")
	}
	// Props 中的普通赋值/class 静态成员 (无显式描述符), 以及 PropDescs 里显式
	// 定义的键。二者都按 propKeyOrder 的插入顺序输出 (结构性键除外)。
	for k := range c.Props {
		if isFunctionStructuralKey(k) || deletedProp(c.PropDescs, k) {
			continue
		}
		keys = append(keys, k)
	}
	for _, k := range activePropDescs(c.PropDescs) {
		if isFunctionStructuralKey(k) {
			continue
		}
		if _, dup := c.Props[k]; dup {
			continue
		}
		keys = append(keys, k)
	}
	return orderFuncKeys(keys, funcPreferOrder(c.propKeyOrder))
}

func (c *Closure) EnumerableOwnKeys() []string {
	// 函数自有属性里 name/length/prototype 恒不可枚举, 其余 (普通赋值如
	// f.custom = 1、class 静态字段) 可枚举。内置静态成员与 class 静态
	// 方法/访问器由 NonEnumProps 标记排除 (见 SetBuiltinProperty)。
	var keys []string
	for k := range c.Props {
		if isFunctionStructuralKey(k) {
			continue
		}
		// 显式描述符 (defineProperty) 的可枚举性优先于 NonEnumProps。
		if d, ok := c.PropDescs[k]; ok && !d.Deleted {
			if d.Enumerable {
				keys = append(keys, k)
			}
			continue
		}
		if c.NonEnumProps[k] {
			continue
		}
		keys = append(keys, k)
	}
	// 仅经 defineProperty 定义 (未落 Props) 的可枚举键也要列出。
	for k, d := range c.PropDescs {
		if d.Deleted || !d.Enumerable || isFunctionStructuralKey(k) {
			continue
		}
		if _, dup := c.Props[k]; dup {
			continue
		}
		keys = append(keys, k)
	}
	return orderFuncKeys(keys, c.propKeyOrder)
}

func (c *Closure) HasOwn(name string) bool {
	_, ok := c.OwnDescriptor(name)
	return ok
}

func (c *Closure) OwnDescriptor(name string) (PropertyDescriptor, bool) {
	if d, ok := c.PropDescs[name]; ok {
		if d.Deleted {
			return PropertyDescriptor{}, false
		}
		return d, true
	}
	switch name {
	case "name":
		return functionNameDescriptor(c)
	case "length":
		n := 0
		if c.Fn != nil {
			n = c.Fn.Length
		}
		return closureLengthDescriptor(n), true
	case "prototype":
		if !closureHasPrototypeOwn(c) {
			return PropertyDescriptor{}, false
		}
		// 惰性创建语义与 fn.prototype 访问一致 (含 constructor 自引用)。
		v, _ := c.GetProperty("prototype")
		if v == nil {
			v = UndefinedSingleton
		}
		return PropertyDescriptor{Value: v, Writable: true, Enumerable: false, Configurable: false}, true
	}
	if c.Props != nil {
		if v, ok := c.Props[name]; ok {
			enum := !c.NonEnumProps[name]
			if acc, isAcc := v.(*Accessor); isAcc {
				return PropertyDescriptor{Value: acc, Writable: false, Enumerable: enum, Configurable: true}, true
			}
			return PropertyDescriptor{Value: v, Writable: true, Enumerable: enum, Configurable: true}, true
		}
	}
	return PropertyDescriptor{}, false
}

func (c *Closure) DefineOwn(name string, desc PropertyDescriptor) bool {
	if c.PropDescs == nil {
		c.PropDescs = make(map[string]PropertyDescriptor)
	}
	noteFuncKey(&c.propKeyOrder, name)
	c.PropDescs[name] = desc
	return true
}

// DeleteOwn 删除闭包的可配置自有属性; 结构性 name/length (不可写但可配置)
// 用墓碑占位, 使其从 OwnDescriptor / OwnKeys 视图消失。
func (c *Closure) DeleteOwn(name string) bool {
	if d, ok := c.PropDescs[name]; ok {
		if d.Deleted {
			return true
		}
		if !d.Configurable {
			return false
		}
		markDeleted(&c.PropDescs, name)
		dropFuncKey(&c.propKeyOrder, name)
		return true
	}
	switch name {
	case "name", "length":
		markDeleted(&c.PropDescs, name)
		return true
	case "prototype":
		// 闭包的 prototype 自有属性不可配置 (规范)。
		if closureHasPrototypeOwn(c) {
			return false
		}
	}
	if c.Props != nil {
		if _, ok := c.Props[name]; ok {
			delete(c.Props, name)
			dropFuncKey(&c.propKeyOrder, name)
			return true
		}
	}
	return true
}

// --- *BuiltinFunction ---

func (b *BuiltinFunction) OwnKeys() []string {
	keys := []string{}
	if !deletedProp(b.PropDescs, "length") {
		keys = append(keys, "length")
	}
	if !deletedProp(b.PropDescs, "name") {
		keys = append(keys, "name")
	}
	for k := range b.Properties {
		if isFunctionStructuralKey(k) || deletedProp(b.PropDescs, k) {
			continue
		}
		keys = append(keys, k)
	}
	for _, k := range activePropDescs(b.PropDescs) {
		if isFunctionStructuralKey(k) {
			continue
		}
		if b.Properties != nil {
			if _, dup := b.Properties[k]; dup {
				continue
			}
		}
		keys = append(keys, k)
	}
	return orderFuncKeys(keys, funcPreferOrder(b.propKeyOrder))
}

func (b *BuiltinFunction) EnumerableOwnKeys() []string {
	// 内置静态成员 (Properties 中经 SetBuiltinProperty 注册的) 不可枚举,
	// 用户赋值新增的属性 (String.qq = 5) 可枚举。
	var keys []string
	for k := range b.Properties {
		if isFunctionStructuralKey(k) {
			continue
		}
		if d, ok := b.PropDescs[k]; ok && !d.Deleted {
			if d.Enumerable {
				keys = append(keys, k)
			}
			continue
		}
		if b.NonEnumProps[k] {
			continue
		}
		keys = append(keys, k)
	}
	for k, d := range b.PropDescs {
		if d.Deleted || !d.Enumerable || isFunctionStructuralKey(k) {
			continue
		}
		if b.Properties != nil {
			if _, dup := b.Properties[k]; dup {
				continue
			}
		}
		keys = append(keys, k)
	}
	return orderFuncKeys(keys, b.propKeyOrder)
}

func (b *BuiltinFunction) HasOwn(name string) bool {
	_, ok := b.OwnDescriptor(name)
	return ok
}

func (b *BuiltinFunction) OwnDescriptor(name string) (PropertyDescriptor, bool) {
	if d, ok := b.PropDescs[name]; ok {
		if d.Deleted {
			return PropertyDescriptor{}, false
		}
		return d, true
	}
	switch name {
	case "name":
		return functionNameDescriptor(b)
	case "length":
		return closureLengthDescriptor(0), true
	}
	if b.Properties != nil {
		if v, ok := b.Properties[name]; ok {
			// "prototype" 是内建函数的实例原型: 不可配置 (规范)。
			configurable := true
			if name == "prototype" {
				configurable = false
			}
			// 内置静态成员不可枚举, 用户赋值新增的可枚举。
			return PropertyDescriptor{Value: v, Writable: true, Enumerable: !b.NonEnumProps[name], Configurable: configurable}, true
		}
	}
	return PropertyDescriptor{}, false
}

func (b *BuiltinFunction) DefineOwn(name string, desc PropertyDescriptor) bool {
	if b.PropDescs == nil {
		b.PropDescs = make(map[string]PropertyDescriptor)
	}
	noteFuncKey(&b.propKeyOrder, name)
	b.PropDescs[name] = desc
	return true
}

// DeleteOwn 删除内建函数对象的可配置自有属性 (length/name 用墓碑占位)。
func (b *BuiltinFunction) DeleteOwn(name string) bool {
	if d, ok := b.PropDescs[name]; ok {
		if d.Deleted {
			return true
		}
		if !d.Configurable {
			return false
		}
		markDeleted(&b.PropDescs, name)
		dropFuncKey(&b.propKeyOrder, name)
		return true
	}
	switch name {
	case "name", "length":
		markDeleted(&b.PropDescs, name)
		return true
	}
	if b.Properties != nil {
		if _, ok := b.Properties[name]; ok {
			delete(b.Properties, name)
			dropFuncKey(&b.propKeyOrder, name)
			return true
		}
	}
	return true
}

// --- *BuiltinMethod ---

func (b *BuiltinMethod) OwnKeys() []string {
	keys := []string{}
	if !deletedProp(b.PropDescs, "length") {
		keys = append(keys, "length")
	}
	if !deletedProp(b.PropDescs, "name") {
		keys = append(keys, "name")
	}
	for _, k := range activePropDescs(b.PropDescs) {
		if isFunctionStructuralKey(k) {
			continue
		}
		keys = append(keys, k)
	}
	return orderFuncKeys(keys, funcPreferOrder(b.propKeyOrder))
}

func (b *BuiltinMethod) EnumerableOwnKeys() []string { return nil }

func (b *BuiltinMethod) HasOwn(name string) bool {
	_, ok := b.OwnDescriptor(name)
	return ok
}

func (b *BuiltinMethod) OwnDescriptor(name string) (PropertyDescriptor, bool) {
	if d, ok := b.PropDescs[name]; ok {
		if d.Deleted {
			return PropertyDescriptor{}, false
		}
		return d, true
	}
	switch name {
	case "name":
		return functionNameDescriptor(b)
	case "length":
		return closureLengthDescriptor(0), true
	}
	return PropertyDescriptor{}, false
}

func (b *BuiltinMethod) DefineOwn(name string, desc PropertyDescriptor) bool {
	if b.PropDescs == nil {
		b.PropDescs = make(map[string]PropertyDescriptor)
	}
	noteFuncKey(&b.propKeyOrder, name)
	b.PropDescs[name] = desc
	return true
}

// DeleteOwn 删除内建方法的可配置自有属性 (length/name 用墓碑占位)。
func (b *BuiltinMethod) DeleteOwn(name string) bool {
	if d, ok := b.PropDescs[name]; ok {
		if d.Deleted {
			return true
		}
		if !d.Configurable {
			return false
		}
		markDeleted(&b.PropDescs, name)
		dropFuncKey(&b.propKeyOrder, name)
		return true
	}
	switch name {
	case "name", "length":
		markDeleted(&b.PropDescs, name)
		return true
	}
	return true
}

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

// ===== *Object =====
// 普通对象的自有属性就是 Properties map, 直接对接接口 (行为与此前
// 内建里的 *Object 分支完全一致)。

func (o *Object) OwnKeys() []string { return o.Keys() }

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

// --- *Closure ---

func (c *Closure) OwnKeys() []string {
	keys := []string{"length", "name"}
	if closureHasPrototypeOwn(c) {
		keys = append(keys, "prototype")
	}
	for _, k := range sortedStringKeys(c.PropDescs) {
		if k == "length" || k == "name" || k == "prototype" {
			continue
		}
		keys = append(keys, k)
	}
	return keys
}

func (c *Closure) EnumerableOwnKeys() []string {
	// 函数的自有属性 (name/length/prototype/静态成员) 均不可枚举。
	return nil
}

func (c *Closure) HasOwn(name string) bool {
	_, ok := c.OwnDescriptor(name)
	return ok
}

func (c *Closure) OwnDescriptor(name string) (PropertyDescriptor, bool) {
	if d, ok := c.PropDescs[name]; ok {
		return d, true
	}
	switch name {
	case "name":
		return functionNameDescriptor(c)
	case "length":
		n := 0
		if c.Fn != nil {
			n = c.Fn.NumParameters
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
			if acc, isAcc := v.(*Accessor); isAcc {
				return PropertyDescriptor{Value: acc, Writable: false, Enumerable: false, Configurable: true}, true
			}
			return PropertyDescriptor{Value: v, Writable: true, Enumerable: false, Configurable: true}, true
		}
	}
	return PropertyDescriptor{}, false
}

func (c *Closure) DefineOwn(name string, desc PropertyDescriptor) bool {
	if c.PropDescs == nil {
		c.PropDescs = make(map[string]PropertyDescriptor)
	}
	c.PropDescs[name] = desc
	return true
}

// --- *BuiltinFunction ---

func (b *BuiltinFunction) OwnKeys() []string {
	keys := []string{"length", "name"}
	rest := make([]string, 0, len(b.Properties))
	for k := range b.Properties {
		if k == "length" || k == "name" {
			continue
		}
		rest = append(rest, k)
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

func (b *BuiltinFunction) EnumerableOwnKeys() []string {
	// 内建函数的静态成员均为不可枚举 (Object.keys(String) === [])。
	return nil
}

func (b *BuiltinFunction) HasOwn(name string) bool {
	_, ok := b.OwnDescriptor(name)
	return ok
}

func (b *BuiltinFunction) OwnDescriptor(name string) (PropertyDescriptor, bool) {
	if d, ok := b.PropDescs[name]; ok {
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
			return PropertyDescriptor{Value: v, Writable: true, Enumerable: false, Configurable: configurable}, true
		}
	}
	return PropertyDescriptor{}, false
}

func (b *BuiltinFunction) DefineOwn(name string, desc PropertyDescriptor) bool {
	if b.PropDescs == nil {
		b.PropDescs = make(map[string]PropertyDescriptor)
	}
	b.PropDescs[name] = desc
	return true
}

// --- *BuiltinMethod ---

func (b *BuiltinMethod) OwnKeys() []string {
	keys := []string{"length", "name"}
	for _, k := range sortedStringKeys(b.PropDescs) {
		if k == "length" || k == "name" {
			continue
		}
		keys = append(keys, k)
	}
	return keys
}

func (b *BuiltinMethod) EnumerableOwnKeys() []string { return nil }

func (b *BuiltinMethod) HasOwn(name string) bool {
	_, ok := b.OwnDescriptor(name)
	return ok
}

func (b *BuiltinMethod) OwnDescriptor(name string) (PropertyDescriptor, bool) {
	if d, ok := b.PropDescs[name]; ok {
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
	b.PropDescs[name] = desc
	return true
}

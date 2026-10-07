package object

// SymbolPropertyStore 是"非 *Object 对象类型的 Symbol 键自有属性"统一抽象。
//
// 背景: Gox 的 Symbol 键属性此前只存在于 *Object.SymbolProperties —— 数组
// (*Array)、类型化数组 (*TypedArray) 这类独立对象类型完全没有 Symbol 键槽,
// 于是 `arr[Symbol.iterator] = fn` 被静默丢弃 (vm.setIndex 的 *Array 分支不
// 认 Symbol 键), 读取也永远 undefined。这直接破坏了数组解构 / for-of 对
// 「用户覆盖 @@iterator」的语义: resolveSymbolIterator 看不到覆盖, 数组解构
// 只好退回 Go 层索引快路径, 忽略被覆盖的迭代器。
//
// 本接口让 *Array / *TypedArray 拥有与 *Object 同构的 Symbol 键存储, VM 与
// stdlib 内建 (Object.defineProperty / getOwnPropertyDescriptor / 迭代协议解析)
// 统一面向接口编程, 不再按类型穷举。
type SymbolPropertyStore interface {
	// GetSymbolProperty 查自身 (不沿原型链) 的 Symbol 键属性值。
	GetSymbolProperty(sym *Symbol) (Value, bool)
	// GetSymbolPropertyDescriptor 返回自身 Symbol 键属性描述符 (供
	// Object.getOwnPropertyDescriptor 与迭代协议解析区分访问器)。
	GetSymbolPropertyDescriptor(sym *Symbol) (PropertyDescriptor, bool)
	// SetSymbolProperty 用户赋值路径: 新建数据属性 (全 true) 或按既有描述符
	// 写入 (不可写静默失败)。与 *Object.SetSymbolProperty 语义一致。
	SetSymbolProperty(sym *Symbol, val Value)
	// DefineOwnSymbolProperty 以完整描述符定义自身 Symbol 键属性。
	DefineOwnSymbolProperty(sym *Symbol, desc PropertyDescriptor)
	// SymbolKeys 按插入顺序返回自身 Symbol 键。
	SymbolKeys() []*Symbol
}

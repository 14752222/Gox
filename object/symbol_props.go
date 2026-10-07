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

// funcSymStore 是函数类 (*Closure / *BuiltinFunction / *BuiltinMethod) 共享的
// Symbol 键自有属性槽 —— 以嵌入方式复用于三个类型, 语义与 *Object.SymbolProperties
// / *Array.SymbolProperties 完全一致 (按 sym.ID 存描述符 + 按插入顺序记键)。
//
// 为什么需要它: 数组/类型化数组已补 Symbol 键槽 (SymbolPropertyStore), 但函数类
// 仍无 —— `f[sym] = 1` 在 vm.setIndex 的 default 分支只做鸭子类型断言, 函数未实现
// SetSymbolProperty 便被**静默丢弃** (读取恒 undefined), Object.getOwnPropertySymbols
// / Reflect.ownKeys 也无从列出。嵌入本槽 + 下面的方法即让函数类满足
// SymbolPropertyStore, 与数组/普通对象同构。
type funcSymStore struct {
	props map[uint64]PropertyDescriptor
	order []*Symbol
}

// GetSymbolProperty 查自身 Symbol 键属性值 (不沿原型链)。
func (s *funcSymStore) GetSymbolProperty(sym *Symbol) (Value, bool) {
	if s.props == nil {
		return nil, false
	}
	desc, ok := s.props[sym.ID]
	if !ok {
		return nil, false
	}
	if desc.Value == nil {
		return UndefinedSingleton, true
	}
	return desc.Value, true
}

// GetSymbolPropertyDescriptor 返回自身 Symbol 键属性描述符。
func (s *funcSymStore) GetSymbolPropertyDescriptor(sym *Symbol) (PropertyDescriptor, bool) {
	if s.props == nil {
		return PropertyDescriptor{}, false
	}
	d, ok := s.props[sym.ID]
	return d, ok
}

// SetSymbolProperty 用户赋值路径: 新建数据属性 (全 true) 或按既有描述符写入
// (不可写静默失败)。与 *Object.SetSymbolProperty 语义一致 —— 访问器不在此处
// 展开 setter (读取侧的 getter 由 getSymbolIndexedValue 展开)。
func (s *funcSymStore) SetSymbolProperty(sym *Symbol, val Value) {
	if s.props == nil {
		s.props = make(map[uint64]PropertyDescriptor)
	}
	if d, exists := s.props[sym.ID]; exists {
		if d.Writable {
			d.Value = val
			s.props[sym.ID] = d
		}
		return
	}
	s.order = append(s.order, sym)
	s.props[sym.ID] = DataProperty(val)
}

// DefineOwnSymbolProperty 以完整描述符定义自身 Symbol 键属性。
func (s *funcSymStore) DefineOwnSymbolProperty(sym *Symbol, desc PropertyDescriptor) {
	if s.props == nil {
		s.props = make(map[uint64]PropertyDescriptor)
	}
	if _, exists := s.props[sym.ID]; !exists {
		s.order = append(s.order, sym)
	}
	s.props[sym.ID] = desc
}

// SymbolKeys 按插入顺序返回自身 Symbol 键。
func (s *funcSymStore) SymbolKeys() []*Symbol { return s.order }

// LookupSymbolPropertyDescriptorChain 沿原型链查找 Symbol 键属性描述符,
// 同时支持 *Object (SymbolProperties) 与实现 SymbolPropertyStore 的非 *Object
// 类型 (数组 / 类型化数组)。与 LookupSymbolPropertyDescriptor 的差别: 后者
// 只在 *Object.Proto 为 *Object 时续走, 遇到数组实例即停。
func LookupSymbolPropertyDescriptorChain(v Value, sym *Symbol) (PropertyDescriptor, bool) {
	for cur := v; cur != nil; {
		switch o := cur.(type) {
		case *Object:
			if d, found := o.GetSymbolPropertyDescriptor(sym); found {
				return d, true
			}
		case SymbolPropertyStore:
			if d, found := o.GetSymbolPropertyDescriptor(sym); found {
				return d, true
			}
		}
		pp, ok := cur.(interface{ GetProto() Value })
		if !ok {
			break
		}
		cur = pp.GetProto()
	}
	return PropertyDescriptor{}, false
}

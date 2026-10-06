package object

// Accessor 表示对象属性的访问器 (getter/setter)。
// 作为 Object.Properties 中的属性值存储。
// 当 GetProperty/SetProperty 遇到 Accessor 时，调用对应的 getter/setter 函数。
type Accessor struct {
	Getter Value // getter 闭包 (nil = 无 getter)
	Setter Value // setter 闭包 (nil = 无 setter)
}

func (a *Accessor) Type() ObjectType                      { return ACCESSOR_OBJ }
func (a *Accessor) Inspect() string                       { return "[Accessor]" }
func (a *Accessor) IsTruthy() bool                        { return true }
func (a *Accessor) GetProperty(name string) (Value, bool) { return nil, false }
func (a *Accessor) SetProperty(name string, val Value)    {}

// NewAccessor 创建访问器。
func NewAccessor(getter, setter Value) *Accessor {
	return &Accessor{Getter: getter, Setter: setter}
}

// DefineAccessor 在对象上定义 getter/setter 属性。
// 若属性已是 Accessor，则合并 (只替换非 nil 的部分)。
func (o *Object) DefineAccessor(name string, getter, setter Value) {
	if o.Properties == nil {
		o.Properties = make(map[string]PropertyDescriptor)
	}
	if existing, ok := o.Properties[name]; ok {
		if acc, isAcc := existing.Value.(*Accessor); isAcc {
			if getter != nil {
				acc.Getter = getter
			}
			if setter != nil {
				acc.Setter = setter
			}
			return
		}
	}
	// 字面量访问器 (get x(){}) 默认 enumerable/configurable 均为 true。
	o.Properties[name] = PropertyDescriptor{
		Value:        NewAccessor(getter, setter),
		Writable:     false,
		Enumerable:   true,
		Configurable: true,
	}
}

// DefineSymbolAccessor 以 Symbol 为键在对象上定义 getter/setter 属性
// (对象字面量 `get [Symbol.x](){}` / `set [Symbol.x](v){}` 语义)。
// 与 DefineAccessor 同构, 只是键落在 SymbolProperties (按 sym.ID) 键空间。
func (o *Object) DefineSymbolAccessor(sym *Symbol, getter, setter Value) {
	if o.SymbolProperties == nil {
		o.SymbolProperties = make(map[uint64]PropertyDescriptor)
	}
	if existing, ok := o.SymbolProperties[sym.ID]; ok {
		if acc, isAcc := existing.Value.(*Accessor); isAcc {
			if getter != nil {
				acc.Getter = getter
			}
			if setter != nil {
				acc.Setter = setter
			}
			return
		}
	} else {
		o.SymbolKeyList = append(o.SymbolKeyList, sym)
	}
	o.SymbolProperties[sym.ID] = PropertyDescriptor{
		Value:        NewAccessor(getter, setter),
		Writable:     false,
		Enumerable:   true,
		Configurable: true,
	}
}

// DefineBuiltinAccessor 在内建原型/命名空间上定义 getter/setter 属性。
// 描述符为 {enumerable: false, configurable: true} (规范内建访问器形态),
// 与 DefineAccessor (对象字面量语义, enumerable: true) 相对。
// 典型用法: Temporal 原型的 year/month/... 等只读字段。
func (o *Object) DefineBuiltinAccessor(name string, getter, setter Value) {
	if o.Properties == nil {
		o.Properties = make(map[string]PropertyDescriptor)
	}
	if existing, ok := o.Properties[name]; ok {
		if acc, isAcc := existing.Value.(*Accessor); isAcc {
			if getter != nil {
				acc.Getter = getter
			}
			if setter != nil {
				acc.Setter = setter
			}
			return
		}
	}
	o.Properties[name] = PropertyDescriptor{
		Value:        NewAccessor(getter, setter),
		Writable:     false,
		Enumerable:   false,
		Configurable: true,
	}
}

// findAccessorInChain 沿原型链查找指定属性的访问器。
// 返回找到的 Accessor (nil = 原型链上无该访问器)。
func findAccessorInChain(start Value, name string) *Accessor {
	seen := 0 // 防止循环原型
	for start != nil && seen < 100 {
		if o, ok := start.(*Object); ok {
			if desc, ok := o.Properties[name]; ok {
				if acc, isAcc := desc.Value.(*Accessor); isAcc {
					return acc
				}
				return nil // 原型链上找到普通属性: 不再向上
			}
			start = o.Proto
		} else {
			return nil
		}
		seen++
	}
	return nil
}

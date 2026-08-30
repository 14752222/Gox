package object

// Accessor 表示对象属性的访问器 (getter/setter)。
// 作为 Object.Properties 中的属性值存储。
// 当 GetProperty/SetProperty 遇到 Accessor 时，调用对应的 getter/setter 函数。
type Accessor struct {
	Getter Value // getter 闭包 (nil = 无 getter)
	Setter Value // setter 闭包 (nil = 无 setter)
}

func (a *Accessor) Type() ObjectType       { return ACCESSOR_OBJ }
func (a *Accessor) Inspect() string         { return "[Accessor]" }
func (a *Accessor) IsTruthy() bool          { return true }
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
	o.Properties[name] = PropertyDescriptor{Value: NewAccessor(getter, setter), Writable: false}
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

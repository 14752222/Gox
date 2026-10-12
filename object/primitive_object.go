package object

import "strconv"

// PrimitiveObject 是 new Boolean(..) / new Number(..) / new String(..)
// 产生的原始值包装对象: 普通对象 + 内部槽 [[PrimitiveValue]]。
// String 包装还按规范暴露 length 与索引字符 (只读)。
type PrimitiveObject struct {
	Object
	Primitive Value
}

// NewPrimitiveObject 以指定原型构造包装对象。
func NewPrimitiveObject(prim Value, proto Value) *PrimitiveObject {
	o := &PrimitiveObject{Primitive: prim}
	o.Proto = proto
	o.Extensible = true
	return o
}

func (w *PrimitiveObject) GetProperty(name string) (Value, bool) {
	if s, ok := w.Primitive.(*String); ok {
		if name == "length" {
			return NewInt(int64(UTF16Len(s.Value))), true
		}
		if idx, err := strconv.Atoi(name); err == nil && idx >= 0 {
			if ch, ok := CharAtUTF16(s.Value, idx); ok {
				return NewString(ch), true
			}
		}
	}
	return w.Object.GetProperty(name)
}

// SetProperty 对 String 包装的 length/索引按规范静默拒绝 (不可写)，
// 其余属性走普通对象语义。
func (w *PrimitiveObject) SetProperty(name string, val Value) {
	if s, ok := w.Primitive.(*String); ok {
		if name == "length" {
			return
		}
		if idx, err := strconv.Atoi(name); err == nil && idx >= 0 && idx < UTF16Len(s.Value) {
			return
		}
	}
	w.Object.SetProperty(name, val)
}

// UnwrapPrimitive 拆包: 包装对象返回内部原始值，其余原样返回。
func UnwrapPrimitive(v Value) Value {
	if w, ok := v.(*PrimitiveObject); ok {
		return w.Primitive
	}
	return v
}

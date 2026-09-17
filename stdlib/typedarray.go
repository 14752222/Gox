package stdlib

import (
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// setupTypedArrays 注册 ES2015+ TypedArray 家族、ArrayBuffer 与 DataView,
// 以及 ES2026 的 Uint8Array.fromBase64 / Uint8Array.prototype.toBase64。
//
// 支持三种构造方式:
//
//	new Uint8Array(n)            — 指定元素个数
//	new Uint8Array(typedArr)     — 复制另一视图
//	new Uint8Array(buffer[, off[, len]]) — 在已有 buffer 上创建视图
//	new Uint8Array(iterable)     — 从可迭代值构造
func setupTypedArrays(env *runtime.Environment) {
	// ----- ArrayBuffer -----
	abFn := object.NewBuiltin("ArrayBuffer", func(args ...object.Value) object.Value {
		n := 0
		if len(args) > 0 {
			f := toFloat(args[0])
			if f < 0 || f != float64(int(f)) {
				return object.NewRangeError("ArrayBuffer: invalid length")
			}
			n = int(f)
		}
		return object.NewArrayBuffer(n)
	})
	env.Declare("ArrayBuffer", abFn, false)

	// ----- 十种 TypedArray 构造器 -----
	for _, kind := range object.TAKindList() {
		kind := kind
		ctor := object.NewBuiltin(kind.Name, func(args ...object.Value) object.Value {
			return constructTypedArray(kind, args)
		})
		ctor.SetProperty("BYTES_PER_ELEMENT", object.NewInt(int64(kind.ElemSize)))
		// 静态方法
		ctor.SetProperty("of", object.NewBuiltin("of", func(args ...object.Value) object.Value {
			ta := object.NewTypedArray(kind, len(args))
			for i, v := range args {
				ta.SetElement(i, v)
			}
			return ta
		}))
		ctor.SetProperty("from", object.NewBuiltin("from", func(args ...object.Value) object.Value {
			if len(args) == 0 {
				return object.NewTypeError("%s.from: source is required", kind.Name)
			}
			next, ok := object.Iterate(args[0])
			if !ok {
				return object.NewTypeError("%s.from: source is not iterable", kind.Name)
			}
			var elements []object.Value
			for {
				v, done := next()
				if done {
					break
				}
				elements = append(elements, v)
			}
			// mapFn 处理
			if len(args) > 1 && object.IsCallable(args[1]) {
				thisArg := object.Value(object.UndefinedSingleton)
				if len(args) > 2 {
					thisArg = args[2]
				}
				mapped := make([]object.Value, len(elements))
				for i, v := range elements {
					mapped[i] = object.CallFunction(args[1], thisArg, v, object.NewInt(int64(i)))
				}
				elements = mapped
			}
			ta := object.NewTypedArray(kind, len(elements))
			for i, v := range elements {
				ta.SetElement(i, v)
			}
			return ta
		}))
		// prototype 对象: 挂共享方法, 供 Uint8Array.prototype.toBase64
		// 这类反射访问, 以及实例查找回退。
		proto := object.NewObject()
		for _, m := range []string{"set", "subarray", "slice", "join",
			"indexOf", "includes", "forEach", "toBase64"} {
			mName := m
			proto.SetProperty(mName, object.NewBuiltinMethod(mName, func(this object.Value, margs ...object.Value) object.Value {
				ta, ok := this.(*object.TypedArray)
				if !ok {
					return object.NewTypeError("%s.prototype.%s: receiver must be a TypedArray", kind.Name, mName)
				}
				// 复用实例内建分派
				if v, found := object.CallTypedArrayMethod(ta, mName, margs); found {
					return v
				}
				return object.UndefinedSingleton
			}))
		}
		ctor.SetProperty("prototype", proto)
		object.SetTypedArrayProto(kind.Name, proto)
		env.Declare(kind.Name, ctor, false)
	}

	// ES2026: Uint8Array.fromBase64(str)
	if u8, ok := env.Get("Uint8Array"); ok {
		u8.SetProperty("fromBase64", object.NewBuiltin("fromBase64", func(args ...object.Value) object.Value {
			s := ""
			if len(args) > 0 {
				s = toStr(args[0])
			}
			data, ok := object.Base64Decode(s)
			if !ok {
				return object.NewSyntaxError("Uint8Array.fromBase64: invalid base64")
			}
			ta := object.NewTypedArray(object.MustLookupTAKind("Uint8Array"), len(data))
			for i, b := range data {
				ta.SetElement(i, object.NewInt(int64(b)))
			}
			return ta
		}))
	}

	// ----- DataView -----
	dvFn := object.NewBuiltin("DataView", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			if ab, ok := args[0].(*object.ArrayBuffer); ok {
				_ = ab
			}
			return object.NewTypeError("DataView: buffer is required")
		}
		ab, ok := args[0].(*object.ArrayBuffer)
		if !ok {
			return object.NewTypeError("DataView: first argument must be an ArrayBuffer")
		}
		off := 0
		if len(args) > 1 {
			off = int(toFloat(args[1]))
		}
		return object.NewDataView(ab, off)
	})
	env.Declare("DataView", dvFn, false)
}

// constructTypedArray 实现 new XxxArray(...) 的分支逻辑。
func constructTypedArray(kind object.TAKind, args []object.Value) object.Value {
	if len(args) == 0 || args[0] == object.UndefinedSingleton {
		return object.NewTypedArray(kind, 0)
	}
	switch src := args[0].(type) {
	case *object.Number:
		// new Uint8Array(n)
		n := src.Value
		if n < 0 || n != float64(int(n)) {
			return object.NewRangeError("%s: invalid length", kind.Name)
		}
		return object.NewTypedArray(kind, int(n))
	case *object.TypedArray:
		ta := object.NewTypedArray(kind, src.Length)
		for i := 0; i < src.Length; i++ {
			ta.SetElement(i, src.GetElement(i))
		}
		return ta
	case *object.ArrayBuffer:
		// new Uint8Array(buffer[, byteOffset[, length]])
		off := 0
		if len(args) > 1 {
			off = int(toFloat(args[1]))
		}
		avail := (len(src.Data) - off) / kind.ElemSize
		length := avail
		if len(args) > 2 && !isUndefinedValue(args[2]) {
			length = int(toFloat(args[2]))
		}
		if off < 0 || off > len(src.Data) || length < 0 || off+length*kind.ElemSize > len(src.Data) {
			return object.NewRangeError("%s: view extends beyond buffer bounds", kind.Name)
		}
		return object.NewTypedArrayView(kind, src, off, length)
	}
	// 可迭代: Array / Set / string 等
	next, ok := object.Iterate(args[0])
	if !ok {
		return object.NewTypeError("%s: invalid argument", kind.Name)
	}
	var elements []object.Value
	for {
		v, done := next()
		if done {
			break
		}
		elements = append(elements, v)
	}
	ta := object.NewTypedArray(kind, len(elements))
	for i, v := range elements {
		ta.SetElement(i, v)
	}
	return ta
}

package stdlib

import (
	"fmt"

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
		// ES2024: new ArrayBuffer(n, {maxByteLength}) → resizable
		max := -1
		if len(args) > 1 && !isUndefinedValue(args[1]) {
			if mv, ok := args[1].GetProperty("maxByteLength"); ok && !isUndefinedValue(mv) {
				f := toFloat(mv)
				if f < 0 || f != float64(int(f)) || int(f) < n {
					return object.NewRangeError("ArrayBuffer: invalid maxByteLength")
				}
				max = int(f)
			}
		}
		if max >= 0 {
			return object.NewResizableArrayBuffer(n, max)
		}
		return object.NewArrayBuffer(n)
	})
	// ArrayBuffer.prototype: 让 ArrayBuffer.prototype.resize / .slice 及
	// 访问器 (byteLength/resizable/maxByteLength/detached) 可被反射访问。
	abProto := object.NewObject()
	abThis := func(this object.Value) (*object.ArrayBuffer, *object.Error) {
		buf, ok := this.(*object.ArrayBuffer)
		if !ok {
			return nil, object.NewTypeError("ArrayBuffer.prototype method: receiver must be an ArrayBuffer")
		}
		return buf, nil
	}
	abProto.SetBuiltinProperty("resize", object.NewBuiltinMethod("resize", func(this object.Value, args ...object.Value) object.Value {
		buf, err := abThis(this)
		if err != nil {
			return err
		}
		return buf.Resize(argOrV(args, 0))
	}))
	abProto.SetBuiltinProperty("slice", object.NewBuiltinMethod("slice", func(this object.Value, args ...object.Value) object.Value {
		buf, err := abThis(this)
		if err != nil {
			return err
		}
		m, _ := buf.GetProperty("slice")
		return object.CallFunction(m, buf, args...)
	}))
	abProto.DefineAccessor("byteLength", object.NewBuiltinMethod("get byteLength", func(this object.Value, args ...object.Value) object.Value {
		buf, err := abThis(this)
		if err != nil {
			return err
		}
		v, _ := buf.GetProperty("byteLength")
		return v
	}), nil)
	abProto.DefineAccessor("resizable", object.NewBuiltinMethod("get resizable", func(this object.Value, args ...object.Value) object.Value {
		buf, err := abThis(this)
		if err != nil {
			return err
		}
		v, _ := buf.GetProperty("resizable")
		return v
	}), nil)
	abProto.DefineAccessor("maxByteLength", object.NewBuiltinMethod("get maxByteLength", func(this object.Value, args ...object.Value) object.Value {
		buf, err := abThis(this)
		if err != nil {
			return err
		}
		v, _ := buf.GetProperty("maxByteLength")
		return v
	}), nil)
	abProto.DefineAccessor("detached", object.NewBuiltinMethod("get detached", func(this object.Value, args ...object.Value) object.Value {
		buf, err := abThis(this)
		if err != nil {
			return err
		}
		v, _ := buf.GetProperty("detached")
		return v
	}), nil)
	abFn.SetProperty("prototype", abProto)
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
			"indexOf", "lastIndexOf", "includes", "at", "fill", "copyWithin",
			"reverse", "sort", "forEach", "map", "filter", "every", "some",
			"find", "findIndex", "findLast", "findLastIndex",
			"reduce", "reduceRight", "keys", "values", "entries",
			"toLocaleString", "toReversed", "toSorted", "with", "toBase64"} {
			mName := m
			proto.SetBuiltinProperty(mName, object.NewBuiltinMethod(mName, func(this object.Value, margs ...object.Value) object.Value {
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

	// ----- %TypedArray% 抽象构造器 -----
	// test262 大量用例经 TypedArray.prototype.xxx 反射访问共享方法,
	// 以及 Object.getOwnPropertyDescriptor(TypedArray.prototype, Symbol.toStringTag)。
	taFn := object.NewBuiltin("TypedArray", func(args ...object.Value) object.Value {
		return object.NewTypeError("TypedArray: abstract class cannot be constructed")
	})
	taProto := object.NewObject()
	taMethods := []string{"set", "subarray", "slice", "join",
		"indexOf", "lastIndexOf", "includes", "at", "fill", "copyWithin",
		"reverse", "sort", "forEach", "map", "filter", "every", "some",
		"find", "findIndex", "findLast", "findLastIndex",
		"reduce", "reduceRight", "keys", "values", "entries",
		"toLocaleString", "toReversed", "toSorted", "with"}
	// 规范定义的各方法 length 值 (verifyProperty 会核对描述符)。
	taMethodLengths := map[string]int{
		"at": 1, "copyWithin": 2, "entries": 0, "every": 1, "fill": 1,
		"filter": 1, "find": 1, "findIndex": 1, "findLast": 1, "findLastIndex": 1,
		"forEach": 1, "includes": 1, "indexOf": 1, "join": 1, "keys": 0,
		"lastIndexOf": 1, "map": 1, "reduce": 1, "reduceRight": 1, "reverse": 0,
		"set": 1, "slice": 2, "some": 1, "sort": 1, "subarray": 2,
		"toLocaleString": 0, "toReversed": 0, "toSorted": 1, "values": 0, "with": 2,
	}
	for _, m := range taMethods {
		mName := m
		bm := object.NewBuiltinMethod(mName, func(this object.Value, margs ...object.Value) object.Value {
			ta, ok := this.(*object.TypedArray)
			if !ok {
				return object.NewTypeError("TypedArray.prototype.%s: receiver must be a TypedArray", mName)
			}
			if v, found := object.CallTypedArrayMethod(ta, mName, margs); found {
				return v
			}
			return object.UndefinedSingleton
		})
		bm.SetFunctionLength(taMethodLengths[mName])
		taProto.SetBuiltinProperty(mName, bm)
	}
	// Symbol.iterator = values (规范同函数对象; 这里语义等价即可)。
	if v, ok := taProto.GetProperty("values"); ok {
		taProto.SetBuiltinSymbolProperty(object.GetGlobalSymbol("Symbol.iterator"), v)
	}
	// Symbol.toStringTag getter: TA 实例返回构造器名, 其他 receiver 返回 undefined。
	taProto.DefineSymbolAccessor(object.GetGlobalSymbol("Symbol.toStringTag"),
		object.NewBuiltinMethod("get [Symbol.toStringTag]", func(this object.Value, args ...object.Value) object.Value {
			if ta, ok := this.(*object.TypedArray); ok {
				return object.NewString(ta.Kind.Name)
			}
			return object.UndefinedSingleton
		}), nil)
	// %TypedArray%.prototype 的访问器: buffer/byteLength/byteOffset/length。
	taAcc := func(prop string) object.Value {
		return object.NewBuiltinMethod("get "+prop, func(this object.Value, args ...object.Value) object.Value {
			ta, ok := this.(*object.TypedArray)
			if !ok {
				return object.NewTypeError("TypedArray.prototype.%s: receiver must be a TypedArray", prop)
			}
			v, _ := ta.GetProperty(prop)
			return v
		})
	}
	for _, prop := range []string{"buffer", "byteLength", "byteOffset", "length"} {
		taProto.DefineAccessor(prop, taAcc(prop), nil)
	}
	taFn.SetProperty("prototype", taProto)
	env.Declare("TypedArray", taFn, false)
	object.SetTypedArrayCtorProto(taFn)

	// 各具体构造器的 prototype 链到 %TypedArray%.prototype。
	for _, kind := range object.TAKindList() {
		if ctor, ok := env.Get(kind.Name); ok {
			if pv, ok2 := ctor.GetProperty("prototype"); ok2 {
				if po, ok3 := pv.(*object.Object); ok3 {
					po.Proto = taProto
				}
			}
		}
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

// argOrV 返回 args[i] 或 undefined。
func argOrV(args []object.Value, i int) object.Value {
	if i < len(args) {
		return args[i]
	}
	return object.UndefinedSingleton
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
		ta := object.NewTypedArray(kind, src.ArrLength())
		for i := 0; i < src.ArrLength(); i++ {
			ta.SetElement(i, src.GetElement(i))
		}
		return ta
	case *object.ArrayBuffer:
		// new Uint8Array(buffer[, byteOffset[, length]])
		// length 省略时为 length-tracking 视图 (ES2024: 跟随 buffer 当前
		// 字节长, resizable buffer resize 后视图长度随之变化)。
		off := 0
		if len(args) > 1 {
			off = int(toFloat(args[1]))
		}
		if off < 0 || off > len(src.Data) || off%kind.ElemSize != 0 {
			return object.NewRangeError("%s: view extends beyond buffer bounds", kind.Name)
		}
		if len(args) > 2 && !isUndefinedValue(args[2]) {
			length := int(toFloat(args[2]))
			if length < 0 || off+length*kind.ElemSize > len(src.Data) {
				return object.NewRangeError("%s: view extends beyond buffer bounds", kind.Name)
			}
			return object.NewTypedArrayView(kind, src, off, length)
		}
		return object.NewTypedArrayView(kind, src, off, -1)
	}
	// 可迭代: Array / Set / string 等
	if next, ok := object.Iterate(args[0]); ok {
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
	// array-like: { length: n, 0: v0, ... } (无 Symbol.iterator)
	if lv, ok := args[0].GetProperty("length"); ok {
		n := int(toFloat(lv))
		if n >= 0 {
			ta := object.NewTypedArray(kind, n)
			for i := 0; i < n; i++ {
				v, _ := args[0].GetProperty(fmt.Sprintf("%d", i))
				if v == nil {
					v = object.UndefinedSingleton
				}
				ta.SetElement(i, v)
			}
			return ta
		}
	}
	return object.NewTypeError("%s: invalid argument", kind.Name)
}

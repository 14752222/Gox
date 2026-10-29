package object

// DataView 的规范语义收口 (rFXXbq built-ins 洼地批次)。
//
// 此前 DataView 只有实例魔法属性 (GetProperty 按 receiver 分发), 没有真
// prototype 对象 —— DataView.prototype === undefined, 一切反射访问
// (Object.getOwnPropertyDescriptor(DataView.prototype, ...) /
// DataView.prototype.getFloat32.call(dv, ...)) 全部失败 (T1-4 C3 簇)。
//
// 本文件提供:
//   - DataViewProto 注册表 (实例 [[Prototype]] 收敛点, 与 MapProto 同法);
//   - DataViewMethod(name) 工厂: 规范语义的 get/set 方法 (接收者内部槽
//     校验 → ToIndex → [set: ToNumber/ToBigInt] → 分离检查 → 越界
//     RangeError), 原型与实例 GetProperty 共用;
//   - Float16 (ES2026 getFloat16/setFloat16) 的纯 Go 半精度转换;
//   - length-tracking 视图 (resizable ArrayBuffer 上不带 length 构造)。
//
// 抛错顺序依据 test262 用例钉死:
//   - detached-buffer-after-toindex-byteoffset: ToIndex 先于分离检查
//     (getFloat32(-1/Infinity) 在已分离 buffer 上应 RangeError);
//   - return-abrupt-from-tonumber-value: set 的 value 转换先于分离/越界
//     检查 (Symbol → TypeError);
//   - resizable-buffer: buffer.shrink 出界后 set 抛 TypeError (out-of-
//     bounds 视图元素区不存在, 按分离同路径)。

import (
	"math"

	"encoding/binary"
)

// DataViewProto 是 DataView 实例的默认 [[Prototype]] (stdlib 装配时注册)。
var DataViewProto Value

// SetDataViewProto 注册 %DataView.prototype% (与 SetMapProto 同法)。
func SetDataViewProto(p Value) { DataViewProto = p }

// GetProto 返回实例 [[Prototype]] (ProtoOf 与 Object.getPrototypeOf 走这里)。
func (d *DataView) GetProto() Value { return DataViewProto }

// currentViewSize 返回视图当前可见字节数。Length >= 0 为定长视图;
// -1 为 length-tracking (resizable buffer 上不带 length 构造), 随
// buffer resize 动态伸缩。调用方保证 buffer 未分离。
func (d *DataView) currentViewSize(buf *ArrayBuffer) int {
	if d.Length >= 0 {
		return d.Length
	}
	n := len(buf.Data) - d.Offset
	if n < 0 {
		return 0
	}
	return n
}

// dvInternalSlotCheck 实现 RequireInternalSlot(view, [[DataView]]):
// 接收者必须是 *DataView, 否则 TypeError。
func dvInternalSlotCheck(this Value) (*DataView, *Error) {
	dv, ok := this.(*DataView)
	if !ok {
		return nil, NewTypeError("DataView method: receiver must be a DataView")
	}
	return dv, nil
}

// dvToIndex 实现 ToIndex: ToNumber → ToIntegerOrInfinity → 范围校验。
// 返回 (索引, 错误值); 错误值非 nil 时调用方必须立即返回。
func dvToIndex(v Value) (int64, Value) {
	f, errVal := toF64E(v)
	if errVal != nil {
		return 0, errVal
	}
	if math.IsNaN(f) {
		return 0, nil // ToIntegerOrInfinity(NaN) = 0
	}
	if math.IsInf(f, 1) {
		return 0, NewRangeError("Index %v is out of range", f)
	}
	i := math.Trunc(f)
	if i < 0 || i >= 1<<53 {
		return 0, NewRangeError("Index %v is out of range", f)
	}
	return int64(i), nil
}

// DataViewMethodNames 返回 DataView 全部 get/set 方法名 (stdlib 装配用)。
func DataViewMethodNames() []string { return dvMethodNames }

// ToIndexValue 是 dvToIndex 的导出版 (stdlib 构造器路径用): ToIndex 语义,
// 返回 (索引, 错误值); 错误值非 nil 时调用方必须立即返回。
func ToIndexValue(v Value) (int64, Value) { return dvToIndex(v) }

// dvMethodNames 是 DataView 全部 get/set 方法名 (含 ES2026 Float16 与
// BigInt64/BigUint64)。
var dvMethodNames = []string{
	"getFloat16", "getFloat32", "getFloat64",
	"getInt8", "getUint8", "getInt16", "getUint16", "getInt32", "getUint32",
	"getBigInt64", "getBigUint64",
	"setFloat16", "setFloat32", "setFloat64",
	"setInt8", "setUint8", "setInt16", "setUint16", "setInt32", "setUint32",
	"setBigInt64", "setBigUint64",
}

// dvTypeSize 返回类型名的元素字节宽度; ok=false 表示未知类型。
func dvTypeSize(typ string) (int, bool) {
	switch typ {
	case "Int8", "Uint8":
		return 1, true
	case "Int16", "Uint16", "Float16":
		return 2, true
	case "Int32", "Uint32", "Float32":
		return 4, true
	case "Float64", "BigInt64", "BigUint64":
		return 8, true
	}
	return 0, false
}

// DataViewMethod 返回规范语义的 DataView.prototype.<name> 方法。
// 原型注册与实例魔法分派共用同一 BuiltinMethod (接收者校验在内)。
func DataViewMethod(name string) *BuiltinMethod {
	op := name[:3] // "get" | "set"
	typ := name[3:]
	w, known := dvTypeSize(typ)
	if !known {
		return nil
	}
	isGet := op == "get"
	if !isGet && op != "set" {
		return nil
	}
	ln := 1
	if !isGet {
		ln = 2
	}
	m := NewBuiltinMethod(name, func(this Value, args ...Value) Value {
		dv, err := dvInternalSlotCheck(this)
		if err != nil {
			return err
		}
		// 1. ToIndex(requestIndex) —— 先于分离检查
		//    (detached-buffer-after-toindex-byteoffset)。
		idx, errVal := dvToIndex(argOr(args, 0))
		if errVal != nil {
			return errVal
		}
		// 2. set: value 转换先于分离/越界检查。
		var bigVal Value
		var numVal float64
		if !isGet {
			rawVal := argOr(args, 1)
			if typ == "BigInt64" || typ == "BigUint64" {
				bv, err2 := toBigIntE(rawVal)
				if err2 != nil {
					return err2
				}
				bigVal = bv
			} else {
				nv, err2 := toF64E(rawVal)
				if err2 != nil {
					return err2
				}
				numVal = nv
			}
		}
		// 3. 分离检查 (ToIndex/ToNumber 之后)。
		buf := dv.Buffer
		if buf == nil || buf.Detach {
			return NewTypeError("DataView: buffer is detached")
		}
		// 4. 视图大小 (length-tracking 随 resize) 与越界检查。
		viewSize := dv.currentViewSize(buf)
		if idx+int64(w) > int64(viewSize) {
			return NewRangeError("DataView.%s: offset %d is out of bounds", name, idx)
		}
		off := dv.Offset + int(idx)
		b := buf.Data[off : off+w]
		// get(byteOffset, littleEndian): littleEndian 是第 2 个实参;
		// set(byteOffset, value, littleEndian): 第 3 个。
		leArg := 1
		if !isGet {
			leArg = 2
		}
		le := len(args) > leArg && isTruthyValue(args[leArg])
		if isGet {
			return dvRead(b, typ, w, le)
		}
		return dvWrite(b, typ, w, numVal, bigVal, le)
	})
	m.SetFunctionLength(ln)
	return m
}

// argOr 返回 args[i] 或 undefined —— 已由 typedarray.go 提供同名助手,
// 这里不再重复声明。

// isTruthyValue 实现 ToBoolean。
func isTruthyValue(v Value) bool {
	if v == nil {
		return false
	}
	return v.IsTruthy()
}

// dvRead 按类型名读取元素 (DataView 路径: 支持自定义字节序与 Float16/BigInt)。
func dvRead(b []byte, typ string, w int, le bool) Value {
	getU16 := func() uint16 {
		if le {
			return binary.LittleEndian.Uint16(b)
		}
		return binary.BigEndian.Uint16(b)
	}
	getU32 := func() uint32 {
		if le {
			return binary.LittleEndian.Uint32(b)
		}
		return binary.BigEndian.Uint32(b)
	}
	getU64 := func() uint64 {
		if le {
			return binary.LittleEndian.Uint64(b)
		}
		return binary.BigEndian.Uint64(b)
	}
	switch typ {
	case "Float16":
		return NewNumber(float64(float16ToFloat32(getU16())))
	case "Float32":
		return NewNumber(float64(math.Float32frombits(getU32())))
	case "Float64":
		return NewNumber(math.Float64frombits(getU64()))
	case "Int8":
		return NewNumber(float64(int8(b[0])))
	case "Uint8":
		return NewNumber(float64(b[0]))
	case "Int16":
		return NewNumber(float64(int16(getU16())))
	case "Uint16":
		return NewNumber(float64(getU16()))
	case "Int32":
		return NewNumber(float64(int32(getU32())))
	case "Uint32":
		return NewNumber(float64(getU32()))
	case "BigInt64":
		return NewBigIntInt64(int64(getU64()))
	case "BigUint64":
		return NewBigIntUint64(getU64())
	}
	return UndefinedSingleton
}

// dvWrite 按类型名写入元素。
func dvWrite(b []byte, typ string, w int, numVal float64, bigVal Value, le bool) Value {
	putU16 := func(x uint16) {
		if le {
			binary.LittleEndian.PutUint16(b, x)
		} else {
			binary.BigEndian.PutUint16(b, x)
		}
	}
	putU32 := func(x uint32) {
		if le {
			binary.LittleEndian.PutUint32(b, x)
		} else {
			binary.BigEndian.PutUint32(b, x)
		}
	}
	putU64 := func(x uint64) {
		if le {
			binary.LittleEndian.PutUint64(b, x)
		} else {
			binary.BigEndian.PutUint64(b, x)
		}
	}
	switch typ {
	case "Float16":
		putU16(float32ToFloat16(float32(numVal)))
	case "Float32":
		putU32(math.Float32bits(float32(numVal)))
	case "Float64":
		putU64(math.Float64bits(numVal))
	case "Int8":
		b[0] = byte(int8(numVal))
	case "Uint8":
		b[0] = byte(numVal)
	case "Int16":
		putU16(uint16(int16(numVal)))
	case "Uint16":
		putU16(uint16(numVal))
	case "Int32":
		putU32(uint32(int32(numVal)))
	case "Uint32":
		putU32(uint32(numVal))
	case "BigInt64":
		if bv, ok := bigVal.(*BigInt); ok {
			putU64(uint64(bv.Value.Int64()))
		}
	case "BigUint64":
		if bv, ok := bigVal.(*BigInt); ok {
			putU64(bv.Value.Uint64())
		}
	}
	return UndefinedSingleton
}

// NewDataViewFull 创建 DataView 实例 (构造器路径)。length < 0 表示
// length-tracking (resizable buffer 上随 resize 伸缩; 定长 buffer 上
// 等价 buffer.byteLength - offset)。
func NewDataViewFull(buf *ArrayBuffer, offset, length int) *DataView {
	n := 0
	if buf != nil {
		n = len(buf.Data)
	}
	if offset < 0 {
		offset = 0
	}
	if offset > n {
		offset = n
	}
	if length > n-offset {
		length = n - offset
	}
	return &DataView{Buffer: buf, Offset: offset, Length: length}
}

// ===== Float16 (IEEE 754 半精度) 纯 Go 转换 =====

// float16ToFloat32 把半精度位型转为 float32 (无舍入, 精确展开)。
func float16ToFloat32(h uint16) float32 {
	sign := uint32(h>>15) & 1
	exp := uint32(h>>10) & 0x1F
	frac := uint32(h) & 0x3FF
	var f uint32
	switch {
	case exp == 0:
		if frac == 0 {
			// ±0
			f = sign << 31
		} else {
			// 次正规: 归一化到 float32
			var e uint32
			for frac&0x400 == 0 {
				frac <<= 1
				e++
			}
			frac &= 0x3FF
			f = sign<<31 | (112-e)<<23 | frac<<13
		}
	case exp == 0x1F:
		// Inf/NaN
		f = sign<<31 | 0xFF<<23 | frac<<13
	default:
		f = sign<<31 | (exp-15+127)<<23 | frac<<13
	}
	return math.Float32frombits(f)
}

// float32ToFloat16 把 float32 舍入 (round-to-nearest-even) 到半精度位型。
func float32ToFloat16(f float32) uint16 {
	bits := math.Float32bits(f)
	sign := uint16(bits>>16) & 0x8000
	exp := int32(bits>>23) & 0xFF
	frac := bits & 0x7FFFFF
	if exp == 0xFF {
		// Inf/NaN
		if frac != 0 {
			return sign | 0x7E00 | uint16(frac>>13) // NaN: 保持非零载荷
		}
		return sign | 0x7C00
	}
	// 换算到半精度指数域
	e := exp - 127 + 15
	if e >= 0x1F {
		// 溢出 → Inf
		return sign | 0x7C00
	}
	if e >= 1 {
		// 正常数: round-to-nearest-even 舍入低 13 位
		h := frac >> 13
		rest := frac & 0x1FFF
		if rest > 0x1000 || (rest == 0x1000 && h&1 == 1) {
			h++
		}
		if h == 0x400 {
			// 尾数进位 → 指数 +1
			return sign | uint16(e+1)<<10
		}
		return sign | uint16(e)<<10 | uint16(h)
	}
	if e <= -11 {
		// 舍入到 0
		return sign
	}
	// 次正规: frac | 隐含位, 右移 (13 + (1 - e)), round-to-nearest-even
	frac |= 0x800000
	shift := uint32(13 + uint32(1-e))
	h := frac >> shift
	rest := frac & ((1 << shift) - 1)
	half := uint32(1) << (shift - 1)
	if rest > half || (rest == half && h&1 == 1) {
		h++
	}
	if h == 0x400 { // 进位到最小正规数 (指数 1, 尾数 0)
		return sign | 0x0400
	}
	return sign | uint16(h)
}

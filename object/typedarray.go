package object

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"sync"
)

// 本文件实现 ES2015+ TypedArray 家族的最小完整核心:
// ArrayBuffer (字节缓冲), 十种 TypedArray 视图, DataView。
//
// 布局模型: TypedArray 持有底层 buffer (*ArrayBuffer) + byteOffset +
// 元素数 length。所有索引访问按元素宽度读写。
//
// 与 V8 的差异 (简化):
//   - 无 SharedArrayBuffer / Atomics
//   - instanceof 与原型链为简化模型 (共享一个 TAProto 概念, 按构造器名区分)

// ===== 类型元数据 =====

// TAKind 描述一种 TypedArray 的元素编码。
type TAKind struct {
	Name      string // "Uint8Array" 等
	ElemSize  int    // 字节宽度
	Float     bool   // 浮点
	BigInt    bool   // 64 位整型 (BigInt 视图)
	Signed    bool
	Clamped   bool // Uint8ClampedArray
	ByteOrder bool // 多字节类型按小端读写 (DataView 默认, TypedArray 用平台序=小端)
}

var taKinds = []TAKind{
	{"Int8Array", 1, false, false, true, false, false},
	{"Uint8Array", 1, false, false, false, false, false},
	{"Uint8ClampedArray", 1, false, false, false, true, false},
	{"Int16Array", 2, false, false, true, false, false},
	{"Uint16Array", 2, false, false, false, false, false},
	{"Int32Array", 4, false, false, true, false, false},
	{"Uint32Array", 4, false, false, false, false, false},
	{"Float32Array", 4, true, false, false, false, false},
	{"Float64Array", 8, true, false, false, false, false},
	{"BigInt64Array", 8, false, true, true, false, false},
	{"BigUint64Array", 8, false, true, false, false, false},
}

// LookupTAKind 按构造器名查找元素编码。
func LookupTAKind(name string) (TAKind, bool) {
	for _, k := range taKinds {
		if k.Name == name {
			return k, true
		}
	}
	return TAKind{}, false
}

// ===== ArrayBuffer =====

type ArrayBuffer struct {
	Data   []byte
	Detach bool
}

func (b *ArrayBuffer) Type() ObjectType { return OBJECT_OBJ }
func (b *ArrayBuffer) Inspect() string {
	return fmt.Sprintf("ArrayBuffer(%d)", len(b.Data))
}
func (b *ArrayBuffer) IsTruthy() bool            { return true }
func (b *ArrayBuffer) SetProperty(string, Value) {}

func (b *ArrayBuffer) GetProperty(name string) (Value, bool) {
	switch name {
	case "byteLength":
		if b.Detach {
			return NewInt(0), true
		}
		return NewInt(int64(len(b.Data))), true
	case "detached":
		return NewBoolean(b.Detach), true
	case "slice":
		return NewBuiltin("slice", func(args ...Value) Value {
			if b.Detach {
				return NewTypeError("ArrayBuffer is detached")
			}
			start, end := 0, len(b.Data)
			if len(args) > 0 {
				start = clampByteIndex(toF64Value(args[0]), len(b.Data))
			}
			if len(args) > 1 && !isUndef(args[1]) {
				end = clampByteIndex(toF64Value(args[1]), len(b.Data))
			}
			if end < start {
				end = start
			}
			cp := make([]byte, end-start)
			copy(cp, b.Data[start:end])
			return &ArrayBuffer{Data: cp}
		}), true
	}
	return nil, false
}

func NewArrayBuffer(n int) *ArrayBuffer { return &ArrayBuffer{Data: make([]byte, n)} }

func clampByteIndex(v float64, n int) int {
	if math.IsNaN(v) {
		return 0
	}
	i := int(v)
	if i < 0 {
		i += n
	}
	if i < 0 {
		return 0
	}
	if i > n {
		return n
	}
	return i
}

// toF64Value 宽松转数字 (ArrayBuffer/TypedArray 索引计算用)。
func toF64Value(v Value) float64 {
	switch t := v.(type) {
	case *Number:
		return t.Value
	case *Boolean:
		if t.Value {
			return 1
		}
		return 0
	case *String:
		return ParseJSNumber(t.Value)
	case *Null:
		return 0
	}
	return math.NaN()
}

// ===== TypedArray =====

type TypedArray struct {
	Kind       TAKind
	Buffer     *ArrayBuffer
	ByteOffset int
	Length     int // 元素个数
	mu         sync.Mutex
}

func (t *TypedArray) Type() ObjectType { return OBJECT_OBJ }
func (t *TypedArray) Inspect() string {
	parts := make([]string, 0, t.Length)
	for i := 0; i < t.Length && i < 8; i++ {
		parts = append(parts, ToString(t.getElement(i)))
	}
	if t.Length > 8 {
		parts = append(parts, "...")
	}
	return fmt.Sprintf("%s(%d) [%s]", t.Kind.Name, t.Length, strings.Join(parts, ", "))
}
func (t *TypedArray) IsTruthy() bool { return true }
func (t *TypedArray) SetProperty(name string, val Value) {
	// 数字索引越界写被忽略 (与规范一致); length 只读
	if n, ok := val.(*Number); ok && name == "length" {
		_ = n
	}
}

// bytes 返回本视图的字节切片。
func (t *TypedArray) bytes() []byte {
	if t.Buffer == nil || t.Buffer.Detach {
		return nil
	}
	return t.Buffer.Data[t.ByteOffset : t.ByteOffset+t.Length*t.Kind.ElemSize]
}

// getElement 读第 i 个元素 (越界返回 undefined)。
func (t *TypedArray) getElement(i int) Value {
	if i < 0 || i >= t.Length {
		return UndefinedSingleton
	}
	b := t.bytes()
	w := t.Kind.ElemSize
	off := i * w
	switch {
	case t.Kind.Float && w == 4:
		return NewNumber(float64(math.Float32frombits(binary.LittleEndian.Uint32(b[off:]))))
	case t.Kind.Float && w == 8:
		return NewNumber(math.Float64frombits(binary.LittleEndian.Uint64(b[off:])))
	case w == 1:
		if t.Kind.Signed {
			return NewInt(int64(int8(b[off])))
		}
		return NewInt(int64(b[off]))
	case w == 2:
		v := binary.LittleEndian.Uint16(b[off:])
		if t.Kind.Signed {
			return NewInt(int64(int16(v)))
		}
		return NewInt(int64(v))
	case w == 4:
		v := binary.LittleEndian.Uint32(b[off:])
		if t.Kind.Signed {
			return NewInt(int64(int32(v)))
		}
		return NewInt(int64(v))
	case w == 8:
		v := binary.LittleEndian.Uint64(b[off:])
		if t.Kind.Signed {
			// BigInt 视图: BigInt 未实现, 以 Number 近似 (超出安全整数会失真)
			return NewNumber(float64(int64(v)))
		}
		return NewNumber(float64(v))
	}
	return UndefinedSingleton
}

// setElement 写第 i 个元素, 按 ToNumber 语义转换。
func (t *TypedArray) setElement(i int, val Value) {
	if i < 0 || i >= t.Length {
		return
	}
	b := t.bytes()
	w := t.Kind.ElemSize
	off := i * w
	switch {
	case t.Kind.Float && w == 4:
		binary.LittleEndian.PutUint32(b[off:], math.Float32bits(float32(toNumberValue(val))))
	case t.Kind.Float && w == 8:
		binary.LittleEndian.PutUint64(b[off:], math.Float64bits(toNumberValue(val)))
	case w == 1:
		b[off] = clampInt8(toNumberValue(val), t.Kind.Signed, t.Kind.Clamped)
	case w == 2:
		binary.LittleEndian.PutUint16(b[off:], uint16(clampInt(toNumberValue(val), 16, t.Kind.Signed)))
	case w == 4:
		binary.LittleEndian.PutUint32(b[off:], uint32(clampInt(toNumberValue(val), 32, t.Kind.Signed)))
	case w == 8:
		binary.LittleEndian.PutUint64(b[off:], uint64(int64(toNumberValue(val))))
	}
}

func toNumberValue(v Value) float64 {
	switch t := v.(type) {
	case *Number:
		return t.Value
	case *Boolean:
		if t.Value {
			return 1
		}
		return 0
	case *String:
		var f float64
		_, err := fmt.Sscanf(strings.TrimSpace(t.Value), "%g", &f)
		if err != nil {
			return math.NaN()
		}
		return f
	case *Null:
		return 0
	}
	if v == UndefinedSingleton {
		return math.NaN()
	}
	return math.NaN()
}

// clampInt 按规范把数值转换为固定位宽整数:
// 有符号/无符号均按模 2^bits 回绕 (two's complement), 仅 NaN 归零。
// 截断式 clamp 只用于 Uint8ClampedArray (见 clampInt8)。
func clampInt(v float64, bits int, signed bool) int64 {
	if math.IsNaN(v) {
		return 0
	}
	raw := int64(v)
	mask := (int64(1) << bits) - 1
	wrapped := raw & mask
	if signed && wrapped&(int64(1)<<(bits-1)) != 0 {
		// 符号位为 1: 扩展为负数
		wrapped -= int64(1) << bits
	}
	return wrapped
}

func clampInt8(v float64, signed, clamped bool) byte {
	if clamped {
		// Uint8ClampedArray: NaN→0, 四舍五入到 [0,255]
		if math.IsNaN(v) || v <= 0 {
			return 0
		}
		if v >= 255 {
			return 255
		}
		return byte(math.Round(v))
	}
	return byte(clampInt(v, 8, signed))
}

func clampI(v, min, max int64) int64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func (t *TypedArray) GetProperty(name string) (Value, bool) {
	// 数字索引
	if n, ok := nameIndex(name); ok {
		return t.getElement(n), true
	}
	// 固定属性
	switch name {
	case "length":
		return NewInt(int64(t.Length)), true
	case "byteLength":
		return NewInt(int64(t.Length * t.Kind.ElemSize)), true
	case "byteOffset":
		return NewInt(int64(t.ByteOffset)), true
	case "buffer":
		if t.Buffer == nil {
			return UndefinedSingleton, true
		}
		return t.Buffer, true
	case "BYTES_PER_ELEMENT":
		return NewInt(int64(t.Kind.ElemSize)), true
	}
	// 方法: 先查本类型的原型对象 (Uint8Array.prototype.toBase64 等
	// 反射访问), 再走内建分派
	if proto, ok := typedArrayProto(t.Kind.Name); ok {
		if v, found := proto.GetProperty(name); found {
			return v, true
		}
	}
	return t.callMethod(name)
}

// callMethod 分派 TypedArray 的内建方法。
func (t *TypedArray) callMethod(name string) (Value, bool) {
	switch name {
	case NewSymbol("Symbol.iterator").Inspect():
		return NewBuiltin("[Symbol.iterator]", func(args ...Value) Value {
			return NewArrayIterator(t.ToArray())
		}), true
	case "set":
		return NewBuiltinMethod("set", func(this Value, sargs ...Value) Value {
			ta, ok := this.(*TypedArray)
			if !ok {
				return NewTypeError("TypedArray.prototype.set: receiver must be a TypedArray")
			}
			if len(sargs) == 0 {
				return UndefinedSingleton
			}
			offset := 0
			if len(sargs) > 1 {
				offset = int(toF64Value(sargs[1]))
			}
			src, ok := sargs[0].(*TypedArray)
			if ok {
				if offset+src.Length > ta.Length {
					return NewRangeError("TypedArray.prototype.set: source is too large")
				}
				for i := 0; i < src.Length; i++ {
					ta.setElement(offset+i, src.getElement(i))
				}
				return UndefinedSingleton
			}
			next, ok := Iterate(sargs[0])
			if !ok {
				return NewTypeError("TypedArray.prototype.set: invalid source")
			}
			i := offset
			for {
				v, done := next()
				if done {
					break
				}
				if i >= ta.Length {
					return NewRangeError("TypedArray.prototype.set: source is too large")
				}
				ta.setElement(i, v)
				i++
			}
			return UndefinedSingleton
		}), true
	case "subarray":
		return NewBuiltinMethod("subarray", func(this Value, sargs ...Value) Value {
			ta, ok := this.(*TypedArray)
			if !ok {
				return NewTypeError("TypedArray.prototype.subarray: receiver must be a TypedArray")
			}
			start, end := ta.rangeArgs(sargs)
			return &TypedArray{
				Kind:       ta.Kind,
				Buffer:     ta.Buffer,
				ByteOffset: ta.ByteOffset + start*ta.Kind.ElemSize,
				Length:     end - start,
			}
		}), true
	case "slice":
		return NewBuiltinMethod("slice", func(this Value, sargs ...Value) Value {
			ta, ok := this.(*TypedArray)
			if !ok {
				return NewTypeError("TypedArray.prototype.slice: receiver must be a TypedArray")
			}
			start, end := ta.rangeArgs(sargs)
			n := end - start
			out := &TypedArray{
				Kind:   ta.Kind,
				Buffer: NewArrayBuffer(n * ta.Kind.ElemSize),
				Length: n,
			}
			for i := 0; i < n; i++ {
				out.setElement(i, ta.getElement(start+i))
			}
			return out
		}), true
	case "join":
		return NewBuiltinMethod("join", func(this Value, sargs ...Value) Value {
			ta, ok := this.(*TypedArray)
			if !ok {
				return NewTypeError("TypedArray.prototype.join: receiver must be a TypedArray")
			}
			sep := ","
			if len(sargs) > 0 && !isUndef(sargs[0]) {
				sep = ToString(sargs[0])
			}
			parts := make([]string, 0, ta.Length)
			for i := 0; i < ta.Length; i++ {
				parts = append(parts, ToString(ta.getElement(i)))
			}
			return NewString(strings.Join(parts, sep))
		}), true
	case "indexOf":
		return NewBuiltinMethod("indexOf", func(this Value, sargs ...Value) Value {
			ta, ok := this.(*TypedArray)
			if !ok {
				return NewTypeError("TypedArray.prototype.indexOf: receiver must be a TypedArray")
			}
			want := toNumberValue(argOr(sargs, 0))
			for i := 0; i < ta.Length; i++ {
				if toNumberValue(ta.getElement(i)) == want {
					return NewInt(int64(i))
				}
			}
			return NewInt(-1)
		}), true
	case "includes":
		return NewBuiltinMethod("includes", func(this Value, sargs ...Value) Value {
			ta, ok := this.(*TypedArray)
			if !ok {
				return NewTypeError("TypedArray.prototype.includes: receiver must be a TypedArray")
			}
			want := toNumberValue(argOr(sargs, 0))
			for i := 0; i < ta.Length; i++ {
				if toNumberValue(ta.getElement(i)) == want {
					return TrueSingleton
				}
			}
			return FalseSingleton
		}), true
	case "fill":
		return NewBuiltinMethod("fill", func(this Value, sargs ...Value) Value {
			ta, ok := this.(*TypedArray)
			if !ok {
				return NewTypeError("TypedArray.prototype.fill: receiver must be a TypedArray")
			}
			var v Value = UndefinedSingleton
			if len(sargs) > 0 {
				v = sargs[0]
			}
			for i := 0; i < ta.Length; i++ {
				ta.setElement(i, v)
			}
			return ta
		}), true
	case "forEach":
		return NewBuiltinMethod("forEach", func(this Value, sargs ...Value) Value {
			ta, ok := this.(*TypedArray)
			if !ok {
				return NewTypeError("TypedArray.prototype.forEach: receiver must be a TypedArray")
			}
			if len(sargs) == 0 || !IsCallable(sargs[0]) {
				return NewTypeError("TypedArray.prototype.forEach: callback must be a function")
			}
			for i := 0; i < ta.Length; i++ {
				CallFunction(sargs[0], argOr(sargs, 1), ta.getElement(i), NewInt(int64(i)), ta)
			}
			return UndefinedSingleton
		}), true
	case "toBase64":
		// ES2026 Uint8Array.prototype.toBase64()
		return NewBuiltinMethod("toBase64", func(this Value, sargs ...Value) Value {
			ta, ok := this.(*TypedArray)
			if !ok || ta.Kind.Name != "Uint8Array" {
				return NewTypeError("toBase64: receiver must be a Uint8Array")
			}
			b := ta.bytes()
			return NewString(base64Encode(b))
		}), true
	}
	return nil, false
}

func argOr(args []Value, i int) Value {
	if i < len(args) {
		return args[i]
	}
	return UndefinedSingleton
}

// rangeArgs 解析 slice/subarray 的 (start, end)。
func (t *TypedArray) rangeArgs(args []Value) (int, int) {
	clamp := func(v float64, n int) int {
		if math.IsNaN(v) {
			return 0
		}
		i := int(v)
		if i < 0 {
			i += n
		}
		if i < 0 {
			return 0
		}
		if i > n {
			return n
		}
		return i
	}
	start, end := 0, t.Length
	if len(args) > 0 {
		start = clamp(toF64Value(args[0]), t.Length)
	}
	if len(args) > 1 && !isUndef(args[1]) {
		end = clamp(toF64Value(args[1]), t.Length)
	}
	if end < start {
		end = start
	}
	return start, end
}

// nameIndex 把属性名解析为非负整数索引。
func nameIndex(name string) (int, bool) {
	if name == "" || len(name) > 9 {
		return 0, false
	}
	n := 0
	for _, c := range name {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
		if n > 1<<28 {
			return 0, false
		}
	}
	// 前导零不是规范索引 (如 "01")
	if len(name) > 1 && name[0] == '0' {
		return 0, false
	}
	return n, true
}

// toArray 导出为普通数组 (迭代器适配用)。
func (t *TypedArray) ToArray() *Array {
	els := make([]Value, t.Length)
	for i := 0; i < t.Length; i++ {
		els[i] = t.getElement(i)
	}
	return NewArray(els)
}

// base64Encode 标准字母表编码 (ES2026 toBase64)。
func base64Encode(b []byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var sb strings.Builder
	for i := 0; i < len(b); i += 3 {
		var chunk [3]byte
		n := copy(chunk[:], b[i:])
		v := uint32(chunk[0])<<16 | uint32(chunk[1])<<8 | uint32(chunk[2])
		sb.WriteByte(alphabet[(v>>18)&0x3F])
		sb.WriteByte(alphabet[(v>>12)&0x3F])
		if n > 1 {
			sb.WriteByte(alphabet[(v>>6)&0x3F])
		} else {
			sb.WriteByte('=')
		}
		if n > 2 {
			sb.WriteByte(alphabet[v&0x3F])
		} else {
			sb.WriteByte('=')
		}
	}
	return sb.String()
}

// base64Decode 标准字母表解码 (ES2026 fromBase64), 宽松处理空白。
func base64Decode(s string) ([]byte, bool) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	var rev [256]int
	for i := range rev {
		rev[i] = -1
	}
	for i := 0; i < len(alphabet); i++ {
		rev[alphabet[i]] = i
	}
	var out []byte
	var buf, bits int
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\r' || c == '\n' || c == ' ' || c == '\t' {
			continue
		}
		if c == '=' {
			break
		}
		v := rev[c]
		if v < 0 {
			return nil, false
		}
		buf = buf<<6 | v
		bits += 6
		if bits >= 8 {
			bits -= 8
			out = append(out, byte(buf>>bits))
		}
	}
	return out, true
}

func isUndef(v Value) bool { return v == nil || v == UndefinedSingleton }

// TAKindList 返回全部 TypedArray 类型元数据 (stdlib 注册用)。
func TAKindList() []TAKind { return taKinds }

// MustLookupTAKind 按名查找类型元数据, 不存在时 panic (仅内部静态使用)。
func MustLookupTAKind(name string) TAKind {
	k, ok := LookupTAKind(name)
	if !ok {
		panic("unknown TypedArray kind: " + name)
	}
	return k
}

// NewTypedArray 创建独立的长度为 n 的 TypedArray (自带 buffer)。
func NewTypedArray(kind TAKind, n int) *TypedArray {
	return &TypedArray{
		Kind:   kind,
		Buffer: NewArrayBuffer(n * kind.ElemSize),
		Length: n,
	}
}

// NewTypedArrayView 在已有 buffer 上创建视图。
func NewTypedArrayView(kind TAKind, buf *ArrayBuffer, byteOffset, length int) *TypedArray {
	return &TypedArray{
		Kind:       kind,
		Buffer:     buf,
		ByteOffset: byteOffset,
		Length:     length,
	}
}

// GetElement / SetElement 导出的元素读写 (stdlib 构造器用)。
func (t *TypedArray) GetElement(i int) Value    { return t.getElement(i) }
func (t *TypedArray) SetElement(i int, v Value) { t.setElement(i, v) }

// Base64Decode 导出 base64 解码 (Uint8Array.fromBase64 用)。
func Base64Decode(s string) ([]byte, bool) { return base64Decode(s) }

// NewSyntaxError 创建 SyntaxError 值。
func NewSyntaxError(msg string) *Error { return &Error{Name: "SyntaxError", Message: msg} }

// ===== DataView =====

// DataView 提供对 ArrayBuffer 字节级的读写视图。
type DataView struct {
	Buffer *ArrayBuffer
	Offset int
	Length int
}

func (d *DataView) Type() ObjectType          { return OBJECT_OBJ }
func (d *DataView) Inspect() string           { return "DataView" }
func (d *DataView) IsTruthy() bool            { return true }
func (d *DataView) SetProperty(string, Value) {}

func NewDataView(buf *ArrayBuffer, offset int) *DataView {
	n := 0
	if buf != nil {
		n = len(buf.Data)
	}
	if offset < 0 || offset > n {
		offset = 0
	}
	return &DataView{Buffer: buf, Offset: offset, Length: n - offset}
}

func (d *DataView) GetProperty(name string) (Value, bool) {
	switch name {
	case "buffer":
		if d.Buffer == nil {
			return UndefinedSingleton, true
		}
		return d.Buffer, true
	case "byteLength":
		return NewInt(int64(d.Length)), true
	case "byteOffset":
		return NewInt(int64(d.Offset)), true
	}
	// getUint8/getInt16/getFloat64/setXxx
	if len(name) > 3 {
		op, typ := name[:3], name[3:]
		isGet := op == "get"
		isSet := op == "set"
		if isGet || isSet {
			kind, ok := LookupTAKind(dataviewType(typ))
			if ok {
				return NewBuiltin(name, func(args ...Value) Value {
					if d.Buffer == nil || d.Buffer.Detach {
						return NewTypeError("DataView: buffer is detached")
					}
					if len(args) == 0 {
						return NewTypeError("DataView.%s: offset is required", name)
					}
					off := d.Offset + int(toF64Value(args[0]))
					w := kind.ElemSize
					if off < 0 || off+w > d.Offset+d.Length {
						return NewRangeError("DataView.%s: offset is out of bounds", name)
					}
					b := d.Buffer.Data[off : off+w]
					truthy := func(v Value) bool {
						return v != nil && v != FalseSingleton && v != UndefinedSingleton && v != NullSingleton && v.IsTruthy()
					}
					if isGet {
						// get(key, littleEndian)
						le := len(args) > 1 && truthy(args[1])
						return NewNumber(readNum(b, kind, le))
					}
					// set(key, value, littleEndian)
					if len(args) < 2 {
						return NewTypeError("DataView.%s: value is required", name)
					}
					v := toF64Value(args[1])
					le := len(args) > 2 && truthy(args[2])
					writeNum(b, kind, v, le)
					return UndefinedSingleton
				}), true
			}
		}
	}
	return nil, false
}

// dataviewType 把 DataView 方法名后缀映射到 TAKind 名。
func dataviewType(s string) string {
	switch s {
	case "Int8":
		return "Int8Array"
	case "Uint8":
		return "Uint8Array"
	case "Int16":
		return "Int16Array"
	case "Uint16":
		return "Uint16Array"
	case "Int32":
		return "Int32Array"
	case "Uint32":
		return "Uint32Array"
	case "Float32":
		return "Float32Array"
	case "Float64":
		return "Float64Array"
	case "BigInt64":
		return "BigInt64Array"
	case "BigUint64":
		return "BigUint64Array"
	}
	return ""
}

func readNum(b []byte, kind TAKind, le bool) float64 {
	w := kind.ElemSize
	get16 := func() uint16 {
		if le {
			return binary.LittleEndian.Uint16(b)
		}
		return binary.BigEndian.Uint16(b)
	}
	get32 := func() uint32 {
		if le {
			return binary.LittleEndian.Uint32(b)
		}
		return binary.BigEndian.Uint32(b)
	}
	get64 := func() uint64 {
		if le {
			return binary.LittleEndian.Uint64(b)
		}
		return binary.BigEndian.Uint64(b)
	}
	switch {
	case kind.Float && w == 4:
		return float64(math.Float32frombits(get32()))
	case kind.Float && w == 8:
		return math.Float64frombits(get64())
	case w == 1:
		if kind.Signed {
			return float64(int8(b[0]))
		}
		return float64(b[0])
	case w == 2:
		v := get16()
		if kind.Signed {
			return float64(int16(v))
		}
		return float64(v)
	case w == 4:
		v := get32()
		if kind.Signed {
			return float64(int32(v))
		}
		return float64(v)
	case w == 8:
		v := get64()
		return float64(int64(v))
	}
	return math.NaN()
}

func writeNum(b []byte, kind TAKind, v float64, littleEndian bool) {
	w := kind.ElemSize
	// DataView 默认大端; TypedArray 内部用小端。
	// 这里实现 DataView 的写路径: littleEndian 参数决定字节序。
	be := !littleEndian
	putU16 := func(x uint16) {
		if be {
			binary.BigEndian.PutUint16(b, x)
		} else {
			binary.LittleEndian.PutUint16(b, x)
		}
	}
	putU32 := func(x uint32) {
		if be {
			binary.BigEndian.PutUint32(b, x)
		} else {
			binary.LittleEndian.PutUint32(b, x)
		}
	}
	putU64 := func(x uint64) {
		if be {
			binary.BigEndian.PutUint64(b, x)
		} else {
			binary.LittleEndian.PutUint64(b, x)
		}
	}
	switch {
	case kind.Float && w == 4:
		putU32(math.Float32bits(float32(v)))
	case kind.Float && w == 8:
		putU64(math.Float64bits(v))
	case w == 1:
		b[0] = byte(int8(v))
	case w == 2:
		putU16(uint16(int16(v)))
	case w == 4:
		putU32(uint32(int32(v)))
	case w == 8:
		putU64(uint64(int64(v)))
	}
}

// ===== TypedArray 原型对象注册表 =====
// 每种 TypedArray 的 prototype 对象由 stdlib 注册, 供实例属性查找回退,
// 使 Uint8Array.prototype.toBase64 这类反射访问成立。
var taProtoRegistry = map[string]Value{}

// SetTypedArrayProto 注册某类型的 prototype 对象。
func SetTypedArrayProto(name string, proto Value) { taProtoRegistry[name] = proto }

func typedArrayProto(name string) (Value, bool) {
	v, ok := taProtoRegistry[name]
	return v, ok
}

// CallTypedArrayMethod 按名称调用 TypedArray 的内建方法
// (stdlib 的 prototype 包装用): 构造方法并立即以 ta 为 this 调用。
func CallTypedArrayMethod(ta *TypedArray, name string, args []Value) (Value, bool) {
	m, found := ta.callMethod(name)
	if !found || !IsCallable(m) {
		return nil, false
	}
	return CallFunction(m, ta, args...), true
}

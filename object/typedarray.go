package object

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/big"
	"sort"
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
	// MaxByteLength 为 resizable ArrayBuffer (ES2024) 的容量上限;
	// -1 表示非 resizable。
	MaxByteLength int
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
	case "resizable":
		return NewBoolean(b.MaxByteLength >= 0), true
	case "maxByteLength":
		if b.Detach {
			return NewInt(0), true
		}
		if b.MaxByteLength >= 0 {
			return NewInt(int64(b.MaxByteLength)), true
		}
		return NewInt(int64(len(b.Data))), true
	case "resize":
		return NewBuiltinMethod("resize", func(this Value, args ...Value) Value {
			buf, ok := this.(*ArrayBuffer)
			if !ok {
				buf = b
			}
			return buf.Resize(argOr(args, 0))
		}), true
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
			return &ArrayBuffer{Data: cp, MaxByteLength: -1}
		}), true
	}
	return nil, false
}

func NewArrayBuffer(n int) *ArrayBuffer {
	return &ArrayBuffer{Data: make([]byte, n), MaxByteLength: -1}
}

// NewResizableArrayBuffer 创建 resizable ArrayBuffer (初始 n 字节, 上限 max 字节)。
func NewResizableArrayBuffer(n, max int) *ArrayBuffer {
	return &ArrayBuffer{Data: make([]byte, n), MaxByteLength: max}
}

// Resize 实现 ArrayBuffer.prototype.resize (ES2024):
// 新长度不得超过 maxByteLength, 扩容部分零填充, 缩容直接截断。
func (b *ArrayBuffer) Resize(lenArg Value) Value {
	if b.Detach {
		return NewTypeError("ArrayBuffer.prototype.resize: buffer is detached")
	}
	if b.MaxByteLength < 0 {
		return NewTypeError("ArrayBuffer.prototype.resize: buffer is not resizable")
	}
	f := toF64Value(lenArg)
	if math.IsNaN(f) || f < 0 || f > float64(b.MaxByteLength) || f != float64(int64(f)) {
		return NewRangeError("ArrayBuffer.prototype.resize: invalid new length")
	}
	n := int(f)
	if n <= len(b.Data) {
		b.Data = b.Data[:n]
	} else {
		nd := make([]byte, n)
		copy(nd, b.Data)
		b.Data = nd
	}
	return UndefinedSingleton
}

// DetachNow 分离缓冲区 (test262 $262.detachArrayBuffer 的宿主实现)。
// 分离后 Data 置空、Detach 置真; 任何已存在的 TypedArray/DataView 视图
// 再访问即抛 TypeError (由各自的边界检查负责)。
func (b *ArrayBuffer) DetachNow() {
	b.Detach = true
	b.Data = nil
	b.MaxByteLength = -1
}

// elementEquals 判定 searchElement 与视图元素 e 是否 SameValueZero。
//
// 与数值比较的关键差异 (BigInt 视图专属): 元素是 BigInt 时, 只有当
// searchElement 也是 BigInt 且值相等才返回 true —— 不能把两者都转 float
// 比较 (会丢失 >2^53 的精度, 且 1n 与 1 不该相等)。
func (t *TypedArray) elementEquals(search Value, e Value) bool {
	if t.Kind.BigInt {
		sb, ok := search.(*BigInt)
		if !ok {
			return false
		}
		eb, ok := e.(*BigInt)
		if !ok {
			return false
		}
		return sb.Value.Cmp(eb.Value) == 0
	}
	a := toNumberValue(search)
	b := toNumberValue(e)
	return a == b || (math.IsNaN(a) && math.IsNaN(b))
}

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

// toF64E 是带 ToNumber 语义的版本: 对象先调 valueOf 再调 toString,
// 用户回调抛出异常时透传 —— test262 的 return-abrupt 系列依赖该可观测行为。
// Symbol 按规范抛 TypeError。
//
// 异常判定必须走回调桥 (TakeCallbackErrorValue), 而非"返回值是否为 *Error":
// CallFunction 遇到 JS throw 时返回 undefined (错误经 callbackError 传递),
// 早期只认 *Error 返回值的写法漏判了所有用户回调抛错。
// err 非 nil 时调用方必须立即返回该错误值。
func toF64E(v Value) (float64, Value) {
	switch v.(type) {
	case *Number, *Boolean, *String, *Null:
		return toF64Value(v), nil
	}
	if v == nil || v == UndefinedSingleton {
		return math.NaN(), nil
	}
	if _, ok := v.(*Symbol); ok {
		return 0, NewTypeError("Cannot convert a Symbol value to a number")
	}
	if vo, ok := v.GetProperty("valueOf"); ok && IsCallable(vo) {
		r := CallFunction(vo, v)
		if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
			return 0, ev
		}
		if err, isErr := r.(*Error); isErr {
			return 0, err
		}
		switch r.(type) {
		case *Number, *Boolean, *String, *Null:
			return toF64Value(r), nil
		}
	}
	if ts, ok := v.GetProperty("toString"); ok && IsCallable(ts) {
		r := CallFunction(ts, v)
		if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
			return 0, ev
		}
		if err, isErr := r.(*Error); isErr {
			return 0, err
		}
		if s, isStr := r.(*String); isStr {
			return ParseJSNumber(s.Value), nil
		}
		switch r.(type) {
		case *Number, *Boolean, *Null:
			return toF64Value(r), nil
		}
	}
	return math.NaN(), nil
}

// ===== TypedArray =====

type TypedArray struct {
	Kind       TAKind
	Buffer     *ArrayBuffer
	ByteOffset int
	Length     int // 元素个数; -1 = length-tracking (跟随 buffer 当前字节长)
	// SymbolProperties 存储以 Symbol 为键的自有属性 (键为 Symbol 唯一 ID)。
	// 与 *Array 同构: 让 `ta[Symbol.iterator] = fn` 这类覆盖能落地,
	// 迭代协议解析 (resolveSymbolIterator) 才看得到。
	SymbolProperties map[uint64]PropertyDescriptor
	symbolKeyOrder   []*Symbol
	// Props 存普通字符串键自有属性 (如用户赋值的 constructor),
	// 支撑 Symbol.species 派生构造语义 (sample.constructor = {...})。
	Props map[string]Value
	// PropDescs 存 Object.defineProperty 定义的字符串键描述符 (TypedArray
	// 本身不是 OwnPropertyStore 的旧实现, defineProperty 落到这里之前一律
	// 静默 no-op)。故该字段同时承担符号以外的定义属性落地。
	PropDescs map[string]PropertyDescriptor
	propOrder []string
	// ProtoOverride 是派生类实例 (class MyU8 extends Uint8Array) 的
	// [[Prototype]] 覆盖: super() 返回的 exotic 实例本该挂派生类原型
	// (MyU8.prototype), 而非同类型注册表里的 Uint8Array.prototype。
	// nil 表示走注册表按类型名解析的默认原型。
	ProtoOverride Value
	mu            sync.Mutex
}

func (t *TypedArray) Type() ObjectType { return OBJECT_OBJ }

// arrLength 返回当前生效的元素个数:
//   - length-tracking 视图 (Length == -1) 跟随 buffer 当前字节长;
//   - 定长视图若 buffer 缩到视图范围之外, 整体视为越界, 长度按 0 计
//     (ES2024 resizable ArrayBuffer 语义)。
func (t *TypedArray) arrLength() int {
	if t.Buffer == nil || t.Buffer.Detach {
		return 0
	}
	avail := len(t.Buffer.Data) - t.ByteOffset
	if avail <= 0 {
		return 0
	}
	if t.Length < 0 {
		return avail / t.Kind.ElemSize
	}
	if t.Length*t.Kind.ElemSize > avail {
		return 0
	}
	return t.Length
}

// ArrLength 导出 arrLength (stdlib 用)。
func (t *TypedArray) ArrLength() int { return t.arrLength() }

// OutOfBounds 报告视图当前是否越界 (ES2024 ValidateTypedArray 的
// [[IsOutOfBounds]] 语义): buffer 分离, 或定长视图的字节范围超出
// buffer 当前字节长 (resizable buffer 缩容导致)。越界视图上调用
// 原型方法必须抛 TypeError。
// TypedArrayOutOfBounds 构造越界视图上 "必须抛 TypeError" 的错误值。
// 供无法直接返回 error 的接口 (如 HasOwn 返回 bool) 经 panic 抛出,
// VM 的 panic 恢复路径会把它转成 JS 异常。
func TypedArrayOutOfBounds(method string) *Error {
	return NewTypeError("TypedArray.prototype.%s: typed array is out of bounds", method)
}

func (t *TypedArray) OutOfBounds() bool {
	if t.Buffer == nil || t.Buffer.Detach {
		return true
	}
	avail := len(t.Buffer.Data) - t.ByteOffset
	if t.Length < 0 {
		// length-tracking 视图: byteOffset 超出当前字节长即越界。
		return avail < 0
	}
	return avail < 0 || t.Length*t.Kind.ElemSize > avail
}

func (t *TypedArray) Inspect() string {
	n := t.arrLength()
	parts := make([]string, 0, n)
	for i := 0; i < n && i < 8; i++ {
		parts = append(parts, ToString(t.getElement(i)))
	}
	if n > 8 {
		parts = append(parts, "...")
	}
	return fmt.Sprintf("%s(%d) [%s]", t.Kind.Name, n, strings.Join(parts, ", "))
}
func (t *TypedArray) IsTruthy() bool { return true }
func (t *TypedArray) SetProperty(name string, val Value) {
	// 数字索引写经 vm 索引路径; length 等固定属性只读。
	switch name {
	case "length", "byteLength", "byteOffset", "buffer", "BYTES_PER_ELEMENT":
		return
	}
	if _, ok := nameIndex(name); ok {
		if n, ok2 := nameIndex(name); ok2 {
			t.setElement(n, val)
		}
		return
	}
	if t.Props == nil {
		t.Props = map[string]Value{}
	}
	t.Props[name] = val
}

// bytes 返回本视图当前生效的字节切片。
func (t *TypedArray) bytes() []byte {
	if t.Buffer == nil || t.Buffer.Detach {
		return nil
	}
	n := t.arrLength() * t.Kind.ElemSize
	return t.Buffer.Data[t.ByteOffset : t.ByteOffset+n]
}

// getElement 读第 i 个元素 (越界返回 undefined)。
func (t *TypedArray) getElement(i int) Value {
	if i < 0 || i >= t.arrLength() {
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
		if t.Kind.BigInt {
			// BigInt 视图: 按元素的符号性回绕读出 BigInt64/BigUint64。
			if t.Kind.Signed {
				return NewBigIntInt64(int64(v))
			}
			return NewBigIntUint64(v)
		}
		if t.Kind.Signed {
			return NewNumber(float64(int64(v)))
		}
		return NewNumber(float64(v))
	}
	return UndefinedSingleton
}

// setElement 写第 i 个元素, 按 ToNumber 语义转换。
//
// BigInt 视图的转换可能抛错 (TypeError/RangeError); 此处以 panic(*Error)
// 抛出, 由 setElementE 捕获后转为返回值。绝大多数调用点不关心抛错
// (写非 BigInt 视图时永不抛), 故保留无返回值的便捷签名。
func (t *TypedArray) setElement(i int, val Value) {
	_ = t.setElementE(i, val)
}

// setElementE 是 setElement 的显式错误版本。写入 BigInt 视图且值无法转为
// BigInt 时返回该 *Error (调用方须立即透传), 成功返回 nil。
func (t *TypedArray) setElementE(i int, val Value) (err Value) {
	if i < 0 || i >= t.arrLength() {
		return nil
	}
	b := t.bytes()
	w := t.Kind.ElemSize
	off := i * w
	// BigInt 视图: 转换可能抛错, 单独走一层 recover。
	if t.Kind.BigInt && w == 8 {
		defer func() {
			if r := recover(); r != nil {
				if e, ok := r.(*Error); ok {
					err = e
					return
				}
				panic(r)
			}
		}()
		binary.LittleEndian.PutUint64(b[off:], bigIntElementBits(val, t.Kind.Signed))
		return nil
	}
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
	return nil
}

// bigIntElementBits 把一个值按 BigInt 视图的元素语义编码为 64 位:
//   - BigInt 原值直接取模 2^64 (无符号) 或按补码回绕 (有符号);
//   - Number 按 ToBigInt (须为整数值, 否则抛 RangeError);
//   - 其余类型 (String/Boolean/null/undefined/object) 抛 TypeError。
//
// 出错时调用方需感知 (set 系列要能中止并抛错), 故这里 panic 一个
// *Error 由 setElementE 捕获; 无错路径零开销。
func bigIntElementBits(v Value, signed bool) uint64 {
	switch t := v.(type) {
	case *BigInt:
		return bigIntToBits(t, signed)
	case *Number:
		if math.IsNaN(t.Value) || math.IsInf(t.Value, 0) || t.Value != math.Trunc(t.Value) {
			panic(NewRangeError("Cannot convert %v to a BigInt", t.Value))
		}
		return bigIntToBits(bigIntFromFloat(t.Value), signed)
	default:
		panic(NewTypeError("Cannot convert %s to a BigInt", v.Inspect()))
	}
}

// bigIntToBits 把 BigInt 回绕到 64 位。
func bigIntToBits(b *BigInt, signed bool) uint64 {
	m := new(big.Int).Mod(b.Value, big64Modulus)
	if m.Sign() < 0 {
		m.Add(m, big64Modulus)
	}
	u := m.Uint64()
	if !signed {
		return u
	}
	// 有符号: 读回时按 int64 解释 (补码)
	return u
}

var big64Modulus = new(big.Int).Lsh(big.NewInt(1), 64)

// bigIntFromFloat 把整数值 float64 转 BigInt。float64 超过 2^53 时已丢精度,
// 但规范 ToBigInt(Number) 也按 Number 的值 (而非十进制字面量) 解释, 故与之一致。
func bigIntFromFloat(f float64) *BigInt {
	bi, _ := new(big.Float).SetFloat64(f).Int(nil)
	return NewBigInt(bi)
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
	// defineProperty 定义的描述符优先 (访问器调 getter)。
	if t.PropDescs != nil {
		if d, ok := t.PropDescs[name]; ok {
			if acc, isAcc := d.Value.(*Accessor); isAcc {
				if acc.Getter == nil {
					return UndefinedSingleton, true
				}
				r := CallFunction(acc.Getter, t)
				if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
					return ev, true
				}
				if r == nil {
					return UndefinedSingleton, true
				}
				return r, true
			}
			if d.Value == nil {
				return UndefinedSingleton, true
			}
			return d.Value, true
		}
	}
	// 用户自有属性 (constructor 等)
	if t.Props != nil {
		if v, ok := t.Props[name]; ok {
			return v, true
		}
	}
	// 固定属性
	switch name {
	case "length":
		return NewInt(int64(t.arrLength())), true
	case "byteLength":
		return NewInt(int64(t.arrLength() * t.Kind.ElemSize)), true
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
	// 方法: 先查原型链 (从 GetProto 起, 派生类实例的 ProtoOverride 优先),
	// 再走内建分派。派生类原型 (MyU8.prototype) 上若有 constructor 等自有
	// 属性, 必须在这里命中 —— 否则会穿透到底层类型原型。
	if v, found := t.lookupPrototypeChain(name); found {
		return v, true
	}
	return t.callMethod(name)
}

// lookupPrototypeChain 沿 [[Prototype]] 链查找字符串自有属性 (不调用元素的
// getter), 命中返回 (值, true)。起点是 GetProto() —— 派生类实例为
// ProtoOverride (MyU8.prototype), 普通实例为同类型原型对象。
func (t *TypedArray) lookupPrototypeChain(name string) (Value, bool) {
	for cur := t.GetProto(); cur != nil; {
		if obj, ok := cur.(*Object); ok {
			if v, found := obj.GetProperty(name); found {
				return v, true
			}
			cur = obj.Proto
			continue
		}
		if gp, ok := cur.(interface{ GetProto() Value }); ok {
			if v, found := cur.GetProperty(name); found {
				return v, true
			}
			cur = gp.GetProto()
			continue
		}
		break
	}
	return nil, false
}

// callMethod 分派 TypedArray 的内建方法。
func (t *TypedArray) callMethod(name string) (Value, bool) {
	// 需要回调的通用算法共享一个形态: 捕获起始长度 (规范在入口读
	// [[ArrayLength]]), 迭代时逐点检查边界 (resizable buffer 缩容后
	// 越界索引读为 undefined 但仍回调)。
	iterMethod := func(mName string, fn func(ta *TypedArray, args []Value) Value) (Value, bool) {
		return NewBuiltinMethod(mName, func(this Value, margs ...Value) Value {
			ta, ok := this.(*TypedArray)
			if !ok {
				return NewTypeError("TypedArray.prototype.%s: receiver must be a TypedArray", mName)
			}
			if ta.OutOfBounds() {
				return NewTypeError("TypedArray.prototype.%s: typed array is out of bounds", mName)
			}
			return fn(ta, margs)
		}), true
	}
	switch name {
	case NewSymbol("Symbol.iterator").Inspect():
		return NewBuiltin("[Symbol.iterator]", func(args ...Value) Value {
			return NewArrayIterator(t.ToArray())
		}), true
	case "values":
		return iterMethod("values", func(ta *TypedArray, args []Value) Value {
			return NewArrayIterator(ta.ToArray())
		})
	case "keys":
		return iterMethod("keys", func(ta *TypedArray, args []Value) Value {
			n := ta.arrLength()
			els := make([]Value, n)
			for i := 0; i < n; i++ {
				els[i] = NewInt(int64(i))
			}
			return NewArrayIterator(NewArray(els))
		})
	case "entries":
		return iterMethod("entries", func(ta *TypedArray, args []Value) Value {
			n := ta.arrLength()
			els := make([]Value, n)
			for i := 0; i < n; i++ {
				els[i] = NewArray([]Value{NewInt(int64(i)), ta.getElement(i)})
			}
			return NewArrayIterator(NewArray(els))
		})
	case "set":
		return iterMethod("set", func(ta *TypedArray, sargs []Value) Value {
			if len(sargs) == 0 {
				return UndefinedSingleton
			}
			offset := 0
			if len(sargs) > 1 {
				f, err := toF64E(sargs[1])
				if err != nil {
					return err
				}
				offset = int(f)
			}
			if offset < 0 {
				return NewRangeError("TypedArray.prototype.set: offset is negative")
			}
			src, ok := sargs[0].(*TypedArray)
			if ok {
				// 跨 BigInt/非 BigInt 视图互拷是 TypeError (规范 SetTypedArrayFromTypedArray)。
				if src.Kind.BigInt != ta.Kind.BigInt {
					return NewTypeError("TypedArray.prototype.set: cannot mix BigInt and non-BigInt views")
				}
				if offset+src.arrLength() > ta.arrLength() {
					return NewRangeError("TypedArray.prototype.set: source is too large")
				}
				for i := 0; i < src.arrLength(); i++ {
					if err := ta.setElementE(offset+i, src.getElement(i)); err != nil {
						return err
					}
				}
				return UndefinedSingleton
			}
			if next, ok := Iterate(sargs[0]); ok {
				i := offset
				for {
					v, done := next()
					if done {
						break
					}
					if i >= ta.arrLength() {
						return NewRangeError("TypedArray.prototype.set: source is too large")
					}
					if err := ta.setElementE(i, v); err != nil {
						return err
					}
					i++
				}
				return UndefinedSingleton
			}
			// array-like / Proxy 源: 读 length 再逐索引取 (规范
			// TypedArraySetFromArrayLike 语义; 中途 resize 由 setElement
			// 边界检查自然覆盖)。
			lv, ok := sargs[0].GetProperty("length")
			if !ok || lv == nil {
				return NewTypeError("TypedArray.prototype.set: invalid source")
			}
			nf, lerr := toF64E(lv)
			if lerr != nil {
				return lerr
			}
			n := int(nf)
			if n < 0 || offset+n > ta.arrLength() {
				return NewRangeError("TypedArray.prototype.set: source is too large")
			}
			for i := 0; i < n; i++ {
				v, _ := sargs[0].GetProperty(fmt.Sprintf("%d", i))
				if v == nil {
					v = UndefinedSingleton
				}
				if err := ta.setElementE(offset+i, v); err != nil {
					return err
				}
			}
			return UndefinedSingleton
		})
	case "subarray":
		return iterMethod("subarray", func(ta *TypedArray, sargs []Value) Value {
			start, end, err := ta.rangeArgs(sargs)
			if err != nil {
				return err
			}
			return &TypedArray{
				Kind:       ta.Kind,
				Buffer:     ta.Buffer,
				ByteOffset: ta.ByteOffset + start*ta.Kind.ElemSize,
				Length:     end - start,
			}
		})
	case "slice":
		return iterMethod("slice", func(ta *TypedArray, sargs []Value) Value {
			start, end, err := ta.rangeArgs(sargs)
			if err != nil {
				return err
			}
			n := end - start
			outV := ta.SpeciesCreate(n)
			out, ok := outV.(*TypedArray)
			if !ok {
				return outV
			}
			for i := 0; i < n; i++ {
				out.setElement(i, ta.getElement(start+i))
			}
			return out
		})
	case "join":
		return iterMethod("join", func(ta *TypedArray, sargs []Value) Value {
			sep := ","
			if len(sargs) > 0 && !isUndef(sargs[0]) {
				sep = ToString(sargs[0])
			}
			n := ta.arrLength()
			parts := make([]string, 0, n)
			for i := 0; i < n; i++ {
				parts = append(parts, ToString(ta.getElement(i)))
			}
			return NewString(strings.Join(parts, sep))
		})
	case "indexOf":
		return iterMethod("indexOf", func(ta *TypedArray, sargs []Value) Value {
			arg := argOr(sargs, 0)
			n := ta.arrLength()
			// 规范: length 为 0 时在 ToIntegerOrInfinity(fromIndex) 之前就返回 ——
			// 因而空视图不会触发 fromIndex 的 valueOf (可能抛错)。
			if n == 0 {
				return NewInt(-1)
			}
			from, err := fromIndexArg(sargs, 1, n)
			if err != nil {
				return err
			}
			for i := from; i < n; i++ {
				// indexOf/lastIndexOf 走 HasProperty+Strict Equality: 分离或缩容后
				// 索引属性不存在 (HasProperty=false), 该点直接跳过 —— 因此这里
				// 必须先做当前边界判定, 不能只看捕获的 n。
				if i >= ta.arrLength() {
					continue
				}
				if ta.elementEquals(arg, ta.getElement(i)) {
					return NewInt(int64(i))
				}
			}
			return NewInt(-1)
		})
	case "lastIndexOf":
		return iterMethod("lastIndexOf", func(ta *TypedArray, sargs []Value) Value {
			arg := argOr(sargs, 0)
			n := ta.arrLength()
			if n == 0 {
				return NewInt(-1)
			}
			from := n - 1
			if len(sargs) > 1 && !isUndef(sargs[1]) {
				fv, err := toF64E(sargs[1])
				if err != nil {
					return err
				}
				f := int(fv)
				if f < 0 {
					from = n + f
				} else if f < from {
					from = f
				}
			}
			for i := from; i >= 0; i-- {
				if i >= ta.arrLength() {
					continue
				}
				if ta.elementEquals(arg, ta.getElement(i)) {
					return NewInt(int64(i))
				}
			}
			return NewInt(-1)
		})
	case "includes":
		return iterMethod("includes", func(ta *TypedArray, sargs []Value) Value {
			arg := argOr(sargs, 0)
			n := ta.arrLength()
			if n == 0 {
				return FalseSingleton
			}
			from, err := fromIndexArg(sargs, 1, n)
			if err != nil {
				return err
			}
			for i := from; i < n; i++ {
				if ta.elementEquals(arg, ta.getElement(i)) {
					return TrueSingleton
				}
			}
			return FalseSingleton
		})
	case "hasOwnProperty":
		// 规范 sec-typedarray-hasownproperty: ValidateTypedArray 只对
		// 整数索引键做 [[IsOutOfBounds]] 检查 —— 越界视图上查索引抛
		// TypeError, 查 "byteLength" / 自定义键仍正常返回, 故不能用
		// 一律前置校验的 iterMethod。
		return NewBuiltinMethod("hasOwnProperty", func(this Value, sargs ...Value) Value {
			ta, ok := this.(*TypedArray)
			if !ok {
				return NewTypeError("TypedArray.prototype.hasOwnProperty: receiver must be a TypedArray")
			}
			if len(sargs) == 0 {
				return FalseSingleton
			}
			if sym, ok := sargs[0].(*Symbol); ok {
				_, found := ta.GetSymbolPropertyDescriptor(sym)
				return NewBoolean(found)
			}
			name := ToString(sargs[0])
			if n, ok := nameIndex(name); ok {
				if ta.OutOfBounds() {
					return TypedArrayOutOfBounds("hasOwnProperty")
				}
				return NewBoolean(n < ta.arrLength())
			}
			switch name {
			case "length", "byteLength", "byteOffset", "buffer", "BYTES_PER_ELEMENT":
				return TrueSingleton
			}
			return FalseSingleton
		}), true
	case "at":
		return iterMethod("at", func(ta *TypedArray, sargs []Value) Value {
			n := ta.arrLength()
			f, err := toF64E(argOr(sargs, 0))
			if err != nil {
				return err
			}
			i := int(f)
			if i < 0 {
				i += n
			}
			if i < 0 || i >= n {
				return UndefinedSingleton
			}
			return ta.getElement(i)
		})
	case "fill":
		return iterMethod("fill", func(ta *TypedArray, sargs []Value) Value {
			var v Value = UndefinedSingleton
			if len(sargs) > 0 {
				v = sargs[0]
			}
			start, end, err := ta.rangeArgs(sargs[1:])
			if err != nil {
				return err
			}
			for i := start; i < end; i++ {
				ta.setElement(i, v)
			}
			return ta
		})
	case "copyWithin":
		return iterMethod("copyWithin", func(ta *TypedArray, sargs []Value) Value {
			n := ta.arrLength()
			rel := func(v float64) int {
				i := int(v)
				if i < 0 {
					i += n
					if i < 0 {
						i = 0
					}
				} else if i > n {
					i = n
				}
				return i
			}
			tf, err := toF64E(argOr(sargs, 0))
			if err != nil {
				return err
			}
			target := rel(tf)
			ff, err := toF64E(argOr(sargs, 1))
			if err != nil {
				return err
			}
			from := rel(ff)
			final := n
			if len(sargs) > 2 && !isUndef(sargs[2]) {
				ef, err := toF64E(sargs[2])
				if err != nil {
					return err
				}
				final = rel(ef)
			}
			count := final - from
			if count > n-target {
				count = n - target
			}
			if count <= 0 {
				return ta
			}
			tmp := make([]Value, count)
			for i := 0; i < count; i++ {
				tmp[i] = ta.getElement(from + i)
			}
			for i := 0; i < count; i++ {
				ta.setElement(target+i, tmp[i])
			}
			return ta
		})
	case "reverse":
		return iterMethod("reverse", func(ta *TypedArray, sargs []Value) Value {
			n := ta.arrLength()
			for i := 0; i < n/2; i++ {
				a, b := ta.getElement(i), ta.getElement(n-1-i)
				ta.setElement(i, b)
				ta.setElement(n-1-i, a)
			}
			return ta
		})
	case "sort":
		return iterMethod("sort", func(ta *TypedArray, sargs []Value) Value {
			n := ta.arrLength()
			els := make([]Value, n)
			for i := 0; i < n; i++ {
				els[i] = ta.getElement(i)
			}
			var cmp func(a, b Value) bool
			if len(sargs) > 0 && IsCallable(sargs[0]) {
				cmpFn := sargs[0]
				cmp = func(a, b Value) bool {
					return toF64Value(CallFunction(cmpFn, UndefinedSingleton, a, b)) < 0
				}
			} else {
				cmp = func(a, b Value) bool {
					return toNumberValue(a) < toNumberValue(b)
				}
			}
			sort.SliceStable(els, func(i, j int) bool { return cmp(els[i], els[j]) })
			for i := 0; i < n; i++ {
				ta.setElement(i, els[i])
			}
			return ta
		})
	case "forEach":
		return iterMethod("forEach", func(ta *TypedArray, sargs []Value) Value {
			if len(sargs) == 0 || !IsCallable(sargs[0]) {
				return NewTypeError("TypedArray.prototype.forEach: callback must be a function")
			}
			n := ta.arrLength()
			for i := 0; i < n; i++ {
				if _, err := callTypedArrayCB(sargs[0], argOr(sargs, 1), ta.getElement(i), NewInt(int64(i)), ta); err != nil {
					return err
				}
			}
			return UndefinedSingleton
		})
	case "map":
		return iterMethod("map", func(ta *TypedArray, sargs []Value) Value {
			if len(sargs) == 0 || !IsCallable(sargs[0]) {
				return NewTypeError("TypedArray.prototype.map: callback must be a function")
			}
			n := ta.arrLength()
			outV := ta.SpeciesCreate(n)
			out, ok := outV.(*TypedArray)
			if !ok {
				return outV
			}
			for i := 0; i < n; i++ {
				v, err := callTypedArrayCB(sargs[0], argOr(sargs, 1), ta.getElement(i), NewInt(int64(i)), ta)
				if err != nil {
					return err
				}
				out.setElement(i, v)
			}
			return out
		})
	case "filter":
		return iterMethod("filter", func(ta *TypedArray, sargs []Value) Value {
			if len(sargs) == 0 || !IsCallable(sargs[0]) {
				return NewTypeError("TypedArray.prototype.filter: callback must be a function")
			}
			n := ta.arrLength()
			var kept []Value
			for i := 0; i < n; i++ {
				v := ta.getElement(i)
				r, err := callTypedArrayCB(sargs[0], argOr(sargs, 1), v, NewInt(int64(i)), ta)
				if err != nil {
					return err
				}
				if r.IsTruthy() {
					kept = append(kept, v)
				}
			}
			outV := ta.SpeciesCreate(len(kept))
			out, ok := outV.(*TypedArray)
			if !ok {
				return outV
			}
			for i, v := range kept {
				out.setElement(i, v)
			}
			return out
		})
	case "every":
		return iterMethod("every", func(ta *TypedArray, sargs []Value) Value {
			if len(sargs) == 0 || !IsCallable(sargs[0]) {
				return NewTypeError("TypedArray.prototype.every: callback must be a function")
			}
			n := ta.arrLength()
			for i := 0; i < n; i++ {
				r, err := callTypedArrayCB(sargs[0], argOr(sargs, 1), ta.getElement(i), NewInt(int64(i)), ta)
				if err != nil {
					return err
				}
				if !r.IsTruthy() {
					return FalseSingleton
				}
			}
			return TrueSingleton
		})
	case "some":
		return iterMethod("some", func(ta *TypedArray, sargs []Value) Value {
			if len(sargs) == 0 || !IsCallable(sargs[0]) {
				return NewTypeError("TypedArray.prototype.some: callback must be a function")
			}
			n := ta.arrLength()
			for i := 0; i < n; i++ {
				r, err := callTypedArrayCB(sargs[0], argOr(sargs, 1), ta.getElement(i), NewInt(int64(i)), ta)
				if err != nil {
					return err
				}
				if r.IsTruthy() {
					return TrueSingleton
				}
			}
			return FalseSingleton
		})
	case "find", "findIndex", "findLast", "findLastIndex":
		return iterMethod(name, func(ta *TypedArray, sargs []Value) Value {
			if len(sargs) == 0 || !IsCallable(sargs[0]) {
				return NewTypeError("TypedArray.prototype.%s: callback must be a function", name)
			}
			n := ta.arrLength()
			backwards := name == "findLast" || name == "findLastIndex"
			for k := 0; k < n; k++ {
				i := k
				if backwards {
					i = n - 1 - k
				}
				v := ta.getElement(i)
				r, err := callTypedArrayCB(sargs[0], argOr(sargs, 1), v, NewInt(int64(i)), ta)
				if err != nil {
					return err
				}
				if r.IsTruthy() {
					if name == "findIndex" || name == "findLastIndex" {
						return NewInt(int64(i))
					}
					return v
				}
			}
			if name == "findIndex" || name == "findLastIndex" {
				return NewInt(-1)
			}
			return UndefinedSingleton
		})
	case "reduce", "reduceRight":
		return iterMethod(name, func(ta *TypedArray, sargs []Value) Value {
			if len(sargs) == 0 || !IsCallable(sargs[0]) {
				return NewTypeError("TypedArray.prototype.%s: callback must be a function", name)
			}
			n := ta.arrLength()
			if n == 0 && len(sargs) < 2 {
				return NewTypeError("TypedArray.prototype.%s: empty array with no initial value", name)
			}
			var acc Value
			i, step := 0, 1
			if name == "reduceRight" {
				i, step = n-1, -1
			}
			if len(sargs) > 1 {
				acc = sargs[1]
			} else {
				acc = ta.getElement(i)
				i += step
			}
			for ; i >= 0 && i < n; i += step {
				r, err := callTypedArrayCB(sargs[0], UndefinedSingleton, acc, ta.getElement(i), NewInt(int64(i)), ta)
				if err != nil {
					return err
				}
				acc = r
			}
			return acc
		})
	case "toLocaleString", "toString":
		return iterMethod("toLocaleString", func(ta *TypedArray, sargs []Value) Value {
			n := ta.arrLength()
			parts := make([]string, 0, n)
			for i := 0; i < n; i++ {
				parts = append(parts, ToString(ta.getElement(i)))
			}
			return NewString(strings.Join(parts, ","))
		})
	case "toReversed":
		return iterMethod("toReversed", func(ta *TypedArray, sargs []Value) Value {
			n := ta.arrLength()
			out := NewTypedArray(ta.Kind, n)
			for i := 0; i < n; i++ {
				out.setElement(i, ta.getElement(n-1-i))
			}
			return out
		})
	case "toSorted":
		return iterMethod("toSorted", func(ta *TypedArray, sargs []Value) Value {
			n := ta.arrLength()
			els := make([]Value, n)
			for i := 0; i < n; i++ {
				els[i] = ta.getElement(i)
			}
			var cmp func(a, b Value) bool
			if len(sargs) > 0 && IsCallable(sargs[0]) {
				cmpFn := sargs[0]
				cmp = func(a, b Value) bool {
					return toF64Value(CallFunction(cmpFn, UndefinedSingleton, a, b)) < 0
				}
			} else {
				cmp = func(a, b Value) bool {
					return toNumberValue(a) < toNumberValue(b)
				}
			}
			sort.SliceStable(els, func(i, j int) bool { return cmp(els[i], els[j]) })
			out := NewTypedArray(ta.Kind, n)
			for i := 0; i < n; i++ {
				out.setElement(i, els[i])
			}
			return out
		})
	case "with":
		return iterMethod("with", func(ta *TypedArray, sargs []Value) Value {
			n := ta.arrLength()
			f, err := toF64E(argOr(sargs, 0))
			if err != nil {
				return err
			}
			i := int(f)
			if i < 0 {
				i += n
			}
			if i < 0 || i >= n {
				return NewRangeError("TypedArray.prototype.with: index out of range")
			}
			out := NewTypedArray(ta.Kind, n)
			for k := 0; k < n; k++ {
				out.setElement(k, ta.getElement(k))
			}
			out.setElement(i, argOr(sargs, 1))
			return out
		})
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

// fromIndexArg 解析 indexOf/includes 的 fromIndex (负值按 length 回绕)。
// err 非 nil 表示 ToNumber 过程中用户回调抛错, 调用方直接透传。
func fromIndexArg(args []Value, i, n int) (int, Value) {
	if len(args) <= i || isUndef(args[i]) {
		return 0, nil
	}
	f, err := toF64E(args[i])
	if err != nil {
		return 0, err
	}
	if math.IsNaN(f) {
		return n, nil
	}
	idx := int(f)
	if idx < 0 {
		idx += n
		if idx < 0 {
			idx = 0
		}
	}
	if idx > n {
		idx = n
	}
	return idx, nil
}

// callTypedArrayCB 调用 TypedArray 迭代方法 (map/filter/forEach/…) 的用户回调,
// 并把回调抛出的异常透传出来。规范要求这些方法在回调抛出时立即中止并抛出 ——
// CallFunction 遇 JS throw 返回 undefined 且把错误塞进回调桥, 故必须显式取。
// 第二个返回值非 nil 表示回调抛出, 调用方须立即把它作为结果返回。
func callTypedArrayCB(fn Value, this Value, args ...Value) (Value, Value) {
	r := CallFunction(fn, this, args...)
	if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
		return nil, ev
	}
	return r, nil
}

func argOr(args []Value, i int) Value {
	if i < len(args) {
		return args[i]
	}
	return UndefinedSingleton
}

// rangeArgs 解析 slice/subarray/fill 的 (start, end)。
// err 非 nil 表示参数 ToNumber 过程中用户回调抛错, 调用方直接透传。
func (t *TypedArray) rangeArgs(args []Value) (int, int, Value) {
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
	start, end := 0, t.arrLength()
	if len(args) > 0 && !isUndef(args[0]) {
		f, err := toF64E(args[0])
		if err != nil {
			return 0, 0, err
		}
		start = clamp(f, t.arrLength())
	}
	if len(args) > 1 && !isUndef(args[1]) {
		f, err := toF64E(args[1])
		if err != nil {
			return 0, 0, err
		}
		end = clamp(f, t.arrLength())
	}
	if end < start {
		end = start
	}
	return start, end, nil
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
	n := t.arrLength()
	els := make([]Value, n)
	for i := 0; i < n; i++ {
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

// SpeciesCreate 实现 TypedArraySpeciesCreate (map/filter/slice/subarray 用)。
//
// 规范 (7.3.20 SpeciesConstructor):
//  1. C = ? Get(O, "constructor")          —— 走原型链, 访问器要被调用
//  2. C === undefined → 用本类型默认构造器
//  3. Type(C) !== Object → TypeError
//  4. S = ? Get(C, @@species)              —— 同样要调用访问器
//  5. S === null || S === undefined → 用本类型默认构造器
//  6. IsConstructor(S) 为假 → TypeError
//  7. 以 new S(n) 构造, 结果须是 TypedArray
//
// 关键修正: 旧实现读 t.Props["constructor"] 的**原始描述符**, 于是
// Object.defineProperty(sample, "constructor", {get(){...}}) 定义的自有
// 访问器从不被调用 (test262 speciesctor-get-ctor 断言 calls===1 直接暴露);
// @@species 也绕过了 Get, 读不到继承来的访问器。此处统一走 Get(O, key)。
func (t *TypedArray) SpeciesCreate(n int) Value {
	def := func() Value { return NewTypedArray(t.Kind, n) }

	ctor := t.getSpecValue("constructor")
	if ctor == nil || ctor == UndefinedSingleton {
		return def()
	}
	// 规范 7.3.20 步骤 3: Type(C) is not Object → TypeError。
	// 函数 (Closure/BuiltinFunction) 也是对象, 必须放行 —— 派生类构造器
	// 正是 Closure, 漏判会让 `class MyU8 extends Uint8Array` 实例的
	// super() 派生物种解析直接抛 TypeError。
	if !IsObjectValue(ctor) {
		return NewTypeError("TypedArray species constructor: constructor is not an object")
	}
	var species Value
	if sym := GetGlobalSymbol("Symbol.species"); sym != nil {
		// 沿原型链查 @@species (Get(C, @@species)): 派生类构造器自身无该
		// 访问器时, 继承自基类构造器 (Uint8Array) 的 get 访问器应被调用,
		// 返回 C 本身。
		if desc, found := LookupSymbolPropertyDescriptorChain(ctor, sym); found {
			species = desc.Value
			if acc, isAcc := species.(*Accessor); isAcc {
				if acc.Getter == nil {
					species = UndefinedSingleton
				} else {
					species = CallFunction(acc.Getter, ctor)
					if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
						return ev
					}
				}
			}
		}
	}
	if species == nil || species == UndefinedSingleton || species == NullSingleton {
		return def()
	}
	if !IsCallable(species) {
		return NewTypeError("TypedArray species constructor is not a constructor")
	}
	// new S(n): 走构造路径 (speciesctor 的 this 语义与 [[Construct]] 一致)。
	res := ConstructFunction(species, NewInt(int64(n)))
	if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
		return ev
	}
	if err, isErr := res.(*Error); isErr {
		return err
	}
	ta, ok := res.(*TypedArray)
	if !ok {
		return NewTypeError("TypedArray species constructor did not return a TypedArray")
	}
	return ta
}

// getSpecValue 以规范 Get 语义读取 TypedArray 实例的属性: 先自有属性
// (访问器需调用 getter), 再沿原型链查。无此属性返回 nil。
func (t *TypedArray) getSpecValue(name string) Value {
	if t.PropDescs != nil {
		if d, ok := t.PropDescs[name]; ok {
			if acc, isAcc := d.Value.(*Accessor); isAcc {
				if acc.Getter == nil {
					return UndefinedSingleton
				}
				r := CallFunction(acc.Getter, t)
				if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
					return ev
				}
				if r == nil {
					return UndefinedSingleton
				}
				return r
			}
			if d.Value == nil {
				return UndefinedSingleton
			}
			return d.Value
		}
	}
	if t.Props != nil {
		if v, ok := t.Props[name]; ok {
			if acc, isAcc := v.(*Accessor); isAcc {
				if acc.Getter == nil {
					return UndefinedSingleton
				}
				r := CallFunction(acc.Getter, t)
				if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
					return ev
				}
				return r
			}
			return v
		}
	}
	// 原型链: 从 GetProto 起 (派生类实例为 ProtoOverride) 沿链查找,
	// 访问器调用 getter。
	for cur := t.GetProto(); cur != nil; {
		if v, found := cur.GetProperty(name); found {
			if acc, isAcc := v.(*Accessor); isAcc {
				if acc.Getter == nil {
					return UndefinedSingleton
				}
				r := CallFunction(acc.Getter, t)
				if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
					return ev
				}
				return r
			}
			return v
		}
		gp, ok := cur.(interface{ GetProto() Value })
		if !ok {
			break
		}
		cur = gp.GetProto()
	}
	return nil
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
// 与 temporalProtos 同理: SetupGlobals 可能被并发调用, 读写都过锁。
var (
	taProtoRegistryMu sync.RWMutex
	taProtoRegistry   = map[string]Value{}
)

// SetTypedArrayProto 注册某类型的 prototype 对象。
func SetTypedArrayProto(name string, proto Value) {
	taProtoRegistryMu.Lock()
	defer taProtoRegistryMu.Unlock()
	taProtoRegistry[name] = proto
}

func typedArrayProto(name string) (Value, bool) {
	taProtoRegistryMu.RLock()
	defer taProtoRegistryMu.RUnlock()
	v, ok := taProtoRegistry[name]
	return v, ok
}

// %TypedArray% 抽象构造器 (供 Object.getPrototypeOf(Int8Array) === %TypedArray%)。
var (
	taCtorProtoMu sync.RWMutex
	taCtorProto   Value
)

// SetTypedArrayCtorProto 注册 %TypedArray% 构造器对象。
func SetTypedArrayCtorProto(v Value) {
	taCtorProtoMu.Lock()
	defer taCtorProtoMu.Unlock()
	taCtorProto = v
}

// GetTypedArrayCtorProto 返回 %TypedArray% 构造器对象 (未注册为 nil)。
func GetTypedArrayCtorProto() Value {
	taCtorProtoMu.RLock()
	defer taCtorProtoMu.RUnlock()
	return taCtorProto
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

// GetProto 返回类型化数组的原型 (经注册表按类型名解析)。
// 迭代协议解析沿原型链查找 Symbol 键时需要它。
func (t *TypedArray) GetProto() Value {
	if t.ProtoOverride != nil {
		return t.ProtoOverride
	}
	if p, ok := typedArrayProto(t.Kind.Name); ok {
		return p
	}
	return nil
}

// ===== Symbol 键自有属性 (SymbolPropertyStore) =====
// 语义与 *Array / *Object 一致, 存储落在 TypedArray 自身。

func (t *TypedArray) GetSymbolProperty(sym *Symbol) (Value, bool) {
	if t.SymbolProperties == nil {
		return nil, false
	}
	desc, ok := t.SymbolProperties[sym.ID]
	if !ok {
		return nil, false
	}
	if desc.Value == nil {
		return UndefinedSingleton, true
	}
	return desc.Value, true
}

func (t *TypedArray) GetSymbolPropertyDescriptor(sym *Symbol) (PropertyDescriptor, bool) {
	if t.SymbolProperties == nil {
		return PropertyDescriptor{}, false
	}
	d, ok := t.SymbolProperties[sym.ID]
	return d, ok
}

func (t *TypedArray) SetSymbolProperty(sym *Symbol, val Value) {
	if t.SymbolProperties == nil {
		t.SymbolProperties = make(map[uint64]PropertyDescriptor)
	}
	if d, exists := t.SymbolProperties[sym.ID]; exists {
		if d.Writable {
			d.Value = val
			t.SymbolProperties[sym.ID] = d
		}
		return
	}
	t.symbolKeyOrder = append(t.symbolKeyOrder, sym)
	t.SymbolProperties[sym.ID] = DataProperty(val)
}

func (t *TypedArray) DefineOwnSymbolProperty(sym *Symbol, desc PropertyDescriptor) {
	if t.SymbolProperties == nil {
		t.SymbolProperties = make(map[uint64]PropertyDescriptor)
	}
	if _, exists := t.SymbolProperties[sym.ID]; !exists {
		t.symbolKeyOrder = append(t.symbolKeyOrder, sym)
	}
	t.SymbolProperties[sym.ID] = desc
}

func (t *TypedArray) SymbolKeys() []*Symbol { return t.symbolKeyOrder }

// ===== OwnPropertyStore (Object.defineProperty / getOwnPropertyDescriptor /
// hasOwnProperty / Object.keys 的落点) =====
//
// 注意: length / byteLength / byteOffset / buffer 虽然能经实例读取, 但**不是**
// 自有属性 —— 规范把它们做成 %TypedArray%.prototype 上的访问器 (10.4.5.4
// ~ 10.4.5.7), 实例仅暴露整数索引与用户定义的字符串键。故 OwnKeys /
// HasOwn / OwnDescriptor 都不列这几个键 (与 V8 一致:
// Object.getOwnPropertyNames(new Uint8Array(2)) === ["0","1"]),
// 实例读它们由 GetProperty 从原型链解析。

// OwnKeys 返回 TypedArray 自有键: 整数索引升序在前, 之后是显式定义的
// 字符串键 (propOrder 插入顺序) 与 SetProperty 写入的键。
func (t *TypedArray) OwnKeys() []string {
	n := t.arrLength()
	keys := make([]string, 0, n+len(t.propOrder))
	for i := 0; i < n; i++ {
		keys = append(keys, Itoa(i))
	}
	seen := make(map[string]bool, n+len(t.propOrder))
	for i := 0; i < n; i++ {
		seen[Itoa(i)] = true
	}
	for _, k := range t.propOrder {
		if seen[k] {
			continue
		}
		seen[k] = true
		keys = append(keys, k)
	}
	if t.Props != nil {
		for k := range t.Props {
			if seen[k] {
				continue
			}
			seen[k] = true
			keys = append(keys, k)
		}
	}
	return keys
}

// EnumerableOwnKeys 返回可枚举自有键: 全部整数索引 (TypedArray 元素恒可枚举)
// 与显式标记可枚举的字符串键。
func (t *TypedArray) EnumerableOwnKeys() []string {
	n := t.arrLength()
	keys := make([]string, 0, n)
	for i := 0; i < n; i++ {
		keys = append(keys, Itoa(i))
	}
	for _, k := range t.propOrder {
		if d, ok := t.PropDescs[k]; ok && d.Enumerable {
			keys = append(keys, k)
		}
	}
	return keys
}

// HasOwn 检查 name 是否为 TypedArray 自有属性: 范围内的整数索引, 或经
// defineProperty / 赋值新增的字符串键。length/byteLength/... 非自有。
func (t *TypedArray) HasOwn(name string) bool {
	// 规范 sec-typedarray-hasownproperty 第 2 步: 先 ValidateTypedArray(O)
	// (ES2024 起含 [[IsOutOfBounds]] 检查), 越界视图上**整数索引**的
	// HasOwnProperty 必须抛 TypeError, 而不是静默返回 false;
	// 非索引键 (byteLength / 自定义属性等) 不受此约束。
	if idx, ok := nameIndex(name); ok {
		if t.OutOfBounds() {
			panic(TypedArrayOutOfBounds("hasOwnProperty"))
		}
		return idx < t.arrLength()
	}
	if t.PropDescs != nil {
		if d, ok := t.PropDescs[name]; ok && !d.Deleted {
			return true
		}
	}
	if t.Props != nil {
		if _, ok := t.Props[name]; ok {
			return true
		}
	}
	return false
}

// OwnDescriptor 返回 TypedArray 自有属性描述符:
//   - 显式 defineProperty 定义的键: 返回存储的完整描述符;
//   - 范围内的整数索引: {value, writable:true, enumerable:true,
//     configurable:true} (规范: 元素属性可配置);
//   - 其余字符串键 (Props 赋值): 普通可写可枚举可配置数据属性。
func (t *TypedArray) OwnDescriptor(name string) (PropertyDescriptor, bool) {
	if t.PropDescs != nil {
		if d, ok := t.PropDescs[name]; ok {
			if d.Deleted {
				return PropertyDescriptor{}, false
			}
			return d, true
		}
	}
	if idx, err := Atoi(name); err == nil && idx >= 0 && idx < t.arrLength() {
		v := t.getElement(idx)
		if v == nil {
			v = UndefinedSingleton
		}
		return PropertyDescriptor{Value: v, Writable: true, Enumerable: true, Configurable: true}, true
	}
	if t.Props != nil {
		if v, ok := t.Props[name]; ok {
			if _, isAcc := v.(*Accessor); isAcc {
				return PropertyDescriptor{Value: v, Writable: false, Enumerable: true, Configurable: true}, true
			}
			return PropertyDescriptor{Value: v, Writable: true, Enumerable: true, Configurable: true}, true
		}
	}
	return PropertyDescriptor{}, false
}

// DefineOwn 以完整描述符定义 TypedArray 自有属性 (Object.defineProperty 落点)。
//
//   - 整数索引 (范围内): 数据描述符写入元素; 访问器描述符违反 Integer-Indexed
//     exotic 的 [[DefineOwnProperty]] (元素不可为访问器), 由调用方依据
//     描述符合法性抛错, 这里按数据落值。
//   - 其余字符串键: 存入 PropDescs (访问器 getter 由 GetProperty 调用), 数据
//     属性同时落 Props 以支持不经描述符通道的读取。
func (t *TypedArray) DefineOwn(name string, desc PropertyDescriptor) bool {
	if t.PropDescs == nil {
		t.PropDescs = make(map[string]PropertyDescriptor)
	}
	_, hadDesc := t.PropDescs[name]
	if !hadDesc {
		t.propOrder = append(t.propOrder, name)
	}
	t.PropDescs[name] = desc
	// 索引: 数据描述符同步写元素, 使 SetProperty 通道外的读也看到新值。
	if idx, err := Atoi(name); err == nil && idx >= 0 {
		if _, isAcc := desc.Value.(*Accessor); !isAcc && idx < t.arrLength() {
			if desc.Value == nil {
				t.setElement(idx, UndefinedSingleton)
			} else {
				t.setElement(idx, desc.Value)
			}
		}
	}
	return true
}

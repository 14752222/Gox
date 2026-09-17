package stdlib

import (
	"math"
	"math/big"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// setupBigInt 注册 BigInt 构造器、原型与静态方法。
//
// BigInt 是 Temporal 的前置依赖: Instant.prototype.epochNanoseconds 的
// 规范类型是 BigInt (纳秒 epoch 约 1.7e18，超出 double 的安全整数范围)。
func setupBigInt(env *runtime.Environment) {
	proto := object.NewObject()

	// ===== BigInt.prototype =====
	proto.SetProperty("toString", object.NewBuiltinMethod("toString", func(this object.Value, args ...object.Value) object.Value {
		bi, ok := this.(*object.BigInt)
		if !ok {
			return object.NewErrorWithName("TypeError", "BigInt.prototype.toString requires that 'this' be a BigInt")
		}
		radix := 10
		if len(args) > 0 && args[0] != object.UndefinedSingleton {
			r := toFloat(args[0])
			radix = int(r)
			if r != float64(radix) || radix < 2 || radix > 36 {
				return object.NewErrorWithName("RangeError", "toString() radix argument must be between 2 and 36")
			}
		}
		return object.NewString(bi.ToStringRadix(radix))
	}))

	// 本运行时没有 Intl，toLocaleString 退化为 toString。
	proto.SetProperty("toLocaleString", object.NewBuiltinMethod("toLocaleString", func(this object.Value, args ...object.Value) object.Value {
		bi, ok := this.(*object.BigInt)
		if !ok {
			return object.NewErrorWithName("TypeError", "BigInt.prototype.toLocaleString requires that 'this' be a BigInt")
		}
		return object.NewString(bi.ToStringRadix(10))
	}))

	proto.SetProperty("valueOf", object.NewBuiltinMethod("valueOf", func(this object.Value, args ...object.Value) object.Value {
		bi, ok := this.(*object.BigInt)
		if !ok {
			return object.NewErrorWithName("TypeError", "BigInt.prototype.valueOf requires that 'this' be a BigInt")
		}
		return bi
	}))

	proto.SetSymbolProperty(object.GetGlobalSymbol("Symbol.toStringTag"), object.NewString("BigInt"))
	object.SetBigIntProto(proto)

	// ===== BigInt() 构造器 =====
	// 与 Symbol 一样，BigInt 不能用 new 调用 (new BigInt(1) 抛 TypeError)，
	// 但作为函数可正常转换。本运行时的 OP_NEW 不区分调用形态，
	// 故这里只保证函数式调用语义。
	fn := object.NewBuiltin("BigInt", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBigIntInt64(0)
		}
		return toBigIntValue(args[0])
	})
	fn.SetProperty("name", object.NewString("BigInt"))
	fn.SetProperty("length", object.NewInt(1))
	fn.SetProperty("prototype", proto)
	proto.SetProperty("constructor", fn)

	// ===== 静态方法 =====
	fn.SetProperty("asIntN", object.NewBuiltin("asIntN", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return object.NewErrorWithName("TypeError", "BigInt.asIntN requires 2 arguments")
		}
		bits, ok := toIndex(args[0])
		if !ok {
			return object.NewErrorWithName("RangeError", "BigInt.asIntN: bits must be a non-negative integer")
		}
		bi, ok := args[1].(*object.BigInt)
		if !ok {
			return toBigIntValue(args[1])
		}
		return object.NewBigInt(wrapToSigned(bi.Value, bits))
	}))

	fn.SetProperty("asUintN", object.NewBuiltin("asUintN", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return object.NewErrorWithName("TypeError", "BigInt.asUintN requires 2 arguments")
		}
		bits, ok := toIndex(args[0])
		if !ok {
			return object.NewErrorWithName("RangeError", "BigInt.asUintN: bits must be a non-negative integer")
		}
		bi, ok := args[1].(*object.BigInt)
		if !ok {
			return toBigIntValue(args[1])
		}
		return object.NewBigInt(wrapToUnsigned(bi.Value, bits))
	}))

	env.Declare("BigInt", fn, false)
}

// toBigIntValue 实现 ECMAScript 的 ToBigInt 抽象操作。
//
// 与 ToNumber 的关键差异: Number 侧必须是整数值，非整数/NaN/Infinity
// 全部抛 RangeError——BigInt 无法表示这些值，静默截断会引入难以察觉的误差。
func toBigIntValue(v object.Value) object.Value {
	switch val := v.(type) {
	case *object.BigInt:
		return val
	case *object.Boolean:
		if val.Value {
			return object.NewBigIntInt64(1)
		}
		return object.NewBigIntInt64(0)
	case *object.Number:
		f := val.Value
		if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
			return object.NewErrorWithName("RangeError", "The number "+object.NumberToString(f)+" cannot be converted to a BigInt because it is not an integer")
		}
		r := new(big.Rat).SetFloat64(f)
		if !r.IsInt() {
			return object.NewErrorWithName("RangeError", "The number "+object.NumberToString(f)+" cannot be converted to a BigInt because it is not an integer")
		}
		return object.NewBigInt(r.Num())
	case *object.String:
		n, ok := object.ParseBigIntString(val.Value)
		if !ok {
			return object.NewErrorWithName("SyntaxError", "Cannot convert "+val.Value+" to a BigInt")
		}
		return object.NewBigInt(n)
	case *object.Undefined, *object.Null:
		return object.NewErrorWithName("TypeError", "Cannot convert "+object.ToString(v)+" to a BigInt")
	default:
		return object.NewErrorWithName("TypeError", "Cannot convert "+object.ToString(v)+" to a BigInt")
	}
}

// toIndex 实现 ECMAScript 的 ToIndex (非负整数索引，上限 2^53-1)。
func toIndex(v object.Value) (int64, bool) {
	n, ok := v.(*object.Number)
	if !ok {
		return 0, false
	}
	f := n.Value
	if math.IsNaN(f) || f != math.Trunc(f) || f < 0 || f >= 1<<53 {
		return 0, false
	}
	return int64(f), true
}

// wrapToUnsigned 将 BigInt 规约到 [0, 2^bits) (对应 BigInt.asUintN)。
func wrapToUnsigned(v *big.Int, bits int64) *big.Int {
	modulus := new(big.Int).Lsh(big.NewInt(1), uint(bits))
	r := new(big.Int).Mod(v, modulus)
	if r.Sign() < 0 {
		r.Add(r, modulus)
	}
	return r
}

// wrapToSigned 将 BigInt 规约到 [-2^(bits-1), 2^(bits-1)) (对应 BigInt.asIntN)。
func wrapToSigned(v *big.Int, bits int64) *big.Int {
	modulus := new(big.Int).Lsh(big.NewInt(1), uint(bits))
	r := new(big.Int).Mod(v, modulus)
	half := new(big.Int).Rsh(modulus, 1)
	if r.Cmp(half) >= 0 {
		r.Sub(r, modulus)
	}
	return r
}

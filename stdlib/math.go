package stdlib

import (
	"math"
	"math/bits"
	"math/rand"

	"github.com/14752222/Gox/object"
)

// setupMath 创建 Math 对象。
func setupMath() *object.Object {
	m := object.NewObject()

	// 常量
	m.SetBuiltinConstProperty("PI", object.NewNumber(math.Pi))
	m.SetBuiltinConstProperty("E", object.NewNumber(math.E))
	m.SetBuiltinConstProperty("LN2", object.NewNumber(math.Ln2))
	m.SetBuiltinConstProperty("LN10", object.NewNumber(math.Log(10)))
	m.SetBuiltinConstProperty("LOG2E", object.NewNumber(math.Log2E))
	m.SetBuiltinConstProperty("LOG10E", object.NewNumber(math.Log10E))
	m.SetBuiltinConstProperty("SQRT2", object.NewNumber(math.Sqrt2))
	m.SetBuiltinConstProperty("SQRT1_2", object.NewNumber(1.0/math.Sqrt2))

	// 取一个或两个数字参数的辅助函数
	numArg := func(args []object.Value, idx int) float64 {
		if idx >= len(args) {
			return math.NaN()
		}
		return toFloat(args[idx])
	}

	// 数学函数
	m.SetBuiltinProperty("abs", object.NewBuiltin("abs", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Abs(numArg(args, 0)))
	}))

	m.SetBuiltinProperty("ceil", object.NewBuiltin("ceil", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Ceil(numArg(args, 0)))
	}))

	m.SetBuiltinProperty("floor", object.NewBuiltin("floor", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Floor(numArg(args, 0)))
	}))

	m.SetBuiltinProperty("round", object.NewBuiltin("round", func(args ...object.Value) object.Value {
		v := numArg(args, 0)
		// JavaScript Math.round: 向上取整 (0.5 → 1, -0.5 → 0, -1.5 → -1)
		return object.NewNumber(math.Floor(v + 0.5))
	}))

	m.SetBuiltinProperty("trunc", object.NewBuiltin("trunc", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Trunc(numArg(args, 0)))
	}))

	m.SetBuiltinProperty("sqrt", object.NewBuiltin("sqrt", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Sqrt(numArg(args, 0)))
	}))

	m.SetBuiltinProperty("cbrt", object.NewBuiltin("cbrt", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Cbrt(numArg(args, 0)))
	}))

	m.SetBuiltinProperty("pow", object.NewBuiltin("pow", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Pow(numArg(args, 0), numArg(args, 1)))
	}))

	m.SetBuiltinProperty("exp", object.NewBuiltin("exp", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Exp(numArg(args, 0)))
	}))

	m.SetBuiltinProperty("log", object.NewBuiltin("log", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Log(numArg(args, 0)))
	}))

	m.SetBuiltinProperty("log2", object.NewBuiltin("log2", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Log2(numArg(args, 0)))
	}))

	m.SetBuiltinProperty("log10", object.NewBuiltin("log10", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Log10(numArg(args, 0)))
	}))

	m.SetBuiltinProperty("max", object.NewBuiltin("max", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewNumber(math.Inf(-1))
		}
		result := numArg(args, 0)
		for i := 1; i < len(args); i++ {
			v := numArg(args, i)
			if math.IsNaN(v) {
				return object.NewNumber(math.NaN())
			}
			if v > result {
				result = v
			}
		}
		return object.NewNumber(result)
	}))

	m.SetBuiltinProperty("min", object.NewBuiltin("min", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewNumber(math.Inf(1))
		}
		result := numArg(args, 0)
		for i := 1; i < len(args); i++ {
			v := numArg(args, i)
			if math.IsNaN(v) {
				return object.NewNumber(math.NaN())
			}
			if v < result {
				result = v
			}
		}
		return object.NewNumber(result)
	}))

	m.SetBuiltinProperty("hypot", object.NewBuiltin("hypot", func(args ...object.Value) object.Value {
		// Math.hypot(...values): 欧几里得范数 sqrt(x² + y² + ...)
		// 按 ECMAScript 语义: 任一参数为 ±Infinity → +Infinity;
		// 否则任一参数为 NaN → NaN
		sum := 0.0
		for _, a := range args {
			v := toFloat(a)
			if math.IsInf(v, 0) {
				return object.NewNumber(math.Inf(1))
			}
			if math.IsNaN(v) {
				return object.NewNumber(math.NaN())
			}
			sum += v * v
		}
		return object.NewNumber(math.Sqrt(sum))
	}))

	m.SetBuiltinProperty("random", object.NewBuiltin("random", func(args ...object.Value) object.Value {
		return object.NewNumber(rand.Float64())
	}))

	m.SetBuiltinProperty("sign", object.NewBuiltin("sign", func(args ...object.Value) object.Value {
		v := numArg(args, 0)
		if v > 0 {
			return object.NewNumber(1)
		}
		if v < 0 {
			return object.NewNumber(-1)
		}
		return object.NewNumber(0)
	}))

	m.SetBuiltinProperty("sin", object.NewBuiltin("sin", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Sin(numArg(args, 0)))
	}))

	m.SetBuiltinProperty("cos", object.NewBuiltin("cos", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Cos(numArg(args, 0)))
	}))

	m.SetBuiltinProperty("tan", object.NewBuiltin("tan", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Tan(numArg(args, 0)))
	}))

	m.SetBuiltinProperty("asin", object.NewBuiltin("asin", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Asin(numArg(args, 0)))
	}))

	m.SetBuiltinProperty("acos", object.NewBuiltin("acos", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Acos(numArg(args, 0)))
	}))

	m.SetBuiltinProperty("atan", object.NewBuiltin("atan", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Atan(numArg(args, 0)))
	}))

	m.SetBuiltinProperty("atan2", object.NewBuiltin("atan2", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Atan2(numArg(args, 0), numArg(args, 1)))
	}))

	// 取一个或两个整数参数的辅助函数 (缺失时按 0 处理)
	numArgInt := func(args []object.Value, idx int) int64 {
		if idx >= len(args) {
			return 0
		}
		return toInt(args[idx])
	}

	// ===== ES6 补充: 双曲函数与数值工具 =====

	m.SetBuiltinProperty("sinh", object.NewBuiltin("sinh", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Sinh(numArg(args, 0)))
	}))
	m.SetBuiltinProperty("cosh", object.NewBuiltin("cosh", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Cosh(numArg(args, 0)))
	}))
	m.SetBuiltinProperty("tanh", object.NewBuiltin("tanh", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Tanh(numArg(args, 0)))
	}))
	m.SetBuiltinProperty("asinh", object.NewBuiltin("asinh", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Asinh(numArg(args, 0)))
	}))
	m.SetBuiltinProperty("acosh", object.NewBuiltin("acosh", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Acosh(numArg(args, 0)))
	}))
	m.SetBuiltinProperty("atanh", object.NewBuiltin("atanh", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Atanh(numArg(args, 0)))
	}))
	m.SetBuiltinProperty("expm1", object.NewBuiltin("expm1", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Expm1(numArg(args, 0)))
	}))
	m.SetBuiltinProperty("log1p", object.NewBuiltin("log1p", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Log1p(numArg(args, 0)))
	}))

	// fround(x): 舍入到最接近的 32 位单精度浮点数
	m.SetBuiltinProperty("fround", object.NewBuiltin("fround", func(args ...object.Value) object.Value {
		return object.NewNumber(float64(float32(numArg(args, 0))))
	}))

	// f16round(x): 舍入到最接近的 16 位半精度浮点数 (ES2024 Float16Array 家族)。
	// 半精度: 1 符号 + 5 指数 (偏移 15) + 10 尾数; 溢出 → ±Infinity,
	// 指数下溢 → 半精度次正规 (精度 11 位)。舍入为就近偶 (round-half-even)。
	m.SetBuiltinProperty("f16round", object.NewBuiltin("f16round", func(args ...object.Value) object.Value {
		return object.NewNumber(f16Round(numArg(args, 0)))
	}))

	// imul(a, b): 按 C 语言 32 位整数乘法语义计算 (溢出自动回绕)
	m.SetBuiltinProperty("imul", object.NewBuiltin("imul", func(args ...object.Value) object.Value {
		a := int32(numArgInt(args, 0))
		b := int32(numArgInt(args, 1))
		return object.NewNumber(float64(a * b))
	}))

	// clz32(x): 返回 32 位无符号表示中前导零的个数
	m.SetBuiltinProperty("clz32", object.NewBuiltin("clz32", func(args ...object.Value) object.Value {
		x := uint32(numArgInt(args, 0))
		if x == 0 {
			return object.NewNumber(32)
		}
		return object.NewNumber(float64(bits.LeadingZeros32(x)))
	}))

	// ES6 常量
	m.SetBuiltinConstProperty("EPSILON", object.NewNumber(2.220446049250313e-16))

	return m
}

// f16Round 把 float64 就近 (round-half-even) 舍入到 IEEE 754 binary16
// 可精确表示的值 (rFXXbq batch25: Math.f16round, ES2024)。
// 直接基于 float64 位型运算 —— 经 float32 中转会丢失双精度细粒度
// (test262: 2.980232238769532e-8 是"最小不归零"的双, float32 化后已抹平)。
func f16Round(x float64) float64 {
	if math.IsNaN(x) {
		return math.NaN()
	}
	b := math.Float64bits(x)
	sign := uint16(b >> 63) << 15
	e := int((b >> 52) & 0x7FF)
	m := b & ((1 << 52) - 1)
	if e == 0x7FF {
		// ±Infinity → ±Infinity
		return math.Float64frombits(uint64(sign>>15)<<63 | 0x7FF0000000000000)
	}
	exp16 := e - 1023 + 15
	var half uint32
	switch {
	case e == 0:
		// float64 次正规 (< 2^-1022): 远小于 binary16 最小次正规 → ±0
		half = 0
	case exp16 >= 31:
		// ≥ 2^16: 溢出 → ±Infinity
		half = 0x7C00
	case exp16 > 0:
		// 正规数: 52 位尾数取高 10 位, 低 42 位参与就近偶舍入
		mant := m >> 42
		rem := m & ((1 << 42) - 1)
		if rem > 1<<41 || (rem == 1<<41 && mant&1 == 1) {
			mant++
		}
		if mant >= 0x400 {
			mant = 0
			exp16++
			if exp16 >= 31 {
				return math.Float64frombits(uint64(sign>>15)<<63 | 0x7FF0000000000000)
			}
		}
		half = uint32(exp16)<<10 | uint32(mant)
	default:
		// binary16 次正规: M = 2^52+m 的定点右移, shift = 1051 - e
		shift := uint(1051 - e)
		if shift >= 54 {
			half = 0
		} else {
			M := (uint64(1) << 52) | m
			rounded := M >> shift
			rem := M & ((1 << shift) - 1)
			halfp := uint64(1) << (shift - 1)
			if rem > halfp || (rem == halfp && rounded&1 == 1) {
				rounded++
			}
			if rounded >= 0x400 {
				half = 1 << 10 // 进位为最小正规数
			} else {
				half = uint32(rounded)
			}
		}
	}
	return f16BitsToF64(sign | uint16(half))
}

// f16BitsToF64 把 binary16 位型精确展开为 float64。
func f16BitsToF64(h uint16) float64 {
	sign := uint64(h&0x8000) << 48
	exp := (h >> 10) & 0x1F
	frac := uint64(h & 0x3FF)
	switch exp {
	case 0:
		if frac == 0 {
			return math.Float64frombits(sign)
		}
		// 次正规: frac × 2^-24
		v := float64(frac) * math.Pow(2, -24)
		if sign != 0 {
			return -v
		}
		return v
	case 0x1F:
		if frac == 0 {
			return math.Float64frombits(sign | 0x7FF0000000000000)
		}
		return math.Float64frombits(sign | 0x7FF8000000000000)
	}
	e := uint64(exp-15+1023) << 52
	m := frac << 42
	return math.Float64frombits(sign | e | m)
}

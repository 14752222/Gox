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

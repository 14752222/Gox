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
	m.SetProperty("PI", object.NewNumber(math.Pi))
	m.SetProperty("E", object.NewNumber(math.E))
	m.SetProperty("LN2", object.NewNumber(math.Ln2))
	m.SetProperty("LN10", object.NewNumber(math.Log(10)))
	m.SetProperty("LOG2E", object.NewNumber(math.Log2E))
	m.SetProperty("LOG10E", object.NewNumber(math.Log10E))
	m.SetProperty("SQRT2", object.NewNumber(math.Sqrt2))
	m.SetProperty("SQRT1_2", object.NewNumber(1.0/math.Sqrt2))

	// 取一个或两个数字参数的辅助函数
	numArg := func(args []object.Value, idx int) float64 {
		if idx >= len(args) {
			return math.NaN()
		}
		return toFloat(args[idx])
	}

	// 数学函数
	m.SetProperty("abs", object.NewBuiltin("abs", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Abs(numArg(args, 0)))
	}))

	m.SetProperty("ceil", object.NewBuiltin("ceil", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Ceil(numArg(args, 0)))
	}))

	m.SetProperty("floor", object.NewBuiltin("floor", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Floor(numArg(args, 0)))
	}))

	m.SetProperty("round", object.NewBuiltin("round", func(args ...object.Value) object.Value {
		v := numArg(args, 0)
		// JavaScript Math.round: 向上取整 (0.5 → 1, -0.5 → 0, -1.5 → -1)
		return object.NewNumber(math.Floor(v + 0.5))
	}))

	m.SetProperty("trunc", object.NewBuiltin("trunc", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Trunc(numArg(args, 0)))
	}))

	m.SetProperty("sqrt", object.NewBuiltin("sqrt", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Sqrt(numArg(args, 0)))
	}))

	m.SetProperty("cbrt", object.NewBuiltin("cbrt", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Cbrt(numArg(args, 0)))
	}))

	m.SetProperty("pow", object.NewBuiltin("pow", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Pow(numArg(args, 0), numArg(args, 1)))
	}))

	m.SetProperty("exp", object.NewBuiltin("exp", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Exp(numArg(args, 0)))
	}))

	m.SetProperty("log", object.NewBuiltin("log", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Log(numArg(args, 0)))
	}))

	m.SetProperty("log2", object.NewBuiltin("log2", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Log2(numArg(args, 0)))
	}))

	m.SetProperty("log10", object.NewBuiltin("log10", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Log10(numArg(args, 0)))
	}))

	m.SetProperty("max", object.NewBuiltin("max", func(args ...object.Value) object.Value {
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

	m.SetProperty("min", object.NewBuiltin("min", func(args ...object.Value) object.Value {
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

	m.SetProperty("hypot", object.NewBuiltin("hypot", func(args ...object.Value) object.Value {
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

	m.SetProperty("random", object.NewBuiltin("random", func(args ...object.Value) object.Value {
		return object.NewNumber(rand.Float64())
	}))

	m.SetProperty("sign", object.NewBuiltin("sign", func(args ...object.Value) object.Value {
		v := numArg(args, 0)
		if v > 0 {
			return object.NewNumber(1)
		}
		if v < 0 {
			return object.NewNumber(-1)
		}
		return object.NewNumber(0)
	}))

	m.SetProperty("sin", object.NewBuiltin("sin", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Sin(numArg(args, 0)))
	}))

	m.SetProperty("cos", object.NewBuiltin("cos", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Cos(numArg(args, 0)))
	}))

	m.SetProperty("tan", object.NewBuiltin("tan", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Tan(numArg(args, 0)))
	}))

	m.SetProperty("asin", object.NewBuiltin("asin", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Asin(numArg(args, 0)))
	}))

	m.SetProperty("acos", object.NewBuiltin("acos", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Acos(numArg(args, 0)))
	}))

	m.SetProperty("atan", object.NewBuiltin("atan", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Atan(numArg(args, 0)))
	}))

	m.SetProperty("atan2", object.NewBuiltin("atan2", func(args ...object.Value) object.Value {
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

	m.SetProperty("sinh", object.NewBuiltin("sinh", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Sinh(numArg(args, 0)))
	}))
	m.SetProperty("cosh", object.NewBuiltin("cosh", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Cosh(numArg(args, 0)))
	}))
	m.SetProperty("tanh", object.NewBuiltin("tanh", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Tanh(numArg(args, 0)))
	}))
	m.SetProperty("asinh", object.NewBuiltin("asinh", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Asinh(numArg(args, 0)))
	}))
	m.SetProperty("acosh", object.NewBuiltin("acosh", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Acosh(numArg(args, 0)))
	}))
	m.SetProperty("atanh", object.NewBuiltin("atanh", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Atanh(numArg(args, 0)))
	}))
	m.SetProperty("expm1", object.NewBuiltin("expm1", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Expm1(numArg(args, 0)))
	}))
	m.SetProperty("log1p", object.NewBuiltin("log1p", func(args ...object.Value) object.Value {
		return object.NewNumber(math.Log1p(numArg(args, 0)))
	}))

	// fround(x): 舍入到最接近的 32 位单精度浮点数
	m.SetProperty("fround", object.NewBuiltin("fround", func(args ...object.Value) object.Value {
		return object.NewNumber(float64(float32(numArg(args, 0))))
	}))

	// imul(a, b): 按 C 语言 32 位整数乘法语义计算 (溢出自动回绕)
	m.SetProperty("imul", object.NewBuiltin("imul", func(args ...object.Value) object.Value {
		a := int32(numArgInt(args, 0))
		b := int32(numArgInt(args, 1))
		return object.NewNumber(float64(a * b))
	}))

	// clz32(x): 返回 32 位无符号表示中前导零的个数
	m.SetProperty("clz32", object.NewBuiltin("clz32", func(args ...object.Value) object.Value {
		x := uint32(numArgInt(args, 0))
		if x == 0 {
			return object.NewNumber(32)
		}
		return object.NewNumber(float64(bits.LeadingZeros32(x)))
	}))

	// ES6 常量
	m.SetProperty("EPSILON", object.NewNumber(2.220446049250313e-16))

	return m
}

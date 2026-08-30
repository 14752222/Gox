package stdlib

import (
	"math"

	"js-runtime/object"
	"js-runtime/runtime"
)

// setupStats 注册全局 stats 对象 (教程示例 API，用于演示数据转换)。
// stats.sum(arr)      — 求数组元素之和 (JS 数组 → Go float64)
// stats.describe(arr) — 返回 {count, min, max, mean} (Go 计算结果 → JS 对象)
func setupStats(env *runtime.Environment) {
	stats := object.NewObject()

	// toArrayArg 是参数解包的辅助函数: 取第 idx 个参数并断言它是数组。
	// 返回 (数组元素, 是否成功)。失败时返回的 Error 会被 VM 转换为异常。
	toArrayArg := func(args []object.Value, idx int, apiName string) (*object.Array, object.Value) {
		if idx >= len(args) {
			return nil, object.NewTypeError("%s: missing argument %d (expected an array)", apiName, idx+1)
		}
		arr, ok := args[idx].(*object.Array)
		if !ok {
			return nil, object.NewTypeError("%s: argument %d must be an array, got %s",
				apiName, idx+1, object.TypeOf(args[idx]))
		}
		return arr, nil
	}

	stats.SetProperty("sum", object.NewBuiltin("sum", func(args ...object.Value) object.Value {
		arr, errVal := toArrayArg(args, 0, "stats.sum")
		if errVal != nil {
			return errVal
		}
		total := 0.0
		for _, el := range arr.Elements {
			total += toFloat(el)
		}
		return object.NewNumber(total)
	}))

	stats.SetProperty("describe", object.NewBuiltin("describe", func(args ...object.Value) object.Value {
		arr, errVal := toArrayArg(args, 0, "stats.describe")
		if errVal != nil {
			return errVal
		}
		if len(arr.Elements) == 0 {
			return object.NewRangeError("stats.describe: cannot describe an empty array")
		}

		count := 0
		min := math.Inf(1)
		max := math.Inf(-1)
		total := 0.0
		for _, el := range arr.Elements {
			v := toFloat(el)
			if math.IsNaN(v) {
				return object.NewRangeError("stats.describe: array contains NaN at index %d", count)
			}
			count++
			total += v
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}

		// Go 计算结果 → JS 对象: 逐个属性 SetProperty 封装
		result := object.NewObject()
		result.SetProperty("count", object.NewNumber(float64(count)))
		result.SetProperty("min", object.NewNumber(min))
		result.SetProperty("max", object.NewNumber(max))
		result.SetProperty("mean", object.NewNumber(total/float64(count)))
		return result
	}))

	env.Declare("stats", stats, false)
}

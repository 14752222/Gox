package stdlib

import (
	"fmt"
	"math"

	"js-runtime/object"
)

// toFloat 将任意 object.Value 转换为 float64。
func toFloat(v object.Value) float64 {
	switch val := v.(type) {
	case *object.Number:
		return val.Value
	case *object.Boolean:
		if val.Value {
			return 1
		}
		return 0
	case *object.Null:
		return 0
	case *object.Undefined:
		return math.NaN()
	case *object.String:
		if val.Value == "" {
			return 0
		}
		var f float64
		_, err := fmt.Sscanf(val.Value, "%f", &f)
		if err != nil {
			return math.NaN()
		}
		return f
	}
	return math.NaN()
}

// toInt 将任意 object.Value 转换为 int64。
func toInt(v object.Value) int64 {
	return int64(toFloat(v))
}

// toStr 将任意 object.Value 转换为 string。
func toStr(v object.Value) string {
	if v == nil {
		return "undefined"
	}
	return v.Inspect()
}

// isFalsy 判断值是否为假值。
func isFalsy(v object.Value) bool {
	return object.IsFalsy(v)
}

// toBool 将任意 object.Value 转换为 bool。
func toBool(v object.Value) bool {
	return !isFalsy(v)
}

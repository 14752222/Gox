package stdlib

import (
	"math"
	"strconv"

	"js-runtime/object"
)

// setupNumberProto 创建 Number.prototype 对象。
func setupNumberProto() *object.Object {
	p := object.NewObject()

	// toFixed(digits): 返回固定小数位数的字符串表示
	p.SetProperty("toFixed", object.NewBuiltinMethod("toFixed", func(this object.Value, args ...object.Value) object.Value {
		num, ok := this.(*object.Number)
		if !ok {
			return thisTypeError("Number", "toFixed", this)
		}
		if isNaN(num.Value) {
			return object.NewString("NaN")
		}
		if math.IsInf(num.Value, 0) {
			if num.Value > 0 {
				return object.NewString("Infinity")
			}
			return object.NewString("-Infinity")
		}
		digits := 0
		if len(args) > 0 {
			digits = int(toInt(args[0]))
		}
		if digits < 0 || digits > 100 {
			return object.NewRangeError("toFixed() digits argument must be between 0 and 100")
		}
		return object.NewString(strconv.FormatFloat(num.Value, 'f', digits, 64))
	}))

	// toPrecision(precision): 返回指定精度的字符串表示
	p.SetProperty("toPrecision", object.NewBuiltinMethod("toPrecision", func(this object.Value, args ...object.Value) object.Value {
		num, ok := this.(*object.Number)
		if !ok {
			return thisTypeError("Number", "toPrecision", this)
		}
		if isNaN(num.Value) {
			return object.NewString("NaN")
		}
		if math.IsInf(num.Value, 0) {
			if num.Value > 0 {
				return object.NewString("Infinity")
			}
			return object.NewString("-Infinity")
		}
		if len(args) == 0 {
			return object.NewString(num.Inspect())
		}
		precision := int(toInt(args[0]))
		if precision < 1 || precision > 100 {
			return object.NewRangeError("toPrecision() precision argument must be between 1 and 100")
		}
		return object.NewString(strconv.FormatFloat(num.Value, 'g', precision, 64))
	}))

	// toString(radix): 将数字转换为指定进制的字符串
	p.SetProperty("toString", object.NewBuiltinMethod("toString", func(this object.Value, args ...object.Value) object.Value {
		num, ok := this.(*object.Number)
		if !ok {
			return thisTypeError("Number", "toString", this)
		}
		radix := 10
		if len(args) > 0 {
			radix = int(toInt(args[0]))
		}
		if radix < 2 || radix > 36 {
			return object.NewRangeError("toString() radix must be between 2 and 36")
		}
		if radix == 10 {
			return object.NewString(num.Inspect())
		}
		if isNaN(num.Value) {
			return object.NewString("NaN")
		}
		if math.IsInf(num.Value, 0) {
			if num.Value > 0 {
				return object.NewString("Infinity")
			}
			return object.NewString("-Infinity")
		}
		// 整数部分转换
		negative := num.Value < 0
		absVal := math.Abs(num.Value)
		intPart := int64(absVal)
		fracPart := absVal - float64(intPart)

		intStr := strconv.FormatInt(intPart, radix)

		var result string
		if negative {
			result = "-" + intStr
		} else {
			result = intStr
		}

		// 小数部分转换
		if fracPart > 0 {
			result += "."
			for i := 0; i < 16 && fracPart > 0; i++ {
				fracPart *= float64(radix)
				digit := int(fracPart)
				if digit >= radix {
					digit = radix - 1
				}
				result += strconv.FormatInt(int64(digit), radix)
				fracPart -= float64(digit)
			}
		}

		return object.NewString(result)
	}))

	// toExponential(fractionDigits): 返回指数表示法字符串
	p.SetProperty("toExponential", object.NewBuiltinMethod("toExponential", func(this object.Value, args ...object.Value) object.Value {
		num, ok := this.(*object.Number)
		if !ok {
			return thisTypeError("Number", "toExponential", this)
		}
		if isNaN(num.Value) {
			return object.NewString("NaN")
		}
		if math.IsInf(num.Value, 0) {
			if num.Value > 0 {
				return object.NewString("Infinity")
			}
			return object.NewString("-Infinity")
		}
		if len(args) > 0 && !isUndefinedValue(args[0]) {
			digits := int(toInt(args[0]))
			// 规范: fractionDigits 必须在 [0, 100] 内，否则抛 RangeError
			if digits < 0 || digits > 100 {
				return object.NewRangeError("toExponential() fractionDigits argument must be between 0 and 100")
			}
			return object.NewString(strconv.FormatFloat(num.Value, 'e', digits, 64))
		}
		return object.NewString(strconv.FormatFloat(num.Value, 'e', -1, 64))
	}))

	// valueOf(): 返回原始数字值
	p.SetProperty("valueOf", object.NewBuiltinMethod("valueOf", func(this object.Value, args ...object.Value) object.Value {
		if num, ok := this.(*object.Number); ok {
			return num
		}
		return object.NewNumber(0)
	}))

	return p
}

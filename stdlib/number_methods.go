package stdlib

import (
	"math"
	"math/big"
	"strconv"

	"github.com/14752222/Gox/object"
)

// setupNumberProto 创建 Number.prototype 对象。
func setupNumberProto() *object.Object {
	p := object.NewObject()

	// thisNumber 取 toString/toFixed/... 的 this 数字值。
	// 规范: Number.prototype 自身就是 [[NumberData]] 为 0 的 Number 对象,
	// 因此 `Number.prototype.toString(2)` / `Number.prototype.toFixed.call(
	// Number.prototype)` 合法且等价于 0 (test262 S15.7.4.2_A2 全族依赖)。
	// 其余非 Number 接收器照旧 TypeError。
	thisNumber := func(method string, this object.Value) (*object.Number, object.Value) {
		if num, ok := object.UnwrapPrimitive(this).(*object.Number); ok {
			return num, nil
		}
		if this == object.Value(p) {
			return object.NewNumber(0), nil
		}
		return nil, thisTypeError("Number", method, this)
	}

	// toFixed(digits): 返回固定小数位数的字符串表示
	p.SetBuiltinProperty("toFixed", object.NewBuiltinMethod("toFixed", func(this object.Value, args ...object.Value) object.Value {
		num, errv := thisNumber("toFixed", this)
		if errv != nil {
			return errv
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
		return object.NewString(strconv.FormatFloat(num.Value, 'f', int(digits), 64))
	}))

	// toPrecision(precision): 返回指定精度的字符串表示
	p.SetBuiltinProperty("toPrecision", object.NewBuiltinMethod("toPrecision", func(this object.Value, args ...object.Value) object.Value {
		num, errv := thisNumber("toPrecision", this)
		if errv != nil {
			return errv
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
		precision, errv := toIntE(args[0])
			if errv != nil {
				return errv
			}
		if precision < 1 || precision > 100 {
			return object.NewRangeError("toPrecision() precision argument must be between 1 and 100")
		}
		return object.NewString(strconv.FormatFloat(num.Value, 'g', int(precision), 64))
	}))

	// toString(radix): 将数字转换为指定进制的字符串
	p.SetBuiltinProperty("toString", object.NewBuiltinMethod("toString", func(this object.Value, args ...object.Value) object.Value {
		num, errv := thisNumber("toString", this)
		if errv != nil {
			return errv
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
		return object.NewString(formatFloatRadix(num.Value, radix))
	}))

	// toLocaleString(): 本运行时无 Intl, 规范 21.1.3.4 的回退即
	// ToString(thisNumber) —— 但 this 必须是 Number (校验同 toString)。
	p.SetBuiltinProperty("toLocaleString", object.NewBuiltinMethod("toLocaleString", func(this object.Value, args ...object.Value) object.Value {
		num, errv := thisNumber("toLocaleString", this)
		if errv != nil {
			return errv
		}
		return object.NewString(num.Inspect())
	}))

	// toExponential(fractionDigits): 返回指数表示法字符串
	p.SetBuiltinProperty("toExponential", object.NewBuiltinMethod("toExponential", func(this object.Value, args ...object.Value) object.Value {
		num, errv := thisNumber("toExponential", this)
		if errv != nil {
			return errv
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
			digits, errv := toIntE(args[0])
			if errv != nil {
				return errv
			}
			// 规范: fractionDigits 必须在 [0, 100] 内，否则抛 RangeError
			if digits < 0 || digits > 100 {
				return object.NewRangeError("toExponential() fractionDigits argument must be between 0 and 100")
			}
			return object.NewString(strconv.FormatFloat(num.Value, 'e', int(digits), 64))
		}
		return object.NewString(strconv.FormatFloat(num.Value, 'e', -1, 64))
	}))

	// valueOf(): 返回原始数字值 (规范: 非 Number 接收器抛 TypeError;
	// Number.prototype 自身即 Number 0 对象, 合法)
	p.SetBuiltinProperty("valueOf", object.NewBuiltinMethod("valueOf", func(this object.Value, args ...object.Value) object.Value {
		num, errv := thisNumber("valueOf", this)
		if errv != nil {
			return errv
		}
		return num
	}))

	return p
}


// formatFloatRadix 按指定进制 (2..36) 格式化有限数字。
// 与 V8 一致输出**最短往返**表示: 从 0 位小数起逐位增加, 取该位数下
// 四舍五入的近似串, 首个解析回原 float64 位型相同的位数即止
// (rFXXbq batch25 —— 旧实现 16 位截断, (0.1).toString(2) 错误)。
func formatFloatRadix(v float64, radix int) string {
	neg := math.Signbit(v) && v != 0
	abs := math.Abs(v)

	r := new(big.Rat).SetFloat64(abs)
	ip := new(big.Int).Quo(r.Num(), r.Denom()) // 向零截断
	frac := new(big.Rat).Sub(r, new(big.Rat).SetInt(ip))
	if frac.Num().Sign() == 0 {
		s := ip.Text(radix)
		if neg {
			s = "-" + s
		}
		return s
	}

	br := big.NewInt(int64(radix))
	// 整数部分进位上限: 整数 +1 时 ip 需 +1
	curIP := new(big.Int).Set(ip)
	const maxDigits = 1200
	d := 0
	for ; d <= maxDigits; d++ {
		// frac * radix^d 四舍五入到整数
		scaled := new(big.Rat).Mul(frac, new(big.Rat).SetInt(new(big.Int).Exp(br, big.NewInt(int64(d)), nil)))
		q, rem := new(big.Int).QuoRem(scaled.Num(), scaled.Denom(), new(big.Int))
		if rem.CmpAbs(big.NewInt(0)) != 0 {
			twice := new(big.Int).Lsh(rem.Abs(rem), 1)
			if twice.CmpAbs(scaled.Denom()) >= 0 {
				q.Add(q, big.NewInt(1))
			}
		}
		// 近似值 = curIP + q/radix^d, 若 q == radix^d 需进位
		limit := new(big.Int).Exp(br, big.NewInt(int64(d)), nil)
		carry := big.NewInt(0)
		if q.Cmp(limit) >= 0 {
			carry.Set(q.Div(q, limit)) // q/limit (恰为 1)
			q.Mod(q, limit)
		}
		cand := new(big.Rat).Add(
			new(big.Rat).SetInt(new(big.Int).Add(curIP, carry)),
			new(big.Rat).SetFrac(q, limit),
		)
		f, _ := cand.Float64()
		if math.Float64bits(f) == math.Float64bits(abs) {
			// 命中: 用进位后的整数部分输出
			ipStr := new(big.Int).Add(ip, carry).Text(radix)
			if d == 0 {
				if neg {
					return "-" + ipStr
				}
				return ipStr
			}
			// q 转成定长 d 位进制串 (前导零)
			ds := q.Text(radix)
			for len(ds) < d {
				ds = "0" + ds
			}
			out := ipStr + "." + ds
			if neg {
				out = "-" + out
			}
			return out
		}
		if d == maxDigits {
			break
		}
	}
	// 兜底 (理论不可达): 输出全精度展开
	scaled := new(big.Rat).Mul(frac, new(big.Rat).SetInt(new(big.Int).Exp(br, big.NewInt(maxDigits), nil)))
	q := new(big.Int).Quo(scaled.Num(), scaled.Denom())
	ds := q.Text(radix)
	for len(ds) < maxDigits {
		ds = "0" + ds
	}
	out := ip.Text(radix) + "." + ds
	if neg {
		out = "-" + out
	}
	return out
}

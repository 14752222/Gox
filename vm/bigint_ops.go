package vm

import (
	"math"
	"math/big"

	"js-runtime/bytecode"
	"js-runtime/object"
)

// 本文件实现 BigInt 参与的运算符语义。
//
// 之所以独立成文件: BigInt 与 Number 在 ECMAScript 中是两套互不相通的
// 数值体系，几乎所有二元/一元运算符都要先做一次"是否涉及 BigInt"的分支，
// 把这些分支塞进 vm.go 的主循环会让算术那一段难以阅读。
//
// 核心规则 (ECMAScript BigInt 语义):
//   - 同类型运算: BigInt op BigInt → BigInt
//   - 混合运算: BigInt op Number → TypeError (不允许隐式转换)
//   - 比较运算: BigInt 与 Number 允许比较 (数学值比较，不抛错)
//   - >>> (无符号右移) 与一元 + 对 BigInt 无定义 → TypeError
//   - / 是截断除法，除零抛 RangeError (不是 Infinity，BigInt 无 Infinity)

// jsThrow 描述一个待抛出的 JS 异常。
//
// VM 主循环中大量错误原本以 fmt.Errorf("TypeError: ...") 的形式直接返回，
// 那样会绕过 try/catch 的 handleThrow 流程，导致 `try { 1n + 1 } catch (e)`
// 捕获不到异常、直接终止脚本。这里用结构化类型承载错误名与消息，
// 由 throwJSError 统一转成 *object.Error 并走正常的抛出路径。
type jsThrow struct {
	Name    string
	Message string
}

func (e *jsThrow) Error() string { return e.Name + ": " + e.Message }

// errMixBigInt 是 BigInt 与 Number 混合算术的统一错误。
var errMixBigInt = &jsThrow{"TypeError", "Cannot mix BigInt with other types, use explicit conversions"}

// binaryArithmetic 处理所有二元算术/位运算符。
// 非 BigInt 路径保持原有的 double 语义不变。
func (vm *VM) binaryArithmetic(op bytecode.Opcode, a, b object.Value) (object.Value, error) {
	aBig, aIsBig := a.(*object.BigInt)
	bBig, bIsBig := b.(*object.BigInt)

	switch {
	case aIsBig && bIsBig:
		return bigIntBinary(op, aBig, bBig)
	case aIsBig || bIsBig:
		return nil, errMixBigInt
	}

	bn := toNumber(b)
	an := toNumber(a)
	switch op {
	case bytecode.OP_SUB:
		return object.NewNumber(an - bn), nil
	case bytecode.OP_MUL:
		return object.NewNumber(an * bn), nil
	case bytecode.OP_DIV:
		if bn == 0 {
			switch {
			case an == 0:
				return object.NewNumber(math.NaN()), nil
			case an > 0:
				return object.NewNumber(math.Inf(1)), nil
			default:
				return object.NewNumber(math.Inf(-1)), nil
			}
		}
		return object.NewNumber(an / bn), nil
	case bytecode.OP_MOD:
		if bn == 0 {
			return object.NewNumber(math.NaN()), nil
		}
		return object.NewNumber(math.Mod(an, bn)), nil
	case bytecode.OP_POW:
		return object.NewNumber(math.Pow(an, bn)), nil
	case bytecode.OP_BIT_AND:
		return object.NewNumber(float64(toInt32JS(an) & toInt32JS(bn))), nil
	case bytecode.OP_BIT_OR:
		return object.NewNumber(float64(toInt32JS(an) | toInt32JS(bn))), nil
	case bytecode.OP_BIT_XOR:
		return object.NewNumber(float64(toInt32JS(an) ^ toInt32JS(bn))), nil
	case bytecode.OP_SHL:
		// JS 的移位量取 ToUint32(count) & 31，结果按 32 位有符号回绕
		return object.NewNumber(float64(toInt32JS(an) << (toUint32JS(bn) & 31))), nil
	case bytecode.OP_SHR:
		// 算术右移: 按 32 位有符号
		return object.NewNumber(float64(toInt32JS(an) >> (toUint32JS(bn) & 31))), nil
	case bytecode.OP_USHR:
		// 无符号右移: 左操作数按 32 位无符号，结果范围 0..2^32-1
		return object.NewNumber(float64(toUint32JS(an) >> (toUint32JS(bn) & 31))), nil
	}
	return object.UndefinedSingleton, nil
}

// toUint32JS 实现 ECMAScript ToUint32: 截断小数后对 2^32 取模。
// NaN/±Infinity 归零 (超出 float64 可表示范围时 Go 的转换是未定义的，必须先取模)。
func toUint32JS(f float64) uint32 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	m := math.Mod(math.Trunc(f), 4294967296)
	if m < 0 {
		m += 4294967296
	}
	return uint32(m)
}

// toInt32JS 实现 ECMAScript ToInt32: ToUint32 的结果按有符号 32 位解释。
func toInt32JS(f float64) int32 {
	return int32(toUint32JS(f))
}

// bigIntBinary 两个操作数均为 BigInt 时的运算。
func bigIntBinary(op bytecode.Opcode, a, b *object.BigInt) (object.Value, error) {
	switch op {
	case bytecode.OP_SUB:
		return a.Sub(b), nil
	case bytecode.OP_MUL:
		return a.Mul(b), nil
	case bytecode.OP_DIV:
		if b.Value.Sign() == 0 {
			return nil, &jsThrow{"RangeError", "Division by zero"}
		}
		return a.Div(b), nil
	case bytecode.OP_MOD:
		if b.Value.Sign() == 0 {
			return nil, &jsThrow{"RangeError", "Division by zero"}
		}
		return a.Rem(b), nil
	case bytecode.OP_POW:
		if b.Value.Sign() < 0 {
			return nil, &jsThrow{"RangeError", "Exponent must be positive"}
		}
		return a.Pow(b), nil
	case bytecode.OP_BIT_AND:
		return a.BitAnd(b), nil
	case bytecode.OP_BIT_OR:
		return a.BitOr(b), nil
	case bytecode.OP_BIT_XOR:
		return a.BitXor(b), nil
	case bytecode.OP_SHL:
		return a.Shl(b), nil
	case bytecode.OP_SHR:
		return a.Shr(b), nil
	case bytecode.OP_USHR:
		// BigInt 是任意精度有符号整数，没有"无符号"这一位模式概念。
		return nil, &jsThrow{"TypeError", "BigInt does not support the unsigned right shift operator (>>>)"}
	}
	return object.UndefinedSingleton, nil
}

// unaryArithmetic 处理一元 - 与 ~。
func (vm *VM) unaryArithmetic(op bytecode.Opcode, a object.Value) (object.Value, error) {
	if b, ok := a.(*object.BigInt); ok {
		switch op {
		case bytecode.OP_NEG:
			return b.Neg(), nil
		case bytecode.OP_BIT_NOT:
			return b.BitNot(), nil
		}
	}
	switch op {
	case bytecode.OP_NEG:
		return object.NewNumber(-toNumber(a)), nil
	case bytecode.OP_BIT_NOT:
		// ~x === -x-1，结果按 32 位有符号: ~5 === -6
		return object.NewNumber(float64(^toInt32JS(toNumber(a)))), nil
	}
	return object.UndefinedSingleton, nil
}

// toNumberValue 实现一元 + (ToNumber)。
//
// BigInt 不参与隐式数值转换，一元 + 对其抛 TypeError。
func toNumberValue(v object.Value) (object.Value, error) {
	if _, ok := v.(*object.BigInt); ok {
		return nil, &jsThrow{"TypeError", "Cannot convert a BigInt value to a number"}
	}
	return object.NewNumber(toNumber(v)), nil
}

// relationalCompare 实现 < > <= >=。
//
// 与算术运算不同，关系比较允许 BigInt 与 Number 混合: 规范先做
// ToPrimitive，再在"同为 BigInt"和"其余"两种情形下分别比较数学值。
func relationalCompare(op bytecode.Opcode, a, b object.Value) (bool, error) {
	aBig, aIsBig := a.(*object.BigInt)
	bBig, bIsBig := b.(*object.BigInt)

	cmp := 0
	switch {
	case aIsBig && bIsBig:
		cmp = aBig.Value.Cmp(bBig.Value)
	case aIsBig, bIsBig:
		// 只有一方是 BigInt: 用有理数精确比较，避免 float64 丢精度。
		// NaN 参与比较时结果为 false (对应 ECMAScript 的 undefined → false)。
		if aIsBig {
			c, ok := compareBigIntWithValue(aBig, b)
			if !ok {
				return false, nil
			}
			cmp = c
		} else {
			c, ok := compareBigIntWithValue(bBig, a)
			if !ok {
				return false, nil
			}
			cmp = -c
		}
	default:
		bn := toNumber(b)
		an := toNumber(a)
		switch op {
		case bytecode.OP_LT:
			return an < bn, nil
		case bytecode.OP_GT:
			return an > bn, nil
		case bytecode.OP_LTE:
			return an <= bn, nil
		default:
			return an >= bn, nil
		}
	}

	switch op {
	case bytecode.OP_LT:
		return cmp < 0, nil
	case bytecode.OP_GT:
		return cmp > 0, nil
	case bytecode.OP_LTE:
		return cmp <= 0, nil
	default:
		return cmp >= 0, nil
	}
}

// compareBigIntWithValue 比较 BigInt 与任意值，返回 (比较结果, 可比较)。
// 不可比较 (NaN) 时返回 false，调用方按 ECMAScript 语义得到 false。
func compareBigIntWithValue(bi *object.BigInt, v object.Value) (int, bool) {
	f := toNumber(v)
	switch {
	case math.IsNaN(f):
		return 0, false
	case math.IsInf(f, 1):
		return -1, true
	case math.IsInf(f, -1):
		return 1, true
	}
	// 用有理数承载 float64，保证与整数边界值 (如 2^53+1) 的比较不失真。
	return new(big.Rat).SetInt(bi.Value).Cmp(new(big.Rat).SetFloat64(f)), true
}

// bigIntStrictEqual 实现 === 的 BigInt 分支: 数学值相等即相等 (不比类型外的任何东西)。
func bigIntStrictEqual(a, b *object.BigInt) bool { return a.Value.Cmp(b.Value) == 0 }

// bigIntLooseEqual 实现 == 的 BigInt 分支。
//
// 规范允许 BigInt 与 Number/String/Boolean 宽松相等: 双方都按数学值比较，
// 但 Number 侧必须是整数值 (1n == 1.5 为 false)。
func bigIntLooseEqual(a, b object.Value) bool {
	aBig, aIs := a.(*object.BigInt)
	bBig, bIs := b.(*object.BigInt)
	switch {
	case aIs && bIs:
		return aBig.Value.Cmp(bBig.Value) == 0
	case aIs:
		return bigIntEqualsValue(aBig, b)
	case bIs:
		return bigIntEqualsValue(bBig, a)
	}
	return false
}

// bigIntEqualsValue 判断 BigInt 与非 BigInt 值的宽松相等。
func bigIntEqualsValue(bi *object.BigInt, v object.Value) bool {
	if s, ok := v.(*object.String); ok {
		// "1" == 1n → true；非整数字符串 ("1.5") 经 ToNumber 后不等。
		return bigIntEqualsFloat(bi, object.ParseJSNumber(s.Value))
	}
	return bigIntEqualsFloat(bi, toNumber(v))
}

// bigIntEqualsFloat 比较 BigInt 与 float64 的数学值。
func bigIntEqualsFloat(bi *object.BigInt, f float64) bool {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return false
	}
	// 非整数的 Number 与任何 BigInt 都不宽松相等。
	if f != math.Trunc(f) {
		return false
	}
	return new(big.Rat).SetInt(bi.Value).Cmp(new(big.Rat).SetFloat64(f)) == 0
}

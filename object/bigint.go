package object

import (
	"math/big"
	"strings"
)

// BigInt 表示 JavaScript 的 BigInt 类型 (任意精度整数)。
//
// 引入 BigInt 的直接动因是 Temporal: Instant.prototype.epochNanoseconds
// 的规范类型是 BigInt，纳秒 epoch 约 1.7e18，远超 IEEE 754 double 的安全
// 整数范围 2^53，用 Number 表示会静默丢精度。
//
// 与 Number 的关键语义差异 (由 VM 保证，不在本类型内):
//   - BigInt 与 Number 不能混合算术 (+ - * / % **)，需抛 TypeError
//   - >>> 对 BigInt 无定义 (无符号右移)，需抛 TypeError
//   - 一元 + 对 BigInt 无定义，需抛 TypeError
//   - / 是截断除法 (向零取整)，% 是截断取余 (符号跟随被除数)
type BigInt struct {
	Value *big.Int
}

// BigIntProto 是所有 BigInt 实例的原型对象，由 stdlib 初始化时设置。
var BigIntProto Value

// SetBigIntProto 设置全局 BigInt 原型 (由 stdlib 调用)。
func SetBigIntProto(p Value) { BigIntProto = p }

// GetBigIntProto 返回全局 BigInt 原型。
func GetBigIntProto() Value { return BigIntProto }

// NewBigInt 基于 *big.Int 创建 BigInt。nil 时按 0n 处理。
func NewBigInt(v *big.Int) *BigInt {
	if v == nil {
		v = new(big.Int)
	}
	return &BigInt{Value: v}
}

// NewBigIntInt64 从 int64 创建 BigInt。
func NewBigIntInt64(v int64) *BigInt { return &BigInt{Value: big.NewInt(v)} }

// NewBigIntUint64 从 uint64 创建 BigInt。
func NewBigIntUint64(v uint64) *BigInt { return &BigInt{Value: new(big.Int).SetUint64(v)} }

func (b *BigInt) Type() ObjectType { return BIGINT_OBJ }

// Inspect 返回调试表示。带 "n" 后缀，与 V8 的 console.log(1n) → 1n 一致。
// 注意与 ToString 区分: 语言层的字符串转换 (拼接、模板、String()) 不带后缀。
func (b *BigInt) Inspect() string { return b.Value.String() + "n" }

func (b *BigInt) IsTruthy() bool { return b.Value.Sign() != 0 }

func (b *BigInt) GetProperty(name string) (Value, bool) {
	if BigIntProto != nil {
		return BigIntProto.GetProperty(name)
	}
	return nil, false
}

func (b *BigInt) SetProperty(name string, val Value) {
	// 原始类型不能设置属性
}

// Clone 返回独立副本。
//
// big.Int 的方法 (Add/Quo/...) 在复用接收者时会就地修改，而 JS 的 BigInt
// 是不可变值，任何运算都必须产生新对象，故所有运算入口都基于 Clone。
func (b *BigInt) Clone() *BigInt { return &BigInt{Value: new(big.Int).Set(b.Value)} }

// ===== 算术运算 =====

func (b *BigInt) Add(o *BigInt) *BigInt { return &BigInt{Value: new(big.Int).Add(b.Value, o.Value)} }
func (b *BigInt) Sub(o *BigInt) *BigInt { return &BigInt{Value: new(big.Int).Sub(b.Value, o.Value)} }
func (b *BigInt) Mul(o *BigInt) *BigInt { return &BigInt{Value: new(big.Int).Mul(b.Value, o.Value)} }

// Div 截断除法 (向零取整)。big.Int 的 Quo/Rem 是截断语义，
// Div/Mod 是欧几里得语义 (余数恒非负)，JS 需要前者。
// 除数为 0 的行为由调用方判定 (抛 RangeError)。
func (b *BigInt) Div(o *BigInt) *BigInt { return &BigInt{Value: new(big.Int).Quo(b.Value, o.Value)} }

// Rem 截断取余 (符号跟随被除数)，对应 JS 的 %。
func (b *BigInt) Rem(o *BigInt) *BigInt { return &BigInt{Value: new(big.Int).Rem(b.Value, o.Value)} }

// Neg 取负。
func (b *BigInt) Neg() *BigInt { return &BigInt{Value: new(big.Int).Neg(b.Value)} }

// Pow 幂运算。指数为负时返回 nil (JS 中 BigInt 负指数抛 RangeError)，
// 因为 BigInt 无法表示分数结果。
func (b *BigInt) Pow(o *BigInt) *BigInt {
	if o.Value.Sign() < 0 {
		return nil
	}
	return &BigInt{Value: new(big.Int).Exp(b.Value, o.Value, nil)}
}

// ===== 位运算 =====

func (b *BigInt) BitAnd(o *BigInt) *BigInt { return &BigInt{Value: new(big.Int).And(b.Value, o.Value)} }
func (b *BigInt) BitOr(o *BigInt) *BigInt  { return &BigInt{Value: new(big.Int).Or(b.Value, o.Value)} }
func (b *BigInt) BitXor(o *BigInt) *BigInt { return &BigInt{Value: new(big.Int).Xor(b.Value, o.Value)} }
func (b *BigInt) BitNot() *BigInt          { return &BigInt{Value: new(big.Int).Not(b.Value)} }

// Shl 左移。位移量为负时退化为右移 (ECMAScript BigInt::leftShift 语义)。
func (b *BigInt) Shl(n *BigInt) *BigInt {
	return &BigInt{Value: new(big.Int).Lsh(b.Value, shiftAmount(n))}
}

// Shr 算术右移。位移量为负时退化为左移。
func (b *BigInt) Shr(n *BigInt) *BigInt {
	return &BigInt{Value: new(big.Int).Rsh(b.Value, shiftAmount(n))}
}

// shiftAmount 将位移量规约为 uint，负值表示反向移位。
// Lsh/Rsh 用同一个 uint 参数，方向由调用方决定，故这里只取绝对值。
func shiftAmount(n *BigInt) uint {
	if !n.Value.IsUint64() {
		// 绝对值超过 uint64 的位移量在 JS 中按符号退化为 ±2^64 级别的移位，
		// 实际不可达 (会先耗尽内存)，此处按最大位移处理。
		if n.Value.Sign() < 0 {
			return ^uint(0)
		}
		return ^uint(0)
	}
	return uint(n.Value.Uint64())
}

// Cmp 比较大小: -1 小于, 0 等于, 1 大于。
func (b *BigInt) Cmp(o *BigInt) int { return b.Value.Cmp(o.Value) }

// ===== 转换 =====

// ToStringRadix 按指定进制输出。JS 的 BigInt.prototype.toString 要求
// radix ∈ [2,36]，超出抛 RangeError；输出字母为小写。
func (b *BigInt) ToStringRadix(radix int) string {
	if radix < 2 || radix > 36 {
		return ""
	}
	return b.Value.Text(radix)
}

// ToFloat64 近似转换为 double。仅用于 BigInt 参与 Number 语义的场合
// (如 JSON 化、Array 索引)；JS 的算术运算不允许这条隐式转换。
func (b *BigInt) ToFloat64() float64 {
	f, _ := new(big.Float).SetInt(b.Value).Float64()
	return f
}

// AsInt64 在值可无损放入 int64 时返回 (value, true)。
func (b *BigInt) AsInt64() (int64, bool) {
	if !b.Value.IsInt64() {
		return 0, false
	}
	return b.Value.Int64(), true
}

// AsUint64 在值可无损放入 uint64 时返回 (value, true)。
func (b *BigInt) AsUint64() (uint64, bool) {
	if !b.Value.IsUint64() {
		return 0, false
	}
	return b.Value.Uint64(), true
}

// ===== 解析 =====

// ParseBigIntLiteral 解析整数字面量文本 (已去掉 "n" 后缀)。
//
// 支持 ECMAScript 的非十进制整数字面量:
//
//	0x/0X 十六进制, 0b/0B 二进制, 0o/0O 八进制, 以及十进制。
//	允许数字分隔符 "_"，允许前导正负号。
//
// 调用方需保证文本中不含小数点或指数部分——BigInt 字面量不允许这两者，
// 该约束由 lexer/parser 在语法阶段拒绝。
func ParseBigIntLiteral(s string) (*big.Int, bool) {
	return parseBigIntText(s, true)
}

// ParseBigIntString 实现 ECMAScript 的 StringToBigInt。
// 与字面量解析的区别: 不接受数字分隔符 "_"。
func ParseBigIntString(s string) (*big.Int, bool) {
	t := jsTrimSpace(s)
	if t == "" {
		return big.NewInt(0), true
	}
	return parseBigIntText(t, false)
}

// parseBigIntText 是两种解析路径的公共实现。
func parseBigIntText(s string, allowSeparators bool) (*big.Int, bool) {
	body := s
	neg := false
	if body != "" && (body[0] == '+' || body[0] == '-') {
		neg = body[0] == '-'
		body = body[1:]
	}
	if body == "" {
		return nil, false
	}

	base := 10
	digits := body
	// 旧式八进制 (0755) 不是合法的 BigInt 字面量，只接受显式前缀形式。
	if len(body) > 2 && body[0] == '0' {
		switch body[1] {
		case 'x', 'X':
			base, digits = 16, body[2:]
		case 'b', 'B':
			base, digits = 2, body[2:]
		case 'o', 'O':
			base, digits = 8, body[2:]
		}
	}

	if allowSeparators {
		// 数字分隔符不能出现在首尾，也不能相邻——ECMAScript 的语法约束。
		var b strings.Builder
		prevSep := true // 开头不允许分隔符
		for i := 0; i < len(digits); i++ {
			c := digits[i]
			if c == '_' {
				if prevSep {
					return nil, false
				}
				prevSep = true
				continue
			}
			if !validDigitForBase(c, base) {
				return nil, false
			}
			prevSep = false
			b.WriteByte(c)
		}
		if prevSep || b.Len() == 0 {
			return nil, false
		}
		digits = b.String()
	} else {
		for i := 0; i < len(digits); i++ {
			if !validDigitForBase(digits[i], base) {
				return nil, false
			}
		}
	}

	v, ok := new(big.Int).SetString(digits, base)
	if !ok {
		return nil, false
	}
	if neg {
		v.Neg(v)
	}
	return v, true
}

// validDigitForBase 检查字符是否为给定进制下的合法数字。
func validDigitForBase(c byte, base int) bool {
	switch {
	case c >= '0' && c <= '9':
		return int(c-'0') < base
	case c >= 'a' && c <= 'f':
		return int(c-'a'+10) < base
	case c >= 'A' && c <= 'F':
		return int(c-'A'+10) < base
	}
	return false
}

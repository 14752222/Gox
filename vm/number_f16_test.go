package vm

import "testing"

// Number.prototype.toString / Math.f16round 回归钉 (rFXXbq batch25)。
//
// 1. Number.prototype 自身是 [[NumberData]]=0 的 Number 对象:
//    Number.prototype.toString(2) === "0" (S15.7.4.2_A2 全族)。
// 2. 非 10 进制走最短往返 (与 V8 一致): 16 位截断的旧实现
//    (0.1).toString(2) 会错。
// 3. precision/digits 参数为 Symbol 时 TypeError (ToNumber(Symbol) 抛)。
// 4. Math.f16round (ES2024): 直接按 float64 位型就近偶舍入到 binary16,
//    不能经 float32 中转 (丢失双精度细粒度)。
func TestNumberToStringRadixAndReceiver(t *testing.T) {
	cases := []struct{ src, want string }{
		{`Number.prototype.toString(2)`, "0"},
		{`(new Number(0)).toString(2)`, "0"},
		{`(new Number(-1)).toString(2)`, "-1"},
		{`(new Number(255)).toString(16)`, "ff"},
		{`(0.1).toString(2)`, "0.0001100110011001100110011001100110011001100110011001101"},
		{`(0.1).toString(16)`, "0.1999999999999a"},
		{`(0.5).toString(3)`, "0.1111111111111111111111111111111112"},
		{`(1/3).toString(3)`, "0.1"},
		{`(1e21).toString(16)`, "3635c9adc5dea00000"},
		{`(2**63).toString(16)`, "8000000000000000"},
		{`(-255.5).toString(16)`, "-ff.8"},
		{`(123.456).toString(8)`, "173.3513615237574734"},
		{`Number.prototype.toFixed.call(Number.prototype, 2)`, "0.00"},
	}
	for _, tc := range cases {
		got := evalJS(t, "String("+tc.src+")")
		assertString(t, got, tc.want)
	}
	// Symbol 参数 → TypeError (非 RangeError)
	assertString(t, evalJS(t, `(() => {
  try { (5).toPrecision(Symbol(1)); return "no-throw"; }
  catch (e) { return e.name; }
})()`), "TypeError")
}

func TestMathF16Round(t *testing.T) {
	cases := []struct{ src, want string }{
		{`Math.f16round(1)`, "1"},
		{`Math.f16round(65504)`, "65504"},
		{`Math.f16round(65505)`, "65504"},
		{`Math.f16round(65520)`, "Infinity"},
		{`Math.f16round(0.1)`, "0.0999755859375"},
		{`Math.f16round(1e-8)`, "0"},
		{`Math.f16round(2.980232238769532e-8)`, "5.960464477539063e-8"},
		{`Math.f16round(2.9802322387695312e-8)`, "0"},
		{`Math.f16round(-2.5)`, "-2.5"},
		{`Math.f16round(0.5)`, "0.5"},
		{`Math.f16round(2049)`, "2048"},
		{`Math.f16round(1/0)`, "Infinity"},
		{`Math.f16round(-1/0)`, "-Infinity"},
		{`Math.f16round(NaN)`, "NaN"},
	}
	for _, tc := range cases {
		got := evalJS(t, "String("+tc.src+")")
		assertString(t, got, tc.want)
	}
}

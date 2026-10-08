package vm

import (
	"math"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== Date / performance 全局可用性 (看板 ryGXAJ) =====
//
// 背景: JS 语言层此前完全没有 Date 与 performance —— 两者 typeof 都是
// undefined, 全仓零注册。apps/ 里凡涉及时间排序、按月分组、时间戳显示、
// 耗时统计的应用都拿不到任何时间源, 只能退化成「采样序号」。
//
// 实现约定: Date 是独立的值类型 *object.Date (与 RegExp/Map/Promise 同法),
// 内部 time value 对 JS 层不可见 —— 故 Date 实例的自有属性集合必须为空。
// 零 cgo / 零第三方依赖, 直接走 Go 标准库 time。

// dateResult 跑一段顶层脚本, 返回末表达式的值。
func dateResult(t *testing.T, src string) object.Value {
	t.Helper()
	return evalWithStdlib(t, src)
}

// TestDateAndPerformanceAreRegistered: Date 与 performance 必须可达且类型正确。
func TestDateAndPerformanceAreRegistered(t *testing.T) {
	if got := dateResult(t, `typeof Date;`).Inspect(); got != "function" {
		t.Errorf("typeof Date 应为 function, got %s", got)
	}
	if got := dateResult(t, `typeof performance;`).Inspect(); got != "object" {
		t.Errorf("typeof performance 应为 object, got %s", got)
	}
	if got := dateResult(t, `typeof performance.now;`).Inspect(); got != "function" {
		t.Errorf("typeof performance.now 应为 function, got %s", got)
	}
	// performance.now() 必须单调可读且非负。
	n, ok := dateResult(t, `performance.now() >= 0;`).(*object.Boolean)
	if !ok || !n.Value {
		t.Errorf("performance.now() 应为非负数值")
	}
}

// TestDateNowIsMonotonicReading: Date.now() 应返回当前 epoch 毫秒量级的读数,
// 且两次调用不倒退。
func TestDateNowIsMonotonicReading(t *testing.T) {
	v := dateResult(t, `
		var t1 = Date.now();
		var t2 = Date.now();
		"" + (t2 >= t1 && t1 > 1.7e12);
	`)
	if got := v.Inspect(); got != "true" {
		t.Errorf("Date.now() 应为递增的 epoch 毫秒读数, got %s", got)
	}
}

// TestDateConstructors: 各构造形态的 time value 必须与 Node 一致。
func TestDateConstructors(t *testing.T) {
	cases := []struct{ src, want string }{
		// 显式 time value
		{`new Date(0).getTime();`, "0"},
		{`new Date(1234567890123).getTime();`, "1234567890123"},
		// ISO 字符串解析
		{`new Date("1970-01-01T00:00:00Z").getTime();`, "0"},
		{`new Date("2026-10-08T00:00:00Z").getTime();`, "1791417600000"},
		// 多参 (本地时区组装): Date.UTC 口径对照 ⇒ 固定 UTC 期望值
		{`Date.UTC(2026, 0, 1);`, "1767225600000"},
		{`Date.UTC(1970, 0, 1);`, "0"},
		{`Date.parse("2026-10-08T00:00:00Z");`, "1791417600000"},
		// 年份 0..99 映射到 1900+year (规范 MakeDate 约定)
		{`Date.UTC(70, 0, 1);`, "0"},
		// Invalid Date
		{`"" + new Date("not-a-date").getTime();`, "NaN"},
		{`String(new Date("garbage"));`, "Invalid Date"},
	}
	for _, c := range cases {
		if got := dateResult(t, c.src).Inspect(); got != c.want {
			t.Errorf("%s\n  want %s\n  got  %s", c.src, c.want, got)
		}
	}
}

// TestDateUTCComponentGetters: UTC 分量取值必须与 Node 一致。
func TestDateUTCComponentGetters(t *testing.T) {
	cases := []struct{ src, want string }{
		{`new Date(1234567890123).getUTCFullYear();`, "2009"},
		{`new Date(1234567890123).getUTCMonth();`, "1"},
		{`new Date(1234567890123).getUTCDate();`, "13"},
		{`new Date(1234567890123).getUTCHours();`, "23"},
		{`new Date(1234567890123).getUTCMinutes();`, "31"},
		{`new Date(1234567890123).getUTCSeconds();`, "30"},
		{`new Date(0).getUTCMilliseconds();`, "0"},
		// Invalid Date 的所有分量 getter 返回 NaN
		{`"" + new Date("bad").getUTCFullYear();`, "NaN"},
		{`"" + new Date("bad").getTime();`, "NaN"},
	}
	for _, c := range cases {
		if got := dateResult(t, c.src).Inspect(); got != c.want {
			t.Errorf("%s\n  want %s\n  got  %s", c.src, c.want, got)
		}
	}
}

// TestDateLocalGetterIsTimezoneAware: 本地分量 getter 必须按宿主时区投影,
// 与 getUTC* 的差恰好等于 getTimezoneOffset —— 早前实现把两者都返回 UTC,
// 在 UTC 时区下会伪通过。这里刻意**不依赖具体时区** (Go 在首次使用时即缓存
// TZ, 测试里改 TZ 环境变量不生效), 而是钉住两条getter间的差必须自洽。
func TestDateLocalGetterIsTimezoneAware(t *testing.T) {
	// getHours 必须等于 getUTCHours - offset/60 (offset = UTC - local, 分钟)。
	v := dateResult(t, `
		var d = new Date(1234567890123);
		var off = d.getTimezoneOffset();
		"" + (d.getHours() === Math.floor((d.getUTCHours() - off / 60 + 24) % 24));
	`)
	if got := v.Inspect(); got != "true" {
		t.Errorf("getHours 与 getUTCHours 的差应等于 getTimezoneOffset, got %s", got)
	}
	// 本地日期也必须随时区推移: UTC 时刻 23:31 在 UTC+8 下已是次日。
	v = dateResult(t, `
		var d = new Date(1234567890123);
		"" + (d.getTimezoneOffset() === 0 || d.getDate() >= d.getUTCDate());
	`)
	if got := v.Inspect(); got != "true" {
		t.Errorf("本地日期应与 UTC 日期同向或相等, got %s", got)
	}
	// getTimezoneOffset 的绝对值不得超过 24h (时区合法区间)。
	v = dateResult(t, `"" + (Math.abs(new Date(0).getTimezoneOffset()) <= 1440);`)
	if got := v.Inspect(); got != "true" {
		t.Errorf("getTimezoneOffset 应落在 ±1440 分钟内, got %s", got)
	}
}

// TestDateStringForms: 字符串表现形式。
// toISOString 是全 UTC 的固定形态, 与时区无关 —— 故这里只钉它。
func TestDateStringForms(t *testing.T) {
	cases := []struct{ src, want string }{
		{`new Date(0).toISOString();`, "1970-01-01T00:00:00.000Z"},
		{`new Date(1234567890123).toISOString();`, "2009-02-13T23:31:30.123Z"},
		{`new Date(0).toUTCString();`, "Thu, 01 Jan 1970 00:00:00 GMT"},
		{`new Date(0).toJSON();`, "1970-01-01T00:00:00.000Z"},
		{`JSON.stringify({d: new Date(0)});`, `{"d":"1970-01-01T00:00:00.000Z"}`},
	}
	for _, c := range cases {
		if got := dateResult(t, c.src).Inspect(); got != c.want {
			t.Errorf("%s\n  want %s\n  got  %s", c.src, c.want, got)
		}
	}
}

// TestDateArithmeticUsesTimeValue: Date 参与算术必须取其 time value ——
// 这是「按时间排序 / 算时间差」能否工作的根。
func TestDateArithmeticUsesTimeValue(t *testing.T) {
	cases := []struct{ src, want string }{
		{`new Date(1000) - new Date(0);`, "1000"},
		{`+new Date(12345);`, "12345"},
		{`Number(new Date(12345));`, "12345"},
		{`"" + (new Date(100) > new Date(50));`, "true"},
		// 排序: apps#4 日志查看器按时间戳排序的核心写法
		{`[3,1,2].map(function(n){return new Date(n);}).sort(function(a,b){return a-b;}).map(function(d){return d.getTime();}).join(",");`, "1,2,3"},
	}
	for _, c := range cases {
		if got := dateResult(t, c.src).Inspect(); got != c.want {
			t.Errorf("%s\n  want %s\n  got  %s", c.src, c.want, got)
		}
	}
}

// TestDatePrototypeChain: Date 实例的自有属性必须为空, 成员全部来自原型,
// 且 instanceof 成立 —— 这是「内部 time value 不能泄漏到 JS 层」的验收口。
func TestDatePrototypeChain(t *testing.T) {
	if got := dateResult(t, `JSON.stringify(Object.keys(new Date()));`).Inspect(); got != "[]" {
		t.Errorf("Date 实例不得有自有属性 (time value 不得泄漏), got %s", got)
	}
	if got := dateResult(t, `new Date() instanceof Date;`).Inspect(); got != "true" {
		t.Errorf("new Date() instanceof Date 应为 true, got %s", got)
	}
	if got := dateResult(t, `typeof new Date(0).getTime;`).Inspect(); got != "function" {
		t.Errorf("getTime 应可经原型取到且可调用, got %s", got)
	}
	if got := dateResult(t, `Object.getPrototypeOf(new Date()) === Date.prototype;`).Inspect(); got != "true" {
		t.Errorf("Date 实例的原型应为 Date.prototype, got %s", got)
	}
}

// TestDateBadReceiverThrows: this 不是 Date 时取值方法必须抛 TypeError。
func TestDateBadReceiverThrows(t *testing.T) {
	v := dateResult(t, `
		var e = "no-throw";
		try { Date.prototype.getTime.call({}); } catch (err) { e = err.constructor.name; }
		e;
	`)
	if got := v.Inspect(); got != "TypeError" {
		t.Errorf("非 Date 的 this 应抛 TypeError, got %s", got)
	}
}

// TestDateSetTimeMutates: setTime 是少数能改内部 time value 的入口。
func TestDateSetTimeMutates(t *testing.T) {
	v := dateResult(t, `
		var d = new Date(0);
		d.setTime(999);
		"" + d.getTime();
	`)
	if got := v.Inspect(); got != "999" {
		t.Errorf("setTime(999) 后 getTime 应为 999, got %s", got)
	}
}

// TestDateInvalidStaysInvalidAcrossOps: Invalid Date 在数值转换下恒为 NaN。
func TestDateInvalidStaysInvalidAcrossOps(t *testing.T) {
	v := dateResult(t, `new Date("nope").valueOf();`)
	n, ok := v.(*object.Number)
	if !ok {
		t.Fatalf("valueOf 应返回 number, got %T (%s)", v, v.Inspect())
	}
	if !math.IsNaN(n.Value) {
		t.Errorf("Invalid Date 的 valueOf 应为 NaN, got %v", n.Value)
	}
	// Invalid Date 参与算术同样得 NaN (传染性)。
	v = dateResult(t, `new Date("nope") - 0;`)
	n, ok = v.(*object.Number)
	if !ok || !math.IsNaN(n.Value) {
		t.Errorf("Invalid Date 参与算术应得 NaN, got %s", v.Inspect())
	}
}

// TestDateRoundTripISO: Date 序列化再解析必须回到同一时刻 —— apps
// (账本按月分组 / 日志排序) 持久化时间的基础假设。
func TestDateRoundTripISO(t *testing.T) {
	v := dateResult(t, `
		var iso = new Date(1234567890123).toISOString();
		"" + (new Date(iso).getTime() === 1234567890123);
	`)
	if got := v.Inspect(); got != "true" {
		t.Errorf("ISO 往返应与原 time value 一致, got %s", got)
	}
}

package object

// 本文件实现 Temporal.Duration。
//
// Duration 是 Temporal 中最微妙的类型: 它的字段分属两套不可互换的体系。
//   - 日历单位 (年/月/周): 长度不固定，必须先落到具体日历上的某个起点
//     才能换算成纳秒。1 个月可能是 28/29/30/31 天。
//   - 精确单位 (日及以下): 1 日恒等于 86400e9 纳秒 (Temporal 不使用闰秒)。
//
// 因此所有涉及日历单位的运算都必须带 relativeTo，否则 years/months/weeks
// 无法参与计算——这与 Date 直接把 "一个月" 当作 30 天的做法是本质区别。

import (
	"fmt"
	"math"
	"math/big"
	"strings"
)

// TemporalDuration 表示一个时间段。
type TemporalDuration struct {
	years        int64
	months       int64
	weeks        int64
	days         int64
	hours        int64
	minutes      int64
	seconds      int64
	milliseconds int64
	microseconds int64
	nanoseconds  int64
}

// NewTemporalDuration 构造 Duration。
func NewTemporalDuration(years, months, weeks, days, hours, minutes, seconds, ms, us, ns int64) *TemporalDuration {
	return &TemporalDuration{
		years: years, months: months, weeks: weeks, days: days,
		hours: hours, minutes: minutes, seconds: seconds,
		milliseconds: ms, microseconds: us, nanoseconds: ns,
	}
}

// NewTemporalDurationFromFields 由浮点输入构造 Duration，非整数字段返回 false。
func NewTemporalDurationFromFields(fields map[string]float64) (*TemporalDuration, bool) {
	get := func(k string) int64 {
		v, ok := fields[k]
		if !ok {
			return 0
		}
		if !isValidDurationField(v) {
			return 0 // 调用方应先校验
		}
		return int64(v)
	}
	return NewTemporalDuration(
		get("years"), get("months"), get("weeks"), get("days"),
		get("hours"), get("minutes"), get("seconds"),
		get("milliseconds"), get("microseconds"), get("nanoseconds"),
	), true
}

func (t *TemporalDuration) Type() ObjectType { return TEMPORAL_DURATION_OBJ }
func (t *TemporalDuration) IsTruthy() bool   { return true }
func (t *TemporalDuration) Inspect() string  { return t.ToString() }

func (t *TemporalDuration) GetProperty(name string) (Value, bool) {
	return protoLookup(TEMPORAL_DURATION_OBJ, name, t)
}
func (t *TemporalDuration) SetProperty(name string, val Value) {}
func (t *TemporalDuration) GetSymbolProperty(sym *Symbol) (Value, bool) {
	return symbolDispatch(TEMPORAL_DURATION_OBJ, sym)
}
func (t *TemporalDuration) SetSymbolProperty(sym *Symbol, val Value) {}
func (t *TemporalDuration) GetProto() Value { return GetTemporalProto(TEMPORAL_DURATION_OBJ) }

// ===== 字段访问 =====

func (t *TemporalDuration) Years() int64        { return t.years }
func (t *TemporalDuration) Months() int64       { return t.months }
func (t *TemporalDuration) Weeks() int64        { return t.weeks }
func (t *TemporalDuration) Days() int64         { return t.days }
func (t *TemporalDuration) Hours() int64        { return t.hours }
func (t *TemporalDuration) Minutes() int64      { return t.minutes }
func (t *TemporalDuration) Seconds() int64      { return t.seconds }
func (t *TemporalDuration) Milliseconds() int64 { return t.milliseconds }
func (t *TemporalDuration) Microseconds() int64 { return t.microseconds }
func (t *TemporalDuration) Nanoseconds() int64  { return t.nanoseconds }

// IsBlank 判断是否为零时长。
func (t *TemporalDuration) IsBlank() bool {
	return t.years == 0 && t.months == 0 && t.weeks == 0 && t.days == 0 &&
		t.hours == 0 && t.minutes == 0 && t.seconds == 0 &&
		t.milliseconds == 0 && t.microseconds == 0 && t.nanoseconds == 0
}

// Sign 返回时长的符号: -1/0/1。符号由第一个非零字段决定。
// 注意混合符号 (如 {days:1, hours:-2}) 也按其最大单位的符号取值。
func (t *TemporalDuration) Sign() int {
	for _, v := range []int64{t.years, t.months, t.weeks, t.days,
		t.hours, t.minutes, t.seconds, t.milliseconds, t.microseconds, t.nanoseconds} {
		if v > 0 {
			return 1
		}
		if v < 0 {
			return -1
		}
	}
	return 0
}

// Negated 返回取负后的新 Duration。
func (t *TemporalDuration) Negated() *TemporalDuration {
	return NewTemporalDuration(-t.years, -t.months, -t.weeks, -t.days,
		-t.hours, -t.minutes, -t.seconds, -t.milliseconds, -t.microseconds, -t.nanoseconds)
}

// Abs 返回绝对值 Duration。
func (t *TemporalDuration) Abs() *TemporalDuration {
	if t.Sign() < 0 {
		return t.Negated()
	}
	return t
}

// TimeTotalNanos 返回精确单位部分的总纳秒数 (日及以下)。
// 日历单位 (年/月/周) 不参与——它们的长度依赖起点。
func (t *TemporalDuration) TimeTotalNanos() *big.Int {
	ns := big.NewInt(0)
	addFields := []struct {
		v      int64
		factor int64
	}{
		{t.days, NanosPerDay},
		{t.hours, NanosPerHour},
		{t.minutes, NanosPerMinute},
		{t.seconds, NanosPerSecond},
		{t.milliseconds, NanosPerMillisecond},
		{t.microseconds, NanosPerMicrosecond},
		{t.nanoseconds, 1},
	}
	for _, f := range addFields {
		ns.Add(ns, new(big.Int).Mul(big.NewInt(f.v), big.NewInt(f.factor)))
	}
	return ns
}

// ===== 比较 =====

// CompareDurationNonCalendar 比较两个 Duration 的精确单位部分 (日及以下)。
// 含日历单位时不适用——需要 relativeTo 才能比较。
func CompareDurationNonCalendar(a, b *TemporalDuration) int {
	return a.TimeTotalNanos().Cmp(b.TimeTotalNanos())
}

// HasCalendarUnits 判断是否含年/月/周这些长度不固定的单位。
func (t *TemporalDuration) HasCalendarUnits() bool {
	return t.years != 0 || t.months != 0 || t.weeks != 0
}

// ===== ISO 字符串表示 =====

// ToString 输出 TemporalDurationString: P1Y2M3DT4H5M6.789S
//
// 末位截断规则: 小数秒去掉尾部多余的 0，全零则不输出小数点。
func (t *TemporalDuration) ToString() string {
	var sb strings.Builder
	if t.Sign() < 0 {
		sb.WriteByte('-')
	}
	sb.WriteByte('P')
	prefixLen := sb.Len()

	appendUnit := func(v int64, unit byte) {
		if v == 0 {
			return
		}
		if v < 0 {
			v = -v
		}
		sb.WriteString(fmt.Sprintf("%d%c", v, unit))
	}
	appendUnit(t.years, 'Y')
	appendUnit(t.months, 'M')
	appendUnit(t.weeks, 'W')
	appendUnit(t.days, 'D')

	dateWritten := sb.Len() > prefixLen
	timeEmpty := t.hours == 0 && t.minutes == 0 && t.seconds == 0 &&
		t.milliseconds == 0 && t.microseconds == 0 && t.nanoseconds == 0
	if timeEmpty {
		// 零时长没有 date 部分也没有 time 部分，规范规定输出 "PT0S"
		if !dateWritten {
			sb.WriteString("T0S")
		}
		return sb.String()
	}
	sb.WriteByte('T')
	appendUnit(t.hours, 'H')
	appendUnit(t.minutes, 'M')

	frac := t.milliseconds*NanosPerMillisecond + t.microseconds*NanosPerMicrosecond + t.nanoseconds
	if t.seconds != 0 || frac != 0 {
		secs := t.seconds
		if secs < 0 {
			secs = -secs
		}
		f := frac
		if f < 0 {
			f = -f
		}
		if frac == 0 {
			sb.WriteString(fmt.Sprintf("%dS", secs))
		} else {
			digits := fmt.Sprintf("%09d", f)
			digits = strings.TrimRight(digits, "0")
			sb.WriteString(fmt.Sprintf("%d.%sS", secs, digits))
		}
	}
	return sb.String()
}

// ===== 时间部分平衡 =====

// BalanceTimeSubsecond 把亚秒字段平衡成毫秒/微秒/纳秒的标准区间。
func BalanceTimeSubsecond(ms, us, ns int64) (int64, int64, int64) {
	total := ms*NanosPerMillisecond + us*NanosPerMicrosecond + ns
	outNs := total % 1000
	totalUs := total / 1000
	outUs := totalUs % 1000
	outMs := totalUs / 1000
	return outMs, outUs, outNs
}

// BalanceNanosToTime 把纳秒总数拆成 (天, 时, 分, 秒, 毫秒, 微秒, 纳秒)。
// 这是 AddDateTime 计算时间进位的入口。
func BalanceNanosToTime(total *big.Int) (days, hours, minutes, seconds, ms, us, ns int64) {
	v := new(big.Int).Set(total)
	divModInt := func(m int64) int64 {
		mm := big.NewInt(m)
		q, r := new(big.Int).QuoRem(v, mm, new(big.Int))
		v = q
		return r.Int64()
	}
	ns = divModInt(1000)
	us = divModInt(1000)
	ms = divModInt(1000)
	seconds = divModInt(60)
	minutes = divModInt(60)
	hours = divModInt(24)
	days = v.Int64()
	return
}

// AddTimeDuration 把 Duration 的精确部分加到 ISO 时间上。
// 返回 (新日期, 新时间, ok): 天进位反映在日期里。
func AddTimeDuration(date ISODate, time ISOTime, delta *big.Int) (ISODate, ISOTime, bool) {
	dayDelta, h, mi, s, msec, usec, nsec := BalanceNanosToTime(
		new(big.Int).Add(big.NewInt(time.NanosOfDay()), delta))

	newTime := ISOTime{
		Hour: int32(h), Minute: int32(mi), Second: int32(s),
		Millisecond: int32(msec), Microsecond: int32(usec), Nanosecond: int32(nsec),
	}
	newDays := date.EpochDays() + dayDelta
	if !IsValidEpochDays(newDays) {
		return ISODate{}, ISOTime{}, false
	}
	return ISODateFromEpochDays(newDays), newTime, true
}

// ===== 相对某起点的完整运算 =====

// AddDateTime 把一个完整 Duration (含日历单位) 加到日期时间上。
//
// relativeTo 是日历单位运算的锚点。overflow 处理月末对齐
// (如 1月31日 + 1个月 → 2月28/29日，reject 模式则失败)。
func AddDateTime(dt ISODateTime, d *TemporalDuration, overflow string) (ISODateTime, bool) {
	newDate, ok := AddDateDuration(dt.Date,
		int32(d.years), int32(d.months), int32(d.weeks), int32(d.days), overflow)
	if !ok {
		return ISODateTime{}, false
	}
	newDate, newTime, ok := AddTimeDuration(newDate, dt.Time, d.TimeTotalNanos())
	if !ok {
		return ISODateTime{}, false
	}
	return ISODateTime{Date: newDate, Time: newTime}, true
}

// ===== total / round =====

// TotalNanosPerUnit 返回各单位对应的纳秒数。
// years/months/weeks 无固定纳秒长度，返回 ok=false。
func TotalNanosPerUnit(unit string) (float64, bool) {
	switch unit {
	case "day", "days":
		return float64(NanosPerDay), true
	case "hour", "hours":
		return float64(NanosPerHour), true
	case "minute", "minutes":
		return float64(NanosPerMinute), true
	case "second", "seconds":
		return float64(NanosPerSecond), true
	case "millisecond", "milliseconds":
		return float64(NanosPerMillisecond), true
	case "microsecond", "microseconds":
		return float64(NanosPerMicrosecond), true
	case "nanosecond", "nanoseconds":
		return 1, true
	}
	return 0, false
}

// TotalOfUnit 计算 Duration 按指定单位的总值 (浮点)。
//
// unit 为日历单位时必须提供 relativeTo，否则无法换算 (返回 ok=false);
// 这与 Temporal.Duration.prototype.total 抛 RangeError 的行为一致。
func TotalOfUnit(d *TemporalDuration, unit string, relativeTo *ISODateTime) (float64, bool) {
	if n, ok := TotalNanosPerUnit(unit); ok {
		total := d.TimeTotalNanos()
		f, _ := new(big.Float).SetInt(total).Float64()
		return f / n, true
	}
	var divisor float64
	switch unit {
	case "year", "years":
		divisor = 31556952000000000.0 // ISO 年: 365.2425 天
	case "month", "months":
		divisor = 2629746000000000.0 // ISO 年 / 12
	case "week", "weeks":
		divisor = float64(7 * NanosPerDay)
	default:
		return 0, false
	}
	// 日历单位有了相对起点后，把 date 差值算成纳秒再除
	if relativeTo == nil {
		return 0, false
	}
	target, ok := AddDateTime(*relativeTo, d, "constrain")
	if !ok {
		return 0, false
	}
	diff := new(big.Int).Sub(target.EpochNanos(), relativeTo.EpochNanos())
	f, _ := new(big.Float).SetInt(diff).Float64()
	return f / divisor, true
}

// RoundNanos 按 roundingMode 与 increment 对纳秒值取整。
// truncation 是返回细粟端 tolerant 与否的控制: 这里实现 halfExpand (四舍五入)。
func RoundNanos(total *big.Int, increment *big.Int, mode string) *big.Int {
	if increment == nil || increment.Sign() <= 0 {
		return new(big.Int).Set(total)
	}
	q, r := new(big.Int).QuoRem(total, increment, new(big.Int))
	if r.Sign() == 0 {
		return new(big.Int).Set(total)
	}
	half := new(big.Int).Rsh(increment, 1)
	// Rem 与 Quo 都是截断语义 (结果与 total 同号)，比较时用绝对值。
	absR := new(big.Int).Abs(r)
	roundUp := false
	switch mode {
	case "ceil":
		roundUp = total.Sign() > 0
	case "floor":
		roundUp = total.Sign() < 0
	case "trunc":
		roundUp = false
	default: // halfExpand
		roundUp = absR.Cmp(half) >= 0
	}
	if roundUp {
		if total.Sign() < 0 {
			q.Sub(q, big.NewInt(1))
		} else {
			q.Add(q, big.NewInt(1))
		}
	}
	return new(big.Int).Mul(q, increment)
}

// abs64 返回绝对值 (避免 math.Abs 的 float64 往返)。
func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// IsValidUnit 检查是否为 Temporal 支持的时间单位名。
func IsValidUnit(u string) bool {
	switch u {
	case "year", "years", "month", "months", "week", "weeks", "day", "days",
		"hour", "hours", "minute", "minutes", "second", "seconds",
		"millisecond", "milliseconds", "microsecond", "microseconds",
		"nanosecond", "nanoseconds":
		return true
	}
	return false
}

// SingularUnit 把复数单位名归一化为单数形式。
func SingularUnit(u string) string { return strings.TrimSuffix(u, "s") }

// UnitToNanos 返回单位对应的纳秒数 (仅精确单位)。
func UnitToNanos(u string) (int64, bool) {
	switch SingularUnit(u) {
	case "day":
		return NanosPerDay, true
	case "hour":
		return NanosPerHour, true
	case "minute":
		return NanosPerMinute, true
	case "second":
		return NanosPerSecond, true
	case "millisecond":
		return NanosPerMillisecond, true
	case "microsecond":
		return NanosPerMicrosecond, true
	case "nanosecond":
		return 1, true
	}
	return 0, false
}

// DurationFieldAbsSum 返回所有字段绝对值之和，用于检测溢出量级。
func (t *TemporalDuration) DurationFieldAbsSum() float64 {
	sum := 0.0
	for _, v := range []int64{t.years, t.months, t.weeks, t.days,
		t.hours, t.minutes, t.seconds, t.milliseconds, t.microseconds, t.nanoseconds} {
		sum += math.Abs(float64(v))
	}
	return sum
}

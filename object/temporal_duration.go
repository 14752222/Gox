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
func (t *TemporalDuration) GetProto() Value                          { return GetTemporalProto(TEMPORAL_DURATION_OBJ) }

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

// ToStringWithPrecision 按显示精度输出 TemporalDurationString。
//
// digits 为小数秒位数: >=0 固定输出该位数 (不足补零, 0 则无小数点);
// -1 表示 auto (裁剪尾部 0)。quantum 为舍入量子 (纳秒), 0 表示不舍入。
//
// 舍入对象是**整个时间部分** (时/分/秒/亚秒), 舍入后按规范 BalanceTimeDuration
// 平衡到天: 进位的上限是 duration 的自然最大单位与 "day" 的较小者
// (round-cross-unit-boundary: 1:59:59.9 expand→2H, 但 30D+23:59:60 只进到 31D,
// 不再进月; 而仅有秒单位时 59.9→60S 保持为 60S 不进分钟)。
//
// 返回值 ok=false 表示舍入结果超出 Duration 可表示范围 (IsValidDuration 失败),
// 调用方应抛 RangeError (throws-when-rounded-duration-is-invalid)。
func (t *TemporalDuration) ToStringWithPrecision(digits int, quantum int64, mode string) (string, bool) {
	// 整个时间部分折算为精确纳秒 (big.Int, 避免 float64 大值溢出/丢精度)。
	timeNanos := new(big.Int)
	addField := func(v int64, per int64) {
		if v != 0 {
			timeNanos.Add(timeNanos, new(big.Int).Mul(big.NewInt(v), big.NewInt(per)))
		}
	}
	addField(t.hours, NanosPerHour)
	addField(t.minutes, NanosPerMinute)
	addField(t.seconds, NanosPerSecond)
	addField(t.milliseconds, NanosPerMillisecond)
	addField(t.microseconds, NanosPerMicrosecond)
	addField(t.nanoseconds, 1)

	if quantum > 0 {
		r := RoundNanosSigned(timeNanos, big.NewInt(quantum), mode)
		neg := t.Sign() < 0
		if neg && r.Sign() > 0 {
			r.SetInt64(0)
		}
		if !neg && r.Sign() < 0 {
			r.SetInt64(0)
		}
		timeNanos = r
	}

	// 平衡顶: duration 的自然最大非零单位, clamp 到 "day" (规范 BalanceTimeDuration
	// 的 largestUnit 不超过 day, 故时间部分最多进到天, 不进周/月/年)。
	top := t.naturalLargestUnit()
	days := t.days
	switch top {
	case "year", "month", "week", "day":
		// 允许整个时间部分进到天。
		dayNanos := big.NewInt(NanosPerDay)
		extra, rem := new(big.Int), new(big.Int)
		extra.QuoRem(timeNanos, dayNanos, rem)
		timeNanos = rem
		days += extra.Int64()
		top = "day"
	}

	// 范围校验: 舍入可能把秒推过 2^53 秒 (IsValidDuration 上限),
	// 此时须抛 RangeError。用整数纳秒精确比较, 避免 float64 边界舍入。
	{
		grand := new(big.Int).Mul(big.NewInt(days), big.NewInt(NanosPerDay))
		grand.Add(grand, timeNanos)
		maxNs, _ := new(big.Int).SetString("9007199254740992000000000", 10) // 2^53 s
		if grand.CmpAbs(maxNs) >= 0 {
			return "", false
		}
	}

	// 自顶向下按 top 逐级拆分: 从 top 对应单位开始, 依次取模落到下一级。
	// top 之上的单位只出现一次 (小时可任意大), 之后的每级都规范取模。
	neg := timeNanos.Sign() < 0
	abs := new(big.Int).Abs(timeNanos)
	take := func(m int64) *big.Int { // abs / m 的商, 并把余数写回 abs
		q, r := new(big.Int).QuoRem(abs, big.NewInt(m), new(big.Int))
		abs = q
		return r
	}
	var ns, us, ms, secs, mins, hrs int64
	ns = take(1000).Int64()
	us = take(1000).Int64()
	ms = take(1000).Int64()
	switch top {
	case "year", "month", "week", "day", "hour":
		// 顶为小时或更粗: 分钟及秒规范拆分, 余值即小时 (可 >24)。
		secs = take(60).Int64()
		mins = take(60).Int64()
		hrs = abs.Int64()
	case "minute":
		// 顶为分钟: 秒规范拆分, 余值全归分钟 (不进小时)。
		secs = take(60).Int64()
		mins = abs.Int64()
	default: // second 及更细: 不向上平衡, 秒即全部余值。
		secs = abs.Int64()
	}
	if neg {
		hrs, mins, secs = -hrs, -mins, -secs
		ms, us, ns = -ms, -us, -ns
	}

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
	appendUnit(days, 'D')
	dateWritten := sb.Len() > prefixLen

	timeEmpty := hrs == 0 && mins == 0 && secs == 0 && ms == 0 && us == 0 && ns == 0
	// 有日期部分且时间全零且 precision=auto 时省略时间部分。
	if timeEmpty && dateWritten && digits < 0 {
		return sb.String(), true
	}
	sb.WriteByte('T')
	appendUnit(hrs, 'H')
	appendUnit(mins, 'M')

	if timeEmpty {
		// 时间全零但需写出: "0S" 或固定精度 "0.00...S"。
		if digits > 0 {
			sb.WriteString("0." + strings.Repeat("0", digits) + "S")
		} else {
			sb.WriteString("0S")
		}
		return sb.String(), true
	}

	if secs != 0 || ms != 0 || us != 0 || ns != 0 || digits >= 0 {
		s := secs
		if s < 0 {
			s = -s
		}
		f := ms*NanosPerMillisecond + us*NanosPerMicrosecond + ns
		if f < 0 {
			f = -f
		}
		if digits < 0 {
			if f == 0 {
				sb.WriteString(fmt.Sprintf("%dS", s))
			} else {
				d := strings.TrimRight(fmt.Sprintf("%09d", f), "0")
				sb.WriteString(fmt.Sprintf("%d.%sS", s, d))
			}
		} else if digits == 0 {
			sb.WriteString(fmt.Sprintf("%dS", s))
		} else {
			d := fmt.Sprintf("%09d", f)[:digits]
			sb.WriteString(fmt.Sprintf("%d.%sS", s, d))
		}
	}
	return sb.String(), true
}

// naturalLargestUnit 返回 duration 中最大的非零单位名 (rank 最高者)。
// 全零时返回 "nanosecond"。用于 toString 舍入后平衡的上限判定。
func (t *TemporalDuration) naturalLargestUnit() string {
	switch {
	case t.years != 0:
		return "year"
	case t.months != 0:
		return "month"
	case t.weeks != 0:
		return "week"
	case t.days != 0:
		return "day"
	case t.hours != 0:
		return "hour"
	case t.minutes != 0:
		return "minute"
	case t.seconds != 0:
		return "second"
	default:
		return "nanosecond"
	}
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
	// TimeTotalNanos 含天数部分，而天数已由 AddDateDuration 处理，
	// 这里只加日内时间，避免天数被计算两次。
	timeOnly := new(big.Int).Sub(d.TimeTotalNanos(),
		new(big.Int).Mul(big.NewInt(d.days), big.NewInt(NanosPerDay)))
	newDate, newTime, ok := AddTimeDuration(newDate, dt.Time, timeOnly)
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

// RelativeAnchor 是 Duration.total/round 的日历换算锚点。
//
// IsZoned 为真时 (带时区的相对起点)，一天的长度由当地日历日决定
// (夏令时切换日可能是 23/25 小时)，因此必须用 EpochNanos 差值；
// 为假时 (PlainDate/PlainDateTime)，一天恒等于 86400e9 纳秒。
type RelativeAnchor struct {
	Wall    ISODateTime
	IsZoned bool
	// TZ 非空表示 zoned 形态; 用于把墙钟端点换算成确切时刻 (含夏令时)。
	TZ string
	// Epoch 是 zoned 形态下墙钟 Wall 对应的确切时刻 (纳秒)。
	Epoch *big.Int
}

// EpochNanos 返回锚点的确切时刻。Plain 形态下按 UTC 墙钟折算。
func (a RelativeAnchor) EpochNanos() *big.Int {
	if a.Epoch != nil {
		return a.Epoch
	}
	return a.Wall.EpochNanos()
}

// addDateTimeEpoch 把 duration 加到锚点上，返回终点的确切时刻与墙钟。
func addDateTimeEpoch(rel RelativeAnchor, d *TemporalDuration) (*big.Int, bool) {
	target, ok := AddDateTime(rel.Wall, d, "constrain")
	if !ok {
		return nil, false
	}
	if rel.IsZoned && rel.TZ != "" {
		// zoned: 墙钟 → 确切时刻 (compatible 消歧)，夏令时日的真实长度
		// 才能体现在 epoch 差里。
		cands := GetPossibleInstantsFor(rel.TZ, target)
		ns, ok := DisambiguatePossibleInstants(cands, rel.TZ, target, "compatible")
		if !ok {
			return nil, false
		}
		return ns, true
	}
	return target.EpochNanos(), true
}

// TotalOfUnit 计算 Duration 按指定单位的总值 (浮点)。
//
// unit 为日历单位时必须提供 relativeTo，否则无法换算 (返回 ok=false);
// 这与 Temporal.Duration.prototype.total 抛 RangeError 的行为一致。
func TotalOfUnit(d *TemporalDuration, unit string, relativeTo *ISODateTime) (float64, bool) {
	if relativeTo == nil {
		return totalOfUnitAnchor(d, unit, RelativeAnchor{}, false)
	}
	return totalOfUnitAnchor(d, unit, RelativeAnchor{Wall: *relativeTo}, true)
}

// TotalOfUnitZoned 同 TotalOfUnit，但相对起点带时区 (zoned 形态)。
// 夏令时切换日以确切时刻差计算真实天数。
func TotalOfUnitZoned(d *TemporalDuration, unit string, wall ISODateTime, tz string, epoch *big.Int) (float64, bool) {
	return totalOfUnitAnchor(d, unit, RelativeAnchor{Wall: wall, IsZoned: true, TZ: tz, Epoch: epoch}, true)
}

// totalOfUnitAnchor 实现规范 TotalRelativeDuration。
//
// 对单位 U: 求相对起点 A 到 A+d 的区间中"整 U 的个数 n"与"最后一个
// 未满 U 的余量占该单位实际长度的比例"，返回 n + 余量/U 的实际长度。
//
// 关键: 有 relativeTo 时，**所有**单位都按 A→A+d 的区间算 —— 包括 day。
// 因为 d 里的年/月/周必须先在日历上落成具体天数 (1 年可能是 365/366 天)，
// 再除以单位长度。只有完全没有 relativeTo 时，精确单位才退回纳秒直除。
func totalOfUnitAnchor(d *TemporalDuration, unit string, rel RelativeAnchor, hasRel bool) (float64, bool) {
	if !hasRel {
		// 无 relativeTo: 仅当 duration 不含日历单位 (年/月/周) 且 unit 为精确单位
		// 时才可算; 否则日历单位无法落到具体长度 → 调用方抛 RangeError。
		if d.HasCalendarUnits() {
			return 0, false
		}
		if n, ok := TotalNanosPerUnit(unit); ok {
			total := d.TimeTotalNanos()
			f, _ := new(big.Float).SetInt(total).Float64()
			return f / n, true
		}
		return 0, false
	}
	targetEpoch, ok := addDateTimeEpoch(rel, d)
	if !ok {
		return 0, false
	}
	startEpoch := rel.EpochNanos()
	switch unit {
	case "day", "days":
		diff := new(big.Int).Sub(targetEpoch, startEpoch)
		f, _ := new(big.Float).SetInt(diff).Float64()
		return f / float64(NanosPerDay), true
	case "year", "years", "month", "months":
		wantYear := unit == "year" || unit == "years"
		sign := int64(1)
		if targetEpoch.Cmp(startEpoch) < 0 {
			sign = -1
		}
		// 逐单位逼近: 求最大的 n 使 relativeTo + n 个单位不越过 target。
		n := int64(0)
		for {
			next := addCalendarUnits(rel.Wall, n+sign, wantYear)
			if sign > 0 {
				if addCalendarUnitsEpoch(rel, next).Cmp(targetEpoch) > 0 {
					break
				}
			} else if addCalendarUnitsEpoch(rel, next).Cmp(targetEpoch) < 0 {
				break
			}
			n += sign
		}
		anchor := addCalendarUnits(rel.Wall, n, wantYear)
		anchorEpoch := addCalendarUnitsEpoch(rel, anchor)
		unitLen := new(big.Int).Sub(addCalendarUnitsEpoch(rel, addCalendarUnits(anchor, sign, wantYear)), anchorEpoch)
		rem := new(big.Int).Sub(targetEpoch, anchorEpoch)
		if unitLen.Sign() == 0 {
			return 0, false
		}
		ratio := new(big.Rat).SetFrac(rem, unitLen)
		ratio.Add(ratio, big.NewRat(n, 1))
		f, _ := ratio.Float64()
		return f, true
	case "week", "weeks":
		diff := new(big.Int).Sub(targetEpoch, startEpoch)
		f, _ := new(big.Float).SetInt(diff).Float64()
		return f / float64(7*NanosPerDay), true
	}
	// 小时及以下: 直接用精确部分纳秒 (不受日历单位影响)。
	if n, ok := TotalNanosPerUnit(unit); ok {
		total := d.TimeTotalNanos()
		f, _ := new(big.Float).SetInt(total).Float64()
		return f / n, true
	}
	return 0, false
}

// addCalendarUnitsEpoch 把"相对锚点加 n 个年/月"后的墙钟换算成确切时刻。
func addCalendarUnitsEpoch(rel RelativeAnchor, dt ISODateTime) *big.Int {
	if rel.IsZoned && rel.TZ != "" {
		cands := GetPossibleInstantsFor(rel.TZ, dt)
		ns, ok := DisambiguatePossibleInstants(cands, rel.TZ, dt, "compatible")
		if ok {
			return ns
		}
	}
	return dt.EpochNanos()
}

// addCalendarUnits 在 ISO 日期时间上加 n 个年 (wantYear) 或月。时间部分不变。
func addCalendarUnits(dt ISODateTime, n int64, wantYear bool) ISODateTime {
	var y, mo int32
	if wantYear {
		y, mo = int32(n), 0
	} else {
		y, mo = int32(n/12), int32(n%12)
	}
	d, ok := AddDateDuration(dt.Date, y, mo, 0, 0, "constrain")
	if !ok {
		return dt
	}
	return ISODateTime{Date: d, Time: dt.Time}
}

// RoundNanos 按 roundingMode 与 increment 对纳秒值取整。
// truncation 是返回细粟端 tolerant 与否的控制: 这里实现 halfExpand (四舍五入)。
// RoundNanos 实现规范 RoundNumberToIncrement 的有符号语义:
// 以 floor 为基准 (余数恒非负)，trunc 等价 floor、expand 等价 ceil，
// halfExpand/halfCeil 平局向上、halfTrunc/halfFloor 平局向下。
// 对负时长需要"按绝对值舍入再加符号"的调用方应先取反模式 (ceil↔floor、
// expand↔trunc、halfCeil↔halfFloor、halfExpand↔halfTrunc) 再传入正数。
func RoundNanos(total *big.Int, increment *big.Int, mode string) *big.Int {
	if increment == nil || increment.Sign() <= 0 {
		return new(big.Int).Set(total)
	}
	// DivMod 为欧几里得语义: q = floor(total/increment), 0 <= r < increment。
	q, r := new(big.Int).DivMod(total, increment, new(big.Int))
	if r.Sign() == 0 {
		return new(big.Int).Set(total)
	}
	twiceR := new(big.Int).Lsh(r, 1)
	cmp := twiceR.Cmp(increment)
	roundUp := false
	switch mode {
	case "ceil", "expand":
		roundUp = true
	case "halfCeil", "halfExpand":
		roundUp = cmp >= 0
	case "halfFloor", "halfTrunc":
		roundUp = cmp > 0
	case "halfEven":
		roundUp = cmp > 0 || (cmp == 0 && q.Bit(0) == 1)
	default: // floor, trunc
		roundUp = false
	}
	if roundUp {
		q.Add(q, big.NewInt(1))
	}
	return new(big.Int).Mul(q, increment)
}

// RoundNanosSigned 是 RoundNanos 的**有符号**版本: 直接实现规范的
// RoundNumberToIncrement —— roundingMode 的语义相对 0 / ±∞ 定义, 无需调用方
// 自己镜像模式。用于对**带符号**差值 (如 PlainTime/Instant 的 since/until) 取整;
// RoundNanos 继续服务那些以"正幅值"为前提的调用方 (toString 的墙钟字段等)。
//
// 负数时的差异 (以 expand 为例): RoundNanos 的 floor 基准会把 -1:59:59 在原位
// 向上凑成 -119 分钟 (向 0), 而 expand 要求离 0 更远即 -120 分钟。规范要求
// -1:59:59 在 minutes/expand 下平衡为 -2 小时 (round-cross-unit-boundary)。
func RoundNanosSigned(total *big.Int, increment *big.Int, mode string) *big.Int {
	if increment == nil || increment.Sign() <= 0 || total.Sign() == 0 {
		return new(big.Int).Set(total)
	}
	neg := total.Sign() < 0
	abs := new(big.Int).Abs(total)
	// 求 |total| 的 floor 商与余数。
	q, r := new(big.Int).DivMod(abs, increment, new(big.Int))
	if r.Sign() == 0 {
		return new(big.Int).Set(total)
	}
	cmp := new(big.Int).Lsh(r, 1).Cmp(increment)
	up := false // |total| 的商是否需要 q+1
	switch mode {
	case "ceil":
		// 朝 +∞: 负数向 0 (商不变), 正数远离 0 (q+1)。
		up = !neg
	case "floor":
		// 朝 −∞: 负数远离 0 (q+1), 正数向 0 (商不变)。
		up = neg
	case "expand":
		up = true
	case "trunc":
		up = false
	case "halfCeil":
		up = cmp > 0 || (cmp == 0 && !neg)
	case "halfFloor":
		up = cmp > 0 || (cmp == 0 && neg)
	case "halfExpand":
		up = cmp >= 0
	case "halfTrunc":
		up = cmp > 0
	case "halfEven":
		up = cmp > 0 || (cmp == 0 && q.Bit(0) == 1)
	default: // 缺省 trunc
		up = false
	}
	if up {
		q.Add(q, big.NewInt(1))
	}
	res := new(big.Int).Mul(q, increment)
	if neg {
		res.Neg(res)
	}
	return res
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

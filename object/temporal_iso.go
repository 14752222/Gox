package object

// 本文件实现 Temporal 的底层时间数学。
//
// 设计要点:
//
//  1. 不使用 Go 的 time.Time 作为内部表示。time.Time 的零值是公元 1 年、
//     纳秒范围只覆盖 1678–2262 年，而 Temporal 的合法范围是
//     ±10^8 天 (约 ±273790 年)，两者不兼容。
//
//  2. 不用单一 int64 表示纳秒 epoch。int64 纳秒同样只覆盖 1678–2262 年。
//     精确时间的内部表示统一为「epoch 天 + 日内纳秒」二元组:
//     天用 int64 (±10^8 绰绰有余)，日内纳秒用 int64 (0..86_400_000_000_000)。
//     只有需要对外暴露 epochNanoseconds / epochMilliseconds 时才合成 big.Int。
//
//  3. 墙钟时间的内部表示是 ISO 字段记录 (年/月/日/时/分/秒/毫秒/微秒/纳秒)，
//     与精确时间之间通过 UTC 换算，时区参与时才引入偏移。

import (
	"math"
	"math/big"
)

// ===== 单位常量 =====

const (
	NanosPerMicrosecond = 1000
	NanosPerMillisecond = 1000 * NanosPerMicrosecond
	NanosPerSecond      = 1000 * NanosPerMillisecond
	NanosPerMinute      = 60 * NanosPerSecond
	NanosPerHour        = 60 * NanosPerMinute
	NanosPerDay         = 24 * NanosPerHour // 86_400_000_000_000
)

// Temporal 的合法时间范围: ±10^8 天 (ECMAScript 规范定义的
// 最大时间跨度，约 ±273790 年)。超出即 RangeError。
const (
	MinEpochDays = -100000000
	MaxEpochDays = 100000000
)

// ===== ISO 字段记录 =====

// ISODate 表示 ISO 8601 (proleptic Gregorian，含 0 年与负年) 的日期。
type ISODate struct {
	Year  int32 // 可为 0 或负 (0 表示公元前 1 年)
	Month int32 // 1-12
	Day   int32 // 1-31
}

// ISOTime 表示一天内的墙钟时间，精度到纳秒。
type ISOTime struct {
	Hour        int32 // 0-23
	Minute      int32 // 0-59
	Second      int32 // 0-59 (规范允许 60 仅用于闰秒约定，这里按 0-59)
	Millisecond int32 // 0-999
	Microsecond int32 // 0-999
	Nanosecond  int32 // 0-999
}

// ISODateTime 是日期与时间的组合，即 Temporal 内部所谓的 ISO Date-Time Record。
type ISODateTime struct {
	Date ISODate
	Time ISOTime
}

// NanosOfDay 返回该时间在当天内的纳秒偏移 [0, NanosPerDay)。
func (t ISOTime) NanosOfDay() int64 {
	return int64(t.Nanosecond) +
		int64(t.Microsecond)*NanosPerMicrosecond +
		int64(t.Millisecond)*NanosPerMillisecond +
		int64(t.Second)*NanosPerSecond +
		int64(t.Minute)*NanosPerMinute +
		int64(t.Hour)*NanosPerHour
}

// TimeFromNanosOfDay 由日内纳秒偏移还原时间字段。
func TimeFromNanosOfDay(nanos int64) ISOTime {
	nanos = balanceMod(nanos, NanosPerDay)
	h := nanos / NanosPerHour
	nanos -= h * NanosPerHour
	mi := nanos / NanosPerMinute
	nanos -= mi * NanosPerMinute
	s := nanos / NanosPerSecond
	nanos -= s * NanosPerSecond
	ms := nanos / NanosPerMillisecond
	nanos -= ms * NanosPerMillisecond
	us := nanos / NanosPerMicrosecond
	nanos -= us * NanosPerMicrosecond
	return ISOTime{
		Hour: int32(h), Minute: int32(mi), Second: int32(s),
		Millisecond: int32(ms), Microsecond: int32(us), Nanosecond: int32(nanos),
	}
}

// balanceMod 返回 value 对 m 的非负余数 (欧几里得取模)。
// Go 的 % 对被除数为负时结果跟随被除数的符号，与 ECMAScript 的
// 平衡算法 (需非负余数) 不一致。
func balanceMod(value, m int64) int64 {
	r := value % m
	if r < 0 {
		r += m
	}
	return r
}

// ===== 民用日期 ↔ epoch 天 =====
//
// 使用 Howard Hinnant 的 days_from_civil / civil_from_days 算法:
// 纯整数运算、无查表、对负年 (公元前) 同样正确，且不涉及 time.Time
// 的年份限制。算法基于 400 年 = 146097 天的格里高利周期。

// DaysFromCivil 将 (年, 月, 日) 转换为 1970-01-01 起的天数。
func DaysFromCivil(y, m, d int32) int64 {
	yy := int64(y)
	mm := int64(m)
	dd := int64(d)
	if mm <= 2 {
		yy--
	}
	era := yy / 400
	if yy < 0 {
		era = (yy - 399) / 400
	}
	yoe := yy - era*400                           // [0, 399]
	doy := (153*(mm+shiftMonth(mm))+2)/5 + dd - 1 // [0, 365]
	doe := yoe*365 + yoe/4 - yoe/100 + doy        // [0, 146096]
	return era*146097 + int64(doe) - 719468
}

// shiftMonth 是算法中的月份平移: 3月→0, ..., 12月→9, 1月→10, 2月→11。
func shiftMonth(m int64) int64 {
	if m > 2 {
		return -3
	}
	return 9
}

// CivilFromDays 将 epoch 天数还原为 (年, 月, 日)。
func CivilFromDays(z int64) ISODate {
	zz := z + 719468
	era := zz / 146097
	if zz < 0 {
		era = (zz - 146096) / 146097
	}
	doe := zz - era*146097                                 // [0, 146096]
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365 // [0, 399]
	y := yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100) // [0, 365]
	mp := (5*doy + 2) / 153                  // [0, 11]
	d := doy - (153*mp+2)/5 + 1              // [1, 31]
	m := mp + 3                              // [3, 14]
	if mp >= 10 {
		m = mp - 9 // [1, 2]
	}
	yy := y
	if m <= 2 {
		yy++
	}
	return ISODate{Year: int32(yy), Month: int32(m), Day: int32(d)}
}

// EpochDays 返回 ISODate 对应的 epoch 天数。
func (d ISODate) EpochDays() int64 { return DaysFromCivil(d.Year, d.Month, d.Day) }

// ISODateFromEpochDays 由 epoch 天数构造 ISODate。
func ISODateFromEpochDays(ed int64) ISODate { return CivilFromDays(ed) }

// ===== ISO 日历辅助 =====

// IsLeapYear 判断是否为闰年 (格里高利规则)。
func IsLeapYear(y int32) bool {
	if y%4 != 0 {
		return false
	}
	if y%100 != 0 {
		return true
	}
	return y%400 == 0
}

// DaysInMonth 返回指定年月的天数 (28/29/30/31)。
func DaysInMonth(y, m int32) int32 {
	switch m {
	case 1, 3, 5, 7, 8, 10, 12:
		return 31
	case 4, 6, 9, 11:
		return 30
	case 2:
		if IsLeapYear(y) {
			return 29
		}
		return 28
	}
	return 0 // 非法月份
}

// DaysInYear 返回年份天数 (365 或 366)。
func DaysInYear(y int32) int32 {
	if IsLeapYear(y) {
		return 366
	}
	return 365
}

// DayOfWeek 返回星期几: 1 = 星期一 ... 7 = 星期日 (ISO 8601 约定)。
func (d ISODate) DayOfWeek() int32 {
	ed := d.EpochDays()
	// 1970-01-01 是星期四 (4)
	return int32(balanceMod(ed+3, 7)) + 1
}

// DayOfYear 返回年内第几天 (1-based)。
func (d ISODate) DayOfYear() int32 {
	firstOfYear := DaysFromCivil(d.Year, 1, 1)
	return int32(d.EpochDays()-firstOfYear) + 1
}

// WeekOfYear 返回 ISO 周序号 (1-53)。
// ISO 8601 规则: 第 1 周是包含当年第一个星期四的那一周。
func (d ISODate) WeekOfYear() int32 {
	// 当年的星期四所在的周即为第 1 周: 先找到该周周一，再算周差。
	thu := d.EpochDays() - int64(d.DayOfWeek()) + 4
	year := CivilFromDays(thu).Year
	firstThu := DaysFromCivil(year, 1, 4)
	firstMonday := firstThu - int64(ISODateFromEpochDays(firstThu).DayOfWeek()) + 1
	week := (d.EpochDays()-firstMonday)/7 + 1
	return int32(week)
}

// InLeapYear 判断该日期所在年是否为闰年。
func (d ISODate) InLeapYear() bool { return IsLeapYear(d.Year) }

// ===== 范围校验 =====

// ISODateTimeToEpochNanos 把 ISO 字段记录 (按 UTC 解释) 转为 epoch 纳秒。
func (dt ISODateTime) EpochNanos() *big.Int {
	days := dt.Date.EpochDays()
	nanos := days*NanosPerDay + dt.Time.NanosOfDay()
	return big.NewInt(nanos)
}

// EpochNanosToISODateTime 由 epoch 纳秒还原 UTC 下的 ISO 字段记录。
func EpochNanosToISODateTime(ns *big.Int) ISODateTime {
	// 拆成 (天, 日内纳秒)，避免中间值溢出 int64
	days, rem := quoRemBig(ns, NanosPerDay)
	return ISODateTime{
		Date: ISODateFromEpochDays(days),
		Time: TimeFromNanosOfDay(rem),
	}
}

// quoRemBig 返回 ns 对 m 的截断商与欧几里得余数 (余数非负)。
// 这是「天 + 日内纳秒」二元组的分解入口: 商是 epoch 天，余数是日内纳秒。
func quoRemBig(ns *big.Int, m int64) (int64, int64) {
	mm := big.NewInt(m)
	q, r := new(big.Int).QuoRem(ns, mm, new(big.Int))
	// Quo 是截断商，Rem 跟随被除数符号；转成天数为向下取整 + 非负余数。
	if r.Sign() < 0 {
		q.Sub(q, big.NewInt(1))
		r.Add(r, mm)
	}
	return q.Int64(), r.Int64()
}

// IsValidEpochDays 检查 epoch 天是否在 Temporal 的合法范围内。
func IsValidEpochDays(ed int64) bool { return ed >= MinEpochDays && ed <= MaxEpochDays }

// IsValidEpochNanos 检查 epoch 纳秒是否在合法范围内。
//
// 边界值必须用 big.Int 乘法构造: ±10^8 天 × 86400e9 纳秒 = ±8.64e21，
// 远超 int64 上限 (9.22e18)，编译期常量表达式会直接溢出。
var (
	minEpochNanos = new(big.Int).Mul(big.NewInt(MinEpochDays), big.NewInt(NanosPerDay))
	maxEpochNanos = new(big.Int).Mul(big.NewInt(MaxEpochDays), big.NewInt(NanosPerDay))
)

func IsValidEpochNanos(ns *big.Int) bool {
	return ns.Cmp(minEpochNanos) >= 0 && ns.Cmp(maxEpochNanos) <= 0
}

// IsValidISODate 检查 ISO 日期字段是否构成合法日期。
func IsValidISODate(d ISODate) bool {
	if d.Month < 1 || d.Month > 12 {
		return false
	}
	if d.Day < 1 {
		return false
	}
	return d.Day <= DaysInMonth(d.Year, d.Month)
}

// IsValidISOTime 检查时间字段是否在合法区间。
func IsValidISOTime(t ISOTime) bool {
	return t.Hour >= 0 && t.Hour <= 23 &&
		t.Minute >= 0 && t.Minute <= 59 &&
		t.Second >= 0 && t.Second <= 59 &&
		t.Millisecond >= 0 && t.Millisecond <= 999 &&
		t.Microsecond >= 0 && t.Microsecond <= 999 &&
		t.Nanosecond >= 0 && t.Nanosecond <= 999
}

// ===== 约束与溢出 =====

// RegulateISODate 按 overflow 策略规约日期字段。
//
// constrain: 把越界的月/日夹到合法区间 (1/31 → 1/31，2/31 → 2/28)。
// reject:    越界即返回 ok=false，由调用方抛 RangeError。
func RegulateISODate(y, m, d int32, overflow string) (ISODate, bool) {
	if overflow == "reject" {
		out := ISODate{Year: y, Month: m, Day: d}
		return out, IsValidISODate(out)
	}
	mm := clampInt32(m, 1, 12)
	dd := clampInt32(d, 1, DaysInMonth(y, mm))
	return ISODate{Year: y, Month: mm, Day: dd}, true
}

// RegulateISOTime 按 overflow 策略规约时间字段 (支持 24:00:00 → 00:00:00)。
func RegulateISOTime(h, mi, s, ms, us, ns int32, overflow string) (ISOTime, bool) {
	if h == 24 && mi == 0 && s == 0 && ms == 0 && us == 0 && ns == 0 {
		if overflow == "reject" {
			return ISOTime{}, false
		}
		return ISOTime{}, true
	}
	t := ISOTime{h, mi, s, ms, us, ns}
	if !IsValidISOTime(t) {
		if overflow == "reject" {
			return ISOTime{}, false
		}
		t.Hour = clampInt32(h, 0, 23)
		t.Minute = clampInt32(mi, 0, 59)
		t.Second = clampInt32(s, 0, 59)
		t.Millisecond = clampInt32(ms, 0, 999)
		t.Microsecond = clampInt32(us, 0, 999)
		t.Nanosecond = clampInt32(ns, 0, 999)
	}
	return t, true
}

// floorDiv 返回 a/b 的向下取整结果 (区别于 Go 原生向零取整)。
// Temporal 的日期运算跨公元前/跨负偏移时依赖向下取整语义。
func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func clampInt32(v, lo, hi int32) int32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// ===== Duration 相关的时间数学 =====

// AddDateDuration 在 ISO 日期上加上 (年, 月, 周, 日)，按 overflow 处理月末对齐。
//
// 月末对齐语义: 1月31日 + 1个月 = 2月28/29日 (constrain)，
// 而 reject 模式下若结果日超出目标月天数则失败。
func AddDateDuration(d ISODate, years, months, weeks, days int32, overflow string) (ISODate, bool) {
	y := int64(d.Year) + int64(years)
	m := int64(d.Month) + int64(months)
	// 先把月份规约到 1-12，年做进位。
	// 必须用向下取整的除法: Go 的整数除法向零取整，负月 (跨年往前) 会算错。
	y += floorDiv(m-1, 12)
	m = balanceMod(m-1, 12) + 1

	maxDay := int64(DaysInMonth(int32(y), int32(m)))
	day := int64(d.Day)
	if overflow == "reject" {
		if day > maxDay {
			return ISODate{}, false
		}
	} else if day > maxDay {
		day = maxDay
	}
	out := ISODate{Year: int32(y), Month: int32(m), Day: int32(day)}
	// 周与日直接折算成天数加在 epoch 天上，天然处理月末/跨年
	ed := out.EpochDays() + int64(weeks)*7 + int64(days)
	if !IsValidEpochDays(ed) {
		return ISODate{}, false
	}
	return ISODateFromEpochDays(ed), true
}

// DiffISODate 计算 from 到 to 的日期差 (按最大单位 largestUnit 截断)。
// 返回 (年, 月, 周, 日)。
func DiffISODate(from, to ISODate, largestUnit string) (years, months, weeks, days int32) {
	switch largestUnit {
	case "year", "month":
		// 逐月累加直到逼近，再算剩余天数
		sign := int32(1)
		if to.EpochDays() < from.EpochDays() {
			sign = -1
		}
		cur := from
		totalMonths := int32(0)
		for {
			next, _ := AddDateDuration(cur, 0, sign, 0, 0, "constrain")
			if (sign > 0 && next.EpochDays() > to.EpochDays()) ||
				(sign < 0 && next.EpochDays() < to.EpochDays()) {
				break
			}
			cur = next
			totalMonths += sign
		}
		remDays := to.EpochDays() - cur.EpochDays()
		if largestUnit == "year" {
			years = totalMonths / 12
			months = totalMonths % 12
		} else {
			months = totalMonths
		}
		days = int32(remDays)
		return years, months, 0, days
	case "week":
		totalDays := to.EpochDays() - from.EpochDays()
		weeks = int32(totalDays / 7)
		days = int32(totalDays % 7)
		return 0, 0, weeks, days
	default: // day
		return 0, 0, 0, int32(to.EpochDays() - from.EpochDays())
	}
}

// EpochNanosToMilliseconds 取 epoch 纳秒对应的毫秒 (用于 epochMilliseconds)。
// 纳秒转毫秒是截断除法 (向零取整)。
func EpochNanosToMilliseconds(ns *big.Int) int64 {
	q, _ := new(big.Int).QuoRem(ns, big.NewInt(NanosPerMillisecond), new(big.Int))
	return q.Int64()
}

// NanosToFloatMs 把纳秒差转为毫秒浮点 (用于相对时间格式化等场景)。
func NanosToFloatMs(ns *big.Int) float64 {
	f, _ := new(big.Float).SetInt(ns).Float64()
	return f / float64(NanosPerMillisecond)
}

// CompareNanos 比较两个 big.Int 纳秒值。
func CompareNanos(a, b *big.Int) int { return a.Cmp(b) }

// maxNanos / minNanos 取两个纳秒值的较大/较小者。
func maxNanos(a, b *big.Int) *big.Int {
	if a.Cmp(b) > 0 {
		return new(big.Int).Set(a)
	}
	return new(big.Int).Set(b)
}

func minNanos(a, b *big.Int) *big.Int {
	if a.Cmp(b) < 0 {
		return new(big.Int).Set(a)
	}
	return new(big.Int).Set(b)
}

// isValidDurationField 检查 Duration 各字段是否为规范允许的有限整数。
// 规范约束: 所有字段都是整数，且总时长 |ns| < 2^96。
func isValidDurationField(f float64) bool {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return false
	}
	return f == math.Trunc(f)
}

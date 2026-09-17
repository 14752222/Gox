package object

// 本文件定义 Temporal 的值类型与其 ISO 字符串表示。
//
// 对象模型选择: 与 Map/Set/Promise 一致，各 Temporal 类型是独立 struct 而非
// *Object。原因是 Temporal 的内部状态 (epoch 纳秒、ISO 字段、时区标识符)
// 必须对 JS 层完全隐藏——若作为普通属性存进 Object.Properties，
// Object.keys() / 展开运算符 / JSON.stringify 都会把它们泄漏出去。
//
// 所有 Temporal 类型都是不可变的: 运算返回新实例，SetProperty 静默失败。

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"
)

// ===== 原型管理 =====

// temporalProtos 保存各 Temporal 类型的全局原型对象，由 stdlib 初始化。
var temporalProtos = map[ObjectType]Value{}

// SetTemporalProto 注册某个 Temporal 类型的原型 (由 stdlib 调用)。
func SetTemporalProto(t ObjectType, p Value) { temporalProtos[t] = p }

// GetTemporalProto 返回某个 Temporal 类型的原型。
func GetTemporalProto(t ObjectType) Value {
	if v, ok := temporalProtos[t]; ok {
		return v
	}
	return nil
}

// protoLookup 是各 Temporal 类型 GetProperty 的统一实现。
//
// receiver 必须是 Temporal 实例本身，不能省略: 原型上大量的日历/墙钟字段
// (year/month/day/hour/...) 以及 Symbol.toStringTag 都以 Accessor (getter)
// 形式注册，必须用实例作为 this 才能取到各自的内部数据。若退化成
// proto.GetProperty(name)，Object.getProperty 会把 receiver 绑定成原型对象。
func protoLookup(t ObjectType, name string, receiver Value) (Value, bool) {
	p := GetTemporalProto(t)
	if p == nil {
		return nil, false
	}
	if o, ok := p.(*Object); ok {
		return o.getProperty(name, receiver)
	}
	return p.GetProperty(name)
}

// symbolDispatch 是 GetSymbolProperty 的统一实现 (供 Symbol.toStringTag 等)。
func symbolDispatch(t ObjectType, sym *Symbol) (Value, bool) {
	p := GetTemporalProto(t)
	if p == nil {
		return nil, false
	}
	// Value 接口不含 Symbol 键访问 (只有 *Object 实现了 SymbolProperties)，
	// 而 Temporal 的原型一律由 stdlib 建为 *Object，故这里只做这一种转换。
	if o, ok := p.(*Object); ok {
		return o.GetSymbolProperty(sym)
	}
	return nil, false
}

// ===== Temporal.Instant =====

// TemporalInstant 表示一个精确的时间点 (自 epoch 起的纳秒数)。
//
// Instant 与时区无关: 不含任何墙钟字段，toString 固定以 UTC 加 Z 输出。
type TemporalInstant struct {
	nanos *big.Int
}

// NewTemporalInstant 基于 epoch 纳秒构造 Instant。
func NewTemporalInstant(nanos *big.Int) *TemporalInstant {
	return &TemporalInstant{nanos: new(big.Int).Set(nanos)}
}

// NewTemporalInstantFromMs 基于 epoch 毫秒构造 Instant。
func NewTemporalInstantFromMs(ms int64) *TemporalInstant {
	return &TemporalInstant{nanos: new(big.Int).Mul(big.NewInt(ms), big.NewInt(NanosPerMillisecond))}
}

// Nanos 返回内部 epoch 纳秒 (调用方不应修改返回值)。
func (t *TemporalInstant) Nanos() *big.Int { return new(big.Int).Set(t.nanos) }

// DateTimeUTC 返回该 Instant 在 UTC 下的 ISO 字段。
func (t *TemporalInstant) DateTimeUTC() ISODateTime { return EpochNanosToISODateTime(t.nanos) }

// EpochMilliseconds 返回 epoch 毫秒。
func (t *TemporalInstant) EpochMilliseconds() int64 { return EpochNanosToMilliseconds(t.nanos) }

func (t *TemporalInstant) Type() ObjectType { return TEMPORAL_INSTANT_OBJ }
func (t *TemporalInstant) IsTruthy() bool   { return true }
func (t *TemporalInstant) Inspect() string  { return t.ToString() }

// ToString 输出 ISO 8601 扩展格式，固定 UTC 并带 Z。
func (t *TemporalInstant) ToString() string {
	dt := EpochNanosToISODateTime(t.nanos)
	return formatISODateTime(dt) + "Z"
}

func (t *TemporalInstant) GetProperty(name string) (Value, bool) {
	return protoLookup(TEMPORAL_INSTANT_OBJ, name, t)
}
func (t *TemporalInstant) SetProperty(name string, val Value) {}
func (t *TemporalInstant) GetSymbolProperty(sym *Symbol) (Value, bool) {
	return symbolDispatch(TEMPORAL_INSTANT_OBJ, sym)
}
func (t *TemporalInstant) SetSymbolProperty(sym *Symbol, val Value) {}
func (t *TemporalInstant) GetProto() Value                          { return GetTemporalProto(TEMPORAL_INSTANT_OBJ) }

// ===== Temporal.PlainDateTime =====

// TemporalPlainDateTime 表示不含时区的日期时间组合 (墙钟时间)。
type TemporalPlainDateTime struct {
	dt       ISODateTime
	calendar string
}

func NewTemporalPlainDateTime(dt ISODateTime, calendar string) *TemporalPlainDateTime {
	if calendar == "" {
		calendar = DefaultCalendarID
	}
	return &TemporalPlainDateTime{dt: dt, calendar: calendar}
}

func (t *TemporalPlainDateTime) ISODateTime() ISODateTime { return t.dt }
func (t *TemporalPlainDateTime) CalendarID() string       { return t.calendar }

func (t *TemporalPlainDateTime) Type() ObjectType { return TEMPORAL_PLAIN_DATETIME_OBJ }
func (t *TemporalPlainDateTime) IsTruthy() bool   { return true }
func (t *TemporalPlainDateTime) Inspect() string  { return t.ToString() }

func (t *TemporalPlainDateTime) ToString() string { return formatISODateTime(t.dt) }

func (t *TemporalPlainDateTime) GetProperty(name string) (Value, bool) {
	return protoLookup(TEMPORAL_PLAIN_DATETIME_OBJ, name, t)
}
func (t *TemporalPlainDateTime) SetProperty(name string, val Value) {}
func (t *TemporalPlainDateTime) GetSymbolProperty(sym *Symbol) (Value, bool) {
	return symbolDispatch(TEMPORAL_PLAIN_DATETIME_OBJ, sym)
}
func (t *TemporalPlainDateTime) SetSymbolProperty(sym *Symbol, val Value) {}
func (t *TemporalPlainDateTime) GetProto() Value {
	return GetTemporalProto(TEMPORAL_PLAIN_DATETIME_OBJ)
}

// ===== Temporal.PlainDate =====

// TemporalPlainDate 表示不含时区的日历日期。
type TemporalPlainDate struct {
	date     ISODate
	calendar string
}

func NewTemporalPlainDate(date ISODate, calendar string) *TemporalPlainDate {
	if calendar == "" {
		calendar = DefaultCalendarID
	}
	return &TemporalPlainDate{date: date, calendar: calendar}
}

func (t *TemporalPlainDate) ISODate() ISODate   { return t.date }
func (t *TemporalPlainDate) CalendarID() string { return t.calendar }

func (t *TemporalPlainDate) Type() ObjectType { return TEMPORAL_PLAIN_DATE_OBJ }
func (t *TemporalPlainDate) IsTruthy() bool   { return true }
func (t *TemporalPlainDate) Inspect() string  { return t.ToString() }

func (t *TemporalPlainDate) ToString() string { return formatISODate(t.date) }

func (t *TemporalPlainDate) GetProperty(name string) (Value, bool) {
	return protoLookup(TEMPORAL_PLAIN_DATE_OBJ, name, t)
}
func (t *TemporalPlainDate) SetProperty(name string, val Value) {}
func (t *TemporalPlainDate) GetSymbolProperty(sym *Symbol) (Value, bool) {
	return symbolDispatch(TEMPORAL_PLAIN_DATE_OBJ, sym)
}
func (t *TemporalPlainDate) SetSymbolProperty(sym *Symbol, val Value) {}
func (t *TemporalPlainDate) GetProto() Value                          { return GetTemporalProto(TEMPORAL_PLAIN_DATE_OBJ) }

// ===== Temporal.PlainTime =====

// TemporalPlainTime 表示一天内的墙钟时间，与日期和时区无关。
type TemporalPlainTime struct {
	time ISOTime
}

func NewTemporalPlainTime(time ISOTime) *TemporalPlainTime { return &TemporalPlainTime{time: time} }

func (t *TemporalPlainTime) ISOTime() ISOTime { return t.time }

func (t *TemporalPlainTime) Type() ObjectType { return TEMPORAL_PLAIN_TIME_OBJ }
func (t *TemporalPlainTime) IsTruthy() bool   { return true }
func (t *TemporalPlainTime) Inspect() string  { return t.ToString() }

func (t *TemporalPlainTime) ToString() string { return formatISOTime(t.time, "auto") }

func (t *TemporalPlainTime) GetProperty(name string) (Value, bool) {
	return protoLookup(TEMPORAL_PLAIN_TIME_OBJ, name, t)
}
func (t *TemporalPlainTime) SetProperty(name string, val Value) {}
func (t *TemporalPlainTime) GetSymbolProperty(sym *Symbol) (Value, bool) {
	return symbolDispatch(TEMPORAL_PLAIN_TIME_OBJ, sym)
}
func (t *TemporalPlainTime) SetSymbolProperty(sym *Symbol, val Value) {}
func (t *TemporalPlainTime) GetProto() Value                          { return GetTemporalProto(TEMPORAL_PLAIN_TIME_OBJ) }

// ===== Temporal.PlainYearMonth =====

// TemporalPlainYearMonth 表示一个日历年月的实例 (如 "2020-01" 代表的整个一月)。
//
// refDay 是内部参考日: PlainYearMonth 内部以完整日期存储，参考日决定黄瓜
// daysInMonth 的归属月份。默认情况下 (参考日 1) 代表该月的第一天。
type TemporalPlainYearMonth struct {
	year, month, refDay int32
	calendar            string
}

func NewTemporalPlainYearMonth(y, m, refDay int32, calendar string) *TemporalPlainYearMonth {
	if calendar == "" {
		calendar = DefaultCalendarID
	}
	if refDay < 1 {
		refDay = 1
	}
	return &TemporalPlainYearMonth{year: y, month: m, refDay: refDay, calendar: calendar}
}

func (t *TemporalPlainYearMonth) Year() int32        { return t.year }
func (t *TemporalPlainYearMonth) Month() int32       { return t.month }
func (t *TemporalPlainYearMonth) RefDay() int32      { return t.refDay }
func (t *TemporalPlainYearMonth) CalendarID() string { return t.calendar }

func (t *TemporalPlainYearMonth) Type() ObjectType { return TEMPORAL_PLAIN_YM_OBJ }
func (t *TemporalPlainYearMonth) IsTruthy() bool   { return true }
func (t *TemporalPlainYearMonth) Inspect() string  { return t.ToString() }

func (t *TemporalPlainYearMonth) ToString() string {
	if t.year >= 0 && t.year <= 9999 {
		return fmt.Sprintf("%04d-%02d", t.year, t.month)
	}
	return formatISOYear(t.year) + fmt.Sprintf("-%02d", t.month)
}

func (t *TemporalPlainYearMonth) GetProperty(name string) (Value, bool) {
	return protoLookup(TEMPORAL_PLAIN_YM_OBJ, name, t)
}
func (t *TemporalPlainYearMonth) SetProperty(name string, val Value) {}
func (t *TemporalPlainYearMonth) GetSymbolProperty(sym *Symbol) (Value, bool) {
	return symbolDispatch(TEMPORAL_PLAIN_YM_OBJ, sym)
}
func (t *TemporalPlainYearMonth) SetSymbolProperty(sym *Symbol, val Value) {}
func (t *TemporalPlainYearMonth) GetProto() Value                          { return GetTemporalProto(TEMPORAL_PLAIN_YM_OBJ) }

// ===== Temporal.PlainMonthDay =====

// TemporalPlainMonthDay 表示日历中的一个"月-日" (如"每年 3 月 1 日")。
// refYear 用于把 02-29 这类日期定位到某个具体的闰年。
type TemporalPlainMonthDay struct {
	month, day, refYear int32
	calendar            string
}

func NewTemporalPlainMonthDay(m, d, refYear int32, calendar string) *TemporalPlainMonthDay {
	if calendar == "" {
		calendar = DefaultCalendarID
	}
	return &TemporalPlainMonthDay{month: m, day: d, refYear: refYear, calendar: calendar}
}

func (t *TemporalPlainMonthDay) Month() int32       { return t.month }
func (t *TemporalPlainMonthDay) Day() int32         { return t.day }
func (t *TemporalPlainMonthDay) RefYear() int32     { return t.refYear }
func (t *TemporalPlainMonthDay) CalendarID() string { return t.calendar }

func (t *TemporalPlainMonthDay) Type() ObjectType { return TEMPORAL_PLAIN_MD_OBJ }
func (t *TemporalPlainMonthDay) IsTruthy() bool   { return true }
func (t *TemporalPlainMonthDay) Inspect() string  { return t.ToString() }

func (t *TemporalPlainMonthDay) ToString() string {
	return fmt.Sprintf("%02d-%02d", t.month, t.day)
}

func (t *TemporalPlainMonthDay) GetProperty(name string) (Value, bool) {
	return protoLookup(TEMPORAL_PLAIN_MD_OBJ, name, t)
}
func (t *TemporalPlainMonthDay) SetProperty(name string, val Value) {}
func (t *TemporalPlainMonthDay) GetSymbolProperty(sym *Symbol) (Value, bool) {
	return symbolDispatch(TEMPORAL_PLAIN_MD_OBJ, sym)
}
func (t *TemporalPlainMonthDay) SetSymbolProperty(sym *Symbol, val Value) {}
func (t *TemporalPlainMonthDay) GetProto() Value                          { return GetTemporalProto(TEMPORAL_PLAIN_MD_OBJ) }

// ===== Temporal.ZonedDateTime =====

// TemporalZonedDateTime 是「精确时间 + 时区 + 日历」三元组。
//
// 内部以 epoch 纳秒为准，时区与日历仅影响Getting墙钟字段与格式化。
// 这是 Temporal 修复 Date 的核心设计: 时区显式携带，而非隐含在宿主本地时区。
type TemporalZonedDateTime struct {
	nanos    *big.Int
	timeZone string
	calendar string
}

func NewTemporalZonedDateTime(nanos *big.Int, timeZone, calendar string) *TemporalZonedDateTime {
	if calendar == "" {
		calendar = DefaultCalendarID
	}
	return &TemporalZonedDateTime{nanos: new(big.Int).Set(nanos), timeZone: timeZone, calendar: calendar}
}

func (t *TemporalZonedDateTime) Nanos() *big.Int    { return new(big.Int).Set(t.nanos) }
func (t *TemporalZonedDateTime) TimeZoneID() string { return t.timeZone }
func (t *TemporalZonedDateTime) CalendarID() string { return t.calendar }

func (t *TemporalZonedDateTime) Type() ObjectType { return TEMPORAL_ZONED_DATETIME_OBJ }
func (t *TemporalZonedDateTime) IsTruthy() bool   { return true }
func (t *TemporalZonedDateTime) Inspect() string  { return t.ToString() }

// ToString 输出 ISO 8601 带偏移与时区注解的形式:
// 2020-01-01T09:00:00+09:00[Asia/Tokyo]
func (t *TemporalZonedDateTime) ToString() string {
	off := TimeZoneOffsetSecondsAt(t.timeZone, t.nanos)
	dt := AddOffsetToISODateTime(EpochNanosToISODateTime(t.nanos), off)
	s := formatISODateTime(dt)
	if off != 0 {
		s += formatUTCOffset(off)
	} else {
		s += "Z"
	}
	if !IsOffsetOnlyTimeZone(t.timeZone) {
		s += "[" + t.timeZone + "]"
	}
	return s
}

func (t *TemporalZonedDateTime) GetProperty(name string) (Value, bool) {
	return protoLookup(TEMPORAL_ZONED_DATETIME_OBJ, name, t)
}
func (t *TemporalZonedDateTime) SetProperty(name string, val Value) {}
func (t *TemporalZonedDateTime) GetSymbolProperty(sym *Symbol) (Value, bool) {
	return symbolDispatch(TEMPORAL_ZONED_DATETIME_OBJ, sym)
}
func (t *TemporalZonedDateTime) SetSymbolProperty(sym *Symbol, val Value) {}
func (t *TemporalZonedDateTime) GetProto() Value {
	return GetTemporalProto(TEMPORAL_ZONED_DATETIME_OBJ)
}

// ===== Temporal.TimeZone =====

// TemporalTimeZone 表示 IANA 时区或固定 UTC 偏移。
//
// offsetOnly 为真时 id 形如 "+09:00"，其行为与时区数据库无关。
type TemporalTimeZone struct {
	id         string
	offsetOnly bool
}

func NewTemporalTimeZone(id string) *TemporalTimeZone {
	return &TemporalTimeZone{id: id, offsetOnly: IsOffsetOnlyTimeZone(id)}
}

func (t *TemporalTimeZone) ID() string         { return t.id }
func (t *TemporalTimeZone) IsOffsetOnly() bool { return t.offsetOnly }

func (t *TemporalTimeZone) Type() ObjectType { return TEMPORAL_TIMEZONE_OBJ }
func (t *TemporalTimeZone) IsTruthy() bool   { return true }
func (t *TemporalTimeZone) Inspect() string  { return t.id }

func (t *TemporalTimeZone) ToString() string { return t.id }

func (t *TemporalTimeZone) GetProperty(name string) (Value, bool) {
	return protoLookup(TEMPORAL_TIMEZONE_OBJ, name, t)
}
func (t *TemporalTimeZone) SetProperty(name string, val Value) {}
func (t *TemporalTimeZone) GetSymbolProperty(sym *Symbol) (Value, bool) {
	return symbolDispatch(TEMPORAL_TIMEZONE_OBJ, sym)
}
func (t *TemporalTimeZone) SetSymbolProperty(sym *Symbol, val Value) {}
func (t *TemporalTimeZone) GetProto() Value                          { return GetTemporalProto(TEMPORAL_TIMEZONE_OBJ) }

// ===== Temporal.Calendar =====

// TemporalCalendar 表示一个日历系统。
// 本运行时仅实现 iso8601，其余日历标识符在构造时抛 RangeError。
type TemporalCalendar struct {
	id string
}

// DefaultCalendarID 是默认的日历标识符。
const DefaultCalendarID = "iso8601"

func NewTemporalCalendar(id string) *TemporalCalendar { return &TemporalCalendar{id: id} }

func (t *TemporalCalendar) ID() string { return t.id }

func (t *TemporalCalendar) Type() ObjectType { return TEMPORAL_CALENDAR_OBJ }
func (t *TemporalCalendar) IsTruthy() bool   { return true }
func (t *TemporalCalendar) Inspect() string  { return t.id }
func (t *TemporalCalendar) ToString() string { return t.id }

func (t *TemporalCalendar) GetProperty(name string) (Value, bool) {
	return protoLookup(TEMPORAL_CALENDAR_OBJ, name, t)
}
func (t *TemporalCalendar) SetProperty(name string, val Value) {}
func (t *TemporalCalendar) GetSymbolProperty(sym *Symbol) (Value, bool) {
	return symbolDispatch(TEMPORAL_CALENDAR_OBJ, sym)
}
func (t *TemporalCalendar) SetSymbolProperty(sym *Symbol, val Value) {}
func (t *TemporalCalendar) GetProto() Value                          { return GetTemporalProto(TEMPORAL_CALENDAR_OBJ) }

// ===== ISO 字符串格式化 =====

// formatISOYear 按 ISO 8601 扩展格式输出年份:
// 0-9999 用 4 位无符号，其余用 ± 加 6 位 (含前导零)。
func formatISOYear(y int32) string {
	if y >= 0 && y <= 9999 {
		return fmt.Sprintf("%04d", y)
	}
	sign := "+"
	if y < 0 {
		sign = "-"
		y = -y
	}
	return fmt.Sprintf("%s%06d", sign, y)
}

func padN(v int32, width int) string {
	s := strconv.FormatInt(int64(v), 10)
	for len(s) < width {
		s = "0" + s
	}
	return s
}

func formatISODate(d ISODate) string {
	return formatISOYear(d.Year) + "-" + padN(d.Month, 2) + "-" + padN(d.Day, 2)
}

// formatISOTime 按指定的秒以下精度输出时间。
// precision: "auto" 按实际值裁剪尾部零，其余为固定小数位数 (0/3/6/9)。
func formatISOTime(t ISOTime, precision string) string {
	s := padN(t.Hour, 2) + ":" + padN(t.Minute, 2)
	if precision == "minute" {
		return s
	}
	frac := ""
	switch precision {
	case "auto":
		switch {
		case t.Nanosecond != 0:
			frac = padN(t.Millisecond, 3) + padN(t.Microsecond, 3) + padN(t.Nanosecond, 3)
		case t.Microsecond != 0:
			frac = padN(t.Millisecond, 3) + padN(t.Microsecond, 3)
		case t.Millisecond != 0:
			frac = padN(t.Millisecond, 3)
		}
	case "0":
	case "3":
		frac = padN(t.Millisecond, 3)
	case "6":
		frac = padN(t.Millisecond, 3) + padN(t.Microsecond, 3)
	case "9":
		frac = padN(t.Millisecond, 3) + padN(t.Microsecond, 3) + padN(t.Nanosecond, 3)
	}
	sec := padN(t.Second, 2)
	if frac != "" {
		sec += "." + strings.TrimRight(frac, "0")
		sec = strings.TrimSuffix(sec, ".")
	}
	return s + ":" + sec
}

func formatISODateTime(dt ISODateTime) string {
	return formatISODate(dt.Date) + "T" + formatISOTime(dt.Time, "auto")
}

// formatUTCOffset 输出 ±HH:MM 形式的偏移 (秒级偏移会补 :SS)。
func formatUTCOffset(sec int32) string {
	if sec == 0 {
		return "Z"
	}
	sign := "+"
	if sec < 0 {
		sign = "-"
		sec = -sec
	}
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60
	out := fmt.Sprintf("%s%02d:%02d", sign, h, m)
	if s != 0 {
		out += fmt.Sprintf(":%02d", s)
	}
	return out
}

// AddOffsetToISODateTime 把 UTC 字段加上偏移得到当地墙钟字段。
func AddOffsetToISODateTime(dt ISODateTime, offsetSec int32) ISODateTime {
	total := dt.Time.NanosOfDay() + int64(offsetSec)*NanosPerSecond
	days := int64(0)
	for total >= NanosPerDay {
		total -= NanosPerDay
		days++
	}
	for total < 0 {
		total += NanosPerDay
		days--
	}
	if days == 0 {
		return ISODateTime{Date: dt.Date, Time: TimeFromNanosOfDay(total)}
	}
	return ISODateTime{Date: ISODateFromEpochDays(dt.Date.EpochDays() + days), Time: TimeFromNanosOfDay(total)}
}

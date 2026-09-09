package stdlib

import (
	"math"

	"js-runtime/object"
)

// 本文件是 Temporal 标准库层的公共辅助设施。
//
// 分成两组:
//  1. 错误与定义辅助 —— 统一 RangeError/TypeError 的产生方式，以及把
//     getter/方法/静态方法注册到 Temporal 原型上的样板封装。
//  2. 选项解析 —— Temporal 几乎所有方法都接受 options 对象
//     (largestUnit/smallestUnit/roundingMode/overflow/disambiguation...)，
//     这里把它们收敛成带默认值与合法性校验的读取器，避免各处重复实现。

// ===== 错误 =====

func tgError(name, msg string) object.Value { return object.NewErrorWithName(name, msg) }

func tgRange(msg string) object.Value { return object.NewErrorWithName("RangeError", msg) }

func tgType(msg string) object.Value { return object.NewErrorWithName("TypeError", msg) }

// tgTypeBadThis 生成统一的 "要求 this 为某类型" 错误信息。
func tgTypeBadThis(typeName string) object.Value {
	return tgType("Method called on incompatible receiver " + typeName)
}

// ===== 原型成员定义 =====

// tgGetter 在原型上定义只读 getter。
// 用 DefineAccessor 而非 SetProperty: Temporal 的字段
// (year/month/day/hours/...) 是访问器属性，读取时以实例为 this 调用。
func tgGetter(proto *object.Object, name string, fn func(this object.Value) object.Value) {
	proto.DefineAccessor(name, object.NewBuiltinMethod(name, func(this object.Value, _ ...object.Value) object.Value {
		return fn(this)
	}), nil)
}

// tgMethod 在原型上定义方法。
func tgMethod(proto *object.Object, name string, fn func(this object.Value, args ...object.Value) object.Value) {
	proto.SetProperty(name, object.NewBuiltinMethod(name, fn))
}

// tgStatic 在构造器上定义静态方法。构造器是 BuiltinFunction 而非 Object。
func tgStatic(ctor *object.BuiltinFunction, name string, fn func(args ...object.Value) object.Value) {
	ctor.SetProperty(name, object.NewBuiltin(name, fn))
}

// tgFn 在命名空间对象 (如 Temporal.Now) 上定义普通函数属性。
func tgFn(ns *object.Object, name string, fn func(args ...object.Value) object.Value) {
	ns.SetProperty(name, object.NewBuiltin(name, fn))
}

// tgIntGetter 定义一个返回整数的 getter。
func tgIntGetter(proto *object.Object, name string, fn func(this object.Value) int64) {
	tgGetter(proto, name, func(this object.Value) object.Value {
		return object.NewNumber(float64(fn(this)))
	})
}

// tgStrGetter 定义一个返回字符串的 getter。
func tgStrGetter(proto *object.Object, name string, fn func(this object.Value) string) {
	tgGetter(proto, name, func(this object.Value) object.Value {
		return object.NewString(fn(this))
	})
}

// tgConstMethod 定义一个返回值与方法名同名语义的通用序列化方法。
func tgToStringMethod(proto *object.Object, fn func(this object.Value) string) {
	tgMethod(proto, "toString", func(this object.Value, args ...object.Value) object.Value {
		return object.NewString(fn(this))
	})
	tgMethod(proto, "toJSON", func(this object.Value, args ...object.Value) object.Value {
		return object.NewString(fn(this))
	})
	// 无 Intl 支持: toLocaleString 退化为 toString
	tgMethod(proto, "toLocaleString", func(this object.Value, args ...object.Value) object.Value {
		return object.NewString(fn(this))
	})
}

// ===== this 类型提取 =====
//
// 每个 Temporal 原型的方法都需要先把 this 断言到具体类型。这些函数
// 统一返回 (值, 是否成功) 让调用方写出一致的错误分支。

func asInstant(v object.Value) (*object.TemporalInstant, bool) {
	t, ok := v.(*object.TemporalInstant)
	return t, ok
}
func asPlainDateTime(v object.Value) (*object.TemporalPlainDateTime, bool) {
	t, ok := v.(*object.TemporalPlainDateTime)
	return t, ok
}
func asPlainDate(v object.Value) (*object.TemporalPlainDate, bool) {
	t, ok := v.(*object.TemporalPlainDate)
	return t, ok
}
func asPlainTime(v object.Value) (*object.TemporalPlainTime, bool) {
	t, ok := v.(*object.TemporalPlainTime)
	return t, ok
}
func asPlainYearMonth(v object.Value) (*object.TemporalPlainYearMonth, bool) {
	t, ok := v.(*object.TemporalPlainYearMonth)
	return t, ok
}
func asPlainMonthDay(v object.Value) (*object.TemporalPlainMonthDay, bool) {
	t, ok := v.(*object.TemporalPlainMonthDay)
	return t, ok
}
func asZonedDateTime(v object.Value) (*object.TemporalZonedDateTime, bool) {
	t, ok := v.(*object.TemporalZonedDateTime)
	return t, ok
}
func asDuration(v object.Value) (*object.TemporalDuration, bool) {
	t, ok := v.(*object.TemporalDuration)
	return t, ok
}
func asTimeZone(v object.Value) (*object.TemporalTimeZone, bool) {
	t, ok := v.(*object.TemporalTimeZone)
	return t, ok
}
func asCalendar(v object.Value) (*object.TemporalCalendar, bool) {
	t, ok := v.(*object.TemporalCalendar)
	return t, ok
}

// ===== options 解析 =====

// optValue 取出 options 对象中的某个键。
// options 为 undefined/null 时返回 nil (表示未提供)。
func optValue(opts object.Value, key string) object.Value {
	if opts == nil || opts == object.UndefinedSingleton || opts == object.NullSingleton {
		return nil
	}
	o, ok := opts.(*object.Object)
	if !ok {
		return nil
	}
	v, found := o.GetProperty(key)
	if !found || v == nil || v == object.UndefinedSingleton {
		return nil
	}
	return v
}

// optString 读取字符串选项，非法值返回 def。
func optString(opts object.Value, key, def string) string {
	v := optValue(opts, key)
	if v == nil {
		return def
	}
	s, ok := v.(*object.String)
	if !ok {
		return def
	}
	return s.Value
}

// optNumber 读取数值选项。
func optNumber(opts object.Value, key string, def float64) float64 {
	v := optValue(opts, key)
	if v == nil {
		return def
	}
	n, ok := v.(*object.Number)
	if !ok {
		return def
	}
	return n.Value
}

// optIntOrUndef 读取整数选项，返回 (值, 是否提供)。非法浮点返回 false。
func optIntOrUndef(opts object.Value, key string) (int64, bool) {
	v := optValue(opts, key)
	if v == nil {
		return 0, false
	}
	n, ok := v.(*object.Number)
	if !ok {
		return 0, false
	}
	f := n.Value
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
		return 0, false
	}
	return int64(f), true
}

// getOverflow 读取 overflow 选项 ("constrain" 或 "reject")。
func getOverflow(opts object.Value) string {
	s := optString(opts, "overflow", "constrain")
	if s != "constrain" && s != "reject" {
		return "invalid"
	}
	return s
}

// getDisambiguation 读取 disambiguation 选项。
func getDisambiguation(opts object.Value) string {
	s := optString(opts, "disambiguation", "compatible")
	switch s {
	case "compatible", "earlier", "later", "reject":
		return s
	}
	return "invalid"
}

// getRoundingMode 读取 roundingMode 选项。
func getRoundingMode(opts object.Value) string {
	s := optString(opts, "roundingMode", "halfExpand")
	switch s {
	case "ceil", "floor", "expand", "trunc", "halfCeil", "halfFloor", "halfExpand", "halfTrunc":
		return s
	}
	return "invalid"
}

// unitsOrdered 从大到小排列的所有合法单位，用于 largestUnit/smallestUnit 区间校验。
var unitsOrdered = []string{
	"year", "month", "week", "day",
	"hour", "minute", "second", "millisecond", "microsecond", "nanosecond",
}

// DateTimeUnits 是 PlainDateTime/ZonedDateTime 允许的 sangxia（含日期与时间单位）。
var DateTimeUnits = unitsOrdered

// dateUnits 是 PlainDate 允许的单元。
var dateUnits = []string{"year", "month", "week", "day"}

// timeUnits 是 PlainTime 允许的单元。
var timeUnits = []string{"hour", "minute", "second", "millisecond", "microsecond", "nanosecond"}

// unitRank 返回单位在 OrderedUnits 中的序号，非法单位返回 -1。
func unitRank(u string) int {
	for i, v := range unitsOrdered {
		if v == u {
			return i
		}
	}
	return -1
}

// normalizeUnit 把 possibly 复数形式的单位名归一化为单数，并给出 Rank。
func normalizeUnit(s string) (string, int) {
	u := s
	if len(u) > 1 && u[len(u)-1] == 's' {
		u = u[:len(u)-1]
	}
	return u, unitRank(u)
}

// getUnitOption 读取 largestUnit/smallestUnit/totalUnit 选项。
func getUnitOption(opts object.Value, key string, allowed []string, def string) (string, bool) {
	v := optValue(opts, key)
	if v == nil {
		return def, true
	}
	s, ok := v.(*object.String)
	if !ok {
		return "", false
	}
	u, _ := normalizeUnit(s.Value)
	for _, a := range allowed {
		if a == u {
			return u, true
		}
	}
	if s.Value == "auto" && (key == "largestUnit" || key == "smallestUnit") {
		return "auto", true
	}
	return "", false
}

// getSmallestUnit 读取 smallestUnit，默认 nanosecond。
func getSmallestUnit(opts object.Value, allowed []string) (string, bool) {
	return getUnitOption(opts, "smallestUnit", allowed, "nanosecond")
}

// getLargestUnit 读取 largestUnit，默认 auto。
func getLargestUnit(opts object.Value, allowed []string) (string, bool) {
	return getUnitOption(opts, "largestUnit", allowed, "auto")
}

// getRoundingIncrement 读取 roundingIncrement，校验为正整数。
func getRoundingIncrement(opts object.Value) (int64, bool) {
	v := optValue(opts, "roundingIncrement")
	if v == nil {
		return 1, true
	}
	n, ok := v.(*object.Number)
	if !ok {
		return 0, false
	}
	f := n.Value
	if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || f < 1 || f > 1e9 {
		return 0, false
	}
	return int64(f), true
}

// getCalendarID 从 options 或注解中提取日历标识符。
// 本运行时只实现 iso8601，其余一律 RangeError。
func getCalendarID(opts object.Value, annotated string) (string, bool) {
	id := annotated
	if s := optString(opts, "calendar", ""); s != "" {
		id = s
	}
	if id == "" {
		return object.DefaultCalendarID, true
	}
	if id != object.DefaultCalendarID {
		return "", false
	}
	return id, true
}

// ===== Duration 参数 =====

// toDurationValue 把任意值转成 Duration。
// 支持 Duration 实例与 ISO Duration 字符串。
func toDurationValue(v object.Value) (*object.TemporalDuration, bool) {
	if d, ok := asDuration(v); ok {
		return d, true
	}
	if s, ok := v.(*object.String); ok {
		f, ok := object.ParseTemporalDurationString(s.Value)
		if !ok {
			return nil, false
		}
		return object.NewTemporalDuration(
			f.Years, f.Months, f.Weeks, f.Days,
			f.Hours, f.Minutes, f.Seconds,
			f.Milliseconds, f.Microseconds, f.Nanoseconds), true
	}
	if o, ok := v.(*object.Object); ok {
		return durationFromFieldsObject(o)
	}
	return nil, false
}

// durationFromFieldsObject 从属性包对象构造 Duration。
func durationFromFieldsObject(o object.Value) (*object.TemporalDuration, bool) {
	obj, ok := o.(*object.Object)
	if !ok {
		return nil, false
	}
	fields := map[string]float64{}
	for _, k := range []string{"years", "months", "weeks", "days",
		"hours", "minutes", "seconds", "milliseconds", "microseconds", "nanoseconds"} {
		if v, found := obj.GetProperty(k); found && v != object.UndefinedSingleton {
			f := toFloat(v)
			if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
				return nil, false
			}
			fields[k] = f
		}
	}
	return object.NewTemporalDuration(
		int64(fields["years"]), int64(fields["months"]), int64(fields["weeks"]),
		int64(fields["days"]), int64(fields["hours"]), int64(fields["minutes"]),
		int64(fields["seconds"]), int64(fields["milliseconds"]),
		int64(fields["microseconds"]), int64(fields["nanoseconds"])), true
}

// ===== 时区参数 =====

// toTimeZoneID 从 TimeZone 实例或字符串提取时区标识符。
func toTimeZoneID(v object.Value) (string, bool) {
	if tz, ok := asTimeZone(v); ok {
		return tz.ID(), true
	}
	if s, ok := v.(*object.String); ok {
		if object.IsValidTimeZoneID(s.Value) {
			return s.Value, true
		}
	}
	return "", false
}

// ===== 通用参数 =====

// argString 把参数转为字符串。
func argString(args []object.Value, i int) (string, bool) {
	if i >= len(args) {
		return "", false
	}
	s, ok := args[i].(*object.String)
	if !ok {
		return "", false
	}
	return s.Value, true
}

// argFloat 把参数转为浮点。
func argFloat(args []object.Value, i int) (float64, bool) {
	if i >= len(args) {
		return 0, false
	}
	v := args[i]
	if n, ok := v.(*object.Number); ok {
		return n.Value, true
	}
	if b, ok := v.(*object.BigInt); ok {
		return b.ToFloat64(), true
	}
	return toFloat(v), true
}

// requireIntOption 读取必须为非负整数的选项 (如 roundingIncrement)。
func requireIntOption(args []object.Value, i int, def float64) float64 {
	if i >= len(args) {
		return def
	}
	return toFloat(args[i])
}

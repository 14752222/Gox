package stdlib

import (
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/14752222/Gox/object"
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
	proto.DefineBuiltinAccessor(name, object.NewBuiltinMethod(name, func(this object.Value, _ ...object.Value) object.Value {
		return fn(this)
	}), nil)
}

// tgMethod 在原型上定义方法。
func tgMethod(proto *object.Object, name string, fn func(this object.Value, args ...object.Value) object.Value) {
	proto.SetBuiltinProperty(name, object.NewBuiltinMethod(name, fn))
}

// tgStatic 在构造器上定义静态方法。构造器是 BuiltinFunction 而非 Object。
func tgStatic(ctor *object.BuiltinFunction, name string, fn func(args ...object.Value) object.Value) {
	ctor.SetProperty(name, object.NewBuiltin(name, fn))
}

// tgFn 在命名空间对象 (如 Temporal.Now) 上定义普通函数属性。
func tgFn(ns *object.Object, name string, fn func(args ...object.Value) object.Value) {
	ns.SetBuiltinProperty(name, object.NewBuiltin(name, fn))
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
		s := fn(this)
		// 约定: fn 返回空串表示品牌检查失败 (Temporal 的字符串属性
		// 如 calendarId/timeZoneId 不可能为空)，必须抛 TypeError。
		if s == "" {
			return tgType("Method called on incompatible receiver (brand check failed for " + name + ")")
		}
		return object.NewString(s)
	})
}

// tgToStringMethod 注册 Temporal 的序列化三件套 (toString/toJSON/toLocaleString)。
//
// fn 返回空串表示 this 品牌检查失败，此时必须抛 TypeError 而不是返回 "" ——
// Temporal 的序列化方法不允许跨类调用。options 参数 (args[0]) 先做
// GetOptionsObject 类型约束: 非 undefined/非对象一律 TypeError。
func tgToStringMethod(proto *object.Object, typeName string, fn func(this object.Value, args ...object.Value) (string, object.Value)) {
	wrap := func(this object.Value, args ...object.Value) object.Value {
		if len(args) > 0 {
			if e := optionsTypeErr(args[0]); e != nil {
				return e
			}
		}
		s, err := fn(this, args...)
		if err != nil {
			return err
		}
		if s == "" {
			return tgTypeBadThis(typeName)
		}
		return object.NewString(s)
	}
	tgMethod(proto, "toString", wrap)
	tgMethod(proto, "toJSON", wrap)
	// 无 Intl 支持: toLocaleString 退化为 toString，但容忍
	// locales 参数 (字符串或数组) 与 options 参数。
	tgMethod(proto, "toLocaleString", func(this object.Value, args ...object.Value) object.Value {
		if len(args) > 1 {
			if e := optionsTypeErr(args[1]); e != nil {
				return e
			}
		}
		s, err := fn(this)
		if err != nil {
			return err
		}
		if s == "" {
			return tgTypeBadThis(typeName)
		}
		return object.NewString(s)
	})
}

// tgValueOf 注册 Temporal 规范的 valueOf: 无条件抛 TypeError。
// 禁止 Temporal 对象被隐式转数字做算术/比较，必须显式用 compare/equals。
func tgValueOf(proto *object.Object) {
	tgMethod(proto, "valueOf", func(this object.Value, args ...object.Value) object.Value {
		return tgType("Do not use Temporal objects with arithmetic or comparison operators; use compare(), equals() or arithmetic methods instead")
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

// optionsTypeErr 实现 GetOptionsObject 的类型约束:
// undefined (或未传) → 无错；任意对象 → 无错；其它原始值 (含 null) → TypeError。
func optionsTypeErr(opts object.Value) object.Value {
	if opts == nil || opts == object.UndefinedSingleton {
		return nil
	}
	if _, ok := opts.(*object.Object); ok {
		return nil
	}
	// Proxy / 函数类值 (Closure/BuiltinFunction) 同样是对象
	if _, ok := opts.(*object.Proxy); ok {
		return nil
	}
	if object.IsCallable(opts) {
		return nil
	}
	return tgType("Options argument must be an object or undefined")
}

// roundOptionsOf 处理 round 类方法的 roundTo 参数:
// 字符串等价于 { smallestUnit: 字符串 }，对象原样，其余 TypeError。
func roundOptionsOf(opts object.Value) (object.Value, object.Value) {
	if s, ok := opts.(*object.String); ok {
		o := object.NewObject()
		o.SetProperty("smallestUnit", s)
		return o, nil
	}
	if e := optionsTypeErr(opts); e != nil {
		return nil, e
	}
	return opts, nil
}

// optValue 取出 options 对象中的某个键。
// options 为 undefined 或非对象时返回 nil (表示未提供)；
// null/原始值的类型错误应由 optionsTypeErr 在方法入口先行拦截。
func optValue(opts object.Value, key string) object.Value {
	if opts == nil || opts == object.UndefinedSingleton {
		return nil
	}
	var v object.Value
	var found bool
	switch o := opts.(type) {
	case propBag:
		v, found = o.GetProperty(key)
	case *object.Proxy:
		v, found = o.GetProperty(key)
	default:
		return nil
	}
	if !found || v == nil || v == object.UndefinedSingleton {
		return nil
	}
	return v
}

// optionToString 实现选项值的 ToString 语义: String 原样，
// Number/Boolean/BigInt 转字符串，null → "null"，对象调用其 toString；
// Symbol 按规范必须 TypeError，返回 false。
func optionToString(v object.Value) (string, bool) {
	switch s := v.(type) {
	case *object.String:
		return s.Value, true
	case *object.Number:
		return object.NumberToString(s.Value), true
	case *object.Boolean:
		if s.Value {
			return "true", true
		}
		return "false", true
	case *object.Null:
		return "null", true
	case *object.BigInt:
		return s.Value.String(), true
	case *object.Symbol:
		return "", false
	case *object.Object, *object.Proxy:
		return object.ToString(v), true
	}
	return "", false
}

// optString 读取字符串选项: 对值做 ToString，无法转换时返回 def。
func optString(opts object.Value, key, def string) string {
	v := optValue(opts, key)
	if v == nil {
		return def
	}
	s, ok := optionToString(v)
	if !ok {
		return def
	}
	return s
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

// getEnumOption 是枚举类选项的统一读取器:
// 缺省返回 def；值做 ToString (Symbol → TypeError)；非法字符串 → RangeError。
func getEnumOption(opts object.Value, key, def string, allowed ...string) (string, object.Value) {
	v := optValue(opts, key)
	if v == nil {
		return def, nil
	}
	s, ok := optionToString(v)
	if !ok {
		return "", tgType("Cannot convert option " + key + " to string")
	}
	for _, a := range allowed {
		if a == s {
			return s, nil
		}
	}
	return "", tgRange("Invalid " + key + " option: " + s)
}

// getOverflow 读取 overflow 选项 ("constrain" 或 "reject")。
func getOverflow(opts object.Value) (string, object.Value) {
	return getEnumOption(opts, "overflow", "constrain", "constrain", "reject")
}

// getDisambiguation 读取 disambiguation 选项。
func getDisambiguation(opts object.Value) (string, object.Value) {
	return getEnumOption(opts, "disambiguation", "compatible",
		"compatible", "earlier", "later", "reject")
}

// getRoundingMode 读取 roundingMode 选项。
// halfEven 是银行家舍入，与 halfExpand 在符号对称性上不同，但作为
// 合法值必须被接受 (舍入实现内部再区分)。
func getRoundingMode(opts object.Value) (string, object.Value) {
	return getEnumOption(opts, "roundingMode", "halfExpand",
		"ceil", "floor", "expand", "trunc",
		"halfCeil", "halfFloor", "halfExpand", "halfTrunc", "halfEven")
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
// 返回值 err 非 nil 表示必须向外抛出的错误值:
// ToString 失败 (Symbol 等) → TypeError；字符串非法 → RangeError。
func getUnitOption(opts object.Value, key string, allowed []string, def string) (string, object.Value) {
	v := optValue(opts, key)
	if v == nil {
		return def, nil
	}
	s, ok := optionToString(v)
	if !ok {
		return "", tgType("Cannot convert option " + key + " to string")
	}
	u, _ := normalizeUnit(s)
	for _, a := range allowed {
		if a == u {
			return u, nil
		}
	}
	if s == "auto" && (key == "largestUnit" || key == "smallestUnit") {
		return "auto", nil
	}
	return "", tgRange("Invalid unit option: " + s)
}

// getSmallestUnit 读取 smallestUnit，默认 nanosecond。
func getSmallestUnit(opts object.Value, allowed []string) (string, object.Value) {
	return getUnitOption(opts, "smallestUnit", allowed, "nanosecond")
}

// getLargestUnit 读取 largestUnit，默认 auto。
func getLargestUnit(opts object.Value, allowed []string) (string, object.Value) {
	return getUnitOption(opts, "largestUnit", allowed, "auto")
}

// getRoundingIncrement 读取 roundingIncrement (ToIntegerWithTruncation 语义)。
// 返回值错误已按规范区分类型: ToNumber 失败 (如 Symbol) → TypeError，
// 数值越界 → RangeError。
func getRoundingIncrement(opts object.Value) (int64, object.Value) {
	v := optValue(opts, "roundingIncrement")
	if v == nil {
		return 1, nil
	}
	f, cerr := tgToNumber(v)
	if cerr != nil {
		return 0, cerr
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, tgRange("Invalid roundingIncrement")
	}
	// spec: ToIntegerWithTruncation — 小数向零截断 (2.5 → 2)。
	f = math.Trunc(f)
	if f < 1 || f > 1e9 {
		return 0, tgRange("Invalid roundingIncrement")
	}
	return int64(f), nil
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

// toDurationE 实现 ToTemporalDuration, 区分错误类型:
// 字符串解析失败 → RangeError; 非对象 → TypeError;
// 属性包无合法字段 → TypeError; 字段值非整数 → RangeError。
func toDurationE(v object.Value) (*object.TemporalDuration, object.Value) {
	if d, ok := asDuration(v); ok {
		return d, nil
	}
	if s, ok := v.(*object.String); ok {
		f, ok := object.ParseTemporalDurationString(s.Value)
		if !ok {
			return nil, tgRange("Invalid Duration string: " + s.Value)
		}
		return object.NewTemporalDuration(
			f.Years, f.Months, f.Weeks, f.Days,
			f.Hours, f.Minutes, f.Seconds,
			f.Milliseconds, f.Microseconds, f.Nanoseconds), nil
	}
	if o, ok := v.(propBag); ok {
		return durationFromFieldsE(o)
	}
	return nil, tgType("argument is not a Duration")
}

// durationFromFieldsE 从属性包构造 Duration (带规范错误类型)。
func durationFromFieldsE(obj propBag) (*object.TemporalDuration, object.Value) {
	fields := map[string]float64{}
	any := false
	for _, k := range []string{"years", "months", "weeks", "days",
		"hours", "minutes", "seconds", "milliseconds", "microseconds", "nanoseconds"} {
		if v, found := obj.GetProperty(k); found && v != object.UndefinedSingleton {
			f, cerr := tgToNumber(v)
			if cerr != nil {
				return nil, cerr
			}
			if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
				return nil, tgRange(k + " must be an integer")
			}
			fields[k] = f
			any = true
		}
	}
	if !any {
		return nil, tgType("Duration-like object has no valid properties")
	}
	return object.NewTemporalDuration(
		int64(fields["years"]), int64(fields["months"]), int64(fields["weeks"]),
		int64(fields["days"]), int64(fields["hours"]), int64(fields["minutes"]),
		int64(fields["seconds"]), int64(fields["milliseconds"]),
		int64(fields["microseconds"]), int64(fields["nanoseconds"])), nil
}

// checkDurationUniformSign 实现 spec 的 DurationSign 检查:
// 非零分量符号不一致 → RangeError (所有 add/subtract 前置校验)。
func checkDurationUniformSign(d *object.TemporalDuration) object.Value {
	sign := 0
	for _, v := range []int64{d.Years(), d.Months(), d.Weeks(), d.Days(),
		d.Hours(), d.Minutes(), d.Seconds(), d.Milliseconds(), d.Microseconds(), d.Nanoseconds()} {
		if v == 0 {
			continue
		}
		s := 1
		if v < 0 {
			s = -1
		}
		if sign == 0 {
			sign = s
		} else if sign != s {
			return tgRange("Duration with mixed signs is not allowed")
		}
	}
	return nil
}

// propBag 是可读属性的选项包/字段包 (普通对象或 Proxy)。
type propBag interface {
	GetProperty(string) (object.Value, bool)
}

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
// 字符串可以是裸标识符 ("Asia/Tokyo"、偏移 "+09:00")，也可以是带
// 时区注解的完整 ISO 时间串 ("2021-08-19T17:33[Europe/Madrid]")。
func toTimeZoneID(v object.Value) (string, bool) {
	id, err := toTimeZoneIDE(v)
	return id, err == nil
}

// toTimeZoneIDE 同 toTimeZoneID，但按规范区分错误类型:
// 字符串非法 → RangeError；非字符串非时区值 (null/数字等) → TypeError。
func toTimeZoneIDE(v object.Value) (string, object.Value) {
	if tz, ok := asTimeZone(v); ok {
		return tz.ID(), nil
	}
	if zdt, ok := asZonedDateTime(v); ok {
		return zdt.TimeZoneID(), nil
	}
	s, ok := v.(*object.String)
	if !ok {
		return "", tgType("Invalid time zone value: expected a string or Temporal.TimeZone")
	}
	if object.IsValidTimeZoneID(s.Value) {
		return s.Value, nil
	}
	// 裸偏移字符串 ("+02:00"、"-05:00:30.5"): 规范化为偏移时区 ID。
	if off, ok := object.ParseTimeZoneOffsetString(s.Value); ok {
		return tzOffsetID(off)
	}
	// 尝试作为带注解/偏移的 Temporal 字符串解析，取其中的时区部分。
	for _, parse := range []func(string) (object.TemporalFields, bool){
		object.ParseTemporalZonedDateTimeString,
		object.ParseTemporalDateTimeString,
		object.ParseTemporalTimeString,
	} {
		if f, ok := parseCalendarSyntactic(s.Value, parse); ok {
			if f.TimeZoneAnnotation != "" {
				ann := f.TimeZoneAnnotation
				if object.IsValidTimeZoneID(ann) {
					return ann, nil
				}
				// 偏移注解 ([+02:00] / [-05:30:15])
				if off, ok := object.ParseTimeZoneOffsetString(ann); ok {
					return tzOffsetID(off)
				}
				return "", tgRange("Invalid time zone: " + ann)
			}
			if f.OffsetSeconds != nil {
				return tzOffsetID(*f.OffsetSeconds)
			}
		}
	}
	return "", tgRange("Invalid time zone: " + s.Value)
}

// tzOffsetID 把秒级偏移规范化为时区 ID；亚分钟级偏移按规范为非法时区。
func tzOffsetID(off int32) (string, object.Value) {
	if off%60 != 0 {
		return "", tgRange("UTC offset with sub-minute precision is not a valid time zone")
	}
	return object.FormatOffsetSeconds(off), nil
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

// ===== ToTemporalX 参数转换 =====
//
// until/since/equals/compare 等方法的 "other" 参数不是要求实例，
// 而是走规范的 ToTemporalX 抽象操作: 实例直接用，字符串按文法解析，
// 对象按属性包读取，相关类型互相降级 (PlainDateTime → PlainDate 等)。

// calendarFromAnnotation 校验字符串解析出的日历注解 (仅支持 iso8601)。
func calendarFromAnnotation(cal string) object.Value {
	if cal != "" && cal != object.DefaultCalendarID {
		return tgRange("Unsupported calendar: " + cal)
	}
	return nil
}

// checkCalendarString 校验日历 ID 字符串 (spec IsValidCalendarCode 子集):
// 纯 ASCII, 小写化后仅支持 iso8601; 其他一律 RangeError。
// parseCalendarSyntactic 先严格解析，失败时把闰秒 ":60" 降级为 ":59" 再试——
// 日历/时区字符串只要求文法有效 (syntactic)，不做取值范围校验。
func parseCalendarSyntactic(s string, parse func(string) (object.TemporalFields, bool)) (object.TemporalFields, bool) {
	if f, ok := parse(s); ok {
		return f, true
	}
	// 只对时间部分 (注解 '[' 之前) 做闰秒降级，注解内的 ":60" 仍非法。
	head := s
	if i := strings.IndexByte(s, '['); i >= 0 {
		head = s[:i]
	}
	if strings.Contains(head, ":60") {
		tail := s[len(head):]
		return parse(strings.Replace(head, ":60", ":59", 1) + tail)
	}
	return object.TemporalFields{}, false
}

func checkCalendarString(s string) object.Value {
	lower := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c > 127 {
			return tgRange("Invalid calendar ID: " + s)
		}
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		lower = append(lower, c)
	}
	id := string(lower)
	if id == "" {
		return tgRange("empty string is not a valid calendar ID")
	}
	// 先按 ISO 串解析 ("2020-01-01"、"01-01"、"2020-01"、带注解形式):
	// 提取其中的日历注解; 无注解时取默认 iso8601。
	for _, parse := range []func(string) (object.TemporalFields, bool){
		object.ParseTemporalDateTimeString,
		object.ParseTemporalDateString,
		object.ParseTemporalYearMonthString,
		object.ParseTemporalMonthDayString,
		object.ParseTemporalTimeString,
	} {
		if f, ok := parseCalendarSyntactic(s, parse); ok {
			if f.Calendar == "" {
				return nil
			}
			return checkCalendarString(f.Calendar)
		}
	}
	simple := true
	for _, c := range []byte(id) {
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_'
		if !ok {
			simple = false
			break
		}
	}
	if simple {
		if id != object.DefaultCalendarID {
			return tgRange("Unsupported calendar: " + id)
		}
		return nil
	}
	// 解析器可能因闰秒 (23:59:60) 等拒绝整串; 直接抽 u-ca 注解兜底。
	if i := strings.Index(s, "[u-ca="); i >= 0 {
		rest := s[i+len("[u-ca="):]
		if j := strings.IndexByte(rest, ']'); j > 0 {
			return checkCalendarString(rest[:j])
		}
	}
	if i := strings.Index(s, "[!u-ca="); i >= 0 {
		rest := s[i+len("[!u-ca="):]
		if j := strings.IndexByte(rest, ']'); j > 0 {
			return checkCalendarString(rest[:j])
		}
	}
	return tgRange("Invalid calendar ID: " + s)
}

// checkCalendarProp 校验属性包上的 calendar 字段:
// 缺失/undefined → 无错; Calendar 实例 → 校验其 ID;
// 非字符串 → TypeError; 非法/不支持 → RangeError。
func checkCalendarProp(o propBag) object.Value {
	v, found := o.GetProperty("calendar")
	if !found || v == object.UndefinedSingleton {
		return nil
	}
	if c, ok := asCalendar(v); ok {
		return checkCalendarString(c.ID())
	}
	// 任意 Temporal 对象都带 [[Calendar]] slot (spec ToTemporalCalendarSlotValue)。
	switch v.(type) {
	case *object.TemporalPlainDate, *object.TemporalPlainDateTime,
		*object.TemporalPlainYearMonth, *object.TemporalPlainMonthDay,
		*object.TemporalZonedDateTime:
		return nil
	}
	s, ok := v.(*object.String)
	if !ok {
		return tgType("calendar must be a string")
	}
	return checkCalendarString(s.Value)
}

// ctorCalendarID 处理构造器可选 calendar 参数 (undefined → 默认 iso8601)。
func ctorCalendarID(v object.Value) (string, object.Value) {
	if v == nil || v == object.UndefinedSingleton {
		return object.DefaultCalendarID, nil
	}
	s, ok := v.(*object.String)
	if !ok {
		return "", tgType("calendar must be a string")
	}
	if e := checkCalendarString(s.Value); e != nil {
		return "", e
	}
	return object.DefaultCalendarID, nil
}

// tgToNumber 实现 ToNumber: 数字原样; 对象走 ToPrimitive (valueOf → toString),
// 回调抛出的 JS 异常原样透传。返回值不是有限数时由调用方决定错误类型。
func tgToNumber(v object.Value) (float64, object.Value) {
	switch v.(type) {
	case *object.Number, *object.Boolean, *object.Null, *object.Undefined, *object.String:
		return toFloat(v), nil
	case *object.BigInt:
		return 0, tgType("Cannot convert BigInt to number")
	case *object.Symbol:
		return 0, tgType("Cannot convert a Symbol value to a number")
	}
	if g, ok := v.(interface{ GetProperty(string) (object.Value, bool) }); ok {
		if vf, found := g.GetProperty("valueOf"); found && object.IsCallable(vf) {
			r := object.CallFunction(vf, v)
			if cbErr := object.TakeCallbackError(); cbErr != nil {
				if thrown := object.TakeCallbackErrorValue(); thrown != nil {
					if e, isErr := thrown.(*object.Error); isErr {
						return 0, e
					}
				}
				return 0, tgType(cbErr.Error())
			}
			switch r.(type) {
			case *object.Number, *object.Boolean, *object.Null, *object.Undefined, *object.String:
				return toFloat(r), nil
			}
		}
		if tf, found := g.GetProperty("toString"); found && object.IsCallable(tf) {
			r := object.CallFunction(tf, v)
			if cbErr := object.TakeCallbackError(); cbErr != nil {
				if thrown := object.TakeCallbackErrorValue(); thrown != nil {
					if e, isErr := thrown.(*object.Error); isErr {
						return 0, e
					}
				}
				return 0, tgType(cbErr.Error())
			}
			if s, ok := r.(*object.String); ok {
				return object.ParseJSNumber(s.Value), nil
			}
		}
	}
	return toFloat(v), nil
}

// readIntField 读取整型字段: 缺失返回 ok=false; 非有限数按 spec 抛 RangeError。
func readIntField(o propBag, key string) (int32, bool, object.Value) {
	v, found := o.GetProperty(key)
	if !found || v == object.UndefinedSingleton {
		return 0, false, nil
	}
	f, cerr := tgToNumber(v)
	if cerr != nil {
		return 0, false, cerr
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false, tgRange(key + " must be a finite number")
	}
	return int32(math.Trunc(f)), true, nil
}

// readDateFields 从属性包读取 year/month(或 monthCode)/day 并按 overflow 调节。
// 失败时返回 *object.Error (TypeError=字段缺失, RangeError=值越界)。
func readDateFields(o propBag, overflow string) (object.ISODate, object.Value) {
	if ce := checkCalendarProp(o); ce != nil {
		return object.ISODate{}, ce
	}
	y, okY, e := readIntField(o, "year")
	if e != nil {
		return object.ISODate{}, e
	}
	var m int32
	var okM bool
	if mc, found := o.GetProperty("monthCode"); found && mc != object.UndefinedSingleton {
		s, ok := mc.(*object.String)
		if !ok || len(s.Value) != 3 || s.Value[0] != 'M' {
			return object.ISODate{}, tgRange("Invalid monthCode")
		}
		m = int32(s.Value[1]-'0')*10 + int32(s.Value[2]-'0')
		if m < 1 || m > 12 {
			return object.ISODate{}, tgRange("Invalid monthCode")
		}
		okM = true
	} else {
		var e2 object.Value
		m, okM, e2 = readIntField(o, "month")
		if e2 != nil {
			return object.ISODate{}, e2
		}
	}
	d, okD, e3 := readIntField(o, "day")
	if e3 != nil {
		return object.ISODate{}, e3
	}
	if !okY || !okM || !okD {
		return object.ISODate{}, tgType("missing required date fields (year, month, day)")
	}
	if overflow == "reject" {
		out := object.ISODate{Year: y, Month: m, Day: d}
		if !object.IsValidISODate(out) {
			return object.ISODate{}, tgRange("Invalid ISO date")
		}
		return out, nil
	}
	if m < 1 {
		m = 1
	}
	if m > 12 {
		m = 12
	}
	dim := object.DaysInMonth(y, m)
	if d < 1 {
		d = 1
	}
	if d > dim {
		d = dim
	}
	out := object.ISODate{Year: y, Month: m, Day: d}
	if !object.IsValidISODate(out) {
		return object.ISODate{}, tgRange("Invalid ISO date")
	}
	return out, nil
}

// readDateTimeFields 在 readDateFields 基础上合并可选时间字段。
func readDateTimeFields(o propBag, overflow string) (object.ISODateTime, object.Value) {
	d, e := readDateFields(o, overflow)
	if e != nil {
		return object.ISODateTime{}, e
	}
	tm := object.ISOTime{}
	if !mergeTimeInto(&tm, o) || !object.IsValidISOTime(tm) {
		return object.ISODateTime{}, tgRange("Invalid ISO time")
	}
	return object.ISODateTime{Date: d, Time: tm}, nil
}

// toPlainDateTimeValue 实现 ToTemporalDateTime。
func toPlainDateTimeValue(v object.Value) (*object.TemporalPlainDateTime, object.Value) {
	switch t := v.(type) {
	case *object.TemporalPlainDateTime:
		return t, nil
	case *object.TemporalPlainDate:
		return object.NewTemporalPlainDateTime(object.ISODateTime{Date: t.ISODate()}, t.CalendarID()), nil
	case *object.TemporalZonedDateTime:
		dt := object.GetISOPartsFromEpoch(t.TimeZoneID(), t.Nanos())
		return object.NewTemporalPlainDateTime(dt, t.CalendarID()), nil
	case *object.String:
		f, ok := object.ParseTemporalDateTimeString(t.Value)
		if !ok {
			return nil, tgRange("Invalid PlainDateTime string: "+t.Value)
		}
		if e := calendarFromAnnotation(f.Calendar); e != nil {
			return nil, e
		}
		return object.NewTemporalPlainDateTime(f.ISODateTime(), object.DefaultCalendarID), nil
	case propBag:
		dt, ferr := readDateTimeFields(t, "constrain")
		if ferr != nil {
			return nil, ferr
		}
		return object.NewTemporalPlainDateTime(dt, object.DefaultCalendarID), nil
	}
	return nil, tgType("argument is not a PlainDateTime")
}

// toPlainDateValue 实现 ToTemporalDate。
func toPlainDateValue(v object.Value) (*object.TemporalPlainDate, object.Value) {
	switch t := v.(type) {
	case *object.TemporalPlainDate:
		return t, nil
	case *object.TemporalPlainDateTime:
		return object.NewTemporalPlainDate(t.ISODateTime().Date, t.CalendarID()), nil
	case *object.TemporalZonedDateTime:
		dt := object.GetISOPartsFromEpoch(t.TimeZoneID(), t.Nanos())
		return object.NewTemporalPlainDate(dt.Date, t.CalendarID()), nil
	case *object.String:
		f, ok := object.ParseTemporalDateString(t.Value)
		if !ok || !f.HasDate {
			return nil, tgRange("Invalid PlainDate string: "+t.Value)
		}
		if e := calendarFromAnnotation(f.Calendar); e != nil {
			return nil, e
		}
		return object.NewTemporalPlainDate(object.ISODate{Year: f.Year, Month: f.Month, Day: f.Day}, object.DefaultCalendarID), nil
	case propBag:
		d, ferr := readDateFields(t, "constrain")
		if ferr != nil {
			return nil, ferr
		}
		return object.NewTemporalPlainDate(d, object.DefaultCalendarID), nil
	}
	return nil, tgType("argument is not a PlainDate")
}

// toPlainTimeValue 实现 ToTemporalTime。
func toPlainTimeValue(v object.Value) (*object.TemporalPlainTime, object.Value) {
	switch t := v.(type) {
	case *object.TemporalPlainTime:
		return t, nil
	case *object.TemporalPlainDateTime:
		return object.NewTemporalPlainTime(t.ISODateTime().Time), nil
	case *object.TemporalZonedDateTime:
		dt := object.GetISOPartsFromEpoch(t.TimeZoneID(), t.Nanos())
		return object.NewTemporalPlainTime(dt.Time), nil
	case *object.String:
		f, ok := object.ParseTemporalTimeString(t.Value)
		if !ok {
			// 也接受带日期的 DateTime 串 (取时间部分，必须含时间)。
			if df, dok := object.ParseTemporalDateTimeString(t.Value); dok && df.HasTime {
				f, ok = df, true
			}
		}
		if !ok || !f.HasTime {
			return nil, tgRange("Invalid PlainTime string: "+t.Value)
		}
		// UTC 指示符 Z 不是合法的 PlainTime 串 (数值偏移允许但被忽略)。
		if f.IsZ {
			return nil, tgRange("Invalid PlainTime string: "+t.Value)
		}
		if f.Second == 60 {
			f.Second = 59
		}
		return object.NewTemporalPlainTime(object.ISOTime{
			Hour: f.Hour, Minute: f.Minute, Second: f.Second,
			Millisecond: f.Millisecond, Microsecond: f.Microsecond, Nanosecond: f.Nanosecond}), nil
	case propBag:
		any := false
		for _, k := range []string{"hour", "minute", "second", "millisecond", "microsecond", "nanosecond"} {
			if pv, found := t.GetProperty(k); found && pv != object.UndefinedSingleton {
				any = true
				break
			}
		}
		if !any {
			return nil, tgType("argument is not a PlainTime: no time fields")
		}
		tm := object.ISOTime{}
		if !mergeTimeInto(&tm, t) {
			return nil, tgRange("Invalid time fields")
		}
		// 属性包中的闰秒 (second: 60) 按规范约束为 59。
		if tm.Second == 60 {
			tm.Second = 59
		}
		if !object.IsValidISOTime(tm) {
			return nil, tgRange("Invalid time fields")
		}
		return object.NewTemporalPlainTime(tm), nil
	}
	return nil, tgType("argument is not a PlainTime")
}

// toInstantValue 实现 ToTemporalInstant。
func toInstantValue(v object.Value) (*object.TemporalInstant, object.Value) {
	switch t := v.(type) {
	case *object.TemporalInstant:
		return t, nil
	case *object.TemporalZonedDateTime:
		return object.NewTemporalInstant(t.Nanos()), nil
	case *object.String:
		f, ok := object.ParseTemporalInstantString(t.Value)
		if !ok {
			return nil, tgRange("Invalid Instant string: "+t.Value)
		}
		if e := calendarFromAnnotation(f.Calendar); e != nil {
			return nil, e
		}
		dt := f.ISODateTime()
		ns := dt.EpochNanos()
		if f.OffsetSeconds != nil {
			ns = ns.Sub(ns, big.NewInt(int64(*f.OffsetSeconds)*object.NanosPerSecond))
		}
		if !object.IsValidEpochNanos(ns) {
			return nil, tgRange("Instant is outside of supported range")
		}
		return object.NewTemporalInstant(ns), nil
	}
	return nil, tgType("argument is not an Instant")
}

// toPlainYearMonthValue 实现 ToTemporalYearMonth。
func toPlainYearMonthValue(v object.Value) (*object.TemporalPlainYearMonth, object.Value) {
	switch t := v.(type) {
	case *object.TemporalPlainYearMonth:
		return t, nil
	case *object.TemporalPlainDate:
		return object.NewTemporalPlainYearMonth(t.ISODate().Year, t.ISODate().Month, 1, object.DefaultCalendarID), nil
	case *object.TemporalPlainDateTime:
		d := t.ISODateTime().Date
		return object.NewTemporalPlainYearMonth(d.Year, d.Month, 1, object.DefaultCalendarID), nil
	case *object.String:
		f, ok := object.ParseTemporalYearMonthString(t.Value)
		if !ok {
			return nil, tgRange("Invalid PlainYearMonth string: "+t.Value)
		}
		if e := calendarFromAnnotation(f.Calendar); e != nil {
			return nil, e
		}
		return object.NewTemporalPlainYearMonth(f.Year, f.Month, 1, object.DefaultCalendarID), nil
	case propBag:
		y, m, err := yearMonthFields(t)
		if err != nil {
			return nil, err
		}
		return object.NewTemporalPlainYearMonth(y, m, 1, object.DefaultCalendarID), nil
	}
	return nil, tgType("argument is not a PlainYearMonth")
}

// toPlainMonthDayValue 实现 ToTemporalMonthDay。
func toPlainMonthDayValue(v object.Value) (*object.TemporalPlainMonthDay, object.Value) {
	switch t := v.(type) {
	case *object.TemporalPlainMonthDay:
		return t, nil
	case *object.TemporalPlainDate:
		d := t.ISODate()
		return object.NewTemporalPlainMonthDay(d.Month, d.Day, 1972, object.DefaultCalendarID), nil
	case *object.TemporalPlainDateTime:
		d := t.ISODateTime().Date
		return object.NewTemporalPlainMonthDay(d.Month, d.Day, 1972, object.DefaultCalendarID), nil
	case *object.String:
		f, ok := object.ParseTemporalMonthDayString(t.Value)
		if !ok {
			return nil, tgRange("Invalid PlainMonthDay string: "+t.Value)
		}
		if e := calendarFromAnnotation(f.Calendar); e != nil {
			return nil, e
		}
		return object.NewTemporalPlainMonthDay(f.Month, f.Day, 1972, object.DefaultCalendarID), nil
	case propBag:
		if ce := checkCalendarProp(t); ce != nil {
			return nil, ce
		}
		mv, hasM := t.GetProperty("month")
		dv, hasD := t.GetProperty("day")
		m := int32(0)
		if mc, ok := t.GetProperty("monthCode"); ok && mc != object.UndefinedSingleton {
			s, ok2 := mc.(*object.String)
			if !ok2 || len(s.Value) != 3 || s.Value[0] != 'M' {
				return nil, tgRange("Invalid monthCode")
			}
			m = int32(s.Value[1]-'0')*10 + int32(s.Value[2]-'0')
		} else if hasM {
			m = int32(toFloat(mv))
		}
		if !hasD || m == 0 {
			return nil, tgType("argument is not a PlainMonthDay")
		}
		d := int32(toFloat(dv))
		if m < 1 || m > 12 || d < 1 || d > object.DaysInMonth(1972, m) {
			return nil, tgRange("Invalid month-day")
		}
		return object.NewTemporalPlainMonthDay(m, d, 1972, object.DefaultCalendarID), nil
	}
	return nil, tgType("argument is not a PlainMonthDay")
}

// toZonedDateTimeValue 实现 ToTemporalZonedDateTime。
// 字符串必须带时区注解；对象需含时区字段；Instant 按 UTC 处理。
func toZonedDateTimeValue(v object.Value) (*object.TemporalZonedDateTime, object.Value) {
	switch t := v.(type) {
	case *object.TemporalZonedDateTime:
		return t, nil
	case *object.TemporalInstant:
		return object.NewTemporalZonedDateTime(t.Nanos(), "UTC", object.DefaultCalendarID), nil
	case *object.String:
		f, ok := object.ParseTemporalZonedDateTimeString(t.Value)
		if !ok || f.TimeZoneAnnotation == "" {
			return nil, tgRange("Invalid ZonedDateTime string: "+t.Value)
		}
		if e := calendarFromAnnotation(f.Calendar); e != nil {
			return nil, e
		}
		tz := f.TimeZoneAnnotation
		if !object.IsValidTimeZoneID(tz) {
			return nil, tgRange("Invalid time zone: "+tz)
		}
		var ns *big.Int
		if f.OffsetSeconds != nil {
			dt := f.ISODateTime()
			ns = dt.EpochNanos()
			ns = ns.Sub(ns, big.NewInt(int64(*f.OffsetSeconds)*object.NanosPerSecond))
		} else {
			dt := f.ISODateTime()
			cands := object.GetPossibleInstantsFor(tz, dt)
			n, ok := object.DisambiguatePossibleInstants(cands, tz, dt, "compatible")
			if !ok {
				return nil, tgRange("Invalid ZonedDateTime string: "+t.Value)
			}
			ns = n
		}
		if !object.IsValidEpochNanos(ns) {
			return nil, tgRange("ZonedDateTime is outside of supported range")
		}
		return object.NewTemporalZonedDateTime(ns, tz, object.DefaultCalendarID), nil
	case propBag:
		tzv, found := t.GetProperty("timeZone")
		if !found {
			return nil, tgType("argument is not a ZonedDateTime: missing timeZone")
		}
		tz, ok := toTimeZoneID(tzv)
		if !ok {
			return nil, tgRange("Invalid time zone")
		}
		dt, ferr := readDateTimeFields(t, "constrain")
		if ferr != nil {
			return nil, ferr
		}
		cands := object.GetPossibleInstantsFor(tz, dt)
		ns, ok3 := object.DisambiguatePossibleInstants(cands, tz, dt, "compatible")
		if !ok3 {
			return nil, tgRange("Invalid wall-clock time")
		}
		return object.NewTemporalZonedDateTime(ns, tz, object.DefaultCalendarID), nil
	}
	return nil, tgType("argument is not a ZonedDateTime")
}

// ===== 函数 length 修正 =====
//
// 内建函数的 length 自有属性默认是 0，而规范为每个 Temporal 方法规定了
// 具体值 (必参个数)。这里在各 setup 完成后统一按表修正。

// tgSetFnLen 给函数类值设置规范的 length 描述符。
func tgSetFnLen(v object.Value, n int) {
	d := object.PropertyDescriptor{Value: object.NewInt(int64(n)), Writable: false, Enumerable: false, Configurable: true}
	switch f := v.(type) {
	case *object.BuiltinFunction:
		f.DefineOwn("length", d)
	case *object.BuiltinMethod:
		f.DefineOwn("length", d)
	}
}

// tgApplyLengths 按表设置原型方法的 length。
func tgApplyLengths(proto object.Value, table map[string]int) {
	o, ok := proto.(*object.Object)
	if !ok {
		return
	}
	for name, n := range table {
		if v, found := o.GetProperty(name); found {
			tgSetFnLen(v, n)
		}
	}
}

// temporalLengthTables 返回 类型 → (构造器名, 构造器 length, 方法表)。
func temporalLengthTables() []struct {
	typ     object.ObjectType
	ctor    string
	ctorLen int
	methods map[string]int
} {
	// 通用的序列化三件套与 valueOf 都是 0 必参
	ser := map[string]int{"toString": 0, "toJSON": 0, "toLocaleString": 0, "valueOf": 0}
	with := func(extra map[string]int) map[string]int {
		out := map[string]int{}
		for k, v := range ser {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	type ent = struct {
		typ     object.ObjectType
		ctor    string
		ctorLen int
		methods map[string]int
	}
	return []ent{
		{object.TEMPORAL_INSTANT_OBJ, "Instant", 1, with(map[string]int{
			"add": 1, "subtract": 1, "until": 1, "since": 1, "round": 1,
			"equals": 1, "toZonedDateTimeISO": 1,
		})},
		{object.TEMPORAL_DURATION_OBJ, "Duration", 0, with(map[string]int{
			"with": 1, "negated": 0, "abs": 0, "add": 1, "subtract": 1,
			"round": 1, "total": 1,
		})},
		{object.TEMPORAL_PLAIN_DATETIME_OBJ, "PlainDateTime", 3, with(map[string]int{
			"with": 1, "withPlainTime": 0, "withPlainDate": 1, "withCalendar": 1,
			"withTimeZone": 1, "add": 1, "subtract": 1, "until": 1, "since": 1,
			"round": 1, "equals": 1, "toPlainDate": 0, "toPlainTime": 0,
			"toPlainYearMonth": 0, "toPlainMonthDay": 0, "toZonedDateTime": 1,
		})},
		{object.TEMPORAL_PLAIN_DATE_OBJ, "PlainDate", 3, with(map[string]int{
			"with": 1, "withCalendar": 1, "add": 1, "subtract": 1, "until": 1,
			"since": 1, "equals": 1, "toPlainDateTime": 0, "toPlainYearMonth": 0,
			"toPlainMonthDay": 0, "toZonedDateTime": 1,
		})},
		{object.TEMPORAL_PLAIN_TIME_OBJ, "PlainTime", 0, with(map[string]int{
			"with": 1, "add": 1, "subtract": 1, "until": 1, "since": 1, "round": 1,
			"equals": 1, "toPlainDateTime": 1, "toZonedDateTime": 1,
		})},
		{object.TEMPORAL_PLAIN_YM_OBJ, "PlainYearMonth", 2, with(map[string]int{
			"with": 1, "add": 1, "subtract": 1, "until": 1, "since": 1,
			"equals": 1, "toPlainDate": 1,
		})},
		{object.TEMPORAL_PLAIN_MD_OBJ, "PlainMonthDay", 2, with(map[string]int{
			"with": 1, "equals": 1, "toPlainDate": 1,
		})},
		{object.TEMPORAL_ZONED_DATETIME_OBJ, "ZonedDateTime", 2, with(map[string]int{
			"with": 1, "withPlainTime": 0, "withPlainDate": 1, "withCalendar": 1,
			"withTimeZone": 1, "add": 1, "subtract": 1, "until": 1, "since": 1,
			"round": 1, "equals": 1, "startOfDay": 0, "toInstant": 0,
			"toPlainDate": 0, "toPlainTime": 0, "toPlainDateTime": 0,
			"toPlainYearMonth": 0, "toPlainMonthDay": 0, "getTimeZoneTransition": 1,
		})},
		{object.TEMPORAL_TIMEZONE_OBJ, "TimeZone", 1, with(map[string]int{
			"getOffsetNanosecondsFor": 1, "getOffsetStringFor": 1,
			"getPlainDateTimeFor": 1, "getInstantFor": 1, "getPossibleInstantsFor": 1,
			"getNextTransition": 1, "getPreviousTransition": 1,
		})},
		{object.TEMPORAL_CALENDAR_OBJ, "Calendar", 1, with(map[string]int{
			"dateAdd": 2, "dateUntil": 2, "year": 1, "month": 1, "monthCode": 1,
			"day": 1, "dayOfWeek": 1, "dayOfYear": 1, "weekOfYear": 1,
			"yearOfWeek": 1, "daysInWeek": 1, "daysInMonth": 1, "daysInYear": 1,
			"monthsInYear": 1, "inLeapYear": 1, "fields": 1, "mergeFields": 2,
			"dateFromFields": 1, "yearMonthFromFields": 1, "monthDayFromFields": 1,
		})},
	}
}

// tgApplyTemporalLengths 在 setupTemporal 末尾统一修正所有 length。
func tgApplyTemporalLengths(temporal *object.Object) {
	for _, e := range temporalLengthTables() {
		proto := object.GetTemporalProto(e.typ)
		if proto != nil {
			tgApplyLengths(proto, e.methods)
		}
		if cv, found := temporal.GetProperty(e.ctor); found {
			if bf, ok := cv.(*object.BuiltinFunction); ok {
				bf.SetFunctionLength(e.ctorLen)
				// 静态方法: from 1 / compare 2 (MonthDay 无 compare)
				if fv, ok := bf.GetProperty("from"); ok {
					tgSetFnLen(fv, 1)
				}
				if fv, ok := bf.GetProperty("compare"); ok {
					tgSetFnLen(fv, 2)
				}
				for _, n := range []string{"fromEpochMilliseconds", "fromEpochNanoseconds"} {
					if fv, ok := bf.GetProperty(n); ok {
						tgSetFnLen(fv, 1)
					}
				}
			}
		}
	}
	// Temporal.Now 的方法全部 0 必参
	if nv, found := temporal.GetProperty("Now"); found {
		if no, ok := nv.(*object.Object); ok {
			for _, n := range []string{"instant", "timeZoneId", "zonedDateTimeISO", "plainDateTimeISO", "plainDateISO", "plainTimeISO"} {
				if fv, ok2 := no.GetProperty(n); ok2 {
					tgSetFnLen(fv, 0)
				}
			}
		}
	}
}

// ===== 日历参数 =====

// toCalendarIDValue 实现 ToTemporalCalendarSlotValue (简化: 仅 iso8601)。
func toCalendarIDValue(v object.Value) (string, object.Value) {
	if c, ok := asCalendar(v); ok {
		return c.ID(), nil
	}
	s, ok := optionToString(v)
	if !ok {
		return "", tgType("Invalid calendar value")
	}
	if s == "" {
		return "", tgRange("Invalid calendar: empty string is not a valid calendar ID")
	}
	if s != object.DefaultCalendarID {
		return "", tgRange("Unsupported calendar: " + s)
	}
	return s, nil
}

// tgEraGetters 注册 era/eraYear getter。iso8601 日历下二者恒为 undefined，
// 但 getter 必须存在且做品牌检查 (跨类调用抛 TypeError)。
func tgEraGetters(proto *object.Object, typeName string, check func(object.Value) bool) {
	get := func(this object.Value) object.Value {
		if !check(this) {
			return tgTypeBadThis(typeName)
		}
		return object.UndefinedSingleton
	}
	tgGetter(proto, "era", get)
	tgGetter(proto, "eraYear", get)
}

// ===== 单位区间与增量 =====

// resolveAutoLargest 把 largestUnit=auto 解析成不小于 smallestUnit 的默认单位:
// 取 fallback 与 smallestUnit 中较大的那个 (rank 小者单位大)。
func resolveAutoLargest(smallest, fallback string) string {
	if unitRank(smallest) < unitRank(fallback) {
		return smallest
	}
	return fallback
}

// validRoundingIncrement 校验 roundingIncrement 对 smallestUnit 合法:
// 增量必须能整除上一级单位的容量 (小时 24、分/秒 60、毫秒及以下 1000)，
// 日历单位只允许 1。
func validRoundingIncrement(unit string, inc int64) bool {
	if inc < 1 {
		return false
	}
	switch unit {
	case "year", "month", "week", "day":
		return inc == 1
	case "hour":
		return inc < 24 && 24%inc == 0
	case "minute", "second":
		return inc < 60 && 60%inc == 0
	default:
		return inc < 1000 && 1000%inc == 0
	}
}

// ===== toString options =====

// toStringSettings 是 toString(options) 的解析结果。
type toStringSettings struct {
	precision    string // "auto"/"minute"/"0".."9"
	calendarName string // auto/always/never/critical
	timeZoneName string
	offsetName   string
	smallestUnit string // "" 表示未提供
	mode         string
}

// parseToStringSettings 解析并校验 toString 的 options。
// hasTime: 该类是否有时间部分 (决定是否接受 smallestUnit/fractionalSecondDigits)。
func parseToStringSettings(opts object.Value, hasTime bool) (*toStringSettings, object.Value) {
	s := &toStringSettings{
		precision: "auto", calendarName: "auto",
		timeZoneName: "auto", offsetName: "auto", mode: "trunc",
	}
	mode, merr := getRoundingMode(opts)
	if merr != nil {
		return nil, merr
	}
	s.mode = mode
	if hasTime {
		su := optValue(opts, "smallestUnit")
		if su != nil {
			str, ok := optionToString(su)
			if !ok {
				return nil, tgType("Cannot convert smallestUnit to string")
			}
			u, _ := normalizeUnit(str)
			switch u {
			case "minute", "second", "millisecond", "microsecond", "nanosecond":
				s.smallestUnit = u
			default:
				return nil, tgRange("Invalid smallestUnit for toString: " + str)
			}
		}
		fv := optValue(opts, "fractionalSecondDigits")
		if fv != nil {
			if s.smallestUnit != "" {
				return nil, tgRange("fractionalSecondDigits and smallestUnit cannot be used together")
			}
			if n, ok := fv.(*object.Number); ok {
				f := n.Value
				if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || f < 0 || f > 9 {
					return nil, tgRange("fractionalSecondDigits must be an integer from 0 to 9")
				}
				s.precision = strconv.Itoa(int(f))
			} else if str, ok := optionToString(fv); ok && str == "auto" {
				s.precision = "auto"
			} else {
				return nil, tgRange("Invalid fractionalSecondDigits")
			}
		}
	}
	if cn := optValue(opts, "calendarName"); cn != nil {
		str, ok := optionToString(cn)
		if !ok {
			return nil, tgType("Cannot convert calendarName to string")
		}
		switch str {
		case "auto", "always", "never", "critical":
			s.calendarName = str
		default:
			return nil, tgRange("Invalid calendarName: " + str)
		}
	}
	for _, k := range []struct {
		key string
		dst *string
	}{{"timeZoneName", &s.timeZoneName}, {"offset", &s.offsetName}} {
		if v := optValue(opts, k.key); v != nil {
			str, ok := optionToString(v)
			if !ok {
				return nil, tgType("Cannot convert " + k.key + " to string")
			}
			switch str {
			case "auto", "never", "critical":
				*k.dst = str
			default:
				return nil, tgRange("Invalid " + k.key + ": " + str)
			}
		}
	}
	return s, nil
}

// precisionForSmallest 把 smallestUnit 映射为显示精度。
func precisionForSmallest(su string) string {
	switch su {
	case "minute":
		return "minute"
	case "second":
		return "0"
	case "millisecond":
		return "3"
	case "microsecond":
		return "6"
	default:
		return "9"
	}
}

// calendarAnnotation 按 calendarName 生成日历注解串。
func calendarAnnotation(s *toStringSettings) string {
	switch s.calendarName {
	case "always":
		return "[u-ca=" + object.DefaultCalendarID + "]"
	case "critical":
		return "[!u-ca=" + object.DefaultCalendarID + "]"
	}
	return ""
}

// roundTimeOfDay 按 smallestUnit+mode 舍入一天内的时间 (环绕 24h)。
func roundTimeOfDay(t object.ISOTime, su, mode string) object.ISOTime {
	if su == "" {
		return t
	}
	n := big.NewInt(t.NanosOfDay())
	r := roundNanosToUnit(n, su, 1, mode)
	day := big.NewInt(object.NanosPerDay)
	r.Mod(r, day)
	if r.Sign() < 0 {
		r.Add(r, day)
	}
	return object.TimeFromNanosOfDay(r.Int64())
}

// roundDateTimeForDisplay 按 smallestUnit 舍入墙钟日期时间 (允许跨日进位)。
func roundDateTimeForDisplay(dt object.ISODateTime, su, mode string) object.ISODateTime {
	if su == "" {
		return dt
	}
	n := roundNanosToUnit(dt.EpochNanos(), su, 1, mode)
	return object.EpochNanosToISODateTime(n)
}

// formatZDTOffset 按 offsetName 设置输出偏移后缀。
func formatZDTOffset(offSec int32, show string) string {
	crit := show == "critical"
	if show == "never" {
		return ""
	}
	s := object.FormatOffsetSeconds(offSec)
	if crit {
		return "!" + s
	}
	return s
}

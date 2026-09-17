package stdlib

import (
	"math"
	"math/big"

	"github.com/14752222/Gox/object"
)

// 本文件实现 Temporal.PlainDate 与 Temporal.PlainTime。
//
// 单位的合法性在这两个类型上被严格切分:
//   - PlainDate 只接受日期单位 (year/month/week/day)，时间分量一律丢弃
//   - PlainTime 只接受时间单位 (hour…nanosecond)，且结果始终环绕一天

// dateOnlyUnits 与 timeOnlyUnits 分别是 PlainDate / PlainTime 的合法单位集。
var dateOnlyUnits = []string{"year", "month", "week", "day"}
var timeOnlyUnits = []string{"hour", "minute", "second", "millisecond", "microsecond", "nanosecond"}

// ==================== PlainDate ====================

func setupTemporalPlainDate(temporal *object.Object) {
	proto := object.NewObject()
	object.SetTemporalProto(object.TEMPORAL_PLAIN_DATE_OBJ, proto)

	dateGetter := func(name string, fn func(d object.ISODate) int64) {
		tgGetter(proto, name, func(this object.Value) object.Value {
			t, ok := asPlainDate(this)
			if !ok {
				return tgTypeBadThis("PlainDate")
			}
			return object.NewNumber(float64(fn(t.ISODate())))
		})
	}
	dateGetter("year", func(d object.ISODate) int64 { return int64(d.Year) })
	dateGetter("month", func(d object.ISODate) int64 { return int64(d.Month) })
	dateGetter("day", func(d object.ISODate) int64 { return int64(d.Day) })
	dateGetter("dayOfWeek", func(d object.ISODate) int64 { return int64(d.DayOfWeek()) })
	dateGetter("dayOfYear", func(d object.ISODate) int64 { return int64(d.DayOfYear()) })
	dateGetter("weekOfYear", func(d object.ISODate) int64 { return int64(d.WeekOfYear()) })
	dateGetter("daysInMonth", func(d object.ISODate) int64 {
		return int64(object.DaysInMonth(d.Year, d.Month))
	})
	dateGetter("daysInYear", func(d object.ISODate) int64 { return int64(object.DaysInYear(d.Year)) })
	dateGetter("monthsInYear", func(d object.ISODate) int64 { return 12 })
	dateGetter("daysInWeek", func(d object.ISODate) int64 { return 7 })

	tgGetter(proto, "monthCode", func(this object.Value) object.Value {
		t, ok := asPlainDate(this)
		if !ok {
			return tgTypeBadThis("PlainDate")
		}
		return object.NewString(objectPadMonth(t.ISODate().Month))
	})
	tgGetter(proto, "inLeapYear", func(this object.Value) object.Value {
		t, ok := asPlainDate(this)
		if !ok {
			return tgTypeBadThis("PlainDate")
		}
		return object.NewBoolean(t.ISODate().InLeapYear())
	})
	tgStrGetter(proto, "calendarId", func(this object.Value) string {
		t, ok := asPlainDate(this)
		if !ok {
			return ""
		}
		return t.CalendarID()
	})

	tgMethod(proto, "with", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDate(this)
		if !ok {
			return tgTypeBadThis("PlainDate")
		}
		if len(args) == 0 {
			return tgType("PlainDate.prototype.with requires an object")
		}
		d, ok := mergeDateFields(t.ISODate(), args[0])
		if !ok {
			return tgType("PlainDate.prototype.with: invalid fields")
		}
		return object.NewTemporalPlainDate(d, t.CalendarID())
	})

	tgMethod(proto, "add", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDate(this)
		if !ok {
			return tgTypeBadThis("PlainDate")
		}
		return plainDateShift(t, args, false)
	})
	tgMethod(proto, "subtract", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDate(this)
		if !ok {
			return tgTypeBadThis("PlainDate")
		}
		return plainDateShift(t, args, true)
	})

	tgMethod(proto, "until", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDate(this)
		if !ok {
			return tgTypeBadThis("PlainDate")
		}
		return plainDateDiffFrom(t.ISODate(), args, false)
	})
	tgMethod(proto, "since", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDate(this)
		if !ok {
			return tgTypeBadThis("PlainDate")
		}
		return plainDateDiffFrom(t.ISODate(), args, true)
	})

	tgMethod(proto, "equals", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDate(this)
		if !ok {
			return tgTypeBadThis("PlainDate")
		}
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		o, ok := asPlainDate(args[0])
		if !ok {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(t.ISODate() == o.ISODate())
	})

	// ===== 转换 =====
	tgMethod(proto, "toPlainDateTime", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDate(this)
		if !ok {
			return tgTypeBadThis("PlainDate")
		}
		time := object.ISOTime{}
		if len(args) > 0 {
			if pt, ok := asPlainTime(args[0]); ok {
				time = pt.ISOTime()
			}
		}
		return object.NewTemporalPlainDateTime(object.ISODateTime{Date: t.ISODate(), Time: time}, t.CalendarID())
	})

	tgMethod(proto, "toPlainYearMonth", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDate(this)
		if !ok {
			return tgTypeBadThis("PlainDate")
		}
		d := t.ISODate()
		return object.NewTemporalPlainYearMonth(d.Year, d.Month, d.Day, t.CalendarID())
	})

	tgMethod(proto, "toPlainMonthDay", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDate(this)
		if !ok {
			return tgTypeBadThis("PlainDate")
		}
		d := t.ISODate()
		return object.NewTemporalPlainMonthDay(d.Month, d.Day, d.Year, t.CalendarID())
	})

	tgMethod(proto, "toZonedDateTime", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDate(this)
		if !ok {
			return tgTypeBadThis("PlainDate")
		}
		if len(args) == 0 {
			return tgType("toZonedDateTime requires a time zone")
		}
		tz, ok := toTimeZoneID(args[0])
		if !ok {
			return tgRange("Invalid time zone")
		}
		dt := object.ISODateTime{Date: t.ISODate(), Time: object.ISOTime{}}
		instants := object.GetPossibleInstantsFor(tz, dt)
		ns, ok := object.DisambiguatePossibleInstants(instants, tz, dt, "compatible")
		if !ok {
			return tgRange("Ambiguous or non-existent wall-clock time")
		}
		return object.NewTemporalZonedDateTime(ns, tz, t.CalendarID())
	})

	tgToStringMethod(proto, func(this object.Value) string {
		t, ok := asPlainDate(this)
		if !ok {
			return ""
		}
		return t.ToString()
	})
	tgMethod(proto, "valueOf", func(this object.Value, args ...object.Value) object.Value {
		return this
	})

	ctor := object.NewBuiltin("PlainDate", func(args ...object.Value) object.Value {
		if len(args) < 3 {
			return tgType("PlainDate requires year, month, day")
		}
		f := func(i int) int32 {
			v, _ := argFloat(args, i)
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return 0
			}
			return int32(math.Trunc(v))
		}
		d := object.ISODate{Year: f(0), Month: f(1), Day: f(2)}
		if !object.IsValidISODate(d) {
			return tgRange("Invalid ISO date")
		}
		return object.NewTemporalPlainDate(d, "")
	})
	ctor.SetProperty("name", object.NewString("PlainDate"))
	ctor.SetProperty("prototype", proto)
	proto.SetProperty("constructor", ctor)

	tgStatic(ctor, "from", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return tgType("PlainDate.from requires an argument")
		}
		if pd, ok := asPlainDate(args[0]); ok {
			return pd
		}
		switch v := args[0].(type) {
		case *object.String:
			f, ok := object.ParseTemporalDateString(v.Value)
			if !ok {
				return tgRange("Invalid PlainDate string: " + v.Value)
			}
			if f.Calendar != "" && f.Calendar != object.DefaultCalendarID {
				return tgRange("Unsupported calendar: " + f.Calendar)
			}
			return object.NewTemporalPlainDate(f.ISODateTime().Date, object.DefaultCalendarID)
		case *object.Object:
			d, ok := dateFromFieldsObject(v)
			if !ok {
				return tgType("PlainDate.from: invalid fields")
			}
			return object.NewTemporalPlainDate(d, object.DefaultCalendarID)
		}
		return tgType("PlainDate.from: unsupported argument")
	})

	tgStatic(ctor, "compare", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return tgType("compare requires two PlainDates")
		}
		a, _ := asPlainDate(args[0])
		b, _ := asPlainDate(args[1])
		if a == nil || b == nil {
			return tgType("compare: arguments must be PlainDate")
		}
		x, y := a.ISODate().EpochDays(), b.ISODate().EpochDays()
		if x < y {
			return object.NewNumber(-1)
		}
		if x > y {
			return object.NewNumber(1)
		}
		return object.NewNumber(0)
	})

	setToStringTag(proto, "Temporal.PlainDate")
	temporal.SetProperty("PlainDate", ctor)
}

// plainDateShift 实现 PlainDate.add/subtract。
// Duration 的时间分量在日期运算中被忽略——加到没有时间概念的日期上没有意义。
func plainDateShift(t *object.TemporalPlainDate, args []object.Value, negate bool) object.Value {
	if len(args) == 0 {
		return tgType("missing duration")
	}
	d, ok := toDurationValue(args[0])
	if !ok {
		return tgType("invalid duration")
	}
	overflow := "constrain"
	if len(args) > 1 {
		overflow = getOverflow(args[1])
		if overflow == "invalid" {
			return tgRange("Invalid overflow option")
		}
	}
	if negate {
		d = d.Negated()
	}
	res, ok := object.AddDateDuration(t.ISODate(),
		int32(d.Years()), int32(d.Months()), int32(d.Weeks()), int32(d.Days()), overflow)
	if !ok {
		return tgRange("Result is outside of supported range")
	}
	return object.NewTemporalPlainDate(res, t.CalendarID())
}

// plainDateDiffFrom 计算两个 PlainDate 的差值。
func plainDateDiffFrom(from object.ISODate, args []object.Value, since bool) object.Value {
	if len(args) == 0 {
		return tgType("missing argument")
	}
	other, ok := asPlainDate(args[0])
	if !ok {
		return tgType("argument is not a PlainDate")
	}
	var opts object.Value
	if len(args) > 1 {
		opts = args[1]
	}
	largest, ok := getLargestUnit(opts, dateOnlyUnits)
	if !ok {
		return tgRange("Invalid largestUnit for PlainDate")
	}
	to := other.ISODate()
	if since {
		from, to = to, from
	}
	sign := int64(1)
	if from.EpochDays() > to.EpochDays() {
		sign = -1
		from, to = to, from
	}
	if largest == "auto" {
		largest = "day"
	}
	years, months, weeks, days := object.DiffISODate(from, to, largest)
	dur := object.NewTemporalDuration(
		int64(years), int64(months), int64(weeks), int64(days), 0, 0, 0, 0, 0, 0)
	if sign < 0 {
		return dur.Negated()
	}
	return dur
}

// dateFromFieldsObject 从属性包构造 ISODate。
func dateFromFieldsObject(o *object.Object) (object.ISODate, bool) {
	get := func(k string) (int32, bool) {
		v, found := o.GetProperty(k)
		if !found || v == object.UndefinedSingleton {
			return 0, false
		}
		f := toFloat(v)
		return int32(f), true
	}
	y, okY := get("year")
	m, okM := get("month")
	d, okD := get("day")
	if !okY || !okM || !okD {
		return object.ISODate{}, false
	}
	out := object.ISODate{Year: y, Month: m, Day: d}
	if !object.IsValidISODate(out) {
		return object.ISODate{}, false
	}
	return out, true
}

// mergeDateFields 把属性包中的字段合并到已有 ISODate。
func mergeDateFields(base object.ISODate, src object.Value) (object.ISODate, bool) {
	o, ok := src.(*object.Object)
	if !ok {
		return base, false
	}
	if v, found := o.GetProperty("year"); found && v != object.UndefinedSingleton {
		base.Year = int32(toFloat(v))
	}
	if v, found := o.GetProperty("month"); found && v != object.UndefinedSingleton {
		base.Month = int32(toFloat(v))
	}
	if v, found := o.GetProperty("day"); found && v != object.UndefinedSingleton {
		base.Day = int32(toFloat(v))
	}
	if !object.IsValidISODate(base) {
		return base, false
	}
	return base, true
}

// ==================== PlainTime ====================

func setupTemporalPlainTime(temporal *object.Object) {
	proto := object.NewObject()
	object.SetTemporalProto(object.TEMPORAL_PLAIN_TIME_OBJ, proto)

	timeGetter := func(name string, fn func(t object.ISOTime) int64) {
		tgGetter(proto, name, func(this object.Value) object.Value {
			t, ok := asPlainTime(this)
			if !ok {
				return tgTypeBadThis("PlainTime")
			}
			return object.NewNumber(float64(fn(t.ISOTime())))
		})
	}
	timeGetter("hour", func(t object.ISOTime) int64 { return int64(t.Hour) })
	timeGetter("minute", func(t object.ISOTime) int64 { return int64(t.Minute) })
	timeGetter("second", func(t object.ISOTime) int64 { return int64(t.Second) })
	timeGetter("millisecond", func(t object.ISOTime) int64 { return int64(t.Millisecond) })
	timeGetter("microsecond", func(t object.ISOTime) int64 { return int64(t.Microsecond) })
	timeGetter("nanosecond", func(t object.ISOTime) int64 { return int64(t.Nanosecond) })

	tgMethod(proto, "with", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainTime(this)
		if !ok {
			return tgTypeBadThis("PlainTime")
		}
		if len(args) == 0 {
			return tgType("PlainTime.prototype.with requires an object")
		}
		o, ok := args[0].(*object.Object)
		if !ok {
			return tgType("PlainTime.prototype.with: invalid fields")
		}
		time := t.ISOTime()
		if !mergeTimeInto(&time, o) || !object.IsValidISOTime(time) {
			return tgType("PlainTime.prototype.with: invalid fields")
		}
		return object.NewTemporalPlainTime(time)
	})

	tgMethod(proto, "add", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainTime(this)
		if !ok {
			return tgTypeBadThis("PlainTime")
		}
		return plainTimeShift(t, args, false)
	})
	tgMethod(proto, "subtract", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainTime(this)
		if !ok {
			return tgTypeBadThis("PlainTime")
		}
		return plainTimeShift(t, args, true)
	})

	tgMethod(proto, "until", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainTime(this)
		if !ok {
			return tgTypeBadThis("PlainTime")
		}
		return plainTimeDiffFrom(t.ISOTime(), args, false)
	})
	tgMethod(proto, "since", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainTime(this)
		if !ok {
			return tgTypeBadThis("PlainTime")
		}
		return plainTimeDiffFrom(t.ISOTime(), args, true)
	})

	tgMethod(proto, "round", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainTime(this)
		if !ok {
			return tgTypeBadThis("PlainTime")
		}
		var opts object.Value
		if len(args) > 0 {
			opts = args[0]
		}
		smallest, ok := getSmallestUnit(opts, timeOnlyUnits)
		if !ok || smallest == "auto" {
			return tgRange("Invalid smallestUnit for PlainTime.round")
		}
		inc, ok := getRoundingIncrement(opts)
		if !ok {
			return tgRange("Invalid roundingIncrement")
		}
		mode := getRoundingMode(opts)
		if mode == "invalid" {
			return tgRange("Invalid roundingMode")
		}
		total := big.NewInt(t.ISOTime().NanosOfDay())
		rounded := roundNanosToUnit(total, smallest, inc, mode)
		// 舍入可能跨两次日界 (如 23:59 round 到 hour + ceil → 次日 00:00)，
		// PlainTime 的合法区间是单日内部，故按环绕处理。
		_, rem := quoRemForDay(rounded)
		return object.NewTemporalPlainTime(object.TimeFromNanosOfDay(rem))
	})

	tgMethod(proto, "equals", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainTime(this)
		if !ok {
			return tgTypeBadThis("PlainTime")
		}
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		o, ok := asPlainTime(args[0])
		if !ok {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(t.ISOTime() == o.ISOTime())
	})

	tgMethod(proto, "toPlainDateTime", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainTime(this)
		if !ok {
			return tgTypeBadThis("PlainTime")
		}
		date := object.ISODate{Year: 1970, Month: 1, Day: 1}
		if len(args) > 0 {
			if pd, ok := asPlainDate(args[0]); ok {
				date = pd.ISODate()
			}
		}
		return object.NewTemporalPlainDateTime(object.ISODateTime{Date: date, Time: t.ISOTime()}, "")
	})

	tgToStringMethod(proto, func(this object.Value) string {
		t, ok := asPlainTime(this)
		if !ok {
			return ""
		}
		return t.ToString()
	})
	tgMethod(proto, "valueOf", func(this object.Value, args ...object.Value) object.Value {
		return this
	})

	ctor := object.NewBuiltin("PlainTime", func(args ...object.Value) object.Value {
		f := func(i int) int32 {
			if i >= len(args) {
				return 0
			}
			v, _ := argFloat(args, i)
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return 0
			}
			return int32(math.Trunc(v))
		}
		time := object.ISOTime{Hour: f(0), Minute: f(1), Second: f(2),
			Millisecond: f(3), Microsecond: f(4), Nanosecond: f(5)}
		if !object.IsValidISOTime(time) {
			return tgRange("Invalid ISO time")
		}
		return object.NewTemporalPlainTime(time)
	})
	ctor.SetProperty("name", object.NewString("PlainTime"))
	ctor.SetProperty("prototype", proto)
	proto.SetProperty("constructor", ctor)

	tgStatic(ctor, "from", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return tgType("PlainTime.from requires an argument")
		}
		if pt, ok := asPlainTime(args[0]); ok {
			return pt
		}
		switch v := args[0].(type) {
		case *object.String:
			f, ok := object.ParseTemporalTimeString(v.Value)
			if !ok {
				return tgRange("Invalid PlainTime string: " + v.Value)
			}
			return object.NewTemporalPlainTime(f.ISODateTime().Time)
		case *object.Object:
			time := object.ISOTime{}
			if !mergeTimeInto(&time, v) || !object.IsValidISOTime(time) {
				return tgType("PlainTime.from: invalid fields")
			}
			return object.NewTemporalPlainTime(time)
		}
		return tgType("PlainTime.from: unsupported argument")
	})

	tgStatic(ctor, "compare", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return tgType("compare requires two PlainTimes")
		}
		a, _ := asPlainTime(args[0])
		b, _ := asPlainTime(args[1])
		if a == nil || b == nil {
			return tgType("compare: arguments must be PlainTime")
		}
		x, y := a.ISOTime().NanosOfDay(), b.ISOTime().NanosOfDay()
		if x < y {
			return object.NewNumber(-1)
		}
		if x > y {
			return object.NewNumber(1)
		}
		return object.NewNumber(0)
	})

	setToStringTag(proto, "Temporal.PlainTime")
	temporal.SetProperty("PlainTime", ctor)
}

// plainTimeShift 实现 PlainTime.add/subtract，结果环绕一天。
func plainTimeShift(t *object.TemporalPlainTime, args []object.Value, negate bool) object.Value {
	if len(args) == 0 {
		return tgType("missing duration")
	}
	d, ok := toDurationValue(args[0])
	if !ok {
		return tgType("invalid duration")
	}
	if negate {
		d = d.Negated()
	}
	total := new(big.Int).Add(big.NewInt(t.ISOTime().NanosOfDay()), d.TimeTotalNanos())
	_, rem := quoRemForDay(total)
	return object.NewTemporalPlainTime(object.TimeFromNanosOfDay(rem))
}

// plainTimeDiffFrom 计算两个 PlainTime 的差值 (环绕一天语义)。
func plainTimeDiffFrom(from object.ISOTime, args []object.Value, since bool) object.Value {
	if len(args) == 0 {
		return tgType("missing argument")
	}
	other, ok := asPlainTime(args[0])
	if !ok {
		return tgType("argument is not a PlainTime")
	}
	var opts object.Value
	if len(args) > 1 {
		opts = args[1]
	}
	largest, ok := getLargestUnit(opts, timeOnlyUnits)
	if !ok {
		return tgRange("Invalid largestUnit for PlainTime")
	}
	smallest, ok := getSmallestUnit(opts, timeOnlyUnits)
	if !ok {
		return tgRange("Invalid smallestUnit for PlainTime")
	}
	// PlainTime 的差值始终环绕一天: 23:00 到 01:00 是 +2 小时而非 -22 小时。
	diff := other.ISOTime().NanosOfDay() - from.NanosOfDay()
	if diff < 0 {
		diff += object.NanosPerDay
	}
	if largest == "auto" {
		largest = "hour"
	}
	su := smallest
	if su == "auto" {
		su = "nanosecond"
	}
	rounded := roundNanosToUnit(big.NewInt(diff), su, 1, "trunc")
	dur := durationFromNanosByLargest(rounded, largest)
	if since {
		return dur.Negated()
	}
	return dur
}

// quoRemForDay 把纳秒值拆成 (跨天天数, 日内纳秒)。
func quoRemForDay(total *big.Int) (int64, int64) {
	q, r := new(big.Int).QuoRem(total, big.NewInt(object.NanosPerDay), new(big.Int))
	if r.Sign() < 0 {
		q.Sub(q, big.NewInt(1))
		r.Add(r, big.NewInt(object.NanosPerDay))
	}
	return q.Int64(), r.Int64()
}

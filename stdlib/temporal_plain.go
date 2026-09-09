package stdlib

import (
	"fmt"
	"math"
	"math/big"

	"js-runtime/object"
)

// 本文件实现 Temporal 的"墙钟时间"三兄弟: PlainDateTime / PlainDate / PlainTime。
//
// 它们的共同点是都与时区无关——这正是 Temporal 修复 Date 的关键设计:
// Date 的 getHours() 隐含依赖宿主本地时区，同一段代码在不同机器上行为不同；
// 而 PlainDateTime 不含时区信息，跨时区的结果是确定的。

// datetimeUnits 是 PlainDateTime 支持的单位全集 (日期 + 时间)。
var datetimeUnits = []string{"year", "month", "week", "day",
	"hour", "minute", "second", "millisecond", "microsecond", "nanosecond"}

func setupTemporalPlainDateTime(temporal *object.Object) {
	proto := object.NewObject()
	object.SetTemporalProto(object.TEMPORAL_PLAIN_DATETIME_OBJ, proto)

	// ===== 墙钟字段 getter =====
	dtGetter := func(name string, fn func(dt object.ISODateTime) int64) {
		tgGetter(proto, name, func(this object.Value) object.Value {
			t, ok := asPlainDateTime(this)
			if !ok {
				return tgTypeBadThis("PlainDateTime")
			}
			return object.NewNumber(float64(fn(t.ISODateTime())))
		})
	}
	dtGetter("year", func(dt object.ISODateTime) int64 { return int64(dt.Date.Year) })
	dtGetter("month", func(dt object.ISODateTime) int64 { return int64(dt.Date.Month) })
	dtGetter("day", func(dt object.ISODateTime) int64 { return int64(dt.Date.Day) })
	dtGetter("hour", func(dt object.ISODateTime) int64 { return int64(dt.Time.Hour) })
	dtGetter("minute", func(dt object.ISODateTime) int64 { return int64(dt.Time.Minute) })
	dtGetter("second", func(dt object.ISODateTime) int64 { return int64(dt.Time.Second) })
	dtGetter("millisecond", func(dt object.ISODateTime) int64 { return int64(dt.Time.Millisecond) })
	dtGetter("microsecond", func(dt object.ISODateTime) int64 { return int64(dt.Time.Microsecond) })
	dtGetter("nanosecond", func(dt object.ISODateTime) int64 { return int64(dt.Time.Nanosecond) })
	dtGetter("dayOfWeek", func(dt object.ISODateTime) int64 { return int64(dt.Date.DayOfWeek()) })
	dtGetter("dayOfYear", func(dt object.ISODateTime) int64 { return int64(dt.Date.DayOfYear()) })
	dtGetter("weekOfYear", func(dt object.ISODateTime) int64 { return int64(dt.Date.WeekOfYear()) })
	dtGetter("daysInMonth", func(dt object.ISODateTime) int64 {
		return int64(object.DaysInMonth(dt.Date.Year, dt.Date.Month))
	})
	dtGetter("daysInYear", func(dt object.ISODateTime) int64 { return int64(object.DaysInYear(dt.Date.Year)) })
	dtGetter("monthsInYear", func(dt object.ISODateTime) int64 { return 12 })
	dtGetter("daysInWeek", func(dt object.ISODateTime) int64 { return 7 })

	tgGetter(proto, "monthCode", func(this object.Value) object.Value {
		t, ok := asPlainDateTime(this)
		if !ok {
			return tgTypeBadThis("PlainDateTime")
		}
		return object.NewString(objectPadMonth(t.ISODateTime().Date.Month))
	})
	tgGetter(proto, "inLeapYear", func(this object.Value) object.Value {
		t, ok := asPlainDateTime(this)
		if !ok {
			return tgTypeBadThis("PlainDateTime")
		}
		return object.NewBoolean(t.ISODateTime().Date.InLeapYear())
	})
	tgStrGetter(proto, "calendarId", func(this object.Value) string {
		t, ok := asPlainDateTime(this)
		if !ok {
			return ""
		}
		return t.CalendarID()
	})

	// ===== 构造与替换 =====
	tgMethod(proto, "with", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDateTime(this)
		if !ok {
			return tgTypeBadThis("PlainDateTime")
		}
		if len(args) == 0 {
			return tgType("PlainDateTime.prototype.with requires an object")
		}
		dt, ok := mergeDateTimeFields(t.ISODateTime(), args[0], false)
		if !ok {
			return tgType("PlainDateTime.prototype.with: invalid fields")
		}
		return object.NewTemporalPlainDateTime(dt, t.CalendarID())
	})

	tgMethod(proto, "withPlainTime", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDateTime(this)
		if !ok {
			return tgTypeBadThis("PlainDateTime")
		}
		dt := t.ISODateTime()
		if len(args) > 0 {
			if pt, ok := asPlainTime(args[0]); ok {
				dt.Time = pt.ISOTime()
			} else if merged, ok := mergeTimeFields(dt.Time, args[0]); ok {
				dt.Time = merged
			} else if args[0] != object.UndefinedSingleton && args[0] != nil {
				return tgType("withPlainTime: invalid argument")
			}
		}
		return object.NewTemporalPlainDateTime(dt, t.CalendarID())
	})

	tgMethod(proto, "withPlainDate", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDateTime(this)
		if !ok {
			return tgTypeBadThis("PlainDateTime")
		}
		if len(args) == 0 {
			return tgType("withPlainDate requires a PlainDate")
		}
		pd, ok := asPlainDate(args[0])
		if !ok {
			return tgType("withPlainDate: argument is not a PlainDate")
		}
		dt := t.ISODateTime()
		dt.Date = pd.ISODate()
		return object.NewTemporalPlainDateTime(dt, t.CalendarID())
	})

	// ===== 算术 =====
	tgMethod(proto, "add", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDateTime(this)
		if !ok {
			return tgTypeBadThis("PlainDateTime")
		}
		if len(args) == 0 {
			return tgType("PlainDateTime.prototype.add requires a Duration")
		}
		d, ok := toDurationValue(args[0])
		if !ok {
			return tgType("PlainDateTime.prototype.add: invalid duration")
		}
		overflow := "constrain"
		if len(args) > 1 {
			overflow = getOverflow(args[1])
			if overflow == "invalid" {
				return tgRange("Invalid overflow option")
			}
		}
		res, ok := object.AddDateTime(t.ISODateTime(), d, overflow)
		if !ok {
			return tgRange("Result is outside of supported range")
		}
		return object.NewTemporalPlainDateTime(res, t.CalendarID())
	})

	tgMethod(proto, "subtract", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDateTime(this)
		if !ok {
			return tgTypeBadThis("PlainDateTime")
		}
		if len(args) == 0 {
			return tgType("PlainDateTime.prototype.subtract requires a Duration")
		}
		d, ok := toDurationValue(args[0])
		if !ok {
			return tgType("PlainDateTime.prototype.subtract: invalid duration")
		}
		overflow := "constrain"
		if len(args) > 1 {
			overflow = getOverflow(args[1])
			if overflow == "invalid" {
				return tgRange("Invalid overflow option")
			}
		}
		res, ok := object.AddDateTime(t.ISODateTime(), d.Negated(), overflow)
		if !ok {
			return tgRange("Result is outside of supported range")
		}
		return object.NewTemporalPlainDateTime(res, t.CalendarID())
	})

	// ===== 差值 =====
	tgMethod(proto, "until", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDateTime(this)
		if !ok {
			return tgTypeBadThis("PlainDateTime")
		}
		return dateTimeDiffFrom(t.ISODateTime(), args, false)
	})
	tgMethod(proto, "since", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDateTime(this)
		if !ok {
			return tgTypeBadThis("PlainDateTime")
		}
		return dateTimeDiffFrom(t.ISODateTime(), args, true)
	})

	// ===== 舍入 =====
	tgMethod(proto, "round", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDateTime(this)
		if !ok {
			return tgTypeBadThis("PlainDateTime")
		}
		var opts object.Value
		if len(args) > 0 {
			opts = args[0]
		}
		smallest, ok := getSmallestUnit(opts, datetimeUnits)
		if !ok || smallest == "auto" {
			return tgRange("Invalid smallestUnit for PlainDateTime.round")
		}
		inc, ok := getRoundingIncrement(opts)
		if !ok {
			return tgRange("Invalid roundingIncrement")
		}
		mode := getRoundingMode(opts)
		if mode == "invalid" {
			return tgRange("Invalid roundingMode")
		}
		return roundDateTime(t.ISODateTime(), t.CalendarID(), smallest, inc, mode)
	})

	// ===== 比较与转换 =====
	tgMethod(proto, "equals", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDateTime(this)
		if !ok {
			return tgTypeBadThis("PlainDateTime")
		}
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		o, ok := asPlainDateTime(args[0])
		if !ok {
			return object.NewBoolean(false)
		}
		a, b := t.ISODateTime(), o.ISODateTime()
		return object.NewBoolean(a.Date == b.Date && a.Time == b.Time)
	})

	tgMethod(proto, "toPlainDate", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDateTime(this)
		if !ok {
			return tgTypeBadThis("PlainDateTime")
		}
		return object.NewTemporalPlainDate(t.ISODateTime().Date, t.CalendarID())
	})
	tgMethod(proto, "toPlainTime", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDateTime(this)
		if !ok {
			return tgTypeBadThis("PlainDateTime")
		}
		return object.NewTemporalPlainTime(t.ISODateTime().Time)
	})
	tgMethod(proto, "toZonedDateTime", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDateTime(this)
		if !ok {
			return tgTypeBadThis("PlainDateTime")
		}
		if len(args) == 0 {
			return tgType("toZonedDateTime requires a time zone")
		}
		tz, ok := toTimeZoneID(args[0])
		if !ok {
			return tgRange("Invalid time zone")
		}
		disambiguation := "compatible"
		if len(args) > 1 {
			disambiguation = getDisambiguation(args[1])
			if disambiguation == "invalid" {
				return tgRange("Invalid disambiguation")
			}
		}
		instants := object.GetPossibleInstantsFor(tz, t.ISODateTime())
		ns, ok := object.DisambiguatePossibleInstants(instants, tz, t.ISODateTime(), disambiguation)
		if !ok {
			return tgRange("Ambiguous or non-existent wall-clock time (use disambiguation option)")
		}
		return object.NewTemporalZonedDateTime(ns, tz, t.CalendarID())
	})

	tgToStringMethod(proto, func(this object.Value) string {
		t, ok := asPlainDateTime(this)
		if !ok {
			return ""
		}
		return t.ToString()
	})
	tgMethod(proto, "valueOf", func(this object.Value, args ...object.Value) object.Value {
		return this
	})

	// ===== 构造器 =====
	ctor := object.NewBuiltin("PlainDateTime", func(args ...object.Value) object.Value {
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
		if len(args) < 3 {
			return tgType("PlainDateTime requires at least year, month, day")
		}
		date := object.ISODate{Year: f(0), Month: f(1), Day: f(2)}
		if !object.IsValidISODate(date) {
			return tgRange("Invalid ISO date")
		}
		time := object.ISOTime{Hour: f(3), Minute: f(4), Second: f(5),
			Millisecond: f(6), Microsecond: f(7), Nanosecond: f(8)}
		if !object.IsValidISOTime(time) {
			return tgRange("Invalid ISO time")
		}
		return object.NewTemporalPlainDateTime(object.ISODateTime{Date: date, Time: time}, "")
	})
	ctor.SetProperty("name", object.NewString("PlainDateTime"))
	ctor.SetProperty("prototype", proto)
	proto.SetProperty("constructor", ctor)

	tgStatic(ctor, "from", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return tgType("PlainDateTime.from requires an argument")
		}
		if pdt, ok := asPlainDateTime(args[0]); ok {
			return pdt
		}
		switch v := args[0].(type) {
		case *object.String:
			f, ok := object.ParseTemporalDateTimeString(v.Value)
			if !ok {
				return tgRange("Invalid PlainDateTime string: " + v.Value)
			}
			if f.Calendar != "" && f.Calendar != object.DefaultCalendarID {
				return tgRange("Unsupported calendar: " + f.Calendar)
			}
			return object.NewTemporalPlainDateTime(f.ISODateTime(), object.DefaultCalendarID)
		case *object.Object:
			dt, ok := dateTimeFromFieldsObject(v)
			if !ok {
				return tgType("PlainDateTime.from: invalid fields")
			}
			return object.NewTemporalPlainDateTime(dt, object.DefaultCalendarID)
		}
		return tgType("PlainDateTime.from: unsupported argument")
	})

	tgStatic(ctor, "compare", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return tgType("compare requires two PlainDateTimes")
		}
		a, _ := asPlainDateTime(args[0])
		b, _ := asPlainDateTime(args[1])
		if a == nil || b == nil {
			return tgType("compare: arguments must be PlainDateTime")
		}
		return object.NewNumber(float64(compareISODateTime(a.ISODateTime(), b.ISODateTime())))
	})

	setToStringTag(proto, "Temporal.PlainDateTime")
	temporal.SetProperty("PlainDateTime", ctor)
}

// ===== 内部辅助 =====

// compareISODateTime 比较两个 ISO 日期时间。
func compareISODateTime(a, b object.ISODateTime) int {
	if a.Date.EpochDays() != b.Date.EpochDays() {
		if a.Date.EpochDays() < b.Date.EpochDays() {
			return -1
		}
		return 1
	}
	an, bn := a.Time.NanosOfDay(), b.Time.NanosOfDay()
	switch {
	case an < bn:
		return -1
	case an > bn:
		return 1
	}
	return 0
}

// roundDateTime 把 PlainDateTime 按 smallestUnit 舍入。
//
// 舍入锚点是当天零点 (midnight): 规范规定 PlainDateTime 的 round 以
// 当日午夜为基准计算偏移后再舍入，再叠加回午夜。跨日舍入 (如 smallestUnit
// = day) 会自然得到午夜零点。
func roundDateTime(dt object.ISODateTime, calendar, smallest string, inc int64, mode string) object.Value {
	midnight := object.ISODateTime{Date: dt.Date, Time: object.ISOTime{}}
	diff := new(big.Int).Sub(dt.EpochNanos(), midnight.EpochNanos())
	rounded := roundNanosToUnit(diff, smallest, inc, mode)
	res := new(big.Int).Add(midnight.EpochNanos(), rounded)
	if !object.IsValidEpochNanos(res) {
		return tgRange("Result is outside of supported range")
	}
	return object.NewTemporalPlainDateTime(object.EpochNanosToISODateTime(res), calendar)
}

// dateTimeDiffFrom 计算 PlainDateTime 之间的差值 Duration。
func dateTimeDiffFrom(from object.ISODateTime, args []object.Value, since bool) object.Value {
	if len(args) == 0 {
		return tgType("missing argument")
	}
	toPtr, ok := asPlainDateTime(args[0])
	if !ok {
		return tgType("argument is not a PlainDateTime")
	}
	var opts object.Value
	if len(args) > 1 {
		opts = args[1]
	}
	largest, ok := getLargestUnit(opts, datetimeUnits)
	if !ok {
		return tgRange("Invalid largestUnit")
	}
	smallest, ok := getSmallestUnit(opts, datetimeUnits)
	if !ok {
		return tgRange("Invalid smallestUnit")
	}
	inc, ok := getRoundingIncrement(opts)
	if !ok {
		return tgRange("Invalid roundingIncrement")
	}
	mode := getRoundingMode(opts)
	if mode == "invalid" {
		return tgRange("Invalid roundingMode")
	}

	to := toPtr.ISODateTime()
	// since 反转方向后统一按 "from → to" 的顺序计算
	if since {
		from, to = to, from
	}
	if largest == "auto" {
		largest = "day"
	}
	su := smallest
	if su == "auto" {
		su = "nanosecond"
	}
	if unitRank(su) < unitRank(largest) {
		return tgRange("smallestUnit is larger than largestUnit")
	}

	// 统一方向: 保证 from <= to，负数结果由末尾的 Negated 还原
	sign := int64(1)
	if compareISODateTime(from, to) > 0 {
		sign = -1
		from, to = to, from
	}

	// largestUnit 为"日"及以上时走两段式: 先算日历差 (精确到 largestUnit)，
	// 再把剩余的时间落差作为时间部分。这样 1月31日 → 3月1日 在 largestUnit
	// = month 下得到 P1M1D 而不是含糊的"31 天"。
	if unitRank(largest) <= unitRank("day") {
		years, months, weeks, days := object.DiffISODate(from.Date, to.Date, largest)
		interDate, ok := object.AddDateDuration(from.Date, years, months, weeks, days, "constrain")
		if !ok {
			return tgRange("Difference out of range")
		}
		interDT := object.ISODateTime{Date: interDate, Time: from.Time}
		rem := new(big.Int).Sub(to.EpochNanos(), interDT.EpochNanos())
		timeDur := durationFromNanosByLargest(rem, su)
		dur := object.NewTemporalDuration(
			int64(years), int64(months), int64(weeks), int64(days),
			timeDur.Hours(), timeDur.Minutes(), timeDur.Seconds(),
			timeDur.Milliseconds(), timeDur.Microseconds(), timeDur.Nanoseconds())
		if sign < 0 {
			return dur.Negated()
		}
		return dur
	}

	// 其余情况整体按纳秒差处理后再按 largestUnit 拆分
	total := new(big.Int).Sub(to.EpochNanos(), from.EpochNanos())
	rounded := roundNanosToUnit(total, su, inc, mode)
	dur := durationFromNanosByLargest(rounded, largest)
	if sign < 0 {
		return dur.Negated()
	}
	return dur
}

// dateTimeFromFieldsObject 从属性包构造 ISODateTime。
func dateTimeFromFieldsObject(o *object.Object) (object.ISODateTime, bool) {
	date := object.ISODate{Year: 1970, Month: 1, Day: 1}
	for _, k := range []string{"year", "month", "day"} {
		v, found := o.GetProperty(k)
		if !found || v == object.UndefinedSingleton {
			return object.ISODateTime{}, false
		}
		f := toFloat(v)
		switch k {
		case "year":
			date.Year = int32(f)
		case "month":
			date.Month = int32(f)
		case "day":
			date.Day = int32(f)
		}
	}
	if !object.IsValidISODate(date) {
		return object.ISODateTime{}, false
	}
	time := object.ISOTime{}
	if !mergeTimeInto(&time, o) {
		return object.ISODateTime{}, false
	}
	return object.ISODateTime{Date: date, Time: time}, true
}

// mergeDateTimeFields 把属性包中的字段合并到已有 ISODateTime 上。
// partial 为真时允许只提供部分字段 (用于 withPlainTime 等场景)。
func mergeDateTimeFields(dt object.ISODateTime, src object.Value, partial bool) (object.ISODateTime, bool) {
	o, ok := src.(*object.Object)
	if !ok {
		return dt, false
	}
	if v, found := o.GetProperty("year"); found && v != object.UndefinedSingleton {
		dt.Date.Year = int32(toFloat(v))
	}
	if v, found := o.GetProperty("month"); found && v != object.UndefinedSingleton {
		dt.Date.Month = int32(toFloat(v))
	}
	if v, found := o.GetProperty("day"); found && v != object.UndefinedSingleton {
		dt.Date.Day = int32(toFloat(v))
	}
	if !mergeTimeInto(&dt.Time, o) {
		return dt, false
	}
	if !object.IsValidISODate(dt.Date) || !object.IsValidISOTime(dt.Time) {
		return dt, false
	}
	_ = partial
	return dt, true
}

// mergeTimeInto 把时区字段合并到 ISOTime。缺失字段保持原值。
func mergeTimeInto(t *object.ISOTime, o *object.Object) bool {
	keys := []string{"hour", "minute", "second", "millisecond", "microsecond", "nanosecond"}
	targets := []*int32{&t.Hour, &t.Minute, &t.Second, &t.Millisecond, &t.Microsecond, &t.Nanosecond}
	for i, k := range keys {
		v, found := o.GetProperty(k)
		if !found || v == object.UndefinedSingleton {
			continue
		}
		f := toFloat(v)
		if f != math.Trunc(f) {
			return false
		}
		*targets[i] = int32(f)
	}
	return true
}

// mergeTimeFields 从属性包构造新的 ISOTime (其余字段归零)。
func mergeTimeFields(base object.ISOTime, src object.Value) (object.ISOTime, bool) {
	o, ok := src.(*object.Object)
	if !ok {
		return base, false
	}
	out := object.ISOTime{}
	if !mergeTimeInto(&out, o) {
		return base, false
	}
	if !object.IsValidISOTime(out) {
		return base, false
	}
	return out, true
}

// objectPadMonth 生成 monthCode ("M01" ... "M12")。
func objectPadMonth(m int32) string { return fmt.Sprintf("M%02d", m) }

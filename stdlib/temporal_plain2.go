package stdlib

import (
	"math"
	"math/big"
	"strings"

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
		overflow := "constrain"
		if len(args) > 1 {
			if e := optionsTypeErr(args[1]); e != nil {
				return e
			}
			ov, oerr := getOverflow(args[1])
			if oerr != nil {
				return oerr
			}
			overflow = ov
		}
		bag, isBag := args[0].(propBag)
		if !isBag {
			return tgType("PlainDate.prototype.with requires an object")
		}
		d, merr := mergeDateBag(t.ISODate(), bag, overflow)
		if merr != nil {
			return merr
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

	tgMethod(proto, "withCalendar", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDate(this)
		if !ok {
			return tgTypeBadThis("PlainDate")
		}
		if len(args) == 0 {
			return tgType("withCalendar requires an argument")
		}
		cal, cerr := toCalendarIDValue(args[0])
		if cerr != nil {
			return cerr
		}
		return object.NewTemporalPlainDate(t.ISODate(), cal)
	})

	tgEraGetters(proto, "PlainDate", func(v object.Value) bool { _, ok := asPlainDate(v); return ok })

	tgMethod(proto, "equals", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDate(this)
		if !ok {
			return tgTypeBadThis("PlainDate")
		}
		if len(args) == 0 {
			return tgType("equals requires an argument")
		}
		o, cerr := toPlainDateValue(args[0])
		if cerr != nil {
			return cerr
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
		if len(args) > 0 && args[0] != object.UndefinedSingleton && args[0] != nil {
			pt, terr := toPlainTimeValue(args[0])
			if terr != nil {
				return terr
			}
			time = pt.ISOTime()
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
		tzArg := args[0]
		tm := object.ISOTime{}
		switch tzArg.(type) {
		case *object.Object, *object.Proxy:
			bag := tzArg.(propBag)
			// 属性包形式: { timeZone, plainTime?, hour?, minute?, ... }
			tzv, found := bag.GetProperty("timeZone")
			if !found || tzv == object.UndefinedSingleton {
				return tgType("toZonedDateTime: missing timeZone property")
			}
			tzArg = tzv
			if pt, found := bag.GetProperty("plainTime"); found && pt != object.UndefinedSingleton {
				ptv, perr := toPlainTimeValue(pt)
				if perr != nil {
					return perr
				}
				tm = ptv.ISOTime()
			} else {
				if merr := mergeTimeInto(&tm, bag); merr != nil {
					return merr
				}
			}
		}
		tz, tzTzErr := toTimeZoneIDE(tzArg)
		if tzTzErr != nil {
			return tzTzErr
		}
		dt := object.ISODateTime{Date: t.ISODate(), Time: tm}
		instants := object.GetPossibleInstantsFor(tz, dt)
		ns, ok := object.DisambiguatePossibleInstants(instants, tz, dt, "compatible")
		if !ok {
			return tgRange("Ambiguous or non-existent wall-clock time")
		}
		return object.NewTemporalZonedDateTime(ns, tz, t.CalendarID())
	})

	tgToStringMethod(proto, "PlainDate", func(this object.Value, args ...object.Value) (string, object.Value) {
		t, ok := asPlainDate(this)
		if !ok {
			return "", nil
		}
		var opts object.Value
		if len(args) > 0 {
			opts = args[0]
		}
		st, perr := parseToStringSettings(opts, false)
		if perr != nil {
			return "", perr
		}
		return object.FormatISODate(t.ISODate()) + calendarAnnotation(st), nil
	})
	tgValueOf(proto)

	ctor := object.NewBuiltin("PlainDate", func(args ...object.Value) object.Value {
		if len(args) < 3 || args[0] == object.UndefinedSingleton || args[1] == object.UndefinedSingleton || args[2] == object.UndefinedSingleton {
			return tgRange("PlainDate requires year, month, day")
		}
		var convErr object.Value
		f := func(i int) int32 {
			n, cerr := toIntTruncArg(args[i])
			if cerr != nil {
				convErr = cerr
				return 0
			}
			return n
		}
		d := object.ISODate{Year: f(0), Month: f(1), Day: f(2)}
		if convErr != nil {
			return convErr
		}
		if !object.IsValidISODate(d) {
			return tgRange("Invalid ISO date")
		}
		cal := ""
		if len(args) > 3 {
			cid, cerr := ctorCalendarID(args[3])
			if cerr != nil {
				return cerr
			}
			cal = cid
		}
		return object.NewTemporalPlainDate(d, cal)
	})
	ctor.SetProperty("name", object.NewString("PlainDate"))
	ctor.SetProperty("prototype", proto)
	proto.SetBuiltinProperty("constructor", ctor)

	tgStatic(ctor, "from", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return tgType("PlainDate.from requires an argument")
		}
		// spec: 选项包在任何分支之前做 GetOptionsObject + overflow 校验。
		if e := optionsTypeErr(argAt(args, 1)); e != nil {
			return e
		}
		fromOverflow, oe := getOverflow(argAt(args, 1))
		if oe != nil {
			return oe
		}
		if len(args) > 1 {
			if e := optionsTypeErr(args[1]); e != nil {
				return e
			}
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
		case propBag:
			d, ferr := readDateFields(v, fromOverflow)
			if ferr != nil {
				return ferr
			}
			return object.NewTemporalPlainDate(d, object.DefaultCalendarID)
		}
		return tgType("PlainDate.from: unsupported argument")
	})

	tgStatic(ctor, "compare", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return tgType("compare requires two PlainDates")
		}
		a, ea := toPlainDateValue(args[0])
		if ea != nil {
			return ea
		}
		b, eb := toPlainDateValue(args[1])
		if eb != nil {
			return eb
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
	temporal.SetBuiltinProperty("PlainDate", ctor)
}

// plainDateShift 实现 PlainDate.add/subtract。
// Duration 的时间分量在日期运算中被忽略——加到没有时间概念的日期上没有意义。
func plainDateShift(t *object.TemporalPlainDate, args []object.Value, negate bool) object.Value {
	if len(args) == 0 {
		return tgType("missing duration")
	}
	d, derr := toDurationE(args[0])
	if derr != nil {
		return derr
	}
	if serr := checkDurationUniformSign(d); serr != nil {
		return serr
	}
	overflow := "constrain"
	if len(args) > 1 {
		if e := optionsTypeErr(args[1]); e != nil {
			return e
		}
		overflowTmp, overflowErr := getOverflow(args[1])
		if overflowErr != nil {
			return overflowErr
		}
		overflow = overflowTmp
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
	other, cerr := toPlainDateValue(args[0])
	if cerr != nil {
		return cerr
	}
	var opts object.Value
	if len(args) > 1 {
		opts = args[1]
	}
	if e := optionsTypeErr(opts); e != nil {
		return e
	}
	largest, uerr := getLargestUnit(opts, dateOnlyUnits)
	if uerr != nil {
		return uerr
	}
	smallest, uerr2 := getSmallestUnit(opts, dateOnlyUnits)
	if uerr2 != nil {
		return uerr2
	}
	mode, modeErr := getRoundingModeTrunc(opts)
	if modeErr != nil {
		return modeErr
	}
	inc, incErr := getRoundingIncrement(opts)
	if incErr != nil {
		return incErr
	}
	to := other.ISODate()
	su := smallest
	if su == "auto" {
		su = "day"
	}
	if largest == "auto" {
		largest = resolveAutoLargest(su, "day")
	}
	if unitRank(su) < unitRank(largest) {
		return tgRange("smallestUnit is larger than largestUnit")
	}
	if !validRoundingIncrement(su, inc) {
		return tgRange("Invalid roundingIncrement for " + su)
	}
	if since {
		from, to = to, from
		mode = negateRoundingMode(mode)
	}
	return calendarRoundDiff(
		object.ISODateTime{Date: from}, object.ISODateTime{Date: to},
		largest, su, inc, mode)
}

// dateFromFieldsObject 从属性包构造 ISODate (constrain 语义)。
func dateFromFieldsObject(o *object.Object) (object.ISODate, bool) {
	d, e := readDateFields(o, "constrain")
	return d, e == nil
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
		if len(args) > 1 {
			if e := optionsTypeErr(args[1]); e != nil {
				return e
			}
			if _, oerr := getOverflow(args[1]); oerr != nil {
				return oerr
			}
		}
		o, ok := args[0].(*object.Object)
		if !ok {
			return tgType("PlainTime.prototype.with: invalid fields")
		}
		time := t.ISOTime()
		if merr := mergeTimeInto(&time, o); merr != nil {
			return merr
		}
		if !object.IsValidISOTime(time) {
			return tgRange("PlainTime.prototype.with: invalid fields")
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
		if len(args) == 0 {
			return tgType("PlainTime.prototype.round requires an argument")
		}
		var opts object.Value
		if len(args) > 0 {
			opts = args[0]
		}
		opts, roe := roundOptionsOf(opts)
		if roe != nil {
			return roe
		}
		smallest, uerr := getSmallestUnit(opts, timeOnlyUnits)
		if uerr != nil {
			return uerr
		}
		if smallest == "auto" {
			return tgRange("Invalid smallestUnit for PlainTime.round")
		}
		inc, incErr := getRoundingIncrement(opts)
		if incErr != nil {
			return incErr
		}
		mode, modeErr := getRoundingMode(opts)
		if modeErr != nil {
			return modeErr
		}
		if !validRoundingIncrement(smallest, inc) {
			return tgRange("Invalid roundingIncrement for " + smallest)
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
			return tgType("equals requires an argument")
		}
		o, cerr := toPlainTimeValue(args[0])
		if cerr != nil {
			return cerr
		}
		return object.NewBoolean(t.ISOTime() == o.ISOTime())
	})

	tgMethod(proto, "toZonedDateTime", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainTime(this)
		if !ok {
			return tgTypeBadThis("PlainTime")
		}
		if len(args) == 0 {
			return tgType("toZonedDateTime requires an argument")
		}
		var date object.ISODate
		var tz string
		if _, ok := asPlainDate(args[0]); ok {
			return tgType("toZonedDateTime requires an object with timeZone")
		} else if o, ok := args[0].(*object.Object); ok {
			tzv, found := o.GetProperty("timeZone")
			if !found {
				return tgType("toZonedDateTime: missing timeZone")
			}
			id, idTzErr := toTimeZoneIDE(tzv)
			if idTzErr != nil {
				return idTzErr
			}
			tz = id
			d, ok3 := dateFromFieldsObject(o)
			if !ok3 {
				return tgType("toZonedDateTime: invalid date fields")
			}
			date = d
		} else {
			return tgType("toZonedDateTime requires an object with timeZone")
		}
		dt := object.ISODateTime{Date: date, Time: t.ISOTime()}
		cands := object.GetPossibleInstantsFor(tz, dt)
		ns, ok4 := object.DisambiguatePossibleInstants(cands, tz, dt, "compatible")
		if !ok4 {
			return tgRange("Invalid wall-clock time")
		}
		return object.NewTemporalZonedDateTime(ns, tz, object.DefaultCalendarID)
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

	tgToStringMethod(proto, "PlainTime", func(this object.Value, args ...object.Value) (string, object.Value) {
		t, ok := asPlainTime(this)
		if !ok {
			return "", nil
		}
		var opts object.Value
		if len(args) > 0 {
			opts = args[0]
		}
		st, perr := parseToStringSettings(opts, true)
		if perr != nil {
			return "", perr
		}
		tm := roundTimeOfDay(t.ISOTime(), st.roundingQuantumNanos(), st.mode)
		prec := st.precision
		if st.smallestUnit != "" {
			prec = precisionForSmallest(st.smallestUnit)
		}
		return object.FormatISOTime(tm, prec) + calendarAnnotation(st), nil
	})
	tgValueOf(proto)

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
	proto.SetBuiltinProperty("constructor", ctor)

	tgStatic(ctor, "from", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return tgType("PlainTime.from requires an argument")
		}
		// spec: 选项包在任何分支之前做 GetOptionsObject + overflow 校验。
		if e := optionsTypeErr(argAt(args, 1)); e != nil {
			return e
		}
		if _, oe := getOverflow(argAt(args, 1)); oe != nil {
			return oe
		}
		if pt, ok := asPlainTime(args[0]); ok {
			return pt
		}
		switch v := args[0].(type) {
		case *object.String:
			// 歧义拒绝: 不含时间分隔符的 YearMonth/MonthDay 形式不是 PlainTime。
			if !strings.ContainsAny(v.Value, "Tt ") {
				if _, amb := object.ParseTemporalYearMonthString(v.Value); amb {
					return tgRange("Invalid PlainTime string: " + v.Value)
				}
				if _, amb := object.ParseTemporalMonthDayString(v.Value); amb {
					return tgRange("Invalid PlainTime string: " + v.Value)
				}
			}
			f, ok := object.ParseTemporalTimeString(v.Value)
			if !ok {
				if df, dok := object.ParseTemporalDateTimeString(v.Value); dok && df.HasTime {
					f, ok = df, true
				}
			}
			if !ok || !f.HasTime {
				return tgRange("Invalid PlainTime string: " + v.Value)
			}
			if f.IsZ {
				return tgRange("Invalid PlainTime string: " + v.Value)
			}
			return object.NewTemporalPlainTime(f.ISODateTime().Time)
		case propBag:
			time := object.ISOTime{}
			if merr := mergeTimeInto(&time, v); merr != nil {
				return merr
			}
			if !object.IsValidISOTime(time) {
				return tgRange("PlainTime.from: invalid fields")
			}
			return object.NewTemporalPlainTime(time)
		}
		return tgType("PlainTime.from: unsupported argument")
	})

	tgStatic(ctor, "compare", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return tgType("compare requires two PlainTimes")
		}
		a, ea := toPlainTimeValue(args[0])
		if ea != nil {
			return ea
		}
		b, eb := toPlainTimeValue(args[1])
		if eb != nil {
			return eb
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
	temporal.SetBuiltinProperty("PlainTime", ctor)
}

// plainTimeShift 实现 PlainTime.add/subtract，结果环绕一天。
func plainTimeShift(t *object.TemporalPlainTime, args []object.Value, negate bool) object.Value {
	if len(args) == 0 {
		return tgType("missing duration")
	}
	d, derr := toDurationE(args[0])
	if derr != nil {
		return derr
	}
	if serr := checkDurationUniformSign(d); serr != nil {
		return serr
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
	other, cerr := toPlainTimeValue(args[0])
	if cerr != nil {
		return cerr
	}
	var opts object.Value
	if len(args) > 1 {
		opts = args[1]
	}
	if e := optionsTypeErr(opts); e != nil {
		return e
	}
	largest, uerr := getLargestUnit(opts, timeOnlyUnits)
	if uerr != nil {
		return uerr
	}
	smallest, uerr := getSmallestUnit(opts, timeOnlyUnits)
	if uerr != nil {
		return uerr
	}
	inc, incErr := getRoundingIncrement(opts)
	if incErr != nil {
		return incErr
	}
	mode, modeErr := getRoundingMode(opts)
	if modeErr != nil {
		return modeErr
	}
	// PlainTime 的差值始终环绕一天: 23:00 到 01:00 是 +2 小时而非 -22 小时。
	diff := other.ISOTime().NanosOfDay() - from.NanosOfDay()
	if diff < 0 {
		diff += object.NanosPerDay
	}
	su := smallest
	if su == "auto" {
		su = "nanosecond"
	}
	if largest == "auto" {
		largest = resolveAutoLargest(su, "hour")
	}
	if !validRoundingIncrement(su, inc) {
		return tgRange("Invalid roundingIncrement for " + su)
	}
	if unitRank(su) < unitRank(largest) {
		return tgRange("smallestUnit is larger than largestUnit")
	}
	rounded := roundNanosToUnit(big.NewInt(diff), su, inc, mode)
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

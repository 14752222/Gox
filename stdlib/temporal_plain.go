package stdlib

import (
	"fmt"
	"math"
	"math/big"

	"github.com/14752222/Gox/object"
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
		if len(args) > 1 {
			if e := optionsTypeErr(args[1]); e != nil {
				return e
			}
			if _, oerr := getOverflow(args[1]); oerr != nil {
				return oerr
			}
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
		if len(args) > 0 && args[0] != object.UndefinedSingleton && args[0] != nil {
			pt, terr := toPlainTimeValue(args[0])
			if terr != nil {
				return terr
			}
			dt.Time = pt.ISOTime()
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
		if len(args) == 0 {
			return tgType("PlainDateTime.prototype.round requires an argument")
		}
		var opts object.Value
		if len(args) > 0 {
			opts = args[0]
		}
		opts, roe := roundOptionsOf(opts)
		if roe != nil {
			return roe
		}
		ro, rerr := readRoundOptions(opts, datetimeUnits)
		if rerr != nil {
			return rerr
		}
		smallest, inc, mode := ro.smallest, ro.inc, ro.mode
		if smallest == "" || smallest == "auto" {
			return tgRange("Invalid smallestUnit for PlainDateTime.round")
		}
		if !validRoundingIncrement(smallest, inc) {
			return tgRange("Invalid roundingIncrement for " + smallest)
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
			return tgType("equals requires an argument")
		}
		o, cerr := toPlainDateTimeValue(args[0])
		if cerr != nil {
			return cerr
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
		tz, tzTzErr := toTimeZoneIDE(args[0])
		if tzTzErr != nil {
			return tzTzErr
		}
		disambiguation := "compatible"
		if len(args) > 1 {
			if e := optionsTypeErr(args[1]); e != nil {
				return e
			}
			disambiguationTmp, disambiguationErr := getDisambiguation(args[1])
			if disambiguationErr != nil {
				return disambiguationErr
			}
			disambiguation = disambiguationTmp
		}
		instants := object.GetPossibleInstantsFor(tz, t.ISODateTime())
		ns, ok := object.DisambiguatePossibleInstants(instants, tz, t.ISODateTime(), disambiguation)
		if !ok {
			return tgRange("Ambiguous or non-existent wall-clock time (use disambiguation option)")
		}
		return object.NewTemporalZonedDateTime(ns, tz, t.CalendarID())
	})

	tgMethod(proto, "withCalendar", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDateTime(this)
		if !ok {
			return tgTypeBadThis("PlainDateTime")
		}
		if len(args) == 0 {
			return tgType("withCalendar requires an argument")
		}
		cal, cerr := toCalendarIDValue(args[0])
		if cerr != nil {
			return cerr
		}
		return object.NewTemporalPlainDateTime(t.ISODateTime(), cal)
	})

	tgMethod(proto, "toPlainYearMonth", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDateTime(this)
		if !ok {
			return tgTypeBadThis("PlainDateTime")
		}
		d := t.ISODateTime().Date
		return object.NewTemporalPlainYearMonth(d.Year, d.Month, d.Day, t.CalendarID())
	})

	tgMethod(proto, "toPlainMonthDay", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainDateTime(this)
		if !ok {
			return tgTypeBadThis("PlainDateTime")
		}
		d := t.ISODateTime().Date
		return object.NewTemporalPlainMonthDay(d.Month, d.Day, d.Year, t.CalendarID())
	})

	tgEraGetters(proto, "PlainDateTime", func(v object.Value) bool { _, ok := asPlainDateTime(v); return ok })

	tgToStringMethod(proto, "PlainDateTime", func(this object.Value, args ...object.Value) (string, object.Value) {
		t, ok := asPlainDateTime(this)
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
		dt := roundDateTimeForDisplay(t.ISODateTime(), st.roundingQuantumNanos(), st.mode)
		prec := st.precision
		if st.smallestUnit != "" {
			prec = precisionForSmallest(st.smallestUnit)
		}
		return object.FormatISODate(dt.Date) + "T" + object.FormatISOTime(dt.Time, prec) + calendarAnnotation(st), nil
	})
	tgValueOf(proto)

	// ===== 构造器 =====
	ctor := object.NewBuiltin("PlainDateTime", func(args ...object.Value) object.Value {
		var convErr object.Value
		f := func(i int) int32 {
			if i >= len(args) || args[i] == object.UndefinedSingleton {
				return 0
			}
			n, cerr := toIntTruncArg(args[i])
			if cerr != nil {
				convErr = cerr
				return 0
			}
			return n
		}
		if len(args) < 3 || args[0] == object.UndefinedSingleton || args[1] == object.UndefinedSingleton || args[2] == object.UndefinedSingleton {
			return tgRange("PlainDateTime requires at least year, month, day")
		}
		date := object.ISODate{Year: f(0), Month: f(1), Day: f(2)}
		if convErr != nil {
			return convErr
		}
		if !object.IsValidISODate(date) {
			return tgRange("Invalid ISO date")
		}
		time := object.ISOTime{Hour: f(3), Minute: f(4), Second: f(5),
			Millisecond: f(6), Microsecond: f(7), Nanosecond: f(8)}
		if !object.IsValidISOTime(time) {
			return tgRange("Invalid ISO time")
		}
		cal := ""
		if len(args) > 9 {
			cid, cerr := ctorCalendarID(args[9])
			if cerr != nil {
				return cerr
			}
			cal = cid
		}
		return object.NewTemporalPlainDateTime(object.ISODateTime{Date: date, Time: time}, cal)
	})
	ctor.SetProperty("name", object.NewString("PlainDateTime"))
	ctor.SetProperty("prototype", proto)
	proto.SetBuiltinProperty("constructor", ctor)

	tgStatic(ctor, "from", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return tgType("PlainDateTime.from requires an argument")
		}
		// spec: GetOptionsObject 只验类型；overflow 读取推迟到字段之后。
		if e := optionsTypeErr(argAt(args, 1)); e != nil {
			return e
		}
		if pdt, ok := asPlainDateTime(args[0]); ok {
			if _, oe := getOverflow(argAt(args, 1)); oe != nil {
				return oe
			}
			return pdt
		}
		switch v := args[0].(type) {
		case *object.TemporalPlainDate:
			if _, oe := getOverflow(argAt(args, 1)); oe != nil {
				return oe
			}
			return object.NewTemporalPlainDateTime(object.ISODateTime{Date: v.ISODate()}, object.DefaultCalendarID)
		case *object.TemporalZonedDateTime:
			if _, oe := getOverflow(argAt(args, 1)); oe != nil {
				return oe
			}
			dt := object.GetISOPartsFromEpoch(v.TimeZoneID(), v.Nanos())
			return object.NewTemporalPlainDateTime(dt, object.DefaultCalendarID)
		case *object.String:
			f, ok := object.ParseTemporalDateTimeString(v.Value)
			if !ok {
				return tgRange("Invalid PlainDateTime string: " + v.Value)
			}
			if _, oe := getOverflow(argAt(args, 1)); oe != nil {
				return oe
			}
			if f.Calendar != "" && f.Calendar != object.DefaultCalendarID {
				return tgRange("Unsupported calendar: " + f.Calendar)
			}
			return object.NewTemporalPlainDateTime(f.ISODateTime(), object.DefaultCalendarID)
		case propBag:
			raw, rerr := readDateTimeFieldsRaw(v)
			if rerr != nil {
				return rerr
			}
			fromOverflow, oe := getOverflow(argAt(args, 1))
			if oe != nil {
				return oe
			}
			dt, ferr := raw.regulate(fromOverflow)
			if ferr != nil {
				return ferr
			}
			return object.NewTemporalPlainDateTime(dt, object.DefaultCalendarID)
		}
		return tgType("PlainDateTime.from: unsupported argument")
	})

	tgStatic(ctor, "compare", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return tgType("compare requires two PlainDateTimes")
		}
		a, ea := toPlainDateTimeValue(args[0])
		if ea != nil {
			return ea
		}
		b, eb := toPlainDateTimeValue(args[1])
		if eb != nil {
			return eb
		}
		return object.NewNumber(float64(compareISODateTime(a.ISODateTime(), b.ISODateTime())))
	})

	setToStringTag(proto, "Temporal.PlainDateTime")
	temporal.SetBuiltinProperty("PlainDateTime", ctor)
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
	toPtr, cerr := toPlainDateTimeValue(args[0])
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
	do, derr := readDiffOptions(opts, datetimeUnits)
	if derr != nil {
		return derr
	}
	largest, su, inc, mode := do.largest, do.smallest, do.inc, do.mode

	to := toPtr.ISODateTime()
	if su == "auto" {
		su = "nanosecond"
	}
	if largest == "auto" {
		largest = resolveAutoLargest(su, "day")
	}
	if !validRoundingIncrement(su, inc) {
		return tgRange("Invalid roundingIncrement for " + su)
	}
	if unitRank(su) < unitRank(largest) {
		return tgRange("smallestUnit is larger than largestUnit")
	}
	// since 反转方向并对舍入模式取反
	if since {
		from, to = to, from
		mode = negateRoundingMode(mode)
	}
	return calendarRoundDiff(from, to, largest, su, inc, mode)
}

// dateTimeFromFieldsObject 从属性包构造 ISODateTime (constrain 语义)。
func dateTimeFromFieldsObject(o *object.Object) (object.ISODateTime, bool) {
	dt, e := readDateTimeFields(o, "constrain")
	return dt, e == nil
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
	if merr := mergeTimeInto(&dt.Time, o); merr != nil {
		return dt, false
	}
	if !object.IsValidISODate(dt.Date) || !object.IsValidISOTime(dt.Time) {
		return dt, false
	}
	_ = partial
	return dt, true
}

// mergeTimeInto 把时间字段合并到 ISOTime。缺失字段保持原值。
// 返回 nil 表示成功；getter 抛错或字段非法时返回错误值。
func mergeTimeInto(t *object.ISOTime, o propBag) object.Value {
	// 规范按字母序读取字段 (hour, microsecond, millisecond, minute, nanosecond, second)。
	keys := []string{"hour", "microsecond", "millisecond", "minute", "nanosecond", "second"}
	targets := []*int32{&t.Hour, &t.Microsecond, &t.Millisecond, &t.Minute, &t.Nanosecond, &t.Second}
	for i, k := range keys {
		v, found := o.GetProperty(k)
		if !found || v == object.UndefinedSingleton {
			continue
		}
		f, cerr := tgToNumber(v)
		if cerr != nil {
			return cerr
		}
		if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
			return tgRange("Invalid time fields")
		}
		*targets[i] = int32(f)
	}
	return nil
}

// mergeTimeFields 从属性包构造新的 ISOTime (其余字段归零)。
func mergeTimeFields(base object.ISOTime, src object.Value) (object.ISOTime, bool) {
	o, ok := src.(propBag)
	if !ok {
		return base, false
	}
	out := object.ISOTime{}
	if merr := mergeTimeInto(&out, o); merr != nil {
		return base, false
	}
	if !object.IsValidISOTime(out) {
		return base, false
	}
	return out, true
}

// objectPadMonth 生成 monthCode ("M01" ... "M12")。
func objectPadMonth(m int32) string { return fmt.Sprintf("M%02d", m) }

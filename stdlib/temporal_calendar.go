package stdlib

import (
	"math"

	"github.com/14752222/Gox/object"
)

// 本文件实现 Temporal.TimeZone、Temporal.Calendar、
// Temporal.PlainYearMonth 与 Temporal.PlainMonthDay。

// ==================== TimeZone ====================

func setupTemporalTimeZone(temporal *object.Object) {
	proto := object.NewObject()
	object.SetTemporalProto(object.TEMPORAL_TIMEZONE_OBJ, proto)

	tgStrGetter(proto, "id", func(this object.Value) string {
		t, ok := asTimeZone(this)
		if !ok {
			return ""
		}
		return t.ID()
	})

	// instantArg 从参数中提取 Instant (支持 Instant 实例与 ISO 串)。
	instantArg := func(v object.Value) (*object.BigInt, bool) {
		if inst, ok := asInstant(v); ok {
			return object.NewBigInt(inst.Nanos()), true
		}
		if s, ok := v.(*object.String); ok {
			f, ok := object.ParseTemporalInstantString(s.Value)
			if !ok {
				return nil, false
			}
			ns := f.ISODateTime().EpochNanos()
			if f.OffsetSeconds != nil {
				// 带偏移的串: 当地墙钟时间减去偏移才是精确时刻
				ns.Sub(ns, object.NewBigIntInt64(int64(*f.OffsetSeconds)*object.NanosPerSecond).Value)
			}
			return object.NewBigInt(ns), true
		}
		return nil, false
	}

	tgMethod(proto, "getOffsetNanosecondsFor", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asTimeZone(this)
		if !ok {
			return tgTypeBadThis("TimeZone")
		}
		if len(args) == 0 {
			return tgType("getOffsetNanosecondsFor requires an instant")
		}
		bn, ok := instantArg(args[0])
		if !ok {
			return tgType("argument is not an Instant")
		}
		secs := object.TimeZoneOffsetSecondsAt(t.ID(), bn.Value)
		return object.NewNumber(float64(int64(secs) * object.NanosPerSecond))
	})

	tgMethod(proto, "getOffsetStringFor", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asTimeZone(this)
		if !ok {
			return tgTypeBadThis("TimeZone")
		}
		if len(args) == 0 {
			return tgType("getOffsetStringFor requires an instant")
		}
		bn, ok := instantArg(args[0])
		if !ok {
			return tgType("argument is not an Instant")
		}
		return object.NewString(object.GetOffsetStringFor(t.ID(), bn.Value))
	})

	tgMethod(proto, "getPlainDateTimeFor", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asTimeZone(this)
		if !ok {
			return tgTypeBadThis("TimeZone")
		}
		if len(args) == 0 {
			return tgType("getPlainDateTimeFor requires an instant")
		}
		bn, ok := instantArg(args[0])
		if !ok {
			return tgType("argument is not an Instant")
		}
		dt := object.GetISOPartsFromEpoch(t.ID(), bn.Value)
		return object.NewTemporalPlainDateTime(dt, object.DefaultCalendarID)
	})

	tgMethod(proto, "getInstantFor", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asTimeZone(this)
		if !ok {
			return tgTypeBadThis("TimeZone")
		}
		if len(args) == 0 {
			return tgType("getInstantFor requires a PlainDateTime")
		}
		pdt, ok := asPlainDateTime(args[0])
		if !ok {
			return tgType("argument is not a PlainDateTime")
		}
		var opts object.Value
		if len(args) > 1 {
			opts = args[1]
		}
		disambiguation := getDisambiguation(opts)
		if disambiguation == "invalid" {
			return tgRange("Invalid disambiguation")
		}
		dt := pdt.ISODateTime()
		candidates := object.GetPossibleInstantsFor(t.ID(), dt)
		ns, ok := object.DisambiguatePossibleInstants(candidates, t.ID(), dt, disambiguation)
		if !ok {
			return tgRange("Ambiguous or non-existent wall-clock time")
		}
		return object.NewTemporalInstant(ns)
	})

	// getPossibleInstantsFor 是 DST 空洞/重复的暴露点: 返回 0 或 2 个候选
	// 时调用方 (ZonedDateTime.from) 必须显式选择消歧策略。
	tgMethod(proto, "getPossibleInstantsFor", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asTimeZone(this)
		if !ok {
			return tgTypeBadThis("TimeZone")
		}
		if len(args) == 0 {
			return tgType("getPossibleInstantsFor requires a PlainDateTime")
		}
		pdt, ok := asPlainDateTime(args[0])
		if !ok {
			return tgType("argument is not a PlainDateTime")
		}
		candidates := object.GetPossibleInstantsFor(t.ID(), pdt.ISODateTime())
		els := make([]object.Value, 0, len(candidates))
		for _, c := range candidates {
			els = append(els, object.NewTemporalInstant(c))
		}
		return object.NewArray(els)
	})

	tgMethod(proto, "equals", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asTimeZone(this)
		if !ok {
			return tgTypeBadThis("TimeZone")
		}
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		other, ok := toTimeZoneID(args[0])
		if !ok {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(t.ID() == other)
	})

	tgToStringMethod(proto, func(this object.Value) string {
		t, ok := asTimeZone(this)
		if !ok {
			return ""
		}
		return t.ToString()
	})

	ctor := object.NewBuiltin("TimeZone", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return tgType("TimeZone requires an identifier")
		}
		id, ok := toTimeZoneID(args[0])
		if !ok {
			return tgRange("Invalid time zone identifier")
		}
		return object.NewTemporalTimeZone(id)
	})
	ctor.SetProperty("name", object.NewString("TimeZone"))
	ctor.SetProperty("prototype", proto)
	proto.SetProperty("constructor", ctor)

	tgStatic(ctor, "from", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return tgType("TimeZone.from requires an identifier")
		}
		if tz, ok := asTimeZone(args[0]); ok {
			return tz
		}
		id, ok := toTimeZoneID(args[0])
		if !ok {
			return tgRange("Invalid time zone identifier")
		}
		return object.NewTemporalTimeZone(id)
	})

	setToStringTag(proto, "Temporal.TimeZone")
	temporal.SetProperty("TimeZone", ctor)
}

// ==================== Calendar ====================

func setupTemporalCalendar(temporal *object.Object) {
	proto := object.NewObject()
	object.SetTemporalProto(object.TEMPORAL_CALENDAR_OBJ, proto)

	tgStrGetter(proto, "id", func(this object.Value) string {
		c, ok := asCalendar(this)
		if !ok {
			return ""
		}
		return c.ID()
	})

	// ISO 日历字段方法: 参数可以是 PlainDate/PlainDateTime/属性包。
	calField := func(name, field string) {
		tgMethod(proto, name, func(this object.Value, args ...object.Value) object.Value {
			if _, ok := asCalendar(this); !ok {
				return tgTypeBadThis("Calendar")
			}
			if len(args) == 0 {
				return tgType(name + " requires a date")
			}
			dt, ok := calendarArgToISODateTime(args[0])
			if !ok {
				return tgType(name + ": invalid date argument")
			}
			v, ok := object.CalendarFieldISO(field, dt)
			if !ok {
				return tgRange("Unsupported calendar field: " + field)
			}
			if field == "inLeapYear" {
				return object.NewBoolean(v == 1)
			}
			if field == "monthCode" {
				return object.NewString(objectPadMonth(v))
			}
			return object.NewNumber(float64(v))
		})
	}
	calField("year", "year")
	calField("month", "month")
	calField("monthCode", "monthCode")
	calField("day", "day")
	calField("dayOfWeek", "dayOfWeek")
	calField("dayOfYear", "dayOfYear")
	calField("weekOfYear", "weekOfYear")
	calField("daysInWeek", "daysInWeek")
	calField("daysInMonth", "daysInMonth")
	calField("daysInYear", "daysInYear")
	calField("monthsInYear", "monthsInYear")
	calField("inLeapYear", "inLeapYear")

	// dateAdd: 在日期上加上 Duration 的日期分量。
	tgMethod(proto, "dateAdd", func(this object.Value, args ...object.Value) object.Value {
		if _, ok := asCalendar(this); !ok {
			return tgTypeBadThis("Calendar")
		}
		if len(args) < 2 {
			return tgType("dateAdd requires a date and a duration")
		}
		date, ok := calendarArgToISODate(args[0])
		if !ok {
			return tgType("dateAdd: invalid date")
		}
		d, ok := toDurationValue(args[1])
		if !ok {
			return tgType("dateAdd: invalid duration")
		}
		overflow := "constrain"
		if len(args) > 2 {
			overflow = getOverflow(args[2])
			if overflow == "invalid" {
				return tgRange("Invalid overflow option")
			}
		}
		res, ok := object.AddDateDuration(date,
			int32(d.Years()), int32(d.Months()), int32(d.Weeks()), int32(d.Days()), overflow)
		if !ok {
			return tgRange("Result is outside of supported range")
		}
		return object.NewTemporalPlainDate(res, object.DefaultCalendarID)
	})

	// dateUntil: 两个日期之间的差值，最大单位由 options.largestUnit 决定。
	tgMethod(proto, "dateUntil", func(this object.Value, args ...object.Value) object.Value {
		if _, ok := asCalendar(this); !ok {
			return tgTypeBadThis("Calendar")
		}
		if len(args) < 2 {
			return tgType("dateUntil requires two dates")
		}
		one, ok := calendarArgToISODate(args[0])
		if !ok {
			return tgType("dateUntil: invalid first date")
		}
		two, ok := calendarArgToISODate(args[1])
		if !ok {
			return tgType("dateUntil: invalid second date")
		}
		var opts object.Value
		if len(args) > 2 {
			opts = args[2]
		}
		largest, ok := getLargestUnit(opts, dateOnlyUnits)
		if !ok {
			return tgRange("Invalid largestUnit")
		}
		if largest == "auto" {
			largest = "day"
		}
		y, m, w, d := object.DiffISODate(one, two, largest)
		return object.NewTemporalDuration(int64(y), int64(m), int64(w), int64(d), 0, 0, 0, 0, 0, 0)
	})

	tgMethod(proto, "dateFromFields", func(this object.Value, args ...object.Value) object.Value {
		if _, ok := asCalendar(this); !ok {
			return tgTypeBadThis("Calendar")
		}
		if len(args) == 0 {
			return tgType("dateFromFields requires fields")
		}
		o, ok := args[0].(*object.Object)
		if !ok {
			return tgType("dateFromFields: invalid fields")
		}
		d, ok := dateFromFieldsObject(o)
		if !ok {
			return tgType("dateFromFields: invalid fields")
		}
		return object.NewTemporalPlainDate(d, object.DefaultCalendarID)
	})

	tgMethod(proto, "yearMonthFromFields", func(this object.Value, args ...object.Value) object.Value {
		if _, ok := asCalendar(this); !ok {
			return tgTypeBadThis("Calendar")
		}
		if len(args) == 0 {
			return tgType("yearMonthFromFields requires fields")
		}
		o, ok := args[0].(*object.Object)
		if !ok {
			return tgType("yearMonthFromFields: invalid fields")
		}
		y, m, err := yearMonthFields(o)
		if err != nil {
			return err
		}
		return object.NewTemporalPlainYearMonth(y, m, 1, object.DefaultCalendarID)
	})

	tgMethod(proto, "monthDayFromFields", func(this object.Value, args ...object.Value) object.Value {
		if _, ok := asCalendar(this); !ok {
			return tgTypeBadThis("Calendar")
		}
		if len(args) == 0 {
			return tgType("monthDayFromFields requires fields")
		}
		o, ok := args[0].(*object.Object)
		if !ok {
			return tgType("monthDayFromFields: invalid fields")
		}
		mc, hasCode := o.GetProperty("monthCode")
		if hasCode && mc != object.UndefinedSingleton {
			s, ok := mc.(*object.String)
			if !ok || len(s.Value) != 3 || s.Value[0] != 'M' {
				return tgRange("Invalid monthCode")
			}
			m := int32(s.Value[1]-'0')*10 + int32(s.Value[2]-'0')
			dv, found := o.GetProperty("day")
			if !found {
				return tgType("monthDayFromFields requires day")
			}
			return object.NewTemporalPlainMonthDay(m, int32(toFloat(dv)), 1972, object.DefaultCalendarID)
		}
		mv, okM := o.GetProperty("month")
		dv, okD := o.GetProperty("day")
		if !okM || !okD {
			return tgType("monthDayFromFields requires month and day")
		}
		return object.NewTemporalPlainMonthDay(int32(toFloat(mv)), int32(toFloat(dv)), 1972,
			object.DefaultCalendarID)
	})

	tgMethod(proto, "equals", func(this object.Value, args ...object.Value) object.Value {
		c, ok := asCalendar(this)
		if !ok {
			return tgTypeBadThis("Calendar")
		}
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		if other, ok := asCalendar(args[0]); ok {
			return object.NewBoolean(c.ID() == other.ID())
		}
		if s, ok := args[0].(*object.String); ok {
			return object.NewBoolean(c.ID() == s.Value)
		}
		return object.NewBoolean(false)
	})

	tgToStringMethod(proto, func(this object.Value) string {
		c, ok := asCalendar(this)
		if !ok {
			return ""
		}
		return c.ToString()
	})

	ctor := object.NewBuiltin("Calendar", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return tgType("Calendar requires an identifier")
		}
		s, ok := args[0].(*object.String)
		if !ok {
			return tgType("Calendar identifier must be a string")
		}
		if s.Value != object.DefaultCalendarID {
			return tgRange("Unsupported calendar: " + s.Value)
		}
		return object.NewTemporalCalendar(s.Value)
	})
	ctor.SetProperty("name", object.NewString("Calendar"))
	ctor.SetProperty("prototype", proto)
	proto.SetProperty("constructor", ctor)

	tgStatic(ctor, "from", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return tgType("Calendar.from requires an identifier")
		}
		if c, ok := asCalendar(args[0]); ok {
			return c
		}
		s, ok := args[0].(*object.String)
		if !ok {
			return tgType("Calendar identifier must be a string")
		}
		if s.Value != object.DefaultCalendarID {
			return tgRange("Unsupported calendar: " + s.Value)
		}
		return object.NewTemporalCalendar(s.Value)
	})

	setToStringTag(proto, "Temporal.Calendar")
	temporal.SetProperty("Calendar", ctor)
}

// calendarArgToISODateTime 把 Calendar 方法的日期参数转成 ISO 字段。
// 支持 PlainDate / PlainDateTime / 属性包。
func calendarArgToISODateTime(v object.Value) (object.ISODateTime, bool) {
	if pdt, ok := asPlainDateTime(v); ok {
		return pdt.ISODateTime(), true
	}
	if pd, ok := asPlainDate(v); ok {
		return object.ISODateTime{Date: pd.ISODate()}, true
	}
	if o, ok := v.(*object.Object); ok {
		return dateTimeFromFieldsObject(o)
	}
	return object.ISODateTime{}, false
}

// calendarArgToISODate 同上，但只取日期部分。
func calendarArgToISODate(v object.Value) (object.ISODate, bool) {
	dt, ok := calendarArgToISODateTime(v)
	if !ok {
		return object.ISODate{}, false
	}
	return dt.Date, true
}

// yearMonthFields 从属性包取出 (year, month)。
func yearMonthFields(o *object.Object) (int32, int32, object.Value) {
	mc, hasCode := o.GetProperty("monthCode")
	yv, hasYear := o.GetProperty("year")
	if !hasYear {
		return 0, 0, tgType("yearMonthFromFields requires year")
	}
	y := int32(toFloat(yv))
	if hasCode && mc != object.UndefinedSingleton {
		s, ok := mc.(*object.String)
		if !ok || len(s.Value) != 3 || s.Value[0] != 'M' {
			return 0, 0, tgRange("Invalid monthCode")
		}
		return y, int32(s.Value[1]-'0')*10 + int32(s.Value[2]-'0'), nil
	}
	mv, hasMonth := o.GetProperty("month")
	if !hasMonth {
		return 0, 0, tgType("yearMonthFromFields requires month")
	}
	return y, int32(toFloat(mv)), nil
}

// ==================== PlainYearMonth ====================

func setupTemporalPlainYearMonth(temporal *object.Object) {
	proto := object.NewObject()
	object.SetTemporalProto(object.TEMPORAL_PLAIN_YM_OBJ, proto)

	tgGetter(proto, "year", func(this object.Value) object.Value {
		t, ok := asPlainYearMonth(this)
		if !ok {
			return tgTypeBadThis("PlainYearMonth")
		}
		return object.NewNumber(float64(t.Year()))
	})
	tgGetter(proto, "month", func(this object.Value) object.Value {
		t, ok := asPlainYearMonth(this)
		if !ok {
			return tgTypeBadThis("PlainYearMonth")
		}
		return object.NewNumber(float64(t.Month()))
	})
	tgGetter(proto, "monthCode", func(this object.Value) object.Value {
		t, ok := asPlainYearMonth(this)
		if !ok {
			return tgTypeBadThis("PlainYearMonth")
		}
		return object.NewString(objectPadMonth(t.Month()))
	})
	tgGetter(proto, "daysInMonth", func(this object.Value) object.Value {
		t, ok := asPlainYearMonth(this)
		if !ok {
			return tgTypeBadThis("PlainYearMonth")
		}
		return object.NewNumber(float64(object.DaysInMonth(t.Year(), t.Month())))
	})
	tgGetter(proto, "daysInYear", func(this object.Value) object.Value {
		t, ok := asPlainYearMonth(this)
		if !ok {
			return tgTypeBadThis("PlainYearMonth")
		}
		return object.NewNumber(float64(object.DaysInYear(t.Year())))
	})
	tgGetter(proto, "monthsInYear", func(this object.Value) object.Value {
		return object.NewNumber(12)
	})
	tgGetter(proto, "inLeapYear", func(this object.Value) object.Value {
		t, ok := asPlainYearMonth(this)
		if !ok {
			return tgTypeBadThis("PlainYearMonth")
		}
		return object.NewBoolean(object.IsLeapYear(t.Year()))
	})
	tgStrGetter(proto, "calendarId", func(this object.Value) string {
		t, ok := asPlainYearMonth(this)
		if !ok {
			return ""
		}
		return t.CalendarID()
	})

	tgMethod(proto, "with", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainYearMonth(this)
		if !ok {
			return tgTypeBadThis("PlainYearMonth")
		}
		if len(args) == 0 {
			return tgType("with requires an object")
		}
		y, m, err := yearMonthFieldsFromWith(t, args[0])
		if err != nil {
			return err
		}
		return object.NewTemporalPlainYearMonth(y, m, t.RefDay(), t.CalendarID())
	})

	// add/subtract 只按年月推进: PlainYearMonth 没有"日"的概念，
	// 因此 Duration 的日及以下分量被忽略。
	ymShift := func(t *object.TemporalPlainYearMonth, args []object.Value, negate bool) object.Value {
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
		months := d.Years()*12 + d.Months()
		if negate {
			months = -months
		}
		m := int64(t.Month()) + months
		y := int64(t.Year()) + floorDiv64(m-1, 12)
		mm := ((m-1)%12+12)%12 + 1
		out := object.NewTemporalPlainYearMonth(int32(y), int32(mm), t.RefDay(), t.CalendarID())
		if overflow == "reject" && t.RefDay() > object.DaysInMonth(int32(y), int32(mm)) {
			return tgRange("Result day is out of range for the resulting month")
		}
		return out
	}
	tgMethod(proto, "add", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainYearMonth(this)
		if !ok {
			return tgTypeBadThis("PlainYearMonth")
		}
		return ymShift(t, args, false)
	})
	tgMethod(proto, "subtract", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainYearMonth(this)
		if !ok {
			return tgTypeBadThis("PlainYearMonth")
		}
		return ymShift(t, args, true)
	})

	tgMethod(proto, "until", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainYearMonth(this)
		if !ok {
			return tgTypeBadThis("PlainYearMonth")
		}
		return ymDiff(t, args, false)
	})
	tgMethod(proto, "since", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainYearMonth(this)
		if !ok {
			return tgTypeBadThis("PlainYearMonth")
		}
		return ymDiff(t, args, true)
	})

	tgMethod(proto, "equals", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainYearMonth(this)
		if !ok {
			return tgTypeBadThis("PlainYearMonth")
		}
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		o, ok := asPlainYearMonth(args[0])
		if !ok {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(t.Year() == o.Year() && t.Month() == o.Month())
	})

	// toPlainDate 需要补一个"日"，参考日缺省为 1 号
	tgMethod(proto, "toPlainDate", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainYearMonth(this)
		if !ok {
			return tgTypeBadThis("PlainYearMonth")
		}
		day := t.RefDay()
		if len(args) > 0 {
			if o, ok := args[0].(*object.Object); ok {
				if dv, found := o.GetProperty("day"); found && dv != object.UndefinedSingleton {
					day = int32(toFloat(dv))
				}
			}
		}
		d := object.ISODate{Year: t.Year(), Month: t.Month(), Day: day}
		if !object.IsValidISODate(d) {
			return tgRange("Invalid day for this year-month")
		}
		return object.NewTemporalPlainDate(d, t.CalendarID())
	})

	tgToStringMethod(proto, func(this object.Value) string {
		t, ok := asPlainYearMonth(this)
		if !ok {
			return ""
		}
		return t.ToString()
	})
	tgMethod(proto, "valueOf", func(this object.Value, args ...object.Value) object.Value {
		return this
	})

	ctor := object.NewBuiltin("PlainYearMonth", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return tgType("PlainYearMonth requires year and month")
		}
		y, _ := argFloat(args, 0)
		m, _ := argFloat(args, 1)
		if math.IsNaN(y) || math.IsNaN(m) {
			return tgRange("Invalid year or month")
		}
		yi, mi := int32(y), int32(m)
		if mi < 1 || mi > 12 {
			return tgRange("month must be between 1 and 12")
		}
		return object.NewTemporalPlainYearMonth(yi, mi, 1, object.DefaultCalendarID)
	})
	ctor.SetProperty("name", object.NewString("PlainYearMonth"))
	ctor.SetProperty("prototype", proto)
	proto.SetProperty("constructor", ctor)

	tgStatic(ctor, "from", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return tgType("PlainYearMonth.from requires an argument")
		}
		if ym, ok := asPlainYearMonth(args[0]); ok {
			return ym
		}
		switch v := args[0].(type) {
		case *object.String:
			f, ok := object.ParseTemporalYearMonthString(v.Value)
			if !ok {
				return tgRange("Invalid PlainYearMonth string: " + v.Value)
			}
			return object.NewTemporalPlainYearMonth(f.Year, f.Month, 1, object.DefaultCalendarID)
		case *object.Object:
			y, m, err := yearMonthFields(v)
			if err != nil {
				return err
			}
			return object.NewTemporalPlainYearMonth(y, m, 1, object.DefaultCalendarID)
		}
		return tgType("PlainYearMonth.from: unsupported argument")
	})

	tgStatic(ctor, "compare", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return tgType("compare requires two PlainYearMonths")
		}
		a, _ := asPlainYearMonth(args[0])
		b, _ := asPlainYearMonth(args[1])
		if a == nil || b == nil {
			return tgType("compare: arguments must be PlainYearMonth")
		}
		x, y := int64(a.Year())*12+int64(a.Month()), int64(b.Year())*12+int64(b.Month())
		if x < y {
			return object.NewNumber(-1)
		}
		if x > y {
			return object.NewNumber(1)
		}
		return object.NewNumber(0)
	})

	setToStringTag(proto, "Temporal.PlainYearMonth")
	temporal.SetProperty("PlainYearMonth", ctor)
}

// yearMonthFieldsFromWith 处理 PlainYearMonth.with: 未指定字段沿用原值。
func yearMonthFieldsFromWith(t *object.TemporalPlainYearMonth, src object.Value) (int32, int32, object.Value) {
	o, ok := src.(*object.Object)
	if !ok {
		return 0, 0, tgType("with requires an object")
	}
	y, m := t.Year(), t.Month()
	if v, found := o.GetProperty("year"); found && v != object.UndefinedSingleton {
		y = int32(toFloat(v))
	}
	if v, found := o.GetProperty("month"); found && v != object.UndefinedSingleton {
		m = int32(toFloat(v))
	}
	if v, found := o.GetProperty("monthCode"); found && v != object.UndefinedSingleton {
		s, ok := v.(*object.String)
		if !ok || len(s.Value) != 3 || s.Value[0] != 'M' {
			return 0, 0, tgRange("Invalid monthCode")
		}
		m = int32(s.Value[1]-'0')*10 + int32(s.Value[2]-'0')
	}
	if m < 1 || m > 12 {
		return 0, 0, tgRange("month must be between 1 and 12")
	}
	return y, m, nil
}

// ymDiff 计算两个 PlainYearMonth 的差值 (只产生年/月)。
func ymDiff(t *object.TemporalPlainYearMonth, args []object.Value, since bool) object.Value {
	if len(args) == 0 {
		return tgType("missing argument")
	}
	other, ok := asPlainYearMonth(args[0])
	if !ok {
		return tgType("argument is not a PlainYearMonth")
	}
	var opts object.Value
	if len(args) > 1 {
		opts = args[1]
	}
	largest, ok := getLargestUnit(opts, []string{"year", "month"})
	if !ok {
		return tgRange("Invalid largestUnit for PlainYearMonth")
	}
	a := int64(t.Year())*12 + int64(t.Month())
	b := int64(other.Year())*12 + int64(other.Month())
	diff := b - a
	if since {
		diff = -diff
	}
	if largest == "year" || largest == "auto" {
		return object.NewTemporalDuration(diff/12, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	}
	return object.NewTemporalDuration(0, diff, 0, 0, 0, 0, 0, 0, 0, 0)
}

// floorDiv64 向下取整除法 (负数时也正确)。
func floorDiv64(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// ==================== PlainMonthDay ====================

func setupTemporalPlainMonthDay(temporal *object.Object) {
	proto := object.NewObject()
	object.SetTemporalProto(object.TEMPORAL_PLAIN_MD_OBJ, proto)

	tgGetter(proto, "monthCode", func(this object.Value) object.Value {
		t, ok := asPlainMonthDay(this)
		if !ok {
			return tgTypeBadThis("PlainMonthDay")
		}
		return object.NewString(objectPadMonth(t.Month()))
	})
	tgGetter(proto, "month", func(this object.Value) object.Value {
		t, ok := asPlainMonthDay(this)
		if !ok {
			return tgTypeBadThis("PlainMonthDay")
		}
		return object.NewNumber(float64(t.Month()))
	})
	tgGetter(proto, "day", func(this object.Value) object.Value {
		t, ok := asPlainMonthDay(this)
		if !ok {
			return tgTypeBadThis("PlainMonthDay")
		}
		return object.NewNumber(float64(t.Day()))
	})
	tgStrGetter(proto, "calendarId", func(this object.Value) string {
		t, ok := asPlainMonthDay(this)
		if !ok {
			return ""
		}
		return t.CalendarID()
	})

	tgMethod(proto, "with", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainMonthDay(this)
		if !ok {
			return tgTypeBadThis("PlainMonthDay")
		}
		if len(args) == 0 {
			return tgType("with requires an object")
		}
		o, ok := args[0].(*object.Object)
		if !ok {
			return tgType("with requires an object")
		}
		m, d := t.Month(), t.Day()
		if v, found := o.GetProperty("month"); found && v != object.UndefinedSingleton {
			m = int32(toFloat(v))
		}
		if v, found := o.GetProperty("monthCode"); found && v != object.UndefinedSingleton {
			s, ok := v.(*object.String)
			if !ok || len(s.Value) != 3 {
				return tgRange("Invalid monthCode")
			}
			m = int32(s.Value[1]-'0')*10 + int32(s.Value[2]-'0')
		}
		if v, found := o.GetProperty("day"); found && v != object.UndefinedSingleton {
			d = int32(toFloat(v))
		}
		if m < 1 || m > 12 || d < 1 || d > object.DaysInMonth(1972, m) {
			return tgRange("Invalid month-day")
		}
		return object.NewTemporalPlainMonthDay(m, d, t.RefYear(), t.CalendarID())
	})

	tgMethod(proto, "equals", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainMonthDay(this)
		if !ok {
			return tgTypeBadThis("PlainMonthDay")
		}
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		o, ok := asPlainMonthDay(args[0])
		if !ok {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(t.Month() == o.Month() && t.Day() == o.Day())
	})

	// toPlainDate 需要补一个"年"，02-29 只有在闰年才合法
	tgMethod(proto, "toPlainDate", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asPlainMonthDay(this)
		if !ok {
			return tgTypeBadThis("PlainMonthDay")
		}
		year := t.RefYear()
		if len(args) > 0 {
			if o, ok := args[0].(*object.Object); ok {
				if yv, found := o.GetProperty("year"); found && yv != object.UndefinedSingleton {
					year = int32(toFloat(yv))
				}
			}
		}
		d := object.ISODate{Year: year, Month: t.Month(), Day: t.Day()}
		if !object.IsValidISODate(d) {
			return tgRange("Invalid date for the given year")
		}
		return object.NewTemporalPlainDate(d, t.CalendarID())
	})

	tgToStringMethod(proto, func(this object.Value) string {
		t, ok := asPlainMonthDay(this)
		if !ok {
			return ""
		}
		return t.ToString()
	})
	tgMethod(proto, "valueOf", func(this object.Value, args ...object.Value) object.Value {
		return this
	})

	ctor := object.NewBuiltin("PlainMonthDay", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return tgType("PlainMonthDay requires month and day")
		}
		m, _ := argFloat(args, 0)
		d, _ := argFloat(args, 1)
		mi, di := int32(m), int32(d)
		// 02-29 在 PlainMonthDay 中是合法的，参考年用 1972 (闰年)
		if mi < 1 || mi > 12 || di < 1 || di > object.DaysInMonth(1972, mi) {
			return tgRange("Invalid month-day")
		}
		return object.NewTemporalPlainMonthDay(mi, di, 1972, object.DefaultCalendarID)
	})
	ctor.SetProperty("name", object.NewString("PlainMonthDay"))
	ctor.SetProperty("prototype", proto)
	proto.SetProperty("constructor", ctor)

	tgStatic(ctor, "from", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return tgType("PlainMonthDay.from requires an argument")
		}
		if md, ok := asPlainMonthDay(args[0]); ok {
			return md
		}
		switch v := args[0].(type) {
		case *object.String:
			f, ok := object.ParseTemporalMonthDayString(v.Value)
			if !ok {
				return tgRange("Invalid PlainMonthDay string: " + v.Value)
			}
			return object.NewTemporalPlainMonthDay(f.Month, f.Day, 1972, object.DefaultCalendarID)
		case *object.Object:
			mv, okM := v.GetProperty("month")
			dv, okD := v.GetProperty("day")
			if !okM || !okD {
				return tgType("PlainMonthDay.from requires month and day")
			}
			return object.NewTemporalPlainMonthDay(int32(toFloat(mv)), int32(toFloat(dv)), 1972,
				object.DefaultCalendarID)
		}
		return tgType("PlainMonthDay.from: unsupported argument")
	})

	setToStringTag(proto, "Temporal.PlainMonthDay")
	temporal.SetProperty("PlainMonthDay", ctor)
}

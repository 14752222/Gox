package stdlib

import (
	"math/big"

	"js-runtime/object"
)

// 本文件实现 Temporal.ZonedDateTime、Temporal.TimeZone 与 Temporal.Calendar。
//
// ZonedDateTime 是 Temporal 中最复杂也最有价值的类型——它同时携带精确时刻
// 与时区，因此夏令时的空洞/重复可以直接判定，而不是像 Date 那样静默算错。

func setupTemporalZonedDateTime(temporal *object.Object) {
	proto := object.NewObject()
	object.SetTemporalProto(object.TEMPORAL_ZONED_DATETIME_OBJ, proto)

	// zdtISO 取出实例在当前时区下的墙钟字段。
	zdtISO := func(t *object.TemporalZonedDateTime) object.ISODateTime {
		return object.GetISOPartsFromEpoch(t.TimeZoneID(), t.Nanos())
	}

	// ===== 墙钟字段 getter =====
	fieldGetter := func(name string, fn func(dt object.ISODateTime) int64) {
		tgGetter(proto, name, func(this object.Value) object.Value {
			t, ok := asZonedDateTime(this)
			if !ok {
				return tgTypeBadThis("ZonedDateTime")
			}
			return object.NewNumber(float64(fn(zdtISO(t))))
		})
	}
	fieldGetter("year", func(dt object.ISODateTime) int64 { return int64(dt.Date.Year) })
	fieldGetter("month", func(dt object.ISODateTime) int64 { return int64(dt.Date.Month) })
	fieldGetter("day", func(dt object.ISODateTime) int64 { return int64(dt.Date.Day) })
	fieldGetter("hour", func(dt object.ISODateTime) int64 { return int64(dt.Time.Hour) })
	fieldGetter("minute", func(dt object.ISODateTime) int64 { return int64(dt.Time.Minute) })
	fieldGetter("second", func(dt object.ISODateTime) int64 { return int64(dt.Time.Second) })
	fieldGetter("millisecond", func(dt object.ISODateTime) int64 { return int64(dt.Time.Millisecond) })
	fieldGetter("microsecond", func(dt object.ISODateTime) int64 { return int64(dt.Time.Microsecond) })
	fieldGetter("nanosecond", func(dt object.ISODateTime) int64 { return int64(dt.Time.Nanosecond) })
	fieldGetter("dayOfWeek", func(dt object.ISODateTime) int64 { return int64(dt.Date.DayOfWeek()) })
	fieldGetter("dayOfYear", func(dt object.ISODateTime) int64 { return int64(dt.Date.DayOfYear()) })
	fieldGetter("weekOfYear", func(dt object.ISODateTime) int64 { return int64(dt.Date.WeekOfYear()) })
	fieldGetter("daysInMonth", func(dt object.ISODateTime) int64 {
		return int64(object.DaysInMonth(dt.Date.Year, dt.Date.Month))
	})
	fieldGetter("daysInYear", func(dt object.ISODateTime) int64 { return int64(object.DaysInYear(dt.Date.Year)) })
	fieldGetter("monthsInYear", func(dt object.ISODateTime) int64 { return 12 })
	fieldGetter("daysInWeek", func(dt object.ISODateTime) int64 { return 7 })

	// ===== 时区相关 getter =====
	// offsetNanoseconds 是该时刻在该时区的 UTC 偏移，纳秒精度。
	tgGetter(proto, "offsetNanoseconds", func(this object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		return object.NewNumber(float64(int64(object.TimeZoneOffsetSecondsAt(t.TimeZoneID(), t.Nanos())) * object.NanosPerSecond))
	})
	tgStrGetter(proto, "offset", func(this object.Value) string {
		t, ok := asZonedDateTime(this)
		if !ok {
			return ""
		}
		return object.GetOffsetStringFor(t.TimeZoneID(), t.Nanos())
	})
	tgStrGetter(proto, "timeZoneId", func(this object.Value) string {
		t, ok := asZonedDateTime(this)
		if !ok {
			return ""
		}
		return t.TimeZoneID()
	})
	tgStrGetter(proto, "calendarId", func(this object.Value) string {
		t, ok := asZonedDateTime(this)
		if !ok {
			return ""
		}
		return t.CalendarID()
	})
	tgGetter(proto, "monthCode", func(this object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		return object.NewString(objectPadMonth(zdtISO(t).Date.Month))
	})
	tgGetter(proto, "inLeapYear", func(this object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		return object.NewBoolean(zdtISO(t).Date.InLeapYear())
	})
	// epochMilliseconds / epochNanoseconds: 精确时刻与 Instant 的表示一致。
	tgGetter(proto, "epochMilliseconds", func(this object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		return object.NewNumber(float64(object.EpochNanosToMilliseconds(t.Nanos())))
	})
	tgGetter(proto, "epochNanoseconds", func(this object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		return object.NewBigInt(t.Nanos())
	})

	// hoursInDay: 夏令时切换日当天可能是 23 或 25 小时。
	tgGetter(proto, "hoursInDay", func(this object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		dt := zdtISO(t)
		start := object.DaysFromCivil(dt.Date.Year, dt.Date.Month, dt.Date.Day) * object.NanosPerDay
		startNs := big.NewInt(start)
		next := new(big.Int).Add(startNs, big.NewInt(object.NanosPerDay))
		offStart := int64(object.TimeZoneOffsetSecondsAt(t.TimeZoneID(), startNs)) * object.NanosPerSecond
		offNext := int64(object.TimeZoneOffsetSecondsAt(t.TimeZoneID(), next)) * object.NanosPerSecond
		return object.NewNumber(float64(object.NanosPerDay + offStart - offNext) / float64(object.NanosPerHour))
	})

	// ===== 构造与替换 =====
	tgMethod(proto, "with", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		if len(args) == 0 {
			return tgType("ZonedDateTime.prototype.with requires an object")
		}
		dt, ok := mergeDateTimeFields(zdtISO(t), args[0], true)
		if !ok {
			return tgType("ZonedDateTime.prototype.with: invalid fields")
		}
		return zdtFromWallClock(dt, t.TimeZoneID(), t.CalendarID(), "compatible", args[0])
	})

	tgMethod(proto, "withPlainTime", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		dt := zdtISO(t)
		dt.Time = object.ISOTime{}
		if len(args) > 0 {
			if pt, ok := asPlainTime(args[0]); ok {
				dt.Time = pt.ISOTime()
			} else if merged, ok := mergeTimeFields(dt.Time, args[0]); ok {
				dt.Time = merged
			}
		}
		return zdtFromWallClock(dt, t.TimeZoneID(), t.CalendarID(), "compatible", nil)
	})

	tgMethod(proto, "withPlainDate", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		if len(args) == 0 {
			return tgType("withPlainDate requires a PlainDate")
		}
		pd, ok := asPlainDate(args[0])
		if !ok {
			return tgType("withPlainDate: argument is not a PlainDate")
		}
		dt := zdtISO(t)
		dt.Date = pd.ISODate()
		return zdtFromWallClock(dt, t.TimeZoneID(), t.CalendarID(), "compatible", nil)
	})

	tgMethod(proto, "withTimeZone", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		if len(args) == 0 {
			return tgType("withTimeZone requires a time zone")
		}
		tz, ok := toTimeZoneID(args[0])
		if !ok {
			return tgRange("Invalid time zone")
		}
		return object.NewTemporalZonedDateTime(t.Nanos(), tz, t.CalendarID())
	})

	tgMethod(proto, "startOfDay", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		dt := zdtISO(t)
		return zdtFromWallClock(object.ISODateTime{Date: dt.Date, Time: object.ISOTime{}},
			t.TimeZoneID(), t.CalendarID(), "compatible", nil)
	})

	// ===== 算术 =====
	// ZonedDateTime 的加法要先看点重阳午夜: 时间单位走 Instant (精确相加)，
	// 日历单位走墙钟 (保留本地时刻)，两者语义不同。
	tgMethod(proto, "add", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		return zdtShift(t, args, false)
	})
	tgMethod(proto, "subtract", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		return zdtShift(t, args, true)
	})

	// ===== 差值 =====
	tgMethod(proto, "until", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		return zdtDiffFrom(t, args, false)
	})
	tgMethod(proto, "since", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		return zdtDiffFrom(t, args, true)
	})

	tgMethod(proto, "round", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		var opts object.Value
		if len(args) > 0 {
			opts = args[0]
		}
		smallest, ok := getSmallestUnit(opts, datetimeUnits)
		if !ok || smallest == "auto" {
			return tgRange("Invalid smallestUnit for ZonedDateTime.round")
		}
		inc, ok := getRoundingIncrement(opts)
		if !ok {
			return tgRange("Invalid roundingIncrement")
		}
		mode := getRoundingMode(opts)
		if mode == "invalid" {
			return tgRange("Invalid roundingMode")
		}
		res, ok := roundInstantNanos(t.Nanos(), smallest, inc, mode)
		if !ok {
			return tgRange("Result is outside of supported range")
		}
		return object.NewTemporalZonedDateTime(res, t.TimeZoneID(), t.CalendarID())
	})

	tgMethod(proto, "equals", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		o, ok := asZonedDateTime(args[0])
		if !ok {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(t.Nanos().Cmp(o.Nanos()) == 0 && t.TimeZoneID() == o.TimeZoneID())
	})

	// ===== 转换 =====
	tgMethod(proto, "toInstant", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		return object.NewTemporalInstant(t.Nanos())
	})
	tgMethod(proto, "toPlainDateTime", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		return object.NewTemporalPlainDateTime(zdtISO(t), t.CalendarID())
	})
	tgMethod(proto, "toPlainDate", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		return object.NewTemporalPlainDate(zdtISO(t).Date, t.CalendarID())
	})
	tgMethod(proto, "toPlainTime", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		return object.NewTemporalPlainTime(zdtISO(t).Time)
	})

	tgToStringMethod(proto, func(this object.Value) string {
		t, ok := asZonedDateTime(this)
		if !ok {
			return ""
		}
		return t.ToString()
	})
	tgMethod(proto, "valueOf", func(this object.Value, args ...object.Value) object.Value {
		return this
	})

	ctor := object.NewBuiltin("ZonedDateTime", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return tgType("ZonedDateTime requires epochNanoseconds")
		}
		bn, ok := args[0].(*object.BigInt)
		if !ok {
			return tgType("epochNanoseconds must be a BigInt")
		}
		if !object.IsValidEpochNanos(bn.Value) {
			return tgRange("epochNanoseconds is out of range")
		}
		tzid := "UTC"
		if len(args) > 1 {
			tz, ok := toTimeZoneID(args[1])
			if !ok {
				return tgRange("Invalid time zone")
			}
			tzid = tz
		}
		return object.NewTemporalZonedDateTime(bn.Value, tzid, object.DefaultCalendarID)
	})
	ctor.SetProperty("name", object.NewString("ZonedDateTime"))
	ctor.SetProperty("prototype", proto)
	proto.SetProperty("constructor", ctor)

	tgStatic(ctor, "from", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return tgType("ZonedDateTime.from requires an argument")
		}
		if z, ok := asZonedDateTime(args[0]); ok {
			return z
		}
		switch v := args[0].(type) {
		case *object.String:
			f, ok := object.ParseTemporalZonedDateTimeString(v.Value)
			if !ok {
				return tgRange("Invalid ZonedDateTime string: " + v.Value)
			}
			if f.Calendar != "" && f.Calendar != object.DefaultCalendarID {
				return tgRange("Unsupported calendar: " + f.Calendar)
			}
			if f.TimeZoneAnnotation == "" {
				return tgRange("ZonedDateTime string requires a time zone annotation")
			}
			return zdtFromFields(f, args)
		case *object.Object:
			dt, ok := dateTimeFromFieldsObject(v)
			if !ok {
				return tgType("ZonedDateTime.from: invalid fields")
			}
			tz := tzOfFieldsObject(v)
			var opts object.Value
			if len(args) > 1 {
				opts = args[1]
			}
			// 属性包自带 offset 时直接得到精确时刻，跳过 DST 求解。
			if off, ok := offsetOfFieldsObject(v); ok {
				want, ok := object.ParseTimeZoneOffsetString(off)
				if !ok {
					return tgRange("Invalid offset")
				}
				ns := new(big.Int).Sub(dt.EpochNanos(), big.NewInt(int64(want)*object.NanosPerSecond))
				return object.NewTemporalZonedDateTime(ns, tz, object.DefaultCalendarID)
			}
			return zdtFromWallClock(dt, tz, object.DefaultCalendarID, "compatible", opts)
		}
		return tgType("ZonedDateTime.from: unsupported argument")
	})

	tgStatic(ctor, "compare", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return tgType("compare requires two ZonedDateTimes")
		}
		a, _ := asZonedDateTime(args[0])
		b, _ := asZonedDateTime(args[1])
		if a == nil || b == nil {
			return tgType("compare: arguments must be ZonedDateTime")
		}
		return object.NewNumber(float64(a.Nanos().Cmp(b.Nanos())))
	})

	setToStringTag(proto, "Temporal.ZonedDateTime")
	temporal.SetProperty("ZonedDateTime", ctor)
}

// ===== 内部辅助 =====

// roundInstantNanos 对精确时刻做舍入。
func roundInstantNanos(ns *big.Int, smallest string, inc int64, mode string) (*big.Int, bool) {
	n, ok := object.UnitToNanos(smallest)
	if !ok {
		return nil, false
	}
	increment := new(big.Int).Mul(big.NewInt(n), big.NewInt(inc))
	res := object.RoundNanos(ns, increment, mode)
	if !object.IsValidEpochNanos(res) {
		return nil, false
	}
	return res, true
}

// zdtFromWallClock 由墙钟字段 + 时区求解精确时刻。
//
// 这正是 DST 问题的核心处理点: 同一个墙钟时间可能对应 0 个 (春季空洞)
// 或 2 个 (秋季重复) 精确时刻，必须显式指定 disambiguation 才能确定。
func zdtFromWallClock(dt object.ISODateTime, tz, calendar, disambiguation string, opts object.Value) object.Value {
	if !object.IsValidISODate(dt.Date) || !object.IsValidISOTime(dt.Time) {
		return tgRange("Invalid ISO date-time")
	}
	if opts != nil {
		if d := optString(opts, "disambiguation", ""); d != "" {
			ok := false
			for _, cand := range []string{"compatible", "earlier", "later", "reject"} {
				if cand == d {
					ok = true
				}
			}
			if !ok {
				return tgRange("Invalid disambiguation option")
			}
			disambiguation = d
		}
		if o := getOverflow(opts); o == "invalid" {
			return tgRange("Invalid overflow option")
		}
	}
	candidates := object.GetPossibleInstantsFor(tz, dt)
	ns, ok := object.DisambiguatePossibleInstants(candidates, tz, dt, disambiguation)
	if !ok {
		return tgRange("Ambiguous or non-existent wall-clock time (use the disambiguation option)")
	}
	return object.NewTemporalZonedDateTime(ns, tz, calendar)
}

// zdtFromFields 由带注释的解析结果构造 ZonedDateTime。
func zdtFromFields(f object.TemporalFields, args []object.Value) object.Value {
	tz := f.TimeZoneAnnotation
	disambiguation := "compatible"
	var opts object.Value
	if len(args) > 1 {
		opts = args[1]
		disambiguation = getDisambiguation(opts)
		if disambiguation == "invalid" {
			return tgRange("Invalid disambiguation option")
		}
		if o := optString(opts, "offset", ""); o != "" {
			// options.offset 用于在 DST 歧义时挑选候选 (这里我们只需判定兼容)
			_ = o
		}
	}
	// 字符串自带 UTC 偏移时直接得到精确时刻，无需再查时区。
	if f.OffsetSeconds != nil {
		dt := f.ISODateTime()
		ns := new(big.Int).Sub(dt.EpochNanos(), big.NewInt(int64(*f.OffsetSeconds)*object.NanosPerSecond))
		if object.IsValidEpochNanos(ns) {
			if len(args) > 1 {
				if o := optString(opts, "offset", ""); o != "" && !offsetMatches(ns, tz, o) {
					return tgRange("Offset does not match the given time zone")
				}
			}
			return object.NewTemporalZonedDateTime(ns, tz, object.DefaultCalendarID)
		}
	}
	return zdtFromWallClock(f.ISODateTime(), tz, object.DefaultCalendarID, disambiguation, opts)
}

// offsetMatches 校验给定时刻的时区偏移是否与字符串中的偏移一致。
func offsetMatches(ns *big.Int, tz, offset string) bool {
	if !object.IsValidTimeZoneID(tz) {
		return true
	}
	want, ok := object.ParseTimeZoneOffsetString(offset)
	if !ok {
		return true
	}
	return object.TimeZoneOffsetSecondsAt(tz, ns) == want
}

// zdtShift 实现 ZonedDateTime.add/subtract。
//
// 语义分叉:
//   - 纯时间 Duration (< 1 天且无日历单位): 走 Instant 精确相加
//   - 含日历单位: 走墙钟运算 (保留当地时刻)，再按 disambiguation 求解
func zdtShift(t *object.TemporalZonedDateTime, args []object.Value, negate bool) object.Value {
	if len(args) == 0 {
		return tgType("missing duration")
	}
	d, ok := toDurationValue(args[0])
	if !ok {
		return tgType("invalid duration")
	}
	var opts object.Value
	if len(args) > 1 {
		opts = args[1]
	}
	if negate {
		d = d.Negated()
	}
	if !d.HasCalendarUnits() {
		ns := new(big.Int).Add(t.Nanos(), d.TimeTotalNanos())
		if !object.IsValidEpochNanos(ns) {
			return tgRange("Result is outside of supported range")
		}
		return object.NewTemporalZonedDateTime(ns, t.TimeZoneID(), t.CalendarID())
	}
	// 墙钟加法: 先算当地的新墙钟时间，再求解它对应的精确时刻
	overflow := getOverflow(opts)
	if overflow == "invalid" {
		return tgRange("Invalid overflow option")
	}
	disambiguation := getDisambiguation(opts)
	if disambiguation == "invalid" {
		return tgRange("Invalid disambiguation option")
	}
	dt, ok := object.AddDateTime(object.GetISOPartsFromEpoch(t.TimeZoneID(), t.Nanos()), d, overflow)
	if !ok {
		return tgRange("Result is outside of supported range")
	}
	return zdtFromWallClock(dt, t.TimeZoneID(), t.CalendarID(), disambiguation, opts)
}

// zdtDiffFrom 计算两个 ZonedDateTime 的差值。
//
// 日历单位的差值在墙钟上计算 (差值可能与 Instant 相差一个小时——夏令时
// 切换时两个准确时刻相差 4 小时但当地钟面只走了 3 小时)，这是 Temporal
// 相对于 Date 语义更精确的一处体现。
func zdtDiffFrom(t *object.TemporalZonedDateTime, args []object.Value, since bool) object.Value {
	if len(args) == 0 {
		return tgType("missing argument")
	}
	other, ok := asZonedDateTime(args[0])
	if !ok {
		return tgType("argument is not a ZonedDateTime")
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
	if largest == "auto" {
		largest = "hour"
	}
	su := smallest
	if su == "auto" {
		su = "nanosecond"
	}
	if unitRank(su) < unitRank(largest) {
		return tgRange("smallestUnit is larger than largestUnit")
	}

	sign := int64(1)
	if t.Nanos().Cmp(other.Nanos()) > 0 {
		sign = -1
	}

	// 日历单位: 用各自时区的墙钟字段计算，而非直接用 Instant 差
	if unitRank(largest) <= unitRank("day") {
		fromDT := object.GetISOPartsFromEpoch(t.TimeZoneID(), t.Nanos())
		toDT := object.GetISOPartsFromEpoch(t.TimeZoneID(), other.Nanos())
		years, months, weeks, days := object.DiffISODate(fromDT.Date, toDT.Date, largest)
		interDate, ok := object.AddDateDuration(fromDT.Date, years, months, weeks, days, "constrain")
		if !ok {
			return tgRange("Difference out of range")
		}
		interDT := object.ISODateTime{Date: interDate, Time: fromDT.Time}
		rem := new(big.Int).Sub(other.Nanos(), new(big.Int).Add(t.Nanos(),
			new(big.Int).Sub(interDT.EpochNanos(), fromDT.EpochNanos())))
		timeDur := durationFromNanosByLargest(rem, su)
		dur := object.NewTemporalDuration(
			int64(years), int64(months), int64(weeks), int64(days),
			timeDur.Hours(), timeDur.Minutes(), timeDur.Seconds(),
			timeDur.Milliseconds(), timeDur.Microseconds(), timeDur.Nanoseconds())
		if sign < 0 && other.Nanos().Cmp(t.Nanos()) < 0 {
			return dur.Negated()
		}
		return dur
	}

	total := new(big.Int).Sub(other.Nanos(), t.Nanos())
	rounded := roundNanosToUnit(total, su, inc, mode)
	dur := durationFromNanosByLargest(rounded, largest)
	if sign < 0 && other.Nanos().Cmp(t.Nanos()) < 0 {
		return dur.Negated()
	}
	return dur
}

// tzOfFieldsObject 由属性包提取 timeZone 字段。
// 缺失时按规范使用宿主默认时区；这里退化为 UTC 以免依赖宿主环境配置。
func tzOfFieldsObject(o *object.Object) string {
	v, found := o.GetProperty("timeZone")
	if !found || v == object.UndefinedSingleton {
		return "UTC"
	}
	if id, ok := toTimeZoneID(v); ok {
		return id
	}
	return "UTC"
}

// offsetOfFieldsObject 由属性包提取 offset 字段 (可选)。
func offsetOfFieldsObject(o *object.Object) (string, bool) {
	v, found := o.GetProperty("offset")
	if !found || v == object.UndefinedSingleton {
		return "", false
	}
	s, ok := v.(*object.String)
	if !ok {
		return "", false
	}
	return s.Value, true
}

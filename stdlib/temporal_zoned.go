package stdlib

import (
	"math/big"

	"github.com/14752222/Gox/object"
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
	fieldGetter("yearOfWeek", func(dt object.ISODateTime) int64 { return int64(dt.Date.YearOfWeek()) })
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
		return object.NewNumber(float64(object.NanosPerDay+offStart-offNext) / float64(object.NanosPerHour))
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
		if len(args) > 1 {
			if e := optionsTypeErr(args[1]); e != nil {
				return e
			}
			if _, oerr := getOverflow(args[1]); oerr != nil {
				return oerr
			}
			if _, derr := getDisambiguation(args[1]); derr != nil {
				return derr
			}
			if _, oerr2 := getEnumOption(args[1], "offset", "prefer", "prefer", "use", "ignore", "reject"); oerr2 != nil {
				return oerr2
			}
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
		if len(args) > 0 && args[0] != object.UndefinedSingleton && args[0] != nil {
			pt, terr := toPlainTimeValue(args[0])
			if terr != nil {
				return terr
			}
			dt.Time = pt.ISOTime()
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
		tz, tzTzErr := toTimeZoneIDE(args[0])
		if tzTzErr != nil {
			return tzTzErr
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
		if len(args) == 0 {
			return tgType("ZonedDateTime.prototype.round requires an argument")
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
			return tgRange("Invalid smallestUnit for ZonedDateTime.round")
		}
		if !validRoundingIncrement(smallest, inc) {
			return tgRange("Invalid roundingIncrement for " + smallest)
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
			return tgType("equals requires an argument")
		}
		o, cerr := toZonedDateTimeValue(args[0])
		if cerr != nil {
			return cerr
		}
		return object.NewBoolean(t.Nanos().Cmp(o.Nanos()) == 0 && t.TimeZoneID() == o.TimeZoneID() && t.CalendarID() == o.CalendarID())
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

	tgToStringMethod(proto, "ZonedDateTime", func(this object.Value, args ...object.Value) (string, object.Value) {
		t, ok := asZonedDateTime(this)
		if !ok {
			return "", nil
		}
		var opts object.Value
		if len(args) > 0 {
			opts = args[0]
		}
		st, perr := parseToStringSettings(opts, true, true)
		if perr != nil {
			return "", perr
		}
		tz := t.TimeZoneID()
		ns := t.Nanos()
		if q := st.roundingQuantumNanos(); q > 0 {
			ns = object.RoundNanos(ns, big.NewInt(q), st.mode)
		}
		off := object.TimeZoneOffsetSecondsAt(tz, ns)
		dt := object.AddOffsetToISODateTime(object.EpochNanosToISODateTime(ns), off)
		prec := st.precision
		if st.smallestUnit != "" {
			prec = precisionForSmallest(st.smallestUnit)
		}
		s := object.FormatISODate(dt.Date) + "T" + object.FormatISOTime(dt.Time, prec)
		s += formatZDTOffset(off, st.offsetName)
		if st.timeZoneName != "never" {
			crit := ""
			if st.timeZoneName == "critical" {
				crit = "!"
			}
			s += "[" + crit + tz + "]"
		}
		return s + calendarAnnotation(st), nil
	})
	tgValueOf(proto)

	ctor := object.NewBuiltin("ZonedDateTime", func(args ...object.Value) object.Value {
		if g := tgCtorGuard("ZonedDateTime"); g != nil {
			return g
		}
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
			tz, tzTzErr := toTimeZoneIDE(args[1])
			if tzTzErr != nil {
				return tzTzErr
			}
			tzid = tz
		}
		return object.NewTemporalZonedDateTime(bn.Value, tzid, object.DefaultCalendarID)
	})
	ctor.SetProperty("name", object.NewString("ZonedDateTime"))
	tgMarkCtorNew(ctor)
	tgSetCtorPrototype(ctor, proto)
	proto.SetBuiltinProperty("constructor", ctor)

	tgStatic(ctor, "from", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return tgType("ZonedDateTime.from requires an argument")
		}
		// spec: 各分支在字段/槽读取完成后才做 GetOptionsObject 并按
		// disambiguation → offset → overflow 顺序读选项。
		if z, ok := asZonedDateTime(args[0]); ok {
			if _, _, _, oe := readZDTOptionsE(argAt(args, 1)); oe != nil {
				return oe
			}
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
			// 注解必须能解析成合法时区标识符 (裸 IANA 名 / UTC / 偏移标识符)。
			// 偏移标识符不允许带小数秒 —— toTimeZoneIDE 会拒绝并规范化,
			// 于是 "[+01:35:00.000000000]" 抛 RangeError, "[+01:35]" 规范化为
			// "+01:35" (zoneddatetime-string-multiple-offsets)。
			if id, tzErr := toTimeZoneIDE(object.NewString(f.TimeZoneAnnotation)); tzErr != nil {
				return tzErr
			} else {
				f.TimeZoneAnnotation = id
			}
			return zdtFromFields(f, args)
		case propBag:
			switch v.(type) {
			case *object.Object, *object.Proxy:
			default:
				return tgType("ZonedDateTime.from: unsupported argument")
			}
			raw, off, tz, ferr := readZDTFieldsRaw(v)
			if ferr != nil {
				return ferr
			}
			dis, _, fromOverflow, oe := readZDTOptionsE(argAt(args, 1))
			if oe != nil {
				return oe
			}
			dt, ferr := raw.regulate(fromOverflow)
			if ferr != nil {
				return ferr
			}
			// 属性包自带 offset 时直接得到精确时刻，跳过 DST 求解。
			if off != "" {
				want, ok := object.ParseTimeZoneOffsetString(off)
				if !ok {
					return tgRange("Invalid offset")
				}
				ns := new(big.Int).Sub(dt.EpochNanos(), big.NewInt(int64(want)*object.NanosPerSecond))
				return object.NewTemporalZonedDateTime(ns, tz, object.DefaultCalendarID)
			}
			return zdtFromWallClock(dt, tz, object.DefaultCalendarID, dis, nil)
		}
		return tgType("ZonedDateTime.from: unsupported argument")
	})

	tgStatic(ctor, "compare", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return tgType("compare requires two ZonedDateTimes")
		}
		a, ea := toZonedDateTimeValue(args[0])
		if ea != nil {
			return ea
		}
		b, eb := toZonedDateTimeValue(args[1])
		if eb != nil {
			return eb
		}
		return object.NewNumber(float64(a.Nanos().Cmp(b.Nanos())))
	})

	tgMethod(proto, "withCalendar", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		if len(args) == 0 {
			return tgType("withCalendar requires an argument")
		}
		cal, cerr := toCalendarIDValue(args[0])
		if cerr != nil {
			return cerr
		}
		return object.NewTemporalZonedDateTime(t.Nanos(), t.TimeZoneID(), cal)
	})

	tgMethod(proto, "toPlainYearMonth", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		d := object.GetISOPartsFromEpoch(t.TimeZoneID(), t.Nanos()).Date
		return object.NewTemporalPlainYearMonth(d.Year, d.Month, d.Day, t.CalendarID())
	})

	tgMethod(proto, "toPlainMonthDay", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asZonedDateTime(this)
		if !ok {
			return tgTypeBadThis("ZonedDateTime")
		}
		d := object.GetISOPartsFromEpoch(t.TimeZoneID(), t.Nanos()).Date
		return object.NewTemporalPlainMonthDay(d.Month, d.Day, d.Year, t.CalendarID())
	})

	tgEraGetters(proto, "ZonedDateTime", func(v object.Value) bool { _, ok := asZonedDateTime(v); return ok })

	setToStringTag(proto, "Temporal.ZonedDateTime")
	temporal.SetBuiltinProperty("ZonedDateTime", ctor)
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
		if _, oErr := getOverflow(opts); oErr != nil {
			return oErr
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
		if e := optionsTypeErr(opts); e != nil {
			return e
		}
		disambiguationTmp, disambiguationErr := getDisambiguation(opts)
		if disambiguationErr != nil {
			return disambiguationErr
		}
		disambiguation = disambiguationTmp
		// options.offset 需按枚举校验 ("use"/"prefer"/"ignore"/"reject")。
		if _, oerr := getOffsetOption(opts); oerr != nil {
			return oerr
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
	d, derr := toDurationE(args[0])
	if derr != nil {
		return derr
	}
	if serr := checkDurationUniformSign(d); serr != nil {
		return serr
	}
	var opts object.Value
	if len(args) > 1 {
		opts = args[1]
	}
	if negate {
		d = d.Negated()
	}
	// spec: 选项包在任何分支之前做 GetOptionsObject + overflow 校验。
	if e := optionsTypeErr(opts); e != nil {
		return e
	}
	overflow, overflowErr := getOverflow(opts)
	if overflowErr != nil {
		return overflowErr
	}
	if !d.HasCalendarUnits() {
		ns := new(big.Int).Add(t.Nanos(), d.TimeTotalNanos())
		if !object.IsValidEpochNanos(ns) {
			return tgRange("Result is outside of supported range")
		}
		return object.NewTemporalZonedDateTime(ns, t.TimeZoneID(), t.CalendarID())
	}
	disambiguation, disambiguationErr := getDisambiguation(opts)
	if disambiguationErr != nil {
		return disambiguationErr
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
	other, cerr := toZonedDateTimeValue(args[0])
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

	sign := int64(1)
	if t.Nanos().Cmp(other.Nanos()) > 0 {
		sign = -1
	}

	// 日历单位: 用各自时区的墙钟字段计算，而非直接用 Instant 差
	if unitRank(largest) <= unitRank("day") {
		fromDT := object.GetISOPartsFromEpoch(t.TimeZoneID(), t.Nanos())
		toDT := object.GetISOPartsFromEpoch(t.TimeZoneID(), other.Nanos())
		if since {
			fromDT, toDT = toDT, fromDT
			mode = negateRoundingMode(mode)
		}
		return calendarRoundDiff(fromDT, toDT, largest, su, inc, mode)
	}

	total := new(big.Int).Sub(other.Nanos(), t.Nanos())
	rounded := roundNanosToUnit(total, su, inc, mode)
	dur := durationFromNanosByLargest(rounded, largest)
	if sign < 0 && other.Nanos().Cmp(t.Nanos()) < 0 {
		return dur.Negated()
	}
	return dur
}

// readZDTFieldsRaw 按字母序读取 ZonedDateTime 属性包字段:
// calendar, day, hour, microsecond, millisecond, minute, month, monthCode,
// nanosecond, offset, second, timeZone, year。校验推迟到选项读取之后。
func readZDTFieldsRaw(o propBag) (rawDateTimeFields, string, string, object.Value) {
	var r rawDateTimeFields
	if ce := checkCalendarProp(o); ce != nil {
		return r, "", "", ce
	}
	d, okD, e := readIntField(o, "day")
	if e != nil {
		return r, "", "", e
	}
	r.day, r.hasDay = d, okD
	tkeys := []string{"hour", "microsecond", "millisecond", "minute"}
	targets := []*int32{&r.time.Hour, &r.time.Microsecond, &r.time.Millisecond, &r.time.Minute}
	for i, k := range tkeys {
		f, ok, e := readIntField(o, k)
		if e != nil {
			return r, "", "", e
		}
		if ok {
			*targets[i] = f
		}
	}
	if e := readMonthField(o, &r.rawDateFields); e != nil {
		return r, "", "", e
	}
	if e := readMonthCodeField(o, &r.rawDateFields); e != nil {
		return r, "", "", e
	}
	if n, ok, e := readIntField(o, "nanosecond"); e != nil {
		return r, "", "", e
	} else if ok {
		r.time.Nanosecond = n
	}
	// offset (ToString + 校验)
	off := ""
	if ov, found := o.GetProperty("offset"); found && ov != object.UndefinedSingleton {
		// 规范 (InterpretISODateTimeOffset 前的 ParseDateTimeUTCOffset):
		// offset 必须是**字符串**; 非字符串一律 TypeError; 字符串必须符合
		// UTCOffset 文法, 否则 RangeError。旧实现用 optionToString 把
		// 数字等转成串后既不判类型也不校验格式, 于是 "00:00"/"+0"/0 之类
		// 全部被静默接受 (ZonedDateTime 各入口的 invalid-offset-string 族)。
		s, isStr := ov.(*object.String)
		if !isStr {
			return r, "", "", tgType("offset must be a string")
		}
		if s.Value != "" {
			if _, ok := object.ParseTimeZoneOffsetString(s.Value); !ok {
				return r, "", "", tgRange(s.Value + " is not a valid offset string")
			}
		}
		off = s.Value
	}
	if s, ok, e := readIntField(o, "second"); e != nil {
		return r, "", "", e
	} else if ok {
		r.time.Second = s
	}
	tz, tzErr := tzOfFieldsObject(o)
	if tzErr != nil {
		return r, "", "", tzErr
	}
	y, okY, e := readIntField(o, "year")
	if e != nil {
		return r, "", "", e
	}
	r.year, r.hasYear = y, okY
	return r, off, tz, nil
}

// tzOfFieldsObject 由属性包提取 timeZone 字段。
// 缺失时按规范使用宿主默认时区；这里退化为 UTC 以免依赖宿主环境配置。
func tzOfFieldsObject(o propBag) (string, object.Value) {
	v, found := o.GetProperty("timeZone")
	if !found || v == object.UndefinedSingleton {
		return "UTC", nil
	}
	return toTimeZoneIDE(v)
}

// offsetOfFieldsObject 由属性包提取 offset 字段 (可选)。
func offsetOfFieldsObject(o propBag) (string, bool) {
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

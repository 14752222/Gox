package stdlib

import (
	"math"
	"math/big"

	"github.com/14752222/Gox/object"
)

// 本文件实现 Temporal.Duration 的 JS 接口。

// durationUnits 是 Duration.round/total 允许的所有单位 (含日历单位)。
var durationUnits = []string{"year", "month", "week", "day",
	"hour", "minute", "second", "millisecond", "microsecond", "nanosecond"}

// durationDefaultLargest 实现 spec 的默认 largestUnit:
// 取 duration 自身最大非零单位与 smallestUnit 中较大者。
func durationDefaultLargest(d *object.TemporalDuration, smallest string) string {
	largest := "nanosecond"
	switch {
	case d.Years() != 0:
		largest = "year"
	case d.Months() != 0:
		largest = "month"
	case d.Weeks() != 0:
		largest = "week"
	case d.Days() != 0:
		largest = "day"
	case d.Hours() != 0:
		largest = "hour"
	case d.Minutes() != 0:
		largest = "minute"
	case d.Seconds() != 0:
		largest = "second"
	case d.Milliseconds() != 0:
		largest = "millisecond"
	case d.Microseconds() != 0:
		largest = "microsecond"
	}
	// LargerOfTwoTemporalUnits: rank 小者单位更大。
	if unitRank(smallest) < unitRank(largest) {
		return smallest
	}
	return largest
}

func setupTemporalDuration(temporal *object.Object) {
	proto := object.NewObject()
	object.SetTemporalProto(object.TEMPORAL_DURATION_OBJ, proto)

	// ===== 字段 getter =====
	intGetters := map[string]func(*object.TemporalDuration) int64{
		"years":        func(d *object.TemporalDuration) int64 { return d.Years() },
		"months":       func(d *object.TemporalDuration) int64 { return d.Months() },
		"weeks":        func(d *object.TemporalDuration) int64 { return d.Weeks() },
		"days":         func(d *object.TemporalDuration) int64 { return d.Days() },
		"hours":        func(d *object.TemporalDuration) int64 { return d.Hours() },
		"minutes":      func(d *object.TemporalDuration) int64 { return d.Minutes() },
		"seconds":      func(d *object.TemporalDuration) int64 { return d.Seconds() },
		"milliseconds": func(d *object.TemporalDuration) int64 { return d.Milliseconds() },
		"microseconds": func(d *object.TemporalDuration) int64 { return d.Microseconds() },
		"nanoseconds":  func(d *object.TemporalDuration) int64 { return d.Nanoseconds() },
	}
	for name, get := range intGetters {
		g := get
		tgGetter(proto, name, func(this object.Value) object.Value {
			d, ok := asDuration(this)
			if !ok {
				return tgTypeBadThis("Duration")
			}
			return object.NewNumber(float64(g(d)))
		})
	}

	// sign: 由第一个非零字段的符号决定。混合符号的 Duration 也按此取值。
	tgGetter(proto, "sign", func(this object.Value) object.Value {
		d, ok := asDuration(this)
		if !ok {
			return tgTypeBadThis("Duration")
		}
		return object.NewNumber(float64(d.Sign()))
	})
	tgGetter(proto, "blank", func(this object.Value) object.Value {
		d, ok := asDuration(this)
		if !ok {
			return tgTypeBadThis("Duration")
		}
		return object.NewBoolean(d.IsBlank())
	})

	// ===== 运算 =====
	tgMethod(proto, "negated", func(this object.Value, args ...object.Value) object.Value {
		d, ok := asDuration(this)
		if !ok {
			return tgTypeBadThis("Duration")
		}
		return d.Negated()
	})
	tgMethod(proto, "abs", func(this object.Value, args ...object.Value) object.Value {
		d, ok := asDuration(this)
		if !ok {
			return tgTypeBadThis("Duration")
		}
		return d.Abs()
	})
	tgMethod(proto, "add", func(this object.Value, args ...object.Value) object.Value {
		d, ok := asDuration(this)
		if !ok {
			return tgTypeBadThis("Duration")
		}
		if len(args) == 0 {
			return tgType("Duration.prototype.add requires a Duration")
		}
		o, derr := toDurationE(args[0])
		if derr != nil {
			return derr
		}
		if serr := checkDurationUniformSign(o); serr != nil {
			return serr
		}
		return balancedDurationAdd(d, o)
	})
	tgMethod(proto, "subtract", func(this object.Value, args ...object.Value) object.Value {
		d, ok := asDuration(this)
		if !ok {
			return tgTypeBadThis("Duration")
		}
		if len(args) == 0 {
			return tgType("Duration.prototype.subtract requires a Duration")
		}
		o, derr := toDurationE(args[0])
		if derr != nil {
			return derr
		}
		if serr := checkDurationUniformSign(o); serr != nil {
			return serr
		}
		return balancedDurationAdd(d, o.Negated())
	})

	tgMethod(proto, "with", func(this object.Value, args ...object.Value) object.Value {
		d, ok := asDuration(this)
		if !ok {
			return tgTypeBadThis("Duration")
		}
		if len(args) == 0 {
			return tgType("Duration.prototype.with requires an object")
		}
		newD, ok := durationFromFieldsObject(args[0])
		if !ok {
			return tgType("Duration.prototype.with: invalid fields")
		}
		// 未指定的字段沿用原值
		pick := func(v int64, def int64) int64 {
			if v != 0 {
				return v
			}
			return def
		}
		return object.NewTemporalDuration(
			pick(newD.Years(), d.Years()), pick(newD.Months(), d.Months()),
			pick(newD.Weeks(), d.Weeks()), pick(newD.Days(), d.Days()),
			pick(newD.Hours(), d.Hours()), pick(newD.Minutes(), d.Minutes()),
			pick(newD.Seconds(), d.Seconds()), pick(newD.Milliseconds(), d.Milliseconds()),
			pick(newD.Microseconds(), d.Microseconds()), pick(newD.Nanoseconds(), d.Nanoseconds()))
	})

	// ===== 舍入与总量 =====
	tgMethod(proto, "round", func(this object.Value, args ...object.Value) object.Value {
		d, ok := asDuration(this)
		if !ok {
			return tgTypeBadThis("Duration")
		}
		if len(args) == 0 {
			return tgType("Duration.prototype.round requires an argument")
		}
		var opts object.Value
		if len(args) > 0 {
			opts = args[0]
		}
		opts, roe := roundOptionsOf(opts)
		if roe != nil {
			return roe
		}
		// 规范读取顺序: largestUnit → relativeTo → roundingIncrement →
		// roundingMode → smallestUnit；逐项 get+cast，语义校验延迟到读完后。
		lv, _, e := castUnitOption(opts, "largestUnit")
		if e != nil {
			return e
		}
		relV := optValue(opts, "relativeTo")
		iv := optValue(opts, "roundingIncrement")
		var incF float64
		if iv != nil {
			var numErr object.Value
			incF, numErr = tgToNumber(iv)
			if numErr != nil {
				return numErr
			}
		}
		mv, e := castOptionString(opts, "roundingMode")
		if e != nil {
			return e
		}
		sv, _, e := castUnitOption(opts, "smallestUnit")
		if e != nil {
			return e
		}

		largest, uerr := validateUnitString(lv, "largestUnit", durationUnits, "auto")
		if uerr != nil {
			return uerr
		}
		inc, incErr := validateRoundingIncrement(iv, nil, incF)
		if incErr != nil {
			return incErr
		}
		mode, modeErr := validateRoundingModeString(mv, "halfExpand")
		if modeErr != nil {
			return modeErr
		}
		smallest, uerr := validateUnitString(sv, "smallestUnit", durationUnits, "auto")
		if uerr != nil {
			return uerr
		}
		relTo, hasRel := relativeToISOValue(relV)

		if hasRel {
			if smallest == "auto" {
				smallest = "nanosecond"
			}
			if largest == "auto" {
				largest = durationDefaultLargest(d, smallest)
			}
			if unitRank(smallest) < unitRank(largest) {
				return tgRange("smallestUnit is larger than largestUnit")
			}
		// 用相对起点把日历单位也折算成纳秒后统一舍入
		end, ok := object.AddDateTime(*relTo, d, "constrain")
		if !ok {
			return tgRange("Duration is out of range")
		}
		if !validRoundingIncrement(smallest, inc) {
			return tgRange("Invalid roundingIncrement for " + smallest)
		}
		dur := calendarRoundDiff(*relTo, end, largest, smallest, inc, mode)
		if e := checkDurationRange(dur); e != nil {
			return e
		}
		return dur
		}

		if d.HasCalendarUnits() {
			return tgRange("Duration with calendar units requires relativeTo to round")
		}
		if smallest == "auto" {
			smallest = "nanosecond"
		}
		if largest == "auto" {
			largest = durationDefaultLargest(d, smallest)
		}
		if unitRank(smallest) < unitRank(largest) {
			return tgRange("smallestUnit is larger than largestUnit")
		}
		if largest == "year" || largest == "month" || largest == "week" {
			return tgRange("rounding to a calendar unit requires relativeTo")
		}
		if !validRoundingIncrement(smallest, inc) {
			return tgRange("Invalid roundingIncrement for " + smallest)
		}
		rounded := roundNanosToUnit(d.TimeTotalNanos(), smallest, inc, mode)
		return durationFromNanosByLargest(rounded, largest)
	})

	tgMethod(proto, "total", func(this object.Value, args ...object.Value) object.Value {
		d, ok := asDuration(this)
		if !ok {
			return tgTypeBadThis("Duration")
		}
		if len(args) == 0 {
			return tgType("Duration.prototype.total requires an argument")
		}
		var opts object.Value
		if len(args) > 0 {
			opts = args[0]
		}
		unit := ""
		var relV object.Value
		if s, isStr := opts.(*object.String); isStr {
			// 字符串简写: total("hours")
			opts = nil
			unit = s.Value
		} else if opts == nil || opts == object.UndefinedSingleton {
			return tgType("Options argument is required")
		} else if e := optionsTypeErr(opts); e != nil {
			return e
		}
		if opts != nil {
			// 规范读取顺序: relativeTo → unit。
			relV = optValue(opts, "relativeTo")
		}
		if unit == "" {
			unit = optString(opts, "unit", "")
		}
		if unit == "" {
			return tgRange("total requires a unit option")
		}
		u, _ := normalizeUnit(unit)
		found := false
		for _, a := range durationUnits {
			if a == u {
				found = true
			}
		}
		if !found {
			return tgRange("Invalid unit: " + unit)
		}
		relTo, _ := relativeToISOValue(relV)
		v, ok := object.TotalOfUnit(d, u, relTo)
		if !ok {
			return tgRange("total of " + u + " requires a relativeTo")
		}
		return object.NewNumber(v)
	})

	tgToStringMethod(proto, "Duration", func(this object.Value, args ...object.Value) (string, object.Value) {
		d, ok := asDuration(this)
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
		_ = st
		return d.ToString(), nil
	})
	tgValueOf(proto)

	// ===== 构造器 =====
	ctor := object.NewBuiltin("Duration", func(args ...object.Value) object.Value {
		get := func(i int) float64 {
			if i >= len(args) || args[i] == object.UndefinedSingleton {
				// spec: 显式 undefined 按 0 处理。
				return 0
			}
			f, _ := argFloat(args, i)
			return f
		}
		// 全部参数必须是整数
		for i := 0; i < 10; i++ {
			f := get(i)
			if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
				return tgRange("Duration arguments must be integers")
			}
		}
		d := object.NewTemporalDuration(
			int64(get(0)), int64(get(1)), int64(get(2)), int64(get(3)), int64(get(4)),
			int64(get(5)), int64(get(6)), int64(get(7)), int64(get(8)), int64(get(9)))
		if cerr := checkDurationRange(d); cerr != nil {
			return cerr
		}
		return d
	})
	ctor.SetProperty("name", object.NewString("Duration"))
	ctor.SetProperty("prototype", proto)
	proto.SetBuiltinProperty("constructor", ctor)

	tgStatic(ctor, "from", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return tgType("Temporal.Duration.from requires an argument")
		}
		d, derr := toDurationE(args[0])
		if derr != nil {
			return derr
		}
		return d
	})

	tgStatic(ctor, "compare", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return tgType("Duration.compare requires two durations")
		}
		a, derr := toDurationE(args[0])
		if derr != nil {
			return derr
		}
		b, derr := toDurationE(args[1])
		if derr != nil {
			return derr
		}
		var opts object.Value
		if len(args) > 2 {
			opts = args[2]
		}
		if e := optionsTypeErr(opts); e != nil {
			return e
		}
		diff, err := compareDurations(a, b, opts)
		if err != nil {
			return err
		}
		return object.NewNumber(float64(diff))
	})

	setToStringTag(proto, "Temporal.Duration")
	temporal.SetBuiltinProperty("Duration", ctor)
}

// compareDurations 比较两个 Duration。
//
// 含日历单位 (年/月/周) 时必须提供 relativeTo——"1 个月" 的长度取决于
// 起点是 1 月还是 2 月，没有起点就无法比较。这是 Duration 与 Date 的
// 本质区别之一。
func compareDurations(a, b *object.TemporalDuration, opts object.Value) (int, object.Value) {
	if !a.HasCalendarUnits() && !b.HasCalendarUnits() {
		return a.TimeTotalNanos().Cmp(b.TimeTotalNanos()), nil
	}
	relTo, ok := relativeToISO(opts)
	if !ok {
		return 0, tgRange("Duration.compare with calendar units requires a relativeTo")
	}
	endA, ok := object.AddDateTime(*relTo, a, "constrain")
	if !ok {
		return 0, tgRange("Duration out of range")
	}
	endB, ok := object.AddDateTime(*relTo, b, "constrain")
	if !ok {
		return 0, tgRange("Duration out of range")
	}
	return endA.EpochNanos().Cmp(endB.EpochNanos()), nil
}

// relativeToISO 从 options.relativeTo 提取锚点时间点。
// 支持 PlainDateTime/PlainDate/ZonedDateTime 实例、属性包与字符串。
func relativeToISO(opts object.Value) (*object.ISODateTime, bool) {
	return relativeToISOValue(optValue(opts, "relativeTo"))
}

// relativeToISOValue 同上，但接受已读出的原始值 (避免重复 get 被观察)。
func relativeToISOValue(v object.Value) (*object.ISODateTime, bool) {
	if v == nil {
		return nil, false
	}
	if zdt, ok := asZonedDateTime(v); ok {
		dt := object.GetISOPartsFromEpoch(zdt.TimeZoneID(), zdt.Nanos())
		return &dt, true
	}
	pdt, cerr := toPlainDateTimeValue(v)
	if cerr != nil {
		return nil, false
	}
	dt := pdt.ISODateTime()
	return &dt, true
}

// balancedDurationAdd 按规范语义做 Duration 加法: 相加后必须平衡字段。
//
// 平衡的目标粒度由两个操作数的"最大非零单位"中较大的那个决定:
//
//	PT1H30M + PT30M → 粒度 hour → PT2H   (而不是未平衡的 PT1H60M)
//	P1Y2M   + P1M   → 粒度 year → P1Y3M
//
// 注意与 Duration.from 的区别: 解析 "PT90M" 得到的就是 PT90M，
// 平衡只发生在运算结果上。
func balancedDurationAdd(a, b *object.TemporalDuration) *object.TemporalDuration {
	years := a.Years() + b.Years()
	months := a.Months() + b.Months()
	weeks := a.Weeks() + b.Weeks()
	days := a.Days() + b.Days()
	timeNs := new(big.Int).Add(durationTimePartNanos(a), durationTimePartNanos(b))

	rank := unitRank(defaultLargestUnitOf(a))
	if r := unitRank(defaultLargestUnitOf(b)); r < rank {
		rank = r
	}

	// 粒度为时间单位时: 整体按纳差拆分，不向天进位
	if rank > unitRank("day") {
		return durationFromNanosByLargest(timeNs, unitsOrdered[rank])
	}

	// 日为界: 时间部分先向天进位
	dayCarry, rem := divModNanos(timeNs, object.NanosPerDay)
	days += dayCarry
	switch {
	case rank <= unitRank("month"): // year / month: 周折算成天
		days += weeks * 7
		weeks = 0
	case rank == unitRank("week"): // week: 天折算成周
		total := days + weeks*7
		weeks, days = total/7, total%7
	default: // day
		days += weeks * 7
		weeks = 0
	}
	_, h, m, s, ms, us, ns := object.BalanceNanosToTime(rem)
	return object.NewTemporalDuration(years, months, weeks, days, h, m, s, ms, us, ns)
}

// durationTimePartNanos 返回 Duration 中"日内"部分的纳秒数 (时及以下，不含天)。
func durationTimePartNanos(d *object.TemporalDuration) *big.Int {
	ns := big.NewInt(0)
	add := func(v, factor int64) {
		ns.Add(ns, new(big.Int).Mul(big.NewInt(v), big.NewInt(factor)))
	}
	add(d.Hours(), object.NanosPerHour)
	add(d.Minutes(), object.NanosPerMinute)
	add(d.Seconds(), object.NanosPerSecond)
	add(d.Milliseconds(), object.NanosPerMillisecond)
	add(d.Microseconds(), object.NanosPerMicrosecond)
	add(d.Nanoseconds(), 1)
	return ns
}

// defaultLargestUnitOf 返回 Duration 中最大的非零单位，全零时按 "second"。
func defaultLargestUnitOf(d *object.TemporalDuration) string {
	switch {
	case d.Years() != 0:
		return "year"
	case d.Months() != 0:
		return "month"
	case d.Weeks() != 0:
		return "week"
	case d.Days() != 0:
		return "day"
	case d.Hours() != 0:
		return "hour"
	case d.Minutes() != 0:
		return "minute"
	}
	return "second"
}

// divModNanos 返回 value 对 m 的向下取整商与非负余数。
func divModNanos(value *big.Int, m int64) (int64, *big.Int) {
	mm := big.NewInt(m)
	q, r := new(big.Int).QuoRem(value, mm, new(big.Int))
	if r.Sign() < 0 {
		q.Sub(q, big.NewInt(1))
		r.Add(r, mm)
	}
	return q.Int64(), r
}

// durationFromNanosByLargest 把纳秒总量按 largestUnit 拆成 Duration 字段。
//
// largestUnit 决定最粗的粒度: 例如 total=90061000000000ns、
// largest="minute" 时得到 PT1501M... 而 largest="day" 时得到 P1DT1H1M40S。
func durationFromNanosByLargest(total *big.Int, largest string) *object.TemporalDuration {
	units := []string{"day", "hour", "minute", "second", "millisecond", "microsecond", "nanosecond"}
	factors := []int64{
		object.NanosPerDay, object.NanosPerHour, object.NanosPerMinute,
		object.NanosPerSecond, object.NanosPerMillisecond, object.NanosPerMicrosecond, 1,
	}
	if largest == "auto" {
		largest = "day"
	}
	start := 0
	for i, u := range units {
		if u == largest {
			start = i
			break
		}
	}
	rest := new(big.Int).Set(total)
	vals := make([]int64, len(units))
	for i := start; i < len(units); i++ {
		m := big.NewInt(factors[i])
		q, r := new(big.Int).QuoRem(rest, m, new(big.Int))
		vals[i] = q.Int64()
		rest = r
	}
	return object.NewTemporalDuration(0, 0, 0, vals[0], vals[1], vals[2],
		vals[3], vals[4], vals[5], vals[6])
}

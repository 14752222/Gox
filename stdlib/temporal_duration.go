package stdlib

import (
	"math"
	"math/big"
	"strconv"
	"strings"

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
		r := balancedDurationAdd(d, o)
		if cerr := checkDurationRange(r); cerr != nil {
			return cerr
		}
		return r
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
		r := balancedDurationAdd(d, o.Negated())
		if cerr := checkDurationRange(r); cerr != nil {
			return cerr
		}
		return r
	})

	tgMethod(proto, "with", func(this object.Value, args ...object.Value) object.Value {
		d, ok := asDuration(this)
		if !ok {
			return tgTypeBadThis("Duration")
		}
		if len(args) == 0 {
			return tgType("Duration.prototype.with requires an object")
		}
		bag, isBag := plainBagOf(args[0])
		if !isBag {
			return tgType("Duration.prototype.with requires an object")
		}
		// ToPartialDuration: 按字母序读十个字段, 只接受"存在且非 undefined"
		// 的字段; 至少一个字段存在, 否则 TypeError。
		names := []string{"days", "hours", "microseconds", "milliseconds",
			"minutes", "months", "nanoseconds", "seconds", "weeks", "years"}
		vals := map[string]int64{}
		any := false
		for _, k := range names {
			v, found := bag.GetProperty(k)
			if !found || v == object.UndefinedSingleton {
				continue
			}
			if e, isErr := v.(*object.Error); isErr {
				return e
			}
			f, cerr := tgToNumber(v)
			if cerr != nil {
				return cerr
			}
			if math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) {
				return tgRange("Duration field must be an integer")
			}
			vals[k] = int64(f)
			any = true
		}
		if !any {
			return tgType("Duration.prototype.with: no recognized fields")
		}
		pick := func(k string, def int64) int64 {
			if v, ok := vals[k]; ok {
				return v
			}
			return def
		}
		r := object.NewTemporalDuration(
			pick("years", d.Years()), pick("months", d.Months()),
			pick("weeks", d.Weeks()), pick("days", d.Days()),
			pick("hours", d.Hours()), pick("minutes", d.Minutes()),
			pick("seconds", d.Seconds()), pick("milliseconds", d.Milliseconds()),
			pick("microseconds", d.Microseconds()), pick("nanoseconds", d.Nanoseconds()))
		// 符号必须一致 (含与 this 混合), 且量级须在范围内。
		if serr := checkDurationUniformSign(r); serr != nil {
			return serr
		}
		if cerr := checkDurationRange(r); cerr != nil {
			return cerr
		}
		return r
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
		largestProvided := optValue(opts, "largestUnit") != nil
		smallestProvided := optValue(opts, "smallestUnit") != nil
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
		relTo, relErr, hasRel := toRelativeTemporalObject(relV)
		if relErr != nil {
			return relErr
		}

		// 规范: 舍入到日历单位 (year/month/week/day) 且带 relativeTo 时,
		// roundingIncrement 不受 "必须整除上级" 约束, 只要求 increment >= 1；
		// 但若显式指定了更大的日历 largestUnit (需跨日历单位 balance),
		// 则 increment 必须为 1, 否则 RangeError。
		explicitLargest := largest
		isCalUnit := func(u string) bool {
			return u == "year" || u == "month" || u == "week" || u == "day"
		}
		validInc := func(u string) bool {
			if hasRel && isCalUnit(u) {
				if inc < 1 {
					return false
				}
				if explicitLargest != "auto" && explicitLargest != u &&
					isCalUnit(explicitLargest) && unitRank(explicitLargest) < unitRank(u) {
					return inc == 1
				}
				return true
			}
			return validRoundingIncrement(u, inc)
		}

		if !largestProvided && !smallestProvided {
			return tgRange("Duration.prototype.round requires either largestUnit or smallestUnit")
		}

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
			if !validInc(smallest) {
				return tgRange("Invalid roundingIncrement for " + smallest)
			}
			end, ok := relEndDateTime(relTo, d)
			if !ok {
				return tgRange("Duration is out of range")
			}
			dur := calendarRoundDiff(relTo.Wall, end, largest, smallest, inc, mode)
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
		res := durationFromNanosByLargest(rounded, largest)
		if e := checkDurationRange(res); e != nil {
			return e
		}
		return res
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
		// 规范读取顺序: relativeTo → unit。
		if opts != nil {
			relV = optValue(opts, "relativeTo")
		}
		relTo, relErr, relProvided := toRelativeTemporalObject(relV)
		if relErr != nil {
			return relErr
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
		v, ok := totalOfUnitRel(d, u, relTo, relProvided)
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
		// Duration 的 smallestUnit 只能是 second 及以下 (规范 GetTemporalUnit
		// 限定 subsecond 单位); minute 及以上 → RangeError。
		if st.smallestUnit == "minute" {
			return "", tgRange("Invalid smallestUnit for Duration.toString: minute")
		}
		// 显示位数: smallestUnit 决定固定位数, 否则由 fractionalSecondDigits
		// 决定; "auto" → -1 (裁剪尾部 0)。
		digits := -1
		if st.smallestUnit != "" {
			switch precisionForSmallest(st.smallestUnit) {
			case "0":
				digits = 0
			case "3":
				digits = 3
			case "6":
				digits = 6
			default:
				digits = 9
			}
		} else if st.precision != "auto" {
			if n, err := strconv.Atoi(st.precision); err == nil {
				digits = n
			}
		}
		quantum := st.roundingQuantumNanos()
		out, ok := d.ToStringWithPrecision(digits, quantum, st.mode)
		if !ok {
			return "", tgRange("Rounded duration is out of range")
		}
		return out, nil
	})
	tgValueOf(proto)

	// ===== 构造器 =====
	ctor := object.NewBuiltin("Duration", func(args ...object.Value) object.Value {
		if g := tgCtorGuard("Duration"); g != nil {
			return g
		}
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
	tgMarkCtorNew(ctor)
	tgSetCtorPrototype(ctor, proto)
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

// temporalRelativeTo 是 ToRelativeTemporalObject 的结果。
//
// 规范区分两种形态，且这一区分直接决定"一天有多长":
//   - Plain 形态 (裸日期串 / 日期时间串 / 无 timeZone 的属性包):
//     墙钟日历，一天恒等于 86400e9 纳秒。
//   - Zoned 形态 (带 [TZ] 注解的串 / 带 timeZone 字段的属性包 /
//     ZonedDateTime 实例): 一天是"当地一个日历日"，夏令时切换日可能
//     是 23 或 25 小时 —— total/round 必须按 epoch 差而非墙钟差计算。
type temporalRelativeTo struct {
	// Wall 是解析出的墙钟日期时间 (zoned 形态取其当地字段)。
	Wall object.ISODateTime
	// TZ 非空表示 zoned 形态; 空串表示 plain 形态。
	TZ string
	// Nanos 是 zoned 形态下的确切时刻; plain 形态下为 nil。
	Nanos *big.Int
}

// IsZoned 判断是否为 zoned 相对起点。
func (r temporalRelativeTo) IsZoned() bool { return r.TZ != "" && r.Nanos != nil }

// totalOfUnitRel 桥接 object.TotalOfUnit，把已解析的相对起点转成锚点。
func totalOfUnitRel(d *object.TemporalDuration, unit string, rel temporalRelativeTo, provided bool) (float64, bool) {
	if !provided {
		return object.TotalOfUnit(d, unit, nil)
	}
	dt := rel.Wall
	if rel.IsZoned() {
		return object.TotalOfUnitZoned(d, unit, dt, rel.TZ, rel.Nanos)
	}
	return object.TotalOfUnit(d, unit, &dt)
}

// relEndDateTime 把 duration 加到相对起点的墙钟上，返回终点墙钟。
// 日历单位运算在墙钟上进行 (与 total 的 zoned 确切时刻路径不同 ——
// round 的日历差值本就以墙钟字段计算)。
func relEndDateTime(rel temporalRelativeTo, d *object.TemporalDuration) (object.ISODateTime, bool) {
	return object.AddDateTime(rel.Wall, d, "constrain")
}

// relativeToISO 从 options.relativeTo 提取锚点时间点。
func relativeToISO(opts object.Value) (*object.ISODateTime, bool) {
	rel, err, ok := toRelativeTemporalObject(optValue(opts, "relativeTo"))
	if !ok || err != nil {
		return nil, false
	}
	dt := rel.Wall
	return &dt, true
}

// relativeToISOValue 返回墙钟锚点 (保留旧签名，供只关心墙钟的调用方使用)。
func relativeToISOValue(v object.Value) (*object.ISODateTime, bool) {
	rel, err, ok := toRelativeTemporalObject(v)
	if !ok || err != nil {
		return nil, false
	}
	dt := rel.Wall
	return &dt, true
}

// toRelativeTemporalObject 实现规范 ToRelativeTemporalObject。
//
// 返回 (值, 错误, 是否已提供)。未提供 relativeTo 时 ok=false, err=nil。
// 错误类型遵循规范: 非字符串原始值 (number/bigint/boolean/null) → TypeError;
// 非法 ISO 串 → RangeError; 对象缺必需字段 → TypeError。
func toRelativeTemporalObject(v object.Value) (temporalRelativeTo, object.Value, bool) {
	var z temporalRelativeTo
	if v == nil {
		return z, nil, false
	}
	switch t := v.(type) {
	case *object.TemporalZonedDateTime:
		wall := object.GetISOPartsFromEpoch(t.TimeZoneID(), t.Nanos())
		return temporalRelativeTo{Wall: wall, TZ: t.TimeZoneID(), Nanos: t.Nanos()}, nil, true
	case *object.TemporalPlainDateTime:
		return temporalRelativeTo{Wall: t.ISODateTime()}, nil, true
	case *object.TemporalPlainDate:
		return temporalRelativeTo{Wall: object.ISODateTime{Date: t.ISODate()}}, nil, true
	case *object.String:
		return relativeFromString(t.Value)
	case *object.Object, *object.Proxy:
		return relativeFromBag(v.(propBag))
	case *object.Undefined:
		return z, nil, false
	}
	// number / bigint / boolean / null / symbol 等原始值: 规范要求 TypeError。
	return z, tgType("relativeTo must be an object or a string"), true
}

// relativeFromString 把 ISO 串解析成相对起点。
// 带 [TZ] 注解 → zoned 形态 (按 offset 或时区消歧); 否则 plain 形态。
func relativeFromString(s string) (temporalRelativeTo, object.Value, bool) {
	var z temporalRelativeTo
	if !strings.ContainsAny(s, "0123456789") {
		// 既非日期也非时间, 交由解析器给出 RangeError。
		return z, tgRange("Invalid ISO string: " + s), true
	}
	// 扩展年 (含前导 +/− 且位数 > 4) 必须由完整文法校验, 裸日期串走
	// TemporalRelativeToString。minus-zero 年份 ("-000000-...") 非法。
	if strings.HasPrefix(s, "-000000") || strings.HasPrefix(s, "\u2212000000") {
		return z, tgRange("Invalid ISO string: " + s), true
	}
	f, ok := object.ParseTemporalRelativeToString(s)
	if !ok {
		return z, tgRange("Invalid ISO string: " + s), true
	}
	if e := calendarFromAnnotation(f.Calendar); e != nil {
		return z, e, true
	}
	if f.TimeZoneAnnotation == "" {
		// plain: 只需要日期部分 (时间可选)。
		if !f.HasDate {
			return z, tgRange("Invalid ISO string: " + s), true
		}
		dt := f.ISODateTime()
		if !object.ISODateTimeInRange(dt) {
			return z, tgRange("PlainDateTime is outside of supported range"), true
		}
		return temporalRelativeTo{Wall: dt}, nil, true
	}
	tz := f.TimeZoneAnnotation
	if id, ok := canonicalTimeZoneID(tz); ok {
		tz = id
	} else {
		return z, tgRange("Invalid time zone: " + tz), true
	}
	dt := f.ISODateTime()
	var ns *big.Int
	if f.OffsetSeconds != nil {
		// 有显式 offset: 先由墙钟 + offset 得到 epoch, 再校验与 tz 一致。
		// 规范: offset 与 [TZ] 注解必须相容 (含子分钟残留舍入的边界),
		// 否则 RangeError —— rounding-not-accepted 整族。
		cands := object.GetPossibleInstantsFor(tz, dt)
		n, ok := object.DisambiguatePossibleInstants(cands, tz, dt, "compatible")
		if !ok {
			return z, tgRange("Invalid relativeTo string: " + s), true
		}
		actualOff := object.TimeZoneOffsetSecondsAt(tz, n)
		if int32(*f.OffsetSeconds) != actualOff {
			return z, tgRange("UTC offset does not match the time zone: " + s), true
		}
		ns = n
	} else {
		cands := object.GetPossibleInstantsFor(tz, dt)
		n, ok := object.DisambiguatePossibleInstants(cands, tz, dt, "compatible")
		if !ok {
			return z, tgRange("Invalid relativeTo string: " + s), true
		}
		ns = n
	}
	if !object.IsValidEpochNanos(ns) {
		return z, tgRange("ZonedDateTime is outside of supported range"), true
	}
	wall := object.GetISOPartsFromEpoch(tz, ns)
	return temporalRelativeTo{Wall: wall, TZ: tz, Nanos: ns}, nil, true
}

// relativeFromBag 把属性包解析成相对起点。
// 含 timeZone 字段 → zoned 形态; 否则 plain 形态 (需 year/month/day)。
func relativeFromBag(o propBag) (temporalRelativeTo, object.Value, bool) {
	var z temporalRelativeTo
	// 用 readZDTFieldsRaw 一次性按字母序读出全部字段 (含 timeZone/offset)。
	raw, _, _, rerr := readZDTFieldsRaw(o)
	if rerr != nil {
		return z, rerr, true
	}
	if !raw.hasYear || (!raw.hasMonth && !raw.hasMonthCode) || !raw.hasDay {
		return z, tgType("relativeTo must have year, month, and day properties"), true
	}
	dt, ferr := raw.regulate("constrain")
	if ferr != nil {
		return z, ferr, true
	}
	if !raw.hasTZ {
		if !object.ISODateTimeInRange(dt) {
			return z, tgRange("PlainDateTime is outside of supported range"), true
		}
		return temporalRelativeTo{Wall: dt}, nil, true
	}
	tz := raw.timeZone
	if id, ok := canonicalTimeZoneID(tz); ok {
		tz = id
	} else {
		return z, tgRange("Invalid time zone: " + tz), true
	}
	cands := object.GetPossibleInstantsFor(tz, dt)
	ns, ok := object.DisambiguatePossibleInstants(cands, tz, dt, "compatible")
	if !ok {
		return z, tgRange("Invalid wall-clock time"), true
	}
	if !object.IsValidEpochNanos(ns) {
		return z, tgRange("ZonedDateTime is outside of supported range"), true
	}
	return temporalRelativeTo{Wall: object.GetISOPartsFromEpoch(tz, ns), TZ: tz, Nanos: ns}, nil, true
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

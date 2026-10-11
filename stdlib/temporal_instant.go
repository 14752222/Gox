package stdlib

import (
	"math"
	"math/big"

	"github.com/14752222/Gox/object"
)

// 本文件实现 Temporal.Instant 及其原型。
//
// Instant 是 Temporal 中最简单的类型: 一个精确时间点，不含时区与日历。
// 它的 until/since 也最简单——差值就是纳秒数，不需要 relativeTo，
// 因为 Instant 本来就不涉及"一个月有多长"这种问题。

// instantUnits 是 Instant.until/since/round 允许的粒度。
// 上限是 hour 而非 day: Instant 没有日历概念，"几天"必须相对于某个
// 时区才有意义，规范因此禁止在 Instant 上使用 day 及以上单位。
var instantUnits = []string{"hour", "minute", "second", "millisecond", "microsecond", "nanosecond"}

func setupTemporalInstant(env *object.Object) {
	proto := object.NewObject()
	object.SetTemporalProto(object.TEMPORAL_INSTANT_OBJ, proto)

	// ===== getter =====
	tgGetter(proto, "epochMilliseconds", func(this object.Value) object.Value {
		t, ok := asInstant(this)
		if !ok {
			return tgTypeBadThis("Instant")
		}
		return object.NewNumber(float64(t.EpochMilliseconds()))
	})

	// epochNanoseconds 的规范类型是 BigInt——纳秒 epoch 约 1.7e18，
	// 超出 double 的安全整数范围，用 Number 表示会静默丢精度。
	tgGetter(proto, "epochNanoseconds", func(this object.Value) object.Value {
		t, ok := asInstant(this)
		if !ok {
			return tgTypeBadThis("Instant")
		}
		return object.NewBigInt(t.Nanos())
	})

	// ===== 运算 =====
	tgMethod(proto, "add", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asInstant(this)
		if !ok {
			return tgTypeBadThis("Instant")
		}
		if len(args) == 0 {
			return tgType("Instant.prototype.add requires a Duration")
		}
		d, derr := toDurationE(args[0])
		if derr != nil {
			return derr
		}
		if serr := checkDurationUniformSign(d); serr != nil {
			return serr
		}
		if d.HasCalendarUnits() {
			return tgRange("Instant.add: duration with calendar units requires a relativeTo")
		}
		ns := new(big.Int).Add(t.Nanos(), d.TimeTotalNanos())
		if !object.IsValidEpochNanos(ns) {
			return tgRange("Instant is outside of supported range")
		}
		return object.NewTemporalInstant(ns)
	})

	tgMethod(proto, "subtract", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asInstant(this)
		if !ok {
			return tgTypeBadThis("Instant")
		}
		if len(args) == 0 {
			return tgType("Instant.prototype.subtract requires a Duration")
		}
		d, derr := toDurationE(args[0])
		if derr != nil {
			return derr
		}
		if serr := checkDurationUniformSign(d); serr != nil {
			return serr
		}
		if d.HasCalendarUnits() {
			return tgRange("Instant.subtract: duration with calendar units requires a relativeTo")
		}
		ns := new(big.Int).Sub(t.Nanos(), d.TimeTotalNanos())
		if !object.IsValidEpochNanos(ns) {
			return tgRange("Instant is outside of supported range")
		}
		return object.NewTemporalInstant(ns)
	})

	// ===== 差值 =====
	tgMethod(proto, "until", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asInstant(this)
		if !ok {
			return tgTypeBadThis("Instant")
		}
		return instantDiffFrom(t, args, false)
	})

	tgMethod(proto, "since", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asInstant(this)
		if !ok {
			return tgTypeBadThis("Instant")
		}
		return instantDiffFrom(t, args, true)
	})

	// ===== 取整 =====
	tgMethod(proto, "round", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asInstant(this)
		if !ok {
			return tgTypeBadThis("Instant")
		}
		if len(args) == 0 {
			return tgType("Instant.prototype.round requires an argument")
		}
		var opts object.Value
		if len(args) > 0 {
			opts = args[0]
		}
		opts, roe := roundOptionsOf(opts)
		if roe != nil {
			return roe
		}
		smallest, uerr := getSmallestUnit(opts, instantUnits)
		if uerr != nil {
			return uerr
		}
		if smallest == "auto" {
			return tgRange("smallestUnit cannot be auto")
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
		return roundInstantTo(t.Nanos(), smallest, inc, mode)
	})

	// ===== 比较 =====
	tgMethod(proto, "equals", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asInstant(this)
		if !ok {
			return tgTypeBadThis("Instant")
		}
		if len(args) == 0 {
			return tgType("equals requires an argument")
		}
		o, cerr := toInstantValue(args[0])
		if cerr != nil {
			return cerr
		}
		return object.NewBoolean(t.Nanos().Cmp(o.Nanos()) == 0)
	})

	// ===== 转换 =====
	tgMethod(proto, "toZonedDateTimeISO", func(this object.Value, args ...object.Value) object.Value {
		t, ok := asInstant(this)
		if !ok {
			return tgTypeBadThis("Instant")
		}
		if len(args) == 0 {
			return tgType("toZonedDateTimeISO requires a time zone")
		}
		// ZonedDateTime 的 Instant.only 分支: Instant → 新纪元纳秒不变，
		// 时区只影响墙钟字段的呈现。
		tz, tzTzErr := toTimeZoneIDE(args[0])
		if tzTzErr != nil {
			return tzTzErr
		}
		return object.NewTemporalZonedDateTime(t.Nanos(), tz, object.DefaultCalendarID)
	})

	tgToStringMethod(proto, "Instant", func(this object.Value, args ...object.Value) (string, object.Value) {
		t, ok := asInstant(this)
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
		tz := "UTC"
		if tzv := optValue(opts, "timeZone"); tzv != nil {
			id, tzErr := toTimeZoneIDE(tzv)
			if tzErr != nil {
				return "", tzErr
			}
			tz = id
		}
		ns := t.Nanos()
		if st.smallestUnit != "" {
			ns = roundNanosToUnit(ns, st.smallestUnit, 1, st.mode)
		}
		off := object.TimeZoneOffsetSecondsAt(tz, ns)
		dt := object.AddOffsetToISODateTime(object.EpochNanosToISODateTime(ns), off)
		prec := st.precision
		if st.smallestUnit != "" {
			prec = precisionForSmallest(st.smallestUnit)
		}
		return object.FormatISODate(dt.Date) + "T" + object.FormatISOTime(dt.Time, prec) + object.FormatOffsetSeconds(off), nil
	})

	tgValueOf(proto)

	// ===== 构造器 =====
	ctor := object.NewBuiltin("Instant", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return tgType("Temporal.Instant requires an epochNanoseconds argument")
		}
		bn, ok := args[0].(*object.BigInt)
		if !ok {
			return tgType("Temporal.Instant: epochNanoseconds must be a BigInt")
		}
		ns := bn.Value
		if !object.IsValidEpochNanos(ns) {
			return tgRange("Invalid epochNanoseconds")
		}
		return object.NewTemporalInstant(ns)
	})
	ctor.SetProperty("name", object.NewString("Instant"))
	ctor.SetProperty("prototype", proto)
	proto.SetBuiltinProperty("constructor", ctor)

	tgStatic(ctor, "from", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return tgType("Temporal.Instant.from requires an argument")
		}
		if inst, ok := asInstant(args[0]); ok {
			return inst
		}
		s, ok := argString(args, 0)
		if !ok {
			return tgType("Temporal.Instant.from: argument must be a string or Instant")
		}
		f, ok := object.ParseTemporalInstantString(s)
		if !ok {
			return tgRange("Invalid Instant string: " + s)
		}
		if f.Calendar != "" && f.Calendar != object.DefaultCalendarID {
			return tgRange("Unsupported calendar: " + f.Calendar)
		}
		dt := f.ISODateTime()
		ns := dt.EpochNanos()
		if f.OffsetSeconds != nil {
			offset := int64(*f.OffsetSeconds) * object.NanosPerSecond
			ns = new(big.Int).Sub(ns, big.NewInt(offset))
		}
		if !object.IsValidEpochNanos(ns) {
			return tgRange("Instant is outside of supported range")
		}
		return object.NewTemporalInstant(ns)
	})

	tgStatic(ctor, "fromEpochMilliseconds", func(args ...object.Value) object.Value {
		ms, ok := argFloat(args, 0)
		if !ok || math.IsNaN(ms) || math.IsInf(ms, 0) {
			return tgRange("Invalid epochMilliseconds")
		}
		if ms != float64(int64(ms)) {
			return tgRange("epochMilliseconds must be an integer")
		}
		ns := new(big.Int).Mul(big.NewInt(int64(ms)), big.NewInt(object.NanosPerMillisecond))
		if !object.IsValidEpochNanos(ns) {
			return tgRange("Instant is outside of supported range")
		}
		return object.NewTemporalInstant(ns)
	})

	tgStatic(ctor, "fromEpochNanoseconds", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return tgType("fromEpochNanoseconds requires a BigInt")
		}
		bn, ok := args[0].(*object.BigInt)
		if !ok {
			return tgType("fromEpochNanoseconds requires a BigInt")
		}
		if !object.IsValidEpochNanos(bn.Value) {
			return tgRange("Instant is outside of supported range")
		}
		return object.NewTemporalInstant(bn.Value)
	})

	tgStatic(ctor, "compare", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return tgType("compare requires two Instants")
		}
		a, ea := toInstantValue(args[0])
		if ea != nil {
			return ea
		}
		b, eb := toInstantValue(args[1])
		if eb != nil {
			return eb
		}
		return object.NewNumber(float64(a.Nanos().Cmp(b.Nanos())))
	})

	setToStringTag(proto, "Temporal.Instant")
	env.SetBuiltinProperty("Instant", ctor)
}

// instantDiffFrom 计算 Instant 之间 (或两个 Instant) 的差值 Duration。
// since=true 时方向反转。
func instantDiffFrom(this *object.TemporalInstant, args []object.Value, since bool) object.Value {
	if len(args) == 0 {
		return tgType("missing argument")
	}
	other, cerr := toInstantValue(args[0])
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
	largest, uerr := getLargestUnit(opts, instantUnits)
	if uerr != nil {
		return uerr
	}
	smallest, uerr := getSmallestUnit(opts, instantUnits)
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

	// 差的符号由方向决定: until 取 other - this，since 取 this - other。
	diff := new(big.Int).Sub(other.Nanos(), this.Nanos())
	if since {
		diff.Neg(diff)
	}

	// 先按 smallestUnit 取整，再按 largestUnit 包装成 Duration 字段。
	su := smallest
	if su == "auto" {
		su = "nanosecond"
	}
	if largest == "auto" {
		// Instant.until/since 的默认最大单位是秒，且不得小于 smallestUnit
		largest = resolveAutoLargest(su, "second")
	}
	if !validRoundingIncrement(su, inc) {
		return tgRange("Invalid roundingIncrement for " + su)
	}
	// unitRank 越大表示单位越小 (nanosecond 序号最大)，因此合法的区间是
	// rank(smallest) >= rank(largest)。
	if unitRank(su) < unitRank(largest) {
		return tgRange("smallestUnit is larger than largestUnit")
	}
	rounded := roundNanosToUnit(diff, su, inc, mode)
	return nanosToDurationForInstant(rounded, largest)
}

// roundInstantTo 把 Instant 的纳秒值按 smallestUnit 与 increment 取整。
func roundInstantTo(ns *big.Int, smallestUnit string, inc int64, mode string) object.Value {
	n, ok := object.UnitToNanos(smallestUnit)
	if !ok {
		return tgRange("Invalid smallestUnit")
	}
	increment := new(big.Int).Mul(big.NewInt(n), big.NewInt(inc))
	rounded := object.RoundNanos(ns, increment, mode)
	if !object.IsValidEpochNanos(rounded) {
		return tgRange("Result is outside of supported range")
	}
	return object.NewTemporalInstant(rounded)
}

// roundNanosToUnit 按目标单位的倍数对纳秒做舍入。
func roundNanosToUnit(ns *big.Int, unit string, inc int64, mode string) *big.Int {
	n, ok := object.UnitToNanos(unit)
	if !ok {
		return ns
	}
	increment := new(big.Int).Mul(big.NewInt(n), big.NewInt(inc))
	return object.RoundNanos(ns, increment, mode)
}

// nanosToDurationForInstant 把纳秒差值包装成以 largestUnit 为最大单位的 Duration。
// Instant 的差值没有日历成分，因此只填充largestUnit 这一级字段。
func nanosToDurationForInstant(ns *big.Int, largestUnit string) *object.TemporalDuration {
	factor, ok := object.UnitToNanos(largestUnit)
	if !ok {
		factor = 1
	}
	// 纳秒差值必须换算到目标单位: 90e9 纳秒在 second 粒度下是 90，
	// 直接塞进 seconds 字段会得到 PT90000000000S 这种荒谬结果。
	v := new(big.Int).Quo(ns, big.NewInt(factor)).Int64()
	switch largestUnit {
	case "hour":
		return object.NewTemporalDuration(0, 0, 0, 0, v, 0, 0, 0, 0, 0)
	case "minute":
		return object.NewTemporalDuration(0, 0, 0, 0, 0, v, 0, 0, 0, 0)
	case "second":
		return object.NewTemporalDuration(0, 0, 0, 0, 0, 0, v, 0, 0, 0)
	case "millisecond":
		return object.NewTemporalDuration(0, 0, 0, 0, 0, 0, 0, v, 0, 0)
	case "microsecond":
		return object.NewTemporalDuration(0, 0, 0, 0, 0, 0, 0, 0, v, 0)
	default:
		return object.NewTemporalDuration(0, 0, 0, 0, 0, 0, 0, 0, 0, v)
	}
}

// setToStringTag 在内建原型/命名空间对象上设置 Symbol.toStringTag，使
// Object.prototype.toString 输出 "[object <tag>]"。描述符为规范的
// { writable:false, enumerable:false, configurable:true } —— 可被
// `delete X.prototype[Symbol.toStringTag]` 删除并回落 builtinTag。
func setToStringTag(proto *object.Object, tag string) {
	proto.SetBuiltinSymbolProperty(object.GetGlobalSymbol("Symbol.toStringTag"), object.NewString(tag))
}

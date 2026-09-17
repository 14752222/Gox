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
		d, ok := toDurationValue(args[0])
		if !ok {
			return tgType("Instant.prototype.add: invalid duration")
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
		d, ok := toDurationValue(args[0])
		if !ok {
			return tgType("Instant.prototype.subtract: invalid duration")
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
		var opts object.Value
		if len(args) > 0 {
			opts = args[0]
		}
		smallest, ok := getSmallestUnit(opts, instantUnits)
		if !ok {
			return tgRange("Invalid smallestUnit for Instant.round")
		}
		if smallest == "auto" {
			return tgRange("smallestUnit cannot be auto")
		}
		inc, ok := getRoundingIncrement(opts)
		if !ok {
			return tgRange("Invalid roundingIncrement")
		}
		mode := getRoundingMode(opts)
		if mode == "invalid" {
			return tgRange("Invalid roundingMode")
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
			return object.NewBoolean(false)
		}
		o, ok := asInstant(args[0])
		if !ok {
			return object.NewBoolean(false)
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
		tz, ok := toTimeZoneID(args[0])
		if !ok {
			return tgRange("Invalid time zone")
		}
		return object.NewTemporalZonedDateTime(t.Nanos(), tz, object.DefaultCalendarID)
	})

	tgToStringMethod(proto, func(this object.Value) string {
		t, ok := asInstant(this)
		if !ok {
			return ""
		}
		return t.ToString()
	})

	tgMethod(proto, "valueOf", func(this object.Value, args ...object.Value) object.Value {
		return this
	})

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
	proto.SetProperty("constructor", ctor)

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
		a, ok := asInstant(args[0])
		if !ok {
			return tgType("compare: first argument is not an Instant")
		}
		b, ok := asInstant(args[1])
		if !ok {
			return tgType("compare: second argument is not an Instant")
		}
		return object.NewNumber(float64(a.Nanos().Cmp(b.Nanos())))
	})

	setToStringTag(proto, "Temporal.Instant")
	env.SetProperty("Instant", ctor)
}

// instantDiffFrom 计算 Instant 之间 (或两个 Instant) 的差值 Duration。
// since=true 时方向反转。
func instantDiffFrom(this *object.TemporalInstant, args []object.Value, since bool) object.Value {
	if len(args) == 0 {
		return tgType("missing argument")
	}
	other, ok := asInstant(args[0])
	if !ok {
		return tgType("argument is not an Instant")
	}
	var opts object.Value
	if len(args) > 1 {
		opts = args[1]
	}
	largest, ok := getLargestUnit(opts, instantUnits)
	if !ok {
		return tgRange("Invalid largestUnit for Instant")
	}
	smallest, ok := getSmallestUnit(opts, instantUnits)
	if !ok {
		return tgRange("Invalid smallestUnit for Instant")
	}
	inc, ok := getRoundingIncrement(opts)
	if !ok {
		return tgRange("Invalid roundingIncrement")
	}
	mode := getRoundingMode(opts)
	if mode == "invalid" {
		return tgRange("Invalid roundingMode")
	}

	// 差的符号由方向决定: until 取 other - this，since 取 this - other。
	diff := new(big.Int).Sub(other.Nanos(), this.Nanos())
	if since {
		diff.Neg(diff)
	}

	// 先按 smallestUnit 取整，再按 largestUnit 包装成 Duration 字段。
	if largest == "auto" {
		largest = "second" // Instant.until/since 的默认最大单位是秒
	}
	su := smallest
	if su == "auto" {
		su = "nanosecond"
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

// setToStringTag 在原型上设置 Symbol.toStringTag，使 Object.prototype.toString
// 输出 "[object Temporal.XXX]"。
func setToStringTag(proto *object.Object, tag string) {
	proto.SetSymbolProperty(object.GetGlobalSymbol("Symbol.toStringTag"), object.NewString(tag))
}

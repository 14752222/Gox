package stdlib

import (
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// 本文件是 Temporal 的注册入口与 Temporal.Now 时间源。

// setupTemporal 注册全局 Temporal 命名空间对象。
//
// Temporal 是一个"命名空间对象"而非函数，所有类型都是它的属性。
// 调用后通过 Temporal.Instant.from(...) / Temporal.PlainDate.from(...)
// 这类形式访问。
func setupTemporal(env *runtime.Environment) {
	// T3-5 冷启动惰性化: Temporal 是启动剖析里最大的单点 (CPU 剖面
	// setupTemporal ≈ 12% 的 EvalFileVM 首次调用)，而绝大多数脚本从不
	// 触碰它。名字立即声明占位 (枚举/typeof/hasOwnProperty 可见)，整棵
	// 对象树推迟到首次 Get。物化时需自行补做 SetupGlobals 收尾对惰性根
	// 跳过的部分: builtinLengths 里 Temporal.* 的 length 补齐。
	env.DeclareLazy("Temporal", func() object.Value {
		temporal := buildTemporalNamespace()
		applyBuiltinLengthsPrefixes(env, []string{"Temporal"})
		return temporal
	})
}

// buildTemporalNamespace 构造 Temporal 命名空间对象树 (惰性工厂主体)。
func buildTemporalNamespace() *object.Object {
	temporal := object.NewObject()
	setNamespaceProto(temporal)

	setupTemporalInstant(temporal)
	setupTemporalDuration(temporal)
	setupTemporalPlainDateTime(temporal)
	setupTemporalPlainDate(temporal)
	setupTemporalPlainTime(temporal)
	setupTemporalZonedDateTime(temporal)
	setupTemporalTimeZone(temporal)
	setupTemporalCalendar(temporal)
	setupTemporalPlainYearMonth(temporal)
	setupTemporalPlainMonthDay(temporal)

	setupTemporalNow(temporal)

	tgApplyTemporalLengths(temporal)

	setToStringTag(temporal, "Temporal")
	return temporal
}

// setupTemporalNow 注册 Temporal.Now 命名空间。
//
// Temporal.Now 是唯一读取系统时钟的入口。所有带 ISO 后缀的方法
// (plainDateISO 等) 都固定使用 iso8601 日历，因此不需要 options.calendar。
func setupTemporalNow(temporal *object.Object) {
	now := object.NewObject()
	setNamespaceProto(now)

	// hostTimeZone 返回宿主系统时区标识符，缺失时退化为 UTC。
	hostTimeZone := func() string {
		name, _ := time.Now().Zone()
		if name == "" {
			return "UTC"
		}
		// Zone() 给出的是缩写名 (如 CST)，未必是可用的 IANA 标识符;
		// 只有能加载成功的才使用，否则回退 UTC。
		if object.IsValidTimeZoneID(name) {
			return name
		}
		return "UTC"
	}
	_ = hostTimeZone

	tgFn(now, "instant", func(args ...object.Value) object.Value {
		return object.NewTemporalInstant(object.SystemInstantNanos())
	})

	tgFn(now, "timeZoneId", func(args ...object.Value) object.Value {
		return object.NewString(hostTimeZone())
	})

	tgFn(now, "zonedDateTimeISO", func(args ...object.Value) object.Value {
		tz := hostTimeZone()
		if len(args) > 0 {
			id, idTzErr := toTimeZoneIDE(args[0])
			if idTzErr != nil {
				return idTzErr
			}
			tz = id
		}
		return object.NewTemporalZonedDateTime(object.SystemInstantNanos(), tz, object.DefaultCalendarID)
	})

	tgFn(now, "plainDateTimeISO", func(args ...object.Value) object.Value {
		tz := hostTimeZone()
		if len(args) > 0 {
			id, idTzErr := toTimeZoneIDE(args[0])
			if idTzErr != nil {
				return idTzErr
			}
			tz = id
		}
		return object.NewTemporalPlainDateTime(object.SystemDateTimeInZone(tz), object.DefaultCalendarID)
	})

	tgFn(now, "plainDateISO", func(args ...object.Value) object.Value {
		tz := hostTimeZone()
		if len(args) > 0 {
			id, idTzErr := toTimeZoneIDE(args[0])
			if idTzErr != nil {
				return idTzErr
			}
			tz = id
		}
		dt := object.SystemDateTimeInZone(tz)
		return object.NewTemporalPlainDate(dt.Date, object.DefaultCalendarID)
	})

	tgFn(now, "plainTimeISO", func(args ...object.Value) object.Value {
		tz := hostTimeZone()
		if len(args) > 0 {
			id, idTzErr := toTimeZoneIDE(args[0])
			if idTzErr != nil {
				return idTzErr
			}
			tz = id
		}
		dt := object.SystemDateTimeInZone(tz)
		return object.NewTemporalPlainTime(dt.Time)
	})

	setToStringTag(now, "Temporal.Now")
	temporal.SetBuiltinProperty("Now", now)
}

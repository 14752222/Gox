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
	temporal := object.NewObject()

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

	setToStringTag(temporal, "Temporal")
	env.Declare("Temporal", temporal, false)
}

// setupTemporalNow 注册 Temporal.Now 命名空间。
//
// Temporal.Now 是唯一读取系统时钟的入口。所有带 ISO 后缀的方法
// (plainDateISO 等) 都固定使用 iso8601 日历，因此不需要 options.calendar。
func setupTemporalNow(temporal *object.Object) {
	now := object.NewObject()

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
			id, ok := toTimeZoneID(args[0])
			if !ok {
				return tgRange("Invalid time zone")
			}
			tz = id
		}
		return object.NewTemporalZonedDateTime(object.SystemInstantNanos(), tz, object.DefaultCalendarID)
	})

	tgFn(now, "plainDateTimeISO", func(args ...object.Value) object.Value {
		tz := hostTimeZone()
		if len(args) > 0 {
			id, ok := toTimeZoneID(args[0])
			if !ok {
				return tgRange("Invalid time zone")
			}
			tz = id
		}
		return object.NewTemporalPlainDateTime(object.SystemDateTimeInZone(tz), object.DefaultCalendarID)
	})

	tgFn(now, "plainDateISO", func(args ...object.Value) object.Value {
		tz := hostTimeZone()
		if len(args) > 0 {
			id, ok := toTimeZoneID(args[0])
			if !ok {
				return tgRange("Invalid time zone")
			}
			tz = id
		}
		dt := object.SystemDateTimeInZone(tz)
		return object.NewTemporalPlainDate(dt.Date, object.DefaultCalendarID)
	})

	tgFn(now, "plainTimeISO", func(args ...object.Value) object.Value {
		tz := hostTimeZone()
		if len(args) > 0 {
			id, ok := toTimeZoneID(args[0])
			if !ok {
				return tgRange("Invalid time zone")
			}
			tz = id
		}
		dt := object.SystemDateTimeInZone(tz)
		return object.NewTemporalPlainTime(dt.Time)
	})

	setToStringTag(now, "Temporal.Now")
	temporal.SetProperty("Now", now)
}

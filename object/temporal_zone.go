package object

// 本文件实现 Temporal 的时区子系统。
//
// 与自研 Date 不同，Temporal 的时区能力直接复用 Go 标准库的 IANA 时区数据库
// (time.LoadLocation)，因此夏令时规则、历史上的偏移变更都不需要自己维护
// 数据表——这恰恰是 Date 最容易出错的部分。
//
// 内部表示注意: Go 的 time.Time 虽以 int64 纳秒存储，但官方保证的可靠范围
// 仅 1678–2262 年。Temporal 的合法范围远大于此，对超出部分的时间戳，
// Location.Zone() 会退化为"该时区最近/最早的已知规则"，这里按此近似处理
// 而不报错——真实世界的时区规则本身也不存在于那个尺度上。

import (
	"math/big"
	"time"
)

// ===== 时区标识符分类 =====

// IsOffsetOnlyTimeZone 判断标识符是否形如 "+09:00" 的纯偏移时区。
// 纯偏移时区不进 tzdb 查询，也不带 [ ] 注解输出。
func IsOffsetOnlyTimeZone(id string) bool {
	if id == "" {
		return false
	}
	c := id[0]
	if c != '+' && c != '-' {
		return false
	}
	_, ok := ParseTimeZoneOffsetString(id)
	return ok
}

// loadLocation 加载 tzdb 中的 Location。"UTC" 直接返回 time.UTC。
func loadLocation(id string) (*time.Location, bool) {
	switch id {
	case "UTC", "GMT":
		return time.UTC, true
	}
	loc, err := time.LoadLocation(id)
	if err != nil {
		return nil, false
	}
	return loc, true
}

// nanosToUnix 把 epoch 纳秒拆成 (秒, 纳秒) 供 time.Unix 使用。
// 超出 Unix 时间戳可靠范围时截断到边界值，避免 time.Time 内部溢出。
func nanosToUnix(ns *big.Int) (int64, int64) {
	const maxUnixSec = 1<<31 - 1 // time.Time 可安全格式化的上界附近
	secBig := new(big.Int).Quo(ns, big.NewInt(NanosPerSecond))
	remBig := new(big.Int).Rem(ns, big.NewInt(NanosPerSecond))
	sec := secBig.Int64()
	if secBig.Cmp(big.NewInt(maxUnixSec)) > 0 {
		sec = maxUnixSec
		remBig = big.NewInt(0)
	}
	if secBig.Cmp(big.NewInt(-maxUnixSec)) < 0 {
		sec = -maxUnixSec
		remBig = big.NewInt(0)
	}
	return sec, remBig.Int64()
}

// TimeZoneOffsetSecondsAt 返回某个时刻在指定时区的 UTC 偏移秒数。
//
// epochNanos 是精确时间点。对于纯偏移时区直接返回其固定偏移。
func TimeZoneOffsetSecondsAt(tz string, epochNanos *big.Int) int32 {
	if off, ok := ParseTimeZoneOffsetString(tz); ok {
		return off
	}
	loc, ok := loadLocation(tz)
	if !ok {
		return 0
	}
	sec, nsec := nanosToUnix(epochNanos)
	_, off := time.Unix(sec, nsec).In(loc).Zone()
	return int32(off)
}

// GetPossibleInstantsFor 返回某个时区下、某个墙钟时间对应的所有可能精确时刻。
//
// 返回值长度的含义:
//   - 0: 该墙钟时间不存在 (春季夏令时跳变的空洞)
//   - 1: 唯一确定
//   - 2: 存在歧义 (秋季夏令时回拨导致的重复小时)
//
// 这是 Temporal 相对 Date 最重要的正确性保证之一: Date 在这两种情形下会
// 静默给出错误结果，而 Temporal 把选择权交给 disambiguation 选项。
func GetPossibleInstantsFor(tz string, dt ISODateTime) []*big.Int {
	utcNs := dt.EpochNanos()
	if !IsValidEpochNanos(utcNs) {
		return nil
	}
	oneDay := big.NewInt(NanosPerDay)

	before := new(big.Int).Sub(utcNs, oneDay)
	after := new(big.Int).Add(utcNs, oneDay)
	offsetBefore := int64(TimeZoneOffsetSecondsAt(tz, before)) * NanosPerSecond
	offsetAfter := int64(TimeZoneOffsetSecondsAt(tz, after)) * NanosPerSecond

	candidate1 := new(big.Int).Sub(utcNs, big.NewInt(offsetBefore))
	var out []*big.Int
	if !IsValidEpochNanos(candidate1) {
		return out
	}
	if int64(TimeZoneOffsetSecondsAt(tz, candidate1))*NanosPerSecond == offsetBefore {
		out = append(out, candidate1)
	}
	if offsetAfter != offsetBefore {
		candidate2 := new(big.Int).Sub(utcNs, big.NewInt(offsetAfter))
		if IsValidEpochNanos(candidate2) &&
			int64(TimeZoneOffsetSecondsAt(tz, candidate2))*NanosPerSecond == offsetAfter {
			out = append(out, candidate2)
		}
	}
	return out
}

// DisambiguatePossibleInstants 按 disambiguation 策略从候选时刻中选取一个。
//
//   - compatible: 取后者 (与旧版 Date 的语义最接近: 回拨后取第二次出现的时间)
//   - earlier:    取较早者
//   - later:      取较晚者
//   - reject:     存在 0 或 2 个候选时失败
func DisambiguatePossibleInstants(candidates []*big.Int, tz string, dt ISODateTime, disambiguation string) (*big.Int, bool) {
	switch len(candidates) {
	case 1:
		return candidates[0], true
	case 0:
		// 空洞 (spring-forward): 该墙钟时间根本不存在。
		//
		// 除 reject 外都不能直接报错。compatible 的语义是"沿用旧 Date 的行为":
		// 把不存在的时刻往后推到第一个有效时刻，即 02:30 → 03:30 EDT。
		// earlier 则反向折回，即 02:30 → 01:30 EST。
		if disambiguation == "reject" {
			return nil, false
		}
		utcNs := dt.EpochNanos()
		oneDay := big.NewInt(NanosPerDay)
		offsetBefore := int64(TimeZoneOffsetSecondsAt(tz, new(big.Int).Sub(utcNs, oneDay))) * NanosPerSecond
		offsetAfter := int64(TimeZoneOffsetSecondsAt(tz, new(big.Int).Add(utcNs, oneDay))) * NanosPerSecond
		offset := offsetBefore
		if disambiguation == "earlier" {
			offset = offsetAfter
		}
		ns := new(big.Int).Sub(utcNs, big.NewInt(offset))
		if !IsValidEpochNanos(ns) {
			return nil, false
		}
		return ns, true
	}
	// 两个候选 (fall-back 重复小时)
	if disambiguation == "reject" {
		return nil, false
	}
	earlier, later := candidates[0], candidates[len(candidates)-1]
	if earlier.Cmp(later) > 0 {
		earlier, later = later, earlier
	}
	if disambiguation == "earlier" {
		return earlier, true
	}
	// compatible 与 later 都取较晚者
	return later, true
}

// GetISOPartsFromEpoch 计算某个精确时刻在指定时区下的墙钟字段。
func GetISOPartsFromEpoch(tz string, epochNanos *big.Int) ISODateTime {
	off := TimeZoneOffsetSecondsAt(tz, epochNanos)
	return AddOffsetToISODateTime(EpochNanosToISODateTime(epochNanos), off)
}

// GetOffsetStringFor 返回指定时刻在该时区的 ±HH:MM 偏移串。
func GetOffsetStringFor(tz string, epochNanos *big.Int) string {
	return formatUTCOffset(TimeZoneOffsetSecondsAt(tz, epochNanos))
}

// ===== UTC 偏移 ↔ ISO 字段 =====

// SubtractOffsetFromISODateTime 把当地墙钟字段减去偏移得到 UTC 字段。
func SubtractOffsetFromISODateTime(dt ISODateTime, offsetSec int32) ISODateTime {
	return AddOffsetToISODateTime(dt, -offsetSec)
}

// ParseTimeZoneName 校验并返回时区标识符。
// 非法标识符返回 ok=false，由调用方抛 RangeError。
func ParseTimeZoneName(id string) (string, bool) {
	if id == "" {
		return "", false
	}
	if IsValidTimeZoneID(id) {
		return id, true
	}
	return "", false
}

// ===== 当前时间 =====

// SystemInstantNanos 返回当前的 epoch 纳秒与纳秒部分。
// Temporal.Now.instant() 等以此为时间源。
func SystemInstantNanos() *big.Int {
	ns := time.Now().UnixNano()
	return big.NewInt(ns)
}

// SystemUTCEpochNanos 是取其 nilary 形式的便捷包装。
func SystemUTCEpochNanos() *big.Int { return SystemInstantNanos() }

// SystemDateTimeInZone 返回当前时刻在指定时区下的墙钟字段。
func SystemDateTimeInZone(tz string) ISODateTime {
	ns := SystemInstantNanos()
	return GetISOPartsFromEpoch(tz, ns)
}

// CalendarFieldISO 计算 iso8601 日历下的某个日历字段值。
// 仅支持 iso8601；其余日历系统需要 ICU 级数据表，本运行时不实现。
func CalendarFieldISO(field string, dt ISODateTime) (int32, bool) {
	switch field {
	case "year":
		return dt.Date.Year, true
	case "month":
		return dt.Date.Month, true
	case "monthCode":
		return dt.Date.Month, true
	case "day":
		return dt.Date.Day, true
	case "hour":
		return dt.Time.Hour, true
	case "minute":
		return dt.Time.Minute, true
	case "second":
		return dt.Time.Second, true
	case "millisecond":
		return dt.Time.Millisecond, true
	case "microsecond":
		return dt.Time.Microsecond, true
	case "nanosecond":
		return dt.Time.Nanosecond, true
	case "dayOfWeek":
		return dt.Date.DayOfWeek(), true
	case "dayOfYear":
		return dt.Date.DayOfYear(), true
	case "weekOfYear":
		return dt.Date.WeekOfYear(), true
	case "daysInWeek":
		return 7, true
	case "daysInMonth":
		return DaysInMonth(dt.Date.Year, dt.Date.Month), true
	case "daysInYear":
		return DaysInYear(dt.Date.Year), true
	case "monthsInYear":
		return 12, true
	case "inLeapYear":
		if dt.Date.InLeapYear() {
			return 1, true
		}
		return 0, true
	}
	return 0, false
}

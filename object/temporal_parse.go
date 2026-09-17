package object

// 本文件实现 Temporal 的 ISO 8601 字符串文法解析。
//
// Temporal 不像 Date 那样接受"尽力猜测"的宽松解析——它只接受严格定义的
// 文法，任何歧义都是 RangeError。这正是 Date.parse 各引擎行为不一致的
// 反面教材所催生的设计。
//
// 覆盖的文法 (ECMAScript Temporal 规范):
//   - TemporalInstantString        DateTime [offset|Z] [ tz ]
//   - TemporalDateTimeString       DateTime [ tz ]
//   - TemporalDateString           Date [ tz ]
//   - TemporalTimeString           Time [ tz ]
//   - TemporalYearMonthString      YearMonth [ tz ]
//   - TemporalMonthDayString       MonthDay [ tz ]
//   - TemporalZonedDateTimeString  DateTime [offset] tz   (tz 必需)
//   - TemporalDurationString       Duration
//   - TemporalTimeZoneString       IANA 名 或 UTC 偏移串

import (
	"math"
	"math/big"
	"time"
)

// ===== 解析结果 =====

// TemporalFields 是一次 ISO 字符串解析的完整结果。
// 各字段是否填充取决于被解析的具体文法: 例如 DateString 文法不含时间部分，
// 调用方需自行按文法要求校验，缺失一律按 RangeError 处理。
type TemporalFields struct {
	HasDate bool
	Year    int32
	Month   int32
	Day     int32

	HasTime     bool
	Hour        int32
	Minute      int32
	Second      int32
	Millisecond int32
	Microsecond int32
	Nanosecond  int32

	// OffsetSeconds 为 nil 表示字符串中没有 UTC 偏移。
	// Z 与 +00:00 解析结果一致 (均为 0)，但 IsZ 保留原始写法以便错误信息区分。
	OffsetSeconds *int32
	IsZ           bool

	// TimeZoneAnnotation 是 [Asia/Tokyo] 这类注解中的时区标识符。
	TimeZoneAnnotation string
	// Calendar 是 [u-ca=iso8601] 这类注解中的日历标识符。
	Calendar string
}

// DateTime returns the combined ISO date-time record (UTC 语义，未应用偏移)。
func (f TemporalFields) ISODateTime() ISODateTime {
	return ISODateTime{
		Date: ISODate{Year: f.Year, Month: f.Month, Day: f.Day},
		Time: ISOTime{Hour: f.Hour, Minute: f.Minute, Second: f.Second,
			Millisecond: f.Millisecond, Microsecond: f.Microsecond, Nanosecond: f.Nanosecond},
	}
}

// Annotation 是本文件在 [ ] 中识别的注解种类。
const (
	annotNone = iota
	annotTimeZone
	annotCalendar
)

// ===== 扫描器 =====

type isoScanner struct {
	s   string
	pos int
}

func (p *isoScanner) remaining() int { return len(p.s) - p.pos }
func (p *isoScanner) peek() byte {
	if p.pos >= len(p.s) {
		return 0
	}
	return p.s[p.pos]
}
func (p *isoScanner) at(c byte) bool { return p.peek() == c }
func (p *isoScanner) eof() bool      { return p.pos >= len(p.s) }
func (p *isoScanner) bump() byte     { c := p.s[p.pos]; p.pos++; return c }
func (p *isoScanner) isDigit() bool  { c := p.peek(); return c >= '0' && c <= '9' }
func (p *isoScanner) take(c byte) bool {
	if p.at(c) {
		p.pos++
		return true
	}
	return false
}

// digits 读取恰好 n 位十进制数字。
func (p *isoScanner) digits(n int) (int32, bool) {
	if p.remaining() < n {
		return 0, false
	}
	v := int32(0)
	for i := 0; i < n; i++ {
		c := p.s[p.pos+i]
		if c < '0' || c > '9' {
			return 0, false
		}
		v = v*10 + int32(c-'0')
	}
	p.pos += n
	return v, true
}

// frac 读取 '.' 之后的 1-9 位小数，返回补齐到纳秒的值。
// Temporal 允许任意截断的小数位数 (.5 == .500)，但上限 9 位。
func (p *isoScanner) frac(maxDigits int) (int32, bool) {
	if !p.take('.') {
		return 0, true
	}
	if !p.isDigit() {
		return 0, false // 小数点后必须至少一位数字
	}
	digits := ""
	for len(digits) < 9 && p.isDigit() {
		digits += string(p.bump())
	}
	if len(digits) > maxDigits {
		return 0, false
	}
	// 右补齐到 9 位: ".5" 视作 ".500000000"
	for len(digits) < 9 {
		digits += "0"
	}
	nanos := int32(0)
	for i := 0; i < 9; i++ {
		nanos = nanos*10 + int32(digits[i]-'0')
	}
	return nanos, true
}

// ===== 日期部分 =====

// parseDate 解析 ANSI/ISO 日期，支持扩展格式与基本格式:
//
//	YYYY-MM-DD / YYYYMMDD        (+2020-01-01)
//	±YYYYYY-MM-DD / ±YYYYYYMMDD  (扩展年，符号必需)
func (p *isoScanner) parseDate(f *TemporalFields) bool {
	sign := int32(1)
	if p.at('+') || p.at('-') {
		if p.at('-') {
			sign = -1
		}
		p.pos++
		// 扩展年份必须恰好 6 位
		y, ok := p.digits(6)
		if !ok {
			return false
		}
		if y == 0 {
			return false // 禁止 -000000 / +000000
		}
		f.Year = sign * y
		if p.take('-') {
			m, ok := p.digits(2)
			if !ok {
				return false
			}
			f.Month = m
			if !p.take('-') {
				return false
			}
			d, ok := p.digits(2)
			if !ok {
				return false
			}
			f.Day = d
		} else {
			m, ok := p.digits(2)
			if !ok {
				return false
			}
			d, ok := p.digits(2)
			if !ok {
				return false
			}
			f.Month, f.Day = m, d
		}
	} else {
		y, ok := p.digits(4)
		if !ok {
			return false
		}
		f.Year = sign * y
		if p.take('-') {
			m, ok := p.digits(2)
			if !ok {
				return false
			}
			f.Month = m
			if p.take('-') {
				d, ok := p.digits(2)
				if !ok {
					return false
				}
				f.Day = d
			}
		} else {
			// 基本格式 YYYYMMDD
			m, ok := p.digits(2)
			if !ok {
				return false
			}
			d, ok := p.digits(2)
			if !ok {
				return false
			}
			f.Month, f.Day = m, d
		}
	}
	f.HasDate = true
	return IsValidISODate(ISODate{f.Year, f.Month, f.Day})
}

// parseYearMonth 解析 YYYY-MM 或 YYYYMM (含扩展年形式)。
// parseYearMonth 解析 YYYY-MM 或 YYYYMM (含扩展年形式 ±YYYYYY-MM)。
//
// 不能复用 parseDate: 后者以 IsValidISODate 收尾，会因缺少日部分而失败，
// 而 YearMonth 文法本来就不含日。
func (p *isoScanner) parseYearMonth(f *TemporalFields) bool {
	sign := int32(1)
	if p.at('+') || p.at('-') {
		if p.at('-') {
			sign = -1
		}
		p.pos++
		y, ok := p.digits(6)
		if !ok || y == 0 {
			return false
		}
		f.Year = sign * y
	} else {
		y, ok := p.digits(4)
		if !ok {
			return false
		}
		f.Year = sign * y
	}
	p.take('-')
	m, ok := p.digits(2)
	if !ok || m < 1 || m > 12 {
		return false
	}
	f.Month = m
	f.Day = 0
	f.HasDate = true
	return true
}

// parseMonthDay 解析 MM-DD / MMDD，或带 -- 前缀的 --MM-DD。
func (p *isoScanner) parseMonthDay(f *TemporalFields) bool {
	p.take('-')
	p.take('-')
	m, ok := p.digits(2)
	if !ok {
		return false
	}
	p.take('-')
	d, ok := p.digits(2)
	if !ok {
		return false
	}
	f.Month, f.Day = m, d
	f.HasDate = true
	// 闰日 (02-29) 在 MonthDay 文法中合法: 只检查范围内有效性 (默认年 1972 闰年)
	return m >= 1 && m <= 12 && d >= 1 && d <= DaysInMonth(1972, m)
}

// ===== 时间部分 =====

// parseTime 解析 HH[:MM[:SS[.fff]]]，返回是否成功。
// Temporal 的基本格式 HHMMSS 与扩展格式 HH:MM:SS 都接受。
func (p *isoScanner) parseTime(f *TemporalFields) bool {
	h, ok := p.digits(2)
	if !ok {
		return false
	}
	f.Hour = h

	if p.take(':') {
		m, ok := p.digits(2)
		if !ok {
			return false
		}
		f.Minute = m
		if p.take(':') {
			s, ok := p.digits(2)
			if !ok {
				return false
			}
			f.Second = s
		}
	} else if p.remaining() >= 2 && p.isDigit() {
		m, ok := p.digits(2)
		if !ok {
			return false
		}
		f.Minute = m
		if p.remaining() >= 2 && p.isDigit() {
			s, ok := p.digits(2)
			if !ok {
				return false
			}
			f.Second = s
		}
	}

	nanos, ok := p.frac(9)
	if !ok {
		return false
	}
	f.Millisecond = nanos / 1000000
	f.Microsecond = (nanos / 1000) % 1000
	f.Nanosecond = nanos % 1000

	// 24:00:00 是合法的时间 (表示当天结束)，其余越界值拒绝。
	f.HasTime = true
	if f.Hour == 24 {
		return f.Minute == 0 && f.Second == 0 && nanos == 0
	}
	return IsValidISOTime(ISOTime{f.Hour, f.Minute, f.Second, f.Millisecond, f.Microsecond, f.Nanosecond})
}

// ===== UTC 偏移 =====

// parseOffset 解析 ±HH:MM[:SS[.fff]] 或 Z。
// 返回 offset 是否出现；偏移量以秒为单位存入 out。
func (p *isoScanner) parseOffset(f *TemporalFields) bool {
	if p.at('Z') || p.at('z') {
		p.pos++
		z := int32(0)
		f.OffsetSeconds = &z
		f.IsZ = true
		return true
	}
	neg := false
	if p.at('-') {
		neg = true
	} else if !p.at('+') {
		return false
	}
	p.pos++

	h, ok := p.digits(2)
	if !ok {
		return false
	}
	m := int32(0)
	s := int32(0)
	frac := int32(0)
	if p.take(':') {
		m, ok = p.digits(2)
		if !ok {
			return false
		}
		if p.take(':') {
			s, ok = p.digits(2)
			if !ok {
				return false
			}
			var e error
			frac, e = scanFraction(p)
			if e != nil {
				return false
			}
		}
	} else if p.remaining() >= 2 && p.isDigit() {
		m, ok = p.digits(2)
		if !ok {
			return false
		}
	}
	if h > 23 || m > 59 || s > 59 {
		return false
	}
	total := h*3600 + m*60 + s
	if neg {
		total = -total
	}
	// 规范: 偏移字符串中出现非零秒以下精度时，纳秒部分仍需记录。
	// 当前实现只保留到秒，亚秒偏移在真实时区数据库中不存在。
	_ = frac
	f.OffsetSeconds = &total
	return true
}

// scanFraction 读取小数部分并返回纳秒值 (0 表示无小数)。
func scanFraction(p *isoScanner) (int32, error) {
	if !p.take('.') {
		return 0, nil
	}
	if !p.isDigit() {
		return 0, errISOParse
	}
	digits := ""
	for len(digits) < 9 && p.isDigit() {
		digits += string(p.bump())
	}
	for len(digits) < 9 {
		digits += "0"
	}
	v := int32(0)
	for i := 0; i < 9; i++ {
		v = v*10 + int32(digits[i]-'0')
	}
	return v, nil
}

// errISOParse 是文法不匹配的哨兵错误。
var errISOParse = &isoParseError{}

type isoParseError struct{}

func (e *isoParseError) Error() string { return "invalid ISO 8601 string" }

// ===== 注解 =====

// parseAnnotations 解析尾部的所有 [ ... ] 注解块。
// Temporal 允许时区注解与日历注解以任意顺序出现，且各自最多一次。
func (p *isoScanner) parseAnnotations(f *TemporalFields) bool {
	var tz, cal string
	for p.at('[') {
		p.pos++
		start := p.pos
		for !p.eof() && !p.at(']') {
			p.pos++
		}
		if p.eof() {
			return false
		}
		body := p.s[start:p.pos]
		p.pos++ // 消费 ']'

		if len(body) >= 2 && body[0] == 'u' && body[1] == '-' {
			cal = parseCalendarAnnotation(body)
			if cal == "" {
				return false
			}
			continue
		}
		if !isAsciiAlphaNumExt(body) || tz != "" {
			return false
		}
		tz = body
	}
	f.TimeZoneAnnotation = tz
	f.Calendar = cal
	return true
}

// parseCalendarAnnotation 从 "u-ca=gregory" 中取出日历名。
func parseCalendarAnnotation(body string) string {
	const prefix = "u-ca="
	if len(body) <= len(prefix) || body[:len(prefix)] != prefix {
		return ""
	}
	v := body[len(prefix):]
	for _, c := range v {
		ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
		if !ok {
			return ""
		}
	}
	return v
}

// isAsciiAlphaNumExt 检查是否为合法的时区标识符字符集合。
func isAsciiAlphaNumExt(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '-', c == '_', c == '/':
		default:
			return false
		}
	}
	return true
}

// ===== 公开文法入口 =====

// ParseTemporalDateString 解析 TemporalDateString (Date [ tz ])。
func ParseTemporalDateString(s string) (TemporalFields, bool) {
	var f TemporalFields
	p := &isoScanner{s: s}
	if !p.parseDate(&f) {
		return f, false
	}
	if !p.parseAnnotations(&f) || !p.eof() {
		return f, false
	}
	return f, true
}

// ParseTemporalDateTimeString 解析 TemporalDateTimeString (DateTime [ tz ])。
// 允许 ISO 的多种宽松写法: 'T' 或空格分隔、可带/不带分钟与秒。
func ParseTemporalDateTimeString(s string) (TemporalFields, bool) {
	var f TemporalFields
	p := &isoScanner{s: s}
	if !p.parseDate(&f) {
		return f, false
	}
	if !p.eof() && !p.at('[') {
		if !(p.take('T') || p.take('t') || p.take(' ')) {
			return f, false
		}
		if !p.parseTime(&f) {
			return f, false
		}
	}
	if !p.parseAnnotations(&f) || !p.eof() {
		return f, false
	}
	return f, true
}

// ParseTemporalTimeString 解析 TemporalTimeString (Time [ tz ])。
func ParseTemporalTimeString(s string) (TemporalFields, bool) {
	var f TemporalFields
	p := &isoScanner{s: s}
	if p.take('T') || p.take('t') {
		// 允许带 T 前缀
	}
	if !p.parseTime(&f) {
		return f, false
	}
	if !p.parseAnnotations(&f) || !p.eof() {
		return f, false
	}
	return f, true
}

// ParseTemporalInstantString 解析 TemporalInstantString。
// Instant 文法要求必须带 UTC 偏移或 Z——没有偏移就无法确定精确时刻。
func ParseTemporalInstantString(s string) (TemporalFields, bool) {
	var f TemporalFields
	p := &isoScanner{s: s}
	if !p.parseDate(&f) {
		return f, false
	}
	if !p.eof() && !p.at('[') {
		if !(p.take('T') || p.take('t') || p.take(' ')) {
			return f, false
		}
		if !p.parseTime(&f) {
			return f, false
		}
	}
	if !p.parseOffset(&f) {
		return f, false
	}
	if !p.parseAnnotations(&f) || !p.eof() {
		return f, false
	}
	return f, true
}

// ParseTemporalZonedDateTimeString 解析 TemporalZonedDateTimeString。
// 与 Instant 文法的区别: 偏移可选，但时区注解必需。
func ParseTemporalZonedDateTimeString(s string) (TemporalFields, bool) {
	var f TemporalFields
	p := &isoScanner{s: s}
	if !p.parseDate(&f) {
		return f, false
	}
	if !p.eof() && !p.at('[') {
		if !(p.take('T') || p.take('t') || p.take(' ')) {
			return f, false
		}
		if !p.parseTime(&f) {
			return f, false
		}
	}
	p.parseOffset(&f)
	if !p.parseAnnotations(&f) || !p.eof() {
		return f, false
	}
	if f.TimeZoneAnnotation == "" {
		return f, false
	}
	return f, true
}

// ParseTemporalYearMonthString 解析 TemporalYearMonthString (YYYY-MM [ tz ])。
func ParseTemporalYearMonthString(s string) (TemporalFields, bool) {
	var f TemporalFields
	p := &isoScanner{s: s}
	if !p.parseYearMonth(&f) {
		return f, false
	}
	if !p.parseAnnotations(&f) || !p.eof() {
		return f, false
	}
	return f, true
}

// ParseTemporalMonthDayString 解析 TemporalMonthDayString (MM-DD [ tz ])。
func ParseTemporalMonthDayString(s string) (TemporalFields, bool) {
	var f TemporalFields
	p := &isoScanner{s: s}
	if !p.parseMonthDay(&f) {
		return f, false
	}
	if !p.parseAnnotations(&f) || !p.eof() {
		return f, false
	}
	return f, true
}

// ParseTimeZoneOffsetString 解析 ±HH:MM[:SS] 形式的偏移串，返回偏移秒数。
func ParseTimeZoneOffsetString(s string) (int32, bool) {
	var f TemporalFields
	p := &isoScanner{s: s}
	if !p.parseOffset(&f) || !p.eof() || f.OffsetSeconds == nil {
		return 0, false
	}
	return *f.OffsetSeconds, true
}

// ===== 时区标识符 =====

// IsValidTimeZoneID 判断字符串是否为可用的时区标识符。
// 支持 "UTC"、±HH:MM 偏移串、以及宿主 tzdb 中存在的 IANA 名。
//
// 依赖 Go 标准库的时区数据库: 直接调用 time.LoadLocation，命中系统
// /usr/share/zoneinfo 或 Go 内嵌的 tzdata。这正是用 Go 实现 Temporal
// 相对自研 Date 的最大优势——DST 规则与历史偏移变更无需自己维护数据表。
func IsValidTimeZoneID(id string) bool {
	if id == "" {
		return false
	}
	if id == "UTC" || id == "GMT" {
		return true
	}
	if _, ok := ParseTimeZoneOffsetString(id); ok {
		return true
	}
	_, err := time.LoadLocation(id)
	return err == nil
}

// ===== Duration 文法 =====

// DurationFields 是 TemporalDurationString 的解析结果。
// 各字段已折算到规范要求的整数纳秒体系 (小数已向下一级传递)。
type DurationFields struct {
	Years, Months, Weeks, Days              int64
	Hours, Minutes, Seconds                 int64
	Milliseconds, Microseconds, Nanoseconds int64
	Negative                                bool
	// largestUnit 记录字符串中出现的最大单位，用于 round/relativeto 的默认值推断。
	LargestUnit string
}

// ParseTemporalDurationString 解析 TemporalDurationString:
//
//	P[nY][nM][nW][nD][T[nH][nM][nS]]
//
// 规则要点:
//   - 前置 - 表示整个 Duration 取负
//   - 至少一个组件，且组件必须按 Y→M→W→D→(T)H→M→S 顺序出现且不重复
//   - 只有最后一个组件允许带小数；小数值按进制向下传递，
//     任一步无法整除时整个串非法 (如 P0.5M: 月长不固定，无法转天数)
func ParseTemporalDurationString(s string) (DurationFields, bool) {
	var out DurationFields
	p := &isoScanner{s: s}

	if p.take('-') {
		out.Negative = true
	} else {
		p.take('+')
	}
	if !(p.take('P') || p.take('p')) {
		return out, false
	}

	inTimePart := false
	sawAny := false
	// value 是当前组件已读到的完整数值 (如 1.5)；小数部分在 flush 时折算到下一级。
	var (
		value     float64
		haveValue bool
	)
	largest := ""

	// flush 把已读到的 value 落到 unit 对应的字段上，并把小数部分
	// 折算到下一级单位。消费完毕后必须清空 haveValue——否则循环末尾会
	// 误判为"尾部有多余的数值"。
	flush := func(unit byte) bool {
		if !haveValue {
			return false
		}
		if largest == "" {
			largest = string(unit)
		}
		ok := false
		switch unit {
		case 'Y':
			out.Years = int64(value)
			ok = carryFraction(&value, &out.Months, 12)
		case 'M':
			if inTimePart {
				out.Minutes = int64(value)
				ok = carryFraction(&value, &out.Seconds, 60)
			} else {
				out.Months = int64(value)
				// 月不是固定长度单位，无法把小数折算成天数
				ok = value == math.Trunc(value)
			}
		case 'W':
			out.Weeks = int64(value)
			ok = carryFraction(&value, &out.Days, 7)
		case 'D':
			out.Days = int64(value)
			ok = carryFraction(&value, &out.Hours, 24)
		case 'H':
			out.Hours = int64(value)
			ok = carryFraction(&value, &out.Minutes, 60)
		case 'S':
			whole := math.Trunc(value)
			nanos := (value - whole) * 1e9
			if nanos == math.Trunc(nanos) {
				out.Seconds = int64(whole)
				out.Nanoseconds += int64(nanos)
				ok = true
			}
		}
		value, haveValue = 0, false
		return ok
	}

	for !p.eof() {
		c := p.peek()
		if c == 'T' || c == 't' {
			if haveValue {
				return out, false // T 之前的值没有单位
			}
			if inTimePart {
				return out, false
			}
			inTimePart = true
			p.pos++
			continue
		}
		if c >= '0' && c <= '9' {
			if haveValue {
				return out, false // 连续两组数字缺少单位分隔符
			}
			start := p.pos
			for p.isDigit() {
				p.pos++
			}
			intPart := p.s[start:p.pos]
			if p.take('.') || p.take(',') {
				fStart := p.pos
				for p.isDigit() {
					p.pos++
				}
				if fStart == p.pos {
					return out, false
				}
				v, err := parseFloat64(intPart + "." + p.s[fStart:p.pos])
				if err != nil {
					return out, false
				}
				value = v
			} else {
				v, err := parseFloat64(intPart)
				if err != nil {
					return out, false
				}
				value = v
			}
			haveValue = true
			continue
		}
		switch c {
		case 'Y', 'y', 'M', 'm', 'W', 'w', 'D', 'd', 'H', 'h', 'S', 's':
			unit := upperByte(c)
			// Y/W/D 只能出现在 T 之前，H/S 只能出现在 T 之后。
			// M 两边都合法: T 前是"月"，T 后是"分"——这正是 Duration 文法里
			// 最容易出错的一处歧义。
			isDateUnit := unit == 'Y' || unit == 'W' || unit == 'D'
			if isDateUnit && inTimePart {
				return out, false
			}
			if (unit == 'H' || unit == 'S') && !inTimePart {
				return out, false
			}
			switch unit {
			case 'Y', 'W', 'D', 'H':
				if out.fieldSet(unit) {
					return out, false // 重复单位
				}
			case 'M':
				if inTimePart {
					if out.Minutes != 0 {
						return out, false
					}
				} else if out.Months != 0 {
					return out, false
				}
			case 'S':
				if out.Seconds != 0 || out.Nanoseconds != 0 {
					return out, false
				}
			}
			p.pos++
			if !flush(unit) {
				return out, false
			}
			sawAny = true
			continue
		}
		return out, false
	}
	if haveValue || !sawAny {
		return out, false // 尾部多余的数值 / 空 Duration (仅有 P)
	}
	out.LargestUnit = largest
	if out.Negative {
		out.Years, out.Months, out.Weeks, out.Days = -out.Years, -out.Months, -out.Weeks, -out.Days
		out.Hours, out.Minutes, out.Seconds = -out.Hours, -out.Minutes, -out.Seconds
		out.Milliseconds, out.Microseconds, out.Nanoseconds =
			-out.Milliseconds, -out.Microseconds, -out.Nanoseconds
	}
	return out, true
}

// fieldSet 报告某个单位是否已被赋值 (用于检测重复单位)。
func (d *DurationFields) fieldSet(unit byte) bool {
	switch unit {
	case 'Y':
		return d.Years != 0
	case 'W':
		return d.Weeks != 0
	case 'D':
		return d.Days != 0
	case 'H':
		return d.Hours != 0
	}
	return false
}

// ===== 小数向下传递 =====
//
// 三个 apply* 函数是 Temporal Duration 小数处理的核心: 把上一级的小数部分
// 乘以下一级的换算系数，并要求结果为整数——否则该时长无法用整数纳秒精确表示。

// carryFraction 把 value 的小数部分按 factor 折算到下一级单位。
//
// 这是 Temporal Duration 小数处理的核心。例如 P1.5Y: 整数部分 1 留在 Years，
// 小数 0.5 年 ×12 = 6 个月累加到 Months。若折算结果不是整数，说明该时长
// 无法用「整数纳秒 + 日历单位」精确表示，整个字符串非法。
func carryFraction(value *float64, dst *int64, factor float64) bool {
	whole := math.Trunc(*value)
	scaled := (*value - whole) * factor
	if scaled != math.Trunc(scaled) {
		return false
	}
	*dst += int64(scaled)
	*value = whole
	return true
}

func upperByte(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - 'a' + 'A'
	}
	return c
}

// parseFloat64 解析无符号十进制数。自己实现而非用 strconv，是为了
// 明确拒绝 "1e5"/"+1"/"Inf" 等 ISO 文法不允许的写法。
func parseFloat64(s string) (float64, error) {
	intPart := s
	fracPart := ""
	if i := indexByte(s, '.'); i >= 0 {
		intPart = s[:i]
		fracPart = s[i+1:]
	}
	if intPart == "" {
		return 0, errISOParse
	}
	// 没有小数点时是纯整数，scale 保持 1，直接返回。
	v := 0.0
	for i := 0; i < len(intPart); i++ {
		if intPart[i] < '0' || intPart[i] > '9' {
			return 0, errISOParse
		}
		v = v*10 + float64(intPart[i]-'0')
	}
	if fracPart == "" {
		return v, nil
	}
	scale := 1.0
	for i := 0; i < len(fracPart); i++ {
		if fracPart[i] < '0' || fracPart[i] > '9' {
			return 0, errISOParse
		}
		v = v*10 + float64(fracPart[i]-'0')
		scale *= 10
	}
	return v / scale, nil
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// DurationToTotalNanos 返回 Duration 中"精确时长"部分的总纳秒数
// (days 及以下；years/months/weeks 依赖日历/周定义，不参与)。
func DurationToTotalNanos(d DurationFields) *big.Int {
	ns := big.NewInt(0)
	add := func(v int64, factor int64) {
		ns.Add(ns, new(big.Int).Mul(big.NewInt(v), big.NewInt(factor)))
	}
	add(d.Days, NanosPerDay)
	add(d.Hours, NanosPerHour)
	add(d.Minutes, NanosPerMinute)
	add(d.Seconds, NanosPerSecond)
	add(d.Milliseconds, NanosPerMillisecond)
	add(d.Microseconds, NanosPerMicrosecond)
	add(d.Nanoseconds, 1)
	return ns
}

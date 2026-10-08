package object

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Date 表示 JavaScript 的 Date 对象。
//
// 对象模型选择: 与 RegExp / Map / Promise 一致，Date 是独立 struct 而非 *Object。
// 原因是 Date 的内部状态 (time value, 即 epoch 毫秒) 必须对 JS 层完全隐藏 ——
// 若作为普通属性存进 Object.Properties，Object.keys() / 展开运算符 /
// JSON.stringify 都会把它泄漏出去，而规范里 Date 实例的自有属性集合为空。
//
// time value 的取值语义完全对齐 ECMA-262: 一个有限双精度浮点数表示距
// 1970-01-01T00:00:00Z 的毫秒数；NaN 表示 Invalid Date。范围限制
// ±8.64e15 (±100,000,000 天) 同样对齐规范定义的 Time Clip 边界。
type Date struct {
	// ms 是 JS 的 time value (epoch 毫秒)。NaN 表示 Invalid Date。
	ms float64
}

// msPerDay 是一天的毫秒数 (规范 TimeValue 换算基准)。
const msPerDay = 86400000.0

// maxTimeValue 是规范定义的时间值上限 (±8.64e15, 即 ±100,000,000 天)。
const maxTimeValue = 8.64e15

// NewDate 以 time value (epoch 毫秒) 构造 Date。NaN 得到 Invalid Date。
func NewDate(ms float64) *Date {
	return &Date{ms: ms}
}

// NewDateNow 构造代表当前时刻的 Date。
func NewDateNow() *Date {
	return &Date{ms: float64(time.Now().UnixMilli())}
}

// TimeValue 返回该 Date 的 time value (epoch 毫秒, Invalid Date 为 NaN)。
func (d *Date) TimeValue() float64 { return d.ms }

// UnixMilli 返回 epoch 毫秒的整数部分。NaN / 越界时返回 0 (调用方应先判 Valid)。
func (d *Date) UnixMilli() int64 { return int64(d.ms) }

// Valid 报告该 Date 是否持有有效时间值 (非 NaN)。
func (d *Date) Valid() bool { return !math.IsNaN(d.ms) }

// goTime 把 time value 转成 Go 的 time.Time (UTC)。
// 前置条件: Valid() == true。
func (d *Date) goTime() time.Time {
	return time.UnixMilli(int64(d.ms)).UTC()
}

// Type 返回 Date 的类型标识。
func (d *Date) Type() ObjectType { return DATE_OBJ }

// Inspect 返回 console.log 的展示形态。与 Node 一致: 直接渲染 toISOString()
// 的结果 (Invalid Date 输出该字面)。这是 Date 作为对象的 inspect 表现,
// 与 toString() 的本地时区形态刻意不同。
func (d *Date) Inspect() string {
	if !d.Valid() {
		return "Invalid Date"
	}
	return d.ToISOString()
}

// ToISOString 返回扩展年份 ISO 8601 UTC 表示 (toISOString)。
// 年份落在 [1000, 9999] 外用扩展形式 ±YYYYYY, 无效日期抛 RangeError 的语义
// 由调用层控制 —— 本方法对 Invalid Date 返回 "Invalid Date" 字面。
func (d *Date) ToISOString() string {
	if !d.Valid() {
		return "Invalid Date"
	}
	t := d.goTime()
	y := t.Year()
	yearStr := ""
	if y >= 0 && y <= 9999 {
		yearStr = fmt.Sprintf("%04d", y)
	} else if y > 9999 {
		yearStr = fmt.Sprintf("+%06d", y)
	} else {
		yearStr = fmt.Sprintf("-%06d", -y)
	}
	return fmt.Sprintf("%s-%02d-%02dT%02d:%02d:%02d.%03dZ",
		yearStr, int(t.Month()), t.Day(), t.Hour(), t.Minute(), t.Second(), extractMillisDate(t))
}

// ToString 返回 toString() 的结果 —— 宿主本地时区的墙钟时间。
// 形态对齐 V8: "Thu Jan 01 1970 08:00:00 GMT+0800 (China Standard Time)";
// 本运行时没有 Intl / 时区数据库名称, 括号里的时区名退化为缩写。
func (d *Date) ToString() string {
	if !d.Valid() {
		return "Invalid Date"
	}
	t := d.goTime().Local()
	zoneName, offset := t.Zone()
	suffix := formatZoneSuffix(zoneName, offset)
	return fmt.Sprintf("%s %s %02d %d %02d:%02d:%02d %s",
		weekdayAbbrev(t.Weekday()), monthAbbrev(t.Month()), t.Day(), t.Year(),
		t.Hour(), t.Minute(), t.Second(), suffix)
}

// ToUTCString 返回 HTTP 日期形态的 UTC 串 (toUTCString)。
func (d *Date) ToUTCString() string {
	if !d.Valid() {
		return "Invalid Date"
	}
	t := d.goTime()
	return fmt.Sprintf("%s, %02d %s %04d %02d:%02d:%02d GMT",
		weekdayAbbrev(t.Weekday()), t.Day(), monthAbbrev(t.Month()), t.Year(),
		t.Hour(), t.Minute(), t.Second())
}

// ToDateString 返回仅日期的本地串 (toDateString)。
func (d *Date) ToDateString() string {
	if !d.Valid() {
		return "Invalid Date"
	}
	t := d.goTime().Local()
	return fmt.Sprintf("%s %s %02d %d",
		weekdayAbbrev(t.Weekday()), monthAbbrev(t.Month()), t.Day(), t.Year())
}

// ToTimeString 返回仅时间的本地串 (toTimeString)，含时区后缀。
func (d *Date) ToTimeString() string {
	if !d.Valid() {
		return "Invalid Date"
	}
	t := d.goTime().Local()
	zoneName, offset := t.Zone()
	return fmt.Sprintf("%02d:%02d:%02d %s", t.Hour(), t.Minute(), t.Second(),
		formatZoneSuffix(zoneName, offset))
}

// formatZoneSuffix 拼出 V8 风格的时区后缀 "GMT+0800 (Name)";
// 无名时区只出 "GMT+0800"。
func formatZoneSuffix(name string, offsetSec int) string {
	sign := '+'
	if offsetSec < 0 {
		sign = '-'
		offsetSec = -offsetSec
	}
	hh := offsetSec / 3600
	mm := (offsetSec % 3600) / 60
	base := fmt.Sprintf("GMT%c%02d%02d", sign, hh, mm)
	if name != "" {
		return base + " (" + name + ")"
	}
	return base
}

// weekdayAbbrev 返回星期缩写。
func weekdayAbbrev(w time.Weekday) string {
	switch w {
	case time.Sunday:
		return "Sun"
	case time.Monday:
		return "Mon"
	case time.Tuesday:
		return "Tue"
	case time.Wednesday:
		return "Wed"
	case time.Thursday:
		return "Thu"
	case time.Friday:
		return "Fri"
	case time.Saturday:
		return "Sat"
	}
	return ""
}

// monthAbbrev 返回月份缩写。
func monthAbbrev(m time.Month) string {
	switch m {
	case time.January:
		return "Jan"
	case time.February:
		return "Feb"
	case time.March:
		return "Mar"
	case time.April:
		return "Apr"
	case time.May:
		return "May"
	case time.June:
		return "Jun"
	case time.July:
		return "Jul"
	case time.August:
		return "Aug"
	case time.September:
		return "Sep"
	case time.October:
		return "Oct"
	case time.November:
		return "Nov"
	case time.December:
		return "Dec"
	}
	return ""
}

// IsTruthy: 所有 Date 对象皆为 truthy —— Invalid Date 也不例外
// (规范里它是个普通对象, 不走 NaN 的 falsy 规则)。
func (d *Date) IsTruthy() bool { return true }

// GetProperty 沿原型链取属性。
//
// Date 实例本身没有任何自有属性 (规范里其自有属性集合为空), 所有成员都由
// %Date.prototype% 提供 —— 其中 getTime / getFullYear / ... 一族注册为**访问器**,
// 由原形按 MG 的 getter 调用协议以本实例作 this 取值。这里不做任何名字特判,
// 纯粹沿链转发 (与 RegExp 处理成员的手法同构)。
func (d *Date) GetProperty(name string) (Value, bool) {
	if DateProto != nil {
		return DateProto.GetProperty(name)
	}
	return nil, false
}

// LocalTime 返回本实例在**宿主本地时区**下的时间分量载体。
// Invalid Date 返回 ok=false (调用方应据此返回 NaN)。
func (d *Date) LocalTime() (time.Time, bool) {
	if !d.Valid() {
		return time.Time{}, false
	}
	return d.goTime().Local(), true
}

// UTCTime 同 LocalTime, 但恒按 UTC 投影。
func (d *Date) UTCTime() (time.Time, bool) {
	if !d.Valid() {
		return time.Time{}, false
	}
	return d.goTime(), true
}

// TimezoneOffset 返回 getTimezoneOffset 的值: (UTC 分钟 - 本地分钟)。
// ECMAScript 定义为 UTC 减本地, 符号与 Go 的 Zone() 偏移 (本地减 UTC) 相反。
func (d *Date) TimezoneOffset() float64 {
	if !d.Valid() {
		return math.NaN()
	}
	_, offset := d.goTime().Local().Zone()
	return float64(-offset) / 60
}

// extractMillisDate 取出毫秒分量。
func extractMillisDate(t time.Time) int { return t.Nanosecond() / int(time.Millisecond) }

// extractMillis 从 epoch 毫秒里取出不足 1 秒的毫秒部分。
// 先取绝对值再取模: 负 time value (1970 之前) 的取模结果在 Go 里是负的,
// 而毫秒分量按 UTC 时刻计算恒为非负。
func extractMillis(ms float64) int {
	m := int64(math.Abs(ms)) % 1000
	return int(m)
}

// GetProto 返回本实例的原型 (%Date.prototype%)。
// 供 instanceof 的原型链遍历 (vm.protoOf) 使用。
func (d *Date) GetProto() Value { return DateProto }

// SetProperty: Date 的内部状态不可从 JS 层写入, 且实例自有属性集合为空 ——
// 赋值静默失败 (与 Temporal 类型的不可变性同一约定)。
func (d *Date) SetProperty(name string, val Value) {}

// ===== 原型管理 =====

// DateProto 是 %Date.prototype%，由 stdlib 的 setupDate 注册。
// Date.GetProperty 的方法查找依赖它，故必须在产生任何 Date 实例之前装配
// (与 RegExpProto 同一约定)。
var DateProto Value

// SetDateProto 注册 %Date.prototype% (由 stdlib 调用)。
func SetDateProto(p Value) { DateProto = p }

// GetDateProto 返回 %Date.prototype%。
func GetDateProto() Value { return DateProto }

// ===== 解析辅助 =====

// ParseDateString 尝试把字符串解析成 time value (epoch 毫秒)。
//
// 覆盖 JS 实现普遍要求的形态: ISO 8601 (Date Time String Format) 的子集。
// 无法识别时返回 (NaN, false)。刻意不做宽松的"尽力猜"解析 —— 各家引擎的
// 宽松解析规则互不兼容且无规范背书，静默猜错比直接得到 Invalid Date 危害更大。
func ParseDateString(s string) (float64, bool) {
	s = trimDateSpace(s)
	if s == "" {
		return math.NaN(), false
	}
	if ms, ok := parseISO(s); ok {
		return ms, true
	}
	// Go 的若干标准布局作为兜底 (覆盖 Date.prototype.toString 的输出形态,
	// 保证 Date 序列化后能被再解析回来)。
	layouts := []string{
		"Mon Jan 02 2006 15:04:05 MST",
		"Mon Jan 02 2006 15:04:05",
		"2006-01-02 15:04:05",
		"2006-01-02",
		time.RFC1123,
		time.RFC1123Z,
		time.RFC822,
		time.RFC822Z,
		time.RFC850,
		time.ANSIC,
		time.UnixDate,
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return float64(t.UnixMilli()), true
		}
	}
	return math.NaN(), false
}

// trimDateSpace 去掉首尾空白 (含不间断空格等常见写作习惯字符)。
func trimDateSpace(s string) string {
	return strings.Trim(s, " \t\n\r\v\f\u00a0\ufeff")
}

// parseISO 解析 ECMA-262 Date Time String Format 的常用子集。
//
// 支持: YYYY / YYYY-MM / YYYY-MM-DD / 上述三者接 THH:mm / THH:mm:ss /
// THH:mm:ss.sss，尾部可带 Z 或 ±HH:mm 时区偏移。日期后可省略 T 用空格分隔。
func parseISO(s string) (float64, bool) {
	if len(s) < 4 {
		return 0, false
	}
	negYear := false
	if s[0] == '+' {
		s = s[1:]
	} else if s[0] == '-' {
		negYear = true
		s = s[1:]
	}
	year, rest, ok := fixedDigits(s, 4)
	if !ok {
		return 0, false
	}
	if len(rest) > 0 && rest[0] == '-' {
		rest = rest[1:]
	}
	month := 1
	day := 1
	if m, rest2, ok2 := fixedDigits(rest, 2); ok2 {
		month = m
		rest = rest2
		if len(rest) > 0 && rest[0] == '-' {
			rest = rest[1:]
		}
		if dd, rest3, ok3 := fixedDigits(rest, 2); ok3 {
			day = dd
			rest = rest3
		}
	}
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return 0, false
	}

	hour, min, sec, ms := 0, 0, 0, 0
	if rest != "" && (rest[0] == 'T' || rest[0] == 't' || rest[0] == ' ') {
		hms := rest[1:]
		if h, r2, ok2 := fixedDigits(hms, 2); ok2 {
			hour = h
			hms = r2
			if hms != "" && hms[0] == ':' {
				if mi, r3, ok3 := fixedDigits(hms[1:], 2); ok3 {
					min = mi
					hms = r3
					if hms != "" && hms[0] == ':' {
						if se, r4, ok4 := fixedDigits(hms[1:], 2); ok4 {
							sec = se
							hms = r4
							if hms != "" && (hms[0] == '.' || hms[0] == ',') {
								if mil, r5, ok5 := fixedDigits(hms[1:], 3); ok5 {
									ms = mil
									hms = r5
								} else {
									return 0, false
								}
							}
						} else {
							return 0, false
						}
					}
				} else {
					return 0, false
				}
			}
		} else {
			return 0, false
		}
		// 时区: Z / z / ±HH:mm / ±HHmm / ±HH
		offsetMin := 0
		switch {
		case hms == "" || hms == "Z" || hms == "z":
			// UTC
		default:
			sign := 1
			switch hms[0] {
			case '+':
				hms = hms[1:]
			case '-':
				sign = -1
				hms = hms[1:]
			default:
				return 0, false
			}
			oh, r6, ok6 := fixedDigits(hms, 2)
			if !ok6 {
				return 0, false
			}
			om := 0
			r6 = trimColon(r6)
			if len(r6) == 2 {
				if v, _, ok7 := fixedDigits(r6, 2); ok7 {
					om = v
				} else {
					return 0, false
				}
			} else if len(r6) != 0 {
				return 0, false
			}
			offsetMin = sign * (oh*60 + om)
		}
		rest = "" // 已完整消费时间部分
		// 以 UTC 构造再减去偏移 => 得到正确 UTC 时刻。
		if hour < 0 || hour > 24 || min < 0 || min > 59 || sec < 0 || sec > 60 {
			return 0, false
		}
		return buildEpoch(year, negYear, month, day, hour, min, sec, ms, offsetMin)
	}
	if rest != "" {
		return 0, false
	}
	return buildEpoch(year, negYear, month, day, 0, 0, 0, 0, 0)
}

// trimColon 去掉时区里 HH 与 mm 之间的可选冒号。
func trimColon(s string) string {
	if len(s) > 0 && s[0] == ':' {
		return s[1:]
	}
	return s
}

// buildEpoch 由各分量算出 epoch 毫秒。offsetMin 是所要表达的本地时区相对
// UTC 的偏移 (分钟);先构造出该墙钟时刻对应的 UTC 时间再减去偏移。
func buildEpoch(year int, negYear bool, month, day, hour, min, sec, ms, offsetMin int) (float64, bool) {
	y := year
	if negYear {
		y = -year
	}
	t := time.Date(y, time.Month(month), day, hour, min, sec, ms*int(time.Millisecond), time.UTC)
	ms64 := float64(t.UnixMilli()) - float64(offsetMin)*60000
	return ms64, true
}

// BuildTimeValue 由各分量组装 time value (epoch 毫秒)。
//
// utcMode=true 时分量按 UTC 解释 (对应 Date.UTC);false 时按宿主本地时区解释
// (对应 new Date(y, m, d, ...))。分量允许越界 —— 由 time.Date 自动进位,
// 这与规范 MakeTime/MakeDay/MakeDate 的行为一致。
func BuildTimeValue(year, month, day, hour, min, sec, ms int, utcMode bool) float64 {
	loc := time.Local
	if utcMode {
		loc = time.UTC
	}
	t := time.Date(year, time.Month(month+1), day, hour, min, sec, ms*int(time.Millisecond), loc)
	return float64(t.UnixMilli())
}

// fixedDigits 从 s 开头读取恰好 n 位十进制数字。位数不足返回 false。
func fixedDigits(s string, n int) (int, string, bool) {
	if len(s) < n {
		return 0, s, false
	}
	v, err := strconv.Atoi(s[:n])
	if err != nil {
		return 0, s, false
	}
	return v, s[n:], true
}

// TimeClip 把任意双精度时间值规约到规范允许的范围内, 超出返回 NaN。
// 这是所有 Date 算术与 setter 的统一收口。
func TimeClip(ms float64) float64 {
	if math.IsNaN(ms) || math.IsInf(ms, 0) {
		return math.NaN()
	}
	if ms > maxTimeValue || ms < -maxTimeValue {
		return math.NaN()
	}
	return ms
}

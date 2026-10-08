package stdlib

import (
	"math"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// setupDate 注册 Date 构造器 / Date.prototype / performance。
//
// 背景: JS 语言层此前完全没有 Date 与 performance (两者 typeof 都是 undefined,
// 全仓零注册) —— 看板 ryGXAJ。apps/ 里凡是涉及排序、分组、时间戳、耗时统计的
// 应用都拿不到任何时间源，只能退化成「采样序号」。
//
// 实现约束: 零 cgo、零第三方依赖 —— 直接用 Go 标准库 time。Date 的内部状态
// (time value) 存在 object.Date 结构体里，对 JS 层不可见 (Date 实例的自有
// 属性集合按规范必须为空)。
func setupDate(env *runtime.Environment) {
	proto := setupDateProto()
	object.SetDateProto(proto)

	// ===== Date 构造器 =====
	fn := object.NewBuiltin("Date", func(args ...object.Value) object.Value {
		switch len(args) {
		case 0:
			return object.NewDateNow()
		case 1:
			return dateFromSingle(args[0])
		default:
			return dateFromComponents(args)
		}
	})
	fn.SetProperty("name", object.NewString("Date"))
	fn.SetFunctionLength(7)
	fn.SetProperty("prototype", proto)
	proto.SetBuiltinProperty("constructor", fn)

	// ===== 静态方法 =====

	// Date.now(): 当前 time value。
	fn.SetProperty("now", object.NewBuiltin("now", func(args ...object.Value) object.Value {
		return object.NewNumber(float64(nowMillis()))
	}))

	// Date.parse(str): 解析字符串得到 time value, 失败 NaN。
	fn.SetProperty("parse", object.NewBuiltin("parse", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewNumber(math.NaN())
		}
		ms, ok := object.ParseDateString(toStr(args[0]))
		if !ok {
			return object.NewNumber(math.NaN())
		}
		return object.NewNumber(object.TimeClip(ms))
	}))

	// Date.UTC(y, m[, d[, h[, mi[, s[, ms]]]]]): 按 UTC 组装 time value。
	fn.SetProperty("UTC", object.NewBuiltin("UTC", func(args ...object.Value) object.Value {
		return object.NewNumber(dateUTC(args))
	}))

	env.Declare("Date", fn, false)

	// ===== performance 命名空间 =====
	setupPerformance(env)
}

// dateFromSingle 处理 new Date(v) 的单参形态。
//
// Date 对象按规范取其 time value;其余值走 ToPrimitive → 数字 → 字符串两条路:
// 数字直接用 (再做 Time Clip), 字符串先解析。
func dateFromSingle(v object.Value) object.Value {
	switch val := v.(type) {
	case *object.Date:
		return object.NewDate(val.TimeValue())
	case *object.Number:
		return object.NewDate(object.TimeClip(val.Value))
	case *object.String:
		ms, ok := object.ParseDateString(val.Value)
		if !ok {
			return object.NewDate(math.NaN())
		}
		return object.NewDate(object.TimeClip(ms))
	case *object.Boolean:
		if val.Value {
			return object.NewDate(1)
		}
		return object.NewDate(0)
	case *object.Null, *object.Undefined:
		return object.NewDate(math.NaN())
	default:
		// 其它对象型 (含包装对象): ToPrimitive → ToNumber。
		n := toFloat(v)
		if math.IsNaN(n) {
			s := toStr(v)
			if ms, ok := object.ParseDateString(s); ok {
				return object.NewDate(object.TimeClip(ms))
			}
			return object.NewDate(math.NaN())
		}
		return object.NewDate(object.TimeClip(n))
	}
}

// dateFromComponents 处理 new Date(y, m, d, h, mi, s, ms) 的多参形态。
// 缺位按 0 (日期为 1) 补齐;越界分量由 dateUTC 统一 Time Clip。
func dateFromComponents(args []object.Value) object.Value {
	nums := make([]float64, 0, 7)
	for _, a := range args {
		nums = append(nums, toFloat(a))
	}
	return object.NewNumber(dateUTCFromNums(nums, false))
}

// dateUTC 实现 Date.UTC(...): 与多参构造器同一组装逻辑, 但按 UTC 解释。
func dateUTC(args []object.Value) float64 {
	nums := make([]float64, 0, 7)
	for _, a := range args {
		nums = append(nums, toFloat(a))
	}
	return dateUTCFromNums(nums, true)
}

// dateUTCFromNums 由分量组装 time value。
//
// utcMode=true 对应 Date.UTC (分量按 UTC 解释);false 对应 new Date(y,m,...)
// (分量按宿主本地时区解释)。两者最终都经 TimeClip 收口到合法范围。
func dateUTCFromNums(nums []float64, utcMode bool) float64 {
	for _, n := range nums {
		if math.IsNaN(n) {
			return math.NaN()
		}
	}
	get := func(i int) int {
		if i < len(nums) {
			return int(nums[i])
		}
		return 0
	}
	year := get(0)
	month := get(1)
	day := 1
	if len(nums) > 2 {
		day = get(2)
	}
	hour, min, sec, ms := get(3), get(4), get(5), get(6)

	// 年份 0..99 按规范映射到 1900+year。
	if year >= 0 && year <= 99 {
		year += 1900
	}
	ms64 := object.BuildTimeValue(year, month, day, hour, min, sec, ms, utcMode)
	return object.TimeClip(ms64)
}

// nowMillis 返回当前 epoch 毫秒。单独抽出是为了让 performance.timeOrigin
// 与 Date.now 共享同一个时钟基准。
func nowMillis() int64 { return time.Now().UnixMilli() }

// setupDateProto 装配 %Date.prototype%。
//
// Getter 类的分量访问实现在 object.Date.GetProperty 里 (需要读实例内部的
// time value, 无法在共享原型上静态注册);这里只放行为方法与转换器。
func setupDateProto() *object.Object {
	p := object.NewObject()

	// ===== 取值方法族 (getTime / getFullYear / getUTCHours / ...) =====
	// 规范里这些是**方法** (调用要带括号), 不是访问器属性 —— 故一律注册为
	// BuiltinMethod。分量本无法在共享原型上静态取值, 由 this (Date 实例)
	// 提供各自的内部 time value。
	for name, fn := range map[string]func(*object.Date) float64{
		"getTime":            func(d *object.Date) float64 { return d.TimeValue() },
		"getFullYear":        localGetter(func(t time.Time) float64 { return float64(t.Year()) }),
		"getMonth":           localGetter(func(t time.Time) float64 { return float64(int(t.Month()) - 1) }),
		"getDate":            localGetter(func(t time.Time) float64 { return float64(t.Day()) }),
		"getDay":             localGetter(func(t time.Time) float64 { return float64(t.Weekday()) }),
		"getHours":           localGetter(func(t time.Time) float64 { return float64(t.Hour()) }),
		"getMinutes":         localGetter(func(t time.Time) float64 { return float64(t.Minute()) }),
		"getSeconds":         localGetter(func(t time.Time) float64 { return float64(t.Second()) }),
		"getMilliseconds":    localGetter(millisOf),
		"getUTCFullYear":     utcGetter(func(t time.Time) float64 { return float64(t.Year()) }),
		"getUTCMonth":        utcGetter(func(t time.Time) float64 { return float64(int(t.Month()) - 1) }),
		"getUTCDate":         utcGetter(func(t time.Time) float64 { return float64(t.Day()) }),
		"getUTCDay":          utcGetter(func(t time.Time) float64 { return float64(t.Weekday()) }),
		"getUTCHours":        utcGetter(func(t time.Time) float64 { return float64(t.Hour()) }),
		"getUTCMinutes":      utcGetter(func(t time.Time) float64 { return float64(t.Minute()) }),
		"getUTCSeconds":      utcGetter(func(t time.Time) float64 { return float64(t.Second()) }),
		"getUTCMilliseconds": utcGetter(millisOf),
		"getTimezoneOffset":  func(d *object.Date) float64 { return d.TimezoneOffset() },
	} {
		dateGetter(p, name, fn)
	}

	p.SetBuiltinProperty("valueOf", object.NewBuiltinMethod("valueOf", func(this object.Value, args ...object.Value) object.Value {
		d, ok := this.(*object.Date)
		if !ok {
			return object.NewErrorWithName("TypeError", "Date.prototype.valueOf requires that 'this' be a Date")
		}
		return object.NewNumber(d.TimeValue())
	}))

	// ===== 字符串形态 =====
	p.SetBuiltinProperty("toString", object.NewBuiltinMethod("toString", func(this object.Value, args ...object.Value) object.Value {
		d, ok := this.(*object.Date)
		if !ok {
			return object.NewErrorWithName("TypeError", "Date.prototype.toString requires that 'this' be a Date")
		}
		return object.NewString(d.ToString())
	}))

	p.SetBuiltinProperty("toISOString", object.NewBuiltinMethod("toISOString", func(this object.Value, args ...object.Value) object.Value {
		d, ok := this.(*object.Date)
		if !ok {
			return object.NewErrorWithName("TypeError", "Date.prototype.toISOString requires that 'this' be a Date")
		}
		return object.NewString(d.ToISOString())
	}))

	p.SetBuiltinProperty("toJSON", object.NewBuiltinMethod("toJSON", func(this object.Value, args ...object.Value) object.Value {
		d, ok := this.(*object.Date)
		if !ok {
			return object.NewErrorWithName("TypeError", "Date.prototype.toJSON requires that 'this' be a Date")
		}
		return object.NewString(d.ToISOString())
	}))

	p.SetBuiltinProperty("toUTCString", object.NewBuiltinMethod("toUTCString", func(this object.Value, args ...object.Value) object.Value {
		d, ok := this.(*object.Date)
		if !ok {
			return object.NewErrorWithName("TypeError", "Date.prototype.toUTCString requires that 'this' be a Date")
		}
		return object.NewString(d.ToUTCString())
	}))

	p.SetBuiltinProperty("toGMTString", object.NewBuiltinMethod("toGMTString", func(this object.Value, args ...object.Value) object.Value {
		d, ok := this.(*object.Date)
		if !ok {
			return object.NewErrorWithName("TypeError", "Date.prototype.toGMTString requires that 'this' be a Date")
		}
		return object.NewString(d.ToUTCString())
	}))

	p.SetBuiltinProperty("toDateString", object.NewBuiltinMethod("toDateString", func(this object.Value, args ...object.Value) object.Value {
		d, ok := this.(*object.Date)
		if !ok {
			return object.NewErrorWithName("TypeError", "Date.prototype.toDateString requires that 'this' be a Date")
		}
		return object.NewString(d.ToDateString())
	}))

	p.SetBuiltinProperty("toTimeString", object.NewBuiltinMethod("toTimeString", func(this object.Value, args ...object.Value) object.Value {
		d, ok := this.(*object.Date)
		if !ok {
			return object.NewErrorWithName("TypeError", "Date.prototype.toTimeString requires that 'this' be a Date")
		}
		return object.NewString(d.ToTimeString())
	}))

	// 本运行时没有 Intl / 本地化数据, toLocaleString 退化为 toString。
	p.SetBuiltinProperty("toLocaleString", object.NewBuiltinMethod("toLocaleString", func(this object.Value, args ...object.Value) object.Value {
		d, ok := this.(*object.Date)
		if !ok {
			return object.NewErrorWithName("TypeError", "Date.prototype.toLocaleString requires that 'this' be a Date")
		}
		return object.NewString(d.ToString())
	}))
	p.SetBuiltinProperty("toLocaleDateString", object.NewBuiltinMethod("toLocaleDateString", func(this object.Value, args ...object.Value) object.Value {
		d, ok := this.(*object.Date)
		if !ok {
			return object.NewErrorWithName("TypeError", "Date.prototype.toLocaleDateString requires that 'this' be a Date")
		}
		return object.NewString(d.ToDateString())
	}))
	p.SetBuiltinProperty("toLocaleTimeString", object.NewBuiltinMethod("toLocaleTimeString", func(this object.Value, args ...object.Value) object.Value {
		d, ok := this.(*object.Date)
		if !ok {
			return object.NewErrorWithName("TypeError", "Date.prototype.toLocaleTimeString requires that 'this' be a Date")
		}
		return object.NewString(d.ToTimeString())
	}))

	// ===== Setter =====
	p.SetBuiltinProperty("setTime", object.NewBuiltinMethod("setTime", func(this object.Value, args ...object.Value) object.Value {
		d, ok := this.(*object.Date)
		if !ok {
			return object.NewErrorWithName("TypeError", "Date.prototype.setTime requires that 'this' be a Date")
		}
		t := math.NaN()
		if len(args) > 0 {
			t = toFloat(args[0])
		}
		*d = *object.NewDate(object.TimeClip(t))
		return object.NewNumber(d.TimeValue())
	}))

	// ===== @@toPrimitive =====
	// 有了它, `date1 - date2` / `date + ""` / Date 参与比较算术才会走规范路径
	// (default hint → string)。
	p.SetBuiltinSymbolProperty(object.GetGlobalSymbol("Symbol.toPrimitive"), object.NewBuiltinMethod("Symbol.toPrimitive", func(this object.Value, args ...object.Value) object.Value {
		d, ok := this.(*object.Date)
		if !ok {
			return object.NewErrorWithName("TypeError", "Date.prototype[Symbol.toPrimitive] requires that 'this' be a Date")
		}
		hint := "default"
		if len(args) > 0 {
			if s, ok := args[0].(*object.String); ok {
				hint = s.Value
			}
		}
		switch hint {
		case "number":
			return object.NewNumber(d.TimeValue())
		default: // "string" / "default"
			return object.NewString(d.ToString())
		}
	}))

	return p
}

// dateGetter 定义 Date.prototype 上的取值方法 (getXxx() 一族)。
//
// 统一处理「this 不是 Date → TypeError」与「Invalid Date → NaN」两条边界。
func dateGetter(p *object.Object, name string, f func(*object.Date) float64) {
	p.SetBuiltinProperty(name, object.NewBuiltinMethod(name, func(this object.Value, args ...object.Value) object.Value {
		d, ok := this.(*object.Date)
		if !ok {
			return object.NewErrorWithName("TypeError", "Date.prototype."+name+" requires that 'this' be a Date")
		}
		return object.NewNumber(f(d))
	}))
}

// localGetter 把「按宿主本地时区取分量」的函数适配成 Date 取值方法。
func localGetter(f func(time.Time) float64) func(*object.Date) float64 {
	return func(d *object.Date) float64 {
		t, ok := d.LocalTime()
		if !ok {
			return math.NaN()
		}
		return f(t)
	}
}

// utcGetter 同 localGetter, 但恒按 UTC 投影。
func utcGetter(f func(time.Time) float64) func(*object.Date) float64 {
	return func(d *object.Date) float64 {
		t, ok := d.UTCTime()
		if !ok {
			return math.NaN()
		}
		return f(t)
	}
}

// millisOf 取出毫秒分量。
func millisOf(t time.Time) float64 { return float64(t.Nanosecond() / int(time.Millisecond)) }

// setupPerformance 注册 performance 命名空间。
//
// 最小面是 performance.now(): 相对某个固定起点的单调毫秒读数 (含小数),
// 这是 JS 侧唯一可用的高精度计时源 —— 此前完全没有, 耗时统计只能靠
// Date.now() 的毫秒粗粒度。
func setupPerformance(env *runtime.Environment) {
	var p = object.NewObject()

	// timeOrigin 取值语义: performance.now() 的零点对应的 epoch 毫秒
	// (宿主时间基准)。与 Date.now() 同一时钟。
	origin := float64(nowMillis())

	p.SetBuiltinProperty("now", object.NewBuiltin("now", func(args ...object.Value) object.Value {
		return object.NewNumber(float64(nowMillis()) - origin)
	}))
	p.SetBuiltinProperty("timeOrigin", object.NewNumber(origin))
	p.SetBuiltinProperty("toJSON", object.NewBuiltin("toJSON", func(args ...object.Value) object.Value {
		return p
	}))
	setToStringTag(p, "Performance")
	setNamespaceProto(p)

	env.Declare("performance", p, false)
}

package gfx

// ===== gx/geo: 定位 (一次定位 / 持续监听 / 最后位置缓存) =====
//
// 定位是这一层里**形态最全**的能力, 它同时用到了三种机制:
//
//	一次定位    getLocation()  → Promise (宿主 pending → ResolveNative 回填)
//	持续监听    watchLocation() → 流 (同一个 id 反复回填, 不摘除条目)
//	最后位置    lastLocation() → 纯内核缓存 (同步读, 不碰宿主)
//
// ## 为什么要有 lastLocation()
//
// 定位慢 (冷启动 GPS 可以十几秒), 而界面上"显示我附近的东西"往往希望**先渲染
// 一个近似位置再精确化**。让每个应用自己缓存一份会导致同一份逻辑写很多遍, 还会
// 在"第一次还没有位置"时各自发明一套空状态。内核缓存最后一次成功的定位, 于是
// `lastLocation()` 可以同步返回 —— 有就用, 没有就是 null, 语义干净。
//
// ## 坐标系统 (type) 这一块**不转换**
//
// Android/iOS 在中国大陆拿到的系统定位本来就有两种口径 (原始 GNSS 是 wgs84,
// 系统 API 多数给 gcj02)。`opts.type` 原样透传给宿主, **内核不做换算**: 火星
// 坐标换算是平台/合规问题, 在内核里实现一份"谁都不敢保证对"的换算是错的
// —— 换错比不换更糟 (围栏/热力图会整体偏移几百米)。宿主给不出要求的坐标系时
// 应当返回 platform-error, 而不是给个近似的。

import (
	"math"
	"time"

	"github.com/14752222/Gox/object"
)

// Location 是一次定位结果。
type Location struct {
	Latitude         float64 // 纬度 (type 指定的坐标系)
	Longitude        float64
	Altitude         float64 // 米; 不支持时 0
	Accuracy         float64 // 水平精度 (米); 越小越好
	AltitudeAccuracy float64
	Speed            float64 // m/s
	Heading          float64 // 0..360, 正北为 0
	Timestamp        int64   // Unix 毫秒 (定位时刻, 不是收到时刻)
	Provider         string  // "gps" | "network" | "passive" | "fused" | "unknown"
	Mocked           bool    // 模拟位置 (开发环境 / 作弊检测)
	Type             string  // 坐标系: "wgs84" | "gcj02"
}

// 方法名与事件名。
const (
	nmLocationGet   = "location.get"
	nmLocationWatch = "location.watch"
	evLocation      = "location" // "最后位置变了" (供 useLastLocation 订阅)
)

var lastFix *Location

// LastLocation 读最后一次成功的定位 (Go 侧; 没有则 nil)。
func LastLocation() *Location {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	if lastFix == nil {
		return nil
	}
	cp := *lastFix
	return &cp
}

// ReportLocation 由宿主上报一个定位结果 (GUI 线程)。
//
// 它主要是给**流式**推送与桌面模拟器用的: 一次定位的结果走 ResolveNative 的
// 返回值, 而这个函数负责更新 lastLocation() 缓存。
func ReportLocation(loc Location) {
	nativeMu.Lock()
	cp := loc
	lastFix = &cp
	nativeMu.Unlock()
	notifyNativeChanged(evLocation)
}

// locationToJS 组装脚本侧对象。
func locationToJS(l Location) object.Value {
	o := object.NewObject()
	o.SetProperty("latitude", object.NewNumber(l.Latitude))
	o.SetProperty("longitude", object.NewNumber(l.Longitude))
	// lat / lng 是**便利别名**: 业务代码里这两个写法太常见, 而"某个库只认
	// latitude"的情况同样常见, 两个都留的成本只是一个属性。
	o.SetProperty("lat", object.NewNumber(l.Latitude))
	o.SetProperty("lng", object.NewNumber(l.Longitude))
	o.SetProperty("altitude", object.NewNumber(l.Altitude))
	o.SetProperty("accuracy", object.NewNumber(l.Accuracy))
	o.SetProperty("altitudeAccuracy", object.NewNumber(l.AltitudeAccuracy))
	o.SetProperty("speed", object.NewNumber(l.Speed))
	o.SetProperty("heading", object.NewNumber(l.Heading))
	o.SetProperty("timestamp", object.NewNumber(float64(l.Timestamp)))
	nativeSet(o, "provider", l.Provider)
	nativeSet(o, "type", l.Type)
	o.SetProperty("mocked", object.NewBoolean(l.Mocked))
	return o
}

// locationFromJS 把宿主 / Promise 结果解析回 Location。
//
// 宽容: 缺字段按 0 处理 (宿主只给经纬度是最常见的情形); 但如果**经纬度都不是
// 数字**, 那就是个形状不对的结果 —— 返回 ok=false 让调用方报 platform-error,
// 而不是把 (0,0) 这个"几内亚湾"的坐标当成真实定位交给应用。
func locationFromJS(v object.Value) (Location, bool) {
	o, ok := v.(*object.Object)
	if !ok {
		return Location{}, false
	}
	if !hasCoord(o, "latitude", "lat") || !hasCoord(o, "longitude", "lng") {
		return Location{}, false
	}
	l := Location{
		Latitude:         pickCoord(o, "latitude", "lat"),
		Longitude:        pickCoord(o, "longitude", "lng"),
		Altitude:         objPropNum(o, "altitude"),
		Accuracy:         objPropNum(o, "accuracy"),
		AltitudeAccuracy: objPropNum(o, "altitudeAccuracy"),
		Speed:            objPropNum(o, "speed"),
		Heading:          objPropNum(o, "heading"),
		Timestamp:        int64(objPropNum(o, "timestamp")),
		Provider:         objPropStr(o, "provider"),
		Type:             objPropStr(o, "type"),
		Mocked:           nativeBool(objProp(o, "mocked")),
	}
	if l.Timestamp == 0 {
		l.Timestamp = time.Now().UnixMilli()
	}
	if l.Provider == "" {
		l.Provider = "unknown"
	}
	if l.Type == "" {
		l.Type = "wgs84"
	}
	return l, true
}

func pickCoord(o *object.Object, names ...string) float64 {
	for _, n := range names {
		if _, ok := o.GetProperty(n); ok {
			return objPropNum(o, n)
		}
	}
	return 0
}

func hasCoord(o *object.Object, names ...string) bool {
	for _, n := range names {
		if v, ok := o.GetProperty(n); ok {
			if _, isNum := v.(*object.Number); isNum {
				return true
			}
		}
	}
	return false
}

// locationOpts 读取定位选项并补缺省 (type 缺省 wgs84 —— 与 GPS 原始口径一致)。
func locationOpts(args []object.Value, i int) *object.Object {
	opts := nativeOpts(args, i)
	if objPropStr(opts, "type") == "" {
		opts.SetProperty("type", object.NewString("wgs84"))
	}
	if objProp(opts, "highAccuracy") == nil {
		opts.SetProperty("highAccuracy", object.NewBoolean(false))
	}
	return opts
}

// jsGetLocation 是 `getLocation(options?, callback?)` → Promise<Location>。
//
// options: {type:"wgs84"|"gcj02", highAccuracy, timeout, maximumAge}
//
// 结果走 callNativeWithSink 而不是 callNative: 宿主给的原始结果要先经
// locationFromJS 归一化 + 写进 lastLocation 缓存, 脚本拿到的才与 lastLocation()
// / useLastLocation() 的形状一致 (双路径不同形状是"附近的人"这类界面最容易
// 踩的坑)。
func jsGetLocation(args ...object.Value) object.Value {
	return callNativeWithSink(nmLocationGet, locationOpts(args, 0), nativeOptFunc(args, 1),
		func(result object.Value) (object.Value, object.Value) {
			l, ok := locationFromJS(result)
			if !ok {
				// 宿主给的不是合法定位对象 → 明确失败而不是静默给 (0,0)。
				return nil, nativeErr(ErrPlatform,
					"location.get 返回了无法解析的定位结果").toJS()
			}
			ReportLocation(l)
			return locationToJS(l), nil
		})
}

// jsWatchLocation 是 `watchLocation(callback, options?)` → watchId。
//
// **同步返回 id** (不是 Promise): 注册监听本身是瞬时动作, 而没有 id 应用就没法
// 在卸载页面时停掉它 —— 让注册变成异步, 会把"组件卸载"与"拿到 id"排成一场竞态
// (卸载时 id 还没回来, 监听永远停不掉, GPS 一直开着直到进程退出)。
//
// 回调签名是 `(location, err)` —— 与 err-first 的异步族**相反**, 因为这是事件流
// (像 EventTarget 的 listener): 事件优先、错误其次。这个不一致是刻意的, 每次推送
// 都先收 err 会让最常见的写法 `(loc) => ...` 全部错位。
func jsWatchLocation(args ...object.Value) object.Value {
	cb := nativeOptFunc(args, 0)
	if cb == nil {
		return object.NewTypeError("watchLocation: 需要回调函数")
	}
	// 包一层 sink: 流式推送也要经 locationFromJS 归一化 + 写 lastLocation 缓存。
	// 与 jsGetLocation 的 callNativeWithSink 是同一契约 —— 否则"watch 收得到、
	// lastLocation() 却是 null"会让双路径不同步 (附近的人 / 围栏这类界面最常见的坑)。
	sink := object.NewBuiltin("__watch_sink", func(v ...object.Value) object.Value {
		// 事件流形式: 宿主经 ResolveNative 投 (result, errVal)。
		var result object.Value = object.UndefinedSingleton
		var errVal object.Value = object.NullSingleton
		if len(v) > 0 {
			result = v[0]
		}
		if len(v) > 1 {
			errVal = v[1]
		}
		if errVal != object.NullSingleton && errVal != object.UndefinedSingleton {
			// 流里的错误照投给回调 (err 位) —— 用户回调签名是 (loc, err)。
			object.CallFunction(cb, nil, object.UndefinedSingleton, errVal)
			return object.UndefinedSingleton
		}
		l, ok := locationFromJS(result)
		if !ok {
			// 形状不对 → 当错误投 (与 getLocation 的 platform-error 同口径)。
			recordWarn("gx/geo watchLocation: 宿主推送了无法解析的定位结果")
			object.CallFunction(cb, nil, object.UndefinedSingleton,
				nativeErr(ErrPlatform, "location.watch 返回了无法解析的定位结果").toJS())
			return object.UndefinedSingleton
		}
		ReportLocation(l)
		object.CallFunction(cb, nil, locationToJS(l), object.NullSingleton)
		return object.UndefinedSingleton
	})
	id, err := startNativeStream(nmLocationWatch, locationOpts(args, 1), sink)
	if err != nil {
		// 起流失败要**出声**: 与 Promise 不同, 这里没有 catch 可挂 —— 静默返回
		// 一个永不触发的 id 会让应用以为正在监听。
		recordWarn("gx/geo watchLocation 失败: %v", err)
		return object.NewNumber(-1)
	}
	return object.NewString(id)
}

// jsClearWatch 停掉一个监听 (`clearWatch(id)`); id 无效时静默。
func jsClearWatch(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.UndefinedSingleton
	}
	stopNativeStream(valueText(args[0]))
	return object.UndefinedSingleton
}

// jsClearAllWatches 停掉全部定位监听 (页面卸载时的兜底), 返回停掉的个数。
func jsClearAllWatches(args ...object.Value) object.Value {
	nativeMu.Lock()
	ids := make([]string, 0, len(nativeCalls))
	for id, c := range nativeCalls {
		if c.stream && nativeMethodCapability(c.method) == "location" {
			ids = append(ids, id)
		}
	}
	nativeMu.Unlock()
	for _, id := range ids {
		stopNativeStream(id)
	}
	return object.NewNumber(float64(len(ids)))
}

// jsLastLocation 同步返回最后一次定位 (没有 → null)。
func jsLastLocation(args ...object.Value) object.Value {
	l := LastLocation()
	if l == nil {
		return object.NullSingleton
	}
	return locationToJS(*l)
}

// jsHasLocation 报告有没有缓存过定位 (空状态判断的便利入口)。
func jsHasLocation(args ...object.Value) object.Value {
	return object.NewBoolean(LastLocation() != nil)
}

// jsLocationDistance 算两个位置之间的球面距离 (米)。
//
// 为什么把它放进内核: 它**不需要平台能力** (纯计算), 但每个做"附近"的应用都会
// 写一遍, 而手写的 haversine 十有八九漏掉角度→弧度换算或用了错误的地球半径。
// 内核给一份算对了的, 顺带成为文档里的示例。
//
// 入参可以是两个 location 对象, 也可以是四个数字 (lat1, lng1, lat2, lng2)。
func jsLocationDistance(args ...object.Value) object.Value {
	var lat1, lng1, lat2, lng2 float64
	switch {
	case len(args) >= 4:
		lat1, lng1 = nativeNumberValue(args[0]), nativeNumberValue(args[1])
		lat2, lng2 = nativeNumberValue(args[2]), nativeNumberValue(args[3])
	case len(args) >= 2:
		a, ok1 := locationFromJS(args[0])
		b, ok2 := locationFromJS(args[1])
		if !ok1 || !ok2 {
			return object.NewTypeError("distanceBetween: 需要两个含 latitude/longitude 的对象, 或四个数字")
		}
		lat1, lng1, lat2, lng2 = a.Latitude, a.Longitude, b.Latitude, b.Longitude
	default:
		return object.NewTypeError("distanceBetween: 需要两个位置对象或四个数字")
	}
	return object.NewNumber(haversineMeters(lat1, lng1, lat2, lng2))
}

// haversineMeters 是标准的半正矢公式 (球面近似, 相对误差 < 0.5%, 足够"附近 500
// 米"这类判断; 真要米级精度的测距是测量学问题, 不属于运行时的职责)。
func haversineMeters(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadius = 6371008.8 // 平均地球半径 (IUGG)
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	sin2 := func(x float64) float64 { s := math.Sin(x); return s * s }
	dLat := rad(lat2 - lat1)
	dLng := rad(lng2 - lng1)
	a := sin2(dLat/2) + math.Cos(rad(lat1))*math.Cos(rad(lat2))*sin2(dLng/2)
	return 2 * earthRadius * math.Asin(math.Min(1, math.Sqrt(a)))
}

// jsReportLocation 让宿主 / 模拟器上报一个定位 (桌面没有定位服务时靠它驱动链路)。
func jsReportLocation(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.NewTypeError("reportLocation: 需要 {latitude, longitude, ...}")
	}
	l, ok := locationFromJS(args[0])
	if !ok {
		return object.NewTypeError("reportLocation: 需要 {latitude, longitude, ...}")
	}
	ReportLocation(l)
	return object.UndefinedSingleton
}

// jsLocationPlatform 报告当前平台的定位能力 (排查用: "差一个权限"与"差一个实现"
// 这两种"定位不工作"的排查路径完全不同)。
func jsLocationPlatform(args ...object.Value) object.Value {
	o := object.NewObject()
	nativeSet(o, "platform", deviceInfo().Platform)
	o.SetProperty("supported", object.NewBoolean(CanIUse("location")))
	o.SetProperty("hasFix", object.NewBoolean(LastLocation() != nil))
	return o
}

// resetGeoStateForTest 清空最后位置。
func resetGeoStateForTest() {
	nativeMu.Lock()
	lastFix = nil
	nativeMu.Unlock()
}

func init() {
	object.RegisterBuiltinModule("gx/geo", func() map[string]object.Value {
		return map[string]object.Value{
			"getLocation":     scr("getLocation", jsGetLocation),
			"watchLocation":   scr("watchLocation", jsWatchLocation),
			"clearWatch":      scr("clearWatch", jsClearWatch),
			"clearAllWatches": scr("clearAllWatches", jsClearAllWatches),

			"lastLocation": scr("lastLocation", jsLastLocation),
			"useLastLocation": scr("useLastLocation", nativeGetter("useLastLocation",
				func() object.Value {
					if l := LastLocation(); l != nil {
						return locationToJS(*l)
					}
					return object.NullSingleton
				}, func(v object.Value) object.Value { return v })),
			"hasLocation": scr("hasLocation", jsHasLocation),

			"distanceBetween":  scr("distanceBetween", jsLocationDistance),
			"locationPlatform": scr("locationPlatform", jsLocationPlatform),
			"reportLocation":   scr("reportLocation", jsReportLocation),
		}
	})
}

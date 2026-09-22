package gfx

// ===== 原生层端到端 (假宿主 + 真 VM + 事件泵) =====
//
// 这些用例把整条 Promise 链路钉死: 脚本 import gx/geo → getLocation() →
// callNative → 宿主返回 Pending → 测试在泵轮次里 ResolveNative → then 收到
// 定位对象。**不需要真机**: NativeHost 是可替换的, 假宿主在这里扮演移动壳的
// Kotlin/Swift 侧, 把"宿主该怎么回填"这件事也一并验证了。

import (
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// fakeNativeHost 是 NativeHost 的测试替身。
//
// 与 fakeDialogHost 同款: 记录收到的调用 + 预设应答。pending 方法先记下 id,
// 测试在泵轮次里调 resolvePending 回填。
type fakeNativeHost struct {
	t         *testing.T
	caps      []string
	results   map[string]func(opts *object.Object) NativeCallResult // 方法名 → 应答
	pendingID map[string][]string                                    // 方法名 → pending 的 __id 队列 (FIFO)
	gotMethod []string
	gotOpts   []*object.Object
}

func newFakeHost(t *testing.T, caps []string) *fakeNativeHost {
	return &fakeNativeHost{
		t: t, caps: caps,
		results:   map[string]func(opts *object.Object) NativeCallResult{},
		pendingID: map[string][]string{},
	}
}

func (f *fakeNativeHost) Capabilities() []string { return f.caps }

// pending 声明某方法异步 (返回 Pending 并记录 id)。
func (f *fakeNativeHost) pending(method string) {
	f.results[method] = func(opts *object.Object) NativeCallResult {
		if id := objPropStr(opts, "__id"); id != "" {
			f.pendingID[method] = append(f.pendingID[method], id)
		}
		return NativePending()
	}
}

// immediate 声明某方法同步返回固定值。
func (f *fakeNativeHost) immediate(method string, res NativeCallResult) {
	f.results[method] = func(opts *object.Object) NativeCallResult { return res }
}

func (f *fakeNativeHost) Call(method string, args object.Value) NativeCallResult {
	f.gotMethod = append(f.gotMethod, method)
	if o, ok := args.(*object.Object); ok {
		f.gotOpts = append(f.gotOpts, o)
	} else {
		f.gotOpts = append(f.gotOpts, nil)
	}
	if fn, ok := f.results[method]; ok {
		if o, ok := args.(*object.Object); ok {
			return fn(o)
		}
		return fn(object.NewObject())
	}
	return NativeFailure(ErrUnsupported, "test host: %s 未预设", method)
}

// resolvePending 回填一次 pending 的调用 (FIFO 出队, 只回填一次避免重复结算)。
func (f *fakeNativeHost) resolvePending(method string, result object.Value, err *NativeError) {
	q := f.pendingID[method]
	if len(q) == 0 {
		f.t.Fatalf("resolvePending(%s): 没有 pending 的 id", method)
	}
	id := q[0]
	f.pendingID[method] = q[1:]
	ResolveNative(id, result, err)
}

// resolveStream 回填一次**流式**推送: 流的语义是同一个 id 反复回填 (宿主在系统
// 回调里持续 ResolveNative), 所以取队首 id 但**不出队**。
func (f *fakeNativeHost) resolveStream(method string, result object.Value, err *NativeError) {
	q := f.pendingID[method]
	if len(q) == 0 {
		f.t.Fatalf("resolveStream(%s): 没有 pending 的 id", method)
	}
	ResolveNative(q[0], result, err)
}

// 全局锁: 假宿主注册到包级单例 (SetNativeHost), 测试串行跑但每个用例都要
// 独立注册 + 拆卸, 避免串味。

func setupHost(t *testing.T, caps []string) *fakeNativeHost {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() {
		object.GlobalScheduler().ClearAll()
		resetNativeStateForTest()
		resetDeviceStateForTest()
		resetAppStateForTest()
		resetGeoStateForTest()
		resetPermissionStateForTest()
		resetViewportStateForTest()
		SetDefaultFactory(nil)
	})
	host := newFakeHost(t, caps)
	SetNativeHost(host)
	return host
}

// pumpSettled 跑事件循环直到 done() 为真, 或超时。
func pumpSettled(t *testing.T, v *vm.VM, fake *fakeSurface, done func() bool) {
	t.Helper()
	round := 0
	const maxRounds = 200
	err := v.RunTimersWithPump(func(maxWait time.Duration) bool {
		round++
		if done() || round >= maxRounds {
			fake.push(Event{Kind: EventClose})
		}
		const pollWait = 10 * time.Millisecond
		if maxWait <= 0 || maxWait > pollWait {
			maxWait = pollWait
		}
		return Pump(maxWait)
	})
	if err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if !done() {
		t.Fatalf("事件循环结束但断言未达成 (转了 %d 轮)", round)
	}
}

// ===== getLocation 全链路 =====

func TestGetLocationPromiseChain(t *testing.T) {
	host := setupHost(t, []string{"location"})
	host.pending("location.get")

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})

	v, err := vm.EvalVM(`
		import { getLocation, lastLocation, hasLocation } from "gx/geo";
		import { h, render } from "gx/gfx";
		globalThis.got = "pending";
		globalThis.err = null;
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 200, height: 150});
		getLocation({type: "gcj02", highAccuracy: true})
			.then((loc) => { got = JSON.stringify({lat: loc.latitude, lng: loc.longitude, type: loc.type}); })
			.catch((e) => { err = e; });
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	// 同步阶段: Promise 未结算 (微任务语义)
	if g, _ := globalStr(t, v, "got"); g != "pending" {
		t.Fatalf("getLocation 的 then 不该在脚本执行期同步跑: %q", g)
	}

	loc := object.NewObject()
	loc.SetProperty("latitude", object.NewNumber(23.1066))
	loc.SetProperty("longitude", object.NewNumber(113.3245))
	loc.SetProperty("type", object.NewString("gcj02"))
	loc.SetProperty("provider", object.NewString("fused"))
	loc.SetProperty("mocked", object.NewBoolean(false))

	pumpSettled(t, v, fake, func() bool {
		// 第 1 轮先把定位回填掉 (宿主在系统回调里 Post + ResolveNative)
		if len(host.pendingID["location.get"]) > 0 {
			host.resolvePending("location.get", loc, nil)
		}
		g, _ := globalStr(t, v, "got")
		return g != "pending"
	})

	got, _ := v.Globals().Get("got")
	if s, ok := got.(*object.String); !ok || s.Value != `{"lat":23.1066,"lng":113.3245,"type":"gcj02"}` {
		t.Fatalf("脚本收到的定位不对: %v", got)
	}
	// lastLocation 缓存已被更新
	l := LastLocation()
	if l == nil || l.Latitude != 23.1066 {
		t.Fatalf("lastLocation 未更新: %+v", l)
	}
	// 选项确实传到了宿主 (含 __id 与 type)
	if len(host.gotOpts) == 0 || objPropStr(host.gotOpts[0], "type") != "gcj02" {
		t.Fatalf("定位选项没传到宿主: %+v", host.gotOpts)
	}
	if len(host.gotOpts) == 0 || objPropStr(host.gotOpts[0], "__id") == "" {
		t.Fatalf("宿主必须拿到 __id 才能回填")
	}
}

func TestGetLocationUnsupported(t *testing.T) {
	// 没有宿主: 立即 reject, errCode=unsupported, 且不依赖事件循环
	setupHost(t, nil)
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})

	v, err := vm.EvalVM(`
		import { getLocation } from "gx/geo";
		import { h, render } from "gx/gfx";
		globalThis.err = null;
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 200, height: 150});
		getLocation().catch((e) => { err = {code: e.errCode, msg: e.errMsg}; });
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	pumpSettled(t, v, fake, func() bool {
		e, ok := v.Globals().Get("err")
		return ok && e != nil && e != object.NullSingleton && e != object.UndefinedSingleton
	})
	e, _ := v.Globals().Get("err")
	eo, ok := e.(*object.Object)
	if !ok {
		t.Fatalf("err 不是对象: %T", e)
	}
	if c := objPropStr(eo, "code"); c != "unsupported" {
		t.Fatalf("缺宿主时 errCode 应为 unsupported, got %q", c)
	}
}

func TestCanIUseResolution(t *testing.T) {
	host := setupHost(t, []string{"location", "camera.takePhoto", "clipboard", "dialog"})
	// 宿主声明 camera.takePhoto → canIUse("camera") 与 canIUse("camera.takePhoto") 都真
	if !CanIUse("location") || !CanIUse("camera") || !CanIUse("camera.takePhoto") {
		t.Fatalf("宿主声明的能力应可查: %s", "location/camera")
	}
	if CanIUse("battery") {
		t.Fatalf("没声明的能力不该可查")
	}
	// 内核模块能力: gx/storage 已注册 → canIUse("storage") 真
	if !CanIUse("storage") {
		t.Fatalf("storage 是内核能力, 应可查")
	}
	_ = host
}

// ===== camera / gallery =====

func TestTakePhotoChain(t *testing.T) {
	host := setupHost(t, []string{"camera"})
	host.pending("camera.takePhoto")

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})

	v, err := vm.EvalVM(`
		import { takePhoto } from "gx/media";
		import { h, render } from "gx/gfx";
		globalThis.got = "pending";
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 200, height: 150});
		takePhoto({camera: "front"})
			.then((f) => { got = f.path + "|" + f.mimeType + "|" + f.sizeText; })
			.catch((e) => { got = "ERR:" + e.errCode; });
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	photo := object.NewObject()
	photo.SetProperty("path", object.NewString(`/tmp/shot.jpg`))
	photo.SetProperty("mimeType", object.NewString("image/jpeg"))
	photo.SetProperty("size", object.NewNumber(2048))

	pumpSettled(t, v, fake, func() bool {
		if len(host.pendingID["camera.takePhoto"]) > 0 {
			host.resolvePending("camera.takePhoto", photo, nil)
		}
		g, _ := globalStr(t, v, "got")
		return g != "pending"
	})
	if g, _ := globalStr(t, v, "got"); g != `/tmp/shot.jpg|image/jpeg|2.0 KB` {
		t.Fatalf("脚本收到照片不对: %q", g)
	}
}

func TestChooseImageCancelled(t *testing.T) {
	host := setupHost(t, []string{"gallery"})
	host.pending("gallery.pick")

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})

	v, err := vm.EvalVM(`
		import { chooseImage, chooseOneImage } from "gx/media";
		import { h, render } from "gx/gfx";
		globalThis.multi = "pending";
		globalThis.one = "pending";
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 200, height: 150});
		chooseImage({count: 3})
			.then((arr) => { multi = "n=" + arr.length; })
			.catch((e) => { multi = "ERR:" + e.errCode; });
		chooseOneImage()
			.then((f) => { one = f === null ? "null" : f.path; })
			.catch((e) => { one = "ERR:" + e.errCode; });
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	// 返回两数组 (count=3 选了两张)
	arr := object.NewArray([]object.Value{
		mustMediaObj("a.jpg"),
		mustMediaObj("b.jpg"),
	})
	// chooseOneImage 内部会把 count 强制成 1 并**再走一次** gallery.pick ——
	// 宿主会看到两个 pending id (chooseImage 一次, chooseOneImage 一次)。
	// 第 1 轮回填第一个 id (chooseImage 那路), 第 2 轮回填第二个 (chooseOneImage)。
	pumpSettled(t, v, fake, func() bool {
		q := host.pendingID["gallery.pick"]
		if len(q) > 0 {
			if len(q) == 2 {
				// chooseImage: 两张图
				host.resolvePending("gallery.pick", arr, nil)
			} else {
				// chooseOneImage: 单张 (第一张)
				host.resolvePending("gallery.pick", mustMediaObj("a.jpg"), nil)
			}
		}
		multi, _ := globalStr(t, v, "multi")
		one, _ := globalStr(t, v, "one")
		return multi != "pending" && one != "pending"
	})
	if g, _ := globalStr(t, v, "multi"); g != "n=2" {
		t.Fatalf("chooseImage 结果不对: %q", g)
	}
	if g, _ := globalStr(t, v, "one"); g != "a.jpg" {
		t.Fatalf("chooseOneImage 结果不对: %q", g)
	}
}

func mustMediaObj(path string) object.Value {
	o := object.NewObject()
	o.SetProperty("path", object.NewString(path))
	o.SetProperty("name", object.NewString(path))
	return o
}

// ===== permission =====

func TestAuthorizeChain(t *testing.T) {
	host := setupHost(t, []string{"permission"})
	host.pending("permission.request")

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})

	v, err := vm.EvalVM(`
		import { authorize, permissionState, checkPermission } from "gx/permission";
		import { h, render } from "gx/gfx";
		globalThis.got = "pending";
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 200, height: 150});
		authorize("camera")
			.then((s) => { got = s; })
			.catch((e) => { got = "ERR:" + e.errCode; });
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	pumpSettled(t, v, fake, func() bool {
		if len(host.pendingID["permission.request"]) > 0 {
			host.resolvePending("permission.request", object.NewString("granted"), nil)
		}
		g, _ := globalStr(t, v, "got")
		return g != "pending"
	})
	if g, _ := globalStr(t, v, "got"); g != "granted" {
		t.Fatalf("authorize 结果不对: %q", g)
	}
	// 授权结果应已写进缓存 (applyAuthorize 的副作用)
	if s := PermissionState("camera"); s != PermGranted {
		t.Fatalf("授权后缓存应为 granted, got %q", s)
	}
}

func TestReportPermissionFromSetting(t *testing.T) {
	setupHost(t, nil)
	resetPermissionStateForTest()
	t.Cleanup(resetPermissionStateForTest)

	ReportPermission("camera", "denied")
	if s := PermissionState("camera"); s != PermDenied {
		t.Fatalf("上报 denied 后缓存应为 denied, got %q", s)
	}
	// 未知权限被静默忽略
	ReportPermission("telepathy", "granted")
	if s := PermissionState("telepathy"); s != PermUnknown {
		t.Fatalf("未知权限不应进缓存: %q", s)
	}
}

// ===== app 生命周期 =====

func TestAppStateReportAndUse(t *testing.T) {
	setupHost(t, nil)
	resetAppStateForTest()

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})

	v, err := vm.EvalVM(`
		import { appState, onAppStateChange, reportAppState } from "gx/app";
		import { h, render } from "gx/gfx";
		globalThis.seen = [];
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 200, height: 150});
		onAppStateChange((s) => seen.push(s));
		globalThis.start = appState();
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	if s, _ := globalStr(t, v, "start"); s != "unknown" {
		t.Fatalf("初始 appState 应为 unknown, got %q", s)
	}
	// 模拟宿主上报两次 (前台→后台), 只报一次"没变"不该重复触发。
	// **必须在泵轮次内调**: 上报的副作用是给脚本回调, 而脚本回调桥在事件循环
	// 之外 (currentVM == nil) 是空操作 —— 拿到 currentVM 是主脚本执行期的
	// 特权, 回调触发与断言都要放进 pump 的 step 里。
	reported := false
	pumpSettled(t, v, fake, func() bool {
		if !reported {
			ReportAppState("active")
			ReportAppState("background")
			ReportAppState("background") // 重复 → 无事件
			reported = true
		}
		return jsArrayLen(t, v, "seen") >= 2
	})

	if got, _ := globalStr(t, v, "start"); got != "unknown" {
		t.Fatalf("start 不该变: %q", got)
	}
	if n := jsArrayLen(t, v, "seen"); n != 2 {
		t.Fatalf("onAppStateChange 应收到 2 次 (去重后), got %d", n)
	}
}

// ===== 双模 (回调优先, Promise 次之) =====

func TestDualModeCallbackPrecedence(t *testing.T) {
	host := setupHost(t, []string{"camera"})
	host.immediate("camera.takePhoto", NativeResult(mustMediaObj("c.jpg")))

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})

	v, err := vm.EvalVM(`
		import { takePhoto } from "gx/media";
		import { h, render } from "gx/gfx";
		globalThis.cbResult = "pending";
		globalThis.cbErr = "none";
		globalThis.ret = "?";
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 200, height: 150});
		globalThis.ret = String(typeof takePhoto({}, (err, file) => { cbErr = err ? err.errCode : "none"; cbResult = file ? file.path : "none"; }));
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	pumpSettled(t, v, fake, func() bool {
		r, _ := globalStr(t, v, "cbResult")
		return r != "pending"
	})
	// 传了回调 → 返回 undefined (不是 Promise)
	if r, _ := globalStr(t, v, "ret"); r != "undefined" {
		t.Fatalf("双模传回调时应返回 undefined, got %q", r)
	}
	if r, _ := globalStr(t, v, "cbResult"); r != "c.jpg" {
		t.Fatalf("回调结果不对: %q", r)
	}
	if r, _ := globalStr(t, v, "cbErr"); r != "none" {
		t.Fatalf("成功回调的 err 应为 none: %q", r)
	}
}

// ===== reportBattery / reportNetwork 上报链路 =====

func TestBatteryReportAndUse(t *testing.T) {
	setupHost(t, nil)
	resetDeviceStateForTest()

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})

	v, err := vm.EvalVM(`
		import { battery, useBattery, onBatteryChange, reportBattery, isCharging } from "gx/device";
		import { h, render } from "gx/gfx";
		globalThis.pct = -1;
		globalThis.charging = null;
		globalThis.events = 0;
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 200, height: 150});
		// 注意: 不能写 events++ —— 后置自增在 Go 侧 object.CallFunction 触发的
		// hook 闭包里会算出 NaN (VM 行为缺陷, 已用探针复现); 显式加法正常。
		onBatteryChange(() => { globalThis.events = globalThis.events + 1; });
		globalThis.pct = battery().levelPercent;
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	if p, _ := globalNum(t, v, "pct"); p != -1 {
		t.Fatalf("无上报时电池应 unknown (-1), got %v", p)
	}
	// 宿主上报 65%: ReportBattery → notifyNativeChanged → onBatteryChange 回调。
	// 回调经 object.CallFunction, 依赖 currentVM —— 只在事件循环 (pump) 轮次内
	// 有效; 在 EvalVM 之后裸调 (pump 外) 会被静默丢弃 (events 恒 0)。
	reported := false
	pumpSettled(t, v, fake, func() bool {
		if !reported {
			ReportBattery(BatteryState{Supported: true, Level: 0.65, Charging: true, ChargingType: "ac"})
			reported = true
		}
		n, _ := globalNum(t, v, "events")
		return n >= 1
	})
	if n, _ := globalNum(t, v, "events"); n != 1 {
		t.Fatalf("onBatteryChange 应触发 1 次, got %v", n)
	}
	if p, _ := globalNum(t, v, "pct"); p != -1 {
		t.Fatalf("battery() 是快照取值函数, 上报后旧值不该变: %v", p)
	}
	if c := Battery(); !c.Charging || c.Level != 0.65 || c.ChargingType != "ac" {
		t.Fatalf("电池快照不对: %+v", c)
	}
}

func TestNetworkReportDefaults(t *testing.T) {
	setupHost(t, nil)
	resetDeviceStateForTest()

	if n := Network(); n.Connected {
		t.Fatalf("无上报时不应 connected")
	}
	if n := Network(); n.Type != "none" {
		t.Fatalf("无上报时 type 应为 none, got %q", n.Type)
	}
	ReportNetwork(NetworkState{Connected: true, Type: "wifi", Strength: 80, Metered: false})
	if n := Network(); !n.Connected || n.Type != "wifi" || n.Strength != 80 {
		t.Fatalf("上报后快照不对: %+v", n)
	}
}

// ===== reportViewport 上报链路 =====

func TestViewportReportAndUse(t *testing.T) {
	setupHost(t, nil)
	resetViewportStateForTest()

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})

	v, err := vm.EvalVM(`
		import { insets, isSplit, widthClass, contentArea, reportViewport } from "gx/viewport";
		import { h, render } from "gx/gfx";
		globalThis.top = 0;
		globalThis.split = false;
		globalThis.wc = "";
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 200, height: 150});
		globalThis.top = insets().top;
		globalThis.split = isSplit();
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	if t0, _ := globalNum(t, v, "top"); t0 != 0 {
		t.Fatalf("无上报时 insets 应为 0, got %v", t0)
	}
	if s, _ := globalBool(t, v, "split"); s != false {
		t.Fatalf("无上报时不应是分屏: %v", s)
	}

	// 折叠屏分屏 + 状态栏上报
	ReportViewport(nil, Viewport{
		Insets:         Insets{Top: 48, Bottom: 24},
		Mode:           ViewportSplit,
		MultiWindow:    true,
		Stage:          "primary",
		SplitDirection: "horizontal",
		SplitRatio:     0.5,
		WidthClass:     SizeCompact,
		HeightClass:    SizeCompact,
	})
	if i := viewportFor(nil).Insets; i.Top != 48 || i.Bottom != 24 {
		t.Fatalf("insets 上报不对: %+v", i)
	}
	if !splitActive(viewportFor(nil)) {
		t.Fatalf("上报 split 后应判为分屏")
	}
	if wc := viewportFor(nil).WidthClass; wc != SizeCompact {
		t.Fatalf("widthClass 上报不对: %q", wc)
	}
}

// ===== 软能力 (无宿主时软降级, 不报错) =====

func TestSoftCapabilitiesNoHost(t *testing.T) {
	setupHost(t, nil)

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})

	v, err := vm.EvalVM(`
		import { vibrate, keepScreenOn, getBrightness, setBrightness, deviceId } from "gx/device";
		import { h, render } from "gx/gfx";
		globalThis.b = -2;
		globalThis.did = "?";
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 200, height: 150});
		vibrate(50);
		keepScreenOn(true);
		globalThis.b = getBrightness();
		setBrightness(0.5);
		globalThis.did = deviceId();
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	if b, _ := globalNum(t, v, "b"); b != -1 {
		t.Fatalf("无宿主时 getBrightness 应 -1, got %v", b)
	}
	// deviceId 无宿主 → 空串
	if d, _ := globalStr(t, v, "did"); d != "" {
		t.Fatalf("无宿主时 deviceId 应为空串, got %q", d)
	}
	_ = fake
}

// ===== 超时 =====

func TestCallTimeout(t *testing.T) {
	host := setupHost(t, []string{"location"})
	host.pending("location.get") // 永不回填

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})

	v, err := vm.EvalVM(`
		import { getLocation } from "gx/geo";
		import { h, render } from "gx/gfx";
		globalThis.err = null;
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 200, height: 150});
		getLocation({timeout: 30}).catch((e) => { err = e.errCode; });
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	pumpSettled(t, v, fake, func() bool {
		e, _ := v.Globals().Get("err")
		return e != nil && e != object.NullSingleton && e != object.UndefinedSingleton
	})
	e, _ := v.Globals().Get("err")
	s, ok := e.(*object.String)
	if !ok || s.Value != "timeout" {
		t.Fatalf("超时应报 timeout, got %v", e)
	}
}

// ===== watchLocation 流 =====

func TestWatchLocationStream(t *testing.T) {
	host := setupHost(t, []string{"location"})
	host.pending("location.watch")

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})

	v, err := vm.EvalVM(`
		import { watchLocation, clearWatch, clearAllWatches, lastLocation } from "gx/geo";
		import { h, render } from "gx/gfx";
		globalThis.fixes = [];
		globalThis.wid = "";
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 200, height: 150});
		globalThis.wid = watchLocation((loc, err) => { fixes.push(err ? "ERR:" + err.errCode : loc.latitude); });
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	// watchLocation 同步返回 id (不是 pending 的 "pending" 字符串)
	if w, _ := globalStr(t, v, "wid"); w == "" || w == "pending" || w == "-1" {
		t.Fatalf("watchLocation 应同步返回 id, got %q (宿主收到的调用: %v)", w, host.gotMethod)
	}
	id, _ := v.Globals().Get("wid")

	// 宿主推送两次定位。
	//
	// 注意回填时序: ResolveNative 的 sink 经 resolveDeferred (SetTimeout 0) 投回
	// 事件循环, **下一轮**才执行。所以在 step 里"先看 fixes 再决定回填哪个"是
	// 不成立的 —— step 刚开始时上一轮的推送还没落进 fixes, n 总是滞后, 会把同一
	// 个 first 推两遍。改成"每个 step 只回填一次", 按轮次推进。
	first := object.NewObject()
	first.SetProperty("latitude", object.NewNumber(31.23))
	first.SetProperty("longitude", object.NewNumber(121.47))
	second := object.NewObject()
	second.SetProperty("latitude", object.NewNumber(31.24))
	second.SetProperty("longitude", object.NewNumber(121.48))

	var pushedFirst, pushedSecond bool
	pumpSettled(t, v, fake, func() bool {
		// 流式: 同一个 id 反复回填。每个 step 只推一次 (sink 经 resolveDeferred
		// 在下一轮才执行, step 内读 fixes 看不到增量), 按轮次推进。
		if !pushedFirst {
			host.resolveStream("location.watch", first, nil)
			pushedFirst = true
		} else if !pushedSecond {
			host.resolveStream("location.watch", second, nil)
			pushedSecond = true
		}
		return jsArrayLen(t, v, "fixes") >= 2
	})
	if a := jsArray(t, v, "fixes"); len(a.Elements) != 2 {
		t.Fatalf("流应收到 2 次推送, got %d", len(a.Elements))
	}
	// 第二次 fix 已写进 lastLocation
	if l := LastLocation(); l == nil || l.Latitude != 31.24 {
		t.Fatalf("lastLocation 未更新: %+v", l)
	}
	// 清除后宿主再推 → 忽略 + 出声 (不崩)
	stopNativeStream(valueText(id))
	if PendingNativeCalls() != 0 {
		t.Fatalf("清流后不应有 pending: %d", PendingNativeCalls())
	}
}

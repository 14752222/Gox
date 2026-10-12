// Package hosttest 是「宿主契约」的**平台无关**测试套件 (无 build tag)。
//
// ## 由来
//
// gfx/mobile 证明过一条分工: 平台相关部分退化成「读平台 API → 拼 JSON → 纯 Go
// 计算」, 于是真正会算错的逻辑全部能在桌面 `go test` 里跑完 (见 gfx/mobile/fold.go
// 文件头)。本包把同一手法推广到宿主契约: 契约语义 (能力声明口径 / 待决调用回填 /
// Surface 事件语义) 与平台无关, 不该散落在各平台包的注释里各自表述 —— 它们现在
// 只写在 gfx/native.go 与 gfx/gfx.go 的注释里, 而注释不会被 CI 执行。
//
// 所以这里把注释落成可执行断言, 两个入口:
//
//	RunNativeHostContract(t, host)      —— gfx.NativeHost 的实现 (宿主)
//	RunSurfaceContract(t, factory)      —— gfx.Surface 的工厂 (窗口后端)
//
// 平台侧各写一个最小测试调用它, 桌面参照: gfx/win32/host_contract_test.go。
//
// ## 断言清单与源码依据
//
// NativeHost (依据均为 gfx/native.go, 括注符号名而不是行号 —— 行号会漂移):
//  1. 声明形状: 方法名 = <能力>.<动作>, 能力 ID 取第一个点之前那段
//     (nativeMethodCapability); 空白项/带首尾空白/空能力段都是死条目
//     (SetNativeHost 注册时会 Trim + 丢空, 声明表与内核快照对不上)。
//  2. 声明稳定: 内核注册时只快照一次 (SetNativeHost), Capabilities() 两次回答
//     不一致 ⇒ canIUse 的结论是注册时机 lottery。
//  3. 纯上报型能力 (battery/network) 不得出现在声明里: 内核从不 Call 它们
//     (native_device.go 文件头「推送型不需要宿主实现任何 Call」), 声明了只会让
//     canIUse 说一个对应不到任何 Call 的 true (win32/host_test.go 同名单测有
//     完整推理)。insets/viewport 的能力名声明是 native.go 接口注释明确允许的
//     另一回事 (「由别的模块实现、但我能确认平台支持」), 不在禁止之列。
//  4. 声明即可用: 声明的每个**方法名**实际 Call 一次, 不得 panic、不得返回
//     ErrUnsupported —— 「可以多报、不许少报」的反面就是「声明了却自认没实现」,
//     这会让 canIUse 说谎 (NativeHost 接口注释)。Pending 是合法应答 (异步回填);
//     ErrUnavailable 等运行时态放行但会 Logf 记录 (「当前不可用」≠「未实现」)。
//     能力名形态 (无点) 不 Call —— 内核也不会 Call 它。
//  5. canIUse 一致性: 经真实注册路径 (SetNativeHost), 每个声明项与其能力 ID
//     都必须能被 gfx.CanIUse 查到 (双向匹配, 见 CanIUse 注释)。
//  6. SetNativeHost(nil) 必须把所有待决调用立刻 reject(unsupported)
//     (SetNativeHost 注释: 否则 Promise 永久 pending, 症状是「await 之后再也没
//     动静」)。这条验证的是**内核侧**的承诺, 与被测 host 无关: 套件用自己的探针
//     宿主经 gx/geo 真实脚本路径制造一次待决调用再拆卸宿主。
//
// Surface (依据均为 gfx/gfx.go 的 Surface / waker 注释):
//  1. Size() 返回正的客户区尺寸。
//  2. Show 整帧 / ShowRegions 脏矩形 / rects 为空 (nil 或空切片) = 全帧, 三种
//     形态都必须被接受。黑盒只能断言「接受」, 像素级正确性是后端内部的事。
//  3. WaitEvents 返回 false 的唯一含义是「事件源关闭 (窗口销毁)」; 窗口活着时
//     超时醒来必须返回 true, 且 maxWait 是上限不是建议值。关闭方向黑盒无法触发
//     (Surface 接口没有 Close), 由自测的假 Surface 演示。
//  4. waker 是可选接口: 未实现是合法退化 (「WaitEvents + postDrainCap 上限」)。
//     实现了就必须把 wake **真正并入**等待集合 —— 断言「wake 投递后 1s 内醒」,
//     抓的是粗切片轮询 / 忽略 wake 的实现; 片长 <1s 的轮询黑盒测不出, 如实承认。
//
// ## 黑盒极限 (如实写明, 免得后人以为套件没覆盖)
//
//   - 字面意义的「少报」(宿主实际支持某方法却完全不声明) 黑盒**不可检测**:
//     契约无法枚举宿主没声明的方法。套件检测的是它对偶 —— 「声明了却做不到」
//     (断言 4) 与「声明了内核查不到」(断言 5), 负例自测证明这两条真的会红。
//   - 副作用方法 (弹系统 UI, 如 device.openSettings / camera.takePhoto): 契约
//     以空参数 Call, 移动端应 Pending (等用户), 桌面端若会立即执行副作用, 平台
//     测试可以用一个**带注释的包装**屏蔽该方法的执行 (参照 win32 的写法) ——
//     但包装只许屏蔽副作用, 不许用来掩盖 unsupported。
//
// ## 三端接入口径 (android / ios / harmony)
//
// 本包无 build tag, 桌面 `go test ./gfx/hosttest/` 即可自测。三端的接入**不是**
// 给本包加代码, 而是在平台包里各加一个带平台 build tag 的最小测试文件:
//
//	func TestNativeHostContract(t *testing.T) { hosttest.RunNativeHostContract(t, myHost) }
//	func TestSurfaceContract(t *testing.T)    { hosttest.RunSurfaceContract(t, myFactory.Create) }
//
// 套件本身只依赖 gfx/object/vm (纯 Go), 真正缺的工程是「在真机/模拟器上跑 Go
// test」(android 的 instrumented runner / harmony 的 hdc 推包), 那是真机工程,
// 不在本包范围。注意断言 5/6 会临时占用进程级宿主槽并在结束后拆卸
// (SetNativeHost(nil)); 平台测试若后续用例依赖单例宿主, 需自行重新注册。
package hosttest

import (
	"strings"
	"testing"
	"time"

	"github.com/14752222/Gox/gfx"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// failer 是契约断言的最小失败上报接口 (*testing.T 满足)。
//
// 负例自测用一个记录型实现直接跑内部的 checkXxx: 证明「断言真的会红」不需要
// 伪造 *testing.T (testing.T 没有可用的外部构造手法)。
type failer interface {
	Helper()
	Logf(format string, args ...interface{})
	Errorf(format string, args ...interface{})
	Fatalf(format string, args ...interface{})
}

// RunNativeHostContract 对宿主实现跑全套 NativeHost 契约断言 (清单见包注释)。
func RunNativeHostContract(t *testing.T, host gfx.NativeHost) {
	t.Helper()
	if host == nil {
		t.Fatalf("hosttest: 传入 nil host —— 契约测的是真实宿主实现")
	}
	t.Run("capabilities-shape", func(t *testing.T) {
		checkCapabilitiesShape(t, host.Capabilities())
	})
	t.Run("capabilities-stable", func(t *testing.T) {
		checkCapabilitiesStable(t, host)
	})
	t.Run("no-report-only-capabilities", func(t *testing.T) {
		checkNoReportOnly(t, host.Capabilities())
	})
	t.Run("declared-methods-usable", func(t *testing.T) {
		checkDeclaredUsable(t, host)
	})
	t.Run("can-i-use-consistency", func(t *testing.T) {
		checkCanIUseConsistency(t, host)
	})
	t.Run("set-nil-rejects-pending", checkSetNilRejectsPending)
}

// capabilityOf 与 gfx 的 nativeMethodCapability 同一规则: 第一个点之前那段;
// 没点按能力名 (纯上报型/确认型)。
func capabilityOf(method string) string {
	if i := strings.IndexByte(method, '.'); i > 0 {
		return method[:i]
	}
	return method
}

// checkCapabilitiesShape 断言声明表的每一项都是合法形状 (依据: 命名约定 +
// SetNativeHost 注册时的 Trim/丢空过滤 —— 声明表长这样, 内核快照就是另一个样,
// 两张表对不上是「各写一份必然先分叉后矛盾」的温床)。
func checkCapabilitiesShape(f failer, caps []string) {
	f.Helper()
	for _, c := range caps {
		trimmed := strings.TrimSpace(c)
		if trimmed == "" {
			f.Errorf("Capabilities() 含空白项 %q: 内核注册时会静默丢掉, 留着是死条目", c)
			continue
		}
		if trimmed != c {
			f.Errorf("Capabilities() 项 %q 带首尾空白: 内核注册时会 Trim, 宿主自述与内核快照对不上", c)
		}
		if strings.HasPrefix(trimmed, ".") || strings.HasSuffix(trimmed, ".") {
			f.Errorf("Capabilities() 项 %q 形状非法: 能力 ID 取第一个点之前那段, 它会推导出空能力/空动作", c)
		}
	}
}

// checkCapabilitiesStable 断言 Capabilities() 是稳定回答 (依据: SetNativeHost
// 注册时只取一次快照)。
func checkCapabilitiesStable(f failer, host gfx.NativeHost) {
	f.Helper()
	first := host.Capabilities()
	second := host.Capabilities()
	if !sameStringMultiset(first, second) {
		f.Errorf("Capabilities() 两次调用结果不一致: %v vs %v —— 内核注册时只快照一次, 漂移的回答让 canIUse 变成注册时机 lottery", first, second)
	}
}

func sameStringMultiset(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	count := make(map[string]int, len(a))
	for _, s := range a {
		count[s]++
	}
	for _, s := range b {
		count[s]--
		if count[s] < 0 {
			return false
		}
	}
	return true
}

// reportOnlyCaps 是纯上报型能力 ID: 内核从不 Call 宿主, 数据由 ReportBattery /
// ReportNetwork 喂缓存 (native_device.go 文件头)。声明它们 = 骗 canIUse。
var reportOnlyCaps = []string{"battery", "network"}

// checkNoReportOnly 断言纯上报型能力不出现在声明里 (任何形态: 能力名或方法名)。
func checkNoReportOnly(f failer, caps []string) {
	f.Helper()
	for _, c := range caps {
		id := capabilityOf(strings.TrimSpace(c))
		for _, forbidden := range reportOnlyCaps {
			if id == forbidden {
				f.Errorf("Capabilities() 不得声明纯上报型能力 %q (声明项 %q): 内核从不 Call 它 (数据走 Report 通道), 这个声明只会骗 canIUse", forbidden, c)
			}
		}
	}
}

// checkDeclaredUsable 断言「声明了就必须真能用」(依据: NativeHost 接口注释
// 「可以多报 … 但不许少报」的反面 —— 声明了却 Call 出 unsupported, canIUse 就说谎)。
func checkDeclaredUsable(f failer, host gfx.NativeHost) {
	f.Helper()
	for _, m := range host.Capabilities() {
		m = strings.TrimSpace(m)
		if m == "" || !strings.Contains(m, ".") {
			continue // 能力名形态: 内核也不会 Call, 跳过
		}
		res, panicked := callSafely(host, m)
		if panicked != nil {
			f.Errorf("声明的方法 %q 被 Call 时 panic: %v", m, panicked)
			continue
		}
		if res.Err != nil && res.Err.Code == gfx.ErrUnsupported {
			f.Errorf("声明的方法 %q 返回 unsupported: 与 Capabilities() 的声明直接矛盾 (canIUse(%q) 会说 true) —— 要么实现它, 要么从声明表删掉", m, capabilityOf(m))
			continue
		}
		if res.Err != nil {
			f.Logf("声明的方法 %q 返回 %s (%s): 运行时态, 放行 (「当前不可用」≠「未实现」)", m, res.Err.Code, res.Err.Msg)
		}
	}
}

// callSafely 把宿主 Call 的 panic 转成返回值 —— panic 本身也是契约违规
// (宿主崩溃不该撕碎 GUI 线程), 需要单独的错误消息而不是裸露的栈。
func callSafely(host gfx.NativeHost, method string) (res gfx.NativeCallResult, panicked interface{}) {
	defer func() { panicked = recover() }()
	return host.Call(method, object.NewObject()), nil
}

// checkCanIUseConsistency 经真实注册路径验证: 宿主的每个声明项在内核视角都
// 能查到 (依据: CanIUse 判据 1「宿主声明过 → 可用」+ 双向匹配)。
func checkCanIUseConsistency(f failer, host gfx.NativeHost) {
	f.Helper()
	gfx.SetNativeHost(host)
	defer gfx.SetNativeHost(nil)
	for _, c := range host.Capabilities() {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if !gfx.CanIUse(c) {
			f.Errorf("声明项 %q 注册后 CanIUse 仍为 false (内核快照丢了它?)", c)
		}
		if id := capabilityOf(c); id != c && !gfx.CanIUse(id) {
			f.Errorf("声明方法 %q 后 CanIUse(%q) 应为 true (CanIUse 注释的双向匹配)", c, id)
		}
	}
}

// pendingProbeHost 是 checkSetNilRejectsPending 的探针宿主: 声明 location
// 能力, 任何 Call 都 Pending 并记下 __id (永不回填, 专等宿主被拆卸)。
type pendingProbeHost struct{ id string }

func (p *pendingProbeHost) Capabilities() []string { return []string{"location"} }

func (p *pendingProbeHost) Call(method string, args object.Value) gfx.NativeCallResult {
	if o, ok := args.(*object.Object); ok {
		if v, _ := o.GetProperty("__id"); v != nil {
			p.id = object.ToString(v)
		}
	}
	return gfx.NativePending()
}

// checkSetNilRejectsPending 验证内核侧的承诺: SetNativeHost(nil) 把所有待决
// 调用立刻 reject(unsupported) (依据: SetNativeHost 注释 —— 否则那些 Promise
// 永久 pending, 症状是「await 之后再也没动静」, 比报错难查得多)。
//
// 与被测 host 无关 (它验证的是内核), 所以套件用自己的探针宿主; 经 gx/geo 的
// 真实脚本路径制造待决调用, 是为了连「__id 传给宿主」这一环也一起钉住。
func checkSetNilRejectsPending(t *testing.T) {
	t.Helper()
	object.GlobalScheduler().ClearAll()
	fake := newFakeSurface()
	gfx.SetDefaultFactory(&fakeFactory{s: fake})
	t.Cleanup(func() {
		gfx.SetNativeHost(nil)
		gfx.SetDefaultFactory(nil)
		object.GlobalScheduler().ClearAll()
	})

	probe := &pendingProbeHost{}
	gfx.SetNativeHost(probe)

	v, err := vm.EvalVM(`
		import { getLocation } from "gx/geo";
		import { h, render } from "gx/gfx";
		globalThis.rejected = "";
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 200, height: 150});
		getLocation().catch((e) => { globalThis.rejected = e.errCode; });
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	if probe.id == "" {
		t.Fatalf("探针宿主没收到 __id (ResolveNative 注释: id 来自参数对象的 __id 字段, 宿主照抄即可)")
	}
	if n := gfx.PendingNativeCalls(); n != 1 {
		t.Fatalf("脚本返回后应有 1 个待决调用, 实际 %d", n)
	}

	// 契约语义: 拆卸宿主 ⇒ 待决调用立刻以 unsupported 结算 (经 resolveDeferred
	// 投回事件循环, 所以先看到待决表清空, 再在泵轮次里看到 catch 跑到)。
	gfx.SetNativeHost(nil)
	if n := gfx.PendingNativeCalls(); n != 0 {
		t.Fatalf("SetNativeHost(nil) 后待决表应为空, 实际 %d —— 那些 Promise 将永久 pending", n)
	}
	pumpUntil(t, v, fake, func() bool { return globalString(v, "rejected") != "" })
	if got := globalString(v, "rejected"); got != gfx.ErrUnsupported {
		t.Fatalf("拆卸宿主后 Promise 应以 %q 结算, 实际 %q", gfx.ErrUnsupported, got)
	}
}

// pumpUntil 跑事件循环直到 done() 为真 (与 gfx 包 native_e2e_test 的
// pumpSettled 同一手法: 每轮上限 10ms 轮询, 达标或超轮次后推 EventClose 收尾)。
func pumpUntil(t *testing.T, v *vm.VM, fake *fakeSurface, done func() bool) {
	t.Helper()
	round := 0
	const maxRounds = 200
	err := v.RunTimersWithPump(func(maxWait time.Duration) bool {
		round++
		if done() || round >= maxRounds {
			fake.push(gfx.Event{Kind: gfx.EventClose})
		}
		const pollWait = 10 * time.Millisecond
		if maxWait <= 0 || maxWait > pollWait {
			maxWait = pollWait
		}
		return gfx.Pump(maxWait)
	})
	if err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if !done() {
		t.Fatalf("事件循环结束但断言未达成 (转了 %d 轮)", round)
	}
}

// globalString 读脚本全局字符串 (缺失/非字符串 → 空)。
func globalString(v *vm.VM, name string) string {
	val, ok := v.Globals().Get(name)
	if !ok {
		return ""
	}
	s, ok := val.(*object.String)
	if !ok {
		return ""
	}
	return s.Value
}

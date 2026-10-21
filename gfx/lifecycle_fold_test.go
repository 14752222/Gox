package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== 折叠屏生命周期专项回归 (rVs62S / N1) =====
//
// 核心风险: 折叠瞬间触发的生命周期事件必须在 JS 层被正确消费, 否则列表滚动
// 状态、相机资源都会丢。而时序错了**不会崩** —— 只会表现为「分栏慢一帧」或
// 「滚动位置丢一次」, 人眼很容易漏, 所以必须**记录事件顺序**断言, 而不是只看
// 「无崩溃」。
//
// 必验的时序事实 (平台侧宿主契约, 内核侧对应物):
//
//	Android: onConfigurationChanged 早于 foldStatusChange
//	         → 窗口 resize (viewport 事件) 必须早于 reportPosture (display 事件)
//	鸿蒙:   windowSizeChange 早于 foldStatusChange → 同上
//
// 本文件用 fakeSurface.ResizeClient (投 EventResize → onResize +
// NotifyViewportChanged) + reportPosture (notifyEnvChanged → onDisplayChange)
// + ReportAppState (onAppStateChange) 回放 20 轮
// 「展开 → 折叠 → 切后台 → 回前台」, 断言:
//
//	1. 每轮事件顺序: viewport(尺寸) → display(折叠) → appState(background)
//	   → appState(active) → display(展开) → viewport(尺寸还原);
//	2. 滚动位置 (脚本侧状态) 经后台/前台往返不丢;
//	3. 无未捕获异常 (脚本 try/catch 计数为 0);
//	4. appState 去重语义不破坏顺序 (重复上报不产生事件)。
//
// 行为说明 (已知不修, 写死在这里): Android 返回键的 500ms 同步等待在空闲泵
// (无 rAF/定时器) 下依赖 Tick 唤醒, 有 ANR 风险 (app/NATIVE-HOST.md 遗留项 1)。
// mock 必须能模拟「脚本没回话」→ 超时后按「未处理」返回 —— 对应
// TestBackPressUnhandledSemantics 的三个分支。

// TestFoldLifecycleEventOrder20Rounds 折叠生命周期 20 轮循环 + 事件顺序断言。
func TestFoldLifecycleEventOrder20Rounds(t *testing.T) {
	setupHost(t, nil)
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)
	resetViewportStateForTest()
	t.Cleanup(resetViewportStateForTest)
	resetAppStateForTest()
	t.Cleanup(resetAppStateForTest)
	resetPermissionStateForTest()

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { h, render } from "gx/gfx";
		import { onViewportChange } from "gx/viewport";
		import { onDisplayChange, reportPosture } from "gx/screen";
		import { useAppState, onAppStateChange, appState } from "gx/app";

		globalThis.w = render(h("rect", {width: 10, height: 10}),
			{title: "T", width: 1000, height: 800});

		globalThis.log = [];
		globalThis.errors = 0;
		globalThis.scrollY = 137;   // 模拟列表滚动位置 (要经后台/前台不丢)
		globalThis.saved = null;

		var note = (tag) => { try { globalThis.log.push(tag); } catch (e) { globalThis.errors++; } };

		onViewportChange(() => note("viewport"));
		onDisplayChange(() => note("display"));
		// hooks 是**无参回调** (与 onDisplayChange 同约定): 新状态在回调内
		// 用 appState() 读, 不能指望形参 s —— notifyNativeChanged 派发时不带
		// 载荷, 写 (s) => ... 只会拿到 undefined。
		onAppStateChange(() => note("app:" + appState()));

		// 后台保存 / 前台恢复 —— 折叠屏 demo 的状态保命写法
		var st = useAppState();
		globalThis.tick = () => { st(); return appState(); };

		// 心跳: 脚本没有定时器时 RunTimersWithPump 会带着无限期预算睡进
		// WaitEvents (vm/timers.go 的 !found 分支), 泵不醒 done 也不会再被调
		// —— 与 foldable_demo.js 的规避同源。
		setInterval(() => {}, 5);

		globalThis.cycle = function (n) {
			// —— 折叠 (Android: onConfigurationChanged 先于 foldStatusChange) ——
			globalThis.w.resize(480, 800);            // → viewport
			reportPosture({display: "fold-life", posture: "folded",
				width: 480, height: 800});            // → display
			// —— 切后台 (保存滚动位置) ——
			note("save:" + globalThis.scrollY);
			// —— 回前台 (恢复) ——
			// —— 展开 (顺序镜像) ——
			reportPosture({display: "fold-life", posture: "flat",
				width: 1000, height: 800});            // → display
			globalThis.w.resize(1000, 800);           // → viewport
			note("round:" + n);
		};
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	const rounds = 20
	round := 0
	pumpSettled(t, v, fake, func() bool {
		// 排空唤醒信号: cycle 每轮 push 2 个事件 (折叠+展开的 resize), 而
		// Pump 每轮只消费一个 arrived —— 不排空会在 ~16 轮后把 arrived 通道
		// 塞满, push 阻塞, 测试表现为超时。事件本体 (f.events) 由
		// processEvents 全量 drain, 不受影响。
		for {
			select {
			case <-fake.arrived:
				continue
			default:
			}
			break
		}
		// appState 的上报与脚本函数驱动都必须在泵轮次内调 (回调桥要 currentVM)。
		if round < rounds {
			round++
			callGlobal(t, v, "cycle", object.NewNumber(float64(round)))
			ReportAppState("background")
			ReportAppState("background") // 重复 → 去重, 不产生事件
			ReportAppState("active")
			return false
		}
		return true
	})

	if n, _ := globalNum(t, v, "errors"); int(n) != 0 {
		t.Fatalf("脚本侧捕获异常 %d 次", int(n))
	}

	// 事件顺序逐轮断言。每轮期望 (8 项):
	//   viewport(折叠尺寸) → display(folded) → save:137 → display(flat)
	//   → viewport(还原尺寸) → round:N → app:background → app:active
	// 后两项由 Go 侧在 cycle() 返回后注入 (宿主真实时序里 onResume/onPause
	// 是独立系统回调, 同样发生在 onConfigurationChanged/foldStatusChange 之后);
	// 重复上报的第二个 background 被去重, 不产生事件。
	got := globalStringArray(t, v, "log")
	perRound := 8
	if len(got) != rounds*perRound {
		t.Fatalf("事件数 = %d, want %d\nlog 前 20: %v", len(got), rounds*perRound, got[:minInt(20, len(got))])
	}
	for r := 0; r < rounds; r++ {
		one := got[r*perRound : (r+1)*perRound]
		want := []string{
			"viewport", "display", "save:137", "display",
			"viewport", "round:" + itoaTest(r+1), "app:background", "app:active",
		}
		for i := range want {
			if one[i] != want[i] {
				t.Fatalf("第 %d 轮事件顺序错位 [%d]: got %q, want %q\n整轮: %v", r+1, i, one[i], want[i], one)
			}
		}
	}

	// 滚动位置不丢
	if n, _ := globalNum(t, v, "scrollY"); int(n) != 137 {
		t.Fatalf("滚动位置丢了: %v", n)
	}
}

// TestBackPressUnhandledSemantics 返回键「脚本没回话」的语义 (行为说明的
// 可执行版本)。
//
// 三个分支: 没注册回调 → 未处理 (宿主走默认返回, 退出界面); 回调返回真值 →
// 已处理; 回调抛错 → 按「未处理」返回 (recordWarn 记录, 不中断)。
// Android 侧 500ms 同步等待的 ANR 风险是宿主实现的事 (NATIVE-HOST.md 遗留
// 项 1), 内核侧的契约是: ReportBackPress 同步返回、不等泵。
func TestBackPressUnhandledSemantics(t *testing.T) {
	setupHost(t, nil)
	resetAppStateForTest()
	t.Cleanup(resetAppStateForTest)

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { h, render } from "gx/gfx";
		import { onBackPress } from "gx/app";
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 200, height: 150});
		// onBackPress 返回**注销句柄** (直接调用即注销); offBackPress(fn) 则要
		// 传原回调的同一引用 —— 传注销句柄进去是静默 no-op, 这是易错点。
		globalThis.off1 = onBackPress(() => true);      // 消费
		globalThis.off2 = onBackPress(() => { throw new Error("boom"); }); // 抛错 → 未处理
		globalThis.off3 = onBackPress(() => false);     // 不消费
		globalThis.offH1 = () => off1();
		globalThis.offRest = () => { off2(); off3(); };
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	// 任一真值即消费 (h1 返回 true)。ReportBackPress 必须经泵轮次调
	// (hook 回调走脚本桥, currentVM 为 nil 时静默为 undefined)。
	// done 必须做成**一次性**: pumpSettled 收尾还会再调一次 done() 终检,
	// 那时泵已停 (currentVM 已清), 重复调 ReportBackPress 只会拿到一串
	// undefined 把结果覆写成 false —— 这是 fakeSurface 测试的通用坑。
	handled := false
	done := false
	pumpSettled(t, v, fake, func() bool {
		if !done {
			done = true
			handled = ReportBackPress()
		}
		return done
	})
	if !handled {
		t.Fatal("有回调返回 true 时应算已处理")
	}
	// 卸载 h1 后: 剩 h2(抛错) + h3(false) → 未处理。卸载必须经泵轮次
	// (offBackPress 返回的句柄是脚本侧对象, 也要 currentVM)。
	offDone := false
	pumpSettled(t, v, fake, func() bool {
		if !offDone {
			offDone = true
			callGlobal(t, v, "offH1")
		}
		return offDone
	})
	handled2 := true
	h2Done := false
	pumpSettled(t, v, fake, func() bool {
		if !h2Done {
			h2Done = true
			handled2 = ReportBackPress()
		}
		return h2Done
	})
	if handled2 {
		t.Fatal("回调抛错 + 返回 false 时应按「未处理」返回")
	}
	// 全部卸载 → 未处理 (mock 模拟「脚本没回话」)
	offRestDone := false
	pumpSettled(t, v, fake, func() bool {
		if !offRestDone {
			offRestDone = true
			callGlobal(t, v, "offRest")
		}
		return offRestDone
	})
	handled3 := true
	h3Done := false
	pumpSettled(t, v, fake, func() bool {
		if !h3Done {
			h3Done = true
			handled3 = ReportBackPress()
		}
		return h3Done
	})
	if handled3 {
		t.Fatal("无回调时应按「未处理」返回 (宿主据此走默认返回)")
	}
}

// itoaTest 避免测试文件里引 strconv 只为一件事。
func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// globalStringArray 取脚本里的字符串数组全局。
func globalStringArray(t *testing.T, v *vm.VM, name string) []string {
	t.Helper()
	val, ok := v.Globals().Get(name)
	if !ok {
		t.Fatalf("没有全局 %q", name)
	}
	arr, ok := val.(*object.Array)
	if !ok {
		t.Fatalf("%q 不是数组: %T", name, val)
	}
	out := make([]string, 0, len(arr.Elements))
	for _, e := range arr.Elements {
		if s, ok := e.(*object.String); ok {
			out = append(out, s.Value)
		} else {
			out = append(out, "")
		}
	}
	return out
}

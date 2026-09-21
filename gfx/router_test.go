package gfx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== gx/router 端到端用例 (2026-09-21) =====
//
// 覆盖: 路由注册与匹配 / 参数路由 / 懒加载 (含组件级守卫从模块导出) /
// 三级守卫 (全局 beforeEach / 路由级 beforeEnter / 组件级 beforeRouteLeave·Enter) /
// 历史栈 (push·back) / keepAlive 与状态袋。

// runRouterDemo 读入 testdata 下的演示脚本并逐轮注入事件。
//
// 与共享的 runDemoSteps 只差**一处**: 它额外把模块基准路径设成 testdata。
// 演示里的懒加载写的是 `import("./router_page_detail.js")` —— 相对脚本自身
// 所在目录, 而 EvalVM 不带基准路径 (那是 EvalFile 的行为)。等价于用
// EvalFile 跑一遍, 于是"用户在仓库根目录 go run . testdata/router_demo.js
// 看到的效果"与用例里跑的是同一条解析路径。
//
// 之所以不放进 TestExampleScriptsMount 的清单: 那个用例的 cwd 是 gfx/,
// 懒加载模块的路径会解析不到 (与 image_demo.js / storage_demo.js 同一情形,
// 都由专职用例覆盖)。
func runRouterDemo(t *testing.T, name string, steps []func(*vm.VM, *GuiNode, *fakeSurface)) *vm.VM {
	t.Helper()
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() {
		object.GlobalScheduler().ClearAll()
		// 路由实例表与屏幕状态都是**包级单例**: 不清理会让下一个用例的
		// 键绑定/视图认领挂到上一个用例的 router 上 (与窗口注册表同一纪律)。
		resetRouterStateForTest()
		resetScreenStateForTest()
	})

	src, err := os.ReadFile(filepath.Join("..", "testdata", name))
	if err != nil {
		t.Fatalf("读取脚本: %v", err)
	}
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(string(src))
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	v.SetModuleBase(filepath.Join("..", "testdata"))

	appMu.Lock()
	root := activeApp.root
	appMu.Unlock()

	round := 0
	pump := func(maxWait time.Duration) bool {
		round++
		// 每轮补一个无害唤醒 (语义同 runDemoSteps): 只做断言的步骤自己不产生
		// 事件, 事件泵会按 WaitEvents 的语义白等一场 (假 Surface 是 10 秒)。
		fake.push(Event{Kind: EventMouseLeave})
		if round-1 < len(steps) {
			steps[round-1](v, root, fake)
		} else {
			fake.push(Event{Kind: EventClose})
		}
		return Pump(maxWait)
	}
	if err := v.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if Active() {
		t.Fatalf("关闭后应用未退出")
	}
	return v
}

// clickRoute 在下一轮 pump 前点一次"指向 path 的链接"。
//
// 定位靠 RouterLink 埋的 __routeTo 内省属性 (而不是文案): 文案会随演示调整,
// 而路由目标才是这个控件真正的身份。
func clickRoute(t *testing.T, root *GuiNode, fake *fakeSurface, path string) {
	t.Helper()
	link := findFirstWhere(root, func(n *GuiNode) bool {
		if n.Tag != "view" || n.PropHandler("onClick") == nil {
			return false
		}
		p, ok := n.PropStr("__routeTo")
		return ok && p == path
	})
	if link == nil {
		t.Fatalf("树上没有指向 %s 的链接", path)
	}
	click(fake, link)
}

// TestRouterDemoScript 走完整链路: 初始 → 懒加载页面 → 历史 → 守卫拦截 →
// 兜底路由 → keepAlive 回访。
//
// 每一轮 pump 之间只做一件事 (仓库既有纪律: 受控输入/布局变化都要等下一轮
// 才落回节点), 所以步骤看起来碎 —— 碎是刻意的。
func TestRouterDemoScript(t *testing.T) {
	runRouterDemo(t, "router_demo.js", []func(*vm.VM, *GuiNode, *fakeSurface){
		// 0. 初始: initial "/" → home 页
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "route: /") {
				t.Fatalf("顶部应显示 route: /")
			}
			if !textContainsAny(root, "home page") {
				t.Fatalf("初始应渲染 home 页")
			}
			clickRoute(t, root, fake, "/detail/7")
		},
		// 1. 懒加载页: 首次进入必须**当帧**出内容 (本 VM 的 import 是同步结算的),
		//    并且模块的命名导出 beforeRouteEnter 必须已经跑过。
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "detail id=7") {
				t.Fatalf("懒加载页应已渲染, 实际树里没有 detail id=7")
			}
			if !textContainsAny(root, "path: /detail/7") {
				t.Fatalf("页面里的 useRoute() 应读到自己的路径")
			}
			if got := jsArrayLen(t, v, "enterLog"); got != 1 {
				t.Fatalf("beforeRouteEnter 应跑过 1 次 (来自懒加载模块的命名导出), 实际 %d", got)
			}
			callGlobalInspect(t, v, "pushRoute", object.NewString("/list"))
		},
		// 2. list 页 (keepAlive): 点输入框拿焦点
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "list page") {
				t.Fatalf("应切到 list 页")
			}
			inp := findFirst(root, "input")
			if inp == nil {
				t.Fatalf("list 页应有输入框")
			}
			click(fake, inp)
		},
		// 3-4. 逐字符输入 (受控 input: 每个按键单独一轮)
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			fake.push(Event{Kind: EventKeyDown, Key: "h"})
		},
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			fake.push(Event{Kind: EventKeyDown, Key: "i"})
		},
		// 5. 草稿进到受控 value; 试进 /guard —— 路由级 beforeEnter 恒 false
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			inp := findFirst(root, "input")
			if inp == nil {
				t.Fatalf("list 页的输入框丢了")
			}
			if val, _ := inp.PropStr("value"); val != "hi" {
				t.Fatalf("草稿应为 hi, 实际 %q", val)
			}
			callGlobalInspect(t, v, "pushRoute", object.NewString("/guard"))
		},
		// 6. 被路由级守卫拦下: **路由不变**, 页面还是 list
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "list page") {
				t.Fatalf("被 beforeEnter 拦下时不该离开 list")
			}
			if !textContainsAny(root, "route: /list") {
				t.Fatalf("被拦截时路由应保持 /list")
			}
			navLogHas(t, v, "beforeEnter:/guard")
			callGlobalInspect(t, v, "navBack")
		},
		// 7. 后退回详情页 (组件级 beforeRouteLeave 应已跑过)
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "detail id=7") {
				t.Fatalf("后退应回到 detail 页")
			}
			if got := jsArrayLen(t, v, "leaveLog"); got != 1 {
				t.Fatalf("beforeRouteLeave (模块命名导出) 应跑过 1 次, 实际 %d", got)
			}
			callGlobalInspect(t, v, "navBack")
		},
		// 8. 再后退回首页
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "home page") {
				t.Fatalf("再后退应回到首页")
			}
			callGlobalInspect(t, v, "pushRoute", object.NewString("/list"))
		},
		// 9. **keepAlive 的关键断言**: 回到 /list 时用的是同一份页面缓存 ——
		//    输入框草稿还在 (子树没被重建), 状态袋里的 renders 也还停在 1。
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "list page") {
				t.Fatalf("应回到 list 页")
			}
			inp := findFirst(root, "input")
			if inp == nil {
				t.Fatalf("keepAlive 的 list 页应有输入框")
			}
			if val, _ := inp.PropStr("value"); val != "hi" {
				t.Fatalf("keepAlive 页面回来时草稿应保留, 实际 %q", val)
			}
			if !textContainsAny(root, "renders=1") {
				t.Fatalf("keepAlive 页面不该被重建 (renders 应停在 1)")
			}
			callGlobalInspect(t, v, "setAllowGuardHook", object.NewBoolean(false))
		},
		// 10. 全局守卫放行开关变成 false → /blocked 被 beforeEach 拦下
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			callGlobalInspect(t, v, "pushRoute", object.NewString("/blocked"))
		},
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "route: /list") {
				t.Fatalf("被全局守卫拦下时路由应保持 /list")
			}
			clickRoute(t, root, fake, "/deep/unknown")
		},
		// 11. 无匹配路径落到 "*" 兜底 (该链接的 __routeTo 解析成通配记录);
		//     顺带收尾断言历史栈形状 —— **必须在泵轮次内读** (主脚本执行结束后
		//     currentVM 为 nil, 全局函数调用会静默变成空操作, 读到 undefined)。
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "not found") {
				t.Fatalf("未匹配路径应落到 * 兜底页")
			}
			// 历史: [/, /detail/7, /list] → 后退两次到 /, 再 push /list 时截断
			// "前进"部分, 最终落到 /deep/unknown, 栈应为 [/, /list, /deep/unknown]。
			paths := callGlobalInspect(t, v, "historyPaths")
			for _, want := range []string{`"/"`, `"/list"`, `"/deep/unknown"`} {
				if !strings.Contains(paths, want) {
					t.Fatalf("历史栈里应有 %s, 实际 %s", want, paths)
				}
			}
			if strings.Contains(paths, "/guard") || strings.Contains(paths, "/blocked") {
				t.Fatalf("被拦截的导航不该进历史栈: %s", paths)
			}
			if strings.Contains(paths, "/detail/7") {
				t.Fatalf("push 应截断前进部分, 不该再留下 /detail/7: %s", paths)
			}
		},
	})
}

// navLogHas 断言守卫日志里出现过某条记录 (日志顺序在用例里逐条钉死太脆,
// 这里只钉"这一级守卫确实参与了这次导航")。
func navLogHas(t *testing.T, v *vm.VM, want string) {
	t.Helper()
	arr := jsArray(t, v, "navLog")
	for _, e := range arr.Elements {
		if s, ok := e.(*object.String); ok && s.Value == want {
			return
		}
	}
	t.Fatalf("守卫日志里没有 %q", want)
}

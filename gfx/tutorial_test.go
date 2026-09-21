package gfx

// ===== docs/tutorial.md 的示例回归 (2026-09-21) =====
//
// 教程文档最容易悄悄过期: 文档里的代码块不会报错, 只会让读者踩坑。所以本文件把
// **教程正文里出现的四个示例脚本**全部跑一遍 —— 与 testdata/ 下其它演示脚本同一
// 条纪律("示例脚本放在 testdata/ 并确保可直接运行")。
//
//   tutorial_api.js      无界面: 同步/异步宿主 API + 定时器 + 回环 HTTP
//   tutorial_modules.js  无界面: gx/* 与聚合入口 gox 的导入方式 + gx/storage
//   tutorial_gui.js      有界面: render + signal + model + each + show
//   tutorial_router.js   有界面: createRouter / RouterView / RouterLink / 守卫 / keepAlive
//
// 前两个不进 TestExampleScriptsMount 的共享循环: 一个会起 HTTP 服务与定时器
// (需要自己跑事件循环), 一个会真实写存储 (需要 GOX_STORAGE_DIR 隔离) —— 与
// image / storage / router 演示单独立用例是同一个理由。

import (
	"path/filepath"
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// TestTutorialHeadlessScripts 跑两个"无界面"示例。
//
// 走 EvalFileVM + RunTimers 而不是 EvalVM: 前者会把模块基准路径设成脚本所在目录
// (tutorial_modules.js 的 `./tutorial_util.js` 才解析得到), 并且这就是用户执行
// `gox testdata/tutorial_modules.js` 时的**同一条代码路径**。
func TestTutorialHeadlessScripts(t *testing.T) {
	for _, name := range []string{"tutorial_api.js", "tutorial_modules.js"} {
		t.Run(name, func(t *testing.T) {
			object.GlobalScheduler().ClearAll()
			t.Cleanup(func() { object.GlobalScheduler().ClearAll() })
			// gx/storage 的演示会真落盘 ⇒ 指到临时目录 (同 TestStorageDemoScript)
			t.Setenv("GOX_STORAGE_DIR", t.TempDir())

			v, err := vm.EvalFileVM(filepath.Join("..", "testdata", name))
			if err != nil {
				t.Fatalf("执行脚本: %v", err)
			}
			if err := v.RunTimers(); err != nil {
				t.Fatalf("事件循环: %v", err)
			}
		})
	}
}

// TestTutorialGuiScript 验证 §1 的最小 GUI 骨架: 挂载、首帧, 以及四条接线
// (响应式文本 / model 双向绑定 / each / show) 真的活着。
func TestTutorialGuiScript(t *testing.T) {
	runDemoSteps(t, "tutorial_gui.js", []func(*GuiNode, *fakeSurface){
		// 0. 初始帧: 计数为 0; each 已展开; show 的分支**懒构建** (没显示过就不在树上)
		func(root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "count: 0") {
				t.Fatalf("初始应显示 count: 0")
			}
			if !textContainsAny(root, "· 第一行") {
				t.Fatalf("each 指令没有展开列表")
			}
			if textContainsAny(root, "detail panel") {
				t.Fatalf("show=false 的分支不该被构建 (懒构建失效?)")
			}
			inp := findFirst(root, "input")
			if inp == nil {
				t.Fatalf("找不到 input")
			}
			// model 指令必须在 h() 层把读写两个方向都补上, 漏掉 onInput 会"打不进字"
			if _, ok := inp.Props["value"]; !ok {
				t.Errorf("input 上没有 value —— model 没补上读方向")
			}
			if _, ok := inp.Props["onInput"]; !ok {
				t.Errorf("input 上没有 onInput —— model 没补上写方向")
			}
			click(fake, buttonWithText(root, "+1"))
		},
		// 1. 点 "+1" 后计数文本必须跟着变 (响应式子节点接对了)
		func(root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "count: 1") {
				t.Fatalf("点击 +1 后应为 count: 1")
			}
			click(fake, buttonWithText(root, "toggle"))
		},
		// 2. show 切到 true: 分支才被构建出来
		func(root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "detail panel") {
				t.Fatalf("toggle 之后 detail 分支应出现")
			}
			click(fake, buttonWithText(root, "reset"))
		},
		// 3. reset 把计数写回 0
		func(root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "count: 0") {
				t.Fatalf("reset 后应为 count: 0")
			}
		},
	})
}

// TestTutorialRouterScript 验证 §4 的路由示例: 路由表 / 参数路由 / 命名路由 /
// 兜底 / redirect 记录 / 路由级守卫的重定向 / keepAlive 的页面状态保留。
//
// 复用 runRouterDemo 的驱动 (它比 runDemoSteps 多给回调一个 *vm.VM, 因为路由
// 断言要靠 globalThis 上的 pushRoute 等钩子; 顺带会把 router/screen 的包级
// 单例状态清干净)。每一轮 pump 只做一件事 —— 布局与响应式都要等下一轮才落回节点。
func TestTutorialRouterScript(t *testing.T) {
	runRouterDemo(t, "tutorial_router.js", []func(*vm.VM, *GuiNode, *fakeSurface){
		// 0. initial "/" → home 页
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "route: /") {
				t.Fatalf("顶部应显示 route: /")
			}
			if !textContainsAny(root, "home page") {
				t.Fatalf("初始应渲染 home 页")
			}
			clickRoute(t, root, fake, "/detail/7")
		},
		// 1. 参数路由: :id 进 props.param; useRoute() 读到自己的路径
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "detail id=7") {
				t.Fatalf("应渲染 detail id=7")
			}
			if !textContainsAny(root, "path: /detail/7") {
				t.Fatalf("页面里的 useRoute() 应读到自己的路径")
			}
			callGlobalInspect(t, v, "pushRoute", object.NewString("/list"))
		},
		// 2. list 页 (keepAlive) 首次进入: renders=1
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "list page") {
				t.Fatalf("应切到 list 页")
			}
			if !textContainsAny(root, "renders=1") {
				t.Fatalf("首次进入 list 应为 renders=1")
			}
			callGlobalInspect(t, v, "pushRoute", object.NewString("/detail/3"))
		},
		// 3. 同一模板换参数: 参数跟着换
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "detail id=3") {
				t.Fatalf("应渲染 detail id=3")
			}
			callGlobalInspect(t, v, "pushRoute", object.NewString("/list"))
		},
		// 4. keepAlive 的关键断言: 回来时用的是同一份页面缓存 → 组件体没重跑
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "renders=1") {
				t.Fatalf("keepAlive 的页面回来时不该重跑组件体 (renders 应停在 1)")
			}
			if jsArrayLen(t, v, "navLog") == 0 {
				t.Fatalf("beforeEach / afterEach 没跑过")
			}
			callGlobalInspect(t, v, "pushRoute", object.NewString("/admin"))
		},
		// 5. 未登录: 路由级 beforeEnter 返回 "/" ⇒ 重定向回首页 (栈不留半截状态)
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "home page") {
				t.Fatalf("未登录访问 /admin 应被重定向回首页")
			}
			if !textContainsAny(root, "loggedIn = false") {
				t.Fatalf("登录开关应仍是 false")
			}
			callGlobalInspect(t, v, "setLoggedInHook", object.NewBoolean(true))
		},
		// 6. 登录后同一个守卫放行
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			callGlobalInspect(t, v, "pushRoute", object.NewString("/admin"))
		},
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "admin page") {
				t.Fatalf("登录后应能进 /admin")
			}
			callGlobalInspect(t, v, "pushRoute", object.NewString("/old"))
		},
		// 7. redirect 记录: /old 改派到 /list
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "list page") {
				t.Fatalf("/old 应重定向到 /list")
			}
			callGlobalInspect(t, v, "pushRoute", object.NewString("/nope/deep"))
		},
		// 8. "*" 兜底
		func(v *vm.VM, root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "not found") {
				t.Fatalf("未匹配路径应落到 * 兜底")
			}
			if !textContainsAny(root, "path: /nope/deep") {
				t.Fatalf("兜底页应读到原始路径")
			}
		},
	})
}

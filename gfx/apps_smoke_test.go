package gfx

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// apps/ 下的**实用应用**必须真的"跑得起来"。
//
// 为什么值得立这条闸门: apps/<name>/ 是多文件独立工程（main.js → ./app.js →
// ./store.js → ./components/*.js），Go 编译器完全不认识它们 —— 静态检查、`go vet`
// 全过、跑起来才现形的一类错误只能靠真挂载拦：入口用了 JSX 却漏 `import { h }`
// （挂载当场 ReferenceError: h is not defined）、相对 import 路径写错、模块作用域下
// 不适用的声明形态……
//
// 与 TestScaffoldTemplateProject 的分工:
//   - 脚手架用例断的是**模板的接线质量**（响应式 prop、model 双向、页签高亮、点击计数），
//     标签与路径写死，改模板就得跟着改；
//   - 本用例只断「能挂上、能画、能关」这条底线，且**用例表从文件系统自动发现**
//     ⇒ 新增应用不需要改这个文件，加了目录就自动纳入闸门。
//
// 走 EvalFileVM 而不是 EvalVM: 应用是多文件工程，只有前者会 SetModuleBase，
// 相对 import 才解析得到 —— 与真实 `gox apps/<name>/src/main.js` 同一条代码路径。
func TestAppProjectsMount(t *testing.T) {
	entries, err := filepath.Glob(filepath.Join("..", "apps", "*", "src", "main.js"))
	if err != nil {
		t.Fatalf("扫描 apps/: %v", err)
	}
	if len(entries) == 0 {
		// 一条防「静默零用例通过」的断言: glob 一失效，整个用例会变成空循环而恒绿。
		t.Fatal("apps/ 下一个应用都没扫到 —— 是路径变了，还是应用全没了?")
	}

	for _, entry := range entries {
		entry := entry
		// apps/<name>/src/main.js → <name>
		name := filepath.Base(filepath.Dir(filepath.Dir(entry)))
		t.Run(name, func(t *testing.T) {
			// 调度器是**进程级单例**，用例之间必须清干净，否则上一个应用的定时器
			// 会在这个用例里继续跑 —— 「单跑过、全集挂」的经典成因。
			object.GlobalScheduler().ClearAll()
			t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

			// seqFactory 每次 Create 造一个新的假 Surface —— 应用可能开多个窗口
			// （多屏 / 多窗口形态），fakeFactory 每次返回同一个，验不了那种应用。
			factory := &seqFactory{}
			SetDefaultFactory(factory)
			t.Cleanup(func() { SetDefaultFactory(nil) })

			v, err := vm.EvalFileVM(entry)
			if err != nil {
				t.Fatalf("挂载 %s 失败: %v", entry, err)
			}
			if len(factory.made) == 0 {
				t.Fatalf("%s 没有开任何窗口（顶层没调 render?）", name)
			}

			// ---- 每个窗口: 树建起来了 + 首帧真的画出了东西 ----
			for i, s := range factory.made {
				root := rootOfSurface(t, s)
				if root == nil {
					t.Fatalf("第 %d 个窗口没有布局根节点", i+1)
				}
				if root.Tag == "" {
					t.Fatalf("第 %d 个窗口的根节点没有标签（组件返回了空?）", i+1)
				}
				if n := countNodes(root); n < 3 {
					t.Errorf("第 %d 个窗口的元素树只有 %d 个节点，像是空界面", i+1, n)
				}
				// 抗锯齿会让颜色数远超「设计上的几种」，所以只能当「画没画上」的**粗判据**，
				// 不断言某个具体色值（与 scaffold 用例同一口径）。
				if colors := distinctColors(renderTree(root, 960, 700)); colors < 4 {
					t.Errorf("第 %d 个窗口首帧只有 %d 种颜色，像是没画上", i+1, colors)
				}
			}

			// ---- 收工: 分轮推 EventClose，直到没有活动窗口 ----
			//
			// 两条纪律（都踩过，细则见 gfx-invariants 的测试套路）:
			//   ① Pump 处理到 EventClose **立即返回 false**，排在后面的定时器任务会被丢掉
			//      ⇒ 多窗口 / 带定时器的应用要**分轮**推 close，不能一轮全指望；
			//   ② GUI 模式下「无定时器」时 runTimersLoop 收到 maxWait=0 的语义是**无限期等**，
			//      假 Surface 会把 0 当睡 10 秒 ⇒ 测试泵必须把等待钳到有限值。
			const (
				pollWait  = 10 * time.Millisecond
				maxRounds = 12
			)
			for round := 0; round < maxRounds && Active(); round++ {
				pushed := false
				pump := func(maxWait time.Duration) bool {
					if maxWait <= 0 || maxWait > pollWait {
						maxWait = pollWait
					}
					if !pushed {
						pushed = true
						for _, s := range factory.made {
							s.push(Event{Kind: EventClose})
						}
					}
					return Pump(maxWait)
				}
				if err := v.RunTimersWithPump(pump); err != nil {
					t.Fatalf("RunTimersWithPump: %v", err)
				}
			}
			if Active() {
				t.Errorf("推了 %d 轮 EventClose 仍有窗口没关掉（有应用没处理关窗 / 定时器不放行?）", maxRounds)
			}
		})
	}
}

package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== T15 示例应用三件套: testdata/apps/ 的挂载与交互冒烟 =====
//
// todo / snake 会真实读写 gx/storage (读在脚本加载时发生), 所以整体用
// GOX_STORAGE_DIR 指到临时目录 —— 与 TestStorageDemoScript 同一理由;
// dashboard 不碰存储, 但统一隔离没有副作用。
//
// 断言口径与 TestExampleScriptsMount 一致: 挂载不报错 + 首帧上屏,
// 外加每个应用一两个真实交互 (按键/点击), 保证"示例不是只能看的"。

func runAppSmoke(t *testing.T, name string, steps []func(*GuiNode, *fakeSurface)) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GOX_STORAGE_DIR", dir)
	runDemoSteps(t, "apps/"+name, steps)
}

func TestTodoAppSmoke(t *testing.T) {
	runAppSmoke(t, "todo.js", []func(*GuiNode, *fakeSurface){
		// 1) 初始两条任务; 点"添加" (输入框为空 → 不新增, 但按钮链路通)
		func(root *GuiNode, fake *fakeSurface) {
			btns := findAll(root, "button")
			if len(btns) < 5 {
				t.Fatalf("按钮数量 = %d, want >= 5 (添加 + 3 过滤 + 清已完成)", len(btns))
			}
			if !textContainsAny(root, "剩 2 项未完成 / 共 2 项") {
				t.Fatalf("初始统计文案未出现")
			}
			fake.push(Event{Kind: EventMouseUp, X: btns[0].Box.X + 2, Y: btns[0].Box.Y + 2})
		},
		// 2) 切到"未完成"过滤 → 行集不变 (两条都未完成); 统计行还在
		func(root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "剩 2 项未完成") {
				t.Fatalf("过滤后统计文案丢失")
			}
		},
	})
}

func TestDashboardAppSmoke(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	runDemoSteps(t, "apps/dashboard.js", []func(*GuiNode, *fakeSurface){
		// 1) 三张指标卡各有一块 canvas 折线图
		func(root *GuiNode, fake *fakeSurface) {
			if got := len(findAll(root, "canvas")); got != 3 {
				t.Fatalf("canvas 数量 = %d, want 3", got)
			}
			if !textContainsAny(root, "服务仪表盘") {
				t.Fatalf("标题未挂载")
			}
		},
		// 2) 点"暂停"按钮
		func(root *GuiNode, fake *fakeSurface) {
			btns := findAll(root, "button")
			if len(btns) != 1 {
				t.Fatalf("按钮数量 = %d, want 1", len(btns))
			}
			fake.push(Event{Kind: EventMouseUp, X: btns[0].Box.X + 2, Y: btns[0].Box.Y + 2})
		},
		// 3) 徽标文字应已切换 (show + 信号驱动; 事件在下一轮泵处理)
		func(root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "已暂停") || textContainsAny(root, "实时") {
				t.Fatalf("暂停后徽标未切换")
			}
		},
	})
}

func TestSnakeAppSmoke(t *testing.T) {
	runAppSmoke(t, "snake.js", []func(*GuiNode, *fakeSurface){
		// 1) 初始: 一块游戏画布 + 得分 0
		func(root *GuiNode, fake *fakeSurface) {
			if got := len(findAll(root, "canvas")); got != 1 {
				t.Fatalf("canvas 数量 = %d, want 1", got)
			}
			if !textContainsAny(root, "得分 0") || !textContainsAny(root, "最高 0") {
				t.Fatalf("得分区文案未挂载")
			}
		},
		// 2) 按下 ArrowUp: 不许 180° 掉头的规则下, 初速向右 → 转向上,
		//    再按 ArrowLeft 换向。两步之后游戏仍在跑 (没有 GAME OVER 文案)。
		func(root *GuiNode, fake *fakeSurface) {
			fake.push(Event{Kind: EventKeyDown, Key: "ArrowUp"})
			fake.push(Event{Kind: EventKeyDown, Key: "ArrowLeft"})
			if textContainsAny(root, "GAME OVER") {
				t.Fatalf("仅转向不该触发结束")
			}
		},
	})
}

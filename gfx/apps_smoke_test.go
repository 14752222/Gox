package gfx

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== apps/ 十应用集: 挂载冒烟 =====
//
// 口径与 TestScaffoldTemplateProject 一致: 每个应用的 src/main.js 走
// EvalFileVM(多文件工程, 只有它会 SetModuleBase) 在 fakeSurface 上真实挂载,
// 断言 "不报错 + 窗口挂上了 + 事件泵能跑 + 全部窗口能关掉"。
//
// 防的是一类"静态检查全过、跑起来才现形"的错误: 入口忘了 import h、
// import 路径写错、gx 模块名拼错 —— lint 与 go build 都抓不住, 只有真挂载会炸。
//
// 存储类应用 (m-notes / settings) 读写 gx/storage, 用 GOX_STORAGE_DIR 隔离;
// 定时器类应用 (sysmon / music) 注册 setInterval, 每个用例前后清调度器。

// appSmokeCase 描述一个应用的冒烟参数。
type appSmokeCase struct {
	dir     string   // apps/ 下的目录名
	minWins int      // 至少挂出的窗口数 (multiscreen 是 2)
	mustTag []string // 根树里必须出现的元素标签 (抽查, 不是全量)
}

var appSuite = []appSmokeCase{
	// 桌面 6
	{dir: "notes", minWins: 1, mustTag: []string{"column"}},
	{dir: "json-toolbox", minWins: 1, mustTag: []string{"column"}},
	{dir: "files", minWins: 1, mustTag: []string{"column", "scroll"}},
	{dir: "sysmon", minWins: 1, mustTag: []string{"canvas"}},
	{dir: "settings", minWins: 1, mustTag: []string{"switch", "select"}},
	{dir: "music", minWins: 1, mustTag: []string{"canvas", "slider"}},
	// 移动 3
	{dir: "m-notes", minWins: 1, mustTag: []string{"search", "input"}},
	{dir: "m-weather", minWins: 1, mustTag: []string{"canvas", "select"}},
	{dir: "m-calc", minWins: 1, mustTag: []string{"grid"}},
	// 跨端 1 (多屏: 两个窗口)
	{dir: "multiscreen", minWins: 2, mustTag: []string{"column"}},
	// 折叠屏 1 (四场景: 单双栏 / 折痕避让 / Tabletop / 跨屏拖拽)
	{dir: "foldable-demo", minWins: 1, mustTag: []string{"column", "row", "button"}},
}

func TestAppsSuiteSmoke(t *testing.T) {
	for _, tc := range appSuite {
		tc := tc
		t.Run(tc.dir, func(t *testing.T) {
			object.GlobalScheduler().ClearAll()
			t.Cleanup(func() { object.GlobalScheduler().ClearAll() })
			t.Setenv("GOX_STORAGE_DIR", t.TempDir())

			// 屏表也必须逐用例复位: 应用可以调 gx/screen 的 reportPosture 往
			// 宿主上报一块"折叠屏"(foldable-demo 就是这么演示的), 上报是**整表
			// 覆盖**且会一直留着 —— 不复位的话后面按真实屏断言的密度/DPI 用例
			// 会读到上一轮塞进来的假屏, 表现成一串"3x 屏上应为 48, got 16"的
			// 假失败 (与用例本身无关, 纯粹是串味)。
			ResetDisplaysForTest()
			t.Cleanup(ResetDisplaysForTest)

			factory := &seqFactory{}
			SetDefaultFactory(factory)
			t.Cleanup(func() { SetDefaultFactory(nil) })

			entry := filepath.Join("..", "apps", tc.dir, "src", "main.js")
			if _, err := os.Stat(entry); err != nil {
				t.Fatalf("入口不存在: %v", err)
			}
			v, err := vm.EvalFileVM(entry)
			if err != nil {
				t.Fatalf("挂载 %s 失败: %v", tc.dir, err)
			}

			appMu.Lock()
			a := activeApp
			appMu.Unlock()
			if a == nil {
				t.Fatalf("%s 没有挂载窗口", tc.dir)
			}
			if got := WindowCount(); got < tc.minWins {
				t.Fatalf("%s 窗口数 = %d, want >= %d", tc.dir, got, tc.minWins)
			}
			for _, tag := range tc.mustTag {
				if countTag(a.root, tag) == 0 {
					t.Errorf("%s 根树缺少 <%s>", tc.dir, tag)
				}
			}

			// 跑几轮事件泵: 让首帧落地 + 喂一次定时器, 然后逐窗关闭
			// (每个窗口一个假 Surface, EventClose 要逐面推)。
			round := 0
			pump := func(maxWait time.Duration) bool {
				round++
				if round <= 2 {
					for _, s := range factory.made {
						s.push(Event{Kind: EventMouseLeave})
					}
				} else {
					idx := round - 3
					if idx < len(factory.made) {
						factory.made[idx].push(Event{Kind: EventClose})
					}
				}
				return Pump(maxWait)
			}
			if err := v.RunTimersWithPump(pump); err != nil {
				t.Fatalf("%s 事件泵: %v", tc.dir, err)
			}
			if Active() {
				t.Fatalf("%s 关窗后应用未退出", tc.dir)
			}
		})
	}
}

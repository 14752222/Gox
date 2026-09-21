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

// ===== 多窗口 / 多屏 / 折叠屏用例 (2026-09-21) =====
//
// 这三件事的公共内核是"**会话按作用域隔离 + 环境信号驱动重算**", 所以放在
// 一个用例里走一遍能把它们的耦合点一次验清:
//
//	独立导航栈 —— A 里切页, B 的栈一个字节都不动
//	状态同步   —— sync 之后 A 的导航镜像到 B (路径同步 / 状态袋可共享)
//	折叠双栏   —— 半折时左右两栏并排, 恢复平展后回到单栏
//	重建保状态 —— 子树被销毁重建 (不保活的页), 状态袋里的值仍在
func TestRouterWindowDemoScript(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() {
		object.GlobalScheduler().ClearAll()
		resetRouterStateForTest()
		resetScreenStateForTest()
	})

	src, err := os.ReadFile(filepath.Join("..", "testdata", "router_window_demo.js"))
	if err != nil {
		t.Fatalf("读取脚本: %v", err)
	}
	factory := &displaySeqFactory{
		displays: []Display{
			{ID: "panel-0", Name: "panel-0", W: 900, H: 1000, WorkW: 900, WorkH: 1000, Scale: 1, Primary: true, Posture: postureFlat},
			{ID: "panel-1", Name: "panel-1", X: 900, W: 1600, H: 1000, WorkW: 1600, WorkH: 1000, Scale: 1, Posture: postureFlat},
		},
		of: map[Surface]string{},
	}
	SetDefaultFactory(factory)
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(string(src))
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	if WindowCount() != 2 {
		t.Fatalf("应有两个窗口, 实际 %d", WindowCount())
	}
	// 先确认两个窗口都真的挂上了树 (拿不到根就是"多窗口没落地")
	rootOfWindow(t, factory.made, 0)
	rootOfWindow(t, factory.made, 1)

	steps := []func(v *vm.VM, a, b *GuiNode){
		// 0. 两个窗口各自 initial "/", 且**各自一个作用域**
		func(v *vm.VM, a, b *GuiNode) {
			if !textContainsAny(a, "home page") || !textContainsAny(b, "home page") {
				t.Fatalf("两个窗口都应渲染 home 页")
			}
			if got := callGlobalInspect(t, v, "scopeDump"); !strings.Contains(got, "win:") ||
				!strings.Contains(got, "preview") {
				t.Fatalf("应有两个独立作用域 (窗口自动 + 显式具名), 实际 %s", got)
			}
			callGlobalInspect(t, v, "pushA", object.NewString("/list"))
		},
		// 1. **独立导航栈**: A 到了 list, B 一个字节没动
		func(v *vm.VM, a, b *GuiNode) {
			if !textContainsAny(a, "list page") {
				t.Fatalf("A 应切到 list")
			}
			if textContainsAny(b, "list page") {
				t.Fatalf("A 导航不该影响 B (独立导航栈), B 实际 %v", allTexts(b))
			}
			callGlobalInspect(t, v, "syncAB", object.NewString("mirror"))
			callGlobalInspect(t, v, "pushA", object.NewString("/note/1"))
		},
		// 2. 绑了同步组之后: A 的导航镜像给 B (路径同步)
		func(v *vm.VM, a, b *GuiNode) {
			if !textContainsAny(a, "note fresh") {
				t.Fatalf("A 应显示 note fresh (首次进入), 实际 %v", allTexts(a))
			}
			if !textContainsAny(b, "note fresh") {
				t.Fatalf("mirror 之后 B 应跟随到 /note/1, 实际 %v", allTexts(b))
			}
			// 离开这一页 (note 不保活 ⇒ 子树被销毁), 下一步再退回来
			callGlobalInspect(t, v, "pushA", object.NewString("/list"))
		},
		// 3. 离开后再**后退回同一条栈项**: 子树会重建, 但状态袋挂在栈项上
		func(v *vm.VM, a, b *GuiNode) {
			if !textContainsAny(a, "list page") {
				t.Fatalf("A 应在 list, 实际 %v", allTexts(a))
			}
			callGlobalInspect(t, v, "backA")
		},
		// 4. **状态袋跨子树重建**: note 页是新建的子树, 却读回了 seen 标记
		func(v *vm.VM, a, b *GuiNode) {
			if !textContainsAny(a, "note kept") {
				t.Fatalf("note 页重建后应从状态袋读回 seen (期望 note kept), 实际 %v", allTexts(a))
			}
			callGlobalInspect(t, v, "fold")
		},
		// 5. 折叠半开 → 双栏: 左栏是上一条 (list, keepAlive 保活), 右栏是当前页
		func(v *vm.VM, a, b *GuiNode) {
			if got := callGlobalInspect(t, v, "postureA"); got != "half-open" {
				t.Fatalf("A 所在屏的姿态应为 half-open, 实际 %s", got)
			}
			if got := callGlobalInspect(t, v, "postureB"); got != "flat" {
				t.Fatalf("B 在另一块屏上, 姿态应保持 flat (按屏隔离), 实际 %s", got)
			}
			if !textContainsAny(a, "note kept") {
				t.Fatalf("折叠后当前页应还在 (状态保持): %v", allTexts(a))
			}
			if !textContainsAny(a, "list page") {
				t.Fatalf("半折时应并排显示上一条 (list), 实际 %v", allTexts(a))
			}
			if textContainsAny(b, "list page") {
				t.Fatalf("折叠只影响姿态所在的那块屏: B 不该变双栏, 实际 %v", allTexts(b))
			}
			callGlobalInspect(t, v, "unfold")
		},
		// 6. 恢复平展 → 单栏 (左栏那条被摘掉), 当前页与状态保持
		func(v *vm.VM, a, b *GuiNode) {
			if !textContainsAny(a, "note kept") {
				t.Fatalf("恢复平展后当前页应保持, 实际 %v", allTexts(a))
			}
			if textContainsAny(a, "list page") {
				t.Fatalf("平展时应回到单栏, 实际 %v", allTexts(a))
			}
			callGlobalInspect(t, v, "resetScreens")
		},
	}
	runMultiWindowSteps(t, v, factory.made, steps)
}

// displaySeqFactory 是 seqFactory 的加强版: 除了每次造一块新的假 Surface,
// 它还**实现 displayProvider** —— 于是用例同时覆盖"多显示器枚举 + 窗口在哪块屏"
// 这条后端通路 (真后端在 gfx/win32/display.go, 这里是它的替身)。
//
// 按创建序把第 i 个窗口放到第 i 块屏上: 折叠姿态因此只影响其中一块屏上的窗口,
// 这正是"多屏 + 折叠"要验的隔离性。
type displaySeqFactory struct {
	seqFactory
	displays []Display
	of       map[Surface]string
}

func (f *displaySeqFactory) Displays() []Display { return f.displays }

func (f *displaySeqFactory) DisplayOf(s Surface) (string, bool) {
	id, ok := f.of[s]
	return id, ok
}

func (f *displaySeqFactory) Create(cfg WindowConfig) (Surface, error) {
	s := newFakeSurface()
	f.made = append(f.made, s)
	idx := len(f.made) - 1
	if idx < len(f.displays) {
		f.of[s] = f.displays[idx].ID
	}
	return s, nil
}

// rootOfWindow 取第 i 个窗口的元素树根 (made 是各窗口的假 Surface, 按创建序)。
func rootOfWindow(t *testing.T, made []*fakeSurface, i int) *GuiNode {
	t.Helper()
	if i < 0 || i >= len(made) {
		t.Fatalf("没有第 %d 个窗口 (共 %d 个)", i, len(made))
	}
	a := appForSurface(made[i])
	if a == nil || a.root == nil {
		t.Fatalf("第 %d 个窗口没有元素树", i)
	}
	return a.root
}

// runMultiWindowSteps 驱动多窗口事件循环: 每轮给**每个**窗口补一次无害唤醒
// (假 Surface 的唤醒通道容量有限, 每轮每窗一次是安全口径), 全部步骤走完后
// 一起关窗 → 泵返回 false。
func runMultiWindowSteps(t *testing.T, v *vm.VM, made []*fakeSurface, steps []func(v *vm.VM, a, b *GuiNode)) {
	t.Helper()
	round := 0
	pump := func(maxWait time.Duration) bool {
		round++
		for _, s := range made {
			s.push(Event{Kind: EventMouseLeave})
		}
		if round-1 < len(steps) {
			steps[round-1](v, rootOfWindow(t, made, 0), rootOfWindow(t, made, 1))
		} else {
			for _, s := range made {
				s.push(Event{Kind: EventClose})
			}
		}
		return Pump(maxWait)
	}
	if err := v.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if Active() {
		t.Fatalf("关闭后应用未退出")
	}
}

// allTexts 收集子树里的文本 (失败信息里带上它, 排查快得多)。
func allTexts(root *GuiNode) []string {
	var out []string
	for _, n := range findAll(root, "#text") {
		out = append(out, n.Text)
	}
	return out
}

package gfx

// ===== testdata/view_demo2.js (gx/view 改写版演示) 的全链路交互测试 =====
//
// 这个演示的卖点就是"把内核语义做成界面上可读的数字", 所以断言也骑在这上面:
// 头部 stats 行里的 build 计数 + 每行的 gen 徽标 —— 点一个按钮, 数涨了几就等于
// "到底重建了几行"。这比数文本个数强得多: 重建前后文本可以长得一模一样
// (view_test.go 的注释也是这个口径, 只是那边靠节点指针, 这边靠演示自己报数)。
//
// 两条纪律 (与 view_test.go 同源):
//   - 从 Go 侧驱动脚本/断言必须在 v.RunTimersWithPump 执行期内;
//   - **假 Surface 的尺寸决定布局尺寸**: fakeFactory.Create 原样返回 fake, 不读
//     WindowConfig, 所以这里手工把 fake.w/h 设成脚本里 <window> 的尺寸 —— 否则
//     "内容没溢出窗口"这条断言量的是 400×300 的旧口径, 等于没量。
//
// 点击用 routing_test.go 的 click() (同包共享 helper), 只推 MouseUp —— 顺带覆盖
// "列表在 <scroll> 里"这条真实路径 (滚动区内的按钮必须仍然点得中)。

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// demoW / demoH 必须与脚本里 <window width height> 一致。
const (
	demoW = 700
	demoH = 580
)

// demoButton 按可见文本精确定位一个按钮 (演示里所有按钮都有字面标签;
// 隐藏/显示按钮的标签是函数子节点, findAll 走整棵子树照样找得到)。
func demoButton(t *testing.T, root *GuiNode, label string) *GuiNode {
	t.Helper()
	for _, b := range findAll(root, "button") {
		for _, c := range findAll(b, "#text") {
			if strings.TrimSpace(c.Text) == label {
				return b
			}
		}
	}
	t.Fatalf("找不到标签为 %q 的按钮 (树上现有: %v)", label, demoButtonLabels(root))
	return nil
}

// rowRemoveButton 定位"标题为 title 的那一行"里的 remove 按钮 ——
// 按内容而不是按下标找行, 行序被 shuffle 改过之后依然可靠。
func rowRemoveButton(t *testing.T, root *GuiNode, title string) *GuiNode {
	t.Helper()
	for _, b := range findAll(root, "button") {
		if strings.TrimSpace(viewTexts(b)[0]) != "remove" {
			continue
		}
		if p := b.Parent; p != nil && textContainsAny(p, title) {
			return b
		}
	}
	t.Fatalf("找不到标题 %q 那一行的 remove 按钮 (当前文本 = %v)", title, viewTexts(root))
	return nil
}

func demoButtonLabels(root *GuiNode) []string {
	var out []string
	for _, b := range findAll(root, "button") {
		for _, c := range findAll(b, "#text") {
			out = append(out, strings.TrimSpace(c.Text))
		}
	}
	return out
}

// demoStat 读 stats 行里某个键后面的值 (行形如
// "rows 3/12   ·   builds 7   ·   phase loading   ·   panel shown")。
func demoStat(t *testing.T, root *GuiNode, key string) string {
	t.Helper()
	line := textStartingWith(root, "rows ")
	if line == "" {
		t.Fatalf("没有 stats 行 (文本 = %v)", viewTexts(root))
	}
	fields := strings.Fields(line)
	for i, f := range fields {
		if f == key && i+1 < len(fields) {
			return fields[i+1]
		}
	}
	t.Fatalf("stats 行 %q 里没有 %q", line, key)
	return ""
}

// demoBuilds 读全局构建计数 ("这一轮到底重建了几行" 的硬指标)。
func demoBuilds(t *testing.T, root *GuiNode) int {
	t.Helper()
	n, err := strconv.Atoi(demoStat(t, root, "builds"))
	if err != nil {
		t.Fatalf("builds 不是整数: %v", err)
	}
	return n
}

func TestViewDemo2Script(t *testing.T) {
	// 定时器调度器是进程级单例: 演示里的秒表 (setInterval) 不清掉会泄漏到别的用例。
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	src, err := os.ReadFile(filepath.Join("..", "testdata", "view_demo2.js"))
	if err != nil {
		t.Fatalf("读取脚本: %v", err)
	}
	fake := newFakeSurface()
	fake.w, fake.h = demoW, demoH
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(string(src))
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	appMu.Lock()
	root := activeApp.root
	appMu.Unlock()

	b0 := 0 // 初次挂载的构建次数: 其余步骤一律按增量断言 (行数会变, 绝对值没意义)

	steps := []func(){
		// 0) 初见: 3 行 + 4 个 tag = 7 次构建; 面板在, 相位是 loading
		func() {
			if !textContainsAny(root, "gen 1") || !textContainsAny(root, "gen 3") {
				t.Fatalf("初始行没有 gen 徽标: %v", viewTexts(root))
			}
			if got := demoStat(t, root, "rows"); got != "3/12" {
				t.Fatalf("stats rows = %q, want 3/12", got)
			}
			if got := demoStat(t, root, "phase"); got != "loading" {
				t.Fatalf("stats phase = %q", got)
			}
			if got := demoStat(t, root, "panel"); got != "shown" {
				t.Fatalf("stats panel = %q", got)
			}
			b0 = demoBuilds(t, root)
			if b0 != 7 {
				t.Fatalf("初次构建次数 = %d, want 7 (3 行 + 4 个 tag)", b0)
			}
			// 窗口不是滚动容器: 内容一旦比窗口高就被裁掉, 而裁掉的部分点不中。
			// 量"子树的底部"而不是 root.Box.H —— 根节点会被布局撑满整个窗口,
			// 拿它比较等于恒真; 跳过根节点拿到的才是真实的内容高度。
			bottom := 0
			for _, n := range allNodes(root) {
				if n == root {
					continue
				}
				if b := n.Box.Y + n.Box.H; b > bottom {
					bottom = b
				}
			}
			t.Logf("窗口 %dx%d, 内容底部 y = %d (下边距 14)", demoW, demoH, bottom)
			if bottom > demoH-14 {
				t.Fatalf("内容溢出窗口: 底部 y=%d > 可用高 %d", bottom, demoH-14)
			}
		},
		// 1) 删掉中间那一行 (顺带验证: 滚动容器里的按钮照样点得中)
		func() { click(fake, rowRemoveButton(t, root, "Beta")) },
		// 2) stable ⇒ 删中间行不重建任何幸存行
		func() {
			if got := demoBuilds(t, root); got != b0 {
				t.Fatalf("删中间行后 builds = %d, want %d (stable 下下标不参与复用判定)", got, b0)
			}
			if textContainsAny(root, "Beta") {
				t.Fatalf("被删的行还在树上: %v", viewTexts(root))
			}
			if !textContainsAny(root, "Alpha") || !textContainsAny(root, "Gamma") {
				t.Fatalf("幸存行被误删: %v", viewTexts(root))
			}
			if got := demoStat(t, root, "rows"); got != "2/12" {
				t.Fatalf("stats rows = %q, want 2/12", got)
			}
		},
		// 3) shuffle
		func() { click(fake, demoButton(t, root, "shuffle")) },
		// 4) stable 重排: 一行都不重建, 且每行还带着自己原来的 gen (节点身份没变)
		func() {
			if got := demoBuilds(t, root); got != b0 {
				t.Fatalf("shuffle 后 builds = %d, want %d (stable 重排不该重建任何行)", got, b0)
			}
			got := strings.Join(viewTexts(root), "|")
			if !strings.Contains(got, "gen 3|Gamma|remove|gen 1|Alpha") {
				t.Fatalf("重排后行身份/顺序不对: %s", got)
			}
		},
		// 5) retitle 1st (换对象引用)
		func() { click(fake, demoButton(t, root, "retitle 1st")) },
		// 6) 只有第一行重建: builds +1
		func() {
			if got := demoBuilds(t, root); got != b0+1 {
				t.Fatalf("retitle 后 builds = %d, want %d (= 只重建了 1 行)", got, b0+1)
			}
			if !textContainsAny(root, "Gamma*") {
				t.Fatalf("新的标题没上屏: %v", viewTexts(root))
			}
		},
		// 7) swap 1st/2nd (tags 列表, 没有 stable)
		func() { click(fake, demoButton(t, root, "swap 1st / 2nd")) },
		// 8) 移动过下标的 2 行重建 (gen +2), 没动的两行 gen 原样
		func() {
			if got := demoBuilds(t, root); got != b0+3 {
				t.Fatalf("swap 后 builds = %d, want %d (2 行下标变了)", got, b0+3)
			}
			if !textContainsAny(root, "[gen "+strconv.Itoa(b0+3)+"] 2. For") {
				t.Fatalf("下移的那一行没按新下标重渲染: %v", viewTexts(root))
			}
			if !textContainsAny(root, "[gen "+strconv.Itoa(b0)+"] 4. Match") {
				t.Fatalf("下标没动的 tag 被白重建了: %v", viewTexts(root))
			}
		},
		// 9) 隐藏面板
		func() { click(fake, demoButton(t, root, "hide panel")) },
		// 10) 面板摘出布局流 (保活), fallback 上屏
		func() {
			if textContainsAny(root, "panel-local input") {
				t.Fatalf("隐藏的面板还挂在布局树上")
			}
			if !textContainsAny(root, "panel hidden") {
				t.Fatalf("fallback 没上屏: %v", viewTexts(root))
			}
			if got := demoStat(t, root, "panel"); got != "hidden" {
				t.Fatalf("stats panel = %q", got)
			}
		},
		// 11) 再显示
		func() { click(fake, demoButton(t, root, "show panel")) },
		// 12) 面板回来: 同一个节点 + 秒表还在走 (保活的直接证据)
		func() {
			if !textContainsAny(root, "panel-local input") || !textContainsAny(root, "alive ") {
				t.Fatalf("面板没回来 / 秒表没在走: %v", viewTexts(root))
			}
			if got := demoStat(t, root, "panel"); got != "shown" {
				t.Fatalf("stats panel = %q", got)
			}
		},
		// 13) 打一个不存在的相位 (原版这条分支不可达)
		func() { click(fake, demoButton(t, root, "bad phase")) },
		// 14) Switch 的 fallback 上屏
		func() {
			if !textContainsAny(root, "没有任何 Match 为真") {
				t.Fatalf("Switch fallback 没上屏: %v", viewTexts(root))
			}
		},
		// 15) error 分支
		func() { click(fake, demoButton(t, root, "error")) },
		// 16) error 分支上屏, 且它自带 retry/ignore (真实交互, 不是死演示)
		func() {
			if !textContainsAny(root, "failed") || !textContainsAny(root, "retry") {
				t.Fatalf("error 分支不对: %v", viewTexts(root))
			}
			if got := demoStat(t, root, "phase"); got != "error" {
				t.Fatalf("stats phase = %q", got)
			}
		},
		// 17) clear (行全销毁)
		func() { click(fake, demoButton(t, root, "clear")) },
		// 18) 露出 fallback; 销毁不产生构建 (fallback 保活)
		func() {
			if !textContainsAny(root, "list is empty") {
				t.Fatalf("空列表 fallback 没上屏: %v", viewTexts(root))
			}
			if got := demoStat(t, root, "rows"); got != "0/12" {
				t.Fatalf("stats rows = %q, want 0/12", got)
			}
			if got := demoBuilds(t, root); got != b0+3 {
				t.Fatalf("clear 后 builds = %d, want %d (销毁不构建)", got, b0+3)
			}
		},
		// 19) 空列表时再加一行
		func() { click(fake, demoButton(t, root, "add")) },
		// 20) 只构建这一行, 且标题走自动命名 (输入框是空的)
		func() {
			if got := demoBuilds(t, root); got != b0+4 {
				t.Fatalf("add 后 builds = %d, want %d (只构建新行)", got, b0+4)
			}
			if !textContainsAny(root, "Row 4") {
				t.Fatalf("新行没上屏 / 标题不对: %v", viewTexts(root))
			}
			if got := demoStat(t, root, "rows"); got != "1/12" {
				t.Fatalf("stats rows = %q, want 1/12", got)
			}
		},
	}

	round := 0
	pump := func(maxWait time.Duration) bool {
		round++
		// 无害唤醒: 只做断言的步骤自己不产生事件, 而事件泵在队列为空时会按
		// WaitEvents 的语义长时间阻塞 (假 Surface 在 maxWait<=0 时是 10 秒)。
		fake.push(Event{Kind: EventMouseLeave})
		if round-1 < len(steps) {
			steps[round-1]()
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
}

package gfx

import (
	"image"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// 脚手架生成出来的默认工程必须真的"跑得起来" —— 编译通过 ≠ 挂得上窗、
// 布局不炸、首帧画得出东西、控件接线是活的、点一下有反应。
//
// 为什么值得单独立一个用例: 模板是**独立文本文件**（见 scaffold/scaffold.go 的
// 包注释），Go 编译器不认识它们。要防的是一类"静态检查全过、跑起来才现形"的错误,
// 例如: 入口用了 JSX 却忘了 import h（JSX 降级后是 h(...) 调用, 挂载当场
// ReferenceError: h is not defined）、把取值函数写成快照（挂载正常但之后永远不更新）、
// model 绑定只补上了读方向（打不进字）。
//
// 走 EvalFileVM 而不是本文件常用的 EvalVM: 模板是多文件工程
// （main.js → ./app.js → ./components/*.js），只有前者会 SetModuleBase，
// 相对 import 才解析得到 —— 与真实 `gox <文件>` 是同一条代码路径。
func TestScaffoldTemplateProject(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	entry := filepath.Join("..", "scaffold", "template", "src", "main.js")
	v, err := vm.EvalFileVM(entry)
	if err != nil {
		t.Fatalf("挂载模板工程失败: %v", err)
	}

	appMu.Lock()
	a := activeApp
	appMu.Unlock()
	if a == nil {
		t.Fatal("模板脚本没有挂载窗口")
	}
	root := a.root
	// <window> 只是**窗口配置**的载体（title / width / height），render() 会把它剥掉,
	// 所以树根是 App() 返回的那个 <column>。
	if root.Tag != "column" {
		t.Fatalf("根节点是 <%s>, 期望 <column>（App 的根容器）", root.Tag)
	}

	// ---- 初始可见的部分（计数器面板）----
	visible := []string{"column", "row", "text", "button", "slider", "progress", "separator", "spacer"}
	for _, tag := range visible {
		if countTag(root, tag) == 0 {
			t.Errorf("初始界面里缺少 <%s>", tag)
		}
	}

	// ---- 路由是懒构建 + keepAlive: 没去过的页面根本还没建 ----
	//
	// 这条断言同时是"待办页面确实懒构建"的凭据, 也是下面必须先切页签的原因。
	// (路由记录 keepAlive: true 与原 show 指令的 keep-alive 同义 —— 离开只是把
	// 子树摘出布局流, 页面内的局部状态保活。)
	for _, tag := range []string{"input", "scroll", "checkbox"} {
		if n := countTag(root, tag); n != 0 {
			t.Errorf("待办页面还没去过, 却已经建出了 %d 个 <%s>（懒构建失效?）", n, tag)
		}
	}

	// ---- 页签 = RouterLink, 用 __routeTo 内省属性定位 ----
	//
	// (gfx/router_view.go 给每个链接都写了这个属性, 注释里明确说"测试按它定位链接"。)
	// 响应式接线防的还是本工程最常见的坑: 把 getter 写成快照（background={colors.accent}
	// 而不是 () => ...）时, 挂载、布局、首帧全都正常, 只是之后永远不再更新 —— 属于
	// "看着能跑"的错误。接线成功的节点会留下 effect（node.go 的 reactiveProp）。
	links := map[string]*GuiNode{}
	for _, path := range []string{"/", "/todos", "/status"} {
		lnk := routerLinkByPath(root, path)
		if lnk == nil {
			t.Fatalf("找不到指向 %q 的 RouterLink", path)
		}
		if len(lnk.effects) == 0 {
			t.Errorf("页签链接 %q 没有响应式接线（activeBackground / color 大概写成快照了）", path)
		}
		links[path] = lnk
	}
	if sl := findFirst(root, "slider"); sl == nil || len(sl.effects) == 0 {
		t.Error("滑块没有响应式接线（value 大概写成快照了）")
	}

	// ---- 首帧真的画出了东西（不是一张纯色）----
	if colors := distinctColors(renderTree(root, 620, 540)); colors < 4 {
		t.Errorf("首帧只有 %d 种颜色, 像是没画上", colors)
	}

	// ---- 交互: 切页签 → 面板懒构建 → 勾掉一条待办 → 计数必须跟着变 ----
	//
	// 必须推给 **VM 的事件循环**, 不能直接调 a.pump(): 点击在抬起那一刻派发,
	// 而回调桥要求 currentVM —— 主执行结束后它是 nil, 脚本回调会静默变成空操作
	// （helpers_test.go 的 mountTestApp 注释里那条纪律）。所以走 RunTimersWithPump。
	//
	// 每轮只投一个事件: 假 Surface 的 events / arrived 通道各只有 16 个缓冲,
	// 且每个 pump 轮次只消费一个唤醒 token —— 一轮里连投多个会让 push 永久阻塞,
	// 整个用例挂到 "panic: test timed out"。只为断言的轮次也补一个无害唤醒
	// （EventMouseLeave 语义上幂等）, 否则泵会白等到 wait 上限。
	tabBtn := links["/todos"]
	var (
		tabX, tabY, cbX, cbY int
		before, after        string
		panelChecked         bool
		activeBG, idleBG     string
	)

	steps := []func(){
		// 0-1: 让首帧布局落定
		func() { fake.push(Event{Kind: EventMouseLeave}) },
		func() { fake.push(Event{Kind: EventMouseLeave}) },
		// 2-3: 点待办页签（按下 / 抬起）
		func() {
			// 切页前记下两个页签的背景色: 下面要用它们断言"高亮跟着路由走"。
			// 回归背景: bumpRevSignal 曾把小整数缓存的同一个 *Number 指针反复
			// 发给 signal, 第二次起被按身份判重丢弃 —— activeBackground 冻在首帧。
			activeBG = propString(links["/"], "background")
			idleBG = propString(links["/todos"], "background")
			if activeBG == "" || activeBG == idleBG {
				t.Errorf("初始高亮没点亮: 活动页签背景 %q, 非活动页签背景 %q", activeBG, idleBG)
			}
			tabX, tabY = centerOf(tabBtn)
			fake.push(Event{Kind: EventMouseDown, X: tabX, Y: tabY})
		},
		func() { fake.push(Event{Kind: EventMouseUp, X: tabX, Y: tabY}) },
		// 4: 上一轮的重绘已落地 ⇒ 断言高亮随路由迁移 + 面板被懒构建出来了
		func() {
			if got := propString(links["/"], "background"); got != idleBG {
				t.Errorf("切页后计数器页签没有回到非活动色: got %q want %q", got, idleBG)
			}
			if got := propString(links["/todos"], "background"); got != activeBG {
				t.Errorf("切页后待办页签没有点亮: got %q want %q", got, activeBG)
			}
			if n := countTag(root, "checkbox"); n != 3 {
				t.Errorf("切到待办页签后 checkbox 数 = %d, 期望 3（each 指令没展开?）", n)
			}
			in := findFirst(root, "input")
			if in == nil {
				t.Errorf("切到待办页签后 待办面板里没有 input")
				return
			}
			if _, ok := in.Props["value"]; !ok {
				t.Error("input 上没有 value —— model 指令没有补上读方向")
			}
			if _, ok := in.Props["onInput"]; !ok {
				t.Error("input 上没有 onInput —— model 指令没有补上写方向（会打不进字）")
			}
			panelChecked = true
			before = firstTextContaining(root, "项未完成")
			if before == "" {
				t.Error("找不到未完成计数文本")
			}
			if cb := findFirst(root, "checkbox"); cb != nil {
				cbX, cbY = centerOf(cb)
			}
		},
		// 5-6: 勾掉第一条待办
		func() { fake.push(Event{Kind: EventMouseDown, X: cbX, Y: cbY}) },
		func() { fake.push(Event{Kind: EventMouseUp, X: cbX, Y: cbY}) },
		// 7: 计数文本必须跟着变
		func() { after = firstTextContaining(root, "项未完成") },
	}

	round := 0
	pump := func(maxWait time.Duration) bool {
		// **把无限等待钳成有限值**: 事件循环在"无定时器"时传 maxWait=0（语义是
		// 无限期等外部事件）, 而假 Surface 把 0 当成睡 10 秒 —— 不钳的话每轮白等
		// 10s, 用例直接涨到十几秒（见 dialog_test.go 的同款纪律）。
		const pollWait = 10 * time.Millisecond
		if maxWait <= 0 || maxWait > pollWait {
			maxWait = pollWait
		}
		if round >= len(steps) {
			// 步骤跑完 ⇒ 推一次 close 收工（模板没有定时器, 不存在被丢掉的收尾任务）
			fake.push(Event{Kind: EventClose})
			round++
			return Pump(maxWait)
		}
		steps[round]()
		round++
		return Pump(maxWait)
	}
	if err := v.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if Active() {
		t.Error("全部窗口关闭后应用仍未退出")
	}
	if !panelChecked {
		t.Fatal("没有走到待办面板的断言（页签没切过去?）")
	}
	if after == "" || after == before {
		t.Errorf("勾掉一条待办后计数没有变: before=%q after=%q", before, after)
	}
}

// centerOf 取节点中心（点击落点）。
func centerOf(n *GuiNode) (int, int) {
	return n.Box.X + n.Box.W/2, n.Box.Y + n.Box.H/2
}

// routerLinkByPath 按 __routeTo 找 RouterLink（gfx/router_view.go 给每个链接
// 写了这个内省属性, 注释里明确说"测试按它定位链接"）。
func routerLinkByPath(root *GuiNode, path string) *GuiNode {
	return findFirstWhere(root, func(n *GuiNode) bool {
		if n.Tag != "view" {
			return false
		}
		s, ok := n.Props["__routeTo"].(*object.String)
		return ok && s.Value == path
	})
}

// propString 读节点 props 里的字符串值（响应式 prop 每次求值的结果都写在 Props 上）。
func propString(n *GuiNode, name string) string {
	if s, ok := n.Props[name].(*object.String); ok {
		return s.Value
	}
	return ""
}

// nodeText 拼出子树里所有 #text 的内容。
func nodeText(n *GuiNode) string {
	var sb strings.Builder
	for _, t := range findAll(n, "#text") {
		sb.WriteString(t.Text)
	}
	return sb.String()
}

// firstTextContaining 找第一个包含 sub 的文本节点内容。
func firstTextContaining(root *GuiNode, sub string) string {
	for _, n := range findAll(root, "#text") {
		if strings.Contains(n.Text, sub) {
			return n.Text
		}
	}
	return ""
}

// distinctColors 数一帧里出现了多少种颜色（抗锯齿会让颜色数远超"设计上的几种"，
// 所以只能当"画没画上"的粗判据，不能断言某个具体色值）。
func distinctColors(img *image.RGBA) int {
	seen := map[uint32]struct{}{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.RGBAAt(x, y)
			seen[uint32(c.R)<<16|uint32(c.G)<<8|uint32(c.B)] = struct{}{}
		}
	}
	return len(seen)
}

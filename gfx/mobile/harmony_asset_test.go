package mobile

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/14752222/Gox/gfx"
	"github.com/14752222/Gox/vm"
)

// TestHarmonyAssetScriptRunsAndTracksFold 直接跑**要打进 HAP 的那份 rawfile 脚本**。
//
// 与 TestAndroidAssetScriptClickDrivesCounter 同一套路 (脚本来源就是真机上唯一
// 会被执行的那份), 但多验收一条 HF2: **折叠上报进来之后, 界面上的读数要跟着变**。
//
// 为什么不止于 fold_test.go 的那几条 Go 侧用例: 那些证明的是"上报到了屏表",
// 而这里证明的是"屏表变化真的唤醒了响应式视图"。两者之间还隔着一层订阅
// (useReservedRegions / useLayoutMode 里那两个版本号信号) —— 那一层断了的时候
// 屏表明明是新的, 界面却纹丝不动, 从画面几乎不可能反推到"订阅没接通"。
func TestHarmonyAssetScriptRunsAndTracksFold(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "..",
		"app", "harmony", "entry", "src", "main", "resources", "rawfile", "app.js"))
	if err != nil {
		t.Fatalf("读取鸿蒙 rawfile 脚本: %v", err)
	}

	gfx.ResetDisplaysForTest()
	t.Cleanup(gfx.ResetDisplaysForTest)

	s := New(Config{Width: 400, Height: 600, Density: 2})
	s.Register()
	defer gfx.SetDefaultFactory(nil)

	v, err := vm.EvalVM(string(src))
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	// 断言全部攒到泵外再做 (理由同 Android 那份: t.Fatalf 走 runtime.Goexit,
	// 在 pump 回调里调用会把 RunTimersWithPump 连同已挂载的窗口丢在半路,
	// 现象是"测试卡住"而不是"断言失败")。
	var (
		before  []string // 点击前
		after   []string // 点击并重绘后
		folded  []string // 折叠上报并重绘后
		problem string
	)
	closeAndPump := func(maxWait time.Duration) bool {
		s.Post(gfx.Event{Kind: gfx.EventClose})
		return gfx.Pump(maxWait)
	}
	round := 0
	pump := func(maxWait time.Duration) bool {
		round++
		switch round {
		case 1:
			// 首帧: 让 render 挂上的树走完布局 + 首绘 —— 坐标要到这之后才算得出来。
			s.Tick()
			return gfx.Pump(maxWait)
		case 2:
			root := gfx.ActiveRoot()
			if root == nil {
				problem = "首帧之后仍拿不到窗口根节点"
				return closeAndPump(maxWait)
			}
			before = collectText(root)
			btn := lastButton(root)
			if btn == nil {
				problem = fmt.Sprintf("asset 里没找到 button 节点, 树上文本: %v", before)
				return closeAndPump(maxWait)
			}
			if btn.Box.W == 0 || btn.Box.H == 0 {
				problem = fmt.Sprintf("button 未完成布局 (Box=%+v)", btn.Box)
				return closeAndPump(maxWait)
			}
			cx, cy := btn.Box.X+btn.Box.W/2, btn.Box.Y+btn.Box.H/2
			s.Touch(TouchDown, cx, cy)
			s.Touch(TouchUp, cx, cy)
			return gfx.Pump(maxWait)
		case 3:
			after = collectText(gfx.ActiveRoot())
			if !hasText(after, foldAbsentText) {
				problem = fmt.Sprintf("折叠上报前界面应显示 %q, 实际: %v", foldAbsentText, after)
				return closeAndPump(maxWait)
			}
			// HF2: 从平台侧的唯一入口喂一份折叠上报。用与 ArkTS 上报器同形状的
			// 载荷 (半折 + 一条 vertical division), 尺寸类显式给, 免得依赖内核
			// 自己按 density 推的那一路。
			if err := ReportDisplayFold(foldPayload); err != nil {
				problem = fmt.Sprintf("ReportDisplayFold: %v", err)
				return closeAndPump(maxWait)
			}
			// 上报是 gfx.Post 到 GUI 线程的: 这一轮 Pump 负责执行它 (以及它
			// 触发的脚本订阅回调), 下一轮再读树。
			return gfx.Pump(maxWait)
		case 4:
			// 补一次 Tick 让布局/绘制跟上 (文本更新本身在上一轮就完成了,
			// 这里只是保证"读到的树"与"该显示的树"一致)。
			s.Tick()
			folded = collectText(gfx.ActiveRoot())
			return closeAndPump(maxWait)
		default:
			problem = fmt.Sprintf("关窗后事件泵未收敛 (已到第 %d 轮)", round)
			return false
		}
	}
	if err := v.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if problem != "" {
		t.Fatal(problem)
	}

	// 先确认点击前是 0: 否则"点击后是 1"可能是脚本一上来就写着 1 (断言空转)。
	if !hasText(before, "HF1 触摸链路: 计数 0") {
		t.Fatalf("点击前界面应显示计数 0, 实际: %v", before)
	}
	if !hasText(after, "HF1 触摸链路: 计数 1") {
		t.Fatalf("点一次按钮后界面应显示计数 1; 点击前=%v 点击后=%v", before, after)
	}
	// HF2 的正题: 上报之后界面上的折痕读数必须换掉。
	if !hasText(folded, foldPresentText) {
		t.Fatalf("折叠上报后界面应显示 %q; 上报前=%v 上报后=%v", foldPresentText, after, folded)
	}
	// 顺带看住"布局建议"这条 —— 它是 posture + 宽度档 + 有无折痕 三者合成
	// 的结果, 只有上报里的 widthClass 也被吃进去了才会变成 dual。
	if !hasText(folded, "布局建议: dual ｜ 宽度档: expanded") {
		t.Fatalf("半折 + expanded 的布局建议应为 dual; 实际: %v", folded)
	}
}

// foldAbsentText / foldPresentText 是脚本里那两条折痕摘要文案, 逐字对齐
// app/harmony/entry/src/main/resources/rawfile/app.js。
//
// 单独提出来是刻意的: 改脚本文案时这里会红, 提醒"文案也是契约的一部分"
// (它是真机验收时要肉眼比对的那一行)。
const (
	foldAbsentText  = "未检测到折痕 (直板机, 或宿主未上报)"
	foldPresentText = "折痕 1 条 ｜ 保留区共 1 条"
)

// foldPayload 与鸿蒙 ArkTS 上报器 (GoxDisplayFold.ets) 输出的载荷同形状。
const foldPayload = `{
  "posture": "half-open",
  "width": 2000, "height": 1600,
  "widthClass": "expanded", "heightClass": "regular",
  "hinge": {"x": 960, "y": 0, "width": 40, "height": 1600, "orientation": "vertical"},
  "regions": [
    {"kind": "division", "x": 960, "y": 0, "width": 40, "height": 1600, "active": true}
  ]
}`

package gfx

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== README 首屏示例（testdata/adaptive_list_demo.js）的自适应回归 (r1yfFa) =====
//
// 为什么跑**真文件**而不是在测试里另写一份脚本：README 首屏示例一旦与内核
// 语义脱节（断点改名、订阅纪律变了、matchBreakpoint 口径变了），这个测试先红，
// 而不是等到有人照着 README 抄出一份不跟着窗口变的代码。
//
// 断言三件事：
//  1. 宽度 → 断点 → 布局分支的映射（480 单栏 / 700 与 1000 双栏）；
//  2. 切换是**订阅驱动**的：resize 之后同一个 __bp() 自己变（不重跑脚本）；
//  3. 【坑 1】纯读数快照不订阅：__staleSnapshot 拖宽后仍停在首帧的 "sm" ——
//     把这条钉死，README 里那句「删掉 bp() 就不再自适应」才有据可查。

func TestReadmeAdaptiveListDemoColumns(t *testing.T) {
	setupHost(t, nil)
	resetViewportStateForTest()
	t.Cleanup(resetViewportStateForTest)
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	src, err := os.ReadFile(filepath.Join("..", "testdata", "adaptive_list_demo.js"))
	if err != nil {
		t.Fatalf("读取脚本: %v", err)
	}
	v, err := vm.EvalVM(string(src))
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	// 每档：改窗口宽度 → 读断点名 / 是否双栏 / 列数。resize 与读值都必须在
	// 泵轮次内做（fake 的 ResizeClient 会同步 NotifyViewportChanged 跑脚本
	// 回调，currentVM 不在时整条链静默 no-op）。
	//
	// 四档在**一趟泵**里顺序跑完，不要每档一次 pumpSettled：pumpSettled 收尾会
	// 推 EventClose，App 一关，下一档的脚本回调就全哑了（静默返回 undefined）。
	//
	// 每档两轮：第一轮 resize（投 EventResize），第二轮才读 —— 窗口尺寸是在
	// 事件被 processEvents 消费之后才进 app 状态的，同轮读会读到旧宽度。
	type want struct {
		width int
		bp    string
		side  bool
		cols  int
	}
	cases := []want{
		{480, "sm", false, 1}, // <600dp  单栏
		{700, "md", true, 2},  // >=600dp 双栏
		{1000, "lg", true, 2},
		{480, "sm", false, 1}, // 拖回去：能窄回来才算自适应（单向的不算）
	}
	type got struct {
		bp   string
		side bool
		cols float64
	}
	results := make([]got, len(cases))
	idx, phase := 0, 0
	pumpSettled(t, v, fake, func() bool {
		if idx >= len(cases) {
			return true
		}
		if phase == 0 {
			phase = 1
			callGlobal(t, v, "__resize",
				object.NewNumber(float64(cases[idx].width)), object.NewNumber(420))
			return false
		}
		results[idx] = got{
			bp:   callString(t, v, "__bp"),
			side: callBool(t, v, "__side"),
			cols: callNumber(t, v, "__cols"),
		}
		idx++
		phase = 0
		return false
	})
	for i, c := range cases {
		got := results[i]
		if got.bp != c.bp || got.side != c.side || int(got.cols) != c.cols {
			t.Fatalf("宽度 %d：bp=%q side=%v cols=%d，want bp=%q side=%v cols=%d",
				c.width, got.bp, got.side, int(got.cols), c.bp, c.side, c.cols)
		}
	}

	// 【坑 1】纯读数快照没订阅：来回拖过 480/700/1000 之后，它仍是首帧的 "sm"。
	// 它若跟着变，说明「纯读数不订阅」这条语义变了，README 那段注释就失真了。
	if stale := stringGlobal(t, v, "__staleSnapshot"); stale != "sm" {
		t.Fatalf("纯读数快照不该被更新：got %q, want \"sm\"", stale)
	}
	// 对照：订阅型的 bp() 在 700/1000 档**跟着变过**（上面四档断言），而快照
	// 一路没动 —— 这就是「纯读数不订阅」的可执行证据。
	//
	// 末档的 bp 读上面已经取过（results[3]），这里不再单独调一次脚本函数：
	// 泵停之后 currentVM 已清，CallFunction 会静默返回 undefined。
	if results[len(results)-1].bp != "sm" {
		t.Fatalf("末档断点 = %q, want \"sm\"", results[len(results)-1].bp)
	}
}

// callString 调一个无参全局函数并把返回值当字符串取回。
func callString(t *testing.T, v *vm.VM, name string) string {
	t.Helper()
	fn, ok := v.Globals().Get(name)
	if !ok {
		t.Fatalf("没有全局 %q", name)
	}
	res := object.CallFunction(fn, nil)
	if s, ok := res.(*object.String); ok {
		return s.Value
	}
	t.Fatalf("%q 返回 %T, 想要字符串", name, res)
	return ""
}

// callBool 同上，取布尔（非布尔值按真值判定）。
func callBool(t *testing.T, v *vm.VM, name string) bool {
	t.Helper()
	fn, ok := v.Globals().Get(name)
	if !ok {
		t.Fatalf("没有全局 %q", name)
	}
	res := object.CallFunction(fn, nil)
	if b, ok := res.(*object.Boolean); ok {
		return b.Value
	}
	return res != nil && res.IsTruthy()
}

// callNumber 同上，取数字。
func callNumber(t *testing.T, v *vm.VM, name string) float64 {
	t.Helper()
	fn, ok := v.Globals().Get(name)
	if !ok {
		t.Fatalf("没有全局 %q", name)
	}
	res := object.CallFunction(fn, nil)
	if n, ok := res.(*object.Number); ok {
		return n.Value
	}
	t.Fatalf("%q 返回 %T, 想要数字", name, res)
	return 0
}

// stringGlobal 取一个字符串全局（非函数）。
func stringGlobal(t *testing.T, v *vm.VM, name string) string {
	t.Helper()
	val, ok := v.Globals().Get(name)
	if !ok {
		t.Fatalf("没有全局 %q", name)
	}
	if s, ok := val.(*object.String); ok {
		return s.Value
	}
	t.Fatalf("%q 不是字符串: %T", name, val)
	return ""
}

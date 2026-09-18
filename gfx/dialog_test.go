package gfx

import (
	"errors"
	"image"
	"sync"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// P3-4 原生对话框测试。
//
// **绝不在单测里真的弹系统对话框**: 无人值守的环境下 `MessageBoxW` 会一直
// 挂住 (CI 直接超时), 而"打开文件"对话框还要人去点。所以这里给假 Surface
// 装上 `nativeDialogHost`, 由测试预设"用户会怎么答" —— 这正是可选接口的
// 附带好处: 原生能力可替换, 弹框这件事在测试里变成一笔纯数据。

// fakeDialogHost 是 nativeDialogHost 的测试替身: 记录调用参数 + 返回预设结果。
type fakeDialogHost struct {
	mu sync.Mutex
	// 收到的调用
	messages []fakeMessageCall
	opens    []NativeFileOptions
	// 预设的应答
	confirmAnswer  bool
	messageErr     error
	openPath       string
	openOk         bool
	openErr        error
	showMessageNil bool // true 时让 ShowMessage 走"后端不支持"分支之外的另一条
}

type fakeMessageCall struct {
	Kind    NativeDialogKind
	Title   string
	Message string
}

func (f *fakeDialogHost) ShowMessage(kind NativeDialogKind, title, message string) (bool, error) {
	f.mu.Lock()
	f.messages = append(f.messages, fakeMessageCall{Kind: kind, Title: title, Message: message})
	f.mu.Unlock()
	if f.messageErr != nil {
		return false, f.messageErr
	}
	return f.confirmAnswer, nil
}

func (f *fakeDialogHost) ShowOpenFile(opts NativeFileOptions) (string, bool, error) {
	f.mu.Lock()
	f.opens = append(f.opens, opts)
	f.mu.Unlock()
	if f.openErr != nil {
		return "", false, f.openErr
	}
	return f.openPath, f.openOk, nil
}

func (f *fakeDialogHost) messageCalls() []fakeMessageCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeMessageCall(nil), f.messages...)
}

func (f *fakeDialogHost) openCalls() []NativeFileOptions {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]NativeFileOptions(nil), f.opens...)
}

// ===== 纯 Go 层: 不挂窗口时的降级 =====

// TestDialogFallsBackWithoutBackend 没有后端时三个 API 都必须**安静降级**,
// 不能 panic、不能挂住。
//
// 两种"没有后端"要分开验:
//  1. 根本没挂窗口 → `currentApp()` 为 nil;
//  2. 挂了窗口, 但那个 Surface **没实现** `nativeDialogHost` (真后端里
//     X11 就是这种情况)。
//
// 显式构造而不是依赖"此刻恰好没有 activeApp": 测试是按文件顺序串行跑的,
// 别的用例留下的 activeApp 会让第 1 种情况变成随机失败。
func TestDialogFallsBackWithoutBackend(t *testing.T) {
	// 明确清掉 activeApp (保存现场, 用完还原)
	appMu.Lock()
	saved := activeApp
	activeApp = nil
	appMu.Unlock()
	defer func() {
		appMu.Lock()
		activeApp = saved
		appMu.Unlock()
	}()

	if _, ok := dialogBackend(); ok {
		t.Fatalf("未挂载窗口时不该有对话框后端")
	}
	// 同步入口: alert 无返回值, confirm 取"确定"(true), openFile 取取消
	alertBox("T", "M")
	if !confirmBox("T", "M") {
		t.Fatalf("无后端时 confirm 应返回 true (保守取确定)")
	}
	if p, ok := openFileBox(NativeFileOptions{}); ok || p != "" {
		t.Fatalf("无后端时 openFile 应返回 (空, false), got (%q,%v)", p, ok)
	}

	// 情况 2: 挂了窗口但 Surface 没实现可选接口。
	// 用裸 fakeSurface (dialog 字段为 nil 时**不满足** nativeDialogHost 的语义
	// 要在 fake 上表达 —— 这里换一个专门的最小 Surface)
	bare := &bareSurface{}
	a := &app{surface: bare, root: mkNode("column", nil), dirtyNodes: map[*GuiNode]struct{}{}}
	appMu.Lock()
	activeApp = a
	appMu.Unlock()
	defer func() {
		appMu.Lock()
		activeApp = saved
		appMu.Unlock()
	}()
	if _, ok := dialogBackend(); ok {
		t.Fatalf("后端未实现 nativeDialogHost 时不该被判为可用")
	}
	if !confirmBox("T", "M") {
		t.Fatalf("后端不支持时 confirm 应降级为 true")
	}
	if p, ok := openFileBox(NativeFileOptions{}); ok || p != "" {
		t.Fatalf("后端不支持时 openFile 应降级, got (%q,%v)", p, ok)
	}
}

// bareSurface 是一个**只实现 Surface 接口**的最小后端 (刻意不实现任何可选
// 能力接口), 用来验"能力缺失时的降级"。
type bareSurface struct{}

func (b *bareSurface) Show(*image.RGBA)                           {}
func (b *bareSurface) ShowRegions(*image.RGBA, []image.Rectangle) {}
func (b *bareSurface) Size() (int, int)                           { return 200, 150 }
func (b *bareSurface) WaitEvents(time.Duration) bool              { return true }
func (b *bareSurface) Events() <-chan Event                       { return nil }

// TestDialogBackendNotImplemented 实现接口但调用报错时, 同步入口照样降级。
func TestDialogBackendNotImplemented(t *testing.T) {
	fake, _ := mountTestApp(t, mkNode("column", nil), 200, 150)
	fake.dialog = &fakeDialogHost{
		messageErr: errors.New("boom"),
		openErr:    errors.New("boom"),
	}

	if !confirmBox("T", "M") {
		t.Fatalf("原生调用报错时 confirm 应降级为 true")
	}
	if p, ok := openFileBox(NativeFileOptions{}); ok || p != "" {
		t.Fatalf("原生调用报错时 openFile 应降级, got (%q,%v)", p, ok)
	}
	// 后端确实被调到了 (证明走的是真后端而不是直接降级)
	if n := len(fake.dialog.messageCalls()); n != 1 {
		t.Fatalf("ShowMessage 调用次数 = %d, want 1", n)
	}
}

// drainWithClose 跑事件循环, 直到 `done()` 报告"该验的都发生了", 再推 close 收工。
//
// 为什么不能"先 push close 再 RunTimersWithPump": `Pump` 处理到 EventClose 就
// 返回 false, 事件循环立刻退出 —— 而对话框的作答是**投回事件循环的定时器
// 任务**(见 dialog.go 的 resolveDeferred), 排在 close 后面, 于是被一起丢掉。
// 这类假失败会让人误以为 Promise 链断了, 实际只是循环收工太早。
//
// 为什么用 `done()` 而不是"数够 N 轮": GUI 模式下 `runTimersLoop` 在
// "无定时器"时会以 `pump(0)` **无限期**等外部事件 (见 vm/vm.go 的注释),
// 而 `pump(0)` 一旦进到 WaitEvents 就再也不返回 —— 轮次计数根本没机会递增,
// 用例直接挂死。所以必须由"脚本那边确实完成了"这个**外部事实**来驱动收工。
//
// 兜底: 给一个硬性轮次上限, done() 永远不满足时也要能退出并报错。
func drainWithClose(t *testing.T, v *vm.VM, fake *fakeSurface, done func() bool) {
	t.Helper()
	round := 0
	const maxRounds = 200
	err := v.RunTimersWithPump(func(maxWait time.Duration) bool {
		round++
		if done() || round >= maxRounds {
			fake.push(Event{Kind: EventClose})
		}
		// **必须把无限等待钳成有限值**: 事件循环在"无定时器"时会传
		// maxWait=0 (语义是"无限期等外部事件", 见 vm/vm.go)。假 Surface 会把
		// 0 当成 10 秒来睡, 于是循环卡死在 WaitEvents 里 —— 既等不到 close
		// 被 push, 也回不到 done() 检查。钳到 10ms 就只是一次短轮询。
		const pollWait = 10 * time.Millisecond
		if maxWait <= 0 || maxWait > pollWait {
			maxWait = pollWait
		}
		return Pump(maxWait)
	})
	if err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if !done() {
		t.Fatalf("事件循环结束但预期状态未达成 (转了 %d 轮, 疑似对话框任务被丢弃)", round)
	}
}

// globalString 读脚本全局字符串。
func globalString(t *testing.T, v *vm.VM, name string) string {
	t.Helper()
	val, ok := v.Globals().Get(name)
	if !ok {
		t.Fatalf("全局 %q 不存在", name)
	}
	return valueText(val)
}

// globalStringSettled 返回一个"该全局不再是 pending 哨兵"的判定函数。
func globalStringSettled(t *testing.T, v *vm.VM, name string) func() bool {
	return func() bool { return globalString(t, v, name) != "pending" }
}

// ===== Promise 链路 (全链路 VM) =====

// TestAlertResolvesPromise 验 alert 返回 Promise, 且 resolve 发生在
// "本次脚本执行结束之后"(微任务语义)。
func TestAlertResolvesPromise(t *testing.T) {
	fake := newFakeSurface()
	fake.dialog = &fakeDialogHost{}
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	v, err := vm.EvalVM(`
		import { alert, confirm, openFile } from "gx/dialog";
		import { h, render } from "gx/gfx";
		globalThis.log = [];
		render(h("rect", {width: 10, height: 10}),
			{title: "T", width: 200, height: 150});

		alert("hello", "标题").then(() => log.push("alert-done"));
		log.push("after-call");
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	// 关键断言: 调用 alert 之后、事件循环跑起来之前, then 回调**还没有**执行
	// (Promise 是微任务, 不是同步立即执行)。
	logVal, _ := v.Globals().Get("log")
	arr := logVal.(*object.Array)
	if len(arr.Elements) != 1 || valueText(arr.Elements[0]) != "after-call" {
		t.Fatalf("alert 的 then 不该在脚本同步执行期就跑: %v", arr.Elements)
	}

	drainWithClose(t, v, fake, func() bool {
		val, _ := v.Globals().Get("log")
		if arr, ok := val.(*object.Array); ok {
			return len(arr.Elements) >= 2
		}
		return false
	})

	logVal, _ = v.Globals().Get("log")
	arr = logVal.(*object.Array)
	if len(arr.Elements) != 2 || valueText(arr.Elements[1]) != "alert-done" {
		t.Fatalf("alert 的 then 应在事件循环里执行: %v", arr.Elements)
	}
	// 参数正确传到原生层
	msgs := fake.dialog.messageCalls()
	if len(msgs) != 1 || msgs[0].Message != "hello" || msgs[0].Title != "标题" {
		t.Fatalf("原生消息框参数不对: %+v", msgs)
	}
	if msgs[0].Kind != DialogInfo {
		t.Fatalf("alert 的 kind = %v, want DialogInfo", msgs[0].Kind)
	}
}

// TestConfirmResolvesBoolean 验 confirm 把布尔作答带回脚本(await 语义)。
func TestConfirmResolvesBoolean(t *testing.T) {
	for _, answer := range []bool{true, false} {
		t.Run(map[bool]string{true: "确定", false: "取消"}[answer], func(t *testing.T) {
			fake := newFakeSurface()
			fake.dialog = &fakeDialogHost{confirmAnswer: answer}
			SetDefaultFactory(&fakeFactory{fake})
			defer SetDefaultFactory(nil)
			object.GlobalScheduler().ClearAll()
			t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

			v, err := vm.EvalVM(`
				import { confirm } from "gx/dialog";
				import { h, render } from "gx/gfx";
				globalThis.got = "pending";
				render(h("rect", {width: 10, height: 10}),
					{title: "T", width: 200, height: 150});
				confirm("sure?").then(v => { got = v; });
			`)
			if err != nil {
				t.Fatalf("EvalVM: %v", err)
			}
			drainWithClose(t, v, fake, globalStringSettled(t, v, "got"))
			got, _ := v.Globals().Get("got")
			b, ok := got.(*object.Boolean)
			if !ok {
				t.Fatalf("confirm 的 resolve 值应是布尔, got %T (%v)", got, got)
			}
			if b.Value != answer {
				t.Fatalf("confirm 作答 %v, 脚本收到 %v", answer, b.Value)
			}
			// 一定是"确定/取消"型, 不是纯信息框
			if k := fake.dialog.messageCalls()[0].Kind; k != DialogConfirm {
				t.Fatalf("confirm 的 kind = %v, want DialogConfirm", k)
			}
		})
	}
}

// TestOpenFileResolvesPathOrNull 验取消 → null、确定 → 路径字符串。
func TestOpenFileResolvesPathOrNull(t *testing.T) {
	cases := []struct {
		name string
		path string
		ok   bool
		want string
	}{
		{"选中文件", `C:\tmp\a.txt`, true, `C:\tmp\a.txt`},
		{"用户取消", "", false, "null"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := newFakeSurface()
			fake.dialog = &fakeDialogHost{openPath: c.path, openOk: c.ok}
			SetDefaultFactory(&fakeFactory{fake})
			defer SetDefaultFactory(nil)
			object.GlobalScheduler().ClearAll()
			t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

			v, err := vm.EvalVM(`
				import { openFile } from "gx/dialog";
				import { h, render } from "gx/gfx";
				globalThis.got = "pending";
				render(h("rect", {width: 10, height: 10}),
					{title: "T", width: 200, height: 150});
				openFile({title: "打开", filter: "文本文件|*.txt"}).then(p => { got = String(p); });
			`)
			if err != nil {
				t.Fatalf("EvalVM: %v", err)
			}
			drainWithClose(t, v, fake, globalStringSettled(t, v, "got"))
			got, _ := v.Globals().Get("got")
			s, _ := got.(*object.String)
			if s == nil || s.Value != c.want {
				t.Fatalf("openFile 结果 = %v, want %q", got, c.want)
			}
			// 选项正确翻译到原生层
			opts := fake.dialog.openCalls()
			if len(opts) != 1 || opts[0].Title != "打开" {
				t.Fatalf("openFile 选项未传到原生层: %+v", opts)
			}
			if len(opts[0].Filter) != 1 || opts[0].Filter[0].Name != "文本文件" || opts[0].Filter[0].Pattern != "*.txt" {
				t.Fatalf("filter 未正确解析: %+v", opts[0].Filter)
			}
		})
	}
}

// TestDialogKeepsEventLoopAlive 验"对话框作答投回事件循环"这条链在
// **没有窗口事件**时也能跑完 —— 否则 `await alert(...)` 之后的代码
// 会在事件循环退出前一刻还没执行。
func TestDialogKeepsEventLoopAlive(t *testing.T) {
	fake := newFakeSurface()
	fake.dialog = &fakeDialogHost{}
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	v, err := vm.EvalVM(`
		import { alert } from "gx/dialog";
		import { h, render } from "gx/gfx";
		globalThis.done = false;
		render(h("rect", {width: 10, height: 10}),
			{title: "T", width: 200, height: 150});
		alert("x").then(() => { done = true; });
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	// 中间几轮不推任何事件: 只靠"作答投回的定时器任务"驱动。
	// 若那条任务没有被事件循环消化, done 会停在 false。
	drainWithClose(t, v, fake, func() bool {
		val, _ := v.Globals().Get("done")
		b, ok := val.(*object.Boolean)
		return ok && b.Value
	})
	done, _ := v.Globals().Get("done")
	b, _ := done.(*object.Boolean)
	if b == nil || !b.Value {
		t.Fatalf("alert 的 then 没有被执行 (事件循环过早退出?)")
	}
}

// ===== 参数解析 (纯 Go, 不挂窗口) =====

// TestParseFileFilterForms 验 filter 的三种书写形态都能解析。
func TestParseFileFilterForms(t *testing.T) {
	mk := func(v object.Value) []NativeFileFilter { return parseFileFilter(v) }

	// ① 单条字符串 "描述|通配符"
	got := mk(object.NewString("文本文件|*.txt"))
	if len(got) != 1 || got[0].Name != "文本文件" || got[0].Pattern != "*.txt" {
		t.Fatalf("字符串形态解析错: %+v", got)
	}
	// 没有竖线 → 整串当通配符 (描述取同值, 至少不会渲染出空下拉项)
	got = mk(object.NewString("*.md"))
	if len(got) != 1 || got[0].Pattern != "*.md" || got[0].Name != "*.md" {
		t.Fatalf("无竖线形态解析错: %+v", got)
	}
	// ② 字符串数组
	arr := &object.Array{Elements: []object.Value{
		object.NewString("文本|*.txt"),
		object.NewString("图片|*.png;*.jpg"),
	}}
	got = mk(arr)
	if len(got) != 2 || got[1].Name != "图片" || got[1].Pattern != "*.png;*.jpg" {
		t.Fatalf("数组形态解析错: %+v", got)
	}
	// ③ 结构化对象
	obj := object.NewObject()
	obj.SetProperty("name", object.NewString("日志"))
	obj.SetProperty("pattern", object.NewString("*.log"))
	got = mk(obj)
	if len(got) != 1 || got[0].Name != "日志" || got[0].Pattern != "*.log" {
		t.Fatalf("对象形态解析错: %+v", got)
	}
	// 缺 pattern 时兜底 *.*; 缺 name 时用 pattern 顶替
	obj2 := object.NewObject()
	obj2.SetProperty("name", object.NewString("随便"))
	got = mk(obj2)
	if got[0].Pattern != "*.*" {
		t.Fatalf("缺 pattern 应兜底 *.*, got %q", got[0].Pattern)
	}
	// 非上述类型 → 空 (调用方会走缺省过滤)
	if got := mk(object.NewNumber(1)); got != nil {
		t.Fatalf("非过滤器类型应返回 nil, got %+v", got)
	}
}

// TestParseFileOptionsTolerant 验参数写错时"能坏得温和": 不抛、只丢选项。
func TestParseFileOptionsTolerant(t *testing.T) {
	// 完全没参数
	o := parseFileOptions(nil)
	if o.Title != "" && o.Filter != nil {
		t.Fatalf("无参数不该产生过滤: %+v", o)
	}
	// 参数不是对象 → 忽略
	o = parseFileOptions([]object.Value{object.NewString("x")})
	if o.Filter != nil {
		t.Fatalf("非对象参数应被忽略: %+v", o)
	}
	// 正常对象
	obj := object.NewObject()
	obj.SetProperty("title", object.NewString("挑一个"))
	obj.SetProperty("default", object.NewString("a.txt"))
	obj.SetProperty("dir", object.NewString(`C:\`))
	o = parseFileOptions([]object.Value{obj})
	if o.Title != "挑一个" || o.Default != "a.txt" || o.Dir != `C:\` {
		t.Fatalf("选项解析错: %+v", o)
	}
}

// TestDialogArgValidation 验缺必需参数时返回 TypeError 而不是 panic。
func TestDialogArgValidation(t *testing.T) {
	if _, ok := jsAlert().(*object.Error); !ok {
		t.Fatalf("alert 缺 message 应返回 Error")
	}
	if _, ok := jsConfirm().(*object.Error); !ok {
		t.Fatalf("confirm 缺 message 应返回 Error")
	}
	// openFile 无参数是合法的 (用系统缺省过滤), 应返回 Promise
	if _, ok := jsOpenFile().(*object.Promise); !ok {
		t.Fatalf("openFile 无参数应返回 Promise")
	}
}

// TestDialogTitleDefaults 验 title 缺省为 "Gox" (空标题的消息框在
// Windows 上会显示成"错误"之类让人困惑的东西)。
func TestDialogTitleDefaults(t *testing.T) {
	title, msg := dialogTitleMessage([]object.Value{object.NewString("正文")})
	if title != "Gox" || msg != "正文" {
		t.Fatalf("缺省 title 错: title=%q msg=%q", title, msg)
	}
	title, msg = dialogTitleMessage([]object.Value{object.NewString("正文"), object.NewString("我的标题")})
	if title != "我的标题" || msg != "正文" {
		t.Fatalf("显式 title 错: title=%q msg=%q", title, msg)
	}
}

// ===== buildFilter 的平台格式 (在 win32 包里有独立用例; 这里只验契约) =====

// TestFallbackWritesToStderr 只是确认降级路径不会 panic 且确实写了内容
// (输出内容本身不做断言: 它走 stderr, 断言会污染测试输出)。
func TestFallbackWritesToStderr(t *testing.T) {
	// 只要求不 panic。真正的行为断言在 TestDialogFallsBackWithoutBackend。
	fallbackMessageBox("", "msg")
	fallbackMessageBox("T", "msg")
}

// ===== 断言辅助 =====

// 编译期契约: 测试替身必须满足可选接口, 同步/异步入口的签名也不许被改坏。
var (
	_ nativeDialogHost                       = (*fakeDialogHost)(nil)
	_ func(NativeFileOptions) (string, bool) = openFileBox
)

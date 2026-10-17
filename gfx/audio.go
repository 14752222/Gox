package gfx

import (
	"image"
	"image/color"
	"sync"

	"github.com/14752222/Gox/object"
)

// 音频组件 `<audio>` (S9)。
//
// ## 与 `<video>` 是同一条线: 契约 + 降级
//
// video.go 把"窗口里放一块视频表面"定义成 `nativeVideoHost` (平台视频层),
// 内核自己不碰解码。audio 是它的**无画面孪生体**: 播放语义一模一样
// (src / playing / muted / loop / volume + 六条指令 + 六个事件), 差别只在
// 音频没有矩形、不叠层 —— 输出是进程级的 (声卡), 不属于任何窗口区域。
//
// 所以契约更小:
//
//	AudioInlineSupported() bool
//	AudioAttach(src string, opts AudioPlayOptions) (uint64, error)
//	AudioCommand(id uint64, cmd AudioCommand, value float64) error
//	AudioDetach(id uint64) error
//
// 少掉的 `Rect` / `VideoSurfaceRect` 正是"没有可见表面"的直接后果 ——
// 没有要跟随布局的东西。
//
// ## 降级路径与 video 逐字对称
//
// 后端没有这块能力时 (当前三个桌面后端都没有): 画一条占位 (浅色底 + 声波条),
// 进程级 stderr 告警**一次**, 对该节点派发一次 `onError {code:"unsupported"}`。
// 绝不假装在播 —— 与 video 同一条理由: 静默失败的代价是脚本作者去查一天
// 自己的拼写。
//
// `canIUse("audio")` 照实回答 (native.go 的 CanIUse), 脚本可以"先问再选路":
// 退回系统播放器 (`gx/media.preview`) 或提示用户。
//
// ## 受控语义
//
// 与 video / 其它输入类组件一致: **播放状态只看 prop, 宿主上报只派发事件**。
// `playing` 是受控播放态 (写 `autoplay` 等价于初始 playing=true),
// `muted` / `loop` / `volume` 同理; onPlay / onPause / onTimeUpdate / onEnded
// 回到脚本, 要不要写回 signal 由脚本决定。
//
// ## 谁来实现宿主
//
// 平台音频层 (Windows WASAPI / macOS AVPlayer / Linux PulseAudio) 或纯软件
// 后端 (gfx/softmedia: WAV + PulseAudio dlopen, 无设备时降级 null sink)。
// 断言落空即降级 —— 与 nativeDialogHost / nativeVideoHost 同一套做法,
// **不扩 Surface**。

// ===== 缺省尺寸 =====

const (
	// 没有显式尺寸时的兜底: 一条窄条 (浏览器 <audio controls> 的量级)。
	// 必须非 0 —— 0 尺寸子树会被 drawNode 整支跳过, 用户看到的是"什么都没有",
	// 而不是"这里本该有条音频" (与 image/video 的占位同一条理由)。
	audioDefaultW = 300
	audioDefaultH = 36
)

var (
	colorAudioBackdrop = color.RGBA{R: 0x24, G: 0x25, B: 0x2E, A: 255}  // 占位底
	colorAudioGlyph    = color.RGBA{R: 0xE9, G: 0xEA, B: 0xEE, A: 255}  // 声波条
	colorAudioDisabled = color.RGBA{R: 0xC8, G: 0xC8, B: 0x99, A: 255} // 禁用罩层
)

// ===== 宿主契约 (可选能力) =====

// AudioCommand 是发给平台音频层的控制指令。
type AudioCommand string

const (
	AudioCmdPlay   AudioCommand = "play"
	AudioCmdPause  AudioCommand = "pause"
	AudioCmdSeek   AudioCommand = "seek"   // Value = 目标位置 (秒)
	AudioCmdVolume AudioCommand = "volume" // Value = 0..1
	AudioCmdMute   AudioCommand = "mute"   // Value = 0 / 1
	AudioCmdLoop   AudioCommand = "loop"   // Value = 0 / 1
)

// AudioPlayOptions 是挂载一个音频源时的初始参数。
type AudioPlayOptions struct {
	Muted    bool
	Loop     bool
	Controls bool // 请求平台自带的播放控件 (有就画, 没有就忽略)
	Volume   float64
}

// AudioEvent 是宿主上报回来的播放状态事件。
//
// 与 VideoEvent 同款: 裸结构体, 平台代码 (win32 / cocoa / PulseAudio 绑定)
// 不该为了回填事件去构造 JS 对象, 那是内核这一侧的事。
type AudioEvent struct {
	Kind        string  // ready / play / pause / ended / timeupdate / error
	CurrentTime float64 // 当前播放位置 (秒)
	Duration    float64 // 总时长 (秒), 0 = 未知
	Code        string  // Kind=="error" 时的错误码
	Message     string // Kind=="error" 时的说明
}

// nativeAudioHost 是**平台音频层**的可选能力接口。
//
// 由窗口后端 (Surface 的实现者) 实现, 在调用点断言; 断言落空即降级 ——
// 与 nativeDialogHost / nativeVideoHost 同一套做法, **不扩 Surface**。
//
// 契约要点:
//   - 音频没有矩形: 输出不属于任何窗口区域, 所以没有 Attach(r, …) 与
//     Rect 同步。句柄仅用于把后续指令/事件对回同一个源;
//   - Attach 返回的句柄 > 0, 宿主必须保证它在 Detach 之前有效;
//   - 所有方法都可能在 VM 线程被调用, 宿主自己负责与音频线程串行;
//   - 状态变化用 ResolveAudioEvent(id, …) 回填。
type nativeAudioHost interface {
	// AudioInlineSupported 报告本后端能否播放音频。
	// 返回 false 的后端会被内核当成"没有这块能力"(组件降级, canIUse 为 false)。
	AudioInlineSupported() bool
	// AudioAttach 开始播放 src, 返回 > 0 的句柄。
	AudioAttach(src string, opts AudioPlayOptions) (uint64, error)
	// AudioCommand 下发控制指令 (Value 的含义见各命令)。
	AudioCommand(id uint64, cmd AudioCommand, value float64) error
	// AudioDetach 停止播放并释放资源。
	AudioDetach(id uint64) error
}

// audioHostOf 取某个窗口的平台音频能力。
//
// 与 videoHostOf 同理走窗口 (Surface) 而不是进程级: 能力挂在窗口后端上,
// 没有窗口就无从断言。虽然音频输出本身是进程级的, "这个后端放不了音乐"
// 仍是窗口后端的属性。
func audioHostOf(a *app) (nativeAudioHost, bool) {
	if a == nil {
		return nil, false
	}
	a.mu.Lock()
	s := a.surface
	a.mu.Unlock()
	if s == nil {
		return nil, false
	}
	h, ok := s.(nativeAudioHost)
	if !ok || !h.AudioInlineSupported() {
		return nil, false
	}
	return h, true
}

// AudioInlineSupported 报告"当前窗口有没有音频播放能力"。
//
// 这是 `canIUse("audio")` 在内核侧的判据 (native.go 的 CanIUse 会调它),
// 也是脚本在"内联播放 vs 跳系统播放器"之间做选择的依据。
func AudioInlineSupported() bool {
	_, ok := audioHostOf(currentApp())
	return ok
}

// ===== 包级状态 =====

// audioState 是每个 `<audio>` 节点的宿主侧状态。
//
// 与 videoState 同款理由不进 GuiNode: 只被 audio.go 读写的一整块,
// 放包级 map 里可以让 node.go 只增加"挂了一个音频源"这一处回调。
type audioState struct {
	app     *app
	id      uint64 // 平台音频句柄; 0 = 还没挂上
	applied audioApplied
	// reported 保证"没有平台音频层"对**这一个节点**只派发一次 onError。
	// 按节点而不是按进程: 界面上有 3 个音频条, 脚本该收到 3 次失败,
	// 才能把 3 个条都换成提示。
	reported bool
}

// audioApplied 记录已经发给宿主的受控属性。
//
// 必须记: flush 在**每次 redraw** 都会跑, 无条件重发命令等于每帧都给宿主
// 发一遍 play/pause, 宿主按"用户操作"处理就会不断打断播放。
type audioApplied struct {
	valid   bool
	playing bool
	muted   bool
	loop    bool
	volume  float64
}

var (
	audioMu     sync.Mutex
	audioStates = map[*GuiNode]*audioState{}
	audioWarned bool // "本后端没有平台音频层" 的进程级去重
)

// warnAudioNoHost 是降级告警的出口。做成变量便于单测替换成计数器。
var warnAudioNoHost = func(src string) {
	recordWarn("<audio src=%q> 当前后端没有平台音频层, 已降级为占位; "+
		"内联播放需要后端实现 nativeAudioHost (见 docs/media-decision.md)", src)
}

var warnAudioAttach = func(src string, err error) {
	recordWarn("audio %q 挂载平台音频失败: %v", src, err)
}

// resetAudioStateForTest 清空音频侧包级状态 (跨用例串味的唯一来源)。
func resetAudioStateForTest() {
	audioMu.Lock()
	audioStates = map[*GuiNode]*audioState{}
	audioWarned = false
	audioMu.Unlock()
}

// ===== 属性读取 =====

// audioSrcOf 取音频路径 (空串 = 这个节点还没准备好挂)。
func audioSrcOf(n *GuiNode) string {
	v, _ := n.PropStr("src")
	return v
}

// audioOptions 组出挂载参数 (与 videoOptions 同款, 少 poster/fit 无关项)。
func audioOptions(n *GuiNode) AudioPlayOptions {
	opts := AudioPlayOptions{
		Muted:    propBoolOr(n, "muted", false),
		Loop:     propBoolOr(n, "loop", false),
		Controls: propBoolOr(n, "controls", false),
		Volume:   1,
	}
	if v, ok := n.PropNum("volume"); ok {
		opts.Volume = clampUnit(v)
	}
	return opts
}

// audioInitialPlaying 算初始播放态: `playing` 优先, 没写就看 `autoplay`。
func audioInitialPlaying(n *GuiNode) bool {
	if v, ok := n.PropBool("playing"); ok {
		return v
	}
	return propBoolOr(n, "autoplay", false)
}

// ===== 同步(每次 redraw 开头跑) =====

// flushAudioNodes 把渲染树里的 `<audio>` 节点与平台音频源对齐。
//
// 调用位置与 flushVideoSurfaces 完全一致: "树已接好、还没上屏"的那一刻,
// 且排在读脏标记之前 —— 它可能整帧标脏 (挂上/摘掉都要重画那一块占位)。
func flushAudioNodes(a *app) {
	if a == nil || a.root == nil {
		return
	}
	host, hasHost := audioHostOf(a)

	audioMu.Lock()
	defer audioMu.Unlock()

	live := map[*GuiNode]bool{}
	var walk func(n *GuiNode)
	walk = func(n *GuiNode) {
		if n == nil {
			return
		}
		if n.Tag == "audio" {
			live[n] = true
			audioSyncNode(a, n, host, hasHost)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(a.root)

	// 树上已经找不到的节点 (被 each 重建 / 整支摘掉): 释放它的音频源。
	// 只清理**属于本窗口**的条目 —— 别的窗口有自己的 flush。
	for n, st := range audioStates {
		if st.app != a || live[n] {
			continue
		}
		if st.id != 0 {
			_ = host.AudioDetach(st.id)
		}
		delete(audioStates, n)
	}
}

// audioSyncNode 把单个节点同步到宿主。
func audioSyncNode(a *app, n *GuiNode, host nativeAudioHost, hasHost bool) {
	st := audioStates[n]
	if st == nil {
		st = &audioState{app: a}
		audioStates[n] = st
	}

	if !hasHost {
		// 降级路径: 没写 src 的节点连"想要播放"都不算, 不打扰;
		// 写了 src 的报一次进程级告警 + 对本报一次 onError。
		if audioSrcOf(n) != "" && !st.reported {
			st.reported = true
			if !audioWarned {
				audioWarned = true
				warnAudioNoHost(audioSrcOf(n))
			}
			audioEmitError(a, n, "unsupported",
				"当前后端没有平台音频层, 内联播放不可用; 用 canIUse(\"audio\") 事先判断")
		}
		return
	}

	// 没有 src: 不挂。已经挂了的摘掉 —— 否则会留下一个"界面里已经没有对应
	// 元素"的声源 (与 video 的 src 清零同一条理由)。
	if audioSrcOf(n) == "" {
		audioReleaseLocked(host, n, st)
		return
	}

	if st.id == 0 {
		id, err := host.AudioAttach(audioSrcOf(n), audioOptions(n))
		if err != nil {
			if !st.reported {
				st.reported = true
				warnAudioAttach(audioSrcOf(n), err)
				audioEmitError(a, n, "attach-failed", err.Error())
			}
			return
		}
		st.id = id
		st.applied = audioApplied{} // 还没应用过任何属性
		audioPushCommands(host, n, st, true)
		return
	}

	audioPushCommands(host, n, st, false)
}

// audioPushCommands 把受控属性的**变化**发给宿主 (force=true 时全量发一遍)。
//
// 每轮都重读 prop 而不是只在挂载时读一次: `playing` / `muted` 这些是受控属性,
// 脚本改 signal 之后必须真的传到宿主, 否则"点了播放没反应"。
func audioPushCommands(host nativeAudioHost, n *GuiNode, st *audioState, force bool) {
	want := audioApplied{
		valid:   true,
		playing: audioInitialPlaying(n),
		muted:   propBoolOr(n, "muted", false),
		loop:    propBoolOr(n, "loop", false),
		volume:  audioOptions(n).Volume,
	}
	if !force && st.applied == want {
		return // 无变化: 一条命令都不发 (重发会打断正在进行的播放)
	}
	if force || !st.applied.valid || st.applied.playing != want.playing {
		if want.playing {
			host.AudioCommand(st.id, AudioCmdPlay, 0)
		} else {
			host.AudioCommand(st.id, AudioCmdPause, 0)
		}
	}
	if force || !st.applied.valid || st.applied.muted != want.muted {
		host.AudioCommand(st.id, AudioCmdMute, boolNum(want.muted))
	}
	if force || !st.applied.valid || st.applied.loop != want.loop {
		host.AudioCommand(st.id, AudioCmdLoop, boolNum(want.loop))
	}
	if force || !st.applied.valid || st.applied.volume != want.volume {
		host.AudioCommand(st.id, AudioCmdVolume, want.volume)
	}
	st.applied = want
}

// audioReleaseLocked 摘掉音频源并从状态表删除 (调用方必须已持 audioMu)。
func audioReleaseLocked(host nativeAudioHost, n *GuiNode, st *audioState) {
	if st.id != 0 {
		_ = host.AudioDetach(st.id)
	}
	delete(audioStates, n)
}

// audioRelease 是给 disposeNode 用的出口: 节点离开渲染树时立刻释放音频源。
//
// 不依赖下一次 flush 兜底的理由同 videoRelease: 窗口在子树销毁后可能
// **再也没有重绘** (没有脏区就不进 redraw), 那个声源会一直放到窗口关闭。
func audioRelease(n *GuiNode) {
	audioMu.Lock()
	defer audioMu.Unlock()
	st, ok := audioStates[n]
	if !ok {
		return
	}
	if st.id != 0 {
		if host, hasHost := audioHostOf(st.app); hasHost {
			_ = host.AudioDetach(st.id)
		}
	}
	delete(audioStates, n)
}

// ===== 宿主 → 脚本 事件回填 =====

// ResolveAudioEvent 由平台音频宿主回填一次状态变化。
//
// 句柄对不上 (源已摘 / 窗口已关) 时静默丢弃: 那是"事件在路上、界面已经
// 不要它了"的正常竞态, 不是错误 (与 ResolveVideoEvent 同一语义)。
func ResolveAudioEvent(id uint64, ev AudioEvent) {
	if id == 0 {
		return
	}
	audioMu.Lock()
	var node *GuiNode
	var st *audioState
	for n, s := range audioStates {
		if s.id == id {
			node, st = n, s
			break
		}
	}
	audioMu.Unlock()
	if node == nil || st == nil || st.app == nil {
		return
	}
	a := st.app
	switch ev.Kind {
	case "ready":
		a.callHandler(node, "onReady", nil)
	case "play":
		a.callHandler(node, "onPlay", nil)
	case "pause":
		a.callHandler(node, "onPause", nil)
	case "ended":
		a.callHandler(node, "onEnded", nil)
	case "timeupdate":
		a.callHandler(node, "onTimeUpdate", audioTimeObj(ev))
	case "error":
		code := ev.Code
		if code == "" {
			code = "playback-failed"
		}
		audioEmitError(a, node, code, ev.Message)
	}
}

// audioEmitError 派发一次 onError。没有 VM (纯 Go 单测) 时 callHandler
// 是空操作 —— 与其它回调一致, 不需要在这里特判。
func audioEmitError(a *app, n *GuiNode, code, msg string) {
	o := object.NewObject()
	o.SetProperty("code", object.NewString(code))
	o.SetProperty("message", object.NewString(msg))
	a.callHandler(n, "onError", o)
}

func audioTimeObj(ev AudioEvent) object.Value {
	o := object.NewObject()
	o.SetProperty("currentTime", object.NewNumber(ev.CurrentTime))
	o.SetProperty("duration", object.NewNumber(ev.Duration))
	return o
}

// ===== 绘制 =====

// paintAudio 绘制 `<audio>`: 浅色占位条 + 声波 (三根高低柱)。
//
// 音频没有画面: 占位不是"假装有波形", 而是给 `controls` 一条**看得见的
// 落点** —— 没有它, 界面上就是一个看不见摸不着的空区域, 用户不知道点哪。
// 装饰 (background / border / radius / shadow) 由 paintBoxDecor 先画,
// 自己这条分支里补画 (被 case 截走的标签的固定要求)。
func paintAudio(img *image.RGBA, n *GuiNode, disabled bool) {
	paintBoxDecor(img, n, disabled)
	if n.Box.W <= 0 || n.Box.H <= 0 {
		return
	}
	// 显式写了 background 的节点, 底色归用户 (同 video: 解析失败时
	// paintBoxDecor 自己出声, 这里只负责"用户表达过意图就别盖掉它")。
	ownBG := audioHasBackground(n)
	if !ownBG {
		FillRect(img, n.Box, colorAudioBackdrop)
	}
	paintWaveGlyph(img, n.Box, colorAudioGlyph)
	if disabled {
		FillRect(img, n.Box, colorAudioDisabled)
	}
}

// audioHasBackground 报告节点有没有显式 `background` (判据同 videoHasBackground)。
func audioHasBackground(n *GuiNode) bool {
	v, ok := n.Props["background"]
	return ok && v != nil
}

// paintWaveGlyph 画三根高低柱 (声波/均衡器意象): 中柱最高, 两边矮一截。
//
// 用 FillRect 扫描线, 不引任何矢量库 —— 与 paintPlayGlyph 同一套做法。
// 太矮的条不画 (糊成一团), 太窄的盒子同样不画。
func paintWaveGlyph(img *image.RGBA, r Rect, c color.RGBA) {
	barH := r.H * 46 / 100
	if barH > 20 {
		barH = 20
	}
	if barH < 6 {
		return
	}
	barW := r.W * 4 / 100
	if barW < 3 {
		barW = 3
	}
	if barW > 10 {
		barW = 10
	}
	gap := barW * 2
	if gap < 4 {
		gap = 4
	}
	totalW := barW*3 + gap*2
	if totalW > r.W-8 {
		return // 盒子太窄, 画出来是糊的
	}
	x0 := r.X + (r.W-totalW)/2
	cy := r.Y + r.H/2
	// 三根柱的高度: 40% / 100% / 62% —— 中间高, 一眼是"声波"而不是"三道杠"
	for i, frac := range []int{40, 100, 62} {
		h := barH * frac / 100
		if h < 2 {
			h = 2
		}
		x := x0 + i*(barW+gap)
		FillRect(img, Rect{X: x, Y: cy - h/2, W: barW, H: h}, c)
	}
}

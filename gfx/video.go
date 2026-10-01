package gfx

import (
	"image"
	"image/color"
	"sync"

	"github.com/14752222/Gox/object"
)

// 视频组件 `<video>` (S8)。
//
// ## 为什么内核里没有解码器
//
// 决策与权衡记录在 docs/video-decision.md, 一句话: Gox 的界面是**纯 Go 软件
// 光栅化**, 三平台共用一套往 `*image.RGBA` 上画像素的代码, 没有 GPU、没有平台
// 视频纹理。内联播放意味着 mp4/h264 的逐帧解码 + YUV→RGB + 缩放 + 音视频同步
// 全都要自己扛 —— 纯 Go 解码器撑不起实时, cgo 解码器则直接打破"零 cgo、
// 三平台交叉编译"这条发行根基。
//
// ## 所以这条线的正确形状是"契约 + 降级"
//
//   1. **契约**: 内核把"在这个窗口客户区矩形里放这个视频文件、播放/暂停/跳转"
//      这层语义定义清楚 (nativeVideoHost)。解码与合成交给**平台视频层** ——
//      Windows MF / macOS AVPlayerLayer / Android SurfaceView / iOS AVPlayerLayer。
//      这与 `nativeDialogHost`(原生消息框)、`windowController`(改标题/尺寸)
//      是同一套"可选能力走可选接口、不扩 Surface"的做法。
//   2. **降级**: 后端没接这块能力时(当前的 win32 / X11 / cocoa 都没接),
//      组件画**封面图**(或深色占位 + 播放三角), 并**诚实报错** —— 一次
//      stderr 告警(进 gx/dev 的警告环) + 对该节点派发一次
//      `onError {code:"unsupported"}`。绝不假装在播。
//
// `canIUse("video")` 会照实回答 (见 native.go 的 CanIUse), 所以脚本可以
// 先问再决定"用内联播放还是退回系统播放器"。
//
// ## 受控语义
//
// 与其它输入类组件一致: **显示/播放状态只看 prop, 用户操作只派发事件**。
// `playing` 是受控的播放态 (写 `autoplay` 等价于"初始 playing=true"),
// `muted` / `loop` / `volume` 同理。宿主上报的状态变化经 `ResolveVideoEvent`
// 回到脚本 (onPlay / onPause / onEnded / onTimeUpdate / onError),
// 要不要把 `playing` 写回 signal 由脚本决定。
//
// ## 平台视频表面与脏矩形的关系
//
// 平台表面是**叠在窗口之上的一层**, 不属于 `*image.RGBA` —— 所以它既不在
// drawNode 的绘制里, 也不参与脏矩形 diff。它的位置/尺寸同步放在
// `app.redraw()` 里 **Layout 之后** (`flushVideoSurfaces`) —— 必须在布局算完
// 之后, 否则同步过去的是上一帧的盒子 (首帧全是 0, 表面要等到下一帧才出现);
// 也必须在 Draw 之前, 免得脏区那一遍先白画一遍。

// ===== 缺省尺寸 =====

const (
	// 没有封面图、也没显式写尺寸时的兜底: 16:9。必须非 0 —— 0 尺寸子树会被
	// drawNode 整支跳过, 用户看到的是"什么都没有", 而不是"这里本该有段视频"
	// (与 image 的占位同一条理由)。
	videoDefaultW = 320
	videoDefaultH = 180
)

var (
	colorVideoBackdrop = color.RGBA{R: 0x1E, G: 0x1F, B: 0x24, A: 255}  // 无封面时的底
	colorVideoGlyph    = color.RGBA{R: 0xE9, G: 0xEA, B: 0xEE, A: 255}  // 播放三角
	colorVideoLetter   = color.RGBA{R: 0x00, G: 0x00, B: 0x00, A: 255}  // contain 留边的黑
	colorVideoDisabled = color.RGBA{R: 0xC8, G: 0xC8, B: 0xC8, A: 0x99} // 禁用罩层
)

// ===== 宿主契约 (可选能力) =====

// VideoCommand 是发给平台视频表面的控制指令。
type VideoCommand string

const (
	VideoCmdPlay   VideoCommand = "play"
	VideoCmdPause  VideoCommand = "pause"
	VideoCmdSeek   VideoCommand = "seek"   // Value = 目标位置 (秒)
	VideoCmdVolume VideoCommand = "volume" // Value = 0..1
	VideoCmdMute   VideoCommand = "mute"   // Value = 0 / 1
	VideoCmdLoop   VideoCommand = "loop"   // Value = 0 / 1
)

// VideoSurfaceOptions 是挂载一块视频表面时的初始参数。
type VideoSurfaceOptions struct {
	Muted    bool
	Loop     bool
	Controls bool // 请求平台自带的播放控件 (有就画, 没有就忽略)
	Volume   float64
}

// VideoEvent 是宿主上报回来的播放状态事件。
//
// 用**裸结构体**而不是 map[string]object.Value: 平台代码 (win32 / cocoa /
// Android JNI) 不该为了回填事件去构造 JS 对象, 那是内核这一侧的事。
type VideoEvent struct {
	Kind        string  // ready / play / pause / ended / timeupdate / error
	CurrentTime float64 // 当前播放位置 (秒)
	Duration    float64 // 总时长 (秒), 0 = 未知
	Code        string  // Kind=="error" 时的错误码
	Message     string  // Kind=="error" 时的说明
}

// nativeVideoHost 是**平台视频层**的可选能力接口。
//
// 由窗口后端 (Surface 的实现者) 实现, 在调用点断言; 断言落空即降级 ——
// 与 nativeDialogHost / windowController / capturer 同一套做法, **不扩 Surface**。
//
// 契约要点:
//   - r 是**窗口客户区**坐标 (与事件坐标同一坐标系), 不是屏幕坐标;
//   - Attach 返回的句柄 > 0, 宿主必须保证它在 Detach 之前有效;
//   - 所有方法都可能在 VM 线程被调用, 宿主自己负责与渲染线程串行
//     (与其它可选能力一致: 平台后端要么本来就是同一线程, 要么自己加锁);
//   - 状态变化用 ResolveVideoEvent(surfaceID, …) 回填。
type nativeVideoHost interface {
	// VideoInlineSupported 报告本后端能否在窗口内叠加平台视频表面。
	// 返回 false 的后端会被内核当成"没有这块能力"(组件降级, canIUse 为 false)。
	VideoInlineSupported() bool
	// VideoSurfaceAttach 在客户区矩形 r 处叠加一块播放 src 的视频表面。
	VideoSurfaceAttach(r Rect, src string, opts VideoSurfaceOptions) (uint64, error)
	// VideoSurfaceRect 跟随布局移动/缩放已有表面。
	VideoSurfaceRect(id uint64, r Rect) error
	// VideoSurfaceCommand 下发控制指令 (Value 的含义见各命令)。
	VideoSurfaceCommand(id uint64, cmd VideoCommand, value float64) error
	// VideoSurfaceDetach 移除表面并释放资源。
	VideoSurfaceDetach(id uint64) error
}

// videoHostOf 取某个窗口的平台视频能力。
//
// 走窗口 (Surface) 而不是"进程级": 视频表面是**贴在某个窗口里**的,
// 没有窗口就无从谈位置, 这与 nativeDialogHost 需要一个 owner 窗口同理。
func videoHostOf(a *app) (nativeVideoHost, bool) {
	if a == nil {
		return nil, false
	}
	a.mu.Lock()
	s := a.surface
	a.mu.Unlock()
	if s == nil {
		return nil, false
	}
	h, ok := s.(nativeVideoHost)
	if !ok || !h.VideoInlineSupported() {
		return nil, false
	}
	return h, true
}

// VideoInlineSupported 报告"当前窗口有没有内联播放能力"。
//
// 这是 `canIUse("video")` 在内核侧的判据 (native.go 的 CanIUse 会调它),
// 也是脚本在"内联播放 vs 跳系统播放器"之间做选择的依据。
func VideoInlineSupported() bool {
	_, ok := videoHostOf(currentApp())
	return ok
}

// ===== 包级状态 =====

// videoState 是每个 `<video>` 节点的宿主侧状态。
//
// 为什么不把这些字段挂到 GuiNode 上: 它们是**只被 video.go 读写**的一整块,
// 放包级 map 里可以让 node.go 只增加"挂了一个视频表面"这一处回调,
// 而不是往那个已经很长的结构体里塞四个字段。
type videoState struct {
	app     *app
	id      uint64 // 平台表面句柄; 0 = 还没挂上
	rect    Rect   // 上次同步给宿主的客户区矩形
	applied videoApplied
	// reported 保证"没有平台视频层"对**这一个节点**只派发一次 onError。
	// 按节点而不是按进程: 界面上有 3 个视频框, 脚本该收到 3 次失败,
	// 才能把 3 个框都换成封面/提示。
	reported bool
}

// videoApplied 记录已经发给宿主的受控属性。
//
// 必须记: flush 在**每次 redraw** 都会跑, 无条件重发命令等于每帧都给宿主
// 发一遍 play/pause/seek, 宿主按"用户操作"处理就会不断打断播放。
type videoApplied struct {
	valid   bool
	playing bool
	muted   bool
	loop    bool
	volume  float64
}

var (
	videoMu     sync.Mutex
	videoStates = map[*GuiNode]*videoState{}
	videoWarned bool // "本后端没有平台视频层" 的进程级去重
)

// warnVideoNoHost 是降级告警的出口。做成变量便于单测替换成计数器。
var warnVideoNoHost = func(src string) {
	recordWarn("<video src=%q> 当前后端没有平台视频层, 已降级为封面/占位; "+
		"内联播放需要后端实现 nativeVideoHost (见 docs/video-decision.md)", src)
}

var warnVideoAttach = func(src string, err error) {
	recordWarn("video %q 挂载平台表面失败: %v", src, err)
}

// resetVideoStateForTest 清空视频侧包级状态 (跨用例串味的唯一来源)。
func resetVideoStateForTest() {
	videoMu.Lock()
	videoStates = map[*GuiNode]*videoState{}
	videoWarned = false
	videoMu.Unlock()
}

// ===== 属性读取 =====

// videoSrcOf 取视频路径 (空串 = 这个节点还没准备好挂)。
func videoSrcOf(n *GuiNode) string {
	v, _ := n.PropStr("src")
	return v
}

// videoPosterPath 把 `poster` prop 解析成实际文件路径。
func videoPosterPath(n *GuiNode) string {
	v, _ := n.PropStr("poster")
	return resolveAssetPath(v)
}

// videoPosterNaturalSize 返回封面图的自然尺寸; 没有/加载失败返回 0,0。
func videoPosterNaturalSize(n *GuiNode) (w, h int) {
	p := videoPosterPath(n)
	if p == "" {
		return 0, 0
	}
	e, err := loadImage(p)
	if err != nil {
		return 0, 0
	}
	b := e.img.Bounds()
	return b.Dx(), b.Dy()
}

// videoOptions 组出挂载参数。
func videoOptions(n *GuiNode) VideoSurfaceOptions {
	opts := VideoSurfaceOptions{
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

// videoInitialPlaying 算初始播放态: `playing` 优先, 没写就看 `autoplay`。
func videoInitialPlaying(n *GuiNode) bool {
	if v, ok := n.PropBool("playing"); ok {
		return v
	}
	return propBoolOr(n, "autoplay", false)
}

func propBoolOr(n *GuiNode, name string, def bool) bool {
	if v, ok := n.PropBool(name); ok {
		return v
	}
	return def
}

func clampUnit(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ===== 同步(每次 redraw 开头跑) =====

// flushVideoSurfaces 把渲染树里的 `<video>` 节点与平台视频表面对齐。
//
// 调用位置与理由同 flushRouterViews: 必须是"树已接好、还没上屏"的那一刻,
// 而且要排在读脏标记之前 —— 它可能整帧标脏 (挂上/摘掉表面都要重画那一块)。
func flushVideoSurfaces(a *app) {
	if a == nil || a.root == nil {
		return
	}
	host, hasHost := videoHostOf(a)

	videoMu.Lock()
	defer videoMu.Unlock()

	live := map[*GuiNode]bool{}
	var walk func(n *GuiNode)
	walk = func(n *GuiNode) {
		if n == nil {
			return
		}
		if n.Tag == "video" {
			live[n] = true
			videoSyncNode(a, n, host, hasHost)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(a.root)

	// 树上已经找不到的节点 (被 each 重建 / 整支摘掉): 释放它的表面。
	// 只清理**属于本窗口**的条目 —— 别的窗口有自己的 flush。
	for n, st := range videoStates {
		if st.app != a || live[n] {
			continue
		}
		if st.id != 0 {
			_ = host.VideoSurfaceDetach(st.id)
		}
		delete(videoStates, n)
	}
}

// videoSyncNode 把单个节点同步到宿主。
func videoSyncNode(a *app, n *GuiNode, host nativeVideoHost, hasHost bool) {
	st := videoStates[n]
	if st == nil {
		st = &videoState{app: a}
		videoStates[n] = st
	}

	if !hasHost {
		// 降级路径: 没写 src 的节点连"想要播放"都不算, 不打扰;
		// 写了 src 的报一次进程级告警 + 对本报一次 onError。
		if videoSrcOf(n) != "" && !st.reported {
			st.reported = true
			if !videoWarned {
				videoWarned = true
				warnVideoNoHost(videoSrcOf(n))
			}
			videoEmitError(a, n, "unsupported",
				"当前后端没有平台视频层, 内联播放不可用; 用 canIUse(\"video\") 事先判断")
		}
		return
	}

	// 盒子归零 (还没布局 / 被裁掉) 或没有 src: 不挂表面。已经挂了的摘掉 ——
	// 否则会留下一块"界面里已经没有对应元素"的画面。
	if n.Box.W <= 0 || n.Box.H <= 0 || videoSrcOf(n) == "" {
		videoReleaseLocked(host, n, st)
		return
	}

	if st.id == 0 {
		id, err := host.VideoSurfaceAttach(n.Box, videoSrcOf(n), videoOptions(n))
		if err != nil {
			if !st.reported {
				st.reported = true
				warnVideoAttach(videoSrcOf(n), err)
				videoEmitError(a, n, "attach-failed", err.Error())
			}
			return
		}
		st.id = id
		st.rect = n.Box
		st.applied = videoApplied{} // 还没应用过任何属性
		videoPushCommands(host, n, st, true)
		return
	}

	if st.rect != n.Box {
		if err := host.VideoSurfaceRect(st.id, n.Box); err != nil {
			warnVideoAttach(videoSrcOf(n), err)
		}
		st.rect = n.Box
	}
	videoPushCommands(host, n, st, false)
}

// videoPushCommands 把受控属性的**变化**发给宿主 (force=true 时全量发一遍)。
//
// 每轮都重读 prop 而不是只在挂载时读一次: `playing` / `muted` 这些是受控属性,
// 脚本改 signal 之后必须真的传到宿主, 否则"点了播放没反应"。
func videoPushCommands(host nativeVideoHost, n *GuiNode, st *videoState, force bool) {
	want := videoApplied{
		valid:   true,
		playing: videoInitialPlaying(n),
		muted:   propBoolOr(n, "muted", false),
		loop:    propBoolOr(n, "loop", false),
		volume:  videoOptions(n).Volume,
	}
	if !force && st.applied == want {
		return // 无变化: 一条命令都不发 (重发会打断正在进行的播放)
	}
	if force || !st.applied.valid || st.applied.playing != want.playing {
		if want.playing {
			host.VideoSurfaceCommand(st.id, VideoCmdPlay, 0)
		} else {
			host.VideoSurfaceCommand(st.id, VideoCmdPause, 0)
		}
	}
	if force || !st.applied.valid || st.applied.muted != want.muted {
		host.VideoSurfaceCommand(st.id, VideoCmdMute, boolNum(want.muted))
	}
	if force || !st.applied.valid || st.applied.loop != want.loop {
		host.VideoSurfaceCommand(st.id, VideoCmdLoop, boolNum(want.loop))
	}
	if force || !st.applied.valid || st.applied.volume != want.volume {
		host.VideoSurfaceCommand(st.id, VideoCmdVolume, want.volume)
	}
	st.applied = want
}

func boolNum(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

// videoReleaseLocked 摘掉表面并从状态表删除 (调用方必须已持 videoMu)。
func videoReleaseLocked(host nativeVideoHost, n *GuiNode, st *videoState) {
	if st.id != 0 {
		_ = host.VideoSurfaceDetach(st.id)
	}
	delete(videoStates, n)
}

// videoRelease 是给 disposeNode 用的出口: 节点离开渲染树时立刻释放平台表面。
//
// 不依赖下一次 flush 兜底的理由: 窗口在子树销毁后可能**再也没有重绘**
// (没有脏区就不进 redraw), 那块表面会一直挂在窗口上, 变成关不掉的浮层。
func videoRelease(n *GuiNode) {
	videoMu.Lock()
	defer videoMu.Unlock()
	st, ok := videoStates[n]
	if !ok {
		return
	}
	if st.id != 0 {
		if host, hasHost := videoHostOf(st.app); hasHost {
			_ = host.VideoSurfaceDetach(st.id)
		}
	}
	delete(videoStates, n)
}

// ===== 宿主 → 脚本 事件回填 =====

// ResolveVideoEvent 由平台视频宿主回填一次状态变化。
//
// 句柄对不上 (表面已摘 / 窗口已关) 时静默丢弃: 那是"事件在路上、界面已经不要它了"
// 的正常竞态, 不是错误。
func ResolveVideoEvent(surfaceID uint64, ev VideoEvent) {
	if surfaceID == 0 {
		return
	}
	videoMu.Lock()
	var node *GuiNode
	var st *videoState
	for n, s := range videoStates {
		if s.id == surfaceID {
			node, st = n, s
			break
		}
	}
	videoMu.Unlock()
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
		a.callHandler(node, "onTimeUpdate", videoTimeObj(ev))
	case "error":
		code := ev.Code
		if code == "" {
			code = "playback-failed"
		}
		videoEmitError(a, node, code, ev.Message)
	}
}

// videoEmitError 派发一次 onError。没有 VM (纯 Go 单测) 时 callHandler
// 是空操作 —— 与其它回调一致, 不需要在这里特判。
func videoEmitError(a *app, n *GuiNode, code, msg string) {
	o := object.NewObject()
	o.SetProperty("code", object.NewString(code))
	o.SetProperty("message", object.NewString(msg))
	a.callHandler(n, "onError", o)
}

func videoTimeObj(ev VideoEvent) object.Value {
	o := object.NewObject()
	o.SetProperty("currentTime", object.NewNumber(ev.CurrentTime))
	o.SetProperty("duration", object.NewNumber(ev.Duration))
	return o
}

// ===== 绘制 =====

// paintVideo 绘制 `<video>`: 封面图 (或深色占位 + 播放三角)。
//
// 装饰 (background / border / radius / shadow) 由 paintBoxDecor 先画 ——
// 这是本仓库对"被 case 截走的标签"的固定要求: 自己那条分支里必须补画,
// 否则 `<video background=…>` 会静默失效。
func paintVideo(img *image.RGBA, n *GuiNode, disabled bool) {
	paintBoxDecor(img, n, disabled)
	if n.Box.W <= 0 || n.Box.H <= 0 {
		return
	}
	// 显式写了 background 的节点, 底色**归用户**: 我们不再盖自己的深色底/黑边,
	// 否则 `<video background="#123">` 会静默失效 (装饰被内容盖住)。
	// 没写 background 时才铺"视频该有的样子"。
	ownBG := videoHasBackground(n)

	if e, err := loadImage(videoPosterPath(n)); err == nil {
		mode := videoFit(n)
		r, ok := videoFitRect(n.Box, e.img.Bounds().Dx(), e.img.Bounds().Dy(), mode)
		if ok {
			if mode == "contain" && !ownBG {
				// contain 的留边铺黑: 不铺的话会露出父容器的底色, 观感上像
				// "这块区域被谁切掉了一角"。
				FillRect(img, n.Box, colorVideoLetter)
			}
			blitNearest(img, r, e.img)
		}
	} else {
		if !ownBG {
			FillRect(img, n.Box, colorVideoBackdrop)
		}
		paintPlayGlyph(img, n.Box, colorVideoGlyph)
	}
	if disabled {
		FillRect(img, n.Box, colorVideoDisabled)
	}
}

// videoHasBackground 报告节点有没有显式 `background`。
//
// 判据是"属性存在"而不是"解析出有效颜色": 解析失败时 paintBoxDecor 自己会
// 出声 (recordWarn), 这里只负责"用户表达过意图就别盖掉它"。
func videoHasBackground(n *GuiNode) bool {
	v, ok := n.Props["background"]
	return ok && v != nil
}

// videoFit 取 fit prop, 认 contain(缺省) / cover / fill, 以及别名
// stretch / contain 的等价写法。不认识的值按 contain 处理。
func videoFit(n *GuiNode) string {
	v, _ := n.PropStr("fit")
	switch v {
	case "cover":
		return "cover"
	case "fill", "stretch":
		return "fill"
	default:
		return "contain"
	}
}

// videoFitRect 按 fit 模式算出"源图应该画到哪个矩形"。
//
// 返回 ok=false 表示矩形退化 (尺寸为 0), 调用方跳过绘制。
// cover 会返回**比盒更大的矩形** —— 多出来的部分由 blitNearest 的脏区裁剪
// 自动吃掉 (它按传入子图取可视区), 所以这里不需要自己裁剪。
func videoFitRect(box Rect, sw, sh int, mode string) (Rect, bool) {
	if box.W <= 0 || box.H <= 0 || sw <= 0 || sh <= 0 {
		return Rect{}, false
	}
	switch mode {
	case "fill":
		return box, true
	case "cover":
		// 取较大的缩放比: 短边也要铺满。
		// 用整数比较代替浮点, 避免边界上的 1px 抖动。
		if int64(box.W)*int64(sh) >= int64(box.H)*int64(sw) {
			// 宽度是约束 (按宽放大)
			h := int(int64(box.W) * int64(sh) / int64(sw))
			return Rect{X: box.X, Y: box.Y + (box.H-h)/2, W: box.W, H: h}, h > 0
		}
		w := int(int64(box.H) * int64(sw) / int64(sh))
		return Rect{X: box.X + (box.W-w)/2, Y: box.Y, W: w, H: box.H}, w > 0
	default: // contain
		if int64(box.W)*int64(sh) <= int64(box.H)*int64(sw) {
			h := int(int64(box.W) * int64(sh) / int64(sw))
			return Rect{X: box.X, Y: box.Y + (box.H-h)/2, W: box.W, H: h}, h > 0
		}
		w := int(int64(box.H) * int64(sw) / int64(sh))
		return Rect{X: box.X + (box.W-w)/2, Y: box.Y, W: w, H: box.H}, w > 0
	}
}

// paintPlayGlyph 画一个朝右的实心三角 (用扫描线, 不引任何矢量库)。
//
// 几何: 顶点 (x0, y0) / (x0, y0+h) / (x0+w, y0+h/2) —— 左边是竖直的直角边,
// 这是通行的播放键形状。第 i 行的宽度按到中线的距离线性收窄。
func paintPlayGlyph(img *image.RGBA, r Rect, c color.RGBA) {
	h := r.H * 42 / 100
	if h > 48 {
		h = 48
	}
	if h > r.H-4 {
		h = r.H - 4
	}
	if h < 6 {
		return // 太小了画出来只是一团糊
	}
	w := h * 3 / 4
	if w > r.W-8 {
		w = r.W - 8
	}
	if w < 4 {
		return
	}
	x0 := r.X + (r.W-w)/2
	y0 := r.Y + (r.H-h)/2
	for i := 0; i < h; i++ {
		d := i
		if h-1-i < d {
			d = h - 1 - i
		}
		span := w * d * 2 / (h - 1)
		if span <= 0 {
			continue
		}
		FillRect(img, Rect{X: x0, Y: y0 + i, W: span, H: 1}, c)
	}
}

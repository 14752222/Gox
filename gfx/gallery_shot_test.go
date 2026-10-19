package gfx

// ===== 组件画廊截图生成器 (T08 收尾) =====
//
// 干什么: 把 testdata/shots/*.js 逐个挂到假 Surface 上渲染, 取**真实上屏的那一帧**
// 写成 PNG —— 画廊里贴的图就是真后端会呈现的像素, 不是手画的示意图。
//
// 为什么走离屏而不是"开个真窗口截图":
//   1. 组件画廊要的是**组件本身**。真窗口截图会带上窗口装饰、桌面背景与缩放,
//      同一张图在 125% / 200% DPI 下还不一样, 没法当作可复现的文档素材;
//   2. 离屏不需要桌面会话 —— Linux CI 无头也能跑, 于是三平台矩阵跑得起来
//      (win32 / x11 / cocoa 的窗口创建在 runner 上要么没条件、要么不可复现,
//      但**光栅化 + 平台字体栈**这条链三平台都能真跑, 字体差异正好在这里现形)。
//
// 平台差异是预期的: 字体来自各平台系统字体 (见 font.go 的候选链), 所以
// **同一脚本在不同平台产出的 PNG 不逐像素相等**。产物因此按 GOOS 分目录
// (<out>/darwin/button.png …), 三平台各存一套; 仓库里提交的是 darwin 那一套,
// 另外两套由 CI 矩阵当 artifact 出 (见 .github/workflows/desktop-shots.yml)。
// 这也意味着**不要**把 PNG 当 golden 文件做逐像素比对 —— 那不是它能承担的职责。
//
// 用法:
//   go test ./gfx/ -run TestGalleryShotScripts -v            # 只渲染 + 断言非空白
//   GOX_SHOTS_OUT=website/public/components/shots go test ./gfx/ -run TestGalleryShotScripts
//   GOX_SHOTS_OUT=/tmp/shots GOX_SHOTS_STATS=1 go test ./gfx/ -run TestGalleryShotScripts -v
//
// GOX_SHOTS_OUT 未设时**不写文件** (普通 `go test ./gfx/` 该是纯只读的),
// 但断言照跑 —— 每个截图脚本都是"这个组件能画出来"的活体回归用例。

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/font/sfnt"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// galleryScriptDir 是截图脚本所在目录 (相对 gfx/ 包)。
const galleryScriptDir = "../testdata/shots"

// galleryClickTargets 是需要"先点开再截图"的脚本 → 点哪个标签的实例。
// 只列弹层类: 收起态的日历/色板就是一行灰字, 拍出来说明不了任何事;
// 展开态才是要拍的那个样子 (而且展开态顺带覆盖了 buildXxxPopup 的绘制分支)。
var galleryClickTargets = map[string]string{
	"select":      "select",
	"datepicker":  "datepicker",
	"colorpicker": "colorpicker",
	// tree 的展开态同样是运行时状态（treeOpen），不点开就只有根节点一行 ——
	// 收起态拍不出"同级缩进 + 叶子占位对齐"这两处最易错位的几何。
	"tree": "tree",
}

// galleryHoverTargets 是需要"先悬停再截图"的脚本 → 悬停到哪个标签的实例上。
//
// 与点击不同：悬停弹层 (tooltip) 有 wall-clock 延迟 (缺省 500ms)，而且
// EventMouseLeave 会立刻把它关掉 —— 所以这类脚本不走 settle 轮的 MouseLeave，
// 而是「移入 → 空转一轮（泵被 nextTooltipWake 钳到延迟到点，醒来物化弹层）
// → 再移入一次取稳定帧」。空转轮绝不能推事件：推了就睡不到延迟到点。
var galleryHoverTargets = map[string]string{
	"tooltip": "tooltip",
}

// gallerySettleRounds 是取帧前的"空转"轮数 (每轮投一个无害的 EventMouseLeave)。
//
// 为什么不是 0: 首帧在 Mount 里就出了, 但脚本挂载后可能还有一轮副作用要跑
// (signal 写入 → effect → 标脏 → 重绘)。空转轮让这些落到画面上再取帧,
// 取到的是"稳定态"而不是"半成品"。
const gallerySettleRounds = 2

// 非空白判据 (阈值取自实测: 参见各脚本的实际统计)。
//
// 为什么要这条判据: 四处注册表漏登记一个标签、layoutNode 少一个 case, 症状都是
// "渲染成空盒子" —— 但进程不报错、测试也全绿。画面上**颜色种类**与**非底色像素**
// 是这种静默失效最直接的探针 (空盒子 = 只剩窗口底色一种颜色)。
//
// 阈值取得比实测值低不少: 判据要抓的是"什么都没画", 不是"少画了一个像素";
// 字体渲染跨平台有细微差别, 卡太紧会在某个平台上无故变红。
const (
	galleryMinColors = 8   // 实测最小的一份 (label) 也有 ~40 种
	galleryMinInk    = 400 // 实测最小的一份 (label) 也有 ~1500 个非底色像素
)

// galleryShapeOnlyScripts 是"本身就是纯几何"的脚本 —— 它们没有文字，也就
// 没有字形抗锯齿产生的那几百种过渡色，画面里只有轨道色/填充色/背景色三种。
//
// 为什么给它们单独一条判据而不是整体下调阈值: 颜色数是"组件被画成空盒子"
// 最灵敏的探针，整体下调等于对所有组件放宽（空盒子也能过）。这里只对确知
// 没有文字的组件放宽，且**非底色像素阈值保持不变** —— 那才是"真的画了东西"
// 的证据。
var galleryShapeOnlyScripts = map[string]bool{
	"progress": true, // 纯矩形轨道 + 纯色填充，无文字无抗锯齿
}

// galleryMinColorsFor 返回某个脚本适用的颜色数下限。
func galleryMinColorsFor(name string) int {
	if galleryShapeOnlyScripts[strings.TrimSuffix(name, ".js")] {
		return 3
	}
	return galleryMinColors
}

// galleryFactory 是"按 <window> 声明的尺寸开面"的工厂。
//
// 假 Surface 自带 400x300 的固定尺寸, 而画廊脚本各有各的 <window width height>
// (日历要 380 高, 开关只要 190)。真后端按 WindowConfig 开窗, 所以这里也让面
// 跟着 cfg 走 —— 否则布局会按 400x300 算, 截出来的图与脚本声明不符。
type galleryFactory struct{ s *fakeSurface }

func (g *galleryFactory) Create(cfg WindowConfig) (Surface, error) {
	if cfg.Width > 0 && cfg.Height > 0 {
		g.s.w, g.s.h = cfg.Width, cfg.Height
	}
	return g.s, nil
}

// TestGalleryShotScripts 逐个脚本渲染并断言"画面上真有东西"。
func TestGalleryShotScripts(t *testing.T) {
	names := galleryScriptNames(t)
	out := os.Getenv("GOX_SHOTS_OUT")
	for _, name := range names {
		t.Run(strings.TrimSuffix(name, ".js"), func(t *testing.T) {
			img := renderGalleryShot(t, name)
			// 先记字体再断言: 跨平台"中文整体不渲染"的根因只有一条 —— 选中的
			// 基础字体没有 CJK 字形 (Gox 单字体、无逐字形回退, 见 font.go 文件头),
			// 而断言能看到的症状只是"颜色数塌了"。失败子测试的日志 go test 会
			// 打出来, CI 的证据注入再把它带进注解 —— 省掉"为取一条诊断信息
			// 推一次 CI"。
			t.Logf("字体: %s", galleryFontProbe())
			galleryAssertNotBlank(t, name, img)
			if statsOn() {
				t.Logf("%s: %dx%d, %s", name, img.Bounds().Dx(), img.Bounds().Dy(), galleryInkStats(img).String())
			}
			if out != "" {
				galleryWritePNG(t, out, name, img)
			}
		})
	}
}

// galleryScriptNames 列出 testdata/shots/ 下的脚本 (排序后返回, 保证顺序稳定)。
func galleryScriptNames(t *testing.T) []string {
	t.Helper()
	ents, err := os.ReadDir(galleryScriptDir)
	if err != nil {
		t.Fatalf("读截图脚本目录: %v", err)
	}
	var names []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".js") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		t.Fatalf("%s 里一个脚本都没有", galleryScriptDir)
	}
	return names
}

// renderGalleryShot 渲染一个脚本并返回最后一帧。
//
// 事件纪律与 runDemoSteps 同源 (一轮一个事件): 假 Surface 的 events 通道容量 16、
// WaitEvents 每轮只消费一个唤醒信号, 一次塞两个事件会让第二轮拿到的是"上一轮剩下
// 的那个", 步骤与轮次就错位了。这里要的那两步点击因此拆成两个轮次。
func renderGalleryShot(t *testing.T, name string) *image.RGBA {
	t.Helper()
	path := filepath.Join(galleryScriptDir, name)
	// 演示脚本可能注册定时器, 而调度器是进程级单例 (同 runDemoSteps 的理由)。
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	fake := newFakeSurface()
	SetDefaultFactory(&galleryFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	// 走 EvalFileVM 而不是 EvalVM: 前者的 moduleBase 是**脚本所在目录**
	// (testdata/shots/), 截图脚本里 `import "../datav/dashboard.js"` 这类
	// 相对导入才会按文件路径解析 —— 与 `./gox testdata/shots/x.js` 直接
	// 运行的语义一致 (EvalVM 以进程 CWD 为基准, 跑在 gfx/ 包目录下会把
	// ../ 指到仓库外)。无 import 的脚本两者等价。
	v, err := vm.EvalFileVM(path)
	if err != nil {
		t.Fatalf("EvalFileVM: %v", err)
	}
	a := currentApp()
	if a == nil {
		t.Fatalf("脚本没有挂上窗口")
	}

	// 步骤表: 每轮 pump 执行一步; 表走完后那一轮取帧并关窗。
	var steps []func()
	base := strings.TrimSuffix(name, ".js")
	if tag, ok := galleryHoverTargets[base]; ok {
		node := findFirst(a.root, tag)
		if node == nil {
			t.Fatalf("脚本里没有 <%s> 节点, 没法悬停它", tag)
		}
		if node.Box.W <= 0 || node.Box.H <= 0 {
			t.Fatalf("<%s> 没有布局尺寸 (%v), 布局阶段可能就漏了它", tag, node.Box)
		}
		x, y := centerOf(node)
		steps = append(steps,
			func() { fake.push(Event{Kind: EventMouseMove, X: x, Y: y}) },
			func() {}, // 空转: 泵睡到 tooltip 延迟到点 (见 galleryHoverTargets)
			func() { fake.push(Event{Kind: EventMouseMove, X: x, Y: y}) },
		)
	} else {
		if tag, ok := galleryClickTargets[base]; ok {
			field := findFirst(a.root, tag)
			if field == nil {
				t.Fatalf("脚本里没有 <%s> 节点, 没法点开它", tag)
			}
			if field.Box.W <= 0 || field.Box.H <= 0 {
				t.Fatalf("<%s> 没有布局尺寸 (%v), 布局阶段可能就漏了它", tag, field.Box)
			}
			x, y := centerOf(field)
			steps = append(steps,
				func() { fake.push(Event{Kind: EventMouseDown, X: x, Y: y}) },
				func() { fake.push(Event{Kind: EventMouseUp, X: x, Y: y}) },
			)
		}
		for i := 0; i < gallerySettleRounds; i++ {
			steps = append(steps, func() { fake.push(Event{Kind: EventMouseLeave}) })
		}
	}

	round := 0
	animWait := 0
	var frame *image.RGBA
	pump := func(maxWait time.Duration) bool {
		if round < len(steps) {
			steps[round]()
			round++
			return Pump(maxWait)
		}
		// 取帧前先等抽屉滑入到位 (Prism S6): 动画心跳是 16ms 定时器, 一轮
		// pump 只推一帧 —— 步骤表走完就取帧拍到的是"滑到一半"的中间态
		// (遮罩只淡入了一点、面板底还没铺到静止位置)。上限 40 轮是死循环
		// 保险 (spinner/skeleton 那类无限加载动画不走这条 —— 它们不在
		// drawerAnimateNodes 里)。
		if len(drawerAnimateNodes(a.root)) > 0 && animWait < 40 {
			animWait++
			// 绝不能推事件: 有事件泵会立刻醒来, 16ms 的心跳定时器就永远
			// 轮不到到期 (实测: 每轮推 MouseLeave 时进度停在 0)。空转一轮,
			// 泵睡到 maxWait (= 下一帧心跳) 醒来, animTick 推进一帧。
			return Pump(maxWait)
		}
		// 取帧必须在关窗**之前**: 关窗那一轮会把窗口从注册表摘掉, 之后的
		// 帧再也不会来 (而 shotsImage 读的是"最近一次上屏的帧")。
		frame = shotsImage(fake)
		// 画布尺寸断言同样赶在关窗前: 树摘掉后 Box 还是旧值, 但趁手里有
		// 活树一并查了, 诊断信息 (哪个画布没拿到尺寸) 更实。
		galleryAssertCanvasesLaidOut(t, name, a.root)
		fake.push(Event{Kind: EventClose})
		return Pump(maxWait)
	}
	if err := v.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if frame == nil {
		t.Fatalf("窗口一帧都没上屏 (渲染链路没走到 Show)")
	}
	return frame
}

// galleryInk 是"这一帧画了多少东西"的统计 (判据与诊断共用一份口径)。
type galleryInk struct {
	Colors int // 颜色种类 (含抗锯齿产生的过渡色)
	Ink    int // 非底色像素数
}

func (g galleryInk) String() string {
	return "颜色 " + strconv.Itoa(g.Colors) + " 种 · 非底色像素 " + strconv.Itoa(g.Ink)
}

func galleryInkStats(img *image.RGBA) galleryInk {
	b := img.Bounds()
	bg := img.RGBAAt(b.Min.X, b.Min.Y)
	seen := map[color.RGBA]struct{}{}
	ink := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := img.RGBAAt(x, y)
			seen[c] = struct{}{}
			if c != bg {
				ink++
			}
		}
	}
	return galleryInk{Colors: len(seen), Ink: ink}
}

// galleryAssertNotBlank 是"组件没被画成空盒子"的判据。
func galleryAssertNotBlank(t *testing.T, name string, img *image.RGBA) {
	t.Helper()
	st := galleryInkStats(img)
	minColors := galleryMinColorsFor(name)
	if st.Colors < minColors || st.Ink < galleryMinInk {
		t.Fatalf("%s: 画面近乎空白 (%s, 阈值 颜色>=%d 非底色像素>=%d) —— "+
			"组件可能被画成了空盒子, 先查四处注册表 (node.go / layout.go / raster.go)",
			name, st, minColors, galleryMinInk)
	}
}

// galleryAssertCanvasesLaidOut 断言每个自绘画布都拿到了非零布局盒。
//
// 为什么整帧判据之外还要单查画布: "组件画成空盒子"有两种形态, 整帧的
// 颜色/墨量判据只能抓第一种 (整个画面空)。第二种是**单块画布没尺寸**:
// 组件对 width prop 做算术时传入百分比字符串 ("24%") —— 字符串减数字得
// NaN, 布局把 NaN 当 0/负值收下, onDraw 在空盒上跑完不报错, 那一块
// 内容静默消失, 而同屏别的面板照样 rich, 整帧断言照旧绿 (2026-10-15
// 实测: KPI 卡的四个大数字就是这么没的)。逐个节点查盒才抓得到。
func galleryAssertCanvasesLaidOut(t *testing.T, name string, root *GuiNode) {
	t.Helper()
	for _, c := range findAll(root, "canvas") {
		if c.Box.W <= 0 || c.Box.H <= 0 {
			t.Fatalf("%s: canvas 布局盒为 %v (有 onDraw 但没拿到尺寸) —— "+
				"查组件里对 width/height prop 的算术: 百分比字符串参与运算会得 NaN",
				name, c.Box)
		}
	}
}

// galleryWritePNG 把一帧写进 <outDir>/<GOOS>/<脚本名>.png。
//
// 分平台目录的理由见文件头: 字体来自系统字体, 三平台像素本就不同, 混在一个
// 目录里会互相覆盖, 也看不出"这次变化是代码引起的还是换了平台引起的"。
func galleryWritePNG(t *testing.T, outDir, name string, img *image.RGBA) {
	t.Helper()
	dir := filepath.Join(outDir, runtime.GOOS)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录 %s: %v", dir, err)
	}
	path := filepath.Join(dir, strings.TrimSuffix(name, ".js")+".png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("建文件 %s: %v", path, err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("编码 PNG %s: %v", path, err)
	}
	t.Logf("写出 %s (%dx%d)", path, img.Bounds().Dx(), img.Bounds().Dy())
}

// statsOn 报告是否要打印每张图的统计 (调阈值时用)。
func statsOn() bool { return os.Getenv("GOX_SHOTS_STATS") != "" }

// galleryFontProbe 报告"这一帧用的是哪个字体、它到底有没有 CJK 字形、候选表
// 里还有没有别的选择"。
//
// 为什么要有它: 跨平台"中文整体不渲染"的根因只有一条 —— 选中的基础字体没有
// CJK 字形, 而 Gox 是**单字体、无逐字形回退**(见 font.go 文件头), 于是整屏
// 中文静默消失。画廊断言能看到的只是"颜色数塌了", 光看那个数字分不清是组件
// 被画成空盒子还是字体选错了。把族名 / 字形覆盖 / 候选表头写出来, 一眼定位。
func galleryFontProbe() string {
	initFontCandidates()
	fam := baseFamilyKey()
	if fam == "" {
		fam = "(取不到)"
	}
	covered := "未知"
	if f, err := loadBaseFont(); err == nil {
		if fontCoversCJK(f) { // opentype.Font 是 sfnt.Font 的别名, 直接传
			covered = "是"
		} else {
			covered = "否"
		}
	}
	const headN = 5
	n := len(fontCandidates)
	if n > headN {
		n = headN
	}
	names := make([]string, 0, n)
	for _, p := range fontCandidates[:n] {
		name := filepath.Base(p)
		if looksCJK(name) {
			name += "[名像CJK]"
		}
		names = append(names, name)
	}
	// 目录扫描的命中数要单独报: 它与候选总数的差就是"静态兜底有几条"。
	// 扫描命中变成 0 是**共享状态被清掉**的典型症状 (见 font_test.go 里那条
	// cleanup 的教训), 不是"这台机器没字体"。
	return fmt.Sprintf("族=%q 有CJK字形=%s｜候选 %d 条 (目录扫描命中 %d 条), 前 %d 条: %s",
		fam, covered, len(fontCandidates), len(scannedFonts), n, strings.Join(names, ", "))
}

// cjkProbeRunes 是"这个字体到底能不能画中文"的探针码位 (挑常用字)。
//
// 判据必须是 **glyph != 0**: sfnt 在码位没有字形时**不报错**, 而是回落到
// .notdef (glyph 0) 并返回 nil —— 只看 error 会得到"每个字体都覆盖中文"这个
// 假结论 (第一版就是这么写的, 于是 DejaVu Sans 被判成"有 CJK 字形")。
var cjkProbeRunes = []rune("中文你好的")

// fontCoversCJK 报告字体是否对全部探针码位都有**真**字形 (非 .notdef)。
func fontCoversCJK(f *sfnt.Font) bool {
	var buf sfnt.Buffer
	for _, r := range cjkProbeRunes {
		if g, err := f.GlyphIndex(&buf, r); err != nil || g == 0 {
			return false
		}
	}
	return true
}

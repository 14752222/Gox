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
	src, err := os.ReadFile(filepath.Join(galleryScriptDir, name))
	if err != nil {
		t.Fatalf("读脚本: %v", err)
	}
	// 演示脚本可能注册定时器, 而调度器是进程级单例 (同 runDemoSteps 的理由)。
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	fake := newFakeSurface()
	SetDefaultFactory(&galleryFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(string(src))
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	a := currentApp()
	if a == nil {
		t.Fatalf("脚本没有挂上窗口")
	}

	// 步骤表: 每轮 pump 执行一步; 表走完后那一轮取帧并关窗。
	var steps []func()
	if tag, ok := galleryClickTargets[strings.TrimSuffix(name, ".js")]; ok {
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

	round := 0
	var frame *image.RGBA
	pump := func(maxWait time.Duration) bool {
		if round < len(steps) {
			steps[round]()
			round++
			return Pump(maxWait)
		}
		// 取帧必须在关窗**之前**: 关窗那一轮会把窗口从注册表摘掉, 之后的
		// 帧再也不会来 (而 shotsImage 读的是"最近一次上屏的帧")。
		frame = shotsImage(fake)
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
	if st.Colors < galleryMinColors || st.Ink < galleryMinInk {
		t.Fatalf("%s: 画面近乎空白 (%s, 阈值 颜色>=%d 非底色像素>=%d) —— "+
			"组件可能被画成了空盒子, 先查四处注册表 (node.go / layout.go / raster.go)",
			name, st, galleryMinColors, galleryMinInk)
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

package gfx

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// 文字渲染子系统 (P3)。
//
// 字体来源: 系统字体文件, 经 x/image/font/opentype 解析。按字号懒建 face,
// rune → glyph 掩码做 LRU 缓存 (键 = 字号|rune)。不做复杂 shaping: 中西文按
// 码位直排 (任务书 P3 约定; 连字/复杂脚本不支持)。
//
// **缓存进去的掩码必须是深拷贝** (cloneAlpha): face.Glyph 复用同一块 Pix 并
// 反复返回同一个 *image.Alpha, 直接缓存它的指针 = 整屏文字同一个字形。见 cloneAlpha。
//
// 候选字体按平台组装 (见 fontCandidatesForOS)。Windows 的字体目录与文件名
// 稳定, 可以写死; Linux 各发行版差异极大 (Noto / WenQuanYi / Droid 命名毫无
// 规律), 只能真去扫目录 —— 这就是 P3-7 修的"Linux 上文字完全不渲染"。

// fontCandidates 系统字体候选, 依次尝试首个可解析者。
// 有效顺序恒为: 宿主注入 > 目录扫描 > 静态候选 (由 rebuildCandidatesLocked 组装)。
var fontCandidates = fontCandidatesForOS()

// injectedFonts 是宿主通过 SetFontPath 注入的字体 (优先级最高)。
// 移动端的常见用法: APK/IPA 自带字体 → 解到沙箱 → 把绝对路径交进来。
var injectedFonts []string

// scannedFonts 是目录扫描的结果 (initFontCandidates 填, 只填一次)。
var scannedFonts []string

// rebuildCandidatesLocked 重算生效候选表。调用方必须持有 fontMu。
func rebuildCandidatesLocked() {
	next := make([]string, 0, len(injectedFonts)+len(scannedFonts)+4)
	next = append(next, injectedFonts...)
	if runtime.GOOS == "darwin" || runtime.GOOS == "ios" {
		// 苹果系静态候选排在扫描结果之前: 路径受系统管控恒存在 (模拟器
		// 实测), 而目录扫描会先撞上 .SF Numeric 这类"专用子字体"
		// (ADTNumeric.ttc face 0, 只有数字/大写/标点, 小写与 CJK 全缺字)
		// —— 实测整屏小写与中文不渲染, 只有 ": 0"。
		next = append(next, fontCandidatesForOS()...)
		next = append(next, scannedFonts...)
	} else {
		// 其余平台保持"扫描优先": 静态路径在部分发行版/精简镜像里根本不存在,
		// 扫描到的"这台机器上确实存在"的字体更可靠。
		next = append(next, scannedFonts...)
		next = append(next, fontCandidatesForOS()...)
	}
	fontCandidates = next
}

// SetFontPath 注入宿主自带的字体文件, 排在所有候选之前 (先试注入的, 再退到系统字体)。
//
// 为什么移动端必须有它: Android 定制 ROM 的字体命名无规律, iOS 的沙箱根本读不到
// 系统字体 —— "随包分发一份字体"是这两个平台上唯一可控的做法 (也正好顺带满足
// App Store 关于脚本/资源随包分发的约束)。
//
// 时机与代价: 首次渲染前调用是零成本的。若在字体已加载之后调用, 本函数会清掉
// 已缓存的 face 与字形掩码 (旧掩码属于旧字体) 并立即重载, 所以**别在动画中间调**。
// 重复注入同一路径幂等。
func SetFontPath(paths ...string) {
	fontMu.Lock()
	defer fontMu.Unlock()
	added := false
	for _, p := range paths {
		if p == "" || containsString(injectedFonts, p) {
			continue
		}
		injectedFonts = append(injectedFonts, p)
		added = true
	}
	if !added {
		return
	}
	rebuildCandidatesLocked()
	// 已有缓存说明字体已经用过: 不重置的话新字体不会生效 (baseFont 是缓存值),
	// 或者更糟 —— 新 face 配旧字形掩码。
	if baseFont != nil || len(faceBySize) > 0 {
		baseFont = nil
		faceBySize = map[int]font.Face{}
		glyphLRU.reset()
	}
}

// containsString 小工具: 判断切片里是否已有该字符串 (候选表只有几十条, 线性扫即可)。
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// fontCandsOnce 保证扫描型候选只收集一次 (扫描要真解析字体头, 不便宜)。
var fontCandsOnce sync.Once

// fontScanLimit 是递归扫描的**条目**数上限 (含目录)。深目录 + 大字体集合时
// 无限递归会把首帧拖住, 宁可少找几个字体也不能卡住界面。
const fontScanLimit = 2000

// fontCandidatesForOS 返回当前平台的静态候选 (不含扫描结果)。
func fontCandidatesForOS() []string {
	switch runtime.GOOS {
	case "windows":
		return []string{
			`C:\Windows\Fonts\msyh.ttc`,   // 微软雅黑
			`C:\Windows\Fonts\msyhbd.ttc`, // 微软雅黑 粗体
			`C:\Windows\Fonts\simsun.ttc`, // 宋体
			`C:\Windows\Fonts\segoeui.ttf`,
		}
	case "darwin":
		// 顺序即优先级, 首个可解析者成为 baseFont: 需同时覆盖 CJK 与拉丁。
		// PingFang.ttc 在旧版 macOS 存在, 新版 (26+) 已并入 dyld 共享缓存
		// 读不到文件 —— Hiragino Sans GB / STHeiti 是新版实测存在的 CJK。
		return []string{
			"/System/Library/Fonts/PingFang.ttc",
			"/System/Library/Fonts/Hiragino Sans GB.ttc",
			"/System/Library/Fonts/STHeiti Medium.ttc",
			"/System/Library/Fonts/Supplemental/Arial.ttf",
			"/Library/Fonts/Arial.ttf",
		}
	case "android":
		// Android 的系统字体在只读系统分区, 路径稳定。顺序即优先级: 先官方 CJK
		// (NotoSansCJK, 中文机型必装), 再老设备的 DroidSansFallback, 最后
		// Roboto —— 它只是"至少有字"的拉丁兜底, 中文会画成豆腐块, 所以绝不能
		// 排在任何 CJK 字体前面。
		return []string{
			"/system/fonts/NotoSansCJK-Regular.ttc",
			"/system/fonts/NotoSansCJK-VF.otf.ttc",
			"/system/fonts/DroidSansFallback.ttf",
			"/system/fonts/NotoSansSC-Regular.otf",
			"/system/fonts/Roboto-Regular.ttf",
		}
	case "ios":
		// iOS 的沙箱在真机上读不到系统字体文件 (字体在 dyld 共享缓存里),
		// 所以真机真正可靠的做法是**宿主注入随包字体** (gfx.SetFontPath, 见下),
		// 这里的候选只是尽力而为。模拟器读得到文件, 且新 runtime (26+) 的
		// 字体布局与新版 macOS 同代: PingFang 已不存在, Hiragino 系列在
		// Core/ 子目录 (实测)。
		return []string{
			"/System/Library/Fonts/PingFang.ttc",
			"/System/Library/Fonts/Core/HiraginoKakuGothic.ttc",
			"/System/Library/Fonts/CoreUI/HiraginoMaruGothProN.ttc",
			"/System/Library/Fonts/AppFonts/HiraginoMincho.ttc",
			"/System/Library/Fonts/Core/SFUI.ttf", // 拉丁兜底
		}
	default:
		// 常见发行版的兜底路径; 主力候选靠目录扫描补齐。
		return []string{
			"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
			"/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf",
		}
	}
}

// fontScanDirs 返回当前平台需要递归扫描的字体目录 (按优先级)。
//
// Windows 刻意不给扫描目录: 静态候选已经可靠, 而扫 C:\Windows\Fonts 要解析
// 几百个 ttc 才能确认"能用" —— 为了可能多找几个字体, 每个进程启动都付一次
// 昂贵开销, 不划算。
func fontScanDirs() []string {
	if runtime.GOOS == "windows" {
		return nil
	}
	if runtime.GOOS == "android" {
		// Android 也扫: 静态候选只覆盖官方机型, 定制 ROM 的字体命名无规律
		// (与 Linux 同一理由)。代价是首帧前解析一批字体头, 一次性开销。
		return []string{"/system/fonts", "/product/fonts"}
	}
	if runtime.GOOS == "ios" {
		// 模拟器读得到 runtime 的字体目录 (真机沙箱读不到, 静默失败后
		// 靠静态候选/宿主注入兜底); 递归扫覆盖 Core/AppFonts/CoreUI 子目录。
		return []string{"/System/Library/Fonts", "/Library/Fonts"}
	}
	var dirs []string
	if runtime.GOOS == "darwin" {
		dirs = append(dirs, "/System/Library/Fonts", "/Library/Fonts")
	} else {
		dirs = append(dirs, "/usr/share/fonts", "/usr/local/share/fonts")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		// 用 UserHomeDir 而不是拼 $HOME: 各平台都能拿到, 且 Windows 上不会
		// 因为环境变量缺失而拼出 "/.fonts" 这种怪路径。
		if runtime.GOOS == "darwin" {
			dirs = append(dirs, filepath.Join(home, "Library", "Fonts"))
		} else {
			dirs = append(dirs,
				filepath.Join(home, ".local", "share", "fonts"),
				filepath.Join(home, ".fonts"),
			)
		}
	}
	return dirs
}

// initFontCandidates 惰性把扫描到的字体前置到候选列表 (幂等, 只跑一次)。
//
// 放在加载首个字体之前而不是 init(): 从不用 GUI 的脚本不该为扫描付钱。
func initFontCandidates() {
	fontCandsOnce.Do(func() {
		scannedFonts = scanSystemFonts(fontScanDirs(), fontScanLimit)
		// 扫描结果排在静态候选之前: 它们来自"这台机器上确实存在"的目录,
		// 而静态路径在部分发行版/精简镜像里根本不存在。宿主注入的仍排最前。
		rebuildCandidatesLocked()
	})
}

// cjkFontHints 是"这个文件名看起来能覆盖中文"的线索。
// CJK 字体必须排在任何拉丁字体之前 —— 拉丁字体也能正常解析加载, 只是中文
// 全画成豆腐块, 比"完全不出字"更难排查。
var cjkFontHints = []string{
	"notosanscjk", "notoserifcjk", "wenquanyi", "wqy", "droidsansfallback",
	"sourcehansans", "sourcehanserif", "fireflysung", "uming", "ukai", "arphic",
	"notosansmonocjk", "sarasa",
	// macOS 系统目录扫描用 (命名与 Linux 完全不同): 苹果内置 CJK 字体。
	"pingfang", "hiragino", "songti", "stheiti", "heiti", "libian", "lantinghei",
}

// looksCJK 按文件名猜测是否覆盖中文。
func looksCJK(fileName string) bool {
	lower := strings.ToLower(fileName)
	for _, h := range cjkFontHints {
		if strings.Contains(lower, h) {
			return true
		}
	}
	return false
}

// fontExtOK 报告文件名是否为字体扩展名。只对候选做解析, 免得把目录里的
// README/LICENSE/缓存文件也读进内存。
func fontExtOK(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".ttf", ".otf", ".ttc", ".otc":
		return true
	}
	return false
}

// scanSystemFonts 递归扫描字体目录, 返回"确实能解析"的字体路径:
// 先 CJK 字体, 再其余; 同组内保持目录序 —— 稳定比"最优"更重要, 每次启动
// 选到同一套字体, 渲染结果才可复现, 测试断言才不会随机抖。
//
// limit 是访问的条目数上限; 目录不存在/无权限一律静默跳过 (Linux 上
// ~/.fonts 常常不存在, 那不是错误)。
func scanSystemFonts(dirs []string, limit int) []string {
	var cjk, other []string
	visited := 0
	var walk func(dir string)
	walk = func(dir string) {
		if visited >= limit {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if visited >= limit {
				return
			}
			visited++
			full := filepath.Join(dir, e.Name())
			if e.IsDir() {
				walk(full)
				continue
			}
			if !fontExtOK(e.Name()) {
				continue
			}
			if _, err := parseFontFile(full); err != nil {
				continue // 不是真字体 / 格式不支持
			}
			if looksCJK(e.Name()) {
				cjk = append(cjk, full)
			} else {
				other = append(other, full)
			}
		}
	}
	for _, d := range dirs {
		walk(d)
	}
	return append(cjk, other...)
}

// parseFontFile 试解析一个字体文件, 返回其首个 face。
func parseFontFile(path string) (*opentype.Font, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	col, err := opentype.ParseCollection(data)
	if err != nil {
		return nil, err
	}
	return col.Font(0)
}

var (
	fontMu     sync.Mutex
	baseFont   *opentype.Font        // 解析后的基础字体
	faceBySize = map[int]font.Face{} // 字号 → face
	glyphLRU   = newGlyphCache(1024) // rune 掩码缓存
)

// loadBaseFont 加载首个可用系统字体 (幂等, 自行加锁)。
func loadBaseFont() (*opentype.Font, error) {
	fontMu.Lock()
	defer fontMu.Unlock()
	return loadBaseFontLocked()
}

// loadBaseFontLocked 加载首个可用系统字体 (调用方必须已持有 fontMu;
// fontFace 在持锁路径里调用, 避免不可重入锁死锁)。
func loadBaseFontLocked() (*opentype.Font, error) {
	if baseFont != nil {
		return baseFont, nil
	}
	// 扫描型候选 (Linux) 到这里才补齐: 它要遍历目录并真解析字体头, 放 init()
	// 会让"从不用 GUI"的脚本平白付一次开销。initFontCandidates 自带 sync.Once,
	// 且**不取 fontMu** —— 调用方已经持有, 再取会自锁。
	initFontCandidates()
	var lastErr error
	for _, path := range fontCandidates {
		if filepath.Base(path) == "" {
			continue
		}
		f, err := parseFontFile(path)
		if err != nil {
			// 带上文件名: 候选动辄几十上百条, 只说"解析失败"没法定位是哪台
			// 机器上哪个文件的问题。
			lastErr = fmt.Errorf("%s: %w", filepath.Base(path), err)
			continue
		}
		baseFont = f
		return baseFont, nil
	}
	return nil, fmt.Errorf("no usable system font (tried %d candidates, last: %v)",
		len(fontCandidates), lastErr)
}

// fontFace 返回指定像素字号的 face (懒建并缓存)。
func fontFace(size int) (font.Face, error) {
	if size < 8 {
		size = 8
	}
	fontMu.Lock()
	defer fontMu.Unlock()
	if f, ok := faceBySize[size]; ok {
		return f, nil
	}
	base, err := loadBaseFontLocked()
	if err != nil {
		return nil, err
	}
	// Size 单位是 pt, DPI=72 时 1pt = 1px, 直接以像素当字号。
	face, err := opentype.NewFace(base, &opentype.FaceOptions{
		Size:    float64(size),
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, err
	}
	faceBySize[size] = face
	return face, nil
}

// glyphEntry 是缓存的 glyph 渲染结果 (相对 dot 原点)。
type glyphEntry struct {
	mask    *image.Alpha // glyph 掩码
	offX    int          // 掩码绘制偏移 (dr.Min 相对 dot)
	offY    int
	advance int // 前进宽度 (px)
}

// glyphCache 是 rune → glyph 的 LRU 缓存 (单线程 GUI 访问, 锁仅为防御)。
type glyphCache struct {
	mu    sync.Mutex
	cap   int
	order []glyphKey
	entry map[glyphKey]*glyphEntry
	// devtools 基础设施 1: 命中/未中/淘汰计数 (口径与 imageLRU 一致)。
	hits, misses, evicts int
}

type glyphKey struct {
	size int
	r    rune
}

func newGlyphCache(capacity int) *glyphCache {
	return &glyphCache{cap: capacity, entry: map[glyphKey]*glyphEntry{}}
}

func (c *glyphCache) get(size int, r rune) *glyphEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := glyphKey{size, r}
	if e, ok := c.entry[k]; ok {
		c.hits++
		return e
	}
	c.misses++
	return nil
}

func (c *glyphCache) put(size int, r rune, e *glyphEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := glyphKey{size, r}
	if _, exists := c.entry[k]; exists {
		return
	}
	if len(c.order) >= c.cap {
		// 淘汰最旧
		old := c.order[0]
		c.order = c.order[1:]
		delete(c.entry, old)
		c.evicts++
	}
	c.order = append(c.order, k)
	c.entry[k] = e
}

// stats 返回缓存统计 (gx/dev 的 devSnapshot 用)。
// reset 清空缓存 (宿主换字体后必须清: 掩码是**旧字体**渲染出来的字形,
// 留着会中新 face 配旧字形 —— 画面表现为"换字体没生效"或文字串型)。
func (c *glyphCache) reset() {
	c.mu.Lock()
	c.order = nil
	c.entry = map[glyphKey]*glyphEntry{}
	c.hits, c.misses, c.evicts = 0, 0, 0
	c.mu.Unlock()
}

func (c *glyphCache) stats() (size, cap, hits, misses, evicts int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entry), c.cap, c.hits, c.misses, c.evicts
}

// glyph 渲染 (或取缓存) 一个字符: 以 dot=(0,0) 调 face.Glyph,
// 缓存掩码与偏移, 绘制时平移。
func glyph(size int, r rune) (*glyphEntry, error) {
	if e := glyphLRU.get(size, r); e != nil {
		return e, nil
	}
	face, err := fontFace(size)
	if err != nil {
		return nil, err
	}
	dr, mask, _, advance, ok := face.Glyph(fixed.P(0, 0), r)
	if !ok {
		return &glyphEntry{advance: size / 2}, nil // 缺字形: 占位宽度
	}
	alpha, _ := mask.(*image.Alpha)
	if alpha == nil {
		// sfnt 的掩码总是 *image.Alpha; 其他实现回退为无掩码
		return &glyphEntry{advance: advance.Ceil()}, nil
	}
	e := &glyphEntry{
		// **必须深拷贝**: face.Glyph 返回的 *image.Alpha 是 face 自己的字段,
		// 每次调用都复写同一块 Pix (见 cloneAlpha 的说明)。直接缓存这个指针
		// 会让所有 rune 共享"最后一次光栅化"的结果。
		mask:    cloneAlpha(alpha),
		offX:    dr.Min.X,
		offY:    dr.Min.Y,
		advance: advance.Ceil(),
	}
	glyphLRU.put(size, r, e)
	return e, nil
}

// cloneAlpha 深拷贝一个 glyph 掩码。
//
// **必须拷贝, 不能直接存 face.Glyph 返回的那个 *image.Alpha**:
// x/image/font/opentype 的 Face 把掩码缓冲放在自己身上, 每次 Glyph 调用都是
//
//	f.mask.Pix = f.mask.Pix[:nPixels]   // 复用同一块底层数组
//	... f.rast.Draw(&f.mask, ...)
//	return dr, &f.mask, f.mask.Rect.Min, advance, x != 0
//
// ⇒ 同一个 *image.Alpha (和同一块 Pix) 被反复返回并反复覆写, 只有 Rect.Max
// 随字形大小变化。把它塞进 LRU 的次数等于缓存了多少个"别名", 每个别名的像素
// 都指向"最后一次光栅化的那个字" —— 症状是**界面上所有字都长成同一个字形**
// (字距/布局/字号全对, 只有字形是错的; 首帧里每个字首次出现时还是对的, 之后
// 任何一次重绘就整屏同形)。
//
// 2026-09-21 修的"文本显示异常"就是这个: 截图里每个汉字都是同一个"置"。
// 上游实现见 opentype.go 的 Glyph (x/image v0.46.0, 注释 "re-allocating its
// buffer if necessary") —— 这是**接口约定**, 不是上游的 bug: font.Face 文档
// 明确说掩码只在"下一次 Glyph 调用前"有效。
func cloneAlpha(src *image.Alpha) *image.Alpha {
	b := src.Bounds()
	dst := image.NewAlpha(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		copy(dst.Pix[dst.PixOffset(b.Min.X, y):dst.PixOffset(b.Max.X, y)],
			src.Pix[src.PixOffset(b.Min.X, y):src.PixOffset(b.Max.X, y)])
	}
	return dst
}

// ascentCache 记录每字号 ascent (基线到顶部距离, px)。
var ascentCache = map[int]int{}

// textAscent 返回指定字号的 ascent。
func textAscent(size int) int {
	if a, ok := ascentCache[size]; ok {
		return a
	}
	face, err := fontFace(size)
	if err != nil {
		return size
	}
	m := face.Metrics()
	a := m.Ascent.Ceil()
	if a <= 0 {
		a = size
	}
	ascentCache[size] = a
	return a
}

// MeasureText 测量单行文本 (像素)。h 为行高 (含上下余量)。
func MeasureText(text string, size int) (w, h int) {
	if size < 8 {
		size = 8
	}
	for _, r := range text {
		e, err := glyph(size, r)
		if err != nil {
			return 0, 0
		}
		w += e.advance
	}
	h = size + size/4 // 近似行高 (ascent+descent 简化)
	return w, h
}

// ===== 自动换行 (P2-6) =====

// lineHeight 返回字号对应的行高。与 MeasureText 的 h 同口径 —— 多行文本的
// 总高就是 行数 × lineHeight, 别处不要另算一套。
func lineHeight(size int) int {
	if size < 8 {
		size = 8
	}
	return size + size/4
}

// runeAdvance 返回单个字符的前进宽度; 取不到字形时退化为半个字号宽
// (与 glyph 的缺字形占位一致), 保证换行计算永远有正数可用。
func runeAdvance(size int, r rune) int {
	if r == '\t' {
		// 制表符没有字形: 按 4 个空格算 (与终端习惯一致)
		return 4 * runeAdvance(size, ' ')
	}
	e, err := glyph(size, r)
	if err != nil || e.advance <= 0 {
		return size / 2
	}
	return e.advance
}

// runeWidth 返回字符串在给定字号下的像素宽度 (逐 rune 累加 advance)。
func runeWidth(text string, size int) int {
	w := 0
	for _, r := range text {
		w += runeAdvance(size, r)
	}
	return w
}

// ellipsisMark 是超行截断用的省略号。用三个点而不是 "…": 字体候选里
// 微软雅黑有 U+2026, 但宋体/Segoe 的度量差异会让最后一行宽度抖动。
const ellipsisMark = "..."

// ellipsize 把一行裁到 maxWidth 内并补省略号: 从尾部逐个字符回退, 直到
// "剩余内容 + ..." 放得下为止。maxWidth <= 0 (无约束) 时直接补后缀。
func ellipsize(line string, size, maxWidth int) string {
	if maxWidth <= 0 {
		return line + ellipsisMark
	}
	if runeWidth(line, size)+runeWidth(ellipsisMark, size) <= maxWidth {
		return line + ellipsisMark
	}
	markW := runeWidth(ellipsisMark, size)
	rs := []rune(line)
	used := 0
	for len(rs) > 0 {
		adv := runeAdvance(size, rs[len(rs)-1])
		if used+markW+adv > maxWidth {
			break
		}
		used += adv
		rs = rs[:len(rs)-1]
	}
	return string(rs) + ellipsisMark
}

// wrapText 把文本按 maxWidth 切成多行 (P2-6 的核心):
//   - 显式 '\n' 强制换行, 连续换行保留空行;
//   - 其余贪心逐 rune 累加 advance, 若加上下一个字符会超宽就折行 ——
//     中西文一视同仁 (CJK 字符 advance 约等于字号, 自动按字折行);
//   - maxWidth <= 0 表示"没有宽度约束": 只按 '\n' 切, 不折行;
//   - 单字符本身就宽于 maxWidth 时让它独占一行 —— 否则内层无法收尾,
//     maxWidth 比一个汉字还窄时会死循环;
//   - maxLines > 0 时只保留前 maxLines 行, 末行补 "..." 并裁到放得下。
//
// 返回值至少一行 (空串文本也会得到 [""]), 调用方不必再判空。
func wrapText(text string, size, maxWidth, maxLines int) []string {
	if size < 8 {
		size = 8
	}
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		if maxWidth <= 0 {
			lines = append(lines, para)
			continue
		}
		cur := make([]rune, 0, 32)
		curW := 0
		for _, r := range para {
			adv := runeAdvance(size, r)
			if curW > 0 && curW+adv > maxWidth {
				lines = append(lines, string(cur))
				cur = cur[:0]
				curW = 0
			}
			cur = append(cur, r)
			curW += adv
		}
		lines = append(lines, string(cur))
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = ellipsize(lines[maxLines-1], size, maxWidth)
	}
	return lines
}

// MeasureTextMulti 多行测量: 宽 = 最长行, 高 = 行数 × 行高。
// maxWidth <= 0 时按 '\n' 分行, maxLines > 0 时按省略号截断口径测量
// (与 wrapText 完全一致, 所以布局尺寸和绘制结果不会打架)。
func MeasureTextMulti(text string, size, maxWidth, maxLines int) (w, h int) {
	if size < 8 {
		size = 8
	}
	lines := wrapText(text, size, maxWidth, maxLines)
	for _, ln := range lines {
		if lw := runeWidth(ln, size); lw > w {
			w = lw
		}
	}
	return w, len(lines) * lineHeight(size)
}

// DrawText 在 img 的 (x,y) (左上角) 画单行文本, 限制在 clip 矩形内;
// maxWidth > 0 时超出截断 (v1: 硬截断, 不加省略号)。
// 返回实际绘制的宽度。
func DrawText(img *image.RGBA, clip image.Rectangle, text string, x, y, size int, c color.RGBA, maxWidth int) int {
	if size < 8 {
		size = 8
	}
	// 子树不透明度 (P3-2): 文字走的是"字形覆盖度当 alpha"的混合 (见 blitGlyph,
	// 只用 c.R/G/B), 所以这里不能像 FillRect 那样改 c.A —— 把淡出因子
	// 编码进 alpha 通道传下去, blitGlyph 会乘到覆盖度上。
	c = applyFade(c)
	ascent := textAscent(size)
	dotY := y + ascent
	drawn := 0
	for _, r := range text {
		e, err := glyph(size, r)
		if err != nil {
			return drawn
		}
		if maxWidth > 0 && drawn+e.advance > maxWidth {
			break
		}
		drawn += e.advance
		if e.mask != nil {
			blitGlyph(img, clip, e, x+e.offX, dotY+e.offY, c)
		}
		x += e.advance
	}
	return drawn
}

// blitGlyph 把 glyph 掩码按颜色 alpha 混合写入 img (clip 裁剪)。
//
// 淡出 (P3-2) 的接法是"c.A 当额外覆盖度因子": 调用方 (DrawText) 已经
// 把不透明度折进了 c.A, 这里再乘一次就得到 final alpha = 覆盖度 × 不透明度。
func blitGlyph(img *image.RGBA, clip image.Rectangle, e *glyphEntry, dx, dy int, c color.RGBA) {
	fade := uint32(c.A)
	b := e.mask.Bounds()
	for my := b.Min.Y; my < b.Max.Y; my++ {
		iy := dy + my - b.Min.Y
		if iy < clip.Min.Y || iy >= clip.Max.Y || iy < img.Rect.Min.Y || iy >= img.Rect.Max.Y {
			continue
		}
		for mx := b.Min.X; mx < b.Max.X; mx++ {
			ix := dx + mx - b.Min.X
			if ix < clip.Min.X || ix >= clip.Max.X || ix < img.Rect.Min.X || ix >= img.Rect.Max.X {
				continue
			}
			a := uint32(e.mask.AlphaAt(mx, my).A) * fade / 255
			if a == 0 {
				continue
			}
			// src-over: dst = src*a + dst*(1-a)
			off := img.PixOffset(ix, iy)
			p := img.Pix[off:]
			na := 255 - a
			p[0] = uint8((uint32(c.R)*a + uint32(p[0])*na) / 255)
			p[1] = uint8((uint32(c.G)*a + uint32(p[1])*na) / 255)
			p[2] = uint8((uint32(c.B)*a + uint32(p[2])*na) / 255)
		}
	}
}

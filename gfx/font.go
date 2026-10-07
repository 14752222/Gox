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
// 有效顺序恒为: 宿主注入 > 目录扫描 > 静态候选 > 随包兜底 (rebuildCandidatesLocked)。
var fontCandidates = fontCandidatesForOS()

// __BASE_ASSETS__ 是"随包字体"所在目录, 由宿主 (main / 平台入口) 在首次渲染前
// 调用 SetBaseAssetsDir 填入 —— grep 这个标记名就能找到全部注入点。
//
// 为什么需要它: 精简镜像 (CI 的 ubuntu runner 就是) 只装了 TTF 版 Noto CJK,
// 而系统里那一份的 .ttc 面序与名字并不可信 (见 axisFromFont 的长注释);
// 与其去猜系统布局, 不如**自己带一对正体+粗体**, 让"默认字体是正体"与
// "加粗看得出区别"这两条不再依赖发行版。它排在静态候选**之后** —— 桌面
// 系统的原生字体观感更好, 随包字体只在前面都落空时才生效。
var baseAssetsDir string

// SetBaseAssetsDir 告知 gfx "随包字体"的根目录 (gox 运行时/宿主调用)。
//
// 留空 = 不用随包字体 (默认; 保持"只用系统字体"的旧行为)。与 SetFontPath
// 的区别: 那个是"宿主明确指定必须用的字体"(优先级最高), 这个是"最后兜底"。
func SetBaseAssetsDir(dir string) {
	fontMu.Lock()
	defer fontMu.Unlock()
	if baseAssetsDir == dir {
		return
	}
	baseAssetsDir = dir
	rebuildCandidatesLocked()
	// 已加载过字体才需要重置缓存; 否则下次加载自然用新候选表。
	if baseFont != nil || len(faceBySize) > 0 {
		baseFont = nil
		faceBySize = map[int]font.Face{}
		resetFontCaches()
	}
}

// bundledFontCandidates 返回随包字体候选 (成对给出: 正体在前, 粗体在后)。
// 未设置资源目录时返回 nil。
func bundledFontCandidates() []string {
	if baseAssetsDir == "" {
		return nil
	}
	d := filepath.Join(baseAssetsDir, "fonts")
	return []string{
		filepath.Join(d, "NotoSansSC-Regular.otf"),
		filepath.Join(d, "NotoSansSC-Bold.otf"),
	}
}

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
	// 随包字体永远排最后: 它是"前面全落空"时的保底, 不该抢系统字体的位置。
	next = append(next, bundledFontCandidates()...)
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
	// 或者更糟 —— 新 face 配旧字形掩码。样式轴那一层的缓存 (faceCache /
	// 字体文件解析 / 族索引) 同样要清, 它们的键里都含着"当时有哪些字体"。
	if baseFont != nil || len(faceBySize) > 0 {
		baseFont = nil
		faceBySize = map[int]font.Face{}
		resetFontCaches()
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
		//
		// CJK 放在拉丁之前: 拉丁字体也能正常加载, 只是中文全画成豆腐块 ——
		// 比"完全不出字"更难查。TTF 命名的 Noto CJK 在 Debian/Ubuntu 的
		// fonts-noto-cjk 里恒存在 (扫描落空时的保底, 与 Windows 的 msyh
		// 同一角色)。
		return []string{
			"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
			"/usr/share/fonts/opentype/noto/NotoSansCJK-VF.otf.ttc",
			"/usr/share/fonts/truetype/noto/NotoSansCJK-Regular.ttc",
			"/usr/share/fonts/opentype/noto/NotoSerifCJK-Regular.ttc",
			"/usr/share/fonts/truetype/wqy/wqy-microhei.ttc",
			"/usr/share/fonts/truetype/arphic/uming.ttc",
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

// parseFontFile 试解析一个字体文件, 返回它**最适合当默认正文**的那个面。
//
// 不再是 col.Font(0): 集合字体里的 face 0 未必是正体 (fonts-noto-cjk 的
// NotoSansCJK-Bold.ttc 每个面都是粗体), 而拿粗体面当默认字体 = 整屏正文加粗
// —— 见 chooseBaseFont 的说明。
func parseFontFile(path string) (*opentype.Font, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	col, err := opentype.ParseCollection(data)
	if err != nil {
		return nil, err
	}
	return regularFaceOf(col)
}

// regularFaceOf 在一个字体集合里挑"最像正体"的面 (评分见 baseFaceRank)。
//
// 一个面都读不出来时退回 col.Font(0): 调用方 scanSystemFonts 只拿返回值当
// "这文件是不是真字体"的判据, 报告错误的口径与旧实现保持一致。
func regularFaceOf(col *opentype.Collection) (*opentype.Font, error) {
	var best *opentype.Font
	bestRank := 0
	for i, n := 0, col.NumFonts(); i < n; i++ {
		f, err := col.Font(i)
		if err != nil {
			continue
		}
		r := baseFaceRank(f)
		if best == nil || r < bestRank {
			best, bestRank = f, r
			if r == 0 {
				break // 已经是正体, 无需再看
			}
		}
	}
	if best != nil {
		return best, nil
	}
	return col.Font(0)
}

// baseFaceRank 给"这个面适不适合当默认正文字面"打分, 越小越合适 (0 = 正体)。
//
// 分档而不是只判"粗不粗": Linux 的 fonts-noto-cjk 目录里同时躺着 Black /
// Bold / DemiLight / Light / Medium / Regular, 目录序又是文件名字典序 ——
// 只排除粗体的话默认字体会落到 DemiLight (它确实不粗, 但也不该当正文)。
// 判据与 axisFromFont 同一口径 (子族名), 所以索引认得的粗/斜这里也认得。
func baseFaceRank(f *opentype.Font) int {
	a := fontAxisOf(f)
	rank := 0
	if a.bold {
		rank += 2
	} else if !subIsPlain(subfamilyOf(f)) {
		rank++ // Light / Medium / DemiLight …: 不粗, 但也不是正体
	}
	if a.italic {
		rank += 4
	}
	return rank
}

// subIsPlain 报告子族名是否就是"正体" (Regular / Book / Normal / Roman / 空)。
// 空串算正体: 有些老字体不写子族名, 而"没写"通常就是正体。
func subIsPlain(sub string) bool {
	s := strings.ToLower(strings.TrimSpace(sub))
	if s == "" {
		return true
	}
	for _, w := range []string{"regular", "book", "normal", "roman"} {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
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
	f, err := chooseBaseFont(fontCandidates)
	if err != nil {
		return nil, err
	}
	baseFont = f
	return baseFont, nil
}

// chooseBaseFont 从候选里挑出默认字体: **正体面优先**, 一个都没有才退回首个
// 能解析的候选。
//
// 为什么不能"取首个可解析者": Linux 的候选表是"先目录扫描、后静态路径", 而
// scanSystemFonts 对 CJK 组保持目录序 (= 文件名字典序) —— fonts-noto-cjk 里
// NotoSansCJK-Bold.ttc 恰好排在 NotoSansCJK-Regular.ttc 之前。于是默认字体
// 成了**粗体面**: 症状是整屏正文都变粗, 而且"要粗体"的请求经族索引恰好落到
// 同一个文件、同一个 face index, 逐像素完全相同 —— 看起来像"fontWeight 完全
// 没生效"。CI 上的 TestDrawTextStyledDiffersPerAxis 就是这么红的。
//
// 判面用的是 baseFaceRank (与族索引登记面同一口径), 不是文件名 —— Linux 上
// 文件名与真实字重对不上的情况很常见。
func chooseBaseFont(paths []string) (*opentype.Font, error) {
	var first *opentype.Font
	var lastErr error
	for _, p := range paths {
		if filepath.Base(p) == "" {
			continue
		}
		f, err := parseFontFile(p)
		if err != nil {
			// 带上文件名: 候选动辄几十上百条, 只说"解析失败"没法定位是哪台
			// 机器上哪个文件的问题。
			lastErr = fmt.Errorf("%s: %w", filepath.Base(p), err)
			continue
		}
		if baseFaceRank(f) == 0 {
			return f, nil
		}
		// 候选里没有正体 (整机只有粗体/斜体面): 留首个作兜底, 有字渲染
		// 永远优于无字渲染 (与 SetCursor 同一套静默降级口径)。
		if first == nil {
			first = f
		}
	}
	if first != nil {
		return first, nil
	}
	return nil, fmt.Errorf("no usable system font (tried %d candidates, last: %v)",
		len(paths), lastErr)
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

	// 合成标记 (§四 文本域缺口): 该族没有真实粗体/斜体面时, 由 blitGlyph
	// 用"再压一遍" / "按基线剪切"近似出来。放在 entry 上而不是每次查表,
	// 是因为它在同一次 (族, 轴, 字号) 请求里恒定, 而 blitGlyph 是逐字形
	// 调用的热路径 —— 热路径上不该再查一次索引。
	synthB bool
	synthI bool
}

// glyphCache 是 "面 × 字号 × rune" → glyph 的 LRU 缓存 (单线程 GUI 访问,
// 锁仅为防御)。
type glyphCache struct {
	mu    sync.Mutex
	cap   int
	order []glyphKey
	entry map[glyphKey]*glyphEntry
	// devtools 基础设施 1: 命中/未中/淘汰计数 (口径与 imageLRU 一致)。
	hits, misses, evicts int
}

// glyphKey 是字形缓存的键。
//
// face 必须是"**请求的**面"而不是"最终用的面": 合成粗体与真粗体是两个请求,
// 若两者恰好都解析到同一个文件 (族里没有粗体面 ⇒ 合成请求用正体面), 只按
// 面去键就会让真粗体与合成粗体共用同一个掩码 —— 表现是"设了 bold 有时有
// 时没有", 随渲染顺序漂移。所以键里带上请求的两个轴 (合成与否由
// (face, bold, italic) 唯一决定, 见 faceForStyle)。
type glyphKey struct {
	face   int
	bold   bool
	italic bool
	size   int
	r      rune
}

func newGlyphCache(capacity int) *glyphCache {
	return &glyphCache{cap: capacity, entry: map[glyphKey]*glyphEntry{}}
}

func (c *glyphCache) get(k glyphKey) *glyphEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entry[k]; ok {
		c.hits++
		return e
	}
	c.misses++
	return nil
}

func (c *glyphCache) put(k glyphKey, e *glyphEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
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

// glyphFor 渲染 (或取缓存) 一个字符: 以 dot=(0,0) 调 face.Glyph, 缓存掩码
// 与偏移, 绘制时平移。
//
// 缺字回退 (§四 文本域缺口): 请求的族里没有这个字符时退回**默认字体**再试
// 一次 —— 这是"字体族"这个功能最容易被忽略的一半。选了 Menlo 之后中文全变
// 豆腐块, 比不做字体族还糟; 而字体族本来就不该改变"覆盖范围", 只该改变
// "字形风格"。
func glyphFor(st TextStyle, r rune) (*glyphEntry, error) {
	rf, err := faceForStyle(st)
	if err != nil {
		return nil, err
	}
	k := glyphKey{face: rf.id, bold: st.Bold, italic: st.Italic, size: st.Size, r: r}
	if e := glyphLRU.get(k); e != nil {
		return e, nil
	}
	e, err := rasterizeGlyph(rf, st.Size, r)
	if err != nil {
		return nil, err
	}
	// 合成标记必须**落到 entry 上**才起作用: 真正画的时候 (blitGlyph) 只看
	// entry, 手上没有 resolvedFace —— 少这一步的症状是"设了 bold 完全没反应"
	// (而且是静默的: 形状/度量全对, 只是不粗)。
	e.synthB, e.synthI = rf.synthB, rf.synthI
	// 缺字回退: 只在"请求的不是默认字体"时才多试一次 —— 默认字体自己缺字
	// 就是真缺字, 再试一次是白付出的代价 (而它是热路径)。
	if e.mask == nil && !rf.isBase {
		if base, err := baseFaceFor(st); err == nil {
			if be, err := rasterizeGlyph(base, st.Size, r); err == nil && be.mask != nil {
				// 合成标记跟着**请求**走: 回退只是换字形, 粗斜体意图不变。
				be.synthB, be.synthI = rf.synthB, rf.synthI
				e = be
			}
		}
	}
	if e.mask == nil {
		e.synthB, e.synthI = false, false // 没掩码就没有"合成"可言
	}
	glyphLRU.put(k, e)
	return e, nil
}

// rasterizeGlyph 用给定面光栅化一个字形 (不做缺字回退)。
func rasterizeGlyph(rf resolvedFace, size int, r rune) (*glyphEntry, error) {
	dr, mask, _, advance, ok := rf.face.Glyph(fixed.P(0, 0), r)
	if !ok {
		// 缺字形: 只记前进宽度 (占位), 不回退 —— 回退由调用方决定
		return &glyphEntry{advance: size / 2}, nil
	}
	alpha, _ := mask.(*image.Alpha)
	if alpha == nil {
		// sfnt 的掩码总是 *image.Alpha; 其他实现回退为无掩码
		return &glyphEntry{advance: advance.Ceil()}, nil
	}
	return &glyphEntry{
		// **必须深拷贝**: face.Glyph 返回的 *image.Alpha 是 face 自己的字段,
		// 每次调用都复写同一块 Pix (见 cloneAlpha 的说明)。直接缓存这个指针
		// 会让所有 rune 共享"最后一次光栅化"的结果。
		mask:    cloneAlpha(alpha),
		offX:    dr.Min.X,
		offY:    dr.Min.Y,
		advance: advance.Ceil(),
	}, nil
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

// ===== 度量 / 换行: 老入口 (只要字号) =====
//
// 下面这一组是"只传字号"的写法, 现在全部转调 textstyle.go 的 styled 版本
// (§四 文本域缺口)。保留它们而不是把所有调用点改成传 TextStyle, 是因为
// 绝大多数调用点是内置组件的自绘 (按钮标签、菜单项、分页页码…), 它们本来
// 就只用得上字号 —— 让 60 处都去构造一个 TextStyle 只会把噪声搬个地方。
//
// 语义上它们等价于 TextStyle{Size: size}: 无族名 (走默认字体)、不合成粗斜、
// 自动行高、零字距 —— 与加样式轴之前**逐像素一致**, 既有像素断言不受影响。

// MeasureText 测量单行文本 (像素)。h 为行高 (含上下余量)。
func MeasureText(text string, size int) (w, h int) {
	return MeasureTextStyled(text, TextStyle{Size: size})
}

// ===== 自动换行 (P2-6) =====

// lineHeight 返回字号对应的行高。与 MeasureText 的 h 同口径 —— 多行文本的
// 总高就是 行数 × lineHeight, 别处不要另算一套。
func lineHeight(size int) int {
	return lineHeightStyled(TextStyle{Size: size})
}

// runeAdvance 返回单个字符的前进宽度; 取不到字形时退化为半个字号宽
// (与 glyph 的缺字形占位一致), 保证换行计算永远有正数可用。
func runeAdvance(size int, r rune) int {
	return runeAdvanceStyled(TextStyle{Size: size}, r)
}

// runeWidth 返回字符串在给定字号下的像素宽度 (逐 rune 累加 advance)。
func runeWidth(text string, size int) int {
	return runeWidthStyled(text, TextStyle{Size: size})
}

// ellipsisMark 是超行截断用的省略号。用三个点而不是 "…": 字体候选里
// 微软雅黑有 U+2026, 但宋体/Segoe 的度量差异会让最后一行宽度抖动。
const ellipsisMark = "..."

// ellipsize 把一行裁到 maxWidth 内并补省略号 (算法见 ellipsizeStyled)。
func ellipsize(line string, size, maxWidth int) string {
	return ellipsizeStyled(line, TextStyle{Size: size}, maxWidth)
}

// wrapText 把文本按 maxWidth 切成多行 (算法见 wrapTextStyled)。
func wrapText(text string, size, maxWidth, maxLines int) []string {
	return wrapTextStyled(text, TextStyle{Size: size}, maxWidth, maxLines)
}

// MeasureTextMulti 多行测量: 宽 = 最长行, 高 = 行数 × 行高。
// maxWidth <= 0 时按 '\n' 分行, maxLines > 0 时按省略号截断口径测量
// (与 wrapText 完全一致, 所以布局尺寸和绘制结果不会打架)。
func MeasureTextMulti(text string, size, maxWidth, maxLines int) (w, h int) {
	return MeasureTextMultiStyled(text, TextStyle{Size: size}, maxWidth, maxLines)
}

// DrawText 在 img 的 (x,y) (左上角) 画单行文本, 限制在 clip 矩形内;
// maxWidth > 0 时超出截断 (v1: 硬截断, 不加省略号)。
// 返回实际绘制的宽度。样式版见 DrawTextStyled。
func DrawText(img *image.RGBA, clip image.Rectangle, text string, x, y, size int, c color.RGBA, maxWidth int) int {
	return DrawTextStyled(img, clip, text, x, y, TextStyle{Size: size}, c, maxWidth)
}

// italicSlopeNum / italicSlopeDen 是合成斜体的剪切斜率 (≈ tan 12°, 与大多数
// 真斜体的倾斜角一致)。写成整数比而不是浮点: 每个字形行都要算一次偏移,
// 而这是逐像素热路径。
const (
	italicSlopeNum = 21
	italicSlopeDen = 100
)

// blitGlyph 把 glyph 掩码按颜色 alpha 混合写入 img (clip 裁剪)。
//
// baseY 是**基线的行号** (不是掩码顶边): 合成斜体要绕基线剪切 —— 字底钉住、
// 字顶向右倾, 这才是 italic; 绕掩码顶边剪的观感是"整个字被斜着推出去",
// 长字符串上尤其明显。
//
// 淡出 (P3-2) 的接法是"c.A 当额外覆盖度因子": 调用方 (DrawText) 已经
// 把不透明度折进了 c.A, 这里再乘一次就得到 final alpha = 覆盖度 × 不透明度。
func blitGlyph(img *image.RGBA, clip image.Rectangle, e *glyphEntry, dx, dy, baseY int, c color.RGBA) {
	blitMask(img, clip, e, dx, dy, baseY, c)
	if e.synthB {
		// 合成粗体: 往右再压一遍 (1px 的横向涂抹)。它是**近似** —— 真粗体
		// 是重新设计的字重 (笔画对比、字面宽度都不同), 这里只求"看起来更重"。
		// 1px 是刻意的: 再宽会糊掉小字号 (12px 的中文一 smear 就成一团)。
		blitMask(img, clip, e, dx+1, dy, baseY, c)
	}
}

// blitMask 是单次掩码混合 (合成粗体就是同一份掩码压两遍)。
func blitMask(img *image.RGBA, clip image.Rectangle, e *glyphEntry, dx, dy, baseY int, c color.RGBA) {
	fade := uint32(c.A)
	b := e.mask.Bounds()
	for my := b.Min.Y; my < b.Max.Y; my++ {
		iy := dy + my - b.Min.Y
		if iy < clip.Min.Y || iy >= clip.Max.Y || iy < img.Rect.Min.Y || iy >= img.Rect.Max.Y {
			continue
		}
		// 合成斜体: 离基线越高右移越多, 基线及以下不动。
		shift := 0
		if e.synthI {
			shift = (baseY - iy) * italicSlopeNum / italicSlopeDen
		}
		for mx := b.Min.X; mx < b.Max.X; mx++ {
			ix := dx + mx - b.Min.X + shift
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

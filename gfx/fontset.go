package gfx

// ===== 字体族索引 (§四 文本域缺口: 粗斜体 / 字体族) =====
//
// 目标: 让 `fontFamily` / `fontWeight` / `fontStyle` 三个 prop 真的能选到
// **系统里那个字体文件**, 而不是像 v1 那样只能改字号。
//
// ## 为什么要建索引, 而不是"按名字猜路径"
//
// 平台差异比想象中大得多: 同一款 Arial Bold 在 macOS 是
// `/System/Library/Fonts/Supplemental/Arial Bold.ttf`, 在 Windows 是
// `C:\Windows\Fonts\arialbd.ttf`, 在 Linux 是
// `/usr/share/fonts/truetype/liberation/LiberationSans-Bold.ttf` —— 路径里
// 没有任何一处是共通的。唯一可靠的做法是**读字体自己声明的族名与子族名**
// (name 表的 ID 1 / ID 2), 于是"想要 Menlo 的斜体"在任何平台上都是同一次
// 查表。
//
// ## 索引只读 name/post 表, 不整份读进内存
//
// 集合字体 (.ttc) 动辄几十 MB (PingFang.ttc 尤甚)。用
// `ParseCollectionReaderAt(*os.File)` 让 sfnt 按需 ReadAt, 只把 name 表那
// 几百字节取出来, 读完就关文件 —— 内存占用与字体文件大小无关。
//
// ## 惰性 + 一次
//
// 建索引要遍历字体目录 (上限见 familyScanLimit), 是**一次性**开销。它只在
// 脚本第一次用 `fontFamily` 时触发: 从不用它的应用一个字节都不解析, 与
// initFontCandidates 的"不用 GUI 就不扫描"是同一条取舍。
//
// ## 三级降级, 永不报错
//
//	1. 族 + 样式都有真实变体 → 直接用那个面;
//	2. 族在但缺样式       → 取最接近的真实变体 + **合成**粗/斜;
//	3. 族都没有           → 退回默认字体 + 合成。
//
// 一个拼错的字体名不该让整屏文字不渲染 —— 那正是"静默降级"这个口径存在的
// 理由 (与 SetCursor / windowManager 同一套)。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

// styleAxis 是字体样式轴的两个维度 (粗体 × 斜体)。四个组合就是"一个族里
// 可能有四个面"。用结构体而不是位掩码: 它要做 map 的键, 可比较且读起来
// 是 styleAxis{bold: true} 而不是 0b01。
type styleAxis struct {
	bold   bool
	italic bool
}

// faceSrc 定位"某个字体文件里的某一个面"。
//
// 一个 .ttc 里可能有十几个面 (PingFang.ttc 就同时装着 SC/TC/HK 各种字重),
// 所以只有 (路径, 面序号) 才算唯一 —— 只记路径会把"族名对了但面不对"的
// 情况悄悄画成另一个字重, 那比找不到更难查。
type faceSrc struct {
	path  string
	index int
}

// familyScanLimit 是族索引遍历的条目上限 (含目录), 与 fontScanLimit 同一
// 用意: 目录异常深/字体异常多时宁可少找几个族, 也不能把首帧拖住。
const familyScanLimit = 2000

// familyIndex 是 "族名 → 样式轴 → 字体面" 的索引。
type familyIndex struct {
	mu    sync.Mutex
	built bool

	// files: 族名(归一) → 样式轴 → 面。
	files map[string]map[styleAxis]faceSrc
	// alt: 别名(归一) → 族名。两个来源: 文件主干名 (让
	// fontFamily="DejaVuSans-Bold" 这类"按文件名写"的配置也能命中) 与
	// 去掉空格的族名 ("pingfangsc" → "pingfang sc")。
	alt map[string]string
	// mono: 等宽族 (post 表的 IsFixedPitch)。"monospace" 这个泛型族名的兜底。
	mono map[string]bool
	// styleOf: 族名 → 该族实际有哪些面。合成与否由它推出来。
	styleOf map[string][]styleAxis
}

var familyIdx = &familyIndex{}

// build 建索引 (幂等)。整段持锁: 它只在首次用到 fontFamily 时跑一次, 而
// 查表本身是 GUI 线程上的高频操作 —— 让两者共用一把锁是最简单的正确做法
// (分阶段加锁会引入"半建成的索引"这种中间态)。
func (ix *familyIndex) build() {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if ix.built {
		return
	}
	ix.built = true
	ix.files = map[string]map[styleAxis]faceSrc{}
	ix.alt = map[string]string{}
	ix.mono = map[string]bool{}
	ix.styleOf = map[string][]styleAxis{}

	// 宿主注入的字体排最前 (与 baseFont 的候选顺序同一口径): 移动端
	// 随包字体必须能按族名选到, 否则 SetFontPath 只解决了"默认字体"。
	for _, p := range injectedFonts {
		ix.indexFileLocked(p)
	}
	visited := 0
	var walk func(dir string)
	walk = func(dir string) {
		if visited >= familyScanLimit {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if visited >= familyScanLimit {
				return
			}
			visited++
			full := filepath.Join(dir, e.Name())
			if e.IsDir() {
				walk(full)
				continue
			}
			if fontExtOK(e.Name()) {
				ix.indexFileLocked(full)
			}
		}
	}
	for _, d := range fontScanDirs() {
		walk(d)
	}
	// 静态候选补一遍: 扫描有上限, 而静态候选恰恰是"这个平台上必然存在"的
	// 那几个 (C:\Windows\Fonts 在 Windows 上根本不进扫描目录)。
	for _, p := range fontCandidatesForOS() {
		ix.indexFileLocked(p)
	}
}

// indexFileLocked 把一个字体文件的所有面记进索引 (调用方必须持有 ix.mu)。
//
// 失败一律静默: 目录里混着不可解析的文件是常态 (老格式位图字体、损坏文件),
// 不值得为此中断整次索引 —— 那会让"用了 fontFamily"变成"整屏无字"。
func (ix *familyIndex) indexFileLocked(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	col, err := opentype.ParseCollectionReaderAt(f)
	if err != nil {
		return
	}
	var buf sfnt.Buffer
	n := col.NumFonts()
	stemKey := normalizeFamilyKey(stripStyleSuffix(strings.TrimSuffix(
		filepath.Base(path), filepath.Ext(path))))
	for i := 0; i < n; i++ {
		ft, err := col.Font(i)
		if err != nil {
			continue
		}
		// 一个面可能有两个族名, **两个都要登记**:
		//   - ID 1 (Family)      —— "Arial" / "Hiragino Sans GB";
		//   - ID 16 (TypographicFamily) —— 有时是上面那个的本地化名。
		// 只取其中一个都会漏: 苹果 CJK 字体的 ID 16 是"冬青黑體簡體中文"这种
		// 繁体/中文名 (脚本里没人会那么写), 而它的 ID 1 才是通用名; 反过来,
		// 带光学尺寸的字体族只在 ID 16 里写真正的族名 (ID 1 是 "Arial Bold")。
		// 两个都收, 代价只是一个 map 键, 收益是"两种写法都能命中"。
		names := make([]string, 0, 3)
		for _, id := range []sfnt.NameID{sfnt.NameIDFamily, sfnt.NameIDTypographicFamily} {
			if key := normalizeFamilyKey(fontNameOf(ft, &buf, id)); key != "" {
				names = append(names, key)
			}
		}
		if len(names) == 0 {
			continue
		}
		axis := axisFromFont(ft, fontNameOf(ft, &buf, sfnt.NameIDSubfamily))
		mono := false
		if pt := ft.PostTable(); pt != nil {
			mono = pt.IsFixedPitch
		}
		for _, key := range names {
			if _, ok := ix.files[key]; !ok {
				ix.files[key] = map[styleAxis]faceSrc{}
			}
			// 先到先得: 同一 (族, 轴) 在多个目录里都有时, 取遍历顺序靠前的
			// —— 顺序稳定 ⇒ 每次启动选到同一份, 渲染结果可复现 (与
			// scanSystemFonts 的排序纪律同一条理由)。
			if _, dup := ix.files[key][axis]; !dup {
				ix.files[key][axis] = faceSrc{path: path, index: i}
				ix.styleOf[key] = append(ix.styleOf[key], axis)
			}
			if mono {
				ix.mono[key] = true
			}
			ix.setAltLocked(compactFamilyKey(key), key)
			if stemKey != "" {
				ix.setAltLocked(stemKey, key)
				ix.setAltLocked(compactFamilyKey(stemKey), key)
			}
		}
	}
}

// setAltLocked 记一条别名 (首个写入者赢: 别名冲突时不能让后扫到的字体
// 悄悄改写前面已经生效的映射)。
func (ix *familyIndex) setAltLocked(alias, family string) {
	if alias == "" || alias == family {
		return
	}
	if _, ok := ix.alt[alias]; !ok {
		ix.alt[alias] = family
	}
}

// lookup 解析 (族名, 想要的样式) → 具体面 + 还需要合成哪些轴。
//
// 查表顺序: 原样 → 泛型族名的偏好列表 → 文件名别名 → 泛型兜底。
//
// 族名为空串时按"默认字体的族"处理 (§四 文本域缺口): 于是
// `fontWeight="bold"` 这种**只写字重不写族名**的用法也能拿到默认字体的
// 真实粗体面, 而不是退回"默认字体 + 合成"。这几乎是最常见的写法 —— 让人
// 为了加粗一个标题去查系统里装的是什么字体, 说不过去。
func (ix *familyIndex) lookup(family string, want styleAxis) (faceSrc, styleAxis, bool) {
	// baseFamilyKey 要在**取 ix.mu 之前**算: 它走 fontMu (加载默认字体),
	// 而这里持有 ix.mu —— 先算好就不存在"两把锁交叉"的可能。
	baseKey := baseFamilyKey()

	ix.build()
	ix.mu.Lock()
	defer ix.mu.Unlock()

	key := normalizeFamilyKey(family)
	// 直接给路径 (含扩展名且文件存在): 现索引一次再按族名查 —— 这样
	// `fontFamily="assets/MyFont.ttf"` 与 `fontFamily="MyFont"` 落到同一处。
	if p, ok := familyAsFontPath(family); ok {
		ix.indexFileLocked(p)
		key = normalizeFamilyKey(stripStyleSuffix(strings.TrimSuffix(
			filepath.Base(p), filepath.Ext(p))))
		if k, ok := ix.alt[key]; ok {
			key = k
		}
	}
	if key == "" {
		key = baseKey // 空族名 = 跟随默认字体
	}
	if key == "" {
		return faceSrc{}, styleAxis{}, false
	}

	cands := []string{key}
	for _, g := range genericFamilies[key] {
		cands = append(cands, g)
	}
	// 泛型里"跟随系统默认字体"这一档: 把默认字体的族名插到最前, 于是
	// `fontFamily="sans-serif"` + bold 会优先拿默认字体的真实粗体面。
	if genericFollowsBase[key] && baseKey != "" && baseKey != key {
		cands = append([]string{baseKey}, cands...)
	}
	for _, c := range cands {
		if src, synth, ok := ix.pickLocked(c, want); ok {
			return src, synth, true
		}
	}
	// monospace 的兜底: 偏好列表一个都没命中时, 找任意等宽族。
	if genericMono[key] {
		if c := ix.firstMonoLocked(); c != "" {
			if src, synth, ok := ix.pickLocked(c, want); ok {
				return src, synth, true
			}
		}
	}
	return faceSrc{}, styleAxis{}, false
}

// pickLocked 在一个族里挑最接近 want 的面。
//
// 优先级: 精确 → 只保粗体 → 只保斜体 → 正体。为什么粗体优先于斜体: 字重
// 差异是"一眼可见"的 (标题/正文的层级靠它), 而倾斜在等宽/无衬线体上本来
// 就常常不存在, 合成倾斜的观感损失比合成粗体小。
func (ix *familyIndex) pickLocked(family string, want styleAxis) (faceSrc, styleAxis, bool) {
	m, ok := ix.files[family]
	if !ok {
		if real, hit := ix.alt[family]; hit {
			m, ok = ix.files[real]
			if !ok {
				return faceSrc{}, styleAxis{}, false
			}
		} else {
			return faceSrc{}, styleAxis{}, false
		}
	}
	if src, hit := m[want]; hit {
		return src, styleAxis{}, true
	}
	tries := []styleAxis{{bold: want.bold}, {italic: want.italic}, {}}
	for _, a := range tries {
		if src, hit := m[a]; hit {
			return src, styleAxis{bold: want.bold && !a.bold, italic: want.italic && !a.italic}, true
		}
	}
	return faceSrc{}, styleAxis{}, false
}

// firstMonoLocked 返回字典序第一个等宽族 (确定性: 不依赖 map 遍历顺序)。
func (ix *familyIndex) firstMonoLocked() string {
	var names []string
	for name, mono := range ix.mono {
		if mono {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return names[0]
}

// ===== 族名 / 样式的归一 =====

// normalizeFamilyKey 归一一个族名 (小写、去引号、内部空白折叠为单空格)。
// 归一而不是"原样比较": 脚本里写 "PingFang SC" / "pingfang sc" / " PingFang  SC "
// 都该命中同一个族。
func normalizeFamilyKey(s string) string {
	s = strings.TrimSpace(strings.Trim(strings.TrimSpace(s), `"`+"'"))
	if s == "" {
		return ""
	}
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// compactFamilyKey 去掉全部空白 ("dejavu sans" → "dejavusans")。
// 它是别名表的另一个键: 字体文件名里几乎从不带空格。
func compactFamilyKey(s string) string {
	return strings.ReplaceAll(s, " ", "")
}

// fontNameOf 读字体 name 表的一个条目 (取不到 → 空串)。
func fontNameOf(f *opentype.Font, buf *sfnt.Buffer, id sfnt.NameID) string {
	s, err := f.Name(buf, id)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

// axisFromFont 判定一个面属于哪个样式轴。
//
// 子族名是主判据 (Regular / Bold / Italic / Bold Italic / Oblique …),
// post 表的 ItalicAngle 是补判据 (有些字体子族名只写 "Regular" 却真的倾斜)。
// **不看 OS/2 的 usWeightClass**: x/image 没有暴露 OS/2 表, 而且子族名
// 里的 "bold" 已经覆盖了 SemiBold/DemiBold/ExtraBold 这一族词。
func axisFromFont(f *opentype.Font, subfamily string) styleAxis {
	s := strings.ToLower(subfamily)
	bold := strings.Contains(s, "bold") || strings.Contains(s, "heavy") || strings.Contains(s, "black")
	if !bold {
		bold = appleWeightBold(s)
	}
	italic := strings.Contains(s, "italic") || strings.Contains(s, "oblique")
	if !italic {
		if pt := f.PostTable(); pt != nil && pt.ItalicAngle != 0 {
			italic = true
		}
	}
	return styleAxis{bold: bold, italic: italic}
}

// appleWeightBold 认苹果 CJK 字体的字重子族名 (W3 / W6 / W9 …)。
//
// 为什么值得为它单开一条: 苹果的中文系统字体 (冬青黑体、苹方、华文黑体)
// 把字重写成 "W3"(正体) / "W6"(粗体), **完全不含 bold 字样**。不认它的话
// "给中文加粗"在所有苹果设备上都会悄悄退化成合成粗体 —— 观感明显更差, 而
// 且完全没有报错可查。W3 及更轻算正体, W6 及更重算粗体 (与苹果自己的
// 字重表一致: W3≈400, W6≈700)。
func appleWeightBold(subfamily string) bool {
	s := strings.TrimSpace(subfamily)
	if len(s) < 2 || (s[0] != 'w' && s[0] != 'W') {
		return false
	}
	digits := s[1:]
	for _, r := range digits {
		if r < '0' || r > '9' {
			return false
		}
	}
	n, err := strconv.Atoi(digits)
	if err != nil {
		return false
	}
	return n >= 6
}

// styleSuffixes 是文件名里常见的样式后缀 (由长到短匹配)。
//
// 为什么要剥掉: 字体文件名普遍是"族名+样式"拼起来的 (DejaVuSans-Bold.ttf /
// menloi.ttf), 而脚本写的一定是族名。不剥的话 `fontFamily="DejaVuSans-Bold"`
// 就只能靠文件名主干整体命中 —— 少剥一层就少一批能用的写法。
var styleSuffixes = []string{
	"bolditalic", "boldoblique", "semibold", "demibold", "extrabold",
	"italic", "oblique", "bold", "regular", "medium", "light", "thin",
	"bd", "bi", "it", "b", "i",
}

// stripStyleSuffix 反复剥掉尾部的样式后缀与分隔符。
//
// 单字母后缀 (b/i/it/bi) 只在**文件名主干**上用: "menloi" 要能对上 Menlo。
// 它们也确实是歧义的 (族名 "Ab" 会被剥成 "A"), 所以调用方只把它喂给别名表
// —— 别名表是"最后才查"的一层, 猜错不会覆盖任何真实族名。
func stripStyleSuffix(stem string) string {
	for {
		trimmed := strings.TrimRight(stem, " -_.")
		if trimmed == "" {
			return ""
		}
		cut := ""
		for _, suf := range styleSuffixes {
			if len(trimmed) > len(suf)+1 && strings.HasSuffix(strings.ToLower(trimmed), suf) {
				cut = trimmed[:len(trimmed)-len(suf)]
				break
			}
		}
		if cut == "" {
			return trimmed
		}
		stem = cut
	}
}

// genericFamilies 是 CSS 泛型族名 → 按平台常见度排好的具体族名偏好表。
// 顺序即优先级: 表里靠前的存在就用靠前的, 一个都没有才走别的兜底。
var genericFamilies = map[string][]string{
	"sans-serif": {"helvetica neue", "helvetica", "arial", "roboto",
		"segoe ui", "noto sans", "dejavu sans", "liberation sans",
		"pingfang sc", "hiragino sans gb", "microsoft yahei", "pingfang"},
	"ui-sans-serif": {"helvetica neue", "helvetica", "arial", "roboto",
		"segoe ui", "noto sans", "dejavu sans", "liberation sans",
		"pingfang sc", "hiragino sans gb", "microsoft yahei", "pingfang"},
	"serif": {"times new roman", "georgia", "dejavu serif", "liberation serif",
		"noto serif", "songti sc", "stsong", "simsun", "times"},
	"ui-serif": {"times new roman", "georgia", "dejavu serif", "liberation serif",
		"noto serif", "songti sc", "stsong", "simsun", "times"},
	"monospace": {"menlo", "sf mono", "consolas", "source code pro",
		"dejavu sans mono", "liberation mono", "roboto mono", "noto sans mono",
		"courier new", "courier"},
	"ui-monospace": {"menlo", "sf mono", "consolas", "source code pro",
		"dejavu sans mono", "liberation mono", "roboto mono", "noto sans mono",
		"courier new", "courier"},
	"cursive": {"snell roundhand", "apple chancery", "comic sans ms", "zapfino"},
	"fantasy": {"papyrus", "impact", "chalkboard"},
}

// genericFollowsBase 是"这一档泛型应该优先跟随系统默认字体"的集合。
// 无衬线/系统字体是同一回事 —— 默认字体就是这台机器上最合适的那个。
var genericFollowsBase = map[string]bool{
	"": true, "system-ui": true, "ui-sans-serif": true, "sans-serif": true,
	"-apple-system": true, "blinkmacsystemfont": true,
}

// genericMono 是"没有具体族命中时要退到任意等宽族"的泛型名。
var genericMono = map[string]bool{"monospace": true, "ui-monospace": true, "mono": true}

// familyAsFontPath 报告 family 是否是一个**字体文件路径**且文件存在。
func familyAsFontPath(family string) (string, bool) {
	s := strings.TrimSpace(strings.Trim(strings.TrimSpace(family), `"`+"'"))
	if s == "" || (!strings.ContainsAny(s, `/\`) && !fontExtOK(s)) {
		return "", false
	}
	if st, err := os.Stat(s); err != nil || st.IsDir() {
		return "", false
	}
	return s, true
}

// baseFamilyKey 返回默认字体 (baseFont) 的族名 (归一后), 取不到则空串。
func baseFamilyKey() string {
	baseFamilyOnce.Do(func() {
		f, err := loadBaseFont()
		if err != nil {
			return
		}
		var buf sfnt.Buffer
		if s := fontNameOf(f, &buf, sfnt.NameIDTypographicFamily); s != "" {
			baseFamily = normalizeFamilyKey(s)
			return
		}
		baseFamily = normalizeFamilyKey(fontNameOf(f, &buf, sfnt.NameIDFamily))
	})
	return baseFamily
}

var (
	baseFamilyOnce sync.Once
	baseFamily     string
)

// ===== 面的构造 =====

// resolvedFace 是一次"样式请求"的解析结果。
//
// id 只在同一个请求内稳定 (它是缓存槽位号), 用作字形缓存的键 —— 于是
// "同族同轴同字号"天然共享同一批字形, 而不同请求绝不会串味。
type resolvedFace struct {
	id     int
	face   font.Face
	size   int
	ascent int
	synthB bool // 用合成方式加粗 (该族没有真实粗体面)
	synthI bool // 用合成方式倾斜
	// isBase 标记"这就是默认字体"。缺字回退链靠它终止: 默认字体自己缺字
	// 就是真缺字, 再试一次只是白花一次 Glyph 调用。
	isBase bool
}

// faceRequest 是 faceCache 的键。family 保留原始写法 (可能是个路径)。
type faceRequest struct {
	family string
	axis   styleAxis
	size   int
}

var (
	faceCacheMu  sync.Mutex
	faceCache    = map[faceRequest]resolvedFace{}
	faceCacheSeq int
)

// faceForStyle 解析样式请求 → 可用的 face + 合成标记 + ascent。
//
// 永不返回"因为字体名写错"而失败: 只有在连默认字体都加载不出来时才报错
// (那说明这台机器上没有可用字体, 是环境问题而不是配置问题)。
func faceForStyle(st TextStyle) (resolvedFace, error) {
	size := st.Size
	if size < 8 {
		size = 8
	}
	req := faceRequest{family: st.Family, axis: styleAxis{bold: st.Bold, italic: st.Italic}, size: size}

	faceCacheMu.Lock()
	if rf, ok := faceCache[req]; ok {
		faceCacheMu.Unlock()
		return rf, nil
	}
	faceCacheMu.Unlock()

	rf, err := buildResolvedFace(req)
	if err != nil {
		return resolvedFace{}, err
	}
	faceCacheMu.Lock()
	if prev, ok := faceCache[req]; ok { // 并发下可能已有人填了, 用那一个
		faceCacheMu.Unlock()
		return prev, nil
	}
	faceCacheSeq++
	rf.id = faceCacheSeq
	faceCache[req] = rf
	faceCacheMu.Unlock()
	return rf, nil
}

// buildResolvedFace 真正去解析 (索引 → 面), 失败则退回默认字体 + 合成。
func buildResolvedFace(req faceRequest) (resolvedFace, error) {
	// ===== 快路径: 没写 fontFamily、也没要粗斜体 =====
	//
	// 这条路径**绝不能碰字体族索引**: 建索引要遍历字体目录并解析几百个
	// 字体头 (~300ms, 实机实测), 而绝大多数文本都不写 fontFamily ——
	// 让"用了一次 GUI"就等于付这笔钱, 整个惰性设计的意义就没了。
	//
	// 语义上也确实无事可做: 无族名 + 无样式的请求, 答案就是 baseFont
	// 本身, 与加样式轴之前完全一致 (所以默认样式的渲染逐像素不变)。
	if normalizeFamilyKey(req.family) == "" && req.axis == (styleAxis{}) {
		return baseFaceFor(TextStyle{Size: req.size})
	}

	src, synth, ok := familyIdx.lookup(req.family, req.axis)
	if ok {
		if ft, err := fontFromSrc(src); err == nil {
			if fc, err := newFaceFrom(ft, req.size); err == nil {
				return resolvedFace{
					face:   fc,
					size:   req.size,
					ascent: ascentOfFace(fc, req.size),
					synthB: synth.bold,
					synthI: synth.italic,
				}, nil
			}
		}
	}
	// 族找不到 / 面建不出来: 退回默认字体, 请求的粗斜体改由合成实现。
	// 静默而不是报错 —— 一个拼错的字体名不该让整屏文字不渲染。
	rf, err := baseFaceFor(TextStyle{Size: req.size})
	if err != nil {
		return resolvedFace{}, err
	}
	rf.synthB, rf.synthI = req.axis.bold, req.axis.italic
	return rf, nil
}

// baseFaceFor 取默认字体在某个样式字号下的面 —— 缺字回退链的终点。
//
// 它**不进 faceCache**: 那是一张"样式请求 → 面"的表, 而这里要的是"不管
// 请求什么族, 都给默认字体", 混进去会把默认面挂在某个族的名下。
func baseFaceFor(st TextStyle) (resolvedFace, error) {
	size := st.Size
	if size < 8 {
		size = 8
	}
	fc, err := fontFace(size)
	if err != nil {
		return resolvedFace{}, err
	}
	return resolvedFace{face: fc, size: size, ascent: textAscent(size), isBase: true}, nil
}

// newFaceFrom 按像素字号给任意字体建 face (与 fontFace 同口径: DPI=72,
// 1pt = 1px, HintingFull)。
func newFaceFrom(f *opentype.Font, size int) (font.Face, error) {
	if size < 8 {
		size = 8
	}
	return opentype.NewFace(f, &opentype.FaceOptions{
		Size:    float64(size),
		DPI:     72,
		Hinting: font.HintingFull,
	})
}

// ascentOfFace 读一个 face 的 ascent (取不到时退回字号本身, 与 textAscent
// 的兜底一致 —— 布局永远拿得到一个正数)。
func ascentOfFace(fc font.Face, size int) int {
	m := fc.Metrics()
	a := m.Ascent.Ceil()
	if a <= 0 {
		a = size
	}
	return a
}

// ===== 字体文件 → 面的缓存 =====

var (
	fontSrcMu     sync.Mutex
	facesByPath   = map[string][]*opentype.Font{}
	maxCachedFont = 24 // 最多缓存这么多个**文件**的解析结果
	fontPathOrder []string
)

// fontFromSrc 取出 (路径, 面序号) 对应的字体对象。
//
// 这里用整份读入 (而不是索引阶段的 ReaderAt): face 要反复取字形轮廓,
// 留在文件句柄上会让 FD 一直开着, 而一个应用的字体数量是个位数 —— 读进
// 内存更简单也更稳。按文件缓存, 于是"同一族的粗体+正体"只读一次盘。
func fontFromSrc(src faceSrc) (*opentype.Font, error) {
	fontSrcMu.Lock()
	faces, ok := facesByPath[src.path]
	fontSrcMu.Unlock()
	if !ok {
		data, err := os.ReadFile(src.path)
		if err != nil {
			return nil, err
		}
		col, err := opentype.ParseCollection(data)
		if err != nil {
			return nil, err
		}
		n := col.NumFonts()
		faces = make([]*opentype.Font, n)
		for i := 0; i < n; i++ {
			if f, err := col.Font(i); err == nil {
				faces[i] = f
			}
		}
		fontSrcMu.Lock()
		if len(fontPathOrder) >= maxCachedFont {
			old := fontPathOrder[0]
			fontPathOrder = fontPathOrder[1:]
			delete(facesByPath, old)
		}
		facesByPath[src.path] = faces
		fontPathOrder = append(fontPathOrder, src.path)
		fontSrcMu.Unlock()
	}
	if src.index < 0 || src.index >= len(faces) {
		return nil, fmt.Errorf("font %s: no face #%d", filepath.Base(src.path), src.index)
	}
	f := faces[src.index]
	if f == nil {
		return nil, fmt.Errorf("font %s: face #%d unreadable", filepath.Base(src.path), src.index)
	}
	return f, nil
}

// resetFontCaches 清掉全部字体派生缓存 (宿主换字体后必须做)。
//
// 漏清任何一层都会得到"换了字体没生效"或更糟的"新 face 配旧字形" (见
// glyphCache.reset 的说明)。族索引也必须重建: 它里面的路径可能已经失效。
func resetFontCaches() {
	faceCacheMu.Lock()
	faceCache = map[faceRequest]resolvedFace{}
	faceCacheSeq = 0
	faceCacheMu.Unlock()

	fontSrcMu.Lock()
	facesByPath = map[string][]*opentype.Font{}
	fontPathOrder = nil
	fontSrcMu.Unlock()

	familyIdx.mu.Lock()
	familyIdx.built = false
	familyIdx.files = nil
	familyIdx.alt = nil
	familyIdx.mono = nil
	familyIdx.styleOf = nil
	familyIdx.mu.Unlock()

	baseFamilyOnce = sync.Once{}
	baseFamily = ""

	glyphLRU.reset()
}

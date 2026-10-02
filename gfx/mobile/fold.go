package mobile

// 折叠屏 (foldable) 的**纯 Go 逻辑层**。
//
// 为什么这一层必须落在 gfx/mobile (而不是 gfx/ios 或 gfx/android):
// 那两个包带 platform build tag, 在开发机 (Windows) 上根本编译不到 —— 于是
// "折痕怎么切、保留区怎么归一、姿态怎么判" 这些**真正会算错**的部分只能在
// 真机上调试。把它们放进本包 (无 build tag、只依赖标准库) 之后, 桌面
// `go test ./gfx/mobile/` 就能覆盖全部边界情形, 平台侧退化成:
//
//	Swift/Kotlin: 读平台 API → 拼一段 JSON → gox_set_display_fold(json)
//	                                    ↓
//	                          本包 ReportDisplayFold(json)
//	                                    ↓ gfx.Post (跨线程边界唯一入口)
//	                          内核 reportPosture / ReportViewport
//
// **纪律**: 本文件只做"读数据 → 算数据", 不认识任何平台类型, 也不直接碰内核
// 状态 (改内核状态一律经 gfx.Post 投回 GUI 线程)。理由与 mobile.go 的线程模型
// 注释同源: 平台回调可能在任意线程上, 而 reportPosture 会**同步**跑脚本的
// 订阅回调 (见 gfx/screen.go 的 notifyEnvChanged) —— 在平台线程上直接跑 JS
// 会撕裂 VM。
//
// 与内核的分工:
//   - 本包产出**已归一化**的 Region 列表 (裁到屏内、判好 active);
//   - 内核 (gfx/screen.go) 负责 upsert 进显示器表并通知订阅者;
//   - 谁都不猜姿态: 判不出来就 "unknown" (与 mobile.go Displays() 里那句
//     "Foldable/Posture/Hinge 留给折叠屏宿主显式上报, 这里不猜" 同一理由)。

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/14752222/Gox/gfx"
)

// ===== 平台无关的数据模型 =====

// RawHinge 是宿主原样报上来的折痕几何 (未经裁剪)。
//
// 坐标是**显示器坐标 (设备像素)**, 与 gfx.DisplayHinge 同一坐标系 —— 这样
// 宿主不需要做任何换算: Android 的 FoldingFeature.bounds 与 iOS 的
// reservedRegions 天生就是这个坐标系。
type RawHinge struct {
	X, Y, W, H  int
	Orientation string // "vertical" (左右折) / "horizontal" (上下折)
}

// RawRegion 是宿主原样报上来的一段保留区。
//
// Active 用指针是有意的: "没说" 与 "说了 false" 是两回事 —— 没说时按
// "零宽/零高即 inactive" 推导 (对齐 iOS 平放时 division 宽度为 0 的描述),
// 说了 false 则无条件尊重 (宿主可能知道得比几何更多)。
type RawRegion struct {
	Kind   string
	X, Y   int
	W, H   int
	Active *bool
}

// Region 是**归一化之后**的保留区: 已裁到屏内, Active 已是确定值, ID 稳定。
type Region struct {
	ID     string
	Kind   string
	X, Y   int
	W, H   int
	Active bool
}

// FoldInfo 是宿主上报的完整折叠态 (JSON 载荷的 Go 镜像)。
//
// 字段名与 gfx.Display 对齐, 但**只包含折叠相关的部分**: 尺寸/密度仍然走
// 既有的 Resize, 免得同一次上报要在两个地方各写一份。
type FoldInfo struct {
	// ID 是显示器归属 (载荷里的 `id` 或 `display`, `id` 优先)。
	//
	// 为什么接它: 三端宿主 (gfx/{android,ios,harmony}/libgox) 都把唯一的
	// 内置屏注册成 ID "0", 鸿蒙上报器也一直带着 `display: '0'` —— 之前
	// decodeFoldInfo 不解析, 属于"上报了却没人读"。接上之后 upsert 按
	// 这个 ID 命中同一块屏; 缺省 (空) 保持原行为, 由内核落到"当前窗口
	// 所在的那块屏" (ReportPostureFromFold → displayOfSurface(nil))。
	ID          string
	Posture     string      `json:"posture"`
	WidthClass  string      `json:"widthClass"`
	HeightClass string      `json:"heightClass"`
	Hinge       *RawHinge   `json:"hinge"`
	Regions     []RawRegion `json:"regions"`
	Width       int         `json:"width"`
	Height      int         `json:"height"`
}

// 归一化后的姿态取值。与 gfx/screen.go 的私有常量同名同值, 但本包**不能**
// 引用它们 (那是未导出标识符) —— 这里的字符串就是两边之间的契约, 改一处
// 必须同步另一处 (gfx/screen_test.go 的 TestPostureVocabularyMatches 钉住了
// 这层一致性)。
const (
	PostureFlat     = "flat"
	PostureHalfOpen = "half-open"
	PostureFolded   = "folded"
	PostureUnknown  = "unknown"
)

// 保留区类型。同样与 gfx 的 RegionDivision / RegionOcclusion 是契约关系。
const (
	KindDivision  = "division"
	KindOcclusion = "occlusion"
)

// ===== 归一化 =====

// NormalizeRegions 把宿主上报的原始保留区裁到屏幕范围内, 并给出稳定的 ID。
//
// 规则 (每条都对应一个真实会踩的坑):
//   - **负坐标钳到 0**: 宿主偶尔会报出 {-2, 0, 24, 2560} 这种值 (折痕贴边时
//     的圆整误差), 不钳的话裁剪出来的矩形会从屏外开始, 避让偏移算出来是负的;
//   - **超出 w/h 的部分裁掉**: 上报的是设备坐标, 而当前 Surface 可能还没
//     跟上新的尺寸 (Activity 的 onConfigurationChanged 与 Surface 回调不同步);
//     不裁就会得到"区域在屏外"的幽灵保留区;
//   - **完全落在屏外 → 丢弃**: 与"不可见"不同, 它压根不描述本屏;
//   - **w<=0 || h<=0 的 division 判 Active=false**: 对齐 iOS "平放时 division
//     宽度为 0 且 inactive"; occlusion 即使很窄也有意义 (它描述一块真实遮挡),
//     但零面积的还是没有意义, 一并判 inactive;
//   - **顺序稳定**: 按 kind 分组 (division 在前)、组内保持声明序 —— 宿主数组
//     顺序可能随平台回调顺序抖动, 归一化后必须定下来, 否则脚本侧
//     `regions[0]` 会时而指向 division 时而指向 occlusion。
//
// id 为空的按其 kind 生成 (fold-N / occlusion-N / region-N), 与
// gfx/screen.go reportPostureGo 的 ID 生成规则**保持一致**; 非空 id 原样保留。
func NormalizeRegions(raw []RawRegion, w, h int) []Region {
	if len(raw) == 0 {
		return nil
	}
	out := make([]Region, 0, len(raw))
	foldIdx, occIdx, otherIdx := 0, 0, 0
	for _, r := range raw {
		kind := normalizeFoldKind(r.Kind)
		reg := Region{Kind: kind, X: r.X, Y: r.Y, W: r.W, H: r.H}
		switch kind {
		case KindDivision:
			reg.ID = "fold-" + itoa(foldIdx)
			foldIdx++
		case KindOcclusion:
			reg.ID = "occlusion-" + itoa(occIdx)
			occIdx++
		default:
			reg.ID = "region-" + itoa(otherIdx)
			otherIdx++
		}

		// 裁剪到屏内。w/h <= 0 表示"调用方不知道屏多大" —— 此时不做裁剪,
		// 只保留原始几何 (宁可放过也不能凭空把区域裁没)。
		if w > 0 && h > 0 {
			if reg.X < 0 {
				reg.W += reg.X // 左边界被切掉多少就少多少宽
				reg.X = 0
			}
			if reg.Y < 0 {
				reg.H += reg.Y
				reg.Y = 0
			}
			// **零尺寸的 division 不能丢**: 平展时 iOS 报的就是
			// {w:0, active:false}, 它承载"这里有一条折痕"这一结构信息 ——
			// 丢掉的话 StructuralRegions 会在 折叠→平展→折叠 之间
			// flip-flop (列数跟着跳)。它只是判 inactive, 不是不存在。
			// 负尺寸才是真的坏数据 (宿主算错了), 丢弃。
			if reg.W < 0 || reg.H < 0 {
				continue
			}
			if reg.X >= w || reg.Y >= h {
				continue // 起点就在屏外: 不描述本屏
			}
			if reg.X+reg.W > w {
				reg.W = w - reg.X
			}
			if reg.Y+reg.H > h {
				reg.H = h - reg.Y
			}
			if reg.W == 0 && reg.H == 0 && kind != KindDivision {
				continue // 零面积的 occlusion/未知区没有任何信息量
			}
		}

		if r.Active != nil {
			// 显式给了就用 —— 宿主可能知道几何之外的信息 (例如已折起但
			// 折痕宽度还没更新)。
			reg.Active = *r.Active
		} else {
			reg.Active = reg.W > 0 && reg.H > 0
		}
		out = append(out, reg)
	}
	if len(out) == 0 {
		return nil
	}
	stableSortRegions(out)
	return out
}

// stableSortRegions 按 kind 分组 (division → occlusion → 未知), 组内保持原序。
//
// 用 sort.SliceStable 而不是 sort.Slice: 组内顺序是宿主给的语义顺序
// (多段折痕时的左→右), 不能被排序算法打乱。
func stableSortRegions(rs []Region) {
	rank := func(k string) int {
		switch k {
		case KindDivision:
			return 0
		case KindOcclusion:
			return 1
		default:
			return 2
		}
	}
	sort.SliceStable(rs, func(i, j int) bool { return rank(rs[i].Kind) < rank(rs[j].Kind) })
}

// SplitByKind 取出指定 kind 的保留区。
//
// 空输入返回 **nil 而不是空切片**: 脚本侧最自然的写法是 `len(regions()) === 0`,
// 返回值统一成 nil 能让 Go 侧与 JS 侧 (object.NewArray(nil) 也是空数组) 语义
// 一致, 也免得调用方为了判断空而写 `len(x) == 0 || x == nil`。
func SplitByKind(rs []Region, kind string) []Region {
	var out []Region
	for _, r := range rs {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

// StructuralRegions 返回"设备结构上存在"的保留区 —— **忽略 Active**。
//
// 这是 iOS `reservedRegions` 那套 API 里 includeInactive 的对应物, 解决一个
// 很具体的问题: 半折 → 平展 → 半折 的过程中, division 的 active 会 flip-flop
// (平展时宽度 0 且 inactive)。若列数跟着 active 走, 界面会在 1 列 / 2 列之间
// 跳 —— 而用户手上根本没换姿势 (只是折了一下又折回来)。按结构算就稳定了:
// 折痕一直都在, 变的只是"此刻要不要避让"。
//
// 判据是 **kind 语义**, 不是几何:
//   - division 是**结构性**的 —— 有折痕的机器永远有一条 (平展时尺寸为 0 而已),
//     所以只要出现就算;
//   - occlusion 是**几何性**的 —— 屏下摄像头那块只在真有面积时才占地方,
//     零尺寸的没有意义。
//
// 注意这**不等于**"忽略姿态": 要不要分栏仍然看 PostureFrom 的结果, 这里只
// 回答"设备结构上有没有这条折痕"。
func StructuralRegions(rs []Region) []Region {
	var out []Region
	for _, r := range rs {
		if r.Kind == KindDivision || (r.W > 0 && r.H > 0) {
			out = append(out, r)
		}
	}
	return out
}

// PostureFrom 从折痕几何 + 保留区推断姿态。
//
// **绝不猜**:
//   - 没有 hinge 且没有 active 的 division ⇒ flat (非折叠屏 / 平展, 二者
//     对布局而言等价 —— 都是一整块);
//   - 有 hinge 但**没有尺寸信息** (w/h 均 <= 0) ⇒ unknown。宿主报了一条折痕
//     却说不出它多宽多高, 我们无法判断现在是平展 (宽度 0) 还是半折;
//   - 有 hinge 且有宽度 ⇒ 看尺寸类未知与否由调用方决定, 这里只按几何判:
//     有宽度的 division/hinge 说明屏被切开了 ⇒ half-open。
//
// 之所以不在这里引入"尺寸类"参与判定: 尺寸类是断点 (compact/medium/expanded),
// 而姿态是物理形态, 两者正交 —— 一台展开的手机 (regular) 完全可能是 half-open。
func PostureFrom(hinge *RawHinge, regions []Region) string {
	hasActiveDiv := false
	for _, r := range regions {
		if r.Kind == KindDivision && r.Active {
			hasActiveDiv = true
			break
		}
	}
	if hinge == nil {
		if hasActiveDiv {
			return PostureHalfOpen
		}
		return PostureFlat
	}
	if hinge.W <= 0 && hinge.H <= 0 {
		// 有折痕对象但没有尺寸: 分不清"平展 (宽度 0)"与"宿主忘了填"。
		return PostureUnknown
	}
	return PostureHalfOpen
}

// ToDisplayRegions 转换给内核上报路径用 (gfx.DisplayRegion)。
//
// 只做字段搬运, 不改语义 —— 归一化已经在 NormalizeRegions 里做完了。
func ToDisplayRegions(rs []Region) []gfx.DisplayRegion {
	if len(rs) == 0 {
		return nil
	}
	out := make([]gfx.DisplayRegion, 0, len(rs))
	for _, r := range rs {
		out = append(out, gfx.DisplayRegion{
			ID: r.ID, Kind: r.Kind,
			X: r.X, Y: r.Y, W: r.W, H: r.H,
			Active: r.Active,
		})
	}
	return out
}

// ===== 上报入口 =====

// ReportDisplayFold 是平台侧的唯一折叠上报入口。
//
// 参数是一段 JSON (字段见 FoldInfo)。**选 JSON 而不是强类型跨语言签名**:
// 这条通道的字段还会长 (多段折叠区、occlusion 子类、将来的 angle), 而每加
// 一个字段, 强类型入口要三处同步 (Go 的 //export / cgo 生成的 libgox.h /
// Swift 调用点), 漏一处就是编译期或更糟的静默失配。JSON 的代价只在姿态
// 变化时付一次 (不是每帧), 换来的是一处定义处处可用。
//
// **线程**: 本函数可以从任意线程调用 (Kotlin 的 WindowInfoTracker 回调、
// Swift 的 traitCollectionDidChange 都不保证在主线程), 内部一律 `gfx.Post`
// 投回 GUI 线程再动内核状态 —— 内核的 reportPosture 会同步跑脚本订阅回调,
// 在平台线程上跑 JS 会撕裂 VM。
//
// 返回 error 只描述"这段 JSON 有问题"; 投递到 GUI 线程之后发生的事 (内核
// 拒绝、脚本抛错) 不在返回值里 —— 那些走内核自己的报错通道。
func ReportDisplayFold(jsonStr string) error {
	info, err := decodeFoldInfo(jsonStr)
	if err != nil {
		return err
	}
	// 尺寸类与姿态/保留区一起投: 它们在同一个 JSON 里, 拆成两次 Post 会让
	// 中间态被别的任务看见 (先报尺寸后报姿态 ⇒ 有一瞬间是"大屏 + 平展",
	// 订阅者会重排一次再重排回来)。
	w, h := info.Width, info.Height
	widthClass, heightClass := info.WidthClass, info.HeightClass
	hinge := info.Hinge
	rawRegions := info.Regions
	posture := info.Posture

	gfx.Post(func() {
		regions := NormalizeRegions(rawRegions, w, h)
		// 姿态优先用宿主明说的; 没说明才由几何推断 (宿主比我们更清楚, 例如
		// 折叠态下只剩一段屏时 hinge 仍在但姿态是 folded)。
		pos := strings.ToLower(strings.TrimSpace(posture))
		if pos == "" {
			pos = PostureFrom(hinge, regions)
		}
		gfx.ReportPostureFromFold(gfx.Display{
			// 空串让内核自己落到"当前窗口所在屏"; 非空则按 ID upsert。
			ID:       info.ID,
			Posture:  pos,
			Foldable: hinge != nil || len(SplitByKind(regions, KindDivision)) > 0,
			Hinge:    toDisplayHinge(hinge),
			Regions:  ToDisplayRegions(regions),
			W:        w,
			H:        h,
		})
		if widthClass != "" || heightClass != "" {
			gfx.ReportViewport(nil, gfx.Viewport{
				WidthClass:  widthClass,
				HeightClass: heightClass,
			})
		}
	})
	return nil
}

// decodeFoldInfo 解析 JSON 载荷并做**字段级**归一化 (不改几何)。
//
// 兼容宽松: 尺寸类同时认 `sizeClass:{width,height}` 与扁平的
// `widthClass`/`heightClass`; 折痕同时认 `w/h` 与 `width/height` —— 与
// gfx/screen.go 的 objPropSize 同一取舍 ("最自然的写法必须能用")。
// 显示器归属同时认 `id` 与 `display` (`id` 优先) —— 鸿蒙上报器历史上发的
// 就是 `display`。
//
// **有意不认的两个名字** (宿主侧拼了也是静默无效, 见 NATIVE-HOST.md):
//   - `scale`: ReportPostureFromFold 这条通道没有 scale 的输入 —— 尺寸类
//     由宿主预计算 (widthClass/heightClass), 设备像素比走 mobile.New 的
//     Density 与 Displays() 上报, 接了也没人读;
//   - `foldable`: 内核由折痕结构推导 (`hinge != nil || 有 division`), 且
//     ReportPostureFromFold 在 hinge 存在时**无条件**置 Foldable=true ——
//     宿主显式声明 false 会被覆盖, 语义上没有可表达的增量。
func decodeFoldInfo(jsonStr string) (FoldInfo, error) {
	var raw struct {
		ID          string `json:"id"`
		Display     string `json:"display"`
		Posture     string `json:"posture"`
		WidthClass  string `json:"widthClass"`
		HeightClass string `json:"heightClass"`
		SizeClass   *struct {
			Width  string `json:"width"`
			Height string `json:"height"`
		} `json:"sizeClass"`
		Hinge *struct {
			X           int    `json:"x"`
			Y           int    `json:"y"`
			W           int    `json:"w"`
			H           int    `json:"h"`
			Width       int    `json:"width"`
			Height      int    `json:"height"`
			Orientation string `json:"orientation"`
		} `json:"hinge"`
		Regions []struct {
			ID     string `json:"id"`
			Kind   string `json:"kind"`
			X      int    `json:"x"`
			Y      int    `json:"y"`
			W      int    `json:"w"`
			H      int    `json:"h"`
			Width  int    `json:"width"`
			Height int    `json:"height"`
			Active *bool  `json:"active"`
		} `json:"regions"`
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	if err := json.Unmarshal([]byte(jsonStr), &raw); err != nil {
		return FoldInfo{}, err
	}
	info := FoldInfo{
		ID:          strings.TrimSpace(raw.ID),
		Posture:     raw.Posture,
		WidthClass:  raw.WidthClass,
		HeightClass: raw.HeightClass,
		Width:       raw.Width,
		Height:      raw.Height,
	}
	if info.ID == "" {
		info.ID = strings.TrimSpace(raw.Display)
	}
	if info.WidthClass == "" && raw.SizeClass != nil {
		info.WidthClass = raw.SizeClass.Width
	}
	if info.HeightClass == "" && raw.SizeClass != nil {
		info.HeightClass = raw.SizeClass.Height
	}
	if raw.Hinge != nil {
		orientation := strings.ToLower(strings.TrimSpace(raw.Hinge.Orientation))
		if orientation == "" {
			// 与 gfx/screen.go 的默认值一致: 没说明按"左右折"(竖条) ——
			// 这是绝大多数折叠机的形态 (竖折机的内屏也是左右折)。
			orientation = "vertical"
		}
		info.Hinge = &RawHinge{
			X: raw.Hinge.X, Y: raw.Hinge.Y,
			W: pickSize(raw.Hinge.W, raw.Hinge.Width),
			H: pickSize(raw.Hinge.H, raw.Hinge.Height),
			Orientation: orientation,
		}
	}
	for _, r := range raw.Regions {
		info.Regions = append(info.Regions, RawRegion{
			Kind: r.Kind,
			X:    r.X, Y: r.Y,
			W: pickSize(r.W, r.Width),
			H: pickSize(r.H, r.Height),
			// 注意: 这里**丢掉**了上报的 id。ID 由 NormalizeRegions 统一生成,
			// 否则同一段折痕随宿主回调顺序不同会拿到不同 id, 脚本侧按 id
			// 缓存的状态会错位。
			Active: r.Active,
		})
	}
	return info, nil
}

// pickSize 在 "短名优先" 的前提下取尺寸 (与 gfx/screen.go objPropSize 同规则)。
func pickSize(short, long int) int {
	if short != 0 {
		return short
	}
	return long
}

// toDisplayHinge 把原始折痕搬成内核类型 (nil 进 nil 出)。
func toDisplayHinge(h *RawHinge) *gfx.DisplayHinge {
	if h == nil {
		return nil
	}
	return &gfx.DisplayHinge{X: h.X, Y: h.Y, W: h.W, H: h.H, Orientation: h.Orientation}
}

// normalizeFoldKind 归一化保留区类型 (未知 → "", 语义见 gfx/screen.go 同名函数)。
// 这里刻意接受与内核相同的一组别名, 免得同一份 JSON 经两条路径得到不同结果。
func normalizeFoldKind(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case KindDivision, "fold", "hinge":
		return KindDivision
	case KindOcclusion, "occluded", "camera":
		return KindOcclusion
	default:
		return ""
	}
}

// itoa 是 strconv.Itoa 的本地替身 —— 本文件只需要这一处, 引整个 strconv
// 只为拼 ID 不划算 (且能省一次 import 审查)。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

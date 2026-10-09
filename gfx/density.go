package gfx

import "sync"

// ===== 密度换算: 脚本写的逻辑单位 (dp/pt) → 内核的设备像素 =====
//
// ## 问题
//
// 内核的坐标约定是**设备像素** (后端 Size / 事件坐标都是它)。脚本写的
// `font={20}`、`padding={12}` 是按 dp/pt 直觉写的: 作者要的是"20 磅那么大",
// 不是"20 个物理像素"。3x 手机屏上 20 像素只有 6.7pt —— 实测整个 UI 挤成
// 一小块 (看板 rpr9zf §1)。
//
// ## 口径
//
//   - **脚本写出来的尺寸数字一律按逻辑单位解释**, 取值时乘窗口所在显示器的
//     `Display.Scale` 换成设备像素。
//   - **内核自己算出来的东西一律是设备像素**, 不参与换算: 屏幕/窗口尺寸、
//     图片自然尺寸、滚动偏移、内置组件度量常量 (menuItemH 等)。
//   - `Scale == 1` (桌面 x11/win32) 时换算恒等 —— 见 dpToPx 的快路径, 桌面
//     渲染结果与换算前**逐像素一致**, 既有 golden 与像素断言不会漂移。
//
// ## 为什么是"取值时换算"而不是"建树时改 props"
//
// 密度会变: 窗口被拖到另一块屏、用户在系统里改缩放、折叠屏切换主副屏。
// 写进 props 就变成一次性的, 而且会污染"脚本写的值" (脚本读回自己的 prop
// 时不该看到被内核乘过的数)。取值时换算则天然每帧新鲜。
//
// ## 为什么缓存而不是每次读 prop 都去问后端
//
// 解析一次要走 `displayProvider.DisplayOf(surface)` —— 那是平台 API
// (NSWindow / hwnd / ANativeWindow)。布局一帧要读上千次尺寸属性, 每次都
// 调平台 API 会直接吃掉帧预算。所以系数按帧缓存: Layout (每帧布局入口)
// 开头刷新一次, 本帧所有读数共用。

var (
	densityMu    sync.Mutex
	densityScale float64  // 本帧系数; <=0 表示还没算过
	densitySurf  Surface  // 该系数对应的窗口 (窗口换了就作废)
)

// activeSurface 返回"当前窗口"的 Surface (无窗口 → nil)。
func activeSurface() Surface {
	if a := currentApp(); a != nil {
		return a.surface
	}
	return nil
}

// resolveDensity 现场解析密度系数 (无窗口 / 后端查不到 / Scale<=1 → 1)。
//
// 与 defaultFontSize 的前身同一条判定链: 查不到屏必须**安静地落 1**, 而不是
// 报 warning —— 纯节点单测、无头宿主、以及"后端没实现 displayProvider"的
// 平台都走这条路径, 出声只会制造噪音。
func resolveDensity() float64 {
	s := activeSurface()
	if s == nil {
		return 1
	}
	if dp, ok := defaultFactory.(displayProvider); ok {
		if id, hit := dp.DisplayOf(s); hit && id != "" {
			if d, found := findDisplay(id); found && d.Scale > 1 {
				return d.Scale
			}
		}
	}
	return 1
}

// refreshDensity 刷新本帧的密度系数。每帧布局的入口 (Layout) 调一次。
func refreshDensity() {
	densityMu.Lock()
	densityScale = resolveDensity()
	densitySurf = activeSurface()
	densityMu.Unlock()
}

// displayScale 返回本帧的密度系数 (恒 >= 1)。
//
// 缓存的作废条件只有"窗口换了" (densitySurf != 当前窗口)。跨屏 (同一窗口
// 从 1x 屏挪到 2x 屏) 由 Layout 开头的 refreshDensity 覆盖 —— 那才是每帧
// 的必经之路。没走过 Layout 的读数 (挂载前的测量、纯逻辑单测) 现场解析,
// **不写缓存**: 写了会让"下一帧的第一次读数"用到上一帧的窗口。
func displayScale() float64 {
	cur := activeSurface()
	densityMu.Lock()
	s, surf := densityScale, densitySurf
	densityMu.Unlock()
	if s > 0 && surf == cur {
		return s
	}
	return resolveDensity()
}

// ResetDensityForTest 清掉密度缓存 (测试用: 用例之间不许串味)。
func ResetDensityForTest() {
	densityMu.Lock()
	densityScale = 0
	densitySurf = nil
	densityMu.Unlock()
}

// dpToPx 把脚本写的逻辑尺寸换算成设备像素 (四舍五入)。
//
// Scale<=1 时走 `int(v)` 的恒等快路径 —— 注意不能统一用 roundHalf: 既有
// 代码在 Scale=1 时是**截断** (`int(10.7) == 10`), 改成四舍五入会让桌面
// 1x 的渲染结果漂移, 而"桌面 1x 逐像素不变"是本次改动的硬约束。
func dpToPx(v float64) int {
	s := displayScale()
	if s <= 1 {
		return int(v)
	}
	return roundHalf(v * s)
}

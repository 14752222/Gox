package gfx

// 触摸模式 (Prism S8)。
//
// ## 要解决的是"同一个控件在两种输入设备上的两难"
//
// S3/S4/S5 把桌面-heavy 的几何定下来了: 字段行高 36、集合行高 32、页码按钮
// 32×32、复选框视觉 18×18。这些数字对鼠标是对的 —— 32px 的行让一屏能看 40 行,
// 一屏只看得见 10 行的表格没人会用。但同一套数字在手机上违反 WCAG 2.1 SC 2.5.5
// (AAA) 与 Apple HIG / Material 的 44px 触控下限。
//
// 二选一都会错:
//   - **把所有控件拉到 44** —— 桌面端立刻变成"老年机模式": 一行工具栏放不下
//     原来的一半命令, 而这正是 S3 刚修掉的观感;
//   - **保持现状什么都不加** —— 手机上每个小按钮都在漏点, 而这种 bug 只在
//     "手指点了没反应"的时候被发现, 研发环境里永远复现不了。
//
// 所以走第三条路: **视觉尺寸不变, 命中区按需扩到 TouchMin**。UIKit 里这叫
// hit target slop (UITableViewCell 的 contentView 与 accessoryView 就是这么
// 拆的), 用户瞄的是"那个图标", 不是"那个 18×18 的矩形"。
//
// ## 三个读取源 (优先级高 → 低)
//
// 与 reduced-motion (motion.go) 完全同构 —— 那里踩过的坑这里不必再踩一遍:
//  1. 显式开关 `SetTouchMode` —— 宿主/测试说了算;
//  2. 环境变量 `GOX_TOUCH_MODE=1` —— CI 与打包后仍可用;
//  3. 平台猜测 —— 移动端 GOOS 默认按"有触控设备"算。
//
// **为什么默认是"移动端 = 触摸"**: 反过来 (默认全关) 的结果是移动端宿主一行
// 代码都不写就永远拿不到触控命中区, 而"缺了它"的表现是隐性漏点、不报错;
// 桌面端因为 GOOS 是 windows/linux/darwin, 自动落到关闭 —— 既有桌面的命中
// 行为**逐像素不变**, 这才是那个必须钉住的兼容面。
//
// ## 为什么扩的是"命中区"而不是"布局尺寸"
//
// 命中区改动只落在 hittest.go 与各组件自绘区的几处 Contains 判定上, 不进
// 固有尺寸 / flex / 剪裁。副作用边界因此是可枚举的: 命中区**允许重叠**
// (按 z 序取后绘制者), 而布局盒子重叠会让一整片位置断言全线漂移。这也是
// 为什么不写成"触摸模式下把行高改成 44" —— 那会让 gallery 截图与几何断言
// 一起红, 而它们红的原因只是"用户在跑测试而不是在用手指"。

import (
	"os"
	"runtime"
	"strings"
	"sync/atomic"
)

// touchModeOverride 是显式开关 (三态: 未设置 / 开 / 关)。与 reduced-motion
// 同一套写法: 两个 atomic.Bool 表达三态, 不引入指针与锁。
var (
	touchModeSet   atomic.Bool
	touchModeValue atomic.Bool
)

// TouchModeEnabled 报告当前是否处于触摸模式。
//
// 组件层不该自己判断操作系统 —— 走这里, 才能被宿主显式开关与环境变量覆盖。
func TouchModeEnabled() bool { return touchModeActive() }

// SetTouchMode 显式设置触摸模式 (优先级最高)。
//
// 传 nil 表示清除显式设置, 回落到环境变量 / 平台猜测。典型调用方:
//   - 移动端宿主在确认设备只有触屏后 SetTouchMode(&on);
//   - 接了外接鼠标的平板 / Android 桌面模式切成 false;
//   - 桌面应用自己也想要放大命中区 (无障碍偏好)。
func SetTouchMode(on *bool) {
	if on == nil {
		touchModeSet.Store(false)
		touchModeValue.Store(false)
		return
	}
	touchModeValue.Store(*on)
	touchModeSet.Store(true)
}

// ResetTouchModeForTest 清掉显式开关 (用例之间不许串味)。
func ResetTouchModeForTest() {
	touchModeSet.Store(false)
	touchModeValue.Store(false)
}

// touchModeActive 是触摸模式的单一读取点。
func touchModeActive() bool {
	if touchModeSet.Load() {
		return touchModeValue.Load()
	}
	if envTouchMode() {
		return true
	}
	return guessTouchPlatform()
}

// envTouchMode 读 GOX_TOUCH_MODE (接受 1/true/yes/on, 大小写不敏感)。
func envTouchMode() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("GOX_TOUCH_MODE"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// guessTouchPlatform 是"没有显式声明时的缺省猜测": 移动端 GOOS 按触控算。
//
// 复用 gx/device 的同一条判据 (isMobilePlatform + normalizePlatform), 而不是
// 另起一套平台词表 —— 两处判据打架时最难受的是应用作者: 它会在
// getSystemInfo().platform 与命中行为之间看到不一致。
//
// 桌面 (windows/linux/darwin) 一律 false: 保持既有的桌面命中行为不变。
func guessTouchPlatform() bool {
	return isMobilePlatform(normalizePlatform(runtime.GOOS))
}

// ===== 命中区几何 =====

// TouchHitRect 把 r 补到触控下限 (物理像素)。触摸模式未开启时原样返回。
//
// 以盒子的**中心**外扩而不是从左上角长: 控件画在哪, 命中区就围在哪。
func TouchHitRect(r Rect) Rect {
	if !touchModeActive() {
		return r
	}
	return TouchInflate(r, true, true)
}

// touchHitRectRow 是"只补垂直方向"的版本 (给整行内部的节点用)。
//
// 行本来就是撑满容器宽的, 横向再扩没意义 (外面通常就是同一层的另一个容器);
// 而 32px 的行在手机上垂直方向恰恰是最容易被点空的那一维。
func touchHitRectRow(r Rect) Rect {
	if !touchModeActive() {
		return r
	}
	return TouchInflate(r, false, true)
}

// TouchInflate 把矩形以中心为原点外扩到至少 TouchMin 见方。
//
// horiz / vert 分别决定是否补对应轴 —— 给"只想补高度的行"留口子。两轴都不补
// 时返回原矩形。
//
// TouchMin 是物理像素口径 (见 tokens.go), 不经 dpToPx: 再乘缩放会在 3x 屏上
// 得到 132px 的荒谬命中区, 而高清屏上的视觉尺寸本来就已经等比放大过了。
func TouchInflate(r Rect, horiz, vert bool) Rect {
	if r.W <= 0 || r.H <= 0 {
		return r
	}
	out := r
	if horiz && out.W < TouchMin {
		dx := (TouchMin - out.W) / 2
		out.X -= dx
		out.W += 2 * dx
	}
	if vert && out.H < TouchMin {
		dy := (TouchMin - out.H) / 2
		out.Y -= dy
		out.H += 2 * dy
	}
	return out
}

// touchHitContains 是给组件自绘区的统一判据: 触摸模式下用补过的命中区。
//
// 为什么要单独走这里而不是去改 Rect.Contains: 自绘区的 rect 是绘制与命中
// **共用**的一份几何 (tabs 的 tabStrip / pagination 的 pageBtns), 改 Contains
// 等于把绘制也改了 —— 那正是这份实现要避开的改动面。
func touchHitContains(r Rect, x, y int) bool {
	return TouchHitRect(r).Contains(x, y)
}

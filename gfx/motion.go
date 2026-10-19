package gfx

// reduced-motion 读取点（Prism S4）。
//
// 为什么要一个统一入口：S4/S6/S7 三批组件都会引入过渡动画（勾选出场、
// switch 位移、dialog 缩放、toast 入场、skeleton shimmer）。如果每个调用点
// 各自判断环境，必然漏掉一两处，而**漏掉的那一处正是晕动症用户会遇到的**。
// 所以时长一律走 tokens.go 的 MotionDuration()，它内部调这里。
//
// 优先级（高 → 低）：
//  1. 显式开关：SetReducedMotionForTest / 未来的窗口级 prop —— 调用方说了算；
//  2. 环境变量 GOX_REDUCED_MOTION=1 —— 零依赖、CI 可测、打包后仍可用；
//  3. gox.json 的 "ui": {"reducedMotion": true} —— 应用级声明；
//  4. 宿主能力探测 —— 各平台查询系统「减少动态效果」设置。
//
// 为什么不用 prefers-reduced-motion 媒体查询：gfx 没有 CSS 层，也没有
// WebView；「prefers-reduced-motion」在这里只是一个**语义同义词**，实现必须
// 落到 Go 侧能读到的三个来源上。
//
// 线程模型：override 用 atomic.Bool 而非普通变量 —— SetTheme 可能从 VM 线程
// 调用，而绘制在 GUI 线程读；两者同线程是常态，但 atomic 让未来引入后台
// 线程时不必回头改这里（代价是一次有序加载）。

import (
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/14752222/Gox/config"
)

// reducedMotionOverride 是显式开关（最高优先级）。三态：
//   - unset: 未显式设置，走环境变量 / gox.json / 平台探测；
//   - true : 强制开启（动画瞬时）；
//   - false: 强制关闭（忽略环境变量，用于需要在动画上取证的测试）。
//
// 用两个 atomic 表达三态而不引入指针与锁：set 标志 + 值标志。
var (
	reducedMotionSet   atomic.Bool
	reducedMotionValue atomic.Bool
)

// PrefersReducedMotion 报告用户是否要求「减少动态效果」。
//
// 组件不应直接调它（会绕过 token 化），而应通过 MotionDuration() 取时长。
func PrefersReducedMotion() bool {
	return prefersReducedMotion()
}

// prefersReducedMotion 是包内实现（小写，避免在 gfx 包内制造第二个导出名）。
func prefersReducedMotion() bool {
	if reducedMotionSet.Load() {
		return reducedMotionValue.Load()
	}
	if envReducedMotion() {
		return true
	}
	if configReducedMotion() {
		return true
	}
	return platformReducedMotion()
}

// envReducedMotion 读 GOX_REDUCED_MOTION。接受 1/true/yes/on（大小写不敏感），
// 其余值与空串视为未开启 —— 这样 `GOX_REDUCED_MOTION=0 go test` 也能明确
// 表达"别开"，不必与 unset 区分。
func envReducedMotion() bool {
	v := strings.TrimSpace(os.Getenv("GOX_REDUCED_MOTION"))
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// SetReducedMotion 显式设置「减少动态效果」开关（优先级最高）。
//
// 传 nil 表示清除显式设置，回落到环境变量 / 配置 / 平台探测。
// 这是给宿主与测试用的入口：真实应用一般由用户在系统设置里切换，走平台探测。
func SetReducedMotion(on *bool) {
	if on == nil {
		reducedMotionSet.Store(false)
		reducedMotionValue.Store(false)
		return
	}
	reducedMotionValue.Store(*on)
	reducedMotionSet.Store(true)
}

// configReducedMotion 读 gox.json 的 "ui": {"reducedMotion": true}。
//
// 为什么缓存：prefersReducedMotion 会在每帧的每个过渡调用点被问到（动画
// 队列、组件绘制），而 config.Load 是**读磁盘 + JSON 解析**——放进热路径
// 等于每帧读一次文件。配置在进程生命周期内不会变（改了要重启），所以
// sync.Once 足够；测试用 ResetMotionConfigForTest 清缓存。
//
// 读不到 gox.json（没在工程目录里跑、或文件缺失）视为"未声明"，不报错：
// gfx 是库，不该因为 CWD 不在工程根就拒绝渲染。
var (
	motionConfigOnce sync.Once
	motionConfigVal  bool
)

// configReducedMotion 报告 gox.json 是否声明了 reducedMotion。
func configReducedMotion() bool {
	motionConfigOnce.Do(func() {
		if cfg, err := config.Load("."); err == nil {
			motionConfigVal = cfg.UI.ReducedMotion
		}
	})
	return motionConfigVal
}

// ResetMotionConfigForTest 清掉 gox.json 缓存（切换 CWD 的用例用）。
func ResetMotionConfigForTest() {
	motionConfigOnce = sync.Once{}
	motionConfigVal = false
}

// platformReducedMotion 是宿主能力探测的落点。
//
// 当前各平台后端都还没有对应的系统设置读取（Android 的
// Settings.Global.ANIMATOR_DURATION_SCALE、iOS 的
// UIAccessibilityIsReduceMotionEnabled、Linux 的 gsettings
// org.gnome.desktop.interface enable-animations），因此保守返回 false ——
// 宁可"默认不做降级"（视觉与历史一致），也不要"误判成降级"（用户以为
// 动画坏了）。接入点留在这里：后端实现 displayProvider 那样的小接口后，
// 在下面加一个 switch 即可。
func platformReducedMotion() bool {
	// TODO(S8): 接入各平台后端的 reduced-motion 能力查询。
	return false
}

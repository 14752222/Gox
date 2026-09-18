package object

// 接线作用域 (wiring scope): gfx 与 gx/solid 之间的生命周期桥。
//
// 问题: 组件是 parser 层的直接函数调用, 组件体内调用 createEffect 建立
// 副作用时, solid 只能看到"一个游离的 effect" —— 它不知道这段代码属于
// 哪棵子树, 于是条件渲染换页时没人去 dispose 它 (泄漏 + 对着离树节点写回)。
//
// 解法: gfx 在求值"响应式子节点"(wireReactiveChild 的 getter, 也就是
// 条件/列表渲染那层) 期间压入一个 WiringScope; 组件体内调用的
// onCleanup(fn)/onMount(fn) (gx/solid 提供) 查看栈顶, 把回调登记到当前
// 作用域。getter 求值完成后 gfx 取走登记表: Mounts 立即执行 (子树已挂上),
// Cleanups 存到 slot 上, 下一次重建 (clearSlot 之前) 或整树销毁时执行。
//
// 为什么放在 object 包: stdlib (solid) 不能 import gfx, gfx 也不能 import
// stdlib; 两者共同的底层就是 object (与内置模块注册表 builtin_module.go
// 同一个道理 —— 那里的注释解释了这层放置的由来)。
//
// 单线程引擎, 无锁 (与 solidStack 同一纪律)。

// WiringScope 是一次响应式子树构建期间的生命周期登记表。
type WiringScope struct {
	// Cleanups 在"这代子树"被替换或销毁时逐个调用 (后注册先执行, 与
	// 栈式解构的直觉一致)。
	Cleanups []Value
	// Mounts 在本次构建完成 (子树已挂到 slot 上) 后立即逐个调用。
	Mounts []Value
}

var wiringStack []*WiringScope

// PushWiringScope 压入一个新的接线作用域并返回它。
func PushWiringScope() *WiringScope {
	sc := &WiringScope{}
	wiringStack = append(wiringStack, sc)
	return sc
}

// PopWiringScope 弹出栈顶作用域 (调用方随后自行消费其中的登记)。
func PopWiringScope() {
	if n := len(wiringStack); n > 0 {
		wiringStack = wiringStack[:n-1]
	}
}

// CurrentWiringScope 返回当前接线作用域; 不在接线期 (如顶层脚本直接调用
// onCleanup) 返回 nil, 由调用方决定警告或忽略。
func CurrentWiringScope() *WiringScope {
	if n := len(wiringStack); n > 0 {
		return wiringStack[n-1]
	}
	return nil
}

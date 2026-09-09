package stdlib

import (
	"math"
	"sync"
	"time"

	"js-runtime/object"
	"js-runtime/runtime"
)

// 严格定时器全局函数:
//
//   - setStrictTimeout(fn, delay) → id           一次性, 独立调度线程按绝对时间轴触发
//   - setStrictInterval(fn, interval[, mode]) → id  重复, 绝对时间轴, 间隔不漂移
//   - clearStrictTimeout(id) / clearStrictInterval(id)
//   - setStrictIntervalMode("queue" | "interrupt") → 设置后续 interval 的默认模式
//
// 到期信号由独立 Go 调度线程分派, 回调体经 VM 事件循环在 JS 主线程中执行。
// 回调接收一个 info 参数:
//
//	info.scheduledTime  目标触发时刻 (绝对时间轴), 毫秒
//	info.dueTime        实际派发时刻, 毫秒
//	info.early          提前到达毫秒数 (通常 0)
//	info.late           迟到毫秒数 (通常 0)
//	info.skipped        因回调耗时被吞并的中间触发次数
func setupStrictTimers(env *runtime.Environment) {
	scheduler := object.GlobalStrictScheduler()

	// setStrictTimeout(fn, delay)
	env.Declare("setStrictTimeout", object.NewBuiltin("setStrictTimeout", func(args ...object.Value) object.Value {
		if len(args) < 1 || !object.IsCallable(args[0]) {
			return object.NewErrorWithName("TypeError", "setStrictTimeout: first argument must be a function")
		}
		delay := durationFromArg(args, 1)
		id := scheduler.SetStrictTimeout(args[0], delay)
		return object.NewNumber(float64(id))
	}), false)

	// setStrictInterval(fn, interval[, mode])
	env.Declare("setStrictInterval", object.NewBuiltin("setStrictInterval", func(args ...object.Value) object.Value {
		if len(args) < 1 || !object.IsCallable(args[0]) {
			return object.NewErrorWithName("TypeError", "setStrictInterval: first argument must be a function")
		}
		interval := strictIntervalFromArg(args, 1)
		mode := defaultStrictMode()
		if len(args) > 2 {
			if s, ok := args[2].(*object.String); ok {
				mode = strictModeFromString(s.Value)
			}
		}
		id := scheduler.SetStrictInterval(args[0], interval, mode)
		return object.NewNumber(float64(id))
	}), false)

	// clearStrictTimeout(id) / clearStrictInterval(id) — 语义相同, 共用调度器
	clearFn := func(args ...object.Value) object.Value {
		if len(args) > 0 {
			scheduler.Clear(int(toInt(args[0])))
		}
		return object.UndefinedSingleton
	}
	env.Declare("clearStrictTimeout", object.NewBuiltin("clearStrictTimeout", clearFn), false)
	env.Declare("clearStrictInterval", object.NewBuiltin("clearStrictInterval", clearFn), false)

	// setStrictIntervalMode("queue" | "interrupt")
	env.Declare("setStrictIntervalMode", object.NewBuiltin("setStrictIntervalMode", func(args ...object.Value) object.Value {
		mode := object.StrictModeQueue
		if len(args) > 0 {
			if s, ok := args[0].(*object.String); ok {
				mode = strictModeFromString(s.Value)
			}
		}
		setDefaultStrictMode(mode)
		return object.UndefinedSingleton
	}), false)
}

// maxDurationMs 是延时的上限 (约 31 年)。
// 超过后 float64 → time.Duration 的转换会溢出，溢出结果在 Go 中是未定义的。
const maxDurationMs = 1e12

// durationFromArg 将第 idx 个参数解析为毫秒延时。
//
// 必须显式处理 NaN 与负数: 直接 time.Duration(float64) 转换 NaN 时结果
// 未定义 (通常得到一个极大的负 Duration)，会让定时器立即触发或行为异常。
func durationFromArg(args []object.Value, idx int) time.Duration {
	if idx >= len(args) {
		return 0
	}
	ms := toFloat(args[idx])
	if math.IsNaN(ms) || ms <= 0 {
		return 0
	}
	if ms > maxDurationMs {
		ms = maxDurationMs
	}
	return time.Duration(ms * float64(time.Millisecond))
}

// minStrictInterval 是严格 interval 的最小间隔。
//
// 间隔为 0 时调度线程会陷入 time.After(0) 的忙循环: 每次触发后
// when = Start + k*0 恒在过去，循环无休止地重新计算并灌满分派队列
// (CPU 100%)。因此这里钳到 1ms，与浏览器对 setInterval(fn, 0) 的处理一致。
const minStrictInterval = time.Millisecond

// strictIntervalFromArg 解析严格 interval 的间隔 (不允许 <= 0)。
func strictIntervalFromArg(args []object.Value, idx int) time.Duration {
	d := durationFromArg(args, idx)
	if d <= 0 {
		return minStrictInterval
	}
	return d
}

// strictModeFromString 将字符串解析为派发模式; 未知值回退到 queue。
func strictModeFromString(s string) object.StrictIntervalMode {
	switch s {
	case "interrupt":
		return object.StrictModeInterrupt
	case "queue":
		return object.StrictModeQueue
	}
	return object.StrictModeQueue
}

var (
	strictModeMu     sync.RWMutex
	strictModeGlobal = object.StrictModeQueue
)

// defaultStrictMode 返回当前默认派发模式。
func defaultStrictMode() object.StrictIntervalMode {
	strictModeMu.RLock()
	defer strictModeMu.RUnlock()
	return strictModeGlobal
}

// setDefaultStrictMode 设置默认派发模式。
func setDefaultStrictMode(m object.StrictIntervalMode) {
	strictModeMu.Lock()
	defer strictModeMu.Unlock()
	strictModeGlobal = m
}

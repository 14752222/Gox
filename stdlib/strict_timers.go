package stdlib

import (
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
		delay := time.Duration(0)
		if len(args) > 1 {
			delay = time.Duration(toFloat(args[1]) * float64(time.Millisecond))
		}
		id := scheduler.SetStrictTimeout(args[0], delay)
		return object.NewNumber(float64(id))
	}), false)

	// setStrictInterval(fn, interval[, mode])
	env.Declare("setStrictInterval", object.NewBuiltin("setStrictInterval", func(args ...object.Value) object.Value {
		if len(args) < 1 || !object.IsCallable(args[0]) {
			return object.NewErrorWithName("TypeError", "setStrictInterval: first argument must be a function")
		}
		interval := time.Duration(0)
		if len(args) > 1 {
			interval = time.Duration(toFloat(args[1]) * float64(time.Millisecond))
		}
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
			id := int(toFloat(args[0]))
			scheduler.Clear(id)
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

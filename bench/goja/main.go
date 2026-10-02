// goja 对比 runner —— 在 goja 引擎里执行 scripts/bench/ 下的同一批脚本。
//
// 目的: 补上仓库里**唯一缺失的纯解释器对照系**。gox 与 node(V8) 的对比已在
// bench-compare.sh 里, 但 V8 有 JIT, gox 是纯解释器 —— 两者差了 ~15 倍并不
// 说明"gox 解释器差", 只说明"解释器 vs JIT"的固有差距。goja 同样是纯解释器
// (无 JIT), 才是 gox 真正该对标的同类。
//
// 口径 (与 bench-compare.sh 严格对齐, 保证三列可比):
//   - 本 runner **只负责把一段脚本跑完**; 计时一律由 harness 在外层用 wall
//     time 完成 (与 gox/node 同源)。runner 自己**不打印耗时**, 避免口径混淆。
//   - startup_idle.js / fib28.js 直接在 goja 里 RunString 即可。
//   - timers_10k.js 依赖 setTimeout —— goja 内核**不提供**宿主定时器, 这里用
//     一个最小事件循环补齐 (见下面 timerQueue)。这是与 gox/node 唯一的
//     口径差异, 已在 docs/performance.md 里如实写明。
//
// 用法:  goxbench-goja <script.js>
//
// 退出码: 0 成功; 1 读取/编译/执行失败。
package main

import (
	"container/heap"
	"fmt"
	"os"
	"time"

	"github.com/dop251/goja"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: goxbench-goja <script.js>")
		os.Exit(2)
	}
	src, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取脚本失败: %v\n", err)
		os.Exit(1)
	}

	rt := goja.New()
	q := &timerQueue{}

	// console.log / console.error: 脚本里用来输出结果; harness 会把 stdout
	// 丢弃 (> /dev/null), 所以这里直接转发到 os.Stdout/Stderr 即可。
	console := rt.NewObject()
	_ = console.Set("log", func(call goja.FunctionCall) goja.Value {
		parts := make([]interface{}, 0, len(call.Arguments))
		for _, a := range call.Arguments {
			parts = append(parts, a.Export())
		}
		fmt.Fprintln(os.Stdout, parts...)
		return goja.Undefined()
	})
	_ = console.Set("error", func(call goja.FunctionCall) goja.Value {
		parts := make([]interface{}, 0, len(call.Arguments))
		for _, a := range call.Arguments {
			parts = append(parts, a.Export())
		}
		fmt.Fprintln(os.Stderr, parts...)
		return goja.Undefined()
	})
	_ = rt.Set("console", console)

	// setTimeout / setInterval / clearTimeout / clearInterval
	_ = rt.Set("setTimeout", func(call goja.FunctionCall) goja.Value {
		return q.schedule(rt, call, false)
	})
	_ = rt.Set("setInterval", func(call goja.FunctionCall) goja.Value {
		return q.schedule(rt, call, true)
	})
	_ = rt.Set("clearTimeout", func(call goja.FunctionCall) goja.Value {
		q.cancel(call.Argument(0).ToInteger())
		return goja.Undefined()
	})
	_ = rt.Set("clearInterval", func(call goja.FunctionCall) goja.Value {
		q.cancel(call.Argument(0).ToInteger())
		return goja.Undefined()
	})

	if _, err := rt.RunString(string(src)); err != nil {
		fmt.Fprintf(os.Stderr, "执行脚本失败: %v\n", err)
		os.Exit(1)
	}

	// 跑最小事件循环: 把所有定时任务按到期时间顺序派发到队列空为止。
	// 这正是 timers_10k.js 需要的语义 (1 万个 setTimeout(fn,0) 全部触发)。
	if err := q.drain(rt); err != nil {
		fmt.Fprintf(os.Stderr, "定时器回调抛错: %v\n", err)
		os.Exit(1)
	}
}

// benchTimer 是一个待派发的定时任务。
type benchTimer struct {
	id       int64
	due      time.Time
	fn       goja.Callable
	delay    time.Duration
	repeat   bool
	index    int // 堆内部索引 (container/heap 要求)
	canceled bool
}

// timerQueue 是 setTimeout/setInterval 的最小实现 (按到期时间的最小堆)。
//
// 为什么自己写而不是引 github.com/dop251/goja_nodejs 的 eventloop:
// 那个模块会额外拉一串依赖 (nodejs 兼容层), 而我们只需要"到期即回调"这一点
// 语义。少一层依赖, 对比 runner 保持最小可审计。
type timerQueue struct {
	timers  timerHeap
	nextID  int64
	cancels map[int64]bool
}

func (q *timerQueue) schedule(rt *goja.Runtime, call goja.FunctionCall, repeat bool) goja.Value {
	fn, ok := goja.AssertFunction(call.Argument(0))
	if !ok {
		return goja.Undefined()
	}
	delayMs := call.Argument(1).ToInteger()
	if delayMs < 0 {
		delayMs = 0
	}
	delay := time.Duration(delayMs) * time.Millisecond
	q.nextID++
	t := &benchTimer{id: q.nextID, due: time.Now().Add(delay), fn: fn, delay: delay, repeat: repeat}
	heap.Push(&q.timers, t)
	return rt.ToValue(t.id)
}

func (q *timerQueue) cancel(id int64) {
	if q.cancels == nil {
		q.cancels = map[int64]bool{}
	}
	q.cancels[id] = true
}

// drain 依到期时间顺序派发全部任务, 直到队列为空 (或只剩被取消项)。
func (q *timerQueue) drain(rt *goja.Runtime) error {
	for q.timers.Len() > 0 {
		t := heap.Pop(&q.timers).(*benchTimer)
		if q.cancels[t.id] {
			delete(q.cancels, t.id)
			continue
		}
		if d := time.Until(t.due); d > 0 {
			time.Sleep(d)
		}
		if _, err := t.fn(goja.Undefined()); err != nil {
			return err
		}
		if t.repeat {
			// 重排到下一个周期; interval<=0 时退化为尽快重跑 (与浏览器
			// "clamp 到最小 ~4ms" 不同, 但 timers_10k.js 只用 setTimeout(0),
			// 不涉及 setInterval 的周期语义)。
			t.due = time.Now().Add(t.delay)
			heap.Push(&q.timers, t)
		}
	}
	return nil
}

// ---- container/heap 的最小堆 (按 due, 相同则按 id 保插入序) ----

type timerHeap []*benchTimer

func (h timerHeap) Len() int { return len(h) }
func (h timerHeap) Less(i, j int) bool {
	if h[i].due.Equal(h[j].due) {
		return h[i].id < h[j].id
	}
	return h[i].due.Before(h[j].due)
}
func (h timerHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index, h[j].index = i, j
}
func (h *timerHeap) Push(x interface{}) {
	t := x.(*benchTimer)
	t.index = len(*h)
	*h = append(*h, t)
}
func (h *timerHeap) Pop() interface{} {
	old := *h
	n := len(old)
	t := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return t
}

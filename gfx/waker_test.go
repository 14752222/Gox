package gfx

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
)

// ===== rl65eE: 跨线程 Post 的显式唤醒 =====
//
// 回归目标: Post 的 check-then-sleep 竞态 —— Pump 在睡进 WaitEvents **之前**
// 检查 hasPendingPost(), 另一线程在此之后 Post 的任务看不到 ⇒ 单窗口空闲时
// 以无界预算睡下去, 直到恰好来了平台输入事件才醒。修法是显式唤醒原语
// (gfx.go 的 wakeCh) + 后端的 waker 可选接口。
//
// 这一组用例用假 Surface 把"Pump 会走 waker 路径、并因此能被 Post 立刻
// 叫醒"钉死; 真后端把 wake 并进平台等待集合的部分只能真机验证 (见报告)。

// drainWake 清空全局唤醒通道里的残留 token (用例之间不许串味 —— wakeCh 是
// 包级全局状态, 上一条用例留下的 token 会让下一条"立刻返回", 断言就失去意义)。
func drainWake() {
	for {
		select {
		case <-wakeCh:
		default:
			return
		}
	}
}

// wakerSurface 是"只多实现 waker 可选能力"的假 Surface: 它的
// WaitEventsWake 直接 select 在 Post 的唤醒通道上。它**故意**只在 wake
// 上醒, 于是"Pump 是否走了 waker 路径"就是可观测事实。
type wakerSurface struct {
	*fakeSurface
	usedWake atomic.Bool
}

func (w *wakerSurface) WaitEventsWake(maxWait time.Duration, wake <-chan struct{}) bool {
	w.usedWake.Store(true)
	// 安全网: 正常路径一定由 wake 打断; 这个上限只为"实现坏了"时不挂死测试。
	// maxWait<=0 (无限期) 时给一个远大于断言阈值、又远小于 fakeSurface 兜底
	// (10s) 的值 —— 走错路径会在这里超时, 断言 elapsed 时被抓住。
	wait := 20 * time.Second
	var timer <-chan time.Time
	t := time.NewTimer(wait)
	defer t.Stop()
	timer = t.C
	select {
	case <-wake:
		return true
	case <-timer:
		return true
	}
}

type wakerFactory struct{ s *wakerSurface }

func (f *wakerFactory) Create(WindowConfig) (Surface, error) { return f.s, nil }

// TestOptionalWakerNotRequired 锁"可选能力必须能缺席": 假 Surface 与
// bareSurface 都不能实现 waker (否则"未实现则降级"这条路径根本测不到,
// 而真后端里任何一个漏实现都会被静默掩盖)。
func TestOptionalWakerNotRequired(t *testing.T) {
	var bare Surface = &bareSurface{}
	if _, ok := bare.(waker); ok {
		t.Fatal("bareSurface 不该实现 waker (可选能力必须能缺席)")
	}
	var fake Surface = newFakeSurface()
	if _, ok := fake.(waker); ok {
		t.Fatal("fakeSurface 不该实现 waker")
	}
}

// TestPostSignalsWake 验证 Post 确实写了唤醒 token, 且重复 Post 去重。
func TestPostSignalsWake(t *testing.T) {
	drainWake()
	t.Cleanup(func() {
		DrainTasks()
		drainWake()
	})

	Post(func() {})
	select {
	case <-wakeCh:
	default:
		t.Fatal("Post 应向唤醒通道写一个 token")
	}

	// 去重: 缓冲 1, 连着两次 Post 在没人消费时只留一个 token。
	Post(func() {})
	Post(func() {})
	n := 0
	for {
		select {
		case <-wakeCh:
			n++
			continue
		default:
		}
		break
	}
	if n != 1 {
		t.Fatalf("唤醒 token 应去重为 1 个, got %d", n)
	}
}

// TestPumpWakesOnCrossThreadPost 是本次修复的核心回归: 单窗口空闲 (无平台
// 事件、无定时器) 时, 另一 goroutine 的 Post 必须能让下一次 Pump 在预算内
// 醒来并执行任务 —— 而不是睡到恰好来了输入事件。
//
// 失败口径: 若 Pump 没走 waker 路径, 它会退回 fakeSurface.WaitEvents(0)
// (兜底 10s), 断言 elapsed < 2s 立刻抓住; usedWake 也会是 false。
func TestPumpWakesOnCrossThreadPost(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })
	drainWake()

	ws := &wakerSurface{fakeSurface: newFakeSurface()}
	SetDefaultFactory(&wakerFactory{ws})
	t.Cleanup(func() {
		SetDefaultFactory(nil)
		DrainTasks()
		drainWake()
	})

	root := &GuiNode{Tag: "column", Props: map[string]object.Value{}}
	if _, err := Mount(root, WindowConfig{Title: "waker", Width: 200, Height: 150}); err != nil {
		t.Fatalf("Mount: %v", err)
	}

	ran := make(chan struct{})
	go func() {
		// 等 Pump 一定已经睡进 WaitEventsWake 再 Post —— 这才复现
		// "检查发生在 Post 之前"的竞态窗口。
		time.Sleep(60 * time.Millisecond)
		Post(func() { close(ran) })
	}()

	start := time.Now()
	if !Pump(0) {
		t.Fatal("Pump 意外返回 false (窗口被判定为关闭)")
	}
	elapsed := time.Since(start)

	if !ws.usedWake.Load() {
		t.Fatal("Pump 没有走 waker 路径 (WaitEventsWake 未被调用)")
	}
	select {
	case <-ran:
	default:
		t.Fatal("跨线程 Post 的任务没有被本轮 Pump 执行")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("Pump 没有在唤醒预算内醒来, 耗时 %v (疑似退回 WaitEvents 的无界睡)", elapsed)
	}
}

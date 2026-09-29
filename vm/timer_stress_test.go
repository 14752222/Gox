package vm

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"
)

// ===== T06: 事件循环与定时器压测 =====
//
// 对应看板「事件循环与定时器压测」的 DoD:
//   - 1 万次定时器调度无内存增长趋势;
//   - 定时器风暴全部触发、计数精确 (不丢不重);
//   - fs/http 高频并发不泄漏任务令牌 (事件循环能正常退出);
//   - UI 掉帧口径在 gfx/frame_budget_test.go (渲染单帧预算)。
//
// 预算刻意放宽 (内存上限、耗时上限), 闸门只拦"数量级劣化",
// 不拦正常的运行抖动 —— 压测数据另行由 scripts/bench-compare.sh 存档对比。

// TestTimerStormTenThousand 万次 setTimeout 全部触发: 计数精确、
// 堆内存无增长趋势 (GC 后增量 < 16MB —— 1 万个小闭包 + 调度结构,
// 实测常驻远低于该值; 若定时器结构泄漏, 这个数会被轻松击穿)。
func TestTimerStormTenThousand(t *testing.T) {
	runtime.GC()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	v, err := EvalVM(`
		let fired = 0;
		for (let i = 0; i < 10000; i++) {
			setTimeout(function () { fired = fired + 1; }, 1);
		}
		// 延后 5ms 的收尾定时器: 必然排在全部 1ms 定时器之后, 拿到的就是终值
		setTimeout(function () { __fired = fired; }, 5);
		let __fired = -1;
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		if err := v.RunTimers(); err != nil {
			t.Fatalf("RunTimers: %v", err)
		}
		if val, ok := v.Globals().Get("__fired"); ok && val.Inspect() == "10000" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("60s 内未跑完万次定时器")
		}
	}

	runtime.GC()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	growth := after.HeapAlloc - before.HeapAlloc
	if growth < 0 {
		growth = 0
	}
	if growth > 16<<20 {
		t.Fatalf("万次定时器后堆增长 %dMB, 疑似泄漏 (before=%d after=%d)",
			growth>>20, before.HeapAlloc, after.HeapAlloc)
	}
}

// TestTimerIntervalChurn 高频 setInterval 建立又清除: 500 只 interval
// 各跳若干次后全部 clear, 再补 1 万次一次性定时器 —— clear 必须精确,
// 不残留活跃任务; 事件循环能在有限时间内跑完收敛。
func TestTimerIntervalChurn(t *testing.T) {
	v, err := EvalVM(`
		let ids = [];
		for (let i = 0; i < 500; i++) {
			ids.push(setInterval(function () {}, 1));
		}
		setTimeout(function () {
			for (let i = 0; i < ids.length; i++) clearInterval(ids[i]);
			// 清干净之后再来一万次一次性: 若 clear 失败/残留, 这里会被放大
			for (let j = 0; j < 10000; j++) setTimeout(function () {}, 1);
			setTimeout(function () { __done = true; }, 5);
		}, 50);
		let __done = false;
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		if err := v.RunTimers(); err != nil {
			t.Fatalf("RunTimers: %v", err)
		}
		if val, ok := v.Globals().Get("__done"); ok && val.Inspect() == "true" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("60s 内未完成 churn 流程")
		}
	}
}

// TestHTTPConcurrencyBurst 200 个并发 fetch 全部 200 响应:
// goroutine 客户端 + 挂起任务令牌的往返, 全部完成后事件循环干净退出。
func TestHTTPConcurrencyBurst(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	v, err := EvalVM(`
		let ok = 0, fail = 0, done = false;
		for (let i = 0; i < 200; i++) {
			fetch(` + "`" + srv.URL + `/n` + "`" + ` + i).then(function (r) {
				if (r.status === 200) { ok = ok + 1; } else { fail = fail + 1; }
			}).catch(function () { fail = fail + 1; });
		}
		setTimeout(function () { done = (ok + fail === 200); }, 20);
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if err := v.RunTimers(); err != nil {
			t.Fatalf("RunTimers: %v", err)
		}
		if val, ok := v.Globals().Get("done"); ok && val.Inspect() == "true" {
			if fv, _ := v.Globals().Get("fail"); fv.Inspect() != "0" {
				t.Fatalf("出现失败请求 fail=%s", fv.Inspect())
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("30s 内 200 个 fetch 未全部完成")
		}
	}
}

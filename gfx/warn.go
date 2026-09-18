package gfx

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// 警告环形缓冲 (devtools 公共基础设施 3, 2026-09-19 拍板落地)。
//
// 内核有三处警告出口 (未知标签 / 图片加载失败 / 事件回调异常), 过去只打
// stderr —— 打完就没了, "图为什么是占位框"这类问题没有现场可查。这里给
// 三处出口一个公共汇聚点: 照常打 stderr, 同时留最近 N 条 (带时间戳) 供
// `gx/dev` 的 devSnapshot() 读取。去重仍由各出口自己的 once 机制负责
// (warnUnknownTagOnce / warnImageLoadOnce), 环形缓冲只管留存。
//
// 只增不减 + 单线程 GUI 访问, 锁仅为与测试并发断言时的防御 (与 imageLRU
// 同一纪律)。

// warnRingCap 是留存条数。64 条足够覆盖"打开应用到现在"的全部不同警告
// (每类警告经 once 去重后通常只有一两条), 又不至于在刷屏型警告下吃内存。
const warnRingCap = 64

// warnEntry 是一条留存的警告。
type warnEntry struct {
	At   time.Time
	Text string
}

var (
	warnRingMu sync.Mutex
	warnRing   []warnEntry
)

// recordWarn 记一条警告 (format+args 经 fmt.Sprintf) 并打到 stderr。
// 文本本身不再带 "gfx: " 前缀参数化 —— 统一在这里加, 保证 stderr 与快照
// 里的内容逐字一致。
func recordWarn(format string, args ...interface{}) {
	text := fmt.Sprintf(format, args...)
	warnRingMu.Lock()
	warnRing = append(warnRing, warnEntry{At: time.Now(), Text: text})
	if len(warnRing) > warnRingCap {
		warnRing = warnRing[len(warnRing)-warnRingCap:]
	}
	warnRingMu.Unlock()
	os.Stderr.WriteString("gfx: " + text + "\n")
}

// warnSnapshot 取缓冲的副本 (gx/dev 读取入口)。
func warnSnapshot() []warnEntry {
	warnRingMu.Lock()
	defer warnRingMu.Unlock()
	out := make([]warnEntry, len(warnRing))
	copy(out, warnRing)
	return out
}

// resetWarnRing 清空缓冲 (仅测试用)。
func resetWarnRing() {
	warnRingMu.Lock()
	warnRing = nil
	warnRingMu.Unlock()
}

// warnEventError 是事件回调异常的出口 (render.go 的 callHandlerValue 用)。
// 做成变量与 warnUnknownTag / warnImageLoad 同款, 便于单测替换为计数器。
var warnEventError = func(name string, err error) {
	recordWarn("%s error: %v", name, err)
}

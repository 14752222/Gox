package object

import (
	"sync"
	"time"
)

// console 环形缓冲 (devtools [M3] 留存, 2026-10-02 落地)。
//
// 为什么放在 object: console 的实现在 stdlib, 而 devtools 面板 (gx/dev) 在
// gfx; 两边都要能读写同一份留存, 又不能互相 import (stdlib ↔ gfx 无依赖,
// 硬连会牵出循环)。object 是两者共同的叶子包, 把缓冲放这里最省事 ——
// 与 builtin_module.go 用 object 做注册中继是同一条依赖反转思路。
//
// 行为边界 (写清楚免得被当 bug):
//   - **只留存, 不改写流**: console.log/warn/... 照旧写 stdout/stderr,
//     这里只是**事后**补一份副本 (见 stdlib/console.go)。所以"清屏/重定向
//     stdout"不会清掉这里的记录, 反之亦然。
//   - **固定容量**: 只保留最近 devLogCap 条, 更早的被挤掉 (环形语义用
//     "append + 从头裁剪"实现: 容量小、写入频率是人眼级, 不值得真做环形下标)。
//   - **单线程为主**: JS 执行是单线程的, 锁只为"测试 goroutine 并发断言"
//     兜底 (与 gfx/warn.go 的警告环形缓冲同一纪律)。
//   - 生产零成本: 不注册/不读取时这里只是一次 append, 没有别的开销。

// devLogCap 是留存条数。500 条足够覆盖"打开面板往回翻一段"的量级, 又不至于
// 被刷屏型 console.log 撑爆内存 (每条只是一个 string + 时间戳)。
const devLogCap = 500

// DevLogEntry 是一条留存的 console 输出。
type DevLogEntry struct {
	Level string    // "log" / "info" / "warn" / "error"
	Text  string    // 已拼接好的文本 (与写向 stdout/stderr 的内容逐字一致)
	At    time.Time // 记录时刻
}

var (
	devLogMu  sync.Mutex
	devLogBuf []DevLogEntry
)

// RecordDevLog 追加一条留存。它**不会失败、不会 panic、不返回错误** ——
// 调用点在 console 的写流路径上, 任何异常都不允许影响原有 console 行为。
func RecordDevLog(level, text string) {
	devLogMu.Lock()
	devLogBuf = append(devLogBuf, DevLogEntry{Level: level, Text: text, At: time.Now()})
	if len(devLogBuf) > devLogCap {
		// 从头裁剪: 保留最近 devLogCap 条。
		devLogBuf = devLogBuf[len(devLogBuf)-devLogCap:]
	}
	devLogMu.Unlock()
}

// DevLogSnapshot 取最近日志的副本。
//
//	level == "" 表示不过滤; 否则只留 Level 完全相等的那一档。
//	limit <= 0 表示全部; 否则最多返回**最近** limit 条 (顺序仍是旧→新)。
//
// 返回的是副本, 调用方随意改不影响缓冲。
func DevLogSnapshot(level string, limit int) []DevLogEntry {
	devLogMu.Lock()
	defer devLogMu.Unlock()

	var out []DevLogEntry
	if level == "" {
		out = make([]DevLogEntry, len(devLogBuf))
		copy(out, devLogBuf)
	} else {
		for _, e := range devLogBuf {
			if e.Level == level {
				out = append(out, e)
			}
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out
}

// DevLogCap 返回缓冲容量 (测试/文档用)。
func DevLogCap() int { return devLogCap }

// ResetDevLog 清空缓冲 (测试隔离用: 缓冲是包级单例, 用例之间不许串味)。
func ResetDevLog() {
	devLogMu.Lock()
	devLogBuf = nil
	devLogMu.Unlock()
}

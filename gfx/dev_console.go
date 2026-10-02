package gfx

import (
	"github.com/14752222/Gox/object"
)

// gx/dev 的 console 留存出口 (devtools [M3], 2026-10-02 落地)。
//
//	console.log/warn/... → stdout/stderr (原有行为) + object 环形缓冲 (留存)
//	devLogs(level?, limit?) → [{ level, text, at }]
//
// 设计要点:
//   - **只读快照**: 这里只把 object.DevLogSnapshot 的结果转成 JS 数组对象,
//     不做过滤之外的任何加工 (level 过滤与 limit 裁剪在 object 侧完成,
//     两处只保留一份判据)。
//   - **at 用 "15:04:05" 字符串**: 与 devSnapshot 的 warnings.at 同一格式,
//     面板里两列时间戳能对齐; 面板本来也只做展示, 不需要毫秒精度。
//   - 生产零成本: console 侧那次 append 是常数开销; 不调 devLogs 就没有别的。

// jsDevLogs 是 devLogs(level?, limit?) 的实现。
//
//	devLogs()            → 全部留存 (最多 500 条, 旧→新)
//	devLogs("error")     → 只看 error 一档
//	devLogs("", 20)      → 最近 20 条
func jsDevLogs(args ...object.Value) object.Value {
	level := ""
	if len(args) > 0 {
		if s, ok := args[0].(*object.String); ok {
			level = s.Value
		}
	}
	limit := 0
	if len(args) > 1 {
		if n, ok := args[1].(*object.Number); ok && n.Value > 0 {
			limit = int(n.Value)
		}
	}

	entries := object.DevLogSnapshot(level, limit)
	out := make([]object.Value, 0, len(entries))
	for _, e := range entries {
		o := object.NewObject()
		o.SetProperty("level", object.NewString(e.Level))
		o.SetProperty("text", object.NewString(e.Text))
		o.SetProperty("at", object.NewString(e.At.Format("15:04:05")))
		out = append(out, o)
	}
	return object.NewArray(out)
}

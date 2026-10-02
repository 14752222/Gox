package gfx

import (
	"fmt"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== [M3] Inspector v1: console 留存 (object 环形缓冲 + gx/dev 的 devLogs) =====

// TestDevLogRingCapAndTrim 锁住容量与裁剪语义。
func TestDevLogRingCapAndTrim(t *testing.T) {
	object.ResetDevLog()
	t.Cleanup(object.ResetDevLog)

	total := object.DevLogCap() + 100
	for i := 0; i < total; i++ {
		object.RecordDevLog("log", fmt.Sprintf("m%d", i))
	}
	got := object.DevLogSnapshot("", 0)
	if len(got) != object.DevLogCap() {
		t.Fatalf("快照条数 = %d, want %d (容量)", len(got), object.DevLogCap())
	}
	// 最早应是被挤到最前面的那条: 第 (total-cap) 条
	wantFirst := fmt.Sprintf("m%d", total-object.DevLogCap())
	if got[0].Text != wantFirst {
		t.Fatalf("裁剪后首条 = %q, want %q", got[0].Text, wantFirst)
	}
	if got[len(got)-1].Text != fmt.Sprintf("m%d", total-1) {
		t.Fatalf("末条应为最近写入的那条, 得到 %q", got[len(got)-1].Text)
	}
	if got[0].At.IsZero() {
		t.Fatalf("条目应带时间戳")
	}
}

// TestDevLogLevelFilterAndLimit 锁住 level 过滤与 limit 裁剪 (顺序仍为旧→新)。
func TestDevLogLevelFilterAndLimit(t *testing.T) {
	object.ResetDevLog()
	t.Cleanup(object.ResetDevLog)

	object.RecordDevLog("log", "l1")
	object.RecordDevLog("warn", "w1")
	object.RecordDevLog("error", "e1")
	object.RecordDevLog("warn", "w2")
	object.RecordDevLog("warn", "w3")

	warns := object.DevLogSnapshot("warn", 0)
	if len(warns) != 3 {
		t.Fatalf("warn 档 = %d 条, want 3", len(warns))
	}
	for _, e := range warns {
		if e.Level != "warn" {
			t.Fatalf("过滤后混入 %q 档: %s", e.Level, e.Text)
		}
	}
	if warns[0].Text != "w1" || warns[2].Text != "w3" {
		t.Fatalf("顺序应旧→新: %v", []string{warns[0].Text, warns[2].Text})
	}

	// limit 取最近 N 条
	last2 := object.DevLogSnapshot("warn", 2)
	if len(last2) != 2 || last2[0].Text != "w2" || last2[1].Text != "w3" {
		t.Fatalf("limit=2 应取最近两条 w2/w3, 得到 %v", last2)
	}

	// 全部 + limit
	all := object.DevLogSnapshot("", 3)
	if len(all) != 3 || all[2].Text != "w3" {
		t.Fatalf("全部档 limit=3 应得最后三条, 得到 %d 条", len(all))
	}
}

// TestDevLogsBuiltin 端到端: console.* 经真实 VM 写流后被 devLogs 读到。
func TestDevLogsBuiltin(t *testing.T) {
	object.ResetDevLog()
	t.Cleanup(object.ResetDevLog)

	v, _ := evalUI(t, `
		import { devLogs } from "gx/dev";
		import { h, render } from "gx/gfx";
		render(h("column", null, h("text", null, "x")));
		console.log("hello", 1);
		console.info("info-line");
		console.warn("careful");
		console.error("boom");
		globalThis.g_all = devLogs();
		globalThis.g_warn = devLogs("warn");
		globalThis.g_tail = devLogs("", 2);
	`)

	all := globalVal(t, v, "g_all").(*object.Array)
	if len(all.Elements) != 4 {
		t.Fatalf("devLogs() 条数 = %d, want 4", len(all.Elements))
	}
	first := asObj(t, all.Elements[0], "日志[0]")
	if propStr(t, first, "level") != "log" || propStr(t, first, "text") != "hello 1" {
		t.Fatalf("log 条目不对: %s", first.Inspect())
	}
	if propStr(t, first, "at") == "" {
		t.Fatalf("日志条目应带时间戳 at")
	}

	warn := globalVal(t, v, "g_warn").(*object.Array)
	if len(warn.Elements) != 1 || propStr(t, asObj(t, warn.Elements[0], "warn[0]"), "text") != "careful" {
		t.Fatalf("devLogs(\"warn\") 不对: %v", warn.Elements)
	}

	tail := globalVal(t, v, "g_tail").(*object.Array)
	if len(tail.Elements) != 2 {
		t.Fatalf("devLogs(\"\", 2) 条数 = %d, want 2", len(tail.Elements))
	}
	if propStr(t, asObj(t, tail.Elements[1], "tail[1]"), "text") != "boom" {
		t.Fatalf("最近一条应为 error 的 boom")
	}
}

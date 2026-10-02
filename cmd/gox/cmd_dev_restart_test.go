package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestDevRestarterKillAndStart 验证进程级热重启的基本契约: 每次 start 都换一个
// 新子进程 PID, 且启动开销足够小 (配合防抖仍远低于 1s 目标)。
func TestDevRestarterKillAndStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("测试用 sleep 子进程, Windows 上跳过")
	}
	orig := devSpawn
	devSpawn = func(entry string) (*exec.Cmd, error) {
		return exec.Command("sleep", "5"), nil
	}
	defer func() { devSpawn = orig }()

	r := &devRestarter{entry: "x.ts"}
	d1, err := r.start()
	if err != nil {
		t.Fatalf("第一次 start: %v", err)
	}
	pid1 := r.child.Process.Pid
	d2, err := r.start()
	if err != nil {
		t.Fatalf("第二次 start: %v", err)
	}
	pid2 := r.child.Process.Pid
	if pid1 == pid2 {
		t.Errorf("重启后 PID 未变 (%d) —— 旧进程没被杀掉", pid1)
	}
	r.kill()
	if r.child != nil {
		t.Error("kill 后 child 应清空")
	}
	for i, d := range []time.Duration{d1, d2} {
		t.Logf("restart #%d: 杀旧+起新 = %s; + 防抖(%s) = %s",
			i+1, d.Round(time.Millisecond), devDebounce, (d + devDebounce).Round(time.Millisecond))
		if devDebounce+d >= time.Second {
			t.Errorf("重启延迟 %s + 防抖 %s 超出 1s 目标", d.Round(time.Millisecond), devDebounce)
		}
	}
}

// TestIsGUIProject 判据: 入口向上能找到 gox.json 即视为 GUI 工程。
func TestIsGUIProject(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(src, "main.tsx")
	if err := os.WriteFile(entry, []byte("// x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if isGUIProject(entry) {
		t.Error("没有 gox.json 时不应判为 GUI 工程")
	}
	if err := os.WriteFile(filepath.Join(dir, "gox.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !isGUIProject(entry) {
		t.Error("上级目录有 gox.json 时应判为 GUI 工程")
	}
}

// TestBuildFindEntryTS cmd_build 的入口探测: main.js 优先, TS 工程回落。
func TestBuildFindEntryTS(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeEntry := func(name string) {
		if err := os.WriteFile(filepath.Join(dir, "src", name), []byte("// x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 只有 main.tsx → 命中它
	writeEntry("main.tsx")
	got, err := buildFindEntry(dir)
	if err != nil || filepath.Base(got) != "main.tsx" {
		t.Fatalf("只有 main.tsx 时应命中它, got=%q err=%v", got, err)
	}
	// 再放 main.js → JS 优先 (老工程行为不变)
	writeEntry("main.js")
	got, err = buildFindEntry(dir)
	if err != nil || filepath.Base(got) != "main.js" {
		t.Fatalf("main.js 应优先, got=%q err=%v", got, err)
	}
	// 空目录 → 报错列出候选
	empty := t.TempDir()
	if _, err := buildFindEntry(empty); err == nil {
		t.Fatal("空目录应报找不到入口")
	}
}

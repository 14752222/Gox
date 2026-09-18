package gfx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// TestStorageDemoScript 跑 testdata/storage_demo.js: 它会真实写存储文件,
// 所以不进 TestExampleScriptsMount 的共享循环 (那里没有环境隔离), 单独立
// 用例并用 GOX_STORAGE_DIR 指到临时目录 —— 与 image/multiwindow 单独立
// 用例是同一个理由 (各有各的前置)。
func TestStorageDemoScript(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GOX_STORAGE_DIR", dir)
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	src, err := os.ReadFile(filepath.Join("..", "testdata", "storage_demo.js"))
	if err != nil {
		t.Fatalf("读取演示脚本: %v", err)
	}
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(string(src))
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	if shots(fake) < 1 {
		t.Fatalf("首帧未上屏")
	}

	// 点两次 "+1 & save" → count=2 落盘
	appMu.Lock()
	root := activeApp.root
	appMu.Unlock()
	btns := findAll(root, "button")
	if len(btns) != 3 {
		t.Fatalf("按钮数量 = %d, want 3", len(btns))
	}
	fake.push(Event{Kind: EventMouseUp, X: btns[0].Box.X + 2, Y: btns[0].Box.Y + 2})
	fake.push(Event{Kind: EventMouseUp, X: btns[0].Box.X + 2, Y: btns[0].Box.Y + 2})
	fake.push(Event{Kind: EventClose})
	if err := v.RunTimersWithPump(Pump); err != nil {
		t.Fatalf("事件循环: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "storage-demo", "storage.json"))
	if err != nil {
		t.Fatalf("storage.json 未落盘: %v", err)
	}
	if s := string(raw); !strings.Contains(s, `"count":2`) || !strings.Contains(s, `"theme":"light"`) {
		t.Fatalf("落盘内容不对: %s", s)
	}
}

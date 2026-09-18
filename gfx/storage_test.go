package gfx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== gx/storage (设备能力 API 方案 A) =====
//
// 存储是进程级状态 (storageData 缓存), 测试之间必须换目录: GOX_STORAGE_DIR
// 指向 t.TempDir()。stdlib 侧每次访问都重读环境变量, 所以 t.Setenv 即时生效。

// storageTestEnv 给一个隔离的存储根目录。
func storageTestEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GOX_STORAGE_DIR", dir)
	return dir
}

func TestStorageRoundTripCore(t *testing.T) {
	storageTestEnv(t)
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(`
		import { setAppName, setStorage, getStorage, removeStorage, getStorageInfo } from "gx/storage";
		setAppName("core");
		setStorage("theme", "dark");
		setStorage("profile", { name: "gox", level: 3, tags: ["a", "b"] });
		globalThis.g_theme = getStorage("theme");
		globalThis.g_level = getStorage("profile").level;
		globalThis.g_tag1 = getStorage("profile").tags[1];
		globalThis.g_missing = getStorage("nope");
		globalThis.g_keys = getStorageInfo().keys.join(",");
		globalThis.g_limit = getStorageInfo().limit;
		globalThis.g_size = getStorageInfo().currentSize > 0;
		removeStorage("theme");
		globalThis.g_removed = getStorage("theme");
		globalThis.g_keys2 = getStorageInfo().keys.join(",");
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	g := v.Globals()
	assertGlobal := func(name, want string) {
		t.Helper()
		val, _ := g.Get(name)
		if val.Inspect() != want {
			t.Fatalf("%s = %v, want %q", name, val.Inspect(), want)
		}
	}
	assertGlobal("g_theme", "dark")
	assertGlobal("g_level", "3")
	assertGlobal("g_tag1", "b")
	assertGlobal("g_keys", "profile,theme") // 排序输出, 确定性
	assertGlobal("g_keys2", "profile")
	assertGlobal("g_removed", "undefined")
	// limit 恒 -1 (v1 不做配额)
	assertGlobal("g_limit", "-1")
	assertGlobal("g_size", "true")
	val, _ := g.Get("g_missing")
	if _, undef := val.(*object.Undefined); !undef {
		t.Fatalf("不存在的键应返回 undefined, 得到 %v", val.Inspect())
	}
}

// TestStoragePersistsAcrossVMs 持久化的意义就在"换个进程还在": 两个独立 VM
// (同一存储根) 先写后读。
func TestStoragePersistsAcrossVMs(t *testing.T) {
	dir := storageTestEnv(t)
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	if _, err := vm.EvalVM(`
		import { setAppName, setStorage } from "gx/storage";
		setAppName("persist");
		setStorage("token", "abc123");
		setStorage("count", 7);
	`); err != nil {
		t.Fatalf("写入 VM: %v", err)
	}
	// 文件确实落盘 (原子替换后的最终名)
	raw, err := os.ReadFile(filepath.Join(dir, "persist", "storage.json"))
	if err != nil {
		t.Fatalf("storage.json 未落盘: %v", err)
	}
	if !strings.Contains(string(raw), `"token":"abc123"`) {
		t.Fatalf("落盘内容不含 token: %s", raw)
	}

	v, err := vm.EvalVM(`
		import { setAppName, getStorage } from "gx/storage";
		setAppName("persist");
		globalThis.g_token = getStorage("token");
		globalThis.g_count = getStorage("count");
	`)
	if err != nil {
		t.Fatalf("读取 VM: %v", err)
	}
	g := v.Globals()
	if val, _ := g.Get("g_token"); val.Inspect() != "abc123" {
		t.Fatalf("跨 VM 读取 token = %v", val.Inspect())
	}
	if val, _ := g.Get("g_count"); val.Inspect() != "7" {
		t.Fatalf("跨 VM 读取 count = %v", val.Inspect())
	}
}

// TestStorageAppDataDirConvention 目录约定是兼容承诺 (拍板 2026-09-18):
// <root>/<appName>, appName 未显式设置时从进程参数推、再落回 "app"。
func TestStorageAppDataDirConvention(t *testing.T) {
	dir := storageTestEnv(t)
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(`
		import { setAppName, appDataDir } from "gx/storage";
		setAppName("My App!2026");
		globalThis.g_dir = appDataDir();
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	val, _ := v.Globals().Get("g_dir")
	s, _ := val.(*object.String)
	if s == nil {
		t.Fatalf("appDataDir 未返回字符串: %v", val.Inspect())
	}
	want := filepath.Join(dir, "MyApp2026") // 非法字符被收敛
	if s.Value != want {
		t.Fatalf("appDataDir = %q, want %q", s.Value, want)
	}
}

//go:build windows

package win32

import (
	"testing"
	"unsafe"

	"github.com/14752222/Gox/gfx"
)

// P3-4 原生对话框的平台侧测试。
//
// **不真的弹框**: 无人值守环境里 MessageBoxW 会挂到超时, 文件对话框还要人去点。
// 这里只验两件**纯数据**的事, 它们恰好是这类 syscall 代码最容易错的地方:
//  1. `OPENFILENAMEW` 的内存布局 (字段偏移与 C 不一致 = 传进 Windows 的结构
//     是错位的, 表现为"对话框弹不出来"或更糟的静默乱读);
//  2. 过滤串的**双 NUL 结尾**格式。

// TestOpenFileNameWLayout 把结构体布局钉死在测试里。
//
// 这些偏移值是 Win64 上 `OPENFILENAMEW` 的真实 ABI。任何人调整字段顺序、
// 误删填充占位、或把 uint32 改成 uintptr, 这里立刻失败 —— 而在实机上
// 这类错误只会表现为"对话框没反应", 极难定位。
func TestOpenFileNameWLayout(t *testing.T) {
	type field struct {
		name string
		got  uintptr
		want uintptr
	}
	checks := []field{
		{"lStructSize", unsafe.Offsetof(openFileNameW{}.StructSize), 0},
		{"hwndOwner", unsafe.Offsetof(openFileNameW{}.HwndOwner), 8},
		{"hInstance", unsafe.Offsetof(openFileNameW{}.HInstance), 16},
		{"lpstrFilter", unsafe.Offsetof(openFileNameW{}.LpstrFilter), 24},
		{"lpstrCustomFilter", unsafe.Offsetof(openFileNameW{}.LpstrCustomFilter), 32},
		{"nMaxCustFilter", unsafe.Offsetof(openFileNameW{}.NMaxCustFilter), 40},
		{"nFilterIndex", unsafe.Offsetof(openFileNameW{}.NFilterIndex), 48},
		{"lpstrFile", unsafe.Offsetof(openFileNameW{}.LpstrFile), 56},
		{"nMaxFile", unsafe.Offsetof(openFileNameW{}.NMaxFile), 64},
		{"lpstrFileTitle", unsafe.Offsetof(openFileNameW{}.LpstrFileTitle), 72},
		{"nMaxFileTitle", unsafe.Offsetof(openFileNameW{}.NMaxFileTitle), 80},
		{"lpstrInitialDir", unsafe.Offsetof(openFileNameW{}.LpstrInitialDir), 88},
		{"lpstrTitle", unsafe.Offsetof(openFileNameW{}.LpstrTitle), 96},
		{"flags", unsafe.Offsetof(openFileNameW{}.Flags), 104},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s 偏移 = %d, want %d (与 Win32 OPENFILENAMEW 的 ABI 不符)", c.name, c.got, c.want)
		}
	}
	// 真实结构体在 Win64 上是 152 字节; 我们多留了几个"用不到但必须占位"
	// 的字段, 所以只要 **不小于** 152 就是安全的 (GetOpenFileNameW 按
	// lStructSize 校验, 开小会踩到后面的内存)。
	size := unsafe.Sizeof(openFileNameW{})
	if size < 152 {
		t.Fatalf("openFileNameW 尺寸 = %d, want >= 152 (结构体开小了会踩内存)", size)
	}
	// 也不能过大到离谱 (那说明填充加错了地方)
	if size > 512 {
		t.Fatalf("openFileNameW 尺寸 = %d 明显过大, 检查是否误加了字段", size)
	}
	// lStructSize 会被实际传给 API, 必须是自己的真实大小
	if uintptr(size)%8 != 0 {
		t.Fatalf("结构体尺寸 %d 未按 8 字节对齐 (Win64 ABI)", size)
	}
}

// TestBuildFilterDoubleNUL 验过滤串格式: `描述\x00通配符\x00...\x00\x00`。
//
// **结尾必须是两个 NUL**: 少一个, Windows 会一直往后读, 表现为
// "类型下拉框里出现乱码"或对话框直接失败。
func TestBuildFilterDoubleNUL(t *testing.T) {
	// 单条
	got := buildFilter([]gfx.NativeFileFilter{{Name: "文本文件", Pattern: "*.txt"}})
	want := "文本文件\x00*.txt\x00\x00"
	if got != want {
		t.Fatalf("单条过滤串 = %q, want %q", got, want)
	}
	// 多条: 每条都是 name\0pattern\0, 最后再补一个 NUL
	got = buildFilter([]gfx.NativeFileFilter{
		{Name: "文本", Pattern: "*.txt"},
		{Name: "图片", Pattern: "*.png;*.jpg"},
	})
	want = "文本\x00*.txt\x00图片\x00*.png;*.jpg\x00\x00"
	if got != want {
		t.Fatalf("多条过滤串 = %q, want %q", got, want)
	}
	// 空列表 → 兜底"所有文件" (不给过滤串时对话框没有类型下拉框)
	got = buildFilter(nil)
	if got != "所有文件\x00*.*\x00\x00" {
		t.Fatalf("空过滤应兜底所有文件, got %q", got)
	}
	// 描述里混进 NUL 必须被剔掉 (否则过滤串被提前截断, 静默丢项)
	got = buildFilter([]gfx.NativeFileFilter{{Name: "坏\x00名", Pattern: "*.t\x00xt"}})
	if got != "坏名\x00*.txt\x00\x00" {
		t.Fatalf("NUL 未被剔除: %q", got)
	}
	// 缺 name 用 pattern 顶替; 缺 pattern 兜底 *.*
	got = buildFilter([]gfx.NativeFileFilter{{Name: "", Pattern: "*.md"}})
	if got != "*.md\x00*.md\x00\x00" {
		t.Fatalf("缺 name 应回填 pattern: %q", got)
	}
	got = buildFilter([]gfx.NativeFileFilter{{Name: "日志"}})
	if got != "日志\x00*.*\x00\x00" {
		t.Fatalf("缺 pattern 应兜底 *.*: %q", got)
	}
}

// TestUTF16PtrNulTerminated 验字符串转换**带 NUL 结尾**。
// 少这个结尾, Windows 会把缓冲区后面的垃圾一并当成字符串读走。
func TestUTF16PtrNulTerminated(t *testing.T) {
	p, keep := utf16Ptr("ab")
	if p == nil || keep == nil {
		t.Fatalf("utf16Ptr 返回 nil")
	}
	sl := unsafe.Slice(p, len(keep))
	if len(sl) != 3 { // 'a' 'b' NUL
		t.Fatalf("单元数 = %d, want 3 (含结尾 NUL)", len(sl))
	}
	if sl[0] != 'a' || sl[1] != 'b' || sl[2] != 0 {
		t.Fatalf("内容 = %v, want [97 98 0]", sl)
	}
	// 空串也要有结尾 NUL (空指针会让 API 报参数错误)
	p2, keep2 := utf16Ptr("")
	if len(keep2) != 1 || unsafe.Slice(p2, 1)[0] != 0 {
		t.Fatalf("空串应转成单个 NUL, got %v", keep2)
	}
	// 非 BMP 字符 (emoji) 要占两个单元, 且不被截断
	_, keep3 := utf16Ptr("😀")
	if len(keep3) != 3 { // 代理对 2 个单元 + NUL
		t.Fatalf("emoji 单元数 = %d, want 3", len(keep3))
	}
}

// TestSurfaceImplementsDialogHost 编译期确认 win32 surface 满足 gfx 的
// 可选接口 (少了就是"运行时静默降级成 stderr 输出", 很难发现)。
func TestSurfaceImplementsDialogHost(t *testing.T) {
	var s interface{} = (*surface)(nil)
	if _, ok := s.(interface {
		ShowMessage(kind gfx.NativeDialogKind, title, message string) (bool, error)
		ShowOpenFile(opts gfx.NativeFileOptions) (string, bool, error)
	}); !ok {
		t.Fatalf("win32 surface 未实现 gfx 的 nativeDialogHost 接口")
	}
}

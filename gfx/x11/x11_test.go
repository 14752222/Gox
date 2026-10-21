//go:build linux

package x11

import (
	"image"
	"image/color"
	"os"
	"testing"

	"github.com/14752222/Gox/gfx"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// TestRGBAToXZPixelFormat 验证 RGBA→X ZPixmap 的 BGRA 字节布局与裁剪区域读取。
func TestRGBAToXZPixelFormat(t *testing.T) {
	// 2x2 图: R=0xFF0000, G=0x00FF00, B=0x0000FF, W=0xFFFFFF
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.SetRGBA(0, 0, colorRGB(0xFF, 0, 0))
	img.SetRGBA(1, 0, colorRGB(0, 0xFF, 0))
	img.SetRGBA(0, 1, colorRGB(0, 0, 0xFF))
	img.SetRGBA(1, 1, colorRGB(0xFF, 0xFF, 0xFF))

	data := RGBAToXZPixmap(img, img.Bounds())
	if len(data) != 2*2*4 {
		t.Fatalf("buffer size = %d, want 16", len(data))
	}
	// 小端 ZPixmap: B,G,R,unused
	if data[0] != 0 || data[1] != 0 || data[2] != 0xFF {
		t.Fatalf("red pixel → BGR = %v", data[0:3])
	}
	// 绿色 (0,0xFF,0) → BGR = (0,0xFF,0)：B 与 R 均为 0，仅 G 通道点亮
	if data[4] != 0 || data[5] != 0xFF || data[6] != 0 {
		t.Fatalf("green pixel → BGR = %v", data[4:7])
	}
	if data[8] != 0xFF || data[9] != 0 || data[10] != 0 {
		t.Fatalf("blue pixel → BGR = %v", data[8:11])
	}

	// 子区域: 只取右下 1x1 (白色)
	sub := RGBAToXZPixmap(img, image.Rect(1, 1, 2, 2))
	if len(sub) != 4 || sub[0] != 0xFF || sub[1] != 0xFF || sub[2] != 0xFF {
		t.Fatalf("sub-rect = %v", sub)
	}
}

// TestKeysymName 键符→键名映射 (与 win32 后端同名约定)。
func TestKeysymName(t *testing.T) {
	cases := map[uint32]string{
		0x61:      "a",
		0x41:      "A",
		0x20:      " ",
		0xFF0D:    "Enter",
		0xFF08:    "Backspace",
		0xFF1B:    "Escape",
		0xFF51:    "ArrowLeft",
		0xFF53:    "ArrowRight",
		0xFF52:    "ArrowUp",
		0xFF54:    "ArrowDown",
		0xFFFF:    "Delete",
		0xFFBE:    "F1",
		0xFFC9:    "F12",
		0x1004E00: "一", // CJK 区间经 Unicode 键符
	}
	for ks, want := range cases {
		if got := KeysymName(ks); got != want {
			t.Fatalf("KeysymName(0x%X) = %q, want %q", ks, got, want)
		}
	}
	if KeysymName(0x12345678) != "" {
		t.Fatalf("unknown keysym should map to empty")
	}
}

func colorRGB(r, g, b byte) color.RGBA {
	return color.RGBA{R: r, G: g, B: b, A: 255}
}

// ===== 多显示器枚举 (M4 displayProvider) =====

// displayProviderShape 是 gfx.displayProvider 的结构等价声明。
//
// gfx 里那个接口未导出 (可选能力走类型断言), 后端包没法引用它的名字 ——
// 于是只能按方法集自证。这正是"不扩 Surface、用可选接口"纪律的代价与好处:
// 多写这一小块, 换来内核不动既有接口。
type displayProviderShape interface {
	Displays() []gfx.Display
	DisplayOf(gfx.Surface) (string, bool)
}

func TestFactoryImplementsDisplayProvider(t *testing.T) {
	var _ displayProviderShape = (*factory)(nil)
}

// TestDisplaysDegradesWithoutXServer 钉住"没有 X 连接 → 返回 nil" (交给内核
// 虚拟屏兜底), 而不是自己造一块假屏。内核 allDisplays 的优先级链要求
// "后端枚举不到"与"后端说有一块屏"可区分; 造了假屏会让真·无头环境也报出
// 一块 1024x768 的幽灵显示器。
func TestDisplaysDegradesWithoutXServer(t *testing.T) {
	if os.Getenv("DISPLAY") != "" {
		t.Skip("有 DISPLAY, 走真实枚举路径 (在真 X 会话下无法断言'无屏')")
	}
	var f factory
	if got := f.Displays(); got != nil {
		t.Fatalf("无 X server 时 Displays() 应返回 nil, 实际 %+v", got)
	}
}

// TestDisplayOfRejectsForeignSurface 验证非本后端 surface 一律 ("", false),
// 让 gfx 内核退回主屏, 而不是把别的后端的窗口误判到 x11:0。
func TestDisplayOfRejectsForeignSurface(t *testing.T) {
	var f factory
	if id, ok := f.DisplayOf(nil); ok || id != "" {
		t.Fatalf("nil surface 应返回 (\"\", false), 实际 (%q,%v)", id, ok)
	}
	if id, ok := f.DisplayOf(foreignSurface{}); ok || id != "" {
		t.Fatalf("外部 surface 应返回 (\"\", false), 实际 (%q,%v)", id, ok)
	}
}

// foreignSurface 借 gfx.Surface 的方法集"冒充"另一个后端的 surface
// (只需满足接口, 不会被真正调用)。
type foreignSurface struct{ gfx.Surface }

// TestItoaX 覆盖序号拼接 (monitor 名缺失时退路 id 的后半段)。
func TestItoaX(t *testing.T) {
	cases := map[int]string{0: "0", 1: "1", 9: "9", 10: "10", 12345: "12345"}
	for n, want := range cases {
		if got := itoaX(n); got != want {
			t.Errorf("itoaX(%d) = %q, want %q", n, got, want)
		}
	}
}

// TestHasPrimaryDisplay 验证主屏标记的存在性判定 (RandR 没标主屏时,
// randrDisplays 要把首块抬成主屏, 靠的就是这个谓词)。
func TestHasPrimaryDisplay(t *testing.T) {
	if hasPrimaryDisplay(nil) {
		t.Fatalf("空列表不该有主屏")
	}
	if hasPrimaryDisplay([]gfx.Display{{ID: "a"}, {ID: "b"}}) {
		t.Fatalf("没有 Primary 标记时不该报有主屏")
	}
	if !hasPrimaryDisplay([]gfx.Display{{ID: "a"}, {ID: "b", Primary: true}}) {
		t.Fatalf("有 Primary 标记应报有主屏")
	}
}

// TestXProtoEventsAreValues 锁住"xproto 事件是值类型"这个前提。
//
// 回归背景: translate 曾把全部 case 写成 `*xproto.ButtonPressEvent` 这类
// 指针形态, 而 jezek/xgb 的 *EventNew 返回的是值 —— type switch 全不匹配,
// 于是窗口能画出来但鼠标/键盘输入全部静默丢弃, 且编译/测试**全绿**
// (当时没有任何测试碰 translate, 因为直构 surface 需要活连接)。
// 这个测试不需要 X 服务器: 它只断言事件构造函数的返回类型形态, 一旦上游
// 改成返回指针, 这里立刻红, 提醒同步改 translate。
func TestXProtoEventsAreValues(t *testing.T) {
	// 32 字节足以让各 EventNew 解析完头部字段 (它们按需读固定偏移)。
	buf := make([]byte, 64)
	evs := map[string]xgb.Event{
		"ButtonPress":     xproto.ButtonPressEventNew(buf),
		"ButtonRelease":   xproto.ButtonReleaseEventNew(buf),
		"MotionNotify":    xproto.MotionNotifyEventNew(buf),
		"LeaveNotify":     xproto.LeaveNotifyEventNew(buf),
		"KeyPress":        xproto.KeyPressEventNew(buf),
		"KeyRelease":      xproto.KeyReleaseEventNew(buf),
		"ConfigureNotify": xproto.ConfigureNotifyEventNew(buf),
		"ClientMessage":   xproto.ClientMessageEventNew(buf),
	}
	for name, ev := range evs {
		switch ev.(type) {
		case xproto.ButtonPressEvent, xproto.ButtonReleaseEvent,
			xproto.MotionNotifyEvent, xproto.LeaveNotifyEvent,
			xproto.KeyPressEvent, xproto.KeyReleaseEvent,
			xproto.ConfigureNotifyEvent, xproto.ClientMessageEvent:
			// 值类型: 与 translate 的 case 一致
		default:
			t.Fatalf("%s 事件是 %T, translate 只识别值类型 —— 上游改成指针了?", name, ev)
		}
	}
}

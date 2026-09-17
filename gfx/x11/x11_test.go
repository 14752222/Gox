//go:build linux

package x11

import (
	"image"
	"image/color"
	"testing"
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
	if data[4] != 0xFF || data[5] != 0xFF || data[6] != 0 {
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

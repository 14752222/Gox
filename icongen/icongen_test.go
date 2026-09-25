package icongen

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// goldenRoot 是 golden 文件的根目录; 与生成结果的相对路径一一对应。
var goldenRoot = filepath.Join("testdata", "golden")

// TestGenerateAllGolden 是最重的一条用例: 全平台生成的每个文件逐字节比对
// golden。图标生成是纯函数（同源图 → 同输出）, 任何尺寸/格式回归都会在这里
// 变红 —— 特别是 iOS 1024 的 alpha 通道问题, 只看字节数是发现不了的。
//
// 改动生成逻辑后跑:  UPDATE_GOLDEN=1 go test ./icongen/...  刷新 golden,
// 并人工确认 diff 合理（用预览看几张代表图）。
func TestGenerateAllGolden(t *testing.T) {
	root := t.TempDir()
	written, err := GenerateAll(root, filepath.Join("testdata", "source.png"), "#18243B")
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	if len(written) == 0 {
		t.Fatal("没有生成任何文件")
	}

	update := os.Getenv("UPDATE_GOLDEN") == "1"
	for _, rel := range written {
		got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("读取生成结果 %s: %v", rel, err)
		}
		golden := filepath.Join(goldenRoot, filepath.FromSlash(rel))
		if update {
			if err := writeFile(golden, got); err != nil {
				t.Fatalf("刷新 golden %s: %v", golden, err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("缺少 golden 文件 %s（先跑 UPDATE_GOLDEN=1 go test 生成）: %v", golden, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s 与 golden 不一致（got %dB, want %dB）", rel, len(got), len(want))
		}
	}
}

func TestGenerateAllIdempotent(t *testing.T) {
	// 两次生成的文件清单与内容必须完全一致 —— icon 会被 build 流程反复调用。
	root := t.TempDir()
	first, err := GenerateAll(root, filepath.Join("testdata", "source.png"), "#18243B")
	if err != nil {
		t.Fatal(err)
	}
	snap := map[string][]byte{}
	for _, rel := range first {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		snap[rel] = data
	}
	second, err := GenerateAll(root, filepath.Join("testdata", "source.png"), "#18243B")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != len(second) {
		t.Fatalf("两次文件清单不一致: %d vs %d", len(first), len(second))
	}
	for _, rel := range second {
		if !bytes.Equal(snap[rel], mustRead(t, filepath.Join(root, filepath.FromSlash(rel)))) {
			t.Errorf("%s 二次生成内容变化, 不幂等", rel)
		}
	}
}

// TestIOSStructure 核对 AppIconSet: 11 个尺寸条目、1024 无 alpha。
func TestIOSStructure(t *testing.T) {
	root := t.TempDir()
	if _, err := GenerateAll(root, filepath.Join("testdata", "source.png"), "#18243B"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "ios", "Assets.xcassets", "AppIcon.appiconset", "Contents.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contents struct {
		Images []struct {
			Filename string `json:"filename"`
			Size     string `json:"size"`
		} `json:"images"`
	}
	if err := json.Unmarshal(data, &contents); err != nil {
		t.Fatalf("Contents.json 不是合法 JSON: %v", err)
	}
	if len(contents.Images) != len(iosIconSpecs) {
		t.Errorf("Contents.json 条目数 %d != 期望 %d", len(contents.Images), len(iosIconSpecs))
	}

	// 1024 营销图必须无 alpha —— App Store 上传校验会拒收带 alpha 的 PNG。
	marketing, err := os.Open(filepath.Join(root, "ios", "Assets.xcassets", "AppIcon.appiconset", "AppIcon-1024.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer marketing.Close()
	img, err := png.Decode(marketing)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 1024 || img.Bounds().Dy() != 1024 {
		t.Errorf("营销图尺寸 %v", img.Bounds())
	}
	if !isOpaqueImg(img) {
		t.Error("1024 营销图带 alpha 通道, App Store 会拒收")
	}
}

// TestAndroidForegroundSafeZone 核对前景层: 画布 108dp 档尺寸, 四角透明,
// 中心有内容。
func TestAndroidForegroundSafeZone(t *testing.T) {
	root := t.TempDir()
	if _, err := GenerateAll(root, filepath.Join("testdata", "source.png"), "#18243B"); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(root, "android", "res", "mipmap-xxxhdpi", "ic_launcher_foreground.png"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 432 {
		t.Errorf("xxxhdpi 前景画布应 432px, got %v", img.Bounds())
	}
	rgba := img // png.Decode 可能返回 NRGBA, 统一走 At().RGBA() 判定
	for _, corner := range []image.Point{{0, 0}, {431, 0}, {0, 431}, {431, 431}} {
		if _, _, _, a := rgba.At(corner.X, corner.Y).RGBA(); a != 0 {
			// 角落在安全区外, 应当是透明的（源图是带圆角的方图, 缩到 66% 居中后碰不到角）
			t.Errorf("前景层角落 %v 不透明, 安全区留白失效", corner)
		}
	}
	center := rgba.At(216, 216)
	if _, _, _, a := center.RGBA(); a == 0 {
		t.Error("前景层中心是空的, 源图没画进去")
	}
}

// TestICOAndICNSHeaders 直接解析二进制头, 核对 .ico/.icns 结构合法。
func TestICOAndICNSHeaders(t *testing.T) {
	root := t.TempDir()
	if _, err := GenerateAll(root, filepath.Join("testdata", "source.png"), "#18243B"); err != nil {
		t.Fatal(err)
	}

	ico, err := os.ReadFile(filepath.Join(root, "desktop", "icon.ico"))
	if err != nil {
		t.Fatal(err)
	}
	if binary.LittleEndian.Uint16(ico[2:]) != 1 || binary.LittleEndian.Uint16(ico[4:]) != uint16(len(icoSizes)) {
		t.Errorf(".ico 头非法: type=%d count=%d", binary.LittleEndian.Uint16(ico[2:]), binary.LittleEndian.Uint16(ico[4:]))
	}
	// 逐条目校验 offset+size 不越界
	for i := 0; i < len(icoSizes); i++ {
		entry := ico[6+16*i:]
		size := binary.LittleEndian.Uint32(entry[8:])
		off := binary.LittleEndian.Uint32(entry[12:])
		if off+size > uint32(len(ico)) {
			t.Errorf(".ico 条目 %d 越界: off=%d size=%d total=%d", i, off, size, len(ico))
		}
	}

	icns, err := os.ReadFile(filepath.Join(root, "desktop", "icon.icns"))
	if err != nil {
		t.Fatal(err)
	}
	if string(icns[0:4]) != "icns" {
		t.Fatalf(".icns magic 错误: %q", icns[0:4])
	}
	if total := binary.BigEndian.Uint32(icns[4:]); int(total) != len(icns) {
		t.Errorf(".icns 总长字段 %d != 实际 %d", total, len(icns))
	}
}

func TestSourceRejectsNonSquare(t *testing.T) {
	dir := t.TempDir()
	// 画一张 100×50 的非方形图
	img := image.NewRGBA(image.Rect(0, 0, 100, 50))
	p := filepath.Join(dir, "wide.png")
	f, _ := os.Create(p)
	png.Encode(f, img)
	f.Close()

	if _, err := LoadSource(p); err == nil {
		t.Fatal("非方形源图应当报错")
	}
}

func TestSourceRejectsMissing(t *testing.T) {
	if _, err := LoadSource(filepath.Join(t.TempDir(), "nope.png")); err == nil {
		t.Fatal("缺失的源图应当报错")
	}
}

// isOpaqueImg 报告整图是否全不透明（逐像素扫描, 只在测试里用）。
func isOpaqueImg(img image.Image) bool {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a>>8 != 0xff {
				return false
			}
		}
	}
	return true
}

func mustRead(t *testing.T, p string) []byte {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

package gfx

// ===== gx/media 图片处理 (testdata/media_image_api_demo.js) 全链路回归 =====
//
// Go 侧先生成一张 4x4 的 PNG 测试图，再让 JS 走
// load → resize → crop → encode(png/jpeg, quality) → write → 回读 全链路。
// 图片处理是同步 API，不需要事件泵，EvalVM 跑完断言即可。

import (
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// writeTestPNG 生成 4x4 四色测试图。
func writeTestPNG(t *testing.T, path string) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	colors := []color.RGBA{{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255}, {255, 255, 0, 255}}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			img.SetRGBA(x, y, colors[(x/2)+(y/2)*2])
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("创建测试图: %v", err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("编码测试图: %v", err)
	}
}

func TestMediaImageAPIScript(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.png")
	out := filepath.Join(dir, "out.jpg")
	writeTestPNG(t, in)

	src, err := os.ReadFile(filepath.Join("..", "testdata", "media_image_api_demo.js"))
	if err != nil {
		t.Fatalf("读示例脚本: %v", err)
	}
	// 占位符落在 JS 字符串字面量里, Windows 反斜杠路径必须按 JS 转义 ——
	// 否则 `F:\tmp\...` 的 `\t` 会被解析成 TAB, open 报 invalid argument
	// (2026-10-12 CI windows job 红); 正斜杠在 Windows 的 Go os 包同样合法。
	script := strings.ReplaceAll(string(src), "__IN__", filepath.ToSlash(in))
	script = strings.ReplaceAll(script, "__OUT__", filepath.ToSlash(out))

	v, err := vm.EvalVM(script)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	summary, ok := v.Globals().Get("__imgDemoSummary")
	if !ok {
		t.Fatalf("脚本没有导出 __imgDemoSummary")
	}
	s, isStr := summary.(*object.String)
	if !isStr {
		t.Fatalf("__imgDemoSummary 应为字符串, 得到 %s", summary.Inspect())
	}
	if strings.Contains(s.Value, "=false") {
		t.Fatalf("存在失败断言: %s", s.Value)
	}
	const wantChecks = 9
	if got := strings.Count(s.Value, "=true"); got != wantChecks {
		t.Fatalf("断言条数 = %d, 应为 %d: %s", got, wantChecks, s.Value)
	}
}

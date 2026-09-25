package icongen

import (
	"encoding/json"
	"fmt"
	"path/filepath"
)

// iosIconSpec 描述 AppIcon.appiconset 里的一张图。
// Size 是像素边长; Pt 是点数; Scale 是 @1x/@2x/@3x; Idiom 是设备族。
var iosIconSpecs = []struct {
	Size  int
	Pt    string
	Scale string
	Idiom string
}{
	{40, "20x20", "2x", "iphone"},
	{60, "20x20", "3x", "iphone"},
	{58, "29x29", "2x", "iphone"},
	{87, "29x29", "3x", "iphone"},
	{80, "40x40", "2x", "iphone"},
	{120, "40x40", "3x", "iphone"},
	{120, "60x60", "2x", "iphone"},
	{180, "60x60", "3x", "iphone"},
	{152, "76x76", "2x", "ipad"},
	{167, "83.5x83.5", "2x", "ipad"},
	{1024, "1024x1024", "1x", "ios-marketing"},
}

// GenerateIOS 生成 iOS AppIcon.appiconset（全尺寸 + Contents.json）,
// 写入 root（= ios/Assets.xcassets/AppIcon.appiconset）。
//
// 全系图标走去 alpha 通道: App Store 的 1024 营销图带 alpha 直接拒审,
// 小图统一处理省得逐个记规则。文件名用 AppIcon-<size>.png, 稳定且自说明。
func (s Source) GenerateIOS(root string) ([]string, error) {
	type entry struct {
		Filename string `json:"filename"`
		Idiom    string `json:"idiom"`
		Scale    string `json:"scale"`
		Size     string `json:"size"`
	}
	var images []entry
	var written []string
	for _, spec := range iosIconSpecs {
		name := fmt.Sprintf("AppIcon-%d.png", spec.Size)
		p := filepath.Join(root, name)
		if err := s.writePNG(p, spec.Size, true); err != nil {
			return nil, fmt.Errorf("生成 %s 失败: %w", p, err)
		}
		images = append(images, entry{
			Filename: name,
			Idiom:    spec.Idiom,
			Scale:    spec.Scale,
			Size:     spec.Pt,
		})
		rel, _ := filepath.Rel(filepath.Dir(filepath.Dir(root)), p)
		written = append(written, "ios/"+filepath.ToSlash(rel))
	}

	contents := map[string]interface{}{"images": images}
	data, err := json.MarshalIndent(contents, "", "  ")
	if err != nil {
		return nil, err
	}
	p := filepath.Join(root, "Contents.json")
	if err := writeFile(p, data); err != nil {
		return nil, err
	}
	rel, _ := filepath.Rel(filepath.Dir(filepath.Dir(root)), p)
	written = append(written, "ios/"+filepath.ToSlash(rel))
	return written, nil
}

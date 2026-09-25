package icongen

import (
	"fmt"
	"image"
	"image/draw"
	"os"
	"path/filepath"
	"strings"
)

// Android 各密度下的 res 目录 → 像素边长（图标 48dp / 前景层 108dp 画布;
// 密度倍率 mdpi=1 hdpi=1.5 xhdpi=2 xxhdpi=3 xxxhdpi=4, 48dp 档全部取整）。
var androidLaunchPx = map[string]int{
	"mipmap-mdpi":    48,
	"mipmap-hdpi":    72,
	"mipmap-xhdpi":   96,
	"mipmap-xxhdpi":  144,
	"mipmap-xxxhdpi": 192,
}

// androidForegroundPx 自适应图标前景层的 108dp 画布像素边长。
var androidForegroundPx = map[string]int{
	"mipmap-mdpi":    108,
	"mipmap-hdpi":    162,
	"mipmap-xhdpi":   216,
	"mipmap-xxhdpi":  324,
	"mipmap-xxxhdpi": 432,
}

// adaptiveIconXML 是自适应图标定义。背景用纯色（@color/ic_launcher_background,
// 由 GenerateAndroid 落入 values）, 前景用生成的 PNG。
const adaptiveIconXML = `<?xml version="1.0" encoding="utf-8"?>
<!-- 由 gox icon 生成（源: gox.json 的 icon 指向的 1024 源图）。勿手工改, 重跑会被覆盖。 -->
<adaptive-icon xmlns:android="http://schemas.android.com/apk/res/android">
    <background android:drawable="@color/ic_launcher_background" />
    <foreground android:drawable="@mipmap/ic_launcher_foreground" />
</adaptive-icon>
`

// GenerateAndroid 从源图生成 Android 启动图标资源, 写入 root/res/ 下:
//
//	res/mipmap-*/ic_launcher.png            传统图标（5 档密度）
//	res/mipmap-*/ic_launcher_foreground.png 自适应图标前景层（5 档密度）
//	res/mipmap-anydpi-v26/ic_launcher.xml   自适应图标定义（+ round 变体）
//	res/values/(colors|gox_icon_colors).xml 背景色
//
// bg 是自适应图标的背景色（#RRGGBB）。单图源自动兜底: 源图缩放到 66/108 安全区
// 居中, 背景铺纯色 —— 不要求用户准备前景/背景双层素材。
func (s Source) GenerateAndroid(root, bg string) ([]string, error) {
	var written []string
	for _, dir := range []string{"mipmap-mdpi", "mipmap-hdpi", "mipmap-xhdpi", "mipmap-xxhdpi", "mipmap-xxxhdpi"} {
		p := filepath.Join(root, "res", dir, "ic_launcher.png")
		if err := s.writePNG(p, androidLaunchPx[dir], false); err != nil {
			return nil, fmt.Errorf("生成 %s 失败: %w", p, err)
		}
		written = append(written, resRel(root, p))

		p = filepath.Join(root, "res", dir, "ic_launcher_foreground.png")
		if err := s.writeForeground(p, androidForegroundPx[dir]); err != nil {
			return nil, fmt.Errorf("生成 %s 失败: %w", p, err)
		}
		written = append(written, resRel(root, p))
	}

	anydpi := filepath.Join(root, "res", "mipmap-anydpi-v26")
	for _, name := range []string{"ic_launcher.xml", "ic_launcher_round.xml"} {
		p := filepath.Join(anydpi, name)
		if err := writeFile(p, []byte(adaptiveIconXML)); err != nil {
			return nil, fmt.Errorf("生成 %s 失败: %w", p, err)
		}
		written = append(written, resRel(root, p))
	}

	colorPath, err := writeBackgroundColor(root, bg)
	if err != nil {
		return nil, err
	}
	if colorPath != "" {
		written = append(written, resRel(root, colorPath))
	}
	return written, nil
}

// writeForeground 生成自适应图标前景层: 108dp 画布, 源图内容缩放到中心
// 66dp 安全区（66/108）, 其余透明 —— 系统按形状遮罩裁外圈, 内容不越安全区
// 就不会被裁掉。
func (s Source) writeForeground(path string, canvasPx int) error {
	content := canvasPx * 66 / 108
	inner := s.resize(content)
	canvas := image.NewRGBA(image.Rect(0, 0, canvasPx, canvasPx))
	off := (canvasPx - content) / 2
	dstRect := inner.Bounds().Add(image.Pt(off, off))
	draw.Draw(canvas, dstRect, inner, inner.Bounds().Min, draw.Over)
	data, err := encodePNG(canvas)
	if err != nil {
		return err
	}
	return writeFile(path, data)
}

// writeBackgroundColor 把自适应图标背景色写进资源。
// 已有 values/colors.xml 且包含 ic_launcher_background 时就地替换该色值
// （保持文件其它内容不动）; 否则新建 values/gox_icon_colors.xml。
func writeBackgroundColor(root, bg string) (string, error) {
	colorsPath := filepath.Join(root, "res", "values", "colors.xml")
	if data, err := os.ReadFile(colorsPath); err == nil && strings.Contains(string(data), "ic_launcher_background") {
		updated := replaceColorValue(string(data), "ic_launcher_background", bg)
		if err := writeFile(colorsPath, []byte(updated)); err != nil {
			return "", err
		}
		return colorsPath, nil
	}
	out := filepath.Join(root, "res", "values", "gox_icon_colors.xml")
	xml := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>
<!-- 由 gox icon 生成: 自适应图标背景色（与 gox.json 的 android.adaptiveBackground 一致）。 -->
<resources>
    <color name="ic_launcher_background">%s</color>
</resources>
`, bg)
	if err := writeFile(out, []byte(xml)); err != nil {
		return "", err
	}
	return out, nil
}

// replaceColorValue 替换 `<color name="key">旧值</color>` 的值。
func replaceColorValue(content, key, value string) string {
	start := strings.Index(content, `<color name="`+key+`"`)
	if start < 0 {
		return content
	}
	gt := strings.Index(content[start:], ">")
	if gt < 0 {
		return content
	}
	gt += start
	end := strings.Index(content[gt:], "<")
	if end < 0 {
		return content
	}
	return content[:gt+1] + value + content[gt+end:]
}

// resRel 返回相对项目根的斜杠路径（供命令行打印清单）。
func resRel(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(rel)
}

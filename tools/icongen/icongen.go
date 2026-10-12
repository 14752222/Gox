// Package icongen 负责"单源图标 + 平台资源"的全部纯 Go 生成逻辑:
//
//	assets/icon.png (1024×1024) ──► Android mipmap 全密度 + 自适应图标前景
//	                            ──► iOS AppIcon.appiconset 全尺寸 (1024 去 alpha)
//	                            ──► Windows .ico (多尺寸, PNG 压缩条目)
//	                            ──► macOS .icns (自实现 png2icns 容器)
//	                            ──► favicon.png
//
// 另收留两个"资源对象"生成器（打包链路共用, 都不引第三方依赖）:
//   - BuildWindowsSYSO: 生成可随 go build 链接的 .syso（内嵌 .ico + 版本资源）;
//   - BuildMacAppBundle: 把已编译的 darwin 二进制组装成 .app bundle。
//
// 缩放统一用 golang.org/x/image/draw 的 CatmullRom 滤镜 —— 仓库已有该依赖,
// 零 cgo, 不新增第三方包。所有文件写入都是幂等的（同名覆盖）。
package icongen

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"

	xdraw "golang.org/x/image/draw"
)

// Source 是解码后的源图标。要求正方形 —— 各平台产物都是方形画布,
// 非方形源一定意味着用户没准备好"单源"素材, 宁可报错也不悄悄裁剪。
type Source struct {
	Img  image.Image
	Size int
}

// LoadSource 读取并解码源图标（支持 png/jpeg, 实际约定用 png）。
func LoadSource(path string) (Source, error) {
	f, err := os.Open(path)
	if err != nil {
		return Source{}, fmt.Errorf("打开源图标失败: %w", err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return Source{}, fmt.Errorf("解码 %s 失败: %w", path, err)
	}
	b := img.Bounds()
	if b.Dx() != b.Dy() {
		return Source{}, fmt.Errorf("源图标 %s 不是正方形 (%dx%d), 请提供 1024×1024 的图标", path, b.Dx(), b.Dy())
	}
	return Source{Img: img, Size: b.Dx()}, nil
}

// resize 把源图缩放为 size×size 的 RGBA 图（CatmullRom, 质量优先 —— 图标生成
// 不在热路径上, 没必要换更快但更糙的滤镜）。
func (s Source) resize(size int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, size, size))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), s.Img, s.Img.Bounds(), xdraw.Over, nil)
	return dst
}

// opaque 返回去 alpha 版本: 透明像素铺白底后再缩放。iOS 的 App Store 营销图
// (1024) 带 alpha 会被直接拒收, 所以 iOS 全系图标都走这一路。
func (s Source) opaque(size int) *image.RGBA {
	base := image.NewRGBA(image.Rect(0, 0, s.Size, s.Size))
	draw.Draw(base, base.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(base, base.Bounds(), s.Img, s.Img.Bounds().Min, draw.Over)
	op := Source{Img: base, Size: s.Size}
	return op.resize(size)
}

// encodePNG 把图编码为 PNG。
func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writePNG 生成 size×size 的一张 PNG 并落盘（父目录自动创建）。
func (s Source) writePNG(path string, size int, opaque bool) error {
	var img *image.RGBA
	if opaque {
		img = s.opaque(size)
	} else {
		img = s.resize(size)
	}
	data, err := encodePNG(img)
	if err != nil {
		return err
	}
	return writeFile(path, data)
}

// writeFile 落盘工具: 自动建父目录。全包统一走这里, 保证行为一致。
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建目录 %s 失败: %w", filepath.Dir(path), err)
	}
	return os.WriteFile(path, data, 0o644)
}

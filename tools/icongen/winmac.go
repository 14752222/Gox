package icongen

import (
	"encoding/binary"
	"fmt"
	"path/filepath"
)

// icoSizes 是 .ico 内嵌的尺寸档（Vista+ 支持 PNG 压缩条目 —— 用 PNG 而不是
// BMP, 体积小一个数量级; 写 BMP 反而要自己压行, 得不偿失）。
var icoSizes = []int{16, 24, 32, 48, 64, 128, 256}

// icnsChunks 是 .icns 的 chunk 表: 4 字节类型 → 像素边长。
// macOS 对 PNG 数据的 chunk 自 10.7 起全量支持, 无需自绘位图。
var icnsChunks = []struct {
	Type string
	Size int
}{
	{"icp4", 16},   // 16×16
	{"icp5", 32},   // 32×32
	{"ic07", 128},  // 128×128
	{"ic08", 256},  // 256×256
	{"ic09", 512},  // 512×512
	{"ic10", 1024}, // 512×512@2x
}

// GenerateICO 生成多尺寸 Windows .ico（PNG 压缩条目）。
func (s Source) GenerateICO(path string) error {
	pngs := make(map[int][]byte, len(icoSizes))
	for _, size := range icoSizes {
		data, err := encodePNG(s.resize(size))
		if err != nil {
			return fmt.Errorf("编码 %dpx PNG 失败: %w", size, err)
		}
		pngs[size] = data
	}

	// ICONDIR (6B) + n × ICONDIRENTRY (16B) + 各 PNG 数据
	total := 6 + 16*len(icoSizes)
	for _, size := range icoSizes {
		total += len(pngs[size])
	}
	buf := make([]byte, 0, total)

	dir := make([]byte, 6)
	binary.LittleEndian.PutUint16(dir[0:], 0) // reserved
	binary.LittleEndian.PutUint16(dir[2:], 1) // type: icon
	binary.LittleEndian.PutUint16(dir[4:], uint16(len(icoSizes)))
	buf = append(buf, dir...)

	offset := uint32(6 + 16*len(icoSizes))
	for _, size := range icoSizes {
		entry := make([]byte, 16)
		if size >= 256 {
			entry[0], entry[1] = 0, 0 // 256 用 0 表示
		} else {
			entry[0], entry[1] = byte(size), byte(size)
		}
		binary.LittleEndian.PutUint16(entry[4:], 1)  // planes
		binary.LittleEndian.PutUint16(entry[6:], 32) // bit count
		binary.LittleEndian.PutUint32(entry[8:], uint32(len(pngs[size])))
		binary.LittleEndian.PutUint32(entry[12:], offset)
		buf = append(buf, entry...)
		offset += uint32(len(pngs[size]))
	}
	for _, size := range icoSizes {
		buf = append(buf, pngs[size]...)
	}
	return writeFile(path, buf)
}

// GenerateICNS 生成 macOS .icns（自实现 png2icns: PNG 数据原样装进 icns
// 容器的各个 chunk —— Apple 官方 iconutil 的等价输出也是这个结构）。
func (s Source) GenerateICNS(path string) error {
	var body []byte
	for _, chunk := range icnsChunks {
		png, err := encodePNG(s.resize(chunk.Size))
		if err != nil {
			return fmt.Errorf("编码 %dpx PNG 失败: %w", chunk.Size, err)
		}
		item := make([]byte, 8+len(png))
		copy(item[0:4], chunk.Type)
		binary.BigEndian.PutUint32(item[4:], uint32(8+len(png)))
		copy(item[8:], png)
		body = append(body, item...)
	}

	out := make([]byte, 8+len(body))
	copy(out[0:4], "icns")
	binary.BigEndian.PutUint32(out[4:], uint32(len(out)))
	copy(out[8:], body)
	return writeFile(path, out)
}

// GenerateFavicon 生成 32×32 的 favicon.png（浏览器/文档站用）。
func (s Source) GenerateFavicon(path string) error {
	return s.writePNG(path, 32, false)
}

// GenerateDesktop 生成桌面端图标: root 下 icon.ico + icon.icns。
func (s Source) GenerateDesktop(root string) ([]string, error) {
	var written []string
	for _, pair := range []struct {
		name string
		gen  func(string) error
	}{
		{"icon.ico", s.GenerateICO},
		{"icon.icns", s.GenerateICNS},
	} {
		p := filepath.Join(root, pair.name)
		if err := pair.gen(p); err != nil {
			return nil, fmt.Errorf("生成 %s 失败: %w", p, err)
		}
		written = append(written, "desktop/"+pair.name)
	}
	return written, nil
}

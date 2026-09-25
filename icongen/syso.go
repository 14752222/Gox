package icongen

import (
	"encoding/binary"
	"fmt"
	"os"
	"strings"
	"unicode/utf16"
)

// 本文件自实现 Windows .syso（COFF 目标文件, 内嵌 .rsrc 资源段）。
// `go build` 会自动拾取包目录下 <name>_<GOOS>_<GOARCH>.syso, 把里面的
// 图标与版本资源链进最终 exe —— 这是 Windows 下给 Go 程序加图标/版本
// 信息的零依赖途径（rsrc/goversioninfo 本质上就是这种格式的生成器）。
//
// .rsrc 段布局（所有偏移以段起始为基址, 段 VirtualAddress 取 0）:
//
//	[目录树] 根 → 类型(RT_ICON×n | RT_GROUP_ICON | RT_VERSION) → ID → 语言 → 数据条目
//	[数据区] 各资源数据依次排列（PNG 条目 / GRPICONDIR / VS_VERSIONINFO）

// coffMachine: GOARCH → COFF Machine 字段。
var coffMachine = map[string]uint16{
	"amd64": 0x8664,
	"arm64": 0xAA64,
	"386":   0x014C,
}

// BuildWindowsSYSO 生成可随 go build 链接的 .syso, 写入 outPath
// （建议命名 rsrc_windows_amd64.syso 之类, 放在被编译的包目录里）。
//
// icoPath 是多尺寸 .ico（GenerateICO 的产物）; version 是语义化版本;
// name/title 进 VERSIONINFO 的 InternalName/ProductName 等字段。
func BuildWindowsSYSO(outPath, icoPath, version, name, title string, goarch string) error {
	machine, ok := coffMachine[goarch]
	if !ok {
		return fmt.Errorf("不支持的 GOARCH %q（可用: amd64, arm64, 386）", goarch)
	}
	icoData, err := os.ReadFile(icoPath)
	if err != nil {
		return fmt.Errorf("读取 .ico 失败: %w", err)
	}
	icons, err := parseICOBytes(icoData, icoPath)
	if err != nil {
		return err
	}
	sec := buildResourceSection(icons, version, name, title)
	return writeFile(outPath, buildCOFF(machine, sec))
}

// icoEntry 是 .ico 里的一个尺寸条目。
type icoEntry struct {
	Width, Height int
	Planes        uint16
	BitCount      uint16
	Data          []byte
}

// parseICOBytes 拆开 .ico 内容, 取出各尺寸的原始数据。只支持 PNG 压缩条目
// （GenerateICO 产物即此格式）; 老式 BMP 条目直接报错, 免得链出坏图标。
func parseICOBytes(data []byte, path string) ([]icoEntry, error) {
	if len(data) < 6 {
		return nil, fmt.Errorf("%s 不是合法 .ico（太短）", path)
	}
	if binary.LittleEndian.Uint16(data[2:]) != 1 {
		return nil, fmt.Errorf("%s 不是 .ico 文件", path)
	}
	count := int(binary.LittleEndian.Uint16(data[4:]))
	entries := make([]icoEntry, 0, count)
	for i := 0; i < count; i++ {
		e := data[6+16*i:]
		if len(e) < 16 {
			return nil, fmt.Errorf("%s 条目 %d 截断", path, i)
		}
		size := int(binary.LittleEndian.Uint32(e[8:]))
		off := int(binary.LittleEndian.Uint32(e[12:]))
		if off+size > len(data) {
			return nil, fmt.Errorf("%s 条目 %d 越界", path, i)
		}
		blob := data[off : off+size]
		if len(blob) < 8 || string(blob[1:4]) != "PNG" {
			return nil, fmt.Errorf("%s 条目 %d 不是 PNG 压缩格式（用 gox icon 重新生成）", path, i)
		}
		entries = append(entries, icoEntry{
			Width:    int(e[0]),
			Height:   int(e[1]),
			Planes:   binary.LittleEndian.Uint16(e[4:]),
			BitCount: binary.LittleEndian.Uint16(e[6:]),
			Data:     blob,
		})
	}
	return entries, nil
}

// le 是一个小端序写入器。
type le struct{ b []byte }

func (w *le) u16(v uint16) { w.b = binary.LittleEndian.AppendUint16(w.b, v) }
func (w *le) u32(v uint32) { w.b = binary.LittleEndian.AppendUint32(w.b, v) }
func (w *le) raw(p []byte) { w.b = append(w.b, p...) }
func (w *le) setU16(at int, v uint16) {
	binary.LittleEndian.PutUint16(w.b[at:], v)
}
func (w *le) pad4() {
	for len(w.b)%4 != 0 {
		w.b = append(w.b, 0)
	}
}

// utf16z 把字符串编码为 UTF-16LE 并补结尾 NUL。
func utf16z(s string) []uint16 { return utf16.Encode([]rune(s + "\x00")) }

// resource 是一条挂到目录树上的资源（类型 + ID + 数据）。
type resource struct {
	typ, id uint32
	data    []byte
}

// buildResourceSection 组装 .rsrc 段: 目录树 + 数据区。
func buildResourceSection(icons []icoEntry, version, name, title string) []byte {
	var res []resource
	for i, ic := range icons {
		res = append(res, resource{typ: 3 /*RT_ICON*/, id: uint32(i + 1), data: ic.Data})
	}
	res = append(res, resource{typ: 14 /*RT_GROUP_ICON*/, id: 1, data: buildGroupIcon(icons)})
	res = append(res, resource{typ: 16 /*RT_VERSION*/, id: 1, data: buildVersionInfo(version, name, title)})
	return buildResourceDirectory(res)
}

// buildResourceDirectory 为一组资源构建三层目录（类型 → ID → 语言）+ 数据区。
// 布局: [根目录][类型1: (ID目录+语言目录)×n][类型2: ...][数据条目×N][数据区]。
func buildResourceDirectory(res []resource) []byte {
	// 按类型聚簇（调用方已按 icons → group → version 排好, 天然聚簇）
	type group struct {
		typ   uint32
		items []int // res 下标
	}
	var groups []group
	for i, r := range res {
		if len(groups) == 0 || groups[len(groups)-1].typ != r.typ {
			groups = append(groups, group{r.typ, nil})
		}
		groups[len(groups)-1].items = append(groups[len(groups)-1].items, i)
	}

	// 预计算偏移
	rootSize := uint32(16 + 8*len(groups))
	idDirOff := make([]uint32, len(groups))
	off := rootSize
	for gi, g := range groups {
		n := uint32(len(g.items))
		idDirOff[gi] = off
		off += 16 + 8*n     // ID 目录
		off += (16 + 8) * n // 每资源一个语言目录（16B 头 + 8B 单条目）
	}
	dataEntryBase := off
	bodyBase := dataEntryBase + uint32(16*len(res))

	w := &le{}
	// 根目录: 头 + k 个类型条目
	w.u16(0)
	w.u16(0)
	w.u16(0)
	w.u16(0)
	w.u16(uint16(len(groups)))
	w.u16(0)
	for gi, g := range groups {
		w.u32(g.typ)
		w.u32(idDirOff[gi] | 0x80000000)
	}
	for gi, g := range groups {
		n := len(g.items)
		// ID 目录: 每条指向对应语言目录
		w.u16(0)
		w.u16(0)
		w.u16(uint16(n))
		w.u16(0)
		for li := range g.items {
			w.u32(res[g.items[li]].id)
			w.u32(idDirOff[gi] + uint32(16+8*n+(16+8)*li) | 0x80000000)
		}
		// 语言目录: 每资源一个, 单条目指向数据条目（非子目录位）
		for li := range g.items {
			w.u16(0)
			w.u16(0)
			w.u16(1)
			w.u16(0)
			w.u32(0x0409) // en-US
			idx := 0
			for j := 0; j < gi; j++ {
				idx += len(groups[j].items)
			}
			w.u32(dataEntryBase + uint32(16*(idx+li)))
		}
	}
	// 数据条目区 + 数据区
	var body []byte
	for _, r := range res {
		w.u32(bodyBase + uint32(len(body))) // OffsetToData
		w.u32(uint32(len(r.data)))          // Size
		w.u32(0)                            // CodePage
		w.u32(0)                            // Reserved
		body = append(body, r.data...)
	}
	w.raw(body)
	return w.b
}

// buildGroupIcon 组装 RT_GROUP_ICON（GRPICONDIR, 6B 头 + 14B×n 条目）。
func buildGroupIcon(icons []icoEntry) []byte {
	w := &le{}
	w.u16(0)                  // reserved
	w.u16(1)                  // type: icon
	w.u16(uint16(len(icons))) // count
	for i, ic := range icons {
		w.b = append(w.b, byte(ic.Width), byte(ic.Height), 0, 0)
		w.u16(ic.Planes)
		w.u16(ic.BitCount)
		w.u32(uint32(len(ic.Data)))
		w.u16(uint16(i + 1)) // 引用的 RT_ICON 资源 ID
		w.u16(0)             // GRPICONDIRENTRY 补齐到 14B
	}
	return w.b
}

// versionNode 组装 VERSIONINFO 树的一个节点头（键名 + 空 value）。
// 调用方在拼完子内容后, 把总长回填到返回内容的前 2 字节。
func versionNode(key string, valueLen int, valueType uint16) []byte {
	w := &le{}
	w.u16(0) // wLength 占位
	w.u16(uint16(valueLen))
	w.u16(valueType)
	for _, r := range utf16z(key) {
		w.u16(r)
	}
	w.pad4()
	return w.b
}

// versionString 组装 String 子块（键值对, 值为 UTF-16 文本）。
func versionString(key, value string) []byte {
	keyU, valU := utf16z(key), utf16z(value)
	w := &le{}
	at := 0
	w.u16(0)                 // wLength 占位
	w.u16(uint16(len(valU))) // wValueLength: WORD 数（含 NUL）
	w.u16(1)                 // 文本
	for _, r := range keyU {
		w.u16(r)
	}
	w.pad4()
	for _, r := range valU {
		w.u16(r)
	}
	w.pad4()
	w.setU16(at, uint16(len(w.b)))
	return w.b
}

// buildVersionInfo 组装 VS_VERSIONINFO（RT_VERSION）。
// 约定: 键名与文本值为 UTF-16LE + NUL, 相对资源起始 4 字节对齐;
// String 子块的 wValueLength 以 WORD 数计。
func buildVersionInfo(version, name, title string) []byte {
	fileVer := version + ".0" // VERSIONINFO 需要 x.y.z.w 四段
	if title == "" {
		title = name
	}
	pairs := [][2]string{
		{"FileDescription", title},
		{"FileVersion", fileVer},
		{"InternalName", name},
		{"OriginalFilename", name + ".exe"},
		{"ProductName", title},
		{"ProductVersion", fileVer},
	}
	major, minor, patch := parseVersion3(version)

	// 自底向上组装: String×n → StringTable → StringFileInfo → 根
	var strings_ []byte
	for _, kv := range pairs {
		strings_ = append(strings_, versionString(kv[0], kv[1])...)
	}

	table := versionNode("040904B0", 0, 1)
	table = append(table, strings_...)
	binary.LittleEndian.PutUint16(table, uint16(len(table)))

	sfi := versionNode("StringFileInfo", 0, 1)
	sfi = append(sfi, table...)
	binary.LittleEndian.PutUint16(sfi, uint16(len(sfi)))

	root := versionNode("VS_VERSION_INFO", 52, 0)
	fixed := make([]byte, 52)
	binary.LittleEndian.PutUint32(fixed[0:], 0xFEEF04BD) // dwSignature
	binary.LittleEndian.PutUint32(fixed[4:], 0x00010000) // dwStrucVersion
	binary.LittleEndian.PutUint32(fixed[8:], uint32(major)<<16|uint32(minor))
	binary.LittleEndian.PutUint32(fixed[12:], uint32(patch)<<16)
	binary.LittleEndian.PutUint32(fixed[16:], uint32(major)<<16|uint32(minor))
	binary.LittleEndian.PutUint32(fixed[20:], uint32(patch)<<16)
	binary.LittleEndian.PutUint32(fixed[24:], 0x3F)    // dwFileFlagsMask
	binary.LittleEndian.PutUint32(fixed[28:], 0)       // dwFileFlags
	binary.LittleEndian.PutUint32(fixed[32:], 0x40004) // dwFileOS: VOS_NT_WINDOWS32
	binary.LittleEndian.PutUint32(fixed[36:], 1)       // dwFileType: VFT_APP
	// dwFileSubtype / dwFileDateMS / dwFileDateLS 保持 0
	root = append(root, fixed...)
	root = append(root, sfi...)
	binary.LittleEndian.PutUint16(root, uint16(len(root)))
	return root
}

func parseVersion3(v string) (int, int, int) {
	base := strings.SplitN(v, "-", 2)[0]
	parts := strings.SplitN(base, ".", 3)
	nums := [3]int{}
	for i, p := range parts {
		if i >= 3 {
			break
		}
		n := 0
		for _, c := range p {
			if c < '0' || c > '9' {
				break
			}
			n = n*10 + int(c-'0')
		}
		nums[i] = n
	}
	return nums[0], nums[1], nums[2]
}

// buildCOFF 组装 COFF 目标文件: 20B 头 + 40B 段头 + .rsrc 数据。
// 无符号表、无重定位 —— Go 链接器把 .rsrc 段当"原样数据"收录。
func buildCOFF(machine uint16, section []byte) []byte {
	w := &le{}
	w.u16(machine)
	w.u16(1)      // NumberOfSections
	w.u32(0)      // TimeDateStamp: 固定 0, 产物确定性（可复现构建）
	w.u32(0)      // PointerToSymbolTable
	w.u32(0)      // NumberOfSymbols
	w.u16(0)      // SizeOfOptionalHeader
	w.u16(0x0104) // Characteristics: 32BIT_MACHINE | LINE_NUMS_STRIPPED

	w.raw([]byte(".rsrc\x00\x00\x00")) // Name[8]
	w.u32(uint32(len(section)))        // VirtualSize
	w.u32(0)                           // VirtualAddress: RVA 基址取 0
	w.u32(uint32(len(section)))        // SizeOfRawData
	w.u32(60)                          // PointerToRawData: 20+40
	w.u32(0)                           // PointerToRelocations
	w.u32(0)                           // PointerToLinenumbers
	w.u16(0)                           // NumberOfRelocations
	w.u16(0)                           // NumberOfLinenumbers
	w.u32(0x40000040)                  // Characteristics: INITIALIZED_DATA | READ

	w.raw(section)
	return w.b
}

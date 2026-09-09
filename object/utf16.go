package object

import (
	"unicode/utf16"
	"unicode/utf8"
)

// 本文件提供 JavaScript 字符串索引所需的 UTF-16 码元辅助函数。
//
// JavaScript 字符串的索引单位是 UTF-16 码元 (code unit)，而 Go 的 string
// 内部是 UTF-8 字节序列。二者在 ASCII 范围内一致，但遇到非 ASCII 字符时
// 长度与索引位置都不同:
//
//	"世"   JS length = 1,  Go len() = 3
//	"😀"   JS length = 2 (一个代理对)，Go len() = 4
//
// 因此凡按"字符位置"索引的 API (length/charAt/charCodeAt/at/substring/
// slice/split("")/padStart 等) 都必须经过这里的转换。
//
// 这些函数放在 object 包 (而非 stdlib) 是因为 String.length 等原型属性
// 实现在 object 包内，而 stdlib 依赖 object，反向依赖会造成循环导入。
//
// 为控制开销，纯 ASCII 字符串走快速路径直接按字节索引，避免 []uint16 分配。

// UTF16Len 返回字符串的 UTF-16 码元长度，即 JavaScript 的 .length。
func UTF16Len(s string) int {
	if isASCIIString(s) {
		return len(s)
	}
	return len(utf16.Encode([]rune(s)))
}

// ToUTF16 将 Go 的 UTF-8 字符串转换为 UTF-16 码元序列。
func ToUTF16(s string) []uint16 {
	if isASCIIString(s) {
		u := make([]uint16, len(s))
		for i := 0; i < len(s); i++ {
			u[i] = uint16(s[i])
		}
		return u
	}
	return utf16.Encode([]rune(s))
}

// FromUTF16 将 UTF-16 码元序列转换回 UTF-8 字符串。
// 孤立代理项 (lone surrogate) 会被 utf16.Decode 转换为 U+FFFD，
// 这与 JavaScript 中单独取出半个代理对的行为一致。
func FromUTF16(u []uint16) string {
	if isASCIIUnits(u) {
		b := make([]byte, len(u))
		for i, c := range u {
			b[i] = byte(c)
		}
		return string(b)
	}
	return string(utf16.Decode(u))
}

// CharAtUTF16 按 UTF-16 码元索引取单个码元对应的字符串。
// 索引越界时 ok 返回 false。
func CharAtUTF16(s string, idx int) (string, bool) {
	if idx < 0 {
		return "", false
	}
	if isASCIIString(s) {
		if idx >= len(s) {
			return "", false
		}
		return s[idx : idx+1], true
	}
	u := utf16.Encode([]rune(s))
	if idx >= len(u) {
		return "", false
	}
	return string(utf16.Decode(u[idx : idx+1])), true
}

// CharCodeAtUTF16 按 UTF-16 码元索引取码元数值。
// 索引越界时 ok 返回 false (调用方应返回 NaN)。
func CharCodeAtUTF16(s string, idx int) (float64, bool) {
	if idx < 0 {
		return 0, false
	}
	if isASCIIString(s) {
		if idx >= len(s) {
			return 0, false
		}
		return float64(s[idx]), true
	}
	u := utf16.Encode([]rune(s))
	if idx >= len(u) {
		return 0, false
	}
	return float64(u[idx]), true
}

// SliceUTF16 按 UTF-16 码元区间 [start, end) 切片，返回 UTF-8 字符串。
func SliceUTF16(s string, start, end int) string {
	if isASCIIString(s) {
		if start < 0 {
			start = 0
		}
		if end > len(s) {
			end = len(s)
		}
		if start >= end {
			return ""
		}
		return s[start:end]
	}
	u := utf16.Encode([]rune(s))
	if start < 0 {
		start = 0
	}
	if end > len(u) {
		end = len(u)
	}
	if start >= end {
		return ""
	}
	return string(utf16.Decode(u[start:end]))
}

// SplitCharsUTF16 按 UTF-16 码元拆分字符串，等价于 JS 的 split("")。
// 与 Go 的 `for i, ch := range s` 不同，代理对会被拆成两个码元，
// 与 JavaScript 行为一致。
func SplitCharsUTF16(s string) []string {
	if isASCIIString(s) {
		out := make([]string, len(s))
		for i := 0; i < len(s); i++ {
			out[i] = s[i : i+1]
		}
		return out
	}
	u := utf16.Encode([]rune(s))
	out := make([]string, 0, len(u))
	for _, c := range u {
		out = append(out, string(utf16.Decode([]uint16{c})))
	}
	return out
}

// isASCIIString 判断 UTF-8 字符串是否全部由 ASCII 字符组成。
func isASCIIString(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// isASCIIUnits 判断 UTF-16 码元序列是否全部落在 ASCII 范围。
func isASCIIUnits(u []uint16) bool {
	for _, c := range u {
		if c >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// HasLoneSurrogates 检查字符串是否含有孤立代理项 (ES2024 isWellFormed)。
// 字符串以 UTF-8 存储，孤立代理项只可能来自 FromUTF16 的 U+FFFD 转换
// 之外 —— 即通过 fromCharCode 等途径拼出。检查按码元流进行。
func HasLoneSurrogates(s string) bool {
	u := ToUTF16(s)
	for i := 0; i < len(u); i++ {
		c := u[i]
		if c >= 0xD800 && c <= 0xDBFF { // 高代理项
			if i+1 >= len(u) {
				return true
			}
			next := u[i+1]
			if next < 0xDC00 || next > 0xDFFF {
				return true
			}
			i++ // 成对跳过
			continue
		}
		if c >= 0xDC00 && c <= 0xDFFF { // 孤立低代理项
			return true
		}
	}
	return false
}

// ReplaceLoneSurrogates 将孤立代理项替换为 U+FFFD (ES2024 toWellFormed)。
func ReplaceLoneSurrogates(s string) string {
	u := ToUTF16(s)
	changed := false
	for i := 0; i < len(u); i++ {
		c := u[i]
		if c >= 0xD800 && c <= 0xDBFF {
			if i+1 < len(u) {
				next := u[i+1]
				if next >= 0xDC00 && next <= 0xDFFF {
					i++
					continue
				}
			}
			u[i] = 0xFFFD
			changed = true
			continue
		}
		if c >= 0xDC00 && c <= 0xDFFF {
			u[i] = 0xFFFD
			changed = true
		}
	}
	if !changed {
		return s
	}
	return FromUTF16(u)
}

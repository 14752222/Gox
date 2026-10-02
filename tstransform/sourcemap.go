package tstransform

// sourcemap.go —— 转译行映射 (gen(JS) → src(TS))。
//
// 背景 (M2 P0-1): gox 引擎的 parser/compiler 只认 JS + JSX, .ts/.tsx 在入库前
// 被 esbuild 剥掉类型。剥离是**保行的**吗? 不总是 —— 类型注解单独占一行时会被
// 删掉 (行数不变但内容少了一截), 而 enum / namespace 这类构造会被 esbuild 展开
// 成多行辅助代码 (行数直接变多)。于是 VM 的字节码位置表 (记录的是转译后 JS 的
// 行号) 一旦直接拿去索引用户的 .ts 原文, 就会指到错误的行、读到错误的源码 ——
// 这比不显示源码帧更糟。所以必须把 JS 行号翻译回 TS 行号。
//
// 实现选型: 直接用 esbuild 的 Sourcemap 输出 (VLQ 编码的 mappings), 自己解码。
// 不引入 github.com/go-sourcemap/sourcemap 之类的依赖 —— 本项目硬约束是
// "零新第三方依赖", 而 VLQ 解码是 30 行的定长算法, 引入依赖不划算。
//
// 映射精度: 我们只需要"行级"定位 (错误行 + 该行第一个映射列)。一行的 mappings
// 里可能有多个段 (一行 JS 对应源里多个 token), 取 ≤ 目标列的最近段即可。

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// seg 是某条转译后行上的一个映射段。
type seg struct {
	genCol  int // 转译后 JS 的列 (0-based, 段起点)
	srcLine int // 源 .ts 的行 (1-based; 0 表示该段无源映射)
	srcCol  int // 源 .ts 的列 (1-based)
}

// LineMap 是"转译后 JS 行 → 源 TS 行列"的映射表。
//
// 行号一律 1-based (与用户看到的编辑器 / VM 位置表一致), 列 1-based。
// lines[i] 对应转译后第 i+1 行, 段按 genCol 升序。
type LineMap struct {
	lines [][]seg
	// sourceNames 仅用于诊断 (esbuild map 的 sources 字段), 一般就是文件自身。
	sourceNames []string
}

// IsEmpty 报告映射表是否可用 (没有任何有效段)。
func (m *LineMap) IsEmpty() bool {
	if m == nil {
		return true
	}
	for _, segs := range m.lines {
		for _, s := range segs {
			if s.srcLine != 0 {
				return false
			}
		}
	}
	return true
}

// MapPos 把转译后 JS 的 (genLine, genCol) 映射回源 .ts 的 (srcLine, srcCol)。
//
// genCol 用 1-based (与 parser 报的列一致); 内部映射段是 0-based, 这里统一换算。
// 若目标行没有映射段, 会向上回溯到最近有映射的行 —— 这是 source-map 消费端的
// 标准 find-greatest-lower-bound 行为, 优于"直接放弃"。仍然找不到时 ok=false,
// 调用方据此回退并标注, 绝不假装精确。
func (m *LineMap) MapPos(genLine, genCol int) (srcLine, srcCol int, ok bool) {
	if m == nil || genLine < 1 {
		return 0, 0, false
	}
	zeroCol := genCol - 1
	if zeroCol < 0 {
		zeroCol = 0
	}
	for l := genLine; l >= 1; l-- {
		if l-1 >= len(m.lines) {
			continue
		}
		segs := m.lines[l-1]
		if len(segs) == 0 {
			continue
		}
		best := -1
		if l == genLine {
			for i, s := range segs {
				if s.srcLine == 0 {
					continue
				}
				if s.genCol <= zeroCol {
					best = i
				} else {
					break
				}
			}
		}
		if best < 0 {
			// 回溯行: 取该行最后一个有效段 (genCol 限制已无意义)。
			for i := len(segs) - 1; i >= 0; i-- {
				if segs[i].srcLine != 0 {
					best = i
					break
				}
			}
		}
		if best >= 0 {
			s := segs[best]
			return s.srcLine, s.srcCol, true
		}
	}
	return 0, 0, false
}

// MapLine 只做行级映射 (拿不到列时的降级路径, 以及测试用)。
func (m *LineMap) MapLine(genLine int) (int, bool) {
	sl, _, ok := m.MapPos(genLine, 1)
	return sl, ok
}

// sourceLabel 返回映射表里记录的第一个源文件名 (诊断用)。
func (m *LineMap) sourceLabel() string {
	if m == nil || len(m.sourceNames) == 0 {
		return ""
	}
	return m.sourceNames[0]
}

// ── 解析 ────────────────────────────────────────────────────────────────────

// DecodeLineMap 解析 esbuild 产出的 sourcemap JSON, 返回行映射表。
// raw 为空 / 无 mappings 时返回 (nil, nil) —— 上层据此走"无映射"降级路径。
func DecodeLineMap(raw []byte) (*LineMap, error) {
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	var m struct {
		Sources  []string `json:"sources"`
		Mappings string   `json:"mappings"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("解析 sourcemap 失败: %w", err)
	}
	if m.Mappings == "" {
		return nil, nil
	}

	lm := &LineMap{sourceNames: m.Sources}
	// VLQ mappings 的游标: genCol 每行重置, srcIdx/srcLine/srcCol 跨段累加。
	// 这是 source map v3 规范定义的状态机, 不要把 srcLine 也按行重置。
	srcIdx, srcLine, srcCol := 0, 0, 0
	for _, lineStr := range strings.Split(m.Mappings, ";") {
		var segs []seg
		genCol := 0
		if lineStr != "" {
			for _, group := range strings.Split(lineStr, ",") {
				if group == "" {
					continue
				}
				vals, ok := decodeVLQGroup(group)
				if !ok || len(vals) < 1 {
					continue
				}
				genCol += vals[0]
				if len(vals) >= 4 {
					srcIdx += vals[1]
					srcLine += vals[2]
					srcCol += vals[3]
					segs = append(segs, seg{genCol: genCol, srcLine: srcLine + 1, srcCol: srcCol + 1})
					_ = srcIdx
				} else {
					// 只有 genCol 的段: 该位置在源里没有对应 token (esbuild 插入的
					// 合成代码)。记为无源映射, MapPos 会跳过它。
					segs = append(segs, seg{genCol: genCol})
				}
			}
		}
		lm.lines = append(lm.lines, segs)
	}
	return lm, nil
}

// decodeVLQGroup 解出一组 VLQ 字段 (逗号分隔的一段里的全部值)。
func decodeVLQGroup(group string) ([]int, bool) {
	var vals []int
	pos := 0
	for pos < len(group) {
		v, next, ok := decodeVLQ(group, pos)
		if !ok {
			return nil, false
		}
		vals = append(vals, v)
		pos = next
	}
	return vals, true
}

// base64VLQ 字母表 (RFC 4648 base64, source map v3 用它做 VLQ 编码)。
const base64VLQ = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// base64Rev 是 base64VLQ 的反查表; 非法字符为 -1。
var base64Rev = func() [256]int8 {
	var t [256]int8
	for i := range t {
		t[i] = -1
	}
	for i := 0; i < len(base64VLQ); i++ {
		t[base64VLQ[i]] = int8(i)
	}
	return t
}()

// decodeVLQ 从 s[pos] 起解码一个 VLQ 值。
//
// 编码规则: 每 6 bit 一组, 最低位是"是否继续", 次低位是符号, 其余是数值 ——
// 全部字节转成 5 bit 有效数据 (base64 的 6 bit 去掉继续位) 后小端拼接。
func decodeVLQ(s string, pos int) (value, next int, ok bool) {
	result := 0
	shift := 0
	for pos < len(s) {
		c := s[pos]
		d := base64Rev[c]
		if d < 0 {
			return 0, pos, false
		}
		pos++
		digit := int(d & 31)
		result += digit << shift
		shift += 5
		if d&32 == 0 {
			// 收尾: 最低位是符号
			negative := result&1 != 0
			result >>= 1
			if negative {
				result = -result
			}
			return result, pos, true
		}
	}
	return 0, pos, false
}

// ── 错误消息重写 (尽力而为) ─────────────────────────────────────────────────

// lineRefRe 匹配 parser 错误里 "line 12:5" 形式的行号引用 (见 parser/errors.go)。
var lineRefRe = regexp.MustCompile(`line (\d+):(\d+)`)

// RemapMessage 把错误消息里的 JS 行号引用改写成 .ts 行号。
//
// 用途: parser / compiler 在**转译后 JS** 上报错 (如引擎 parser 不支持某个 JS
// 构造)。这类消息带的是 JS 行号, 直接给用户会指到错误位置。这里做逐条重写:
//   - 能映射的: 替换为 TS 行号, 并追加 `(mapped to .ts)`;
//   - 不能映射的: 原样保留, 整体追加一句"部分行号未能映射"。
//
// 纯 JS 文件不会走到这里 (调用方只在有 LineMap 时调用), 因此不会误改 JS 报错。
func (m *LineMap) RemapMessage(msg string) string {
	if m == nil || m.IsEmpty() {
		return msg
	}
	unmapped := false
	out := lineRefRe.ReplaceAllStringFunc(msg, func(s string) string {
		sub := lineRefRe.FindStringSubmatch(s)
		gl, _ := strconv.Atoi(sub[1])
		gc, _ := strconv.Atoi(sub[2])
		if sl, sc, ok := m.MapPos(gl, gc); ok {
			return fmt.Sprintf("line %d:%d (mapped to .ts)", sl, sc)
		}
		unmapped = true
		return s + " (JS line, 未能映射到 .ts)"
	})
	if out != msg {
		out += "\n  (注意: 上述位置由转译后 JS 映射回 TypeScript 源码)"
		if unmapped {
			out += "\n  (部分行号未能映射, 已保留转译后 JS 行号)"
		}
	}
	return out
}

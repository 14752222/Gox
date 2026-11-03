package object

// RegExp v 标志 (unicodeSets, ES2024) 类集合语法 → RE2 翻译器。
//
// v 模式在字符类里引入了 u 模式没有的一整套语法 (spec: sec-character-class):
//   - 嵌套类:      [[a-z][0-9]]            (并集)
//   - 减法:        [[a-z]--[aeiou]]
//   - 交集:        [[\p{L}]&&[\p{Lu}]]
//   - 字符串字面量: [\q{ab|cd}]  (写作 \q{ab,cd})
//   - 保留标点:    ( ) [ ] { } / - $ | 在类内必须转义
//   - 保留双标点:  && 之外 (&&(合法操作符) 其余 !! ## $$ %% ** ++ ,, .. :: ;; << == >> ?? @@ ^^ `` ~~ 一律 SyntaxError
//   - 否定类:      [^ ...] 不得含字符串操作数
//
// RE2 无上述任何能力，故本翻译器把 v 模式字符类**物化**成显式码点区间
// (复用 \p 属性的 RangeTable 物化机制, 见 propMaterialize/propRangeTableByName):
//   - 含字符串的类展开为 (?:[区间]|串1|串2) 交替;
//   - 交集/减法在区间域上直接计算;
//   - 否定类在区间域上取补 (0..0x10FFFF, 代理区按 JS 串可含码元保留)。
//
// 全部物化会牺牲一点模式长度 (大属性如 Script=Han 展开成数千区间), 换来的是
// RE2 原生语义下的正确求值 —— property-escapes 批次 (rFXXbq batch2) 已验证该
// 策略在 test262 上的可行性。

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// csRange 闭区间码点范围。
type csRange struct{ lo, hi rune }

// csSet 类集合求值结果 (并集语义): 区间集 + 字符串字面量集。
type csSet struct {
	ranges  []csRange
	strings []string
}

func (s *csSet) addRange(lo, hi rune) { s.ranges = append(s.ranges, csRange{lo, hi}) }
func (s *csSet) addSet(o *csSet) {
	s.ranges = append(s.ranges, o.ranges...)
	s.strings = append(s.strings, o.strings...)
}

// csSortMerge 区间排序合并 (并集规范化)。
func csSortMerge(r []csRange) []csRange {
	if len(r) == 0 {
		return nil
	}
	sort.Slice(r, func(i, j int) bool { return r[i].lo < r[j].lo })
	out := []csRange{r[0]}
	for _, x := range r[1:] {
		if x.lo <= out[len(out)-1].hi+1 {
			if x.hi > out[len(out)-1].hi {
				out[len(out)-1].hi = x.hi
			}
		} else {
			out = append(out, x)
		}
	}
	return out
}

func (s *csSet) normalize() {
	s.ranges = csSortMerge(s.ranges)
}

// csComplement 在 [0, 0x10FFFF] 上取补 (代理区保留: JS 串可含码元)。
func csComplement(r []csRange) []csRange {
	r = csSortMerge(r)
	var comp []csRange
	prev := rune(-1)
	for _, x := range r {
		if x.lo > prev+1 {
			comp = append(comp, csRange{prev + 1, x.lo - 1})
		}
		if x.hi > prev {
			prev = x.hi
		}
	}
	if prev < 0x10FFFF {
		comp = append(comp, csRange{prev + 1, 0x10FFFF})
	}
	return comp
}

// csIntersect 两个已规范化区间集的交集。
func csIntersect(a, b []csRange) []csRange {
	var out []csRange
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		lo := a[i].lo
		if b[j].lo > lo {
			lo = b[j].lo
		}
		hi := a[i].hi
		if b[j].hi < hi {
			hi = b[j].hi
		}
		if lo <= hi {
			out = append(out, csRange{lo, hi})
		}
		if a[i].hi < b[j].hi {
			i++
		} else {
			j++
		}
	}
	return out
}

// csSubtract a - b (两者均已规范化): a ∩ 补(b)。
func csSubtract(a, b []csRange) []csRange {
	return csIntersect(a, csComplement(b))
}

// setDifference 类集合差 (spec 13.3.8 ClassSetSubtraction, 含字符串操作数):
// 单字符字符串视作字符参与区间运算; 多字符字符串按全串相等移除。
func setDifference(a, b *csSet) *csSet {
	bChars := append([]csRange{}, b.ranges...)
	for _, s := range b.strings {
		if len([]rune(s)) == 1 {
			r := []rune(s)[0]
			bChars = append(bChars, csRange{r, r})
		}
	}
	out := &csSet{ranges: csSubtract(a.ranges, csSortMerge(bChars))}
	bSet := map[string]bool{}
	for _, s := range b.strings {
		bSet[s] = true
	}
	for _, s := range a.strings {
		if !bSet[s] {
			out.strings = append(out.strings, s)
		}
	}
	return out
}

// setIntersect 类集合交 (含字符串操作数): 区间与 b 的字符/区间求交;
// 字符串按全串相等保留。
func setIntersect(a, b *csSet) *csSet {
	// 双侧对称物化: 任一侧的单字符字符串先降格为码点再进区间域求交,
	// 只有多字符字符串保留在字符串域做精确匹配。此前只对右侧做降格,
	// [\q{0|2|4|9️⃣}&&\d] 这类"字符串在左、字符类在右"的交集被错误求空。
	aChars := append([]csRange{}, a.ranges...)
	var aStrs []string
	for _, s := range a.strings {
		if r := []rune(s); len(r) == 1 {
			aChars = append(aChars, csRange{r[0], r[0]})
		} else {
			aStrs = append(aStrs, s)
		}
	}
	bChars := append([]csRange{}, b.ranges...)
	bSet := map[string]bool{}
	for _, s := range b.strings {
		if r := []rune(s); len(r) == 1 {
			bChars = append(bChars, csRange{r[0], r[0]})
		} else {
			bSet[s] = true
		}
	}
	out := &csSet{ranges: csIntersect(csSortMerge(aChars), csSortMerge(bChars))}
	for _, s := range aStrs {
		if bSet[s] {
			out.strings = append(out.strings, s)
		}
	}
	return out
}

// csEmitRanges 把区间集物化为 RE2 字符类体 (不含方括号)。
func csEmitRanges(r []csRange) string {
	var b strings.Builder
	for _, x := range r {
		if x.lo == x.hi {
			fmt.Fprintf(&b, `\x{%X}`, x.lo)
		} else {
			fmt.Fprintf(&b, `\x{%X}-\x{%X}`, x.lo, x.hi)
		}
	}
	return b.String()
}

// csEmit 把求值结果物化为完整 RE2 片段。
func csEmit(s *csSet, negated bool) (string, error) {
	if negated {
		if len(s.strings) > 0 {
			return "", fmt.Errorf("negated character class may not contain strings")
		}
		comp := csComplement(s.ranges)
		if len(comp) == 0 {
			return `[^\s\S]`, nil
		}
		return "[" + csEmitRanges(comp) + "]", nil
	}
	if len(s.strings) > 0 {
		// (?:[区间]|串1|串2); 区间为空则只有串
		var b strings.Builder
		b.WriteString("(?:")
		first := true
		if len(s.ranges) > 0 {
			b.WriteString("[" + csEmitRanges(s.ranges) + "]")
			first = false
		}
		for _, str := range s.strings {
			if !first {
				b.WriteByte('|')
			}
			first = false
			b.WriteString(csQuoteLiteral(str))
		}
		b.WriteString(")")
		return b.String(), nil
	}
	if len(s.ranges) == 0 {
		return `[^\s\S]`, nil // 空类匹配空集
	}
	return "[" + csEmitRanges(s.ranges) + "]", nil
}

// csQuoteLiteral 把字符串字面量转成 RE2 字面 (只转正则元字符)。
func csQuoteLiteral(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch c {
		case '\\', '.', '+', '*', '?', '(', ')', '|', '[', ']', '{', '}', '^', '$':
			b.WriteByte('\\')
		}
		b.WriteRune(c)
	}
	return b.String()
}

// ===== 语法解析 =====

// csParser v 模式类集合解析器。
type csParser struct {
	runes []rune
	i     int
}

var csErrSyntax = fmt.Errorf("invalid class set syntax")

// csIsDoublePunct 保留双标点字符集 (spec ReservedDoublePunctuators):
// 同一字符连续出现两次即 SyntaxError —— && 例外 (交集操作符, 已在
// parseClassSetExpr/parseNested 的操作符分派处消费, 走不到这里)。
// 字符集: ! # $ % * + , . : ; < = > ? @ ^ ` ~ & (取自规范双标点全表去重)。
func csIsDoublePunct(c rune) bool {
	switch c {
	case '!', '#', '$', '%', '*', '+', ',', '.', ':', ';',
		'<', '=', '>', '?', '@', '^', '`', '~', '&':
		return true
	}
	return false
}

// csIsReservedPunct v 模式类内必须转义的保留标点 (spec ClassSetReservedPunctuator)。
func csIsReservedPunct(c rune) bool {
	switch c {
	case '(', ')', '[', ']', '{', '}', '/', '-', '$', '|':
		return true
	}
	return false
}

// translateVModeClasses 扫描整个 v 标志模式, 把每个类集合表达式物化为 RE2。
// 非 class 区域原样保留 (转义序列跳过, 不误伤 \[ )。
func translateVModeClasses(pattern string) (string, error) {
	p := &csParser{runes: []rune(pattern)}
	var b strings.Builder
	for p.i < len(p.runes) {
		c := p.runes[p.i]
		if c == '\\' {
			// 非类区域: 原样保留转义 (含转义的反斜杠)
			b.WriteRune(c)
			if p.i+1 < len(p.runes) {
				b.WriteRune(p.runes[p.i+1])
				p.i += 2
				continue
			}
			p.i++
			continue
		}
		if c != '[' {
			b.WriteRune(c)
			p.i++
			continue
		}
		p.i++ // 消费 '['
		re, err := p.parseClassSetExpr()
		if err != nil {
			return "", err
		}
		b.WriteString(re)
	}
	return b.String(), nil
}

// parseClassSetExpr 解析一个字符类 (调用方已消费 '['; 本函数消费到匹配 ']')。
func (p *csParser) parseClassSetExpr() (string, error) {
	negated := false
	if p.i < len(p.runes) && p.runes[p.i] == '^' {
		negated = true
		p.i++
	}
	res, err := p.parseUnionOperand()
	if err != nil {
		return "", err
	}
	// 操作符左结合: ([a]∩[b])--[c]
	for p.i < len(p.runes) {
		if p.runes[p.i] == ']' {
			p.i++
			return csEmit(res, negated)
		}
		if p.runes[p.i] == '-' && p.i+1 < len(p.runes) && p.runes[p.i+1] == '-' {
			p.i += 2
			rhs, err2 := p.parseUnionOperand()
			if err2 != nil {
				return "", err2
			}
			res = setDifference(res, rhs)
			continue
		}
		if p.runes[p.i] == '&' && p.i+1 < len(p.runes) && p.runes[p.i+1] == '&' {
			p.i += 2
			rhs, err2 := p.parseUnionOperand()
			if err2 != nil {
				return "", err2
			}
			res = setIntersect(res, rhs)
			continue
		}
		// 类内裸露的非法字符 (含单 &, 保留标点等)
		return "", csErrSyntax
	}
	return "", csErrSyntax // 未闭合
}

// parseUnionOperand 解析一个操作数: 原子序列 (并集), 含区间 a-b。
func (p *csParser) parseUnionOperand() (*csSet, error) {
	acc := &csSet{}
	for p.i < len(p.runes) {
		c := p.runes[p.i]
		if c == ']' {
			break
		}
		if c == '-' && p.i+1 < len(p.runes) && p.runes[p.i+1] == '-' {
			break // 减法操作符, 归上层
		}
		if c == '&' && p.i+1 < len(p.runes) && p.runes[p.i+1] == '&' {
			break // 交集操作符, 归上层
		}
		if c == '[' {
			p.i++ // 消费 '['
			nested, err := p.parseNested()
			if err != nil {
				return nil, err
			}
			acc.addSet(nested)
			continue
		}
		atom, err := p.parseAtom()
		if err != nil {
			return nil, err
		}
		// 区间尝试: 单字符原子 + '-' + 单字符原子
		if lo, ok := atomAsRune(atom); ok {
			if p.i+1 < len(p.runes) && p.runes[p.i] == '-' &&
				p.runes[p.i+1] != '-' && p.runes[p.i+1] != ']' {
				save := p.i
				p.i++ // 消费 '-'
				hiAtom, err2 := p.parseAtom()
				if err2 != nil {
					return nil, err2
				}
				if hi, ok2 := atomAsRune(hiAtom); ok2 {
					if hi < lo {
						return nil, csErrSyntax // 区间乱序
					}
					acc.addRange(lo, hi)
					continue
				}
				// 右端点非单字符 (如 a-\p{L}): u 模式已禁, v 模式同样 SyntaxError
				_ = save
				return nil, csErrSyntax
			}
			acc.addRange(lo, lo)
			continue
		}
		acc.addSet(atom)
	}
	// v 模式操作数不得为空 (ClassSetOperand 至少一个成员):
	// [&&] / [--] / [] / [a--] 等空形态一律 SyntaxError
	if len(acc.ranges) == 0 && len(acc.strings) == 0 {
		return nil, csErrSyntax
	}
	acc.normalize()
	return acc, nil
}

// parseNested 嵌套类: 调用方已消费 '[', 返回集合。与 parseClassSetExpr 相同
// 语法, 但返回集合 (供集合运算), 并要求内部不得在嵌套层出现顶层才合法的形态。
func (p *csParser) parseNested() (*csSet, error) {
	negated := false
	if p.i < len(p.runes) && p.runes[p.i] == '^' {
		negated = true
		p.i++
	}
	res, err := p.parseUnionOperand()
	if err != nil {
		return nil, err
	}
	for p.i < len(p.runes) {
		if p.runes[p.i] == ']' {
			p.i++
			if negated {
				if len(res.strings) > 0 {
					return nil, csErrSyntax
				}
				return &csSet{ranges: csComplement(res.ranges)}, nil
			}
			return res, nil
		}
		if p.runes[p.i] == '-' && p.i+1 < len(p.runes) && p.runes[p.i+1] == '-' {
			p.i += 2
			rhs, err2 := p.parseUnionOperand()
			if err2 != nil {
				return nil, err2
			}
			if len(res.strings) > 0 || len(rhs.strings) > 0 {
				return nil, csErrSyntax
			}
			res = setDifference(res, rhs)
			continue
		}
		if p.runes[p.i] == '&' && p.i+1 < len(p.runes) && p.runes[p.i+1] == '&' {
			p.i += 2
			rhs, err2 := p.parseUnionOperand()
			if err2 != nil {
				return nil, err2
			}
			if len(res.strings) > 0 || len(rhs.strings) > 0 {
				return nil, csErrSyntax
			}
			res = setIntersect(res, rhs)
			continue
		}
		return nil, csErrSyntax
	}
	return nil, csErrSyntax
}

// atomAsRune 单字符原子 → 其码点。
func atomAsRune(s *csSet) (rune, bool) {
	if len(s.strings) != 0 || len(s.ranges) != 1 || s.ranges[0].lo != s.ranges[0].hi {
		return 0, false
	}
	return s.ranges[0].lo, true
}

// parseAtom 解析单个原子: 转义序列 / 嵌套类 / 字面字符。
func (p *csParser) parseAtom() (*csSet, error) {
	c := p.runes[p.i]
	switch c {
	case '[':
		p.i++
		return p.parseNested()
	case '\\':
		if p.i+1 >= len(p.runes) {
			return nil, csErrSyntax
		}
		e := p.runes[p.i+1]
		p.i += 2
		switch e {
		case 'p', 'P':
			if p.i >= len(p.runes) || p.runes[p.i] != '{' {
				return nil, csErrSyntax
			}
			end := indexRune(p.runes, p.i, '}')
			if end < 0 {
				return nil, csErrSyntax
			}
			content := string(p.runes[p.i+1 : end])
			p.i = end + 1
			// 属性为字符串集 (property of strings): \P 取补非法, \p 返回串集
			cn := propCanonicalName(content)
			if strings.IndexByte(content, '=') < 0 {
				if strs, ok := propOfStringSequences[cn]; ok {
					if e == 'P' {
						return nil, csErrSyntax
					}
					return &csSet{strings: strs}, nil
				}
			}
			r, ok := propRangesForEscape(e == 'P', content)
			if !ok {
				return nil, csErrSyntax
			}
			return &csSet{ranges: r}, nil
		case 'q':
			// 字符串字面量 \q{a,bc,...} —— 仅 v 模式
			if p.i >= len(p.runes) || p.runes[p.i] != '{' {
				return nil, csErrSyntax
			}
			end := indexRune(p.runes, p.i, '}')
			if end < 0 {
				return nil, csErrSyntax
			}
			body := string(p.runes[p.i+1 : end])
			p.i = end + 1
			return p.parseQString(body)
		case 'd':
			return singleRangesSet([]csRange{{'0', '9'}}), nil
		case 'w':
			return singleRangesSet([]csRange{{'0', '9'}, {'A', 'Z'}, {'_', '_'}, {'a', 'z'}}), nil
		case 's':
			return singleRangesSet(jsSpaceRanges()), nil
		case 'n':
			return runeSet('\n'), nil
		case 'r':
			return runeSet('\r'), nil
		case 't':
			return runeSet('\t'), nil
		case 'f':
			return runeSet('\f'), nil
		case 'v':
			return runeSet(0x0b), nil
		case '0':
			return runeSet(0), nil
		case 'b':
			return runeSet(0x08), nil // 类内 \b 是退格
		case 'c':
			if p.i < len(p.runes) {
				// \cX: 控制字符 X & 0x1F
				ctrl := p.runes[p.i] & 0x1F
				p.i++
				return runeSet(ctrl), nil
			}
			return nil, csErrSyntax
		case 'x':
			if p.i < len(p.runes) && p.runes[p.i] == '{' {
				end := indexRune(p.runes, p.i, '}')
				if end < 0 {
					return nil, csErrSyntax
				}
				r, ok := parseHexRun(string(p.runes[p.i+1 : end]))
				if !ok || r > 0x10FFFF {
					return nil, csErrSyntax
				}
				p.i = end + 1
				return runeSet(r), nil
			}
			if p.i+1 < len(p.runes) {
				r, ok := parseHexRun(string(p.runes[p.i : p.i+2]))
				if !ok {
					return nil, csErrSyntax
				}
				p.i += 2
				return runeSet(r), nil
			}
			return nil, csErrSyntax
		case 'u':
			if p.i < len(p.runes) && p.runes[p.i] == '{' {
				end := indexRune(p.runes, p.i, '}')
				if end < 0 {
					return nil, csErrSyntax
				}
				r, ok := parseHexRun(string(p.runes[p.i+1 : end]))
				if !ok || r > 0x10FFFF {
					return nil, csErrSyntax
				}
				p.i = end + 1
				return runeSet(r), nil
			}
			if p.i+3 < len(p.runes) {
				r, ok := parseHexRun(string(p.runes[p.i : p.i+4]))
				if !ok {
					return nil, csErrSyntax
				}
				p.i += 4
				return runeSet(r), nil
			}
			return nil, csErrSyntax
		case '-':
			return runeSet('-'), nil
		case '&':
			return runeSet('&'), nil
		default:
			// 保留标点转义合法; 标识符字符转义非法 (spec: v 模式无 \a \e 等)
			if csIsReservedPunct(e) || !isWordRune(e) {
				return runeSet(e), nil
			}
			return nil, csErrSyntax
		}
	default:
		if csIsReservedPunct(c) {
			return nil, csErrSyntax // 保留标点必须转义
		}
		// 保留双标点 (spec ReservedDoublePunctuators): 连续两个同字符一律
		// SyntaxError —— breaking-change-from-u-to-v 族 (u 模式合法字面量
		// 在 v 模式必须早报错)。&& 已作交集操作符在调用方消费, 走不到这里。
		if csIsDoublePunct(c) && p.i+1 < len(p.runes) && p.runes[p.i+1] == c {
			return nil, csErrSyntax
		}
		p.i++
		return runeSet(c), nil
	}
}

// parseQString \q{...} 体: ClassStringDisjunction, 以未转义的 '|' 分隔
// 各字符串字面量, 每段至少 1 字符。段内保留标点同样必须转义。
func (p *csParser) parseQString(body string) (*csSet, error) {
	if body == "" {
		return nil, csErrSyntax
	}
	out := &csSet{}
	for _, part := range splitUnescaped(body, '|') {
		if part == "" {
			return nil, csErrSyntax
		}
		runes := []rune(part)
		var sb strings.Builder
		for i := 0; i < len(runes); i++ {
			c := runes[i]
			if c == '\\' {
				if i+1 >= len(runes) {
					return nil, csErrSyntax
				}
				e := runes[i+1]
				i++
				switch e {
				case 'n':
					sb.WriteRune('\n')
					continue
				case 'r':
					sb.WriteRune('\r')
					continue
				case 't':
					sb.WriteRune('\t')
					continue
				case 'f':
					sb.WriteRune('\f')
					continue
				case 'v':
					sb.WriteRune(0x0b)
					continue
				case '0':
					sb.WriteRune(0)
					continue
				case 'b':
					sb.WriteRune(0x08)
					continue
				case 'c':
					if i+1 < len(runes) {
						sb.WriteRune(runes[i+1] & 0x1F)
						i++
						continue
					}
					return nil, csErrSyntax
				case 'x':
					// \xHH 或 \x{HHHH}
					if i+1 < len(runes) && runes[i+1] == '{' {
						j := i + 2
						for j < len(runes) && runes[j] != '}' {
							j++
						}
						if j >= len(runes) {
							return nil, csErrSyntax
						}
						r, ok := parseHexRun(string(runes[i+2 : j]))
						if !ok || r > 0x10FFFF {
							return nil, csErrSyntax
						}
						sb.WriteRune(r)
						i = j
						continue
					}
					if i+2 < len(runes) {
						r, ok := parseHexRun(string(runes[i+1 : i+3]))
						if !ok {
							return nil, csErrSyntax
						}
						sb.WriteRune(r)
						i += 2
						continue
					}
					return nil, csErrSyntax
				case 'u':
					// \uHHHH 或 \u{HHHH}
					if i+1 < len(runes) && runes[i+1] == '{' {
						j := i + 2
						for j < len(runes) && runes[j] != '}' {
							j++
						}
						if j >= len(runes) {
							return nil, csErrSyntax
						}
						r, ok := parseHexRun(string(runes[i+2 : j]))
						if !ok || r > 0x10FFFF {
							return nil, csErrSyntax
						}
						sb.WriteRune(r)
						i = j
						continue
					}
					if i+4 < len(runes) {
						r, ok := parseHexRun(string(runes[i+1 : i+5]))
						if !ok {
							return nil, csErrSyntax
						}
						sb.WriteRune(r)
						i += 4
						continue
					}
					return nil, csErrSyntax
				default:
					if csIsReservedPunct(e) || !isWordRune(e) {
						sb.WriteRune(e)
						continue
					}
					return nil, csErrSyntax
				}
			}
			if csIsReservedPunct(c) {
				return nil, csErrSyntax
			}
			sb.WriteRune(c)
		}
		out.strings = append(out.strings, sb.String())
	}
	return out, nil
}

// splitUnescaped 按 sep 切分, 忽略反斜杠转义的 sep。
func splitUnescaped(s string, sep rune) []string {
	var parts []string
	var cur strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\\' && i+1 < len(runes) {
			cur.WriteRune('\\')
			cur.WriteRune(runes[i+1])
			i++
			continue
		}
		if runes[i] == sep {
			parts = append(parts, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteRune(runes[i])
	}
	parts = append(parts, cur.String())
	return parts
}

func runeSet(c rune) *csSet              { return &csSet{ranges: []csRange{{c, c}}} }
func singleRangesSet(r []csRange) *csSet { return &csSet{ranges: r} }

func isWordRune(c rune) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func indexRune(rs []rune, from int, target rune) int {
	for i := from + 1; i < len(rs); i++ {
		if rs[i] == target {
			return i
		}
	}
	return -1
}

func parseHexRun(s string) (rune, bool) {
	if s == "" || len(s) > 6 {
		return 0, false
	}
	v := rune(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			v = v*16 + rune(c-'0')
		case c >= 'a' && c <= 'f':
			v = v*16 + rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			v = v*16 + rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	return v, true
}

// jsSpaceRanges JS \s 的码点区间。
func jsSpaceRanges() []csRange {
	return []csRange{
		{0x09, 0x0d}, {0x20, 0x20}, {0xa0, 0xa0},
		{0x1680, 0x1680}, {0x2000, 0x200a},
		{0x2028, 0x2029}, {0x202f, 0x202f}, {0x205f, 0x205f},
		{0x3000, 0x3000}, {0xfeff, 0xfeff},
	}
}

// propRangesForEscape 属性转义的区间物化 (供 v 模式类集合运算)。
// 与 translateOnePropertyEscape 同一套白名单/规范化, 但返回区间集而非 RE2 文本。
func propRangesForEscape(negated bool, content string) ([]csRange, bool) {
	for i := 0; i < len(content); i++ {
		if content[i] == ' ' || content[i] == '\t' || content[i] == '\n' || content[i] == '\r' {
			return nil, false
		}
	}
	name, value := content, ""
	hasValue := false
	if idx := strings.IndexByte(content, '='); idx >= 0 {
		name, value = content[:idx], content[idx+1:]
		hasValue = true
	}
	cname := propCanonicalName(name)

	var tables []*unicode.RangeTable
	special := ""

	if hasValue {
		if value == "" {
			return nil, false
		}
		lv := propCanonicalValue(value)
		switch cname {
		case "GeneralCategory", "gc":
			short, ok := propGCLongToShortNounder[lv]
			if !ok {
				short, ok = propGCShort[lv]
			}
			if !ok {
				return nil, false
			}
			if short == "LC" {
				tables = []*unicode.RangeTable{unicode.Lu, unicode.Ll, unicode.Lt}
			} else if short == "Cn" {
				// 全空间减所有已分配类别: 先收集已分配, 最后取补
				for _, rt := range unicode.Categories {
					tables = append(tables, rt)
				}
				negated = !negated // Cn = complement(分配类别)
			} else {
				rt := propRangeTableByName(short)
				if rt == nil {
					return nil, false
				}
				tables = []*unicode.RangeTable{rt}
			}
		case "Script", "sc", "ScriptExtensions", "scx":
			sn, ok := propScriptValueName(value)
			if !ok {
				return nil, false
			}
			rt, ok2 := unicode.Scripts[sn]
			if !ok2 {
				return nil, false
			}
			tables = []*unicode.RangeTable{rt}
		default:
			return nil, false // v 模式 \p 仅 GC/Script 支持带值形态
		}
	} else {
		switch cname {
		case "Any":
			special = "any"
		case "ASCII":
			special = "ascii"
		case "Assigned":
			special = "assigned"
		default:
			// 无值形态: 二元属性 / GC / Script
			if rt, ok := propBinaryTable(name); ok {
				tables = []*unicode.RangeTable{rt}
			} else if keys, ok := propDerivedBinary[propNounder(name)]; ok {
				for _, k := range keys {
					if rt := propRangeTableByName(k); rt != nil {
						tables = append(tables, rt)
					}
				}
				if len(tables) == 0 {
					return nil, false
				}
			} else if short, ok := propGCLongToShortNounder[strings.ToLower(propCanonicalName(name))]; ok {
				if short == "LC" {
					tables = []*unicode.RangeTable{unicode.Lu, unicode.Ll, unicode.Lt}
				} else {
					rt := propRangeTableByName(short)
					if rt == nil {
						return nil, false
					}
					tables = []*unicode.RangeTable{rt}
				}
			} else if short, ok := propGCShort[strings.ToLower(propCanonicalName(name))]; ok {
				if short == "LC" {
					tables = []*unicode.RangeTable{unicode.Lu, unicode.Ll, unicode.Lt}
				} else {
					rt := propRangeTableByName(short)
					if rt == nil {
						return nil, false
					}
					tables = []*unicode.RangeTable{rt}
				}
			} else if sn, ok := propScriptName(name); ok {
				rt, ok2 := unicode.Scripts[sn]
				if !ok2 {
					return nil, false
				}
				tables = []*unicode.RangeTable{rt}
			} else {
				return nil, false
			}
		}
	}

	var ranges []csRange
	switch special {
	case "any":
		ranges = []csRange{{0, 0x10FFFF}}
	case "ascii":
		ranges = []csRange{{0, 0x7F}}
	case "assigned":
		ranges = []csRange{{0, 0x10FFFF}} // 代理码元语义近似
	default:
		for _, t := range tables {
			ranges = append(ranges, rangeTableRanges(t)...)
		}
	}
	if negated {
		ranges = csComplement(ranges)
	}
	return csSortMerge(ranges), true
}

// rangeTableRanges RangeTable → 区间集 (Stride>1 逐点展开)。
func rangeTableRanges(t *unicode.RangeTable) []csRange {
	var out []csRange
	for _, r := range t.R16 {
		lo, hi, st := rune(r.Lo), rune(r.Hi), rune(r.Stride)
		if st <= 1 {
			out = append(out, csRange{lo, hi})
			continue
		}
		for c := lo; c <= hi; c += st {
			out = append(out, csRange{c, c})
		}
	}
	for _, r := range t.R32 {
		lo, hi, st := rune(r.Lo), rune(r.Hi), rune(r.Stride)
		if st <= 1 {
			out = append(out, csRange{lo, hi})
			continue
		}
		for c := lo; c <= hi; c += st {
			out = append(out, csRange{c, c})
		}
	}
	return out
}

// propOfStringSequences 属性为字符串集 (properties of strings) 的已知实现。
// v 模式下 \p{...} 可求值为多码点字符串集合 (ClassSetOperand → 字符串)。
// 只收录数据量可控且能精确物化的属性; RGI_Emoji 族 (~3600 串) 暂缺。
var propOfStringSequences = map[string][]string{
	"EmojiKeycapSequence": func() []string {
		var out []string
		for _, k := range []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "#", "*"} {
			out = append(out, k+"\uFE0F\u20E3")
		}
		return out
	}(),
}

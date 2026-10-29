package object

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// RegExp 表示 JavaScript 的正则表达式对象。
//
// 除正则本体外, 实例还承载**自有属性存储** (PropDescs): 规范形态的自有属性
// 只有 lastIndex ({value, writable:true, enumerable:false, configurable:false}),
// 但用户可以在实例上 defineProperty 任意键 —— test262 正是靠「把 lastIndex 改
// 成不可写」来断言 @@match / @@replace 里的 Set(R,"lastIndex",v,true) 会抛
// TypeError。此前 lastIndex 只是裸结构体字段, 无从表达「不可写」, 于是
// 这 8 个用例全是假阳性通过 (详见 stdlib/regexp.go 的注释与 rEXjyz)。
type RegExp struct {
	Pattern    string         // 原始正则表达式模式
	Flags      string         // 标志位 (g, i, m, s, u, y)
	Regexp     *regexp.Regexp // Go 编译后的正则
	Global     bool           // g 标志
	IgnoreCase bool           // i 标志
	Multiline  bool           // m 标志
	DotAll     bool           // s 标志
	Sticky     bool           // y 标志 (粘性匹配)
	Unicode    bool           // u 标志
	HasIndices bool           // d 标志 (匹配位置索引)
	mu         sync.Mutex
	LastIndex  int // 上次匹配位置 (g/y 标志使用)

	// PropDescs 承载 defineProperty / 赋值落在实例上的自有属性描述符。
	// lastIndex 的规范描述符是懒建 (见 ownLastIndexDescriptor)。
	PropDescs map[string]PropertyDescriptor
	// propKeyOrder 记录 defineProperty 定义键的创建顺序 (OwnKeys 口径)。
	propKeyOrder []string
}

func (r *RegExp) Type() ObjectType { return REGEXP_OBJ }
func (r *RegExp) Inspect() string {
	return fmt.Sprintf("/%s/%s", r.Pattern, r.Flags)
}
func (r *RegExp) IsTruthy() bool { return true }

// GetProto 返回实例的 [[Prototype]] = %RegExp.prototype%。
//
// 原型链遍历的事实来源是鸭子类型入口 `interface{ GetProto() Value }`。此前
// *RegExp 缺这个方法, 链在实例处断掉 —— 即便 RegExpProto 已装配, 经链式
// 查找 (symbol 成员 / getPrototypeOf) 的读法仍取不到 exec / @@match 等成员。
func (r *RegExp) GetProto() Value { return RegExpProto }

// ownLastIndexDescriptor 返回 lastIndex 的规范自有描述符:
// {writable:true, enumerable:false, configurable:false}。
func (r *RegExp) ownLastIndexDescriptor() PropertyDescriptor {
	return PropertyDescriptor{
		Value:        NewNumber(float64(r.LastIndex)),
		Writable:     true,
		Enumerable:   false,
		Configurable: false,
	}
}

func (r *RegExp) GetProperty(name string) (Value, bool) {
	// (1) 显式定义过的自有属性: 访问器调 getter (this = 实例), 数据属性取
	// 描述符里的值。r.exec = fn 也落在这里 (SetProperty 走 DefineOwn)。
	if d, ok := r.PropDescs[name]; ok && !d.Deleted {
		if acc, isAcc := d.Value.(*Accessor); isAcc {
			if acc.Getter != nil && IsCallable(acc.Getter) {
				return CallFunction(acc.Getter, r), true
			}
			return UndefinedSingleton, true
		}
		if d.Value == nil {
			return UndefinedSingleton, true
		}
		return d.Value, true
	}
	switch name {
	case "source":
		return NewString(r.Pattern), true
	case "flags":
		return NewString(r.Flags), true
	case "global":
		return NewBoolean(r.Global), true
	case "ignoreCase":
		return NewBoolean(r.IgnoreCase), true
	case "multiline":
		return NewBoolean(r.Multiline), true
	case "dotAll":
		return NewBoolean(r.DotAll), true
	case "sticky":
		return NewBoolean(r.Sticky), true
	case "unicode":
		return NewBoolean(r.Unicode), true
	case "hasIndices":
		return NewBoolean(r.HasIndices), true
	case "lastIndex":
		// 事实来源始终是 LastIndex 字段 (描述符只承载可写性等元数据),
		// 二者在 DefineOwn / SetProperty 处保持同步。
		return NewNumber(float64(r.LastIndex)), true
	}
	if RegExpProto != nil {
		return RegExpProto.GetProperty(name)
	}
	return nil, false
}

func (r *RegExp) SetProperty(name string, val Value) {
	// 显式描述符优先: 访问器调 setter, 不可写数据属性静默失败 (非严格模式),
	// 与 *Array.SetProperty / *Object.SetProperty 的口径一致。
	if d, ok := r.PropDescs[name]; ok && !d.Deleted {
		if acc, isAcc := d.Value.(*Accessor); isAcc {
			if acc.Setter != nil && IsCallable(acc.Setter) {
				CallFunction(acc.Setter, r, val)
			}
			return
		}
		if !d.Writable {
			return
		}
		d.Value = val
		r.PropDescs[name] = d
	}
	if name == "lastIndex" {
		if num, ok := val.(*Number); ok {
			r.LastIndex = int(num.Value)
			// 同步描述符里的值 (若有), 使 getOwnPropertyDescriptor 读到的
			// value 与实际匹配位置一致。
			if d, exists := r.PropDescs[name]; exists && !d.Deleted {
				if _, isAcc := d.Value.(*Accessor); !isAcc {
					d.Value = val
					r.PropDescs[name] = d
				}
			}
		}
		return
	}
	// 其余键 (r.exec = fn / r.foo = 1) 落成普通自有数据属性。
	r.DefineOwn(name, DataProperty(val))
}

// SetLastIndexStrict 按规范 Set(R, "lastIndex", v, true) 的语义写入:
// 「严格」写入 —— 不可写 / 只有 getter 的访问器 ⇒ 返回 false, 调用方据此抛
// TypeError (ReturnIfAbrupt)。这是 @@match / @@replace 步骤 8.c / 15.c 的落点。
//
// Gox 的 Value.SetProperty 没有返回值, 无法表达严格 Set, 故单开这个方法。
func (r *RegExp) SetLastIndexStrict(v int) bool {
	if d, ok := r.PropDescs["lastIndex"]; ok && !d.Deleted {
		if acc, isAcc := d.Value.(*Accessor); isAcc {
			if acc.Setter == nil || !IsCallable(acc.Setter) {
				return false
			}
			CallFunction(acc.Setter, r, NewNumber(float64(v)))
			return true
		}
		if !d.Writable {
			return false
		}
		d.Value = NewNumber(float64(v))
		r.PropDescs["lastIndex"] = d
	}
	r.LastIndex = v
	return true
}

// ===== OwnPropertyStore 五件套 (Object.defineProperty /
// getOwnPropertyDescriptor / getOwnPropertyNames / hasOwn 的统一落点) =====

func (r *RegExp) OwnKeys() []string {
	keys := make([]string, 0, 1+len(r.PropDescs))
	if !deletedProp(r.PropDescs, "lastIndex") {
		keys = append(keys, "lastIndex")
	}
	for _, k := range r.propKeyOrder {
		if k == "lastIndex" || deletedProp(r.PropDescs, k) {
			continue
		}
		keys = append(keys, k)
	}
	return keys
}

// EnumerableOwnKeys 返回可枚举自有键: lastIndex 恒不可枚举, 故默认为空 ——
// Object.keys(/a/g) 与 Node 一致为 []。用户 defineProperty 出的可枚举键照列。
func (r *RegExp) EnumerableOwnKeys() []string {
	var keys []string
	for _, k := range r.propKeyOrder {
		d, ok := r.PropDescs[k]
		if !ok || d.Deleted || !d.Enumerable {
			continue
		}
		keys = append(keys, k)
	}
	return keys
}

func (r *RegExp) HasOwn(name string) bool {
	_, ok := r.OwnDescriptor(name)
	return ok
}

func (r *RegExp) OwnDescriptor(name string) (PropertyDescriptor, bool) {
	if d, ok := r.PropDescs[name]; ok {
		if d.Deleted {
			return PropertyDescriptor{}, false
		}
		return d, true
	}
	if name == "lastIndex" {
		return r.ownLastIndexDescriptor(), true
	}
	return PropertyDescriptor{}, false
}

func (r *RegExp) DefineOwn(name string, desc PropertyDescriptor) bool {
	if r.PropDescs == nil {
		r.PropDescs = make(map[string]PropertyDescriptor)
	}
	if _, exists := r.PropDescs[name]; !exists && name != "lastIndex" {
		r.propKeyOrder = append(r.propKeyOrder, name)
	}
	r.PropDescs[name] = desc
	// 数据描述符的 lastIndex: 把值同步进字段, 使匹配逻辑仍以结构体字段为准。
	if name == "lastIndex" {
		if _, isAcc := desc.Value.(*Accessor); !isAcc {
			if n, ok := desc.Value.(*Number); ok {
				r.LastIndex = int(n.Value)
			}
		}
	}
	return true
}

// DeleteOwn 删除实例的可配置自有属性。lastIndex 规范恒不可配置 ⇒ false。
func (r *RegExp) DeleteOwn(name string) bool {
	if d, ok := r.PropDescs[name]; ok {
		if d.Deleted {
			return true
		}
		if !d.Configurable {
			return false
		}
		delete(r.PropDescs, name)
		for i, k := range r.propKeyOrder {
			if k == name {
				r.propKeyOrder = append(r.propKeyOrder[:i], r.propKeyOrder[i+1:]...)
				break
			}
		}
		return true
	}
	if name == "lastIndex" {
		return false
	}
	return true
}

// NewRegExp 创建正则表达式对象。
func NewRegExp(pattern, flags string) (*RegExp, error) {
	r := &RegExp{
		Pattern: pattern,
		Flags:   flags,
	}

	// 解析标志
	for _, f := range flags {
		switch f {
		case 'g':
			r.Global = true
		case 'i':
			r.IgnoreCase = true
		case 'm':
			r.Multiline = true
		case 's':
			r.DotAll = true
		case 'y':
			// y (sticky): 匹配必须从 lastIndex 处开始
			r.Sticky = true
		case 'u':
			// u (unicode): 按 Unicode 码点处理模式
			r.Unicode = true
		case 'd':
			// d (hasIndices): exec 结果附带 indices 匹配位置
			r.HasIndices = true
		}
	}

	// 转换 JS 正则到 Go 正则
	goPattern := jsToGoRegex(pattern, r.IgnoreCase, r.Multiline, r.DotAll, r.Unicode)
	re, err := regexp.Compile(goPattern)
	if err != nil {
		return nil, fmt.Errorf("SyntaxError: Invalid regular expression: %s", pattern)
	}
	r.Regexp = re
	return r, nil
}

// jsToGoRegex 将 JS 正则模式转换为 Go 正则模式 (简化)。
func jsToGoRegex(pattern string, ignoreCase, multiline, dotAll, unicode bool) string {
	// Go 的 regexp 使用 RE2 语法，大部分与 JS 兼容
	// 主要差异: JS 的 \d, \w, \s 等在 Go 中需要使用 (?i) 等标志
	// \uXXXX / \u{...} 是 JS 有而 RE2 没有的 —— 先翻译成 \x{...}
	result := pattern
	if unicode {
		// \p{...}/\P{...} 属性转义 (rFXXbq: RegExp/property-escapes 447 例):
		// RE2 只认短类别 (Lu) / 规范脚本名 (Greek) / 35 个二元属性;
		// JS 的 Name=Value 形态、长类别名、4 字母脚本别名、派生二元属性
		// 在此统一翻译成 RE2 等价形式。
		result = translatePropertyEscapes(result)
	}
	result = translateUnicodeEscapes(result, unicode)

	// 添加标志前缀
	prefix := ""
	if ignoreCase {
		prefix += "i"
	}
	if multiline {
		prefix += "m"
	}
	if dotAll {
		// Go 的 regexp 不直接支持 s 标志，但可以用 (?s) 前缀
		prefix += "s"
	}
	if prefix != "" {
		result = "(?" + prefix + ")" + result
	}

	return result
}

// translateUnicodeEscapes 把 JS 正则的 \uXXXX 与 \u{HexDigits} 转义翻译成
// RE2 的 \x{HexDigits} (字符类内外都成立, [\u4e00-\u9fa5] 判 CJK 靠它)。
//
// 规则 (与 Node v22 逐条对齐):
//   - \uXXXX (4 位十六进制) → \x{XXXX}。两种模式都合法、语义相同;
//   - \u{X...} (1..6 位十六进制) 且带 u 标志 → \x{X...};
//   - \u{X...} **不带** u 标志 → 去掉反斜杠留字面 u (Annex B 的
//     IdentityEscape), 后面的 {...} 原样保留 —— Node 里 /\u{2}/ 真的匹配
//     "uu" (u + 量词), /\u{1F600}/ 匹配字面 "u{1F600}", RE2 对这两种
//     {...} 的解释恰好一致;
//   - 畸形 \u (后面既不是 4 位 hex 也不是 {hex}): 无 u 标志按 IdentityEscape
//     译成字面 u (Node 里 new RegExp("\\uz") 合法); 带 u 标志原样保留,
//     交给 regexp.Compile 报错 —— 与 JS 的 SyntaxError 一致;
//   - \\u... (转义的反斜杠后跟字面 u) 原样保留 —— 那是"反斜杠+u"两个
//     字符, 不是 unicode 转义。
//
// 已知局限: 非 u 标志下的代理对 (\uD83D\uDE00) 会被译成两个孤立的
// \x{d83d}\x{de00}, 匹配不了真实 UTF-8 文本里的 astral 字符 —— RE2 按
// 码点匹配, 没有码单元语义。需要 astral 匹配时写 \u{1F600} 形态。
func translateUnicodeEscapes(pattern string, unicode bool) string {
	// 没出现 \u 候选就别拷贝整串 (绝大多数模式不含它)
	hasCandidate := false
	for i := 0; i+1 < len(pattern); i++ {
		if pattern[i] == '\\' && pattern[i+1] == 'u' {
			hasCandidate = true
			break
		}
	}
	if !hasCandidate {
		return pattern
	}

	var b strings.Builder
	b.Grow(len(pattern))
	for i := 0; i < len(pattern); {
		c := pattern[i]
		if c != '\\' || i+1 >= len(pattern) {
			b.WriteByte(c)
			i++
			continue
		}
		next := pattern[i+1]
		// \\ : 转义的反斜杠, 其后的字符不参与转义解释
		if next == '\\' {
			b.WriteString(`\\`)
			i += 2
			continue
		}
		if next != 'u' {
			b.WriteByte(c)
			i++
			continue
		}
		// \uXXXX → \x{XXXX} (两种标志下语义一致)
		if i+6 <= len(pattern) && isHexRun(pattern[i+2:i+6]) {
			b.WriteString(`\x{`)
			b.WriteString(pattern[i+2 : i+6])
			b.WriteByte('}')
			i += 6
			continue
		}
		// \u{HexDigits}
		if i+2 < len(pattern) && pattern[i+2] == '{' {
			if end := strings.IndexByte(pattern[i+3:], '}'); end >= 0 {
				hex := pattern[i+3 : i+3+end]
				if len(hex) >= 1 && len(hex) <= 6 && isHexRun(hex) {
					if unicode {
						b.WriteString(`\x{`)
						b.WriteString(hex)
						b.WriteByte('}')
						i += 3 + end + 1
						continue
					}
					// 无 u 标志: IdentityEscape —— 字面 u, {...} 原样保留
					b.WriteByte('u')
					i += 2
					continue
				}
			}
		}
		// 畸形 \u
		if unicode {
			// 带 u 标志时 JS 是 SyntaxError, 原样留给 RE2 报错
			b.WriteByte(c)
			i++
			continue
		}
		// 无 u 标志: IdentityEscape → 字面 u
		b.WriteByte('u')
		i += 2
	}
	return b.String()
}

// isHexRun 报告 s 是否全为十六进制数字且非空。
func isHexRun(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

var RegExpProto Value

func SetRegExpProto(p Value) { RegExpProto = p }

// Lock 加锁 (供外部包使用)。
func (r *RegExp) Lock() {
	r.mu.Lock()
}

// Unlock 解锁 (供外部包使用)。
func (r *RegExp) Unlock() {
	r.mu.Unlock()
}

// ===== Unicode 属性转义 (\p{...} / \P{...}) 翻译 =====
//
// ES2018 u 标志下的属性转义语法比 RE2 宽得多 (rFXXbq: property-escapes 447 例):
//   - \p{General_Category=Uppercase_Letter} 长属性名 + 长值;
//   - \p{sc=Greek} / \p{Script_Extensions=Hani} 值形态;
//   - 4 字母脚本别名 (Zyyy/Zinh/Hans/Hrkt/...);
//   - 派生/二元属性 (Alphabetic/Cased/Dash/...) —— RE2 只认短类别与
//     规范脚本名, 二元属性一律不认, 必须物化成显式码点区间。
//
// 翻译策略: 类别/脚本名直接映射 (RE2 原生支持, 模式短); 其余属性用
// unicode 包的 RangeTable 合成区间并物化为 [\x{a}-\x{b}...] 类; 不认识的
// 名字原样保留, 由 regexp.Compile 报 SyntaxError —— 恰与规范"未知属性抛
// SyntaxError"对齐。

// propGCLongToShort: General_Category 长值 (规范化) → RE2 短键。
var propGCLongToShort = map[string]string{
	"uppercase_letter": "Lu", "lowercase_letter": "Ll", "titlecase_letter": "Lt",
	"modifier_letter": "Lm", "other_letter": "Lo", "letter": "L", "cased_letter": "LC",
	"nonspacing_mark": "Mn", "spacing_combining_mark": "Mc", "enclosing_mark": "Me", "mark": "M",
	"decimal_number": "Nd", "letter_number": "Nl", "other_number": "No", "number": "N",
	"connector_punctuation": "Pc", "dash_punctuation": "Pd", "open_punctuation": "Ps",
	"close_punctuation": "Pe", "initial_punctuation": "Pi", "final_punctuation": "Pf",
	"other_punctuation": "Po", "punctuation": "P",
	"math_symbol": "Sm", "currency_symbol": "Sc", "modifier_symbol": "Sk", "other_symbol": "So", "symbol": "S",
	"space_separator": "Zs", "line_separator": "Zl", "paragraph_separator": "Zp", "separator": "Z",
	"control": "Cc", "cntrl": "Cc", "format": "Cf", "surrogate": "Cs",
	"private_use": "Co", "unassigned": "Cn", "other": "C",
	"digit": "Nd", "combining_mark": "M", "space": "Zs", "punct": "P",
}

// propGCShort: General_Category 短值 (规范化小写) → RE2 键。
var propGCShort = map[string]string{
	"lu": "Lu", "ll": "Ll", "lt": "Lt", "lm": "Lm", "lo": "Lo", "l": "L", "lc": "LC",
	"mn": "Mn", "mc": "Mc", "me": "Me", "m": "M",
	"nd": "Nd", "nl": "Nl", "no": "No", "n": "N",
	"pc": "Pc", "pd": "Pd", "ps": "Ps", "pe": "Pe", "pi": "Pi", "pf": "Pf", "po": "Po", "p": "P",
	"sm": "Sm", "sc": "Sc", "sk": "Sk", "so": "So", "s": "S",
	"zs": "Zs", "zl": "Zl", "zp": "Zp", "z": "Z",
	"cc": "Cc", "cf": "Cf", "cs": "Cs", "co": "Co", "c": "C",
	"cn": "Cn", // Unassigned 短码 (与 Cc 控制字符区分)
}

// propScriptAliases: JS 4 字母脚本别名 → RE2 规范脚本名。
var propScriptAliases = map[string]string{
	"zyyy": "Common", "qaai": "Inherited", "zinh": "Inherited",
	"hrkt": "Hiragana", // Katakana_Or_Hiragana 近似为 Hiragana
	"jpan": "Han", "hans": "Han", "hant": "Han", // 日/简/繁汉字近似为 Han
	"kore": "Hangul",
	"latn": "Latin", "grek": "Greek", "cyrl": "Cyrillic", "arab": "Arabic", "hebr": "Hebrew",
	"deva": "Devanagari", "brai": "Braille", "hani": "Han", "hang": "Hangul",
	"kana": "Katakana", "bopo": "Bopomofo", "armn": "Armenian", "geor": "Georgian",
	"thai": "Thai", "laoo": "Lao", "tibt": "Tibetan", "mymr": "Myanmar", "khmr": "Khmer",
	// ISO 15924 四字母码全表 (test262 用例覆盖面, rFXXbq)
	"adlm": "Adlam", "hluw": "Anatolian_Hieroglyphs",
	"avst": "Avestan", "bamu": "Bamum", "bass": "Bassa_Vah", "batk": "Batak",
	"bhks": "Bhaiksuki", "bugi": "Buginese", "buhd": "Buhid", "cakm": "Chakma",
	"cans": "Canadian_Aboriginal", "cari": "Carian", "aghb": "Caucasian_Albanian",
	"cher": "Cherokee", "chrs": "Chorasmian", "copt": "Coptic", "qaac": "Coptic",
	"xsux": "Cuneiform", "cprt": "Cypriot", "cpmn": "Cypro_Minoan",
	"dsrt": "Deseret", "diak": "Dives_Akuru", "dogr": "Dogra", "dupl": "Duployan",
	"egyp": "Egyptian_Hieroglyphs", "elba": "Elbasan", "elym": "Elymaic",
	"ethi": "Ethiopic", "glag": "Glagolitic", "goth": "Gothic", "gran": "Grantha",
	"gujr": "Gujarati", "gong": "Gunjala_Gondi", "guru": "Gurmukhi",
	"rohg": "Hanifi_Rohingya", "hano": "Hanunoo", "hatr": "Hatran",
	"armi": "Imperial_Aramaic", "phli": "Inscriptional_Pahlavi",
	"prti": "Inscriptional_Parthian", "java": "Javanese", "kthi": "Kaithi",
	"kali": "Kayah_Li", "khar": "Kharoshthi", "kits": "Khitan_Small_Script",
	"khoj": "Khojki", "krai": "Kirat_Rai", "lepc": "Lepcha", "limb": "Limbu",
	"lina": "Linear_A", "linb": "Linear_B", "lyci": "Lycian", "lydi": "Lydian",
	"mahj": "Mahajani", "maka": "Makasar", "mand": "Mandaic", "mani": "Manichaean",
	"marc": "Marchen", "gonm": "Masaram_Gondi", "medf": "Medefaidrin",
	"mtei": "Meetei_Mayek", "mend": "Mende_Kikakui", "merc": "Meroitic_Cursive",
	"mero": "Meroitic_Hieroglyphs", "plrd": "Miao", "mong": "Mongolian",
	"mroo": "Mro", "mult": "Multani", "narb": "Old_North_Arabian",
	"nagm": "Nag_Mundari", "nand": "Nandinagari", "talu": "New_Tai_Lue",
	"nkoo": "Nko", "nshu": "Nushu", "hmnp": "Nyiakeng_Puachue_Hmong",
	"ogam": "Ogham", "olck": "Ol_Chiki", "hung": "Old_Hungarian",
	"ital": "Old_Italic", "perm": "Old_Permic", "xpeo": "Old_Persian",
	"sogo": "Old_Sogdian", "sarb": "Old_South_Arabian", "orkh": "Old_Turkic",
	"ougr": "Old_Uyghur", "orya": "Oriya", "osge": "Osage", "osma": "Osmanya",
	"hmng": "Pahawh_Hmong", "palm": "Palmyrene", "pauc": "Pau_Cin_Hau",
	"phag": "Phags_Pa", "phnx": "Phoenician", "phlp": "Psalter_Pahlavi",
	"rjng": "Rejang", "runr": "Runic", "samr": "Samaritan", "saur": "Saurashtra",
	"shrd": "Sharada", "shaw": "Shavian", "sidd": "Siddham", "sgnw": "SignWriting",
	"sinh": "Sinhala", "sogd": "Sogdian", "sora": "Sora_Sompeng",
	"soyo": "Soyombo", "sund": "Sundanese", "sunu": "Sunuwar",
	"sylo": "Syloti_Nagri", "syrc": "Syriac", "tglg": "Tagalog",
	"tagb": "Tagbanwa", "tale": "Tai_Le", "lana": "Tai_Tham", "tavt": "Tai_Viet",
	"takr": "Takri", "taml": "Tamil", "tang": "Tangut", "tnsa": "Tangsa",
	"telu": "Telugu", "thaa": "Thaana", "tfng": "Tifinagh", "tirh": "Tirhuta",
	"ugar": "Ugaritic", "vaii": "Vai", "vith": "Vithkuqi", "wcho": "Wancho",
	"wara": "Warang_Citi", "yezi": "Yezidi", "yiii": "Yi", "zanb": "Zanabazar_Square",
	"zzzz": "Common", // Unknown 近似 (RE2 无 Unknown 表)
}

// propCanonicalName 规范化属性名: 仅去 '_', 保留大小写。
// 规范 (UnicodeMatchProperty) 对属性名做**大小写敏感**匹配
// (loose-matching-11: \p{gC=...} 必须 SyntaxError), 只忽略 '_'。
func propCanonicalName(s string) string {
	return strings.ReplaceAll(s, "_", "")
}

// propCanonicalValue 规范化属性值: 仅转小写。
// 规范 (UnicodeMatchPropertyValue) 对值做大小写不敏感匹配, 但**不**忽略
// '_' 与空格 (loose-matching-12: \p{gc=uppercaseletter} 必须 SyntaxError)。
func propCanonicalValue(s string) string {
	return strings.ToLower(s)
}

// translatePropertyEscapes 把 u 标志模式中的 \p{...}/\P{...} 翻译为 RE2 语法。
// 只在 u 标志下调用 (Annex B: 非 u 模式 \p 是 IdentityEscape)。
func translatePropertyEscapes(pattern string) string {
	var b strings.Builder
	b.Grow(len(pattern))
	for i := 0; i < len(pattern); {
		c := pattern[i]
		if c != '\\' || i+1 >= len(pattern) {
			b.WriteByte(c)
			i++
			continue
		}
		next := pattern[i+1]
		if next == '\\' {
			b.WriteString(`\\`)
			i += 2
			continue
		}
		if next != 'p' && next != 'P' {
			b.WriteByte(c)
			i++
			continue
		}
		if i+2 >= len(pattern) || pattern[i+2] != '{' {
			b.WriteByte(c)
			i++
			continue
		}
		end := strings.IndexByte(pattern[i+3:], '}')
		if end < 0 {
			b.WriteByte(c)
			i++
			continue
		}
		content := pattern[i+3 : i+3+end]
		replacement, ok := translateOnePropertyEscape(next == 'P', content)
		if ok {
			b.WriteString(replacement)
		} else {
			// 语法上不可识别: 原样保留 → RE2 报 SyntaxError (规范同)
			b.WriteString(pattern[i : i+3+end+1])
		}
		i += 3 + end + 1
	}
	return b.String()
}

// translateOnePropertyEscape 翻译单个属性转义内容, negated 为 \P 时真。
// 第二返回值 false 表示不认识 (调用方原样保留 → SyntaxError)。
func translateOnePropertyEscape(negated bool, content string) (string, bool) {
	// 语法上不允许任何空格: \p{ General_Category=Lu } 必须 SyntaxError
	// (loose-matching-01/02)。含空格 → 原样保留, RE2 报 SyntaxError。
	for i := 0; i < len(content); i++ {
		if content[i] == ' ' || content[i] == '\t' || content[i] == '\n' || content[i] == '\r' {
			return "", false
		}
	}
	name, value := content, ""
	hasValue := false
	// 仅 '=' 是 u 模式合法分隔符; ':' 是 v 标志 class-set 语法的扩展,
	// u 模式必须 SyntaxError (grammar-extension-separator)。
	if idx := strings.IndexByte(content, '='); idx >= 0 {
		name, value = content[:idx], content[idx+1:]
		hasValue = true
	}
	name = propCanonicalName(name)
	value = propCanonicalValue(value)
	if hasValue {
		if value == "" {
			return "", false
		}
		switch name {
		case "General_Category", "gc":
			short, ok2 := propGCLongToShort[value]
			if !ok2 {
				short, ok2 = propGCShort[value]
			}
			if !ok2 {
				return "", false
			}
			return propGCClass(short, negated), true
		case "Script", "sc", "Script_Extensions", "scx":
			script, ok2 := propScriptValueName(value)
			if !ok2 {
				return "", false
			}
			return propSimpleClass(script, negated), true
	}
	// 二元属性带值 (\p{ASCII=Yes} 等) 与未知属性名带值: 规范均 SyntaxError
	// (binary-property-with-value-ASCII_-_Yes)。
	return "", false
	}
	// 无值形态: 先按 General_Category 值匹配 (General_CategoryValue 经
	// UnicodeMatchPropertyValue, 大小写不敏感) —— 覆盖 \p{Lu}/\p{Letter}/
	// \p{space}/\p{Unassigned} 等。
	lv := propCanonicalValue(name)
	if short, ok := propGCLongToShort[lv]; ok {
		return propGCClass(short, negated), true
	}
	if short, ok := propGCShort[lv]; ok {
		return propGCClass(short, negated), true
	}
	// Any/ASCII/Assigned: 属性名精确大小写 (loose-matching-03..10:
	// \p{any}/\p{ANY}/\p{Ascii}/\p{assigned} 等均 SyntaxError)。
	switch name {
	case "Any":
		return propBinaryRanges("any", negated), true
	case "ASCII":
		return propBinaryRanges("ascii", negated), true
	case "Assigned":
		return propBinaryRanges("assigned", negated), true
	}
	// 二元属性 (unicode.Properties 表, 物化为区间)
	if rt, ok := propBinaryTable(name); ok {
		return propMaterialize([](*unicode.RangeTable){rt}, negated), true
	}
	// 派生二元属性: 取各组成键的 RangeTable 合并物化 (RE2 不认
	// Other_Alphabetic 等键, 不能用 \p{...} 组合)
	if parts, ok := propDerivedBinary[name]; ok {
		var tables []*unicode.RangeTable
		for _, k := range parts {
			if rt := propRangeTableByName(k); rt != nil {
				tables = append(tables, rt)
			}
		}
		return propMaterialize(tables, negated), true
	}
	// 脚本名: RE2 原生支持
	if sn, ok := propScriptName(name); ok {
		return propSimpleClass(sn, negated), true
	}
	return "", false
}

// propGCClass 由 GC 短码生成字符类 (LC 与 Cn 需要特殊处理)。
func propGCClass(short string, negated bool) string {
	if short == "LC" {
		// RE2 无 Cased_Letter: 展开为并集
		return propRangeClass([]string{"Lu", "Ll", "Lt"}, negated)
	}
	if short == "Cn" {
		// RE2 无 Cn 表: 物化为全空间减去所有已分配类别的取补区间
		var tables []*unicode.RangeTable
		for _, rt := range unicode.Categories {
			tables = append(tables, rt)
		}
		return propMaterialize(tables, !negated)
	}
	return propSimpleClass(short, negated)
}

// propScriptCanonical: init 时从 unicode.Scripts 构建的
// "规范化脚本名 → RE2 键" 全表 (含 Canadian_Aboriginal 等多词名)。
var propScriptCanonical = func() map[string]string {
	m := map[string]string{}
	for k := range unicode.Scripts {
		m[propCanonicalName(k)] = k
	}
	return m
}()

// propScriptValueCanonical: init 时构建的 "小写脚本名 → 规范名" 表
// (供属性值匹配使用, 值匹配大小写不敏感)。
var propScriptValueCanonical = func() map[string]string {
	m := map[string]string{}
	for k := range unicode.Scripts {
		m[strings.ToLower(k)] = k
	}
	return m
}()

// propScriptValueName 属性值形态的脚本名匹配 (大小写不敏感,
// UnicodeMatchPropertyValue 语义): 别名 → 全表 → 大驼峰还原。
func propScriptValueName(v string) (string, bool) {
	lv := strings.ToLower(v)
	if a, ok := propScriptAliases[lv]; ok {
		return a, true
	}
	if a, ok := propScriptValueCanonical[lv]; ok {
		return a, true
	}
	parts := strings.Split(v, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "_"), true
}

// propScriptName 无值形态的脚本名匹配: 属性名大小写敏感
// (UnicodeMatchProperty 语义)。别名 (ISO 15924 码) 仅接受规范大小写
// (首字母大写), 全名须与 unicode.Scripts 键精确一致。
func propScriptName(cname string) (string, bool) {
	if len(cname) == 4 {
		canonical := strings.ToUpper(cname[:1]) + cname[1:]
		if canonical != cname {
			return "", false
		}
		if a, ok := propScriptAliases[strings.ToLower(cname)]; ok {
			return a, true
		}
	}
	if cname == "" {
		return "", false
	}
	if a, ok := propScriptCanonical[cname]; ok {
		return a, true
	}
	return "", false
}

// propSimpleClass 单键类 (RE2 原生支持 \p{...} / \P{...})。
func propSimpleClass(key string, negated bool) string {
	if negated {
		return `\P{` + key + `}`
	}
	return `\p{` + key + `}`
}

// propRangeClass 键并集类: RE2 原生键的组合 (如 LC = Lu∪Ll∪Lt)。
func propRangeClass(keys []string, negated bool) string {
	var b strings.Builder
	b.WriteString("[")
	if negated {
		b.WriteString("^")
	}
	for _, k := range keys {
		b.WriteString(`\p{`)
		b.WriteString(k)
		b.WriteString("}")
	}
	b.WriteString("]")
	return b.String()
}

// propBinaryRanges Any/ASCII/Assigned 的专用形态。
func propBinaryRanges(name string, negated bool) string {
	switch name {
	case "any":
		if negated {
			return `[^\s\S]`
		}
		return `[\s\S]`
	case "ascii":
		if negated {
			return `[^\x00-\x7F]`
		}
		return `[\x00-\x7F]`
	case "assigned":
		// RE2 无 Cn 表; 代理码元在 RE2 rune 语义下不单独出现, 用 Any 近似。
		if negated {
			return `[^\s\S]`
		}
		return `[\s\S]`
	}
	return `\p{` + name + `}`
}

// propMaterialize 把若干 RangeTable 合并物化为显式 RE2 字符类。
// 超出 BMP 的码点用 \x{...} 表示; negated 输出取补类。
func propMaterialize(tables []*unicode.RangeTable, negated bool) string {
	type iv struct{ lo, hi rune }
	var ranges []iv
	for _, t := range tables {
		if t == nil {
			continue
		}
		// RangeTable 的 Stride 语义: Stride==1 为连续区间 [Lo,Hi];
		// Stride>1 为等差离散码点 (Lo, Lo+Stride, ... ≤ Hi), 逐点展开。
		for _, r := range t.R16 {
			lo, hi, st := rune(r.Lo), rune(r.Hi), rune(r.Stride)
			if st <= 1 {
				ranges = append(ranges, iv{lo, hi})
				continue
			}
			for c := lo; c <= hi; c += st {
				ranges = append(ranges, iv{c, c})
			}
		}
		for _, r := range t.R32 {
			lo, hi, st := rune(r.Lo), rune(r.Hi), rune(r.Stride)
			if st <= 1 {
				ranges = append(ranges, iv{lo, hi})
				continue
			}
			for c := lo; c <= hi; c += st {
				ranges = append(ranges, iv{c, c})
			}
		}
	}
	if len(ranges) == 0 {
		if negated {
			return `[\s\S]`
		}
		return `[^\s\S]`
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i].lo < ranges[j].lo })
	merged := []iv{ranges[0]}
	for _, r := range ranges[1:] {
		if r.lo <= merged[len(merged)-1].hi+1 {
			if r.hi > merged[len(merged)-1].hi {
				merged[len(merged)-1].hi = r.hi
			}
		} else {
			merged = append(merged, r)
		}
	}
	if negated {
		// 取补: 遍历 [0, 0x10FFFF] 的间隙 (代理区保留 —— JS 串可含码元)
		var comp []iv
		prev := rune(-1)
		for _, r := range merged {
			if r.lo > prev+1 {
				comp = append(comp, iv{prev + 1, r.lo - 1})
			}
			if r.hi > prev {
				prev = r.hi
			}
		}
		if prev < 0x10FFFF {
			comp = append(comp, iv{prev + 1, 0x10FFFF})
		}
		merged = comp
	}
	var b strings.Builder
	b.WriteString("[")
	for _, r := range merged {
		if r.lo == r.hi {
			b.WriteString(fmt.Sprintf(`\x{%X}`, r.lo))
		} else {
			b.WriteString(fmt.Sprintf(`\x{%X}-\x{%X}`, r.lo, r.hi))
		}
	}
	b.WriteString("]")
	return b.String()
}

// propDerivedBinary: 派生二元属性 → RE2 原生键并集 (UCD 派生规则近似)。
var propDerivedBinary = map[string][]string{
	"ASCII_Hex_Digit": {"ASCII_Hex_Digit"},
	"Alphabetic": {"L", "Nl", "Other_Alphabetic"},
	"Case_Ignorable": {"Mn", "Me", "Cf", "Lm", "Sk"},
	"Cased": {"Lu", "Ll", "Lt", "Other_Lowercase", "Other_Uppercase"},
	"Dash": {"Pd"},
	"Hex_Digit": {"Nd", "Hex_Digit"},
	"ID_Continue": {"L", "Nl", "Mn", "Mc", "Nd", "Pc", "Other_ID_Continue"},
	"ID_Start": {"L", "Nl", "Other_ID_Start"},
	"Ideographic": {"Ideographic"},
	"Lowercase": {"Ll", "Other_Lowercase"},
	"Math": {"Sm", "Other_Math"},
	"Quotation_Mark": {"Pi", "Pf", "Quotation_Mark"},
	"Uppercase": {"Lu", "Other_Uppercase"},
	"XID_Continue": {"L", "Nl", "Mn", "Mc", "Nd", "Pc", "Other_ID_Continue"},
	"XID_Start": {"L", "Nl", "Other_ID_Start"},
}

// propSpecBinary 是规范 (sec-runtime-semantics-unicodematchproperty-p)
// 支持的二元属性白名单 (规范化名)。表里没有的 (如 Other_* 系列) 一律
// SyntaxError —— unsupported-binary-property-* 用例正考这个。
var propSpecBinary = map[string]bool{
	"ASCII_Hex_Digit": true,
	"Alphabetic": true,
	"Any": true,
	"Assigned": true,
	"Bidi_Control": true,
	"Bidi_Mirrored": true,
	"Case_Ignorable": true,
	"Cased": true,
	"Changes_When_Casefolded": true,
	"Changes_When_Casemapped": true,
	"Changes_When_Lowercased": true,
	"Changes_When_NFKC_Casefolded": true,
	"Changes_When_Titlecased": true,
	"Changes_When_Uppercased": true,
	"Dash": true,
	"Default_Ignorable_Code_Point": true,
	"Deprecated": true,
	"Diacritic": true,
	"Emoji": true,
	"Emoji_Component": true,
	"Emoji_Modifier": true,
	"Emoji_Modifier_Base": true,
	"Emoji_Presentation": true,
	"Extended_Pictographic": true,
	"Extender": true,
	"Grapheme_Base": true,
	"Grapheme_Extend": true,
	"Hex_Digit": true,
	"IDS": true,
	"IDS_Binary_Operator": true,
	"IDS_Trinary_Operator": true,
	"ID_Continue": true,
	"ID_Start": true,
	"Ideographic": true,
	"Join_Control": true,
	"Logical_Order_Exception": true,
	"Lowercase": true,
	"Math": true,
	"Noncharacter_Code_Point": true,
	"Pattern_Syntax": true,
	"Pattern_White_Space": true,
	"Quotation_Mark": true,
	"Radical": true,
	"Regional_Indicator": true,
	"Sentence_Terminal": true,
	"Soft_Dotted": true,
	"Terminal_Punctuation": true,
	"Unified_Ideograph": true,
	"Uppercase": true,
	"Variation_Selector": true,
	"White_Space": true,
	"XID_Continue": true,
	"XID_Start": true,
}

// propBinaryTable 返回规范支持且 unicode 包有表的二元属性
// (键为规范化名)。ok=false 表示没有对应表 (调用方原样保留 → SyntaxError)。
func propBinaryTable(cname string) (*unicode.RangeTable, bool) {
	if !propSpecBinary[cname] {
		return nil, false
	}
	goName, ok := propPropertiesAlias[cname]
	if !ok {
		return nil, false
	}
	rt, ok := unicode.Properties[goName]
	return rt, ok
}

// propPropertiesAlias: 规范化 JS 二元属性名 → unicode.Properties 键。
// propPropertiesAlias: JS 二元属性名 (规范大小写, 属性名匹配大小写敏感) →
// unicode.Properties 键。
var propPropertiesAlias = map[string]string{
	"ASCII_Hex_Digit":                "ASCII_Hex_Digit",
	"Bidi_Control":                   "Bidi_Control",
	"Dash":                           "Dash",
	"Deprecated":                     "Deprecated",
	"Diacritic":                      "Diacritic",
	"Extender":                       "Extender",
	"Hex_Digit":                      "Hex_Digit",
	"Hyphen":                         "Hyphen",
	"IDS_Binary_Operator":            "IDS_Binary_Operator",
	"IDS_Trinary_Operator":           "IDS_Trinary_Operator",
	"Ideographic":                    "Ideographic",
	"Join_Control":                   "Join_Control",
	"Logical_Order_Exception":        "Logical_Order_Exception",
	"Noncharacter_Code_Point":        "Noncharacter_Code_Point",
	"Other_Alphabetic":               "Other_Alphabetic",
	"Other_Default_Ignorable_Code_Point": "Other_Default_Ignorable_Code_Point",
	"Other_Grapheme_Extend":          "Other_Grapheme_Extend",
	"Other_ID_Continue":              "Other_ID_Continue",
	"Other_ID_Start":                 "Other_ID_Start",
	"Other_Lowercase":                "Other_Lowercase",
	"Other_Math":                     "Other_Math",
	"Other_Uppercase":                "Other_Uppercase",
	"Pattern_Syntax":                 "Pattern_Syntax",
	"Pattern_White_Space":            "Pattern_White_Space",
	"Prepended_Concatenation_Mark":   "Prepended_Concatenation_Mark",
	"Quotation_Mark":                 "Quotation_Mark",
	"Radical":                        "Radical",
	"Regional_Indicator":             "Regional_Indicator",
	"Sentence_Terminal":              "Sentence_Terminal",
	"Soft_Dotted":                    "Soft_Dotted",
	"Terminal_Punctuation":           "Terminal_Punctuation",
	"Unified_Ideograph":              "Unified_Ideograph",
	"Variation_Selector":             "Variation_Selector",
	"White_Space":                    "White_Space",
}

// propRangeTableByName 按规范化键取 RE2 原生类别/脚本的 RangeTable
// (供派生属性区间物化使用)。
func propRangeTableByName(goKey string) *unicode.RangeTable {
	if rt, ok := unicode.Categories[goKey]; ok {
		return rt
	}
	if rt, ok := unicode.Scripts[goKey]; ok {
		return rt
	}
	return unicode.Properties[goKey]
}

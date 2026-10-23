package object

import "testing"

// ===== 正则 \uXXXX / \u{...} 转义 (translateUnicodeEscapes) =====
//
// 背景: RE2 不认 JS 的 \u 转义, 此前 /[\u4e00-\u9fa5]/ 判 CJK 直接
// SyntaxError: Invalid regular expression。translateUnicodeEscapes 在编译前
// 把它翻译成 \x{...}; 语义逐条与 Node v22 对齐:
//   - \uXXXX 两种标志下都合法且同义 → \x{XXXX};
//   - \u{X...} 带 u 标志 → \x{X...};
//   - \u{X...} 不带 u 标志 → Annex B IdentityEscape: 字面 u, {...} 原样
//     (Node 里 /\u{2}/ 匹配 "uu"、/\u{1F600}/ 匹配字面 "u{1F600}", RE2 恰好同解);
//   - 畸形 \u (非 4 位 hex、非 {hex}): 无 u 标志 → 字面 u; 带 u 标志 →
//     原样保留, 由 regexp.Compile 报错 (JS 同为 SyntaxError);
//   - \\u... (转义反斜杠后的字面 u) 原样保留。

// TestRegExpUnicodeEscapeTranslate 编译产物的抽样断言。
func TestRegExpUnicodeEscapeTranslate(t *testing.T) {
	cases := []struct {
		pat, flags string
		wantExpr  string // 编译后的 Go 正则文本 (regexp.String())
	}{
		{`[\u4e00-\u9fa5]`, "", `[\x{4e00}-\x{9fa5}]`},
		{`\u4e00`, "", `\x{4e00}`},
		{`\u0041`, "", `\x{0041}`},
		{`[\u4e00-\u9fa5]+`, "", `[\x{4e00}-\x{9fa5}]+`},
		{`\\u0041`, "", `\\u0041`},       // 转义反斜杠: 不动
		{`[a-z]`, "", `[a-z]`},           // 无 \u: 整串不拷贝 (快路径)
		{`\u{2}`, "", `u{2}`},            // 无 u 标志: IdentityEscape + 量词
		{`\uz`, "", `uz`},                // 无 u 标志: 畸形 → 字面 u
		{`\u{1F600}`, "", `u{1F600}`},    // 无 u 标志: 字面
		{`\u{1F600}`, "u", `\x{1F600}`},  // u 标志: 码点
		{`\u{d800}`, "u", `\x{d800}`},    // u 标志: 孤立代理也合法 (与 Node 一致)
	}
	for _, tc := range cases {
		re, err := NewRegExp(tc.pat, tc.flags)
		if err != nil {
			t.Errorf("PAT %q flags %q 编译失败: %v", tc.pat, tc.flags, err)
			continue
		}
		if got := re.Regexp.String(); got != tc.wantExpr {
			t.Errorf("PAT %q flags %q: expr = %q, want %q", tc.pat, tc.flags, got, tc.wantExpr)
		}
	}
}

// TestRegExpUnicodeEscapeMatch 匹配语义 (与 Node v22 逐条对过)。
func TestRegExpUnicodeEscapeMatch(t *testing.T) {
	cases := []struct {
		pat, flags, input string
		want              bool
	}{
		{`[\u4e00-\u9fa5]`, "", "中", true},          // CJK 判定 (本修的动机)
		{`[\u4e00-\u9fa5]`, "", "A", false},
		{`[\u4e00-\u9fa5]`, "", "hello 世界", true},
		{`\u4e00`, "", "一", true},
		{`\u{1F600}`, "u", "\U0001F600", true},       // u 标志按码点
		{`\u{2}`, "", "uu", true},                    // 无 u 标志: u + 量词
		{`\u{2}`, "", "u{2}", false},
		{`\u{1F600}`, "", "u{1F600}", true},          // 无 u 标志: 字面
		{`\u{1F600}`, "", "\U0001F600", false},
		{`\\u0041`, "", `\u0041`, true},              // 字面反斜杠+u
		{`[\u4e00-\u9fa5]+`, "", "中文", true},
	}
	for _, tc := range cases {
		re, err := NewRegExp(tc.pat, tc.flags)
		if err != nil {
			t.Errorf("PAT %q flags %q 编译失败: %v", tc.pat, tc.flags, err)
			continue
		}
		if got := re.Regexp.MatchString(tc.input); got != tc.want {
			t.Errorf("PAT %q flags %q vs %q: got %v want %v", tc.pat, tc.flags, tc.input, got, tc.want)
		}
	}
}

// TestRegExpUnicodeEscapeSyntaxErrors 带 u 标志的畸形/超界必须仍是 SyntaxError
// (Node 同): 翻译不能把非法模式"放宽"成合法。
func TestRegExpUnicodeEscapeSyntaxErrors(t *testing.T) {
	for _, pat := range []string{
		`\u{110000}`, // 超出码点上限
		`\uz`,        // 畸形: \u 后不是 4 位 hex 也不是 {hex}
		`\u12`,       // 不足 4 位
	} {
		if _, err := NewRegExp(pat, "u"); err == nil {
			t.Errorf("PAT %q 带 u 标志应当 SyntaxError (Node 同)", pat)
		}
	}
}

package stdlib

import (
	"math"
	"strings"

	"github.com/14752222/Gox/object"
)

// jsWhitespace 是 ECMAScript 定义的 WhiteSpace 与 LineTerminator 集合。
// 比 strings.TrimSpace 更贴近规范: 额外包含 NBSP、各类 Unicode 空格与 BOM。
const jsWhitespace = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004" +
	"\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"

// maxStringLength 是字符串构造的长度上限防护，避免 strings.Repeat 因
// 超大 count 触发 OOM 或 panic (规范中对应 RangeError: Invalid string length)。
const maxStringLength = 1 << 30

// requireObjectCoercible 实现规范的 RequireObjectCoercible 抽象操作：
// null 与 undefined 非法，其余一切（含原始值）都合法。
//
// 与 thisStringValue 是**两个不同的操作**，别混：RequireObjectCoercible 只挡
// null/undefined，之后通常还要 ToString/ToObject 做强制转换；ThisStringValue
// 则要求 this 已经是 String（或 String 对象），不做强制转换。
func requireObjectCoercible(v object.Value) bool {
	if v == nil {
		return false // undefined
	}
	if _, ok := v.(*object.Undefined); ok {
		return false
	}
	if _, ok := v.(*object.Null); ok {
		return false
	}
	return true
}

// thisStringValue 实现规范的 ThisStringValue 抽象操作。
//
// 返回 (*object.String, true) 表示 this 合法；返回 (nil, false) 表示调用方
// 该抛 TypeError。三条分支：
//
//  1. this 是 String 原始值或 String 对象 → 它本身
//  2. this 是 **String.prototype 本身** → 空串
//  3. 其余（普通对象、数字、null、undefined、以及 Object.create(String.prototype)
//     这种"只有原型没有 [[StringData]]"的对象）→ 非法
//
// 第 2 条是这里最容易被漏掉、也最危险的一条。String.prototype 就是一个
// String 对象，其 [[StringData]] 是空串 —— 它不是"普通对象"。Node v22 实测：
//
//	String.prototype.toString()  === ""
//	String.prototype.valueOf()   === ""
//	String.prototype.concat("a") === "a"
//
// 于是漏掉它有**两重后果**，而第二重正是这次 built-ins 跑批崩溃的根因：
//   - 规范偏差：该返回 "" 的地方抛了 TypeError；
//   - 无限递归：一旦按"普通对象"兜底去 ToString(this)，就会掉进
//     ToString ↔ String.prototype.toString 的跨边界循环（详见 toString 的注释）。
//
// 之所以单独抽成函数：本文件里 ThisStringValue 被手抄了 30 处，其中只有
// 少数几处走了这里。抽出来后，其余那些的统一是纯粹的机械替换 —— 那次统一
// 待 built-ins 聚类数据出来后按簇的权重决定排期（见看板 rnm4C5）。
func thisStringValue(this object.Value, proto *object.Object) (*object.String, bool) {
	if s, ok := this.(*object.String); ok {
		return s, true
	}
	// String.prototype: 一个 [[StringData]] 为空串的 String 对象。
	if this == object.Value(proto) {
		return object.NewString(""), true
	}
	return nil, false
}

// setupStringProto 创建 String.prototype 对象。
func setupStringProto() *object.Object {
	p := object.NewObject()

	// toUpperCase()
	p.SetBuiltinProperty("toUpperCase", object.NewBuiltinMethod("toUpperCase", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "toUpperCase", this)
		}
		return object.NewString(strings.ToUpper(s.Value))
	}))

	// toLowerCase()
	p.SetBuiltinProperty("toLowerCase", object.NewBuiltinMethod("toLowerCase", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "toLowerCase", this)
		}
		return object.NewString(strings.ToLower(s.Value))
	}))

	// charAt(index): 按 UTF-16 码元索引，越界返回空串
	p.SetBuiltinProperty("charAt", object.NewBuiltinMethod("charAt", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "charAt", this)
		}
		idx := 0
		if len(args) > 0 {
			idx = int(toInt(args[0]))
		}
		ch, ok := object.CharAtUTF16(s.Value, idx)
		if !ok {
			return object.NewString("")
		}
		return object.NewString(ch)
	}))

	// charCodeAt(index): 按 UTF-16 码元索引，越界返回 NaN
	p.SetBuiltinProperty("charCodeAt", object.NewBuiltinMethod("charCodeAt", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "charCodeAt", this)
		}
		idx := 0
		if len(args) > 0 {
			idx = int(toInt(args[0]))
		}
		code, ok := object.CharCodeAtUTF16(s.Value, idx)
		if !ok {
			// 规范: 越界返回 NaN。旧实现返回 0x10FFFF 是错误的。
			return object.NewNumber(math.NaN())
		}
		return object.NewNumber(code)
	}))

	// codePointAt(index): 返回完整的 Unicode 码点 (代理对会合并)
	p.SetBuiltinProperty("codePointAt", object.NewBuiltinMethod("codePointAt", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "codePointAt", this)
		}
		idx := 0
		if len(args) > 0 {
			idx = int(toInt(args[0]))
		}
		u := object.ToUTF16(s.Value)
		if idx < 0 || idx >= len(u) {
			return object.UndefinedSingleton
		}
		c := u[idx]
		// 高代理项 + 低代理项 → 合并为增补平面码点
		if c >= 0xD800 && c <= 0xDBFF && idx+1 < len(u) {
			lo := u[idx+1]
			if lo >= 0xDC00 && lo <= 0xDFFF {
				return object.NewNumber(float64(0x10000 + (uint32(c)-0xD800)<<10 + (uint32(lo) - 0xDC00)))
			}
		}
		return object.NewNumber(float64(c))
	}))

	// split(separator, limit)
	p.SetBuiltinProperty("split", object.NewBuiltinMethod("split", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "split", this)
		}
		// 无分隔符: 返回整个字符串作为唯一元素
		if len(args) < 1 || isUndefinedValue(args[0]) {
			return object.NewArray([]object.Value{object.NewString(s.Value)})
		}

		// limit 解析: 省略或负数表示不限，0 表示空数组
		limit := -1
		if len(args) > 1 && !isUndefinedValue(args[1]) {
			limit = int(toInt(args[1]))
		}
		if limit == 0 {
			return object.NewArray([]object.Value{})
		}

		// 正则分隔符
		if re, isRe := args[0].(*object.RegExp); isRe {
			parts := re.Regexp.Split(s.Value, limit)
			elements := make([]object.Value, len(parts))
			for i, part := range parts {
				elements[i] = object.NewString(part)
			}
			return object.NewArray(elements)
		}

		sep, isStr := args[0].(*object.String)
		if !isStr {
			sep = object.NewString(toStr(args[0]))
		}
		// 空分隔符: 按 UTF-16 码元逐个拆分
		if sep.Value == "" {
			chars := object.SplitCharsUTF16(s.Value)
			out := make([]object.Value, len(chars))
			for i, c := range chars {
				out[i] = object.NewString(c)
			}
			if limit > 0 && limit < len(out) {
				out = out[:limit]
			}
			return object.NewArray(out)
		}

		parts := strings.Split(s.Value, sep.Value)
		if limit > 0 && limit < len(parts) {
			parts = parts[:limit]
		}
		elements := make([]object.Value, len(parts))
		for i, part := range parts {
			elements[i] = object.NewString(part)
		}
		return object.NewArray(elements)
	}))

	// substring(start, end): 负数按 0 处理，start > end 时自动交换
	p.SetBuiltinProperty("substring", object.NewBuiltinMethod("substring", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "substring", this)
		}
		n := object.UTF16Len(s.Value)
		start := 0
		end := n
		if len(args) > 0 {
			start = clampIndex(toInt(args[0]), n)
		}
		if len(args) > 1 && !isUndefinedValue(args[1]) {
			end = clampIndex(toInt(args[1]), n)
		}
		if start > end {
			start, end = end, start
		}
		return object.NewString(object.SliceUTF16(s.Value, start, end))
	}))

	// slice(start, end): 支持负索引 (从末尾倒数)
	p.SetBuiltinProperty("slice", object.NewBuiltinMethod("slice", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "slice", this)
		}
		n := object.UTF16Len(s.Value)
		start := 0
		end := n
		if len(args) > 0 {
			start = clampIndex(toInt(args[0]), n)
		}
		if len(args) > 1 && !isUndefinedValue(args[1]) {
			end = clampIndex(toInt(args[1]), n)
		}
		if start >= end {
			return object.NewString("")
		}
		return object.NewString(object.SliceUTF16(s.Value, start, end))
	}))

	// indexOf(searchString, fromIndex): 返回码元索引，未找到返回 -1
	p.SetBuiltinProperty("indexOf", object.NewBuiltinMethod("indexOf", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "indexOf", this)
		}
		if len(args) < 1 {
			return object.NewNumber(-1)
		}
		search := toStr(args[0])
		fromIdx := 0
		if len(args) > 1 {
			// 同时钳位下界与上界: 上界防切片越界 panic，
			// 下界按规范是 max(position, 0) 而非从末尾倒数。
			fromIdx = clampStringIndex(toInt(args[1]), object.UTF16Len(s.Value))
		}
		return object.NewNumber(float64(utf16IndexOf(s.Value, search, fromIdx)))
	}))

	// lastIndexOf(searchString, fromIndex): 从后向前查找
	p.SetBuiltinProperty("lastIndexOf", object.NewBuiltinMethod("lastIndexOf", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "lastIndexOf", this)
		}
		if len(args) < 1 {
			return object.NewNumber(-1)
		}
		search := toStr(args[0])
		n := object.UTF16Len(s.Value)
		fromIdx := n // 省略参数时等价于从末尾开始
		if len(args) > 1 && !isUndefinedValue(args[1]) {
			f := toFloat(args[1])
			switch {
			case math.IsInf(f, -1):
				fromIdx = -1 // 规范: 起始位置为负直接返回 -1
			case math.IsInf(f, 1):
				fromIdx = n
			default:
				fromIdx = int(toInt(args[1]))
			}
		}
		return object.NewNumber(float64(utf16LastIndexOf(s.Value, search, fromIdx)))
	}))

	// includes(searchString, fromIndex)
	p.SetBuiltinProperty("includes", object.NewBuiltinMethod("includes", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "includes", this)
		}
		if len(args) < 1 {
			return object.NewBoolean(false)
		}
		search := toStr(args[0])
		fromIdx := 0
		if len(args) > 1 {
			fromIdx = clampStringIndex(toInt(args[1]), object.UTF16Len(s.Value))
		}
		return object.NewBoolean(utf16IndexOf(s.Value, search, fromIdx) >= 0)
	}))

	// startsWith(searchString, position)
	p.SetBuiltinProperty("startsWith", object.NewBuiltinMethod("startsWith", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "startsWith", this)
		}
		if len(args) < 1 {
			return object.NewBoolean(false)
		}
		search := toStr(args[0])
		n := object.UTF16Len(s.Value)
		pos := 0
		if len(args) > 1 {
			pos = clampStringIndex(toInt(args[1]), n)
		}
		return object.NewBoolean(strings.HasPrefix(object.SliceUTF16(s.Value, pos, n), search))
	}))

	// endsWith(searchString, endPosition)
	p.SetBuiltinProperty("endsWith", object.NewBuiltinMethod("endsWith", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "endsWith", this)
		}
		if len(args) < 1 {
			return object.NewBoolean(false)
		}
		search := toStr(args[0])
		n := object.UTF16Len(s.Value)
		endPos := n
		if len(args) > 1 && !isUndefinedValue(args[1]) {
			endPos = clampStringIndex(toInt(args[1]), n)
		}
		return object.NewBoolean(strings.HasSuffix(object.SliceUTF16(s.Value, 0, endPos), search))
	}))

	// trim()
	p.SetBuiltinProperty("trim", object.NewBuiltinMethod("trim", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "trim", this)
		}
		return object.NewString(strings.Trim(s.Value, jsWhitespace))
	}))

	// trimStart() / trimLeft()
	trimStartFn := object.NewBuiltinMethod("trimStart", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "trimStart", this)
		}
		return object.NewString(strings.TrimLeft(s.Value, jsWhitespace))
	})
	p.SetBuiltinProperty("trimStart", trimStartFn)
	p.SetBuiltinProperty("trimLeft", trimStartFn)

	// normalize([form]) (ES6): Unicode 归一化。
	// 运行时不引入 ICU 依赖，NFC/NFD/NFKC/NFKD 统一近似为原样返回
	// (对已归一化的源文本行为正确)。
	p.SetBuiltinProperty("normalize", object.NewBuiltinMethod("normalize", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "normalize", this)
		}
		if len(args) > 0 && !isUndefinedValue(args[0]) {
			form := toStr(args[0])
			switch form {
			case "NFC", "NFD", "NFKC", "NFKD":
			default:
				return object.NewRangeError("The normalization form should be one of NFC, NFD, NFKC, NFKD")
			}
		}
		return s
	}))

	// isWellFormed() (ES2024): 不含孤立代理项时为 true
	p.SetBuiltinProperty("isWellFormed", object.NewBuiltinMethod("isWellFormed", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "isWellFormed", this)
		}
		return object.NewBoolean(!object.HasLoneSurrogates(s.Value))
	}))

	// toWellFormed() (ES2024): 孤立代理项替换为 U+FFFD
	p.SetBuiltinProperty("toWellFormed", object.NewBuiltinMethod("toWellFormed", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "toWellFormed", this)
		}
		return object.NewString(object.ReplaceLoneSurrogates(s.Value))
	}))

	// trimEnd() / trimRight()
	trimEndFn := object.NewBuiltinMethod("trimEnd", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "trimEnd", this)
		}
		return object.NewString(strings.TrimRight(s.Value, jsWhitespace))
	})
	p.SetBuiltinProperty("trimEnd", trimEndFn)
	p.SetBuiltinProperty("trimRight", trimEndFn)

	// replace(searchValue, replaceValue | replaceFn)
	p.SetBuiltinProperty("replace", object.NewBuiltinMethod("replace", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "replace", this)
		}
		if len(args) < 2 {
			return s
		}
		// 处理 RegExp 搜索
		if re, isRe := args[0].(*object.RegExp); isRe {
			replacement := toStr(args[1])
			if object.IsCallable(args[1]) {
				fn := args[1]
				if re.Global {
					result := re.Regexp.ReplaceAllStringFunc(s.Value, func(match string) string {
						r := object.CallFunction(fn, object.UndefinedSingleton, object.NewString(match))
						return toStr(r)
					})
					return object.NewString(result)
				}
				loc := re.Regexp.FindStringIndex(s.Value)
				if loc == nil {
					return s
				}
				matchStr := s.Value[loc[0]:loc[1]]
				r := object.CallFunction(fn, object.UndefinedSingleton, object.NewString(matchStr))
				return object.NewString(s.Value[:loc[0]] + toStr(r) + s.Value[loc[1]:])
			}
			// 字符串替换: 展开 $1/$2/$&/$$ 捕获组
			if re.Global {
				locs := re.Regexp.FindAllStringSubmatchIndex(s.Value, -1)
				if locs == nil {
					return s
				}
				var sb strings.Builder
				lastEnd := 0
				for _, loc := range locs {
					sb.WriteString(s.Value[lastEnd:loc[0]])
					matchStr := s.Value[loc[0]:loc[1]]
					sb.WriteString(expandReplacement(replacement, matchStr,
						submatchGroups(s.Value, loc)))
					lastEnd = loc[1]
				}
				sb.WriteString(s.Value[lastEnd:])
				return object.NewString(sb.String())
			}
			loc := re.Regexp.FindStringSubmatchIndex(s.Value)
			if loc == nil {
				return s
			}
			replacementStr := expandReplacement(replacement,
				s.Value[loc[0]:loc[1]], submatchGroups(s.Value, loc))
			return object.NewString(s.Value[:loc[0]] + replacementStr + s.Value[loc[1]:])
		}

		search := toStr(args[0])
		// 函数回调: replace("pattern", (match) => replacement)
		if object.IsCallable(args[1]) {
			fn := args[1]
			idx := strings.Index(s.Value, search)
			if idx < 0 {
				return s
			}
			matchStr := s.Value[idx : idx+len(search)]
			result := object.CallFunction(fn, object.UndefinedSingleton, object.NewString(matchStr))
			return object.NewString(s.Value[:idx] + toStr(result) + s.Value[idx+len(search):])
		}
		replacement := toStr(args[1])
		return object.NewString(strings.Replace(s.Value, search, replacement, 1))
	}))

	// replaceAll(searchValue, replaceValue | replaceFn)
	p.SetBuiltinProperty("replaceAll", object.NewBuiltinMethod("replaceAll", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "replaceAll", this)
		}
		if len(args) < 2 {
			return s
		}
		// 规范: 用非全局正则做 replaceAll 应抛 TypeError
		if re, isRe := args[0].(*object.RegExp); isRe {
			if !re.Global {
				return object.NewErrorWithName("TypeError",
					"String.prototype.replaceAll called with a non-global RegExp argument")
			}
			replacement := toStr(args[1])
			if object.IsCallable(args[1]) {
				fn := args[1]
				result := re.Regexp.ReplaceAllStringFunc(s.Value, func(match string) string {
					r := object.CallFunction(fn, object.UndefinedSingleton, object.NewString(match))
					return toStr(r)
				})
				return object.NewString(result)
			}
			result := re.Regexp.ReplaceAllStringFunc(s.Value, func(match string) string {
				return expandReplacement(replacement, match, []string{})
			})
			return object.NewString(result)
		}

		search := toStr(args[0])
		// 函数回调
		if object.IsCallable(args[1]) {
			fn := args[1]
			var result strings.Builder
			remaining := s.Value
			for {
				idx := strings.Index(remaining, search)
				if idx < 0 {
					result.WriteString(remaining)
					break
				}
				result.WriteString(remaining[:idx])
				matchStr := remaining[idx : idx+len(search)]
				replaced := object.CallFunction(fn, object.UndefinedSingleton, object.NewString(matchStr))
				result.WriteString(toStr(replaced))
				remaining = remaining[idx+len(search):]
			}
			return object.NewString(result.String())
		}
		replacement := toStr(args[1])
		return object.NewString(strings.ReplaceAll(s.Value, search, replacement))
	}))

	// repeat(count): 负数抛 RangeError，Infinity 亦为非法长度
	p.SetBuiltinProperty("repeat", object.NewBuiltinMethod("repeat", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "repeat", this)
		}
		c := float64(0)
		if len(args) > 0 {
			c = toFloat(args[0])
		}
		if math.IsInf(c, 0) {
			return object.NewErrorWithName("RangeError", "Invalid string length")
		}
		if math.IsNaN(c) {
			c = 0
		}
		count := int(toInt(args[0]))
		if count < 0 {
			return object.NewErrorWithName("RangeError", "Invalid count value: must be non-negative")
		}
		if count > 0 && int64(len(s.Value))*int64(count) > maxStringLength {
			return object.NewErrorWithName("RangeError", "Invalid string length")
		}
		return object.NewString(strings.Repeat(s.Value, count))
	}))

	// padStart(targetLength, padString)
	p.SetBuiltinProperty("padStart", object.NewBuiltinMethod("padStart", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "padStart", this)
		}
		targetLen := 0
		if len(args) > 0 {
			targetLen = int(toInt(args[0]))
		}
		padStr := " "
		if len(args) > 1 && !isUndefinedValue(args[1]) {
			padStr = toStr(args[1])
		}
		// 长度以 UTF-16 码元计，不能用 Go 的字节长度
		n := object.UTF16Len(s.Value)
		if targetLen <= n || padStr == "" {
			return s
		}
		padUnit := object.UTF16Len(padStr)
		if padUnit == 0 {
			return s
		}
		need := targetLen - n
		padding := strings.Repeat(padStr, need/padUnit+1)
		padding = object.SliceUTF16(padding, 0, need)
		return object.NewString(padding + s.Value)
	}))

	// padEnd(targetLength, padString)
	p.SetBuiltinProperty("padEnd", object.NewBuiltinMethod("padEnd", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "padEnd", this)
		}
		targetLen := 0
		if len(args) > 0 {
			targetLen = int(toInt(args[0]))
		}
		padStr := " "
		if len(args) > 1 && !isUndefinedValue(args[1]) {
			padStr = toStr(args[1])
		}
		n := object.UTF16Len(s.Value)
		if targetLen <= n || padStr == "" {
			return s
		}
		padUnit := object.UTF16Len(padStr)
		if padUnit == 0 {
			return s
		}
		need := targetLen - n
		padding := strings.Repeat(padStr, need/padUnit+1)
		padding = object.SliceUTF16(padding, 0, need)
		return object.NewString(s.Value + padding)
	}))

	// concat(...strings)
	//
	// 注意: concat 的规范算法是 RequireObjectCoercible + ToString, **不是**
	// ThisStringValue —— 两者是**不同的抽象操作**, 混用会直接丢用例:
	//
	//	String.prototype.concat.call(42, "x")  === "42x"   (ToString 强制转换)
	//	({toString(){return "one"}}).concat("two", x) === "onetwoundefined"
	//
	// 后者是 ES5 老用例 (S15.5.4.6_A4_T1), 至今仍在 test262 里; 前者是 ES2015
	// 起的口径。只有 null/undefined 必须抛 TypeError (RequireObjectCoercible)。
	// 我第一次改这里时按 ThisStringValue 收口了, 结果前者挂掉 —— 用例级 diff
	// 抓出来的, 肉眼读代码看不出这两个操作的区别。
	p.SetBuiltinProperty("concat", object.NewBuiltinMethod("concat", func(this object.Value, args ...object.Value) object.Value {
		if !requireObjectCoercible(this) {
			return thisTypeError("String", "concat", this)
		}
		result := toStr(this)
		for _, arg := range args {
			result += toStr(arg)
		}
		return object.NewString(result)
	}))

	// toString()
	//
	// this **必须**过 ThisStringValue，不能退化成 toStr(this)：那会造出一条
	// **跨语言边界的无限递归**，Go 侧的递归防御完全拦不住：
	//
	//	ToString(String.prototype)             // object/conversion.go:49
	//	  → CallFunction(obj.toString)         // 跨进 JS 侧
	//	    → String.prototype.toString()      // 旧实现: toStr(this)
	//	      → ToString(String.prototype)     // 跨回 Go 侧, depth **从 0 重算**
	//
	// 关键点在最后一行：object.ToString 的 maxToStringDepth=8 只在**同一侧**
	// 的递归里累加，每绕一圈 JS 边界 depth 都从 0 重新数起，于是计数器永远
	// 到不了阈值。实测一行 `String.prototype.toString()` 就能把进程打挂
	// (fatal error: stack overflow)，built-ins 套件整轮跑批正是被它崩掉。
	//
	// 这类跨边界循环没有"一处防御全覆盖"的解法 —— 必须在**每一侧各自的入口**
	// 做类型校验。规范也正要求如此（见 thisStringValue 的说明）。
	p.SetBuiltinProperty("toString", object.NewBuiltinMethod("toString", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := thisStringValue(this, p); ok {
			return s
		}
		return thisTypeError("String", "toString", this)
	}))

	// valueOf()
	//
	// 与 toString 同款：ThisStringValue 的校验是一样的，崩溃路径也是一样的。
	// 返回值必须是**原始字符串**而非包装对象，故统一走 thisStringValue。
	p.SetBuiltinProperty("valueOf", object.NewBuiltinMethod("valueOf", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := thisStringValue(this, p); ok {
			return s
		}
		return thisTypeError("String", "valueOf", this)
	}))

	// at(index): 支持负索引 (-1 表示最后一个码元)
	p.SetBuiltinProperty("at", object.NewBuiltinMethod("at", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "at", this)
		}
		idx := int64(0)
		if len(args) > 0 {
			idx = toInt(args[0])
		}
		n := int64(object.UTF16Len(s.Value))
		if idx < 0 {
			idx = n + idx
		}
		if idx < 0 || idx >= n {
			return object.UndefinedSingleton
		}
		ch, ok := object.CharAtUTF16(s.Value, int(idx))
		if !ok {
			return object.UndefinedSingleton
		}
		return object.NewString(ch)
	}))

	// match(regexp): 匹配正则表达式，无匹配返回 null
	p.SetBuiltinProperty("match", object.NewBuiltinMethod("match", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "match", this)
		}
		if len(args) == 0 {
			return object.NullSingleton
		}
		re, errVal := toRegExpArg(args[0], "")
		if errVal != nil {
			return errVal
		}
		if re.Global {
			matches := re.Regexp.FindAllString(s.Value, -1)
			if matches == nil {
				return object.NullSingleton
			}
			result := make([]object.Value, len(matches))
			for i, m := range matches {
				result[i] = object.NewString(m)
			}
			return object.NewArray(result)
		}

		loc := re.Regexp.FindStringSubmatchIndex(s.Value)
		if loc == nil {
			return object.NullSingleton
		}
		result := []object.Value{object.NewString(s.Value[loc[0]:loc[1]])}
		groups := submatchGroups(s.Value, loc)
		for _, g := range groups {
			result = append(result, object.NewString(g))
		}
		arr := object.NewArray(result)
		arr.SetProperty("index", object.NewNumber(float64(loc[0])))
		arr.SetProperty("input", object.NewString(s.Value))
		return arr
	}))

	// matchAll(regexp): 返回所有匹配（规范为迭代器，这里简化为数组）
	p.SetBuiltinProperty("matchAll", object.NewBuiltinMethod("matchAll", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "matchAll", this)
		}
		if len(args) == 0 {
			return object.NewArray([]object.Value{})
		}
		// 规范: 非全局正则应抛 TypeError
		re, errVal := toRegExpArg(args[0], "g")
		if errVal != nil {
			return errVal
		}
		if !re.Global {
			return object.NewErrorWithName("TypeError",
				"String.prototype.matchAll called with a non-global RegExp argument")
		}
		allMatches := re.Regexp.FindAllStringSubmatchIndex(s.Value, -1)
		if allMatches == nil {
			return object.NewArray([]object.Value{})
		}
		result := make([]object.Value, len(allMatches))
		for i, loc := range allMatches {
			matchArr := []object.Value{object.NewString(s.Value[loc[0]:loc[1]])}
			groups := submatchGroups(s.Value, loc)
			for _, g := range groups {
				matchArr = append(matchArr, object.NewString(g))
			}
			arr := object.NewArray(matchArr)
			arr.SetProperty("index", object.NewNumber(float64(loc[0])))
			arr.SetProperty("input", object.NewString(s.Value))
			result[i] = arr
		}
		return object.NewArray(result)
	}))

	// search(regexp): 返回第一个匹配的码元索引，未找到返回 -1
	p.SetBuiltinProperty("search", object.NewBuiltinMethod("search", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "search", this)
		}
		if len(args) == 0 {
			return object.NewNumber(-1)
		}
		re, errVal := toRegExpArg(args[0], "")
		if errVal != nil {
			return errVal
		}
		loc := re.Regexp.FindStringIndex(s.Value)
		if loc == nil {
			return object.NewNumber(-1)
		}
		// 字节偏移换算为 UTF-16 码元索引
		return object.NewNumber(float64(object.UTF16Len(s.Value[:loc[0]])))
	}))

	return p
}

// submatchGroups 从 FindStringSubmatchIndex 的结果中提取捕获组文本。
// 未参与匹配的组返回空串。
func submatchGroups(s string, loc []int) []string {
	groups := make([]string, 0, len(loc)/2)
	for i := 1; i < len(loc)/2; i++ {
		if loc[i*2] >= 0 {
			groups = append(groups, s[loc[i*2]:loc[i*2+1]])
		} else {
			groups = append(groups, "")
		}
	}
	return groups
}

// clampStringIndex 把字符串的索引参数规范化到 [0, n]。
//
// 注意: 与数组索引不同，ECMAScript 的 String.prototype.indexOf/includes/
// startsWith/endsWith 的 position 参数**不支持从末尾倒数** ——
// 规范是 start = min(max(position, 0), length)。
// 只有 at() / slice() 这类方法才把负数当作倒数偏移。
func clampStringIndex(idx int64, n int) int {
	if idx < 0 {
		return 0
	}
	if idx > int64(n) {
		return n
	}
	return int(idx)
}

// utf16IndexOf 在 s 中从码元位置 fromIdx 开始查找 search，返回码元索引。
// 未找到返回 -1。索引以 UTF-16 码元计，与 JavaScript 一致。
func utf16IndexOf(s, search string, fromIdx int) int {
	n := object.UTF16Len(s)
	if fromIdx < 0 {
		fromIdx = 0
	}
	if fromIdx > n {
		fromIdx = n
	}
	if search == "" {
		return fromIdx
	}
	tail := object.SliceUTF16(s, fromIdx, n)
	byteIdx := strings.Index(tail, search)
	if byteIdx < 0 {
		return -1
	}
	return fromIdx + object.UTF16Len(tail[:byteIdx])
}

// utf16LastIndexOf 返回 search 在 s 中出现的、起始位置不超过 fromIdx 的
// 最大码元索引。未找到返回 -1。
//
// 规范: 空 searchString 时返回 min(fromIndex, length)；
// 否则从 min(fromIndex, length-len(search)) 处向前查找，
// 该上界小于 0 时直接返回 -1 (于是 lastIndexOf(s, -1) 恒为 -1)。
func utf16LastIndexOf(s, search string, fromIdx int) int {
	n := object.UTF16Len(s)
	if search == "" {
		if fromIdx > n {
			return n
		}
		if fromIdx < 0 {
			return 0
		}
		return fromIdx
	}
	if fromIdx < 0 {
		return -1
	}
	searchLen := object.UTF16Len(search)
	limit := fromIdx
	if limit > n-searchLen {
		limit = n - searchLen
	}
	if limit < 0 {
		return -1
	}
	head := object.SliceUTF16(s, 0, limit+searchLen)
	byteIdx := strings.LastIndex(head, search)
	if byteIdx < 0 {
		return -1
	}
	return object.UTF16Len(head[:byteIdx])
}

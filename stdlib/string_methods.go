package stdlib

import (
	"math"
	"strings"

	"js-runtime/object"
)

// jsWhitespace 是 ECMAScript 定义的 WhiteSpace 与 LineTerminator 集合。
// 比 strings.TrimSpace 更贴近规范: 额外包含 NBSP、各类 Unicode 空格与 BOM。
const jsWhitespace = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004" +
	"\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"

// maxStringLength 是字符串构造的长度上限防护，避免 strings.Repeat 因
// 超大 count 触发 OOM 或 panic (规范中对应 RangeError: Invalid string length)。
const maxStringLength = 1 << 30

// setupStringProto 创建 String.prototype 对象。
func setupStringProto() *object.Object {
	p := object.NewObject()

	// toUpperCase()
	p.SetProperty("toUpperCase", object.NewBuiltinMethod("toUpperCase", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "toUpperCase", this)
		}
		return object.NewString(strings.ToUpper(s.Value))
	}))

	// toLowerCase()
	p.SetProperty("toLowerCase", object.NewBuiltinMethod("toLowerCase", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "toLowerCase", this)
		}
		return object.NewString(strings.ToLower(s.Value))
	}))

	// charAt(index): 按 UTF-16 码元索引，越界返回空串
	p.SetProperty("charAt", object.NewBuiltinMethod("charAt", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("charCodeAt", object.NewBuiltinMethod("charCodeAt", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("codePointAt", object.NewBuiltinMethod("codePointAt", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("split", object.NewBuiltinMethod("split", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("substring", object.NewBuiltinMethod("substring", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("slice", object.NewBuiltinMethod("slice", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("indexOf", object.NewBuiltinMethod("indexOf", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("lastIndexOf", object.NewBuiltinMethod("lastIndexOf", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("includes", object.NewBuiltinMethod("includes", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("startsWith", object.NewBuiltinMethod("startsWith", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("endsWith", object.NewBuiltinMethod("endsWith", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("trim", object.NewBuiltinMethod("trim", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("trimStart", trimStartFn)
	p.SetProperty("trimLeft", trimStartFn)

	// normalize([form]) (ES6): Unicode 归一化。
	// 运行时不引入 ICU 依赖，NFC/NFD/NFKC/NFKD 统一近似为原样返回
	// (对已归一化的源文本行为正确)。
	p.SetProperty("normalize", object.NewBuiltinMethod("normalize", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("isWellFormed", object.NewBuiltinMethod("isWellFormed", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return thisTypeError("String", "isWellFormed", this)
		}
		return object.NewBoolean(!object.HasLoneSurrogates(s.Value))
	}))

	// toWellFormed() (ES2024): 孤立代理项替换为 U+FFFD
	p.SetProperty("toWellFormed", object.NewBuiltinMethod("toWellFormed", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("trimEnd", trimEndFn)
	p.SetProperty("trimRight", trimEndFn)

	// replace(searchValue, replaceValue | replaceFn)
	p.SetProperty("replace", object.NewBuiltinMethod("replace", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("replaceAll", object.NewBuiltinMethod("replaceAll", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("repeat", object.NewBuiltinMethod("repeat", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("padStart", object.NewBuiltinMethod("padStart", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("padEnd", object.NewBuiltinMethod("padEnd", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("concat", object.NewBuiltinMethod("concat", func(this object.Value, args ...object.Value) object.Value {
		result := toStr(this)
		for _, arg := range args {
			result += toStr(arg)
		}
		return object.NewString(result)
	}))

	// toString()
	p.SetProperty("toString", object.NewBuiltinMethod("toString", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			return s
		}
		return object.NewString(toStr(this))
	}))

	// valueOf()
	p.SetProperty("valueOf", object.NewBuiltinMethod("valueOf", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			return s
		}
		return object.NewString(toStr(this))
	}))

	// at(index): 支持负索引 (-1 表示最后一个码元)
	p.SetProperty("at", object.NewBuiltinMethod("at", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("match", object.NewBuiltinMethod("match", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("matchAll", object.NewBuiltinMethod("matchAll", func(this object.Value, args ...object.Value) object.Value {
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
	p.SetProperty("search", object.NewBuiltinMethod("search", func(this object.Value, args ...object.Value) object.Value {
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

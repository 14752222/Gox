package stdlib

import (
	"strings"

	"js-runtime/object"
)

// setupStringProto 创建 String.prototype 对象。
func setupStringProto() *object.Object {
	p := object.NewObject()

	// toUpperCase()
	p.SetProperty("toUpperCase", object.NewBuiltinMethod("toUpperCase", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			return object.NewString(strings.ToUpper(s.Value))
		}
		return object.NewString("")
	}))

	// toLowerCase()
	p.SetProperty("toLowerCase", object.NewBuiltinMethod("toLowerCase", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			return object.NewString(strings.ToLower(s.Value))
		}
		return object.NewString("")
	}))

	// charAt(index)
	p.SetProperty("charAt", object.NewBuiltinMethod("charAt", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			idx := 0
			if len(args) > 0 {
				idx = int(toFloat(args[0]))
			}
			if idx < 0 || idx >= len(s.Value) {
				return object.NewString("")
			}
			return object.NewString(string(s.Value[idx]))
		}
		return object.NewString("")
	}))

	// charCodeAt(index)
	p.SetProperty("charCodeAt", object.NewBuiltinMethod("charCodeAt", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			idx := 0
			if len(args) > 0 {
				idx = int(toFloat(args[0]))
			}
			if idx < 0 || idx >= len(s.Value) {
				return object.NewNumber(0x10FFFF)
			}
			return object.NewNumber(float64(s.Value[idx]))
		}
		return object.NewNumber(0)
	}))

	// split(separator, limit)
	p.SetProperty("split", object.NewBuiltinMethod("split", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			if len(args) < 1 {
				return object.NewArray([]object.Value{object.NewString(s.Value)})
			}
			// 处理 RegExp 分隔符
			if re, ok := args[0].(*object.RegExp); ok {
				limit := -1
				if len(args) > 1 {
					limit = int(toFloat(args[1]))
				}
				parts := re.Regexp.Split(s.Value, limit)
				elements := make([]object.Value, len(parts))
				for i, part := range parts {
					elements[i] = object.NewString(part)
				}
				return object.NewArray(elements)
			}
			sep, ok := args[0].(*object.String)
			if !ok {
				return object.NewArray([]object.Value{object.NewString(s.Value)})
			}
			if sep.Value == "" {
				elements := make([]object.Value, len(s.Value))
				for i, ch := range s.Value {
					elements[i] = object.NewString(string(ch))
				}
				return object.NewArray(elements)
			}
			parts := strings.Split(s.Value, sep.Value)
			limit := len(parts)
			if len(args) > 1 {
				l := int(toFloat(args[1]))
				if l < limit {
					limit = l
				}
				if limit < 0 {
					limit = 0
				}
			}
			elements := make([]object.Value, limit)
			for i := 0; i < limit; i++ {
				elements[i] = object.NewString(parts[i])
			}
			return object.NewArray(elements)
		}
		return object.NewArray([]object.Value{})
	}))

	// substring(start, end)
	p.SetProperty("substring", object.NewBuiltinMethod("substring", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			n := len(s.Value)
			start := 0
			end := n
			if len(args) > 0 {
				start = int(toFloat(args[0]))
			}
			if len(args) > 1 {
				end = int(toFloat(args[1]))
			}
			if start < 0 {
				start = 0
			}
			if end < 0 {
				end = 0
			}
			if start > n {
				start = n
			}
			if end > n {
				end = n
			}
			if start > end {
				start, end = end, start
			}
			return object.NewString(s.Value[start:end])
		}
		return object.NewString("")
	}))

	// slice(start, end)
	p.SetProperty("slice", object.NewBuiltinMethod("slice", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			n := len(s.Value)
			start := 0
			end := n
			if len(args) > 0 {
				start = int(toFloat(args[0]))
				if start < 0 {
					start = n + start
					if start < 0 {
						start = 0
					}
				}
				if start > n {
					start = n
				}
			}
			if len(args) > 1 {
				end = int(toFloat(args[1]))
				if end < 0 {
					end = n + end
					if end < 0 {
						end = 0
					}
				}
				if end > n {
					end = n
				}
			}
			if start >= end {
				return object.NewString("")
			}
			return object.NewString(s.Value[start:end])
		}
		return object.NewString("")
	}))

	// indexOf(searchString, fromIndex)
	p.SetProperty("indexOf", object.NewBuiltinMethod("indexOf", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			if len(args) < 1 {
				return object.NewNumber(-1)
			}
			search := toStr(args[0])
			fromIdx := 0
			if len(args) > 1 {
				fromIdx = int(toFloat(args[1]))
				if fromIdx < 0 {
					fromIdx = 0
				}
			}
			idx := strings.Index(s.Value[fromIdx:], search)
			if idx == -1 {
				return object.NewNumber(-1)
			}
			return object.NewNumber(float64(idx + fromIdx))
		}
		return object.NewNumber(-1)
	}))

	// includes(searchString)
	p.SetProperty("includes", object.NewBuiltinMethod("includes", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			if len(args) < 1 {
				return object.NewBoolean(false)
			}
			search := toStr(args[0])
			return object.NewBoolean(strings.Contains(s.Value, search))
		}
		return object.NewBoolean(false)
	}))

	// startsWith(searchString)
	p.SetProperty("startsWith", object.NewBuiltinMethod("startsWith", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			if len(args) < 1 {
				return object.NewBoolean(false)
			}
			search := toStr(args[0])
			return object.NewBoolean(strings.HasPrefix(s.Value, search))
		}
		return object.NewBoolean(false)
	}))

	// endsWith(searchString)
	p.SetProperty("endsWith", object.NewBuiltinMethod("endsWith", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			if len(args) < 1 {
				return object.NewBoolean(false)
			}
			search := toStr(args[0])
			return object.NewBoolean(strings.HasSuffix(s.Value, search))
		}
		return object.NewBoolean(false)
	}))

	// trim()
	p.SetProperty("trim", object.NewBuiltinMethod("trim", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			return object.NewString(strings.TrimSpace(s.Value))
		}
		return object.NewString("")
	}))

	// trimStart() / trimLeft()
	trimStartFn := object.NewBuiltinMethod("trimStart", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			return object.NewString(strings.TrimLeft(s.Value, " \t\n\r"))
		}
		return object.NewString("")
	})
	p.SetProperty("trimStart", trimStartFn)
	p.SetProperty("trimLeft", trimStartFn)

	// trimEnd() / trimRight()
	trimEndFn := object.NewBuiltinMethod("trimEnd", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			return object.NewString(strings.TrimRight(s.Value, " \t\n\r"))
		}
		return object.NewString("")
	})
	p.SetProperty("trimEnd", trimEndFn)
	p.SetProperty("trimRight", trimEndFn)

	// replace(searchValue, replaceValue | replaceFn)
	p.SetProperty("replace", object.NewBuiltinMethod("replace", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			if len(args) < 2 {
				return s
			}
			// 处理 RegExp 搜索
			if re, ok := args[0].(*object.RegExp); ok {
				replacement := toStr(args[1])
				if object.IsCallable(args[1]) {
					fn := args[1]
					if re.Global {
						result := re.Regexp.ReplaceAllStringFunc(s.Value, func(match string) string {
							r := object.CallFunction(fn, nil, object.NewString(match))
							return toStr(r)
						})
						return object.NewString(result)
					}
					loc := re.Regexp.FindStringIndex(s.Value)
					if loc == nil {
						return s
					}
					matchStr := s.Value[loc[0]:loc[1]]
					r := object.CallFunction(fn, nil, object.NewString(matchStr))
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
						groups := make([]string, 0, len(loc)/2)
						for i := 1; i < len(loc)/2; i++ {
							if loc[i*2] >= 0 {
								groups = append(groups, s.Value[loc[i*2]:loc[i*2+1]])
							} else {
								groups = append(groups, "")
							}
						}
						sb.WriteString(expandReplacement(replacement, matchStr, groups))
						lastEnd = loc[1]
					}
					sb.WriteString(s.Value[lastEnd:])
					return object.NewString(sb.String())
				}
				loc := re.Regexp.FindStringSubmatchIndex(s.Value)
				if loc == nil {
					return s
				}
				matchStr := s.Value[loc[0]:loc[1]]
				groups := make([]string, 0, len(loc)/2)
				for i := 1; i < len(loc)/2; i++ {
					if loc[i*2] >= 0 {
						groups = append(groups, s.Value[loc[i*2]:loc[i*2+1]])
					} else {
						groups = append(groups, "")
					}
				}
				replacementStr := expandReplacement(replacement, matchStr, groups)
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
				result := object.CallFunction(fn, nil, object.NewString(matchStr))
				return object.NewString(s.Value[:idx] + toStr(result) + s.Value[idx+len(search):])
			}
			replacement := toStr(args[1])
			return object.NewString(strings.Replace(s.Value, search, replacement, 1))
		}
		return object.NewString("")
	}))

	// replaceAll(searchValue, replaceValue | replaceFn)
	p.SetProperty("replaceAll", object.NewBuiltinMethod("replaceAll", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			if len(args) < 2 {
				return s
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
					replaced := object.CallFunction(fn, nil, object.NewString(matchStr))
					result.WriteString(toStr(replaced))
					remaining = remaining[idx+len(search):]
				}
				return object.NewString(result.String())
			}
			replacement := toStr(args[1])
			return object.NewString(strings.ReplaceAll(s.Value, search, replacement))
		}
		return object.NewString("")
	}))

	// repeat(count)
	p.SetProperty("repeat", object.NewBuiltinMethod("repeat", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			count := 0
			if len(args) > 0 {
				count = int(toFloat(args[0]))
			}
			if count < 0 {
				return object.NewString("")
			}
			return object.NewString(strings.Repeat(s.Value, count))
		}
		return object.NewString("")
	}))

	// padStart(targetLength, padString)
	p.SetProperty("padStart", object.NewBuiltinMethod("padStart", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			targetLen := 0
			if len(args) > 0 {
				targetLen = int(toFloat(args[0]))
			}
			padStr := " "
			if len(args) > 1 {
				padStr = toStr(args[1])
			}
			if targetLen <= len(s.Value) || padStr == "" {
				return s
			}
			padding := strings.Repeat(padStr, (targetLen-len(s.Value))/len(padStr)+1)
			padding = padding[:targetLen-len(s.Value)]
			return object.NewString(padding + s.Value)
		}
		return object.NewString("")
	}))

	// padEnd(targetLength, padString)
	p.SetProperty("padEnd", object.NewBuiltinMethod("padEnd", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			targetLen := 0
			if len(args) > 0 {
				targetLen = int(toFloat(args[0]))
			}
			padStr := " "
			if len(args) > 1 {
				padStr = toStr(args[1])
			}
			if targetLen <= len(s.Value) || padStr == "" {
				return s
			}
			padding := strings.Repeat(padStr, (targetLen-len(s.Value))/len(padStr)+1)
			padding = padding[:targetLen-len(s.Value)]
			return object.NewString(s.Value + padding)
		}
		return object.NewString("")
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
		return object.NewString(toStr(this))
	}))

	// valueOf()
	p.SetProperty("valueOf", object.NewBuiltinMethod("valueOf", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			return s
		}
		return object.NewString(toStr(this))
	}))

	// at(index): 支持负索引
	p.SetProperty("at", object.NewBuiltinMethod("at", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.String); ok {
			idx := 0
			if len(args) > 0 {
				idx = int(toFloat(args[0]))
			}
			n := len(s.Value)
			if idx < 0 {
				idx = n + idx
			}
			if idx < 0 || idx >= n {
				return object.UndefinedSingleton
			}
			return object.NewString(string(s.Value[idx]))
		}
		return object.UndefinedSingleton
	}))

	// match(regexp): 匹配正则表达式
	p.SetProperty("match", object.NewBuiltinMethod("match", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return object.NullSingleton
		}
		if len(args) == 0 {
			return object.NullSingleton
		}
		// 如果参数不是 RegExp，转换为 RegExp
		var re *object.RegExp
		if r, ok := args[0].(*object.RegExp); ok {
			re = r
		} else {
			pattern := toStr(args[0])
			r, err := object.NewRegExp(pattern, "")
			if err != nil {
				return object.NewErrorWithName("SyntaxError", err.Error())
			}
			re = r
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
		matchStr := s.Value[loc[0]:loc[1]]
		result := []object.Value{object.NewString(matchStr)}
		for i := 1; i < len(loc)/2; i++ {
			if loc[i*2] >= 0 {
				result = append(result, object.NewString(s.Value[loc[i*2]:loc[i*2+1]]))
			} else {
				result = append(result, object.UndefinedSingleton)
			}
		}
		arr := object.NewArray(result)
		arr.SetProperty("index", object.NewNumber(float64(loc[0])))
		arr.SetProperty("input", object.NewString(s.Value))
		return arr
	}))

	// matchAll(regexp): 返回所有匹配的迭代器（简化为数组）
	p.SetProperty("matchAll", object.NewBuiltinMethod("matchAll", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return object.NewArray([]object.Value{})
		}
		if len(args) == 0 {
			return object.NewArray([]object.Value{})
		}
		var re *object.RegExp
		if r, ok := args[0].(*object.RegExp); ok {
			re = r
		} else {
			pattern := toStr(args[0])
			r, err := object.NewRegExp(pattern, "g")
			if err != nil {
				return object.NewArray([]object.Value{})
			}
			re = r
		}

		allMatches := re.Regexp.FindAllStringSubmatchIndex(s.Value, -1)
		if allMatches == nil {
			return object.NewArray([]object.Value{})
		}
		result := make([]object.Value, len(allMatches))
		for i, loc := range allMatches {
			matchStr := s.Value[loc[0]:loc[1]]
			matchArr := []object.Value{object.NewString(matchStr)}
			for j := 1; j < len(loc)/2; j++ {
				if loc[j*2] >= 0 {
					matchArr = append(matchArr, object.NewString(s.Value[loc[j*2]:loc[j*2+1]]))
				} else {
					matchArr = append(matchArr, object.UndefinedSingleton)
				}
			}
			arr := object.NewArray(matchArr)
			arr.SetProperty("index", object.NewNumber(float64(loc[0])))
			arr.SetProperty("input", object.NewString(s.Value))
			result[i] = arr
		}
		return object.NewArray(result)
	}))

	// search(regexp): 返回第一个匹配的索引
	p.SetProperty("search", object.NewBuiltinMethod("search", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.String)
		if !ok {
			return object.NewNumber(-1)
		}
		if len(args) == 0 {
			return object.NewNumber(-1)
		}
		var re *object.RegExp
		if r, ok := args[0].(*object.RegExp); ok {
			re = r
		} else {
			pattern := toStr(args[0])
			r, err := object.NewRegExp(pattern, "")
			if err != nil {
				return object.NewNumber(-1)
			}
			re = r
		}
		loc := re.Regexp.FindStringIndex(s.Value)
		if loc == nil {
			return object.NewNumber(-1)
		}
		return object.NewNumber(float64(loc[0]))
	}))

	return p
}

// setupStringGlobal 创建 String 构造函数对象。
func setupStringGlobal() *object.Object {
	s := object.NewObject()
	s.SetProperty("name", object.NewString("String"))
	s.SetProperty("fromCharCode", object.NewBuiltin("fromCharCode", func(args ...object.Value) object.Value {
		var result strings.Builder
		for _, arg := range args {
			code := int(toFloat(arg))
			if code >= 0 && code <= 0x10FFFF {
				result.WriteRune(rune(code))
			}
		}
		return object.NewString(result.String())
	}))
	return s
}

package stdlib

import (
	"strings"

	"js-runtime/object"
	"js-runtime/runtime"
)

// setupRegExp 设置 RegExp 构造器。
func setupRegExp(env *runtime.Environment) {
	regexpProto := setupRegExpProto()
	object.SetRegExpProto(regexpProto)

	// RegExp 作为函数/构造器
	regexpFn := object.NewBuiltin("RegExp", func(args ...object.Value) object.Value {
		pattern := ""
		flags := ""
		if len(args) > 0 {
			pattern = toStr(args[0])
		}
		if len(args) > 1 {
			flags = toStr(args[1])
		}
		// 如果参数已经是 RegExp，提取 pattern 和 flags
		if len(args) > 0 {
			if r, ok := args[0].(*object.RegExp); ok {
				pattern = r.Pattern
				if len(args) == 1 {
					flags = r.Flags
				}
			}
		}
		re, err := object.NewRegExp(pattern, flags)
		if err != nil {
			return object.NewErrorWithName("SyntaxError", err.Error())
		}
		return re
	})

	// RegExp.escape(str) (ES2025): 转义正则元字符，使字符串可安全内插。
	regexpFn.SetProperty("escape", object.NewBuiltin("escape", func(args ...object.Value) object.Value {
		s := ""
		if len(args) > 0 {
			s = toStr(args[0])
		}
		var b strings.Builder
		for _, ch := range s {
			if strings.ContainsRune("^$\\.*+?()[]{}|/", ch) {
				b.WriteByte('\\')
			}
			b.WriteRune(ch)
		}
		return object.NewString(b.String())
	}))

	env.Declare("RegExp", regexpFn, false)
}

func setupRegExpProto() *object.Object {
	p := object.NewObject()

	// exec(string): 执行匹配
	p.SetProperty("exec", object.NewBuiltinMethod("exec", func(this object.Value, args ...object.Value) object.Value {
		re, ok := this.(*object.RegExp)
		if !ok {
			return object.NullSingleton
		}
		input := ""
		if len(args) > 0 {
			input = toStr(args[0])
		}

		re.Lock()
		defer re.Unlock()

		searchStr := input
		searchPos := 0
		if re.Global && re.LastIndex > 0 {
			if re.LastIndex > len(input) {
				re.LastIndex = 0
				return object.NullSingleton
			}
			searchStr = input[re.LastIndex:]
			searchPos = re.LastIndex
		}

		loc := re.Regexp.FindStringSubmatchIndex(searchStr)
		if loc == nil {
			re.LastIndex = 0
			return object.NullSingleton
		}

		// 构建匹配结果数组
		matchStr := searchStr[loc[0]:loc[1]]
		result := []object.Value{object.NewString(matchStr)}

		// 捕获组
		for i := 1; i < len(loc)/2; i++ {
			if loc[i*2] >= 0 {
				result = append(result, object.NewString(searchStr[loc[i*2]:loc[i*2+1]]))
			} else {
				result = append(result, object.UndefinedSingleton)
			}
		}

		arr := object.NewArray(result)
		arr.SetProperty("index", object.NewNumber(float64(loc[0]+searchPos)))
		arr.SetProperty("input", object.NewString(input))

		if re.Global {
			re.LastIndex = loc[1] + searchPos
		}

		return arr
	}))

	// test(string): 测试是否匹配
	//
	// 规范: 在带 g/y 标志时，test 必须像 exec 一样从 lastIndex 处开始匹配，
	// 成功后推进 lastIndex，失败则重置为 0。
	// 旧实现用 MatchString 全串匹配，完全忽略 lastIndex，导致
	// /a/g.test("abc") 连续调用永远返回 true (无法遍历所有匹配)。
	p.SetProperty("test", object.NewBuiltinMethod("test", func(this object.Value, args ...object.Value) object.Value {
		re, ok := this.(*object.RegExp)
		if !ok {
			return thisTypeError("RegExp", "test", this)
		}
		input := ""
		if len(args) > 0 {
			input = toStr(args[0])
		}

		// 非全局/非粘性: 与 lastIndex 无关，也不修改它
		if !re.Global && !re.Sticky {
			return object.NewBoolean(re.Regexp.MatchString(input))
		}

		if re.LastIndex < 0 {
			re.LastIndex = 0
		}
		if re.LastIndex > len(input) {
			re.LastIndex = 0
			return object.NewBoolean(false)
		}

		loc := re.Regexp.FindStringIndex(input[re.LastIndex:])
		if loc == nil {
			re.LastIndex = 0
			return object.NewBoolean(false)
		}
		// sticky: 匹配必须正好发生在 lastIndex 处 (相对偏移为 0)
		if re.Sticky && loc[0] != 0 {
			re.LastIndex = 0
			return object.NewBoolean(false)
		}
		re.LastIndex += loc[1]
		return object.NewBoolean(true)
	}))

	// toString(): 返回正则的字符串表示
	p.SetProperty("toString", object.NewBuiltinMethod("toString", func(this object.Value, args ...object.Value) object.Value {
		if re, ok := this.(*object.RegExp); ok {
			return object.NewString(re.Inspect())
		}
		return object.NewString("")
	}))

	// [Symbol.match](string): 用于 String.prototype.match
	p.SetProperty(object.NewSymbol("Symbol.match").Inspect(), object.NewBuiltinMethod("[Symbol.match]", func(this object.Value, args ...object.Value) object.Value {
		re, ok := this.(*object.RegExp)
		if !ok {
			return object.NullSingleton
		}
		input := ""
		if len(args) > 0 {
			input = toStr(args[0])
		}

		if re.Global {
			// 全局匹配: 返回所有匹配项
			matches := re.Regexp.FindAllString(input, -1)
			if matches == nil {
				return object.NullSingleton
			}
			result := make([]object.Value, len(matches))
			for i, m := range matches {
				result[i] = object.NewString(m)
			}
			return object.NewArray(result)
		}

		// 非全局: 返回第一个匹配
		loc := re.Regexp.FindStringSubmatchIndex(input)
		if loc == nil {
			return object.NullSingleton
		}

		matchStr := input[loc[0]:loc[1]]
		result := []object.Value{object.NewString(matchStr)}
		for i := 1; i < len(loc)/2; i++ {
			if loc[i*2] >= 0 {
				result = append(result, object.NewString(input[loc[i*2]:loc[i*2+1]]))
			} else {
				result = append(result, object.UndefinedSingleton)
			}
		}

		arr := object.NewArray(result)
		arr.SetProperty("index", object.NewNumber(float64(loc[0])))
		arr.SetProperty("input", object.NewString(input))
		return arr
	}))

	// [Symbol.replace](string, replacement): 用于 String.prototype.replace
	p.SetProperty(object.NewSymbol("Symbol.replace").Inspect(), object.NewBuiltinMethod("[Symbol.replace]", func(this object.Value, args ...object.Value) object.Value {
		re, ok := this.(*object.RegExp)
		if !ok {
			return object.UndefinedSingleton
		}
		input := ""
		if len(args) > 0 {
			input = toStr(args[0])
		}
		replaceStr := ""
		if len(args) > 1 {
			replaceStr = toStr(args[1])
		}

		// 用 FindAllStringSubmatchIndex 获取所有匹配及捕获组，以便展开 $1/$2/$&
		var locs [][]int
		if re.Global {
			locs = re.Regexp.FindAllStringSubmatchIndex(input, -1)
		} else {
			l := re.Regexp.FindStringSubmatchIndex(input)
			if l != nil {
				locs = [][]int{l}
			}
		}
		if locs == nil {
			return object.NewString(input)
		}

		var result strings.Builder
		lastEnd := 0
		for _, loc := range locs {
			// 追加匹配前的文本
			result.WriteString(input[lastEnd:loc[0]])
			matchStr := input[loc[0]:loc[1]]
			// 收集捕获组
			groups := make([]string, 0, len(loc)/2)
			for i := 1; i < len(loc)/2; i++ {
				if loc[i*2] >= 0 {
					groups = append(groups, input[loc[i*2]:loc[i*2+1]])
				} else {
					groups = append(groups, "")
				}
			}
			result.WriteString(expandReplacement(replaceStr, matchStr, groups))
			lastEnd = loc[1]
		}
		result.WriteString(input[lastEnd:])
		return object.NewString(result.String())
	}))

	// [Symbol.split](string, limit): 用于 String.prototype.split
	p.SetProperty(object.NewSymbol("Symbol.split").Inspect(), object.NewBuiltinMethod("[Symbol.split]", func(this object.Value, args ...object.Value) object.Value {
		re, ok := this.(*object.RegExp)
		if !ok {
			return object.NewArray([]object.Value{})
		}
		input := ""
		if len(args) > 0 {
			input = toStr(args[0])
		}
		limit := -1
		if len(args) > 1 {
			limit = int(toFloat(args[1]))
		}

		parts := re.Regexp.Split(input, limit)
		result := make([]object.Value, len(parts))
		for i, part := range parts {
			result[i] = object.NewString(part)
		}
		return object.NewArray(result)
	}))

	return p
}

// toRegExpArg 将参数转换为 *object.RegExp。
//
// 已经是 RegExp 的直接复用；否则把字符串当作 pattern 用 defaultFlags 编译。
// 编译失败时返回 SyntaxError 作为第二返回值，调用方应直接返回它。
func toRegExpArg(v object.Value, defaultFlags string) (*object.RegExp, object.Value) {
	if re, ok := v.(*object.RegExp); ok {
		return re, nil
	}
	re, err := object.NewRegExp(toStr(v), defaultFlags)
	if err != nil {
		return nil, object.NewErrorWithName("SyntaxError", err.Error())
	}
	return re, nil
}

// expandReplacement 展开 $1, $2, $&, $$, $`, $' 等替换模式。
// groups 是捕获组的字符串切片 (不含整个匹配)。
func expandReplacement(replacement, match string, groups []string) string {
	var result strings.Builder
	for i := 0; i < len(replacement); i++ {
		c := replacement[i]
		if c != '$' || i+1 >= len(replacement) {
			result.WriteByte(c)
			continue
		}
		next := replacement[i+1]
		switch {
		case next == '$':
			result.WriteByte('$')
			i++
		case next == '&':
			result.WriteString(match)
			i++
		case next == '`':
			// $` 表示匹配前的文本 (这里无法获取，用空串占位)
			i++
		case next == '\'':
			// $' 表示匹配后的文本 (这里无法获取，用空串占位)
			i++
		case next >= '0' && next <= '9':
			// 捕获组 $1, $2, ..., $99
			num := 0
			j := i + 1
			for j < len(replacement) && replacement[j] >= '0' && replacement[j] <= '9' {
				num = num*10 + int(replacement[j]-'0')
				j++
			}
			if num == 0 {
				// $0 表示整个匹配
				result.WriteString(match)
				i = j - 1
			} else if num <= len(groups) {
				result.WriteString(groups[num-1])
				i = j - 1
			} else {
				// 无对应捕获组，保留字面文本 $n
				result.WriteString(replacement[i : j-1])
				i = j - 2
				continue
			}
		default:
			result.WriteByte('$')
		}
	}
	return result.String()
}

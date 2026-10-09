package stdlib

import (
	"strings"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
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

	// %RegExp.prototype% 必须挂到构造器上 —— 此前 setupRegExpProto() 的产物
	// 只塞进了 object.RegExpProto (链式查找的事实来源), 却没写成构造器的
	// prototype 属性, 于是 `typeof RegExp.prototype` 是 undefined,
	// `RegExp.prototype.exec` 直接 TypeError (rEXjyz)。
	regexpFn.SetProperty("prototype", regexpProto)
	// constructor 反向引用 (/a/.constructor === RegExp)。
	regexpProto.SetBuiltinProperty("constructor", regexpFn)
	// %RegExp.prototype%[@@toStringTag] = "RegExp" —— 实例的
	// Object.prototype.toString.call(/a/) 品牌来自这条原型链上的符号属性。
	setToStringTag(regexpProto, "RegExp")

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

// lastIndexTypeError 是 @@match / @@replace / exec 里「Set(R,"lastIndex",v,true)
// 失败」时应抛的错 (规范 ReturnIfAbrupt ⇒ TypeError)。
func lastIndexTypeError() object.Value {
	return object.NewTypeError("Cannot assign to read only property 'lastIndex'")
}

// regExpThis 校验 %RegExp.prototype% 各方法的 this (规范步骤 1~2):
//   - Type(rx) 不是 Object ⇒ TypeError;
//   - 是对象但没有 [[RegExpMatcher]] 内部槽 (Gox 里即不是 *object.RegExp)
//     ⇒ 同样 TypeError (no-regexp-matcher.js: 对 %RegExp.prototype% 自身调 exec)。
//
// 此前 `%RegExp.prototype%` 根本不存在, 这几个用例靠
// `RegExp.prototype[Symbol.match]` 求值为 undefined 而"抛"出 TypeError 侥幸通过
// (this-val-non-obj 一族)。原型装配好之后必须由这里显式判定。
// 第二返回值为 nil 表示校验通过。
func regExpThis(this object.Value) (*object.RegExp, object.Value) {
	if this == nil || !object.IsObjectValue(this) {
		return nil, object.NewTypeError("RegExp.prototype method called on incompatible receiver")
	}
	re, ok := this.(*object.RegExp)
	if !ok {
		return nil, object.NewTypeError("RegExp.prototype method called on incompatible receiver")
	}
	return re, nil
}

// lastIndexOfResult 从 exec 结果里取 index (规范的 Get(result, "index"))。
// 结果可以是数组 (内建 exec 的产物) 或任意对象 (用户覆写 exec 的产物)。
func lastIndexOfResult(res object.Value) int {
	if res == nil {
		return -1
	}
	v, ok := res.GetProperty("index")
	if !ok || v == nil {
		return -1
	}
	n := int(toFloat(v))
	if n < 0 {
		return -1
	}
	return n
}

// matchStringOfResult 取 exec 结果的整体匹配串 (规范的 Get(result, "0"))。
func matchStringOfResult(res object.Value) string {
	if res == nil {
		return ""
	}
	v, ok := res.GetProperty("0")
	if !ok || v == nil {
		return ""
	}
	return toStr(v)
}

// groupsOfResult 取 exec 结果的 "groups" 属性 (用于 @@replace 的
// ToObject(namedCaptures) 校验)。返回 (值, 是否存在且非 undefined)。
func groupsOfResult(res object.Value) (object.Value, bool) {
	if res == nil {
		return nil, false
	}
	v, ok := res.GetProperty("groups")
	if !ok || v == nil {
		return nil, false
	}
	if v == object.UndefinedSingleton {
		return nil, false
	}
	return v, true
}

// regExpBuiltinExec 是规范 RegExpBuiltinExec(R, S) 的实现, 同时是
// %RegExp.prototype%.exec 与 @@match / @@replace 的内建兜底路径。
//
// 与旧实现的差别: g/y 时「重置 / 推进 lastIndex」全部走 SetLastIndexStrict
// (规范里的 Set(R,"lastIndex",v,true)), 写不进去即抛 TypeError —— 这是
// builtins/RegExp/prototype/Symbol.{match,replace} 八个用例断言的落点。
func regExpBuiltinExec(re *object.RegExp, input string) object.Value {
	re.Lock()
	defer re.Unlock()

	lastIndex := 0
	if re.Global || re.Sticky {
		lastIndex = re.LastIndex
		if lastIndex < 0 {
			lastIndex = 0
		}
		if lastIndex > len(input) {
			if !re.SetLastIndexStrict(0) {
				return lastIndexTypeError()
			}
			return object.NullSingleton
		}
	}

	searchStr := input[lastIndex:]
	loc := re.Regexp.FindStringSubmatchIndex(searchStr)
	if loc == nil {
		if re.Global || re.Sticky {
			if !re.SetLastIndexStrict(0) {
				return lastIndexTypeError()
			}
		}
		return object.NullSingleton
	}
	// sticky: 匹配必须正好发生在 lastIndex 处 (相对偏移为 0)
	if re.Sticky && loc[0] != 0 {
		if !re.SetLastIndexStrict(0) {
			return lastIndexTypeError()
		}
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
	arr.SetProperty("index", object.NewNumber(float64(loc[0]+lastIndex)))
	arr.SetProperty("input", object.NewString(input))

	if re.Global || re.Sticky {
		if !re.SetLastIndexStrict(loc[1] + lastIndex) {
			return lastIndexTypeError()
		}
	}

	return arr
}

// regExpExec 对应规范 RegExpExec(rx, S):
//
//  1. Let exec be Get(rx, "exec").
//  2. If IsCallable(exec) is true, 用它; 否则走 RegExpBuiltinExec。
//
// 读 exec 这一步**本身就是规范要求的副作用点**: test262 的
// builtin-success-g-set-lastindex-err.js 把 exec 定义成 getter, 靠「读 exec」
// 这一下把 lastIndex 改成不可写, 从而使错误发生在**匹配成功之后**的严格
// Set 上。此前 Gox 直接调 Go 侧的 FindAll*, 从不读 exec, 也就永远不抛。
func regExpExec(re *object.RegExp, input string) object.Value {
	// 只有实例上存在**自有** exec 时才经 CallFunction 调用, 否则直接走内建
	// 路径 —— 两个理由:
	//
	// (1) CallFunction 会**清空**回调错误槽。ToString(参数) 阶段挂起的抛出
	//     正记在这个槽里 (coerce-arg-err / arg-1-coerce-err 一族), 无谓地
	//     调一次 CallFunction 就把那个抛出抹掉了。
	// (2) 内建方法「返回」的 Error 值经 CallFunction 后只是普通 Value, 不会
	//     自动变成 JS 抛出, 需要调用方额外判定。
	if re.HasOwn("exec") {
		if exec, ok := re.GetProperty("exec"); ok && object.IsCallable(exec) {
			pending := object.TakeCallbackError()
			res := object.CallFunction(exec, re, object.NewString(input))
			thrown := object.TakeCallbackError()
			if pending != nil {
				// 参数 ToString 阶段已挂起抛出: 原样归还给 VM 传播。
				object.SetCallbackError(pending)
				return object.UndefinedSingleton
			}
			if thrown != nil {
				object.SetCallbackError(thrown)
				return object.UndefinedSingleton
			}
			if res == nil {
				return object.NullSingleton
			}
			return res
		}
	}
	return regExpBuiltinExec(re, input)
}

// isNullishExecResult 判断 exec 结果是否为「未匹配」(null / undefined)。
func isNullishExecResult(v object.Value) bool {
	return v == nil || v == object.NullSingleton || v == object.UndefinedSingleton
}

// isErrorResult 判断 exec 结果是否为错误值 (严格的 lastIndex 写入失败、
// 用户 exec 里 return 出来的 Error)。调用方须原样返回, 使其作为抛出传播。
func isErrorResult(v object.Value) bool {
	_, ok := v.(*object.Error)
	return ok
}

// collectExecResults 反复 exec 直到未匹配, 收集全部结果 (@@match / @@replace
// 的 global 分支共用)。空匹配时按规范 AdvanceStringIndex 推进一位, 否则
// /(?:)/g 会死循环。lastIndex 的任何严格写入失败都以 TypeError 收场。
func collectExecResults(re *object.RegExp, input string) ([]object.Value, object.Value) {
	var results []object.Value
	for i := 0; i <= len(input)+1; i++ {
		res := regExpExec(re, input)
		if isNullishExecResult(res) {
			break
		}
		if isErrorResult(res) {
			return nil, res
		}
		results = append(results, res)
		if matchStringOfResult(res) == "" {
			if !re.SetLastIndexStrict(re.LastIndex + 1) {
				return nil, lastIndexTypeError()
			}
		}
	}
	return results, nil
}

func setupRegExpProto() *object.Object {
	p := object.NewObject()

	// exec(string): 执行匹配
	p.SetBuiltinProperty("exec", object.NewBuiltinMethod("exec", func(this object.Value, args ...object.Value) object.Value {
		re, errVal := regExpThis(this)
		if errVal != nil {
			return errVal
		}
		input := ""
		if len(args) > 0 {
			input = toStr(args[0])
		}
		return regExpBuiltinExec(re, input)
	}))

	// test(string): 测试是否匹配
	//
	// 规范: 在带 g/y 标志时，test 必须像 exec 一样从 lastIndex 处开始匹配，
	// 成功后推进 lastIndex，失败则重置为 0。
	// 旧实现用 MatchString 全串匹配，完全忽略 lastIndex，导致
	// /a/g.test("abc") 连续调用永远返回 true (无法遍历所有匹配)。
	p.SetBuiltinProperty("test", object.NewBuiltinMethod("test", func(this object.Value, args ...object.Value) object.Value {
		re, errVal := regExpThis(this)
		if errVal != nil {
			return errVal
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
	p.SetBuiltinProperty("toString", object.NewBuiltinMethod("toString", func(this object.Value, args ...object.Value) object.Value {
		if re, ok := this.(*object.RegExp); ok {
			return object.NewString(re.Inspect())
		}
		return object.NewString("")
	}))

	// [Symbol.match](string): 用于 String.prototype.match
	//
	// 规范 22.2.5.6: global / sticky 时先 Set(rx,"lastIndex",0,true)
	// (步骤 8.c), 之后每轮 RegExpExec 推进 lastIndex —— 两处写入都是
	// **严格**的, 写不进去即抛 TypeError。此前这里用 FindAllString 一次算完,
	// 既不读 exec 也不碰 lastIndex, 于是 8 个断言 TypeError 的用例全假阳性。
	p.SetBuiltinProperty(object.NewSymbol("Symbol.match").Inspect(), object.NewBuiltinMethod("[Symbol.match]", func(this object.Value, args ...object.Value) object.Value {
		re, errVal := regExpThis(this)
		if errVal != nil {
			return errVal
		}
		input := ""
		if len(args) > 0 {
			input = toStr(args[0])
		}

		if !re.Global && !re.Sticky {
			// 步骤 7: global 为 false ⇒ 直接 RegExpExec(rx, S)。
			res := regExpExec(re, input)
			if isErrorResult(res) {
				return res
			}
			return res
		}

		// 步骤 8.c
		if !re.SetLastIndexStrict(0) {
			return lastIndexTypeError()
		}
		results, errVal := collectExecResults(re, input)
		if errVal != nil {
			return errVal
		}
		if len(results) == 0 {
			return object.NullSingleton
		}
		out := make([]object.Value, len(results))
		for i, res := range results {
			out[i] = object.NewString(matchStringOfResult(res))
		}
		return object.NewArray(out)
	}))

	// [Symbol.replace](string, replacement): 用于 String.prototype.replace
	p.SetBuiltinProperty(object.NewSymbol("Symbol.replace").Inspect(), object.NewBuiltinMethod("[Symbol.replace]", func(this object.Value, args ...object.Value) object.Value {
		re, errVal := regExpThis(this)
		if errVal != nil {
			return errVal
		}
		input := ""
		if len(args) > 0 {
			input = toStr(args[0])
		}
		replaceStr := ""
		if len(args) > 1 {
			replaceStr = toStr(args[1])
		}

		var results []object.Value
		if re.Global {
			// 步骤: Set(rx, "lastIndex", 0, true) 后逐轮 exec。
			if !re.SetLastIndexStrict(0) {
				return lastIndexTypeError()
			}
			rs, errVal := collectExecResults(re, input)
			if errVal != nil {
				return errVal
			}
			results = rs
		} else {
			res := regExpExec(re, input)
			if isErrorResult(res) {
				return res
			}
			if isNullishExecResult(res) {
				return object.NewString(input)
			}
			results = append(results, res)
		}

		// 规范步骤: namedCaptures = Get(result, "groups");
		// 非 undefined ⇒ ToObject(namedCaptures) —— null 抛 TypeError
		// (result-coerce-groups-err.js)。
		for _, res := range results {
			if g, has := groupsOfResult(res); has {
				if g == nil || g == object.NullSingleton {
					return object.NewTypeError("Cannot convert undefined or null to object")
				}
			}
		}

		// 拼接替换结果。exec 可被用户覆写, 返回的 index / 匹配串未必自洽,
		// 越界一律跳过 (宁可少替换也不 panic)。
		var result strings.Builder
		lastEnd := 0
		for _, res := range results {
			start := lastIndexOfResult(res)
			matchStr := matchStringOfResult(res)
			if start < 0 || start < lastEnd || start > len(input) {
				continue
			}
			end := start + len(matchStr)
			if end > len(input) {
				end = len(input)
			}
			result.WriteString(input[lastEnd:start])
			result.WriteString(expandReplacement(replaceStr, matchStr, submatchGroupsOfResult(res)))
			lastEnd = end
		}
		result.WriteString(input[lastEnd:])
		return object.NewString(result.String())
	}))

	// [Symbol.split](string, limit): 用于 String.prototype.split
	p.SetBuiltinProperty(object.NewSymbol("Symbol.split").Inspect(), object.NewBuiltinMethod("[Symbol.split]", func(this object.Value, args ...object.Value) object.Value {
		re, errVal := regExpThis(this)
		if errVal != nil {
			return errVal
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

// submatchGroupsOfResult 从 exec 结果里收集捕获组 (供 $1 / $2 展开),
// 对应旧实现里 submatchGroups(input, loc) 的角色。
func submatchGroupsOfResult(res object.Value) []string {
	if res == nil {
		return nil
	}
	arr, isArr := res.(*object.Array)
	if !isArr {
		return nil
	}
	groups := make([]string, 0, len(arr.Elements))
	for i := 1; i < len(arr.Elements); i++ {
		groups = append(groups, toStr(arr.Elements[i]))
	}
	return groups
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

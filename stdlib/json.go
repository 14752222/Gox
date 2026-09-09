package stdlib

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"

	"js-runtime/object"
)

// setupJSON 创建 JSON 对象。
func setupJSON() *object.Object {
	j := object.NewObject()

	j.SetProperty("stringify", object.NewBuiltin("stringify", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.UndefinedSingleton
		}
		val := args[0]

		// 解析 replacer 参数
		var replacerArr []string
		var replacerFn object.Value
		if len(args) > 1 {
			switch r := args[1].(type) {
			case *object.Array:
				for _, e := range r.Elements {
					replacerArr = append(replacerArr, toStr(e))
				}
			case *object.BuiltinFunction:
				replacerFn = r
			case *object.BuiltinMethod:
				replacerFn = r
			case *object.Closure:
				replacerFn = r
			}
		}

		// 解析 space 参数
		var indent string
		if len(args) > 2 {
			switch s := args[2].(type) {
			case *object.Number:
				n := int(s.Value)
				if n > 0 {
					if n > 10 {
						n = 10
					}
					for i := 0; i < n; i++ {
						indent += " "
					}
				}
			case *object.String:
				if len(s.Value) > 10 {
					indent = s.Value[:10]
				} else {
					indent = s.Value
				}
			}
		}

		// 循环引用检测: 规范要求抛 TypeError。
		// 缺少这一步会让 jsValueToJSONIndent 无限递归直至栈溢出。
		if hasCycle(val) {
			return object.NewErrorWithName("TypeError", "Converting circular structure to JSON")
		}

		result := jsValueToJSONIndent(val, "", indent, replacerArr, replacerFn, "")
		if result == "" {
			return object.UndefinedSingleton
		}
		return object.NewString(result)
	}))

	j.SetProperty("parse", object.NewBuiltin("parse", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewErrorWithName("SyntaxError", "Unexpected end of JSON input")
		}
		// 规范: 参数先经 ToString 转换，JSON.parse(42) 等价于 JSON.parse("42")
		if isUndefinedValue(args[0]) {
			return object.NewErrorWithName("SyntaxError", "undefined is not valid JSON")
		}
		text := toStr(args[0])
		// 用保持键顺序的解析器: encoding/json 直接解析进 map 会让属性顺序
		// 随机化，导致 JSON.stringify(JSON.parse(x)) 与 x 的键顺序不一致。
		ordered, perr := parseOrderedJSON(text)
		if perr != nil {
			// 用 NewErrorWithName 保证 e.name === "SyntaxError"。
			// 旧实现把 "SyntaxError" 拼进了 message，name 仍是 "Error"。
			return object.NewErrorWithName("SyntaxError", perr.Error())
		}
		result := orderedToValue(ordered)
		// 支持 reviver 函数
		if len(args) > 1 && object.IsCallable(args[1]) {
			holder := object.NewObject()
			holder.SetProperty("", result)
			result = applyReviver(args[1], holder, "")
		}
		return result
	}))

	return j
}

// applyReviver 递归应用 reviver 函数。
func applyReviver(reviver object.Value, holder *object.Object, key string) object.Value {
	val, _ := holder.GetProperty(key)

	// 如果是对象，递归处理每个属性
	if obj, ok := val.(*object.Object); ok {
		for _, k := range obj.Keys() {
			childVal := applyReviver(reviver, obj, k)
			if childVal == object.UndefinedSingleton {
				obj.Properties[k] = object.PropertyDescriptor{Value: object.UndefinedSingleton}
				delete(obj.Properties, k)
			} else {
				obj.SetProperty(k, childVal)
			}
		}
	}
	// 如果是数组，递归处理每个元素
	// 注意 holder 必须用与查询时相同的键存放元素。旧实现用 "" 存入、
	// 却用索引字符串取出，导致 reviver 收到的值永远是 nil。
	if arr, ok := val.(*object.Array); ok {
		for i, elem := range arr.Elements {
			h := object.NewObject()
			k := strconv.Itoa(i)
			h.SetProperty(k, elem)
			childVal := applyReviver(reviver, h, k)
			// reviver 返回 undefined 时数组元素变为 undefined (而非删除)
			if childVal == nil || childVal == object.UndefinedSingleton {
				arr.Elements[i] = object.UndefinedSingleton
			} else {
				arr.Elements[i] = childVal
			}
		}
	}

	// 调用 reviver(key, value)
	return object.CallFunction(reviver, nil, object.NewString(key), val)
}

// hasCycle 检测 JS 值中是否存在循环引用。
func hasCycle(v object.Value) bool {
	return detectCycle(v, make(map[object.Value]bool))
}

// detectCycle 深度优先遍历容器类型，seen 记录当前递归路径。
//
// 关键点: 记录的是"路径"而非"全局已访问集合"。同一个对象在不同分支
// 重复出现是合法的 (如 {a: x, b: x})，只有路径上再次遇到自身才是环。
// 因此在递归返回时必须把节点从 seen 中移除。
//
// 只有 *object.Array 与 *object.Object 会被写入 seen，二者均为指针类型，
// 作为 map 键可安全比较。
func detectCycle(v object.Value, seen map[object.Value]bool) bool {
	switch val := v.(type) {
	case *object.Array:
		if seen[val] {
			return true
		}
		seen[val] = true
		for _, e := range val.Elements {
			if detectCycle(e, seen) {
				delete(seen, val)
				return true
			}
		}
		delete(seen, val)
	case *object.Object:
		if seen[val] {
			return true
		}
		seen[val] = true
		for _, k := range val.Keys() {
			if desc, ok := val.Properties[k]; ok {
				if detectCycle(desc.Value, seen) {
					delete(seen, val)
					return true
				}
			}
		}
		delete(seen, val)
	}
	return false
}

// jsonValueWithToJSON 若值带有可调用的 toJSON 方法，先用它替换自身。
//
// 规范 SerializeJSONProperty: 序列化前先查 toJSON，由它的返回值决定输出。
// Temporal 的所有类型都靠这条路径输出 ISO 字符串——它们不是 *object.Object，
// 走不到默认的属性枚举分支。
func jsonValueWithToJSON(v object.Value) object.Value {
	if v == nil {
		return v
	}
	// 原始类型没有 toJSON，跳过可避免多余的属性查找
	switch v.(type) {
	case *object.Number, *object.String, *object.Boolean, *object.Null, *object.Undefined:
		return v
	}
	tj, ok := v.GetProperty("toJSON")
	if !ok || tj == nil || !object.IsCallable(tj) {
		return v
	}
	return object.CallFunction(tj, v)
}

// jsValueToJSONIndent 将 JS 值转换为 JSON 字符串，支持缩进和 replacer。
func jsValueToJSONIndent(v object.Value, currentIndent, indent string, replacerArr []string, replacerFn object.Value, key string) string {
	// 应用 replacer 函数
	if replacerFn != nil {
		v = object.CallFunction(replacerFn, nil, object.NewString(key), v)
	}
	v = jsonValueWithToJSON(v)

	switch val := v.(type) {
	case *object.Number:
		if val.Value != val.Value { // NaN
			return "null"
		}
		if val.Value > 1e308 || val.Value < -1e308 { // Infinity
			return "null"
		}
		b, _ := json.Marshal(val.Value)
		return string(b)
	case *object.String:
		b, _ := json.Marshal(val.Value)
		return string(b)
	case *object.Boolean:
		if val.Value {
			return "true"
		}
		return "false"
	case *object.Null:
		return "null"
	case *object.Undefined:
		return ""
	case *object.Array:
		childIndent := currentIndent + indent
		var parts []string
		for i, elem := range val.Elements {
			s := jsValueToJSONIndent(elem, childIndent, indent, replacerArr, replacerFn, strconv.Itoa(i))
			if s == "" {
				s = "null"
			}
			if indent != "" {
				parts = append(parts, childIndent+s)
			} else {
				parts = append(parts, s)
			}
		}
		if len(parts) == 0 {
			return "[]"
		}
		if indent != "" {
			return "[\n" + strings.Join(parts, ",\n") + "\n" + currentIndent + "]"
		}
		return "[" + strings.Join(parts, ",") + "]"
	case *object.Object:
		childIndent := currentIndent + indent
		var parts []string
		// 如果有 replacer 数组，只包含指定的属性
		var keys []string
		if len(replacerArr) > 0 {
			seen := map[string]bool{}
			for _, k := range replacerArr {
				if !seen[k] {
					seen[k] = true
					if _, exists := val.Properties[k]; exists {
						keys = append(keys, k)
					}
				}
			}
		} else {
			keys = val.Keys()
		}
		for _, k := range keys {
			propDesc, ok := val.Properties[k]
			if !ok {
				continue
			}
			s := jsValueToJSONIndent(propDesc.Value, childIndent, indent, replacerArr, replacerFn, k)
			if s == "" {
				continue
			}
			kJSON, _ := json.Marshal(k)
			if indent != "" {
				parts = append(parts, childIndent+string(kJSON)+": "+s)
			} else {
				parts = append(parts, string(kJSON)+":"+s)
			}
		}
		if len(parts) == 0 {
			return "{}"
		}
		if indent != "" {
			return "{\n" + strings.Join(parts, ",\n") + "\n" + currentIndent + "}"
		}
		return "{" + strings.Join(parts, ",") + "}"
	case *object.Closure, *object.BuiltinFunction, *object.CompiledFunction:
		return ""
	}
	return ""
}

// jsValueToJSON 将 JS 值转换为 JSON 字符串。
func jsValueToJSON(v object.Value) string {
	v = jsonValueWithToJSON(v)
	switch val := v.(type) {
	case *object.Number:
		if val.Value != val.Value { // NaN
			return "null"
		}
		b, _ := json.Marshal(val.Value)
		return string(b)
	case *object.String:
		b, _ := json.Marshal(val.Value)
		return string(b)
	case *object.Boolean:
		if val.Value {
			return "true"
		}
		return "false"
	case *object.Null:
		return "null"
	case *object.Undefined:
		return ""
	case *object.Array:
		var parts []string
		for _, elem := range val.Elements {
			s := jsValueToJSON(elem)
			if s == "" {
				s = "null"
			}
			parts = append(parts, s)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case *object.Object:
		keys := val.Keys()
		var parts []string
		for _, k := range keys {
			propDesc, ok := val.Properties[k]
			if !ok {
				continue
			}
			s := jsValueToJSON(propDesc.Value)
			if s == "" {
				continue
			}
			kJSON, _ := json.Marshal(k)
			parts = append(parts, string(kJSON)+":"+s)
		}
		return "{" + strings.Join(parts, ",") + "}"
	case *object.Closure, *object.BuiltinFunction, *object.CompiledFunction:
		return ""
	}
	return ""
}

// jsonToJSValue 将 Go 值转换为 JS 对象。
// ===== 保持键顺序的 JSON 解析 =====
//
// encoding/json 把对象解析进 map[string]interface{}，遍历顺序是随机的。
// 这会让 JSON.parse 得到的对象属性顺序不稳定，进而导致
// JSON.stringify(JSON.parse(x)) 的输出与 x 的键顺序不一致 —— 规范要求
// 序列化保持对象属性的原始顺序，因此这里改用 Decoder 逐 token 解析。
//
// 附带收益: 用 UseNumber() 保留数字字面量，避免大整数在 float64 转换中
// 丢失精度。

type jsonKind int

const (
	kindScalar jsonKind = iota
	kindArray
	kindObject
)

// orderedValue 是 JSON 解析的中间表示，对象保留键的原始顺序。
type orderedValue struct {
	kind jsonKind
	obj  *orderedObject
	arr  []*orderedValue
	raw  interface{}
}

// orderedObject 保持键插入顺序的 JSON 对象。
type orderedObject struct {
	keys   []string
	values map[string]*orderedValue
}

func newOrderedObject() *orderedObject {
	return &orderedObject{values: make(map[string]*orderedValue)}
}

// set 写入键值。已存在的键保持原有位置 (后写覆盖先写，与 JS 对象一致)。
func (o *orderedObject) set(key string, v *orderedValue) {
	if _, exists := o.values[key]; !exists {
		o.keys = append(o.keys, key)
	}
	o.values[key] = v
}

// parseOrderedJSON 解析 JSON 文本，返回保持键顺序的中间表示。
func parseOrderedJSON(text string) (*orderedValue, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	v, err := parseOrderedValue(dec)
	if err != nil {
		return nil, err
	}
	// 值之后只允许空白
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected trailing content after JSON value")
	}
	return v, nil
}

func parseOrderedValue(dec *json.Decoder) (*orderedValue, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := newOrderedObject()
			for dec.More() {
				keyTok, kerr := dec.Token()
				if kerr != nil {
					return nil, kerr
				}
				key, ok := keyTok.(string)
				if !ok {
					return nil, errors.New("invalid JSON: object key must be a string")
				}
				val, verr := parseOrderedValue(dec)
				if verr != nil {
					return nil, verr
				}
				obj.set(key, val)
			}
			if _, err := dec.Token(); err != nil { // 消费 '}'
				return nil, err
			}
			return &orderedValue{kind: kindObject, obj: obj}, nil
		case '[':
			arr := []*orderedValue{}
			for dec.More() {
				val, verr := parseOrderedValue(dec)
				if verr != nil {
					return nil, verr
				}
				arr = append(arr, val)
			}
			if _, err := dec.Token(); err != nil { // 消费 ']'
				return nil, err
			}
			return &orderedValue{kind: kindArray, arr: arr}, nil
		}
		return nil, errors.New("invalid JSON: unexpected delimiter")
	case json.Number:
		return &orderedValue{kind: kindScalar, raw: t}, nil
	case string, bool, nil:
		return &orderedValue{kind: kindScalar, raw: t}, nil
	}
	return nil, errors.New("invalid JSON")
}

// orderedToValue 将有序中间表示转换为 JS 值。
func orderedToValue(v *orderedValue) object.Value {
	switch v.kind {
	case kindObject:
		obj := object.NewObject()
		for _, k := range v.obj.keys {
			obj.SetProperty(k, orderedToValue(v.obj.values[k]))
		}
		return obj
	case kindArray:
		elements := make([]object.Value, len(v.arr))
		for i, e := range v.arr {
			elements[i] = orderedToValue(e)
		}
		return object.NewArray(elements)
	}
	switch val := v.raw.(type) {
	case nil:
		return object.NullSingleton
	case bool:
		return object.NewBoolean(val)
	case string:
		return object.NewString(val)
	case json.Number:
		f, err := val.Float64()
		if err != nil {
			return object.NewNumber(math.NaN())
		}
		return object.NewNumber(f)
	case float64:
		return object.NewNumber(val)
	}
	return object.UndefinedSingleton
}

// parseFloatString 按 ECMAScript 的 parseFloat 语义解析字符串。
//
// 与 strconv.ParseFloat 不同的是它会截断: 只读取最长的合法数字前缀，
// 忽略后续非法字符。
//
//	parseFloat("3.14abc") -> 3.14   (strconv 会整体失败)
//	parseFloat("Infinity") -> +Inf
//	parseFloat("abc")     -> NaN
//	parseFloat("  ")      -> NaN
func parseFloatString(s string) (float64, bool) {
	t := trimJSSpace(s)
	if t == "" {
		return 0, false
	}
	if strings.HasPrefix(t, "Infinity") || strings.HasPrefix(t, "+Infinity") {
		return math.Inf(1), true
	}
	if strings.HasPrefix(t, "-Infinity") {
		return math.Inf(-1), true
	}
	end := scanNumberPrefix(t)
	if end == 0 {
		return 0, false
	}
	f, err := strconv.ParseFloat(t[:end], 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// scanNumberPrefix 返回 s 中最长的合法十进制数字字面量前缀的长度。
// 语法: [+-]? ( digits ( "." digits? )? | "." digits ) ( [eE] [+-]? digits )?
// 无法构成合法数字时返回 0。
func scanNumberPrefix(s string) int {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	intStart := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	hasIntDigits := i > intStart

	if i < len(s) && s[i] == '.' {
		i++
		fracStart := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if !hasIntDigits && i == fracStart {
			return 0 // 只有一个小数点
		}
	} else if !hasIntDigits {
		return 0
	}

	// 指数部分: 没有数字时整体回退 (如 "1e" 只应解析出 "1")
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		save := i
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		expStart := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == expStart {
			i = save
		}
	}
	return i
}

// parseIntString 按 ECMAScript 的 parseInt 语义解析字符串。
//
// radix 传 0 表示调用方未指定基数 (等价于 undefined)。此时按 10 处理，
// 但允许 "0x"/"0X" 前缀自动识别为 16 进制。
//
// 与 strconv.ParseInt 不同的是它会截断:
//
//	parseInt("12abc")   -> 12    (strconv 会整体失败)
//	parseInt("0x10")    -> 16
//	parseInt("0x10", 10)-> 0     (radix 明确为 10 时不识别 0x 前缀)
//	parseInt("7", 8)    -> 7
func parseIntString(s string, radix int) (int64, bool) {
	t := trimJSSpace(s)
	if t == "" {
		return 0, false
	}

	negative := false
	if t[0] == '-' {
		negative = true
		t = t[1:]
	} else if t[0] == '+' {
		t = t[1:]
	}

	if radix == 0 {
		// 未指定基数: 默认 10，遇到 0x 前缀转 16 进制
		radix = 10
		if len(t) >= 2 && t[0] == '0' && (t[1] == 'x' || t[1] == 'X') {
			t = t[2:]
			radix = 16
		}
	} else {
		if radix < 2 || radix > 36 {
			return 0, false
		}
		// 基数明确为 16 时允许 0x 前缀
		if radix == 16 && len(t) >= 2 && t[0] == '0' && (t[1] == 'x' || t[1] == 'X') {
			t = t[2:]
		}
	}

	// 截断: 只取当前基数下的合法数字前缀
	end := 0
	for end < len(t) && digitValue(t[end]) < radix {
		end++
	}
	if end == 0 {
		return 0, false
	}

	i, err := strconv.ParseInt(t[:end], radix, 64)
	if err != nil {
		// 超出 int64 范围时退化为浮点 (如 parseInt 一个超长数字串)
		f, ferr := strconv.ParseFloat(t[:end], 64)
		if ferr != nil {
			return 0, false
		}
		if negative {
			f = -f
		}
		return int64(f), true
	}
	if negative {
		i = -i
	}
	return i, true
}

// digitValue 返回字符在 36 进制下的数值。非字母数字字符返回 255。
func digitValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	}
	return 255
}

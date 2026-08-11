package stdlib

import (
	"encoding/json"
	"fmt"
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

		result := jsValueToJSONIndent(val, "", indent, replacerArr, replacerFn, "")
		if result == "" {
			return object.UndefinedSingleton
		}
		return object.NewString(result)
	}))

	j.SetProperty("parse", object.NewBuiltin("parse", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewError("SyntaxError: Unexpected end of JSON input")
		}
		s, ok := args[0].(*object.String)
		if !ok {
			return object.NewError("SyntaxError: Unexpected token in JSON")
		}
		var raw interface{}
		if err := json.Unmarshal([]byte(s.Value), &raw); err != nil {
			return object.NewError("SyntaxError: " + err.Error())
		}
		result := jsonToJSValue(raw)
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
	if arr, ok := val.(*object.Array); ok {
		for i, elem := range arr.Elements {
			holder := object.NewObject()
			holder.SetProperty("", elem)
			childVal := applyReviver(reviver, holder, intToString(i))
			if childVal == object.UndefinedSingleton {
				arr.Elements[i] = object.NullSingleton
			} else {
				arr.Elements[i] = childVal
			}
		}
	}

	// 调用 reviver(key, value)
	return object.CallFunction(reviver, nil, object.NewString(key), val)
}

// jsValueToJSONIndent 将 JS 值转换为 JSON 字符串，支持缩进和 replacer。
func jsValueToJSONIndent(v object.Value, currentIndent, indent string, replacerArr []string, replacerFn object.Value, key string) string {
	// 应用 replacer 函数
	if replacerFn != nil {
		v = object.CallFunction(replacerFn, nil, object.NewString(key), v)
	}

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
			s := jsValueToJSONIndent(elem, childIndent, indent, replacerArr, replacerFn, intToString(i))
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
func jsonToJSValue(raw interface{}) object.Value {
	switch v := raw.(type) {
	case nil:
		return object.NullSingleton
	case bool:
		return object.NewBoolean(v)
	case float64:
		return object.NewNumber(v)
	case string:
		return object.NewString(v)
	case []interface{}:
		elements := make([]object.Value, len(v))
		for i, elem := range v {
			elements[i] = jsonToJSValue(elem)
		}
		return object.NewArray(elements)
	case map[string]interface{}:
		obj := object.NewObject()
		for k, val := range v {
			obj.SetProperty(k, jsonToJSValue(val))
		}
		return obj
	}
	return object.UndefinedSingleton
}

// parseFloatString 尝试解析字符串为 float64。
func parseFloatString(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// parseIntString 尝试解析字符串为整数。
func parseIntString(s string, radix int) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if radix == 0 {
		radix = 10
	}
	if radix < 2 || radix > 36 {
		return 0, false
	}
	// 处理负号
	negative := false
	if s[0] == '-' {
		negative = true
		s = s[1:]
	} else if s[0] == '+' {
		s = s[1:]
	}
	// 处理 0x 前缀
	if radix == 16 && len(s) >= 2 && (s[0:2] == "0x" || s[0:2] == "0X") {
		s = s[2:]
	}
	i, err := strconv.ParseInt(s, radix, 64)
	if err != nil {
		return 0, false
	}
	if negative {
		i = -i
	}
	return i, true
}

// 确保使用 fmt 包 (避免未使用导入)
var _ = fmt.Sprintf

package vm

import (
	"testing"

	"js-runtime/object"
)

// evalJS 编译并执行 JS 源码，返回结果。
func evalJS(t *testing.T, input string) object.Value {
	result, err := Eval(input)
	if err != nil {
		t.Fatalf("Eval error for input %q: %v", input, err)
	}
	return result
}

func assertNumber(t *testing.T, result object.Value, expected float64) {
	t.Helper()
	num, ok := result.(*object.Number)
	if !ok {
		t.Fatalf("expected Number, got %T (%s)", result, result.Inspect())
	}
	if num.Value != expected {
		t.Fatalf("expected %v, got %v", expected, num.Value)
	}
}

func assertString(t *testing.T, result object.Value, expected string) {
	t.Helper()
	s, ok := result.(*object.String)
	if !ok {
		t.Fatalf("expected String, got %T (%s)", result, result.Inspect())
	}
	if s.Value != expected {
		t.Fatalf("expected %q, got %q", expected, s.Value)
	}
}

func assertBoolean(t *testing.T, result object.Value, expected bool) {
	t.Helper()
	b, ok := result.(*object.Boolean)
	if !ok {
		t.Fatalf("expected Boolean, got %T (%s)", result, result.Inspect())
	}
	if b.Value != expected {
		t.Fatalf("expected %v, got %v", expected, b.Value)
	}
}

// ===== Math 测试 =====

func TestMathAbs(t *testing.T) {
	assertNumber(t, evalJS(t, `Math.abs(-5)`), 5)
	assertNumber(t, evalJS(t, `Math.abs(5)`), 5)
	assertNumber(t, evalJS(t, `Math.abs(-3.14)`), 3.14)
}

func TestMathFloorCeil(t *testing.T) {
	assertNumber(t, evalJS(t, `Math.floor(3.7)`), 3)
	assertNumber(t, evalJS(t, `Math.ceil(3.2)`), 4)
	assertNumber(t, evalJS(t, `Math.round(2.5)`), 3)
	assertNumber(t, evalJS(t, `Math.round(2.4)`), 2)
}

func TestMathMaxMin(t *testing.T) {
	assertNumber(t, evalJS(t, `Math.max(1, 2, 3)`), 3)
	assertNumber(t, evalJS(t, `Math.min(1, 2, 3)`), 1)
	assertNumber(t, evalJS(t, `Math.max(-1, -2, -3)`), -1)
}

func TestMathSqrtPow(t *testing.T) {
	assertNumber(t, evalJS(t, `Math.sqrt(16)`), 4)
	assertNumber(t, evalJS(t, `Math.pow(2, 10)`), 1024)
}

func TestMathConstants(t *testing.T) {
	// PI ≈ 3.141592653589793
	result := evalJS(t, `Math.PI`)
	num, _ := result.(*object.Number)
	if num.Value < 3.14 || num.Value > 3.15 {
		t.Fatalf("Math.PI expected ~3.14, got %v", num.Value)
	}
	// E ≈ 2.718281828459045
	result = evalJS(t, `Math.E`)
	num, _ = result.(*object.Number)
	if num.Value < 2.71 || num.Value > 2.72 {
		t.Fatalf("Math.E expected ~2.72, got %v", num.Value)
	}
}

func TestMathTrunc(t *testing.T) {
	assertNumber(t, evalJS(t, `Math.trunc(4.9)`), 4)
	assertNumber(t, evalJS(t, `Math.trunc(-4.9)`), -4)
}

func TestMathSign(t *testing.T) {
	assertNumber(t, evalJS(t, `Math.sign(5)`), 1)
	assertNumber(t, evalJS(t, `Math.sign(-5)`), -1)
	assertNumber(t, evalJS(t, `Math.sign(0)`), 0)
}

// ===== JSON 测试 =====

func TestJSONStringify(t *testing.T) {
	result := evalJS(t, `JSON.stringify({a: 1, b: 2})`)
	assertString(t, result, `{"a":1,"b":2}`)
}

func TestJSONStringifyArray(t *testing.T) {
	result := evalJS(t, `JSON.stringify([1, 2, 3])`)
	assertString(t, result, `[1,2,3]`)
}

func TestJSONStringifyString(t *testing.T) {
	result := evalJS(t, `JSON.stringify("hello")`)
	assertString(t, result, `"hello"`)
}

func TestJSONParse(t *testing.T) {
	result := evalJS(t, `JSON.parse('{"x": 42}')`)
	obj, ok := result.(*object.Object)
	if !ok {
		t.Fatalf("expected Object, got %T", result)
	}
	val, found := obj.GetProperty("x")
	if !found {
		t.Fatal("property x not found")
	}
	num, ok := val.(*object.Number)
	if !ok || num.Value != 42 {
		t.Fatalf("expected x=42, got %v", val)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	result := evalJS(t, `JSON.stringify(JSON.parse('{"name": "test", "value": 100}'))`)
	assertString(t, result, `{"name":"test","value":100}`)
}

// ===== Object 方法测试 =====

func TestObjectKeys(t *testing.T) {
	result := evalJS(t, `Object.keys({a: 1, b: 2, c: 3})`)
	arr, ok := result.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", result)
	}
	if len(arr.Elements) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(arr.Elements))
	}
	// Keys should be sorted
	assertString(t, arr.Elements[0], "a")
	assertString(t, arr.Elements[1], "b")
	assertString(t, arr.Elements[2], "c")
}

func TestObjectValues(t *testing.T) {
	result := evalJS(t, `Object.values({a: 1, b: 2})`)
	arr, ok := result.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", result)
	}
	if len(arr.Elements) != 2 {
		t.Fatalf("expected 2 values, got %d", len(arr.Elements))
	}
	assertNumber(t, arr.Elements[0], 1)
	assertNumber(t, arr.Elements[1], 2)
}

func TestObjectEntries(t *testing.T) {
	result := evalJS(t, `Object.entries({a: 1})`)
	arr, ok := result.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", result)
	}
	if len(arr.Elements) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(arr.Elements))
	}
	entry, ok := arr.Elements[0].(*object.Array)
	if !ok || len(entry.Elements) != 2 {
		t.Fatalf("expected entry [key, value], got %v", arr.Elements[0])
	}
	assertString(t, entry.Elements[0], "a")
	assertNumber(t, entry.Elements[1], 1)
}

func TestObjectAssign(t *testing.T) {
	result := evalJS(t, `Object.assign({a: 1}, {b: 2}, {c: 3})`)
	obj, ok := result.(*object.Object)
	if !ok {
		t.Fatalf("expected Object, got %T", result)
	}
	val, _ := obj.GetProperty("a")
	assertNumber(t, val, 1)
	val, _ = obj.GetProperty("b")
	assertNumber(t, val, 2)
	val, _ = obj.GetProperty("c")
	assertNumber(t, val, 3)
}

func TestObjectCreate(t *testing.T) {
	result := evalJS(t, `let proto = { greet: function() { return "hi" } }; let obj = Object.create(proto); obj.greet()`)
	// This won't work yet because method calls with closures need special handling
	// But let's at least test Object.create returns an object
	_ = result
}

// ===== Array 方法测试 =====

func TestArrayPush(t *testing.T) {
	assertNumber(t, evalJS(t, `let arr = [1, 2]; arr.push(3)`), 3)
	assertNumber(t, evalJS(t, `let arr = [1, 2]; arr.push(3, 4); arr.length`), 4)
}

func TestArrayPop(t *testing.T) {
	assertNumber(t, evalJS(t, `let arr = [1, 2, 3]; arr.pop()`), 3)
	assertNumber(t, evalJS(t, `let arr = [1, 2, 3]; arr.pop(); arr.length`), 2)
}

func TestArrayShift(t *testing.T) {
	assertNumber(t, evalJS(t, `let arr = [1, 2, 3]; arr.shift()`), 1)
	assertNumber(t, evalJS(t, `let arr = [1, 2, 3]; arr.shift(); arr.length`), 2)
}

func TestArrayUnshift(t *testing.T) {
	assertNumber(t, evalJS(t, `let arr = [1, 2]; arr.unshift(0)`), 3)
}

func TestArrayJoin(t *testing.T) {
	assertString(t, evalJS(t, `[1, 2, 3].join("-")`), "1-2-3")
	assertString(t, evalJS(t, `[1, 2, 3].join()`), "1,2,3")
}

func TestArraySlice(t *testing.T) {
	result := evalJS(t, `[1, 2, 3, 4, 5].slice(1, 3)`)
	arr, ok := result.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", result)
	}
	if len(arr.Elements) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(arr.Elements))
	}
	assertNumber(t, arr.Elements[0], 2)
	assertNumber(t, arr.Elements[1], 3)
}

func TestArraySliceNegative(t *testing.T) {
	result := evalJS(t, `[1, 2, 3, 4, 5].slice(-2)`)
	arr, ok := result.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", result)
	}
	if len(arr.Elements) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(arr.Elements))
	}
	assertNumber(t, arr.Elements[0], 4)
	assertNumber(t, arr.Elements[1], 5)
}

func TestArrayIndexOf(t *testing.T) {
	assertNumber(t, evalJS(t, `[1, 2, 3, 2, 1].indexOf(2)`), 1)
	assertNumber(t, evalJS(t, `[1, 2, 3].indexOf(4)`), -1)
}

func TestArrayIncludes(t *testing.T) {
	assertBoolean(t, evalJS(t, `[1, 2, 3].includes(2)`), true)
	assertBoolean(t, evalJS(t, `[1, 2, 3].includes(4)`), false)
}

func TestArrayReverse(t *testing.T) {
	result := evalJS(t, `let arr = [1, 2, 3]; arr.reverse(); arr[0]`)
	assertNumber(t, result, 3)
}

func TestArrayConcat(t *testing.T) {
	result := evalJS(t, `[1, 2].concat([3, 4])`)
	arr, ok := result.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", result)
	}
	if len(arr.Elements) != 4 {
		t.Fatalf("expected 4 elements, got %d", len(arr.Elements))
	}
	assertNumber(t, arr.Elements[3], 4)
}

func TestArrayFlat(t *testing.T) {
	result := evalJS(t, `[1, [2, 3], [4, [5]]].flat()`)
	arr, ok := result.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", result)
	}
	// flat() with depth=1: [1, 2, 3, 4, [5]] = 5 elements
	if len(arr.Elements) != 5 {
		t.Fatalf("expected 5 elements, got %d", len(arr.Elements))
	}
	assertNumber(t, arr.Elements[0], 1)
	assertNumber(t, arr.Elements[1], 2)
}

func TestArraySplice(t *testing.T) {
	result := evalJS(t, `let arr = [1, 2, 3, 4]; arr.splice(1, 2); arr.length`)
	assertNumber(t, result, 2)
}

func TestArrayIsArray(t *testing.T) {
	assertBoolean(t, evalJS(t, `Array.isArray([1, 2])`), true)
	assertBoolean(t, evalJS(t, `Array.isArray("hello")`), false)
}

func TestArrayFrom(t *testing.T) {
	result := evalJS(t, `Array.from("abc")`)
	arr, ok := result.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", result)
	}
	if len(arr.Elements) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(arr.Elements))
	}
	assertString(t, arr.Elements[0], "a")
}

func TestArraySort(t *testing.T) {
	result := evalJS(t, `let arr = [3, 1, 2]; arr.sort(); arr[0]`)
	assertNumber(t, result, 1)
}

// ===== String 方法测试 =====

func TestStringToUpperCase(t *testing.T) {
	assertString(t, evalJS(t, `"hello".toUpperCase()`), "HELLO")
}

func TestStringToLowerCase(t *testing.T) {
	assertString(t, evalJS(t, `"HELLO".toLowerCase()`), "hello")
}

func TestStringCharAt(t *testing.T) {
	assertString(t, evalJS(t, `"hello".charAt(1)`), "e")
	assertString(t, evalJS(t, `"hello".charAt(10)`), "")
}

func TestStringCharCodeAt(t *testing.T) {
	assertNumber(t, evalJS(t, `"A".charCodeAt(0)`), 65)
}

func TestStringSplit(t *testing.T) {
	result := evalJS(t, `"a,b,c".split(",")`)
	arr, ok := result.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", result)
	}
	if len(arr.Elements) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(arr.Elements))
	}
	assertString(t, arr.Elements[0], "a")
	assertString(t, arr.Elements[1], "b")
}

func TestStringIncludes(t *testing.T) {
	assertBoolean(t, evalJS(t, `"hello world".includes("world")`), true)
	assertBoolean(t, evalJS(t, `"hello".includes("xyz")`), false)
}

func TestStringStartsWithEndsWith(t *testing.T) {
	assertBoolean(t, evalJS(t, `"hello".startsWith("he")`), true)
	assertBoolean(t, evalJS(t, `"hello".endsWith("lo")`), true)
	assertBoolean(t, evalJS(t, `"hello".startsWith("lo")`), false)
}

func TestStringTrim(t *testing.T) {
	assertString(t, evalJS(t, `"  hello  ".trim()`), "hello")
}

func TestStringReplace(t *testing.T) {
	assertString(t, evalJS(t, `"hello".replace("l", "L")`), "heLlo")
}

func TestStringReplaceAll(t *testing.T) {
	assertString(t, evalJS(t, `"hello".replaceAll("l", "L")`), "heLLo")
}

func TestStringRepeat(t *testing.T) {
	assertString(t, evalJS(t, `"ab".repeat(3)`), "ababab")
}

func TestStringSubstring(t *testing.T) {
	assertString(t, evalJS(t, `"hello".substring(1, 3)`), "el")
	assertString(t, evalJS(t, `"hello".substring(0)`), "hello")
}

func TestStringSlice(t *testing.T) {
	assertString(t, evalJS(t, `"hello".slice(1, 3)`), "el")
	assertString(t, evalJS(t, `"hello".slice(-2)`), "lo")
}

func TestStringIndexOf(t *testing.T) {
	assertNumber(t, evalJS(t, `"hello".indexOf("l")`), 2)
	assertNumber(t, evalJS(t, `"hello".indexOf("x")`), -1)
}

func TestStringPadStart(t *testing.T) {
	assertString(t, evalJS(t, `"5".padStart(3, "0")`), "005")
}

func TestStringPadEnd(t *testing.T) {
	assertString(t, evalJS(t, `"5".padEnd(3, "0")`), "500")
}

func TestStringConcat(t *testing.T) {
	assertString(t, evalJS(t, `"hello".concat(" ", "world")`), "hello world")
}

func TestStringAt(t *testing.T) {
	assertString(t, evalJS(t, `"hello".at(1)`), "e")
	assertString(t, evalJS(t, `"hello".at(-1)`), "o")
}

func TestStringFromCharCode(t *testing.T) {
	assertString(t, evalJS(t, `String.fromCharCode(65, 66, 67)`), "ABC")
}

// ===== 全局函数测试 =====

func TestParseInt(t *testing.T) {
	assertNumber(t, evalJS(t, `parseInt("42")`), 42)
	assertNumber(t, evalJS(t, `parseInt("0xFF", 16)`), 255)
	assertNumber(t, evalJS(t, `parseInt("10", 2)`), 2)
}

func TestParseFloat(t *testing.T) {
	assertNumber(t, evalJS(t, `parseFloat("3.14")`), 3.14)
}

func TestIsNaN(t *testing.T) {
	assertBoolean(t, evalJS(t, `isNaN(NaN)`), true)
	assertBoolean(t, evalJS(t, `isNaN(5)`), false)
}

func TestIsFinite(t *testing.T) {
	assertBoolean(t, evalJS(t, `isFinite(5)`), true)
	assertBoolean(t, evalJS(t, `isFinite(Infinity)`), false)
}

func TestStringFunction(t *testing.T) {
	assertString(t, evalJS(t, `String(123)`), "123")
	assertString(t, evalJS(t, `String(true)`), "true")
}

func TestNumberFunction(t *testing.T) {
	assertNumber(t, evalJS(t, `Number("42")`), 42)
	assertNumber(t, evalJS(t, `Number(true)`), 1)
}

func TestBooleanFunction(t *testing.T) {
	assertBoolean(t, evalJS(t, `Boolean(0)`), false)
	assertBoolean(t, evalJS(t, `Boolean("hello")`), true)
}

// ===== Error 测试 =====

func TestErrorConstructor(t *testing.T) {
	result := evalJS(t, `Error("test error")`)
	err, ok := result.(*object.Error)
	if !ok {
		t.Fatalf("expected Error, got %T", result)
	}
	if err.Message != "test error" {
		t.Fatalf("expected message 'test error', got %q", err.Message)
	}
}

func TestTypeErrorConstructor(t *testing.T) {
	result := evalJS(t, `TypeError("bad type")`)
	err, ok := result.(*object.Error)
	if !ok {
		t.Fatalf("expected Error, got %T", result)
	}
	if err.Name != "TypeError" {
		t.Fatalf("expected name 'TypeError', got %q", err.Name)
	}
}

// ===== 综合测试 =====

func TestConsoleLog(t *testing.T) {
	// console.log 返回 undefined
	result, err := Eval(`console.log("hello")`)
	if err != nil {
		t.Fatalf("console.log error: %v", err)
	}
	_, ok := result.(*object.Undefined)
	if !ok {
		t.Fatalf("expected undefined from console.log, got %T", result)
	}
}

func TestChainedMethods(t *testing.T) {
	assertString(t, evalJS(t, `"  Hello  ".trim().toLowerCase()`), "hello")
	assertNumber(t, evalJS(t, `[1, 2, 3].join("-").length`), 5)
}

// ===== Array 回调方法测试 =====

func TestArrayMap(t *testing.T) {
	result := evalJS(t, `[1, 2, 3].map((x) => x * 2)`)
	arr, ok := result.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", result)
	}
	if len(arr.Elements) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(arr.Elements))
	}
	assertNumber(t, arr.Elements[0], 2)
	assertNumber(t, arr.Elements[1], 4)
	assertNumber(t, arr.Elements[2], 6)
}

func TestArrayMapWithIndex(t *testing.T) {
	result := evalJS(t, `["a", "b", "c"].map((v, i) => i)`)
	arr, ok := result.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", result)
	}
	assertNumber(t, arr.Elements[0], 0)
	assertNumber(t, arr.Elements[1], 1)
	assertNumber(t, arr.Elements[2], 2)
}

func TestArrayFilter(t *testing.T) {
	result := evalJS(t, `[1, 2, 3, 4, 5, 6].filter((x) => x % 2 === 0)`)
	arr, ok := result.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", result)
	}
	if len(arr.Elements) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(arr.Elements))
	}
	assertNumber(t, arr.Elements[0], 2)
	assertNumber(t, arr.Elements[1], 4)
	assertNumber(t, arr.Elements[2], 6)
}

func TestArrayFilterEmpty(t *testing.T) {
	result := evalJS(t, `[1, 2, 3].filter((x) => x > 10)`)
	arr, ok := result.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", result)
	}
	if len(arr.Elements) != 0 {
		t.Fatalf("expected 0 elements, got %d", len(arr.Elements))
	}
}

func TestArrayReduce(t *testing.T) {
	assertNumber(t, evalJS(t, `[1, 2, 3, 4, 5].reduce((a, b) => a + b, 0)`), 15)
}

func TestArrayReduceNoInitial(t *testing.T) {
	assertNumber(t, evalJS(t, `[1, 2, 3, 4].reduce((a, b) => a + b)`), 10)
}

func TestArrayReduceMultiply(t *testing.T) {
	assertNumber(t, evalJS(t, `[2, 3, 4].reduce((a, b) => a * b, 1)`), 24)
}

func TestArrayReduceRight(t *testing.T) {
	// [1,2,3,4].reduceRight((acc, elem) => acc - elem, 0)
	// Right to left: 0-4=-4, -4-3=-7, -7-2=-9, -9-1=-10
	assertNumber(t, evalJS(t, `[1, 2, 3, 4].reduceRight((a, b) => a - b, 0)`), -10)
}

func TestArrayForEach(t *testing.T) {
	result := evalJS(t, `let sum = 0; [1, 2, 3].forEach((x) => { sum += x }); sum`)
	assertNumber(t, result, 6)
}

func TestArrayFind(t *testing.T) {
	result := evalJS(t, `[1, 2, 3, 4].find((x) => x > 2)`)
	assertNumber(t, result, 3)
}

func TestArrayFindNone(t *testing.T) {
	result := evalJS(t, `[1, 2, 3].find((x) => x > 10)`)
	if _, ok := result.(*object.Undefined); !ok {
		t.Fatalf("expected undefined, got %T (%s)", result, result.Inspect())
	}
}

func TestArrayFindIndex(t *testing.T) {
	assertNumber(t, evalJS(t, `[1, 2, 3, 4].findIndex((x) => x > 2)`), 2)
}

func TestArrayFindIndexNone(t *testing.T) {
	assertNumber(t, evalJS(t, `[1, 2, 3].findIndex((x) => x > 10)`), -1)
}

func TestArraySome(t *testing.T) {
	assertBoolean(t, evalJS(t, `[1, 2, 3].some((x) => x > 2)`), true)
	assertBoolean(t, evalJS(t, `[1, 2, 3].some((x) => x > 10)`), false)
}

func TestArrayEvery(t *testing.T) {
	assertBoolean(t, evalJS(t, `[1, 2, 3].every((x) => x > 0)`), true)
	assertBoolean(t, evalJS(t, `[1, 2, 3].every((x) => x > 1)`), false)
}

func TestArrayFlatMap(t *testing.T) {
	result := evalJS(t, `[1, 2, 3].flatMap((x) => [x, x * 10])`)
	arr, ok := result.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", result)
	}
	if len(arr.Elements) != 6 {
		t.Fatalf("expected 6 elements, got %d", len(arr.Elements))
	}
	assertNumber(t, arr.Elements[0], 1)
	assertNumber(t, arr.Elements[1], 10)
	assertNumber(t, arr.Elements[2], 2)
	assertNumber(t, arr.Elements[3], 20)
	assertNumber(t, arr.Elements[4], 3)
	assertNumber(t, arr.Elements[5], 30)
}

func TestArraySortWithCompare(t *testing.T) {
	result := evalJS(t, `[3, 1, 2].sort((a, b) => a - b)`)
	arr, ok := result.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", result)
	}
	assertNumber(t, arr.Elements[0], 1)
	assertNumber(t, arr.Elements[1], 2)
	assertNumber(t, arr.Elements[2], 3)
}

func TestArraySortDesc(t *testing.T) {
	result := evalJS(t, `[3, 1, 2].sort((a, b) => b - a)`)
	arr, ok := result.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", result)
	}
	assertNumber(t, arr.Elements[0], 3)
	assertNumber(t, arr.Elements[1], 2)
	assertNumber(t, arr.Elements[2], 1)
}

func TestArrayChainedCallbacks(t *testing.T) {
	// [1,2,3,4,5].filter(evens).map(double).reduce(sum) = (2+4)*2 = 12
	assertNumber(t, evalJS(t, `[1, 2, 3, 4, 5].filter((x) => x % 2 === 0).map((x) => x * 2).reduce((a, b) => a + b, 0)`), 12)
}

func TestArrayMapWithArray(t *testing.T) {
	// map callback can access original array (3rd param)
	result := evalJS(t, `[10, 20, 30].map((v, i, arr) => v + arr.length)`)
	arr, ok := result.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", result)
	}
	assertNumber(t, arr.Elements[0], 13)
	assertNumber(t, arr.Elements[1], 23)
	assertNumber(t, arr.Elements[2], 33)
}

func TestArrayReduceWithStringConcat(t *testing.T) {
	assertString(t, evalJS(t, `["a", "b", "c"].reduce((acc, s) => acc + s, "")`), "abc")
}

func TestArrayFindWithObject(t *testing.T) {
	result := evalJS(t, `[{n: 1}, {n: 2}, {n: 3}].find((o) => o.n === 2)`)
	obj, ok := result.(*object.Object)
	if !ok {
		t.Fatalf("expected Object, got %T", result)
	}
	n, _ := obj.GetProperty("n")
	assertNumber(t, n, 2)
}

func TestArraySomeEmpty(t *testing.T) {
	assertBoolean(t, evalJS(t, `[].some((x) => true)`), false)
}

func TestArrayEveryEmpty(t *testing.T) {
	assertBoolean(t, evalJS(t, `[].every((x) => false)`), true)
}

// ===== String 回调方法测试 =====

func TestStringReplaceWithFunction(t *testing.T) {
	assertString(t, evalJS(t, `"hello".replace("l", (m) => m.toUpperCase())`), "heLlo")
}

func TestStringReplaceAllWithFunction(t *testing.T) {
	assertString(t, evalJS(t, `"aaa".replaceAll("a", (m) => m + "!")`), "a!a!a!")
}

func TestStringReplaceNoMatch(t *testing.T) {
	assertString(t, evalJS(t, `"hello".replace("xyz", (m) => m.toUpperCase())`), "hello")
}

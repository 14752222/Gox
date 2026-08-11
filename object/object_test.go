package object

import (
	"math"
	"testing"
)

func TestNumber(t *testing.T) {
	n := NewNumber(42.0)
	if n.Type() != NUMBER_OBJ {
		t.Fatalf("expected NUMBER_OBJ, got %s", n.Type())
	}
	if n.Inspect() != "42" {
		t.Fatalf("expected '42', got %q", n.Inspect())
	}
	if !n.IsTruthy() {
		t.Fatal("42 should be truthy")
	}

	zero := NewNumber(0)
	if zero.IsTruthy() {
		t.Fatal("0 should be falsy")
	}

	nan := NewNumber(math.NaN())
	if nan.IsTruthy() {
		t.Fatal("NaN should be falsy")
	}
	if nan.Inspect() != "NaN" {
		t.Fatalf("expected 'NaN', got %q", nan.Inspect())
	}

	neg := NewNumber(-3.14)
	if neg.Inspect() != "-3.14" {
		t.Fatalf("expected '-3.14', got %q", neg.Inspect())
	}
}

func TestString(t *testing.T) {
	s := NewString("hello")
	if s.Type() != STRING_OBJ {
		t.Fatalf("expected STRING_OBJ, got %s", s.Type())
	}
	if s.Inspect() != "hello" {
		t.Fatalf("expected 'hello', got %q", s.Inspect())
	}
	if !s.IsTruthy() {
		t.Fatal("'hello' should be truthy")
	}

	empty := NewString("")
	if empty.IsTruthy() {
		t.Fatal("'' should be falsy")
	}

	// length property
	length, ok := s.GetProperty("length")
	if !ok {
		t.Fatal("expected length property")
	}
	if num, ok := length.(*Number); !ok || num.Value != 5 {
		t.Fatalf("expected length=5, got %v", length)
	}
}

func TestBoolean(t *testing.T) {
	tt := NewBoolean(true)
	if tt.Type() != BOOLEAN_OBJ {
		t.Fatalf("expected BOOLEAN_OBJ, got %s", tt.Type())
	}
	if tt.Inspect() != "true" {
		t.Fatalf("expected 'true', got %q", tt.Inspect())
	}
	if !tt.IsTruthy() {
		t.Fatal("true should be truthy")
	}

	ff := NewBoolean(false)
	if ff.Inspect() != "false" {
		t.Fatalf("expected 'false', got %q", ff.Inspect())
	}
	if ff.IsTruthy() {
		t.Fatal("false should be falsy")
	}
}

func TestNullAndUndefined(t *testing.T) {
	n := NullSingleton
	if n.Type() != NULL_OBJ {
		t.Fatalf("expected NULL_OBJ, got %s", n.Type())
	}
	if n.IsTruthy() {
		t.Fatal("null should be falsy")
	}
	if n.Inspect() != "null" {
		t.Fatalf("expected 'null', got %q", n.Inspect())
	}

	u := UndefinedSingleton
	if u.Type() != UNDEFINED_OBJ {
		t.Fatalf("expected UNDEFINED_OBJ, got %s", u.Type())
	}
	if u.IsTruthy() {
		t.Fatal("undefined should be falsy")
	}
	if u.Inspect() != "undefined" {
		t.Fatalf("expected 'undefined', got %q", u.Inspect())
	}
}

func TestTypeOf(t *testing.T) {
	tests := []struct {
		value    Value
		expected string
	}{
		{NewNumber(1), "number"},
		{NewString("a"), "string"},
		{NewBoolean(true), "boolean"},
		{NullSingleton, "object"}, // typeof null === "object"
		{UndefinedSingleton, "undefined"},
		{NewArray([]Value{}), "object"},
		{NewObject(), "object"},
		{NewError("test"), "object"},
	}
	for _, tt := range tests {
		result := TypeOf(tt.value)
		if result != tt.expected {
			t.Fatalf("TypeOf(%s): expected %q, got %q", tt.value.Inspect(), tt.expected, result)
		}
	}
}

func TestArray(t *testing.T) {
	arr := NewArray([]Value{
		NewInt(1),
		NewInt(2),
		NewInt(3),
	})

	if arr.Type() != ARRAY_OBJ {
		t.Fatalf("expected ARRAY_OBJ, got %s", arr.Type())
	}
	if arr.Inspect() != "[1, 2, 3]" {
		t.Fatalf("expected '[1, 2, 3]', got %q", arr.Inspect())
	}
	if !arr.IsTruthy() {
		t.Fatal("array should be truthy")
	}

	// length property
	length, ok := arr.GetProperty("length")
	if !ok {
		t.Fatal("expected length property")
	}
	if num, ok := length.(*Number); !ok || num.Value != 3 {
		t.Fatalf("expected length=3, got %v", length)
	}

	// index access
	val, ok := arr.GetProperty("0")
	if !ok {
		t.Fatal("expected element at index 0")
	}
	if num, ok := val.(*Number); !ok || num.Value != 1 {
		t.Fatalf("expected arr[0]=1, got %v", val)
	}

	// index set
	arr.SetProperty("5", NewInt(99))
	length, _ = arr.GetProperty("length")
	if num, ok := length.(*Number); !ok || num.Value != 6 {
		t.Fatalf("expected length=6 after extending, got %v", length)
	}

	// string elements in array
	strArr := NewArray([]Value{NewString("a"), NewString("b")})
	if strArr.Inspect() != `["a", "b"]` {
		t.Fatalf("expected '[\"a\", \"b\"]', got %q", strArr.Inspect())
	}
}

func TestObject(t *testing.T) {
	obj := NewObject()
	obj.SetProperty("name", NewString("Alice"))
	obj.SetProperty("age", NewInt(30))

	if obj.Type() != OBJECT_OBJ {
		t.Fatalf("expected OBJECT_OBJ, got %s", obj.Type())
	}
	if !obj.IsTruthy() {
		t.Fatal("object should be truthy")
	}

	// property access
	name, ok := obj.GetProperty("name")
	if !ok {
		t.Fatal("expected 'name' property")
	}
	if s, ok := name.(*String); !ok || s.Value != "Alice" {
		t.Fatalf("expected 'Alice', got %v", name)
	}

	// property not found
	_, ok = obj.GetProperty("nonexistent")
	if ok {
		t.Fatal("expected property not found")
	}

	// keys
	keys := obj.Keys()
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys, got %d", len(keys))
	}
	if keys[0] != "age" || keys[1] != "name" {
		t.Fatalf("expected sorted keys [age, name], got %v", keys)
	}

	// HasOwnProperty
	if !obj.HasOwnProperty("name") {
		t.Fatal("expected HasOwnProperty('name') = true")
	}
	if obj.HasOwnProperty("nonexistent") {
		t.Fatal("expected HasOwnProperty('nonexistent') = false")
	}
}

func TestObjectPrototype(t *testing.T) {
	// 原型链查找
	proto := NewObject()
	proto.SetProperty("inherited", NewString("from proto"))

	child := NewObjectWithProto(proto)
	child.SetProperty("own", NewString("own value"))

	// 自有属性
	val, ok := child.GetProperty("own")
	if !ok {
		t.Fatal("expected own property")
	}
	if s, ok := val.(*String); !ok || s.Value != "own value" {
		t.Fatalf("expected 'own value', got %v", val)
	}

	// 原型链属性
	val, ok = child.GetProperty("inherited")
	if !ok {
		t.Fatal("expected inherited property from prototype")
	}
	if s, ok := val.(*String); !ok || s.Value != "from proto" {
		t.Fatalf("expected 'from proto', got %v", val)
	}
}

func TestError(t *testing.T) {
	e := NewError("something went wrong")
	if e.Type() != ERROR_OBJ {
		t.Fatalf("expected ERROR_OBJ, got %s", e.Type())
	}
	if !e.IsTruthy() {
		t.Fatal("error should be truthy")
	}
	if e.Inspect() != "Error: something went wrong" {
		t.Fatalf("expected 'Error: something went wrong', got %q", e.Inspect())
	}

	// TypeError
	te := NewTypeError("cannot read %s of %s", "property", "undefined")
	if te.Name != "TypeError" {
		t.Fatalf("expected name 'TypeError', got %q", te.Name)
	}

	// Error properties
	msg, ok := e.GetProperty("message")
	if !ok {
		t.Fatal("expected message property")
	}
	if s, ok := msg.(*String); !ok || s.Value != "something went wrong" {
		t.Fatalf("expected message='something went wrong', got %v", msg)
	}
}

func TestBuiltinFunction(t *testing.T) {
	add := NewBuiltin("add", func(args ...Value) Value {
		a := args[0].(*Number).Value
		b := args[1].(*Number).Value
		return NewNumber(a + b)
	})

	if add.Type() != BUILTIN_OBJ {
		t.Fatalf("expected BUILTIN_OBJ, got %s", add.Type())
	}
	if !add.IsTruthy() {
		t.Fatal("function should be truthy")
	}
	if add.Inspect() != "[Function: add]" {
		t.Fatalf("expected '[Function: add]', got %q", add.Inspect())
	}

	result := add.Fn(NewNumber(2), NewNumber(3))
	if num, ok := result.(*Number); !ok || num.Value != 5 {
		t.Fatalf("expected 5, got %v", result)
	}

	// name property
	name, ok := add.GetProperty("name")
	if !ok {
		t.Fatal("expected name property")
	}
	if s, ok := name.(*String); !ok || s.Value != "add" {
		t.Fatalf("expected name='add', got %v", name)
	}
}

func TestIsFalsy(t *testing.T) {
	tests := []struct {
		value   Value
		expected bool
	}{
		{NewNumber(0), true},
		{NewNumber(1), false},
		{NewNumber(math.NaN()), true}, // NaN
		{NewString(""), true},
		{NewString("a"), false},
		{NewBoolean(false), true},
		{NewBoolean(true), false},
		{NullSingleton, true},
		{UndefinedSingleton, true},
		{NewArray([]Value{}), false},
		{NewObject(), false},
	}
	for _, tt := range tests {
		result := IsFalsy(tt.value)
		if result != tt.expected {
			t.Fatalf("IsFalsy(%s): expected %v, got %v", tt.value.Inspect(), tt.expected, result)
		}
	}
}

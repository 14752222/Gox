package runtime

import (
	"testing"

	"github.com/14752222/Gox/object"
)

func TestEnvironmentBasic(t *testing.T) {
	env := NewEnvironment()

	// Declare and Get
	env.Declare("x", object.NewInt(42), false)
	val, ok := env.Get("x")
	if !ok {
		t.Fatal("expected to find 'x'")
	}
	if num, ok := val.(*object.Number); !ok || num.Value != 42 {
		t.Fatalf("expected x=42, got %v", val)
	}
}

func TestEnvironmentScopeChain(t *testing.T) {
	global := NewEnvironment()
	global.Declare("g", object.NewString("global"), false)

	// Block scope
	block := NewEnclosedEnvironment(global)
	block.Declare("b", object.NewInt(10), false)

	// Can access global from block
	val, ok := block.Get("g")
	if !ok {
		t.Fatal("expected to find 'g' in outer scope")
	}
	if s, ok := val.(*object.String); !ok || s.Value != "global" {
		t.Fatalf("expected g='global', got %v", val)
	}

	// Can access local
	val, ok = block.Get("b")
	if !ok {
		t.Fatal("expected to find 'b' in local scope")
	}
	if num, ok := val.(*object.Number); !ok || num.Value != 10 {
		t.Fatalf("expected b=10, got %v", val)
	}

	// Global cannot access block's variable
	_, ok = global.Get("b")
	if ok {
		t.Fatal("global should not find block's variable 'b'")
	}
}

func TestEnvironmentVariableShadowing(t *testing.T) {
	global := NewEnvironment()
	global.Declare("x", object.NewInt(1), false)

	block := NewEnclosedEnvironment(global)
	block.Declare("x", object.NewInt(2), false) // shadows outer x

	// Block sees its own x
	val, _ := block.Get("x")
	if num, ok := val.(*object.Number); !ok || num.Value != 2 {
		t.Fatalf("expected block x=2, got %v", val)
	}

	// Global still has x=1
	val, _ = global.Get("x")
	if num, ok := val.(*object.Number); !ok || num.Value != 1 {
		t.Fatalf("expected global x=1, got %v", val)
	}
}

func TestEnvironmentSet(t *testing.T) {
	global := NewEnvironment()
	global.Declare("x", object.NewInt(1), false)

	block := NewEnclosedEnvironment(global)

	// Modify outer variable from inner scope
	err := block.Set("x", object.NewInt(99))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	val, _ := global.Get("x")
	if num, ok := val.(*object.Number); !ok || num.Value != 99 {
		t.Fatalf("expected x=99 after set, got %v", val)
	}
}

func TestEnvironmentConst(t *testing.T) {
	env := NewEnvironment()
	env.Declare("PI", object.NewNumber(3.14), true) // isConst = true

	// Can read
	val, ok := env.Get("PI")
	if !ok {
		t.Fatal("expected to find 'PI'")
	}
	if num, ok := val.(*object.Number); !ok || num.Value != 3.14 {
		t.Fatalf("expected PI=3.14, got %v", val)
	}

	// Cannot modify
	err := env.Set("PI", object.NewNumber(3.0))
	if err == nil {
		t.Fatal("expected error when assigning to const")
	}

	// IsConst check
	if !env.IsConst("PI") {
		t.Fatal("expected PI to be const")
	}
}

func TestEnvironmentConstInBlock(t *testing.T) {
	global := NewEnvironment()
	global.Declare("g", object.NewInt(1), false)

	block := NewEnclosedEnvironment(global)
	block.Declare("C", object.NewInt(100), true) // const in block

	// Can read const from block
	val, _ := block.Get("C")
	if num, ok := val.(*object.Number); !ok || num.Value != 100 {
		t.Fatalf("expected C=100, got %v", val)
	}

	// Cannot modify const from inner scope
	inner := NewEnclosedEnvironment(block)
	err := inner.Set("C", object.NewInt(200))
	if err == nil {
		t.Fatal("expected error when assigning to const from inner scope")
	}
}

func TestEnvironmentUndeclaredSet(t *testing.T) {
	env := NewEnvironment()
	err := env.Set("undeclared", object.NewInt(1))
	if err == nil {
		t.Fatal("expected error when setting undeclared variable")
	}
}

func TestEnvironmentHasLocal(t *testing.T) {
	global := NewEnvironment()
	global.Declare("g", object.NewInt(1), false)

	block := NewEnclosedEnvironment(global)
	block.Declare("b", object.NewInt(2), false)

	if !block.HasLocal("b") {
		t.Fatal("expected HasLocal('b') = true in block")
	}
	if block.HasLocal("g") {
		t.Fatal("expected HasLocal('g') = false in block (it's in outer)")
	}
	if !global.HasLocal("g") {
		t.Fatal("expected HasLocal('g') = true in global")
	}
}

func TestIteratorArray(t *testing.T) {
	arr := object.NewArray([]object.Value{
		object.NewInt(10),
		object.NewInt(20),
		object.NewInt(30),
	})

	iter, ok := GetIterable(arr)
	if !ok {
		t.Fatal("expected array to be iterable")
	}

	// Iterate and collect
	var results []float64
	for {
		val, done := iter.Next()
		if done {
			break
		}
		if num, ok := val.(*object.Number); ok {
			results = append(results, num.Value)
		}
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 iterations, got %d", len(results))
	}
	if results[0] != 10 || results[1] != 20 || results[2] != 30 {
		t.Fatalf("expected [10, 20, 30], got %v", results)
	}
}

func TestIteratorString(t *testing.T) {
	s := object.NewString("abc")

	iter, ok := GetIterable(s)
	if !ok {
		t.Fatal("expected string to be iterable")
	}

	var results []string
	for {
		val, done := iter.Next()
		if done {
			break
		}
		if str, ok := val.(*object.String); ok {
			results = append(results, str.Value)
		}
	}

	if len(results) != 3 {
		t.Fatalf("expected 3 iterations, got %d", len(results))
	}
	if results[0] != "a" || results[1] != "b" || results[2] != "c" {
		t.Fatalf("expected [a, b, c], got %v", results)
	}
}

func TestIteratorNonIterable(t *testing.T) {
	// Numbers are not iterable
	_, ok := GetIterable(object.NewInt(42))
	if ok {
		t.Fatal("number should not be iterable")
	}

	// Objects are not iterable by default
	_, ok = GetIterable(object.NewObject())
	if ok {
		t.Fatal("plain object should not be iterable")
	}
}

func TestIteratorEmpty(t *testing.T) {
	arr := object.NewArray([]object.Value{})

	iter, ok := GetIterable(arr)
	if !ok {
		t.Fatal("expected empty array to be iterable")
	}

	_, done := iter.Next()
	if !done {
		t.Fatal("expected empty array to be done immediately")
	}
}

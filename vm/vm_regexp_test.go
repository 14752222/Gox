package vm

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== RegExp 构造器测试 =====

func TestRegExpConstructor(t *testing.T) {
	// 构造器形式
	r := evalJS(t, `RegExp("a+b", "g")`)
	re, ok := r.(*object.RegExp)
	if !ok {
		t.Fatalf("expected RegExp, got %T", r)
	}
	if re.Pattern != "a+b" {
		t.Fatalf("expected pattern a+b, got %q", re.Pattern)
	}
	if !re.Global {
		t.Fatalf("expected global flag")
	}

	// new 形式
	r2 := evalJS(t, `new RegExp("\\d+")`)
	re2, ok := r2.(*object.RegExp)
	if !ok {
		t.Fatalf("expected RegExp, got %T", r2)
	}
	if re2.Pattern != `\d+` {
		t.Fatalf("expected pattern \\d+, got %q", re2.Pattern)
	}

	// 无效正则抛出 SyntaxError
	_, err := Eval(`new RegExp("[")`)
	if err == nil {
		t.Fatalf("expected error for invalid regex")
	}
}

// ===== 正则字面量测试 =====

func TestRegExpLiteral(t *testing.T) {
	// 字面量形式
	r := evalJS(t, `/abc/`)
	re, ok := r.(*object.RegExp)
	if !ok {
		t.Fatalf("expected RegExp, got %T", r)
	}
	if re.Pattern != "abc" {
		t.Fatalf("expected pattern abc, got %q", re.Pattern)
	}

	// 带标志
	r2 := evalJS(t, `/abc/gi`)
	re2, ok := r2.(*object.RegExp)
	if !ok {
		t.Fatalf("expected RegExp, got %T", r2)
	}
	if !re2.Global || !re2.IgnoreCase {
		t.Fatalf("expected global+ignoreCase flags")
	}

	// 字符类
	evalJS(t, `/a[0-9]+b/`)

	// 转义
	evalJS(t, `/\/escaped\//`)

	// 除法仍然是除法 (上下文检测)
	assertNumber(t, evalJS(t, `8 / 2 / 2`), 2)
	assertNumber(t, evalJS(t, `let a = 6; a / 2`), 3)
	assertNumber(t, evalJS(t, `(8) / 2`), 4)
}

func TestRegExpToString(t *testing.T) {
	assertString(t, evalJS(t, `/ab+c/gi.toString()`), "/ab+c/gi")
}

// ===== exec / test 方法测试 =====

func TestRegExpExec(t *testing.T) {
	// 基本匹配
	arr := evalJS(t, `/(\d+)-(\d+)/.exec("call 123-456 now")`)
	parr, ok := arr.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", arr)
	}
	if len(parr.Elements) != 3 {
		t.Fatalf("expected 3 elements (match + 2 groups), got %d", len(parr.Elements))
	}
	assertString(t, parr.Elements[0], "123-456")
	assertString(t, parr.Elements[1], "123")
	assertString(t, parr.Elements[2], "456")

	// index / input 属性
	idx, ok := parr.GetProperty("index")
	if !ok {
		t.Fatalf("missing index property")
	}
	idxNum, _ := idx.(*object.Number)
	if idxNum.Value != 5 {
		t.Fatalf("expected index 5, got %v", idxNum.Value)
	}

	// 未匹配返回 null
	r := evalJS(t, `/xyz/.exec("hello")`)
	if r != object.NullSingleton {
		t.Fatalf("expected null, got %T", r)
	}
}

func TestRegExpTest(t *testing.T) {
	assertBoolean(t, evalJS(t, `/hello/.test("say hello")`), true)
	assertBoolean(t, evalJS(t, `/hello/.test("goodbye")`), false)
	assertBoolean(t, evalJS(t, `/HELLO/i.test("say Hello")`), true)
}

func TestRegExpGlobalLastIndex(t *testing.T) {
	// 全局正则 exec 使用 lastIndex
	src := `
		let re = /a/g;
		let results = [];
		let m;
		while ((m = re.exec("banana")) !== null) {
			results.push(m[0] + "@" + m.index);
		}
		results.join(",");
	`
	assertString(t, evalJS(t, src), "a@1,a@3,a@5")
}

// ===== String 方法与 RegExp 集成测试 =====

func TestStringMatchRegex(t *testing.T) {
	// 非全局 match: 返回完整匹配信息
	arr := evalJS(t, `"phone 123-456".match(/(\d+)-(\d+)/)`)
	parr, ok := arr.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", arr)
	}
	assertString(t, parr.Elements[0], "123-456")

	// 全局 match: 返回所有匹配
	arr2 := evalJS(t, `"a1b2c3".match(/\d/g)`)
	parr2, ok := arr2.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", arr2)
	}
	if len(parr2.Elements) != 3 {
		t.Fatalf("expected 3 matches, got %d", len(parr2.Elements))
	}
	assertString(t, parr2.Elements[0], "1")
	assertString(t, parr2.Elements[2], "3")

	// 无匹配: 非全局返回 null
	r := evalJS(t, `"abc".match(/\d/)`)
	if r != object.NullSingleton {
		t.Fatalf("expected null, got %T", r)
	}
}

func TestStringReplaceRegex(t *testing.T) {
	// 全局替换
	assertString(t, evalJS(t, `"a1b2c3".replace(/\d/g, "X")`), "aXbXcX")

	// 非全局替换 (只替换第一个)
	assertString(t, evalJS(t, `"a1b2c3".replace(/\d/, "X")`), "aXb2c3")

	// 捕获组 $1
	assertString(t, evalJS(t, `"John Smith".replace(/(\w+)\s+(\w+)/, "$2 $1")`), "Smith John")

	// 忽略大小写
	assertString(t, evalJS(t, `"Hello World".replace(/hello/i, "Hi")`), "Hi World")
}

func TestStringSplitRegex(t *testing.T) {
	arr := evalJS(t, `"a,b;c".split(/[,;]/)`)
	parr, ok := arr.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", arr)
	}
	if len(parr.Elements) != 3 {
		t.Fatalf("expected 3 parts, got %d", len(parr.Elements))
	}
	assertString(t, parr.Elements[0], "a")
	assertString(t, parr.Elements[1], "b")
	assertString(t, parr.Elements[2], "c")
}

func TestStringSearchRegex(t *testing.T) {
	assertNumber(t, evalJS(t, `"hello world".search(/world/)`), 6)
	assertNumber(t, evalJS(t, `"hello world".search(/xyz/)`), -1)
}

// ===== 综合场景测试 =====

func TestRegExpComprehensive(t *testing.T) {
	src := `
		// 验证邮箱
		let emailRe = /^[\w.+-]+@[\w-]+\.[\w.]+$/;
		let emails = ["a@b.com", "user.name+tag@sub.domain.org", "not-an-email"];
		let valid = emails.filter(e => emailRe.test(e));
		valid.length;
	`
	assertNumber(t, evalJS(t, src), 2)

	// 提取所有数字
	src2 := `
		let nums = "2024-08-08, temp=36.5C".match(/\d+\.?\d*/g);
		nums.join("|");
	`
	assertString(t, evalJS(t, src2), "2024|08|08|36.5")

	// 非贪婪匹配 + 多行
	src3 := `
		let text = "line1\nline2";
		/^line2/m.test(text);
	`
	assertBoolean(t, evalJS(t, src3), true)
}

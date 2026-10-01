package compiler

import (
	"strings"
	"testing"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/lexer"
	"github.com/14752222/Gox/parser"
)

// ===== 命名导入的别名 (`import { x as y }`) =====
//
// 别名曾经整体失效: parser 的命名导入循环把 `as` 当成一个普通标识符收进
// []string, 于是 `{ x as y }` 变成三个"名字" [x, as, y] —— 编译出来是
// **导出 x / 导出 as / 导出 y 三个绑定**, 而真正的本地名 y 恒为 undefined。
//
//	import { x as y } from "./m.js";     // y 静默 undefined (文件模块)
//	import { f as g } from "gx/solid";   // 误报 `没有导出 "as"` (内置模块)
//
// 现在 AST 用 NamedImport{Imported, Local} 区分"模块里叫什么"与"本文件绑成
// 什么"; 下面钉住解析、绑定、内置校验三条链路。

// parseImport 只解析一条 import, 返回首个 ImportDeclaration。
func parseImport(t *testing.T, src string) *ast.ImportDeclaration {
	t.Helper()
	p := parser.New(lexer.New(src))
	program := p.ParseProgram()
	if p.Errors().HasErrors() {
		t.Fatalf("解析 %s 失败: %s", src, p.Errors().String())
	}
	for _, s := range program.Statements {
		if imp, ok := s.(*ast.ImportDeclaration); ok {
			return imp
		}
	}
	t.Fatalf("%s 里没有解析出 import 语句", src)
	return nil
}

// TestNamedImportAliasParsed 别名要落在 Local 上, 而不是变成额外的名字。
func TestNamedImportAliasParsed(t *testing.T) {
	cases := []struct {
		src  string
		want []ast.NamedImport
	}{
		{`import { x } from "./m.js";`, []ast.NamedImport{{Imported: "x", Local: "x"}}},
		{`import { x as y } from "./m.js";`, []ast.NamedImport{{Imported: "x", Local: "y"}}},
		{
			`import { a, b as c } from "./m.js";`,
			[]ast.NamedImport{{Imported: "a", Local: "a"}, {Imported: "b", Local: "c"}},
		},
		// 同名字别名是合法写法 (某些工具链会生成), 不应报错
		{`import { a as a } from "./m.js";`, []ast.NamedImport{{Imported: "a", Local: "a"}}},
		// 尾随逗号
		{`import { a as b, } from "./m.js";`, []ast.NamedImport{{Imported: "a", Local: "b"}}},
	}
	for _, c := range cases {
		got := parseImport(t, c.src).NamedImports
		if len(got) != len(c.want) {
			t.Fatalf("%s: 得到 %d 项 (%v), 期望 %d 项", c.src, len(got), got, len(c.want))
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: 第 %d 项 = %+v, 期望 %+v", c.src, i, got[i], c.want[i])
			}
		}
	}
}

// TestNamedImportAliasNotSplitIntoExtraNames 回归防线: 别名绝不能凭空多出
// `as` 这样的绑定名 —— 这正是老 bug 的形态。
func TestNamedImportAliasNotSplitIntoExtraNames(t *testing.T) {
	imp := parseImport(t, `import { x as y } from "./m.js";`)
	if len(imp.NamedImports) != 1 {
		t.Fatalf("`{ x as y }` 必须只产生 1 项, 实际 %d 项: %v", len(imp.NamedImports), imp.NamedImports)
	}
	for _, n := range imp.NamedImports {
		if n.Local == "as" || n.Imported == "as" {
			t.Fatalf("`as` 被当成了名字: %+v", n)
		}
	}
}

// TestNamedImportAliasStringRoundTrip String() 要能还原源码 (含 as)。
func TestNamedImportAliasStringRoundTrip(t *testing.T) {
	for _, src := range []string{
		`import { a } from "./m.js";`,
		`import { a as b } from "./m.js";`,
		`import { a as b, c } from "./m.js";`,
		`import def, { a as b } from "./m.js";`,
		`import * as ns from "./m.js";`,
		`import def from "./m.js";`,
		`import "./m.js";`, // 副作用导入, 不能拼成 "import  from"
	} {
		if got := parseImport(t, src).String(); got != src {
			t.Fatalf("往返不一致:\n  源: %s\n  回: %s", src, got)
		}
	}
}

// TestNamedImportAliasBindsLocalName 编译后绑定的必须是**本地名**。
// 这里用"重复声明"当探测器: declareOnce 对同一名字二次声明会报错 ——
// 若绑定的是 Local (y), 那么紧跟一条 `let y` 应当报重复; 若绑的是 Imported
// (x), 则 `let y` 合法而 `let x` 报重复。
func TestNamedImportAliasBindsLocalName(t *testing.T) {
	// 绑到本地名 y → `let y` 冲突
	err := compileErr(t, `import { x as y } from "./m.js"; let y = 1;`)
	if err == nil {
		t.Fatal("`import { x as y }` 之后 `let y` 应报重复声明 (说明 y 被绑定了)")
	}
	// 绑到本地名 y (而非导入名 x) → `let x` 不冲突
	if err := compileErr(t, `import { x as y } from "./m.js"; let x = 1;`); err != nil {
		t.Fatalf("`import { x as y }` 之后 `let x` 不该冲突 (x 只是模块里的名字): %v", err)
	}
}

// TestBuiltinImportAliasChecksImportedName 内置模块的校验必须拿**导入名**去对
// 导出表, 而不是本地名 / `as`。
func TestBuiltinImportAliasChecksImportedName(t *testing.T) {
	// 导出名合法 + 别名合法 → 通过 (老 bug 在这里误报 `没有导出 "as"`)
	if err := compileErr(t, `import { add as plus } from "gx/unit-calc";`); err != nil {
		t.Fatalf("`{ add as plus }` 的 add 是合法导出, 不该报错: %v", err)
	}
	// 别名首段恰好是另一个合法导出名 → 仍按首段核对, 通过
	if err := compileErr(t, `import { add as helper } from "gx/unit-calc";`); err != nil {
		t.Fatalf("`{ add as helper }` 应通过 (helper 只是本地名): %v", err)
	}
}

// TestBuiltinImportAliasBadNameReportsImported 导入名不存在时, 报错要指向
// **导入名**, 并附上本地名以免用户对着 `as xxx` 困惑。
func TestBuiltinImportAliasBadNameReportsImported(t *testing.T) {
	err := compileErr(t, `import { nope as local } from "gx/unit-calc";`)
	if err == nil {
		t.Fatal("导入名不存在应当编译失败")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"nope"`) {
		t.Fatalf("报错该指向导入名 nope: %v", err)
	}
	if strings.Contains(msg, `"as"`) {
		t.Fatalf("报错不该提 `as` (老 bug 的形态): %v", err)
	}
	if !strings.Contains(msg, "本地名 local") {
		t.Fatalf("带别名时该报出本地名以便定位: %v", err)
	}
}

// TestBuiltinImportAliasedErrorName 别名首段不存在时也要拦下。
func TestBuiltinImportAliasedErrorName(t *testing.T) {
	err := compileErr(t, `import { add as ok, bogus as bad } from "gx/unit-calc";`)
	if err == nil {
		t.Fatal("列表里有一个不存在的导入名就该整体失败")
	}
	if !strings.Contains(err.Error(), `"bogus"`) {
		t.Fatalf("应指出是 bogus 不存在: %v", err)
	}
}

// TestNamedImportAliasElementDirectiveHint 元素级指令带别名时, 提示仍要命中。
func TestNamedImportAliasElementDirectiveHint(t *testing.T) {
	err := compileErr(t, `import { each as loop } from "gx/unit-calc";`)
	if err == nil {
		t.Fatal("each 不是导出, 带别名也该拦下")
	}
	if !strings.Contains(err.Error(), "元素级指令") {
		t.Fatalf("提示该说明 each 是元素级指令: %v", err)
	}
}

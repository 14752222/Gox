package compiler

import (
	"strings"
	"testing"

	"github.com/14752222/Gox/lexer"
	"github.com/14752222/Gox/parser"
)

// ===== ES2022 静态初始化块 static { ... } 的编译（看板单 rJ56bZ）=====

// TestCompileStaticBlock 静态块可编译, 且与静态字段交错时都进同一个
// __static_init__ 合成函数 (不 panic、不误判为字段)。
func TestCompileStaticBlock(t *testing.T) {
	compile(t, `class C {
		static a = 1;
		static { this.z = 7; }
		static m() { return this.z; }
	}`)
	compile(t, `class C { static { let x = 1; this.y = x; } }`)
	compile(t, `var C = class { static {} };`)
}

// TestCompileStaticBlockPrivateAndOrder 静态块内可访问私有名, 且与静态字段
// 定义顺序无关地编译成功。
func TestCompileStaticBlockPrivateAndOrder(t *testing.T) {
	compile(t, `class C {
		static #p = 1;
		static { this.q = this.#p + 1; }
	}`)
	compile(t, `class C {
		static { this.q = 1; }
		static #p = this.q;
	}`)
}

// TestCompileStaticBlockUndefinedBreakTarget 静态块里的未定义标签目标应由
// 编译期报 SyntaxError（test262 negative:parse 依赖此形态）。
func TestCompileStaticBlockUndefinedBreakTarget(t *testing.T) {
	cases := []string{
		`class C { static { x: while (false) { break y; } } }`,
		`class C { static { x: while (false) { continue y; } } }`,
	}
	for _, src := range cases {
		l := lexer.New(src)
		p := parser.New(l)
		program := p.ParseProgram()
		if p.Errors().HasErrors() {
			continue // 已在 parse 阶段拦下也算通过
		}
		c := New()
		err := c.Compile(program)
		if err == nil {
			t.Fatalf("期望编译期 SyntaxError, 实际成功: %s", src)
		}
		if !strings.Contains(err.Error(), "SyntaxError") {
			t.Fatalf("错误消息应含 SyntaxError, got: %v", err)
		}
	}
}

// bcdump 是调试工具: 编译一个 JS 源文件并反汇编主程序字节码。
// 用法: go run ./tools/bcdump <file.js>
package main

import (
	"fmt"
	"os"

	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/compiler"
	"github.com/14752222/Gox/lexer"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/parser"
)

func main() {
	src, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	l := lexer.New(string(src))
	p := parser.New(l)
	prog := p.ParseProgram()
	c := compiler.New()
	if err := c.Compile(prog); err != nil {
		fmt.Fprintln(os.Stderr, "compile:", err)
		os.Exit(1)
	}
	ins := c.Bytes()
	cp := c.Constants()
	var dump func(name string, ins bytecode.Instructions, cp *bytecode.ConstantPool)
	dump = func(name string, ins bytecode.Instructions, cp *bytecode.ConstantPool) {
		fmt.Printf("=== %s (%d bytes) ===\n", name, len(ins))
		for pc := 0; pc < len(ins); pc += bytecode.InstructionSize {
			op := bytecode.ReadOpcode(ins, pc)
			operand := bytecode.ReadOperand(ins, pc+1)
			fmt.Printf("  %04d  %-18s %d\n", pc, op.Name(), operand)
		}
		// 递归 dump 常量池里的函数体
		for i := 0; i < cp.Len(); i++ {
			if fn, ok := cp.Get(uint16(i)).(*object.CompiledFunction); ok {
				dump(fmt.Sprintf("fn#%d", i), fn.Instructions, cp)
			}
		}
	}
	dump("main", ins, cp)
}

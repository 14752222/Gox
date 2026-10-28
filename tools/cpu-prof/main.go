// cpu-prof: 临时性能剖面工具 — 跑一份 JS 脚本并输出 CPU profile。
// 用法: go run ./tools/cpu-prof -cpuprof cpu.out -runs 20 scripts/bench/fib28.js
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/pprof"

	"github.com/14752222/Gox/lexer"
	"github.com/14752222/Gox/parser"
	"github.com/14752222/Gox/vm"
)

func runFile(path string) error {
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	l := lexer.New(string(src))
	p := parser.New(l)
	_ = p.ParseProgram()
	if p.Errors().HasErrors() {
		return fmt.Errorf("parser errors: %s", p.Errors().String())
	}
	_, err = vm.EvalFileVM(path)
	return err
}

func main() {
	cpuprof := flag.String("cpuprof", "", "cpu profile out")
	memprof := flag.String("memprof", "", "mem profile out")
	runs := flag.Int("runs", 1, "times to run")
	flag.Parse()
	path := flag.Arg(0)

	if *cpuprof != "" {
		f, err := os.Create(*cpuprof)
		if err != nil {
			panic(err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			panic(err)
		}
		defer pprof.StopCPUProfile()
	}

	var _ = flag.Args
	for i := 0; i < *runs; i++ {
		if err := runFile(path); err != nil {
			panic(err)
		}
	}

	if *memprof != "" {
		f, err := os.Create(*memprof)
		if err != nil {
			panic(err)
		}
		pprof.WriteHeapProfile(f)
		f.Close()
	}
}

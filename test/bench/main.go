package main

import (
	"fmt"
	"os"
	"runtime/pprof"

	"github.com/14752222/Gox/vm"
)

func main() {
	f, _ := os.Create("cpu.prof")
	defer f.Close()
	pprof.StartCPUProfile(f)
	defer pprof.StopCPUProfile()
	for i := 0; i < 5; i++ {
		vm.Eval(`function fib(n){ if(n<2) return n; return fib(n-1)+fib(n-2) } fib(28)`)
		vm.Eval(`function add(a,b){return a+b} let t=0; for(let i=0;i<100000;i++){t=add(t,i)} t`)
		vm.Eval(`let o={}; for(let i=0;i<100000;i++){o["k"+(i%1000)]=i} Object.keys(o).length`)
		vm.Eval(`let t=0; for(let i=0;i<50000;i++){ t += Number("123.45") } t`)
	}
	fmt.Fprintln(os.Stdout, "done")
}

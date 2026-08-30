// Package main 是 js-runtime 的入口点。
// 提供交互式 REPL 和脚本执行功能。
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"js-runtime/object"
	"js-runtime/stdlib"
	"js-runtime/vm"
)

func main() {
	if len(os.Args) > 1 {
		// 执行脚本文件
		runFile(os.Args[1])
		return
	}
	// 启动 REPL
	startREPL()
}

// runFile 读取并执行 JavaScript 文件。
func runFile(path string) {
	vm, err := vm.EvalFileVM(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	result := vm.LastPopped()

	// 打印结果 (如果不是 undefined)
	if _, isUndef := result.(*object.Undefined); !isUndef && result != nil {
		fmt.Println(result.Inspect())
	}

	// 运行定时器事件循环, 让 setTimeout/setInterval 及严格定时器回调执行完毕
	if err := vm.RunTimers(); err != nil {
		fmt.Fprintf(os.Stderr, "timer error: %v\n", err)
		os.Exit(1)
	}
}

// startREPL 启动交互式 REPL。
func startREPL() {
	globals := stdlib.SetupGlobals()

	fmt.Println("js-runtime REPL (ES6 subset, no var)")
	fmt.Println("Type :exit to quit, :help for help")
	fmt.Println()

	for {
		// 读取输入
		fmt.Print("> ")
		input := readLine()
		if input == "" {
			continue
		}

		// 处理特殊命令
		switch strings.TrimSpace(input) {
		case ":exit", ":quit":
			return
		case ":help":
			printHelp()
			continue
		case ":clear":
			globals = stdlib.SetupGlobals()
			fmt.Println("Environment cleared.")
			continue
		}

		// 执行
		result, err := vm.EvalWithGlobals(input, globals)
		if err != nil {
			fmt.Println("  " + err.Error())
			continue
		}

		// 打印结果
		if result != nil {
			if _, isUndef := result.(*object.Undefined); !isUndef {
				fmt.Println("  " + result.Inspect())
			}
		}
	}
}

// printHelp 打印帮助信息。
func printHelp() {
	fmt.Print(`
Commands:
  :exit     Exit the REPL
  :help     Show this help
  :clear    Clear the environment

Features:
  - let/const declarations (no var)
  - Functions, arrow functions, closures
  - if/else, while, for, for...of, break/continue
  - Arrays, Objects, Strings with methods
  - Destructuring, spread, rest params, default params
  - Template literals
  - Math, JSON, console, Object, Array, String, Number
  - Error, TypeError, RangeError, ReferenceError, SyntaxError

Example:
  > let x = 10
  > let y = 20
  > x + y
  30
  > [1, 2, 3].map(x => x * 2)  // [2, 4, 6]
  > [1, 2, 3].filter(x => x > 1)  // [2, 3]
  > [1, 2, 3].reduce((a, b) => a + b, 0)  // 6
  > Math.sqrt(16)
  4
`)
}

// readLine 从标准输入读取一行。
func readLine() string {
	var buf strings.Builder
	buf.Grow(256)
	for {
		b := make([]byte, 1)
		n, err := os.Stdin.Read(b)
		if n == 0 || err == io.EOF {
			if buf.Len() == 0 {
				fmt.Println()
				os.Exit(0)
			}
			break
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if b[0] == '\n' {
			break
		}
		if b[0] != '\r' {
			buf.WriteByte(b[0])
		}
	}
	return buf.String()
}

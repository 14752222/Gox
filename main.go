// Package main 是 Gox 的入口点。
// 提供项目脚手架（create）、交互式 REPL 和脚本执行功能。
//
// 用法:
//
//	gox create <目录>     用默认模板铺一个 GUI 工程
//	gox <文件.js>         执行脚本（GUI 脚本会开窗口并进入消息泵）
//	gox                   启动 REPL
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/14752222/Gox/gfx"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/scaffold"
	"github.com/14752222/Gox/stdlib"
	"github.com/14752222/Gox/vm"
)

// version 是 `gox version` 报的版本号。**与 npm/package.json 的 version 手工保持一致**
// （发版时两处一起改，见 docs/npm-release.md 的同步清单）—— 仓库里没有版本注入机制，
// 刻意不引入第二个真相来源以外的复杂度。
const version = "0.4.0"

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		// 没有参数: 启动 REPL
		startREPL()
		return
	}

	// 子命令只在参数"不像脚本路径"时才认。判据是扩展名与路径分隔符 ——
	// 这样 `gox help.js` / `gox src/create.js` 仍然老老实实当脚本执行，
	// 而 `gox create my-app` 不会被误当成一个叫 create 的文件。
	arg := args[0]
	if !looksLikeScriptPath(arg) {
		switch arg {
		case "create", "new", "init":
			runCreate(args[1:])
			return
		case "help", "--help", "-h":
			printUsage(os.Stdout)
			return
		case "version", "--version":
			fmt.Printf("gox %s\n", version)
			return
		}
		if strings.HasPrefix(arg, "-") {
			// 未知选项: 当脚本路径处理只会换来一句"读不到文件"，不如直接给用法
			fmt.Fprintf(os.Stderr, "未知选项: %s\n\n", arg)
			printUsage(os.Stderr)
			os.Exit(2)
		}
	}

	// 执行脚本文件
	runFile(arg)
}

// looksLikeScriptPath 报告这个参数是否应该按"脚本路径"处理（而不是子命令）。
func looksLikeScriptPath(arg string) bool {
	if strings.HasSuffix(strings.ToLower(arg), ".js") {
		return true
	}
	return strings.ContainsAny(arg, `/\`)
}

// printUsage 打印命令行用法。
func printUsage(w io.Writer) {
	fmt.Fprint(w, `Gox —— 用 Go 实现的 JavaScript 运行时

用法:
  gox create <目录>            按默认模板生成一个 GUI 工程（脚手架）
  gox <文件.js>                执行脚本文件（GUI 脚本会开窗口）
  gox                          启动交互式 REPL
  gox help                     显示这份帮助
  gox version                  显示版本号

create 选项:
  --name <名字>                指定项目名（缺省取目录名）
  -f, --force                  目标目录已存在且非空时覆盖写入

示例:
  gox create my-app
  cd my-app && npm install && npm run dev

生成的项目是普通 Gox 工程: src/main.js 是入口, 直接用
`+"`gox src/main.js`"+` 也能跑（npm 只是用来拿 goxjs 命令）。
详见 docs/gui-guide.md。
`)
}

// runCreate 实现 `gox create <目录>`。
func runCreate(args []string) {
	var (
		dir   string
		name  string
		force bool
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			fmt.Fprint(os.Stdout, `用法: gox create <目录> [--name <名字>] [-f|--force]

按默认模板生成一个 GUI 工程（目录布局与脚手架默认输出一致）:
  package.json        元信息 + dev/start 脚本
  src/main.js         入口: 建窗口 + 挂根组件
  src/app.js          根组件
  src/store.js        共享状态（signal）
  src/theme.js        设计令牌
  src/components/*.js 组件（计数器 / 列表 / 多状态）

选项:
  --name <名字>   指定项目名（缺省取目录名；不合法的字符会被收敛成短横线）
  -f, --force     目标目录已存在且非空时覆盖写入
`)
			return
		case a == "--name":
			if i+1 >= len(args) {
				createFatal("--name 需要一个值")
			}
			i++
			name = args[i]
		case a == "-f" || a == "--force":
			force = true
		case strings.HasPrefix(a, "-"):
			createFatal("未知选项: " + a)
		default:
			if dir != "" {
				createFatal("只能指定一个目标目录，多出来的: " + a)
			}
			dir = a
		}
	}

	if dir == "" {
		fmt.Fprint(os.Stderr, "用法: gox create <目录> [--name <名字>] [-f|--force]\n\n")
		fmt.Fprint(os.Stderr, "示例: gox create my-app\n")
		os.Exit(2)
	}

	files, err := scaffold.Create(scaffold.Options{Dir: dir, Name: name, Force: force})
	if err != nil {
		createFatal(err.Error())
	}

	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = dir
	}
	fmt.Printf("已生成项目: %s\n\n", abs)
	for _, f := range files {
		fmt.Printf("  %s\n", f.Path)
	}
	fmt.Printf("\n下一步:\n  cd %s\n  npm install\n  npm run dev\n", dir)
	fmt.Printf("\n不用 npm 也行: gox %s\n", filepath.ToSlash(filepath.Join(dir, "src", "main.js")))
	fmt.Printf("完整 API 见 docs/gui-guide.md\n")
}

// createFatal 打印脚手架错误并退出。
func createFatal(msg string) {
	fmt.Fprintf(os.Stderr, "gox create: %s\n", msg)
	os.Exit(1)
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
	//
	// GUI 模式下不打印 (P3-6): render() 现在返回**窗口句柄对象**, 于是所有
	// 以 `render(...)` 收尾的演示脚本都会往终端回显一行
	// `{ close: [Function: close], isClosed: [Function: isClosed] }` ——
	// 纯噪音, 而且窗口已经开出来了, 这行也不提供任何信息。
	// 判据放在"有没有窗口"而不是"值是不是 Window": 前者是脚本的真实语义
	// (GUI 脚本的产出是窗口), 后者会让宿主依赖 gfx 的内部类型。
	if _, isUndef := result.(*object.Undefined); !isUndef && result != nil && !gfx.Active() {
		fmt.Println(result.Inspect())
	}

	// 运行事件循环: GUI 模式用消息泵接入 (全部窗口关闭才退出), 普通模式跑定时器
	var loopErr error
	if gfx.Active() {
		loopErr = vm.RunTimersWithPump(gfx.Pump)
	} else {
		loopErr = vm.RunTimers()
	}
	if loopErr != nil {
		fmt.Fprintf(os.Stderr, "timer error: %v\n", loopErr)
		os.Exit(1)
	}
}

// startREPL 启动交互式 REPL。
func startREPL() {
	globals := stdlib.SetupGlobals()

	fmt.Println("Gox REPL (ES6 subset, no var)")
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
			// 去掉内部错误前缀, 对齐浏览器的报错呈现
			msg := strings.TrimPrefix(err.Error(), "vm error: ")
			fmt.Println("  " + msg)
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

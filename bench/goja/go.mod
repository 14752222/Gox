// bench/goja 是**独立 Go module**, 存在的唯一理由是:
// 隔离 goja 依赖 (regexp2 / sourcemap / pprof / x/text ...), 让主模块
// (github.com/14752222/Gox) 的 go.mod 依赖面**一点不被污染**。
//
// 主模块的 `go build ./...` / `go test ./...` 不会进入嵌套 module,
// 所以这里的依赖只在显式 `cd bench/goja && go build` 时才需要。
//
// module 名故意取成 goxbench/goja 而不是子路径: 它不是主模块的一部分,
// 也不打算被 import。
module goxbench/goja

go 1.26.2

require github.com/dop251/goja v0.0.0-20261001174550-3ccc9c78af18

require (
	github.com/dlclark/regexp2/v2 v2.8.1 // indirect
	github.com/go-sourcemap/sourcemap v2.1.3+incompatible // indirect
	github.com/google/pprof v0.0.0-20230207041349-798e818bf904 // indirect
	golang.org/x/text v0.3.8 // indirect
)

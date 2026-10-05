//go:build !(js && wasm)

// 非 js/wasm 平台的占位：让 `go build ./...` / `go vet ./...` 在本机不至于因
// "build constraints exclude all Go files" 报错。wasm 入口本体见 main.go。
package main

import (
	"fmt"
	"runtime"
)

func main() {
	fmt.Printf("goxwasm 仅面向 GOOS=js GOARCH=wasm 构建（当前 %s/%s）。\n", runtime.GOOS, runtime.GOARCH)
}

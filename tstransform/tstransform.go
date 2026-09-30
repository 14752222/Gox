// Package tstransform 把 TypeScript / TSX 源码转译为等价的 JS 源码。
//
// gox 引擎本身只吃 JS + JSX（parser 有内建的 JSX 降级）；TS 支持以「入库转译」
// 的形式实现：文件按扩展名识别（.ts/.tsx/.mts/.cts/.jsx），进入 parser 管线前
// 先完成类型剥离。转译器选 esbuild 的 Go 内嵌 API（github.com/evanw/esbuild）：
//   - 它是 Go 生态里与 swc 对位的工业级实现，纯 Go、进程内、无外部进程/无
//     node_modules，编译进 gox 单二进制后体积约 +9MB —— 不破坏 "单文件分发"；
//   - 覆盖完整 TS 语法（enum / namespace / 参数属性等按 esbuild 语义做等价
//     转换而非拒绝），不用自研剥离器去追语法长尾；
//   - JSX 走 Preserve 模式：.tsx 里的 JSX 原样保留，交给引擎既有的 JSX 降级，
//     与 .js 模板走完全同一条运行时语义，不存在"TS 版组件跑法不一样"的问题。
//
// 类型检查不在 gox 的职责内（esbuild 只剥离类型、不检查）——IDE 里的 tsconfig
// 负责检查，运行时只保证语法过闸。
package tstransform

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// tsExts 是 TS 家族扩展名（含 .jsx —— 它与 .tsx 共用同一条通道，只是没有类型）。
var tsExts = map[string]bool{
	".ts":  true,
	".tsx": true,
	".mts": true,
	".cts": true,
	".jsx": true,
}

// IsTS 报告 path 按扩展名是否需要转译。大小写不敏感。
func IsTS(path string) bool {
	return tsExts[strings.ToLower(filepath.Ext(path))]
}

// ToJS 把 TS/TSX/JSX 源码转译为 JS。path 只用于选 loader 与错误报告。
//
// 失败时返回的错误逐条拼接 esbuild 诊断（含 文件:行:列 与原文摘录），直接
// 打给用户就能定位 —— 与引擎自身的 parse 错误信息格式对齐。
func ToJS(source []byte, path string) ([]byte, error) {
	loader := api.LoaderTS
	switch strings.ToLower(filepath.Ext(path)) {
	case ".tsx":
		loader = api.LoaderTSX
	case ".jsx":
		loader = api.LoaderJSX
	}

	// Target / Format 保持零值: 零值 = ESNext + 不做语法降级 —— gox 引擎
	// 自己决定支持什么, 转译层只剥类型、不改语义。JSXPreserve 让 .tsx 的
	// JSX 原样出给引擎 parser（与 .js 模板同一条 JSX 降级路径）。
	// Sourcefile 让诊断里的文件名/行号对应用户的源文件。
	result := api.Transform(string(source), api.TransformOptions{
		Loader:     loader,
		JSX:        api.JSXPreserve,
		Sourcefile: path,
	})
	if len(result.Errors) > 0 {
		return nil, transformError(path, result.Errors)
	}
	return result.Code, nil
}

// transformError 把 esbuild 诊断列表拼成一条用户可读的错误。
func transformError(path string, diags []api.Message) error {
	name := filepath.Base(path)
	var b strings.Builder
	fmt.Fprintf(&b, "%s: TypeScript 转译失败", name)
	for _, d := range diags {
		if d.Location == nil {
			fmt.Fprintf(&b, "\n  %s", d.Text)
			continue
		}
		fmt.Fprintf(&b, "\n  %s:%d:%d: %s", name, d.Location.Line, d.Location.Column, d.Text)
		if line := strings.TrimSpace(d.Location.LineText); line != "" {
			fmt.Fprintf(&b, "\n      %s", line)
		}
	}
	return errors.New(strings.TrimRight(b.String(), "\n"))
}

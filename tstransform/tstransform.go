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

// tsconfigRawJSON 是喂给 esbuild 的 TS 编译选项。
//
// 关键一项是 legacy 装饰器 (experimentalDecorators): 引擎 parser 不认识 `@`
// 语法, 所以必须让 esbuild 在转译阶段就把装饰器**降级成等价 JS 调用**
// (__decorate 辅助函数), 而不是原样保留 —— 否则带装饰器的 .ts 一进 parser 就挂。
//
// 不设 useDefineForClassFields: esbuild 的默认值随 target 走 (对 ESNext 默认
// define 语义)。显式改它会让类字段初始化语义漂移, 而 gox 引擎的类字段实现
// 与"赋值语义"更接近 getter/setter 路径 —— 保持 esbuild 默认, 需要时再单独收口。
const tsconfigRawJSON = `{"compilerOptions":{"experimentalDecorators":true}}`

// Result 是一次转译的完整产物。
type Result struct {
	// Code 是剥掉类型后的 JS (JSX 原样保留)。
	Code []byte
	// LineMap 是"JS 行 → .ts 行列"映射; esbuild 未给出 mappings 时为 nil,
	// 调用方必须走"无映射"降级路径 (不要假装精确)。
	LineMap *LineMap
}

// IsTS 报告 path 按扩展名是否需要转译。大小写不敏感。
func IsTS(path string) bool {
	return tsExts[strings.ToLower(filepath.Ext(path))]
}

// loaderFor 按扩展名选 esbuild loader。.jsx 与 .tsx 共用 JSX 通道。
func loaderFor(path string) api.Loader {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".tsx":
		return api.LoaderTSX
	case ".jsx":
		return api.LoaderJSX
	default:
		return api.LoaderTS
	}
}

// Transform 是纯转译入口: 剥离类型 + 生成行映射, 不碰磁盘/缓存。
//
// 失败时返回的错误逐条拼接 esbuild 诊断（含 文件:行:列 与原文摘录），直接
// 打给用户就能定位 —— 与引擎自身的 parse 错误信息格式对齐。
func Transform(source []byte, path string) (*Result, error) {
	// Target / Format 保持零值: 零值 = ESNext + 不做语法降级 —— gox 引擎
	// 自己决定支持什么, 转译层只剥类型、不改语义。JSXPreserve 让 .tsx 的
	// JSX 原样出给引擎 parser（与 .js 模板同一条 JSX 降级路径）。
	// Sourcefile 让诊断里的文件名/行号对应用户的源文件。
	//
	// Sourcemap: External 让 esbuild 把 mappings 作为独立的 JSON 返回
	// (result.Map), 不注入到 Code 里 —— Code 是要喂给引擎 parser 的纯净 JS,
	// 塞一段 base64 注释会污染它、也会被 parser 当成语句解析。
	result := api.Transform(string(source), api.TransformOptions{
		Loader:     loaderFor(path),
		JSX:        api.JSXPreserve,
		Sourcefile: path,
		Sourcemap:  api.SourceMapExternal,
		// 不要把源文嵌入 map: 我们已经持有 .ts 原文 (srcText), 复刻一份只会
		// 让缓存文件变大。
		SourcesContent: api.SourcesContentExclude,
		TsconfigRaw:    tsconfigRawJSON,
	})
	if len(result.Errors) > 0 {
		return nil, transformError(path, result.Errors)
	}
	lm, err := DecodeLineMap(result.Map)
	if err != nil {
		// 映射解析失败不能让转译失败 —— 退化为"无映射", 上层标注即可。
		lm = nil
	}
	return &Result{Code: result.Code, LineMap: lm}, nil
}

// ToJS 是 Transform 的兼容包装: 只返回 JS, 丢弃行映射 (旧调用方语义不变)。
func ToJS(source []byte, path string) ([]byte, error) {
	res, err := Transform(source, path)
	if err != nil {
		return nil, err
	}
	return res.Code, nil
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

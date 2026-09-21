// Package scaffold 是 Gox 自带的项目脚手架 —— 对标前端的 `npm create vite`：
// 一条命令铺出一个能直接跑起来的默认 GUI 工程。
//
// 模板以**真实文件**的形式放在 scaffold/template/ 下并用 go:embed 嵌进二进制，
// 而不是写成 Go 字符串常量。理由:
//   - 模板里的 JS 可以正常用 JS 语法（含模板字面量），不会被 Go 的原始字符串字面量截断;
//   - 模板可以被语法检查/编辑器直接打开，改了立刻看得见 diff;
//   - 二进制里没有"半个字符串拼错"这类只有运行时才知道的错。
//
// 生成过程是纯文本复制 + 占位符替换，不做任何 Go 模板渲染 —— 模板里的 `{ }` 太多，
// 走 text/template 只会给自己找转义麻烦。
package scaffold

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// templateFS 嵌入整个模板目录。必须用 all: 前缀 —— 否则 go:embed 会跳过
// 以 `.` 或 `_` 开头的文件，模板里的 `.gitignore` 就丢了。
//
//go:embed all:template
var templateFS embed.FS

// templateRoot 是 embed 里的模板根目录名。
const templateRoot = "template"

// 模板占位符。刻意分成"合法包名"与"展示标题"两个:
// 项目目录名可能是中文或大写，直接用进 package.json 的 name 字段是非法的
// （npm 要求小写、URL 安全），所以两者不能共用一个占位符。
const (
	placeholderName  = "__PROJECT_NAME__"  // npm 合法名: 小写 + 数字 + 短横线
	placeholderTitle = "__PROJECT_TITLE__" // 展示用标题: 用户给什么就是什么
)

// Options 控制一次生成。
type Options struct {
	// Dir 是目标目录（必填）。可以嵌套，父目录会被创建。
	Dir string
	// Name 覆盖项目名（用于 package.json）；留空则从 Dir 的基名推导。
	Name string
	// Force 允许目标目录已存在且非空时仍然写入（会覆盖同名文件，不清理其它文件）。
	Force bool
}

// File 是生成结果里的一个文件，供 CLI 打印清单。
type File struct {
	// Path 是相对项目根的斜杠路径（如 src/components/counter.js）。
	Path string
	// Bytes 是落盘后的大小。
	Bytes int
}

// Create 把默认模板铺到 opts.Dir 下，返回生成的文件清单（按路径排序）。
//
// 默认拒绝往非空目录里生成 —— 脚手架最常见的误操作就是在已有工程里跑一次
// create，把刚写的代码盖掉。要覆盖得显式给 Force。
func Create(opts Options) ([]File, error) {
	if strings.TrimSpace(opts.Dir) == "" {
		return nil, fmt.Errorf("目标目录不能为空")
	}
	dir := filepath.Clean(opts.Dir)

	title := strings.TrimSpace(opts.Name)
	if title == "" {
		title = filepath.Base(dir)
	}
	// Base(".") / Base("..") / Base("C:\\") 都会退化成 "." 或分隔符，说明用户
	// 传的是"当前目录"这类没有名字的目标 —— 拒绝，别生成一个叫 "." 的工程。
	if title == "." || title == string(filepath.Separator) || title == "" {
		return nil, fmt.Errorf("无法从目录 %q 推导项目名，请用 --name 指定", opts.Dir)
	}
	pkgName := npmSafeName(title)

	if err := ensureWritableDir(dir, opts.Force); err != nil {
		return nil, err
	}

	var files []File
	err := fs.WalkDir(templateFS, templateRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(templateRoot, p)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)

		data, readErr := fs.ReadFile(templateFS, p)
		if readErr != nil {
			return readErr
		}
		data = expandPlaceholders(data, pkgName, title)

		dest := filepath.Join(dir, filepath.FromSlash(rel))
		if mkErr := os.MkdirAll(filepath.Dir(dest), 0o755); mkErr != nil {
			return mkErr
		}
		if writeErr := os.WriteFile(dest, data, 0o644); writeErr != nil {
			return writeErr
		}
		files = append(files, File{Path: rel, Bytes: len(data)})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("写入模板失败: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

// ensureWritableDir 检查目标目录可用: 不存在则创建; 已存在且非空时要求 Force。
func ensureWritableDir(dir string, force bool) error {
	info, err := os.Stat(dir)
	switch {
	case err == nil:
		if !info.IsDir() {
			return fmt.Errorf("%s 已存在且不是目录", dir)
		}
		if force {
			return nil
		}
		entries, readErr := os.ReadDir(dir)
		if readErr != nil {
			return fmt.Errorf("读取 %s 失败: %w", dir, readErr)
		}
		if len(entries) > 0 {
			return fmt.Errorf("目录 %s 已存在且非空，拒绝覆盖（换一个目录名，或加 --force 明确覆盖）", dir)
		}
		return nil
	case os.IsNotExist(err):
		if mkErr := os.MkdirAll(dir, 0o755); mkErr != nil {
			return fmt.Errorf("创建目录 %s 失败: %w", dir, mkErr)
		}
		return nil
	default:
		return fmt.Errorf("检查目录 %s 失败: %w", dir, err)
	}
}

// expandPlaceholders 替换模板里的两个占位符。
//
// 逐个字符串替换而不是走模板引擎: 模板里 `{` `}` 遍地都是（JSX 属性、CSS 数值），
// 任何模板语法都得先过一遍转义，得不偿失。
func expandPlaceholders(data []byte, pkgName, title string) []byte {
	s := string(data)
	s = strings.ReplaceAll(s, placeholderName, pkgName)
	s = strings.ReplaceAll(s, placeholderTitle, title)
	return []byte(s)
}

// npmSafeName 把任意项目名收敛成合法的 npm 包名（小写、只留字母数字与短横线）。
//
// 中文/大写目录名在这里被"降级"成短横线 —— 用户拿到的 package.json 仍然合法，
// 而窗口标题、README 里用的是原样的 title（见 placeholderTitle）。
func npmSafeName(raw string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		isAlnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlnum {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		// 连续的非字母数字折叠成一个短横线; 开头的短横线不留。
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "gox-app"
	}
	return out
}

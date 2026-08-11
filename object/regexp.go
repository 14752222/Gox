package object

import (
	"fmt"
	"regexp"
	"sync"
)

// RegExp 表示 JavaScript 的正则表达式对象。
type RegExp struct {
	Pattern     string         // 原始正则表达式模式
	Flags       string         // 标志位 (g, i, m, s, u, y)
	Regexp      *regexp.Regexp // Go 编译后的正则
	Global      bool           // g 标志
	IgnoreCase  bool           // i 标志
	Multiline   bool           // m 标志
	DotAll      bool           // s 标志
	mu          sync.Mutex
	LastIndex   int            // 上次匹配位置 (g 标志使用)
}

func (r *RegExp) Type() ObjectType { return REGEXP_OBJ }
func (r *RegExp) Inspect() string {
	return fmt.Sprintf("/%s/%s", r.Pattern, r.Flags)
}
func (r *RegExp) IsTruthy() bool { return true }

func (r *RegExp) GetProperty(name string) (Value, bool) {
	switch name {
	case "source":
		return NewString(r.Pattern), true
	case "flags":
		return NewString(r.Flags), true
	case "global":
		return NewBoolean(r.Global), true
	case "ignoreCase":
		return NewBoolean(r.IgnoreCase), true
	case "multiline":
		return NewBoolean(r.Multiline), true
	case "dotAll":
		return NewBoolean(r.DotAll), true
	case "lastIndex":
		return NewNumber(float64(r.LastIndex)), true
	}
	if RegExpProto != nil {
		return RegExpProto.GetProperty(name)
	}
	return nil, false
}

func (r *RegExp) SetProperty(name string, val Value) {
	if name == "lastIndex" {
		if num, ok := val.(*Number); ok {
			r.LastIndex = int(num.Value)
		}
	}
}

// NewRegExp 创建正则表达式对象。
func NewRegExp(pattern, flags string) (*RegExp, error) {
	r := &RegExp{
		Pattern: pattern,
		Flags:   flags,
	}

	// 解析标志
	for _, f := range flags {
		switch f {
		case 'g':
			r.Global = true
		case 'i':
			r.IgnoreCase = true
		case 'm':
			r.Multiline = true
		case 's':
			r.DotAll = true
		case 'u', 'y':
			// u (unicode) 和 y (sticky) 标志简化处理
		}
	}

	// 转换 JS 正则到 Go 正则
	goPattern := jsToGoRegex(pattern, r.IgnoreCase, r.Multiline, r.DotAll)
	re, err := regexp.Compile(goPattern)
	if err != nil {
		return nil, fmt.Errorf("SyntaxError: Invalid regular expression: %s", pattern)
	}
	r.Regexp = re
	return r, nil
}

// jsToGoRegex 将 JS 正则模式转换为 Go 正则模式 (简化)。
func jsToGoRegex(pattern string, ignoreCase, multiline, dotAll bool) string {
	// Go 的 regexp 使用 RE2 语法，大部分与 JS 兼容
	// 主要差异: JS 的 \d, \w, \s 等在 Go 中需要使用 (?i) 等标志
	result := pattern

	// 添加标志前缀
	prefix := ""
	if ignoreCase {
		prefix += "i"
	}
	if multiline {
		prefix += "m"
	}
	if dotAll {
		// Go 的 regexp 不直接支持 s 标志，但可以用 (?s) 前缀
		prefix += "s"
	}
	if prefix != "" {
		result = "(?" + prefix + ")" + result
	}

	return result
}

var RegExpProto Value

func SetRegExpProto(p Value) { RegExpProto = p }

// Lock 加锁 (供外部包使用)。
func (r *RegExp) Lock() {
	r.mu.Lock()
}

// Unlock 解锁 (供外部包使用)。
func (r *RegExp) Unlock() {
	r.mu.Unlock()
}

package object

import (
	"fmt"
	"regexp"
	"sync"
)

// RegExp 表示 JavaScript 的正则表达式对象。
//
// 除正则本体外, 实例还承载**自有属性存储** (PropDescs): 规范形态的自有属性
// 只有 lastIndex ({value, writable:true, enumerable:false, configurable:false}),
// 但用户可以在实例上 defineProperty 任意键 —— test262 正是靠「把 lastIndex 改
// 成不可写」来断言 @@match / @@replace 里的 Set(R,"lastIndex",v,true) 会抛
// TypeError。此前 lastIndex 只是裸结构体字段, 无从表达「不可写」, 于是
// 这 8 个用例全是假阳性通过 (详见 stdlib/regexp.go 的注释与 rEXjyz)。
type RegExp struct {
	Pattern    string         // 原始正则表达式模式
	Flags      string         // 标志位 (g, i, m, s, u, y)
	Regexp     *regexp.Regexp // Go 编译后的正则
	Global     bool           // g 标志
	IgnoreCase bool           // i 标志
	Multiline  bool           // m 标志
	DotAll     bool           // s 标志
	Sticky     bool           // y 标志 (粘性匹配)
	Unicode    bool           // u 标志
	mu         sync.Mutex
	LastIndex  int // 上次匹配位置 (g/y 标志使用)

	// PropDescs 承载 defineProperty / 赋值落在实例上的自有属性描述符。
	// lastIndex 的规范描述符是懒建 (见 ownLastIndexDescriptor)。
	PropDescs map[string]PropertyDescriptor
	// propKeyOrder 记录 defineProperty 定义键的创建顺序 (OwnKeys 口径)。
	propKeyOrder []string
}

func (r *RegExp) Type() ObjectType { return REGEXP_OBJ }
func (r *RegExp) Inspect() string {
	return fmt.Sprintf("/%s/%s", r.Pattern, r.Flags)
}
func (r *RegExp) IsTruthy() bool { return true }

// GetProto 返回实例的 [[Prototype]] = %RegExp.prototype%。
//
// 原型链遍历的事实来源是鸭子类型入口 `interface{ GetProto() Value }`。此前
// *RegExp 缺这个方法, 链在实例处断掉 —— 即便 RegExpProto 已装配, 经链式
// 查找 (symbol 成员 / getPrototypeOf) 的读法仍取不到 exec / @@match 等成员。
func (r *RegExp) GetProto() Value { return RegExpProto }

// ownLastIndexDescriptor 返回 lastIndex 的规范自有描述符:
// {writable:true, enumerable:false, configurable:false}。
func (r *RegExp) ownLastIndexDescriptor() PropertyDescriptor {
	return PropertyDescriptor{
		Value:        NewNumber(float64(r.LastIndex)),
		Writable:     true,
		Enumerable:   false,
		Configurable: false,
	}
}

func (r *RegExp) GetProperty(name string) (Value, bool) {
	// (1) 显式定义过的自有属性: 访问器调 getter (this = 实例), 数据属性取
	// 描述符里的值。r.exec = fn 也落在这里 (SetProperty 走 DefineOwn)。
	if d, ok := r.PropDescs[name]; ok && !d.Deleted {
		if acc, isAcc := d.Value.(*Accessor); isAcc {
			if acc.Getter != nil && IsCallable(acc.Getter) {
				return CallFunction(acc.Getter, r), true
			}
			return UndefinedSingleton, true
		}
		if d.Value == nil {
			return UndefinedSingleton, true
		}
		return d.Value, true
	}
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
	case "sticky":
		return NewBoolean(r.Sticky), true
	case "unicode":
		return NewBoolean(r.Unicode), true
	case "lastIndex":
		// 事实来源始终是 LastIndex 字段 (描述符只承载可写性等元数据),
		// 二者在 DefineOwn / SetProperty 处保持同步。
		return NewNumber(float64(r.LastIndex)), true
	}
	if RegExpProto != nil {
		return RegExpProto.GetProperty(name)
	}
	return nil, false
}

func (r *RegExp) SetProperty(name string, val Value) {
	// 显式描述符优先: 访问器调 setter, 不可写数据属性静默失败 (非严格模式),
	// 与 *Array.SetProperty / *Object.SetProperty 的口径一致。
	if d, ok := r.PropDescs[name]; ok && !d.Deleted {
		if acc, isAcc := d.Value.(*Accessor); isAcc {
			if acc.Setter != nil && IsCallable(acc.Setter) {
				CallFunction(acc.Setter, r, val)
			}
			return
		}
		if !d.Writable {
			return
		}
		d.Value = val
		r.PropDescs[name] = d
	}
	if name == "lastIndex" {
		if num, ok := val.(*Number); ok {
			r.LastIndex = int(num.Value)
			// 同步描述符里的值 (若有), 使 getOwnPropertyDescriptor 读到的
			// value 与实际匹配位置一致。
			if d, exists := r.PropDescs[name]; exists && !d.Deleted {
				if _, isAcc := d.Value.(*Accessor); !isAcc {
					d.Value = val
					r.PropDescs[name] = d
				}
			}
		}
		return
	}
	// 其余键 (r.exec = fn / r.foo = 1) 落成普通自有数据属性。
	r.DefineOwn(name, DataProperty(val))
}

// SetLastIndexStrict 按规范 Set(R, "lastIndex", v, true) 的语义写入:
// 「严格」写入 —— 不可写 / 只有 getter 的访问器 ⇒ 返回 false, 调用方据此抛
// TypeError (ReturnIfAbrupt)。这是 @@match / @@replace 步骤 8.c / 15.c 的落点。
//
// Gox 的 Value.SetProperty 没有返回值, 无法表达严格 Set, 故单开这个方法。
func (r *RegExp) SetLastIndexStrict(v int) bool {
	if d, ok := r.PropDescs["lastIndex"]; ok && !d.Deleted {
		if acc, isAcc := d.Value.(*Accessor); isAcc {
			if acc.Setter == nil || !IsCallable(acc.Setter) {
				return false
			}
			CallFunction(acc.Setter, r, NewNumber(float64(v)))
			return true
		}
		if !d.Writable {
			return false
		}
		d.Value = NewNumber(float64(v))
		r.PropDescs["lastIndex"] = d
	}
	r.LastIndex = v
	return true
}

// ===== OwnPropertyStore 五件套 (Object.defineProperty /
// getOwnPropertyDescriptor / getOwnPropertyNames / hasOwn 的统一落点) =====

func (r *RegExp) OwnKeys() []string {
	keys := make([]string, 0, 1+len(r.PropDescs))
	if !deletedProp(r.PropDescs, "lastIndex") {
		keys = append(keys, "lastIndex")
	}
	for _, k := range r.propKeyOrder {
		if k == "lastIndex" || deletedProp(r.PropDescs, k) {
			continue
		}
		keys = append(keys, k)
	}
	return keys
}

// EnumerableOwnKeys 返回可枚举自有键: lastIndex 恒不可枚举, 故默认为空 ——
// Object.keys(/a/g) 与 Node 一致为 []。用户 defineProperty 出的可枚举键照列。
func (r *RegExp) EnumerableOwnKeys() []string {
	var keys []string
	for _, k := range r.propKeyOrder {
		d, ok := r.PropDescs[k]
		if !ok || d.Deleted || !d.Enumerable {
			continue
		}
		keys = append(keys, k)
	}
	return keys
}

func (r *RegExp) HasOwn(name string) bool {
	_, ok := r.OwnDescriptor(name)
	return ok
}

func (r *RegExp) OwnDescriptor(name string) (PropertyDescriptor, bool) {
	if d, ok := r.PropDescs[name]; ok {
		if d.Deleted {
			return PropertyDescriptor{}, false
		}
		return d, true
	}
	if name == "lastIndex" {
		return r.ownLastIndexDescriptor(), true
	}
	return PropertyDescriptor{}, false
}

func (r *RegExp) DefineOwn(name string, desc PropertyDescriptor) bool {
	if r.PropDescs == nil {
		r.PropDescs = make(map[string]PropertyDescriptor)
	}
	if _, exists := r.PropDescs[name]; !exists && name != "lastIndex" {
		r.propKeyOrder = append(r.propKeyOrder, name)
	}
	r.PropDescs[name] = desc
	// 数据描述符的 lastIndex: 把值同步进字段, 使匹配逻辑仍以结构体字段为准。
	if name == "lastIndex" {
		if _, isAcc := desc.Value.(*Accessor); !isAcc {
			if n, ok := desc.Value.(*Number); ok {
				r.LastIndex = int(n.Value)
			}
		}
	}
	return true
}

// DeleteOwn 删除实例的可配置自有属性。lastIndex 规范恒不可配置 ⇒ false。
func (r *RegExp) DeleteOwn(name string) bool {
	if d, ok := r.PropDescs[name]; ok {
		if d.Deleted {
			return true
		}
		if !d.Configurable {
			return false
		}
		delete(r.PropDescs, name)
		for i, k := range r.propKeyOrder {
			if k == name {
				r.propKeyOrder = append(r.propKeyOrder[:i], r.propKeyOrder[i+1:]...)
				break
			}
		}
		return true
	}
	if name == "lastIndex" {
		return false
	}
	return true
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
		case 'y':
			// y (sticky): 匹配必须从 lastIndex 处开始
			r.Sticky = true
		case 'u':
			// u (unicode): 按 Unicode 码点处理模式
			r.Unicode = true
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

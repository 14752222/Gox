package object

import (
	"fmt"
	"sync"
)

// Symbol 表示 JavaScript 的 Symbol 类型。
// Symbol 是唯一且不可变的原始值，常用作对象属性的键。
type Symbol struct {
	Description string // 可选的描述字符串
	ID          uint64 // 唯一标识符
}

func (s *Symbol) Type() ObjectType { return SYMBOL_OBJ }
func (s *Symbol) Inspect() string {
	if s.Description == "" {
		return fmt.Sprintf("Symbol()")
	}
	return fmt.Sprintf("Symbol(%s)", s.Description)
}
func (s *Symbol) IsTruthy() bool { return true }

func (s *Symbol) GetProperty(name string) (Value, bool) {
	switch name {
	case "description":
		return NewString(s.Description), true
	}
	// 其余属性 (toString / valueOf / hasOwnProperty / @@toStringTag ...)
	// 一律沿 %Symbol.prototype% 查找 —— 与 String/Number/BigInt 一致。
	if SymbolProto != nil {
		if v, ok := SymbolProto.GetProperty(name); ok {
			return v, true
		}
	}
	// 兜底: %Symbol.prototype% 未注册时仍支持 toString 反射。
	if name == "toString" {
		return NewBuiltinMethod("toString", func(this Value, args ...Value) Value {
			if sym, ok := this.(*Symbol); ok {
				return NewString(sym.Inspect())
			}
			return NewString("Symbol()")
		}), true
	}
	return nil, false
}

func (s *Symbol) SetProperty(name string, val Value) {
	// Symbol 是不可变的
}

// SymbolProto 是 Symbol 原始值的原型对象 (Symbol.prototype)。
// 由 stdlib 包初始化时设置 —— Symbol 原始值没有自有属性，
// 属性访问 (含 @@toStringTag) 一律沿它查找。
var SymbolProto Value

// SetSymbolProto 设置全局 Symbol 原型 (由 stdlib 调用)。
func SetSymbolProto(p Value) { SymbolProto = p }

// GetSymbolProto 返回全局 Symbol 原型。
func GetSymbolProto() Value { return SymbolProto }

// ===== 全局 Symbol 注册表 =====

var (
	symbolCounter uint64
	symbolMutex   sync.Mutex
	globalSymbols = make(map[string]*Symbol) // 全局 Symbol 注册表
)

// NewSymbol 创建一个新的唯一 Symbol。
func NewSymbol(description string) *Symbol {
	symbolMutex.Lock()
	defer symbolMutex.Unlock()
	symbolCounter++
	return &Symbol{
		Description: description,
		ID:          symbolCounter,
	}
}

// GetGlobalSymbol 从全局注册表获取或创建 Symbol。
func GetGlobalSymbol(key string) *Symbol {
	symbolMutex.Lock()
	defer symbolMutex.Unlock()
	if sym, ok := globalSymbols[key]; ok {
		return sym
	}
	symbolCounter++
	sym := &Symbol{
		Description: key,
		ID:          symbolCounter,
	}
	globalSymbols[key] = sym
	return sym
}

// KeyForSymbol 查找全局 Symbol 的 key。
func KeyForSymbol(sym *Symbol) (string, bool) {
	symbolMutex.Lock()
	defer symbolMutex.Unlock()
	for key, s := range globalSymbols {
		if s.ID == sym.ID {
			return key, true
		}
	}
	return "", false
}

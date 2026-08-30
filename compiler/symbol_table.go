// Package compiler 将 AST 编译为字节码。
//
// 编译流程: AST → [Compiler] → Bytecode(Instructions + ConstantPool)
//
// 符号表管理变量作用域: 每个作用域是一个 SymbolScope，
// 包含 name → Symbol(slot, isConst) 映射。
// 新作用域在进入块和函数时创建。
package compiler

// Symbol 表示一个变量绑定。
type Symbol struct {
	Name     string
	Slot     int  // 在局部变量数组中的槽位号
	IsConst  bool // 是否 const 绑定
	Depth    int  // 作用域深度 (0=全局)
}

// SymbolScope 表示一个作用域层级的符号表。
type SymbolScope struct {
	store  map[string]*Symbol // 变量名 → 符号
	parent *SymbolScope        // 外层作用域
	depth  int                 // 作用域深度
	nextSlot int               // 下一个可用槽位
}

// NewSymbolScope 创建新的作用域。
func NewSymbolScope(parent *SymbolScope) *SymbolScope {
	depth := 0
	nextSlot := 0
	if parent != nil {
		depth = parent.depth + 1
		nextSlot = parent.nextSlot
	}
	return &SymbolScope{
		store:    make(map[string]*Symbol),
		parent:   parent,
		depth:    depth,
		nextSlot: nextSlot,
	}
}

// Define 在当前作用域中定义新变量。
// 返回创建的 Symbol。如果变量已存在则覆盖 (允许重声明)。
func (s *SymbolScope) Define(name string, isConst bool) *Symbol {
	sym := &Symbol{
		Name:    name,
		Slot:    s.nextSlot,
		IsConst: isConst,
		Depth:   s.depth,
	}
	s.store[name] = sym
	s.nextSlot++
	return sym
}

// Resolve 查找变量。先查本地，未命中递归查外层。
func (s *SymbolScope) Resolve(name string) *Symbol {
	if sym, ok := s.store[name]; ok {
		return sym
	}
	if s.parent != nil {
		return s.parent.Resolve(name)
	}
	return nil
}

// NumLocals 返回当前作用域及其父作用域中的总变量数
// (实际上返回最深作用域的 nextSlot)。
func (s *SymbolScope) NumLocals() int {
	return s.nextSlot
}

// HasLocal 检查变量是否在当前作用域中定义。
func (s *SymbolScope) HasLocal(name string) bool {
	_, ok := s.store[name]
	return ok
}

// Depth 返回作用域深度。
func (s *SymbolScope) Depth() int { return s.depth }

// Parent 返回父作用域。
func (s *SymbolScope) Parent() *SymbolScope { return s.parent }

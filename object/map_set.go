package object

import (
	"fmt"
	"sync"
)

// MapEntry 表示 Map 中的一个条目。
type MapEntry struct {
	Key   Value
	Value Value
}

// Map 表示 JavaScript 的 Map 对象。
// Map 保存键值对，键可以是任意值（包括对象和 Symbol）。
type Map struct {
	Entries  []*MapEntry
	keyIndex map[string]int // 用于原始类型键的快速查找
	objIndex map[uint64]int // 用于对象/Symbol 键的快速查找
	mu       sync.RWMutex
}

func (m *Map) Type() ObjectType { return MAP_OBJ }
func (m *Map) Inspect() string {
	return fmt.Sprintf("Map(%d)", len(m.Entries))
}
func (m *Map) IsTruthy() bool { return true }

func (m *Map) GetProperty(name string) (Value, bool) {
	switch name {
	case "size":
		return NewInt(int64(len(m.Entries))), true
	}
	if MapProto != nil {
		return MapProto.GetProperty(name)
	}
	return nil, false
}

func (m *Map) SetProperty(name string, val Value) {
	// Map 的属性不可直接设置
}

// mapKeyHash 为原始类型键生成哈希键字符串。
func mapKeyHash(v Value) (string, bool) {
	switch val := v.(type) {
	case *String:
		return "s:" + val.Value, true
	case *Number:
		return fmt.Sprintf("n:%v", val.Value), true
	case *Boolean:
		if val.Value {
			return "b:1", true
		}
		return "b:0", true
	case *Null:
		return "null", true
	case *Undefined:
		return "undef", true
	}
	return "", false // 对象类型用 ID
}

// mapKeyID 为对象/Symbol 类型键生成唯一 ID。
func mapKeyID(v Value) uint64 {
	switch val := v.(type) {
	case *Symbol:
		return val.ID
	case *Object:
		return uint64(uintptr(0)) // 将在 Set 中用指针
	}
	return 0
}

// Set 设置键值对。
func (m *Map) Set(key, val Value) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.Entries == nil {
		m.Entries = []*MapEntry{}
		m.keyIndex = make(map[string]int)
		m.objIndex = make(map[uint64]int)
	}

	// 检查键是否已存在
	if hash, ok := mapKeyHash(key); ok {
		if idx, found := m.keyIndex[hash]; found {
			m.Entries[idx].Value = val
			return
		}
		// 新键
		idx := len(m.Entries)
		m.Entries = append(m.Entries, &MapEntry{Key: key, Value: val})
		m.keyIndex[hash] = idx
		return
	}

	// 对象类型键：线性搜索（简化实现）
	for i, entry := range m.Entries {
		if entry.Key == key {
			m.Entries[i].Value = val
			return
		}
	}
	idx := len(m.Entries)
	m.Entries = append(m.Entries, &MapEntry{Key: key, Value: val})
	if id := mapKeyID(key); id != 0 {
		m.objIndex[id] = idx
	}
}

// Get 获取键对应的值。
func (m *Map) Get(key Value) (Value, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if hash, ok := mapKeyHash(key); ok {
		if idx, found := m.keyIndex[hash]; found {
			return m.Entries[idx].Value, true
		}
		return nil, false
	}

	for _, entry := range m.Entries {
		if entry.Key == key {
			return entry.Value, true
		}
	}
	return nil, false
}

// Has 检查键是否存在。
func (m *Map) Has(key Value) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if hash, ok := mapKeyHash(key); ok {
		_, found := m.keyIndex[hash]
		return found
	}

	for _, entry := range m.Entries {
		if entry.Key == key {
			return true
		}
	}
	return false
}

// Delete 删除键值对。
func (m *Map) Delete(key Value) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	if hash, ok := mapKeyHash(key); ok {
		if idx, found := m.keyIndex[hash]; found {
			m.Entries = append(m.Entries[:idx], m.Entries[idx+1:]...)
			// 重建索引
			m.keyIndex = make(map[string]int)
			m.objIndex = make(map[uint64]int)
			for i, entry := range m.Entries {
				if h, ok := mapKeyHash(entry.Key); ok {
					m.keyIndex[h] = i
				}
			}
			return true
		}
		return false
	}

	for i, entry := range m.Entries {
		if entry.Key == key {
			m.Entries = append(m.Entries[:i], m.Entries[i+1:]...)
			return true
		}
	}
	return false
}

// Clear 清空 Map。
func (m *Map) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Entries = []*MapEntry{}
	m.keyIndex = make(map[string]int)
	m.objIndex = make(map[uint64]int)
}

// NewMap 创建空 Map。
func NewMap() *Map {
	return &Map{
		Entries:  []*MapEntry{},
		keyIndex: make(map[string]int),
		objIndex: make(map[uint64]int),
	}
}

// ===== Set =====

// Set 表示 JavaScript 的 Set 对象。
// Set 是值的集合，每个值只出现一次。
type Set struct {
	Values   []Value
	keyIndex map[string]int
	mu       sync.RWMutex
}

func (s *Set) Type() ObjectType { return SET_OBJ }
func (s *Set) Inspect() string {
	return fmt.Sprintf("Set(%d)", len(s.Values))
}
func (s *Set) IsTruthy() bool { return true }

func (s *Set) GetProperty(name string) (Value, bool) {
	switch name {
	case "size":
		return NewInt(int64(len(s.Values))), true
	}
	if SetProto != nil {
		return SetProto.GetProperty(name)
	}
	return nil, false
}

func (s *Set) SetProperty(name string, val Value) {}

// Add 添加值到 Set。
func (s *Set) Add(val Value) *Set {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.keyIndex == nil {
		s.keyIndex = make(map[string]int)
	}

	if hash, ok := mapKeyHash(val); ok {
		if _, found := s.keyIndex[hash]; found {
			return s
		}
		s.keyIndex[hash] = len(s.Values)
		s.Values = append(s.Values, val)
		return s
	}

	// 对象类型：线性搜索
	for _, v := range s.Values {
		if v == val {
			return s
		}
	}
	s.Values = append(s.Values, val)
	return s
}

// Has 检查值是否在 Set 中。
func (s *Set) Has(val Value) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if hash, ok := mapKeyHash(val); ok {
		_, found := s.keyIndex[hash]
		return found
	}

	for _, v := range s.Values {
		if v == val {
			return true
		}
	}
	return false
}

// Delete 从 Set 中删除值。
func (s *Set) Delete(val Value) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if hash, ok := mapKeyHash(val); ok {
		if idx, found := s.keyIndex[hash]; found {
			s.Values = append(s.Values[:idx], s.Values[idx+1:]...)
			s.keyIndex = make(map[string]int)
			for i, v := range s.Values {
				if h, ok := mapKeyHash(v); ok {
					s.keyIndex[h] = i
				}
			}
			return true
		}
		return false
	}

	for i, v := range s.Values {
		if v == val {
			s.Values = append(s.Values[:i], s.Values[i+1:]...)
			return true
		}
	}
	return false
}

// Clear 清空 Set。
func (s *Set) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Values = []Value{}
	s.keyIndex = make(map[string]int)
}

// NewSet 创建空 Set。
func NewSet() *Set {
	return &Set{
		Values:   []Value{},
		keyIndex: make(map[string]int),
	}
}

// ===== 原型引用 =====

var (
	MapProto Value
	SetProto Value
)

func SetMapProto(p Value) { MapProto = p }
func SetSetProto(p Value) { SetProto = p }

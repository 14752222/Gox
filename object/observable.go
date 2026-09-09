package object

import "sync"

// 本文件实现 Dart GetX 风格的响应式状态 (Rx):
//
//	let count = obs(0);        // RxInt
//	count.value;               // 读
//	count.value = 5;           // 写并通知监听者
//	count.listen(fn);          // 订阅 (BehaviorSubject 语义: 立即回调当前值)
//	let full = computed(...);  // 自动追踪依赖的计算属性
//
// 结构:
//   - Observable      包装任意标量值
//   - ObservableList  包装数组 (变更方法触发通知)
//   - ObservableMap   包装 Map (set/delete/clear 触发通知)
//   - Computed        基于依赖追踪的惰性计算属性
//
// 依赖追踪: 读取 .value 时若存在"活动追踪器"(computed 求值中), 该
// Observable 会把自己注册为它的依赖, 值变化时使 computed 失效并通知。

// RxIndexed 支持数字索引读写的 Rx 单元 (ObservableList)。
// VM 的索引读写路径通过此接口转发, 避免类型依赖。
type RxIndexed interface {
	GetIndexedElement(i int) Value
	SetIndexedElement(i int, v Value)
}

// ObservableState 是所有 Rx 单元的公共接口 (GetX RxInterface 的对应物)。
type ObservableState interface {
	Value
	// RxValue 返回当前值。
	RxValue() Value
	// RxSubscribe 订阅变化, 返回取消订阅的函数 (GetX cancel())。
	RxSubscribe(fn Value) Value
	// RxClose 关闭单元, 之后不再通知。
	RxClose()
}

// ===== 依赖追踪 =====

type rxTracker struct {
	deps map[RxSource]struct{}
	mu   sync.Mutex
}

// RxSource 是可被 computed 依赖的可观察单元 (以指针身份去重)。
type RxSource interface{ rxSource() }

var (
	rxTrackerMu   sync.Mutex
	rxActiveStack []*rxTracker
)

func rxCurrentTracker() *rxTracker {
	rxTrackerMu.Lock()
	defer rxTrackerMu.Unlock()
	if len(rxActiveStack) == 0 {
		return nil
	}
	return rxActiveStack[len(rxActiveStack)-1]
}

func rxPushTracker(t *rxTracker) {
	rxTrackerMu.Lock()
	rxActiveStack = append(rxActiveStack, t)
	rxTrackerMu.Unlock()
}

func rxPopTracker(t *rxTracker) {
	rxTrackerMu.Lock()
	stack := rxActiveStack
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == t {
			rxActiveStack = stack[:i]
			break
		}
	}
	rxTrackerMu.Unlock()
}

// sameRxValue 判断新旧值是否"相等" (GetX 语义: 相等则不通知)。
// 数字按值比较 (NaN 恒不相等), 其余同类型按值/指针比较。
func sameRxValue(a, b Value) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Type() != b.Type() {
		return false
	}
	switch av := a.(type) {
	case *Number:
		bv := b.(*Number)
		if isNaNValue(av.Value) || isNaNValue(bv.Value) {
			return false
		}
		return av.Value == bv.Value
	case *String:
		return av.Value == b.(*String).Value
	case *Boolean:
		return av.Value == b.(*Boolean).Value
	case *Null, *Undefined:
		return true
	}
	return a == b
}

func isNaNValue(f float64) bool { return f != f }

// ===== Observable: 标量单元 =====

// Observable 包装一个值, 读写 .value 时触发依赖追踪与通知。
type Observable struct {
	inner     Value
	listeners []Value
	closed    bool
	tracker   *rxTracker // 本单元被 computed 依赖时, 记录其追踪器
	mu        sync.Mutex
}

func NewObservable(v Value) *Observable { return &Observable{inner: v} }

func (o *Observable) Type() ObjectType { return OBSERVABLE_OBJ }
func (o *Observable) Inspect() string {
	if o.inner == nil {
		return "Rx<undefined>"
	}
	return "Rx<" + o.inner.Inspect() + ">"
}
func (o *Observable) IsTruthy() bool { return true }

func (o *Observable) rxSource() {}

func (o *Observable) RxValue() Value {
	o.mu.Lock()
	v := o.inner
	o.mu.Unlock()
	// 依赖追踪
	if t := rxCurrentTracker(); t != nil {
		t.track(o)
	}
	return v
}

// set 写入值; changed 为 false 表示值未变化 (GetX 语义: 不通知)。
func (o *Observable) set(v Value) bool {
	o.mu.Lock()
	if o.closed {
		o.mu.Unlock()
		return false
	}
	old := o.inner
	if sameRxValue(old, v) {
		o.mu.Unlock()
		return false
	}
	o.inner = v
	listeners := make([]Value, len(o.listeners))
	copy(listeners, o.listeners)
	o.mu.Unlock()
	notifyRxListeners(listeners, v, old)
	return true
}

// notifyRxListeners 调用监听者: fn(newValue, oldValue)。
func notifyRxListeners(listeners []Value, nv, ov Value) {
	for _, fn := range listeners {
		if IsCallable(fn) {
			CallFunction(fn, nil, nv, ov)
		}
	}
}

func (o *Observable) GetProperty(name string) (Value, bool) {
	switch name {
	case "value":
		return o.RxValue(), true
	case "isClosed":
		o.mu.Lock()
		c := o.closed
		o.mu.Unlock()
		return NewBoolean(c), true
	case "listen":
		return NewBuiltin("listen", func(args ...Value) Value { return o.RxSubscribe(argRx(args, 0)) }), true
	case "close":
		return NewBuiltin("close", func(args ...Value) Value { o.RxClose(); return UndefinedSingleton }), true
	case "refresh":
		return NewBuiltin("refresh", func(args ...Value) Value {
			o.mu.Lock()
			listeners := make([]Value, len(o.listeners))
			copy(listeners, o.listeners)
			v := o.inner
			o.mu.Unlock()
			notifyRxListeners(listeners, v, v)
			return UndefinedSingleton
		}), true
	case "call":
		// GetX: count() 等价 count.value
		return NewBuiltin("call", func(args ...Value) Value { return o.RxValue() }), true
	case "toString":
		return NewBuiltin("toString", func(args ...Value) Value {
			return NewString(ToString(o.RxValue()))
		}), true
	case "valueOf":
		return NewBuiltin("valueOf", func(args ...Value) Value { return o.RxValue() }), true
	}
	return nil, false
}

func (o *Observable) SetProperty(name string, val Value) {
	if name == "value" {
		o.set(val)
	}
}

func (o *Observable) RxSubscribe(fn Value) Value {
	o.mu.Lock()
	if !o.closed {
		o.listeners = append(o.listeners, fn)
	}
	o.mu.Unlock()
	// BehaviorSubject 语义: 立即用当前值回调一次
	if IsCallable(fn) {
		CallFunction(fn, nil, o.RxValue(), o.RxValue())
	}
	return o.makeUnsubscribe(fn)
}

// makeUnsubscribe 返回取消订阅函数 {close()}。
func (o *Observable) makeUnsubscribe(fn Value) *Object {
	sub := NewObject()
	sub.SetProperty("close", NewBuiltin("close", func(args ...Value) Value {
		o.mu.Lock()
		kept := o.listeners[:0]
		for _, l := range o.listeners {
			if l == fn {
				continue
			}
			kept = append(kept, l)
		}
		o.listeners = kept
		o.mu.Unlock()
		return UndefinedSingleton
	}))
	return sub
}

func (o *Observable) RxClose() {
	o.mu.Lock()
	o.closed = true
	o.listeners = nil
	o.mu.Unlock()
}

func argRx(args []Value, i int) Value {
	if i < len(args) {
		return args[i]
	}
	return UndefinedSingleton
}

// ===== ObservableList: RxList =====

// ObservableList 包装数组。读/写索引与变更方法都会通知。
type ObservableList struct {
	inner     *Array
	listeners []Value
	closed    bool
	mu        sync.Mutex
}

func NewObservableList(a *Array) *ObservableList {
	if a == nil {
		a = NewArray(nil)
	}
	return &ObservableList{inner: a}
}

func (l *ObservableList) Type() ObjectType { return OBSERVABLE_OBJ }
func (l *ObservableList) Inspect() string  { return "RxList(" + l.inner.Inspect() + ")" }
func (l *ObservableList) IsTruthy() bool   { return true }
func (l *ObservableList) rxSource()        {}

func (l *ObservableList) RxValue() Value { return l.inner }

func (l *ObservableList) GetProperty(name string) (Value, bool) {
	// 数字索引: 读取也参与依赖追踪
	if n, ok := nameIndex(name); ok {
		rxTouch(l)
		return l.inner.Elements[n], true
	}
	switch name {
	case "length":
		rxTouch(l)
		return NewInt(int64(len(l.inner.Elements))), true
	case "value":
		rxTouch(l)
		return l.inner, true
	case "listen":
		return NewBuiltin("listen", func(args ...Value) Value { return l.RxSubscribe(argRx(args, 0)) }), true
	case "close":
		return NewBuiltin("close", func(args ...Value) Value { l.RxClose(); return UndefinedSingleton }), true
	case "refresh":
		return NewBuiltin("refresh", func(args ...Value) Value { l.notify(l.inner, l.inner); return UndefinedSingleton }), true
	}
	// 变更方法: push/pop/shift/unshift/splice/remove/clear/reverse
	if m, ok := rxListMethod(l, name); ok {
		return m, true
	}
	// 其余方法 (join/map/filter/sort...) 绑定到内部数组后透传,
	// 使 this 检查通过内部数组而非包装对象
	if mv, found := l.inner.GetProperty(name); found && IsCallable(mv) {
		return NewBuiltin(name, func(args ...Value) Value {
			return CallFunction(mv, l.inner, args...)
		}), true
	}
	return nil, false
}

func (l *ObservableList) SetProperty(name string, val Value) {
	if n, ok := nameIndex(name); ok {
		l.SetIndexedElement(n, val)
	}
}

// GetIndexedElement / SetIndexedElement 实现 RxIndexed (VM 索引路径用)。
func (l *ObservableList) GetIndexedElement(i int) Value {
	rxTouch(l)
	l.mu.Lock()
	defer l.mu.Unlock()
	if i < 0 || i >= len(l.inner.Elements) {
		return UndefinedSingleton
	}
	return l.inner.Elements[i]
}

func (l *ObservableList) SetIndexedElement(i int, v Value) {
	l.mu.Lock()
	for len(l.inner.Elements) <= i {
		l.inner.Elements = append(l.inner.Elements, UndefinedSingleton)
	}
	old := l.inner.Elements[i]
	l.inner.Elements[i] = v
	l.mu.Unlock()
	l.notify(v, old)
}

// rxListMethod 构造触发通知的数组变更方法。
func rxListMethod(l *ObservableList, name string) (Value, bool) {
	// 变更后通知: 监听者收到整个数组 (GetX RxList 语义)
	done := func() Value {
		l.notify(l.inner, l.inner)
		return l.inner
	}
	switch name {
	case "push":
		return NewBuiltinMethod("push", func(this Value, args ...Value) Value {
			l.mu.Lock()
			l.inner.Elements = append(l.inner.Elements, args...)
			n := len(l.inner.Elements)
			l.mu.Unlock()
			l.notify(l.inner, l.inner)
			return NewInt(int64(n))
		}), true
	case "pop":
		return NewBuiltinMethod("pop", func(this Value, args ...Value) Value {
			l.mu.Lock()
			n := len(l.inner.Elements)
			var last Value = UndefinedSingleton
			if n > 0 {
				last = l.inner.Elements[n-1]
				l.inner.Elements = l.inner.Elements[:n-1]
			}
			l.mu.Unlock()
			l.notify(l.inner, l.inner)
			return last
		}), true
	case "shift":
		return NewBuiltinMethod("shift", func(this Value, args ...Value) Value {
			l.mu.Lock()
			n := len(l.inner.Elements)
			var first Value = UndefinedSingleton
			if n > 0 {
				first = l.inner.Elements[0]
				l.inner.Elements = l.inner.Elements[1:]
			}
			l.mu.Unlock()
			l.notify(l.inner, l.inner)
			return first
		}), true
	case "unshift":
		return NewBuiltinMethod("unshift", func(this Value, args ...Value) Value {
			l.mu.Lock()
			items := make([]Value, 0, len(args)+len(l.inner.Elements))
			items = append(items, args...)
			items = append(items, l.inner.Elements...)
			l.inner.Elements = items
			n := len(l.inner.Elements)
			l.mu.Unlock()
			l.notify(l.inner, l.inner)
			return NewInt(int64(n))
		}), true
	case "splice":
		return NewBuiltinMethod("splice", func(this Value, args ...Value) Value {
			// 借助内部数组方法实现, 再通知
			arr := l.inner
			_ = arr
			res := spliceObservable(l, args)
			l.notify(l.inner, l.inner)
			return res
		}), true
	case "remove":
		// GetX RxList.remove(element)
		return NewBuiltinMethod("remove", func(this Value, args ...Value) Value {
			l.mu.Lock()
			removed := false
			for i, e := range l.inner.Elements {
				if sameRxValue(e, argRx(args, 0)) {
					l.inner.Elements = append(l.inner.Elements[:i], l.inner.Elements[i+1:]...)
					removed = true
					break
				}
			}
			l.mu.Unlock()
			if removed {
				l.notify(l.inner, l.inner)
			}
			return NewBoolean(removed)
		}), true
	case "clear":
		return NewBuiltinMethod("clear", func(this Value, args ...Value) Value {
			l.mu.Lock()
			l.inner.Elements = nil
			l.mu.Unlock()
			return done()
		}), true
	case "reverse":
		return NewBuiltinMethod("reverse", func(this Value, args ...Value) Value {
			l.mu.Lock()
			for i, j := 0, len(l.inner.Elements)-1; i < j; i, j = i+1, j-1 {
				l.inner.Elements[i], l.inner.Elements[j] = l.inner.Elements[j], l.inner.Elements[i]
			}
			l.mu.Unlock()
			return done()
		}), true
	}
	return nil, false
}

// spliceObservable 委托内部数组的 splice 语义 (简单实现)。
func spliceObservable(l *ObservableList, args []Value) Value {
	arr := l.inner
	n := len(arr.Elements)
	start := 0
	if len(args) > 0 {
		s := int(toF64Value(args[0]))
		if s < 0 {
			s += n
		}
		if s < 0 {
			s = 0
		}
		if s > n {
			s = n
		}
		start = s
	}
	count := n - start
	if len(args) > 1 {
		count = int(toF64Value(args[1]))
		if count < 0 {
			count = 0
		}
		if count > n-start {
			count = n - start
		}
	}
	deleted := make([]Value, count)
	copy(deleted, arr.Elements[start:start+count])
	newEls := make([]Value, 0, n-count+len(args))
	newEls = append(newEls, arr.Elements[:start]...)
	newEls = append(newEls, args[2:]...)
	newEls = append(newEls, arr.Elements[start+count:]...)
	arr.Elements = newEls
	return NewArray(deleted)
}

func (l *ObservableList) notify(nv, ov Value) {
	l.mu.Lock()
	listeners := make([]Value, len(l.listeners))
	copy(listeners, l.listeners)
	closed := l.closed
	l.mu.Unlock()
	if !closed {
		notifyRxListeners(listeners, nv, ov)
	}
}

func (l *ObservableList) RxSubscribe(fn Value) Value {
	l.mu.Lock()
	if !l.closed {
		l.listeners = append(l.listeners, fn)
	}
	l.mu.Unlock()
	if IsCallable(fn) {
		CallFunction(fn, nil, l.inner, l.inner)
	}
	return l.makeSubClose(fn)
}

func (l *ObservableList) makeSubClose(fn Value) *Object {
	sub := NewObject()
	sub.SetProperty("close", NewBuiltin("close", func(args ...Value) Value {
		l.mu.Lock()
		kept := l.listeners[:0]
		for _, x := range l.listeners {
			if x != fn {
				kept = append(kept, x)
			}
		}
		l.listeners = kept
		l.mu.Unlock()
		return UndefinedSingleton
	}))
	return sub
}

func (l *ObservableList) RxClose() {
	l.mu.Lock()
	l.closed = true
	l.listeners = nil
	l.mu.Unlock()
}

// ===== ObservableMap: RxMap =====

// ObservableMap 包装 Map。set/delete/clear 通知。
type ObservableMap struct {
	inner     *Map
	listeners []Value
	closed    bool
	mu        sync.Mutex
}

func NewObservableMap(m *Map) *ObservableMap {
	if m == nil {
		m = NewMap()
	}
	return &ObservableMap{inner: m}
}

func (m *ObservableMap) Type() ObjectType { return OBSERVABLE_OBJ }
func (m *ObservableMap) Inspect() string  { return "RxMap(" + m.inner.Inspect() + ")" }
func (m *ObservableMap) IsTruthy() bool   { return true }
func (m *ObservableMap) rxSource()        {}

func (m *ObservableMap) RxValue() Value { return m.inner }

func (m *ObservableMap) GetProperty(name string) (Value, bool) {
	switch name {
	case "value":
		rxTouch(m)
		return m.inner, true
	case "listen":
		return NewBuiltin("listen", func(args ...Value) Value { return m.RxSubscribe(argRx(args, 0)) }), true
	case "close":
		return NewBuiltin("close", func(args ...Value) Value { m.RxClose(); return UndefinedSingleton }), true
	case "set":
		return NewBuiltinMethod("set", func(this Value, args ...Value) Value {
			if len(args) >= 2 {
				m.inner.Set(args[0], args[1])
				m.notify(m.inner, m.inner)
			}
			return m.inner
		}), true
	case "delete":
		return NewBuiltinMethod("delete", func(this Value, args ...Value) Value {
			ok := m.inner.Delete(argRx(args, 0))
			if ok {
				m.notify(m.inner, m.inner)
			}
			return NewBoolean(ok)
		}), true
	case "clear":
		return NewBuiltinMethod("clear", func(this Value, args ...Value) Value {
			m.inner.Clear()
			m.notify(m.inner, m.inner)
			return UndefinedSingleton
		}), true
	}
	// 其余方法 (get/has/keys/values/entries/forEach) 绑定到内部 Map 透传
	if mv, found := m.inner.GetProperty(name); found && IsCallable(mv) {
		return NewBuiltin(name, func(args ...Value) Value {
			return CallFunction(mv, m.inner, args...)
		}), true
	}
	return nil, false
}

func (m *ObservableMap) SetProperty(string, Value) {}

func (m *ObservableMap) notify(nv, ov Value) {
	m.mu.Lock()
	listeners := make([]Value, len(m.listeners))
	copy(listeners, m.listeners)
	closed := m.closed
	m.mu.Unlock()
	if !closed {
		notifyRxListeners(listeners, nv, ov)
	}
}

func (m *ObservableMap) RxSubscribe(fn Value) Value {
	m.mu.Lock()
	if !m.closed {
		m.listeners = append(m.listeners, fn)
	}
	m.mu.Unlock()
	if IsCallable(fn) {
		CallFunction(fn, nil, m.inner, m.inner)
	}
	sub := NewObject()
	sub.SetProperty("close", NewBuiltin("close", func(args ...Value) Value {
		m.mu.Lock()
		kept := m.listeners[:0]
		for _, x := range m.listeners {
			if x != fn {
				kept = append(kept, x)
			}
		}
		m.listeners = kept
		m.mu.Unlock()
		return UndefinedSingleton
	}))
	return sub
}

func (m *ObservableMap) RxClose() {
	m.mu.Lock()
	m.closed = true
	m.listeners = nil
	m.mu.Unlock()
}

// ===== Computed =====

// Computed 惰性计算属性: 求值期间追踪读取过的 Observable, 任一依赖
// 变化即失效并重算/通知。
type Computed struct {
	fn        Value
	cached    Value
	valid     bool
	listeners []Value
	closed    bool
	tracker   *rxTracker
	// wired 记录已布线的依赖, 防止重算时重复注册监听
	wired map[RxSource]bool
	mu    sync.Mutex
}

func NewComputed(fn Value) *Computed { return &Computed{fn: fn} }

func (c *Computed) Type() ObjectType { return OBSERVABLE_OBJ }
func (c *Computed) Inspect() string  { return "RxComputed<" + ToString(c.RxValue()) + ">" }
func (c *Computed) IsTruthy() bool   { return true }
func (c *Computed) rxSource()        {}

// recompute 在追踪器开启的情况下执行 fn, 并把新依赖布线到失效回调。
func (c *Computed) recompute() Value {
	t := &rxTracker{}
	rxPushTracker(t)
	ret := CallFunction(c.fn, UndefinedSingleton)
	if err := TakeCallbackError(); err != nil {
		rxPopTracker(t)
		return NewErrorWithName("Error", err.Error())
	}
	rxPopTracker(t)
	c.mu.Lock()
	c.tracker = t
	c.cached = ret
	c.valid = true
	c.mu.Unlock()
	c.wireDeps(t)
	return ret
}

// wireDeps 为新增依赖注册失效回调 (已布线的跳过)。
func (c *Computed) wireDeps(t *rxTracker) {
	t.mu.Lock()
	deps := make([]RxSource, 0, len(t.deps))
	for d := range t.deps {
		deps = append(deps, d)
	}
	t.mu.Unlock()
	for _, d := range deps {
		c.mu.Lock()
		already := c.wired[d]
		if !already {
			if c.wired == nil {
				c.wired = map[RxSource]bool{}
			}
			c.wired[d] = true
		}
		c.mu.Unlock()
		if already {
			continue
		}
		depFn := NewBuiltin("__computed_dep", func(args ...Value) Value {
			c.invalidate()
			return UndefinedSingleton
		})
		switch dep := d.(type) {
		case *Observable:
			dep.mu.Lock()
			dep.listeners = append(dep.listeners, depFn)
			dep.mu.Unlock()
		case *ObservableList:
			dep.mu.Lock()
			dep.listeners = append(dep.listeners, depFn)
			dep.mu.Unlock()
		case *ObservableMap:
			dep.mu.Lock()
			dep.listeners = append(dep.listeners, depFn)
			dep.mu.Unlock()
		case *Computed:
			dep.mu.Lock()
			dep.listeners = append(dep.listeners, depFn)
			dep.mu.Unlock()
		}
	}
}

func (c *Computed) RxValue() Value {
	c.mu.Lock()
	if c.valid {
		v := c.cached
		c.mu.Unlock()
		// 上层 computed 也可能依赖本 computed
		if t := rxCurrentTracker(); t != nil {
			t.track(c)
		}
		return v
	}
	c.mu.Unlock()
	v := c.recompute()
	if t := rxCurrentTracker(); t != nil {
		t.track(c)
	}
	return v
}

// invalidate 在依赖变化时触发: 重算并通知监听者。
func (c *Computed) invalidate() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	old := c.cached
	c.mu.Unlock()
	nv := c.recompute()
	if sameRxValue(old, nv) {
		return
	}
	c.mu.Lock()
	listeners := make([]Value, len(c.listeners))
	copy(listeners, c.listeners)
	c.mu.Unlock()
	notifyRxListeners(listeners, nv, old)
}

func (c *Computed) GetProperty(name string) (Value, bool) {
	switch name {
	case "value":
		return c.RxValue(), true
	case "listen":
		return NewBuiltin("listen", func(args ...Value) Value { return c.RxSubscribe(argRx(args, 0)) }), true
	case "close":
		return NewBuiltin("close", func(args ...Value) Value { c.RxClose(); return UndefinedSingleton }), true
	case "refresh":
		return NewBuiltin("refresh", func(args ...Value) Value {
			c.mu.Lock()
			c.valid = false
			c.mu.Unlock()
			c.invalidate()
			return UndefinedSingleton
		}), true
	}
	return nil, false
}

func (c *Computed) SetProperty(name string, val Value) {}

func (c *Computed) RxSubscribe(fn Value) Value {
	c.mu.Lock()
	if !c.closed {
		c.listeners = append(c.listeners, fn)
	}
	c.mu.Unlock()
	// 立即求值并以当前值回调
	v := c.RxValue()
	if IsCallable(fn) {
		CallFunction(fn, nil, v, v)
	}
	sub := NewObject()
	sub.SetProperty("close", NewBuiltin("close", func(args ...Value) Value {
		c.mu.Lock()
		kept := c.listeners[:0]
		for _, x := range c.listeners {
			if x != fn {
				kept = append(kept, x)
			}
		}
		c.listeners = kept
		c.mu.Unlock()
		return UndefinedSingleton
	}))
	return sub
}

func (c *Computed) RxClose() {
	c.mu.Lock()
	c.closed = true
	c.listeners = nil
	c.mu.Unlock()
}

// rxTouch 把当前活动追踪器登记为 src 的依赖 (读侧钩子)。
func rxTouch(src RxSource) {
	if t := rxCurrentTracker(); t != nil {
		t.track(src)
	}
}

// track 把 src 记入追踪器依赖 (deps 惰性初始化)。
func (t *rxTracker) track(src RxSource) {
	t.mu.Lock()
	if t.deps == nil {
		t.deps = map[RxSource]struct{}{}
	}
	t.deps[src] = struct{}{}
	t.mu.Unlock()
}

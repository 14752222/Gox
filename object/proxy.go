package object

// Proxy 表示 JavaScript 的 Proxy 对象。
//
// Proxy 包装一个目标 (target) 和一个处理器 (handler)，
// 当对 Proxy 进行属性访问、调用、构造等操作时，会转发到 handler
// 上对应的 trap 函数 (如 get, set, apply, construct)。
//
// 注意: 这里的 Proxy 类型只负责存储 Target 和 Handler 以及 trap 查找。
// 实际的 trap 转发逻辑在 vm 包中完成 (因为 trap 是 JS 闭包，需要 VM
// 来执行)。这保持了 object → vm 的单向依赖。
type Proxy struct {
	// Target 是被代理的目标对象
	Target Value
	// Handler 是包含 trap 函数的处理器对象
	Handler Value
	// IsRevoked 标记代理是否已被 revoke (简化，预留)
	IsRevoked bool
}

func (p *Proxy) Type() ObjectType { return PROXY_OBJ }
func (p *Proxy) Inspect() string  { return "[object Proxy]" }
func (p *Proxy) IsTruthy() bool   { return true }

// GetProperty / SetProperty 不在此处理。
// 属性访问必须经过 VM 的 trap 转发，因此这里返回未找到/静默忽略。
// VM 在检测到 *object.Proxy 时不会调用这两个方法，而是走 proxy 路径。
func (p *Proxy) GetProperty(name string) (Value, bool) {
	if p.IsRevoked {
		return nil, false
	}
	if trap := GetProxyTrap(p.Handler, "get"); trap != nil {
		// 经 get trap 转发 (this 绑定 handler)。trap 内异常通过
		// 回调错误桥留待调用方用 TakeCallbackError 拾取。
		r := CallFunction(trap, p.Handler, p.Target, NewString(name), p)
		if r == nil {
			return nil, false
		}
		return r, true
	}
	if p.Target != nil {
		return p.Target.GetProperty(name)
	}
	return nil, false
}
func (p *Proxy) SetProperty(name string, val Value) {
	if p.IsRevoked {
		return
	}
	if trap := GetProxyTrap(p.Handler, "set"); trap != nil {
		CallFunction(trap, p.Handler, p.Target, NewString(name), val, p)
		return
	}
	if p.Target != nil {
		p.Target.SetProperty(name, val)
	}
}

// NewProxy 创建代理对象。
// target 必须是对象或可调用函数，handler 必须是对象。
// 返回错误时，对应值为 nil。
func NewProxy(target, handler Value) (*Proxy, *Error) {
	// 目标必须是对象类引用类型 (对象/数组/函数/Map/Set/Proxy/RegExp 等)
	if !IsObjectLike(target) && !IsCallable(target) {
		return nil, NewTypeError("Cannot create proxy with a non-object as target or handler")
	}

	// handler 必须是对象
	if !IsObjectLike(handler) {
		return nil, NewTypeError("Cannot create proxy with a non-object as target or handler")
	}

	return &Proxy{Target: target, Handler: handler}, nil
}

// IsObjectLike 粗略判断一个值是否为"对象类"引用类型 (允许作为 handler)。
// IsArrayValue 实现规范的 IsArray (sec-isarray): 数组为真; Proxy 递归看
// target (revoked proxy 抛 TypeError); 其余一律假。
// Array.isArray / ArraySpeciesCreate 的 IsArray 步骤共用此实现, 避免两处
// 各写一份导致结论分叉。
func IsArrayValue(v Value) bool {
	switch a := v.(type) {
	case *Array:
		return true
	case *Proxy:
		if a.IsRevoked || a.Target == nil {
			panic(NewTypeError("Cannot perform 'IsArray' on a proxy that has been revoked"))
		}
		return IsArrayValue(a.Target)
	}
	return false
}

func IsObjectLike(v Value) bool {
	// 原始值包装对象 (new Boolean/Number/String/Symbol) 的 Type() 返回其
	// 包装的原始类型 (供 instanceof 判定), 但它们在语义上是对象 ——
	// 须在 Type() 分支之前拦下, 否则会被当成原始值。
	if _, ok := v.(*PrimitiveObject); ok {
		return true
	}
	switch v.Type() {
	case OBJECT_OBJ, ARRAY_OBJ, MAP_OBJ, SET_OBJ, ERROR_OBJ, ITERATOR_OBJ,
		CLOSURE_OBJ, BUILTIN_OBJ, PROXY_OBJ, REGEXP_OBJ, PROMISE_OBJ, DATE_OBJ,
		TEMPORAL_INSTANT_OBJ, TEMPORAL_PLAIN_DATETIME_OBJ, TEMPORAL_PLAIN_DATE_OBJ,
		TEMPORAL_PLAIN_TIME_OBJ, TEMPORAL_PLAIN_YM_OBJ, TEMPORAL_PLAIN_MD_OBJ,
		TEMPORAL_ZONED_DATETIME_OBJ, TEMPORAL_DURATION_OBJ, TEMPORAL_TIMEZONE_OBJ,
		TEMPORAL_CALENDAR_OBJ,
		GENERATOR_OBJ, ASYNC_GENERATOR_OBJ, GLOBAL_OBJ:
		// 注: WEAKREF_OBJ 刻意不在此列 —— WeakRef 实例无 Proto 字段, 加入后
		// Reflect.construct(newTarget≠WeakRef) 的结果回传路径会把无原型的
		// 内建实例当规范对象返回, 破坏 prototype-from-newtarget 族用例。
		return true
	}
	return false
}

// GetProxyTrap 从 handler 对象中查找 trap 函数。
// 如果 handler 不是对象或没有对应 trap，返回 nil。
// trap 名称如 "get", "set", "apply", "construct", "has" 等。
func GetProxyTrap(handler Value, trapName string) Value {
	obj, ok := handler.(*Object)
	if !ok {
		return nil
	}
	val, found := obj.GetProperty(trapName)
	if !found || val == nil {
		return nil
	}
	// trap 必须可调用
	if IsCallable(val) {
		return val
	}
	return nil
}

// IsProxy 判断值是否为代理。
func IsProxy(v Value) bool {
	_, ok := v.(*Proxy)
	return ok
}

// ProxyTrapNames 是所有支持的 trap 名称列表。
var ProxyTrapNames = []string{
	"get", "set", "has", "deleteProperty",
	"getPrototypeOf", "setPrototypeOf", "isExtensible",
	"preventExtensions", "ownKeys", "getOwnPropertyDescriptor",
	"defineProperty", "apply", "construct",
}

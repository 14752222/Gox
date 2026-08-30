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
func (p *Proxy) GetProperty(name string) (Value, bool) { return nil, false }
func (p *Proxy) SetProperty(name string, val Value)    {}

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
func IsObjectLike(v Value) bool {
	switch v.Type() {
	case OBJECT_OBJ, ARRAY_OBJ, MAP_OBJ, SET_OBJ, ERROR_OBJ, ITERATOR_OBJ,
		CLOSURE_OBJ, BUILTIN_OBJ, PROXY_OBJ, REGEXP_OBJ, PROMISE_OBJ:
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

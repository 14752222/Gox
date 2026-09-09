package vm

import (
	"testing"

	"js-runtime/object"
)

// ===== Proxy get trap =====

func TestProxyGetTrap(t *testing.T) {
	assertString(t, evalJS(t, `
		let target = { a: 1, b: 2 };
		let handler = {
			get(t, k, r) {
				if (k === 'a') { return 'trapped'; }
				return Reflect.get(t, k, r);
			}
		};
		let p = new Proxy(target, handler);
		p.a;
	`), "trapped")
}

func TestProxyGetDefault(t *testing.T) {
	assertNumber(t, evalJS(t, `
		let p = new Proxy({ a: 1, b: 2 }, {});
		p.b;
	`), 2)
}

func TestProxyGetUndefined(t *testing.T) {
	// 无 trap 且目标无该属性 → undefined
	res := evalJS(t, `
		let p = new Proxy({}, {});
		p.missing;
	`)
	if _, ok := res.(*object.Undefined); !ok {
		t.Fatalf("expected undefined, got %T (%s)", res, res.Inspect())
	}
}

func TestProxyGetMethodCall(t *testing.T) {
	// 代理对象的方法调用: 获取函数属性后调用
	assertString(t, evalJS(t, `
		let target = { greet() { return "hello " + this.name; }, name: "js" };
		let p = new Proxy(target, {});
		p.greet();
	`), "hello js")
}

// ===== Proxy set trap =====

func TestProxySetTrap(t *testing.T) {
	// set trap 被调用，目标被写入
	assertBoolean(t, evalJS(t, `
		let target = { a: 1 };
		let handler = {
			set(t, k, v, r) {
				Reflect.set(t, k, v, r);
				Reflect.set(t, '_set', true, r);
				return true;
			}
		};
		let p = new Proxy(target, handler);
		p.a = 42;
		p._set === true;
	`), true)
}

func TestProxySetWritesTarget(t *testing.T) {
	assertNumber(t, evalJS(t, `
		let target = { a: 1 };
		let p = new Proxy(target, {});
		p.a = 99;
		target.a;
	`), 99)
}

func TestProxySetTrapIntercept(t *testing.T) {
	// set trap 将写入值乘以 10
	assertNumber(t, evalJS(t, `
		let target = { a: 1 };
		let handler = {
			set(t, k, v, r) {
				if (k === 'a') {
					Reflect.set(t, k, v * 10, r);
					return true;
				}
				return false;
			}
		};		let p = new Proxy(target, handler);
		p.a = 5;
		target.a;
	`), 50)
}

// ===== Proxy apply trap =====

func TestProxyApplyTrap(t *testing.T) {
	// apply trap 将函数结果乘以 2
	assertNumber(t, evalJS(t, `
		function add(x, y) { return x + y; }
		let handler = {
			apply(t, thisArg, args) {
				return Reflect.apply(t, thisArg, args) * 2;
			}
		};
		let p = new Proxy(add, handler);
		p(3, 4);
	`), 14)
}

func TestProxyApplyNoTrap(t *testing.T) {
	assertNumber(t, evalJS(t, `
		function add(x, y) { return x + y; }
		let p = new Proxy(add, {});
		p(3, 4);
	`), 7)
}

// ===== Proxy construct trap =====

func TestProxyConstructTrap(t *testing.T) {
	assertString(t, evalJS(t, `
		function Point(x, y) { this.x = x; this.y = y; }
		let handler = {
			construct(t, args) {
				let obj = new t(args[0], args[1]);
				obj.constructed = true;
				return obj;
			}
		};
		let P = new Proxy(Point, handler);
		let pt = new P(3, 4);
		pt.constructed + "|" + pt.x + "," + pt.y;
	`), "true|3,4")
}

func TestProxyConstructNoTrap(t *testing.T) {
	assertNumber(t, evalJS(t, `
		function Point(x) { this.x = x; }
		let P = new Proxy(Point, {});
		let pt = new P(7);
		pt.x;
	`), 7)
}

// ===== Proxy on object with dynamic handler =====

func TestProxyIndexedGet(t *testing.T) {
	// 通过 get trap 拦截任意属性读取
	assertString(t, evalJS(t, `
		let target = {};
		let handler = {
			get(t, k, r) { return "key:" + k; }
		};
		let p = new Proxy(target, handler);
		p["foo"];
	`), "key:foo")
}

// ===== Reflect tests =====

func TestReflectGet(t *testing.T) {
	assertNumber(t, evalJS(t, `Reflect.get({ a: 5 }, "a")`), 5)
}

func TestReflectGetMissing(t *testing.T) {
	res := evalJS(t, `Reflect.get({}, "nope")`)
	if _, ok := res.(*object.Undefined); !ok {
		t.Fatalf("expected undefined, got %T (%s)", res, res.Inspect())
	}
}

func TestReflectSet(t *testing.T) {
	assertBoolean(t, evalJS(t, `
		let o = {};
		let ok = Reflect.set(o, "x", 10);
		ok && o.x === 10;
	`), true)
}

func TestReflectHas(t *testing.T) {
	assertBoolean(t, evalJS(t, `
		let o = { a: 1 };
		Reflect.has(o, "a") && !Reflect.has(o, "b");
	`), true)
}

func TestReflectOwnKeys(t *testing.T) {
	// ownKeys 遵循 ECMAScript OrdinaryOwnPropertyKeys: 插入顺序
	assertString(t, evalJS(t, `
		let o = { b: 1, a: 2, c: 3 };
		let keys = Reflect.ownKeys(o);
		keys.join(",");
	`), "b,a,c")
}

func TestReflectApply(t *testing.T) {
	assertNumber(t, evalJS(t, `
		function add(a, b) { return a + b; }
		Reflect.apply(add, null, [10, 20]);
	`), 30)
}

func TestReflectGetPrototypeOf(t *testing.T) {
	// 普通对象原型为 null (无原型链设置)
	res := evalJS(t, `
		let o = { a: 1 };
		let proto = Reflect.getPrototypeOf(o);
		proto === null || proto === undefined;
	`)
	assertBoolean(t, res, true)
}

func TestReflectDeleteProperty(t *testing.T) {
	assertBoolean(t, evalJS(t, `
		let o = { a: 1, b: 2 };
		Reflect.deleteProperty(o, "a");
		Reflect.has(o, "a");
	`), false)
}

// ===== Proxy.revocable =====

func TestProxyRevocable(t *testing.T) {
	// revoke 后操作抛 TypeError
	_, err := Eval(`
		let { proxy, revoke } = Proxy.revocable({ a: 1 }, {});
		revoke();
		proxy.a;
	`)
	if err == nil {
		t.Fatalf("expected TypeError after revoke, got no error")
	}
}

package vm

import "testing"

// ===== ES2022 静态初始化块 static { ... } 的运行语义（看板单 rJ56bZ）=====
//
// 用 evalJS（完整管线, 含 stdlib）—— 断言里用到 Array.push / Error 等宿主对象。

// TestStaticBlockThisIsConstructor 块内 this = 构造器, 且显式赋值生效。
func TestStaticBlockThisIsConstructor(t *testing.T) {
	assertNumber(t, evalJS(t, `class C { static { this.z = 7 } } C.z`), 7)
	assertBoolean(t, evalJS(t, `var v; class E { static { v = this; } } v === E`), true)
}

// TestStaticBlockOrderWithFields 与静态字段按定义顺序交错求值。
func TestStaticBlockOrderWithFields(t *testing.T) {
	assertString(t, evalJS(t, `
		var seq = [];
		class D {
			static x = seq.push('f1');
			static { seq.push('b1'); }
			static y = seq.push('f2');
			static { seq.push('b2'); }
		}
		seq.join(',')
	`), "f1,b1,f2,b2")
}

// TestStaticBlockVarScopeIsolation 每个块有自己的作用域: var 不跨块、不泄漏。
func TestStaticBlockVarScopeIsolation(t *testing.T) {
	assertString(t, evalJS(t, `
		var t = 'outer'; var p1, p2;
		class F {
			static { var t = 'first'; p1 = t; }
			static { var t = 'second'; p2 = t; }
		}
		t + '|' + p1 + '|' + p2
	`), "outer|first|second")

	// 块内 let 不泄漏到下一个块。
	assertString(t, evalJS(t, `
		var t = 'outer'; var p;
		class G {
			static { let t = 'inner'; }
			static { p = t; }
		}
		p
	`), "outer")
}

// TestStaticBlockAbrupt 块内异常中断类定义并向外抛。
func TestStaticBlockAbrupt(t *testing.T) {
	assertBoolean(t, evalJS(t, `
		var thrown = new Error('boom'); var caught; var after = false;
		try {
			class H {
				static { throw thrown; }
				static x = (after = true);
			}
		} catch (e) { caught = e; }
		(caught === thrown) && (after === false)
	`), true)
}

// TestStaticBlockPrivateAndClassName 块内可见私有名与类名绑定。
func TestStaticBlockPrivateAndClassName(t *testing.T) {
	assertNumber(t, evalJS(t, `
		class S { static #p = 41; static { this.q = this.#p + 1; } }
		S.q
	`), 42)
	assertBoolean(t, evalJS(t, `var nv; class U { static { nv = U; } } nv === U`), true)
}

// TestStaticBlockEmptyThenField 空块是合法的, 不影响后续静态字段。
func TestStaticBlockEmptyThenField(t *testing.T) {
	assertNumber(t, evalJS(t, `class W { static {} static ok = 1; } W.ok`), 1)
}

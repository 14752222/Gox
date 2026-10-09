package object

import "testing"

// ===== Promise 一等对象: 自有属性存储 (看板 rj9MwH) =====
//
// 此前 *Promise 不是一等对象: GetProperty 只认 then/catch/finally 三个键,
// SetProperty 是空函数, 也没有任何自有属性存储。于是
// `Object.defineProperty(p, 'constructor', { get(){ throw … } })` 落不下去
// 也取不到 —— 规范里"取 constructor 抛错 ⇒ 以该值 reject"这条路径在
// Gox 里根本不存在 (built-ins/AsyncGeneratorPrototype/return/*broken-promise*
// 3 例)。
//
// 本文件钉住落点本身的机制 (描述符存取 / 键序 / 赋值 / 删除); getter 的
// **触发**依赖 CallFunction (由 vm 注册回调), 故放在 vm 包的端到端用例里。

// TestPromiseOwnDescriptorRoundTrip 钉住 DefineOwn → OwnDescriptor / HasOwn /
// GetProperty 的往返, 以及缺省描述符的 w/e/c 三标志。
func TestPromiseOwnDescriptorRoundTrip(t *testing.T) {
	p := NewPromise()
	if p.HasOwn("x") {
		t.Fatal("新 promise 不应有自有属性 x")
	}
	p.DefineOwn("x", DataProperty(&String{Value: "v"}))

	if !p.HasOwn("x") {
		t.Fatal("DefineOwn 之后 HasOwn(x) 应为真")
	}
	d, ok := p.OwnDescriptor("x")
	if !ok {
		t.Fatal("OwnDescriptor(x) 应命中")
	}
	if !d.Writable || !d.Enumerable || !d.Configurable {
		t.Fatalf("DataProperty 缺省应 w/e/c 全真, 实得 %+v", d)
	}
	got, ok := p.GetProperty("x")
	if !ok {
		t.Fatal("GetProperty(x) 应命中自有属性")
	}
	s, isStr := got.(*String)
	if !isStr || s.Value != "v" {
		t.Fatalf("GetProperty(x) 期望 \"v\", 实得 %v", got)
	}
}

// TestPromiseOwnKeysKeepsInsertionOrder 钉住键序与可枚举口径:
// OwnKeys 按插入序且不漏不可枚举键, EnumerableOwnKeys 只给可枚举的
// (Object.keys / JSON.stringify 的事实来源)。
func TestPromiseOwnKeysKeepsInsertionOrder(t *testing.T) {
	p := NewPromise()
	p.DefineOwn("a", DataProperty(&Number{Value: 1}))
	p.DefineOwn("b", PropertyDescriptor{
		Value: &Number{Value: 2}, Writable: true, Enumerable: false, Configurable: true,
	})
	p.DefineOwn("c", DataProperty(&Number{Value: 3}))

	if got := p.OwnKeys(); len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Fatalf("OwnKeys 期望 [a b c], 实得 %v", got)
	}
	enum := p.EnumerableOwnKeys()
	if len(enum) != 2 || enum[0] != "a" || enum[1] != "c" {
		t.Fatalf("EnumerableOwnKeys 期望 [a c], 实得 %v", enum)
	}
}

// TestPromiseSetPropertyDataAndNonWritable 钉住赋值口径 (与 *RegExp 同款):
// 新键落成普通数据属性, 不可写数据属性静默失败。
func TestPromiseSetPropertyDataAndNonWritable(t *testing.T) {
	p := NewPromise()

	// 此前 SetProperty 是 no-op: 赋值被静默丢弃。
	p.SetProperty("foo", &Number{Value: 1})
	v, ok := p.GetProperty("foo")
	if !ok {
		t.Fatal("赋值后应能读回 foo")
	}
	if n, isNum := v.(*Number); !isNum || n.Value != 1 {
		t.Fatalf("foo 期望 1, 实得 %v", v)
	}
	p.SetProperty("foo", &Number{Value: 2})
	if v, _ = p.GetProperty("foo"); v.(*Number).Value != 2 {
		t.Fatalf("foo 二次赋值期望 2, 实得 %v", v)
	}

	p.DefineOwn("ro", PropertyDescriptor{
		Value: &Number{Value: 9}, Writable: false, Enumerable: true, Configurable: true,
	})
	p.SetProperty("ro", &Number{Value: 10})
	if v, _ = p.GetProperty("ro"); v.(*Number).Value != 9 {
		t.Fatalf("不可写属性应静默保持 9, 实得 %v", v)
	}
}

// TestPromiseDeleteOwnRespectsConfigurable 钉住删除口径: 可配置键删除后彻底
// 消失 (OwnKeys / HasOwn 都不再看得见), 不可配置键删不掉。
func TestPromiseDeleteOwnRespectsConfigurable(t *testing.T) {
	p := NewPromise()
	p.DefineOwn("del", DataProperty(&Number{Value: 1}))
	p.DefineOwn("keep", PropertyDescriptor{
		Value: &Number{Value: 2}, Writable: true, Enumerable: true, Configurable: false,
	})
	// 不存在的键: 与 *RegExp.DeleteOwn 同口径, 视为成功。
	if !p.DeleteOwn("nope") {
		t.Fatal("删除不存在的键应返回 true")
	}

	if !p.DeleteOwn("del") {
		t.Fatal("可配置键应能删除")
	}
	if p.HasOwn("del") {
		t.Fatal("删除后 HasOwn(del) 应为假")
	}
	for _, k := range p.OwnKeys() {
		if k == "del" {
			t.Fatalf("删除后 OwnKeys 不该再含 del: %v", p.OwnKeys())
		}
	}

	if p.DeleteOwn("keep") {
		t.Fatal("不可配置键应删不掉")
	}
	if !p.HasOwn("keep") {
		t.Fatal("不可配置键应仍在")
	}
}

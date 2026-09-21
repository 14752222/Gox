package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== 匹配层纯函数单测 (不需要 VM) =====
//
// 这一层是 gx/router 最容易被写错、也最容易脱离 VM 验证的部分:
// 静态 vs 参数的优先级、可选段的缺省语义、通配的吞并范围、嵌套路径拼接。
// 表驱动逐条钉住, 免得这些规则只在"某个 demo 恰好这么写"时才被测到。

// routeObj 造一条路由记录对象 (只给需要的字段)。
func routeObj(kv ...interface{}) *object.Object {
	o := object.NewObject()
	for i := 0; i+1 < len(kv); i += 2 {
		key := kv[i].(string)
		switch v := kv[i+1].(type) {
		case string:
			o.SetProperty(key, object.NewString(v))
		case bool:
			o.SetProperty(key, object.NewBoolean(v))
		case []object.Value:
			o.SetProperty(key, object.NewArray(v))
		case object.Value:
			o.SetProperty(key, v)
		}
	}
	return o
}

func routeTableOf(t *testing.T, recs ...*object.Object) *routeTable {
	t.Helper()
	vals := make([]object.Value, 0, len(recs))
	for _, r := range recs {
		vals = append(vals, r)
	}
	return routeCompile(object.NewArray(vals))
}

func TestRouteMatchBasic(t *testing.T) {
	noop := object.NewBuiltin("noop", func(args ...object.Value) object.Value {
		return object.UndefinedSingleton
	})
	tbl := routeTableOf(t,
		routeObj("path", "/", "name", "home", "component", noop),
		routeObj("path", "/items", "name", "items", "component", noop),
		routeObj("path", "/items/new", "name", "new", "component", noop),
		routeObj("path", "/items/:id", "name", "detail", "component", noop),
		routeObj("path", "/items/:id/edit", "name", "edit", "component", noop),
		routeObj("path", "/about/:tab?", "name", "about", "component", noop),
		routeObj("path", "/files/*", "name", "files", "component", noop),
		routeObj("path", "/raw/*rest", "name", "raw", "component", noop),
		routeObj("path", "*", "name", "nf", "component", noop),
	)
	if len(tbl.pending) != 0 {
		t.Fatalf("编译期不该有问题: %v", tbl.pending)
	}

	cases := []struct {
		path   string
		name   string
		params map[string]string
	}{
		{"/", "home", map[string]string{}},
		{"/items", "items", map[string]string{}},
		{"/items/", "items", map[string]string{}},  // 尾斜杠宽容
		{"/items/new", "new", map[string]string{}}, // 静态优先于 :id
		{"/items/42", "detail", map[string]string{"id": "42"}},
		{"/items/42/edit", "edit", map[string]string{"id": "42"}},
		{"/about", "about", map[string]string{}}, // 可选段缺省 → 参数不出现
		{"/about/faq", "about", map[string]string{"tab": "faq"}},
		{"/files", "files", map[string]string{"pathMatch": ""}}, // 通配可以吞空
		{"/files/a/b.txt", "files", map[string]string{"pathMatch": "a/b.txt"}},
		{"/raw/a/b", "raw", map[string]string{"rest": "a/b"}},
		{"/nothing/here", "nf", map[string]string{"pathMatch": "nothing/here"}},
	}
	for _, c := range cases {
		rec, params := tbl.routeFind(c.path)
		if rec == nil {
			t.Fatalf("%s: 没有匹配到任何记录", c.path)
		}
		if rec.name != c.name {
			t.Fatalf("%s: 命中 %q, 期望 %q", c.path, rec.name, c.name)
		}
		if len(params) != len(c.params) {
			t.Fatalf("%s: params = %v, 期望 %v", c.path, params, c.params)
		}
		for k, v := range c.params {
			if params[k] != v {
				t.Fatalf("%s: params[%s] = %q, 期望 %q", c.path, k, params[k], v)
			}
		}
	}
}

// TestRouteMatchStaticBeatsParamEvenIfDeclaredLater 钉住"优先级不依赖声明序":
// 通配写在最前也遮不住具体路径 —— 这是 routeScore 存在的唯一理由。
func TestRouteMatchStaticBeatsParamEvenIfDeclaredLater(t *testing.T) {
	noop := object.NewBuiltin("noop", func(args ...object.Value) object.Value {
		return object.UndefinedSingleton
	})
	tbl := routeTableOf(t,
		routeObj("path", "*", "name", "nf", "component", noop),
		routeObj("path", "/items/:id", "name", "detail", "component", noop),
		routeObj("path", "/items/new", "name", "new", "component", noop),
		routeObj("path", "/", "name", "home", "component", noop),
	)
	for _, c := range []struct{ path, name string }{
		{"/", "home"},
		{"/items/new", "new"},
		{"/items/7", "detail"},
		{"/zzz", "nf"},
	} {
		rec, _ := tbl.routeFind(c.path)
		if rec == nil || rec.name != c.name {
			got := "<nil>"
			if rec != nil {
				got = rec.name
			}
			t.Fatalf("%s: 命中 %q, 期望 %q", c.path, got, c.name)
		}
	}
}

// TestRouteNestedCompile 验证嵌套路由: 子路径拼接、depth、matched 链。
func TestRouteNestedCompile(t *testing.T) {
	noop := object.NewBuiltin("noop", func(args ...object.Value) object.Value {
		return object.UndefinedSingleton
	})
	child := routeObj("path", ":id", "name", "user", "component", noop)
	group := routeObj("path", "/users", "component", noop,
		"children", []object.Value{child})
	tbl := routeTableOf(t, group)
	if len(tbl.pending) != 0 {
		t.Fatalf("编译期不该有问题: %v", tbl.pending)
	}
	rec, params := tbl.routeFind("/users/42")
	if rec == nil || rec.name != "user" {
		t.Fatalf("嵌套子路由未命中: %+v", rec)
	}
	if rec.path != "/users/:id" {
		t.Fatalf("子路径未拼接: %q", rec.path)
	}
	if rec.depth != 1 {
		t.Fatalf("depth = %d, 期望 1", rec.depth)
	}
	if params["id"] != "42" {
		t.Fatalf("params = %v", params)
	}
	chain := routeRecordMatchChain(rec)
	if len(chain) != 2 || chain[0].path != "/users" || chain[1].path != "/users/:id" {
		t.Fatalf("matched 链不对: %+v", chain)
	}
	// 父记录自己有 component (布局路由) ⇒ 它本身也可被匹配 (/users 进它的布局)
	if rec, _ := tbl.routeFind("/users"); rec == nil || rec.path != "/users" {
		t.Fatalf("/users 应命中布局父记录: %+v", rec)
	}

	// 只有 children、没有 component/redirect 的记录是"纯分组": 不该进匹配表,
	// 否则会命中一条渲染不出任何内容的空记录。
	groupOnly := routeTableOf(t, routeObj("path", "/admin",
		"children", []object.Value{routeObj("path", "panel", "name", "panel", "component", noop)}))
	if len(groupOnly.records) != 1 {
		t.Fatalf("纯分组记录不该进匹配表, records=%d", len(groupOnly.records))
	}
	if rec, _ := groupOnly.routeFind("/admin/panel"); rec == nil || rec.name != "panel" {
		t.Fatalf("分组下的子路由应可匹配: %+v", rec)
	}
}

// TestRouteBuildPath 验证 resolve({name, params}) 的反向拼路径。
func TestRouteBuildPath(t *testing.T) {
	noop := object.NewBuiltin("noop", func(args ...object.Value) object.Value {
		return object.UndefinedSingleton
	})
	tbl := routeTableOf(t,
		routeObj("path", "/", "name", "home", "component", noop),
		routeObj("path", "/items/:id", "name", "detail", "component", noop),
		routeObj("path", "/about/:tab?", "name", "about", "component", noop),
		routeObj("path", "/files/*rest", "name", "files", "component", noop),
	)
	cases := []struct {
		name   string
		params map[string]string
		want   string
		wantOK bool
	}{
		{"home", map[string]string{}, "/", true},
		{"detail", map[string]string{"id": "42"}, "/items/42", true},
		{"detail", map[string]string{}, "", false}, // 必填参数缺失 → 明确失败
		{"about", map[string]string{}, "/about", true},
		{"about", map[string]string{"tab": "faq"}, "/about/faq", true},
		{"files", map[string]string{"rest": "a/b"}, "/files/a/b", true},
		{"files", map[string]string{}, "/files", true},
	}
	for _, c := range cases {
		rec := tbl.byName[c.name]
		if rec == nil {
			t.Fatalf("名字 %q 不在表里", c.name)
		}
		got, ok := routeBuildPath(rec, c.params)
		if ok != c.wantOK || got != c.want {
			t.Fatalf("buildPath(%s, %v) = (%q,%v), 期望 (%q,%v)", c.name, c.params, got, ok, c.want, c.wantOK)
		}
	}
}

// TestRouteTargetSplit 验证 "/items/42?tab=x#top" 的三段拆分与回拼。
func TestRouteTargetSplit(t *testing.T) {
	path, query, hash := routeSplitTarget("/items/42?tab=x&q=1#top")
	if path != "/items/42" || query != "tab=x&q=1" || hash != "top" {
		t.Fatalf("拆分结果: %q %q %q", path, query, hash)
	}
	m := routeParseQuery(query)
	if m["tab"] != "x" || m["q"] != "1" {
		t.Fatalf("查询串解析: %v", m)
	}
	// 键序稳定 ⇒ 同一份 query 永远拼出同一个 fullPath (可断言)
	if got := routeBuildQuery(map[string]string{"b": "2", "a": "1"}); got != "a=1&b=2" {
		t.Fatalf("查询串回拼: %q", got)
	}
	if got := routeJoinFullPath(path, "a=1", "top"); got != "/items/42?a=1#top" {
		t.Fatalf("fullPath 回拼: %q", got)
	}
}

// TestRouteCompileTolerance 验证写错路由表时的容错: 跳过失配项并记账, 不 panic。
func TestRouteCompileTolerance(t *testing.T) {
	noop := object.NewBuiltin("noop", func(args ...object.Value) object.Value {
		return object.UndefinedSingleton
	})
	tbl := routeTableOf(t,
		routeObj("name", "nopath", "component", noop),            // 缺 path
		routeObj("path", "/dup", "name", "d", "component", noop), // 重名 (第一条胜)
		routeObj("path", "/dup2", "name", "d", "component", noop),
		routeObj("path", "/opt/:x?/tail", "name", "opt", "component", noop), // 可选段不在末尾
	)
	if len(tbl.records) != 3 {
		t.Fatalf("缺 path 的记录应被跳过, records=%d", len(tbl.records))
	}
	if len(tbl.pending) != 3 {
		t.Fatalf("应有三条问题记录 (缺 path + 可选段位置 + 重名), 实际 %v", tbl.pending)
	}
	if rec := tbl.byName["d"]; rec == nil || rec.path != "/dup" {
		t.Fatalf("重名时应保留先声明的那条: %+v", rec)
	}
	// 中间可选段被降级为必填 (记一条 pending), 记录本身仍然可用
	rec := tbl.byName["opt"]
	if rec == nil {
		t.Fatalf("可选段写法有问题的记录不该消失, 只是降级")
	}
	if rec.segments[1].kind != routeSegParam {
		t.Fatalf("中间的可选段应降级为必填, kind=%d", rec.segments[1].kind)
	}
}

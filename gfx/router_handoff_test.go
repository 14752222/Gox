package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== M8 应用接续 (router.handoff) 测试 =====
//
// 核心断言三条:
//   1. 搬迁: 目标拿到源整条栈 (含 route.state), 源回到栈底/首页;
//   2. keepSource: 源不动, 目标拿克隆 (状态袋断开);
//   3. continuity: 只读内省能看出谁持有哪条栈。

// handoffRouter 造一个三路由 (/, /a, /b) 的 router (不经过 VM)。
func handoffRouter(t *testing.T) *router {
	t.Helper()
	resetRouterStateForTest()
	t.Cleanup(resetRouterStateForTest)

	routes := object.NewArray([]object.Value{
		routeObj("path", "/", "name", "home"),
		routeObj("path", "/a", "name", "a"),
		routeObj("path", "/b", "name", "b"),
	})
	return &router{
		table:       routeCompile(routes),
		sessions:    map[string]*routerSession{},
		backKeys:    false,
		foldDual:    true,
		initialPath: "/",
	}
}

// seedStack 在 scope 上造一条两级栈 / → /a → /b, 并给 /b 的 state 袋塞一个键。
func seedStack(t *testing.T, r *router, scope string) (*routerSession, *routeEntry) {
	t.Helper()
	s := r.sessionFor(scope)
	ea, err := r.resolveTo(object.NewString("/a"), nil)
	if err != nil {
		t.Fatalf("resolve /a: %v", err)
	}
	eb, err := r.resolveTo(object.NewString("/b"), nil)
	if err != nil {
		t.Fatalf("resolve /b: %v", err)
	}
	// 状态袋挂在**当前项** (/b, 也就是 eb) 上 —— 页面用 useRouteState() 读的
	// 是当前栈项的 state, 所以接续断言看的是它。早期版本误写到 ea (/a),
	// 于是"目标 draft 读不到"看起来像搬迁丢了状态, 实为种子放错了项。
	eb.stateObject().SetProperty("draft", object.NewString("hello"))
	s.stack = append(s.stack, ea, eb)
	s.index = 2
	s.commitSignal()
	return s, eb
}

// TestHandoffMovesStackWithState 验证搬迁语义: 栈与 route.state 完整到达目标,
// 源复位到首页。
func TestHandoffMovesStackWithState(t *testing.T) {
	r := handoffRouter(t)
	sa, eb := seedStack(t, r, "win:1")

	p := r.jsHandoff(object.NewString("win:1"), object.NewString("win:2"))
	if p == nil {
		t.Fatalf("handoff 应返回 Promise")
	}
	sb, ok := r.sessions["win:2"]
	if !ok {
		t.Fatalf("handoff 后目标作用域没有会话")
	}

	// 目标: 整条栈搬过来, 当前项与 state 都在。
	if len(sb.stack) != 3 || sb.index != 2 {
		t.Fatalf("目标栈 = depth %d index %d, want 3/2", len(sb.stack), sb.index)
	}
	if sb.current() != eb {
		t.Fatalf("搬迁应复用栈项指针 (route.state 随指针走), 实际是新对象")
	}
	if sb.current().path != "/b" {
		t.Fatalf("目标当前路径 = %q, want /b", sb.current().path)
	}
	if got := stateStr(t, sb.current(), "draft"); got != "hello" {
		t.Fatalf("目标未拿到 route.state: draft=%q", got)
	}
	// 栈形状
	if sb.stack[0].path != "/" || sb.stack[1].path != "/a" || sb.stack[2].path != "/b" {
		t.Fatalf("目标栈路径 = %q/%q/%q", sb.stack[0].path, sb.stack[1].path, sb.stack[2].path)
	}

	// 源: 复位到栈底/首页。
	if len(sa.stack) != 1 || sa.index != 0 {
		t.Fatalf("源应复位为 depth 1 index 0, 实际 %d/%d", len(sa.stack), sa.index)
	}
	if sa.current().path != "/" {
		t.Fatalf("源当前路径 = %q, want / (首页)", sa.current().path)
	}
}

// TestHandoffKeepSource 验证 keepSource: 源不动, 目标是**克隆** (状态袋断开)。
func TestHandoffKeepSource(t *testing.T) {
	r := handoffRouter(t)
	sa, eb := seedStack(t, r, "win:1")

	opts := object.NewObject()
	opts.SetProperty("keepSource", object.NewBoolean(true))
	r.jsHandoff(object.NewString("win:1"), object.NewString("win:2"), opts)

	sb := r.sessions["win:2"]
	// 源完全不动
	if len(sa.stack) != 3 || sa.index != 2 {
		t.Fatalf("keepSource 下源不应变, 实际 %d/%d", len(sa.stack), sa.index)
	}
	// 目标是克隆: 栈项不是同一个指针
	if len(sb.stack) != 3 || sb.current() == eb {
		t.Fatalf("keepSource 下目标应是克隆 (不同指针), 实际 len=%d same=%v",
			len(sb.stack), sb.current() == eb)
	}
	// 状态袋断开: 改目标不影响源
	sb.current().stateObject().SetProperty("draft", object.NewString("changed"))
	if got := stateStr(t, eb, "draft"); got != "hello" {
		t.Fatalf("keepSource 克隆的状态袋与源共享了: 源 draft=%q", got)
	}
	if got := stateStr(t, sb.current(), "draft"); got != "changed" {
		t.Fatalf("目标状态袋改不动: draft=%q", got)
	}
}

// TestHandoffOddCases 验证同作用域 / 空源 / 非法参数不崩。
func TestHandoffOddCases(t *testing.T) {
	r := handoffRouter(t)
	seedStack(t, r, "win:1")

	// 同作用域: 不报错, 源不变
	r.jsHandoff(object.NewString("win:1"), object.NewString("win:1"))
	if len(r.sessions["win:1"].stack) != 3 {
		t.Fatalf("同作用域 handoff 不该改栈")
	}
	// 空源: 不崩
	if p := r.jsHandoff(object.NewString("nope"), object.NewString("win:9")); p == nil {
		t.Fatalf("空源应返回 Promise (reason=empty-source)")
	}
	// 非法参数: TypeError
	res := r.jsHandoff(object.NewNumber(1), object.NewNumber(2))
	if _, ok := res.(*object.Error); !ok {
		t.Fatalf("非法 from/to 应返回 TypeError, 实际 %T", res)
	}
}

// TestContinuityIntrospection 验证 continuity() 只读内省。
func TestContinuityIntrospection(t *testing.T) {
	r := handoffRouter(t)
	seedStack(t, r, "win:1")
	r.jsHandoff(object.NewString("win:1"), object.NewString("win:2"))

	arr, ok := r.jsContinuity().(*object.Array)
	if !ok || len(arr.Elements) != 2 {
		t.Fatalf("continuity() 应有 2 条会话, 实际 %v", arr)
	}
	var target *object.Object
	for _, e := range arr.Elements {
		o := e.(*object.Object)
		if objPropStr(o, "scope") == "win:2" {
			target = o
		}
	}
	if target == nil {
		t.Fatalf("continuity() 里找不到 win:2")
	}
	if d := objPropNum(target, "depth"); d != 3 {
		t.Fatalf("win:2 depth = %v, want 3", d)
	}
	if idx := objPropNum(target, "index"); idx != 2 {
		t.Fatalf("win:2 index = %v, want 2", idx)
	}
	if p := objPropStr(target, "path"); p != "/b" {
		t.Fatalf("win:2 path = %q, want /b", p)
	}
	keys, _ := target.GetProperty("stateKeys")
	ka, _ := keys.(*object.Array)
	if ka == nil || len(ka.Elements) != 1 || object.ToString(ka.Elements[0]) != "draft" {
		t.Fatalf("win:2 stateKeys 应含 draft, 实际 %v", keys)
	}
}

// stateStr 读一条栈项状态袋里的字符串键 (测试用)。
func stateStr(t *testing.T, e *routeEntry, key string) string {
	t.Helper()
	o, ok := e.stateObject().(*object.Object)
	if !ok {
		t.Fatalf("state 不是对象")
	}
	v, ok := o.GetProperty(key)
	if !ok {
		return ""
	}
	return object.ToString(v)
}

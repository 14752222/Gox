package gfx

import (
	"sort"
	"strconv"
	"strings"

	"github.com/14752222/Gox/object"
)

// gx/dev 的元素树查看 (devtools [M3] v1, 2026-10-02 落地)。
//
// 能力:
//
//	import { devTree, devNode, devProps } from "gx/dev";
//	const t = devTree();            // 全部窗口的节点树 (有界)
//	const n = devNode("0/2/1");     // 按稳定路径取单节点 (或 null)
//	const p = devProps("0/2/1");    // 只要该节点的 props
//
// 节点形状:
//
//	{ tag, path, key?, text?, props:{...}, box:{x,y,w,h},
//	  focusable, role?, truncated?, children:[...] }
//
// ## 为什么必须有界 (这是 v1 的第一原则)
//
// devSnapshot 的 tree 只有计数; 面板要看"具体哪棵树"就得把节点内容拉出来。
// 但元素树可以非常大 (虚拟化长列表只是把"物化"限制在可见区间, 树本身仍可能
// 上千节点), 而面板每次拉取都会把整份数据经 JSON 形状对象复制一遍 ——
// **一个把自己当数据源的调试面板, 第一件事就是别把自己撑爆**。所以默认
// maxDepth=8 / maxNodes=2000, 超限时截断并标 truncated:true (顶层与具体
// 被截的节点都有), 面板据此显示"还有内容没展开"。
//
// ## 为什么用 path 寻址而不是返回节点引用
//
// 脚本拿到的 GuiNode 是活对象 (可被 h/JSX 传递), 但 devtools 面板是**拉取式**
// 的: 这一秒拿到的 path, 下一秒列表可能已经重建 —— 引用会失效且无法表达
// "此刻第 2 个窗口根的第 1 个子节点"。path 是每次快照当场算出来的稳定寻址串
// ("0/2/1" = 根 → 第 2 个子 → 第 1 个子; 根恒为 "0"), 配 devNode/devProps
// 做"同一次快照内"的二次取值。
//
// ## 只读 + 零成本
//
// 只读 appsSnapshot + 节点字段, 不改任何状态; 不在脚本里 import 就没有开销。
// 多窗口时同一 path 在多个窗口都可能存在 —— devNode/devProps 的第二个可选
// 参数是窗口号 (id), 不传则取**第一个**匹配窗口 (通常等于 activeApp)。

const (
	devTreeDefaultMaxDepth = 8    // 根为深度 0; 最多展开到第 8 层
	devTreeDefaultMaxNodes = 2000 // 全部窗口合计的节点预算

	maxSerialDepth  = 3   // props 值递归序列化的最大深度
	maxSerialArray  = 20  // props 数组最多展开多少项
	maxSerialProps  = 32  // props 对象最多展开多少个键
	maxSerialString = 200 // props 字符串最长保留多少字符
)

// devBudget 是本次遍历的剩余预算 (跨全部窗口共享)。
type devBudget struct {
	maxDepth  int
	maxNodes  int
	used      int
	truncated bool
}

// ===== gx/dev 导出实现 =====

// jsDevTree 是 devTree(opts?) 的实现。
//
//	devTree()                       → 默认边界 (depth 8 / nodes 2000)
//	devTree({ maxDepth: 3 })        → 只看上三层
//	devTree({ maxNodes: 100 })      → 最多 100 个节点
func jsDevTree(args ...object.Value) object.Value {
	opts := devTreeOpts{maxDepth: devTreeDefaultMaxDepth, maxNodes: devTreeDefaultMaxNodes}
	if len(args) > 0 {
		opts = parseDevTreeOpts(args[0], opts)
	}
	b := &devBudget{maxDepth: opts.maxDepth, maxNodes: opts.maxNodes}

	wlist := make([]object.Value, 0)
	for _, a := range appsSnapshot() {
		wo := object.NewObject()
		wo.SetProperty("id", object.NewNumber(float64(a.id)))
		// title 不可达: 窗口标题存在 Window 句柄上 (gfx/window.go), 而句柄不
		// 进注册表 —— 本文件不能改 render.go/window.go, 所以这里诚实留空串。
		// 需要标题的宿主可在 SetDevEnv 的接线点顺带自建一张 id→title 表。
		wo.SetProperty("title", object.NewString(""))
		wo.SetProperty("scope", object.NewString(appScope(a)))
		wo.SetProperty("root", devSerializeNode(a.rootNode(), "0", 0, b))
		wlist = append(wlist, wo)
	}

	out := object.NewObject()
	out.SetProperty("windows", object.NewArray(wlist))
	out.SetProperty("truncated", object.NewBoolean(b.truncated))
	return out
}

// jsDevNode 是 devNode(path, windowId?) 的实现: 命中返回节点, 未命中返回 null。
func jsDevNode(args ...object.Value) object.Value {
	path, winID, ok := devPathArgs(args)
	if !ok {
		return object.NullSingleton
	}
	n := resolveDevPath(path, winID)
	if n == nil {
		return object.NullSingleton
	}
	b := &devBudget{maxDepth: devTreeDefaultMaxDepth, maxNodes: devTreeDefaultMaxNodes}
	return devSerializeNode(n, path, 0, b)
}

// jsDevProps 是 devProps(path, windowId?) 的实现: 只返回该节点的 props。
func jsDevProps(args ...object.Value) object.Value {
	path, winID, ok := devPathArgs(args)
	if !ok {
		return object.NullSingleton
	}
	n := resolveDevPath(path, winID)
	if n == nil {
		return object.NullSingleton
	}
	return devSerializeProps(n.Props)
}

// devPathArgs 解析 (path, windowId?) 两个参数。
func devPathArgs(args []object.Value) (path string, winID int, ok bool) {
	if len(args) == 0 {
		return "", 0, false
	}
	s, isStr := args[0].(*object.String)
	if !isStr {
		return "", 0, false
	}
	if len(args) > 1 {
		if n, isNum := args[1].(*object.Number); isNum {
			winID = int(n.Value)
		}
	}
	return s.Value, winID, true
}

// ===== 序列化 =====

// devSerializeNode 把一个节点序列化成 JS 对象, 并消耗预算。
func devSerializeNode(n *GuiNode, path string, depth int, b *devBudget) object.Value {
	if n == nil {
		return object.NullSingleton
	}
	b.used++
	o := object.NewObject()
	o.SetProperty("tag", object.NewString(n.Tag))
	o.SetProperty("path", object.NewString(path))
	if key, ok := devNodeKey(n); ok {
		o.SetProperty("key", object.NewString(key))
	}
	// #text 的内容不在 props 里 (它是 Text 字段), 但元素树面板看不到文本就等于
	// 少了一半信息 —— 只在文本节点上补一个 text 字段。
	if n.Tag == "#text" {
		o.SetProperty("text", object.NewString(devTruncate(n.Text)))
	}
	o.SetProperty("props", devSerializeProps(n.Props))
	o.SetProperty("box", devBoxObject(n.Box))
	o.SetProperty("focusable", object.NewBoolean(n.focusable()))
	if role := n.AriaRole(); role != "" {
		o.SetProperty("role", object.NewString(role))
	}

	children := make([]object.Value, 0, len(n.Children))
	cut := false
	if depth >= b.maxDepth {
		// 到这个深度就不再往下展开; 有子节点才算"被截断"。
		cut = len(n.Children) > 0
	} else {
		for i, c := range n.Children {
			if b.used >= b.maxNodes {
				cut = true
				break
			}
			children = append(children, devSerializeNode(c, path+"/"+strconv.Itoa(i), depth+1, b))
		}
	}
	if cut {
		b.truncated = true
		o.SetProperty("truncated", object.NewBoolean(true))
	}
	o.SetProperty("children", object.NewArray(children))
	return o
}

// devSerializeProps 把 props map 序列化成 JS 对象 (键排序, 保证快照稳定)。
func devSerializeProps(props map[string]object.Value) *object.Object {
	o := object.NewObject()
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	seen := map[object.Value]bool{}
	for _, k := range keys {
		o.SetProperty(k, devSerializeValue(props[k], 0, seen))
	}
	return o
}

// devSerializeValue 安全地把一个 JS 值转成可放进快照的形状。
//
// 安全口径 (v1 边界):
//   - 函数 → "[Function]": 快照是数据, 不是可执行图; 函数体字符串既无用又可能很大。
//   - GuiNode → "[Element <tag>]": 避免把另一棵树整个拖进来 (那正是"撑爆"的来源)。
//   - 循环引用 → "[Circular]"; 超过 maxSerialDepth → "[Array n]" / "{…}";
//     数组最多 maxSerialArray 项、对象最多 maxSerialProps 键, 超出以 "…" 收尾。
//   - 兜底走 Inspect 且带 recover —— 任何没预料到的值都不允许让快照 panic。
func devSerializeValue(v object.Value, depth int, seen map[object.Value]bool) object.Value {
	if v == nil {
		return object.NullSingleton
	}
	switch t := v.(type) {
	case *object.String:
		return object.NewString(devTruncate(t.Value))
	case *object.Number:
		return object.NewNumber(t.Value)
	case *object.Boolean:
		return object.NewBoolean(t.Value)
	case *object.Null, *object.Undefined:
		return object.NullSingleton
	}
	if object.IsCallable(v) {
		return object.NewString("[Function]")
	}
	if node, ok := v.(*GuiNode); ok {
		return object.NewString("[Element " + node.Tag + "]")
	}
	switch t := v.(type) {
	case *object.Array:
		if seen[t] {
			return object.NewString("[Circular]")
		}
		if depth >= maxSerialDepth {
			return object.NewString("[Array " + strconv.Itoa(len(t.Elements)) + "]")
		}
		seen[t] = true
		defer delete(seen, t)
		n := len(t.Elements)
		capped := false
		if n > maxSerialArray {
			n, capped = maxSerialArray, true
		}
		out := make([]object.Value, 0, n+1)
		for i := 0; i < n; i++ {
			out = append(out, devSerializeValue(t.Elements[i], depth+1, seen))
		}
		if capped {
			out = append(out, object.NewString("…"))
		}
		return object.NewArray(out)
	case *object.Object:
		if seen[t] {
			return object.NewString("[Circular]")
		}
		if depth >= maxSerialDepth {
			return object.NewString("{…}")
		}
		seen[t] = true
		defer delete(seen, t)
		keys := make([]string, 0, len(t.Properties))
		for k := range t.Properties {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		capped := false
		if len(keys) > maxSerialProps {
			keys, capped = keys[:maxSerialProps], true
		}
		o := object.NewObject()
		for _, k := range keys {
			var val object.Value
			if d, ok := t.Properties[k]; ok {
				val = d.Value
			}
			o.SetProperty(k, devSerializeValue(val, depth+1, seen))
		}
		if capped {
			o.SetProperty("…", object.NewString("[more]"))
		}
		return o
	}
	return object.NewString(devTruncate(devSafeInspect(v)))
}

// devBoxObject 把布局框转成 {x,y,w,h}。
func devBoxObject(r Rect) *object.Object {
	o := object.NewObject()
	o.SetProperty("x", object.NewNumber(float64(r.X)))
	o.SetProperty("y", object.NewNumber(float64(r.Y)))
	o.SetProperty("w", object.NewNumber(float64(r.W)))
	o.SetProperty("h", object.NewNumber(float64(r.H)))
	return o
}

// devNodeKey 读取节点的 key (list/for 的稳定标识)。只认字符串/数字 —— 其它
// 类型不是合法的 key 写法, 宁可不报也不乱转。
func devNodeKey(n *GuiNode) (string, bool) {
	v, ok := n.Props["key"]
	if !ok {
		return "", false
	}
	switch t := v.(type) {
	case *object.String:
		return t.Value, true
	case *object.Number:
		return object.ToString(t), true
	}
	return "", false
}

// devTruncate 截断过长字符串 (保留头部 + 省略号)。
func devTruncate(s string) string {
	if len(s) <= maxSerialString {
		return s
	}
	return s[:maxSerialString] + "…"
}

// devSafeInspect 调 Inspect 兜底, 任何 panic 都降级为占位串。
func devSafeInspect(v object.Value) (s string) {
	defer func() {
		if recover() != nil {
			s = "[unserializable]"
		}
	}()
	if v == nil {
		return "null"
	}
	return v.Inspect()
}

// ===== 路径寻址 =====

// resolveDevPath 在存活窗口里按 path 找节点。path 形如 "0/2/1"; 首段必须是
// "0" (根), 之后每段是子节点下标。windowID != 0 时只在该窗口里找。
func resolveDevPath(path string, windowID int) *GuiNode {
	segs := strings.Split(path, "/")
	if len(segs) == 0 || segs[0] != "0" {
		return nil
	}
	idx := make([]int, 0, len(segs)-1)
	for _, s := range segs[1:] {
		i, err := strconv.Atoi(s)
		if err != nil || i < 0 {
			return nil
		}
		idx = append(idx, i)
	}
	for _, a := range appsSnapshot() {
		if windowID != 0 && a.id != windowID {
			continue
		}
		n := a.rootNode()
		if n == nil {
			continue
		}
		ok := true
		for _, i := range idx {
			if i >= len(n.Children) {
				ok = false
				break
			}
			n = n.Children[i]
		}
		if ok {
			return n
		}
	}
	return nil
}

// ===== opts 解析 =====

type devTreeOpts struct {
	maxDepth int
	maxNodes int
}

// parseDevTreeOpts 读 {maxDepth, maxNodes}; 缺失/类型不符落回缺省值 (与内核
// 各处"坏配置静默回落"的口径一致 —— 调试面板的坏参数不该报错)。
func parseDevTreeOpts(arg object.Value, def devTreeOpts) devTreeOpts {
	o, ok := arg.(*object.Object)
	if !ok {
		return def
	}
	out := def
	if v, ok := o.GetProperty("maxDepth"); ok {
		if n, isNum := v.(*object.Number); isNum && n.Value >= 0 {
			out.maxDepth = int(n.Value)
		}
	}
	if v, ok := o.GetProperty("maxNodes"); ok {
		if n, isNum := v.(*object.Number); isNum && n.Value > 0 {
			out.maxNodes = int(n.Value)
		}
	}
	return out
}

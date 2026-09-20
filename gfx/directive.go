package gfx

import (
	"github.com/14752222/Gox/object"
)

// ===== 元素级指令: each / show =====
//
// 2026-09-20: 控制流从"包装组件"改成**元素级指令** —— 与 model 同一层
// (都是 h() 里的一次 props 改写), 于是三者共用一套心智: 指令写在元素上。
//
//	<view each={rows} key="id">{(row, i) => <row note={row.id}>…</row>}</view>
//	<view show={open} fallback={<text>隐藏中</text>}>…</view>
//	<row each={rows}>{() => …}</row>          每一项是一层 row 盒子 (字面意思)
//
// ## 语义 = 旧的 <For> / <Show>, 一个字都没改
//
// 指令只把键翻译成引擎的 prop 名, 然后把活儿交给**同一个引擎**:
//
//	each  →  jsViewFor   (keyed 复用 / stable / fallback / 每项独立子树与 onCleanup)
//	show  →  jsViewShow  (keep-alive 显隐: 隐藏只摘出布局流, 子树保活; 首次显示才构建)
//
//	each → each        show → when        key → key
//	stable → stable    fallback → fallback    gap → gap (见下)
//
// ## 为什么宿主仍然是透明容器
//
// 指令返回的是一个透明宿主 (slot), 每项 / 每个分支挂在它下面 —— 与旧的 <For> /
// <Show> 逐像素一致, 于是"列表宿主不凭空多一个盒子"这条承诺不变。差别只在
// **每一项现在是元素本身的副本**:
//
//	<view each={rows}>   每项 = 一个透明 view ⇒ 与旧 <For> 完全等价 (推荐迁移写法)
//	<row  each={rows}>   每项 = 一层 row 盒子 (想要"每项一个盒子"时用它)
//
// `view` 是 2026-09-20 同批开放的公开标签 (Fragment / Vue 的 <template>):
// 布局透明, 与内核内部的 slot 占位节点同一套语义。
//
// ## gap
//
// 有 each 时 gap 会**同时**写到宿主与元素副本上: 前者是列表项之间的间距 (旧
// `<For gap={N}>` 的语义), 后者是该元素内部的间距。对"每项内容只有一个子节点"
// 的常见情形两者互不干扰 (单子容器没有内部间距可言); 想只控制一侧就只写一侧 ——
// 宿主没有 gap 时跟随父容器。
//
// ## 与其它指令 / 自带 prop 的关系
//
//   - 两者同给时 **show 在外, each 在内**: 整个列表一起显隐, 且列表在分支里懒构建;
//   - model 照旧生效, 且是**每份副本各一份** (`<view each={rows}><input model={q}/></view>`
//     里 input 的绑定不受影响, 但那种写法下所有副本共用一个 signal —— 要每项独立状态
//     就把状态放进每项的组件里);
//   - 元素自己的 props 与事件 (onClick / padding / note …) 全部保留, 每项一份;
//   - 指令只对**内置元素标签**生效: `<Comp each={x}/>` 是组件自己的 prop (JSX 大写
//     标签当场调用, 内核看不到那个调用)。
//
// ## 键为什么要从元素 props 里剥掉
//
// 指令键 (each / show / fallback / key / stable) 只对引擎有意义。留在 props 里会
// 被当成普通响应式 prop 反复求值, 而且 each 会被当成数据再挂到每份副本上 (语义会
// 递归) —— 所以副本的 props 是"剥掉指令键之后的那一份"。gap 不在剥离之列: 它是
// 元素本来就有的 prop。
var directiveKeys = map[string]struct{}{
	"each": {}, "show": {}, "fallback": {}, "key": {}, "stable": {},
}

// expandElementDirective 展开元素上的 each / show。
//
// 返回 nil 表示"这个元素没带指令, 按普通元素处理"; 返回非 nil 就是最终的渲染结果
// (透明宿主), 调用方**不要**再建普通节点 —— 元素副本由这里自己造。
func expandElementDirective(tag string, props *object.Object, kids []object.Value) object.Value {
	eachVal := modelProp(props, "each")
	showVal := modelProp(props, "show")
	if eachVal == nil && showVal == nil {
		return nil
	}
	d := &elementDirective{tag: tag, raw: props, rest: clonePropsExcept(props, directiveKeys)}

	switch {
	case eachVal != nil && showVal != nil:
		// show 在外, each 在内: 整个列表一起显隐, 列表本身也在分支里懒构建
		return d.wrapShow(showVal, func() object.Value { return d.buildEach(eachVal, kids) })
	case eachVal != nil:
		return d.buildEach(eachVal, kids)
	default:
		return d.wrapShow(showVal, func() object.Value { return d.buildElement(kids...) })
	}
}

// elementDirective 是一次指令展开的全部上下文。
type elementDirective struct {
	tag  string
	raw  *object.Object // 原 props (指令键 + 元素自己的 key 都从它取)
	rest *object.Object // 剥掉指令键之后的那份: 元素副本用它
}

// buildElement 造一个元素副本。kids 为元素内容的子节点 (each 时是每项的内容,
// show 时是元素原本的子节点)。
func (d *elementDirective) buildElement(kids ...object.Value) object.Value {
	args := make([]object.Value, 0, len(kids)+2)
	args = append(args, object.NewString(d.tag), d.rest)
	args = append(args, kids...)
	return JSBuiltinH(args...)
}

// buildEach 用 For 引擎把元素重复 N 次: 每一项 = 同一个元素包住渲染函数的产物。
//
// 没有渲染函数时不造包装器 (直接交给引擎): 引擎已有的那条
// "既没有渲染函数也没有 fallback, 渲染为空" 警告必须照旧生效。
func (d *elementDirective) buildEach(eachVal object.Value, kids []object.Value) object.Value {
	fp := object.NewObject()
	modelSetProp(fp, "each", eachVal)
	copyProp(d.raw, fp, "key")
	copyProp(d.raw, fp, "stable")
	copyProp(d.raw, fp, "fallback")
	copyProp(d.raw, fp, "gap") // 宿主 gap: 旧 <For gap={N}> 的列表项间距语义

	render := firstCallableOf(kids)
	if render == nil {
		return jsViewFor(fp)
	}
	perItem := object.NewBuiltin("directive.each.item", func(args ...object.Value) object.Value {
		return d.buildElement(viewCall(render, args...))
	})
	return jsViewFor(fp, perItem)
}

// wrapShow 用 Show 引擎包一层。body 是一个**懒构建器**: 分支第一次显示时才被调用,
// 于是"没显示过的元素/列表根本没建过"这条旧承诺照旧成立。
func (d *elementDirective) wrapShow(showVal object.Value, build func() object.Value) object.Value {
	sp := object.NewObject()
	modelSetProp(sp, "when", showVal)
	copyProp(d.raw, sp, "fallback")
	body := object.NewBuiltin("directive.show.body", func(args ...object.Value) object.Value {
		return build()
	})
	return jsViewShow(sp, body)
}

// clonePropsExcept 复制 props, 跳过被排除的键 (元素副本的 props 就这么来的)。
func clonePropsExcept(props *object.Object, except map[string]struct{}) *object.Object {
	out := object.NewObject()
	for name, desc := range props.Properties {
		if _, skip := except[name]; skip {
			continue
		}
		out.SetProperty(name, desc.Value)
	}
	return out
}

// copyProp 把 src 上的某个键原样搬到 dst (缺失时什么都不做)。
func copyProp(src, dst *object.Object, name string) {
	if src == nil {
		return
	}
	if desc, ok := src.Properties[name]; ok {
		modelSetProp(dst, name, desc.Value)
	}
}

// firstCallableOf 取第一个可调用值 (JSX 的渲染函数子节点; 其余是排版空白)。
func firstCallableOf(vals []object.Value) object.Value {
	for _, v := range vals {
		if object.IsCallable(v) {
			return v
		}
	}
	return nil
}

package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== "值为 undefined 的键视同没给" (组件库纪律 #4 的内核收口) =====
//
// 背景: JSX / 组件库常条件拼 props, 写出 {model: sig, value: undefined}、
// {each: rows, key: undefined} 这种"键在值无"的形态。内核按"键是否存在"
// 判断就会误报 "同时给了 model 与 value" / "key 收到 UNDEFINED"。
// 收口在 propIsDefined: modelProp / modelHasProp / viewProp / copyProp
// 统一经它过滤。这组测试锁三件事:
//   1. undefined 值的键按不存在算 (不告警、不递值);
//   2. null 是真实值, 不在豁免之列;
//   3. 元素句柄 (*GuiNode) 的 JS typeof 虽是 "undefined", 但绝不能被
//      误判成"没给" —— 值为元素节点的 prop 必须原样拿到。

// TestUndefinedPropIsAbsent 纯单元层: 四个读口的口径一致。
func TestUndefinedPropIsAbsent(t *testing.T) {
	props := object.NewObject()
	undef := object.UndefinedSingleton
	props.SetProperty("model", object.NewString("sig"))
	props.SetProperty("value", undef)
	props.SetProperty("key", undef)
	props.SetProperty("gap", undef)

	// undefined 值的键: 按不存在算
	if modelHasProp(props, "value") {
		t.Fatalf("value: undefined 应按不存在算")
	}
	if modelProp(props, "value") != nil {
		t.Fatalf("modelProp(value: undefined) 应返回 nil")
	}
	if modelHasProp(props, "key") {
		t.Fatalf("key: undefined 应按不存在算")
	}
	if viewProp(props, "gap") != nil {
		t.Fatalf("viewProp(gap: undefined) 应返回 nil")
	}
	dst := object.NewObject()
	copyProp(props, dst, "gap")
	if _, ok := dst.Properties["gap"]; ok {
		t.Fatalf("copyProp 不该搬 undefined 值的 gap 键")
	}

	// null 是真实值: 不豁免
	props.SetProperty("checked", object.NullSingleton)
	if !modelHasProp(props, "checked") {
		t.Fatalf("checked: null 是真实值, 应算存在")
	}

	// 有值的键照常
	if !modelHasProp(props, "model") {
		t.Fatalf("model 有值, 应算存在")
	}
}

// TestGuiNodePropNotTreatedUndefined 元素句柄不能被 typeof 语义误伤。
func TestGuiNodePropNotTreatedUndefined(t *testing.T) {
	node := JSBuiltinH(object.NewString("text"), nil)
	props := object.NewObject()
	props.SetProperty("label", node)

	if got := modelProp(props, "label"); got != node {
		t.Fatalf("值为元素句柄的 prop 应原样拿到 (got %v)", got)
	}
	if got := viewProp(props, "label"); got != node {
		t.Fatalf("viewProp 同理 (got %v)", got)
	}
	if !modelHasProp(props, "label") {
		t.Fatalf("元素句柄值的键应算存在")
	}
	dst := object.NewObject()
	copyProp(props, dst, "label")
	if dst.Properties["label"].Value != node {
		t.Fatalf("copyProp 应原样搬元素句柄")
	}
}

// TestUndefinedPropsNoSpuriousWarns UI 层: model+value:undefined 不告警、
// each+key:undefined 不告警, 且受控/列表都照常渲染。
func TestUndefinedPropsNoSpuriousWarns(t *testing.T) {
	resetWarnRing()
	resetModelWarns()

	v, fake := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";

		const [draft, setDraft] = createSignal("m");
		const [rows, setRows] = createSignal(["a", "b", "c"]);
		render(
			<window title="undef-props" width={400} height={300}>
				<column gap={6}>
					<input width={200} model={draft} value={undefined} />
					<view each={rows} key={undefined}>
						{(r) => <row note={r}><text>{r}</text></row>}
					</view>
				</column>
			</window>
		);
	`)

	runPumpSteps(t, v, fake, []func(){
		func() {
			ins := findAll(uiRoot(t), "input")
			if len(ins) != 1 {
				t.Fatalf("input 数量 = %d, want 1", len(ins))
			}
			// model 为准: value: undefined 不构成冲突, 显示 model 的当前值
			if got := ins[0].inputValue(); got != "m" {
				t.Fatalf("model + value:undefined 的显示 = %q, want \"m\"", got)
			}
			// key: undefined → 按没给处理 → 按位置匹配, 3 行照常渲染
			if rows := findAll(uiRoot(t), "row"); len(rows) != 3 {
				t.Fatalf("row 数量 = %d, want 3 (key:undefined 不该废掉列表)", len(rows))
			}
			for _, frag := range []string{
				"同时给了 model 与 value",
				"key 需要取值函数",
			} {
				if viewWarned(frag) {
					t.Fatalf("不该出现的警告 %q (缓冲 = %v)", frag, warnSnapshot())
				}
			}
		},
	})
}

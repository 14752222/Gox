package gfx

import "testing"

// ===== JSX 组件契约: props 天生是对象 + children 两条路可达 =====
//
// 此前 <Comp>x</Comp> 降级成 Comp(null, "x"): 组件函数体里 props.title 抛
// "Cannot read properties of null", 每个组件都得先 props = props || {};
// 子节点只能从额外实参 (children(arguments)) 拿, props.children 恒 undefined。
// parser/jsx.go 的 buildJSXCall 修了这两条: 无属性传 {}, 有子节点时
// props.children (恒为数组) 与额外实参同时可用。这组测试从 JS 侧锁住契约。

// TestJSXComponentPropsNeverNull props 不再是 null; children 双通道可达。
func TestJSXComponentPropsNeverNull(t *testing.T) {
	v, fake := evalUI(t, `
		import { h, render } from "gx/gfx";

		let r1 = "unset", r2 = "unset";
		function A(props, kid) {
			// 无属性 + 有子节点: props 是 {} (不再 null), children 数组与额外实参同值
			r1 = (props === null ? "null" : "object") + "|" + props.title +
				"|" + props.children[0] + "|" + kid;
			return <text note="a">a</text>;
		}
		function B(props) {
			// 有属性 + 无子节点: 属性照常, children 键压根不出现 (不是 undefined 占位)
			r2 = (props === null ? "null" : "object") + "|" + props.title +
				"|" + (props.children === undefined ? "-" : "has");
			return <text note="b">b</text>;
		}
		render(
			<window title="comp" width={300} height={200}>
				<column>
					<A>x</A>
					<B title="t" />
				</column>
			</window>
		);
		globalThis.report1 = () => r1;
		globalThis.report2 = () => r2;
	`)

	runPumpSteps(t, v, fake, []func(){
		func() {
			if got, want := callGlobalInspect(t, v, "report1"), "object|undefined|x|x"; got != want {
				t.Fatalf("无属性组件: got %q, want %q", got, want)
			}
			if got, want := callGlobalInspect(t, v, "report2"), "object|t|-"; got != want {
				t.Fatalf("有属性无子节点组件: got %q, want %q", got, want)
			}
			viewAssertLayout(t, uiRoot(t), "a|b", "a|b")
		},
	})
}

package gfx

import (
	"strings"
	"testing"

	"github.com/14752222/Gox/object"
)

// 未注册属性 / 未实现事件的诊断（看板 rgQsDD）。
//
// 三条要钉住的语义：
//  1. dev 模式下会**发声**（默认生产口径一声不响，那正是本单要消灭的状态）；
//  2. strictAPI 下从 warning 升级成 error（h() 直接抛）；
//  3. 合法 prop 一声不响 —— 误报会让用户把整个开关关掉，比不做更糟。

// newPropNode 造一个只够接线用的节点（不进树、不参与布局）。
func newPropNode(tag string) *GuiNode {
	return &GuiNode{Tag: tag, Props: map[string]object.Value{}}
}

// capturePropWarns 把警告出口换成计数器，返回取警告的函数。
func capturePropWarns(t *testing.T) func() []string {
	t.Helper()
	var got []string
	old := warnUnknownPropOut
	warnUnknownPropOut = func(tag, name, msg string) {
		got = append(got, tag+"|"+name+"|"+msg)
	}
	t.Cleanup(func() { warnUnknownPropOut = old })
	return func() []string { return got }
}

func TestUnknownPropWarnsInDevMode(t *testing.T) {
	resetPropWarns()
	SetDevMode(true)
	t.Cleanup(func() { SetDevMode(false); resetPropWarns() })
	warns := capturePropWarns(t)

	n := newPropNode("button")
	n.wireProp("label", object.NewString("确定"))

	got := warns()
	if len(got) != 1 {
		t.Fatalf("警告条数 = %d, want 1 (实际 %v)", len(got), got)
	}
	// 文案必须给出**替代写法**: "button 不认 label" 只说半句, 用户照样不知道
	// 该怎么写 —— 内容走子节点才是这条警告的全部价值。
	if !strings.Contains(got[0], "button|label|") ||
		!strings.Contains(got[0], "子节点") {
		t.Errorf("警告文案没说清替代写法: %q", got[0])
	}

	// 去重: h() 在每次重建时都会跑, 不去重会在长列表里刷出成百上千条同内容警告
	n2 := newPropNode("button")
	n2.wireProp("label", object.NewString("取消"))
	if again := warns(); len(again) != 1 {
		t.Errorf("同一 (标签,属性) 应只警告一次, 实际 %d 条: %v", len(again), again)
	}
}

func TestUnknownPropSilentOutsideDevMode(t *testing.T) {
	resetPropWarns()
	// 生产口径（默认）: 一声不响 —— 打包成 exe 的应用既没有 stderr 可看,
	// 也不该为内部细节刷屏。
	SetDevMode(false)
	t.Cleanup(resetPropWarns)
	warns := capturePropWarns(t)

	n := newPropNode("button")
	if err := n.wireProp("label", object.NewString("确定")); err != nil {
		t.Fatalf("非严格模式不该返回错误: %v", err)
	}
	if got := warns(); len(got) != 0 {
		t.Errorf("生产口径不该告警, 实际 %v", got)
	}
}

func TestStrictAPIUnknownPropIsError(t *testing.T) {
	resetPropWarns()
	SetStrictAPI(true)
	t.Cleanup(func() { SetStrictAPI(false); resetPropWarns() })
	warns := capturePropWarns(t)

	n := newPropNode("button")
	err := n.wireProp("label", object.NewString("确定"))
	if err == nil {
		t.Fatal("strictAPI 下未注册属性应报错, 实际返回 nil")
	}
	if e, ok := err.(*object.Error); !ok {
		t.Fatalf("错误类型 = %T, want *object.Error", err)
	} else if !strings.Contains(e.Message, "子节点") {
		t.Errorf("错误文案没说清替代写法: %q", e.Message)
	}
	// 严格模式下是**报错**而不是报警: 两条通道各管一档, 不能既抛错又刷警告
	if got := warns(); len(got) != 0 {
		t.Errorf("strictAPI 下不该再走警告通道, 实际 %v", got)
	}
	// 废键不该落进 Props
	if _, stored := n.Props["label"]; stored {
		t.Error("strictAPI 下未注册属性不该写进 Props")
	}
}

func TestStrictAPIHThrows(t *testing.T) {
	resetPropWarns()
	SetStrictAPI(true)
	t.Cleanup(func() { SetStrictAPI(false); resetPropWarns() })

	props := object.NewObject()
	props.SetProperty("label", object.NewString("确定"))
	// h() 是脚本唯一的建树入口: 严格模式的错误必须从这里抛出去, 否则脚本侧
	// 拿到的还是一个"渲染出来是空白"的节点, 与静默忽略没有区别。
	out := JSBuiltinH(object.NewString("button"), props)
	if e, ok := out.(*object.Error); !ok {
		t.Fatalf("h() 返回 %T (%v), want *object.Error", out, out)
	} else if !strings.Contains(e.Message, "strictAPI") {
		t.Errorf("错误文案应带 strictAPI 前缀: %q", e.Message)
	}
}

func TestKnownPropsStaySilent(t *testing.T) {
	resetPropWarns()
	SetDevMode(true)
	t.Cleanup(func() { SetDevMode(false); resetPropWarns() })
	warns := capturePropWarns(t)

	// 通用属性 / 按标签属性 / 事件 / 指令键 / aria-* —— 一律不许出声。
	// 这一条是防误报的兜底: 误报会让用户直接关掉开关, 那比不做更糟。
	n := newPropNode("button")
	n.wireProp("width", object.NewNumber(120))
	n.wireProp("background", object.NewString("#eee"))
	n.wireProp("onClick", object.NewString("x"))
	n.wireProp("onDraw", object.NewString("x"))
	n.wireProp("aria-label", object.NewString("确定"))
	n.wireProp("data-x", object.NewString("1"))
	n.wireProp("each", object.NewString("x"))

	// 按标签的属性（alignItems 只在容器上读）
	c := newPropNode("column")
	c.wireProp("alignItems", object.NewString("center"))
	c.wireProp("gap", object.NewNumber(8))

	if got := warns(); len(got) != 0 {
		t.Errorf("合法属性不该告警: %v", got)
	}
}

func TestEventNotImplementedSays(t *testing.T) {
	resetPropWarns()
	SetDevMode(true)
	t.Cleanup(func() { SetDevMode(false); resetPropWarns() })
	warns := capturePropWarns(t)

	n := newPropNode("button")
	n.wireProp("onMouseLeave", object.NewString("x"))

	got := warns()
	if len(got) != 1 {
		t.Fatalf("警告条数 = %d, want 1 (实际 %v)", len(got), got)
	}
	// 文案必须明说"内核还没实现": 混进"未知属性"会让人去查自己脚本的拼写,
	// 而问题根本不在脚本这一侧 —— 查一天也查不出来。
	if !strings.Contains(got[0], "还没有实现") {
		t.Errorf("未实现事件的文案没说清原因: %q", got[0])
	}
	if strings.Contains(got[0], "未知属性") {
		t.Errorf("未实现事件不该说成未知属性: %q", got[0])
	}
}

func TestUnknownTagPropsStaySilent(t *testing.T) {
	resetPropWarns()
	SetDevMode(true)
	t.Cleanup(func() { SetDevMode(false); resetPropWarns() })
	warns := capturePropWarns(t)

	// 拼错的标签已有 warnUnknownTag 兜底; 再叠一层"未知属性"只会把真正的问题
	// （标签拼错）淹在一堆噪音里。
	n := newPropNode("buton")
	n.wireProp("label", object.NewString("确定"))
	if got := warns(); len(got) != 0 {
		t.Errorf("未知标签上的属性不该再告警: %v", got)
	}
}

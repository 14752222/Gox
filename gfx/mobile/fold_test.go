package mobile

import (
	"testing"

	"github.com/14752222/Gox/gfx"
)

// 本文件覆盖批 B 的纯函数层与 JSON 上报入口。全部在桌面可跑 —— 这正是把折叠
// 逻辑放在 gfx/mobile (而非 gfx/ios / gfx/android) 的意义: 那两个包的 build tag
// 在 Windows 上编译不到, 里面的错只能靠真机发现。

func b(v bool) *bool { return &v }

// --- 边界 1: 零宽 / 零高的 division 判 inactive (对齐 iOS "平放时宽度为 0") ---

func TestNormalizeRegionsFlatDivisionInactive(t *testing.T) {
	// 平展时 iOS 报的是"宽度 0 的 division", 它必须被判 inactive —— 否则
	// 避让逻辑会为一条不存在的折痕让出空间。
	got := NormalizeRegions([]RawRegion{
		{Kind: "division", X: 1600, Y: 0, W: 0, H: 2560},
	}, 3240, 2560)
	if len(got) != 1 {
		t.Fatalf("零宽 division 应保留在列表里(供 includeInactive 用): %#v", got)
	}
	if got[0].Active {
		t.Fatalf("零宽 division 应判 inactive: %#v", got[0])
	}
	if got[0].Kind != KindDivision || got[0].ID != "fold-0" {
		t.Fatalf("kind/id 不符: %#v", got[0])
	}

	// 显式 active=true 时尊重宿主 (它可能知道几何之外的信息)。
	got = NormalizeRegions([]RawRegion{
		{Kind: "division", X: 1600, Y: 0, W: 0, H: 2560, Active: b(true)},
	}, 3240, 2560)
	if !got[0].Active {
		t.Fatalf("显式 active=true 应被尊重: %#v", got[0])
	}
}

// --- 边界 2: 超窗与负坐标裁剪, 完全在窗外丢弃 ---

func TestNormalizeRegionsClampAndDrop(t *testing.T) {
	got := NormalizeRegions([]RawRegion{
		// 负坐标: 左边界被切掉 10px ⇒ X=0, W 少 10。
		{Kind: "occlusion", X: -10, Y: 0, W: 100, H: 50},
		// 超出右下边界: 裁到屏内。
		{Kind: "occlusion", X: 3100, Y: 2500, W: 500, H: 500},
		// 完全在窗外: 丢弃。
		{Kind: "occlusion", X: 4000, Y: 0, W: 100, H: 100},
	}, 3240, 2560)

	if len(got) != 2 {
		t.Fatalf("完全在窗外的区域应被丢弃, 期望 2 段: %#v", got)
	}
	if got[0].X != 0 || got[0].W != 90 {
		t.Fatalf("负坐标应钳到 0 且宽度相应减少: %#v", got[0])
	}
	if got[1].W != 140 || got[1].H != 60 {
		t.Fatalf("超窗区域应裁到屏内: %#v", got[1])
	}
}

// --- 边界 3: 空输入 ---

func TestNormalizeRegionsEmpty(t *testing.T) {
	if got := NormalizeRegions(nil, 100, 100); got != nil {
		t.Fatalf("空输入应返回 nil: %#v", got)
	}
	// 全部被丢弃时也应返回 nil (而不是空切片), 便于调用方 len()==0 统一判断。
	if got := NormalizeRegions([]RawRegion{{Kind: "division", X: 100, Y: 100, W: 50, H: 50}}, 100, 100); got != nil {
		t.Fatalf("全丢弃应返回 nil: %#v", got)
	}
}

// --- 边界 4: StructuralRegions 在 折叠 → 展开 → 折叠 中稳定 ---

func TestStructuralRegionsStableAcrossPostureFlips(t *testing.T) {
	// 同一段折痕的三种上报形态 (宿主在姿态变化时的真实行为)。
	halfOpen := []RawRegion{{Kind: "division", X: 1600, Y: 0, W: 24, H: 2560, Active: b(true)}}
	flat := []RawRegion{{Kind: "division", X: 1600, Y: 0, W: 0, H: 2560, Active: b(false)}}

	seq := [][]RawRegion{halfOpen, flat, halfOpen}
	var counts []int
	for _, raw := range seq {
		rs := NormalizeRegions(raw, 3240, 2560)
		counts = append(counts, len(StructuralRegions(rs)))
	}
	for i, n := range counts {
		if n != 1 {
			t.Fatalf("第 %d 步 StructuralRegions 应为 1 段 (列数不随姿态 flip-flop), 实际 %d: %v",
				i, n, counts)
		}
	}
	// 对照: 若按 Active 过滤, 平展那一步会掉到 0 —— 这就是必须存在
	// StructuralRegions 的原因。
	flatActive := 0
	for _, r := range NormalizeRegions(flat, 3240, 2560) {
		if r.Active {
			flatActive++
		}
	}
	if flatActive != 0 {
		t.Fatalf("对照组不成立 (平展时应有 0 段 active): %d", flatActive)
	}
}

// --- 边界 5: PostureFrom 绝不猜 ---

func TestPostureFromNeverGuesses(t *testing.T) {
	// 无折痕、无 active division ⇒ flat (非折叠屏与平展对布局等价)。
	if got := PostureFrom(nil, nil); got != PostureFlat {
		t.Fatalf("无折痕应判 flat, 实际 %q", got)
	}
	// 有折痕对象但没有尺寸 ⇒ 分不清平展与漏填, 必须是 unknown。
	if got := PostureFrom(&RawHinge{X: 1600, Y: 0}, nil); got != PostureUnknown {
		t.Fatalf("无尺寸折痕应判 unknown, 实际 %q", got)
	}
	// 有尺寸的折痕 ⇒ half-open。
	if got := PostureFrom(&RawHinge{X: 1600, Y: 0, W: 24, H: 2560}, nil); got != PostureHalfOpen {
		t.Fatalf("有尺寸折痕应判 half-open, 实际 %q", got)
	}
	// 没有 hinge 对象, 但有 active division (宿主只报了 regions) ⇒ half-open。
	rs := NormalizeRegions([]RawRegion{{Kind: "division", X: 1600, Y: 0, W: 24, H: 2560}}, 3240, 2560)
	if got := PostureFrom(nil, rs); got != PostureHalfOpen {
		t.Fatalf("仅有 active division 应判 half-open, 实际 %q", got)
	}
}

// --- 边界 6: SplitByKind 空输入返回 nil ---

func TestSplitByKind(t *testing.T) {
	if got := SplitByKind(nil, KindDivision); got != nil {
		t.Fatalf("空输入应返回 nil: %#v", got)
	}
	rs := NormalizeRegions([]RawRegion{
		{Kind: "occlusion", X: 0, Y: 0, W: 10, H: 10},
		{Kind: "division", X: 100, Y: 0, W: 4, H: 200},
		{Kind: "occlusion", X: 20, Y: 0, W: 10, H: 10},
	}, 400, 300)
	div := SplitByKind(rs, KindDivision)
	if len(div) != 1 || div[0].Kind != KindDivision {
		t.Fatalf("division 过滤错误: %#v", div)
	}
	occ := SplitByKind(rs, KindOcclusion)
	if len(occ) != 2 {
		t.Fatalf("occlusion 过滤错误: %#v", occ)
	}
	if got := SplitByKind(rs, "nonsense"); got != nil {
		t.Fatalf("无匹配应返回 nil: %#v", got)
	}
}

// --- 归一化后的顺序必须稳定 (按 kind 分组, 组内保持声明序) ---

func TestNormalizeRegionsStableOrder(t *testing.T) {
	// 输入刻意让 occlusion 排在 division 前面, 且各自乱序。
	got := NormalizeRegions([]RawRegion{
		{Kind: "occlusion", X: 200, Y: 0, W: 10, H: 10},
		{Kind: "division", X: 100, Y: 0, W: 4, H: 200},
		{Kind: "occlusion", X: 20, Y: 0, W: 10, H: 10},
	}, 400, 300)

	var kinds []string
	for _, r := range got {
		kinds = append(kinds, r.Kind)
	}
	want := []string{KindDivision, KindOcclusion, KindOcclusion}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("顺序应为 division 在前: %v", kinds)
		}
	}
	// 组内声明序: 两个 occlusion 按 X=200 → X=20 的原始顺序 (不是按坐标排序)。
	if got[1].X != 200 || got[2].X != 20 {
		t.Fatalf("组内应保持声明序: %#v", got[1:])
	}
	// ID 也应按分组后的次序生成。
	if got[0].ID != "fold-0" || got[1].ID != "occlusion-0" || got[2].ID != "occlusion-1" {
		t.Fatalf("ID 生成错误: %v %v %v", got[0].ID, got[1].ID, got[2].ID)
	}
}

// --- JSON 入口: 端到端 (解析 → 投递 → 内核屏表) ---

func TestReportDisplayFoldJSON(t *testing.T) {
	gfx.ResetDisplaysForTest()
	t.Cleanup(gfx.ResetDisplaysForTest)

	payload := `{
	  "posture": "half-open",
	  "sizeClass": {"width": "regular", "height": "regular"},
	  "hinge": {"x":1600,"y":0,"w":24,"h":2560,"orientation":"vertical"},
	  "regions": [
	    {"kind":"division","x":1600,"y":0,"w":24,"h":2560,"active":true},
	    {"kind":"occlusion","x":60,"y":0,"w":180,"h":180,"active":true}
	  ],
	  "width": 3240, "height": 2560
	}`
	if err := ReportDisplayFold(payload); err != nil {
		t.Fatalf("解析上报载荷失败: %v", err)
	}
	// 上报是投递到 GUI 线程的: 测试里手动 drain。
	gfx.DrainTasks()

	d, ok := gfx.PrimaryDisplayForTest()
	if !ok {		t.Fatal("上报后应能取到主屏")
	}
	if d.Posture != "half-open" {
		t.Fatalf("posture = %q, 期望 half-open", d.Posture)
	}
	if !d.Foldable {
		t.Fatalf("报了折痕应判 Foldable: %#v", d)
	}
	if d.Hinge == nil || d.Hinge.W != 24 || d.Hinge.Orientation != "vertical" {
		t.Fatalf("hinge 不正确: %#v", d.Hinge)
	}
	if len(d.Regions) != 2 {
		t.Fatalf("regions 应为 2 段: %#v", d.Regions)
	}
	if d.Regions[0].Kind != gfx.RegionDivision || d.Regions[0].ID != "fold-0" {
		t.Fatalf("第一段应为 division/fold-0: %#v", d.Regions[0])
	}
	if d.Regions[1].Kind != gfx.RegionOcclusion || !d.Regions[1].Active {
		t.Fatalf("第二段应为 active 的 occlusion: %#v", d.Regions[1])
	}
	if d.W != 3240 || d.H != 2560 {
		t.Fatalf("尺寸应写入: %d x %d", d.W, d.H)
	}
}

// --- JSON 入口: 坏 JSON 必须返回错误而不是静默 ---

func TestReportDisplayFoldBadJSON(t *testing.T) {
	if err := ReportDisplayFold("{"); err == nil {
		t.Fatal("畸形 JSON 应返回错误")
	}
	// 空字符串也是畸形 JSON (json.Unmarshal 会报 unexpected end of JSON input)。
	if err := ReportDisplayFold(""); err == nil {
		t.Fatal("空串应返回错误")
	}
}

// --- JSON 入口: 宿主只报姿态 (不报几何) 时不破坏既有屏表 ---

func TestReportDisplayFoldPostureOnlyKeepsHinge(t *testing.T) {
	gfx.ResetDisplaysForTest()
	t.Cleanup(gfx.ResetDisplaysForTest)

	// 先建立"半折 + 折痕"。
	full := `{"posture":"half-open","hinge":{"x":1600,"y":0,"w":24,"h":2560,"orientation":"vertical"}}`
	if err := ReportDisplayFold(full); err != nil {
		t.Fatal(err)
	}
	gfx.DrainTasks()
	// 再只报姿态 (宿主在折回平展时的常见做法)。
	if err := ReportDisplayFold(`{"posture":"flat"}`); err != nil {
		t.Fatal(err)
	}
	gfx.DrainTasks()

	d, _ := gfx.PrimaryDisplayForTest()
	if d.Posture != "flat" {
		t.Fatalf("posture 应更新为 flat: %q", d.Posture)
	}
	if d.Hinge == nil {
		t.Fatal("折痕不应随姿态清除 (否则折回去会变成等分 0.5)")
	}
}

// --- JSON 入口: 显示器归属 (id / display 别名) ---

func TestReportDisplayFoldDisplayRouting(t *testing.T) {
	gfx.ResetDisplaysForTest()
	t.Cleanup(gfx.ResetDisplaysForTest)

	// 鸿蒙上报器的真实形状: display: '0' (别名)。无后端枚举时首次上报
	// 定义整张表 ⇒ 表里应只有 ID "0" 这一块屏, 姿态落在它身上。
	if err := ReportDisplayFold(`{"display":"0","posture":"half-open","hinge":{"x":1600,"y":0,"w":24,"h":2560}}`); err != nil {
		t.Fatal(err)
	}
	gfx.DrainTasks()
	if d, ok := gfx.PrimaryDisplayForTest(); !ok || d.ID != "0" || d.Posture != "half-open" {
		t.Fatalf("display 别名应路由到 ID 0: ok=%v id=%q posture=%q", ok, d.ID, d.Posture)
	}

	// `id` 显式优先于 `display`。
	gfx.ResetDisplaysForTest()
	if err := ReportDisplayFold(`{"id":"ext-1","display":"0","posture":"flat"}`); err != nil {
		t.Fatal(err)
	}
	gfx.DrainTasks()
	if d, _ := gfx.PrimaryDisplayForTest(); d.ID != "ext-1" {
		t.Fatalf("id 应优先于 display: %q", d.ID)
	}

	// 空归属保持原行为: 落到内核默认屏 (无后端时是虚拟屏)。
	gfx.ResetDisplaysForTest()
	if err := ReportDisplayFold(`{"posture":"flat"}`); err != nil {
		t.Fatal(err)
	}
	gfx.DrainTasks()
	if d, _ := gfx.PrimaryDisplayForTest(); d.ID == "" {
		t.Fatalf("缺省归属应落到默认屏 (virtual-0), 不该是空 ID: %q", d.ID)
	}
}

// --- 与内核的类型转换: 保留区字段完整搬运 ---

func TestToDisplayRegionsCarriesFields(t *testing.T) {
	rs := NormalizeRegions([]RawRegion{
		{Kind: "division", X: 100, Y: 0, W: 4, H: 200, Active: b(false)},
	}, 400, 300)
	out := ToDisplayRegions(rs)
	if len(out) != 1 {
		t.Fatalf("转换丢段: %#v", out)
	}
	r := out[0]
	if r.ID != "fold-0" || r.Kind != gfx.RegionDivision || r.X != 100 || r.Y != 0 ||
		r.W != 4 || r.H != 200 || r.Active {
		t.Fatalf("字段搬运不完整: %#v", r)
	}
	if ToDisplayRegions(nil) != nil {
		t.Fatal("空输入应返回 nil")
	}
}

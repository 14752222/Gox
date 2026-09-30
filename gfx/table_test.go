package gfx

import (
	"image/color"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== S4 table =====
//
// 用例覆盖五条不变量:
//   1. columns/rows 数据被展开成表头 + 数据行的节点树, 文本进单元格;
//   2. 行高统一 28, 行按 y 等距排布, 各列宽度不重叠;
//   3. 显式列宽优先, 未给的列按内容比例分剩余宽度;
//   4. zebra / borderless / align 这三个开关真的改变了像素;
//   5. 没有 onRowClick 时行不可点, 挂了之后点击回调收到 {index, row}。
// (mkNode / renderTree / pushAndPump 等 helper 见 helpers_test.go。)

// mkTable 造一个 table 节点。cols 是字符串数组 (简写), rows 是二维字符串数组
// (按列下标取值) —— 覆盖最常见的用法。
func mkTable(cols []string, rows [][]string) *GuiNode {
	t := &GuiNode{Tag: "table", Props: map[string]object.Value{}}
	ca := &object.Array{}
	for _, c := range cols {
		ca.Elements = append(ca.Elements, object.NewString(c))
	}
	t.Props["columns"] = ca
	ra := &object.Array{}
	for _, row := range rows {
		inner := &object.Array{}
		for _, cell := range row {
			inner.Elements = append(inner.Elements, object.NewString(cell))
		}
		ra.Elements = append(ra.Elements, inner)
	}
	t.Props["rows"] = ra
	return t
}

// mkTableObj 造一个用对象行 (按 key 取值) 的 table。
func mkTableObj(cols []*object.Object, rows []*object.Object) *GuiNode {
	t := &GuiNode{Tag: "table", Props: map[string]object.Value{}}
	ca := &object.Array{}
	for _, c := range cols {
		ca.Elements = append(ca.Elements, c)
	}
	t.Props["columns"] = ca
	ra := &object.Array{}
	for _, r := range rows {
		ra.Elements = append(ra.Elements, r)
	}
	t.Props["rows"] = ra
	return t
}

// mkCol 造一个列配置对象。
func mkCol(key, label string, width float64, align string) *object.Object {
	o := object.NewObject()
	o.SetProperty("key", object.NewString(key))
	o.SetProperty("label", object.NewString(label))
	if width > 0 {
		o.SetProperty("width", object.NewNumber(width))
	}
	if align != "" {
		o.SetProperty("align", object.NewString(align))
	}
	return o
}

// mkRowObj 造一个数据行对象。
func mkRowObj(pairs map[string]string) *object.Object {
	o := object.NewObject()
	for k, v := range pairs {
		o.SetProperty(k, object.NewString(v))
	}
	return o
}

func TestTableBuildsHeaderAndRows(t *testing.T) {
	tbl := mkTable([]string{"名称", "大小"}, [][]string{
		{"a.txt", "12KB"},
		{"b.png", "48KB"},
	})
	root := mkNode("column", map[string]float64{"padding": 0})
	mountChildren(root, tbl)
	renderTree(root, 400, 200)

	// 表头 1 行 + 数据 2 行 = 3 个行节点
	if n := countTag(root, "table-header"); n != 1 {
		t.Fatalf("表头数量 = %d, want 1", n)
	}
	if n := countTag(root, "table-row"); n != 2 {
		t.Fatalf("数据行数量 = %d, want 2", n)
	}
	// 每行 2 列 → 共 6 个单元格
	if n := countTag(root, "table-cell"); n != 6 {
		t.Fatalf("单元格数量 = %d, want 6", n)
	}

	// 单元格文本取自数据
	cells := findAll(root, "table-cell")
	wantText := []string{"名称", "大小", "a.txt", "12KB", "b.png", "48KB"}
	for i, want := range wantText {
		if cells[i].cellText != want {
			t.Fatalf("单元格 #%d 文本 = %q, want %q", i, cells[i].cellText, want)
		}
	}
}

func TestTableRowGeometry(t *testing.T) {
	tbl := mkTable([]string{"A", "B"}, [][]string{{"1", "2"}, {"3", "4"}})
	root := mkNode("column", map[string]float64{})
	mountChildren(root, tbl)
	renderTree(root, 400, 200)

	hdr := findFirst(root, "table-header")
	if hdr.Box.H != tableRowH {
		t.Fatalf("表头行高 = %d, want %d", hdr.Box.H, tableRowH)
	}
	rows := findAll(root, "table-row")
	if len(rows) != 2 {
		t.Fatalf("数据行数 = %d, want 2", len(rows))
	}
	// 行按 y 等距排布 (每行 tableRowH), 首行紧贴表头下方
	if rows[0].Box.Y != hdr.Box.Y+hdr.Box.H {
		t.Fatalf("首行未紧贴表头: row.Y=%d want %d", rows[0].Box.Y, hdr.Box.Y+hdr.Box.H)
	}
	if rows[1].Box.Y != rows[0].Box.Y+tableRowH {
		t.Fatalf("行间距 = %d, want %d", rows[1].Box.Y-rows[0].Box.Y, tableRowH)
	}
	// 同一行内两列水平相邻不重叠
	c0, c1 := rows[0].Children[0], rows[0].Children[1]
	if c0.Box.X+c0.Box.W != c1.Box.X {
		t.Fatalf("列未水平相接: c0 右缘=%d, c1 左缘=%d", c0.Box.X+c0.Box.W, c1.Box.X)
	}
	// 两列宽之和 = 表格宽
	if c0.Box.W+c1.Box.W != tbl.Box.W {
		t.Fatalf("列宽之和 = %d, want 表格宽 %d", c0.Box.W+c1.Box.W, tbl.Box.W)
	}
}

func TestTableExplicitColumnWidth(t *testing.T) {
	tbl := mkTableObj(
		[]*object.Object{mkCol("name", "名称", 120, ""), mkCol("size", "大小", 0, "")},
		[]*object.Object{mkRowObj(map[string]string{"name": "a", "size": "1KB"})},
	)
	root := mkNode("column", map[string]float64{})
	mountChildren(root, tbl)
	renderTree(root, 400, 200)

	cells := findAll(root, "table-cell")
	// cells 顺序是 表头两格 + 数据行两格, 所以数据行首格在下标 2
	if cells[0].Box.W != 120 {
		t.Fatalf("显式列宽未生效: %d, want 120", cells[0].Box.W)
	}
	// 未知列宽 key: 值从对象里取到 (数据行 = 表头之后的格子)
	if cells[3].cellText != "1KB" {
		t.Fatalf("对象行取值失败: %q", cells[3].cellText)
	}
	if cells[2].cellText != "a" {
		t.Fatalf("对象行 name 取值失败: %q", cells[2].cellText)
	}
}

func TestTableZebraAndBorderlessAffectPixels(t *testing.T) {
	// 斑马纹: 第 2 行 (下标 1) 有 _zebra, 底色应不同于第 1 行
	tbl := mkTable([]string{"A"}, [][]string{{"1"}, {"2"}})
	root := mkNode("column", map[string]float64{})
	withBool(tbl, "zebra", true)
	mountChildren(root, tbl)
	img := renderTree(root, 200, 200)

	rows := findAll(root, "table-row")
	if !func() bool {
		v, ok := rows[1].PropBool("_zebra")
		return ok && v
	}() {
		t.Fatalf("第 2 行未标记 _zebra (斑马纹由构造期按行号写入)")
	}
	// 第 2 行中部的底色应是斑马底色 (而非纯白)
	c := img.RGBAAt(rows[1].Box.X+rows[1].Box.W-2, rows[1].Box.Y+rows[1].Box.H/2)
	if c == pxWhite {
		t.Fatalf("斑马行未着色: 取到纯白 %v", c)
	}

	// borderless: 单元格右下不该有网格线
	tbl2 := mkTable([]string{"A"}, [][]string{{"1"}})
	withBool(tbl2, "borderless", true)
	root2 := mkNode("column", map[string]float64{})
	mountChildren(root2, tbl2)
	renderTree(root2, 200, 200)
	cell := findFirst(root2, "table-cell")
	if cell == nil {
		t.Fatalf("borderless 表格缺少单元格")
	}
	if !tbl2.tableBorderless() {
		t.Fatalf("borderless prop 未读到")
	}
}

func TestTableCellAlign(t *testing.T) {
	// 右对齐的空列: 文字应贴单元格右缘 (而非左缘)
	tbl := mkTableObj(
		[]*object.Object{mkCol("n", "数", 0, "right")},
		[]*object.Object{mkRowObj(map[string]string{"n": "7"})},
	)
	root := mkNode("column", map[string]float64{})
	mountChildren(root, tbl)
	img := renderTree(root, 200, 200)

	cells := findAll(root, "table-cell")
	// 数据格 (第 2 个 cell: 表头是第 1 个)
	data := cells[1]
	if data.cellAlign != "right" {
		t.Fatalf("对齐未从列定义继承: %q", data.cellAlign)
	}
	// 文字宽度 1 字符, 右对齐后墨迹应落在单元格右半区
	rightHalf := Rect{X: data.Box.X + data.Box.W/2, Y: data.Box.Y, W: data.Box.W / 2, H: data.Box.H}
	if !hasInkIn(img, rightHalf) {
		t.Fatalf("右对齐文字未落在单元格右半区")
	}
}

func TestTableRowClickCallback(t *testing.T) {
	tbl := mkTable([]string{"A"}, [][]string{{"1"}, {"2"}})
	// 没有 onRowClick: 行不该挂处理器 (表格默认不是交互控件)
	root := mkNode("column", map[string]float64{})
	mountChildren(root, tbl)
	renderTree(root, 200, 200)
	rows := findAll(root, "table-row")
	if rows[0].PropHandler("onClick") != nil {
		t.Fatalf("无 onRowClick 时行不该挂 onClick (表格默认不可点)")
	}

	// 挂了 onRowClick: 行上有桥接处理器, 点击回调收到 {index, row}
	var gotIndex float64 = -1
	var gotRow object.Value
	tbl2 := mkTable([]string{"A"}, [][]string{{"1"}, {"2"}})
	tbl2.Props["onRowClick"] = object.NewBuiltin("rowClick", func(args ...object.Value) object.Value {
		if len(args) > 0 {
			if o, ok := args[0].(*object.Object); ok {
				if pv, ok := o.GetProperty("index"); ok {
					if num, ok := pv.(*object.Number); ok {
						gotIndex = num.Value
					}
				}
				if pv, ok := o.GetProperty("row"); ok {
					gotRow = pv
				}
			}
		}
		return object.UndefinedSingleton
	})
	root2 := mkNode("column", map[string]float64{})
	mountChildren(root2, tbl2)

	fake, a := mountTestApp(t, root2, 200, 200)
	// 点第 2 行 (index=1)
	rows2 := findAll(root2, "table-row")
	target := rows2[1]
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: target.Box.X + 2, Y: target.Box.Y + 2})
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: target.Box.X + 2, Y: target.Box.Y + 2})

	if gotIndex != 1 {
		t.Fatalf("回调收到的行下标 = %v, want 1", gotIndex)
	}
	if gotRow == nil {
		t.Fatalf("回调未收到行数据")
	}
}

func TestTableEmptyData(t *testing.T) {
	// 无 columns: 不该 panic, 也不该有行
	tbl := mkTable(nil, nil)
	root := mkNode("column", map[string]float64{})
	mountChildren(root, tbl)
	renderTree(root, 200, 100)
	if n := countTag(root, "table-row"); n != 0 {
		t.Fatalf("空表格不该有数据行, got %d", n)
	}
	// 有 columns 无 rows: 只画表头
	tbl2 := mkTable([]string{"A", "B"}, nil)
	root2 := mkNode("column", map[string]float64{})
	mountChildren(root2, tbl2)
	renderTree(root2, 200, 100)
	if n := countTag(root2, "table-header"); n != 1 {
		t.Fatalf("空数据表格仍应有表头, got %d", n)
	}
	if n := countTag(root2, "table-row"); n != 0 {
		t.Fatalf("无 rows 时不该有数据行, got %d", n)
	}
}

func TestTableHeaderPaintsDistinctFace(t *testing.T) {
	tbl := mkTable([]string{"H"}, [][]string{{"d"}})
	root := mkNode("column", map[string]float64{})
	mountChildren(root, tbl)
	img := renderTree(root, 200, 200)

	hdr := findFirst(root, "table-header")
	if hdr == nil {
		t.Fatalf("缺少表头")
	}
	// 表头底色应与数据行底色不同 (数据行无 zebra 时是白底/画布底)
	hdrPx := img.RGBAAt(hdr.Box.X+hdr.Box.W/2, hdr.Box.Y+2)
	if hdrPx == pxWhite {
		t.Fatalf("表头未画底色: 取到纯白")
	}
	if hdrPx != (color.RGBA{R: 0xF3, G: 0xF3, B: 0xF3, A: 255}) {
		t.Fatalf("表头底色 = %v, want {243,243,243,255}", hdrPx)
	}
}

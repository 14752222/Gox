package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
	"image/color"
)

// ===== 装饰绘制 (§四 绘制缺口第一批): 圆角 / 渐变 / 阴影 / 边框宽度 =====

func TestDecorRadiusCutsCorners(t *testing.T) {
	// 40x30 圆角 10: 角上的像素不被覆盖 (留在白底), 内部照常填充
	root := mkNode("column", nil)
	box := mkNode("rect", map[string]float64{"width": 40, "height": 30, "radius": 10})
	box.Props["background"] = object.NewString("#3355aa")
	root.Children = []*GuiNode{box}
	img := renderTree(root, 60, 40)

	assertPx(t, img, 0, 0, pxWhite, "圆角: 左上角像素")
	assertPx(t, img, 39, 29, pxWhite, "圆角: 右下角像素")
	assertPx(t, img, 20, 15, color.RGBA{R: 51, G: 85, B: 170, A: 255}, "圆角: 中心填充")
	// 角带内侧 (圆弧以内) 也应被覆盖: (5,5) 距角心 (10,10) √50≈7.07 < 10
	if got := img.RGBAAt(5, 5); got == pxWhite {
		t.Fatalf("圆角内 (5,5) 应被填充, 得到白底")
	}
	// 直角边缘不受圆角影响 (半径带之外的边)
	assertPx(t, img, 20, 0, color.RGBA{R: 51, G: 85, B: 170, A: 255}, "圆角: 顶边中段")
}

func TestDecorRadiusClampedToHalfMin(t *testing.T) {
	// 半径超过短边一半 → 钳到 min/2 (胶囊形), 不产生负尺寸
	root := mkNode("column", nil)
	box := mkNode("rect", map[string]float64{"width": 30, "height": 20, "radius": 99})
	box.Props["background"] = object.NewString("#3355aa")
	root.Children = []*GuiNode{box}
	img := renderTree(root, 60, 40)
	assertPx(t, img, 0, 0, pxWhite, "胶囊: 角不覆盖")
	assertPx(t, img, 15, 10, color.RGBA{R: 51, G: 85, B: 170, A: 255}, "胶囊: 中心")
}

func TestDecorGradientHorizontal(t *testing.T) {
	root := mkNode("column", nil)
	bar := mkNode("rect", map[string]float64{"width": 100, "height": 20})
	bar.Props["background"] = object.NewString("linear-gradient(to right, #000000, #ffffff)")
	root.Children = []*GuiNode{bar}
	img := renderTree(root, 120, 40)

	if c := img.RGBAAt(1, 10); c.R > 30 || c.A != 255 {
		t.Fatalf("渐变左端应接近黑: %v", c)
	}
	if c := img.RGBAAt(98, 10); c.R < 225 {
		t.Fatalf("渐变右端应接近白: %v", c)
	}
	// 中点: 约一半 (AA/插值取整允许 ±6)
	if c := img.RGBAAt(50, 10); c.R < 120 || c.R > 135 {
		t.Fatalf("渐变中点应约 127: %v", c)
	}
}

func TestDecorGradientVerticalDefaultDir(t *testing.T) {
	// 无方向词 → to bottom
	root := mkNode("column", nil)
	bar := mkNode("rect", map[string]float64{"width": 20, "height": 60})
	bar.Props["background"] = object.NewString("linear-gradient(#ff0000, #0000ff)")
	root.Children = []*GuiNode{bar}
	img := renderTree(root, 60, 80)

	if c := img.RGBAAt(10, 1); c.R < 200 || c.B > 60 {
		t.Fatalf("渐变顶端应偏红: %v", c)
	}
	if c := img.RGBAAt(10, 58); c.B < 200 || c.R > 60 {
		t.Fatalf("渐变底端应偏蓝: %v", c)
	}
}

func TestDecorGradientInvalidFallsBack(t *testing.T) {
	// 单色标不是合法渐变, 也不是合法颜色 → 无填充面 (保持白底)
	root := mkNode("column", nil)
	bar := mkNode("rect", map[string]float64{"width": 30, "height": 20})
	bar.Props["background"] = object.NewString("linear-gradient(#ffffff)")
	root.Children = []*GuiNode{bar}
	img := renderTree(root, 60, 40)
	assertPx(t, img, 15, 10, pxWhite, "非法渐变应无填充")
}

func TestDecorShadowBelowBox(t *testing.T) {
	root := mkNode("column", nil)
	box := mkNode("rect", map[string]float64{"width": 40, "height": 20})
	box.Props["background"] = object.NewString("#ffffff")
	box.Props["border"] = object.NewString("#333333")
	box.Props["shadow"] = shadowObj(0, 6, 0, "#00000080")
	root.Children = []*GuiNode{box}
	img := renderTree(root, 60, 40)

	// 盒子在 (0,0,40,20), 偏移 6 → 阴影最后一行 y=25 应被压暗 (50% 黑叠白 ≈ 128)
	if c := img.RGBAAt(20, 25); c.R > 160 {
		t.Fatalf("盒下方应有阴影: %v", c)
	}
	// 偏移之外 (y=27+) 无模糊时不扩散
	assertPx(t, img, 20, 27, pxWhite, "无模糊阴影不扩散")
	// 盒子内部被背景覆盖 (阴影先画; 盒行 0..19, (20,10) 是内部)
	if c := img.RGBAAt(20, 10); c != (color.RGBA{R: 255, G: 255, B: 255, A: 255}) {
		t.Fatalf("盒内应为纯白背景: %v", c)
	}
}

func TestDecorShadowBlurSoftens(t *testing.T) {
	root := mkNode("column", nil)
	box := mkNode("rect", map[string]float64{"width": 40, "height": 20})
	box.Props["background"] = object.NewString("#ffffff")
	box.Props["shadow"] = shadowObj(0, 6, 6, "#00000080")
	root.Children = []*GuiNode{box}
	img := renderTree(root, 70, 50)

	// 有模糊: 阴影中心 (y=25) 比软尾 (y=29, 模糊半径 6 内) 更暗
	near := img.RGBAAt(20, 25).R
	far := img.RGBAAt(20, 29).R
	if near >= far {
		t.Fatalf("模糊阴影应近浓远淡: near=%d far=%d", near, far)
	}
	if far >= 255 {
		t.Fatalf("模糊阴影应有软尾: far=%d", far)
	}
	// 盒子更上方不受影响
	assertPx(t, img, 20, 0, pxWhite, "阴影不影响盒上方")
}

func TestDecorBorderWidth(t *testing.T) {
	root := mkNode("column", nil)
	box := mkNode("rect", map[string]float64{"width": 30, "height": 24, "borderWidth": 3})
	box.Props["border"] = object.NewString("#c0392b")
	root.Children = []*GuiNode{box}
	img := renderTree(root, 60, 40)

	edge := color.RGBA{R: 192, G: 57, B: 43, A: 255}
	assertPx(t, img, 0, 0, edge, "borderWidth 3: (0,0)")
	assertPx(t, img, 1, 1, edge, "borderWidth 3: (1,1)")
	assertPx(t, img, 2, 2, edge, "borderWidth 3: (2,2)")
	assertPx(t, img, 3, 3, pxWhite, "borderWidth 3: (3,3) 应是内部")
}

func TestDecorBorderDashed(t *testing.T) {
	root := mkNode("column", nil)
	box := mkNode("rect", map[string]float64{"width": 40, "height": 20, "borderWidth": 2})
	box.Props["border"] = object.NewString("#c0392b")
	box.Props["borderStyle"] = object.NewString("dashed")
	root.Children = []*GuiNode{box}
	img := renderTree(root, 60, 40)

	edge := color.RGBA{R: 192, G: 57, B: 43, A: 255}
	// dash=2*bw=4, gap=4: 顶边 x∈[0,4) 着色, x∈[4,8) 空
	assertPx(t, img, 2, 1, edge, "dashed 段内")
	if got := img.RGBAAt(6, 1); got == edge {
		t.Fatalf("dashed 空隙内不应着色: %v", got)
	}
}

func TestDecorRadiusFollowsBorder(t *testing.T) {
	// 圆角 + 边框: 环带跟随圆角 (角上无边框色, 边中段有)
	root := mkNode("column", nil)
	box := mkNode("rect", map[string]float64{"width": 40, "height": 30, "radius": 10, "borderWidth": 2})
	box.Props["border"] = object.NewString("#c0392b")
	box.Props["background"] = object.NewString("#ffffff")
	root.Children = []*GuiNode{box}
	img := renderTree(root, 60, 40)

	assertPx(t, img, 0, 0, pxWhite, "圆角边框: 角不覆盖")
	assertPx(t, img, 20, 0, color.RGBA{R: 192, G: 57, B: 43, A: 255}, "圆角边框: 顶边中段")
	assertPx(t, img, 20, 2, color.RGBA{R: 255, G: 255, B: 255, A: 255}, "圆角边框: 边框内是背景")
}

// shadowObj 构造 shadow prop 值。
func shadowObj(x, y, blur int, col string) object.Value {
	o := object.NewObject()
	o.SetProperty("x", object.NewNumber(float64(x)))
	o.SetProperty("y", object.NewNumber(float64(y)))
	o.SetProperty("blur", object.NewNumber(float64(blur)))
	o.SetProperty("color", object.NewString(col))
	return o
}

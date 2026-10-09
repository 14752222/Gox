package gfx

import (
	"image"
	"image/color"
)

// S4/T09 icon 内置图标组件。
//
//	<icon name="home" />                    // 缺省 16px, 继承文字色
//	<icon name="search" size={24} />
//	<icon name="heart" size={20} color="#e01b24" />
//
// **为什么把图标集内置进内核** (而不是走 testdata/ui/icons.js 的 canvas 自绘
// 套件): 图标是最基础的视觉原件, 每个 app 都要用; 让每个项目各写一份
// 24x24 的 draw 函数, 既重复又难统一观感。内置之后 `<icon name=... />` 一行
// 就能用, 且三平台零依赖 (纯光栅原语, 无字体/无 SVG/无图片资源)。
//
// **风格与 testdata/ui/icons.js 完全对齐** (同一套 24x24 逻辑网格、同一批
// 图标名、同样的"直线 + 矩形 + 圆"像素风): 那套 JS 版本仍可用于更复杂的
// 组合图标, 但基础图标走内核即可, 两者可以混用。
//
// 实现要点: 所有坐标在 24 单位网格里算, 用 u = size/24 缩放到实际像素。
// 圆/线都取整到像素 —— 内核光栅不做抗锯齿 (与 canvas 套件同一个取舍)。

const (
	// iconDefaultSize 是缺省图标边长 (与常见 UI 的行内图标尺寸一致)。
	iconDefaultSize = 16
	// iconGrid 是图标逻辑网格边长 (坐标都按它归一)。
	iconGrid = 24
)

// iconSize 读 size prop (逻辑单位, 内核换算成设备像素 —— 见 density.go),
// 非法/缺省回落 iconDefaultSize (内置度量, 设备像素)。
func (n *GuiNode) iconSize() int {
	if v, ok := n.PropNum("size"); ok && v > 0 {
		return dpToPx(v) // 逻辑值 → 设备像素 (density.go)
	}
	return iconDefaultSize
}

// iconName 读 name prop。
func (n *GuiNode) iconName() string {
	s, _ := n.PropStr("name")
	return s
}

// intrinsicIcon 图标是正方形, 边长按 size。
func intrinsicIcon(n *GuiNode) (w, h int) {
	s := n.iconSize()
	return s, s
}

// iconColor 图标颜色: color prop (含继承) → 缺省文字色。
// 直接复用 textColor 的继承链, 于是 <button color="#fff"><icon .../></button>
// 的图标会自动跟按钮文字同色。
func iconColor(n *GuiNode) color.RGBA {
	return n.textColor()
}

// paintIcon 画图标。未知图标名不报错也不画 (静默空) —— 与 IconView 的
// "其余 → 不渲染"同一口径: 图标名拼错最坏的后果应当是"那里空着", 不该崩。
func paintIcon(img *image.RGBA, n *GuiNode, disabled bool) {
	name := n.iconName()
	fn := builtinIcons[name]
	if fn == nil {
		return
	}
	c := tint(iconColor(n), disabled)
	fn(img, n.Box, n.iconSize(), c)
}

// iconUnit 返回逻辑单位 u = 实际边长 / 24, 并给出把逻辑坐标映射到像素的
// 两个取整辅助。用闭包而不是每处手写 n.Box.X + int(a*u): 让图标定义读起来
// 就是"24 网格里的坐标", 与 testdata/ui/icons.js 的定义一一对应。
type iconCtx struct {
	img  *image.RGBA
	box  Rect
	u    float64
	size int
}

func newIconCtx(img *image.RGBA, box Rect, size int) iconCtx {
	return iconCtx{img: img, box: box, u: float64(size) / float64(iconGrid), size: size}
}

// px 把逻辑坐标 (gx, gy) 映射到窗口像素。
func (c iconCtx) px(gx, gy float64) (int, int) {
	return c.box.X + int(gx*c.u), c.box.Y + int(gy*c.u)
}

// line 画一条粗线段 (厚 1 像素; 小图标里 1px 线最清晰)。
func (c iconCtx) line(x0, y0, x1, y1 float64, col color.RGBA) {
	ax, ay := c.px(x0, y0)
	bx, by := c.px(x1, y1)
	fillLine(c.img, ax, ay, bx, by, 1, col)
}

// rect 画空心矩形 (1px 边)。
func (c iconCtx) rect(x, y, w, h float64, col color.RGBA) {
	ax, ay := c.px(x, y)
	bw := int(w * c.u)
	bh := int(h * c.u)
	if bw < 1 {
		bw = 1
	}
	if bh < 1 {
		bh = 1
	}
	StrokeRect(c.img, Rect{X: ax, Y: ay, W: bw, H: bh}, col)
}

// fillRect 画实心矩形。
func (c iconCtx) fillRect(x, y, w, h float64, col color.RGBA) {
	ax, ay := c.px(x, y)
	bw := int(w * c.u)
	bh := int(h * c.u)
	if bw < 1 {
		bw = 1
	}
	if bh < 1 {
		bh = 1
	}
	FillRect(c.img, Rect{X: ax, Y: ay, W: bw, H: bh}, col)
}

// circle 画实心圆。
func (c iconCtx) circle(cx, cy, r float64, col color.RGBA) {
	ax, ay := c.px(cx, cy)
	rr := int(r * c.u)
	if rr < 1 {
		rr = 1
	}
	FillCircle(c.img, ax, ay, rr, col)
}

// ring 画空心圆环。
func (c iconCtx) ring(cx, cy, r float64, col color.RGBA) {
	ax, ay := c.px(cx, cy)
	rr := int(r * c.u)
	if rr < 1 {
		rr = 1
	}
	StrokeCircle(c.img, ax, ay, rr, col)
}

// builtinIcons 是内置图标集: 名字 → 绘制函数。
//
// 每个绘制函数都在 24x24 逻辑网格里描述形状, 坐标与 testdata/ui/icons.js
// 逐条对应 (便于两套并存时观感一致)。刻意只收录"最通用"的一批, 冷门图标
// 交给项目自己的 canvas 组件 (内核不做无限扩张的图标库)。
type iconDrawFn func(img *image.RGBA, box Rect, size int, c color.RGBA)

var builtinIcons = map[string]iconDrawFn{
	// 房子: 屋顶两笔 + 方体 + 门
	"home": func(img *image.RGBA, box Rect, size int, c color.RGBA) {
		k := newIconCtx(img, box, size)
		k.line(3, 11, 12, 4, c)
		k.line(12, 4, 21, 11, c)
		k.rect(5, 11, 14, 9, c)
		k.fillRect(10, 14, 4, 6, c)
	},
	// 放大镜: 圆环 + 斜柄
	"search": func(img *image.RGBA, box Rect, size int, c color.RGBA) {
		k := newIconCtx(img, box, size)
		k.ring(10, 10, 6, c)
		k.line(14.5, 14.5, 20, 20, c)
		k.line(15.5, 13.5, 21, 19, c)
	},
	// 人: 头圆 + 肩圆 (下半被裁掉, 正好是半身像)
	"user": func(img *image.RGBA, box Rect, size int, c color.RGBA) {
		k := newIconCtx(img, box, size)
		k.circle(12, 7.5, 4, c)
		k.circle(12, 22, 8, c)
	},
	// 齿轮: 外圈 + 内圈 + 8 根齿
	"gear": func(img *image.RGBA, box Rect, size int, c color.RGBA) {
		k := newIconCtx(img, box, size)
		k.ring(12, 12, 6, c)
		k.circle(12, 12, 2, c)
		// 齿用 8 个方向的短线近似 (无三角函数依赖, 用固定方向向量)。
		dirs := [8][2]float64{
			{0, -1}, {0.707, -0.707}, {1, 0}, {0.707, 0.707},
			{0, 1}, {-0.707, 0.707}, {-1, 0}, {-0.707, -0.707},
		}
		for _, d := range dirs {
			k.line(12+d[0]*6.5, 12+d[1]*6.5, 12+d[0]*9, 12+d[1]*9, c)
		}
	},
	// 铃铛: 上半圆 + 两竖 + 底横 + 铃舌
	"bell": func(img *image.RGBA, box Rect, size int, c color.RGBA) {
		k := newIconCtx(img, box, size)
		k.ring(12, 10, 6, c)
		k.line(6, 10, 6, 15, c)
		k.line(18, 10, 18, 15, c)
		k.line(5, 15, 19, 15, c)
		k.circle(12, 18.5, 1.5, c)
	},
	// 气泡: 方框 + 左下尾巴
	"chat": func(img *image.RGBA, box Rect, size int, c color.RGBA) {
		k := newIconCtx(img, box, size)
		k.rect(4, 5, 16, 11, c)
		k.line(4, 16, 4, 20, c)
		k.line(4, 20, 9, 16, c)
	},
	// 文件夹: 标签耳 + 方体
	"folder": func(img *image.RGBA, box Rect, size int, c color.RGBA) {
		k := newIconCtx(img, box, size)
		k.line(3, 7, 9, 7, c)
		k.line(9, 7, 11, 9, c)
		k.rect(3, 9, 18, 10, c)
		k.line(3, 7, 3, 19, c)
	},
	// 日历: 方框 + 横线 + 两根挂钉
	"calendar": func(img *image.RGBA, box Rect, size int, c color.RGBA) {
		k := newIconCtx(img, box, size)
		k.rect(4, 6, 16, 14, c)
		k.line(4, 10, 20, 10, c)
		k.line(8, 4, 8, 8, c)
		k.line(16, 4, 16, 8, c)
	},
	// 心: 两圆 + 交叉线拼下尖
	"heart": func(img *image.RGBA, box Rect, size int, c color.RGBA) {
		k := newIconCtx(img, box, size)
		k.circle(8.5, 9, 4, c)
		k.circle(15.5, 9, 4, c)
		k.line(5, 11, 12, 19, c)
		k.line(12, 19, 19, 11, c)
	},
	// 加号
	"plus": func(img *image.RGBA, box Rect, size int, c color.RGBA) {
		k := newIconCtx(img, box, size)
		k.line(12, 5, 12, 19, c)
		k.line(5, 12, 19, 12, c)
	},
	// 减号
	"minus": func(img *image.RGBA, box Rect, size int, c color.RGBA) {
		k := newIconCtx(img, box, size)
		k.line(5, 12, 19, 12, c)
	},
	// 叉
	"close": func(img *image.RGBA, box Rect, size int, c color.RGBA) {
		k := newIconCtx(img, box, size)
		k.line(6, 6, 18, 18, c)
		k.line(18, 6, 6, 18, c)
	},
	// 对勾
	"check": func(img *image.RGBA, box Rect, size int, c color.RGBA) {
		k := newIconCtx(img, box, size)
		k.line(5, 12.5, 10, 17.5, c)
		k.line(10, 17.5, 19, 6.5, c)
	},
	// 左箭头 (返回)
	"arrow-left": func(img *image.RGBA, box Rect, size int, c color.RGBA) {
		k := newIconCtx(img, box, size)
		k.line(19, 12, 5, 12, c)
		k.line(5, 12, 11, 6, c)
		k.line(5, 12, 11, 18, c)
	},
	// 右箭头
	"arrow-right": func(img *image.RGBA, box Rect, size int, c color.RGBA) {
		k := newIconCtx(img, box, size)
		k.line(5, 12, 19, 12, c)
		k.line(19, 12, 13, 6, c)
		k.line(19, 12, 13, 18, c)
	},
}

// IconNames 返回内置图标名清单 (升序)。给文档/示例枚举用 ——
// 与 testdata/ui/icons.js 的 ICON_KEYS 对应, 便于两边对齐。
func IconNames() []string {
	out := make([]string, 0, len(builtinIcons))
	for name := range builtinIcons {
		out = append(out, name)
	}
	// 稳定排序, 便于文档与测试断言。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1] > out[j]; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

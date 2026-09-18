package gfx

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// TestImageDemoScript 跑一遍 P2-9 的演示脚本 —— 它是 `<image>` 的真实 JSX 用法,
// 验证"五种形态都能编译、能挂载、几何与像素都对"。
//
// 与其他演示脚本不同, image 的 `src` 是**文件路径**, 而演示脚本按"从仓库根目录
// 运行"编写 (README 的惯例是 `go run . testdata/xxx.js`)。测试进程的 cwd 是
// `gfx/`, 所以这里临时切到仓库根再跑。gfx 包内没有并行用例 (无 `t.Parallel`),
// 切换 cwd 是安全的; 断言全部在恢复 cwd 之前完成。
func TestImageDemoScript(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	if err := os.Chdir(".."); err != nil {
		t.Fatalf("切换到仓库根目录: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Fatalf("恢复 cwd: %v", err)
		}
	})

	srcBytes, err := os.ReadFile(filepath.Join("testdata", "image_demo.js"))
	if err != nil {
		t.Fatalf("读取演示脚本: %v", err)
	}
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(string(srcBytes))
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	appMu.Lock()
	root := activeApp.root
	appMu.Unlock()

	round := 0
	pump := func(maxWait time.Duration) bool {
		round++
		// 每轮补一个无害唤醒事件: 队列为空时事件泵会按 WaitEvents 的语义长阻塞。
		fake.push(Event{Kind: EventMouseLeave})
		if round > 1 {
			fake.push(Event{Kind: EventClose})
		}
		return Pump(maxWait)
	}
	if err := v.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if Active() {
		t.Fatalf("关闭窗口后应用未退出")
	}

	// ===== 几何: 五种形态的盒子尺寸 =====
	imgs := findAll(root, "image")
	if len(imgs) != 5 {
		t.Fatalf("演示脚本应有 5 个 image, 实际 %d", len(imgs))
	}
	if b := imgs[0].Box; b.W != 32 || b.H != 32 {
		t.Fatalf("不给尺寸应用自然尺寸 32x32, 实际 %dx%d", b.W, b.H)
	}
	if b := imgs[1].Box; b.W != 96 || b.H != 96 {
		t.Fatalf("放大态应是 96x96, 实际 %dx%d", b.W, b.H)
	}
	if b := imgs[2].Box; b.W != 16 || b.H != 16 {
		t.Fatalf("缩小态应是 16x16, 实际 %dx%d", b.W, b.H)
	}
	if b := imgs[3].Box; b.W != 96 || b.H != 48 {
		t.Fatalf("坏路径的显式尺寸应原样生效, 实际 %dx%d", b.W, b.H)
	}
	if b := imgs[4].Box; b.W != 64 || b.H != 64 {
		t.Fatalf("禁用态应是 64x64, 实际 %dx%d", b.W, b.H)
	}

	// ===== 像素: 自己画一帧验证 (画布给足高度, 避免裁剪干扰断言) =====
	const cw, ch = 400, 480
	img := image.NewRGBA(image.Rect(0, 0, cw, ch))
	FillRect(img, Rect{0, 0, cw, ch}, color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF})
	Layout(root, cw, ch)
	Draw(img, root)

	srcImg := mustLoadImage(t, filepath.Join("testdata", "image_demo.png")).img

	// 原尺寸: 盒内偏移 (2,2) 就是源图 (2,2) —— 取源色现算, 不写死色值
	assertPx(t, img, imgs[0].Box.X+2, imgs[0].Box.Y+2, srcImg.RGBAAt(2, 2), "原尺寸采样")
	// 放大 3 倍: 盒内 (1,1) 映射到源 (0,0)
	assertPx(t, img, imgs[1].Box.X+1, imgs[1].Box.Y+1, srcImg.RGBAAt(0, 0), "放大后采样")
	// 缩小一半: 盒内 (1,1) 映射到源 (2,2)
	assertPx(t, img, imgs[2].Box.X+1, imgs[2].Box.Y+1, srcImg.RGBAAt(2, 2), "缩小后采样")

	// 坏路径: 灰底 + 交叉线占位
	if c := countColor(img, imgs[3].Box, colorImageCross); c == 0 {
		t.Fatalf("坏路径应画出占位交叉线")
	}
	if c := countColor(img, imgs[3].Box, colorImagePlaceholder); c == 0 {
		t.Fatalf("坏路径应画出占位灰底")
	}

	// 禁用态: 罩层改变了像素 (与"未禁用的同位置源色"比较)
	veiled := img.RGBAAt(imgs[4].Box.X+2, imgs[4].Box.Y+2)
	if veiled == srcImg.RGBAAt(1, 1) {
		t.Fatalf("disabled 的 image 应与未禁用时不同, 实际都等于源色 %v", veiled)
	}
}

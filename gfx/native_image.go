package gfx

// ===== gx/media 图片处理: loadImage / resize / crop / encodeImage / writeImage =====
//
// 纯编解码与像素操作，不碰系统 UI，因此与 takePhoto/chooseImage 一族不同，
// 这一族是**同步** API (图片已在内存/磁盘，没有需要等待的外部资源)。
// Go 侧用 image 标准库解码 png/jpeg/gif，缩放用 golang.org/x/image/draw
// 的 ApproxBiLinear。
//
// JS API (gx/media 导出):
//   import { loadImage, resize, crop, encodeImage, writeImage } from "gx/media";
//   const img   = loadImage("in.png");            // → {width, height, format}
//   const small = resize(img, 100, 80);           // → 新图片句柄
//   const part  = crop(img, 10, 10, 50, 50);      // → 新图片句柄
//   const bytes = encodeImage(img, "jpeg", 85);   // → 字节数组 (同 fs.readBytesSync)
//   writeImage(img, "out.png");                   // 编码并写盘, 格式按扩展名推断
//   freeImage(img);                               // 释放 Go 侧像素 (可选)
//
// 图片句柄是不透明对象: {width, height, format} 只读信息 + 内部 __imgId 指向
// Go 侧 image.Image 登记表。句柄不可序列化、不可跨 VM 传递。

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif" // 注册 gif 解码器 (image.Decode 按魔数分派)
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/14752222/Gox/object"
	"golang.org/x/image/draw"
)

var (
	imgRegistryMu sync.Mutex
	imgRegistry   = map[int]image.Image{}
	imgNextID     int
)

// registerImage 把图片放进登记表，返回 JS 句柄对象。
func registerImage(src image.Image, format string) *object.Object {
	imgRegistryMu.Lock()
	imgNextID++
	id := imgNextID
	imgRegistry[id] = src
	imgRegistryMu.Unlock()

	b := src.Bounds()
	o := object.NewObject()
	o.SetProperty("width", object.NewNumber(float64(b.Dx())))
	o.SetProperty("height", object.NewNumber(float64(b.Dy())))
	o.SetProperty("format", object.NewString(format))
	o.SetProperty("__imgId", object.NewNumber(float64(id)))
	return o
}

// imageArg 取出第 i 个参数对应的 Go 图片; 不是图片句柄时返回错误值。
func imageArg(args []object.Value, i int, op string) (image.Image, int, object.Value) {
	if len(args) <= i {
		return nil, 0, object.NewTypeError("%s: missing image argument", op)
	}
	obj, ok := args[i].(*object.Object)
	if !ok {
		return nil, 0, object.NewTypeError("%s: image must be a handle from loadImage, got %s", op, object.TypeOf(args[i]))
	}
	idv, ok := obj.GetProperty("__imgId")
	if !ok {
		return nil, 0, object.NewTypeError("%s: not an image handle (missing __imgId)", op)
	}
	n, ok := idv.(*object.Number)
	if !ok {
		return nil, 0, object.NewTypeError("%s: not an image handle", op)
	}
	id := int(n.Value)
	imgRegistryMu.Lock()
	src, found := imgRegistry[id]
	imgRegistryMu.Unlock()
	if !found {
		return nil, 0, object.NewError(fmt.Sprintf("%s: image already freed", op))
	}
	return src, id, nil
}

func imgIntArg(args []object.Value, i int, op, name string) (int, object.Value) {
	if len(args) <= i {
		return 0, object.NewTypeError("%s: missing %s argument", op, name)
	}
	n, ok := args[i].(*object.Number)
	if !ok {
		return 0, object.NewTypeError("%s: %s must be a number, got %s", op, name, object.TypeOf(args[i]))
	}
	return int(n.Value), nil
}

// imgBytesToArray 字节 → JS 数字数组 (与 stdlib fs.readBytesSync 同一口径)。
func imgBytesToArray(data []byte) *object.Array {
	elems := make([]object.Value, len(data))
	for i, b := range data {
		elems[i] = object.NewNumber(float64(b))
	}
	return &object.Array{Elements: elems}
}

// jsLoadImage: loadImage(path) → 图片句柄
func jsLoadImage(args ...object.Value) object.Value {
	if len(args) < 1 {
		return object.NewTypeError("loadImage: missing path argument")
	}
	path, ok := args[0].(*object.String)
	if !ok {
		return object.NewTypeError("loadImage: path must be a string, got %s", object.TypeOf(args[0]))
	}
	f, err := os.Open(path.Value)
	if err != nil {
		return object.NewError(fmt.Sprintf("loadImage: %v", err))
	}
	defer f.Close()
	src, format, err := image.Decode(f)
	if err != nil {
		return object.NewError(fmt.Sprintf("loadImage: decode %s: %v", path.Value, err))
	}
	return registerImage(src, format)
}

// jsResizeImage: resize(img, width, height) → 新句柄
func jsResizeImage(args ...object.Value) object.Value {
	src, _, errVal := imageArg(args, 0, "resize")
	if errVal != nil {
		return errVal
	}
	w, errVal := imgIntArg(args, 1, "resize", "width")
	if errVal != nil {
		return errVal
	}
	h, errVal := imgIntArg(args, 2, "resize", "height")
	if errVal != nil {
		return errVal
	}
	if w <= 0 || h <= 0 {
		return object.NewTypeError("resize: width/height must be > 0, got %dx%d", w, h)
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	// ApproxBiLinear: 质量与速度的平衡点 (Nearest 太锯齿, CatmullRom 太慢)。
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return registerImage(dst, "")
}

// jsCropImage: crop(img, x, y, width, height) → 新句柄
func jsCropImage(args ...object.Value) object.Value {
	src, _, errVal := imageArg(args, 0, "crop")
	if errVal != nil {
		return errVal
	}
	x, errVal := imgIntArg(args, 1, "crop", "x")
	if errVal != nil {
		return errVal
	}
	y, errVal := imgIntArg(args, 2, "crop", "y")
	if errVal != nil {
		return errVal
	}
	w, errVal := imgIntArg(args, 3, "crop", "width")
	if errVal != nil {
		return errVal
	}
	h, errVal := imgIntArg(args, 4, "crop", "height")
	if errVal != nil {
		return errVal
	}
	b := src.Bounds()
	r := image.Rect(x, y, x+w, y+h).Intersect(b)
	if r.Empty() {
		return object.NewTypeError("crop: rectangle (%d,%d %dx%d) does not intersect image bounds %v", x, y, w, h, b)
	}
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(dst, dst.Bounds(), src, r.Min, draw.Src)
	return registerImage(dst, "")
}

// encodeToBytes 按格式编码; format: "png" | "jpeg" (或 "jpg"), quality 仅 jpeg 生效。
func encodeToBytes(src image.Image, format string, quality int) ([]byte, error) {
	var buf bytes.Buffer
	switch strings.ToLower(format) {
	case "png":
		if err := png.Encode(&buf, src); err != nil {
			return nil, err
		}
	case "jpeg", "jpg":
		if quality <= 0 || quality > 100 {
			quality = 90
		}
		if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: quality}); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported format %q (want png|jpeg)", format)
	}
	return buf.Bytes(), nil
}

// jsEncodeImage: encodeImage(img, format, quality?) → 字节数组
func jsEncodeImage(args ...object.Value) object.Value {
	src, _, errVal := imageArg(args, 0, "encodeImage")
	if errVal != nil {
		return errVal
	}
	if len(args) < 2 {
		return object.NewTypeError("encodeImage: missing format argument")
	}
	format, ok := args[1].(*object.String)
	if !ok {
		return object.NewTypeError("encodeImage: format must be a string, got %s", object.TypeOf(args[1]))
	}
	quality := 90
	if len(args) > 2 {
		if n, ok := args[2].(*object.Number); ok {
			quality = int(n.Value)
		}
	}
	data, err := encodeToBytes(src, format.Value, quality)
	if err != nil {
		return object.NewError(fmt.Sprintf("encodeImage: %v", err))
	}
	return imgBytesToArray(data)
}

// jsWriteImage: writeImage(img, path, quality?) — 格式按扩展名推断 (.png/.jpg/.jpeg)
func jsWriteImage(args ...object.Value) object.Value {
	src, _, errVal := imageArg(args, 0, "writeImage")
	if errVal != nil {
		return errVal
	}
	if len(args) < 2 {
		return object.NewTypeError("writeImage: missing path argument")
	}
	path, ok := args[1].(*object.String)
	if !ok {
		return object.NewTypeError("writeImage: path must be a string, got %s", object.TypeOf(args[1]))
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path.Value), "."))
	quality := 90
	if len(args) > 2 {
		if n, ok := args[2].(*object.Number); ok {
			quality = int(n.Value)
		}
	}
	data, err := encodeToBytes(src, ext, quality)
	if err != nil {
		return object.NewError(fmt.Sprintf("writeImage: %v", err))
	}
	if err := os.WriteFile(path.Value, data, 0644); err != nil {
		return object.NewError(fmt.Sprintf("writeImage: %v", err))
	}
	return object.UndefinedSingleton
}

// jsFreeImage: freeImage(img) — 释放 Go 侧像素内存 (幂等)
func jsFreeImage(args ...object.Value) object.Value {
	_, id, errVal := imageArg(args, 0, "freeImage")
	if errVal != nil {
		return errVal
	}
	imgRegistryMu.Lock()
	delete(imgRegistry, id)
	imgRegistryMu.Unlock()
	return object.UndefinedSingleton
}

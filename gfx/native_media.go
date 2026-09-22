package gfx

// ===== gx/media: 相机 / 相册 / 视频 / 保存到相册 / 系统预览 =====
//
// ## 为什么这一族必须全是异步 (而且不能有同步版本)
//
// 相机与相册在桌面和移动端都意味着"拉起一个系统 UI, 等用户操作":
//
//	桌面   GetOpenFileName 是**模态阻塞**的 (状态文档 §19.1 的教训)
//	Android ActivityResultContracts 是异步回调 (新的 Activity 在前台)
//	iOS   PHPickerViewController / UIImagePickerController 是异步回调
//
// 所以统一成 Promise + pending 回填 —— 这也是整个原生层里唯一"三种平台完全
// 同构"的部分。宿主实现只需要记住: 拉起系统 UI 之后**立刻返回 Pending**,
// 在回调里 Post + ResolveNative。谁要是想在 Call 里"等到用户选完再返回", 就会
// 把 GUI 线程堵死 (脚本、事件泵、所有窗口重绘全停 —— dialog.go 文件头记着这
// 条教训)。
//
// ## 取消不是失败, 但也不是 null
//
// gx/dialog 的 openFile 取消返回 null (沿用浏览器 File System Access 的语义)。
// 相机/相册这里改用 `cancelled` 错误码, 原因: 调用点几乎总是"拍了就上传",
// 而 `null` 会让每个调用点都要写 `if (file === null) return;` —— 与
// `try/catch` 里的取消分支是同一种东西, 不如统一走 catch。要"取消就不做事"
// 的写法照旧很短:
//
//	try { const img = await takePhoto(); upload(img); }
//	catch (e) { if (e.errCode !== "cancelled") toast(e.errMsg); }

import (
	"strconv"

	"github.com/14752222/Gox/object"
)

// MediaFile 是一张照片 / 一段视频 / 一个音频文件。
type MediaFile struct {
	Path      string // 本地文件路径 —— 可直接给 `<image src>` (gfx/image.go 能解码 png/jpeg/gif)
	URI       string // 平台标识 (Android content:// ; 桌面与 iOS 通常等于 Path)
	Name      string // 文件名
	MimeType  string // "image/jpeg" / "video/mp4"
	Width     int    // 像素; 未知 0
	Height    int
	Size      int64   // 字节; -1 未知
	Duration  float64 // 秒 (视频/音频); -1 未知
	CreatedAt int64   // Unix 毫秒; 0 未知
}

// 方法名。能力 ID 取第一个点之前那段: camera / gallery / media。
const (
	nmCameraTake       = "camera.takePhoto"
	nmGalleryPick      = "gallery.pick"
	nmGalleryPickVideo = "gallery.pickVideo"
	nmMediaSave        = "media.save"
	nmMediaPreview     = "media.preview"
)

// mediaToJS 组装脚本侧对象。
func mediaToJS(m MediaFile) object.Value {
	o := object.NewObject()
	nativeSet(o, "path", m.Path)
	nativeSet(o, "uri", m.URI)
	nativeSet(o, "name", m.Name)
	nativeSet(o, "mimeType", m.MimeType)
	o.SetProperty("width", object.NewNumber(float64(m.Width)))
	o.SetProperty("height", object.NewNumber(float64(m.Height)))
	o.SetProperty("size", object.NewNumber(float64(m.Size)))
	o.SetProperty("sizeText", object.NewString(humanSize(m.Size)))
	o.SetProperty("duration", object.NewNumber(m.Duration))
	o.SetProperty("createdAt", object.NewNumber(float64(m.CreatedAt)))
	// src 是 path 的别名: `<image src={f.src}>` 比 `path={f.path}` 更顺眼,
	// 而 uri 优先 (Android 上 content:// 才是可直接喂给图像加载器的那个)。
	src := m.Path
	if m.URI != "" {
		src = m.URI
	}
	nativeSet(o, "src", src)
	isImg := m.MimeType == "" || len(m.MimeType) >= 6 && m.MimeType[:6] == "image/"
	o.SetProperty("isImage", object.NewBoolean(isImg))
	o.SetProperty("isVideo", object.NewBoolean(len(m.MimeType) >= 6 && m.MimeType[:6] == "video/"))
	return o
}

// humanSize 把字节数写成人读的字符串 ("1.2 MB"); 未知 (-1) → 空串。
//
// 放在内核而不是让应用自己写: 文件大小文案是每个涉及媒体的界面都要显示的,
// 而"除以 1024 还是 1000"这种细节散在各个应用里必然不一致。
func humanSize(n int64) string {
	if n < 0 {
		return ""
	}
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(n)
	i := -1
	for v >= unit && i < len(units)-1 {
		v /= unit
		i++
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + " " + units[i]
}

// mediaFromJS 把宿主给的一份结果解析成 MediaFile。
//
// 宽容到什么程度: 宿主直接返回一个**路径字符串**也认 —— Android 的很多 API
// 天然就是给一个 Uri/路径, 要求宿主先包成对象只是给桥接添活。空结果 → ok=false。
func mediaFromJS(v object.Value) (MediaFile, bool) {
	switch x := v.(type) {
	case *object.String:
		if x.Value == "" {
			return MediaFile{}, false
		}
		return MediaFile{Path: x.Value, Name: baseName(x.Value), Size: -1, Duration: -1}, true
	case *object.Object:
		m := MediaFile{
			Path:      objPropStr(x, "path"),
			URI:       objPropStr(x, "uri"),
			Name:      objPropStr(x, "name"),
			MimeType:  objPropStr(x, "mimeType"),
			Width:     int(objPropNum(x, "width")),
			Height:    int(objPropNum(x, "height")),
			Duration:  -1,
			CreatedAt: int64(objPropNum(x, "createdAt")),
		}
		if v := objProp(x, "size"); v != nil {
			m.Size = int64(objPropNum(x, "size"))
		} else {
			m.Size = -1
		}
		if v := objProp(x, "duration"); v != nil {
			m.Duration = objPropNum(x, "duration")
		}
		if m.Path == "" {
			m.Path = m.URI
		}
		if m.URI == "" {
			m.URI = m.Path
		}
		if m.Name == "" {
			m.Name = baseName(m.Path)
		}
		if m.Path == "" && m.URI == "" {
			return MediaFile{}, false
		}
		return m, true
	}
	return MediaFile{}, false
}

// baseName 取路径末段 (跨 / 与 \ 两种分隔符)。
func baseName(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}

// mediaListFromJS 把结果解析成列表 (单个对象也接受, 自动包成一项)。
func mediaListFromJS(v object.Value) []MediaFile {
	switch x := v.(type) {
	case *object.Array:
		out := make([]MediaFile, 0, len(x.Elements))
		for _, el := range x.Elements {
			if m, ok := mediaFromJS(el); ok {
				out = append(out, m)
			}
		}
		return out
	default:
		if m, ok := mediaFromJS(v); ok {
			return []MediaFile{m}
		}
	}
	return nil
}

func mediaListToJS(list []MediaFile) object.Value {
	out := make([]object.Value, 0, len(list))
	for _, m := range list {
		out = append(out, mediaToJS(m))
	}
	return object.NewArray(out)
}

// ===== 相机 / 相册 =====

// jsTakePhoto 是 `takePhoto(options?, callback?)` → Promise<MediaFile>。
//
// options: {camera:"back"|"front", quality:0..100, saveToGallery:bool,
//
//	format:"jpg"|"png", maxWidth, maxHeight}
func jsTakePhoto(args ...object.Value) object.Value {
	opts := nativeOpts(args, 0)
	if objPropStr(opts, "camera") == "" {
		opts.SetProperty("camera", object.NewString("back"))
	}
	if objProp(opts, "quality") == nil {
		opts.SetProperty("quality", object.NewNumber(90))
	}
	// 结果经 mediaToJS 归一化: 宿主给路径字符串也认 (mediaFromJS), 但脚本拿到
	// 的永远是完整形状 (含 sizeText / isImage / src 别名) —— 与 chooseImage /
	// lastLocation 的双路径不同形状的坑一致。
	return callNativeWithSink(nmCameraTake, opts, nativeOptFunc(args, 1),
		func(result object.Value) (object.Value, object.Value) {
			m, ok := mediaFromJS(result)
			if !ok {
				return nil, nativeErr(ErrPlatform,
					"camera.takePhoto 返回了无法解析的文件").toJS()
			}
			return mediaToJS(m), nil
		})
}

// jsChooseImage 是 `chooseImage(options?, callback?)` → Promise<MediaFile[]>。
//
// **恒定返回数组** (与 uniapp 的 chooseImage 一致): 选一张也要循环处理, 让
// "一张"与"多张"返回不同形状的 API 是应用侧 bug 的经典来源。只想要一张就用
// chooseOneImage()。
//
// options: {count:1..9, source:"album"|"camera"|"both", compressed:bool,
//
//	quality:0..100, allowVideo:bool}
func jsChooseImage(args ...object.Value) object.Value {
	opts := nativeOpts(args, 0)
	n := objPropNum(opts, "count")
	if n <= 0 {
		opts.SetProperty("count", object.NewNumber(1))
	} else if n > 9 {
		// 上限 9 是 iOS 系统选择器与多数业务约定俗成的边界; 超了夹住而不是报错
		// (与亮度同档: 这是"尽量满足"的偏好, 不是硬约束)。要更多张就多次调用。
		opts.SetProperty("count", object.NewNumber(9))
	}
	return callNative(nmGalleryPick, opts, nativeOptFunc(args, 1))
}

// jsChooseOneImage 是 `chooseOneImage(options?, callback?)` → Promise<MediaFile|null>。
//
// 它是 chooseImage 的**便利封装** (能力 ID 仍是 gallery, 不新增宿主方法):
// 取第一张, 用户取消 → null。之所以要它: "换头像"这种场景占相册调用的绝大多数,
// 每次都写 `(await chooseImage())[0]` 既啰嗦又容易漏掉空数组。
func jsChooseOneImage(args ...object.Value) object.Value {
	opts := nativeOpts(args, 0)
	opts.SetProperty("count", object.NewNumber(1))
	outer := object.NewPromise()
	callNative(nmGalleryPick, opts, object.NewBuiltin("__choose_one",
		func(args ...object.Value) object.Value {
			if len(args) > 0 && args[0] != object.NullSingleton && args[0] != object.UndefinedSingleton {
				outer.Reject(args[0])
				return object.UndefinedSingleton
			}
			var result object.Value = object.UndefinedSingleton
			if len(args) > 1 {
				result = args[1]
			}
			list := mediaListFromJS(result)
			if len(list) == 0 {
				outer.Resolve(object.NullSingleton)
				return object.UndefinedSingleton
			}
			outer.Resolve(mediaToJS(list[0]))
			return object.UndefinedSingleton
		}))
	return outer
}

// jsChooseVideo 是 `chooseVideo(options?, callback?)` → Promise<MediaFile>。
//
// options: {source, maxDuration(秒), compressed, quality:"low"|"medium"|"high"}
func jsChooseVideo(args ...object.Value) object.Value {
	return callNative(nmGalleryPickVideo, nativeOpts(args, 0), nativeOptFunc(args, 1))
}

// jsSaveImage 是 `saveImage(source, options?, callback?)` → Promise<MediaFile>。
//
// source 可以是 MediaFile 对象, 也可以直接是文件路径字符串。
// options: {album:"相册名"}
func jsSaveImage(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.NewTypeError("saveImage: 需要文件对象或路径")
	}
	var file MediaFile
	from := 1
	if s, ok := args[0].(*object.String); ok {
		// 路径字符串形态: 后面的参数就从第 2 个开始当 options
		file = MediaFile{Path: s.Value, Name: baseName(s.Value)}
	} else {
		m, ok := mediaFromJS(args[0])
		if !ok {
			return object.NewTypeError("saveImage: 需要一个含 path/uri 的对象或路径字符串")
		}
		file = m
	}
	opts := nativeOpts(args, from)
	if objProp(opts, "file") == nil {
		opts.SetProperty("file", mediaToJS(file))
	}
	cb := nativeOptFunc(args, from+1)
	return callNative(nmMediaSave, opts, cb)
}

// jsPreviewImage 是 `previewImage(files, index?, callback?)` → Promise<void>。
//
// files 是 MediaFile 数组 (或单个), index 是初始显示第几张 (缺省 0)。
// 这是"看大图"的标准交互, 由系统提供 (Android Intent.ACTION_VIEW / iOS
// QLPreviewController), 内核不自绘 —— 自绘预览器是另一个量级的工程 (缩放、
// 手势、视频播放), 而且用户对系统预览器的习惯已经形成。
func jsPreviewImage(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.NewTypeError("previewImage: 需要至少一个文件")
	}
	list := mediaListFromJS(args[0])
	if len(list) == 0 {
		return object.NewTypeError("previewImage: 文件列表为空")
	}
	opts := object.NewObject()
	opts.SetProperty("files", mediaListToJS(list))
	idx := 0.0
	if len(args) > 1 {
		idx = nativeNumberValue(args[1])
	}
	if idx < 0 || int(idx) >= len(list) {
		idx = 0
	}
	opts.SetProperty("index", object.NewNumber(idx))
	return callNative(nmMediaPreview, opts, nativeOptFunc(args, 2))
}

// mediaInfoToJS 是媒体能力的自述 (排查用)。
func mediaInfoToJS() object.Value {
	o := object.NewObject()
	o.SetProperty("camera", object.NewBoolean(CanIUse("camera")))
	o.SetProperty("gallery", object.NewBoolean(CanIUse("gallery")))
	o.SetProperty("save", object.NewBoolean(CanIUse(nmMediaSave)))
	o.SetProperty("preview", object.NewBoolean(CanIUse(nmMediaPreview)))
	o.SetProperty("platform", object.NewString(deviceInfo().Platform))
	return o
}

func init() {
	object.RegisterBuiltinModule("gx/media", func() map[string]object.Value {
		return map[string]object.Value{
			"takePhoto":      scr("takePhoto", jsTakePhoto),
			"chooseImage":    scr("chooseImage", jsChooseImage),
			"chooseOneImage": scr("chooseOneImage", jsChooseOneImage),
			"chooseVideo":    scr("chooseVideo", jsChooseVideo),
			"saveImage":      scr("saveImage", jsSaveImage),
			"previewImage":   scr("previewImage", jsPreviewImage),

			"mediaInfo": scr("mediaInfo", func(args ...object.Value) object.Value { return mediaInfoToJS() }),
			"humanSize": scr("humanSize", func(args ...object.Value) object.Value {
				if len(args) == 0 {
					return object.NewString("")
				}
				return object.NewString(humanSize(int64(nativeNumberValue(args[0]))))
			}),
		}
	})
}

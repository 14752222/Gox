package gfx

import (
	"image"
	"strings"

	"github.com/14752222/Gox/object"
)

// T08 upload 文件选择。
//
//	<upload model={files} />
//	<upload name="attach" multiple accept=".png,.jpg" onChange={(e) => save(e.paths)} />
//	<upload filter="图片|*.png;*.jpg" placeholder="上传附件" />
//
// 外观是一个 28px 的虚线边框字段行 (与 input/select 同一套字段常量, 表单里
// 才会齐平): 左侧文件夹图标 + 已选文件的**名字**列表 (没有就显示 placeholder)。
//
// 与其他字段不同, 它**没有弹层**: 点击直接调平台的原生"打开文件"对话框
// (gfx/dialog.go 的 openFileBox)。这条链路是既有能力, 不是为 upload 新开的,
// 所以后端没接对话框时自然走它的降级路径 (把提示打到 stderr 并返回"未选择"),
// upload 只表现为"点了没反应"—— 这里刻意**不**编造假文件名去填, 静默编造会
// 让业务逻辑以为选到了文件 (与 gx/native 的降级口径一致: 能力缺失不软降级)。
//
// 受控 / 非受控**两用** (这一族的其它组件只有受控一种, upload 是例外):
//
//	有 value prop ⇒ 受控: 显示与取值都读 value (字符串数组, 或 {name, path}
//	                对象数组), 选择之后只派发 onChange, 等脚本把新值写回;
//	没有 value prop ⇒ 非受控: 内部维护已选列表, 选择之后立即更新显示**并且**
//	                照旧派发 onChange (脚本可以只听不用, 也可以当作纯通知)。
//
// 之所以在这里破例: "选文件"这个动作的结果 (路径) 无法由脚本自己构造 ——
// 它不是用户打字打出来的, 而是系统对话框给的。要求脚本必须回写 signal 才能
// 看见文件, 会让最简单的用法 (<upload />) 变成"点了没反应"。
//
// v1 边界 (写清楚免得被当 bug):
//   - 底层对话框一次只返回一个路径 (见 nativeDialogHost.ShowOpenFile)。所以
//     `multiple` 的语义是"多次选择累加" (再点一次追加一个), 而不是一次多选;
//   - 不做拖拽入框 (桌面拖放是独立的一期能力)、不做进度/直传 (那是 gx/http
//     的事, 组件只负责"选到文件"), 也不做删除单个文件的叉 —— 脚本改 value
//     或调内部列表即可 (非受控态下再点一次就是追加, 想清空请用受控)。

// ===== 尺寸常量 =====

const (
	// upFieldIconW 是字段左侧文件夹图标的边长。
	upFieldIconW = 14
	// upFieldMinW 是无显式宽度时的最小字段宽度。
	upFieldMinW = 180
	// upDashedGap 是虚线边框的线段/间隔长度 (与焦点框同一套观感)。
	upDashedGap = 4
)

// ===== props =====

// uploadInChain 从 n 起沿祖先链找第一个 upload。
func uploadInChain(n *GuiNode) *GuiNode {
	for p := n; p != nil; p = p.Parent {
		if p.Tag == "upload" {
			return p
		}
	}
	return nil
}

// uploadControlled 报告是否受控 (有 value prop 就算 —— 与 checkbox 的
// checked 同一判据: 显式给了受控 prop 就不该再被内部状态覆盖)。
func (n *GuiNode) uploadControlled() bool {
	_, ok := n.Props["value"]
	return ok
}

// uploadPaths 返回当前已选文件路径列表。
//
// 受控时读 value prop (字符串数组, 或 {name, path} 对象数组 —— 后者是为了
// 让脚本能带上大小/类型这类显示信息, 路径一律取 path 字段); 非受控时读
// 内部列表 (n.upFiles)。两种形态都归一成 []string, 因为"选了哪些文件"这件事
// 的对外表达就是路径。
func (n *GuiNode) uploadPaths() []string {
	if !n.uploadControlled() {
		return append([]string(nil), n.upFiles...)
	}
	v, ok := n.Props["value"]
	if !ok {
		return nil
	}
	arr, ok := v.(*object.Array)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr.Elements))
	for _, e := range arr.Elements {
		switch x := e.(type) {
		case *object.String:
			if x.Value != "" {
				out = append(out, x.Value)
			}
		case *object.Object:
			if p, ok := x.GetProperty("path"); ok {
				if s := valueText(p); s != "" {
					out = append(out, s)
					continue
				}
			}
			if p, ok := x.GetProperty("name"); ok {
				if s := valueText(p); s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// uploadMultiple 读 multiple prop (缺省 false = 每次选择替换上一次)。
func (n *GuiNode) uploadMultiple() bool {
	v, _ := n.PropBool("multiple")
	return v
}

// uploadPlaceholder 读占位文本 (缺省 "选择文件")。
func (n *GuiNode) uploadPlaceholder() string {
	if s, ok := n.PropStr("placeholder"); ok && s != "" {
		return s
	}
	return "选择文件"
}

// uploadDisplay 返回字段上应显示的文本: 已选文件的**文件名**用 、 连接
// (完整路径又长又没信息量, 用户认的是文件名)。没有文件时 ok=false。
//
// 路径分隔符按平台归一: Windows 的路径在 macOS/Linux 上运行时也要能显示出
// 文件名 (脚本里写死的路径与真实运行平台不一致是常态)。
func (n *GuiNode) uploadDisplay() (string, bool) {
	paths := n.uploadPaths()
	if len(paths) == 0 {
		return "", false
	}
	names := make([]string, 0, len(paths))
	for _, p := range paths {
		names = append(names, uploadBaseName(p))
	}
	return strings.Join(names, "、"), true
}

// uploadBaseName 取路径里的文件名 (两种分隔符都认)。
//
// 先去掉尾部分隔符再找最后一段: 目录选择器给出的路径常带尾斜杠
// ("/tmp/dir/"), 直接取"最后一个分隔符之后"会得到空串 —— 显示成"选了个空
// 文件", 而用户明明看见自己选了目录。
func uploadBaseName(p string) string {
	trimmed := strings.TrimRight(p, `/\`)
	if trimmed == "" {
		return p // 整串都是分隔符 ("/" 或 "C:\"): 没有"文件名"可取, 原样返回
	}
	if i := strings.LastIndexAny(trimmed, `/\`); i >= 0 {
		return trimmed[i+1:]
	}
	return trimmed
}

// uploadFileOptions 把 props 翻译成原生对话框的参数。
//
// 过滤规则有两个入口, 优先级: filter (完整形态: 字符串/数组/对象, 见
// parseFileFilter) > accept (HTML input 风格的后缀串, 如 ".png,.jpg")。
// 两个都写时只有 filter 生效 —— 静默二选一, 因为"两个过滤规则合并"没有
// 明确语义 (是并集还是交集?), 含混只会让调用点猜。
func uploadFileOptions(up *GuiNode) NativeFileOptions {
	opts := NativeFileOptions{}
	if s, ok := up.PropStr("title"); ok {
		opts.Title = s
	}
	if opts.Title == "" {
		opts.Title = "选择文件"
	}
	if v, ok := up.Props["filter"]; ok {
		opts.Filter = parseFileFilter(v)
	} else if s, ok := up.PropStr("accept"); ok && s != "" {
		if f := uploadAcceptFilter(s); f != nil {
			opts.Filter = f
		}
	}
	return opts
}

// uploadAcceptFilter 把 accept 串 ("...png,...jpg" 或 "image/*") 归一成
// 一条文件过滤规则; 认不出来返回 nil (不过滤, 比过滤错了好)。
//
// 支持的后缀写成 "*.png" 形式 (原生对话框要的是通配符), 多个用 ; 连接。
// 也认少量常见 MIME 的整类通配 ("image/*" → 六种图片后缀): 这是把
// "哪些后缀算图片"这件事写死在映射表里, 而不是去查系统 MIME 库 —— 后者
// 在每个平台上的答案都不一样, 是个没有尽头的坑。
func uploadAcceptFilter(accept string) []NativeFileFilter {
	var pats []string
	for _, item := range strings.FieldsFunc(accept, func(r rune) bool { return r == ',' || r == ';' }) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if strings.HasPrefix(item, ".") {
			pats = append(pats, "*"+item)
			continue
		}
		if strings.HasSuffix(item, "/*") {
			pats = append(pats, uploadMimeExts[strings.TrimSuffix(item, "/*")]...)
			continue
		}
		if strings.ContainsAny(item, "*?") {
			pats = append(pats, item)
		}
	}
	if len(pats) == 0 {
		return nil
	}
	return []NativeFileFilter{{Name: "可选文件", Pattern: strings.Join(pats, ";")}}
}

// uploadMimeExts 是"整类通配"到后缀通配符的映射 (刻意只收最常见的几类)。
var uploadMimeExts = map[string][]string{
	"image": {"*.png", "*.jpg", "*.jpeg", "*.gif", "*.webp", "*.bmp"},
	"video": {"*.mp4", "*.mov", "*.webm", "*.mkv"},
	"audio": {"*.mp3", "*.wav", "*.ogg", "*.flac", "*.m4a"},
	"text":  {"*.txt", "*.md", "*.csv", "*.json", "*.log"},
}

// ===== 选择 =====

// attachUploadHandler 给 upload 装上内置的"打开文件"处理器 (由 JSBuiltinH
// 在建节点时调用)。
//
// 与 select/datepicker 的 attachXxx 同一手法: 包一层脚本自己的 onClick。
// 不同之处是它**不**做展开/收起 (upload 没有弹层), 只负责发起一次选择。
func attachUploadHandler(n *GuiNode) {
	user := n.PropHandler("onClick")
	n.Props["onClick"] = object.NewBuiltin("uploadPick", func(args ...object.Value) object.Value {
		a := appOfNode(n)
		if a != nil {
			a.uploadPick(n)
			if user != nil {
				a.callHandlerValue(user, "onClick", nil)
			}
		}
		return object.UndefinedSingleton
	})
}

// uploadPick 发起一次文件选择, 拿到路径后按 multiple 决定替换还是追加。
//
// 取消 / 后端不支持对话框: 什么都不做 (也不派发 onChange) —— 与
// gx/dialog 的 openFile 取消返回 null 同一立场: 取消不是"选了空"。
func (a *app) uploadPick(up *GuiNode) {
	if up.disabledInChain() {
		return
	}
	path, ok := openFileBox(uploadFileOptions(up))
	if !ok || path == "" {
		return
	}
	paths := up.uploadPaths()
	if up.uploadMultiple() {
		paths = append(paths, path)
	} else {
		paths = []string{path}
	}
	if !up.uploadControlled() {
		// 非受控: 先把内部列表更新掉, 这一帧的显示立刻跟上 (标脏)。
		up.upFiles = append([]string(nil), paths...)
		markNodeDirty(up)
	}
	a.uploadDispatch(up, paths)
}

// uploadDispatch 派发 onChange({files, paths})。
//
// 两个键而不是一个: paths 是最常用的形态 (只要路径), files 是带文件名的
// 对象数组 (要用在列表里显示时省一次切分)。刻意**不**把它们合成"files 数组
// 里塞字符串或对象"的多态 —— 消费方每次都要判类型, 是最烦人的那种 API。
func (a *app) uploadDispatch(up *GuiNode, paths []string) {
	h := up.PropHandler("onChange")
	if h == nil {
		return
	}
	arr := make([]object.Value, 0, len(paths))
	names := make([]object.Value, 0, len(paths))
	for _, p := range paths {
		o := object.NewObject()
		o.SetProperty("name", object.NewString(uploadBaseName(p)))
		o.SetProperty("path", object.NewString(p))
		arr = append(arr, o)
		names = append(names, object.NewString(p))
	}
	arg := object.NewObject()
	arg.SetProperty("files", object.NewArray(arr))
	arg.SetProperty("paths", object.NewArray(names))
	a.callHandler(up, "onChange", arg)
}

// ===== 键盘 =====

// handleUploadKey 处理 upload 字段上的按键 (Enter/Space = 发起选择)。
// 返回是否消费。
func (a *app) handleUploadKey(up *GuiNode, key string, ev Event) bool {
	if ev.Ctrl || ev.Alt {
		return false
	}
	switch key {
	case "Enter", " ":
		a.uploadPick(up)
		return true
	}
	return false
}

// ===== 布局 / 绘制 =====

// layoutUpload 摆放上传字段: 没有流内子节点 (内容全由绘制分支画),
// 走与其它字段一致的"显示文本 + 图标"口径。
func layoutUpload(n *GuiNode) {
	area := inner(n)
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue
		}
		cw, ch := c.intrinsicSize()
		c.Box = Rect{X: area.X, Y: area.Y, W: cw, H: ch}
		layoutNode(c)
	}
}

// intrinsicUpload 字段的固有尺寸。
func intrinsicUpload(n *GuiNode) (w, h int) {
	text, ok := n.uploadDisplay()
	if !ok {
		text = n.uploadPlaceholder()
	}
	tw, _ := MeasureText(text, n.FontSize())
	w = tw + 2*fieldPadX + upFieldIconW + searchIconGap
	if w < upFieldMinW {
		w = upFieldMinW
	}
	h = selectRowH
	return w, h
}

// paintUpload 画上传字段: 与其它字段同一套底色, 但边框是**虚线** ——
// 实线边框在这套组件里已经等于"输入框/下拉框", 虚线是"这里可以点着去选
// 东西"的通用视觉约定 (按钮与输入框长得一样时, 用户会试着往里打字)。
func paintUpload(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	FillRect(img, b, tint(n.fieldFace(colorFieldFace), disabled))
	StrokeDashedRect(img, b, tint(colorInputEdge, disabled), upDashedGap, upDashedGap)

	if fn := builtinIcons["folder"]; fn != nil {
		fn(img, Rect{
			X: b.X + fieldPadX,
			Y: b.Y + (b.H-upFieldIconW)/2,
			W: upFieldIconW, H: upFieldIconW,
		}, upFieldIconW, tint(colorPlaceholder, disabled))
	}
	size := n.FontSize()
	text, ok := n.uploadDisplay()
	textColor := colorText
	if !ok {
		text, textColor = n.uploadPlaceholder(), colorPlaceholder
	}
	if text == "" {
		return
	}
	x := b.X + fieldPadX + upFieldIconW + searchIconGap
	maxW := b.X + b.W - fieldPadX - x
	if maxW < 0 {
		maxW = 0
	}
	_, th := MeasureText(text, size)
	DrawText(img, img.Bounds(), text, x, b.Y+(b.H-th)/2, size,
		tint(textColor, disabled), maxW)
}

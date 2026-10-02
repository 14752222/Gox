package gfx

import (
	"image"
	"strings"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== T08 <upload> =====
//
// upload 是这一族里唯一"受控 / 非受控两用"的组件, 也是唯一直接依赖平台能力
// (原生"打开文件"对话框) 的组件。用例覆盖三块:
//   1. 两用语义 (受控读 value / 非受控维护内部列表);
//   2. 原生链路与**降级**: 假对话框注入时全链路, 没接对话框时"什么都不做"
//      (而不是编一个假文件名 —— 那会让业务逻辑以为选到了文件);
//   3. props → 对话框参数的翻译 (filter / accept)。

// mkUploadTree 造 <column><upload/></column>, 并按需注入假原生对话框。
func mkUploadTree(t *testing.T, dialog *fakeDialogHost, configure func(*GuiNode)) (*GuiNode, *GuiNode, *app, *fakeSurface) {
	t.Helper()
	root := mkNode("column", map[string]float64{"padding": 10})
	up := &GuiNode{Tag: "upload", Props: map[string]object.Value{}}
	if configure != nil {
		configure(up)
	}
	attachUploadHandler(up)
	mountChildren(root, up)
	fake := newFakeSurface()
	if dialog != nil {
		fake.dialog = dialog
	}
	a := mountTestAppWith(t, root, fake, 320, 200)
	return root, up, a, fake
}

// recordUploads 挂一个 onChange, 记下每次收到的 paths。
func recordUploads(up *GuiNode) *[][]string {
	var got [][]string
	up.Props["onChange"] = object.NewBuiltin("recordUploads", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.UndefinedSingleton
		}
		o, ok := args[0].(*object.Object)
		if !ok {
			return object.UndefinedSingleton
		}
		v, ok := o.GetProperty("paths")
		arr, ok2 := v.(*object.Array)
		if !ok || !ok2 {
			return object.UndefinedSingleton
		}
		var one []string
		for _, e := range arr.Elements {
			if s, ok := e.(*object.String); ok {
				one = append(one, s.Value)
			}
		}
		got = append(got, one)
		return object.UndefinedSingleton
	})
	return &got
}

// ===== 受控 =====

func TestUploadControlledReadsValueProp(t *testing.T) {
	_, up, _, _ := mkUploadTree(t, nil, func(n *GuiNode) {
		n.Props["value"] = object.NewArray([]object.Value{
			object.NewString("/tmp/a.png"),
			object.NewString("/tmp/b.jpg"),
		})
	})

	if !up.uploadControlled() {
		t.Fatalf("有 value prop 就该是受控")
	}
	got := up.uploadPaths()
	if len(got) != 2 || got[0] != "/tmp/a.png" || got[1] != "/tmp/b.jpg" {
		t.Fatalf("受控路径 = %v", got)
	}
	// 显示的是**文件名** (完整路径又长又没信息量)
	text, ok := up.uploadDisplay()
	if !ok || text != "a.png、b.jpg" {
		t.Fatalf("显示文本 = %q ok=%v, want a.png、b.jpg", text, ok)
	}
}

func TestUploadControlledAcceptsObjectEntries(t *testing.T) {
	// {name, path} 对象数组: 让脚本能带上大小/类型这类显示信息, 路径一律取 path
	up := &GuiNode{Tag: "upload", Props: map[string]object.Value{
		"value": object.NewArray([]object.Value{
			objWith("name", "报告.pdf", "path", "/x/y/report.pdf"),
			objWith("name", "只有名字.txt"),
		}),
	}}
	got := up.uploadPaths()
	if len(got) != 2 || got[0] != "/x/y/report.pdf" || got[1] != "只有名字.txt" {
		t.Fatalf("对象数组取路径 = %v", got)
	}
	if text, _ := up.uploadDisplay(); text != "report.pdf、只有名字.txt" {
		t.Fatalf("显示文本 = %q", text)
	}
}

// objWith 造一个 {k: 字符串, ...} 对象。
func objWith(kv ...string) *object.Object {
	o := object.NewObject()
	for i := 0; i+1 < len(kv); i += 2 {
		o.SetProperty(kv[i], object.NewString(kv[i+1]))
	}
	return o
}

func TestUploadControlledDoesNotMutateOnPick(t *testing.T) {
	dialog := &fakeDialogHost{openPath: "/tmp/new.png", openOk: true}
	_, up, a, _ := mkUploadTree(t, dialog, func(n *GuiNode) {
		n.Props["value"] = object.NewArray([]object.Value{object.NewString("/tmp/old.png")})
	})
	got := recordUploads(up)

	a.uploadPick(up)

	if len(*got) != 1 || len((*got)[0]) != 1 || (*got)[0][0] != "/tmp/new.png" {
		t.Fatalf("受控: 应派发新路径, got %v", *got)
	}
	// 受控: 派发不等于改值 (显示仍取决于 value prop)
	if paths := up.uploadPaths(); len(paths) != 1 || paths[0] != "/tmp/old.png" {
		t.Fatalf("受控组件不该自己改值, got %v", paths)
	}
}

// ===== 非受控 =====

func TestUploadUncontrolledAppendsToOwnList(t *testing.T) {
	dialog := &fakeDialogHost{openPath: "/tmp/a.txt", openOk: true}
	_, up, a, _ := mkUploadTree(t, dialog, func(n *GuiNode) { withBool(n, "multiple", true) })
	got := recordUploads(up)

	if up.uploadControlled() {
		t.Fatalf("没有 value prop 就该是非受控")
	}
	a.uploadPick(up)
	if paths := up.uploadPaths(); len(paths) != 1 || paths[0] != "/tmp/a.txt" {
		t.Fatalf("非受控: 该立刻记进内部列表, got %v", paths)
	}

	// multiple ⇒ 再选一次是追加
	dialog.openPath = "/tmp/b.txt"
	a.uploadPick(up)
	if paths := up.uploadPaths(); len(paths) != 2 || paths[1] != "/tmp/b.txt" {
		t.Fatalf("multiple 应累加, got %v", paths)
	}
	if len(*got) != 2 {
		t.Fatalf("两次选择该派发两次 onChange, got %d", len(*got))
	}
	if text, ok := up.uploadDisplay(); !ok || text != "a.txt、b.txt" {
		t.Fatalf("显示文本 = %q ok=%v", text, ok)
	}
}

func TestUploadSingleModeReplaces(t *testing.T) {
	dialog := &fakeDialogHost{openPath: "/tmp/a.txt", openOk: true}
	_, up, a, _ := mkUploadTree(t, dialog, nil)

	a.uploadPick(up)
	dialog.openPath = "/tmp/b.txt"
	a.uploadPick(up)
	if paths := up.uploadPaths(); len(paths) != 1 || paths[0] != "/tmp/b.txt" {
		t.Fatalf("非 multiple 应替换, got %v", paths)
	}
}

func TestUploadUncontrolledMarksDirty(t *testing.T) {
	// 非受控没有 signal 驱动重绘, 内部列表变了必须自己标脏 ——
	// 否则选完文件画面纹丝不动 (最难排查的一类"没反应")。
	dialog := &fakeDialogHost{openPath: "/tmp/a.txt", openOk: true}
	_, up, a, _ := mkUploadTree(t, dialog, nil)
	a.redraw()
	if needDraw(a) {
		t.Fatalf("前置条件: 刚重绘完不该还脏")
	}
	a.uploadPick(up)
	if !needDraw(a) {
		t.Fatalf("非受控选完文件该标脏 (显示要更新)")
	}
}

// ===== 取消 / 无能力: 一律"什么都不做" =====

func TestUploadCancelDispatchesNothing(t *testing.T) {
	dialog := &fakeDialogHost{openOk: false} // 用户在系统框里点了取消
	_, up, a, _ := mkUploadTree(t, dialog, nil)
	got := recordUploads(up)

	a.uploadPick(up)

	if len(*got) != 0 {
		t.Fatalf("取消不该派发 onChange (取消不是'选了空'), got %v", *got)
	}
	if paths := up.uploadPaths(); len(paths) != 0 {
		t.Fatalf("取消不该改内部列表, got %v", paths)
	}
}

func TestUploadWithoutDialogBackendDoesNothing(t *testing.T) {
	// 后端没实现 nativeDialogHost: 走 gfx/dialog.go 的降级 (打 stderr + 返回未选),
	// upload 只表现为"点了没反应" —— 刻意**不**编一个假文件名去填。
	_, up, a, _ := mkUploadTree(t, nil, nil)
	got := recordUploads(up)

	a.uploadPick(up)

	if len(*got) != 0 {
		t.Fatalf("没有对话框能力时不该派发 onChange, got %v", *got)
	}
	if paths := up.uploadPaths(); len(paths) != 0 {
		t.Fatalf("不该编造文件名, got %v", paths)
	}
}

func TestUploadDisabledDoesNotPick(t *testing.T) {
	dialog := &fakeDialogHost{openPath: "/tmp/a.txt", openOk: true}
	_, up, a, _ := mkUploadTree(t, dialog, func(n *GuiNode) { withBool(n, "disabled", true) })
	a.uploadPick(up)
	if len(dialog.openCalls()) != 0 {
		t.Fatalf("禁用字段不该弹原生对话框")
	}
	if paths := up.uploadPaths(); len(paths) != 0 {
		t.Fatalf("禁用字段不该改值, got %v", paths)
	}
}

// ===== props → 对话框参数 =====

func TestUploadFileOptionsFilterWins(t *testing.T) {
	up := &GuiNode{Tag: "upload", Props: map[string]object.Value{}}
	withStr(up, "title", "选择附件")
	withStr(up, "filter", "图片|*.png;*.jpg")
	withStr(up, "accept", ".pdf") // 同时给了 accept: filter 优先 (静默二选一)

	opts := uploadFileOptions(up)
	if opts.Title != "选择附件" {
		t.Fatalf("title = %q", opts.Title)
	}
	if len(opts.Filter) != 1 || opts.Filter[0].Name != "图片" || opts.Filter[0].Pattern != "*.png;*.jpg" {
		t.Fatalf("filter 应优先于 accept, got %+v", opts.Filter)
	}
	// 缺省标题
	bare := &GuiNode{Tag: "upload", Props: map[string]object.Value{}}
	if got := uploadFileOptions(bare).Title; got != "选择文件" {
		t.Fatalf("缺省标题 = %q", got)
	}
}

func TestUploadAcceptFilter(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{".png,.jpg", "*.png;*.jpg"},
		{"*.txt; *.md", "*.txt;*.md"},
		{"image/*", "*.png;*.jpg;*.jpeg;*.gif;*.webp;*.bmp"},
		{"text/*", "*.txt;*.md;*.csv;*.json;*.log"},
		{"  .PNG  ", "*.PNG"},
	}
	for _, c := range cases {
		f := uploadAcceptFilter(c.in)
		if len(f) != 1 {
			t.Fatalf("accept=%q 应得到 1 条过滤规则, got %d", c.in, len(f))
		}
		if f[0].Pattern != c.want {
			t.Fatalf("accept=%q → %q, want %q", c.in, f[0].Pattern, c.want)
		}
	}
	// 认不出来就不过滤 (比过滤错了好)
	for _, bad := range []string{"", "  ", "gibberish"} {
		if f := uploadAcceptFilter(bad); f != nil {
			t.Fatalf("accept=%q 该返回 nil, got %+v", bad, f)
		}
	}
}

func TestUploadPickPassesOptionsToDialog(t *testing.T) {
	dialog := &fakeDialogHost{openPath: "/tmp/a.png", openOk: true}
	_, up, a, _ := mkUploadTree(t, dialog, func(n *GuiNode) {
		withStr(n, "title", "选图")
		withStr(n, "accept", ".png")
	})
	a.uploadPick(up)

	calls := dialog.openCalls()
	if len(calls) != 1 {
		t.Fatalf("该弹一次对话框, got %d", len(calls))
	}
	if calls[0].Title != "选图" {
		t.Fatalf("标题应传到对话框: %q", calls[0].Title)
	}
	if len(calls[0].Filter) != 1 || calls[0].Filter[0].Pattern != "*.png" {
		t.Fatalf("accept 应翻成过滤规则: %+v", calls[0].Filter)
	}
}

// ===== 键盘 =====

func TestUploadKeyboardActivates(t *testing.T) {
	dialog := &fakeDialogHost{openPath: "/tmp/a.txt", openOk: true}
	_, up, a, fake := mkUploadTree(t, dialog, nil)
	a.setFocus(up)

	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Enter"})
	if len(dialog.openCalls()) != 1 {
		t.Fatalf("Enter 该发起一次选择, got %d", len(dialog.openCalls()))
	}
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: " "})
	if len(dialog.openCalls()) != 2 {
		t.Fatalf("空格该发起一次选择, got %d", len(dialog.openCalls()))
	}
	// 非 multiple: 第二次选择是替换, 列表里始终只有一个
	if paths := up.uploadPaths(); len(paths) != 1 || paths[0] != "/tmp/a.txt" {
		t.Fatalf("非 multiple 下第二次应替换, got %v", paths)
	}
}

func TestUploadMouseClickActivates(t *testing.T) {
	dialog := &fakeDialogHost{openPath: "/tmp/a.txt", openOk: true}
	_, up, a, fake := mkUploadTree(t, dialog, nil)
	a.redraw()

	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: up.Box.X + 3, Y: up.Box.Y + 3})
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: up.Box.X + 3, Y: up.Box.Y + 3})
	if len(dialog.openCalls()) != 1 {
		t.Fatalf("点击该发起一次选择, got %d", len(dialog.openCalls()))
	}
	if a.focused != up {
		t.Fatalf("点击后焦点该在字段上, got %v", a.focused)
	}
}

// ===== 布局 / 像素 =====

func TestUploadIntrinsicAndDashedBorder(t *testing.T) {
	_, up, _, _ := mkUploadTree(t, nil, nil)
	if up.Box.H != selectRowH {
		t.Fatalf("字段行高 = %d, want %d", up.Box.H, selectRowH)
	}
	if up.Box.W < upFieldMinW {
		t.Fatalf("字段宽度 %d 小于最小值 %d", up.Box.W, upFieldMinW)
	}

	img := renderTree(up, 320, 80)
	// 顶边是**虚线**: 既有边框色像素, 又不铺满整行 (铺满就成了实线输入框)
	top := Rect{X: up.Box.X, Y: up.Box.Y, W: up.Box.W, H: 1}
	n := countColor(img, top, pxInputEdge)
	if n == 0 {
		t.Fatalf("字段该有边框")
	}
	if n >= up.Box.W {
		t.Fatalf("边框该是虚线 (画了 %d/%d 个像素)", n, up.Box.W)
	}
	// 左侧该有文件夹图标 (非白像素)
	iconBox := Rect{X: up.Box.X + fieldPadX, Y: up.Box.Y, W: upFieldIconW, H: up.Box.H}
	if c := countColor(img, iconBox, pxInputEdge); c == 0 {
		if c2 := countNonWhite(img, iconBox); c2 == 0 {
			t.Fatalf("字段左侧该画出文件夹图标")
		}
	}
}

// countNonWhite 统计区域内的非白像素数 (图标是 placeholder 灰, 与边框同色时
// 会与边框像素混在一起, 拿"非白"当兜底判据)。
func countNonWhite(img *image.RGBA, r Rect) int {
	n := 0
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			if img.RGBAAt(x, y) != pxWhite {
				n++
			}
		}
	}
	return n
}

// ===== aria =====

func TestUploadA11yShape(t *testing.T) {
	up := &GuiNode{Tag: "upload", Props: map[string]object.Value{}}
	withStr(up, "placeholder", "上传简历")
	if !up.focusable() {
		t.Fatalf("upload 该进 Tab 序")
	}
	if got := up.AriaRole(); got != "group" {
		t.Fatalf("role = %q, want group", got)
	}
	if got := up.AriaName(); got != "上传简历" {
		t.Fatalf("无障碍名该回落到 placeholder, got %q", got)
	}
}

// ===== 路径工具 =====

func TestUploadBaseName(t *testing.T) {
	cases := map[string]string{
		"/tmp/a.png":        "a.png",
		`C:\Users\me\b.jpg`: "b.jpg",
		"/tmp/dir/":         "dir", // 末尾分隔符: 取到最后一个非空段
		"plain.txt":         "plain.txt",
		"/":                 "/",
	}
	for in, want := range cases {
		if got := uploadBaseName(in); got != want {
			t.Fatalf("uploadBaseName(%q) = %q, want %q", in, got, want)
		}
	}
	if strings.Contains(uploadBaseName("/a/b/c.txt"), "/") {
		t.Fatalf("文件名里不该还有分隔符")
	}
}

# 视频能力决策记录（T09-6）

> **结论先行：内核不做视频解码，但做 `<video>` 标签与宿主契约。**
>
> `<video>` 组件已在内核落地（S8，2026-10-01，`gfx/video.go`）：它定义属性与事件，
> 把"在这个窗口区域里播这个文件、播放/暂停/跳转/调音量"翻译成宿主调用
> （`nativeVideoHost`，即**平台视频层**：Windows MF / macOS AVPlayerLayer /
> Android SurfaceView / iOS AVPlayerLayer）。
>
> **没有平台视频层的后端降级为封面/占位 + 一次诚实的 `onError`** —— 内核绝不假装在播。
> `canIUse("video")` 照实回答，脚本据此在"内联播放"与"退回系统播放器"之间选路。
>
> 另外三个动作（**选** / **存** / **系统预览**）由 `gx/media` 提供。
> 本文记录这个取舍的依据与边界，避免后续重复讨论。

## 1. 现状盘点

先分清"视频的四个动作"分别落在哪：

| 动作 | 能力 | 现状 |
| --- | --- | --- |
| **选**一段视频（从相册/文件里挑） | `gx/media.chooseVideo()` | ✅ 已实现（`gfx/native_media.go`） |
| **存**一段视频（写回相册/文件） | `gx/media.save()` | ✅ 已实现 |
| 用**系统播放器**打开（跳出去播） | `gx/media.preview()` | ✅ 已实现（系统预览） |
| **应用内内联播放**（`<video src>` 在窗口里播） | `<video>` + `nativeVideoHost` | ✅ **契约已实现**（S8）；**解码不在内核**，交给平台视频层 |

也就是说，"视频"缺的从来不是"标签"，而是**解码器**——内核里没有、也不会有。

## 2. 为什么内核里没有解码器

### 2.1 软件光栅化器撑不起逐帧解码

Gox 的 GUI 是**纯 Go 软件光栅化**（`gfx/raster.go` 往 `*image.RGBA` 上画像素），三平台
共用一套绘制代码，没有 GPU、没有平台视频纹理。内联播放意味着：

1. **解码**：mp4/h264 需要第三方解码库（cgo 或纯 Go 实现）。纯 Go 的 h264 解码器性能
   不足以实时，cgo 则打破了"三平台零依赖、纯 Go 交叉编译"的整个发行链路。
2. **YUV → RGB 转换 + 缩放**：每帧都要做，纯 CPU 做 720p 全屏会直接把一帧的预算
   （16ms）吃光，与"局部重绘/脏矩形"的性能模型冲突。
3. **音视频同步**：要么带回放时钟，要么静音——前者是播放器工程，后者用户不接受。

这三条与"标签"无关，只与"谁来解码"有关。所以内核可以给标签、给事件、给受控语义，
**但绝不碰解码**。

### 2.2 正确的做法是"平台视频层"，不是重写一个播放器

真正把视频做好，是**在窗口里嵌一块平台原生视频表面**，解码和渲染交给系统。这是
一整套"平台媒体宿主能力"（`nativeVideoHost` 形状），属于**平台适配层**的工程：
每个平台都要单独验证，与纯 Go 组件的成本不是一个量级。

内核能做且该做的是把**接口**定下来（S8 已落地），让平台后端按同一个契约接入 ——
这与 `nativeDialogHost`（原生消息框）、`windowController`（改标题/尺寸）、
`capturer`（截屏）、`displayProvider`（多屏）是同一套做法：
**可选能力走可选接口，不扩 `Surface`**。

## 3. 因此确定的口径

### 3.1 内核不做的事（长期有效）

- **不做解码 / 色彩转换 / 音视频同步**：`gfx/` 里没有也不会有这个循环（理由见 §2.1）。
- **不内置播放器 UI**：`controls` 只是"请平台自带控件，有就画、没有就忽略"。需要自定义
  控件的业务本来就该自己拿平台播放器（`nativeVideoHost` 给的就是"一块能用的表面"）。
- **不假装能播**：没有任何平台视频层时，`canIUse("video")` 为 `false`，
  组件画封面/占位并派发一次 `onError({code:"unsupported"})`。

### 3.2 已经落地的部分（S8，2026-10-01）

**标签**：`<video>`，与其它内置元素一样"四处同步"（`node.go` 的 `knownTags`、
`layout.go` 的 `intrinsicSize` / `layoutNode`、`raster.go` 的 `drawNode`，以及
`scripts/check-registries.py` 的豁免表）。

| 面 | 内容 |
| --- | --- |
| 属性 | `src` / `poster` / `playing`（等价 `autoplay`）/ `muted` / `loop` / `volume` / `controls` / `fit`（`contain` 缺省、`cover`、`fill`）/ `width` / `height` / 以及通用的 `background` / `border` / `radius` / `disabled` |
| 事件 | `onReady` / `onPlay` / `onPause` / `onEnded` / `onTimeUpdate({currentTime, duration})` / `onError({code, message})` |
| 缺省尺寸 | 有 `poster` 用封面自然尺寸，否则 320×180（**必须非 0**，否则 0 尺寸子树会被整支跳过） |

**契约**（`gfx/video.go`）：

```go
type nativeVideoHost interface {
	VideoInlineSupported() bool
	VideoSurfaceAttach(r Rect, src string, opts VideoSurfaceOptions) (uint64, error)
	VideoSurfaceRect(id uint64, r Rect) error
	VideoSurfaceCommand(id uint64, cmd VideoCommand, value float64) error
	VideoSurfaceDetach(id uint64) error
}
```

- 由**窗口后端**（`Surface` 的实现者）可选实现，调用点做断言；断言落空即降级。
- `r` 是**窗口客户区**坐标（与事件坐标同一坐标系）。
- 宿主上报状态变化用 `gfx.ResolveVideoEvent(surfaceID, VideoEvent{Kind: …})`，
  内核再转成脚本回调；句柄对不上（表面已摘 / 窗口已关）时静默丢弃。
- 受控语义与其它输入类组件一致：**显示/播放状态只看 prop，用户操作只派发事件**。

**两处容易做错、已固定下来的实现细节**：

1. 平台表面是**叠在窗口之上的一层**，不属于 `*image.RGBA`，因此既不参与脏矩形 diff，
   也不在 `drawNode` 里画 —— 它的位置同步放在 `app.redraw()` 的 **`Layout()` 之后**
   （`flushVideoSurfaces`）。放在布局之前会同步上一帧的盒子，首帧全是 0。
2. 节点被 `disposeNode` 时**立刻**释放表面（`videoRelease`），不等下一次 flush：
   子树销毁后可能再也没有重绘（没有脏区就不进 `redraw`），那块表面会一直浮在窗口上。

**取证**：`gfx/video_test.go`（假宿主记录调用序列 + 降级告警/`onError` 次数 +
事件回填 + 封面/留边/用户底色的逐像素断言）；演示脚本 `testdata/video_demo.js`。

### 3.3 后端要接这块能力，需要做什么

1. 在窗口后端类型上实现上面 5 个方法（**不要改 `Surface` 接口**）。
2. `VideoInlineSupported()` **如实回答**：做不到就返回 `false`，内核会走降级路径。
   谎报的后果是脚本以为能播、canIUse 也说是 —— 但画面上什么都没有。
3. `VideoSurfaceAttach` 在给定客户区矩形里叠加平台视频表面并返回 > 0 的句柄；
   解码/渲染/音频全归你，内核只发 `play` / `pause` / `seek` / `volume` / `mute` / `loop`
   六条指令（**只在变化时下发**，宿主不必担心每帧被打断）。
4. 线程纪律：这些方法都可能在 VM 线程被调用，宿主自己负责与渲染线程串行
   （与其它可选能力一致：要么本来就是同一线程，要么自己加锁）。
5. 状态变化回填 `ResolveVideoEvent`，脚本侧 `playing` 写不写回由脚本决定。

## 4. 与看板 rxXqcf 的关系

rxXqcf 当初把 icon / 富文本 / spinner / 视频一并标了"已完成"，实际只有视频这条线
当时是**有意不做**（icon / spinner 已在 T09 补齐）。本记录把这条线的口径收口为：

- **视频**：标签与宿主契约已落地（S8）；**解码仍不做**，由平台视频层负责。
  脚本写 `<video>` 不再命中 `unknown tag`，而是"能挂上、能拿到事件、播不了会明说"。
- 富文本：见 `limits.md` 的说明——`text` 支持 `wrap`/`ellipsis`，行内混排样式（粗体/彩色
  片段）暂不做，需要时用 `row` 拼装。
- icon：`<icon name=... />` 已内置（T09-5）。
- spinner：已内置（T09-2）。

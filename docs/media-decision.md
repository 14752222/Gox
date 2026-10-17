# 软件媒体后端决策记录（S10，softmedia）

> **结论先行：`<video>` / `<audio>` 的"能播"分两层，softmedia 只做最外一圈。**
>
> | 层 | 谁 | 覆盖范围 | 现状 |
> | --- | --- | --- | --- |
> | 内核契约 | `gfx/video.go` / `gfx/audio.go` | 标签、属性、事件、受控语义、降级 | ✅ S8 / S9 已落地 |
> | **软件媒体后端** | **`gfx/softmedia`** | **标准库就能解码的格式：GIF / MJPEG / WAV** | ✅ **本次落地（Linux）** |
> | 平台媒体层 | `nativeVideoHost` / `nativeAudioHost` 的真宿主 | h264 / HEVC / AAC / MP3 / VP9…… | ❌ 未实现（`gfx/win32`、`gfx/cocoa` 等） |
>
> softmedia 的原则与内核一致：**能解则解，解不动就诚实降级**（占位 + `onError`），
> **绝不假装在播**。`canIUse("video")` / `canIUse("audio")` 在 softmedia 挂上时
> 回答 `true` —— 那是真话：能力确实在，只是这个文件的格式不在它的覆盖范围内。

本文记录取值范围、接入方式与线程纪律，避免后续重复讨论。

## 1. 为什么要有 softmedia

内核 S8 落地时三个桌面后端都没有 `nativeVideoHost`，于是 `<video>` **永远降级**
（`docs/video-decision.md` 第 1 节那张表里的"内联播放"一格其实是空的）。这不是
标签的问题，是"谁来解码"。

而"演示一段动画 / 播一声提示音"这类需求，**不需要 h264**：GIF 与 WAV 用标准库
（`image/gif`、`image/jpeg`、手写 RIFF 解析）就能解，MJPEG 只是几张 JPEG 拼起来。
为这些格式付出"每个平台写一个原生视频层"的成本不合理，但让它们永远降级也不合理
—— 于是有 softmedia：**纯 Go、零 cgo、只跑 Linux**（`//go:build linux`），
挂上它之后这些格式真的会播。

边界是刻意的：

- **不是播放器工程**。没有音视频同步、没有 YUV、没有 seek 索引优化、没有硬件加速。
- **不抢平台媒体层的活**。等 `nativeVideoHost` 的真宿主（Windows MF / macOS
  AVPlayerLayer）落地，softmedia 与它是**叠加**关系：真宿主支持 h264，
  softmedia 支持 GIF/WAV，两者可以并存（见第 4 节的接入形状）。

## 2. 覆盖范围与降级

| 媒体 | 格式 | 支持 | 失败时的行为 |
| --- | --- | --- | --- |
| 视频 | **GIF**（动画，含 disposal 0/1/2/3 合成） | ✅ | — |
| 视频 | **MJPEG**（**.mjpeg**，自定义的**裸 JPEG 拼接流**） | ✅ | — |
| 视频 | mp4 / mov / webm / mkv（h264、HEVC、VP9…） | ❌ | `errUnsupportedFormat` → `attach-failed` + `onError` |
| 音频 | **WAV**（RIFF，PCM 8/16/24/32 位 + IEEE float 32/64 + EXTENSIBLE） | ✅ | — |
| 音频 | mp3 / aac / flac / ogg | ❌ | 同上 |

两条实现细节值得知道：

- **MJPEG 不是标准容器**。它是"若干张 JPEG 首尾相接"，由 softmedia 自己定义
  （标准库没有 AVI/MJPEG 容器解析）。标准 MJPEG 播放器打不开它；它的用途是
  "给 Gox 一个零依赖可解码的动态影像格式"，脚本里别把它当 `.avi` 用。
- **格式判定在**读盘**之前**（看扩展名）。一次性 IO 错误（文件被替换、权限变化）
  不进缓存，下次照常重试；永久性格式错误进 `rejected` 缓存，避免内核每帧重试都
  再解一遍同一个 `.mp4`。

## 3. 接入形状：呈现包装器链

softmedia 经 `gfx.SetPresentWrapper` 挂上（`cmd/gox/softmedia_init.go`，
`//go:build linux`）。包装器链按注册顺序叠加，**先注册的包在里层**：

```
平台 Surface → GPU 合成（-tags gpu） → 软媒体视频帧合成
```

软媒体把视频帧写进 `*image.RGBA`，GPU 再把这张图上屏 —— 顺序必须如此（反了软媒体
就只能在 GPU 的纹理上写字，那需要另一套接口）。`gpu_present_init.go` 的文件名序在
`softmedia_init.go` 之前，天然就是这个顺序；两个文件头都写了这条约定。

包装器（`mediaSurface`）只做三件事：

1. **接管"像素怎么上屏"**：`ShowRegions` 时把各播放器的当前帧合成进 `*image.RGBA`，
   把那些矩形并进脏区（视频帧不属于任何节点，脏矩形看不见它，
   所以由 `gfx.InvalidateRect` 单独标脏）；
2. **实现 `nativeVideoHost` / `nativeAudioHost`**：内核的能力查找对它断言命中；
3. **盯窗口关闭**：`windowCloser.WindowClosed()` —— 不停机的话播放器时钟 goroutine
   会跟着 `gfx.Post` 的队列无限增长（泵已停，没人 Drain）。

### 3.1 可选能力在两个面上都查

包装器内嵌的是 `gfx.Surface` **接口**，inner 的可选方法不会被提升出来，所以能力断言
只查一个面就会出两种错：只查平台面 → 软媒体的视频能力看不见（永远降级）；
只查包装面 → 平台的剪贴板/光标/原生对话框/IME/鼠标捕获/waker 被包装挡掉。

内核因此统一走 `capabilityOf[T](a)`：**先查呈现链终点（包装面），再查平台面** ——
包装器实现了即视为有意接管（它可以内部再转发给平台）。软媒体据此转发了 `waker`
（`WaitEventsWake`，否则每个 Linux 应用空闲期会从"唤醒即醒"退化成有界轮询），
其余能力它一个不实现，全部落回平台面。

> 顺带修掉了一个既有缺口：GPU 模式下 win32/cocoa 的剪贴板与原生对话框之前是被
> 包装器挡掉的（`gpu.Surface` 没转发这些可选能力）。现在它们走同一条回退路径，
> 不再需要每个包装器把全部能力转发一遍。

## 4. 线程纪律

整个后端只有一条规则：**时钟 goroutine 不碰 VM**。

- 每个播放器一条时钟 goroutine：到点换帧 / 推 20ms 音频块，然后
  `gfx.InvalidateRect(盒子)`（视频）或睡够块时长（音频的 pacing）；
- 事件（ready/play/pause/ended/timeupdate）一律经 `gfx.Post` 投回 GUI 线程，
  在 `DrainTasks` 里 `ResolveVideoEvent` / `ResolveAudioEvent` 落到脚本回调；
- `ShowRegions` 里的帧合成发生在渲染线程（同一条 GUI 线程），只读当前帧索引。

时钟 goroutine 与渲染线程的唯一接触面是 `p.mu`；它拿锁时**只**调不拿锁的助手
（一次踩过：`applySeekLocked` 里调了会自己加锁的 `duration()` → 自死锁，测试挂
10 分钟才由 goroutine dump 抓住）。

## 5. 音频输出：PulseAudio + null sink

`pulseSink` 经 `purego.Dlopen("libpulse.so.0")` 绑 PulseAudio 的 simple API
（**零 cgo**，与 `gfx/cocoa` 同一手法）。没有 libpulse / 没有服务 / 没有设备时
**静默降级 null sink**：不放声，但时钟与事件照真 —— 无头环境照样能验证播放语义
（e2e 就是这么跑的）。

macOS / Windows 不挂 softmedia，所以输出只有这一条路；将来要接 CoreAudio / WASAPI
时，`audioSink` 是一个薄接口，换实现即可。

### 5.1 GPU 呈现轨下音频静默（第三方库冲突，非设计选择）

`-tags gpu` / `-tags gpu_cgo` 下 `pulse.go` 换成 `pulse_disabled.go`：不开
PulseAudio，音频一律 null sink。**视频不受影响**（GIF / MJPEG 是纯 stdlib 解码）。

原因是链接期硬冲突：go-webgpu 走 **goffi** 调 libc，而 goffi 与 purego **各自
带了一份 fakecgo**，两个同时链进同一个二进制时——

| CGO_ENABLED | 报错 |
| --- | --- |
| `0` | `duplicated definition of symbol _cgo_init, from goffi/internal/fakecgo and purego/internal/fakecgo` |
| `1` | `unhandled relocation for goffi_errno_location / dlopen / dlsym / dlerror` |

二选一，没法共存。取舍是"丢声音，保视频"：GPU 轨本身是呈现后端的实验轨，默认
构建（用户真正会用的那一条）完全不受影响。真要在这条轨上听声音，得先解上游的
fakecgo 冲突（或让 go-webgpu 改用 purego 做 dlopen）——那不是 Gox 该替第三方决定
的事。

## 5.2 媒体句柄由内核分配（多窗口串台修复）

`VideoSurfaceAttach` / `AudioAttach` 的 id 是**内核分配的进程级唯一句柄**，后端只
当不透明键存，不许自己从 1 编号。

原因是 `ResolveVideoEvent(id, ev)` 在**全局** `videoStates` 里按 id 回表。让后端
自己编号的话，两个窗口各自的第一个 `<video>` 都会拿到 1，事件就会串到别的窗口的
节点上——实测表现为"A 窗口的视频事件打进 B 窗口的脚本"（`globalThis` 还没赋值时
触发回调 → `Cannot read properties of undefined (reading 'push')`），且因 map 迭代
序随机而时好时坏。

配套的两处收尾：

- 关窗时 `videoReleaseForApp` / `audioReleaseForApp` 一次性摘掉该窗口的全部句柄
  （flush 只在有脏区重绘时跑，窗口关闭后永远等不到那一次）；
- 播放器 `post` 在**任务执行时**自检 `stopped`：gfx.Post 的队列是全局广播的，而
  Pump 在无窗口时不 drain，关窗那一刻压着的事件会留到下一个窗口的泵里才被执行
  ——那时旧脚本早已销毁，直接丢弃。

## 6. 演示与验证

- 脚本：`testdata/media_demo.js`（GIF + MJPEG + WAV + 一个 `.mp4` 边界框；
  **必须在仓库根目录运行** —— `src` 按进程工作目录解释）；
- fixture：`testdata/media_demo.gif` / `.mjpeg` / `.wav`（二进制，生成器是一次性的，
  不入库）；
- 测试：
  - `gfx/softmedia`（包内）：fit 几何、GIF disposal 合成、WAV 各位深往返、
    播放器时序、包装器生命周期；
  - `gfx/softmedia_e2e_test.go`（外部包 `gfx_test`）：真 VM + 真渲染路径 ——
    解码帧**进帧缓冲且随时间移动**、事件回填、格式拒绝、关窗停机。

## 7. 已知缺口（故意留给后面）

| 缺口 | 现状 | 什么时候该动 |
| --- | --- | --- |
| h264 / AAC 等 | 降级 + `attach-failed` | 平台媒体层（`nativeVideoHost` 真宿主）落地时 |
| 平台原生音频层 | 无 | 与上面同期；那时 `capabilityOf` 的两面查找天然让两者并存 |
| 音视频同步 | 无（视频与音频各走各的时钟） | 需要"带音轨的视频"时 —— 那是播放器工程，不在本包范围内 |
| MJPEG 容器 | 自定义裸拼接流 | 若需要喂真实设备产出的 MJPEG，得先定容器（AVI 解析） |
| seek 精度 | 按累计帧时长找目标帧（够用，不精确） | 有真实需求时再说 |

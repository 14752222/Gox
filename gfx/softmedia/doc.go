//go:build linux

// Package softmedia 是 gfx 的**软件媒体后端** —— 用纯 Go 标准库可解码的
// 格式, 把 `<video>` / `<audio>` 从"契约 + 降级"变成"真的能播"。
//
// # 它是什么
//
// 内核 (gfx/video.go, gfx/audio.go) 只定义契约: `<video>` / `<audio>` 的
// 属性、事件、受控语义, 以及可选的 nativeVideoHost / nativeAudioHost 能力
// 接口。桌面三后端 (win32 / X11 / cocoa) 都没有实现这两个接口, 所以脚本
// 里写个视频框, 看到的是封面 + 一次诚实的 onError{unsupported}。
//
// softmedia 补上这一块, 但**只补标准库够得着的部分**:
//
//	视频: MJPEG (裸 JPEG 帧拼接) 与 GIF —— image/jpeg + image/gif 解码,
//	      帧直接合成进 *image.RGBA (走 gfx.SetPresentWrapper 呈现包装器);
//	音频: WAV (RIFF PCM / float) —— 自己解析, 样本经 PulseAudio simple API
//	      (purego dlopen, 零 cgo) 输出; 没有声卡/Pulse 时降级 null sink
//	      (不放声, 但播放时钟与事件全部照真)。
//
// # 它不是什么 (边界, 与 docs/media-decision.md 一致)
//
//   - 不是播放器: h264 / hevc / vp9 / av1 与 mp3 / aac / ogg / flac 一概
//     不解。那些留给**平台媒体层** (Windows MF / macOS AVPlayerLayer /
//     Linux GStreamer), 形状就是内核的 nativeVideoHost / nativeAudioHost
//     契约 —— softmedia 自己就是这两个接口的一份参考实现。
//   - 不追求性能: 逐帧全解码 + 最近邻缩放是"能播"的量级, 不是"好用"的
//     量级。要 1080p h264, 请接平台层。
//
// # 接入方式
//
// 宿主入口 (cmd/gox) 在启动时装一次呈现包装器:
//
//	import _ "github.com/14752222/Gox/gfx/softmedia" // Linux: init 里装
//
// 或显式:
//
//	gfx.SetPresentWrapper(softmedia.Wrap)
//
// 包装器拿到的 inner 仍然负责事件、尺寸、窗口操作; softmedia 只替换
// "像素怎么上屏" (把媒体帧合成进去), 并**额外**实现两个可选能力接口
// (videoHostOf / audioHostOf 对包装后的 Surface 断言命中)。
//
// # 线程模型
//
//   - 包装器方法 (Attach/Command/Detach/ShowRegions) 在 VM/渲染线程被调用;
//   - 每个播放器一条时钟 goroutine: 推进帧/样本, 调 gfx.InvalidateRect 让
//     那一块矩形重绘 ( ShowRegions 里合成新帧 );
//   - 事件 (ready/play/pause/ended/timeupdate/error) 一律经 gfx.Post 投回
//     GUI 线程再 ResolveVideoEvent / ResolveAudioEvent —— 时钟 goroutine
//     永远不直接碰 VM;
//   - 播放器内部状态全部由自己的 mutex 保护, ShowRegions 只读"当前帧"。
package softmedia
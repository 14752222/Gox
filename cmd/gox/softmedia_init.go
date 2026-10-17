//go:build linux

// softmedia 软件媒体后端的接入点 (S10)。
//
// 整个文件只有 linux 才编译: softmedia 只解 stdlib 能解的格式 —— 视频
// MJPEG (自定义的裸 JPEG 拼接流) / GIF, 音频 WAV (RIFF PCM/float) —— 加
// PulseAudio 输出, 两者都是 Linux 桌面的事实标准; h264/aac/mp3 留给平台
// 媒体层, 边界见 docs/media-decision.md。
//
// ## 与 GPU 包装器的层次 (链式叠加, 见 gfx.SetPresentWrapper)
//
// gfx 的呈现包装器是链: 先注册的包在里层。本文件名 (softmedia_init.go)
// 字典序在 gpu_present_init.go 之后, 于是 -tags gpu 同时启用时层次是:
//
//	平台 Surface → GPU 合成 → 软媒体视频帧合成
//
// 软媒体把视频帧写进 *image.RGBA, GPU 再把这张图上屏 —— 顺序必须如此
// (反了软媒体就只能在 GPU 的纹理上写字, 那需要另一套完全不同的接口)。
// 改名 / 挪这个文件之前先读上面这段。
//
// 为什么读环境变量的代码不在这里: softmedia 没有"生产关闭"的档位 ——
// 它的降级全在内部 (无 PulseAudio → null sink, 时钟照真; 解不动的格式 →
// Attach 失败 → 内核 attach-failed 降级 + onError)。多一个开关就多一条
// 没人测的路径。

package main

import (
	"github.com/14752222/Gox/gfx"
	"github.com/14752222/Gox/gfx/softmedia"
)

func init() {
	// Wrap 对 nil inner 返回 ok=false (gfx 的 wrapPresent 还有一道跳过),
	// 这里不重复判空。包装器本身不失败: 挂上来只是让 <video>/<audio> 从
	// "内核降级占位" 变成 "真的播"; 能解则解, 不能解走上面的降级链。
	gfx.SetPresentWrapper(softmedia.Wrap)
}

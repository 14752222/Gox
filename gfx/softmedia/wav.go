//go:build linux

package softmedia

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
)

// ===== softmedia 的音频格式口径 =====
//
// 只解 WAV (RIFF): 标准库有 encoding/wav, 但那个包只收 PCM 且把样本都转成
// int, 24-bit 还会溢出 —— softmedia 自己解析 (RIFF 就四块: fmt/data/其它),
// 换来三样自己要用的东西: float 样本 (音量缩放不失真)、float32 格式
// (PulseAudio 直接吃)、以及 24-bit 的正确处理。
//
// mp3 / ogg / flac / m4a / aac 一概不解 (errUnsupportedFormat): 那是平台
// 音频层的活, 见 docs/media-decision.md。

// audioClip 是解码后的音频: 交错 float 样本 (-1..1), 归一化到 -1..1。
type audioClip struct {
	Samples  []float32 // 交错 (多声道按帧交错)
	Channels int
	Rate     int
}

// Seconds 报告时长。
func (c *audioClip) Seconds() float64 {
	if c.Channels == 0 || c.Rate == 0 {
		return 0
	}
	return float64(len(c.Samples)/c.Channels) / float64(c.Rate)
}

// decodeWAV 解析 RIFF/WAVE, 支持 PCM(1) 与 IEEE float(3), 8/16/24/32 位
// 整数与 32/64 位浮点, 以及 WAVE_FORMAT_EXTENSIBLE (0xFFFE, 取子格式)。
func decodeWAV(path string) (*audioClip, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("softmedia: 读音频文件: %w", err)
	}
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return nil, fmt.Errorf("softmedia: 不是 RIFF/WAVE 文件 (%w)", errUnsupportedFormat)
	}

	var (
		format   uint16
		channels int
		rate     int
		bits     int
		pcm      []byte
	)
	pos := 12
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		size := int(le32(data[pos+4 : pos+8]))
		body := data[pos+8:]
		if size > len(body) {
			size = len(body) // 截断的块: 有多少用多少 (网上不少 wav 头是虚的)
		}
		body = body[:size]
		switch id {
		case "fmt ":
			if len(body) < 16 {
				return nil, fmt.Errorf("softmedia: fmt 块残缺 (%w)", errUnsupportedFormat)
			}
			format = le16(body[0:2])
			channels = int(le16(body[2:4]))
			rate = int(le32(body[4:8]))
			bits = int(le16(body[14:16]))
			if format == 0xFFFE && len(body) >= 26 {
				// EXTENSIBLE: 真格式在子格式头两字节 (fmt 扩展的 cbSize>=22 才有)
				format = le16(body[24:26])
			}
		case "data":
			pcm = body
		}
		pos += 8 + size + size&1 // 块按偶数对齐, 奇数尺寸补 1
	}
	if pcm == nil {
		return nil, fmt.Errorf("softmedia: WAV 里没有 data 块 (%w)", errUnsupportedFormat)
	}
	if channels <= 0 || rate <= 0 || bits <= 0 {
		return nil, fmt.Errorf("softmedia: fmt 块不完整 (channels=%d rate=%d bits=%d)", channels, rate, bits)
	}

	samples, err := pcmToFloat(pcm, int(format), bits)
	if err != nil {
		return nil, err
	}
	return &audioClip{Samples: samples, Channels: channels, Rate: rate}, nil
}

// pcmToFloat 按格式/位深把字节流转成 float 样本 (整数按满量程归一化)。
func pcmToFloat(pcm []byte, format, bits int) ([]float32, error) {
	switch format {
	case 1: // PCM 整数
		switch bits {
		case 8: // 无符号, 128 = 静音
			out := make([]float32, len(pcm))
			for i, b := range pcm {
				out[i] = (float32(b) - 128) / 128
			}
			return out, nil
		case 16:
			n := len(pcm) / 2
			out := make([]float32, n)
			for i := 0; i < n; i++ {
				out[i] = float32(int16(le16(pcm[i*2:]))) / 32768
			}
			return out, nil
		case 24:
			n := len(pcm) / 3
			out := make([]float32, n)
			for i := 0; i < n; i++ {
				v := int32(pcm[i*3]) | int32(pcm[i*3+1])<<8 | int32(pcm[i*3+2])<<16
				if v&0x800000 != 0 {
					v |= ^0xFFFFFF // 符号扩展
				}
				out[i] = float32(v) / 8388608
			}
			return out, nil
		case 32:
			n := len(pcm) / 4
			out := make([]float32, n)
			for i := 0; i < n; i++ {
				out[i] = float32(int32(le32(pcm[i*4:]))) / 2147483648
			}
			return out, nil
		}
	case 3: // IEEE float
		switch bits {
		case 32:
			n := len(pcm) / 4
			out := make([]float32, n)
			for i := 0; i < n; i++ {
				out[i] = float32frombits(le32(pcm[i*4:]))
			}
			return out, nil
		case 64:
			n := len(pcm) / 8
			out := make([]float32, n)
			for i := 0; i < n; i++ {
				out[i] = float32(float64frombits(pcm[i*8:]))
			}
			return out, nil
		}
	}
	return nil, fmt.Errorf("softmedia: 不支持的 WAV 编码 (format=%d bits=%d, %w)",
		format, bits, errUnsupportedFormat)
}

func float32frombits(b uint32) float32 {
	return math.Float32frombits(b)
}

func float64frombits(b []byte) float64 {
	return math.Float64frombits(binary.LittleEndian.Uint64(b))
}
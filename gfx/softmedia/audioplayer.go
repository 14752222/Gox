//go:build linux

package softmedia

import (
	"sync"
	"time"

	"github.com/14752222/Gox/gfx"
)

// audioPlayer 是一个 `<audio>` 节点的播放器: WAV 样本 + 一条时钟 goroutine,
// 输出经 PulseAudio (或 null sink)。
//
// 时钟形状与 videoPlayer 同构: goroutine 按 20ms 块推进, 事件经 gfx.Post
// 投回 GUI 线程。放音与时钟**同一条 goroutine** —— 块写完睡够块时长,
// 这就是 pacing: 真 sink 与 null sink 行为一致 (无头环境听不见, 但
// currentTime/ended 的时间轴完全是真的)。
type audioPlayer struct {
	id   uint64
	src  string
	clip *audioClip

	mu      sync.Mutex
	playing bool
	loop    bool
	muted   bool
	volume  float64
	pos     int     // 下一个待写样本的下标 (交错样本索引)
	seekSec float64 // >= 0 = 有待处理的跳转
	stopped bool

	wake   chan struct{}
	stopC  chan struct{}
	once   sync.Once
	sink   *pulseSink // nil = null sink
	chunkFrames int  // 每块帧数 (= rate/50, 20ms)

	lastEvt time.Time
}

func newAudioPlayer(src string, opts gfx.AudioPlayOptions, clip *audioClip) *audioPlayer {
	chunkFrames := clip.Rate / 50 // 20ms 一块
	if chunkFrames < 1 {
		chunkFrames = 1 // 怪速率 (rate<50) 也不能退化成热循环
	}
	return &audioPlayer{
		id:         0,
		src:        src,
		clip:       clip,
		loop:       opts.Loop,
		muted:      opts.Muted,
		volume:     opts.Volume,
		seekSec:    -1,
		wake:       make(chan struct{}, 1),
		stopC:      make(chan struct{}),
		chunkFrames: chunkFrames,
	}
}

func (p *audioPlayer) start() {
	p.sink = openPulse(p.clip.Channels, p.clip.Rate)
	go p.run()
}

func (p *audioPlayer) stop() {
	p.once.Do(func() {
		p.mu.Lock()
		p.stopped = true
		p.mu.Unlock()
		close(p.stopC)
	})
}

func (p *audioPlayer) command(cmd gfx.AudioCommand, value float64) {
	switch cmd {
	case gfx.AudioCmdPlay:
		p.mu.Lock()
		p.playing = true
		p.mu.Unlock()
		p.post(gfx.AudioEvent{Kind: "play", CurrentTime: p.currentTime(), Duration: p.clip.Seconds()})
		p.kick()
	case gfx.AudioCmdPause:
		p.mu.Lock()
		p.playing = false
		p.mu.Unlock()
		p.post(gfx.AudioEvent{Kind: "pause", CurrentTime: p.currentTime(), Duration: p.clip.Seconds()})
		p.kick()
	case gfx.AudioCmdSeek:
		p.mu.Lock()
		p.seekSec = value
		p.mu.Unlock()
		p.kick()
	case gfx.AudioCmdVolume:
		p.mu.Lock()
		p.volume = value
		p.mu.Unlock()
	case gfx.AudioCmdMute:
		p.mu.Lock()
		p.muted = value > 0.5
		p.mu.Unlock()
	case gfx.AudioCmdLoop:
		p.mu.Lock()
		p.loop = value > 0.5
		p.mu.Unlock()
	}
}

func (p *audioPlayer) kick() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// run 是时钟主循环: 20ms 块推进 + pacing 睡眠。
func (p *audioPlayer) run() {
	defer p.sink.close()
	p.post(gfx.AudioEvent{Kind: "ready", Duration: p.clip.Seconds()})
	for {
		p.mu.Lock()
		if p.stopped {
			p.mu.Unlock()
			return
		}
		// seek 在播放判断之前应用 (同 videoPlayer: 暂停中跳转也要生效)
		if p.seekSec >= 0 {
			p.applySeekLocked()
		}
		if !p.playing {
			p.mu.Unlock()
			select {
			case <-p.wake:
			case <-p.stopC:
				return
			}
			continue
		}
		if p.chunkFrames <= 0 || p.pos >= len(p.clip.Samples) {
			// 空片段 / 已到末尾: 到末尾且不循环 → 收尾
			if !p.loop {
				p.playing = false
				p.mu.Unlock()
				p.post(gfx.AudioEvent{Kind: "ended", CurrentTime: p.clip.Seconds(), Duration: p.clip.Seconds()})
				continue
			}
			p.pos = 0
		}
		end := p.pos + p.chunkFrames*p.clip.Channels
		if end > len(p.clip.Samples) {
			end = len(p.clip.Samples) // 尾块: 循环与不循环都先把这一段写完
		}
		chunk := p.clip.Samples[p.pos:end]
		// 音量/静音: 拷一份再缩放 (clip 是共享只读的)
		buf := make([]float32, len(chunk))
		vol, muted := p.volume, p.muted
		p.mu.Unlock()

		if muted {
			// 全零: 走 write 会让 Pulse 也收到静音块 (时钟仍真实)
		} else {
			for i, s := range chunk {
				buf[i] = s * float32(vol)
			}
		}
		p.sink.write(buf)

		// pacing: 睡够这一块的时长 ( sink 写完只是入队, 不等它播完 )
		d := time.Duration(len(buf)/p.clip.Channels) * time.Second / time.Duration(p.clip.Rate)
		if d > 0 {
			t := time.NewTimer(d)
			select {
			case <-t.C:
			case <-p.stopC:
				t.Stop()
				return
			}
			t.Stop()
		}

		p.mu.Lock()
		p.pos = end
		if p.pos >= len(p.clip.Samples) && p.loop {
			p.pos = 0
		}
		p.mu.Unlock()
		p.maybeTimeUpdate()
	}
}

// applySeekLocked 应用跳转 (调用方持 mu)。
func (p *audioPlayer) applySeekLocked() {
	target := p.seekSec
	p.seekSec = -1
	if target < 0 {
		target = 0
	}
	pos := int(target * float64(p.clip.Rate) * float64(p.clip.Channels))
	if pos >= len(p.clip.Samples) {
		pos = len(p.clip.Samples) - 1
	}
	if pos < 0 {
		pos = 0
	}
	p.pos = pos
	p.post(gfx.AudioEvent{Kind: "timeupdate", CurrentTime: target, Duration: p.clip.Seconds()})
}

// maybeTimeUpdate 节流派发 (同 videoPlayer: ~4/s)。
func (p *audioPlayer) maybeTimeUpdate() {
	now := time.Now()
	p.mu.Lock()
	if now.Sub(p.lastEvt) < 250*time.Millisecond {
		p.mu.Unlock()
		return
	}
	p.lastEvt = now
	cur := p.currentTimeLocked()
	dur := p.clip.Seconds()
	p.mu.Unlock()
	p.post(gfx.AudioEvent{Kind: "timeupdate", CurrentTime: cur, Duration: dur})
}

func (p *audioPlayer) currentTime() float64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.currentTimeLocked()
}

func (p *audioPlayer) currentTimeLocked() float64 {
	if p.clip.Channels == 0 || p.clip.Rate == 0 {
		return 0
	}
	return float64(p.pos/p.clip.Channels) / float64(p.clip.Rate)
}

// post 投回 GUI 线程。执行时自检 stopped, 理由同 videoPlayer.post:
// 关窗后残留在全局 Post 队列里的迟到事件会在**下一个**窗口的泵里被执行,
// 那时脚本已销毁 —— 在这里丢弃。
func (p *audioPlayer) post(ev gfx.AudioEvent) {
	id := p.id
	gfx.Post(func() {
		p.mu.Lock()
		stopped := p.stopped
		p.mu.Unlock()
		if stopped {
			return
		}
		gfx.ResolveAudioEvent(id, ev)
	})
}
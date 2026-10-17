// S10 演示: softmedia 软件媒体后端 (Linux 默认构建, 零 cgo)。
// 运行: ./gox testdata/media_demo.js   （**必须在仓库根目录运行**）
// 现象 (Linux 桌面, softmedia 已挂上呈现链):
//   1. 两个视频框**真的在播**: 上面是 GIF (10 帧, 1s 一轮, 循环), 下面是
//      MJPEG (softmedia 自定义的裸 JPEG 拼接流, 25fps) —— 帧由播放器的时钟
//      goroutine 推进, 经 gfx.InvalidateRect 标脏, 合成进帧缓冲后随局部重绘
//      上屏; contain / cover 两种 fit 都落位;
//   2. 音频条播 WAV (16bit/22050 单声道, 0.6s 双提示音): PulseAudio 输出,
//      无声卡/无服务时静默降级 null sink —— 听不见, 但时钟与事件照真;
//   3. 底部一行 canIUse 照实回答 true; ready/play/timeupdate/ended 经
//      onXxx 回到脚本 (计数显示在每框上方);
//   4. 最后一个 .mp4 框是**边界演示**: softmedia 只解 stdlib 能解的格式
//      (GIF/MJPEG/WAV), h264 留给平台媒体层 —— 它 attach-failed + onError,
//      绝不假装在播。
//
// 受控属性现场: 暂停/播放按钮把 playing signal 写回, 与 input 的 value 同一条
// 语义 —— 想要"播放中", 得把 signal 写回, 光设初值宿主看不到变化。
import { h, render } from "gx/gfx";
import { createSignal } from "gx/solid";
import { canIUse } from "gx/device";

// g_mediaEvents 是给测试/把玩时读的 (演示本身只用到计数与时间)。
// 事件名写死在回调里而不去读 e.kind: 内核的 ready/play/pause/ended 是**不带
// 实参**回调的 (见 ResolveVideoEvent), 只有 timeupdate 才给 {currentTime,
// duration} 对象 —— 在 undefined 上取属性会抛, 演示脚本不该依赖这件事。
globalThis.g_mediaEvents = [];
const rec = (tag, name) => () => {
  globalThis.g_mediaEvents.push(tag + ":" + name);
};

const [gifPlaying, setGifPlaying] = createSignal(true);
const [wavPlaying, setWavPlaying] = createSignal(true);
const [gifT, setGifT] = createSignal(0);
const [failed, setFailed] = createSignal(0);

render(
  <window title="Media demo (softmedia)" width={340} height={560}>
    <column gap={6} padding={12}>
      <text font={13} color="#8a8a8a">GIF 内联播放 (contain, 循环)</text>
      <video
        src="testdata/media_demo.gif"
        width={128}
        height={96}
        playing={gifPlaying()}
        loop={true}
        onReady={rec("gif", "ready")}
        onPlay={() => { rec("gif", "play")(); setGifPlaying(true); }}
        onPause={() => { rec("gif", "pause")(); setGifPlaying(false); }}
        onEnded={rec("gif", "ended")}
        onTimeUpdate={(e) => setGifT(e.currentTime)}
        onError={(e) => { globalThis.g_mediaEvents.push("gif:err:" + e.code); setFailed(failed() + 1); }}
      />
      <row gap={8}>
        <button onClick={() => setGifPlaying(!gifPlaying())}>
          {() => gifPlaying() ? "暂停 GIF" : "播放 GIF"}
        </button>
        <text font={12} color="#555555">{() => "t = " + gifT().toFixed(1) + "s / 1.0s"}</text>
      </row>

      <text font={13} color="#8a8a8a">MJPEG 内联播放 (cover: 裁满整盒)</text>
      <video
        src="testdata/media_demo.mjpeg"
        width={128}
        height={40}
        fit="cover"
        playing={true}
        loop={true}
        onReady={rec("mjpeg", "ready")}
        onPlay={rec("mjpeg", "play")}
        onTimeUpdate={rec("mjpeg", "t")}
        onEnded={rec("mjpeg", "ended")}
        onError={(e) => { globalThis.g_mediaEvents.push("mjpeg:err:" + e.code); setFailed(failed() + 1); }}
      />

      <text font={13} color="#8a8a8a">WAV 音频 (PulseAudio / null sink)</text>
      <audio
        src="testdata/media_demo.wav"
        width={300}
        height={36}
        playing={wavPlaying()}
        volume={0.6}
        onReady={rec("wav", "ready")}
        onPlay={() => { rec("wav", "play")(); setWavPlaying(true); }}
        onPause={() => { rec("wav", "pause")(); setWavPlaying(false); }}
        onEnded={() => { rec("wav", "ended")(); setWavPlaying(false); }}
        onTimeUpdate={rec("wav", "t")}
        onError={(e) => { globalThis.g_mediaEvents.push("wav:err:" + e.code); setFailed(failed() + 1); }}
      />
      <button onClick={() => setWavPlaying(!wavPlaying())}>
        {() => wavPlaying() ? "暂停音频" : "播放音频"}
      </button>

      <text font={13} color="#8a8a8a">边界: softmedia 不解 h264 ( attach-failed )</text>
      <video
        src="testdata/video_demo.mp4"
        width={128}
        height={72}
        playing={true}
        onError={(e) => { globalThis.g_mediaEvents.push("mp4:err:" + e.code); setFailed(failed() + 1); }}
      />

      <text font={13} color="#333333">
        {() => 'canIUse("video") = ' + canIUse("video") +
               ' · canIUse("audio") = ' + canIUse("audio") +
               ' · 出错框: ' + failed()}
      </text>
    </column>
  </window>
);

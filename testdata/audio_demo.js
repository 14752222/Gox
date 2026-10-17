// S9 演示: <audio> 音频条 (标签契约 + 宿主能力降级)。
// 运行: ./gox testdata/audio_demo.js   （**必须在仓库根目录运行**）
// 现象 (桌面三后端 win32 / X11 / cocoa 都没实现 nativeAudioHost):
//   1. 两个音频条都画占位 (浅色底 + 声波柱), 其中第二个的显式 background 不被盖掉;
//   2. stderr 打印**一次**告警 (进程级去重), 而每个条各派发一次
//      onError {code:"unsupported"} —— 所以底部那行计数会变成 2;
//   3. 底部一行 canIUse("audio") 照实回答 false, 脚本据此决定"内联播放还是
//      退回系统播放器" (选/存/预览由 gx/media 提供)。
//
// 两个容易踩的点:
//   - `src` 的相对路径按**进程工作目录**解释, 不是"脚本所在目录";
//   - `playing` / `muted` / `loop` / `volume` 是**受控**属性: 想要"播放中", 得把
//     signal 写回; 宿主上报的状态变化经 onPlay / onPause / onTimeUpdate / onEnded
//     回到脚本, 写不写回由脚本决定 (与 input 的 value 同一条语义);
//   - 音频**没有矩形**: 输出是进程级的 (声卡), 不属于任何窗口区域 —— 所以
//     契约里没有 Rect 同步, 占位条只是 controls 的可见落点。
import { h, render } from "gx/gfx";
import { createSignal } from "gx/solid";
import { canIUse } from "gx/device";

// 降级计数: 每个播不了的条都会报一次, 汇总成底部那行可见的提示。
// g_audioErrors 是给测试读的 (演示脚本本身只用到计数)。
globalThis.g_audioErrors = [];
const [failed, setFailed] = createSignal(0);
const onAudioError = (tag) => (e) => {
  globalThis.g_audioErrors.push(tag + ":" + e.code);
  setFailed(failed() + 1);
};

render(
  <window title="Audio demo" width={340} height={200}>
    <column gap={8} padding={12}>
      <text font={13} color="#8a8a8a">plain (占位条: 浅色底 + 声波柱)</text>
      <audio
        src="testdata/audio_demo.wav"
        controls
        width={300}
        height={36}
        onError={onAudioError("plain")}
      />

      <text font={13} color="#8a8a8a">显式 background (底色不归内核)</text>
      <audio
        src="testdata/audio_demo.wav"
        background="#123456"
        width={300}
        height={36}
        onError={onAudioError("bg")}
      />

      <text font={13} color="#333333">
        {() => 'canIUse("audio") = ' + canIUse("audio") + ' · 播不了的条: ' + failed()}
      </text>
    </column>
  </window>
);

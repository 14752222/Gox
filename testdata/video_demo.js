// S8 演示: <video> 视频框 (标签契约 + 宿主能力降级)。
// 运行: ./gox testdata/video_demo.js   （**必须在仓库根目录运行**）
// 现象 (桌面三后端 win32 / X11 / cocoa 都没实现 nativeVideoHost):
//   1. 给了 `poster` 的框按 contain 显示封面 (左右留边铺黑);
//   2. 没给 `poster` 的框画深色底 + 播放三角;
//   3. `fit="cover"` 的框把封面裁满整盒, 且显式 `background` 不会被盖掉;
//   4. stderr 打印**一次**告警 (进程级去重), 而每个框各派发一次
//      onError {code:"unsupported"} —— 所以底部那行计数会变成 3;
//   5. 底部一行 canIUse("video") 照实回答 false, 脚本据此决定"内联播放还是
//      退回系统播放器" (选/存/预览由 gx/media 提供)。
//
// 三个容易踩的点:
//   - `poster` 与 `src` 的相对路径都按**进程工作目录**解释, 不是"脚本所在目录";
//   - `playing` / `muted` / `loop` / `volume` 是**受控**属性: 想要"播放中", 得把
//     signal 写回; 宿主上报的状态变化经 onPlay / onPause / onTimeUpdate / onEnded
//     回到脚本, 写不写回由脚本决定 (与 input 的 value 同一条语义);
//   - 平台视频表面是**叠在窗口上的一层**, 不属于 *image.RGBA —— 它不参与脏矩形
//     重绘, 位置由 app.redraw() 在布局算完之后同步。
import { h, render } from "gx/gfx";
import { createSignal } from "gx/solid";
import { canIUse } from "gx/device";

// 封面就用 image 演示里那张四象限图: 四角异色, 一眼能看出采样方向对不对。
const COVER = "testdata/image_demo.png";

// 降级计数: 每个播不了的框都会报一次, 汇总成底部那行可见的提示。
// g_videoErrors 是给测试读的 (演示脚本本身只用到计数)。
globalThis.g_videoErrors = [];
const [failed, setFailed] = createSignal(0);
const onVideoError = (tag) => (e) => {
  globalThis.g_videoErrors.push(tag + ":" + e.code);
  setFailed(failed() + 1);
};

render(
  <window title="Video demo" width={340} height={720}>
    <column gap={8} padding={12}>
      <text font={13} color="#8a8a8a">poster + controls (封面按 contain 落位)</text>
      <video
        src="testdata/video_demo.mp4"
        poster={COVER}
        controls
        width={316}
        height={178}
        onError={onVideoError("cover")}
      />

      <text font={13} color="#8a8a8a">no poster -&gt; 深色底 + 播放三角</text>
      <video
        src="testdata/video_demo.mp4"
        width={316}
        height={178}
        onError={onVideoError("plain")}
      />

      <text font={13} color="#8a8a8a">fit="cover" + 显式 background (底色不归内核)</text>
      <video
        src="testdata/video_demo.mp4"
        poster={COVER}
        fit="cover"
        background="#123456"
        width={316}
        height={100}
        onError={onVideoError("coverbg")}
      />

      <text font={13} color="#333333">
        {() => 'canIUse("video") = ' + canIUse("video") + ' · 播不了的框: ' + failed()}
      </text>
    </column>
  </window>
);

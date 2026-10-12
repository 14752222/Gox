// 根组件：播放列表 + 可视化 + 控制条。
import { h } from "gox";
import { colors, space, font } from "./theme.js";
import {
  tracks, index, playing, pos, volume, mode, current,
  toggle, selectTrack, prev, next, cycleMode, setVolume,
} from "./store.js";
import { fmtTime, spectrum, MODE_LABEL } from "./lib/player.js";

// TrackRow: 播放列表一行。
const TrackRow = (p) => {
  const t = p.track;
  const i = p.i;
  return (
    <row
      gap={space.sm}
      padding={6}
      alignItems="center"
      background={() => (index() === i ? colors.selBg : colors.panel)}
      onClick={() => selectTrack(i)}
    >
      <text width={24} font={font.sm} color={colors.muted}>
        {() => (index() === i && playing() ? "▶" : String(i + 1))}
      </text>
      <text width={180} font={font.md} color={colors.text}>{t.title}</text>
      <text width={120} font={font.sm} color={colors.muted}>{t.artist}</text>
      <text font={font.sm} color={colors.muted}>{() => fmtTime(t.duration)}</text>
    </row>
  );
}

// Viz: 频谱画布。
const Viz = () => {
  return (
    <canvas
      width={640}
      height={110}
      background={colors.panel}
      onDraw={(ctx) => {
        const bars = spectrum(pos(), 48, playing());
        const bw = ctx.width / bars.length;
        for (let i = 0; i < bars.length; i++) {
          const hgt = bars[i] * (ctx.height - 8);
          ctx.fillRect(i * bw + 1, ctx.height - hgt, bw - 2, hgt, colors.bar);
        }
      }}
    />
  );
}

const Controls = () => {
  return (
    <column gap={space.xs}>
      <row gap={space.sm} alignItems="center">
        <text font={font.sm} color={colors.muted}>{() => fmtTime(pos())}</text>
        <progress width={480} value={() => pos() / current().duration} />
        <text font={font.sm} color={colors.muted}>{() => fmtTime(current().duration)}</text>
      </row>
      <row gap={space.sm} alignItems="center">
        <button padding={6} onClick={prev}>⏮</button>
        <button padding={6} onClick={toggle}>{() => (playing() ? "⏸" : "▶")}</button>
        <button padding={6} onClick={next}>⏭</button>
        <button padding={6} onClick={cycleMode}>{() => MODE_LABEL[mode()]}</button>
        <spacer flexGrow={1} />
        <text font={font.sm} color={colors.muted}>音量</text>
        <slider width={120} value={volume} onInput={(e) => setVolume(e.value)} />
        <text width={36} font={font.sm} color={colors.muted}>{() => Math.round(volume()) + ""}</text>
      </row>
    </column>
  );
}

export function App() {
  return (
    <column
      background={colors.bg}
      gap={space.sm}
      padding={space.md}
      onKeyDown={(e) => {
        if (e.key === " ") toggle();
        if (e.key === "ArrowLeft") prev();
        if (e.key === "ArrowRight") next();
      }}
    >
      <row gap={space.sm} alignItems="center">
        <text font={font.xl} color={colors.text}>{() => current().title}</text>
        <text font={font.md} color={colors.muted}>{() => current().artist}</text>
      </row>
      <Viz />
      <scroll height={210}>
        <column>
          <view each={tracks} key="id">
            {(t, i) => <TrackRow track={t} i={i} />}
          </view>
        </column>
      </scroll>
      <separator />
      <Controls />
    </column>
  );
}

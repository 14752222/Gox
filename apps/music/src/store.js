// 应用状态：播放列表 + 播放状态机。signal 用索引取值（引擎纪律）。
import { createSignal } from "gox";
import { nextIndex, prevIndex, nextMode } from "./lib/player.js";

const TRACKS = [
  { id: "t1", title: "晨光", artist: "Gox Ensemble", duration: 214 },
  { id: "t2", title: "城市轮廓", artist: "Gox Ensemble", duration: 187 },
  { id: "t3", title: "雨停之后", artist: "Side B", duration: 243 },
  { id: "t4", title: "低频散步", artist: "Side B", duration: 176 },
  { id: "t5", title: " midnight 编译", artist: "Bit Rot", duration: 201 },
  { id: "t6", title: "热重载", artist: "Bit Rot", duration: 158 },
];

const idxP = createSignal(0);
const playingP = createSignal(false);
const posP = createSignal(0); // 秒
const volP = createSignal(70);
const modeP = createSignal("all");

const index = idxP[0];
const playing = playingP[0];
const pos = posP[0];
const volume = volP[0];
const mode = modeP[0];

const tracks = () => TRACKS;

const current = () => {
  return TRACKS[idxP[0]()];
}

function play() { playingP[1](true); }
function pause() { playingP[1](false); }
function toggle() { playingP[1](!playing()); }

// selectTrack 点列表行：切歌并从头播。
const selectTrack = (i) => {
  idxP[1](i);
  posP[1](0);
  playingP[1](true);
}

const prev = () => {
  const n = prevIndex(index(), TRACKS.length);
  if (n >= 0) selectTrack(n);
}

const next = () => {
  const n = nextIndex(index(), TRACKS.length, mode());
  if (n < 0) {
    playingP[1](false);
    posP[1](0);
    return;
  }
  selectTrack(n);
}

const cycleMode = () => {
  modeP[1](nextMode(mode()));
}

const setVolume = (v) => {
  volP[1](Number(v));
}

// 模拟播放：每秒进度 +1，播完按模式切下一首。
setInterval(() => {
  if (!playing()) return;
  const p = pos() + 1;
  if (p >= current().duration) {
    next();
    return;
  }
  posP[1](p);
}, 1000);

export {
  tracks, index, playing, pos, volume, mode, current,
  play, pause, toggle, selectTrack, prev, next, cycleMode, setVolume,
};

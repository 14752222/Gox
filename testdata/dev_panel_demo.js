// devtools 方案 A 演示: gx/dev 快照 + 脚本自绘面板 (2026-09-19 拍板落地)。
// 运行: go run . testdata/dev_panel_demo.js
// 现象:
//   1. 面板每秒刷新一次: 帧计数 (整帧/局部/整帧率)、图片与字形缓存命中、
//      树规模 (窗口/节点/深度)、存活 effect 数。
//   2. 点 "bump" 按钮改一个 signal → 面板的"局部帧"数字涨; 拖窗口大小 →
//      "整帧"数字涨 —— 帧埋点与真实路径一一对应。
//   3. 警告区 (红字) 显示内核最近警告: 未知标签 / 图片加载失败 / 回调异常
//      都会在这里留副本 (stderr 同步输出, 这里是留存可查)。
//
// 要点 (docs/gui-patterns.md §5):
//   - 拉取式刷新: setInterval 1s 拉一次 devSnapshot, 不推送;
//     **别用 requestAnimationFrame** —— 它会和真实渲染抢帧。
//   - 面板就是普通脚本: 想看什么自己加一行, 数据面 (gx/dev) 是公共资产。
//   - 应用树坏掉时面板一起坏 (方案 A 的已知边界) —— 需要卡死现场可看时
//     再评估 HTTP 旁路 (方案 C)。
import { createSignal } from "gx/solid";
import { devSnapshot } from "gx/dev";
import { h, render } from "gx/gfx";

const [snap, setSnap] = createSignal(devSnapshot());
setInterval(() => setSnap(devSnapshot()), 1000);

const [n, setN] = createSignal(0);

const row = (label, value) => (
  <row gap={8}>
    <text font={13} color="#445">{label}</text>
    <text font={13}>{value}</text>
  </row>
);

render(
  <window title="gx/dev panel" width={460} height={380}>
    <column gap={6} padding={14}>
      <text font={16}>dev snapshot (1s pull)</text>
      {() => row("frame", `${snap().frame.count} = full ${snap().frame.full} + partial ${snap().frame.partial} (ratio ${snap().frame.fullRatio.toFixed(2)})`)}
      {() => row("image cache", `${snap().imageCache.size}/${snap().imageCache.cap}  hit ${snap().imageCache.hits}  miss ${snap().imageCache.misses}  evict ${snap().imageCache.evicts}`)}
      {() => row("glyph cache", `${snap().glyphCache.size}/${snap().glyphCache.cap}  hit ${snap().glyphCache.hits}  miss ${snap().glyphCache.misses}`)}
      {() => row("tree", `${snap().tree.windows} win / ${snap().tree.nodes} nodes / depth ${snap().tree.depth}`)}
      {() => row("solid effects", `${snap().solid.effects}`)}

      <row gap={10}>
        <button onClick={() => setN(n() + 1)}>bump ({() => n()})</button>
        <text font={12} color="#889">bump → partial frame; resize window → full frame</text>
      </row>

      <separator />
      <text font={13} color="#b00020">warnings ({() => snap().warnings.length})</text>
      <scroll height={110}>
        <column gap={2}>
          {() => snap().warnings.map((w) => (
            <text font={12} color="#b00020" wrap>{`${w.at}  ${w.text}`}</text>
          ))}
          {() => (snap().warnings.length === 0 ? <text font={12} color="#889">(none)</text> : null)}
        </column>
      </scroll>
    </column>
  </window>
);

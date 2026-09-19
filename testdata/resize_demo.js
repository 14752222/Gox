// 屏幕适配 A 演示: onResize + useWindowSize 模式 (2026-09-19 拍板落地)。
// 运行: go run . testdata/resize_demo.js
// 现象:
//   1. 拖动窗口边缘: 顶部文本实时显示当前窗口尺寸 (width×height, 物理像素)。
//   2. 宽度 < 480 时切到"窄栏"布局: 侧栏消失、说明文字换行、字号变小;
//      拉宽回来又恢复 —— 断点就是普通 memo, 布局全部走既有响应式机制。
//   3. 底部进度条的宽度始终跟随窗口 (width - padding*2), 不需要任何额外 API。
//
// 模式要点 (完整文档见 docs/gui-patterns.md §3):
//   - onResize 是**窗口级**事件, 挂在布局根上 (挂非根节点不会触发);
//     载荷 {width, height} 与设备 API 的 getSystemInfo 字段名统一。
//   - useWindowSize = 一个 signal + 根上的 onResize 写回, 三行的事;
//     断点定义在脚本里 (768 还是 480 是业务决策, 不烧进内核)。
//   - 防抖刻意不做进内核: 拖动时 WM_SIZE 连发, signal 每次都更新是正确语义
//     (布局本来就每帧跑); 嫌重自己 setTimeout 合并。
import { createSignal, createMemo } from "gx/solid";
import { h, render } from "gx/gfx";

// useWindowSize: 可复制的三行模式。初始值用窗口配置, 首次 resize 前也能用。
const [win, setWin] = createSignal({ width: 520, height: 360 });
const wide = createMemo(() => win().width >= 480);

const Sidebar = () => (
  <column width={140} gap={6} background="#eef1f6" padding={10}>
    <text font={13} color="#445">Sidebar</text>
    <text font={12} color="#889">wide only</text>
  </column>
);

let winHandle = null; // render() 的句柄 (onResize 里要用, 先占位)

winHandle = render(
  <window title="resize demo" width={520} height={360}>
    <column gap={10} padding={14} onResize={(e) => {
      setWin({ width: e.width, height: e.height });
      winHandle.setTitle(`resize demo - ${e.width}x${e.height}`); // 句柄 API: 标题跟随尺寸
    }}>
      <text font={16}>{() => `window: ${win().width} x ${win().height} (physical px)`}</text>
      <text font={13}>{() => (wide() ? "wide layout: sidebar visible" : "narrow layout: sidebar hidden")}</text>

      <row gap={10}>
        <button onClick={() => winHandle.resize(800, 500)}>resize 800x500</button>
        <button onClick={() => winHandle.resize(360, 240)}>resize 360x240</button>
      </row>

      <row gap={10}>
        {() => (wide() ? <Sidebar /> : <text font={12} color="#889">(narrow)</text>)}
        <column gap={6}>
          <text font={() => (wide() ? 14 : 12)} wrap>
            Drag the window edge, or use the buttons above (the
            winHandle.resize API). The breakpoint (480px) is a plain memo in this script.
          </text>
          <rect height={10} background="#3355aa" width={() => Math.max(60, win().width - 300)} />
        </column>
      </row>
    </column>
  </window>
);

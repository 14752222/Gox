// 画廊截图: <badge> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/badge.js
//
// badge 不是一个独立的盒子，而是**宿主节点的叠加修饰**: 它画在宿主
// (button / icon / 任何有尺寸的节点) 的右上角外侧。所以它必须挂在有尺寸
// 的宿主上才可见 —— 单独写 <badge count={3}/> 得到的是一个 0×0 的壳。
//
// dot 形态带 2px 背景描边（S7），是它能在任意底色上都成立的唯一原因 ——
// 深浅两行并排就是那个"任意底色"的证明。
import { render } from "gox";

render(
  <window title="badge" width={420} height={300}>
    <column gap={24} padding={24}>
      <row gap={32} alignItems="center">
        <button badge={3} onClick={() => {}}>消息</button>
        <button badge={128} max={99} onClick={() => {}}>通知</button>
        <button badgeDot={true} onClick={() => {}}>圆点</button>
      </row>
      <row gap={32} alignItems="center" background="#2d2d2d" padding={16}>
        <button badge={5} onClick={() => {}}>深底数字</button>
        <button badgeDot={true} onClick={() => {}}>深底圆点</button>
      </row>
    </column>
  </window>
);

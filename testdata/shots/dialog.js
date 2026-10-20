// 画廊截图: <dialog> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/dialog.js
//
// 开着的对话框才能拍到面板本身（卡片底、圆角、遮罩）。面板的 r16 与 55%
// scrim 是 S6 的验收点，遮罩深度只能在整帧上看出来。
import { createSignal, render } from "gox";

const [open, setOpen] = createSignal(true);

render(
  <window title="dialog" width={420} height={320}>
    <column padding={16} gap={12}>
      <text>背景内容（被遮罩压暗）</text>
      <button onClick={() => setOpen(true)}>打开对话框</button>
      <dialog open={() => open()} onClose={() => setOpen(false)}>
        <column gap={12} padding={16}>
          <text>确定要删除这一项吗？</text>
          <row gap={8}>
            <button onClick={() => setOpen(false)}>取消</button>
            <button background="#c0392b" color="#ffffff" onClick={() => setOpen(false)}>删除</button>
          </row>
        </column>
      </dialog>
    </column>
  </window>
);

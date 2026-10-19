// 画廊截图: <menu> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/menu.js
//
// 菜单是"点开才有内容"的组件，所以脚本一开始就把下拉展开（expanded），
// 否则拍到的只有一条灰色标题栏。项高、高亮底、快捷键色三者是 S6 的重点。
import { render } from "gox";

render(
  <window title="menu" width={420} height={320}>
    <column>
      <menu title="文件" expanded={true}>
        <menuitem onClick={() => {}}>新建</menuitem>
        <menuitem onClick={() => {}}>打开</menuitem>
        <menuitem onClick={() => {}}>保存</menuitem>
        <menuseparator />
        <menuitem onClick={() => {}} disabled={true}>打印</menuitem>
      </menu>
    </column>
  </window>
);

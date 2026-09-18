// P3-5 演示: 菜单栏 / 右键菜单 / 快捷键。
// 运行: go run . testdata/menu_demo.js
// 现象:
//   1. 窗口顶部一条菜单栏 (File / Edit / View, 右侧一个 status 文本)。
//      点标题展开下拉 (弹层溢出 26px 的菜单栏显示, 不被裁剪也不被内容盖住);
//      再点收起; 点另一个标题会直接换过去 (菜单栏互斥); 点画面别处收起。
//   2. File > New / Open 点了会在下方日志里留一行; Save 是禁用项 (灰字, 点了没反应);
//      View > Theme 会往右弹出子菜单 (Dark / Light)。
//   3. 在下方灰色矩形上点右键 → 在鼠标位置弹出右键菜单 (Copy / Paste);
//      靠窗口右下角右键时菜单会向左上翻折, 保证整块可见。
//   4. Ctrl+S / Ctrl+O / Ctrl+Q 走全局快捷键表 —— 不需要菜单展开就能触发,
//      回调里的 e.shortcut 是规范化后的文本。
//   5. 键盘: 焦点在菜单栏时 ← → 在标题间循环切换 (换过去就开着),
//      ↓/Enter/Space 展开, Esc 先收菜单 (再按一次才轮到别的弹层)。
//   - menubar 是**普通容器**: 脚本自己写 column { menubar; 内容 } 就行,
//     gfx 不会偷偷往 root 里插一条 —— 看到的树与写下的树始终一致。
//   - 右键菜单走数据式 API: onContextMenu: (e) => openContextMenu(e.x, e.y, [...])。
//     没做成 prop 是因为 JSX 元素是单次挂载的对象, 同一个 <menu> 挂到多个
//     组件上会互相争抢 Parent (节点只有一个 Parent 字段)。
import { createSignal } from "gx/solid";
import { h, window, render, openContextMenu } from "gx/gfx";

const [log, setLog] = createSignal("booting");
const [clicks, setClicks] = createSignal(0);

const say = (s) => {
  setClicks(clicks() + 1);
  setLog(`${s}  (#${clicks()})`);
};

render(
  <column>
    <menubar>
      <menu label="File">
        <menuitem label="New" shortcut="Ctrl+N" onClick={() => say("File > New")} />
        <menuitem label="Open" shortcut="Ctrl+O" onClick={() => say("File > Open")} />
        <separator />
        <menuitem label="Save" shortcut="Ctrl+S" onClick={() => say("File > Save")} />
        <menuitem label="Save As" disabled={true} onClick={() => say("不该被触发")} />
      </menu>

      <menu label="Edit">
        <menuitem label="Cut" shortcut="Ctrl+X" onClick={() => say("Edit > Cut")} />
        <menuitem label="Paste" shortcut="Ctrl+V" onClick={() => say("Edit > Paste")} />
      </menu>

      <menu label="View">
        <menuitem label="Zoom In" onClick={() => say("View > Zoom In")} />
        <menuitem label="Theme">
          <menu>
            <menuitem label="Dark" onClick={() => say("Theme > Dark")} />
            <menuitem label="Light" onClick={() => say("Theme > Light")} />
          </menu>
        </menuitem>
      </menu>

      <text>ready</text>
    </menubar>

    <column gap={12} padding={16}>
      <text font={18}>Menu / Context menu</text>
      <text>Right-click the panel below. Try Ctrl+S, or open File.</text>

      <rect
        width={340}
        height={140}
        background="#e8eef7"
        onContextMenu={(e) =>
          openContextMenu(e.x, e.y, [
            <menuitem label="Copy" shortcut="Ctrl+C" onClick={() => say("ctx > Copy")} />,
            <menuitem label="Paste" shortcut="Ctrl+V" onClick={() => say("ctx > Paste")} />,
            <separator />,
            <menuitem label="Inspect" onClick={() => say("ctx > Inspect")} />,
          ])
        }
      />

      <text>{() => log()}</text>
    </column>
  </column>,
  window({ title: "Menu demo", width: 520, height: 420 })
);

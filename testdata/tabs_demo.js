// S4 rPrfGD 演示: 选项卡 —— 标签条点击切页 / keep-alive / 受控绑定。
// 运行: go run . testdata/tabs_demo.js
// 现象: 上方受控 tabs 的激活页由 signal 驱动, 点标签条 → onChange 写回
//   signal → 镜像文本跟着变; 页内容是 keep-alive 的 —— 在"文件"页输入框
//   里打几个字, 切到别的页再切回来, 字还在。下方 tabs 不带 value, 是
//   非受控的: 点标签条直接切换, 不需要接线。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

const [tab, setTab] = createSignal(0);

render(
  <window title="Tabs demo" width={420} height={300}>
    <column gap={12} padding={16}>
      <tabs
        value={() => tab()}
        onChange={(e) => {
          setTab(e.index);
          console.log("controlled switch:", e.index, e.title);
        }}
      >
        <tab title="文件">
          <text>文件页: keep-alive 演示</text>
          <input width={240} placeholder="打几个字, 切走再回来还在" />
        </tab>
        <tab title="编辑">
          <text>编辑页</text>
        </tab>
        <tab title="视图">
          <text>视图页</text>
        </tab>
      </tabs>
      <text>{() => "受控激活页: " + tab()}</text>

      <tabs onChange={(e) => console.log("uncontrolled switch:", e.index, e.title)}>
        <tab title="Alpha">
          <text>非受控页一</text>
        </tab>
        <tab title="Beta">
          <text>非受控页二</text>
        </tab>
      </tabs>
    </column>
  </window>
);

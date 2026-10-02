// 画廊截图: <checkbox> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// (生成方式见 docs/dev-workflow.md 的「组件截图流水线」)。
// 运行: ./gox testdata/shots/checkbox.js
import { createSignal, render } from "gox";

const [agree, setAgree] = createSignal(true);

render(
  <window title="checkbox" width={360} height={200}>
    <column gap={12} padding={16}>
      {/* 纯受控: 勾上还是不勾完全由 checked 决定, 控件自身不存状态 */}
      <row gap={8} alignItems="center">
        <checkbox checked={() => agree()} onClick={() => setAgree(v => !v)} />
        <text>已同意</text>
      </row>

      <row gap={8} alignItems="center">
        <checkbox checked={false} onClick={() => {}} />
        <text>未勾选</text>
      </row>

      <row gap={8} alignItems="center">
        <checkbox checked={true} disabled={true} />
        <text>禁用</text>
      </row>
    </column>
  </window>
);

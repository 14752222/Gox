// 画廊截图: <switch> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// (生成方式见 docs/dev-workflow.md 的「组件截图流水线」)。
// 运行: ./gox testdata/shots/switch.js
import { createSignal, render } from "gox";

const [notify, setNotify] = createSignal(true);

render(
  <window title="switch" width={360} height={200}>
    <column gap={12} padding={16}>
      {/* 开与关两种填充态并排, 一眼能对出"轨道色 = 未选中" */}
      <row gap={8} alignItems="center">
        <switch checked={() => notify()} onClick={() => setNotify(v => !v)} />
        <text>通知已开</text>
      </row>

      <row gap={8} alignItems="center">
        <switch checked={false} onClick={() => {}} />
        <text>通知已关</text>
      </row>

      <row gap={8} alignItems="center">
        <switch checked={true} disabled={true} />
        <text>禁用</text>
      </row>
    </column>
  </window>
);

// 画廊截图: <datepicker> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// (生成方式见 docs/dev-workflow.md 的「组件截图流水线」)。
// 运行: ./gox testdata/shots/datepicker.js
//
// 拍的是**展开态** (生成器点一下字段)。日历开在有值的那个月 —— 给 value 是为了
// 截图不随"今天是几号"漂移; 「今天」那一格的强调色描边跟随真实日期, 重生成时
// 描边位置可能变, 这是预期 (组件语义如此, 不是渲染抖动)。
import { createSignal, render } from "gox";

const [due, setDue] = createSignal("2026-11-15");

render(
  <window title="datepicker" width={380} height={330}>
    <column gap={12} padding={16}>
      {/* 字段行: 当前值 + 右侧日历图标; 弹层贴字段下缘 (会盖住下方内容 —— 覆盖关系本身就是要拍的) */}
      <datepicker width={200} model={due} />
    </column>
  </window>
);

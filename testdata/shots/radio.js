// 画廊截图: <radio> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// (生成方式见 docs/dev-workflow.md 的「组件截图流水线」)。
// 运行: ./gox testdata/shots/radio.js
import { createSignal, render } from "gox";

const [size, setSize] = createSignal("M");

render(
  <window title="radio" width={360} height={200}>
    <column gap={12} padding={16}>
      {/* 互斥不在内核里: 一组 radio 共享同一个 signal, checked 写成"值相等" */}
      <row gap={14} alignItems="center">
        <row gap={4} alignItems="center">
          <radio name="size" checked={() => size() === "S"} onClick={() => setSize("S")} />
          <text>S</text>
        </row>
        <row gap={4} alignItems="center">
          <radio name="size" checked={() => size() === "M"} onClick={() => setSize("M")} />
          <text>M</text>
        </row>
        <row gap={4} alignItems="center">
          <radio name="size" checked={() => size() === "L"} onClick={() => setSize("L")} />
          <text>L</text>
        </row>
      </row>

      <row gap={8} alignItems="center">
        <radio checked={false} disabled={true} />
        <text>禁用</text>
      </row>

      <text>{() => "size = " + size()}</text>
    </column>
  </window>
);

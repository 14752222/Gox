// 画廊截图: <tabs> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/tabs.js
//
// 三页各占一屏的是同一组 tabs，这里只拍第一页（激活态）—— 重点是标签条的
// 槽底、选中片与指示条三者的层次关系（S5 把它们从"只有下划线"改成
// "槽底 + 白片 + 发丝边"）。
import { createSignal, render } from "gox";

const [page, setPage] = createSignal("a");

render(
  <window title="tabs" width={400} height={240}>
    <column padding={16}>
      <tabs value={() => page()} onChange={e => setPage(e.value)}>
        <tab title="账户">
          <column gap={8} padding={12}>
            <text>账户设置内容</text>
            <input placeholder="昵称" />
          </column>
        </tab>
        <tab title="通知">
          <text>通知设置内容</text>
        </tab>
        <tab title="关于">
          <text>版本 0.12.0</text>
        </tab>
      </tabs>
    </column>
  </window>
);

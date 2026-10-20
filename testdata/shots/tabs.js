// 画廊截图: <tabs> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/tabs.js
//
// 三页各占一屏的是同一组 tabs，这里只拍第一页（激活态）—— 重点是标签条
// 的三段层次：未选中 secondary 文字 + 标签间的 1px 发丝分隔线、选中
// colorText 文字 + 横贯标签的 2px 强调色指示条、条底那条 1px 分界线
// （Prism S5 把 v1 的"激活项整条染 accent"改成"文字降档 + 指示条承担选中"）。
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

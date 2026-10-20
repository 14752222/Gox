// 画廊截图: <tooltip> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/tooltip.js
//
// tooltip 自身布局透明，弹层靠 hover 才出现。截图取"鼠标停在触发区上"那一帧，
// 所以先派发一次 MouseMove（见 galleryClickTargets 的 hover 支持）。
import { render } from "gox";

render(
  <window title="tooltip" width={360} height={200}>
    <column gap={16} padding={16}>
      <tooltip text="这是提示文本" placement="bottom">
        <button onClick={() => {}}>悬停看提示</button>
      </tooltip>
    </column>
  </window>
);

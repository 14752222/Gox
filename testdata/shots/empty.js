// 画廊截图: <empty> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/empty.js
//
// 三个空态并排, 覆盖 S7 的三条验收点:
//   1. 插图 → 描述 → 副说明 → 操作, 整块在盒子里**垂直居中** (三个盒子都给
//      了 220 高, 内容高矮不同, 但都落在各自的中间);
//   2. 描述走 textSecondary、副说明走 textMuted —— 两级灰, 同色会被读成
//      "一句话折行了"; 插图只是占位, 用更淡的 track 色, 不抢读;
//   3. 内边距缺省 Space6 (16), 三个盒子四周都留得出呼吸。
import { render } from "gox";

render(
  <window title="empty" width={640} height={260}>
    <row gap={12} padding={16}>
      <column width={192} height={220}>
        <empty height={220} desc="暂无数据" />
      </column>
      <column width={192} height={220}>
        <empty height={220} desc="没有文件" subDesc="拖到此处上传">
          {/* 插图是占位, 给淡灰而不是继承的正文黑 —— 它不该抢读 */}
          <icon name="folder" size={40} color="#d0d0d0" />
          <button>
            <text>上传</text>
          </button>
        </empty>
      </column>
      <column width={192} height={220}>
        <empty height={220} desc="搜索无结果" subDesc="换个关键词">
          <icon name="search" size={40} color="#d0d0d0" />
        </empty>
      </column>
    </row>
  </window>
);

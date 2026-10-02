// 画廊截图: <upload> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// (生成方式见 docs/dev-workflow.md 的「组件截图流水线」)。
// 运行: ./gox testdata/shots/upload.js
//
// 字段行是虚线边框 + 文件夹图标; 已选文件显示**文件名** (不含路径)。
// 注意: 点它弹的是平台原生对话框, 截图里拍不到 —— 这张拍的是"已选/未选"两种字段态。
import { render } from "gox";

render(
  <window title="upload" width={380} height={200}>
    <column gap={12} padding={16}>
      {/* 受控: 显示只看 value (字符串数组 = 路径, 对象数组取 path 字段) */}
      <upload
        width={280}
        value={["/Users/me/Documents/report.pdf", "/Users/me/Pictures/logo.png"]}
        multiple
      />

      {/* 未选: 灰字 placeholder */}
      <upload width={280} placeholder="选择附件" />
    </column>
  </window>
);

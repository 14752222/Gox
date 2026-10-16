// 画廊截图: gx-datav 数据大屏 —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// (生成方式见 docs/dev-workflow.md 的「组件截图流水线」)。
// 运行: ./gox testdata/shots/datav-dashboard.js
//
// 内容就是 ../datav/dashboard.js 的完整大屏 (import 即执行其 render),
// 一张图覆盖 gx-datav 全部 13 个组件 —— 它是"这套库能画出整屏"的活体
// 回归用例: 任何一个组件的绘制路径被改坏 (边框不画、饼图缺片、表格
// 不轮播), 这张截图的非空白断言或颜色种类就会掉。
import "../datav/dashboard.js";

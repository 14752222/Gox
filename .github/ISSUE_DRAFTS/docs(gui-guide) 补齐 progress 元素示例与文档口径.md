labels: ["good first issue"]
### 要解决什么问题

`testdata/` 有 84 个示例，`docs/gui-guide.md`（856 行）对 `<progress>` 只有一行元素表
（L122：value/background/width/height），没有「确定 / 不确定两种形态」「与 signal 联动」
的写法说明，新用户只能翻源码。

### 期望的产出

1. 核对 `<progress>` 的全部 JSX 属性（源码：`gfx/node.go` knownTags、`gfx/layout.go`
   intrinsicSize、`gfx/raster.go` drawNode 的 progress 分支）；
2. `docs/gui-guide.md` 元素参考表补完整口径（属性名/类型/默认值/是否可传函数），
   并加一小节「signal 驱动的进度条」惯用法；
3. `testdata/` 新增或增强一个「signal 驱动 progress」的示例（如模拟下载进度条，
   每帧 +2% 到 100% 后复位）；
4. 涉及语义改动时同步 `website/en/api/gx.md`（en 侧无独立 components 页）。

### 验收

- 示例脚本 `gox testdata/progress_signal_demo.js` 直接可跑；
- 文档改动 Grep 旧说法确认无残留。

---

参与方式见 [CONTRIBUTING.md](https://github.com/14752222/Gox/blob/main/CONTRIBUTING.md)。

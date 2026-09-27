labels: ["good first issue"]
### 要解决什么问题

`<slider>` 目前只能鼠标拖动调节（`gfx/slider.go` 全文无任何键盘事件处理）。
对键盘用户与无障碍场景，滑块不可用。

### 期望的行为

- 聚焦时 ←/↓ 减、→/↑ 增，步长与拖动一致（或按 `step` 属性）；
- Home/End 跳到最小/最大；
- 触发与拖动相同的事件（`onInput`，若已区分 `onChange` 则一并对齐）。

### 入口提示（给上手的人）

- 实现在 `gfx/slider.go`（绘制 `paintSlider` 在 raster 侧，无需动）；
- 键盘事件如何到达组件：参考 `gfx/node.go` 与既有键盘处理组件的写法；
- 新增行为请同步补 `testdata/slider_demo.js` 或新增一个键盘调节示例；
- 并在 `docs/gui-guide.md` §12 症状速查表核对无需变更；
- 本地闸门：`gofmt -l . && go build ./... && go test ./gfx/`。

### 验收

- 一个可运行的 demo 脚本：Tab 聚焦滑块后纯键盘完成一次调节；
- `go test ./gfx/` 全绿。

---

参与方式见 [CONTRIBUTING.md](https://github.com/14752222/Gox/blob/main/CONTRIBUTING.md)。

# multiscreen —— 多屏协同演示（跨端）

一个应用开**两个窗口**，演示 Gox 的多屏/多设备定位：

- **窗口几何**：读/写窗口位置（工作区相对、设备像素），居中、跨屏挪窗；
- **跨屏事件**：把窗口拖到另一块屏，两端同时出现换屏日志；
- **应用接续**：`router.handoff` 把 home 窗口的整条导航栈（含草稿状态）
  搬到 road 窗口 —— 与"手机 → 桌面"的跨设备接续**同一套语义**；
- **自适应断点**：`gx/viewport` 断点随窗口宽度换档，同一份代码
  在窄（手机档）/ 宽（桌面档）下自动换形态 —— 跨端的另一半。

## 运行

```bash
go build -o /tmp/gox-current ./cmd/gox
/tmp/gox-current apps/multiscreen/src/main.js
```

## 压到的能力

| 能力 | API |
|---|---|
| 窗口列表 | `gx/screen` 的 `windows()` |
| 窗口几何 | 句柄 `position()` / `bounds()` / `center()` / `moveTo(x, y)` |
| 跨屏事件 | `gx/screen` 的 `onWindowDisplayChange` |
| 应用接续 | `gx/router` 的 `handoff(from, to, {keepSource})` / `continuity()` |
| 断点布局 | `gx/viewport` 的 `useBreakpoint()` / `matchBreakpoint()` |

## 结构

```
src/
  main.js        入口：建 home / road 两个窗口
  app.js         两窗口共用的面板组件 + 断点布局
  store.js       signal：换屏日志 / 窗口列表 / 几何文本 / 路由
  theme.js       设计令牌
  lib/geo.js     纯逻辑：几何文本格式化
```

## 已知限制

- 跨设备接续（手机 → 桌面）的**传输层**不在内：设备配对 + 通道超出
  演示范围；这里的同机跨窗口接续与其语义完全一致（搬迁 + 源复位），
  `continuity()` 是给未来接续协调器做只读体检的口子。
- `render()` 暂不解析 `x/y/display`，初始按屏放置走 `moveTo`。

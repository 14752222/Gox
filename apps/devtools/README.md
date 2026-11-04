# Gox DevTools —— GUI 开发期调试面板

基于 `gx/dev` 只读数据面（`devSnapshot / devTree / devNode / devProps / devLogs / devEval`）
的**交互式 Inspector**。相对 `testdata/devtools_demo.js`（v1，纯展示）的升级：

| 能力 | v1 demo | 本应用 |
|---|---|---|
| 元素树 | 缩进文本，不可交互 | 原生 `<tree>` 组件：箭头展开/收起、点击选中、多窗口切换 |
| 节点详情 | 无 | 选中节点显示 `path / box / role / focusable` + 完整 props（`devProps`） |
| 控制台 | 固定最近 30 条 | 级别过滤（all/log/info/warn/error）、最近 100 条 |
| REPL | 单行结果 | 历史列表（最近 100 条）、结果/错误分色 |
| 性能 | 无 | `devSnapshot` 摘要行：帧计数/整帧占比/缓存命中率/节点统计/警告数 |
| 刷新 | 固定 1s | 500ms，可用开关暂停（暂停后手动「刷新」） |

## 运行

```bash
# 在本仓库根目录
GOX_DEV=1 ./gox apps/devtools/src/main.js        # Linux/macOS
set GOX_DEV=1 && gox.exe apps/devtools/src/main.js  # Windows
```

`GOX_DEV=1` 不是必须的，但建议开：内核诊断警告（比如 JSX 子节点写了 `{sig}`
而不是 `{sig()}` 这类「静默错误」）会进 `devSnapshot().warnings` 与 console 留存，
面板直接能看到。

## 调试别的应用

面板是**普通脚本**，与被调试应用同进程同环境时价值最大。两种方式：

1. **面板内嵌**：在你的应用里 `import "apps/devtools/src/app.js"` 不合适（会抢窗口），
   推荐方式 2。
2. **同窗口并排**：把 `<App />` 挂进你自己窗口的一侧（它是普通 column 组件），
   或将 store.js 的 `refresh()` 轮询复制进你的调试页。
3. **REPL 环境**：宿主在进事件循环前调过 `gfx.SetDevEnv(vm.Globals())` 时，
   REPL 在**真实 app 环境**里求值，能直接读写 app 的变量；否则是独立沙盒。
   见 `docs/devtools.md`「接线点」。

## 已知限制

- `gx/dev` 是**只读**数据面：面板不能改 props、不能高亮屏幕上的节点
  （没有叠加层协议）。选中节点后看 `box` 里的 x/y/w/h 自行对位。
- console 留存是内核的固定环形缓冲（500 条），面板侧只能过滤，不能清空。
- 树数据有界（深度 8 / 节点 800）；超限节点带 `…` 标记，头部会出现截断提示。

# 开发工作流

## gox dev：开发期间热更新

`gox dev` 会在进程内监听入口文件所在目录（递归含子目录）的 `.js` / `.ts` /
`.tsx` 等脚本文件变更，每次变更后丢弃旧 VM、新建 VM 并重新执行入口文件 ——
相当于不停进程的"重启脚本"，模块缓存随 VM 重建，不存在旧模块残留。

```bash
gox dev                  # 默认入口依次探测: src/main.js → main.tsx → main.ts → main.jsx
gox dev src/main.js      # 显式指定入口（.ts/.tsx 同样直接给路径）
```

TypeScript 工程（`gox create --ts` 生成）无需任何额外配置：`.ts` / `.tsx`
文件在每次加载前自动过类型剥离（内嵌 esbuild 转译，JSX 原样保留），热重载
时延实测约 250ms（防抖 250ms + 毫秒级转译），远低于"改完即见"的阈值。

行为细节：

- 监听 `.js` / `.jsx` / `.ts` / `.tsx` / `.mts` / `.cts` 的写入/创建/重命名；
  编辑器临时文件（vim 的 `4913`、`*.swp`、`*~`、`.#*`、`*.tmp` 等）与
  非脚本文件（css/json 等）不会触发重载。
- 防抖：250ms 窗口内的连续变更（如一次保存多个文件）合并为一次重载，
  日志形如 `[dev] reloaded main.js (3 changes)`。
- 脚本报错（转译错误、编译错误或运行时异常）只打印到终端，dev 会话继续监听，
  修好文件后下一次保存自动恢复。TS 转译错误自带 `文件:行:列` 与原文摘录。

## 没有 dev 命令时的替代方案

旧版 gox 或想用外部工具时，可以用通用文件监听器做"崩溃式重载"
（每次变更重启整个进程）：

```bash
# watchexec (跨平台: macOS/Linux/Windows)
watchexec -e js -r -- goxjs src/main.js

# entr (Linux/macOS)
ls src/*.js | entr -r goxjs src/main.js
```

## 适用边界与注意事项

- **GUI 常驻脚本暂不支持热更新**：`render(...)` 之后脚本依赖消息泵保活，
  泵循环会阻塞 dev 的监听循环，变更在窗口全部关闭之前不会生效。
  此时请用上面的外部监听方案（进程级重启，窗口随之重建）。
  > TODO: 后续可做「复用窗口句柄、热替换根元素树」的 GUI 热更新；
  > 元素树/signal 订阅的状态迁移语义需要先在 gfx 上游讨论。
- **重启会丢内存状态**：无论 gox dev 重建 VM 还是外部方案重启进程，
  store.js 模块作用域里的 signal 状态都会回到初始值，不会跨重载保留。
- **setInterval 等常驻定时器**同理会让 `RunTimers` 不返回，
  dev 循环拿不到控制权 —— 与 GUI 场景一样，建议用外部方案。

## 组件画廊截图流水线

官网组件页每节顶部那张图（`website/public/components/shots/<GOOS>/<组件>.png`）
不是手画的示意图，而是**真实上屏帧**：`testdata/shots/` 下每个组件一个最小示例
脚本，由 `gfx/gallery_shot_test.go` 挂到假 Surface 上离屏渲染后编码成 PNG。
弹层类（select / datepicker / colorpicker）由生成器点一下字段、拍展开态。

```bash
# 只渲染 + 断言非空白（普通 go test ./gfx/ 也会跑这条断言，不写文件）
go test ./gfx/ -run TestGalleryShotScripts -v

# 生成 PNG 到 <目录>/<GOOS>/<组件>.png
GOX_SHOTS_OUT=website/public/components/shots go test ./gfx/ -run TestGalleryShotScripts

# 调阈值 / 看每张图的统计（颜色种类、非底色像素数）
GOX_SHOTS_OUT=/tmp/shots GOX_SHOTS_STATS=1 go test ./gfx/ -run TestGalleryShotScripts -v
```

纪律（都有理由）：

- **新增内置组件要同步补一个 `testdata/shots/<组件>.js`**。这个脚本同时是
  "该组件能画出来"的活体回归用例 —— 四处注册表漏登记一个标签的症状就是
  "渲染成空盒子"，进程不报错，而非空白判据（颜色种类 / 非底色像素阈值）会红。
  生成器里的 `galleryClickTargets` 只列弹层类：收起态的日历/色板就是一行灰字。
- **PNG 不是 golden 文件**：字体来自各平台系统字体，三平台的像素本来就不相等，
  别拿它们做逐像素比对。仓库只提交一套（darwin），三平台对照靠
  `.github/workflows/desktop-shots.yml` 的 CI 矩阵 artifact。
- **离屏 ≠ 窗口后端**：这条链覆盖光栅化 + 平台字体栈，不覆盖 win32 / x11 /
  cocoa 的窗口创建与上屏。要验证真窗口，直接 `./gox testdata/shots/<组件>.js`
  跑起来看 —— 脚本本身就是按"可直接运行"写的。

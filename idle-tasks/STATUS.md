# Gox 桌面化闲时任务进度

> 本文件由定时自动化维护，每次运行前后更新。手工编辑仅限"备注"列或整体重置。

| 阶段 | 任务文件 | 状态 | 断点/备注 |
|---|---|---|---|
| P1 | idle-tasks/P1-solid-jsx.md | 已完成 | 2026-09-10 完成: gx/solid + JSX 降级 + 测试全绿, 报告见 IDLE_TASK_REPORT_P1.md |
| P2 | idle-tasks/P2-window-renderer.md | 已完成 | 2026-09-10 完成: gfx 包 + Win32 纯 syscall 窗口 + 软件渲染 + RunTimersWithPump 事件循环, 真窗口点击验收通过, 报告见 IDLE_TASK_REPORT_P2.md |
| P3 | idle-tasks/P3-fonts-layout-package.md | 已完成 | 2026-09-10 完成: 文字渲染(msyh/sfnt+glyph LRU) + flex 布局子集 + 脏矩形局部重绘(0.082ms/帧@1000节点) + 键盘/rAF + jsbuild --gui; counter_demo 源码与打包 exe 均验收通过, 报告见 IDLE_TASK_REPORT_P3.md |
| P4 | idle-tasks/P4-crossplatform.md | 已完成 | 2026-09-17 完成: gfx/backend 平台选择器 + gfx/x11 (jezek/xgb, 编译级验证) + gfx/cocoa (按止损规则降级为编译级占位+路径分析) + jsbuild --target 交叉编译 (linux/amd64 CLI+GUI 产出合法 ELF) + 分发文档; WSL 未安装故运行时验证按任务书条款说明, 报告见 IDLE_TASK_REPORT_P4.md |

状态取值：未开始 / 进行中 / 已完成 / 受阻。

阶段完成判定：该阶段任务文件中的验收标准全部满足，且对应报告文件
（仓库根目录 `IDLE_TASK_REPORT_P1.md` ~ `IDLE_TASK_REPORT_P4.md`）已写好。

## 运行日志

（每次运行追加一行：日期时间 | 目标阶段 | 本次结果 | 断点位置）

2026-09-10 | P1 | 已完成: signals(gx/solid 内置模块) + JSX(降级为 h() 调用, compiler 零改动) + 24 个新测试全绿 + jsx_demo 验证通过; 过程中修复 3 个实现缺陷(词法死循环 OOM / 空格花括号配对 / 回调错误信号残留), 详见 IDLE_TASK_REPORT_P1.md | 无断点, P2 可启动
2026-09-10 | P2 | 已完成: gfx 包(节点树/h 响应式接线/布局/光栅化/命中测试) + gfx/win32 纯 syscall 窗口(DIB 帧缓冲+消息泵+DPI) + vm.RunTimersWithPump + main.go GUI 模式接入; 全链路假 Surface 测试 + 真窗口点击验收(红条 20/40/60 递增); 修复 RegisterClassExW 错误 87(改用 RegisterClassW) | 无断点, P3 可启动
2026-09-10 | P3 | 已完成: 字体子系统(x/image opentype, glyph LRU, 中西文直排) + margin/align/justify/flexGrow/文本固有尺寸 + 节点级脏矩形局部重绘上屏(bench 0.082ms/帧) + 键盘(焦点链)+rAF + jsbuild --gui(go mod tidy 步骤); 修复字体加载自死锁; counter_demo 源码(3次点击→count:3)与打包 exe(2次→count:2)双验收 | 无断点, P4 可启动
2026-09-17 | P4 | 已完成: gfx/backend 选择器 + x11 后端(xgb: PutImage/事件/限时等待, vet 编译级验证) + cocoa 占位(止损: 无cgo无法加载dylib/无IMP, 路径分析 purego vs cgo) + jsbuild --target(windows/linux/darwin 三平台编译全绿, linux 产物 ELF 确认) + docs/desktop-distribution.md; WSL 未安装, x11/cocoa 运行时验证为待办 | 全部阶段完成
2026-09-17 | - | 全部完成: P1–P4 四个阶段均已完成并验收, 报告齐全; 流水线结束, 后续延伸项(x11 真机验证/cocoa 落地/图标嵌入等)见 IDLE_TASK_REPORT_P4.md 遗留清单 | 无
2026-09-18 | - | 全部完成 (重复触发): 无待办阶段, 本次空转 | 无
2026-09-18 | GUI 补全(非闲时任务) | 批次 H 完成: P3-4 原生对话框 —— gx/dialog 模块(alert/confirm/openFile, 均 async 返回 Promise) + 可选接口 nativeDialogHost + win32 侧 MessageBoxW/GetOpenFileNameW 实现(OPENFILENAMEW Win64 布局固化为回归断言) + 11 个新测试(gfx 顶层 211→222, win32 4 例) + testdata/dialog_native_demo.js + 文档(index §21 / README); 发现并记录"运行时只支持 async function 不支持 async 箭头函数" | 下一批 I: P3-5 菜单栏/右键菜单

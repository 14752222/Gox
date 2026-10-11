# m-notes —— 记事（移动端）

手机竖屏记事本：顶部搜索、笔记列表（长按删除）、底部新增栏。
布局按移动端范式：单列、拇指热区、安全区感知。

## 运行

```bash
go build -o /tmp/gox-current ./cmd/gox
/tmp/gox-current apps/m-notes/src/main.js
```

## 压到的能力

- **`gx/viewport` 的 `useInsets()`**：底栏 `paddingBottom` 跟安全区走，
  桌面端自动退化为普通 padding（同一套代码两端跑）；
- **`onLongPress` 手势**：长按列表行删除笔记（移动端日常操作）；
- **`search` 字段**：逐键过滤 + Enter 提交；
- **`gx/storage` 持久化**：增删即写，重启恢复；
- **移动端尺寸窗口**（390×720）验证小屏布局。

## 结构

```
src/
  main.js        入口（window 390×720）
  app.js         根组件：搜索栏 + 列表 + 底部输入栏（insets 感知）
  store.js       signal：笔记列表 / 过滤词 / 草稿
  theme.js       设计令牌
  lib/filter.js  纯逻辑：过滤 / 新笔记构造
```

## 已知限制

- 软键盘弹起时底栏隐藏留给 `useViewport().keyboard`（见 docs/gui-tabbar.md
  §3 表），本应用先演示 insets 一条链路。

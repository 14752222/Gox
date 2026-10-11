# settings —— 设置面板（桌面）

典型应用设置页：外观（主题/强调色/字号）、行为（开关组）、关于。
所有改动即改即存（`gx/storage`），重启后原样恢复；主题切换走 `gx/theme`。

## 运行

```bash
go build -o /tmp/gox-current ./cmd/gox
/tmp/gox-current apps/settings/src/main.js
```

## 压到的能力

- **`gx/theme`**：`setTheme("light"|"dark")` / `toggleDark()` —— 主题实时切换；
- **`gx/storage`**：每个设置项变更立即 `setStorage` 落盘，启动时 `getStorage` 恢复；
- **表单控件全家桶**：`switch` / `select` / `slider` / `colorpicker` /
  `radio` / `datepicker`，全部受控；
- **分组卡片布局**：`form` + `label` + `separator` 的经典设置页结构。

## 结构

```
src/
  main.js        入口
  app.js         根组件：三组设置卡 + 关于
  store.js       signal + 持久化（每项 set 时写 storage）
  theme.js       设计令牌
  lib/prefs.js   纯逻辑：设置项默认值合并 / 校验
```

## 已知限制

- 设置项只在本应用内生效（字号/强调色应用到本面板自身）；
  跨应用共享主题由宿主的 `gox.json "theme"` 字段负责，不在此演示。

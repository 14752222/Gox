# sysmon —— 系统监控（桌面）

设备信息 + 实时曲线面板：电池、电量、网络状态卡片，CPU/内存采样折线图
（采样值由 `gx/device` 上报口与定时器驱动；桌面无真传感器时按演示数据源走）。

## 运行

```bash
go build -o /tmp/gox-current ./cmd/gox
/tmp/gox-current apps/sysmon/src/main.js
```

## 压到的能力

- **`gx/device`**：`deviceInfo()` / `useBattery()` / `useNetwork()` /
  `onBatteryChange` —— 宿主上报 → 脚本订阅的完整链路；
- **`setInterval` 采样循环**：每秒推一个采样点进环形缓冲（`lib/ring.js`）；
- **`canvas` 自绘折线图**：`onDraw` 里读 signal 自动重绘，与 notes 的图表
  组件同源思路；
- **响应式卡片**：充电中 / 离线状态变色。

## 结构

```
src/
  main.js        入口
  app.js         根组件：设备信息行 + 两张状态卡 + 两块画布
  store.js       signal：采样历史 / 电池 / 网络
  theme.js       设计令牌
  lib/ring.js    纯逻辑：定长环形缓冲 + 折线点计算
```

## 已知限制

- CPU/内存采样在无真设备的桌面宿主上是**演示数据源**（随机游走），
  接口形状与真数据一致；接入真机宿主后换 `gx/device` 的真上报即可。

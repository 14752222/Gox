# m-weather —— 天气（移动端）

手机天气页：当前天气大卡、24 小时温度曲线、7 天预报列表、城市切换。
网络状态实时显示（离线时 banner 提示）。

## 运行

```bash
go build -o /tmp/gox-current ./cmd/gox
/tmp/gox-current apps/m-weather/src/main.js
```

## 压到的能力

- **`gx/device` 的 `useNetwork()` / `onNetworkChange`**：离线 banner 即来即走；
- **`canvas` 温度曲线**：24 小时逐时温度折线 + 渐变描点；
- **移动端单列长滚动布局**：大卡 → 曲线 → 列表，390pt 宽；
- **城市切换**：顶部 `select`，数据换整组（`lib/weather.js` 内置样例数据，
  形状对齐真实天气 API 的返回）；
- **`gx/viewport` 的 `useInsets()`**：顶部状态栏垫片。

## 结构

```
src/
  main.js        入口（window 390×720）
  app.js         根组件：离线 banner + 当前卡 + 曲线 + 预报列表
  store.js       signal：当前城市 / 网络状态
  theme.js       设计令牌
  lib/weather.js 纯逻辑：样例数据 / 温度曲线点 / 描述映射
```

## 已知限制

- 数据是内置样例（三座城市）；接真天气 API 只需把 `lib/weather.js` 的
  数据获取换成 `fetch`，组件不用动。

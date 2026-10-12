# music —— 音乐播放器（桌面）

播放列表 + 播放控制（播放/暂停/上一首/下一首/进度条/音量）+ 频谱可视化。
音源是**模拟播放**（定时器推进进度），重点在播放器 UI 与状态机本身。

## 运行

```bash
go build -o /tmp/gox-current ./cmd/gox
/tmp/gox-current apps/music/src/main.js
```

## 压到的能力

- **播放状态机**（`lib/player.js` 纯逻辑，可单测）：列表循环 / 单曲循环 /
  顺序播放，切歌边界条件；
- **`setInterval` 进度推进** + 受控 `progress` 与 `slider`（音量）；
- **`canvas` 频谱可视化**：`onDraw` 读 signal 自动重绘；
- **`each` + `key` 播放列表**：当前行高亮，点击切歌；
- **键盘操作**：`onKeyDown` 空格播放/暂停、←/→ 切歌。

## 结构

```
src/
  main.js        入口
  app.js         根组件：列表 + 控制条 + 可视化
  store.js       signal：播放状态 / 进度 / 音量 / 模式
  theme.js       设计令牌
  lib/player.js  纯逻辑：下一首索引 / 时间格式化 / 频谱样值
```

## 已知限制

- 不出声：`video` 组件走平台宿主视频层（见 docs/video-decision.md），
  音频播放同理留给宿主；本应用把"播放器该怎么长、状态怎么流"做完整。

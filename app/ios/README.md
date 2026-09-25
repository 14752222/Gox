# Gox iOS 壳工程

最小宿主：`UIView` + `CADisplayLink` + 触摸 → libgox.a（Go 侧内核）。
与 `app/android` 壳工程同构 —— Swift 负责"窗口与输入"，Go 负责"渲染与逻辑"。

## 架构（谁负责什么）

```
┌── Swift (本目录) ──────────┐  创建 UIView + 帧缓冲, CADisplayLink 每帧调
│  GoxViewController         │  gox_tick, 触摸/旋转回调转成 C 调用
└────────────┬───────────────┘
   gfx/ios (C 函数指针通道)     ← 薄胶水: 只做缓冲写入与回调转发
┌────────────▼───────────────┐
│ gfx/mobile (纯 Go)          │  Surface / 触摸映射 / 事件泵
└────────────┬───────────────┘
             │ gfx.Surface
        gfx 内核 (node/layout/raster/font, 零改动)
```

## 构建与运行（模拟器）

```bash
# 0) 依赖: Xcode + xcodegen (brew install xcodegen)

# 1) 编 Go 静态库 (模拟器版; 脚本会把产物拷进本工程的 libs/)
bash scripts/build-ios.sh --sim

# 2) 生成并打开 Xcode 工程
cd app/ios && xcodegen generate && open Gox.xcodeproj

# 3) 选一个 iOS 模拟器, Cmd+R 运行
```

真机: `bash scripts/build-ios.sh`（默认 iphoneos/arm64），Xcode 里选你的
设备运行（需要签名：Signing & Capabilities 里选自己的 Team）。

## v1 已知边界（与 Android 壳同步）

- 软键盘（IME）未接：输入框只能看不能输（M2）。
- 多指手势不支持：第二根手指按下即作废整个手势（见 `gfx/mobile.Touch` 注释）。
- 单缓冲：Go 写入与宿主拷贝可能重叠一帧（撕裂），彻底解决要双缓冲。
- 帧上屏是"整帧拷贝"（Data→CGImage→layer.contents），脏区参数留在契约里
  等优化；与 Android 壳"一次额外拷贝"同一取舍。

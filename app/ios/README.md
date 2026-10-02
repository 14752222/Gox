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

### TestFlight / App Store 分发

```bash
# Release 配置构建 (模拟器验证 Release 包也吃得到)
bash scripts/build-ios.sh --release

# 真机 Release + archive + 导出 .ipa (TestFlight 分发的输入单位)
DEVELOPMENT_TEAM=<TeamID> bash scripts/build-ios.sh --archive

# 上传 TestFlight (需要 App Store Connect API Key)
xcrun altool --upload-app -f dist/<名字>.ipa --apiKey <API_KEY_ID> --apiIssuer <ISSUER_ID>
```

- 分发方式默认 `app-store-connect`，可用 `GOX_EXPORT_METHOD` 换
  （ad-hoc / development / enterprise）。
- archive 产物：`dist/ios/Gox.xcarchive` 与 `dist/*.ipa`。
- 免费个人账号能跑真机调试，但 **TestFlight 上传必须付费开发者账号**
  （Apple Distribution 证书 + App Store profile）。

## v1 已知边界（与 Android 壳同步）

- 多指手势不支持：第二根手指按下即作废整个手势（见 `gfx/mobile.Touch` 注释）。
- 单缓冲：Go 写入与宿主拷贝可能重叠一帧（撕裂），彻底解决要双缓冲。
- 帧上屏是"整帧拷贝"（Data→CGImage→layer.contents），脏区参数留在契约里
  等优化；与 Android 壳"一次额外拷贝"同一取舍。
- 折叠屏：姿态/折痕/保留区已接通（见下），但**多场景
  （`UIApplicationSupportsMultipleScenes`）仍是 false** —— 与
  `gfx/mobile.Factory` 的单窗口边界一致。系统级的"两个 App 并排"是否受影响
  **未实测**（Duo 模拟器上验一次即可）。

> 软键盘（IME）**已接**（`GoxViewController.setupIME` + `gox_bind_ime` +
> `gox_ime_commit` + `GoxField.deleteBackward` 退格转发）。此处原写着"未接"
> 是旧文案，已按代码更正。

## 折叠屏（iPhone Duo / iOS 27.1）

**链路**：Swift `view.reservedRegions(kind:options:)` → JSON → `gox_set_display_fold`
→ `gfx/mobile.ReportDisplayFold`（解析/校验/归一化，**可单测**）→
`gfx.ReportPostureFromFold` + `ReportViewport` → 脚本侧 `gx/viewport` 的
`reservedRegions()` / `hasFold()` / `layoutMode()`。

**为什么逻辑在 Go 侧**：`gfx/ios`（本目录）的代码在 Windows 开发机上**完全
编译不到**，测试面为零。所以宿主的职责被压到最小 —— 只做"把看到的如实报上去"
（坐标乘 scale、kind 如实填），姿态判定、裁剪、结构性判据全在
`gfx/mobile/fold.go`，有 `go test ./gfx/mobile` 兜着。

**上报时机**（缺一会漏，四处都要）：

| 时机 | 覆盖的变化 |
|---|---|
| `viewWillTransition(to:)` | 旋转 / 进分屏（几何变了） |
| `traitCollectionDidChange` | 尺寸类变了但**几何可能没变**（Split View 改分栏比例） |
| `viewSafeAreaInsetsDidChange` | 折叠态下两侧 insets 不对称，常是姿态变化的伴随现象 |
| `startEngine` 成功后**补报一次** | 首次布局早于 `gox_init` 时被吞掉的那一份 |

**版本门槛**：`reservedRegions` 是 **iOS 27.1** 引入的。注意：写这版代码时的
SDK（27.0）里**没有这个符号**，所以走的是**运行时动态派发**（见
`GoxDisplayFold.swift` 文件头的踩坑说明）—— selector 存在（真机 27.1+）才走
完整路径，不存在则整条 27.1 分支跳过，只报 sizeClass / 几何这些今天就能拿到
的真实数据，不崩、不猜姿态。SDK 带上符号后应换回编译期调用并核对枚举原始值。
`project.yml` 的 `deploymentTarget` 仍是 **15.0**：老系统走降级分支（当成非折叠
设备），功能不受影响，提门槛只会白丢用户。

### 验收清单（折叠屏四姿态）

工具：`python app/ios/tools/screencap.py`（与安卓 `app/android/tools/screencap.py`
同级同款）。**断言一律用区域哈希 / ASCII 色块图，不靠肉眼** —— 尤其"长 feed 不
跳动"这条，肉眼最容易放过。

| # | 姿态 | 断言 |
|---|---|---|
| 1 | 外屏（合上） | 单栏；外屏**连 inactive 保留区都没有** ⇒ `hasFold()` 为 false |
| 2 | 内屏 平展 | 双栏；`hasFold()` **仍为 true**（折痕 inactive 但结构上还在） |
| 3 | 内屏 半折（book） | 折痕区域上**没有任何内容落下**（对折痕带做区域哈希，与两侧比对） |
| 4 | 内屏 旋转 / 分屏 | 折痕从竖带变横带；布局重排正确 |
| 5 | 折叠↔平展来回切 | **列数不跳**（`hasFold()` 恒 true 的全部意义）；同一次切换中**长 feed 不跳动** |

```bash
# 例: 折痕带区域哈希 (竖折, 内屏宽 1024pt @2x ⇒ 折痕约在 x=1000..1080 设备像素)
python app/ios/tools/screencap.py hash 1000 0 1080 2000
# 例: 看折痕两侧颜色是否被内容覆盖 (客观色块图)
python app/ios/tools/screencap.py map  900 400 1180 600 40
```

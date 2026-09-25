# 平台配置指南（gox.json / 权限 / 图标 / 打包）

> 一份源图 + 一份声明式配置 → 全平台产物。本文覆盖 `gox.json` 的字段说明、
> 权限清单、图标规范与 `gox build` 各目标的验收步骤。

## 总览

```text
my-app/
├── gox.json              项目级声明式配置（单一事实来源）
├── assets/
│   └── icon.png          1024×1024 源图标（单源生成各平台）
├── src/ ...              JS 代码
├── android/              Android 骨架
│   ├── AndroidManifest.xml      含 <!--GOX:PERMISSIONS--> 权限注入区块
│   ├── build.gradle.kts         applicationId / versionName 已按 gox.json 填好
│   └── res/
│       ├── mipmap-{mdpi..xxxhdpi}/     ic_launcher.png + ic_launcher_foreground.png
│       ├── mipmap-anydpi-v26/          自适应图标定义（前景 PNG + 背景纯色）
│       └── values/                     strings.xml / colors.xml
├── ios/                  iOS 骨架
│   ├── Info.plist               含 <!--GOX:USAGE--> 权限注入区块
│   └── Assets.xcassets/AppIcon.appiconset/  全尺寸图标 + Contents.json
├── desktop/              桌面资源
│   ├── icon.ico / icon.icns
│   └── Info.plist        macOS .app bundle 的 Info.plist 模板
└── favicon.png           32×32, 文档站/浏览器用
```

常用命令：

| 命令 | 作用 |
|---|---|
| `gox create <目录>` | 生成工程（含全部平台骨架 + 默认图标, 开箱即用） |
| `gox sync [目录]` | 把 gox.json 的 permissions 注入两份清单文件（幂等） |
| `gox icon [目录]` | 以 assets/icon.png 为源图生成全平台图标（幂等） |
| `gox build <android\|ios\|windows\|macos> [目录]` | 统一构建入口: sync → icon → 平台打包 |

## gox.json 字段说明

```json
{
  "name": "my-app",
  "title": "我的应用",
  "appId": "com.example.myapp",
  "version": "1.0.0",
  "icon": "assets/icon.png",
  "permissions": [
    "camera",
    { "name": "location", "desc": "用于查找附近门店" }
  ],
  "android": {
    "minSdk": 24,
    "targetSdk": 35,
    "adaptiveBackground": "#18243B"
  },
  "ios": { "deploymentTarget": "15.0" },
  "desktop": { "windowed": true }
}
```

| 字段 | 类型 | 缺省 | 说明 |
|---|---|---|---|
| `name` | string | 必填 | 项目名（npm 合法名风格；`create` 时自动填好） |
| `title` | string | 取 `name` | 展示用标题（窗口标题、应用显示名、VERSIONINFO ProductName） |
| `appId` | string | `com.gox.<name>` | 应用唯一标识（Android applicationId / iOS BundleID）。必须为反向域名形式、每段以字母开头 |
| `version` | string | `1.0.0` | 语义化版本 `x.y.z`（可带 `-后缀`），写入 gradle / Info.plist / Windows 版本资源 |
| `icon` | string | `assets/icon.png` | 1024×1024 源图标路径（相对项目根），见下文图标规范 |
| `permissions` | array / object | `[]` | 权限声明，见下文权限清单。**未声明的权限一律不写入清单文件** |
| `android.minSdk` | int | 24 | 写入构建配置的最低 API 级别 |
| `android.targetSdk` | int | 35 | 目标 API 级别 |
| `android.adaptiveBackground` | string | `#18243B` | 自适应图标背景色（`#RRGGBB`）；单图源自动兜底时背景铺这个纯色 |
| `ios.deploymentTarget` | string | `15.0` | 最低 iOS 版本 |
| `desktop.windowed` | bool | `true` | Windows 打包是否隐藏控制台窗口（显式写 `false` 才关掉） |

> **安全约定**: gox.json 只做数据，不允许出现脚本/命令字段 —— 配置文件
> 永远不会成为任意命令执行的入口。

## 权限清单

`permissions` 支持三种写法（可混用前两种的思想：数组字符串、数组对象、对象映射）：

```json
"permissions": [
  "camera",
  { "name": "location", "desc": "用于查找附近门店" }
]
```

```json
"permissions": { "camera": "用于拍摄头像", "microphone": {} }
```

`desc` 是 iOS 用途描述（`NSxxxUsageDescription`）的自定义文案，**上架 App Store
前请务必逐项写清楚用途**（审核必查）；不写时注入器使用下表的中文兜底文案。

注入逻辑（`gox sync`）：

- **Android**: 每个逻辑权限展开为一个或多个 `<uses-permission>`，写进
  `android/AndroidManifest.xml` 的 `<!--GOX:PERMISSIONS:START/END-->` 区块；
- **iOS**: 每个逻辑权限映射为 Info.plist 的用途描述键，写进
  `ios/Info.plist` 的 `<!--GOX:USAGE:START/END-->` 区块；
- 无论声明了什么，**Android 恒定包含 `INTERNET`**（Gox 运行时自身的最小需要）；
- 区块内勿手工编辑 —— `gox sync` 整块替换，重复执行不会重复追加；
  手工加的权限请放在区块外。

| 逻辑权限 | Android uses-permission | iOS 用途描述键 | 默认兜底文案 |
|---|---|---|---|
| `camera` | `CAMERA` | `NSCameraUsageDescription` | 需要使用相机进行拍照和录制视频。 |
| `microphone` | `RECORD_AUDIO` | `NSMicrophoneUsageDescription` | 需要使用麦克风进行录音。 |
| `location` | `ACCESS_FINE_LOCATION` + `ACCESS_COARSE_LOCATION` | `NSLocationWhenInUseUsageDescription` | 需要获取您的位置信息以提供位置相关功能。 |
| `storage` | `READ_MEDIA_IMAGES` + `READ_EXTERNAL_STORAGE` | `NSPhotoLibraryAddUsageDescription` | 需要访问相册以保存或读取图片。 |
| `photos` | `READ_MEDIA_IMAGES` | `NSPhotoLibraryUsageDescription` | 需要访问相册以选择照片。 |
| `notifications` | `POST_NOTIFICATIONS` | （无需描述键） | — |
| `bluetooth` | `BLUETOOTH_SCAN` + `BLUETOOTH_CONNECT` | `NSBluetoothAlwaysUsageDescription` | 需要使用蓝牙连接外部设备。 |
| `contacts` | `READ_CONTACTS` | `NSContactsUsageDescription` | 需要访问通讯录以选择联系人。 |
| `biometrics` | `USE_BIOMETRIC` | `NSFaceIDUsageDescription` | 需要使用面容 ID / 触控 ID 进行身份验证。 |

未在表中的权限名会在 `gox sync` 时直接报错（而不是静默忽略）——
新权限请在 `config/permissions.go` 的注册表里添加后重新编译。

## 图标规范

**源图要求**

- 推荐 **1024×1024** PNG（可带 alpha，圆角/异形均可）；
- 必须是**正方形**，非方形源图 `gox icon` 直接报错；
- 各平台产物全部由这一张图派生，改图标 = 换源图 + 重跑 `gox icon`。

**生成产物一览**

| 平台 | 产物 | 尺寸 |
|---|---|---|
| Android 传统图标 | `android/res/mipmap-*/ic_launcher.png` | 48 / 72 / 96 / 144 / 192 |
| Android 自适应前景 | `android/res/mipmap-*/ic_launcher_foreground.png` | 108dp 画布：108 / 162 / 216 / 324 / 432（内容缩至中心 66/108 安全区） |
| Android 自适应定义 | `android/res/mipmap-anydpi-v26/ic_launcher(.round).xml` | 背景纯色 + 前景 PNG |
| iOS AppIconSet | `ios/Assets.xcassets/AppIcon.appiconset/AppIcon-*.png` | 40 / 58 / 60 / 80 / 87 / 120 / 152 / 167 / 180 / **1024** |
| Windows | `desktop/icon.ico` | 16 / 24 / 32 / 48 / 64 / 128 / 256（PNG 压缩条目） |
| macOS | `desktop/icon.icns` | 16 / 32 / 128 / 256 / 512 / 1024 |
| favicon | `favicon.png` | 32 |

**注意事项**

- iOS 的 **1024 营销图必须无 alpha 通道**（App Store 上传校验会拒收带
  alpha 的 PNG）—— 生成器已自动铺白底去 alpha，全系 iOS 图标统一处理；
- Android 自适应图标的内容不要贴边：生成器已把源图缩进 66/108 安全区，
  圆形/方形遮罩都不会裁掉主体；背景色由 `android.adaptiveBackground` 控制；
- `gox icon` 幂等：同一源图永远产出同一批文件，可以放心加进 CI。

## 打包（gox build）

`gox build` 统一入口，内部串联 `sync → icon → 平台打包`：

| 目标 | 产物 | 依赖 |
|---|---|---|
| `gox build windows` | `dist/<name>`（exe，图标 + 版本资源内嵌） | Gox 源码仓库、Go |
| `gox build macos` | `dist/<name>.app`（可执行 + Info.plist + .icns） | Gox 源码仓库、Go |
| `gox build android` | `libgox.so` → gradle `assembleDebug`（有工具链时） | NDK / gradle |
| `gox build ios` | `libgox.a` → Xcode 构建提示 | Xcode |

桌面打包（windows/macos）走 `packager/`（jsbuild），机制是把 JS 嵌入生成的
Go 工程后 `go build`；因此需要 Gox 源码仓库（自动向上查找，或设 `GOX_REPO`）。
图标注入说明：

- **Windows**: 生成 `rsrc_windows_<arch>.syso`（纯 Go 实现的 COFF 资源对象，
  内嵌 .ico 图标与 VS_VERSIONINFO 版本信息），`go build` 自动拾取链接，
  无需任何第三方工具；
- **macOS**: 产出 `.app` bundle：`Contents/MacOS/<name>` + `Contents/Info.plist`
  + `Contents/Resources/AppIcon.icns`。

`packager`（jsbuild）也可单独使用：

```bash
go run ./packager app.js --gui --target windows/amd64 \
    --icon desktop/icon.ico --version 1.0.0 -o app.exe
go run ./packager app.js --gui --target darwin/arm64 \
    --icon desktop/icon.icns --version 1.0.0 -o app
```

## Phase 5 真机验收（留给用户执行）

构建级验证已由 CI/开发流程覆盖；以下真机/模拟器验收步骤需要真实设备，
**留给使用者按需执行**：

1. **Android**
   - `gox build android` 后确认 `android/app/build/outputs/apk/debug/` 产出 APK；
   - 安装到真机：桌面图标显示正常（圆形/方形遮罩下主体不被裁切）；
   - 在 gox.json 里声明 `camera` → `gox sync` → 重新构建安装 → 触发拍照功能，
     确认系统弹出相机权限弹窗且文案正确；
   - 删除 `camera` → `gox sync` → 重新构建 → 确认不再弹窗且设置里无该权限。
2. **iOS**
   - ~~`gox build ios`（需 Xcode），用 Xcode 打开 `ios/` 工程安装到真机/模拟器~~
     **模拟器链路已验收**（2026-09-25，Xcode 27 / iPhone 15 Pro 模拟器）：
     `gox build ios --simulator` 一条命令出 `dist/<name>.app`
     （sync → icon → libgox.a → xcodebuild → 组装），simctl 安装启动后
     图标上屏、bundle id/版本/图标资源合并正确、渲染管线出画面；
     入口必须是**单文件**（不能 import 相对路径，脚本会快速失败并指引）；
   - 真机剩余项：`gox build ios --device` 签名安装、桌面图标遮罩目视；
   - 声明 `camera` 并自定义文案 → 重新构建 → 确认弹窗显示**自定义文案**
     而不是默认兜底文案；
   - 检查 `Info.plist` 的 `NSxxxUsageDescription` 与 gox.json 一致
     （打包时已自动合并用户工程权限文案、删除未声明的壳占位键）。
3. **Windows**
   - `gox build windows`，把 `dist/<name>` 拷到 Windows 资源管理器：
     右键属性应看到版本号（详细信息页签），exe 与任务栏显示自定义图标；
4. **macOS**
   - `gox build macos`，双击 `dist/<name>.app` 可启动，Finder 显示图标；
     首次运行 Gatekeeper 提示属正常（未签名），签名公证见
     [docs/desktop-distribution.md](desktop-distribution.md)。

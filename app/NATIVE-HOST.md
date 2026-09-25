# NativeHost 平台实现状态 (Android / iOS / 鸿蒙)

内核契约在 `gfx/native.go` (`gfx.NativeHost{ Capabilities(), Call(method, args) }`),
六个能力模块在 `gfx/native_device.go / native_app.go / native_geo.go /
native_media.go / native_permission.go / gfx/viewport.go`。**Go 源码是方法名、
参数字段、返回形状、8 个错误码的唯一事实来源** —— 本文是镜像, 两处不一致时以
Go 为准并修文档。

- 契约 (Go): `gfx/native.go:176` — `NativeHost` 接口与 `NativeCallResult` 三态
  (Pending > Err > Result)
- 错误码 (Go): `gfx/native.go:84` — unsupported / permission-denied / cancelled /
  timeout / busy / unavailable / platform-error / invalid-arg
- 桌面样板: `gfx/win32/host.go` (win32Host)
- 平台桥: `gfx/android/libgox/main.go` (JNI)、`gfx/ios/libgox/main.go` (C 函数指针)

## 通道协议 (三平台一致)

内核在 GUI 线程调 `host.Call(method, args)`; `args` 是调用方选项 + 内部字段
`__id` (回填钥匙)。平台桥把它序列化成 JSON 传给宿主语言, 回包三选一:

| 回包 | 内核语义 |
|---|---|
| `"__pending__"` | `NativePending` —— 稍后宿主经 `ResolveNative`/`nativeResolveNative`/`gox_resolve_native` 回填 (id 照抄 `__id`) |
| `{"__errCode":"…","__errMsg":"…"}` | `NativeFailure` —— errCode 必须是 8 个统一错误码之一 (桥会把认识的词放行、不认识的归 platform-error) |
| 其它 JSON | `NativeResult` —— 形状随方法而定, 见下表 |

流式调用 (`location.watch`) 对**同一个 id 反复回填**; 只有致命错误
(unsupported / cancelled / permission-denied / unavailable) 才停流
(`native.go nativeErrFatal`)。停流走 `location.unwatch` (内核 `stopNativeStream`
自动调, 宿主要幂等)。

同步方法**必须当场返回** (不能 pending): 内核的
`deviceHostOverlay` / `jsGetBrightness` / `jsOpenSystemSettings` /
`jsSetOrientation` / `refreshPermissionsFromHost` 直接读 `res.Result`
(`gfx/native_device.go:212` 等)。

纯上报型能力没有 Call: 宿主在状态变化时调 Report 通道 (GUI 线程纪律由桥的
`gfx.Post` 保证), 脚本侧用 `battery()` / `useAppState()` / `useInsets()` 等响应式读。

## 实现状态表 (模块 × 平台)

图例: ✅ 完整实现 · 🟡 部分/有平台限制 · ❌ 显式 unsupported · ⬜ 仅契约

| 模块 | 方法/通道 | Android (Kotlin) | iOS (Swift) | 鸿蒙 (ArkTS) |
|---|---|---|---|---|
| gx/device | device.info | ✅ | ✅ | ⬜ unsupported |
| | device.vibrate | ✅ | ✅ (仅系统级一档) | ⬜ |
| | device.brightness | ✅ | ✅ | ⬜ |
| | device.keepScreenOn | ✅ | ✅ | ⬜ |
| | device.openSettings | ✅ (11 个 kind 全覆盖) | 🟡 (只有 app 总览页; iOS 只开放本应用设置页) | ⬜ |
| | battery / network 上报 | ✅ (广播 / NetworkCallback) | ✅ (UIDevice / NWPathMonitor) | ⬜ |
| gx/app | app.share | ✅ | ✅ | ⬜ |
| | app.exit | ✅ (finishAffinity) | ❌ (iOS 不允许自杀, 显式 unsupported) | ⬜ |
| | app.orientation | ✅ (requestedOrientation) | ✅ (动态 mask, 需 Info.plist 方向声明) | ⬜ |
| | appstate / memory 上报 | ✅ (onResume/onPause/onTrimMemory) | ✅ (UIApplication 通知) | ⬜ |
| | 返回键 (reportBackPress) | ✅ (onBackPressed → 同步问脚本 ≤500ms) | ⬜ (iOS 无返回键) | ⬜ |
| gx/geo | location.get | ✅ (LocationManager; 宿主侧 30s 兜底超时) | ✅ (CLLocationManager) | ⬜ |
| | location.watch / unwatch | ✅ (流式, 同 id 反复回填) | ✅ (流式) | ⬜ |
| | gcj02 坐标系请求 | ❌→platform-error (不做火星换算) | ❌→platform-error | ⬜ |
| gx/media | camera.takePhoto | ✅ (MediaStore EXTRA_OUTPUT; API<29 缩略图兜底) | ✅ (UIImagePickerController, quality 落 jpg) | ⬜ |
| | gallery.pick | ✅ (ACTION_PICK / ACTION_GET_CONTENT 多选) | ✅ (PHPicker, 无需相册权限) | ⬜ |
| | gallery.pickVideo | ✅ | ✅ | ⬜ |
| | media.save | ✅ (MediaStore 29+; 低版本公共目录+扫描) | ✅ (PHPhotoLibrary add-only) | ⬜ |
| | media.preview | 🟡 (ACTION_VIEW 单文件; 无系统多图预览) | ✅ (QLPreviewController) | ⬜ |
| gx/permission | permission.get | 🟡 (Android 无法区分 not-determined/denied → 统一 denied; gallery limited ✅) | ✅ (notification 的同步状态拿不到 → 缺键按 unknown) | ⬜ |
| | permission.request | ✅ (批量一次申请一次回填) | ✅ (camera/mic/gallery/notification/contacts/calendar/location) | ⬜ |
| gx/viewport | insets 上报 | ✅ (nativeSetInsets, 既有通道) | ✅ (gox_set_insets, 既有通道) | ⬜ (契约已声明) |

## method ↔ 平台 ↔ 内核调用点对照表

参数列为内核放进 `args` 的字段 (除 `__id`); 返回列为脚本最终看到的形状。

| method | 同步/异步 | 参数 (args 字段) | 成功返回 (宿主回包 JSON) | 内核调用点 |
|---|---|---|---|---|
| device.info | 同步 | — | `{platform, os, osVersion, arch, model, brand, manufacturer, deviceId, locale, language, region, timezone, tzOffset, sdkVersion, screenWidth, screenHeight, pixelRatio, isEmulator, isTablet, appName, appVersion, appBuild}` (缺字段内核用本机缺省覆盖, 见 applyDeviceOverlay) | native_device.go:212 deviceHostOverlay |
| device.vibrate | 同步 | `{duration}` | 软调用, 结果忽略 (true) | native_device.go:448 callNativeSoft |
| device.brightness | 同步 | 读: `{}` / 写: `{value}` | `{value}` (0..1; 读不出 → -1) | native_device.go:505 / :527 |
| device.keepScreenOn | 同步 | `{on}` | 软调用, 结果忽略 | native_device.go:495 |
| device.openSettings | 同步 | `{kind}` (11 个白名单值, gfx.SettingKinds) | `true` / 错误信封 | native_device.go:551 |
| app.share | 异步 | `{text?, url?, imagePath?, files?}` | `{}` (拉起即回填) | native_app.go:157 callNative |
| app.exit | 同步 | `{}` | 软调用 (Android true / iOS unsupported 错误信封) | native_app.go:166 callNativeSoft |
| app.orientation | 同步 | `{mode}` (portrait/landscape/auto, 内核已归一) | `true`/`false` → setOrientation() 返回 bool | native_app.go:193 |
| permission.get | 同步 | — | `{location, camera, gallery, microphone, notification, contacts, calendar, bluetooth, storage}` (值: granted/denied/not-determined/restricted/limited/unknown; iOS 拿不到的键可缺省) | native_permission.go:185 refreshPermissionsFromHost |
| permission.request | 异步 | `{kind}` 或 `{kind, kinds:[…]}` | `{kind: 状态}` 对象 (内核 extractPermissionState 认 `state`/kind/`result` 三种键) | native_permission.go:246/:286 callNativeWithSink |
| camera.takePhoto | 异步 | `{camera:"back"/"front", quality, saveToGallery, format, maxWidth, maxHeight}` | `{path, uri, name, mimeType, width, height, size, duration, createdAt}` (单个 MediaFile; path 字符串也认) | native_media.go:208 callNativeWithSink |
| gallery.pick | 异步 | `{count(1..9), source, compressed, quality, allowVideo}` | MediaFile **数组** | native_media.go:238 callNative |
| gallery.pickVideo | 异步 | `{source, maxDuration, compressed, quality}` | 单个 MediaFile | native_media.go:275 |
| media.save | 异步 | `{album?, file:{path/uri,…}}` | 单个 MediaFile (保存后的位置) | native_media.go:303 |
| media.preview | 异步 | `{files:[MediaFile…], index}` | `{}` (拉起即回填) | native_media.go:330 |
| location.get | 异步 | `{type:"wgs84"/"gcj02", highAccuracy, timeout, maximumAge}` | `{latitude, longitude, altitude, accuracy, altitudeAccuracy, speed, heading, timestamp, provider, mocked, type}` (经 locationFromJS 归一 + 写 lastLocation 缓存) | native_geo.go:180 callNativeWithSink |
| location.watch | 异步(流) | 同上 | 同上, 对同一 id 反复回填 | native_geo.go:237 startNativeStream |
| location.unwatch | 同步 | 仅 `__id` | 软调用 (内核尽力而为, 忽略结果) | native.go:666 stopNativeStream |
| insets (纯上报) | 上报 | — | — | viewport.go:187 ReportViewport |
| battery / network (纯上报) | 上报 | — | — | native_device.go:348/:369 ReportBattery/ReportNetwork |
| appstate / memory (纯上报) | 上报 | — | — | native_app.go:72/:97 ReportAppState/ReportMemoryWarning |
| back (纯上报, 仅 Android) | 上报(同步答) | — | 脚本是否已处理 (bool) | native_app.go:114 ReportBackPress |

## 错误码 → 平台语义映射

| 错误码 | Android | iOS |
|---|---|---|
| unsupported | 未实现的方法/设置页 kind | 同左; app.exit (系统不允许自杀) |
| permission-denied | 相机/定位调用前权限未授权 (不替脚本偷偷弹窗) | 相机/定位/相册 add 权限未授权 |
| cancelled | 相机/相册/视频系统 UI 里取消 (resultCode != OK) | 拍照取消 / PHPicker 空结果 |
| timeout | location.get 30s 无定位 (宿主侧兜底) | — (内核定时器负责) |
| busy | — (v1 无占用场景) | — |
| unavailable | 无震动器 / 无定位 provider / 定位服务关闭 | 无窗口可展示 / 文件读不出 |
| platform-error | 系统回调异常 / gcj02 不可提供 / MediaStore 失败 | 同左 |
| invalid-arg | JSON 不合法 / share 无内容 / preview 空列表 | 同左 |

## 平台接线位置

| 平台 | 宿主契约 (平台语言) | 实现 | 桥 (Go 侧) |
|---|---|---|---|
| Android | `GoxNativeHost.kt` (interface) | `GoxNativeHostImpl.kt` (+ `GoxFileProvider.kt`, `MainActivity` 接线) | `gfx/android/libgox/main.go` (jniNativeHost, JNI 导出 nativeResolveNative / nativeReport*) |
| iOS | `NativeHost.swift` (GoxNativeHost 类 + C 闭包) | 同文件 (六模块) + AppDelegate 方向 mask | `gfx/ios/libgox/main.go` (cfnNativeHost, gox_set_native_host / gox_resolve_native / gox_report_*) |
| 鸿蒙 | `harmony/native/NativeHost.ets` (interface + 协议) | `harmony/native/GoxNativeHost.ets` (stub, 全部显式 unsupported) | 待鸿蒙壳工程落地后接入 |

## 验证情况 (2026-09-25)

| 项 | 结果 |
|---|---|
| `go build ./...` (darwin) | ✅ 通过 |
| iOS c-archive 交叉编译 (`GOOS=ios GOARCH=arm64 CGO_ENABLED=1`) | ✅ 通过 |
| Swift typecheck + **xcodebuild 完整构建** (iphonesimulator, 含 libgox.a 链接) | ✅ BUILD SUCCEEDED |
| Android libgox (cgo+jni.h) | ⬜ 未编译 (本机无 NDK); gofmt 语法检查通过 |
| Kotlin 编译 | ⬜ 未编译 (本机无 gradle/Android SDK); 已按 framework-only API 逐项静态自查 |

## 需要真机/模拟器验收的遗留项

1. Android 返回键链路: `onBackPressed → nativeReportBackPress` 的 500ms 同步等待
   在空闲泵 (无 rAF/定时器) 下依赖 Tick 唤醒, 需真机确认无 ANR。
2. Android 相机: API 29+ MediaStore EXTRA_OUTPUT 路径与 `<queries>` 可见性;
   API<29 走缩略图兜底 (分辨率低, 属已知限制)。
3. iOS `onMain` (DispatchQueue.main.sync): 依赖"主线程从不同步等待脚本线程"
   (gox_tick 只叫醒泵); 若未来主线程新增同步等待 Go 的路径需重审。
4. iOS PHPicker 多选回填顺序、QLPreview 的 iPad popover 行为。
5. 权限状态: Android 的 not-determined 与 denied 无法区分 (shouldShowRequest-
   PermissionRationale 两种情况都 false); 若内核后续增加"永久拒绝"语义需平台跟进。
6. 三平台 battery 的 `chargingType` 词汇 (usb/ac/wireless/none/unknown) 在 iOS
   上不可区分, 恒 unknown (充电中)。

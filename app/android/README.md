# Gox on Android —— 最小宿主壳

这是 Gox 在 Android 上的**壳工程**。引擎（lexer → parser → compiler → VM → stdlib → gfx）
一行都不在这里 —— 这里只做两件事：

1. 把 Go 侧编成 `libgox.so` 装进 APK；
2. 用一个最小 Activity（`SurfaceView` + `Choreographer` + 触摸）把它驱动起来。

```
┌── app/android (本目录, Kotlin) ──┐   SurfaceView / Choreographer / 触摸 / 上屏
│                                  │
├── gfx/android/libgox             │   //export 的 JNI 入口 + 装配 (package main)
├── gfx/android                    │   JNI 通道: JVM 附加 / direct ByteBuffer / GoxHost 回调
├── gfx/mobile                     │   纯 Go 内核: 事件队列 / 触摸映射 / 泵唤醒 / density 上报
└── gfx (内核, 零改动)             │   node / layout / raster / font / hittest
```

## 前置条件

| 需要 | 版本 | 本机实测位置 |
| --- | --- | --- |
| Android SDK | `platforms;android-35` + `build-tools` | `H:/AndroidSDK` |
| Android NDK | r25 以上（本机 28.2.13676358） | `H:/AndroidSDK/ndk/28.2.13676358` |
| JDK | 17（AGP 8.6 的要求） | `C:\Program Files\Eclipse Adoptium\jdk-17.0.17.10-hotspot` |
| Gradle CLI | 8.7 以上 | **本目录没有 wrapper**，要么自己装，要么直接用 Android Studio 打开 |

`local.properties` 里的 `sdk.dir` 按本机 SDK 路径写（该文件被 gitignore）。

## 构建（四步）

```bash
# 1) 编 Go 侧 —— 唯一支持 cgo 的那条路, 产物带 ELF 目标校验 (发现"编成功了但链错目标"这类错)
bash scripts/build-android.sh                 # arm64-v8a (真机)
bash scripts/build-android.sh --abi x86_64    # 模拟器: x86 主机上**原生**执行,
#                                             # arm64 在模拟器里是转译, 软光栅 + 转译慢到没法用
#    找不到 NDK 时: bash scripts/build-android.sh --ndk H:/AndroidSDK/ndk/28.2.13676358
#    ⚠️ GOARCH 由脚本按 ABI 推导 —— 别手工写 GOARCH=arm64 去编 x86_64, 那会
#       "成功"产出一份装上去才炸 (UnsatisfiedLinkError) 的错架构库

# 2) 拷进壳工程 (jniLibs/ 本身被 gitignore, 每次重建都要重新拷)
mkdir -p app/android/app/src/main/jniLibs/arm64-v8a app/android/app/src/main/jniLibs/x86_64
cp dist/android/arm64-v8a/libgox.so app/android/app/src/main/jniLibs/arm64-v8a/
cp dist/android/x86_64/libgox.so   app/android/app/src/main/jniLibs/x86_64/

# 3) 打 APK
cd app/android && gradle assembleDebug        # 或 Android Studio 打开本目录后 Build

# 4) 装机并看日志
adb install -r app/build/outputs/apk/debug/app-debug.apk
adb logcat -s Gox:I
```

**日志为什么要专门看 `Gox` 这个 tag**：Android 在 fork 应用进程前把 stdout/stderr
接进了 `/dev/null`，原生代码往 fd 2 写的东西在真机上是看不到的 —— 所以 Go 侧所有
"跳过了、失败了"的分支都改走 `gfx/android.Logf`（内部调 `__android_log_write`）。
"黑屏 + 无日志" 是没法查的，这一条就是为它准备的。

## JNI 契约（改一处必须同步四处）

符号名由 **包名 + 类名 + 方法名** 拼出来（`Java_com_gox_GoxRuntime_nativeTick`），所以：

| 位置 | 内容 |
| --- | --- |
| `app/src/main/kotlin/com/gox/GoxRuntime.kt` | `object GoxRuntime` 的全部 `external fun` |
| `gfx/android/libgox/main.go` | 对应的 `//export Java_com_gox_GoxRuntime_*` |
| `gfx/android/android.go` 头部的契约注释 | 同一份签名（给不打开 Kotlin 的人看） |
| `app/src/main/kotlin/com/gox/GoxHost.kt` | 反向回调：`flush([I)V` / `finished(ILjava/lang/String;)V` 是**逐字符**写死在 C 里的 |

三条容易踩的：

- **改包名/类名 = 改符号名**；`namespace`（本文件是 `com.gox`）与 Kotlin `package` 必须一致。
- **不要开混淆**（`isMinifyEnabled = false`）：混淆器改类名会让 `System.loadLibrary` 之后的
  方法绑定失败（`UnsatisfiedLinkError`）。真要开，必须给 `com.gox.GoxRuntime` 写 keep 规则。
- `GoxRuntime` 写成 `object` 而不是一堆 `static`：JNI 里实例方法与静态方法的**符号名相同**，
  只差第二个参数是 `jobject` 还是 `jclass`，而 Go 侧不用那个参数 —— 两种写法 ABI 等价。

## 基线权限（删掉 = 启动即崩）

宿主 `MainActivity.onCreate` 里 `registerNetworkCallback()` + `reportNetworkInitial()`
是**无条件执行**的，依赖 `INTERNET` 与 `ACCESS_NETWORK_STATE` 两个 normal 级
"运行时基线权限"（安装即授、不弹窗）。它们与用户声明的逻辑权限（`gox sync`
按 `gox.json` 注入 `GOX:PERMISSIONS` 区块）是**两张不同的表**：

| 位置 | 说明 |
|---|---|
| `config/permissions.go` 的 `AndroidBootstrap` | 基线唯一真源，`AndroidUses()` 恒定铺底 |
| `scaffold/template/android/AndroidManifest.xml` | 模板（`gox sync` 生成新项目用） |
| `app/android/app/src/main/AndroidManifest.xml` | 本壳工程手写维护，无注入区块 |

三处必须镜像。**实测**（2026-10-02）：漏掉 `ACCESS_NETWORK_STATE` 时
`registerNetworkCallback` 抛 `SecurityException` 直接杀死进程 —— 症状是
"全新安装启动即闪退、logcat 只有 SecurityException"。Kotlin 侧
`reportNetworkInitial` 另有 try/catch 降级兜底（缺权限时按"未连接"上报，
不崩），但清单里该有还得有。

## 折叠屏（androidx.window 的 FoldingFeature）

**链路**：Kotlin `WindowInfoTracker` → `FoldingFeature` → JSON → `nativeSetDisplayFold`
→ `gfx/mobile.ReportDisplayFold`（解析/校验/归一化，**可单测**）→
`gfx.ReportPostureFromFold` + `ReportViewport` → 脚本侧 `gx/viewport` 的
`reservedRegions()` / `hasFold()` / `layoutMode()`。

**为什么逻辑在 Go 侧**：`android` tag 下的代码在桌面开发机上**编译不到**，
测试面为零。所以宿主只做"把看到的如实报上去"（`isSeparating` 如实翻成
division/occlusion、坐标原样——`FoldingFeature.bounds` 已经是像素，不是 dp），
姿态判定、裁剪、结构性判据全在 `gfx/mobile/fold.go`。

**判据映射**（`GoxDisplayFold.kt`，与 Android 官方文档对齐）：

| FoldingFeature | 我们的映射 |
|---|---|
| `state == HALF_OPENED` | `posture = "half-open"` |
| `state == FLAT` | `posture = "flat"` |
| `isSeparating == true` | `kind = "division"`（铰链切出两个逻辑区域） |
| `isSeparating == false` | `kind = "occlusion"`（只遮一条，不分割） |
| `orientation == VERTICAL` | 折痕是**竖带**（切左右）→ `"vertical"` |
| `orientation == HORIZONTAL` | 折痕是**横带**（上下/桌面模式）→ `"horizontal"` |

⚠️ 最后两条最容易写反：`Orientation.VERTICAL` 说的是**折痕线是竖的**，不是
"屏幕竖着"。写反的症状是避让带跑到另一个轴上（布局看着像随机错位）。

**依赖**：`androidx.window:window:1.4.0` + `window-java:1.4.0` + `androidx.core:core-ktx`。
`useAndroidX` 因此从 `false` 改成了 `true`（见 `gradle.properties` 的说明）。

**依赖踩坑（2026-10-01 实测，三轮编译才过）**：

1. `windowLayoutInfo(activity, executor)` —— **该重载不存在**（1.4.0），编译器报
   "Too many arguments"。
2. 改用 `window-java` 的 `WindowInfoTrackerCallbackAdapter`（core 的 Flow 只能在
   协程里 `collect`，为一个回调拉进 kotlinx-coroutines 不划算）→ 报
   "Cannot access class `androidx.core.util.Consumer`"。**这个报错不会告诉你缺哪个
   依赖**，得去看 window-java 的字节码才知道要 `androidx.core`。
3. `removeWindowLayoutInfoListener` 要的是**注册时那个 Consumer 实例**（不是
   Activity、不是 Executor）—— 所以要自己存着引用。

**版本选择**：用 1.4.0（2025-05-20 stable）而不是最新的 1.5.1 —— 本工程是
Gradle 8.14 + AGP 8.6.1 + compileSdk 35，1.5.x 是为更新的 AGP/compileSdk 构建的。
我们要的 API（`state` / `orientation` / `isSeparating` / `bounds` / `occlusionType`）
1.4.0 全部齐备。升 1.5.x 请连 AGP + compileSdk 一起升。

**验证命令**（本机无 wrapper，用缓存的 Gradle）：

```bash
G=/c/Users/<you>/.gradle/wrapper/dists/gradle-8.14-all/*/gradle-8.14/bin/gradle
cd app/android && "$G" :app:compileDebugKotlin --console=plain --no-daemon
```

### 验收清单（折叠屏）

工具：`python app/android/tools/screencap.py`。**断言一律用区域哈希 / ASCII 色块图**，
不靠肉眼——尤其"长 feed 不跳动"这条。

| # | 姿态 | 断言 |
|---|---|---|
| 1 | 外屏 / 直板机 | 单栏；`hasFold()` false |
| 2 | 内屏 展开（FLAT） | 双栏；`hasFold()` **仍 true** |
| 3 | 内屏 半开（HALF_OPENED） | 折痕带上没有任何内容落下 |
| 4 | 内外屏切换（density 变） | 会话不重建（`onConfigurationChanged` 走通），密度刷新正确 |
| 5 | 折叠↔展开来回切 | 列数不跳；长 feed 不跳动 |

```bash
export ADB=H:/AndroidSDK/platform-tools/adb.exe
python tools/screencap.py hash 1000 0 1080 2000   # 折痕带区域哈希
python tools/screencap.py map  900 400 1180 600 40 # 看两侧是否被内容覆盖
```

## 首帧自检清单

真机上第一次跑，按这个顺序看（前一条不过就别看后面的）：

1. **有启动日志** → `libgox 启动: 1080x2340 density=2.75, surface 已注册为默认窗口后端`
   —— 没有这行说明 `nativeInit` 就没走到（看有没有 `android init 失败: ...`）。
2. **不是黑屏** → 界面应有的内容：白底、`Gox on Android`、第二行文本、一个按钮。
3. **颜色对** → 帧缓冲是 `image.RGBA`（字节序 R,G,B,A），`Config.ARGB_8888` 在小端平台上的
   内存布局**恰好也是 R,G,B,A**（Skia：`SK_PMCOLOR_BYTE_ORDER(R,G,B,A)` 为真时
   `kN32` = `kRGBA_8888`），且 `copyPixelsFromBuffer` 是纯 memcpy、不做任何转换
   —— 所以**不需要换通道**。真出现红蓝互换，先查这里。
   （alpha 无坑：整帧重绘会先铺满不透明白底，局部重绘不碰之外的像素 ⇒ 帧缓冲 alpha 恒 255，
   Skia 的预乘语义不会造成色差。）
4. **点得动** → 点按钮，第二行文本从 `计数 0` 变 `计数 1`。这就是 M1 的验收标准。
5. **有 FPS** → 每秒一行 `fps=NN surface=WxH`。移动端软光栅是纯 CPU 活，这个数字决定
   后面值不值得做脏区上传优化。静态界面 `fps=0` 是**对的**（没有脏区就没有帧），别误读成卡。
6. **返回键能退出** → 按 BACK 回到 Launcher；重进后是新会话（计数归 0）。
   这条测的是"返回键没被吞"与"引擎会话能干净销毁/重建"两件事。

## M1 实测记录（2026-09-24，模拟器 x86_64 / API 34 / Android 14）

上面六条全部走通：启动日志、渲染（白底黑字、中文正常）、颜色无红蓝互换、
连点三次 `计数 3`、`fps` 有输出、横竖屏切换（`surface=2560x1440`，会话保住、无崩溃）、
BACK 退出后重进归 0。取证工具是本目录的 `tools/screencap.py`（纯标准库解析
`screencap` 原始帧）：

```bash
export ADB=H:/AndroidSDK/platform-tools/adb.exe
# 定位控件: 把屏幕一角压成 ASCII 色块图, 按格子换算出像素坐标
python tools/screencap.py map 0 0 360 180 90
# 客观断言"界面真的变了": 点击前后对同一区域取哈希比对 (不靠肉眼)
python tools/screencap.py hash 20 58 170 82
# 导出 PNG 肉眼复核 (crop 支持整数放大, 看清小字)
python tools/screencap.py crop out.png 10 20 260 120 4
```

已知观感：**所有东西都偏小** —— `font={20}` 就是 20 个物理像素，在 density 4.0 的屏上
只有 5dp。这正是 v1 边界里"density 只上报不换算"那条，属于 M1 的渲染侧剩余工作，
不是 bug。

## M2 实测记录（2026-10-02，模拟器 x86_64 / API 34 / Gboard）

IME 链路全部走通（取证同 M1：`tools/screencap.py` + `logcat -s Gox:I`）：

1. **点输入框聚焦** → `编辑框回传: focused=true`（光标/文本快照通道建立）。
2. **软键盘弹出** → `mInputShown=true`，`nativeSetKeyboard: 883 px` 入日志，
   Gboard 候选词栏出现（InputConnection 上下文回传正确）。
3. **`adb shell input text "hi"` 提交** → 输入框显示 `hi`、光标紧跟文本，
   `输入内容: hi` 联动文本实时刷新（依赖脏区包围盒上传，见下）。
4. **insets 上报** → `insets b=63`（API 30+ `getInsets(Type.ime|systemBars)`，
   前置两件事：`WindowCompat.setDecorFitsSystemWindows(window,false)` 否则
   IME insets 被系统消费不分发；初始化后 `requestApplyInsets()` 否则首次
   分发早于 `nativeInit` 被卫语句挡掉、系统不再主动发）。

**当天修掉的三个真 bug**：

| 症状 | 根因 | 修法 |
|---|---|---|
| 全新安装启动即崩 | 清单缺 `ACCESS_NETWORK_STATE`（基线权限，见上文） | 三处镜像 + Kotlin 降级兜底 |
| 输入联动文本卡旧值 | `lockCanvas(rect)` 把画布**裁剪到单个矩形**，多块脏区只刷第一块 | `blit` 先算多块脏区的**包围盒**再上传 |
| 聚焦后秒崩 | `CursorAnchorInfo` 缺 matrix 抛异常；`decorFitsSystemWindows=true` 吞掉 IME insets | `setMatrix(Matrix())`；`setDecorFitsSystemWindows(false)` + `requestApplyInsets()` |
| **键盘高度显示 0（真根因）** | `nativeSetInsets` 走 `gfx.ReportViewport`（**patchAll 含 patchKeyboard**）：键盘弹起后系统**再分发一次 insets**，把刚报上来的键盘高度清成 0 | 内核新增 `gfx.ReportInsets`（patchInsets 掩码，与 `ReportKeyboardHeight` 对称），Android/鸿蒙/iOS 三端宿主改走它；回归 `gfx/viewport_kb_test.go` 补「先键盘后 insets」这条真机顺序 |

**排查教训（三段弯路，最终根因在内核）**：键盘高度显示 0 先后怀疑过三个方向，
都被更硬的证据推翻 —— ① 怀疑内核 `ReportKeyboardHeight → useKeyboardHeight`
的版本信号通知断了：写了内核回归 `gfx/viewport_kb_test.go`，首版在**泵外**
（Go 测试 goroutine）调 `ReportViewport`，断言假阴性（`EvalVM` 返回后
`currentVM` 已恢复为 nil，通知链里的脚本闭包被回调桥静默丢弃），挪进
`RunTimersWithPump` 泵轮次后用例即绿 —— 但**旧用例只覆盖「先 insets 后键盘」**，
恰好绕开了真机顺序，所以一直是绿的。② 拿警告环形缓冲的 dump 当实时日志，把
"一次 dump 里的重放"当成事件顺序，得出过"泵睡死 / 双 app / rev 重置"等错误
结论 —— `recordWarn` 在 Android 上只写 stderr（= /dev/null），dump 出来的每行
时间戳都是 **dump 时刻**。改用实时探针（内核 `ProbeLog` → logcat）才拿到第一
现场：键盘写入 883 之后 5ms 被一次 insets 上报写回 0。③ 怀疑"画面没更新"是
宿主驱动问题，实测 ticker 全程在跑 —— 是"算对了没上屏"（上屏的正是被清成 0
的那一帧）。

**三条结论**：① 会通知脚本的上报测试必须在泵内调；② "画面没更新"先分清
"没重算"与"算了没上屏"；③ 排查**时序问题**必须用带真实时间戳的实时日志，
环形缓冲 dump 只能当"发生过什么"的清单用。

## 桌面能守住的部分（先跑它，再上真机）

```bash
go test ./gfx/mobile/      # 7+1 个用例, 纯 Go, 开发机直接跑
```

- 触摸映射、泵唤醒、resize 通知、density/显示器上报、factory 绑定都在这里；
- `TestTouchDrivesButtonClick` 是"真机能点、数字递增"的桌面等价物（真脚本 + 真 `gfx.Pump`）；
- `TestAndroidAssetScriptClickDrivesCounter` 直接读**本目录的 `app/src/main/assets/app.js`** ——
  那份要打进 APK 的脚本被改坏（JSX 写错、用了相对 import、引用了不存在的模块）时它会当场红。
  验证过它真的会红：把 `onClick` 掏空 → 用例失败并打出点击前后的文本。

## v1 已知边界（都不是 bug，是没做）

- **单缓冲**：Go 写入与宿主拷贝可能重叠一帧（撕裂）。彻底解决要双缓冲 + 交换指针，等真机
  实测出撕裂概率再定。（**换缓冲本身**的竞态已经处理：`gfx/android` 的 `uploadFrame` 是持锁
  做整段拷贝的，`BindFrameBuffer` 拿同一把锁换地址 ⇒ 旋转/分屏时不会出现"旧缓冲已被 JVM
  回收而引擎还在往里写"。）
- **冷启动 insets 首报为 0（已修）**：一度以为宿主补报路径拿到的就是 0 —— 插桩后发现
  `rootWindowInsets` 与两次 dispatch **都是 b=63**，报上去的值是对的。真正的元凶是
  **折叠上报通道**：`displayFold.start()` 的 WindowLayoutInfo 回调经
  `gfx/mobile.ReportDisplayFold` 走 `gfx.ReportViewport`（patchAll），把刚报好的
  Insets 清成了 0。修法：内核新增 `gfx.ReportSizeClasses`（patchSizeClasses 掩码），
  三端共用的折叠入口改走它；回归 `gfx/mobile/fold_test.go` 的
  `TestReportDisplayFoldKeepsInsetsAndKeyboard`。
- **软键盘（IME）已接，模拟器已验收**（M2，2026-10-02，见下方实测记录）：
  `GoxSurfaceView.onCreateInputConnection` 给了一个 `BaseInputConnection`，
  `commitText` → `nativeIMECommit`、`deleteSurroundingText`
  → `nativeKey("Backspace")`、`setComposingText` 忽略（v1 只做"结果提交"，
  组合过程留在输入法里）；`onCursorUpdate`/`updateCursorAnchor` 回传光标
  位置与文本快照（**带位置参数必须先 `setMatrix`**，否则 `CursorAnchorInfo.Builder.build()`
  抛 `IllegalArgumentException` 崩在主线程 —— 已修）。键盘高度经
  `nativeSetKeyboard` → `gfx.ReportKeyboardHeight` 上报，脚本侧
  `useKeyboardHeight()` 响应式读。**未验证项**：中文拼音输入法的组合行为
  （模拟器只有 Gboard 英文环境，机制级验证覆盖了 ASCII 提交 / 候选词栏 /
  光标同步，拼音逐键组合属 P1 逐键上报范畴）。
- **单指触摸**：多指手势不识别，第二根手指按下即作废整个手势。
- **density 只上报不换算**：`Display.Scale` / `pixelRatio` 有了，但 layout 的逻辑像素换算
  还没做（M1 剩余部分）。所以现在 `font={20}` 就是 20 个物理像素，在高密度屏上偏小。
- **进程内不重入**：Activity 重进会新建一段会话，上一段在 gfx 注册表里的窗口记录由
  引擎自己走 `EventClose` 收尾（`nativeDestroy` → 表面关闭 → 泵收敛）。

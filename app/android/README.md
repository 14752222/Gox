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
| `app/src/main/kotlin/com/gox/GoxRuntime.kt` | `object GoxRuntime` 的 8 个 `external fun` |
| `gfx/android/libgox/main.go` | 对应的 `//export Java_com_gox_GoxRuntime_*` |
| `gfx/android/android.go` 头部的契约注释 | 同一份签名（给不打开 Kotlin 的人看） |
| `app/src/main/kotlin/com/gox/GoxHost.kt` | 反向回调：`flush([I)V` / `finished(ILjava/lang/String;)V` 是**逐字符**写死在 C 里的 |

三条容易踩的：

- **改包名/类名 = 改符号名**；`namespace`（本文件是 `com.gox`）与 Kotlin `package` 必须一致。
- **不要开混淆**（`isMinifyEnabled = false`）：混淆器改类名会让 `System.loadLibrary` 之后的
  方法绑定失败（`UnsatisfiedLinkError`）。真要开，必须给 `com.gox.GoxRuntime` 写 keep 规则。
- `GoxRuntime` 写成 `object` 而不是一堆 `static`：JNI 里实例方法与静态方法的**符号名相同**，
  只差第二个参数是 `jobject` 还是 `jclass`，而 Go 侧不用那个参数 —— 两种写法 ABI 等价。

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
- **软键盘（IME）未接**：真机上输入框只能看不能输（M2，工作量最大的一块）。
- **单指触摸**：多指手势不识别，第二根手指按下即作废整个手势。
- **density 只上报不换算**：`Display.Scale` / `pixelRatio` 有了，但 layout 的逻辑像素换算
  还没做（M1 剩余部分）。所以现在 `font={20}` 就是 20 个物理像素，在高密度屏上偏小。
- **进程内不重入**：Activity 重进会新建一段会话，上一段在 gfx 注册表里的窗口记录由
  引擎自己走 `EventClose` 收尾（`nativeDestroy` → 表面关闭 → 泵收敛）。

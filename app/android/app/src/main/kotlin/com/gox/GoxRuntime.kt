package com.gox

import java.nio.ByteBuffer

/**
 * 引擎入口: 方法体在 `libgox.so` 里 (Go 侧 `gfx/android/libgox/main.go` 的 `//export`)。
 *
 * **类名与方法名是 JNI 契约的一部分** —— Go 侧导出的符号是
 * `Java_<包名>_<类名>_<方法名>`, 所以下面三处必须逐字符对应:
 *
 *   Kotlin: `com.gox.GoxRuntime.nativeTick`
 *   Go:     `//export Java_com_gox_GoxRuntime_nativeTick`
 *   C 头:   `libgox.h` (构建时由 cgo 生成, 不在仓库里)
 *
 * 声明成 `object` (而非 static 方法) 是有意的: JNI 里实例方法与静态方法的**符号名相同**,
 * 区别只在第二个参数是 `jobject`(实例) 还是 `jclass`; Go 侧不使用那个参数, 所以两种写法
 * ABI 等价, 而 `object` 是 Kotlin 里最省事的写法 (能直接 `GoxRuntime.nativeTick()`)。
 */
object GoxRuntime {

    init {
        // 对应 libgox.so (脚本 build-android.sh 产出 → src/main/jniLibs/arm64-v8a/)
        System.loadLibrary("gox")
    }

    /**
     * 建立会话: 绑定帧缓冲与宿主回调, 创建引擎表面并注册成默认窗口后端。
     * **必须在 [nativeRunScript] 之前调**, 返回非 0 表示初始化失败 (日志里有原因)。
     *
     * @param frameBuffer 必须 `ByteBuffer.allocateDirect(w*h*4)`: Go 侧直接往这块内存
     *   写 RGBA (零拷贝)。堆内数组的地址会随 GC 搬家, 传进去等于写坏 JVM。
     */
    external fun nativeInit(
        width: Int,
        height: Int,
        density: Float,
        frameBuffer: ByteBuffer,
        host: GoxHost,
    ): Int

    /** 尺寸变化后重新分配了帧缓冲时调 (旧地址可能已被回收)。 */
    external fun nativeBindFrameBuffer(w: Int, h: Int, frameBuffer: ByteBuffer): Int

    /**
     * 跑一段脚本。**立即返回**: 真正的执行在 Go 自己的线程上 (在 UI 线程里跑事件泵
     * 会把界面冻住)。脚本只能 import 内置模块 (gx/xxx) —— 安卓侧没有文件系统可读。
     */
    external fun nativeRunScript(source: String, name: String)

    /** 每帧调一次, 叫醒 Go 的事件泵 (rAF 由此落在 vsync 上)。 */
    external fun nativeTick()

    /** 触摸。action 传 `MotionEvent.actionMasked` (Go 侧按 Android 的动作值映射)。 */
    external fun nativeTouch(action: Int, x: Float, y: Float)

    /** 外接键盘按键 (软键盘走 IME, 属 M2, 目前输入框在真机上还输不了字)。 */
    external fun nativeKey(key: String, down: Boolean)

    /** 表面尺寸/密度变化 (旋转、分屏、折叠)。 */
    external fun nativeResize(w: Int, h: Int, density: Float)

    /**
     * 安全区上报 (设备像素): 状态栏/刘海/导航栏/手势条占掉的边缘。
     * Go 侧经 gfx.Post 投回 GUI 线程再报内核 (gx/viewport), 任意线程可调。
     */
    external fun nativeSetInsets(top: Int, right: Int, bottom: Int, left: Int)

    /**
     * 输入法提交一批文本 (软键盘 commitText, 可能是整词)。任意线程可调,
     * Go 侧只投事件 —— 整批插入的语义在内核 gfx/ime.go。
     */
    external fun nativeIMECommit(text: String)

    // ── NativeHost 回填/上报通道 (Go 侧 //export 见 gfx/android/libgox/main.go) ──
    //
    // 全部可从任意线程调用: Go 侧一律 gfx.Post 投回 GUI 线程 (ResolveNative /
    // Report* 会同步跑脚本回调, 绝不在调用方线程执行 JS)。

    /**
     * 交付一次异步原生调用的结果 ([GoxNativeHost.nativeCall] 返回 "__pending__"
     * 之后用)。id 照抄 args 里的 `__id`; errCode 非空表示失败 —— **必须是 8 个
     * 统一错误码之一** (unsupported / permission-denied / cancelled / timeout /
     * busy / unavailable / platform-error / invalid-arg), Go 侧会把不认识的词
     * 归一成 platform-error。
     */
    external fun nativeResolveNative(id: String, resultJson: String, errCode: String, errMsg: String)

    /** 电池上报。json 字段: supported/level/charging/chargingType/temperature/lowPowerMode。 */
    external fun nativeReportBattery(json: String)

    /** 网络上报。json 字段: connected/type/metered/ssid/strength/carrier/generation。 */
    external fun nativeReportNetwork(json: String)

    /** 定位上报 (一般不用: 一次定位/监听的结果走 [nativeResolveNative])。 */
    external fun nativeReportLocation(json: String)

    /** 应用生命周期上报: "active" | "background" | "inactive"。 */
    external fun nativeReportAppState(state: String)

    /** 内存警告上报 (onTrimMemory)。 */
    external fun nativeReportMemoryWarning()

    /** 单个权限状态上报 (启动时批量报一遍 / 从设置页回来补报)。 */
    external fun nativeReportPermission(kind: String, state: String)

    /**
     * 返回键询问脚本: 返回 true = 脚本已处理 (宿主不要退出)。
     * **同步阻塞最多 500ms** 等脚本线程答复 (内部 gfx.Post + 泵唤醒 + 限时等待),
     * 超时按未处理返回 —— 见 libgox 里该导出的注释。
     */
    external fun nativeReportBackPress(): Boolean

    /** 结束会话: 唤醒事件泵使其收尾。幂等。 */
    external fun nativeDestroy()
}

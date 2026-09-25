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

    /** 结束会话: 唤醒事件泵使其收尾。幂等。 */
    external fun nativeDestroy()
}

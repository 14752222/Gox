package com.gox

import org.json.JSONObject

/**
 * NativeHost 宿主契约 (Kotlin 侧), 对应 Go 内核 `gfx/native.go` 的 `gfx.NativeHost`。
 *
 * ## 通道协议 (与 gfx/android/libgox/main.go 的协议逐字一致, 三处同步:
 * libgox/main.go / GoxRuntime.kt / 本文件)
 *
 * Go 内核在 GUI 线程调 `host.Call(method, args)` → libgox 把 args 序列化成 JSON
 * → JNI 调 [nativeCall]。返回值三选一 (字符串协议):
 *
 *   - [PENDING]                        → NativePending (稍后经
 *     [GoxRuntime.nativeResolveNative] 回填)
 *   - `{"__errCode":"…","__errMsg":"…"}` → NativeFailure (errCode 必须是 8 个
 *     统一错误码之一, 见下)
 *   - 其它 JSON                         → NativeResult (形状随方法而定, 见
 *     app/NATIVE-HOST.md 的对照表)
 *
 * ## 方法名 (以 Go 源码为唯一事实来源)
 *
 *  | method               | Go 侧定义                          | 同步/异步 |
 *  |----------------------|------------------------------------|-----------|
 *  | device.info          | gfx/native_device.go nmDeviceInfo  | 同步      |
 *  | device.vibrate       | nmDeviceVibrate                    | 同步      |
 *  | device.brightness    | nmDeviceBrightness                 | 同步      |
 *  | device.keepScreenOn  | nmDeviceKeepOn                     | 同步      |
 *  | device.openSettings  | nmDeviceSettings                   | 同步      |
 *  | app.share            | gfx/native_app.go nmAppShare       | 异步      |
 *  | app.exit             | nmAppExit                          | 同步      |
 *  | app.orientation      | nmAppOrientation                   | 同步      |
 *  | permission.get       | gfx/native_permission.go nmPermGet | 同步      |
 *  | permission.request   | nmPermRequest                      | 异步      |
 *  | camera.takePhoto     | gfx/native_media.go nmCameraTake   | 异步      |
 *  | gallery.pick         | nmGalleryPick                      | 异步      |
 *  | gallery.pickVideo    | nmGalleryPickVideo                 | 异步      |
 *  | media.save           | nmMediaSave                        | 异步      |
 *  | media.preview        | nmMediaPreview                     | 异步      |
 *  | location.get         | gfx/native_geo.go nmLocationGet    | 异步      |
 *  | location.watch       | nmLocationWatch                    | 异步(流)  |
 *  | location.unwatch     | 停流 (内核 stopNativeStream 自动调) | 同步      |
 *
 * 纯上报型能力没有 Call (宿主只在状态变化时调 GoxRuntime.nativeReport*):
 * insets / battery / network / appstate / memory。
 *
 * ## 8 个统一错误码 (gfx/native.go, 一个都不能私造)
 *
 *   unsupported / permission-denied / cancelled / timeout / busy /
 *   unavailable / platform-error / invalid-arg
 *
 * 缺能力必须显式 reject ([GoxRuntime.nativeResolveNative] 带 errCode, 或同步
 * 返回错误信封), 绝不能返回假数据。
 *
 * ## 线程纪律
 *
 * [nativeCall] 在 **Go 的脚本线程**上被调 (不是 Android 主线程)。同步实现必须
 * 都是纯内存/系统读, 不碰 UI; 要碰 UI/等系统回调的实现一律 post 到主线程并
 * 返回 [PENDING]。
 */
interface GoxNativeHost {

    /** 声明会的方法名/能力名, 逗号分隔 (内核 canIUse/capabilities 的依据)。 */
    fun nativeCapabilities(): String

    /**
     * 执行一个原生方法。
     *
     * @param method  方法名 ("camera.takePhoto" 等, 见文件头表)
     * @param argsJson 内核给的参数对象 JSON (调用方选项 + 内部字段 "__id";
     *   异步方法回填时必须把 __id 原样传回 [GoxRuntime.nativeResolveNative])
     * @return 见文件头协议 (PENDING / 错误信封 / 结果 JSON)
     */
    fun nativeCall(method: String, argsJson: String): String

    companion object {
        /** "稍后回填" 哨兵: nativeCall 返回它表示结果将经 nativeResolveNative 交付。 */
        const val PENDING = "__pending__"

        // 8 个统一错误码 (与 gfx/native.go 逐字一致)。
        const val ERR_UNSUPPORTED = "unsupported"
        const val ERR_PERMISSION_DENIED = "permission-denied"
        const val ERR_CANCELLED = "cancelled"
        const val ERR_TIMEOUT = "timeout"
        const val ERR_BUSY = "busy"
        const val ERR_UNAVAILABLE = "unavailable"
        const val ERR_PLATFORM = "platform-error"
        const val ERR_INVALID_ARG = "invalid-arg"

        /** 造一个错误信封回包 (nativeCall 的失败形态)。 */
        fun err(code: String, msg: String): String =
            JSONObject(mapOf("__errCode" to code, "__errMsg" to msg)).toString()

        /**
         * 能力声明表: 与 GoxNativeHostImpl 的实现一一对应 (少报会让 canIUse 说
         * "没有", 多报会让 canIUse 说谎 —— 内核 Call 进来还是会 unsupported)。
         */
        val CAPABILITY_LIST = listOf(
            "device.info",
            "device.vibrate",
            "device.brightness",
            "device.keepScreenOn",
            "device.openSettings",
            "app.share",
            "app.exit",
            "app.orientation",
            "permission.get",
            "permission.request",
            "camera.takePhoto",
            "gallery.pick",
            "gallery.pickVideo",
            "media.save",
            "media.preview",
            "location.get",
            "location.watch",
            "location.unwatch",
            // 纯上报型 (只有 Report, 没有 Call):
            "insets",
            "battery",
            "network",
            "appstate",
            "memory",
        )
    }
}

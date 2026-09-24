package com.gox

/**
 * 宿主回调, **由 Go 调用**。
 *
 * 线程: 这两个方法都运行在 **Go 的渲染线程**上, 不是 UI 线程 —— 实现里要碰 View/Bitmap
 * 就必须自己 post 回主线程 (见 MainActivity.flush)。
 *
 * 名字与签名是 JNI 契约的一部分: Go 侧写死了查找 `flush([I)V` 与
 * `finished(ILjava/lang/String;)V`, 改签名会让 [GoxRuntime.nativeInit] 直接返回非 0
 * ("GoxHost.flush([I)V 找不到")。
 */
interface GoxHost {

    /**
     * 把引擎刚画好的一帧刷到屏幕上。
     *
     * @param rects 脏区, 格式 `[x, y, w, h, x, y, w, h, ...]`, 空/null 表示整帧。
     *   **实现必须在返回前拷走** —— 它背后是 JNI 局部引用, Go 侧返回后即失效。
     */
    fun flush(rects: IntArray?)

    /** 脚本结束。code != 0 时 error 有值 (脚本异常或引擎初始化失败)。 */
    fun finished(code: Int, error: String?)
}

/**
 * libgox.so 的类型声明 (NAPI 导出面)。
 *
 * ## 为什么要有这个文件
 *
 * ArkTS 里 `import nativeGox from 'libgox.so'` 拿到的是**无类型**的模块对象。
 * 鸿蒙工程的标准做法是给它配一份 `.d.ts` (并在 entry/oh-package.json5 里以
 * `file:./src/main/cpp/types/libgox` 的形式声明依赖), 这样 IDE 有补全、编译期
 * 能发现参数写错。这些签名**不是**装饰: 每一个都必须与 Go 侧
 * `gfx/harmony/libgox/main.go` 的 `goxMethods` 表逐字对上。
 *
 * ## 三处名字的同步关系 (改一处要改三处)
 *
 *   C enum GOX_M_*        gfx/harmony/libgox/bridge.c
 *   Go goxMethods 表      gfx/harmony/libgox/main.go
 *   本文件 + GoxBridge.ets  app/harmony/entry/src/main/ets/native/
 *
 * 前两者靠**序号**对话 (中间插一行就整体错位); 本文件靠**名字**, 名字写错时
 * ArkTS 拿到的是 `undefined` 函数, 调用即 "not a function" —— 两种失败都被
 * gfx/mobile/fold_contract_test.go 的鸿蒙用例钉住。
 *
 * ## 参数语义上的两条约定
 *
 *  - `action` 沿用 Android MotionEvent 的那组数字 (DOWN=0/UP=1/MOVE=2/CANCEL=3),
 *    由 GoxBridge 的 `TouchAction` 常量给出。两端同值是有意的: 对照移植时不会
 *    因为"数字看着对但含义不同"错得很隐蔽。
 *  - `frameBuffer` **必须是 `ArrayBuffer`**(不是它的 TypedArray 视图)。视图带
 *    byteOffset, 会让 Go 侧按"裸地址 + 长度"算错, 症状是画面整体错位一行。
 */

/** 宿主回调: 由 Go 调, 全部同步。 */
export interface GoxHostCallbacks {
  /**
   * 把一帧送到屏幕上。
   * @param rects 脏区 [x,y,w,h,...] (像素); 传 undefined 表示整帧刷新。
   */
  flush(rects?: Int32Array): void;
  /** 脚本结束 (含异常)。error 为空表示正常结束。 */
  finished(code: number, error?: string): void;
  /** 软键盘开关 (焦点进/出编辑框时内核调)。 */
  imeShow(show: boolean): void;
  /** 声明会的方法名/能力名, 逗号分隔 (与 Android/iOS 同协议)。 */
  nativeCapabilities(): string;
  /** 执行一个原生方法, 返回 pending 哨兵 / 错误信封 / 结果 JSON。 */
  nativeCall(method: string, argsJson: string): string;
}

/** 建表面并绑定帧缓冲与宿主回调。返回 0 表示成功。 */
export const init: (width: number, height: number, density: number,
  frameBuffer: ArrayBuffer, host: GoxHostCallbacks) => number;

/** 尺寸变化后重新绑一块新缓冲。 */
export const bindFrameBuffer: (width: number, height: number,
  frameBuffer: ArrayBuffer) => number;

/** 跑一段脚本 (立即返回, 真正执行在 Go 自己的线程上)。 */
export const runScript: (source: string, name: string) => number;

/** 每帧一次 (ArkUI 的 frameCallback / 自定 setInterval)。 */
export const tick: () => void;

/** 触摸。action 见文件头的语义约定。 */
export const touch: (action: number, x: number, y: number) => void;

/** 外接键盘/按键 (软键盘走 IME 提交)。 */
export const key: (key: string, down: boolean) => void;

/** 窗口尺寸/density 变化。 */
export const resize: (width: number, height: number, density: number) => void;

/** 安全区 (top/right/bottom/left, 像素)。 */
export const setInsets: (top: number, right: number, bottom: number,
  left: number) => void;

/** 软键盘整批插入的文本。 */
export const imeCommit: (text: string) => void;

/** 拆除会话。 */
export const destroy: () => void;

/** 回填一个 pending 的原生调用结果 (errCode 非空即失败)。 */
export const resolveNative: (id: string, resultJson: string,
  errCode: string, errMsg: string) => void;

/** 上报电池状态 (JSON)。 */
export const reportBattery: (json: string) => void;

/** 上报网络状态 (JSON)。 */
export const reportNetwork: (json: string) => void;

/** 上报告位置 (JSON)。 */
export const reportLocation: (json: string) => void;

/** 上报应用前后台状态: "foreground" / "background" / ... */
export const reportAppState: (state: string) => void;

/** 上报内存告警。 */
export const reportMemoryWarning: () => void;

/** 上报权限状态变化: kind 如 "camera", state 如 "granted"。 */
export const reportPermission: (kind: string, state: string) => void;

/**
 * 上报返回键。**唯一同步等答复**的入口: 返回 true 表示脚本已处理 (宿主不要退出)。
 * 内部 500ms 超时, 超时按未处理返回。
 */
export const reportBackPress: () => boolean;

/**
 * 上报折叠屏状态 (JSON, 字段见 gfx/mobile.FoldInfo)。
 *
 * **这是 HF2 的入口**, 与 Android 的 nativeSetDisplayFold / iOS 的
 * gox_set_display_fold 完全同构 —— 三者最终都进
 * `gfx/mobile.ReportDisplayFold`。任意线程可调 (内部 gfx.Post 回 GUI 线程)。
 */
export const setDisplayFold: (json: string) => void;

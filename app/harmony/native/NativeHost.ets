/**
 * NativeHost 宿主契约 (鸿蒙 ArkTS 骨架), 对应 Go 内核 gfx/native.go 的
 * gfx.NativeHost 接口。
 *
 * ## 与 Android/iOS 实现的关系
 *
 * 三平台的通道协议**逐字一致** (以 Go 源码为唯一事实来源, 对照表见
 * app/NATIVE-HOST.md):
 *
 *   Call(method, args) → args 含内部字段 "__id" (内核 nativeArgsWithID 注入)。
 *   返回值三选一 (字符串协议):
 *     "__pending__"                     → Pending (稍后经 resolveNative 回填)
 *     {"__errCode":"…","__errMsg":"…"}  → 失败 (errCode 必须是 8 个统一错误码)
 *     其它 JSON                          → 成功结果
 *
 *   resolveNative(id, resultJson, errCode, errMsg): id 照抄 args 里的 "__id";
 *   errCode 非空即失败。流式调用 (location.watch) 对同一个 id 反复回填。
 *
 * ## 骨架的立场 (重要)
 *
 * 本文件目前只有**契约与 stub**: 未实现的方法必须显式返回 unsupported,
 * **绝不返回假数据** (gfx/native.go 文件头的降级口径)。鸿蒙真实现待工程
 * (ohos shell) 搭起来后, 按本文件的 method 表逐个补。
 */

// ── 8 个统一错误码 (gfx/native.go, 一个都不能私造) ──
export const ERR_UNSUPPORTED: string = 'unsupported'
export const ERR_PERMISSION_DENIED: string = 'permission-denied'
export const ERR_CANCELLED: string = 'cancelled'
export const ERR_TIMEOUT: string = 'timeout'
export const ERR_BUSY: string = 'busy'
export const ERR_UNAVAILABLE: string = 'unavailable'
export const ERR_PLATFORM: string = 'platform-error'
export const ERR_INVALID_ARG: string = 'invalid-arg'

/** "稍后回填" 哨兵: call 返回它表示结果将经 resolveNative 交付。 */
export const PENDING: string = '__pending__'

// ── 方法名 (Go 侧常量的镜像, 见 gfx/native_*.go) ──
export class Methods {
  static readonly DEVICE_INFO: string = 'device.info'
  static readonly DEVICE_VIBRATE: string = 'device.vibrate'
  static readonly DEVICE_BRIGHTNESS: string = 'device.brightness'
  static readonly DEVICE_KEEP_SCREEN_ON: string = 'device.keepScreenOn'
  static readonly DEVICE_OPEN_SETTINGS: string = 'device.openSettings'
  static readonly APP_SHARE: string = 'app.share'
  static readonly APP_EXIT: string = 'app.exit'
  static readonly APP_ORIENTATION: string = 'app.orientation'
  static readonly PERMISSION_GET: string = 'permission.get'
  static readonly PERMISSION_REQUEST: string = 'permission.request'
  static readonly CAMERA_TAKE_PHOTO: string = 'camera.takePhoto'
  static readonly GALLERY_PICK: string = 'gallery.pick'
  static readonly GALLERY_PICK_VIDEO: string = 'gallery.pickVideo'
  static readonly MEDIA_SAVE: string = 'media.save'
  static readonly MEDIA_PREVIEW: string = 'media.preview'
  static readonly LOCATION_GET: string = 'location.get'
  static readonly LOCATION_WATCH: string = 'location.watch'
  static readonly LOCATION_UNWATCH: string = 'location.unwatch'
}

/** 宿主声明的能力表 (方法名或纯上报型能力名, 内核 canIUse 的依据)。 */
export const CAPABILITIES: string[] = [
  // 六模块的 Call 型方法 —— 鸿蒙真实现落地后逐项放开:
  // Methods.DEVICE_INFO, … (当前 stub 不声明, canIUse 诚实地回答 false)
  // 纯上报型 (只有 Report, 没有 Call) —— 由宿主生命周期直接喂:
  'insets',
]

/**
 * 宿主契约 (Go: gfx.NativeHost)。
 */
export interface NativeHost {
  /** 声明会的方法名/能力名 (逗号分隔串, 与 Android/iOS 同协议)。 */
  capabilities(): string
  /**
   * 执行一个原生方法。
   * @param method   方法名 (Methods.*)
   * @param argsJson 内核参数对象 JSON (含 "__id")
   * @returns PENDING / 错误信封 / 结果 JSON (协议见文件头)
   */
  call(method: string, argsJson: string): string
}

/**
 * 造错误信封回包 (call 的失败形态)。
 */
export function err(code: string, msg: string): string {
  return JSON.stringify({ __errCode: code, __errMsg: msg })
}

/**
 * NativeHost 的鸿蒙 stub 实现 (骨架)。
 *
 * ## 当前状态: 全部显式 unsupported
 *
 * 鸿蒙壳工程 (Ability + 渲染管线桥) 还没搭起来, 这里先把**契约的形状**立住:
 *   - capabilities() 只声明纯上报型能力 (insets) —— Call 型方法一个不报,
 *     脚本侧 canIUse("camera") 等会诚实回答 false, 调用会拿到显式的
 *     unsupported reject, 而不是静默假数据;
 *   - call() 对所有方法返回 err(unsupported) —— 逐字与 Go 的 8 个错误码对齐。
 *
 * ## 真实现落地时的接线方式 (与 Android/iOS 同构)
 *
 *   Ability onCreate → 构造 GoxNativeHostImpl → 经 NativeBridge 注入 Go 内核
 *   (Go 侧需要一个 gfx.NativeHost 适配器, 参照 gfx/ios/libgox/main.go 的
 *   cfnNativeHost: Call → 平台函数调用 → JSON 回包, 异步方法经
 *   resolveNative 回填)。
 *
 * 六模块各自要补什么 (对照 Go 侧):
 *   device   Build 参数/@ohos.batteryInfo/@ohos.telephony.radio/@ohos.vibrator
 *   app      Ability 生命周期 (onForeground/onBackground → reportAppState)
 *   geo      @ohos.geoLocationManager (一次定位 + on('locationChange') 流)
 *   media    @ohos.multimedia.camera / photoAccessHelper / picker
 *   permission @ohos.abilityAccessCtrl (requestPermissionsFromUser)
 *   viewport @ohos.window 的 safeArea ( avoidance areas → nativeSetInsets)
 */
import {
  NativeHost,
  Methods,
  ERR_UNSUPPORTED,
  err,
  PENDING,
} from './NativeHost'

export class GoxNativeHostStub implements NativeHost {
  /**
   * 只报纯上报型能力。Call 型方法在真实现落地后加入
   * (少报 canIUse 说"没有", 多报说谎 —— 两头都是错的)。
   */
  capabilities(): string {
    return 'insets'
  }

  call(method: string, argsJson: string): string {
    // argsJson 里的 "__id" 在真实现中是回填钥匙; stub 里解析出来只为日志,
    // 不回填任何东西。
    void argsJson
    switch (method) {
      // 每个契约方法一个显式分支: 鸿蒙真实现按模块逐个替换成真调用,
      // **不许**删掉 unsupported 兜底 —— 未知方法必须显式报错。
      case Methods.DEVICE_INFO:
      case Methods.DEVICE_VIBRATE:
      case Methods.DEVICE_BRIGHTNESS:
      case Methods.DEVICE_KEEP_SCREEN_ON:
      case Methods.DEVICE_OPEN_SETTINGS:
      case Methods.APP_SHARE:
      case Methods.APP_EXIT:
      case Methods.APP_ORIENTATION:
      case Methods.PERMISSION_GET:
      case Methods.PERMISSION_REQUEST:
      case Methods.CAMERA_TAKE_PHOTO:
      case Methods.GALLERY_PICK:
      case Methods.GALLERY_PICK_VIDEO:
      case Methods.MEDIA_SAVE:
      case Methods.MEDIA_PREVIEW:
      case Methods.LOCATION_GET:
      case Methods.LOCATION_WATCH:
      case Methods.LOCATION_UNWATCH:
        return err(ERR_UNSUPPORTED, `鸿蒙宿主尚未实现 ${method} (骨架 stub)`)
      default:
        return err(ERR_UNSUPPORTED, `鸿蒙宿主未实现方法 ${method}`)
    }
  }
}

/**
 * 回填通道占位: 真实现里把结果 (或错误) 经桥送回 Go 内核的 ResolveNative
 * (流式 watch 对同一个 id 反复调)。stub 只表达签名, 不做任何事。
 */
export function resolveNative(id: string, resultJson: string,
  errCode: string, errMsg: string): void {
  void id
  void resultJson
  void errCode
  void errMsg
}

/** PENDING 导出再出口 (宿主实现 import 本文件即可拿到完整协议)。 */
export { PENDING }

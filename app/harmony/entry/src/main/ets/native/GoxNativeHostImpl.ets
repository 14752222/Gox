/**
 * NativeHost 的鸿蒙真实现 (六能力模块)。
 *
 * ## 状态: 已实现 + 显式降级 (v1.0)
 *
 * 与 GoxNativeHostStub 的关系: stub 保留作"无上下文兜底" (单测 / 无 Ability 场景),
 * 正式会话一律走本类 —— 由 pages/Index.ets 构造时注入 GoxSession。
 *
 * ## 同步 / 异步纪律 (内核硬约束, 见 app/NATIVE-HOST.md)
 *
 *   同步方法 (必须当场返回, 不能 pending):
 *     device.info / device.vibrate / device.brightness / device.keepScreenOn /
 *     device.openSettings / app.exit / app.orientation / permission.get /
 *     location.unwatch
 *   异步方法 (返回 PENDING, 稍后 resolveNative 回填):
 *     app.share / permission.request / camera.takePhoto / gallery.pick /
 *     gallery.pickVideo / media.save / media.preview / location.get
 *   流式: location.watch 对同一个 id 反复回填, location.unwatch 停流 (幂等)。
 *
 * ## 窗口句柄的取法
 *
 * 同步方法里拿不到 Promise —— `window.getLastWindow()` 是异步的。所以构造时
 * 就发起一次取窗, 成功后**缓存**; 缓存还没到手时同步方法返回 unavailable
 * (启动头几毫秒的窗口期, 比假装成功诚实)。
 *
 * ## 降级口径 (v1.0, 与 app/NATIVE-HOST.md 状态表逐行对应)
 *
 *   - device.openSettings: 鸿蒙三方应用只允许拉起**本应用详情页** (kind="app"),
 *     其余 10 个 kind 显式 unsupported (与 iOS 同构, iOS 也只有 app 总览页)。
 *   - device.brightness 写: 系统级亮度是 system_api, 三方应用只能设**本窗口**
 *     亮度 (setWindowBrightness); 读走 settings 数据库 (系统亮度)。
 *   - app.share: 走 ShareKit (systemShare)。**TODO(真机)**: ShareKit 面板依赖
 *     华为账号登录态, 模拟器/无账号设备上的拉起形态需真机验收。
 *   - camera.takePhoto: 走 cameraPicker (系统相机 UI, 免权限)。**TODO(真机)**:
 *     saveToGallery / maxWidth 重采样未实现 (v1.0 文档明示), format 恒 jpeg。
 *   - media.save: 走 photoAccessHelper.createAsset (需 ohos.permission.WRITE_IMAGEVIDEO
 *     或走 saveButton 授权), album 参数忽略 (createAsset 不指定相册)。**TODO(真机)**。
 *   - media.preview: 鸿蒙没有等价的系统多文件预览组件 (iOS QLPreview /
 *     Android ACTION_VIEW), v1.0 显式 unsupported。
 *   - gcj02: 不做火星坐标换算, 与 Android/iOS 一致显式 platform-error。
 *
 * 错误码只用 NativeHost.ets 的 8 个统一码, 一个都不私造。
 */
import { common, Want, abilityAccessCtrl, bundleManager, Permissions } from '@kit.AbilityKit';
import { window, display } from '@kit.ArkUI';
import { deviceInfo, settings } from '@kit.BasicServicesKit';
import { vibrator } from '@kit.SensorServiceKit';
import { geoLocationManager } from '@kit.LocationKit';
import { photoAccessHelper } from '@kit.MediaLibraryKit';
import { cameraPicker } from '@kit.CameraKit';
import { fileIo as fs } from '@kit.CoreFileKit';
import { systemShare } from '@kit.ShareKit';
import { uniformTypeDescriptor as utd } from '@kit.ArkData';
import { i18n } from '@kit.LocalizationKit';
import { hilog } from '@kit.PerformanceAnalysisKit';
import nativeGox from 'libgox.so';
import {
  NativeHost,
  Methods,
  ERR_UNSUPPORTED,
  ERR_PERMISSION_DENIED,
  ERR_CANCELLED,
  ERR_UNAVAILABLE,
  ERR_PLATFORM,
  ERR_INVALID_ARG,
  err,
  PENDING,
} from './NativeHost';

const TAG: string = 'Gox';
const DOMAIN: number = 0x0000;

/** 鸿蒙设置应用的身份 (拉起本应用详情页用)。 */
const SETTINGS_BUNDLE: string = 'com.huawei.hmos.settings';
const SETTINGS_ABILITY: string = 'com.huawei.hmos.settings.MainAbility';

/** kind → 鸿蒙系统权限 (permission.get/request 的映射表)。 */
const PERM_MAP: Record<string, Permissions[]> = {
  'location': ['ohos.permission.APPROXIMATELY_LOCATION', 'ohos.permission.LOCATION'] as Permissions[],
  'camera': ['ohos.permission.CAMERA'] as Permissions[],
  'microphone': ['ohos.permission.MICROPHONE'] as Permissions[],
  'notification': ['ohos.permission.NOTIFICATION_CONTROLLER'] as Permissions[],
  'contacts': ['ohos.permission.READ_CONTACTS'] as Permissions[],
  'calendar': ['ohos.permission.READ_CALENDAR'] as Permissions[],
  'bluetooth': ['ohos.permission.ACCESS_BLUETOOTH'] as Permissions[],
}

/** 鸿蒙内核识别的权限 kind 全集 (gfx/native_permission.go)。 */
const PERM_KINDS: string[] = [
  'location', 'camera', 'gallery', 'microphone', 'notification',
  'contacts', 'calendar', 'bluetooth', 'storage',
]

/** 解析 args JSON; 失败时记日志并返回空对象 (与"参数缺失"同处理)。 */
function parseArgs(argsJson: string): Record<string, Object> {
  try {
    return JSON.parse(argsJson) as Record<string, Object>;
  } catch (e) {
    hilog.warn(DOMAIN, TAG, 'args JSON 解析失败, 按空参数处理');
    return {};
  }
}

function argStr(args: Record<string, Object>, key: string, dft: string): string {
  const v = args[key];
  return typeof v === 'string' && (v as string).length > 0 ? v as string : dft;
}

function argNum(args: Record<string, Object>, key: string, dft: number): number {
  const v = args[key];
  return typeof v === 'number' ? v as number : dft;
}

/** 异步成功回填。 */
function resolveOK(id: string, result: Object): void {
  nativeGox.resolveNative(id, JSON.stringify(result), '', '');
}

/** 异步失败回填。 */
function resolveErr(id: string, code: string, msg: string): void {
  nativeGox.resolveNative(id, '', code, msg);
}

export class GoxNativeHostImpl implements NativeHost {
  private readonly context: common.UIAbilityContext;
  /**
   * 缓存的主窗口 (同步方法用, 见文件头"窗口句柄的取法")。
   * undefined = 还没取到, 同步方法只能答 unavailable。
   */
  private win: window.Window | undefined = undefined;
  /** 活跃的 location.watch (id → 系统回调登记), unwatch 按 id 幂等摘除。 */
  private readonly watchers: Map<string, boolean> = new Map();

  constructor(context: common.UIAbilityContext) {
    this.context = context;
    window.getLastWindow(context).then((w: window.Window) => {
      this.win = w;
    }).catch((e: Error) => {
      hilog.warn(DOMAIN, TAG, 'getLastWindow 失败, 窗口类同步方法将答 unavailable');
    });
  }

  capabilities(): string {
    // 已实现的方法 + 纯上报型。media.preview / 多数 settings kind 不在此列
    // (canIUse 要诚实, 见 stub 文件头的"少报说没有, 多报说谎")。
    const caps: string[] = [
      Methods.DEVICE_INFO,
      Methods.DEVICE_VIBRATE,
      Methods.DEVICE_BRIGHTNESS,
      Methods.DEVICE_KEEP_SCREEN_ON,
      Methods.DEVICE_OPEN_SETTINGS,
      Methods.APP_SHARE,
      Methods.APP_EXIT,
      Methods.APP_ORIENTATION,
      Methods.PERMISSION_GET,
      Methods.PERMISSION_REQUEST,
      Methods.CAMERA_TAKE_PHOTO,
      Methods.GALLERY_PICK,
      Methods.GALLERY_PICK_VIDEO,
      Methods.MEDIA_SAVE,
      Methods.LOCATION_GET,
      Methods.LOCATION_WATCH,
      Methods.LOCATION_UNWATCH,
      'insets',
      'displayFold',
      'battery',
      'network',
      'appstate',
      'memory',
    ];
    return caps.join(',');
  }

  call(method: string, argsJson: string): string {
    const args: Record<string, Object> = parseArgs(argsJson);
    const id: string = argStr(args, '__id', '');
    switch (method) {
      case Methods.DEVICE_INFO:
        return this.deviceInfo();
      case Methods.DEVICE_VIBRATE:
        return this.vibrate(argNum(args, 'duration', 300));
      case Methods.DEVICE_BRIGHTNESS:
        return this.brightness(args);
      case Methods.DEVICE_KEEP_SCREEN_ON:
        return this.keepScreenOn(args['on'] === true);
      case Methods.DEVICE_OPEN_SETTINGS:
        return this.openSettings(argStr(args, 'kind', 'app'));
      case Methods.APP_SHARE:
        this.share(id, args);
        return PENDING;
      case Methods.APP_EXIT:
        return this.appExit();
      case Methods.APP_ORIENTATION:
        return this.orientation(argStr(args, 'mode', 'auto'));
      case Methods.PERMISSION_GET:
        return this.permissionGet();
      case Methods.PERMISSION_REQUEST:
        this.permissionRequest(id, args);
        return PENDING;
      case Methods.CAMERA_TAKE_PHOTO:
        this.takePhoto(id, args);
        return PENDING;
      case Methods.GALLERY_PICK:
        this.galleryPick(id, args, false);
        return PENDING;
      case Methods.GALLERY_PICK_VIDEO:
        this.galleryPick(id, args, true);
        return PENDING;
      case Methods.MEDIA_SAVE:
        this.mediaSave(id, args);
        return PENDING;
      case Methods.MEDIA_PREVIEW:
        // 鸿蒙无等价系统预览组件 (v1.0 显式降级, 见文件头)。
        return err(ERR_UNSUPPORTED, '鸿蒙没有系统多文件预览组件 (v1.0 降级)');
      case Methods.LOCATION_GET:
        this.locationGet(id, args);
        return PENDING;
      case Methods.LOCATION_WATCH:
        return this.locationWatch(id, args);
      case Methods.LOCATION_UNWATCH:
        return this.locationUnwatch(id);
      default:
        return err(ERR_UNSUPPORTED, `鸿蒙宿主未实现方法 ${method}`);
    }
  }

  // ── gx/device ──

  /**
   * device.info: 宿主拉取型, 缺字段内核用本机缺省覆盖 (applyDeviceOverlay)。
   * 这里给鸿蒙能拿到的全部; udid 需要受限权限, 不申请 → deviceId 缺省给空。
   */
  private deviceInfo(): string {
    try {
      const d: display.Display = display.getDefaultDisplaySync();
      const locale: i18n.Locale = i18n.System.getLocale();
      const tz: i18n.TimeZone = i18n.System.getTimeZone();
      const info = {
        platform: 'harmony',
        os: 'HarmonyOS',
        osVersion: deviceInfo.osFullName,
        model: deviceInfo.productModel,
        brand: deviceInfo.brand,
        manufacturer: deviceInfo.manufacture,
        sdkVersion: deviceInfo.sdkApiVersion,
        locale: locale.id,
        timezone: tz.getID(),
        tzOffset: tz.getRawOffset() / 60000,
        screenWidth: d.width,
        screenHeight: d.height,
        pixelRatio: d.densityPixels,
        isTablet: deviceInfo.deviceType === 'tablet' || deviceInfo.deviceType === '2in1',
        isEmulator: deviceInfo.deviceType === 'emulator',
        appName: '',
        appVersion: '',
        appBuild: 0,
      };
      // 应用信息 (name/version) 单独取, 失败不拖垮整包。
      try {
        const b: bundleManager.BundleInfo =
          bundleManager.getBundleInfoForSelfSync(
            bundleManager.BundleFlag.GET_BUNDLE_INFO_WITH_APPLICATION);
        info.appName = b.appInfo.labelResource ? b.name : b.name;
        info.appVersion = b.versionName;
        info.appBuild = b.versionCode;
      } catch (e) {
        hilog.warn(DOMAIN, TAG, 'deviceInfo: 应用信息读取失败, 留空');
      }
      return JSON.stringify(info);
    } catch (e) {
      return err(ERR_PLATFORM, `deviceInfo 失败: ${(e as Error).message}`);
    }
  }

  /** device.vibrate: 软调用, 结果内核忽略。无震动器的设备答 unavailable。 */
  private vibrate(durationMs: number): string {
    try {
      vibrator.startVibration(
        { type: 'time', duration: durationMs > 0 ? durationMs : 300 },
        { usage: 'touch' },
      );
      return 'true';
    } catch (e) {
      // TODO(真机): 模拟器一律抛错, 真机无震动马达机型需确认错误形态。
      return err(ERR_UNAVAILABLE, `振动不可用: ${(e as Error).message}`);
    }
  }

  /**
   * device.brightness: 读 → 系统亮度 (settings 库, 0..255 归一);
   * 写 → 本窗口亮度 (三方应用无系统亮度写权限, 见文件头降级口径)。
   */
  private brightness(args: Record<string, Object>): string {
    const hasValue: boolean = typeof args['value'] === 'number';
    if (hasValue) {
      const w = this.win;
      if (w === undefined) {
        return err(ERR_UNAVAILABLE, '窗口未就绪, 亮度未设置');
      }
      let v: number = argNum(args, 'value', -1);
      v = Math.max(0, Math.min(1, v));
      try {
        w.setWindowBrightness(v);
        return JSON.stringify({ value: v });
      } catch (e) {
        return err(ERR_PLATFORM, `设置窗口亮度失败: ${(e as Error).message}`);
      }
    }
    try {
      const s: string = settings.getValueSync(
        this.context, settings.display.SCREEN_BRIGHTNESS_STATUS, '-1');
      const raw: number = Number(s);
      // TODO(真机): 系统亮度量程在各机型/系统版本上需核对 (通常 0..255)。
      const v: number = raw >= 0 ? Math.min(1, raw / 255) : -1;
      return JSON.stringify({ value: v });
    } catch (e) {
      return JSON.stringify({ value: -1 });
    }
  }

  private keepScreenOn(on: boolean): string {
    const w = this.win;
    if (w === undefined) {
      return err(ERR_UNAVAILABLE, '窗口未就绪, 常亮未设置');
    }
    try {
      w.setWindowKeepScreenOn(on);
      return 'true';
    } catch (e) {
      return err(ERR_PLATFORM, `keepScreenOn 失败: ${(e as Error).message}`);
    }
  }

  /**
   * device.openSettings: 鸿蒙三方应用只允许拉起**本应用详情页**。
   * 内核白名单 11 个 kind 里只实现 "app", 其余显式 unsupported (与 iOS 同构)。
   */
  private openSettings(kind: string): string {
    if (kind !== 'app') {
      return err(ERR_UNSUPPORTED,
        `鸿蒙三方应用不支持拉起 "${kind}" 设置页 (仅 app 详情页)`);
    }
    const want: Want = {
      bundleName: SETTINGS_BUNDLE,
      abilityName: SETTINGS_ABILITY,
      uri: 'application_info_entry',
    };
    this.context.startAbility(want).then(() => {
      // 同步契约要求当场返回; 这里只能"先答应" —— 与 Android 的
      // startActivity 即发即忘同语义。
    }).catch((e: Error) => {
      hilog.error(DOMAIN, TAG, '拉起设置页失败: %{public}s', e.message);
    });
    return 'true';
  }

  // ── gx/app ──

  /**
   * app.share: ShareKit 面板 (异步, 拉起即回填 {})。
   * TODO(真机): 面板依赖华为账号登录态, 需真机验收拉起形态与取消路径。
   */
  private share(id: string, args: Record<string, Object>): void {
    const text: string = argStr(args, 'text', '');
    const url: string = argStr(args, 'url', '');
    if (text.length === 0 && url.length === 0) {
      resolveErr(id, ERR_INVALID_ARG, 'share 需要 text 或 url 至少一项');
      return;
    }
    try {
      const data: systemShare.SharedData = new systemShare.SharedData({
        utd: url.length > 0 ? utd.UniformDataType.HYPERLINK : utd.UniformDataType.TEXT,
        content: url.length > 0 ? url : text,
        title: text,
      });
      const controller: systemShare.ShareController = new systemShare.ShareController(data);
      controller.show(this.context, {
        previewMode: systemShare.SharePreviewMode.DEFAULT,
        selectionMode: systemShare.SelectionMode.SINGLE,
      }).then(() => {
        resolveOK(id, {});
      }).catch((e: Error) => {
        resolveErr(id, ERR_PLATFORM, `分享面板拉起失败: ${e.message}`);
      });
    } catch (e) {
      resolveErr(id, ERR_UNAVAILABLE, `ShareKit 不可用: ${(e as Error).message}`);
    }
  }

  /** app.exit: terminateSelf 是"发起即忘", 无法知道成败 → 软调用 true。 */
  private appExit(): string {
    this.context.terminateSelf().catch((e: Error) => {
      hilog.error(DOMAIN, TAG, 'terminateSelf 失败: %{public}s', e.message);
    });
    return 'true';
  }

  /**
   * app.orientation: mode 已由内核归一 (portrait/landscape/auto)。
   * 返回 true/false (内核 setOrientation() 是 bool)。
   */
  private orientation(mode: string): string {
    const w = this.win;
    if (w === undefined) {
      return err(ERR_UNAVAILABLE, '窗口未就绪, 方向未设置');
    }
    let o: window.Orientation = window.Orientation.AUTO_ROTATION;
    if (mode === 'portrait') {
      o = window.Orientation.PORTRAIT;
    } else if (mode === 'landscape') {
      o = window.Orientation.LANDSCAPE;
    }
    try {
      w.setPreferredOrientation(o);
      return 'true';
    } catch (e) {
      return err(ERR_PLATFORM, `设置方向失败: ${(e as Error).message}`);
    }
  }

  // ── gx/permission ──

  /** 本应用的 accessTokenId (checkAccessTokenSync 的入参)。 */
  private tokenId(): number {
    const b: bundleManager.BundleInfo = bundleManager.getBundleInfoForSelfSync(
      bundleManager.BundleFlag.GET_BUNDLE_INFO_WITH_APPLICATION);
    return b.appInfo.accessTokenId;
  }

  /**
   * 单个 kind 的同步状态。
   * gallery: PhotoViewPicker 走系统选择器, 免权限 → 恒 granted (与 iOS PHPicker 同构)。
   * storage: 鸿蒙没有等价公共存储权限 → unknown。
   */
  private permState(kind: string): string {
    if (kind === 'gallery') {
      return 'granted';
    }
    const perms = PERM_MAP[kind];
    if (perms === undefined) {
      return 'unknown';
    }
    try {
      const at: abilityAccessCtrl.AtManager = abilityAccessCtrl.createAtManager();
      const tid: number = this.tokenId();
      for (const p of perms) {
        const st: abilityAccessCtrl.GrantStatus = at.checkAccessTokenSync(tid, p);
        if (st !== abilityAccessCtrl.GrantStatus.PERMISSION_GRANTED) {
          return 'denied';
        }
      }
      return 'granted';
    } catch (e) {
      return 'unknown';
    }
  }

  /** permission.get: 全量快照 (鸿蒙同步可查 → 全部给实值, 不留缺省)。 */
  private permissionGet(): string {
    const out: Record<string, string> = {};
    for (const k of PERM_KINDS) {
      out[k] = this.permState(k);
    }
    return JSON.stringify(out);
  }

  /**
   * permission.request: 批量一次申请一次回填 (与 Android 同语义)。
   * gallery 免权限 → 直接 granted; 无映射的 kind → unknown。
   */
  private permissionRequest(id: string, args: Record<string, Object>): void {
    let kinds: string[] = [];
    const single: string = argStr(args, 'kind', '');
    if (single.length > 0) {
      kinds.push(single);
    }
    const multi = args['kinds'];
    if (Array.isArray(multi)) {
      for (const k of multi as Object[]) {
        if (typeof k === 'string') {
          kinds.push(k as string);
        }
      }
    }
    if (kinds.length === 0) {
      resolveErr(id, ERR_INVALID_ARG, 'permission.request 需要 kind 或 kinds');
      return;
    }
    const result: Record<string, string> = {};
    const need: Permissions[] = [];
    const needKinds: string[] = [];
    for (const k of kinds) {
      if (k === 'gallery') {
        result[k] = 'granted';
        continue;
      }
      const perms = PERM_MAP[k];
      if (perms === undefined) {
        result[k] = 'unknown';
        continue;
      }
      need.push(...perms);
      needKinds.push(k);
    }
    if (need.length === 0) {
      resolveOK(id, result);
      return;
    }
    const at: abilityAccessCtrl.AtManager = abilityAccessCtrl.createAtManager();
    at.requestPermissionsFromUser(this.context, need)
      .then((r: abilityAccessCtrl.PermissionRequestResult) => {
        // authResults 与 need 一一对应; 同一 kind 可能占多个权限位。
        let idx: number = 0;
        for (const k of needKinds) {
          const n: number = PERM_MAP[k].length;
          let granted: boolean = true;
          for (let i = 0; i < n; i++) {
            if (r.authResults[idx + i] !== 0) {
              granted = false;
            }
          }
          idx += n;
          result[k] = granted ? 'granted' : 'denied';
        }
        // 状态变化上报 (内核据此刷新缓存 + 发事件)。
        for (const k of kinds) {
          nativeGox.reportPermission(k, result[k]);
        }
        resolveOK(id, result);
      })
      .catch((e: Error) => {
        resolveErr(id, ERR_PLATFORM, `权限申请失败: ${e.message}`);
      });
  }

  // ── gx/media ──

  /** 把系统返回的 content uri 复制进应用缓存, 产出内核认的 MediaFile。 */
  private copyToCache(srcUri: string, ext: string): string {
    const src: fs.File = fs.openSync(srcUri, fs.OpenMode.READ_ONLY);
    try {
      const path: string = `${this.context.cacheDir}/gox-${Date.now()}.${ext}`;
      const dst: fs.File = fs.openSync(path, fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE);
      try {
        fs.copyFileSync(src.fd, dst.fd);
      } finally {
        fs.closeSync(dst);
      }
      return path;
    } finally {
      fs.closeSync(src);
    }
  }

  private mediaFileOf(path: string, mimeType: string): Record<string, Object> {
    let size: number = 0;
    try {
      size = fs.statSync(path).size;
    } catch (e) {
      // 大小拿不到不致命, 0 交给内核兜底。
    }
    return {
      path: path,
      name: path.substring(path.lastIndexOf('/') + 1),
      mimeType: mimeType,
      size: size,
    };
  }

  /**
   * camera.takePhoto: 系统相机 UI (免权限)。
   * TODO(真机): saveToGallery / maxWidth 重采样未实现; format 恒 jpeg;
   * front 摄像头机型差异需验收。
   */
  private takePhoto(id: string, args: Record<string, Object>): void {
    const profile: cameraPicker.PickerProfile = {
      cameraPosition: argStr(args, 'camera', 'back') === 'front'
        ? cameraPicker.CameraPosition.CAMERA_POSITION_FRONT
        : cameraPicker.CameraPosition.CAMERA_POSITION_BACK,
    };
    cameraPicker.pick(this.context, [cameraPicker.PickerMediaType.PHOTO], profile)
      .then((r: cameraPicker.PickerResult) => {
        if (r.resultCode !== 0 || r.resultUri.length === 0) {
          resolveErr(id, ERR_CANCELLED, '拍照已取消');
          return;
        }
        try {
          const path: string = this.copyToCache(r.resultUri, 'jpg');
          resolveOK(id, this.mediaFileOf(path, 'image/jpeg'));
        } catch (e) {
          resolveErr(id, ERR_PLATFORM, `照片落盘失败: ${(e as Error).message}`);
        }
      })
      .catch((e: Error) => {
        resolveErr(id, ERR_PLATFORM, `拉起相机失败: ${e.message}`);
      });
  }

  /**
   * gallery.pick / pickVideo: PhotoViewPicker (系统选择器, 免权限)。
   * count 限 1..9 (与内核契约一致); compressed/quality 由系统 UI 处理, 宿主不插手。
   */
  private galleryPick(id: string, args: Record<string, Object>, video: boolean): void {
    const opts = new photoAccessHelper.PhotoSelectOptions();
    opts.MIMEType = video
      ? photoAccessHelper.PhotoViewMIMETypes.VIDEO_TYPE
      : photoAccessHelper.PhotoViewMIMETypes.IMAGE_TYPE;
    opts.maxSelectNumber = video ? 1 : Math.max(1, Math.min(9, argNum(args, 'count', 1)));
    const p = new photoAccessHelper.PhotoViewPicker();
    p.select(opts).then((r: photoAccessHelper.PhotoSelectResult) => {
      if (r.photoUris.length === 0) {
        resolveErr(id, ERR_CANCELLED, '未选择任何文件');
        return;
      }
      try {
        const mime: string = video ? 'video/mp4' : 'image/jpeg';
        const ext: string = video ? 'mp4' : 'jpg';
        if (video || opts.maxSelectNumber === 1) {
          const path: string = this.copyToCache(r.photoUris[0], ext);
          resolveOK(id, this.mediaFileOf(path, mime));
          return;
        }
        const files: Record<string, Object>[] = [];
        for (const uri of r.photoUris) {
          files.push(this.mediaFileOf(this.copyToCache(uri, ext), mime));
        }
        resolveOK(id, files);
      } catch (e) {
        resolveErr(id, ERR_PLATFORM, `读取所选文件失败: ${(e as Error).message}`);
      }
    }).catch((e: Error) => {
      resolveErr(id, ERR_PLATFORM, `拉起相册失败: ${e.message}`);
    });
  }

  /**
   * media.save: createAsset 申请相册位 → 写文件内容。
   * album 参数忽略 (createAsset 不指定相册); 需 WRITE_IMAGEVIDEO 或
   * saveButton 授权态。**TODO(真机)**: 授权弹窗形态需真机验收。
   */
  private mediaSave(id: string, args: Record<string, Object>): void {
    const file = args['file'];
    if (file === undefined || typeof file !== 'object') {
      resolveErr(id, ERR_INVALID_ARG, 'media.save 需要 file 参数');
      return;
    }
    const srcPath: string = argStr(file as Record<string, Object>, 'path', '');
    if (srcPath.length === 0) {
      resolveErr(id, ERR_INVALID_ARG, 'media.save 的 file 缺 path');
      return;
    }
    const isVideo: boolean = srcPath.toLowerCase().endsWith('.mp4');
    const ext: string = isVideo ? 'mp4' : 'jpg';
    try {
      const helper: photoAccessHelper.PhotoAccessHelper =
        photoAccessHelper.getPhotoAccessHelper(this.context);
      const type: photoAccessHelper.PhotoType =
        isVideo ? photoAccessHelper.PhotoType.VIDEO : photoAccessHelper.PhotoType.IMAGE;
      helper.createAsset(type, ext).then((uri: string) => {
        try {
          const src: fs.File = fs.openSync(srcPath, fs.OpenMode.READ_ONLY);
          try {
            const dst: fs.File = fs.openSync(uri, fs.OpenMode.WRITE_ONLY);
            try {
              fs.copyFileSync(src.fd, dst.fd);
            } finally {
              fs.closeSync(dst);
            }
          } finally {
            fs.closeSync(src);
          }
          resolveOK(id, {
            uri: uri,
            path: srcPath,
            name: srcPath.substring(srcPath.lastIndexOf('/') + 1),
            mimeType: isVideo ? 'video/mp4' : 'image/jpeg',
          });
        } catch (e) {
          resolveErr(id, ERR_PLATFORM, `写入相册失败: ${(e as Error).message}`);
        }
      }).catch((e: Error) => {
        resolveErr(id, ERR_PERMISSION_DENIED, `相册保存授权失败: ${e.message}`);
      });
    } catch (e) {
      resolveErr(id, ERR_PLATFORM, `media.save 失败: ${(e as Error).message}`);
    }
  }

  // ── gx/geo ──

  /** 系统 Location → 内核 Location JSON (键名与 gfx/harmony 的 dispatch 一致)。 */
  private locationJson(l: geoLocationManager.Location, type: string): string {
    return JSON.stringify({
      latitude: l.latitude,
      longitude: l.longitude,
      altitude: l.altitude,
      accuracy: l.accuracy,
      altitudeAccuracy: 0,
      speed: l.speed,
      heading: l.direction,
      timestamp: l.timeStamp,
      provider: 'system',
      mocked: false,
      type: type,
    });
  }

  /** gcj02 检查: 不做火星换算, 与 Android/iOS 一致显式 platform-error。 */
  private checkGeoType(args: Record<string, Object>): string {
    const t: string = argStr(args, 'type', 'wgs84');
    return t === 'gcj02' ? '' : t;
  }

  private locationGet(id: string, args: Record<string, Object>): void {
    const type: string = this.checkGeoType(args);
    if (type.length === 0) {
      resolveErr(id, ERR_PLATFORM, '鸿蒙宿主不提供 gcj02 (与 Android/iOS 一致)');
      return;
    }
    if (this.permState('location') !== 'granted') {
      resolveErr(id, ERR_PERMISSION_DENIED, '定位权限未授权 (不替脚本偷偷弹窗)');
      return;
    }
    const req: geoLocationManager.CurrentLocationRequest = {
      priority: args['highAccuracy'] === true
        ? geoLocationManager.LocationRequestPriority.ACCURACY
        : geoLocationManager.LocationRequestPriority.FIRST_FIX,
      timeoutMs: Math.max(1, argNum(args, 'timeout', 30)) * 1000,
      maxAgeMs: Math.max(0, argNum(args, 'maximumAge', 0)) * 1000,
    };
    geoLocationManager.getCurrentLocation(req)
      .then((l: geoLocationManager.Location) => {
        resolveOK(id, JSON.parse(this.locationJson(l, type)) as Object);
      })
      .catch((e: Error) => {
        resolveErr(id, ERR_UNAVAILABLE, `定位失败: ${e.message}`);
      });
  }

  /**
   * location.watch: 流式, 对同一个 id 反复回填。
   * unwatch 由内核 stopNativeStream 自动调, 宿主要幂等。
   */
  private locationWatch(id: string, args: Record<string, Object>): string {
    const type: string = this.checkGeoType(args);
    if (type.length === 0) {
      return err(ERR_PLATFORM, '鸿蒙宿主不提供 gcj02 (与 Android/iOS 一致)');
    }
    if (this.permState('location') !== 'granted') {
      return err(ERR_PERMISSION_DENIED, '定位权限未授权 (不替脚本偷偷弹窗)');
    }
    if (this.watchers.has(id)) {
      return 'true'; // 幂等: 同一 id 重复 watch 不重复登记
    }
    const req: geoLocationManager.ContinuousLocationRequest = {
      interval: 1,
      locationScenario: args['highAccuracy'] === true
        ? geoLocationManager.LocationRequestScenario.NAVIGATION
        : geoLocationManager.LocationRequestScenario.UNSET,
    };
    const cb = (l: geoLocationManager.Location): void => {
      resolveOK(id, JSON.parse(this.locationJson(l, type)) as Object);
    };
    try {
      geoLocationManager.on('locationChange', req, cb);
      this.watchers.set(id, true);
      return PENDING;
    } catch (e) {
      return err(ERR_UNAVAILABLE, `watch 定位失败: ${(e as Error).message}`);
    }
  }

  private locationUnwatch(id: string): string {
    if (!this.watchers.has(id)) {
      return 'true'; // 幂等: 不存在的 watch 也是"已停"
    }
    this.watchers.delete(id);
    try {
      // off 不带回调即摘掉该类型的全部登记 (鸿蒙 API 形态)。
      // 当前实现一个会话同一时刻只有一个活跃 watch, 语义安全。
      geoLocationManager.off('locationChange');
    } catch (e) {
      hilog.warn(DOMAIN, TAG, 'unwatch 摘登记失败 (按已停处理)');
    }
    return 'true';
  }
}

/** PENDING 再出口。 */
export { PENDING }

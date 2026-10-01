/**
 * Gox 会话的 ArkTS 侧封装: 帧缓冲、上屏、事件泵。
 *
 * ## 分工 (与 gfx 的三层结构对应)
 *
 *   gfx/mobile       纯 Go: 事件队列 / 触摸映射 / 泵唤醒        ← 可在开发机单测
 *   gfx/harmony      NAPI 通道: ArrayBuffer 直写 + flush 回调
 *   libgox.so        装配: 建 Surface + 注册窗口后端 + 跑 VM
 *   本文件            宿主侧: 分配 ArrayBuffer、把像素送进 Image 组件、喂事件
 *
 * ## 为什么用 ArrayBuffer + PixelMap 而不是 XComponent 的 native window
 *
 * Go 侧拿到 `ArrayBuffer` 的**裸地址**直写 (零拷贝), 写完调一次 `flush`; 宿主把
 * 同一块 buffer 交给 PixelMap 显示。这条路与 Android 的
 * `allocateDirect` + `Bitmap.copyPixelsFromBuffer` 是**同构**的 —— 好处是两端
 * 能共用一份像素校验工具 (gfx 的截图哈希脚本), 且宿主侧不需要写一行 C++。
 *
 * 逐帧直接往 native window 写 (XComponent + OH_NativeWindow) 是后续优化:
 * 那需要壳工程带 cpp 目录、并在 native 侧接管 OnSurfaceCreated。v1 不做 ——
 * 存一个不消费的 `OHNativeWindow*` 只会变成悬垂指针隐患。
 *
 * ## frameBuffer 的两条硬约束 (违反都是静默错画面)
 *
 *  1. 必须是 `ArrayBuffer` 本身, **不是它的 TypedArray 视图**: 视图带 byteOffset,
 *     Go 侧按"裸地址 + 长度"算会整体错位 (画面斜切)。
 *  2. 尺寸变化必须重新分配**并重新绑定** (`bindFrameBuffer`): 旧地址可能已被 GC
 *     复用, 继续往那儿写就是写坏 JS 堆。
 */
import { image } from '@kit.ImageKit';
import { hilog } from '@kit.PerformanceAnalysisKit';
import nativeGox from 'libgox.so';
import { GoxHostCallbacks } from 'libgox.so';
import { NativeHost } from './NativeHost';
import { GoxNativeHostStub } from './GoxNativeHost';

const TAG: string = 'Gox';
const DOMAIN: number = 0x0000;

/** 一帧一次的间隔 (ms)。Gox 的渲染是"有脏区才重绘", 这个只是唤醒泵的心跳。 */
const FRAME_INTERVAL_MS: number = 16;

/**
 * 触摸动作取值 —— **故意与 Android MotionEvent 的那组数字一致**
 * (DOWN=0/UP=1/MOVE=2/CANCEL=3)。两端同值, 对照移植代码时不会因为"数字看着对
 * 但含义不同"错得隐蔽。
 */
export class TouchAction {
  static readonly DOWN: number = 0;
  static readonly UP: number = 1;
  static readonly MOVE: number = 2;
  static readonly CANCEL: number = 3;
}

/** 新一帧就绪时的回调 (宿主据此刷新 UI)。 */
export type FrameSink = () => void;

/** 脚本结束时的回调。 */
export type FinishSink = (code: number, error: string) => void;

/**
 * 一个 Gox 会话 (一屏一个)。
 */
export class GoxSession {
  private buf: ArrayBuffer | null = null;
  private pixmap: image.PixelMap | null = null;
  private w: number = 0;
  private h: number = 0;
  private density: number = 1;
  private timerId: number = -1;
  /** 上一帧还在往 PixelMap 里写时, 丢掉这一帧 —— 宁可掉帧也不堆队列。 */
  private writing: boolean = false;
  private started: boolean = false;

  private readonly onFrame: FrameSink;
  private readonly onFinish: FinishSink;
  private readonly nativeHost: NativeHost;
  private readonly host: GoxHostCallbacks;

  constructor(onFrame: FrameSink, onFinish: FinishSink, nativeHost?: NativeHost) {
    this.onFrame = onFrame;
    this.onFinish = onFinish;
    // 默认用 stub (全部显式 unsupported)。六模块真实现落地后换成真实现类即可。
    this.nativeHost = nativeHost ?? new GoxNativeHostStub();

    this.host = {
      flush: (rects?: Int32Array): void => {
        this.flush(rects);
      },
      finished: (code: number, error?: string): void => {
        this.stopTimer();
        this.onFinish(code, error ?? '');
      },
      imeShow: (show: boolean): void => {
        // 软键盘宿主侧实现在 M2 之前是空操作; 留日志便于确认内核有在调。
        hilog.info(DOMAIN, TAG, 'imeShow(%{public}s)', show ? 'true' : 'false');
      },
      nativeCapabilities: (): string => {
        return this.nativeHost.capabilities();
      },
      nativeCall: (method: string, argsJson: string): string => {
        return this.nativeHost.call(method, argsJson);
      },
    };
  }

  /**
   * 建表面并启动脚本。
   *
   * @returns 0 表示成功; 非 0 时宿主应当报错 —— **不要**继续跑一个没有帧缓冲的
   *          会话 (那样的症状是"界面全黑但日志正常")。
   */
  start(width: number, height: number, density: number,
    script: string, name: string): number {
    if (this.started) {
      return 0;
    }
    this.started = true;
    this.allocate(width, height, density);

    const rc: number = nativeGox.init(width, height, density, this.buf as ArrayBuffer, this.host);
    if (rc !== 0) {
      hilog.error(DOMAIN, TAG, 'libgox.init 返回 %{public}d', rc);
      return rc;
    }
    this.startTimer();
    return nativeGox.runScript(script, name);
  }

  /** 窗口尺寸/density 变化。 */
  resize(width: number, height: number, density: number): void {
    if (!this.started) {
      return;
    }
    if (width === this.w && height === this.h && density === this.density) {
      return;
    }
    this.allocate(width, height, density);
    nativeGox.bindFrameBuffer(width, height, this.buf as ArrayBuffer);
    nativeGox.resize(width, height, density);
  }

  /** 触摸事件 (坐标是**窗口像素**, 不是 vp)。 */
  touch(action: number, x: number, y: number): void {
    if (!this.started) {
      return;
    }
    nativeGox.touch(action, x, y);
  }

  /** 安全区 (像素)。 */
  setInsets(top: number, right: number, bottom: number, left: number): void {
    if (!this.started) {
      return;
    }
    nativeGox.setInsets(top, right, bottom, left);
  }

  /** 收尾: 停帧、拆会话、放掉 PixelMap。 */
  destroy(): void {
    if (!this.started) {
      return;
    }
    this.started = false;
    this.stopTimer();
    nativeGox.destroy();
    const pm = this.pixmap;
    this.pixmap = null;
    if (pm !== null) {
      pm.release();
    }
    this.buf = null;
  }

  /** 当前该显示的 PixelMap (交给 `<Image>`)。未就绪时返回 undefined。 */
  pixelMap(): image.PixelMap | undefined {
    return this.pixmap ?? undefined;
  }

  // ── 内部 ──

  /**
   * 重新分配帧缓冲与 PixelMap。
   *
   * **必须同时换掉这两样**: ArrayBuffer 是 Go 直写的目标, PixelMap 是显示用的
   * 载体 —— 只换一个就会出现"渲染到新缓冲、显示的还是旧尺寸"这种错位。
   */
  private allocate(width: number, height: number, density: number): void {
    this.w = width;
    this.h = height;
    this.density = density;

    const old = this.pixmap;
    this.buf = new ArrayBuffer(width * height * 4);

    const opts: image.InitializationOptions = {
      editable: true,
      pixelFormat: image.PixelMapFormat.RGBA_8888,
      size: { width: width, height: height },
    };
    this.pixmap = image.createPixelMapSync(this.buf, opts);
    if (old !== null) {
      old.release();
    }
    hilog.info(DOMAIN, TAG, '帧缓冲: %{public}dx%{public}d density=%{public}f',
      width, height, density);
  }

  /**
   * Go 侧渲染完一帧后调 (在 Go 的 GUI 线程上, 但 NAPI 调用线程无关)。
   *
   * 这里把 ArrayBuffer 交给 PixelMap, 再让宿主刷新。**不做脏区优化**:
   * `writeBufferToPixels` 是整块的, 契约也明确允许一次额外拷贝 (与 Android 一致)。
   * 要按 rects 局部刷新的前提是先有真机的帧耗时数据。
   */
  private flush(rects?: Int32Array): void {
    void rects;
    const pm = this.pixmap;
    const buf = this.buf;
    if (pm === null || buf === null) {
      return;
    }
    if (this.writing) {
      return; // 上一帧还没写完
    }
    this.writing = true;
    pm.writeBufferToPixels(buf).then(() => {
      this.writing = false;
      this.onFrame();
    }).catch((e: Error) => {
      this.writing = false;
      hilog.error(DOMAIN, TAG, 'writeBufferToPixels 失败: %{public}s', e.message);
    });
  }

  /**
   * 每帧叫醒一次 Go 的事件泵。
   *
   * 内核的渲染是"有脏区才重绘", tick 只是唤醒可能睡着的泵 —— 所以这个定时器
   * 的频率不决定渲染帧率, 只决定**事件响应的最坏延迟**。
   */
  private startTimer(): void {
    if (this.timerId !== -1) {
      return;
    }
    this.timerId = setInterval(() => {
      nativeGox.tick();
    }, FRAME_INTERVAL_MS);
  }

  private stopTimer(): void {
    if (this.timerId === -1) {
      return;
    }
    clearInterval(this.timerId);
    this.timerId = -1;
  }
}

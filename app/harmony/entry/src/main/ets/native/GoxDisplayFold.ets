/**
 * 鸿蒙折叠屏姿态与折痕上报 (HF2)。
 *
 * ## 链路 (三平台同构)
 *
 *   系统 display/window 事件  →  拼 JSON  →  libgox.setDisplayFold(json)
 *     →  gfx/harmony/libgox GoxDispatch(mSetDisplayFold)
 *     →  gfx/mobile.ReportDisplayFold
 *     →  gfx 内核 (Display.Regions / Hinge)
 *     →  JS 侧 screen.posture() / hinge() / reservedRegions() / hasFold()
 *
 * Android 走 GoxDisplayFold.kt → nativeSetDisplayFold (JNI), iOS 走
 * GoxDisplayFold.swift → gox_set_display_fold (C)。**三者最后进的是同一个
 * `gfx/mobile.ReportDisplayFold`** —— 所以解析、校验、归一化的规则只有一份,
 * 平台侧只负责"把系统给的东西翻译成那个 JSON 形状"。
 *
 * ## 三条硬约束 (违反的表现全是"静默失效", 不是崩溃)
 *
 * ### ① 折痕不随 flat 清除
 *
 * `display.getCurrentFoldCreaseRegion()` **只在半折叠态返回非空**。若平展时就把
 * hinge 丢掉, 于是"折起来 → 展开 → 再折起来"会让分栏比例跳变 (内核按新的铰链
 * 重新算布局), 用户会看到界面闪一下。
 *
 * 折痕是**设备几何属性**, 不是姿态属性: 本文件把它缓存下来, 平展/折叠态时仍原样
 * 带着报, 只把 `active` 置 false (内核据此判定"铰链存在但不占用可见区域")。
 * 这样 `hasFold()` 恒为 true, 列数不会在折叠/平展之间跳。
 *
 * ### ② 首次上报替换整张显示器表
 *
 * 内核的 ReportPosture 语义是: **第一次上报建表, 之后按 display id upsert**。
 * 所以首次必须带全 `display` / `width` / `height` (/ 尺寸类), 否则"窗口所在
 * 显示器"解析不到, 姿态读了也是空的 —— 症状是折叠双栏静默失效。本文件在
 * `start()` 里等窗口拿到之后立刻补一次完整上报, 就是为了这个。
 *
 * ### ③ 姿态只做特性信号, 绝不驱动布局
 *
 * 华为官方《多设备适配屏幕差异》原文: **"不推荐使用折叠状态监听接口实现页面
 * 响应式布局和接续"**。理由很实在:
 *
 *   - 上下折 (如 Pocket 系列) 展开后宽度仍 < 600vp, 用 foldStatus 推布局必然
 *     给出错误的"大屏"判断;
 *   - 官方时序里 `windowSizeChange` **早于** `foldStatusChange`(展开态) —— 用
 *     姿态驱动会慢一帧, 表现为"展开后界面先错一帧再跳"。
 *
 * 所以本文件上报的 posture **只**用于 `hasFold()` / `reservedRegions()` 这类
 * 特性判断; 布局响应式一律走尺寸 (width/height + dp 断点, 见 widthClassOf)。
 */
import { display, window } from '@kit.ArkUI';
import { hilog } from '@kit.PerformanceAnalysisKit';
import { common } from '@kit.AbilityKit';
import nativeGox from 'libgox.so';

const TAG: string = 'Gox';
const DOMAIN: number = 0x0000;

/** 折痕矩形 (像素)。 */
interface CreaseRect {
  x: number;
  y: number;
  width: number;
  height: number;
}

/** 上报载荷里的铰链 (与 gfx/mobile.RawHinge 对齐: 短名 w/h 也认, 这里用全名)。 */
interface FoldHinge {
  x: number;
  y: number;
  width: number;
  height: number;
  orientation: string;
}

/** 上报载荷里的保留区 (与 gfx/mobile.RawRegion 对齐)。 */
interface FoldRegion {
  kind: string;
  x: number;
  y: number;
  width: number;
  height: number;
  active: boolean;
}

/** 上报载荷 (字段见 gfx/mobile.decodeFoldInfo; 扁平 widthClass/heightClass 优先于 sizeClass 对象)。 */
interface FoldPayload {
  display: string;
  posture: string;
  width: number;
  height: number;
  widthClass: string;
  heightClass: string;
  hinge?: FoldHinge;
  regions?: FoldRegion[];
}

/** 姿态取值 (与 gfx/mobile 的 Posture* 常量逐字一致)。 */
export class Posture {
  static readonly FLAT: string = 'flat';
  static readonly HALF_OPEN: string = 'half-open';
  static readonly FOLDED: string = 'folded';
  static readonly UNKNOWN: string = 'unknown';
}

/**
 * 宽度分档 —— **必须与内核 gfx/viewport.go 的 deriveSizeClasses 用同一组断点**
 * (600 / 840)。内核在没有宿主上报时也会自己按 dp 推一份, 两边不一致会让
 * "宿主说 expanded、内核算 medium"这种分歧变成一个很难查的布局抖动。
 */
function widthClassOf(dp: number): string {
  if (dp < 600) {
    return 'compact';
  }
  if (dp <= 840) {
    return 'medium';
  }
  return 'expanded';
}

/** 高度分档 (两档, 480dp) —— 与 deriveSizeClasses 一致, 理由见那边的注释。 */
function heightClassOf(dp: number): string {
  return dp < 480 ? 'compact' : 'regular';
}

/** display.FoldStatus → 内核姿态取值。 */
function postureOf(status: display.FoldStatus): string {
  switch (status) {
    case display.FoldStatus.FOLD_STATUS_EXPANDED:
      return Posture.FLAT;
    case display.FoldStatus.FOLD_STATUS_HALF_FOLDED:
      return Posture.HALF_OPEN;
    case display.FoldStatus.FOLD_STATUS_FOLDED:
      return Posture.FOLDED;
    default:
      return Posture.UNKNOWN;
  }
}

/** 折痕方向: 一条"窄而高"的折痕 = 竖向折叠轴 → vertical (与 Android FoldingFeature 同义)。 */
function orientationOf(r: CreaseRect): string {
  return r.width < r.height ? 'vertical' : 'horizontal';
}

/**
 * 折叠屏上报器。一个 Ability 实例持有一个。
 *
 * 用法 (EntryAbility.onWindowStageCreate):
 *
 *   this.fold = new GoxDisplayFold(this.context)
 *   this.fold.start()
 *   // onDestroy 里 this.fold.stop()
 */
export class GoxDisplayFold {
  private ctx: common.UIAbilityContext;
  private started: boolean = false;
  private foldable: boolean = false;
  private win: window.Window | null = null;
  /** 见约束①: 折痕是设备几何属性, 缓存下来, 平展时也带着报。 */
  private lastCrease: CreaseRect | null = null;
  private lastPosture: string = Posture.UNKNOWN;
  /** 上一次成功上报的 JSON —— 完全相同的载荷不重复过桥 (windowSizeChange 很密)。 */
  private lastJson: string = '';
  private foldCb: ((status: display.FoldStatus) => void) | null = null;
  private sizeCb: ((size: window.Size) => void) | null = null;

  constructor(ctx: common.UIAbilityContext) {
    this.ctx = ctx;
  }

  /** 开始监听。幂等 (重复调用直接返回)。 */
  start(): void {
    if (this.started) {
      return;
    }
    this.started = true;

    this.foldable = this.queryFoldable();

    if (this.foldable) {
      // 注意 display.on 是**静态**方法, 回调参数是 FoldStatus 值。
      this.foldCb = (status: display.FoldStatus) => {
        this.onFoldStatus(status);
      };
      try {
        display.on('foldStatusChange', this.foldCb);
      } catch (e) {
        hilog.error(DOMAIN, TAG, 'GoxDisplayFold: 注册 foldStatusChange 失败');
      }
      // 开机时可能已经处于半折叠态 —— 先主动采一次折痕。
      // getFoldStatus 在非折叠 ROM 上会抛 (编译器在这里提示"可能抛异常"):
      // 包住, 失败按不可折叠处理, 别让启动流程被它带走。
      try {
        this.onFoldStatus(display.getFoldStatus());
      } catch (e) {
        hilog.warn(DOMAIN, TAG, 'GoxDisplayFold: getFoldStatus 失败 (按不可折叠处理)');
      }
    }

    // 约束②: 首次上报必须带全尺寸。等 window 拿到之后立刻补一次。
    window.getLastWindow(this.ctx).then((win: window.Window) => {
      this.win = win;
      this.sizeCb = (size: window.Size) => {
        void size;
        this.report();
      };
      try {
        win.on('windowSizeChange', this.sizeCb);
      } catch (e) {
        hilog.error(DOMAIN, TAG, 'GoxDisplayFold: 注册 windowSizeChange 失败');
      }
      this.report();
    }).catch((e: Error) => {
      hilog.error(DOMAIN, TAG, 'GoxDisplayFold: getLastWindow 失败: %{public}s', e.message);
    });
  }

  /** 停止监听 (Ability.onDestroy 里调; 回调实例必须与注册时**同一个**)。 */
  stop(): void {
    if (!this.started) {
      return;
    }
    this.started = false;
    if (this.foldCb !== null) {
      try {
        display.off('foldStatusChange', this.foldCb);
      } catch (e) {
        hilog.warn(DOMAIN, TAG, 'GoxDisplayFold: off foldStatusChange 失败 (忽略)');
      }
      this.foldCb = null;
    }
    if (this.win !== null && this.sizeCb !== null) {
      try {
        this.win.off('windowSizeChange', this.sizeCb);
      } catch (e) {
        hilog.warn(DOMAIN, TAG, 'GoxDisplayFold: off windowSizeChange 失败 (忽略)');
      }
      this.sizeCb = null;
    }
  }

  // ── 内部 ──

  private queryFoldable(): boolean {
    try {
      return display.isFoldable();
    } catch (e) {
      // 非折叠设备上某些 ROM 会直接抛 —— 那是"不可折叠", 不是错误。
      return false;
    }
  }

  private onFoldStatus(status: display.FoldStatus): void {
    this.lastPosture = postureOf(status);
    if (this.foldable) {
      this.refreshCrease(status);
    }
    this.report();
  }

  /**
   * 采一次折痕并缓存 (约束①)。
   *
   * 只有半折叠态系统才给折痕; 平展/折叠态**保留**上次的折痕, 但按"不占可见
   * 区域"上报 (width=0 + active=false)。内核据此仍认得出这台设备有折痕。
   */
  private refreshCrease(status: display.FoldStatus): void {
    if (status !== display.FoldStatus.FOLD_STATUS_HALF_FOLDED) {
      return; // 保留缓存, 不动
    }
    try {
      const region: display.FoldCreaseRegion = display.getCurrentFoldCreaseRegion();
      const rects: Array<display.Rect> = region.creaseRects;
      if (rects.length > 0) {
        const r: display.Rect = rects[0];
        this.lastCrease = { x: r.left, y: r.top, width: r.width, height: r.height };
      }
    } catch (e) {
      hilog.warn(DOMAIN, TAG, 'GoxDisplayFold: 取折痕失败 (保留上次的)');
    }
  }

  private report(): void {
    if (this.win === null) {
      return;
    }
    let rect: window.Rect;
    try {
      rect = this.win.getWindowProperties().windowRect;
    } catch (e) {
      return;
    }
    const wpx: number = rect.width;
    const hpx: number = rect.height;
    if (wpx <= 0 || hpx <= 0) {
      return;
    }

    // dp 换算用 densityPixels (与内核 deriveSizeClasses 的 scale 同义)。
    let scale: number = 1;
    try {
      const d: display.Display = display.getDefaultDisplaySync();
      if (d.densityPixels > 0) {
        scale = d.densityPixels;
      }
    } catch (e) {
      hilog.warn(DOMAIN, TAG, 'GoxDisplayFold: 取 densityPixels 失败, 按 1 处理');
    }

    const payload: FoldPayload = {
      display: '0',
      posture: this.lastPosture,
      width: wpx,
      height: hpx,
      widthClass: widthClassOf(wpx / scale),
      heightClass: heightClassOf(hpx / scale),
    };

    const crease: CreaseRect | null = this.lastCrease;
    if (crease !== null) {
      const active: boolean = this.lastPosture === Posture.HALF_OPEN;
      // 约束①: 非半折叠态报"零宽 + inactive", 折痕位置信息保留。
      const cw: number = active ? crease.width : 0;
      payload.hinge = {
        x: crease.x,
        y: crease.y,
        width: cw,
        height: crease.height,
        orientation: orientationOf(crease),
      };
      payload.regions = [{
        kind: 'division',
        x: crease.x,
        y: crease.y,
        width: cw,
        height: crease.height,
        active: active,
      }];
    }

    const json: string = JSON.stringify(payload);
    if (json === this.lastJson) {
      return; // 值没变就不过桥: windowSizeChange 在拖动窗口时非常密
    }
    this.lastJson = json;

    try {
      nativeGox.setDisplayFold(json);
    } catch (e) {
      hilog.error(DOMAIN, TAG, 'GoxDisplayFold: setDisplayFold 调用失败 (libgox.so 没加载?)');
    }
  }
}

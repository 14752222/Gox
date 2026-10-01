/**
 * Gox 在鸿蒙上的 Ability 入口。
 *
 * ## 职责
 *
 *  - 起**折叠屏上报** (HF2, 见 native/GoxDisplayFold.ets);
 *  - 起**安全区上报** (与 Android 的 nativeSetInsets / iOS 的同类入口对称);
 *  - 喂**应用前后台状态** (内核的 gx/app 模块据此发 onAppStateChange);
 *  - 接**返回键**并同步问脚本要不要退出 (与 Android 的语义一致)。
 *
 * ## 与 Android 的对照 (同名同义, 便于两端对着看)
 *
 *   Android (MainActivity)            鸿蒙 (本文件)
 *   onConfigurationChanged            windowStage + onAreaChange
 *   WindowInsetsController            win.getWindowAvoidArea
 *   Choreographer 每帧                GoxSession 里的 setInterval (见 GoxBridge)
 *   onBackPressed                     onBackPress
 *
 * ## 一条纪律
 *
 * 本文件**只转发事件**, 不执行任何脚本逻辑。内核的 Report* 入口会**同步**跑
 * 脚本侧的订阅回调 (`onViewportChange` 等), 而这里是 ArkTS 的 UI 线程 ——
 * Go 侧已经用 `gfx.Post` 把真正的执行投回 GUI 线程了 (见 gfx/harmony/libgox),
 * 所以这里"发完就走"是安全的, 也**必须**如此。
 */
import { AbilityConstant, UIAbility, Want } from '@kit.AbilityKit';
import { window } from '@kit.ArkUI';
import { hilog } from '@kit.PerformanceAnalysisKit';
import nativeGox from 'libgox.so';
import { GoxDisplayFold } from '../native/GoxDisplayFold';

const TAG: string = 'Gox';
const DOMAIN: number = 0x0000;

export default class EntryAbility extends UIAbility {
  private fold: GoxDisplayFold | null = null;
  private lastInsets: string = '';

  onCreate(want: Want, launchParam: AbilityConstant.LaunchParam): void {
    hilog.info(DOMAIN, TAG, 'EntryAbility.onCreate');
  }

  onWindowStageCreate(windowStage: window.WindowStage): void {
    // HF2: 折叠屏姿态与折痕上报。**必须在 loadContent 之前起** —— 首次上报
    // 要带全尺寸 (内核的"首次替换整张显示器表"语义, 见 GoxDisplayFold 的约束②),
    // 落在页面首帧之前可以避免"先按直板排一次版再跳到双栏"。
    this.fold = new GoxDisplayFold(this.context);
    this.fold.start();

    this.reportInsets();

    windowStage.loadContent('pages/Index', (err) => {
      if (err.code) {
        hilog.error(DOMAIN, TAG, 'loadContent 失败: %{public}s', JSON.stringify(err));
        return;
      }
      hilog.info(DOMAIN, TAG, 'loadContent 完成');
    });
  }

  onWindowStageDestroy(): void {
    hilog.info(DOMAIN, TAG, 'EntryAbility.onWindowStageDestroy');
  }

  onForeground(): void {
    nativeGox.reportAppState('foreground');
  }

  onBackground(): void {
    nativeGox.reportAppState('background');
  }

  onDestroy(): void {
    if (this.fold !== null) {
      this.fold.stop();
      this.fold = null;
    }
    hilog.info(DOMAIN, TAG, 'EntryAbility.onDestroy');
  }

  /**
   * 返回键。返回 true = "脚本已经处理, 宿主不要退出"。
   *
   * 这是**唯一同步等答复**的内核入口 (内部 500ms 超时, 超时按未处理返回)。
   * 与 Android 的 nativeReportBackPress 同语义 —— 宁可退出, 也不能把 UI 线程
   * 吊死 (鸿蒙上表现为整窗无响应, 比 Android 的 ANR 更难查)。
   */
  onBackPress(): boolean {
    try {
      return nativeGox.reportBackPress();
    } catch (e) {
      return false;
    }
  }

  /**
   * 把系统避让区分辨成内核的安全区并上报。
   *
   * 单位是**像素** —— 内核的 Viewport.Insets 一律按像素, 换算成 vp 是渲染侧的事
   * (gfx 用 Display.Scale)。
   */
  private reportInsets(): void {
    window.getLastWindow(this.context).then((win: window.Window) => {
      try {
        const avoid: window.AvoidArea = win.getWindowAvoidArea(window.AvoidAreaType.TYPE_SYSTEM);
        const top: number = Math.round(avoid.topRect.height);
        const bottom: number = Math.round(avoid.bottomRect.height);
        const left: number = Math.round(avoid.leftRect.width);
        const right: number = Math.round(avoid.rightRect.width);
        const key: string = `${top},${right},${bottom},${left}`;
        if (key === this.lastInsets) {
          return; // 与折叠上报同理: 值没变就不过桥
        }
        this.lastInsets = key;
        nativeGox.setInsets(top, right, bottom, left);
        hilog.info(DOMAIN, TAG, 'insets: top=%{public}d right=%{public}d bottom=%{public}d left=%{public}d',
          top, right, bottom, left);
      } catch (e) {
        hilog.warn(DOMAIN, TAG, '取避让区失败 (忽略, 按无安全区处理)');
      }
    }).catch((e: Error) => {
      hilog.error(DOMAIN, TAG, 'reportInsets: getLastWindow 失败: %{public}s', e.message);
    });
  }
}

/**
 * Gox 的主页面: 承载渲染输出 + 转发输入。
 *
 * ## 页面做了什么 (以及**不**做什么)
 *
 * 做: 分配帧缓冲 (交给 GoxSession)、把 PixelMap 显示出来、把触摸/尺寸变化转发
 *     给内核、跑资产里的 app.js。
 *
 * **不做**: 任何界面结构。Gox 的界面全在脚本里 (gfx 的 JSX/组件), 宿主页面只是
 * 一块画布。这与 Android 的 MainActivity 完全一致 —— 两端宿主薄到可以互相对照。
 *
 * ## 输入坐标的单位坑 (实测踩过)
 *
 * ArkUI 的 `TouchEvent.touches[i].x/y` 与 `Area.width/height` 都是 **vp**,
 * 而内核的 Surface 一律按**像素** (gfx/mobile.Touch 的参数是像素)。漏了
 * `vp2px()` 的话, 在高 dpi 设备上表现为"点左边却响应右边" —— 偏移量与 dpi 成正比,
 * 很容易被误判成布局问题。
 */
import { hilog } from '@kit.PerformanceAnalysisKit';
import { util } from '@kit.ArkTS';
import { display } from '@kit.ArkUI';
import { image } from '@kit.ImageKit';
import { GoxSession, TouchAction } from '../native/GoxBridge';

const TAG: string = 'Gox';
const DOMAIN: number = 0x0000;

@Entry
@Component
struct Index {
  @State pixmap: image.PixelMap | undefined = undefined;
  @State status: string = 'Gox 启动中…';
  private session: GoxSession | undefined = undefined;

  aboutToAppear(): void {
    this.boot();
  }

  aboutToDisappear(): void {
    if (this.session !== undefined) {
      this.session.destroy();
      this.session = undefined;
    }
  }

  build() {
    Stack() {
      if (this.pixmap !== undefined) {
        Image(this.pixmap)
          .width('100%')
          .height('100%')
          // 像素级一一对应: Gox 自己排版, 宿主不要插值也不要裁剪。
          .objectFit(ImageFit.Fill)
          .interpolation(ImageInterpolation.None)
      } else {
        Text(this.status)
          .fontSize(16)
          .fontColor('#888888')
      }
    }
    .width('100%')
    .height('100%')
    .backgroundColor('#000000')
    .onAreaChange((oldArea: Area, newArea: Area) => {
      // vp → px (见文件头的单位坑)
      const wpx: number = Math.round(this.toPx(Number(newArea.width)));
      const hpx: number = Math.round(this.toPx(Number(newArea.height)));
      const density: number = this.density();
      if (this.session !== undefined) {
        this.session.resize(wpx, hpx, density);
      }
    })
    .onTouch((event: TouchEvent) => {
      const s = this.session;
      if (s === undefined) {
        return;
      }
      const t: TouchObject | undefined = event.touches[0];
      if (t === undefined) {
        return;
      }
      s.touch(this.actionOf(event.type), Math.round(this.toPx(t.x)), Math.round(this.toPx(t.y)));
    })
  }

  private actionOf(type: TouchType): number {
    switch (type) {
      case TouchType.Down:
        return TouchAction.DOWN;
      case TouchType.Move:
        return TouchAction.MOVE;
      case TouchType.Up:
        return TouchAction.UP;
      default:
        // 多指等一律当 Cancel —— "丢掉这次手势"里最安全的一档 (不会留下按压态),
        // 与 Android 侧 androidAction 的兜底一致。
        return TouchAction.CANCEL;
    }
  }

  private density(): number {
    try {
      const d: display.Display = display.getDefaultDisplaySync();
      return d.densityPixels > 0 ? d.densityPixels : 1;
    } catch (e) {
      return 1;
    }
  }

  /** vp → px。全局 vp2px 在 API 18 弃用, 官方替代是 UIContext#vp2px。 */
  private toPx(v: number): number {
    return this.getUIContext().vp2px(v);
  }

  /** 读资产里的脚本, 建会话, 开跑。 */
  private boot(): void {
    // getHostContext 的签名就是 `Context | undefined`: 必须显式判, 否则
    // 症状是"读 assets/app.js 失败: Cannot read property resourceManager"。
    const ctx = this.getUIContext().getHostContext();
    if (ctx === undefined) {
      this.status = '拿不到宿主 Context, 无法读取脚本';
      hilog.error(DOMAIN, TAG, 'getHostContext() 返回 undefined');
      return;
    }
    ctx.resourceManager.getRawFileContent('app.js').then((data: Uint8Array) => {
      const script: string = util.TextDecoder.create('utf-8').decodeToString(data);
      const density: number = this.density();
      // 首帧尺寸先按整屏算, 真正的窗口尺寸在 onAreaChange 里立刻修正
      // (那一次会走 resize → 重新分配缓冲并重新绑定)。
      let wpx: number = Math.round(this.toPx(360));
      let hpx: number = Math.round(this.toPx(780));
      try {
        const d: display.Display = display.getDefaultDisplaySync();
        if (d.width > 0 && d.height > 0) {
          wpx = d.width;
          hpx = d.height;
        }
      } catch (e) {
        hilog.warn(DOMAIN, TAG, '取默认显示器尺寸失败, 用兜底值启动');
      }

      const s = new GoxSession(
        () => {
          // 新一帧就绪 → 换 @State 触发重绘
          this.pixmap = s.pixelMap();
        },
        (code: number, error: string) => {
          this.status = error.length > 0 ? `脚本结束 (${code}): ${error}` : `脚本结束 (${code})`;
          hilog.info(DOMAIN, TAG, '%{public}s', this.status);
        }
      );
      this.session = s;

      const rc: number = s.start(wpx, hpx, density, script, 'harmony-app');
      if (rc !== 0) {
        this.status = `libgox 启动失败 (${rc})`;
        hilog.error(DOMAIN, TAG, 'libgox 启动失败: %{public}d', rc);
      }
    }).catch((e: Error) => {
      this.status = `读 assets/app.js 失败: ${e.message}`;
      hilog.error(DOMAIN, TAG, '读 assets/app.js 失败: %{public}s', e.message);
    });
  }
}

/**
 * 鸿蒙的电池 / 网络上报器 (gx/device 的纯上报型一半)。
 *
 * 与 Android 的"广播 + NetworkCallback"、iOS 的"UIDevice + NWPathMonitor"同构:
 * 系统状态变化 → 拼成内核约定的 JSON → nativeGox.reportBattery/reportNetwork。
 * JSON 键名逐字对齐 gfx/harmony/libgox/main.go 的 dispatchReportBattery/Network
 * (那两张 jsonStr/jsonBool 表是唯一事实来源)。
 *
 * ## 实现取舍
 *
 *  - 电池: 鸿蒙没有公开的"电量变化"系统广播给三方应用, 采用**轮询**
 *    (30s 一档, 变化才过桥)。电量不是高频量, 轮询的延迟在业务上不可感知。
 *  - 网络: connection.on('netAvailable'/'netLost'/'netCapabilitiesChange')
 *    是真回调; metered 由 NET_CAPABILITY_NOT_METERED 反推。
 *  - SSID: 三方应用拿不到 (需受限权限), 恒空 —— 与内核"拿不到给空"的约定一致。
 */
import { batteryInfo, power } from '@kit.BasicServicesKit';
import { connection } from '@kit.NetworkKit';
import { hilog } from '@kit.PerformanceAnalysisKit';
import nativeGox from 'libgox.so';

const TAG: string = 'Gox';
const DOMAIN: number = 0x0000;

/** 电池轮询间隔 (ms)。 */
const BATTERY_POLL_MS: number = 30000;

export class GoxDeviceReport {
  private batteryTimer: number = -1;
  private lastBattery: string = '';
  private lastNetwork: string = '';
  private started: boolean = false;

  /** 起两类上报。幂等 (重复调用安全)。 */
  start(): void {
    if (this.started) {
      return;
    }
    this.started = true;
    this.reportBatteryNow();
    this.batteryTimer = setInterval(() => {
      this.reportBatteryNow();
    }, BATTERY_POLL_MS);
    this.watchNetwork();
  }

  stop(): void {
    if (!this.started) {
      return;
    }
    this.started = false;
    if (this.batteryTimer !== -1) {
      clearInterval(this.batteryTimer);
      this.batteryTimer = -1;
    }
  }

  /** 立即取一次电池快照并上报 (值没变不过桥, 与 insets 同取舍)。 */
  private reportBatteryNow(): void {
    try {
      // TODO(真机): chargingType 细分 (usb/ac/wireless) 鸿蒙公开 API 不可区分,
      // 与 iOS 一致恒 unknown; 温度电池 batteryInfo 不提供, 恒 -1 (未知)。
      const charging: boolean =
        batteryInfo.chargingStatus === batteryInfo.BatteryChargeState.ENABLE ||
        batteryInfo.chargingStatus === batteryInfo.BatteryChargeState.FULL;
      const json: string = JSON.stringify({
        supported: true,
        level: batteryInfo.batterySOC >= 0 ? batteryInfo.batterySOC / 100 : -1,
        charging: charging,
        chargingType: 'unknown',
        temperature: -1,
        lowPowerMode: power.getPowerMode() === power.DevicePowerMode.MODE_POWER_SAVE,
      });
      if (json === this.lastBattery) {
        return;
      }
      this.lastBattery = json;
      nativeGox.reportBattery(json);
    } catch (e) {
      hilog.warn(DOMAIN, TAG, '电池快照失败 (本轮跳过)');
    }
  }

  /** 网络: 起监听 + 立刻上报一次当前快照。 */
  private watchNetwork(): void {
    try {
      const spec: connection.NetSpecifier = {
        netCapabilities: { bearerTypes: [] },
      };
      const handle: connection.NetConnection = connection.createNetConnection(spec);
      handle.register(() => {
        this.reportNetworkNow();
      });
      handle.on('netAvailable', () => {
        this.reportNetworkNow();
      });
      handle.on('netLost', () => {
        this.reportNetworkNow();
      });
      handle.on('netCapabilitiesChange', () => {
        this.reportNetworkNow();
      });
    } catch (e) {
      hilog.warn(DOMAIN, TAG, '网络监听注册失败 (只报当前快照)');
    }
    this.reportNetworkNow();
  }

  /** 当前默认网络 → 内核 JSON。 */
  private reportNetworkNow(): void {
    try {
      const handle: connection.NetHandle = connection.getDefaultNetSync();
      if (!handle || handle.netId === 0) {
        this.pushNetwork(false, 'none', false, '');
        return;
      }
      const caps: connection.NetCapabilities = connection.getNetCapabilitiesSync(handle);
      const types: connection.NetBearType[] = caps.bearerTypes;
      let type: string = 'other';
      if (types.includes(connection.NetBearType.BEARER_WIFI)) {
        type = 'wifi';
      } else if (types.includes(connection.NetBearType.BEARER_CELLULAR)) {
        type = 'cellular';
      } else if (types.includes(connection.NetBearType.BEARER_ETHERNET)) {
        type = 'ethernet';
      } else if (types.includes(connection.NetBearType.BEARER_BLUETOOTH)) {
        type = 'bluetooth';
      } else if (types.includes(connection.NetBearType.BEARER_VPN)) {
        type = 'vpn';
      }
      const metered: boolean =
        !caps.networkCap.includes(connection.NetCap.NET_CAPABILITY_NOT_METERED);
      // TODO(真机): 蜂窝代际 (4g/5g) 可由 telephony.radio 细分, v1.0 恒空。
      this.pushNetwork(true, type, metered, '');
    } catch (e) {
      hilog.warn(DOMAIN, TAG, '网络快照失败 (本轮跳过)');
    }
  }

  private pushNetwork(connected: boolean, type: string, metered: boolean,
    generation: string): void {
    const json: string = JSON.stringify({
      connected: connected,
      type: type,
      metered: metered,
      ssid: '',
      strength: -1,
      carrier: '',
      generation: generation,
    });
    if (json === this.lastNetwork) {
      return;
    }
    this.lastNetwork = json;
    nativeGox.reportNetwork(json);
  }
}

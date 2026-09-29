# 自动更新（gx/update v1.1）

> 状态：已落地（2026-09-29）。本文是 gx/update 模块与 `gox update` 命令的设计说明，
> 对应看板「v0.1.0 已知问题修复」中 *gx/update v1 增强* 一条。

Gox 桌面应用的自动更新采用**清单驱动 + 整包替换**：应用作者托管一份 JSON 清单，
运行时比较版本号、流式下载新二进制、三段换名就位。不做差分更新、不做进程热替换 ——
v1 的边界刻意向"简单可靠"倾斜。

## 快速开始（JS 侧）

```js
import { checkForUpdate, downloadAndInstall, currentVersion, cleanupBackup } from "gx/update";

// 应用启动时调一次: 清理上次更新残留的 .bak
cleanupBackup();

const rel = await checkForUpdate("https://my.host/app-manifest.json", {
    currentVersion: "0.7.0",   // 缺省读工作目录 gox.json 的 version, 再退 "0.0.0"
    preRelease: false,         // true 时 pre 通道也参与选版本
});
if (rel) console.log(`发现新版本 ${rel.version}: ${rel.notes}`);

const res = await downloadAndInstall("https://my.host/app-manifest.json", {
    onProgress: (done, total) => console.log(`${done}/${total}`),
});
if (res) console.log(`已就位 ${res.version}, 重启生效`);
```

## 快速开始（CLI 侧）

`gox update` 让 gox 自身走同一条更新链：

```bash
gox update --manifest https://gox.example/manifest.json   # 检查并安装
gox update --check --pre                                   # 只检查, pre 通道
gox update --cleanup                                       # 清理 .bak
# 也可以用环境变量: GOX_UPDATE_MANIFEST=https://... gox update
```

## 清单格式

清单是静态 JSON, 托管在任意能提供 GET 的地方（对象存储 / 静态站 / GitHub Release 页均可）：

```json
{
  "releases": [
    {
      "version": "0.8.0",
      "channel": "stable",
      "notes": "新组件与更新通道",
      "files": {
        "windows/amd64": { "url": "https://my.host/gox-0.8.0-win64.exe", "sha256": "<hex>", "size": 15728640 },
        "linux/amd64":   { "url": "https://my.host/gox-0.8.0-linux",     "sha256": "<hex>", "size": 15138816 },
        "darwin/arm64":  { "url": "https://my.host/gox-0.8.0-macos-arm64","sha256": "<hex>", "size": 16777216 }
      }
    },
    {
      "version": "0.9.0-pre.1",
      "channel": "pre",
      "notes": "灰度尝鲜",
      "files": { "…同上…": {} }
    }
  ]
}
```

约定：

| 字段 | 必填 | 说明 |
|------|------|------|
| `releases[].version` | 是 | semver（`v` 前缀宽容; 不合法的条目跳过, 不拖垮整个清单） |
| `releases[].channel` | 否 | `stable`（缺省）/ `pre`; 未知通道不参与选版本 |
| `releases[].notes` | 否 | 发布说明, 原样透出给 JS |
| `releases[].files` | 是 | 键为 `goos/goarch`（与 Go 交叉编译目标一致） |

- **通道规则**：stable 恒参与; `pre` 仅在 `preRelease: true` 时参与, 同版本号时 stable 压过 pre（转正语义）。
- **sha256 / size**：清单给了就强制校验；sha256 不匹配会先全量重下一次再判失败。
- 清单本体超过 8MB 拒收（防"把二进制填进清单"的呆事拖垮内存）。

## 下载与校验（v1.1 增强）

v1 草案是"整包读进内存再写盘"，包体多大内存吃多少，也没有进度可言。v1.1 全部改为流式：

1. **流式落盘**：下载直接写 `<dest>.part`（64KB 缓冲），内存占用 O(缓冲区)。
2. **断点续传**：`.part` 已存在时带 `Range: bytes=N-` 续传；服务端返回 200（不支持 Range）就退回全量覆盖。
3. **sha256 边下边算不可能（续传的偏移无法恢复哈希状态），改为下载完成后整文件校验**；续传拼接错误（半截文件来自旧版本等）自动清 `.part` 全量重下一次。
4. **进度回调**：`ProgressFunc(done, total)` 每块数据回调；桥接 JS 时节流到 64ms 一次（≈60fps 单帧一次），经事件循环主线程派发（0ms 定时器桥，与 http 模块同构），goroutine 不直接触碰 VM。

## 就位策略：三段换名与中断自愈

Windows 上运行中的 exe 不能被覆盖写入，但**可以改名** —— 换名全程用 rename：

```
1) app      → app.bak    （备份当前版本）
2) app.new  → app        （新版本就位）
3) 失败自愈：app.bak → app （回到更新前, 应用照常可跑）
```

中断自愈的边界（诚实声明）：

| 中断时机 | 磁盘状态 | 自愈路径 |
|----------|----------|----------|
| 下载中断 | 只有 `.part` 残留 | 下次下载续传/覆盖，无需处理 |
| 阶段 2 失败 | `.bak` 已自动改回 | 无残留 |
| **阶段 1 之后、阶段 2 之前断电** | 只剩 `app.bak`，`app` 不在 | 启动时 `Repair()` 把 `.bak` 改回 |
| 更新成功后 | `app`（新）+ `app.bak`（旧） | 启动时 `CleanupBackup()` 清垃圾 |

应用主入口建议一开机就调 `Repair()` + `CleanupBackup()`（幂等，健康目录上是 no-op）；
`gox update --cleanup` 是同一件事的手动版。**应用必须重启才运行新版本** ——
当前进程映像不会被热替换，这是刻意不做的（进程内自替换 = 自杀式复杂度）。

## 版本号解析顺序

`currentVersion`（比较基准）按三级解析：

1. 调用方显式给（`options.currentVersion` / CLI 用 `gox version` 常量）；
2. 工作目录 `gox.json` 的 `version`；
3. `"0.0.0"`（永远要更新 —— 全新安装自更新的常见形态）。

## 已知限制（v1.2 候选）

- 差分更新（bsdiff/courgette）不做，全量整包 —— 对 15MB 量级的桌面二进制，带宽成本可接受；
- 无回滚 UI：`.bak` 就在旁边，回滚 = 两条 rename，但 v1 不做界面化；
- macOS 未签名/未公证的更新包会撞 Gatekeeper —— 签名流水线见 docs/desktop-distribution.md；
- 移动端（Android/iOS）更新走应用商店，本模块不覆盖。

## 实现地图

| 位置 | 内容 |
|------|------|
| `update/update.go` | 清单/选版本/流式下载/三段换名/自愈, 纯 Go 无依赖 |
| `update/update_test.go` | semver 通道、续传、坏前缀回退、**中断模拟**（对应看板 rTkaiU 的验证补课） |
| `stdlib/update_module.go` | `gx/update` JS 门面（Promise + 进度桥接 + 忙碌互斥） |
| `vm/update_module_test.go` | JS 侧集成测试（聚合入口导出、check 版本判定、rejection 恢复、gox.json 版本解析） |
| `cmd/gox/cmd_update.go` | `gox update` CLI |
| `scripts/check-registries.py` | 内置模块三处同步闸门自动覆盖 gx/update |

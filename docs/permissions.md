# 脚本权限模型（v1）

> Deno 风格：默认最小权限，高危能力要用 `--allow-*` 标志显式放行，
> 脚本触达未授权能力时抛出指明所需标志的错误。

## 默认策略

| 运行方式 | 默认权限 |
|---|---|
| `gox script.js`（含 `gox dev`） | **最小权限**：四类高危能力全部拒绝 |
| `gox`（REPL） | 全部允许（交互式会话，人已坐在终端前） |
| 作为 Go 库嵌入（`stdlib.SetupGlobals` / 测试） | 全部允许——宿主进程自己是信任边界，需要收紧时由宿主在启动时调 `permissions.Set(...)` |

授权只发生在**进程启动时**（CLI 解析标志），运行期不弹窗、不交互式升级：
脚本能做什么在启动那一刻就是定死的。

## 权限标志

| 标志 | 覆盖能力 | 检查点 |
|---|---|---|
| `--allow-fs-write` | fs 写入/追加/建删目录/删文件/改名/复制 | `writeFileSync` `writeFile` `appendFileSync` `appendFile` `mkdirSync` `mkdir` `rmdirSync` `rmSync` `unlinkSync` `unlink` `renameSync` `copyFileSync`（12 个，`stdlib/fs.go` 的 `fsWriteOps` 名单统一包装） |
| `--allow-process-exit` | 脚本单方面终结宿主进程 | `process.exit()` |
| `--allow-net-listen` | 打开监听端口 | `server.listen()`（`http.createServer` 本身不拦） |
| `--allow-geo-media` | 定位与媒体采集 | `gx/geo` 的 `getLocation`/`watchLocation`；`gx/media` 的 `takePhoto`/`chooseImage`/`chooseOneImage`/`chooseVideo` |
| `--allow-all`（`-A`） | 以上全部 | — |

示例：

```sh
gox app.js                          # 最小权限
gox --allow-fs-write backup.js      # 只放行文件写
gox -A server.js                    # 全部放行
```

## 未授权时的报错

检查点直接返回错误值，VM 抛给脚本，文案点名该加哪个标志：

```
vm error: Error: permission denied: 文件系统写入 需要 --allow-fs-write（--allow-all 可放行全部）
```

**只读能力不设权限**：`fs` 读、`path`、http 客户端（`get`/`request`/`fetch`）、
`process.argv/env/cwd` 等默认放行。v1 只圈四类「不可逆 / 影响宿主 / 影响外部」
的能力；更细粒度（按路径、按域名）留给 v2。

## 实现要点

- 权限状态在 `permissions` 包（`permissions/permissions.go`），一个 atomic
  位掩码；不依赖 stdlib/gfx，反向被它们依赖，避免环。
- 检查点散布在能力入口处：`stdlib/fs.go`（统一包装）、`stdlib/process.go`、
  `stdlib/http.go`、`gfx/native_geo.go`、`gfx/native_media.go`。
- 权限标志拼错会**报错退出**而不是静默忽略——安全相关的 flag 打错字必须被发现。
- CLI 解析在 `cmd/gox/main.go` 的最前面，标志剥离后再分发子命令；`gox dev`
  的子进程继承原始 `os.Args`，权限随之传递。

## v1 边界

- 不覆盖打包产物（`gox build` 出的应用）：应用清单权限走 `gox sync` 那条线
  （平台原生权限声明），与本模型是两层。
- fs 读不圈：只读窥探的防护靠宿主层沙箱，不做进 v1。

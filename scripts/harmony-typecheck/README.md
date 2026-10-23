# 鸿蒙桌面类型检查用的头文件或替身

给 [`../check-harmony.sh`](../check-harmony.sh) 提供 C 头文件，让**没有 DevEco
SDK 的机器**（CI runner、任意 Linux）也能把 `gfx/harmony` 下的 Go 代码完整编译
一遍。

目录内容分两类，别混为一谈：

| 路径 | 性质 | 来源 |
| --- | --- | --- |
| `include/node_api.h` | **真头文件**，原样拷贝 | Node.js v22.13.1（`include/node/`） |
| `include/node_api_types.h` | 真头文件，原样拷贝 | 同上 |
| `include/js_native_api.h` | 真头文件，原样拷贝 | 同上 |
| `include/js_native_api_types.h` | 真头文件，原样拷贝 | 同上 |
| `include/hilog/log.h` | **手写替身**，只声明用到的符号 | 本仓库 |

## 为什么可以拿 Node 的头文件顶替 OHOS 的

鸿蒙的 Native API（NAPI）实现的就是 **Node-API 标准**：`gfx/harmony` 用到的
23 个 `napi_*` 函数全部是标准 Node-API 符号，与 OHOS 无关。这四个头文件自身
构成闭集（只依赖 `stddef.h` / `stdbool.h`）：

```
node_api.h → js_native_api.h → js_native_api_types.h
           → node_api_types.h
```

所以拷进来就能用，不需要整棵 Node 的 `include/` 树（那个还拖着 v8/uv/openssl，
几 MB，没必要）。

**许可**：这四个文件来自 Node.js，MIT License，Copyright Node.js contributors。
完整许可文本见 <https://github.com/nodejs/node/blob/main/LICENSE>。此处的拷贝
仅用于 CI 编译期类型检查，**不参与任何产物构建**，也不随发布包分发。

## `hilog/log.h` 为什么是替身

`hilog` 是鸿蒙专有的日志库，桌面上没有。替身只声明 `gfx/harmony` 实际用到的
符号：`LogType`、`LogLevel`、`OH_LOG_Print`、`LOG_DOMAIN`、`LOG_TAG`、
`OH_LOG_INFO`。

**不提供实现**，也**不做任何实际输出** —— 本检查只到编译期，链接期由
`check-harmony.sh` 造的空壳 `libhilog_ndk.z.so` 顶上（见脚本内注释：那层没验）。

## 维护约定

- **不要手改这四个 Node 头文件**。它们必须是上游原样，改了就不再是"用真签名
  校验"。要升级版本就整份重新拷贝，并在本文件与上表里的版本号一起更新。
- **不要往 `hilog/log.h` 里加没被用到的符号**。替身越窄越好：多一个符号，就少
  一分"鸿蒙侧真的只依赖这些"的保证。
- 这套头文件的价值全在于**签名是真的**。任何"为了让它编译过"而放宽签名的改动，
  都会把这个检查变成一条永远绿的假闸门 —— 那比没有更糟。

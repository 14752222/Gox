# gox-npm（v0）—— `gox install` / `gox add`

> gox-npm 是 Gox 自带的**极简包安装器**：从 npm registry 拉**纯 JS 包**装进
> 工程的 `node_modules/`，让 Gox 的 ESM 运行时能直接 `import`。
> 兼容口径见 [`docs/npm-compat.md`](./npm-compat.md)。

---

## 1. 它是什么 / 不是什么

| | 说明 |
|---|---|
| **是** | 一个把纯 JS 依赖装到 `node_modules/` 的安装器（类似 `npm install` 的最小可用子集） |
| **不是** | Node 本身；不执行 `postinstall`、不跑生命周期脚本、不做传递依赖树 |
| **不是** | `npm/` 子模块那个包（那个包叫 `@goxjs/goxjs`，只分发 Gox 二进制，见 §5） |

**与 `npm/` 子模块的分工**（两者都走 npm registry，但角色完全不同）：

```
npm i -g @goxjs/goxjs      # 装「gox / goxjs 这个命令」——二进制分发器
gox install                # 装「你工程的 JS 依赖」——包安装器，落到 ./node_modules
```

- `@goxjs/goxjs`（仓库 `npm/` 子模块）= **平台二进制分发**。`bin/gox.js` 只做
  一件事：探测平台后 `spawn` 对应架构的 Gox 二进制。
- `gox install`（本命令，`cmd/gox/cmd_npm.go`）= **依赖包安装**。给工程装纯 JS
  依赖，供 VM 的 `import` 用。
- 二者**互不替代**：一个给你运行 Gox 的能力，一个给你的脚本提供库。

---

## 2. 用法

### 装 `package.json` 里的依赖

```bash
gox install
```

读取当前目录 `package.json` 的 `dependencies`，逐个解析、下载、解包到
`node_modules/`，并写 `gox-lock.json`。

> `gox install <pkg>` 等价于 `gox add <pkg>`。

### 加一个依赖

```bash
gox add lodash@^4
gox add @scope/pkg
gox add lodash@4.17.21
```

- 无 `@range` 时按 npm 默认口径写入 `^<选中版本>`。
- 会新建 / 更新 `package.json` 的 `dependencies`，并把结果并入 `gox-lock.json`。

### 装完直接用

```js
// app.js
import { chunk } from "lodash";   // resolves to ./node_modules/lodash/...
console.log(chunk([1, 2, 3, 4], 2));
```

```bash
gox app.js
```

---

## 3. registry / 缓存 / 锁文件

### registry 覆盖

默认 registry：`https://registry.npmjs.org`。内网镜像或测试可覆盖：

```bash
export GOX_NPM_REGISTRY=https://registry.npmmirror.com
gox install
```

客户端会在错误里提示这一覆盖方式（连不上 registry 时尤其有用）。

### 包缓存（tarball 级）

```
~/.gox/pkg-cache/<name>/<version>.tgz
```

- **命中缓存不重复下载**：第二次装同一个 `name@version` 直接复用 `.tgz`。
- 缓存是 **tarball 级**，与解包后的 `node_modules/` 分离：
  `node_modules/` 是工程产物（可删可重建），缓存是全局的下载记录。
- 缓存命中后**仍会校验 integrity**（缓存也可能损坏/被改）。
- 测试/隔离场景可覆盖缓存目录：`GOX_PKG_CACHE=/tmp/xxx`（正式使用无需设置）。

### 完整性校验

若 packument 的 `dist.integrity` 带 `sha512-` 前缀，**必须校验通过**，否则报错
中止，且**不解包到 `node_modules/`**（不留下半成品）。其它算法（如 sha1）v1 不校验。

### `gox-lock.json`

```json
{
  "lockfileVersion": 1,
  "packages": {
    "lodash": {
      "version": "4.17.21",
      "resolved": "https://registry.npmjs.org/lodash/-/lodash-4.17.21.tgz",
      "integrity": "sha512-..."
    }
  }
}
```

- 记录每个直接依赖选中的精确版本、下载 URL、integrity。
- `gox add` 会与已存在的 lock 合并，不丢之前装的依赖。

---

## 4. semver 口径（简化，写死）

| range | 含义 |
|---|---|
| `""` / `*` / `latest` | `dist-tags.latest`（缺失则取最高版本） |
| `1.2.3` | 精确匹配 |
| `^1.2.3` | `>=1.2.3 <下一个 major`；`0.x` 按 npm 规则收紧（`^0.2.3` → `<0.3.0`） |
| `~1.2.3` | `>=1.2.3 <1.(minor+1).0` |
| `>=1.0.0 <2.0.0` | 空格分隔的比较器 AND（`>= > <= < =`） |

- **不支持**：`||`、`x` 通配、hyphen range、预发布参与普通 range。这类 range
  若解析不出结果会**报错**，不会静默装一个错版本。
- 版本比较遵循 semver：预发布版 `<` 同号正式版；普通 range 不匹配预发布版。

---

## 5. 能力边界（v1）

- **只装直接依赖**：不解析被依赖者的 dependencies（无传递依赖树）。v0 面向
  "依赖一个纯 JS 库、且该库自身无运行时依赖（或已自带打包产物）"的场景。
- **只看 `dependencies`**：`devDependencies` / `peerDependencies` / `optionalDependencies`
  暂不处理。
- **不做 CommonJS**：装下来的包若用 `require`，运行期报错（见 `npm-compat.md` 红线一）。
- **不跑生命周期脚本**：无 `preinstall/postinstall`，避免任意代码执行。
- **无 workspace / no lockfile-based 冻结安装**：`gox install` 每次按
  `package.json` 的 range 重新选版本；锁定精确版本请以 `gox-lock.json` 为准手动核对。

---

## 6. 排错

- **找不到包 / 版本**：报错会带上 registry 地址；内网请设 `GOX_NPM_REGISTRY`。
- **`import` 不到包**：确认 `gox install` 已把包落到 `node_modules/`，且包的入口
  字段在支持矩阵内（`npm-compat.md` 的「node_modules 解析支持矩阵」）。
- **运行期 `require is not defined`**：装的是 CJS 包。换 ESM 形态的包（带
  `module` 字段或 `exports` 的 `import` 条件）。
- **`integrity 校验失败`**：tarball 内容与 registry 声明不符；清掉
  `~/.gox/pkg-cache/<name>/<version>.tgz` 重试，仍失败说明 registry 内容异常。

---

## 7. 实现位置

| 关注点 | 位置 |
|---|---|
| 命令入口（`install`/`add` 注册） | `cmd/gox/main.go` |
| 安装器实现 | `cmd/gox/cmd_npm.go` |
| 测试（假 registry，不联网） | `cmd/gox/cmd_npm_test.go` |
| `node_modules` 解析 | `vm/vm.go` 的 `resolveNodeModule` 等 |
| 核心模块内置化 | `stdlib/{fs,path,http,process}.go` |
| 兼容面总口径 | `docs/npm-compat.md` |

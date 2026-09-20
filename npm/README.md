# @goxjs/goxjs

> Gox JavaScript Runtime — 用 Go 从零实现的 JavaScript 运行时：词法分析 → 语法分析 → 字节码编译 → 栈式虚拟机执行。单二进制、零外部依赖。

本包把 Gox 的各平台二进制分发到 npm，安装后直接获得 `goxjs` 命令。

## 安装

```bash
npm i -g @goxjs/goxjs
```

## 使用

**REPL：**

```bash
goxjs
```

```
Gox REPL (ES6 subset, no var)
Type :exit to quit, :help for help

> let x = 10
> [1, 2, 3].map(v => v * 2)
  [2, 4, 6]
```

**运行脚本：**

```bash
goxjs app.js
```

**免安装直接跑：**

```bash
npx goxjs app.js
```

支持 ES6+ 语言子集（`let`/`const`、class、`async`/`await`、解构、模板字符串、ES 模块），内置 `Array` / `Map` / `Set` / `Promise` / `BigInt` / `Temporal` 等标准对象，以及 `fs` / `http` / `fetch` / `path` / `process` 等 Node 风格宿主 API。

完整文档与语言示例见 [GitHub 仓库](https://github.com/14752222/Gox)。

## License

[Apache License 2.0](https://github.com/14752222/Gox/blob/main/LICENSE)

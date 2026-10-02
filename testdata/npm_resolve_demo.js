// npm 兼容面演示（M5）—— 直接 `gox testdata/npm_resolve_demo.js` 即可跑。
//
// 本文件只演示「核心模块作为内置模块可 import」这条新能力（无需联网/装包）。
// `node_modules` 裸包解析（`import { chunk } from "lodash"`）需要先 `gox install`，
// 见 docs/npm-compat.md 的「node_modules 解析支持矩阵」与 docs/gox-npm.md。
//
// 说明: 下面这些 foo/bar 都是示意，不在本文件里真的 import（避免未装包时报错）。

import fs from "fs";
import path from "path";
import process from "process";

// 1) 内置模块的默认导入：`import fs from "fs"` 拿到模块命名空间对象
console.log("fs.readFileSync is", typeof fs.readFileSync);
console.log("path.join is", typeof path.join);
console.log("process.platform =", process.platform);

// 2) 全局对象形态与 import 形态是同一批实现，随便用哪种都行
const file = path.join(process.cwd(), "testdata", "npm_resolve_demo.js");
console.log("demo file exists:", fs.existsSync(file));

// 3) 若装了纯 JS 包，VM 的解析顺序是:
//    exports(含 . 子路径) > module > main > browser > index.js，
//    并从当前文件目录逐级向上找 node_modules。示意（不实际执行）:
//
//      import { chunk } from "lodash";        // bare specifier → node_modules
//      import fp from "lodash/fp";            // 子路径
//      import pkg from "@scope/pkg";          // 作用域包
//
//    解析失败时会列出所有尝试过的候选路径。
console.log("npm compat demo done");

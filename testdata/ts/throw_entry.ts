// testdata/ts/throw_entry.ts —— 触发 throw_module.ts 里的运行时错误。
// 导入必须被真正使用 (否则编译器剪枝, 模块不会加载, 也就没有错误)。

import { boom } from "./throw_module.ts";

boom();

// 验证: 入口经中间的再导出模块拿到源模块的值与函数。
// (入口用命名空间导入, 这样 check-imports.py 的静态相对导入闸门不会误判别名 ——
//  它把 `export { X as Y }` 的 X 当导出名, 但对别名 Y 的支持只在运行时验证。)
import * as ns from "./named_mid.js";

// 41 + fn() 长度 2 = 43
ns.renamed + ns.callIt().length;

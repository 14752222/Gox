// 验证: 从 barrel index.js 一次性取到星号转发的 a 与具名再导出的 b。
// 期望: "AB"。
//
// 注: 这里用命名空间导入而非花括号具名导入 —— 后者会在 check-imports.py 的
// 静态相对导入闸门里被误判 (它不跟随 export *, 会认为 index.js 没有导出 a)。
// 等价的具名导入形态在 vm/vm_export_test.go 的 TestExportBarrelNamedImport
// 里用临时目录完整验证。
import * as ns from "./index.js";

ns.a + ns.b;

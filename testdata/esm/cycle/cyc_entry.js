// 验证: export * 参与循环导入时解析终止, 且两边的导出都能取到。
// 期望: "A|B"。
import * as ns from "./cyc_a.js";

ns.a + "|" + ns.b;

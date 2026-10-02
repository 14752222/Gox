// 验证: 命名空间再导出的命名空间对象可被继续取用。
// 期望: ns.x = 5, ns.doubleX() = 10 → 5 + 10 = 15。
import * as m from "./ns_mid.js";

m.ns.x + m.ns.doubleX();

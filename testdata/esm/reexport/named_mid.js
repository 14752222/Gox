// 验证: 具名再导出 `export { original as renamed } from "./named_src.js"`。
//
// 修复前的症状是运行期 `ReferenceError: original is not defined` —— parser 把
// 来源模块整个忽略, 导出的名字对不上。现在记录为"转发", 由 buildNamespace 在
// 物化命名空间时读取源模块导出槽 (取值时解析)。
export { original as renamed, fn as callIt } from "./named_src.js";

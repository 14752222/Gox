// 验证: 具名默认函数导出到**模块模式**下, 被 import 后立即调用不触发 TDZ。
// (入口脚本是全局模式, 走的是另一条修复路径; 这里刻意经由模块加载。)
export default function f() {
  return "FD";
}

// 验证: `export default function named(){}` 的具名默认导出不产生 TDZ 误报。
//
// 规范里 named 只是**模块内局部绑定**, 不是命名导出 (命名导出只有 default)。
// 修复前引擎会把它当成本模块的对外命名导出, 冲掉正确的初始化顺序, 运行时抛
// "Cannot access lexical declaration before initialization"。所以这里在文件内
// 立即调用一次 —— 能调用说明局部绑定已就绪。
export default function named() {
  return "named";
}

named();

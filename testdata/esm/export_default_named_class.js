// 验证: `export default class Named {}` 具名默认类同样只在模块内可见, 且能被
// 立即实例化/访问静态成员 (与具名默认函数的 TDZ 修复同源)。
export default class Named {
  static tag() {
    return "cls";
  }
}

Named.tag();

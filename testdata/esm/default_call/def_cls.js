// 验证: 具名默认类导出到模块模式, 被 import 后可立即实例化。
export default class C {
  constructor() {
    this.v = 9;
  }
}

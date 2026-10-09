// 正向样本：显式豁免且带理由 —— 允许通过。
//
// 豁免必须写理由（`// initpurity:allow 理由：…`）：没有理由的豁免记不住当时
// 为什么放行，会被后人当成「这里可以随便加」。缺理由时工具按违规处理
// （见 bad_exempt_noreason 样本）。
package good_exempt

import "syscall"

func init() {
	_ = syscall.Getpid() // initpurity:allow 理由：样本，演示带理由的豁免应被放行
}

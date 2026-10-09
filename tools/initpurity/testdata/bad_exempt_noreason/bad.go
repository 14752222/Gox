// 负向样本：豁免没写理由 —— 按违规处理。
//
// 空白支票式豁免比没有豁免更危险：后人看到 `// initpurity:allow` 会以为
// 「这里允许平台调用」，于是继续往里加。所以缺理由 = 不放行。
package bad_exempt_noreason

import "syscall"

func init() {
	_ = syscall.Getpid() // initpurity:allow
}

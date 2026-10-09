// 负向样本：init 里调用平台后端包。
//
// 平台包按「导入路径末段」判定（win32 / cocoa / x11 / android / harmony …），
// 新增平台包自动进闸。这里用 win32 做代表。
// 期望：报 1 处，判据里要点出「平台后端包」。
//
// 注意：本目录在 testdata/ 下，Go 工具链不会编译它 —— import 一个 windows-only
// 的包不会让 Linux 上的 go build 失败。
package bad_platform

import "github.com/14752222/Gox/gfx/win32"

func init() {
	win32.EnumAdapters()
}

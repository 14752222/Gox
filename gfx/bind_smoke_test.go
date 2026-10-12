// 本文件是 package gfx_test 的**外部**测试文件，唯一职责：把 gfx/bind 链进
// gfx 的测试二进制。apps_smoke_test.go（package gfx 内部测试）不能 import
// gfx/bind —— bind 依赖 gfx（单向：bind → gfx），内部测试反着 import 会成环。
// 而 apps/settings 依赖的 gx/theme 模块注册点在 gfx/bind 的 init() 里。
// 内外测试文件编进同一个测试二进制，这里挂上 blank import 让注册对
// 冒烟用例同样生效（cmd/gox、packager 也是靠同样的 blank import 拿到 gx/theme）。
package gfx_test

import (
	_ "github.com/14752222/Gox/gfx/bind"
)

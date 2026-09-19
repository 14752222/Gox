package stdlib

import (
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// setupGoxUmbrella 注册聚合模块 "gox"。
//
// 动机: 一个 GUI 应用通常要同时导入 2-4 个 gx/* 细分模块 ——
//
//	import { createSignal } from "gx/solid";
//	import { h, render } from "gx/gfx";
//	import { For, Show } from "gx/view";
//
// 聚合入口把常用的那一份导入压成一行:
//
//	import { h, render, createSignal, For, Show } from "gox";
//
// 语义: "gox" 是 gx/* 全部导出的并集, 不是新的 API 表 —— 各细分模块
// 仍然是唯一实现, 按需细粒度导入的写法继续可用 (推荐库代码用细分模块,
// 应用代码用聚合入口)。
//
// 实现说明: 构建导出表时逐个 LookupBuiltinModule 惰性触发子模块构建,
// 因此不依赖各注册方 (stdlib/gfx) 的 init 顺序; gfx 未被链接的宿主里
// GUI 相关子集 (gx/gfx 等) 不存在, 并集自动退化为宿主实际提供的部分。
// 导出名跨子模块无冲突 (各模块命名空间已按职责划分)。
func setupGoxUmbrella(env *runtime.Environment) {
	object.RegisterBuiltinModule("gox", func() map[string]object.Value {
		submodules := []string{
			"gx/solid",
			"gx/gfx",
			"gx/view",
			"gx/dialog",
			"gx/storage",
			"gx/dev",
		}
		merged := map[string]object.Value{}
		for _, name := range submodules {
			exports, ok := object.LookupBuiltinModule(name)
			if !ok {
				continue // 宿主未链接该模块的提供方 (如无头环境没有 gx/gfx)
			}
			for k, v := range exports {
				merged[k] = v
			}
		}
		return merged
	})
}

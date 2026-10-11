// Package bind 是 gfx 的 JS 绑定层: 把内核能力以 object.RegisterBuiltinModule
// 注册成脚本可 import 的 "gx/xxx" 模块。拆出独立子包是为了给 gfx 单包划出
// 一条编译期边界 —— 这里只许依赖 gfx 的**导出** API, 反过来 gfx 不依赖本包
// (无循环依赖)。
//
// 纯重构约定: 模块名、导出函数名、参数与返回形状必须与拆分前逐字节一致,
// 行为不变。目前迁出的模块:
//
//   - gx/theme (setTheme / toggleDark / current)
//
// 其余 gx/* 模块 (gx/gfx、gx/dev、gx/a11y、gx/router、gx/screen、gx/view、
// gx/viewport、gx/app、gx/device、gx/geo、gx/media、gx/permission、gx/dialog)
// 的门面深度依赖 gfx 未导出符号 (currentApp、节点内部状态、渲染循环钩子),
// 迁出需要成批导出内部面, 风险大于收益, 暂留在 gfx 包内。
package bind

import (
	"github.com/14752222/Gox/config"
	"github.com/14752222/Gox/gfx"
	"github.com/14752222/Gox/object"
)

// ===== gx/theme JS 门面 =====

func init() {
	object.RegisterBuiltinModule("gx/theme", func() map[string]object.Value {
		// gox.json 的 "theme" 字段在此消费 (模块首次被 import 时)。
		// 脚本随后显式 setTheme 会覆盖它; window 元素的 theme prop 在
		// render() 时应用, 晚于这里 —— 三条入口优先级: setTheme 调用 >
		// window prop > gox.json。
		if cfg, err := config.Load("."); err == nil && cfg.Theme != "" {
			gfx.SetThemeNamed(cfg.Theme)
		}
		return map[string]object.Value{
			"setTheme":   object.NewBuiltin("setTheme", jsSetTheme),
			"toggleDark": object.NewBuiltin("toggleDark", jsToggleDark),
			"current":    object.NewBuiltin("current", jsCurrentTheme),
		}
	})
}

// jsSetTheme setTheme("light"|"dark") 或 setTheme({accent: "#e67e22", ...})。
// 预设名返回布尔 (未知名 false); 对象形式返回实际生效的覆盖个数。
func jsSetTheme(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.NewTypeError("setTheme: expected a preset name or an overrides object")
	}
	switch v := args[0].(type) {
	case *object.String:
		return object.NewBoolean(gfx.SetThemeNamed(v.Value))
	case *object.Object:
		overrides := map[string]string{}
		for _, key := range v.Keys() {
			if val, ok := v.GetProperty(key); ok {
				if s, ok := val.(*object.String); ok {
					overrides[key] = s.Value
				}
			}
		}
		return object.NewNumber(float64(gfx.ApplyOverrides(overrides)))
	default:
		return object.NewTypeError("setTheme: expected a string or an object, got %s", object.TypeOf(args[0]))
	}
}

// jsToggleDark toggleDark(): 亮 ↔ 暗。
func jsToggleDark(args ...object.Value) object.Value {
	gfx.ToggleDark()
	return object.NewBoolean(true)
}

// jsCurrentTheme current(): 读当前 token 表 (键 = token 名, 值 = #rrggbbaa
// 十六进制), 供 devtools 快照或脚本按 token 取色。
func jsCurrentTheme(args ...object.Value) object.Value {
	o := object.NewObject()
	for key, hex := range gfx.ThemeTokens() {
		o.SetProperty(key, object.NewString(hex))
	}
	return o
}

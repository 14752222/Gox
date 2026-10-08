package ast

// ==================== 解构模式的绑定名 (解析器 / 编译器共用一份遍历) ====================
//
// 为什么需要共享: 解构声明在 Gox 里被**脱糖**成合成形态 —— 声明项的名字是
// 合成名 "__destructure__", 真实绑定名只存在于该声明项初始值的赋值表达式
// 左值 (模式) 里 (解析见 parser 的 parseArrayPattern / parseObjectPattern,
// 形态说明见 ast.ForOfStatement 的注释)。
//
// 于是"这条声明绑定了哪些名字"必须走一遍模式树, 而这个需求两边都有:
//   - 编译器: 收集 export 声明贡献的导出名 (declaratorExportNames);
//   - 解析器: 模块早错的「ExportedBindings ⊆ 已声明名」检查
//     (parser/module_early_errors.go)。
// 遍历逻辑因此只留这一份, 避免两处口径漂移。

// DestructureSyntheticName 是解构声明脱糖后在 AST 里留下的合成名。
// 解析器与编译器都靠它判断"这条声明是解构, 真实名字在 Value 的模式里"。
const DestructureSyntheticName = "__destructure__"

// PatternBoundNames 从一个**解构声明项的初始值**里收集真实绑定名。
//
// 入参 value 是该声明项的初始值表达式; 解构时它是
// AssignmentExpression{Left: 模式} (与普通 `x = init` 同形, 靠合成名区分),
// 不是该形态时返回 nil。
//
// 覆盖: 数组 / 对象模式、嵌套模式、元素默认值 ([a = 1] / {a = 1})、
// 对象属性与 rest。合成名自身不计入。
func PatternBoundNames(value Expression) []string {
	assign, ok := value.(*AssignmentExpression)
	if !ok {
		return nil
	}
	var out []string
	collectPatternBoundNames(assign.Left, &out)
	return out
}

func collectPatternBoundNames(pattern Expression, out *[]string) {
	switch p := pattern.(type) {
	case *Identifier:
		if p.Value != DestructureSyntheticName {
			*out = append(*out, p.Value)
		}
	case *ArrayPattern:
		for _, el := range p.Elements {
			if el != nil {
				collectPatternBoundNames(el.Target, out)
			}
		}
	case *ObjectPattern:
		for _, prop := range p.Properties {
			if prop != nil {
				collectPatternBoundNames(prop.Value, out)
			}
		}
		if p.RestTarget != nil {
			collectPatternBoundNames(p.RestTarget, out)
		}
	case *AssignmentExpression:
		// 带默认值的元素: [a = 1] / {a = 1}
		collectPatternBoundNames(p.Left, out)
	}
}

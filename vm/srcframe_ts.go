package vm

// srcframe_units.go 内容 (文件名为 srcframe_ts.go, 归本会话所有) ——
// 源码单元 (srcUnit) 与跨 VM 共享的单元注册表, 以及 TS 行映射接线 (M2 P0-1)。
//
// 为什么按"编译单元"建模: 位置表 (Positions / mainPositions) 是每个编译单元
// 各一张的, 且记录的是转译后 JS 的行列。模块 A 导出的函数被入口调用时, 抛错
// 帧属于 A 的编译单元, 而当时正在运行的 VM 是入口 VM —— 只用一个 VM 级的
// srcText/lineMap 会拿入口的 .ts 去解释 A 的 JS 行号, 位置全错。因此把
// "文件 + 原文 + JS + 行映射" 打包成 srcUnit, 并通过 *CompiledFunction 反查。
//
// 覆盖范围:
//   - 入口 .ts/.tsx (EvalFileVM): 运行时错误 + 引擎 parser/compiler 错误;
//   - 被 import 的 .ts/.tsx (loadModuleFile): 同上 (此前模块内报错根本没有源码帧);
//   - 解析错误 (esbuild 阶段) 由 tstransform 直接带 .ts 行号, 无需映射;
//   - 映射缺失/失败: render 回退到转译后 JS 并标注, 绝不假装精确。

import (
	"fmt"
	"strings"
	"sync"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/tstransform"
)

// srcUnit 是一个编译单元的源码信息。
type srcUnit struct {
	file string // 用户可见的文件路径
	text string // 展示用原文 (.ts 原文 / JS 原文)
	js   string // 转译后 JS (仅 TS; 映射失败时回退展示)
	isTS bool   // 是否来自 TS 家族
	lm   *tstransform.LineMap
	// isModule 标记"这是被 import 的模块单元"。只有模块单元才把函数登记进
	// units 注册表 —— 入口脚本的函数可以在抛出时回退到 mainUnit, 无需登记,
	// 从而把注册表规模限制在"各模块顶层执行时创建的函数"(一次性, 有界)。
	isModule bool
}

// render 渲染该单元在**转译后 JS 位置** (line, col) 上的源码帧。
func (u *srcUnit) render(line, col int) string {
	showText := u.text
	showLine, showCol := line, col
	mapped := false
	if u.lm != nil {
		if sl, sc, ok := u.lm.MapPos(line, col); ok {
			showLine, showCol = sl, sc
			mapped = true
		}
	}
	if u.isTS && !mapped {
		if u.js == "" {
			// 有 TS 来源但既无映射又无 JS 文本 —— 无法安全渲染, 宁可不给帧,
			// 也不拿 JS 行号索引 .ts 原文。
			return ""
		}
		showText = u.js
		showLine, showCol = line, col
	}

	lines := strings.Split(showText, "\n")
	if showLine < 1 || showLine > len(lines) {
		return ""
	}
	src := strings.TrimRight(lines[showLine-1], "\r")
	if showCol < 1 || showCol > len(src)+1 {
		showCol = 1
	}
	gutter := fmt.Sprintf("%d", showLine)
	pad := strings.Repeat(" ", len(gutter))
	var b strings.Builder
	fmt.Fprintf(&b, "    --> %s:%d:%d", u.file, showLine, showCol)
	if u.isTS && !mapped {
		fmt.Fprintf(&b, "\n        (line mapped from JS: 未映射到 .ts, 以下为转译后 JS 第 %d 行)", line)
	}
	b.WriteString("\n" + pad + " |\n")
	fmt.Fprintf(&b, " %s | %s\n", gutter, src)
	fmt.Fprintf(&b, "%s | %s^", pad, strings.Repeat(" ", showCol-1))
	return b.String()
}

// SetTranspileMap 注入 TS 转译的行映射与转译后 JS 文本。
//
// 调用约定: 先 SetSourceInfo(file, tsText) (给原文), 再调用本方法 (补映射与 JS)。
// jsText 即使映射缺失也要给 —— 它是回退展示的素材。
func (vm *VM) SetTranspileMap(lm *tstransform.LineMap, jsText string) {
	if vm.mainUnit == nil {
		vm.mainUnit = &srcUnit{}
	}
	vm.mainUnit.lm = lm
	vm.mainUnit.js = jsText
	vm.mainUnit.isTS = true
}

// unitRegistry 是 *CompiledFunction → 所属源码单元 的注册表。
//
// 并发: 多个独立 VM 可能在不同 goroutine 运行 (test262 worker 池), 且模块 VM
// 与入口 VM 共享同一个注册表 —— 必须加锁。
type unitRegistry struct {
	mu sync.Mutex
	m  map[*object.CompiledFunction]*srcUnit
}

func newUnitRegistry() *unitRegistry {
	return &unitRegistry{m: map[*object.CompiledFunction]*srcUnit{}}
}

func (r *unitRegistry) put(fn *object.CompiledFunction, u *srcUnit) {
	if r == nil || fn == nil || u == nil {
		return
	}
	r.mu.Lock()
	r.m[fn] = u
	r.mu.Unlock()
}

func (r *unitRegistry) get(fn *object.CompiledFunction) *srcUnit {
	if r == nil || fn == nil {
		return nil
	}
	r.mu.Lock()
	u := r.m[fn]
	r.mu.Unlock()
	return u
}

// remapSourceError 把 parser 报的 JS 行号改写成 .ts 行号 (尽力而为)。
//
// 只处理 parse 阶段的 sourceError: compiler 错误目前不带行号, 改了也没意义;
// 转译阶段 (esbuild) 的错误本身已经是 .ts 位置。映射不可用时原样返回。
func remapSourceError(err error, lm *tstransform.LineMap) error {
	if lm == nil {
		return err
	}
	if se, ok := err.(*sourceError); ok && se.parse {
		return &sourceError{parse: true, msg: lm.RemapMessage(se.msg)}
	}
	return err
}

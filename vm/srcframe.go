package vm

import (
	"errors"
	"sort"
	"strings"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/object"
)

// 运行时错误源码帧 (T05 / M2)。
//
// 目标: 未捕获异常的输出从单行 "TypeError: xxx" 升级为
//
//	TypeError: Cannot read properties of undefined
//	    --> app.js:3:9
//	     |
//	   3 | let y = obj.x.y;
//	     |         ^
//
// 覆盖两类错误: JS 异常 (ThrowError, 位置来自抛出点的 PC + 抛出帧的
// 位置表) 与 Go 边界错误 (runProtected 的 InternalError, 位置来自
// panic 时的当前帧)。dev 与 release 行为一致 —— 没有开关, 也不依赖
// 位置映射存在: eval / 动态代码无位置信息时保持原单行输出。
//
// 位置表按编译单元隔离: 主脚本一张 (SetStmtPositions 注入), 每个函数
// 体一张 (CompiledFunction.Positions) —— 函数体内抛错回溯到函数内
// 出错行, 而不是调用点。
//
// M2 (TS): 每个编译单元还带一份"JS 行 → .ts 行列"映射与 .ts 原文。
// 渲染帧时先翻译行号再取原文; 映射不可用时回退到转译后 JS 并标注。
// 详见 srcframe_units.go。

// SetSourceInfo 注入当前脚本的文件名与文本 (EvalFileVM / loadModuleFile 接线)。
// text 对 .ts/.tsx 是**用户原文**, JS 文件就是 JS 本身。
func (vm *VM) SetSourceInfo(file, text string) {
	vm.srcFile = file
	vm.srcText = text
	u := &srcUnit{file: file, text: text}
	if vm.mainUnit != nil {
		// 保留可能已注入的映射 (调用方约定先 SetSourceInfo 再 SetTranspileMap,
		// 但也允许反过来 —— 这里不丢已有映射)。
		u.js = vm.mainUnit.js
		u.isTS = vm.mainUnit.isTS
		u.lm = vm.mainUnit.lm
	}
	vm.mainUnit = u
}

// SetStmtPositions 注入主脚本的语句位置表并建立升序索引。
func (vm *VM) SetStmtPositions(tbl map[int]ast.Pos) {
	if len(tbl) == 0 {
		return
	}
	list := make([]object.SrcPos, 0, len(tbl))
	for off, p := range tbl {
		list = append(list, object.SrcPos{Offset: off, Line: p.Line, Col: p.Col})
	}
	object.SortSrcPos(list)
	vm.mainPositions = list
}

// FrameError 是附带源码帧的错误。Error() 把帧渲染在原错误消息之后。
type FrameError struct {
	Inner error
	Frame string
}

func (e *FrameError) Error() string { return e.Inner.Error() + "\n" + e.Frame }
func (e *FrameError) Unwrap() error { return e.Inner }

// AttachFrame 给运行时错误附加源码帧 (尽力而为): 无位置信息或找不到
// 匹配语句时原样返回。模块/eval 等无源码场景不受影响。
func (vm *VM) AttachFrame(err error) error {
	if err == nil {
		return err
	}
	var fe *FrameError
	if errors.As(err, &fe) {
		return err // 已附加过, 不重复
	}
	pc, tbl, unit, ok := vm.throwSourceUnit(err)
	if !ok || unit == nil {
		return err
	}
	pos, found := nearestPos(tbl, pc)
	if !found {
		return err
	}
	frame := unit.render(pos.Line, pos.Col)
	if frame == "" {
		return err
	}
	return &FrameError{Inner: err, Frame: frame}
}

// throwSourceUnit 选定用于定位的错误 PC、位置表与源码单元。JS 异常
// (ThrowError) 用抛出点记录的帧 —— 函数体错误查该帧闭包的位置表与来源单元,
// 主脚本错误查 VM 主表/主单元。InternalError (Go 边界 panic) 用 panic
// 时刻的当前帧。
func (vm *VM) throwSourceUnit(err error) (int, []object.SrcPos, *srcUnit, bool) {
	var te *ThrowError
	if errors.As(err, &te) {
		if !vm.hasThrowPC {
			return 0, nil, nil, false
		}
		if vm.lastThrowFrame != nil && vm.lastThrowFrame.Closure != nil && vm.lastThrowFrame.Closure.Fn != nil {
			fn := vm.lastThrowFrame.Closure.Fn
			return vm.lastThrowPC, fn.Positions, vm.unitFor(fn), true
		}
		return vm.lastThrowPC, vm.mainPositions, vm.mainUnit, true
	}
	if strings.Contains(err.Error(), "InternalError: VM panic") &&
		vm.frameIdx >= 0 && vm.frameIdx < len(vm.frames) && vm.frames[vm.frameIdx] != nil {
		f := vm.currentFrame()
		var tbl []object.SrcPos
		var unit *srcUnit
		if f.Closure != nil && f.Closure.Fn != nil {
			tbl = f.Closure.Fn.Positions
			unit = vm.unitFor(f.Closure.Fn)
		} else {
			unit = vm.mainUnit
		}
		return f.PC, tbl, unit, true
	}
	return 0, nil, nil, false
}

// unitFor 返回某个已编译函数所属的源码单元; 未注册时回退到主单元
// (纯 JS 场景下两者等价; 显式注册只发生在 TS/模块路径)。
func (vm *VM) unitFor(fn *object.CompiledFunction) *srcUnit {
	if vm.units != nil {
		if u := vm.units.get(fn); u != nil {
			return u
		}
	}
	return vm.mainUnit
}

// nearestPos 在位置表 (offset 升序) 里找 ≤PC 的最近语句位置。
func nearestPos(tbl []object.SrcPos, pc int) (object.SrcPos, bool) {
	if len(tbl) == 0 {
		return object.SrcPos{}, false
	}
	i := sort.Search(len(tbl), func(i int) bool { return tbl[i].Offset > pc })
	if i == 0 {
		return object.SrcPos{}, false
	}
	return tbl[i-1], true
}

// renderFrame 渲染源码帧 (错误行 + 列定位符)。源码行越界时返回空。
//
// 传入的 (line, col) 是**编译单元 (转译后 JS) 的位置**。若单元来自 TS
// 且有行映射, 先翻译回 .ts 行列, 再拿 .ts 原文渲染; 映射缺失/失败时回退到
// 转译后 JS 文本, 并显式标注 (line mapped from JS) —— 宁可暴露"不精确", 也
// 不能拿 JS 行号去索引 TS 原文给出一个看似正确其实错位的帧。
func (vm *VM) renderFrame(line, col int) string {
	if vm.mainUnit == nil {
		return ""
	}
	return vm.mainUnit.render(line, col)
}

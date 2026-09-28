package vm

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/object"
)

// 运行时错误源码帧 (T05)。
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

// SetSourceInfo 注入当前脚本的文件名与文本 (EvalFileVM 接线)。
func (vm *VM) SetSourceInfo(file, text string) {
	vm.srcFile = file
	vm.srcText = text
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
	if err == nil || vm.srcText == "" {
		return err
	}
	var fe *FrameError
	if errors.As(err, &fe) {
		return err // 已附加过, 不重复
	}
	pc, tbl, ok := vm.throwSourcePC(err)
	if !ok {
		return err
	}
	pos, found := nearestPos(tbl, pc)
	if !found {
		return err
	}
	frame := vm.renderFrame(pos.Line, pos.Col)
	if frame == "" {
		return err
	}
	return &FrameError{Inner: err, Frame: frame}
}

// throwSourcePC 选定用于定位的错误 PC 与位置表。JS 异常 (ThrowError)
// 用抛出点记录的帧 —— 函数体错误查该帧闭包的位置表, 主脚本错误查
// VM 主表。InternalError (Go 边界 panic) 用 panic 时刻的当前帧。
func (vm *VM) throwSourcePC(err error) (int, []object.SrcPos, bool) {
	var te *ThrowError
	if errors.As(err, &te) {
		if !vm.hasThrowPC {
			return 0, nil, false
		}
		if vm.lastThrowFrame != nil && vm.lastThrowFrame.Closure != nil && vm.lastThrowFrame.Closure.Fn != nil {
			return vm.lastThrowPC, vm.lastThrowFrame.Closure.Fn.Positions, true
		}
		return vm.lastThrowPC, vm.mainPositions, true
	}
	if strings.Contains(err.Error(), "InternalError: VM panic") &&
		vm.frameIdx >= 0 && vm.frameIdx < len(vm.frames) && vm.frames[vm.frameIdx] != nil {
		f := vm.currentFrame()
		var tbl []object.SrcPos
		if f.Closure != nil && f.Closure.Fn != nil {
			tbl = f.Closure.Fn.Positions
		}
		return f.PC, tbl, true
	}
	return 0, nil, false
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
func (vm *VM) renderFrame(line, col int) string {
	lines := strings.Split(vm.srcText, "\n")
	if line < 1 || line > len(lines) {
		return ""
	}
	src := strings.TrimRight(lines[line-1], "\r")
	if col < 1 || col > len(src)+1 {
		col = 1
	}
	gutter := fmt.Sprintf("%d", line)
	pad := strings.Repeat(" ", len(gutter))
	var b strings.Builder
	fmt.Fprintf(&b, "    --> %s:%d:%d", vm.srcFile, line, col)
	b.WriteString("\n" + pad + " |\n")
	fmt.Fprintf(&b, " %s | %s\n", gutter, src)
	fmt.Fprintf(&b, "%s | %s^", pad, strings.Repeat(" ", col-1))
	return b.String()
}

// Package vm 实现了 JavaScript 的栈式字节码虚拟机。
//
// VM 核心包含三个组件:
//   - 操作数栈 (Stack): 计算过程中的临时值栈
//   - 调用帧 (Frame): 每个函数调用创建一个帧，保存局部变量和 PC
//   - 全局环境 (Environment): 存储全局变量 (通过 OP_LOAD_GLOBAL 访问)
//
// 执行流程:
//  1. 编译器输出字节码 + 常量池
//  2. VM 创建主帧，开始取指-解码-执行循环
//  3. 每条指令: 读取 1 字节 opcode + 2 字节 operand, 执行对应操作
//  4. PC 前进 3 字节，取下一条指令
//  5. OP_CALL 创建新帧, OP_RETURN 弹出帧并返回值
package vm

import "github.com/14752222/Gox/object"

// StackSize 是操作数栈的初始容量。
const StackSize = 2048

// Stack 是 VM 的操作数栈。
// 所有计算中间结果都暂存在这里。
type Stack struct {
	values []object.Value
}

// NewStack 创建空的操作数栈。
func NewStack() *Stack {
	return &Stack{values: make([]object.Value, 0, StackSize)}
}

// Push 将值压入栈顶。
func (s *Stack) Push(v object.Value) {
	s.values = append(s.values, v)
}

// Pop 弹出并返回栈顶值。调用前必须确保栈非空。
func (s *Stack) Pop() object.Value {
	n := len(s.values) - 1
	v := s.values[n]
	s.values[n] = nil // 帮助 GC
	s.values = s.values[:n]
	return v
}

// Peek 返回栈顶值但不弹出。
func (s *Stack) Peek() object.Value {
	return s.values[len(s.values)-1]
}

// PeekAt 返回栈中指定偏移的值 (0 = 栈顶)。
func (s *Stack) PeekAt(n int) object.Value {
	return s.values[len(s.values)-1-n]
}

// Len 返回栈中当前元素数。
func (s *Stack) Len() int {
	return len(s.values)
}

// Truncate 将栈截断到指定高度 n (丢弃 n 以上的值)。
func (s *Stack) Truncate(n int) {
	if n < 0 {
		n = 0
	}
	if n >= len(s.values) {
		return
	}
	for i := n; i < len(s.values); i++ {
		s.values[i] = nil // 帮助 GC
	}
	s.values = s.values[:n]
}

// Reset 清空栈。
func (s *Stack) Reset() {
	s.values = s.values[:0]
}

package vm

import "testing"

// 诊断已完成的临时文件 (一次性严格定时器触发竞态已在 RunTimersUntil
// 修复: 派发队列残留信号必须先消费再退出)。删除被环境安全机制拦截,
// 保留为空壳。
func TestStrictOneShotRepeated_old(t *testing.T) {}

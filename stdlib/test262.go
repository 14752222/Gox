package stdlib

import (
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// SetupTest262Globals 注入 test262 宿主约定需要但语言标准不提供的全局对象。
//
// 目前只提供 $262 —— 官方 test262 runner 要求宿主实现它 (见
// test262/INTERPRETING.md 的 "Host-Defined Algorithms")。Gox 的
// cmd/gox/cmd_test262.go 在拼装用例源码时调用本函数，把 $262 声明进全局
// 环境，从而让 harness/detachArrayBuffer.js 的 $DETACHBUFFER 可用。
//
// 只补语言层无对应能力的成员:
//   - detachArrayBuffer(ab): ArrayBuffer.prototype 没有标准方法能分离缓冲区
//     (ES2024 起只能由宿主分离)，故必须由宿主提供。这是 133 条 detached-buffer
//     用例的唯一依赖。
//
// 未实现的成员 (createRealm / evalScript / gc / agent 等) 一律不声明 ——
// 声明了却抛 "未实现" 只会把 runner 侧的覆盖短板伪装成引擎缺陷。
func SetupTest262Globals(env *runtime.Environment) {
	dollar := object.NewObject()
	dollar.SetBuiltinProperty("detachArrayBuffer", object.NewBuiltin("detachArrayBuffer", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewTypeError("$262.detachArrayBuffer: 缺少 ArrayBuffer 参数")
		}
		buf, ok := args[0].(*object.ArrayBuffer)
		if !ok {
			return object.NewTypeError("$262.detachArrayBuffer: 参数必须是 ArrayBuffer")
		}
		buf.DetachNow()
		return object.UndefinedSingleton
	}))
	env.Declare("$262", dollar, false)
}

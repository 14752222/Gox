package vm

// ===== testdata/http_demo.js 的全链路回归 =====
//
// http 模块的单元测试在 vm_host_modules_test.go（脚本内联、断言点单一）。
// 本文件跑的是**仓库里那份给人看的演示脚本**本身: 它真的起一个 TCP 监听、
// 真的用 fetch / http.get / http.request 打自己 26 次，最后把断言摘要挂到
// globalThis.__httpDemoSummary。跑通它等于确认"示例脚本可直接运行"这条纪律
// 没破 —— 演示脚本最容易悄悄过期，因为它不参与编译，错了也没人知道。
//
// 接线纪律: http 的监听 goroutine 与在途请求靠调度器的挂起任务保活事件循环，
// 所以必须走 EvalFileVM + RunTimers（EvalVM 不驱动循环，服务刚起来就没了）；
// 收尾断言要在 RunTimers 返回之后做，因为那时循环已归零、进程不再被保活。

import (
	"path/filepath"
	"testing"

	"github.com/14752222/Gox/object"
)

// httpDemoReport 是脚本里 26 条 check 拼出来的期望值（顺序即执行顺序）。
// 改脚本时同步改这里 —— 不一致就说明演示的行为真的变了，这是刻意的。
const httpDemoReport = "server.listening=true | server.port>0=true | fetch / status=200 | " +
	"fetch / ok=true | fetch / 首行=http demo server | fetch / X-Powered-By=gox-http-demo | " +
	"GET /api/notes?limit=1 status=200 | GET /api/notes?limit=1 total=2 | " +
	"GET /api/notes?limit=1 returned=1 | GET /api/notes/1 status=200 | " +
	"GET /api/notes/1 text=买咖啡豆 | POST /api/notes status=201 | POST /api/notes id=3 | " +
	"POST 坏 JSON status=400 | GET /api/notes/999 status=404 | GET /api/notes/999 ok=false | " +
	"PUT /api/notes status=405 | http.get /api/notes/1=200:买咖啡豆 | " +
	"DELETE /api/notes/2=200:deleted=2:remaining=2 | GET /api/slow status=200 | " +
	"GET /api/slow delayedMs=30 | GET /api/boom status=500 | " +
	"GET /api/boom body=Internal Server Error | 最终 notes 条数=2 | 最终 notes id=1,3 | " +
	"close 后 listening=false"

func TestHTTPDemoScript(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	v, err := EvalFileVM(filepath.Join("..", "testdata", "http_demo.js"))
	if err != nil {
		t.Fatalf("执行脚本: %v", err)
	}
	// 服务在 listen 的回调里跑完整个客户端序列后自己 close，
	// 挂起任务归零，事件循环正常结束 —— 这一步不返回就说明有请求没被响应。
	if err := v.RunTimers(); err != nil {
		t.Fatalf("事件循环: %v", err)
	}

	summary, ok := v.Globals().Get("__httpDemoSummary")
	if !ok {
		t.Fatalf("脚本没有导出 __httpDemoSummary")
	}
	obj, isObj := summary.(*object.Object)
	if !isObj {
		t.Fatalf("__httpDemoSummary 应该是对象, 得到 %s", summary.Inspect())
	}

	// 端口由内核分配: 只断言"是个合法端口"，具体值每次不同
	portVal, _ := obj.GetProperty("port")
	port, isNum := portVal.(*object.Number)
	if !isNum || port.Value <= 0 || port.Value > 65535 {
		t.Fatalf("port 不是合法端口: %s", portVal.Inspect())
	}

	// close 之后 listening 必须翻回 false（否则脚本退出后套接字还挂着）
	listeningVal, _ := obj.GetProperty("listening")
	assertBoolean(t, listeningVal, false)

	// 26 条断言的逐条结果
	reportVal, _ := obj.GetProperty("report")
	assertString(t, reportVal, httpDemoReport)
}

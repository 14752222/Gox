package vm

// ===== WebSocket 客户端 (testdata/websocket_demo.js) 的全链路回归 =====
//
// 起一个真正的 gorilla echo 服务，让 JS 侧 `new WebSocket(url)` 走完整
// 握手 → onopen → send → onmessage → close → onclose 链路。接线纪律与
// http_demo 一致: 读循环靠调度器挂起任务保活，必须 EvalVM + RunTimers。

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/gorilla/websocket"
)

func startWSEchoServer(t *testing.T) string {
	t.Helper()
	upgrader := websocket.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			mt, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestWebSocketDemoScript(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	url := startWSEchoServer(t)

	src, err := os.ReadFile(filepath.Join("..", "testdata", "websocket_demo.js"))
	if err != nil {
		t.Fatalf("读示例脚本: %v", err)
	}
	script := strings.ReplaceAll(string(src), "__WS_URL__", url)

	v, err := EvalVM(script)
	if err != nil {
		t.Fatalf("执行脚本: %v", err)
	}
	if err := v.RunTimers(); err != nil {
		t.Fatalf("事件循环: %v", err)
	}

	eventsVal, ok := v.Globals().Get("__wsDemoEvents")
	if !ok {
		t.Fatalf("脚本没有导出 __wsDemoEvents (onclose 未触发?)")
	}
	assertString(t, eventsVal,
		"open readyState=1 | message hello | message gox | close code=1000 reason=done clean=true")
}

// 握手失败: onerror 后紧跟 onclose (code=1006)。
func TestWebSocketDialFailure(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	v, err := EvalVM(`
const log = [];
const ws = new WebSocket("ws://127.0.0.1:1/nope");
ws.onerror = (ev) => log.push("error");
ws.onclose = (ev) => {
	log.push("close code=" + ev.code + " clean=" + ev.wasClean);
	globalThis.__wsFailLog = log.join(" | ");
};
`)
	if err != nil {
		t.Fatalf("执行脚本: %v", err)
	}
	if err := v.RunTimers(); err != nil {
		t.Fatalf("事件循环: %v", err)
	}
	logVal, ok := v.Globals().Get("__wsFailLog")
	if !ok {
		t.Fatalf("脚本没有导出 __wsFailLog")
	}
	assertString(t, logVal, "error | close code=1006 clean=false")
}

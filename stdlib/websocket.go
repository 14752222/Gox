package stdlib

import (
	"strings"
	"sync"

	"github.com/14752222/Gox/object"
	"github.com/gorilla/websocket"
)

// ===== WebSocket 客户端 (浏览器风格 API) =====
//
// JS API (尽量贴近浏览器 WebSocket):
//   const ws = new WebSocket(url)
//   ws.onopen = () => { ... }
//   ws.onmessage = (ev) => { ev.data }   // 文本帧为 string，二进制帧暂按 string 投递
//   ws.onclose = (ev) => { ev.code, ev.reason }
//   ws.onerror = (ev) => { ev.message }
//   ws.send(text); ws.close(code?, reason?)
//   只读属性: readyState (0 CONNECTING / 1 OPEN / 2 CLOSING / 3 CLOSED), url
//
// 线程模型与 http 模块一致: VM 单线程，gorilla 的读循环跑在 goroutine 里
// 只做 I/O，事件通过 dispatchToLoop (0ms 定时器) 投递回主线程再触发
// JS 回调。读循环期间用 AddPendingTask 保活事件循环，连接关闭后归还令牌。
//
// 并发约束: gorilla/websocket 要求"最多一个并发 reader、一个并发 writer"。
// 写路径只在主线程 (send/close)，读路径只在读循环 goroutine，天然满足；
// 状态字段用 mu 保护，因为读循环 goroutine 会推进 readyState。

const (
	wsConnecting = 0
	wsOpen       = 1
	wsClosing    = 2
	wsClosed     = 3
)

// wsConn 是一个 WebSocket 连接的 Go 侧状态。
type wsConn struct {
	mu      sync.Mutex
	conn    *websocket.Conn
	state   int
	pending int // 调度器保活令牌; 0 表示已归还
	self    *object.Object
	closedEmitted bool // onclose 只投递一次 (浏览器行为); close() 与读循环都可能触发
}

func (w *wsConn) setState(s int) {
	w.mu.Lock()
	w.state = s
	w.mu.Unlock()
}

func (w *wsConn) getState() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state
}

// emitClose 投递一次 onclose; 重复调用 (主动 close 与读循环退出竞态) 只生效首次。
func (w *wsConn) emitClose(code int, reason string, wasClean bool) {
	w.mu.Lock()
	if w.closedEmitted {
		w.mu.Unlock()
		return
	}
	w.closedEmitted = true
	w.mu.Unlock()
	w.setState(wsClosed)
	w.finishPending()
	ev := newWsEvent("close")
	ev.SetProperty("code", object.NewNumber(float64(code)))
	ev.SetProperty("reason", object.NewString(reason))
	ev.SetProperty("wasClean", object.NewBoolean(wasClean))
	// readyState 的落笔与回调都放主线程，避免 goroutine 直接改 JS 对象。
	dispatchToLoop(func() {
		w.self.SetProperty("readyState", object.NewNumber(wsClosed))
		if v, ok := w.self.GetProperty("onclose"); ok && object.IsCallable(v) {
			object.CallFunction(v, w.self, ev)
			if cbErr := object.TakeCallbackError(); cbErr != nil {
				reportUncaught(cbErr)
			}
		}
	})
}

// finishPending 归还事件循环保活令牌 (幂等)。
func (w *wsConn) finishPending() {
	w.mu.Lock()
	tok := w.pending
	w.pending = 0
	w.mu.Unlock()
	if tok != 0 {
		object.GlobalScheduler().FinishPendingTask(tok)
	}
}

// emit 把事件回调投递到主线程。handler 是 ws.onxxx 的当前值。
func (w *wsConn) emit(name string, ev *object.Object) {
	dispatchToLoop(func() {
		var handler object.Value
		if v, ok := w.self.GetProperty(name); ok && object.IsCallable(v) {
			handler = v
		} else {
			return // 没挂回调就丢弃事件 (与浏览器一致: 未监听则无动作)
		}
		object.CallFunction(handler, w.self, ev)
		if cbErr := object.TakeCallbackError(); cbErr != nil {
			reportUncaught(cbErr)
		}
	})
}

func newWsEvent(evType string) *object.Object {
	ev := object.NewObject()
	ev.SetProperty("type", object.NewString(evType))
	return ev
}

// newWebSocket 是 `new WebSocket(url)` 的构造实现。
func newWebSocket(args ...object.Value) object.Value {
	if len(args) < 1 {
		return object.NewTypeError("WebSocket: missing url argument")
	}
	urlStr, ok := args[0].(*object.String)
	if !ok {
		return object.NewTypeError("WebSocket: url must be a string, got %s", object.TypeOf(args[0]))
	}
	url := urlStr.Value
	if !strings.HasPrefix(url, "ws://") && !strings.HasPrefix(url, "wss://") {
		return object.NewTypeError("WebSocket: url must start with ws:// or wss://")
	}

	w := &wsConn{state: wsConnecting}

	self := object.NewObject()
	w.self = self
	self.SetProperty("url", urlStr)
	self.SetProperty("CONNECTING", object.NewNumber(wsConnecting))
	self.SetProperty("OPEN", object.NewNumber(wsOpen))
	self.SetProperty("CLOSING", object.NewNumber(wsClosing))
	self.SetProperty("CLOSED", object.NewNumber(wsClosed))
	self.SetProperty("onopen", object.NullSingleton)
	self.SetProperty("onmessage", object.NullSingleton)
	self.SetProperty("onclose", object.NullSingleton)
	self.SetProperty("onerror", object.NullSingleton)

	// readyState 是"属性"，但引擎的对象是普通属性包; 用数字字段并在事件
	// 推进时同步更新即可 (回调都在主线程跑，读到的是最近一次事件的值)。
	self.SetProperty("readyState", object.NewNumber(wsConnecting))

	// readyState 的落笔统一走主线程派发 (dial/读循环在 goroutine 里，
	// 直接 SetProperty 会与主线程读写竞争)。
	syncState := func() {
		dispatchToLoop(func() {
			self.SetProperty("readyState", object.NewNumber(float64(w.getState())))
		})
	}

	self.SetProperty("send", object.NewBuiltin("send", func(args ...object.Value) object.Value {
		if w.getState() != wsOpen {
			return object.NewTypeError("WebSocket.send: connection is not open (readyState=%d)", w.getState())
		}
		data := ""
		if len(args) > 0 {
			data = toStr(args[0])
		}
		w.mu.Lock()
		conn := w.conn
		w.mu.Unlock()
		if conn == nil {
			return object.NewTypeError("WebSocket.send: connection is not open")
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(data)); err != nil {
			return object.NewTypeError("WebSocket.send: %v", err)
		}
		return object.UndefinedSingleton
	}))

	self.SetProperty("close", object.NewBuiltin("close", func(args ...object.Value) object.Value {
		code := websocket.CloseNormalClosure
		reason := ""
		if len(args) > 0 {
			if n, ok := args[0].(*object.Number); ok {
				code = int(n.Value)
			}
		}
		if len(args) > 1 {
			reason = toStr(args[1])
		}
		st := w.getState()
		if st == wsClosed || st == wsClosing {
			return object.UndefinedSingleton
		}
		w.setState(wsClosing)
		syncState()
		// 必须先 emitClose 再关底层连接: conn.Close() 会立刻唤醒读循环，
		// 若先关连接，读循环的 "use of closed network connection" (1006)
		// 会抢在本次正常关闭 (1000) 之前投递 onclose。
		w.emitClose(code, reason, true)
		w.mu.Lock()
		conn := w.conn
		w.mu.Unlock()
		if conn != nil {
			_ = conn.WriteMessage(websocket.CloseMessage,
				websocket.FormatCloseMessage(code, reason))
			_ = conn.Close()
		}
		return object.UndefinedSingleton
	}))

	// 握手放 goroutine; 保活令牌在连接彻底关闭 (或握手失败) 时归还。
	w.pending = object.GlobalScheduler().AddPendingTask()
	go w.dial(url, syncState)

	return self
}

// dial 在 goroutine 里完成握手，成功后进入读循环。
func (w *wsConn) dial(url string, syncState func()) {
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		ev := newWsEvent("error")
		ev.SetProperty("message", object.NewString(err.Error()))
		w.emit("onerror", ev)
		// 握手失败同样触发 onclose (浏览器行为: error 后紧跟 close)。
		w.emitClose(1006, "", false)
		return
	}
	w.mu.Lock()
	w.conn = conn
	w.mu.Unlock()
	w.setState(wsOpen)
	syncState()
	w.emit("onopen", newWsEvent("open"))

	// 读循环: 每帧投递一次 onmessage; 连接断开时投递 onclose。
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			code := 1006
			reason := ""
			if ce, ok := err.(*websocket.CloseError); ok {
				code = ce.Code
				reason = ce.Text
			}
			_ = conn.Close()
			w.emitClose(code, reason, code == 1000)
			return
		}
		if w.getState() != wsOpen {
			continue
		}
		// 二进制帧暂以 string 投递 (引擎侧 Blob/ArrayBuffer 桥接超出本任务范围)。
		ev := newWsEvent("message")
		ev.SetProperty("data", object.NewString(string(data)))
		ev.SetProperty("binary", object.NewBoolean(msgType == websocket.BinaryMessage))
		w.emit("onmessage", ev)
	}
}
// wsConstructor 是共享的 WebSocket 构造器 (http 模块导出 + 全局构造器同源)。
var wsConstructor = object.NewBuiltin("WebSocket", newWebSocket)

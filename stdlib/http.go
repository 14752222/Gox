package stdlib

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// httpClient 是模块共享的 HTTP 客户端。带超时避免脚本卡死在不可达地址上。
var httpClient = &http.Client{Timeout: 30 * time.Second}

// setupHTTP 注册 http 模块与全局 fetch。
//
// 线程模型: VM 是单线程的，所有 JS 回调都必须在事件循环主线程执行。
// Go 侧 goroutine (HTTP 服务器的连接处理、客户端请求) 只做 I/O，
// 完成后通过 0ms 定时器把结果投递回主线程。服务器监听与进行中的
// 请求用调度器的挂起任务计数保活事件循环，否则脚本执行完 listen()
// 后主线程发现"没有任务"就直接退出进程了。
func setupHTTP(env *runtime.Environment) {
	h := object.NewObject()

	h.SetProperty("createServer", object.NewBuiltin("createServer", func(args ...object.Value) object.Value {
		if len(args) < 1 || !object.IsCallable(args[0]) {
			return object.NewTypeError("http.createServer: handler must be a function")
		}
		return newJSServer(args[0])
	}))

	// http.get(url, options?, callback?) — GET 请求，无回调时返回 Promise
	h.SetProperty("get", object.NewBuiltin("get", func(args ...object.Value) object.Value {
		return httpRequest("get", args)
	}))

	// http.request(url, options?, callback?) — options: {method, headers, body}
	h.SetProperty("request", object.NewBuiltin("request", func(args ...object.Value) object.Value {
		return httpRequest("request", args)
	}))

	env.Declare("http", h, false)

	// ===== 全局 fetch(url, options?) =====
	env.Declare("fetch", object.NewBuiltin("fetch", func(args ...object.Value) object.Value {
		return httpRequest("fetch", args)
	}), false)
}

// dispatchToLoop 把 fn 投递到事件循环主线程执行。
// 在 http 模块的 goroutine 里调用；主线程通过 0ms 定时器发现并运行它。
// 调度器内部有互斥锁，跨 goroutine 注册定时器是安全的。
func dispatchToLoop(fn func()) {
	object.GlobalScheduler().SetTimeout(object.NewBuiltin("__http_dispatch", func(...object.Value) object.Value {
		fn()
		return object.UndefinedSingleton
	}), 0)
}

// reportUncaught 报告投递回调里未被 JS 捕获的异常。
// VM 对内建函数返回的 *Error 不做抛出处理 (只有 JS 闭包抛出的异常会
// 通过 error 传回事件循环)，因此这里显式打到 stderr，避免错误被静默吞掉。
func reportUncaught(err error) {
	fmt.Fprintf(os.Stderr, "Uncaught %s\n", err.Error())
}

// ===== HTTP 服务器 =====

// jsServer 保存 Go 侧 HTTP 服务器与 JS 处理器的关联状态。
type jsServer struct {
	handler   object.Value
	httpSrv   *http.Server
	listening bool
	token     int // 调度器挂起任务令牌，保活事件循环
}

// newJSServer 创建服务器对象 (尚未监听)。
func newJSServer(handler object.Value) *object.Object {
	srv := &jsServer{handler: handler}
	serverObj := object.NewObject()

	serverObj.SetProperty("listening", object.NewBoolean(false))
	serverObj.SetProperty("port", object.UndefinedSingleton)

	// server.listen(port, host?, callback?)
	// 在独立 goroutine 中启动 Go HTTP 服务器；每个请求经 0ms 定时器
	// 派发到主线程调用 JS 处理器。
	serverObj.SetProperty("listen", object.NewBuiltin("listen", func(args ...object.Value) object.Value {
		if srv.listening {
			return object.NewError("http: server is already listening")
		}
		if len(args) < 1 {
			return object.NewTypeError("http.listen: missing port argument")
		}
		port := toFloat(args[0])
		if port != port || port < 0 || port > 65535 {
			return object.NewRangeError("http.listen: invalid port %s", args[0].Inspect())
		}
		host := ""
		if len(args) > 1 {
			if hs, ok := args[1].(*object.String); ok {
				host = hs.Value
			}
		}
		addr := net.JoinHostPort(host, strconv.Itoa(int(port)))
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return object.NewError(fmt.Sprintf("http.listen: %v", err))
		}
		srv.httpSrv = &http.Server{Handler: http.HandlerFunc(srv.serveHTTP)}
		srv.token = object.GlobalScheduler().AddPendingTask()
		srv.listening = true
		serverObj.SetProperty("listening", object.NewBoolean(true))
		serverObj.SetProperty("port", object.NewNumber(float64(ln.Addr().(*net.TCPAddr).Port)))
		go srv.httpSrv.Serve(ln)
		// 监听就绪后回调 (若提供)。即使无回调，挂起任务也已保活循环。
		if cb := lastCallback(args); cb != nil {
			object.GlobalScheduler().SetTimeout(object.NewBuiltin("__http_listening", func(...object.Value) object.Value {
				object.CallFunction(cb, nil)
				return object.UndefinedSingleton
			}), 0)
		}
		return serverObj
	}))

	// server.close(callback?)
	serverObj.SetProperty("close", object.NewBuiltin("close", func(args ...object.Value) object.Value {
		if srv.listening {
			srv.listening = false
			serverObj.SetProperty("listening", object.NewBoolean(false))
			object.GlobalScheduler().FinishPendingTask(srv.token)
			srv.httpSrv.Close()
		}
		if cb := lastCallback(args); cb != nil {
			object.GlobalScheduler().SetTimeout(object.NewBuiltin("__http_closed", func(...object.Value) object.Value {
				object.CallFunction(cb, nil)
				return object.UndefinedSingleton
			}), 0)
		}
		return object.UndefinedSingleton
	}))

	return serverObj
}

// jsResponse 汇集 JS 处理器写出的响应状态。
// statusCode 与 headers/body 只在主线程读写；goroutine 在 done 关闭后
// (happens-before) 才读取它们写出响应。
type jsResponse struct {
	headers map[string]string
	body    strings.Builder
	ended   bool
	done    chan struct{}
}

// serveHTTP 是 Go 侧每个请求的入口 (运行在 net/http 的 goroutine 中)。
// 读请求体 → 构造 req/res 对象 → 投递到主线程调用 JS 处理器 →
// 等待 res.end() → 把缓冲的响应写回客户端。
func (s *jsServer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<20))
	if err != nil {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}

	resState := &jsResponse{
		headers: make(map[string]string),
		done:    make(chan struct{}),
	}
	reqObj := newRequestObject(r, string(body))
	resObj := newResponseObject(resState)

	// 把处理器调用投递到主线程。注意此时主线程可能在执行任意 JS，
	// 这里绝不能直接 CallFunction。
	dispatchToLoop(func() {
		object.CallFunction(s.handler, nil, reqObj, resObj)
		if cbErr := object.TakeCallbackError(); cbErr != nil {
			// 处理器抛异常且尚未响应: 返回 500，其余情况只报告
			reportUncaught(cbErr)
			if !resState.ended {
				finishWith500(resState, resObj)
			}
		}
		// 处理器正常返回但没 end: 响应可能延后到某个定时器里，继续等待
	})

	<-resState.done

	for k, v := range resState.headers {
		w.Header().Set(k, v)
	}
	statusCode := 200
	if v, ok := resObj.GetProperty("statusCode"); ok {
		if n := int(toFloat(v)); n != 0 {
			statusCode = n
		}
	}
	w.WriteHeader(statusCode)
	io.WriteString(w, resState.body.String())
}

// finishWith500 在主线程上结束一个抛异常的请求: 清空缓冲并以 500 响应。
func finishWith500(resState *jsResponse, resObj *object.Object) {
	resState.headers = make(map[string]string)
	resState.body.Reset()
	resState.body.WriteString("Internal Server Error")
	resObj.SetProperty("statusCode", object.NewNumber(500))
	resState.ended = true
	close(resState.done)
}

// newRequestObject 构造 JS 可见的请求对象。
func newRequestObject(r *http.Request, body string) *object.Object {
	req := object.NewObject()
	req.SetProperty("method", object.NewString(r.Method))
	req.SetProperty("url", object.NewString(r.URL.RequestURI()))
	path := r.URL.Path
	if path == "" {
		path = "/"
	}
	req.SetProperty("path", object.NewString(path))

	headers := object.NewObject()
	for k, vs := range r.Header {
		headers.SetProperty(k, object.NewString(strings.Join(vs, ", ")))
	}
	req.SetProperty("headers", headers)

	// 查询参数: 单值 → 字符串，多值 → 数组
	query := object.NewObject()
	for k, vs := range r.URL.Query() {
		if len(vs) == 1 {
			query.SetProperty(k, object.NewString(vs[0]))
			continue
		}
		elements := make([]object.Value, len(vs))
		for i, v := range vs {
			elements[i] = object.NewString(v)
		}
		query.SetProperty(k, object.NewArray(elements))
	}
	req.SetProperty("query", query)

	req.SetProperty("body", object.NewString(body))

	req.SetProperty("getHeader", object.NewBuiltin("getHeader", func(args ...object.Value) object.Value {
		if len(args) < 1 {
			return object.UndefinedSingleton
		}
		v, ok := headers.GetProperty(toStr(args[0]))
		if !ok {
			return object.UndefinedSingleton
		}
		return v
	}))

	return req
}

// newResponseObject 构造 JS 可见的响应对象。
// write/end 只缓冲到 resState，真正的网络写出发生在 serveHTTP 的尾部。
func newResponseObject(resState *jsResponse) *object.Object {
	res := object.NewObject()
	res.SetProperty("statusCode", object.NewNumber(200))

	res.SetProperty("setHeader", object.NewBuiltin("setHeader", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return object.NewTypeError("res.setHeader: expected (name, value)")
		}
		resState.headers[toStr(args[0])] = toStr(args[1])
		return object.UndefinedSingleton
	}))

	res.SetProperty("getHeader", object.NewBuiltin("getHeader", func(args ...object.Value) object.Value {
		if len(args) < 1 {
			return object.UndefinedSingleton
		}
		if v, ok := resState.headers[toStr(args[0])]; ok {
			return object.NewString(v)
		}
		return object.UndefinedSingleton
	}))

	res.SetProperty("removeHeader", object.NewBuiltin("removeHeader", func(args ...object.Value) object.Value {
		if len(args) > 0 {
			delete(resState.headers, toStr(args[0]))
		}
		return object.UndefinedSingleton
	}))

	// res.writeHead(statusCode, headers?) — 一步设置状态码与响应头
	res.SetProperty("writeHead", object.NewBuiltin("writeHead", func(args ...object.Value) object.Value {
		if len(args) < 1 {
			return object.NewTypeError("res.writeHead: missing statusCode argument")
		}
		res.SetProperty("statusCode", object.NewNumber(toFloat(args[0])))
		if len(args) > 1 {
			if hs, ok := args[1].(*object.Object); ok {
				for _, k := range hs.Keys() {
					if v, found := hs.GetProperty(k); found {
						resState.headers[k] = toStr(v)
					}
				}
			}
		}
		return res
	}))

	// res.write(chunk) — 追加响应体 (支持链式多次调用)
	res.SetProperty("write", object.NewBuiltin("write", func(args ...object.Value) object.Value {
		if resState.ended {
			return object.NewError("res.write: response already ended")
		}
		if len(args) > 0 {
			resState.body.WriteString(toStr(args[0]))
		}
		return object.UndefinedSingleton
	}))

	// res.end(body?) — 结束响应。statusCode 以 JS 属性为准
	// (writeHead 与直接赋值 res.statusCode = 404 两种写法都生效)。
	res.SetProperty("end", object.NewBuiltin("end", func(args ...object.Value) object.Value {
		if resState.ended {
			return object.UndefinedSingleton
		}
		if len(args) > 0 {
			resState.body.WriteString(toStr(args[0]))
		}
		resState.ended = true
		close(resState.done)
		return object.UndefinedSingleton
	}))

	return res
}

// ===== HTTP 客户端 =====

// httpRequest 实现 http.get / http.request / fetch 的公共流程。
//
// 回调风格 (最后一个参数可调用): 完成后 cb(err, resObj)；
// 否则返回 Promise (resolve resObj / reject Error)。
// 网络层错误 (DNS、超时、连接拒绝) 视为失败；HTTP 4xx/5xx 不算错误，
// 与 Node/fetch 语义一致。
func httpRequest(op string, args []object.Value) object.Value {
	if len(args) < 1 {
		return object.NewTypeError("%s: missing url argument", op)
	}
	urlStr, ok := args[0].(*object.String)
	if !ok {
		return object.NewTypeError("%s: url must be a string, got %s", op, object.TypeOf(args[0]))
	}

	method := "GET"
	var reqHeaders map[string]string
	var reqBody string
	if len(args) > 1 {
		if opts, isObj := args[1].(*object.Object); isObj {
			if mv, found := opts.GetProperty("method"); found {
				method = strings.ToUpper(toStr(mv))
			}
			if hv, found := opts.GetProperty("headers"); found {
				if hs, isObj := hv.(*object.Object); isObj {
					reqHeaders = make(map[string]string)
					for _, k := range hs.Keys() {
						if v, ok := hs.GetProperty(k); ok {
							reqHeaders[k] = toStr(v)
						}
					}
				}
			}
			if bv, found := opts.GetProperty("body"); found {
				reqBody = toStr(bv)
			}
		}
	}

	cb := lastCallback(args)

	if cb == nil {
		result := object.NewPromise()
		token := object.GlobalScheduler().AddPendingTask()
		go func() {
			defer object.GlobalScheduler().FinishPendingTask(token)
			status, statusText, headers, body, netErr := performRequest(method, urlStr.Value, reqHeaders, reqBody)
			dispatchToLoop(func() {
				if netErr != nil {
					result.Reject(object.NewError(fmt.Sprintf("%s: %v", op, netErr)))
					return
				}
				if op == "fetch" {
					result.Resolve(newFetchResponse(urlStr.Value, status, statusText, headers, body))
				} else {
					result.Resolve(newClientResponse(urlStr.Value, status, statusText, headers, body))
				}
				// Resolve 会同步驱动 await 恢复链; 恢复链里的未捕获异常
				// (如用户代码错误) 在这里显式报告，避免被静默吞掉。
				if cbErr := object.TakeCallbackError(); cbErr != nil {
					reportUncaught(cbErr)
				}
			})
		}()
		return result
	}

	// 回调风格
	token := object.GlobalScheduler().AddPendingTask()
	go func() {
		defer object.GlobalScheduler().FinishPendingTask(token)
		status, statusText, headers, body, netErr := performRequest(method, urlStr.Value, reqHeaders, reqBody)
		dispatchToLoop(func() {
			if netErr != nil {
				runCallback(cb, object.NewError(fmt.Sprintf("%s: %v", op, netErr)), object.UndefinedSingleton)
				return
			}
			runCallback(cb, nil, newClientResponse(urlStr.Value, status, statusText, headers, body))
		})
	}()
	return object.UndefinedSingleton
}

// runCallback 调用回调并报告未捕获异常。
func runCallback(cb object.Value, errVal object.Value, result object.Value) {
	if errVal == nil {
		errVal = object.NullSingleton
	}
	object.CallFunction(cb, nil, errVal, result)
	if cbErr := object.TakeCallbackError(); cbErr != nil {
		reportUncaught(cbErr)
	}
}

// performRequest 执行一次 HTTP 请求 (运行在 goroutine 中，禁止触碰 VM)。
// 返回完整响应；网络层错误通过 netErr 返回。
func performRequest(method, urlStr string, headers map[string]string, body string) (status int, statusText string, respHeaders map[string]string, respBody []byte, netErr error) {
	var req *http.Request
	var err error
	if body == "" {
		req, err = http.NewRequest(method, urlStr, nil)
	} else {
		req, err = http.NewRequest(method, urlStr, strings.NewReader(body))
	}
	if err != nil {
		return 0, "", nil, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, "", nil, nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, "", nil, nil, err
	}
	respHeaders = make(map[string]string)
	for k, vs := range resp.Header {
		respHeaders[k] = strings.Join(vs, ", ")
	}
	text := resp.Status
	if i := strings.IndexByte(text, ' '); i >= 0 {
		text = text[i+1:]
	}
	return resp.StatusCode, text, respHeaders, data, nil
}

// newClientResponse 构造 http.get/http.request 的响应对象。
func newClientResponse(urlStr string, status int, statusText string, headers map[string]string, body []byte) *object.Object {
	res := object.NewObject()
	res.SetProperty("url", object.NewString(urlStr))
	res.SetProperty("status", object.NewNumber(float64(status)))
	res.SetProperty("statusText", object.NewString(statusText))
	res.SetProperty("headers", headersToObject(headers))
	res.SetProperty("body", object.NewString(string(body)))
	return res
}

// newFetchResponse 构造 fetch 的 Response 对象。
// text()/json() 直接返回已 resolve 的 Promise —— Resolve 发生在返回前，
// 此时还没有任何回调注册，因此不会同步执行用户代码。
func newFetchResponse(urlStr string, status int, statusText string, headers map[string]string, body []byte) *object.Object {
	resp := object.NewObject()
	bodyStr := string(body)
	resp.SetProperty("url", object.NewString(urlStr))
	resp.SetProperty("status", object.NewNumber(float64(status)))
	resp.SetProperty("statusText", object.NewString(statusText))
	resp.SetProperty("ok", object.NewBoolean(status >= 200 && status < 300))
	resp.SetProperty("headers", headersToObject(headers))
	resp.SetProperty("body", object.NewString(bodyStr))

	resp.SetProperty("text", object.NewBuiltin("text", func(...object.Value) object.Value {
		p := object.NewPromise()
		p.Resolve(object.NewString(bodyStr))
		return p
	}))
	resp.SetProperty("json", object.NewBuiltin("json", func(...object.Value) object.Value {
		p := object.NewPromise()
		ordered, perr := parseOrderedJSON(bodyStr)
		if perr != nil {
			p.Reject(object.NewErrorWithName("SyntaxError", perr.Error()))
			return p
		}
		p.Resolve(orderedToValue(ordered))
		return p
	}))
	return resp
}

// headersToObject 把 Go 头映射转为 JS 对象。
func headersToObject(headers map[string]string) *object.Object {
	obj := object.NewObject()
	for k, v := range headers {
		obj.SetProperty(k, object.NewString(v))
	}
	return obj
}

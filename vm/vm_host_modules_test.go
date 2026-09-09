package vm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"js-runtime/object"
)

// 本文件覆盖宿主能力模块: fs / path / process / http (含全局 fetch)。
// 另含 await rejection 恢复的回归测试 (async 驱动 step 的 throw 路径)。

// q 把 Go 字符串编码为 JS 字符串字面量。
func q(s string) string {
	return "'" + strings.NewReplacer("\\", "\\\\", "'", "\\'").Replace(s) + "'"
}

// runAsync 执行脚本并驱动事件循环，返回全局变量 name 的值。
func runAsync(t *testing.T, src, name string) object.Value {
	t.Helper()
	v, err := EvalVM(src)
	if err != nil {
		t.Fatalf("EvalVM error: %v", err)
	}
	if err := v.RunTimers(); err != nil {
		t.Fatalf("RunTimers error: %v", err)
	}
	val, ok := v.Globals().Get(name)
	if !ok {
		t.Fatalf("global %q not found", name)
	}
	return val
}

// ===== fs =====

func TestFSSyncWriteRead(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "hello.txt")

	evalJS(t, "fs.writeFileSync("+q(file)+", 'hello fs')")
	assertString(t, evalJS(t, "fs.readFileSync("+q(file)+")"), "hello fs")
	assertBoolean(t, evalJS(t, "fs.existsSync("+q(file)+")"), true)
	assertBoolean(t, evalJS(t, "fs.existsSync("+q(filepath.Join(dir, "nope.txt"))+")"), false)
}

func TestFSSyncAppendAndEncodings(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "log.txt")

	evalJS(t, "fs.writeFileSync("+q(file)+", 'a')")
	evalJS(t, "fs.appendFileSync("+q(file)+", 'b')")
	assertString(t, evalJS(t, "fs.readFileSync("+q(file)+")"), "ab")
	// base64("ab") = "YWI="
	assertString(t, evalJS(t, "fs.readFileSync("+q(file)+", 'base64')"), "YWI=")
}

func TestFSSyncBytesRoundtrip(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "bin.dat")

	evalJS(t, "fs.writeFileSync("+q(file)+", [65, 66, 67])")
	assertString(t, evalJS(t, "fs.readFileSync("+q(file)+")"), "ABC")
	assertString(t, evalJS(t, "fs.readBytesSync("+q(file)+").join(',')"), "65,66,67")
}

func TestFSSyncMkdirReaddirRm(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "parent", "child")

	evalJS(t, "fs.mkdirSync("+q(sub)+", {recursive: true})")
	evalJS(t, "fs.writeFileSync("+q(filepath.Join(sub, "a.txt"))+", 'A')")
	assertString(t, evalJS(t, "fs.readdirSync("+q(sub)+").join(',')"), "a.txt")

	st := evalJS(t, "JSON.stringify(fs.statSync("+q(sub)+"))")
	s, _ := st.(*object.String)
	if !strings.Contains(s.Value, `"isDirectory":true`) {
		t.Fatalf("statSync of dir: %s", s.Value)
	}

	// 非递归删除非空目录应报错；递归删除成功
	assertJSThrows(t, "fs.rmdirSync("+q(dir)+")", "Error")
	evalJS(t, "fs.rmSync("+q(dir)+", {recursive: true})")
	assertBoolean(t, evalJS(t, "fs.existsSync("+q(sub)+")"), false)
}

func TestFSSyncRenameCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "renamed.txt")
	copyDst := filepath.Join(dir, "copy.txt")

	evalJS(t, "fs.writeFileSync("+q(src)+", 'content')")
	evalJS(t, "fs.renameSync("+q(src)+", "+q(dst)+")")
	assertBoolean(t, evalJS(t, "fs.existsSync("+q(src)+")"), false)
	evalJS(t, "fs.copyFileSync("+q(dst)+", "+q(copyDst)+")")
	assertString(t, evalJS(t, "fs.readFileSync("+q(copyDst)+")"), "content")
}

func TestFSAsyncCallbackStyle(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "async.txt")

	got := runAsync(t, `
		let __r = null;
		fs.writeFile(`+q(file)+`, 'async data', function (err) {
			if (err !== null) { __r = 'write err: ' + err.message; return; }
			fs.readFile(`+q(file)+`, function (err2, data) {
				if (err2 !== null) { __r = 'read err: ' + err2.message; return; }
				__r = 'data:' + data;
			});
		});
	`, "__r")
	assertString(t, got, "data:async data")
}

func TestFSAsyncPromiseAwait(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "promise.txt")

	evalJS(t, "fs.writeFileSync("+q(file)+", 'promise data')")
	got := runAsync(t, `
		let __r = null;
		(async function () {
			const data = await fs.readFile(`+q(file)+`);
			__r = 'await:' + data;
		})().catch(function (e) { __r = 'catch:' + e.message; });
	`, "__r")
	assertString(t, got, "await:promise data")
}

func TestFSAsyncRejection(t *testing.T) {
	got := runAsync(t, `
		let __r = null;
		(async function () {
			try {
				await fs.readFile('definitely_missing_xyz.txt');
				__r = 'NO-THROW';
			} catch (e) {
				__r = 'caught:' + e.name;
			}
		})();
	`, "__r")
	assertString(t, got, "caught:Error")
}

// ===== path =====

func TestPathJoinResolve(t *testing.T) {
	cwd, _ := os.Getwd()
	assertString(t, evalJS(t, `path.join('a', 'b', 'c.txt')`), filepath.Join("a", "b", "c.txt"))
	assertString(t, evalJS(t, `path.resolve('x.txt')`), filepath.Join(cwd, "x.txt"))
}

func TestPathParts(t *testing.T) {
	assertString(t, evalJS(t, `path.basename('/x/y/z.js')`), "z.js")
	assertString(t, evalJS(t, `path.dirname('/x/y/z.js')`), filepath.Dir("/x/y/z.js"))
	// Node 语义: 隐藏文件无扩展名
	assertString(t, evalJS(t, `path.extname('archive.tar.gz')`), ".gz")
	assertString(t, evalJS(t, `path.extname('.bashrc')`), "")
	assertBoolean(t, evalJS(t, `path.isAbsolute(path.resolve('a'))`), true)
}

// ===== process =====

func TestProcessBasics(t *testing.T) {
	assertBoolean(t, evalJS(t, `process.argv.length >= 1`), true)
	cwd, _ := os.Getwd()
	assertString(t, evalJS(t, `process.cwd()`), cwd)
	assertBoolean(t, evalJS(t, `'PATH' in process.env || 'Path' in process.env`), true)
}

// ===== http / fetch =====

func TestHTTPServerFetchRoundtrip(t *testing.T) {
	got := runAsync(t, `
		let __r = null;
		const server = http.createServer(function (req, res) {
			if (req.path === '/json') {
				res.writeHead(200, {'Content-Type': 'application/json'});
				res.end(JSON.stringify({msg: 'hi ' + req.query.name, method: req.method}));
			} else {
				res.end('hello world');
			}
		});
		server.listen(0, async function () {
			const r1 = await fetch('http://127.0.0.1:' + server.port + '/json?name=zed');
			const j = await r1.json();
			const r2 = await fetch('http://127.0.0.1:' + server.port + '/');
			__r = r1.status + ':' + j.msg + ':' + j.method + '|' + r2.status + ':' + (await r2.text());
			server.close();
		});
	`, "__r")
	assertString(t, got, "200:hi zed:GET|200:hello world")
}

func TestHTTPServerPostBodyAndMethod(t *testing.T) {
	got := runAsync(t, `
		let __r = null;
		const server = http.createServer(function (req, res) {
			res.end(req.method + ':' + req.body);
		});
		server.listen(0, async function () {
			const r = await fetch('http://127.0.0.1:' + server.port + '/submit', {
				method: 'POST',
				body: 'payload',
				headers: {'X-Tag': 't1'}
			});
			__r = r.status + ':' + (await r.text()) + ':' + r.ok;
			server.close();
		});
	`, "__r")
	assertString(t, got, "200:POST:payload:true")
}

func TestHTTPServerAsyncResponse(t *testing.T) {
	// end 延后到定时器里调用: 服务器挂起任务保活事件循环直到响应完成
	got := runAsync(t, `
		let __r = null;
		const server = http.createServer(function (req, res) {
			setTimeout(function () {
				res.statusCode = 201;
				res.end('slow');
			}, 10);
		});
		server.listen(0, async function () {
			const r = await fetch('http://127.0.0.1:' + server.port + '/');
			__r = r.status + ':' + (await r.text());
			server.close();
		});
	`, "__r")
	assertString(t, got, "201:slow")
}

func TestHTTPClientGetCallback(t *testing.T) {
	got := runAsync(t, `
		let __r = null;
		const server = http.createServer(function (req, res) {
			res.end('body:' + req.path);
		});
		server.listen(0, function () {
			const base = 'http://127.0.0.1:' + server.port;
			http.get(base + '/x', function (err, res) {
				if (err !== null) { __r = 'err:' + err.message; return; }
				__r = res.status + ':' + res.body;
				server.close();
			});
		});
	`, "__r")
	assertString(t, got, "200:body:/x")
}

// ===== await rejection 恢复 (回归测试) =====
//
// 曾经 __spawn 对被 reject 的 promise 只 reject 外层 Promise 而不把异常
// 抛回 generator，导致 async 函数体内 try/catch 永远捕获不到 await 的
// rejection，函数静默挂死。

func TestAwaitRejectionResumes(t *testing.T) {
	got := runAsync(t, `
		let __r = null;
		(async function () {
			try {
				await Promise.reject(new Error('boom'));
				__r = 'NO-THROW';
			} catch (e) {
				__r = 'caught:' + e.message + ';';
			}
			__r += 'done';
		})();
	`, "__r")
	assertString(t, got, "caught:boom;done")
}

func TestAwaitRejectionWithoutCatch(t *testing.T) {
	// 无 try/catch 时 rejection 应传递到外层 promise 的 catch
	got := runAsync(t, `
		let __r = null;
		(async function () {
			await fs.readFile('definitely_missing_xyz.txt');
			__r = 'NO-THROW';
		})().catch(function (e) {
			__r = 'outer:' + e.name;
		});
	`, "__r")
	assertString(t, got, "outer:Error")
}

// ===== 回调抛异常不留残留状态 (回归测试) =====
//
// 曾经 VM 层 callbackErr 与 object 层错误信号是两份状态，前者只在个别
// 位点被消费。回调抛异常后残留的错误会在之后任意一个内建函数调用点
// 被误抛出 (表现为回调链静默中断)。

func TestCallbackThrowLeavesNoStaleError(t *testing.T) {
	// 1. fs 异步回调里主动抛异常 (fs 层报告后丢弃，进程继续)
	// 2. 随后的正常异步回调必须照常执行，不被陈旧错误打断
	dir := t.TempDir()
	file := filepath.Join(dir, "cb.txt")
	got := runAsync(t, `
		let __r = null;
		fs.writeFile(`+q(file)+`, 'x', function (err) {
			throw new Error('intentional');
		});
		setTimeout(function () {
			__r = 'later tick ok';
		}, 5);
	`, "__r")
	assertString(t, got, "later tick ok")
}

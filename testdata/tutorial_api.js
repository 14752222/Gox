// ============================================================================
// §1 API 的调用方式与参数说明 —— 一份能直接跑通的宿主能力示例
//
// 运行:  gox testdata/tutorial_api.js
//        (npm 安装的等价命令: goxjs testdata/tutorial_api.js)
//
// 这一份覆盖四件事:
//   1) 哪些能力是**全局对象**(不用 import), 哪些要 import —— 见 §2
//   2) 同步 API 的约定: 成功返回值, 失败**抛 JS 异常**
//   3) 异步 API 的两种形态: 省略回调 → 返回 Promise; 末参传函数 → 回调式
//   4) 事件循环: "脚本执行到末尾"不等于"进程退出", 定时器与异步回调跑完才退
//
// 入口收尾刻意写成 `const boot = io()`: 若直接写 `io()`, 顶层表达式回显会
// 多打印一行 `Promise { <pending> }`(见 README「运行脚本」)。
// ============================================================================

console.log("== 1.1 全局对象: 不用 import, 直接可用 ==");
console.log("process.platform =", process.platform);
console.log("process.cwd()    =", process.cwd());
console.log("process.argv     =", process.argv); // [可执行文件, 脚本路径, 附加参数...]
console.log("path.sep         =", path.sep);

// ---------------------------------------------------------------------------
// 1.2 同步 API: 成功返回值, 失败抛异常
// ---------------------------------------------------------------------------
console.log("\n== 1.2 同步 API: 返回值 / 异常 ==");
const tmpDir = path.join(process.cwd(), "tutorial-tmp"); // 路径一律用 path.join 拼, 别手写分隔符
fs.mkdirSync(tmpDir, { recursive: true }); // {recursive:true}: 建多级目录, 已存在不报错
const notePath = path.join(tmpDir, "note.txt");
fs.writeFileSync(notePath, "hello gox\n"); // 返回 undefined

const st = fs.statSync(notePath); // 返回对象: size / mtimeMs / isFile / isDirectory
console.log("basename/extname =", path.basename(notePath), "/", path.extname(notePath));
console.log("statSync         = size", st.size, "isFile", st.isFile, "isDirectory", st.isDirectory);
console.log("readdirSync      =", fs.readdirSync(tmpDir));
console.log("readFileSync     =", JSON.stringify(fs.readFileSync(notePath)));

try {
  fs.readFileSync(path.join(tmpDir, "missing.txt")); // 同步 API 失败 = 抛异常
  console.log("这一行不会执行");
} catch (e) {
  console.log("读缺失文件 ->", e.name + ": " + e.message);
}

// ---------------------------------------------------------------------------
// 1.3 异步 API: 省略回调返回 Promise, 末参给函数走回调式
// ---------------------------------------------------------------------------
// 两句语法纪律: ① 顶层不能 await, 异步逻辑要放进 async function;
//              ② 运行时只支持 `async function`, **不支持 `async () => {}`**。

async function io() {
  console.log("\n== 1.3 异步 API: await / 回调式 ==");
  const text = await fs.readFile(notePath); // 省略回调 -> Promise<string>
  console.log("await fs.readFile        =", JSON.stringify(text));

  await fs.appendFile(notePath, "second line\n");
  const lines = (await fs.readFile(notePath)).trim().split("\n").length;
  console.log("append 后再读 -> 行数     =", lines);

  // 同一个 API 传了回调就不返回 Promise, 而是回调 (err, data): 失败时 err 是 Error, 成功时是 null
  const viaCallback = await new Promise((resolve) => {
    fs.readFile(notePath, "utf8", (err, data) => {
      resolve(err ? "ERR " + err.message : data.trim().split("\n").join(" | "));
    });
  });
  console.log("回调式 fs.readFile       =", viaCallback);

  // 纯函数型 API: 参数与返回值都在签名里, 无副作用
  console.log("stats.sum([1,2,3,4])     =", stats.sum([1, 2, 3, 4]));
  console.log("stats.describe([...])    =", JSON.stringify(stats.describe([3, 1, 4, 1, 5])));

  // ---------------------------------------------------------------------
  // 1.4 定时器: 回调驱动的异步, 归事件循环调度
  // ---------------------------------------------------------------------
  console.log("\n== 1.4 定时器与事件循环 ==");
  setTimeout(() => console.log("setTimeout(0) 回调"), 0);
  const dec = await delay(15, "delay(ms, value) 的返回值");
  console.log("await delay(15, v)       =", dec);

  let ticks = 0;
  const timer = setInterval(() => {
    ticks++;
    console.log("setInterval 第", ticks, "次");
    if (ticks === 3) clearInterval(timer); // 不清掉的话进程不会退出
  }, 5);
  await delay(60, null); // 给 setInterval 三次机会

  // ---------------------------------------------------------------------
  // 1.5 网络: http.createServer + fetch (本机回环, 不依赖外网)
  //     模型是"跨 goroutine I/O + 结果投递回 VM 单线程", 回调里不用加锁
  // ---------------------------------------------------------------------
  console.log("\n== 1.5 HTTP 服务端 + 客户端 ==");
  const server = http.createServer(function (req, res) {
    res.writeHead(200, { "Content-Type": "text/plain; charset=utf-8" });
    res.end("path=" + req.path + " query=" + JSON.stringify(req.query));
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve)); // 端口 0 = 系统分配
  console.log("server.listening =", server.listening, "port =", server.port);

  const resp = await fetch("http://127.0.0.1:" + server.port + "/hello?a=1");
  console.log("fetch -> status", resp.status, "ok", resp.ok);
  console.log("fetch -> body  ", await resp.text());
  await new Promise((resolve) => server.close(resolve)); // 收好事件循环, 否则进程挂着不退出

  // ---------------------------------------------------------------------
  // 1.6 收尾
  // ---------------------------------------------------------------------
  fs.rmSync(tmpDir, { recursive: true, force: true }); // 清理演示产物
  console.log("\n== 1.6 收尾 ==");
  console.log("临时目录已删除, existsSync =", fs.existsSync(tmpDir));
  console.log("所有注册过的定时器都已清掉 ⇒ 事件循环归零 ⇒ 进程正常退出");
}

const boot = io();

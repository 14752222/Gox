// ============================================================================
// http 模块演示 —— 用 http.createServer 起一个最小的 REST 服务，
//                  再用两种客户端风格把服务自己打一遍（全程走本机回环）。
//
// 运行:
//   go run . testdata/http_demo.js      (仓库内)
//   gox testdata/http_demo.js           (npm 安装的命令行，等价)
//
// 覆盖范围:
//   服务端  http.createServer(handler)
//           server.listen(port, host, cb) / server.close(cb) / server.port / server.listening
//           req:  method / url / path / query / headers / body / getHeader(name)
//           res:  statusCode / setHeader / getHeader / removeHeader / writeHead / write / end
//   客户端  fetch(url, opts) -> Promise;  http.get / http.request -> 回调式 (err, res)
//   事件循环 监听与在途请求靠"挂起任务"保活，服务关掉、定时器跑完后进程才退出
//
// 三个刻意的设计:
//   1) 监听端口写 0 —— 由系统分配空闲端口，所以这份 demo 可以并行跑多份，不抢 8080。
//   2) 数据全在内存里 —— 不依赖任何外部服务、数据库或网络，离线可跑。
//   3) 至少有一条路由**故意抛异常** —— 用来演示引擎的 500 路径（终端上的 Uncaught
//      是引擎按设计打到 stderr 的，不是脚本崩了）。
//
// 语法纪律: 运行时只认 let/const（没有 var）；顶层不能 await，异步逻辑要放进
// `async function`（或 async 箭头 `async () => {}`）里。
// ============================================================================

// ---------------------------------------------------------------------------
// 1. 内存里的"数据库"：一张 notes 表
// ---------------------------------------------------------------------------
let notes = [
  { id: 1, text: "买咖啡豆" },
  { id: 2, text: "写 http demo" },
];
let nextId = 3;

// 演示结果收集器。每条 check 都是一行 "名字=值"，最后拼成一份 report 交给测试断言。
const checks = [];
function check(name, value) {
  checks.push(name + "=" + value);
}

// ---------------------------------------------------------------------------
// 2. 响应小工具：把"状态码 + JSON + Content-Type"三件套收成一句
// ---------------------------------------------------------------------------
function json(res, status, data) {
  res.writeHead(status, { "Content-Type": "application/json; charset=utf-8" });
  res.end(JSON.stringify(data));
}

// ---------------------------------------------------------------------------
// 3. 路由：把请求分派到各个处理函数
//
//    req.path 是已经去掉查询串的路径（req.url 才带 ?a=1）；
//    req.query 是查询参数对象：单值给字符串，同名多值给数组。
//    真实项目里的路由可以换成 gx/router，这里手写一个最小分支，
//    好把注意力留在 http 模块本身。
// ---------------------------------------------------------------------------
function handle(req, res) {
  console.log("[server] " + req.method + " " + req.url);

  // 统一给所有响应挂一个自定义头，下面客户端会把它读回来
  res.setHeader("X-Powered-By", "gox-http-demo");

  const segs = req.path.split("/").filter((s) => s.length > 0);

  if (req.path === "/") {
    // res.write 可以多次调用（内容先缓冲在引擎里），最后由 res.end 一次性写出
    res.writeHead(200, { "Content-Type": "text/plain; charset=utf-8" });
    res.write("http demo server\n");
    res.write("routes:\n");
    res.write("  GET    /api/notes?limit=1\n");
    res.write("  GET    /api/notes/:id\n");
    res.write("  POST   /api/notes        {text}\n");
    res.write("  DELETE /api/notes/:id\n");
    res.write("  GET    /api/slow         30ms 后响应\n");
    res.write("  GET    /api/boom         故意抛异常 -> 500\n");
    res.end();
    return;
  }

  if (segs[0] === "api" && segs[1] === "notes") {
    if (segs.length === 2) {
      if (req.method === "GET") return listNotes(req, res);
      if (req.method === "POST") return createNote(req, res);
      return methodNotAllowed(res, "GET, POST");
    }
    if (segs.length === 3) {
      const id = Number(segs[2]);
      if (req.method === "GET") return getNote(res, id);
      if (req.method === "DELETE") return deleteNote(res, id);
      return methodNotAllowed(res, "GET, DELETE");
    }
  }

  if (req.path === "/api/slow") return slow(req, res);
  if (req.path === "/api/boom") return boom(req, res);

  json(res, 404, { error: "no route for " + req.path });
}

function listNotes(req, res) {
  let items = notes;
  // req.query 演示：?limit=1 时只返回前一条
  if (req.query.limit !== undefined) {
    const n = Number(req.query.limit);
    if (n > 0) items = items.slice(0, n);
  }
  json(res, 200, { total: notes.length, returned: items.length, items: items });
}

function getNote(res, id) {
  const found = notes.find((n) => n.id === id);
  if (found === undefined) {
    return json(res, 404, { error: "note " + id + " not found" });
  }
  json(res, 200, found);
}

function createNote(req, res) {
  let payload = null;
  try {
    payload = JSON.parse(req.body); // req.body 永远是字符串，要自己解
  } catch (e) {
    return json(res, 400, { error: "body 不是合法 JSON: " + e.message });
  }
  if (payload === null || typeof payload.text !== "string" || payload.text.length === 0) {
    return json(res, 400, { error: "缺少 text 字段" });
  }
  const note = { id: nextId, text: payload.text };
  nextId = nextId + 1;
  notes.push(note);
  json(res, 201, note); // 201 Created
}

function deleteNote(res, id) {
  const before = notes.length;
  notes = notes.filter((n) => n.id !== id);
  if (notes.length === before) {
    return json(res, 404, { error: "note " + id + " not found" });
  }
  json(res, 200, { deleted: id, remaining: notes.length });
}

// 异步响应：把 res.end 推到定时器里。挂起任务会保活事件循环直到响应写完，
// 否则请求还没回、主线程就发现"没有到期任务"把进程退了。
function slow(req, res) {
  setTimeout(() => {
    json(res, 200, { delayedMs: 30, ok: true });
  }, 30);
}

// 处理器抛异常：引擎向 stderr 报 "Uncaught ..."，并把未 end 的响应当 500 收尾
function boom(req, res) {
  throw new Error("故意抛的异常，用来演示 500 路径");
}

function methodNotAllowed(res, allow) {
  // 这条路由换个写法演示：直接给 res.statusCode 赋值同样生效（不必 writeHead）
  res.statusCode = 405;
  res.setHeader("Allow", allow);
  res.end(JSON.stringify({ error: "method not allowed", allow: allow }));
}

// ---------------------------------------------------------------------------
// 4. 主流程：起服务 -> 用客户端打自己 -> 关服务
// ---------------------------------------------------------------------------
async function main() {
  const server = http.createServer(handle);

  // listen(port, host?, callback?)：端口 0 = 系统分配；回调里才能读到真实端口
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  const base = "http://127.0.0.1:" + server.port;
  console.log("\n[client] 服务已就绪 " + base + "  listening=" + server.listening);
  check("server.listening", server.listening);
  check("server.port>0", server.port > 0);

  // ---- 4.1 fetch：Promise 风格，文本响应 ----
  console.log("\n== 4.1 fetch: text ==");
  const home = await fetch(base + "/");
  const homeText = await home.text();
  check("fetch / status", home.status);
  check("fetch / ok", home.ok);
  check("fetch / 首行", homeText.split("\n")[0]);
  check("fetch / X-Powered-By", home.headers["X-Powered-By"]);
  console.log("status=" + home.status + " ok=" + home.ok + " powered-by=" + home.headers["X-Powered-By"]);
  console.log(homeText.split("\n").slice(1, 3).join("\n"));

  // ---- 4.2 fetch：JSON 响应 + 查询参数 ----
  console.log("\n== 4.2 fetch: json + query ==");
  const listed = await fetch(base + "/api/notes?limit=1");
  const listJson = await listed.json(); // fetch 的 json() 直接给你解好的对象
  check("GET /api/notes?limit=1 status", listed.status);
  check("GET /api/notes?limit=1 total", listJson.total);
  check("GET /api/notes?limit=1 returned", listJson.returned);
  console.log("total=" + listJson.total + " returned=" + listJson.returned + " 首条=" + listJson.items[0].text);

  // 路径参数：/api/notes/1
  const one = await fetch(base + "/api/notes/1");
  check("GET /api/notes/1 status", one.status);
  check("GET /api/notes/1 text", (await one.json()).text);

  // ---- 4.3 fetch：POST 带 body 与请求头 ----
  console.log("\n== 4.3 fetch: POST ==");
  const created = await fetch(base + "/api/notes", {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-Client": "http_demo.js" },
    body: JSON.stringify({ text: "第三条" }),
  });
  const createdJson = await created.json();
  check("POST /api/notes status", created.status);
  check("POST /api/notes id", createdJson.id);
  console.log("201 新建 id=" + createdJson.id + " text=" + createdJson.text);

  // 坏 body -> 400（HTTP 4xx 不是"错误"，Promise 不 reject）
  const bad = await fetch(base + "/api/notes", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: "{ 这不是 JSON",
  });
  check("POST 坏 JSON status", bad.status);

  // 资源不存在 -> 404，且 ok === false
  const missing = await fetch(base + "/api/notes/999");
  check("GET /api/notes/999 status", missing.status);
  check("GET /api/notes/999 ok", missing.ok);

  // 方法不允许 -> 405（这条路由用 res.statusCode = 405 直接赋值）
  const wrongMethod = await fetch(base + "/api/notes", { method: "PUT" });
  check("PUT /api/notes status", wrongMethod.status);

  // ---- 4.4 http.get：回调风格 (err, res)，res 上是 status / body / headers ----
  console.log("\n== 4.4 http.get: 回调风格 ==");
  const viaGet = await new Promise((resolve) => {
    http.get(base + "/api/notes/1", function (err, res) {
      if (err !== null) return resolve("err:" + err.message);
      resolve(res.status + ":" + JSON.parse(res.body).text);
    });
  });
  console.log("http.get -> " + viaGet);
  check("http.get /api/notes/1", viaGet);

  // ---- 4.5 http.request：带 method 的回调风格 ----
  console.log("\n== 4.5 http.request: DELETE ==");
  const viaDelete = await new Promise((resolve) => {
    http.request(
      base + "/api/notes/2",
      { method: "DELETE" },
      function (err, res) {
        if (err !== null) return resolve("err:" + err.message);
        const body = JSON.parse(res.body);
        resolve(res.status + ":deleted=" + body.deleted + ":remaining=" + body.remaining);
      }
    );
  });
  console.log("http.request DELETE -> " + viaDelete);
  check("DELETE /api/notes/2", viaDelete);

  // ---- 4.6 异步响应：end 在定时器里 ----
  console.log("\n== 4.6 异步响应 (end 延后到定时器) ==");
  const slowRes = await fetch(base + "/api/slow");
  const slowJson = await slowRes.json();
  check("GET /api/slow status", slowRes.status);
  check("GET /api/slow delayedMs", slowJson.delayedMs);
  console.log("慢路由 -> " + slowRes.status + " delayedMs=" + slowJson.delayedMs);

  // ---- 4.7 处理器抛异常 -> 500（终端上的 Uncaught 是引擎按设计报的） ----
  console.log("\n== 4.7 处理器抛异常 -> 500 ==");
  const boomRes = await fetch(base + "/api/boom");
  check("GET /api/boom status", boomRes.status);
  check("GET /api/boom body", await boomRes.text());
  console.log("炸掉的路由 -> " + boomRes.status + "（引擎已往 stderr 报了一条 Uncaught）");

  // ---- 4.8 收尾：关服务，让事件循环归零 ----
  console.log("\n== 4.8 close ==");
  const remaining = await (await fetch(base + "/api/notes")).json();
  check("最终 notes 条数", remaining.total);
  check("最终 notes id", remaining.items.map((n) => n.id).join(","));

  await new Promise((resolve) => server.close(resolve));
  check("close 后 listening", server.listening);
  console.log("server.close() 之后 listening=" + server.listening + " —— 事件循环里没有挂起任务了，进程可以退出");

  // 给回归测试用的接口面（名字与任何顶层 const 都不同，避免自我引用）
  globalThis.__httpDemoSummary = {
    port: server.port,
    listening: server.listening,
    report: checks.join(" | "),
  };
  console.log("\n== 断言摘要 ==");
  console.log(globalThis.__httpDemoSummary.report);
}

const boot = main();

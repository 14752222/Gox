// http 模块冒烟测试: 服务器 + fetch + http.get 全链路
const server = http.createServer(function (req, res) {
  if (req.path === "/json") {
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ msg: "hi " + req.query.name, method: req.method, body: req.body }));
  } else if (req.path === "/async") {
    // 异步响应: end 延后到定时器里
    setTimeout(function () {
      res.statusCode = 201;
      res.setHeader("X-Slow", "yes");
      res.end("slow response");
    }, 30);
  } else if (req.path === "/boom") {
    throw new Error("handler exploded");
  } else if (req.method === "POST") {
    res.end("posted:" + req.body);
  } else {
    res.end("hello world");
  }
});

server.listen(0, function () {
  const port = server.port;
  const base = "http://127.0.0.1:" + port;
  console.log("listening on port", port, "(dynamic)");

  // 1. fetch 文本
  fetch(base + "/hello").then(function (r) {
    console.log("fetch status:", r.status, "ok:", r.ok);
    return r.text();
  }).then(function (t) {
    console.log("fetch text:", t);
  });

  // 2. fetch JSON + 查询参数 + POST body
  (async function () {
    const r1 = await fetch(base + "/json?name=zed");
    const j = await r1.json();
    console.log("json:", j.msg, j.method);

    const r2 = await fetch(base + "/submit", { method: "POST", body: "payload-1", headers: { "X-Tag": "t1" } });
    console.log("post:", await r2.text());

    // 3. 异步响应 (201 + 延迟 end)
    const r3 = await fetch(base + "/async");
    console.log("async:", r3.status, await r3.text(), r3.headers["X-Slow"]);

    // 4. 处理器抛异常 → 500
    const r4 = await fetch(base + "/boom");
    console.log("boom status:", r4.status);

    // 5. http.get 回调风格
    http.get(base + "/hello", function (err, res) {
      if (err) { console.log("get err:", err.message); return; }
      console.log("http.get:", res.status, res.body);

      // 6. http.request PUT
      http.request(base + "/put", { method: "PUT", body: "put-data" }, function (err2, res2) {
        if (err2) { console.log("req err:", err2.message); return; }
        console.log("http.request PUT:", res2.status);
        server.close(function () {
          console.log("server closed");
          console.log("HTTP-SMOKE-OK");
        });
      });
    });
  })().catch(function (e) {
    console.log("unexpected catch:", e.message);
  });
});

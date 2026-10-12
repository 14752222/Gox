// WebSocket 客户端演示 (浏览器风格 API)。
// 回显服务地址由运行方注入: 测试里用占位符替换 __WS_URL__，
// 手工跑时可先起一个 echo 服务再替换此常量。
const URL = "__WS_URL__";

const events = [];
const ws = new WebSocket(URL);

ws.onopen = () => {
	events.push("open readyState=" + ws.readyState);
	ws.send("hello");
	ws.send("gox");
};

ws.onmessage = (ev) => {
	events.push("message " + ev.data);
	if (ev.data === "gox") {
		ws.close(1000, "done");
	}
};

ws.onerror = (ev) => {
	events.push("error " + ev.message);
};

ws.onclose = (ev) => {
	events.push("close code=" + ev.code + " reason=" + ev.reason + " clean=" + ev.wasClean);
	globalThis.__wsDemoEvents = events.join(" | ");
};

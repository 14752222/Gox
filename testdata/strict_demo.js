// 严格定时器演示: 对比 setInterval 与 setStrictInterval 的触发间隔漂移
// 回调体耗时 ~20ms, 两者 interval 都是 30ms, 共运行 5 秒。

let normalCount = 0;
let strictCount = 0;
let normalId = 0;
let strictId = 0;

// 普通 setInterval: 每次回调结束后用 now+delay 重新基准, 回调耗时会把
// 实际间隔拉长到 ~50ms (20ms 回调 + 30ms 间隔)。
normalId = setInterval(() => {
	normalCount++;
	// 模拟耗时回调 (占用主线程)
	let s = 0;
	for (let i = 0; i < 100000; i++) { s += i; }
}, 30);

// 严格 setInterval: 绝对时间轴, 触发时刻 = start + k*30ms, 不受回调耗时影响。
strictId = setStrictInterval(() => {
	strictCount++;
	// 同样的耗时回调
	let s = 0;
	for (let i = 0; i < 100000; i++) { s += i; }
}, 30);

// 5 秒后清理定时器并汇总
setTimeout(() => {
	clearInterval(normalId);
	clearStrictInterval(strictId);
	console.log("普通 setInterval 触发次数: " + normalCount);
	console.log("严格 setInterval 触发次数: " + strictCount);
	console.log("比例: " + (strictCount / normalCount).toFixed(1) + " 倍");
}, 5000);

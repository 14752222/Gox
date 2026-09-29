// 定时器吞吐口径: 1 万次 setTimeout(…, 0) 全部触发后打印计数。
// 外层计 wall time —— "万次调度墙钟", 越小越好。
let n = 0;
for (let i = 0; i < 10000; i++) {
  setTimeout(function () { n = n + 1; }, 0);
}
setTimeout(function () { console.log("timers=" + n); }, 100);

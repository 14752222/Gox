// ===== Dart GetX 风格响应式演示 (obs / computed / ever / once) =====

console.log("=== Rx 基础 ===");
let count = obs(0);
count.listen(function (nv, ov) {
  console.log("count:", ov, "->", nv);
});
count.value = 1;      // 通知: 0 -> 1
count.value = 1;      // 值相等, 不通知 (GetX 语义)
count.value = 2;      // 通知: 1 -> 2
console.log("count() 直接调用 =", count());

console.log("=== RxList / RxMap ===");
let list = obs(["a", "b"]);
list.listen(function () {
  console.log("list 变更:", list.join(","));
});
list.push("c");           // 触发
list[0] = "A";            // 索引写触发
let map = obs(new Map());
map.set("k", 1);          // 触发
map.delete("k");          // 触发

console.log("=== computed: 自动依赖追踪 ===");
let price = obs(10);
let qty = obs(2);
let total = computed(function () {
  return price.value * qty.value;
});
total.listen(function (v) {
  console.log("total =", v);
});
console.log("初始 total =", total.value);
price.value = 20;   // total 重算为 40, 通知
qty.value = 3;      // total 重算为 60, 通知

console.log("=== 嵌套 computed ===");
let double = computed(function () { return price.value * 2 });
let label = computed(function () { return "合计: " + double.value });
console.log(label.value);   // 合计: 40

console.log("=== workers: ever / once ===");
let hits = obs(0);
ever(hits, function (v) { console.log("ever:", v) });
once(hits, function (v) { console.log("once:", v) });
hits.value = 1;   // ever + once 都触发
hits.value = 2;   // 仅 ever 触发

console.log("=== 取消订阅 / 关闭 ===");
let c3 = obs(9);
let sub = c3.listen(function () { console.log("listen 立即回调:", c3.value) });
sub.close();      // 取消订阅
c3.value = 10;    // 之后不再触发
c3.close();

console.log("=== demo done ===");

let m = new Map();
m.set("a", 1);
m.set("b", 2);
m.forEach(function(v, k) { console.log(k, v); });
let s = new Set([1, 2, 3]);
s.forEach(function(v) { console.log("set:", v); });
console.log("WeakMap:", typeof WeakMap);
console.log("WeakSet:", typeof WeakSet);

function* gen() { yield 1; yield 2; yield 3; }
let t = 0;
for (const v of gen()) t += v;
console.log(t);

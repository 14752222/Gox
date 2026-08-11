let o = { a: 0, b: null };
o["a"] ||= 5;
o["b"] ||= 6;
console.log("a:", o.a, "b:", o.b);
let o2 = { c: 7 };
let k = "c";
o2[k] &&= 8;
console.log("c:", o2.c);

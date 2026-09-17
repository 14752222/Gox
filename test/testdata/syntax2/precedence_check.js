// 检查赋值/逻辑优先级未受影响
let a = 1 + 2 * 3;
console.log("a:", a);               // 7
let b = true || false && false;
console.log("b:", b);               // true
let c = 5 > 3 ? "yes" : "no";
console.log("c:", c);               // yes
let d = 2 + 3 * 4 === 14;
console.log("d:", d);               // true
let e = 10 - 2 - 3;
console.log("e:", e);               // 5
let f = 2 ** 3 ** 2;
console.log("f:", f);               // 512 (右结合)
let obj = {};
let k = 0;
obj.x = k ||= 5;
console.log("obj.x:", obj.x);       // 5

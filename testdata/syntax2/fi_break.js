const obj = { a: 1, b: 2, c: 3 };
let r = "";
for (const k in obj) {
  if (k === "b") break;
  r += k;
}
console.log("break:", r);

let r2 = "";
for (const k in obj) {
  if (k === "a") continue;
  r2 += k;
}
console.log("continue:", r2);

// for-in over array (should iterate indices)
let r3 = "";
for (let i in [10, 20, 30]) { r3 += i; }
console.log("array:", r3);

// for-in with no keys
let r4 = "";
for (let k in {}) { r4 += k; }
console.log("empty:", r4);

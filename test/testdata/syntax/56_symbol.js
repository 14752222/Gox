const s = Symbol("id");
const obj = {};
obj[s] = 42;
console.log(typeof s);
console.log(obj[s]);

const obj = { a: 1, b: 2 };
console.log(Object.keys(obj).join(","), Object.values(obj).length, Object.entries(obj).length);
const copy = Object.assign({}, obj);
console.log(copy.a);

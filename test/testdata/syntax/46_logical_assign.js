let a = null;
a ??= 7;
let b = 1;
b ||= 9;
let c = 2;
c &&= 5;
console.log(a, b, c);

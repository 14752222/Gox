const a = [3, 1, 2];
console.log(a.map(x => x * 2).join(","));
console.log(a.filter(x => x > 1).length);
console.log(a.reduce((s, x) => s + x, 0));
a.sort();
console.log(a.indexOf(2), a.includes(3), a.slice(1).join(","));

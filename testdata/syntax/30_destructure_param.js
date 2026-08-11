function f({ a, b = 5 }, [c, d]) { return a + b + c + d; }
console.log(f({ a: 1 }, [2, 3]));

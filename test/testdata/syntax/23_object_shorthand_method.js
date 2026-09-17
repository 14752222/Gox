const x = 1, y = 2;
const p = { x, y, sum() { return this.x + this.y; } };
console.log(p.sum());

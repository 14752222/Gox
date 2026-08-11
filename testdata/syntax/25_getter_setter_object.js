const obj = {
  v: 10,
  get doubled() { return this.v * 2; },
  set half(n) { this.v = n / 2; }
};
obj.half = 20;
console.log(obj.doubled);

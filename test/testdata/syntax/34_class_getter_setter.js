class C {
  constructor(v) { this._v = v; }
  get v() { return this._v; }
  set v(n) { this._v = n * 2; }
}
const c = new C(5);
c.v = 10;
console.log(c.v);

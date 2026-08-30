class C {
  x = 1;
  y = 2;
  sum() { return this.x + this.y; }
}
console.log(new C().sum());

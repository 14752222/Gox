class A {
  constructor(x) { this.x = x; }
  greet() { return "A:" + this.x; }
}
class B extends A {
  constructor(x, y) { super(x); this.y = y; }
  greet() { return super.greet() + ",B:" + this.y; }
}
console.log(new B(1, 2).greet());

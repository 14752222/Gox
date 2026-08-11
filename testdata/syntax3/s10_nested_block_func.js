function f() {
  let x = 1;
  {
    x = 2;
  }
  return x;
}
console.log(f());

function f(x) {
  switch (x) {
    case 1: return "one";
    case 2: return "two";
    default: return "many";
  }
}
console.log(f(1), f(2), f(9));

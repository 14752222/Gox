function f(x) {
  switch (x) {
    case 1: { return "one"; }
    default: { return "many"; }
  }
}
console.log(f(1), f(9));

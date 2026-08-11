function a() { return arguments.length; }
console.log("a:", a("x", "y", "z"));

function b() {
  let n = arguments.length;
  return n;
}
console.log("b:", b("x", "y", "z"));

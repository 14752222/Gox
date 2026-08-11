function counter() {
  let n = 0;
  return () => ++n;
}
const c = counter();
c(); c();
console.log(c());

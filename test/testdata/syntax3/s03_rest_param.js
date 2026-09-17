function sum(...nums) {
  let t = 0;
  for (const n of nums) { t += n; }
  return t;
}
console.log(sum(1, 2, 3, 4));

function probe(a, b) {
  console.log("args:", arguments);
  console.log("len:", arguments.length);
  console.log("a0:", arguments[0]);
  console.log("a1:", arguments[1]);
}
probe(7, 9);

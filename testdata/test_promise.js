let p = new Promise(function(resolve, reject) {
  resolve(42);
});
p.then(function(val) {
  console.log("resolved:", val);
  return val * 2;
}).then(function(val) {
  console.log("chained:", val);
});
Promise.resolve(100).then(function(v) { console.log("static:", v); });
Promise.reject("error").catch(function(e) { console.log("caught:", e); });
console.log("Promise:", typeof Promise);

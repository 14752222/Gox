let results = [];
let m;
let i = 0;
while ((m = i++) < 3) {
    results.push(1);
}
console.log("results=" + JSON.stringify(results));
console.log("join=" + results.join(","));
let r = results.join(",");
console.log("r=" + r);
r;

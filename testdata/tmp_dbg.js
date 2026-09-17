let re = /a/g;
let results = [];
let m;
while ((m = re.exec("banana")) !== null) {
    results.push(m[0] + "@" + m.index);
}
console.log("len=" + results.length);
console.log("r0=" + results[0]);
console.log("r1=" + results[1]);
console.log("r2=" + results[2]);
console.log("join=" + results.join(","));

let re = /a/g;
let results = [];
let m;
while ((m = re.exec("banana")) !== null) {
    results.push(m[0] + "@" + m.index);
}
console.log(JSON.stringify(results));

let re = /a/g;
let results = [];
let m;
while ((m = re.exec("banana")) !== null) {
    results.push(m[0] + "@" + m.index);
}
let x = 1;
results.join(",");

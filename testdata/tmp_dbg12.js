let re = /a/g;
let results = [];
let m;
while ((m = re.exec("banana")) !== null) {
    results.push(1);
}
let r = results.join(",");
r;

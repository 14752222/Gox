let re = /a/g;
let results = [];
while (re.exec("banana") !== null) {
    results.push(1);
}
let r = results.join(",");
r;

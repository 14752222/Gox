let results = [];
let m;
let arr = [1,2,3];
let i = 0;
while ((m = arr.indexOf(i)) !== -1) {
    results.push(m);
    break;
}
let r = results.join(",");
r;

function next() { return Math.random() < 0.5 ? 1 : 0; }
let results = [];
while (next() === 1) {
    results.push(1);
}
let r = results.join(",");
r;

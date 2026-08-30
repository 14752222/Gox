let normalCount = 0;
let strictCount = 0;
let normalId = 0;
let strictId = 0;

normalId = setInterval(() => {
	normalCount++;
	let s = 0;
	for (let i = 0; i < 100000; i++) { s += i; }
}, 30);

strictId = setStrictInterval(() => {
	strictCount++;
	let s = 0;
	for (let i = 0; i < 100000; i++) { s += i; }
}, 30);

setTimeout(() => {
	clearInterval(normalId);
	clearStrictInterval(strictId);
	console.log("cleaned: normal=" + normalCount + " strict=" + strictCount);
}, 5000);

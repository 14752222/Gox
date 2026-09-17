function tag(strings, ...vals) { return strings.join("") + vals.length; }
console.log(tag`a${1}b${2}`);

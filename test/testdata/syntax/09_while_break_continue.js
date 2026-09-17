let i = 0, t = 0;
while (i < 10) {
  i++;
  if (i % 2 === 0) continue;
  if (i > 7) break;
  t += i;
}
console.log(t);

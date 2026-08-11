async function f() {
  const v = await Promise.resolve(41);
  return v + 1;
}
f().then(v => console.log(v));

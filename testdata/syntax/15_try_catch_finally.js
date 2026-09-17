let out = "";
try {
  throw new Error("boom");
} catch (e) {
  out += "caught:" + e.message;
} finally {
  out += ":done";
}
console.log(out);

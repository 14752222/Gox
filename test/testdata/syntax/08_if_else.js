function grade(s) {
  if (s >= 90) return "A";
  else if (s >= 60) return "B";
  else return "C";
}
console.log(grade(95), grade(70), grade(30));

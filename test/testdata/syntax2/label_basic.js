// 标签语句基本: outer: for + break outer / continue outer
let out = []
outer: for (let i = 0; i < 3; i++) {
  for (let j = 0; j < 3; j++) {
    if (j === 1) continue outer
    out.push(i + '-' + j)
  }
}
console.log('continue outer:', out.join(','))

// break outer 完全退出外层循环
let res = []
outer2: for (let i = 0; i < 3; i++) {
  for (let j = 0; j < 3; j++) {
    if (i === 1 && j === 1) break outer2
    res.push(i + '-' + j)
  }
}
console.log('break outer:', res.join(','))

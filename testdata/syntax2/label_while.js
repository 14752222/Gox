// 标签语句: 嵌套标签、continue 到外层 while / break 到外层 while
let out = []
let round = 0
outer: while (round < 3) {
  round++
  let i = 0
  while (i < 3) {
    i++
    if (i === 2) continue outer
    out.push(round + '-' + i)
  }
  break outer
}
console.log('while label:', out.join(','))

// do-while 标签 continue
let sum = 0
let count = 0
outer2: do {
  count++
  if (count < 3) continue outer2
  sum += count
} while (count < 5)
console.log('dowhile label sum:', sum)

// while 标签 break (跳出外层)
let seen = []
let k = 0
outer3: while (k < 10) {
  k++
  let j = 0
  while (j < 5) {
    j++
    if (k === 2 && j === 2) break outer3
    seen.push(k + '-' + j)
  }
}
console.log('while label break:', seen.join(','))

// 标签语句: 多层嵌套标签 + for-in 标签 break
let obj = { a: 1, b: 2, c: 3 }
let keys = []
outer: for (let k in obj) {
  for (let kk in obj) {
    if (k === 'b' && kk === 'b') break outer
    keys.push(k + kk)
  }
}
console.log('for-in label:', keys.join(','))

// 标签 + for-of
let res2 = []
outer2: for (let x of [1, 2, 3, 4]) {
  for (let y of [10, 20]) {
    if (x === 3 && y === 20) break outer2
    res2.push(x * 100 + y)
  }
}
console.log('for-of label:', res2.join(','))
